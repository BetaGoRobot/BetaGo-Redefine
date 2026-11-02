package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Vision interface {
	Describe(context.Context, string, []byte) (string, error)
}
type Speech interface {
	Transcribe(context.Context, []byte) (string, error)
}
type Config struct {
	Downloader                                    Downloader
	Objects                                       ObjectStore
	Vision                                        Vision
	Speech                                        Speech
	Limits                                        Limits
	FFmpeg, FFprobe, PDFInfo, PDFToText, PDFToPNG string
}
type Processor struct{ c Config }

func New(c Config) (*Processor, error) {
	if c.Downloader == nil || c.Objects == nil {
		return nil, fmt.Errorf("media downloader and originals store required")
	}
	var err error
	c.Limits, err = normalizeLimits(c.Limits)
	if err != nil {
		return nil, err
	}
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	if c.FFprobe == "" {
		c.FFprobe = "ffprobe"
	}
	if c.PDFInfo == "" {
		c.PDFInfo = "pdfinfo"
	}
	if c.PDFToText == "" {
		c.PDFToText = "pdftotext"
	}
	if c.PDFToPNG == "" {
		c.PDFToPNG = "pdftoppm"
	}
	return &Processor{c}, nil
}
func (p *Processor) Process(ctx context.Context, m domain.Message) (domain.MediaArtifact, error) {
	artifact := domain.MediaArtifact{Source: m.Source}
	if len(m.Media) > 32 {
		return artifact, ErrLimit
	}
	ctx, cancel := context.WithTimeout(ctx, p.c.Limits.Timeout*time.Duration(max(1, len(m.Media))))
	defer cancel()
	for _, media := range m.Media {
		r, size, err := p.c.Downloader.Download(ctx, m, media)
		if err != nil {
			return artifact, fmt.Errorf("media download failed")
		}
		if size > p.c.Limits.MaxDownloadBytes {
			r.Close()
			return artifact, ErrLimit
		}
		readMax := p.c.Limits.MaxDownloadBytes
		if size >= 0 && size < readMax {
			readMax = size
		}
		b, err := boundedRead(r, readMax)
		r.Close()
		if err != nil {
			return artifact, err
		}
		if size >= 0 && int64(len(b)) != size {
			return artifact, fmt.Errorf("media download size mismatch")
		}
		if media.MIME == "" || media.Type == domain.MediaImage {
			media.MIME = http.DetectContentType(b)
		}
		object, err := p.c.Objects.Put(ctx, m.Source, media, bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return artifact, err
		}
		parts, err := p.extract(ctx, media, b)
		if err != nil {
			return artifact, err
		}
		for i := range parts {
			parts[i].Object = object
		}
		artifact.Parts = append(artifact.Parts, parts...)
	}
	return artifact, nil
}
func (p *Processor) extract(ctx context.Context, m domain.Media, b []byte) ([]domain.MediaPart, error) {
	switch m.Type {
	case domain.MediaImage:
		return p.image(ctx, m.MIME, b)
	case domain.MediaAudio, domain.MediaVideo:
		return p.av(ctx, m, b)
	case domain.MediaFile:
		ext := strings.ToLower(filepath.Ext(m.FileName))
		switch {
		case m.MIME == "application/pdf" || ext == ".pdf":
			return p.pdf(ctx, b)
		case ext == ".docx" || ext == ".pptx" || ext == ".xlsx":
			return extractOffice(b, p.c.Limits)
		case strings.HasPrefix(m.MIME, "text/") || ext == ".txt" || ext == ".csv" || ext == ".md":
			if int64(len(b)) > p.c.Limits.MaxTextBytes || !utf8.Valid(b) {
				return nil, ErrLimit
			}
			return []domain.MediaPart{{Text: string(b)}}, nil
		}
	}
	return nil, fmt.Errorf("unsupported media format")
}
func (p *Processor) image(ctx context.Context, mime string, b []byte) ([]domain.MediaPart, error) {
	if p.c.Vision == nil {
		return nil, ErrModalityUnavailable
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("invalid image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > int64(p.c.Limits.MaxPixels) {
		return nil, ErrLimit
	}
	switch format {
	case "png":
		mime = "image/png"
	case "jpeg":
		mime = "image/jpeg"
	default:
		return nil, fmt.Errorf("unsupported image format")
	}
	text, err := p.c.Vision.Describe(ctx, mime, b)
	if err != nil {
		return nil, err
	}
	if int64(len(text)) > p.c.Limits.MaxTextBytes {
		return nil, ErrLimit
	}
	return []domain.MediaPart{{Text: text}}, nil
}
func tempInput(b []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "agent-media-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("media temporary file unavailable")
	}
	name := f.Name()
	remove := func() { os.Remove(name) }
	if _, err = f.Write(b); err != nil {
		f.Close()
		remove()
		return "", func() {}, fmt.Errorf("media temporary write failed")
	}
	if err = f.Close(); err != nil {
		remove()
		return "", func() {}, err
	}
	return name, remove, nil
}

