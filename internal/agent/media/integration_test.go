package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/minio/minio-go/v7"
)

func sourceRef() domain.SourceRef {
	return domain.SourceRef{Scope: domain.Scope{TenantID: "synthetic-tenant", AppID: "app", ChatID: "chat", Visibility: "group"}, MessageID: "synthetic-media", Version: 1, AuthorID: "original-author", OccurredAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}
func TestRealMinIOOriginalRoundtripAndVersionSeparation(t *testing.T) {
	endpoint := os.Getenv("AGENT_TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("isolated MinIO required")
	}
	bucket := fmt.Sprintf("agent-media-%d", time.Now().UnixNano())
	m, err := NewMinIO(MinIOConfig{Endpoint: endpoint, AccessKey: os.Getenv("AGENT_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("AGENT_TEST_MINIO_SECRET_KEY"), Bucket: bucket, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = m.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for obj := range m.client.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			m.client.RemoveObject(context.Background(), bucket, obj.Key, minio.RemoveObjectOptions{})
		}
		m.client.RemoveBucket(context.Background(), bucket)
	})
	source := sourceRef()
	media := domain.Media{Type: domain.MediaFile, ResourceID: "original-provider-file", MIME: "text/plain", Sequence: 2}
	original := []byte("synthetic original 资料")
	first, err := m.Put(ctx, source, media, bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Get(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(b, original) || first.Source.AuthorID != "original-author" {
		t.Fatal("original or provenance lost")
	}
	spoof := first
	spoof.Source.Scope.ChatID = "another-chat"
	if r, err := m.Get(ctx, spoof); err == nil {
		r.Close()
		t.Fatal("cross-scope object key accepted")
	}
	altered := bytes.Repeat([]byte("x"), len(original))
	if _, err := m.Put(ctx, source, media, bytes.NewReader(altered), int64(len(altered))); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("immutable original overwritten: %v", err)
	}
	source.Version = 2
	second, err := m.Put(ctx, source, media, bytes.NewReader(original), int64(len(original)))
	if err != nil || second.Key == first.Key {
		t.Fatal("source versions overwritten")
	}
	again, err := m.Put(ctx, source, media, bytes.NewReader(original), int64(len(original)))
	if err != nil || again.Key != second.Key {
		t.Fatal("retry not stable")
	}
}
func syntheticPDF() []byte {
	return syntheticPDFTexts([]string{"synthetic page one", "synthetic page two"})
}
func syntheticPDFTexts(texts []string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 5 0 R >> >> /Contents 6 0 R >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 5 0 R >> >> /Contents 7 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	for _, text := range texts {
		stream := "BT /F1 12 Tf 30 250 Td (" + text + ") Tj ET\n"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}
func TestRealPDFPageTextAndPageLimit(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("poppler tools required")
	}
	p, _ := New(Config{Downloader: down{}, Objects: &objects{}})
	parts, err := p.pdf(context.Background(), syntheticPDF())
	if err != nil || len(parts) != 2 || parts[0].Page != 1 || parts[1].Page != 2 || !strings.Contains(parts[1].Text, "synthetic page two") {
		t.Fatalf("PDF pages %v %v", parts, err)
	}
	vision := &syntheticVision{}
	p.c.Vision = vision
	scanned, err := p.pdf(context.Background(), syntheticPDFTexts([]string{"", ""}))
	if err != nil || len(scanned) != 2 || scanned[1].Page != 2 || scanned[1].Text != "synthetic frame" || vision.calls != 2 {
		t.Fatalf("PDF OCR page preservation %v %v", scanned, err)
	}
	p.c.Limits.MaxPages = 1
	if _, err = p.pdf(context.Background(), syntheticPDF()); err != ErrLimit {
		t.Fatalf("PDF page limit %v", err)
	}
}

type syntheticVision struct{ calls int }

func (v *syntheticVision) Describe(context.Context, string, []byte) (string, error) {
	v.calls++
	return "synthetic frame", nil
}

type syntheticSpeech struct{ calls int }

func (s *syntheticSpeech) Transcribe(_ context.Context, wav []byte) (string, error) {
	s.calls++
	if len(wav) < 44 || string(wav[:4]) != "RIFF" {
		return "", fmt.Errorf("expected WAV")
	}
	return "synthetic audio", nil
}
func syntheticWAV(seconds int) []byte {
	var b bytes.Buffer
	data := make([]byte, seconds*16000*2)
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	for _, v := range []uint16{1, 1} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint32(32000))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}
func TestRealFFmpegAudioVideoTimelineAndDurationLimit(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	speech := &syntheticSpeech{}
	vision := &syntheticVision{}
	p, _ := New(Config{Downloader: down{}, Objects: &objects{}, Speech: speech, Vision: vision, FFmpeg: ffmpeg, FFprobe: ffprobe})
	ctx := context.Background()
	parts, err := p.av(ctx, domain.Media{Type: domain.MediaAudio}, syntheticWAV(1))
	if err != nil || len(parts) != 1 || parts[0].End != time.Second {
		t.Fatalf("audio timeline %v %v", parts, err)
	}
	p.c.Limits.MaxDuration = 500 * time.Millisecond
	if _, err = p.av(ctx, domain.Media{Type: domain.MediaAudio}, syntheticWAV(1)); err != ErrLimit {
		t.Fatalf("duration not rejected %v", err)
	}
	p.c.Limits.MaxDuration = time.Minute
	video, err := run(ctx, time.Minute, 4<<20, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x120:r=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", "2", "-c:v", "mpeg4", "-c:a", "aac", "-f", "matroska", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	parts, err = p.av(ctx, domain.Media{Type: domain.MediaVideo}, video)
	if err != nil || len(parts) != 2 || parts[0].Start != 0 || parts[1].End < 2*time.Second || vision.calls != 1 || speech.calls != 2 {
		t.Fatalf("video timing/calls %v %v", parts, err)
	}
}
func TestRealFFmpegRefusesNetworkBackedPlaylists(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write(syntheticWAV(1))
	}))
	defer server.Close()
	playlist := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\n" + server.URL + "/segment.ts\n#EXT-X-ENDLIST\n")
	p, _ := New(Config{Downloader: down{}, Objects: &objects{}, Speech: &syntheticSpeech{}, FFmpeg: ffmpeg, FFprobe: ffprobe})
	if _, err = p.av(context.Background(), domain.Media{Type: domain.MediaAudio}, playlist); err == nil {
		t.Fatal("playlist accepted as local media")
	}
	if requests.Load() != 0 {
		t.Fatalf("parser fetched %d untrusted remote segments", requests.Load())
	}
}

func TestOriginalVideoPixelLimitBeforeDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	video, err := run(context.Background(), time.Minute, 4<<20, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=1920x1080:r=1", "-t", "1", "-c:v", "mpeg4", "-an", "-f", "matroska", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{Downloader: down{}, Objects: &objects{}, Vision: &syntheticVision{}, FFmpeg: ffmpeg, FFprobe: ffprobe, Limits: Limits{MaxPixels: 2000000}})
	if err != nil {
		t.Fatal(err)
	}
	p.c.FFmpeg = "/frame-decoder-must-not-run"
	parts, err := p.av(context.Background(), domain.Media{Type: domain.MediaVideo}, video)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("1920x1080 source exceeds MaxPixels=2000000 but accepted: parts=%d err=%v", len(parts), err)
	}
}