var pdfPages = regexp.MustCompile(`(?m)^Pages:\s+([0-9]+)\s*$`)

func (p *Processor) pdf(ctx context.Context, b []byte) ([]domain.MediaPart, error) {
	path, remove, err := tempInput(b)
	if err != nil {
		return nil, err
	}
	defer remove()
	l := p.c.Limits
	info, err := run(ctx, l.Timeout, 16384, p.c.PDFInfo, path)
	if err != nil {
		return nil, err
	}
	match := pdfPages.FindSubmatch(info)
	if len(match) != 2 {
		return nil, fmt.Errorf("PDF page count unavailable")
	}
	pages, _ := strconv.Atoi(string(match[1]))
	if pages < 1 || pages > l.MaxPages {
		return nil, ErrLimit
	}
	parts := make([]domain.MediaPart, 0, pages)
	used := int64(0)
	for page := 1; page <= pages; page++ {
		s := strconv.Itoa(page)
		raw, err := run(ctx, l.Timeout, l.MaxTextBytes-used, p.c.PDFToText, "-f", s, "-l", s, "-layout", path, "-")
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			if p.c.Vision == nil {
				return nil, ErrModalityUnavailable
			}
			img, err := run(ctx, l.Timeout, l.MaxOutputBytes, p.c.PDFToPNG, "-f", s, "-l", s, "-singlefile", "-scale-to", "1600", "-jpeg", path)
			if err != nil {
				return nil, err
			}
			out, err := p.image(ctx, "image/jpeg", img)
			if err != nil {
				return nil, err
			}
			text = out[0].Text
		}
		used += int64(len(text))
		if used > l.MaxTextBytes {
			return nil, ErrLimit
		}
		parts = append(parts, domain.MediaPart{Text: text, Page: page})
	}
	return parts, nil
}
func (p *Processor) av(ctx context.Context, m domain.Media, b []byte) ([]domain.MediaPart, error) {
	path, remove, err := tempInput(b)
	if err != nil {
		return nil, err
	}
	defer remove()
	l := p.c.Limits
	raw, err := run(ctx, l.Timeout, 16384, p.c.FFprobe, "-v", "error", "-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	var info struct {
		Format  struct{ Duration string }
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int64  `json:"width"`
			Height int64  `json:"height"`
		}
	}
	if json.Unmarshal(raw, &info) != nil {
		return nil, fmt.Errorf("invalid media duration")
	}
	seconds, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > l.MaxDuration.Seconds() {
		return nil, ErrLimit
	}
	duration := time.Duration(seconds * float64(time.Second))
	parts := []domain.MediaPart{}
	audio := false
	for _, stream := range info.Streams {
		// Validate encoded source geometry before ffmpeg decodes or scales frames.
		// Division avoids overflow for adversarial stream metadata.
		if stream.Type == "video" && (stream.Width <= 0 || stream.Height <= 0 || stream.Width > int64(l.MaxPixels)/stream.Height) {
			return nil, ErrLimit
		}
		audio = audio || stream.Type == "audio"
	}
	if m.Type == domain.MediaVideo {
		if p.c.Vision == nil {
			return nil, ErrModalityUnavailable
		}
		frames := min(l.MaxFrames, max(1, int(math.Ceil(seconds/15))))
		for i := range frames {
			at := seconds * float64(i) / float64(frames)
			img, err := run(ctx, l.Timeout, l.MaxOutputBytes, p.c.FFmpeg, "-nostdin", "-v", "error", "-threads", "1", "-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", path, "-frames:v", "1", "-vf", "scale=1280:1280:force_original_aspect_ratio=decrease", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
			if err != nil {
				return nil, err
			}
			out, err := p.image(ctx, "image/jpeg", img)
			if err != nil {
				return nil, err
			}
			out[0].Start = time.Duration(at * float64(time.Second))
			out[0].End = out[0].Start
			parts = append(parts, out[0])
		}
	}
	if audio {
		if p.c.Speech == nil {
			return nil, ErrModalityUnavailable
		}
		wav, err := run(ctx, l.Timeout, l.MaxOutputBytes, p.c.FFmpeg, "-nostdin", "-v", "error", "-threads", "1", "-i", path, "-t", strconv.FormatFloat(seconds, 'f', 3, 64), "-vn", "-ac", "1", "-ar", "16000", "-f", "wav", "pipe:1")
		if err != nil {
			return nil, err
		}
		text, err := p.c.Speech.Transcribe(ctx, wav)
		if err != nil {
			return nil, err
		}
		parts = append(parts, domain.MediaPart{Text: text, Start: 0, End: duration})
	} else if m.Type == domain.MediaAudio {
		return nil, fmt.Errorf("audio stream unavailable")
	}
	used := int64(0)
	for _, part := range parts {
		used += int64(len(part.Text))
	}
	if used > l.MaxTextBytes {
		return nil, ErrLimit
	}
	return parts, nil
}
