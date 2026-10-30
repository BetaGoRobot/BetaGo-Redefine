package business

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type AudioSource struct {
	SongID int64
	URL    string
}
type AudioUploader interface {
	Upload(context.Context, domain.Scope, AudioSource) (string, error)
}
type LarkAudioConfig struct {
	AppID, AppSecret, TenantID, BaseURL, FFmpegPath string
	AllowedMediaHosts                               []string
	HTTPClient                                      *http.Client
	Timeout                                         time.Duration
}
type LarkAudioUploader struct {
	cfg    LarkAudioConfig
	http   *http.Client
	ffmpeg string
}

func NewLarkAudioUploader(cfg LarkAudioConfig) (*LarkAudioUploader, error) {
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, domain.ErrInvalid
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://open.feishu.cn"
	}
	if _, e := endpoint(cfg.BaseURL); e != nil {
		return nil, domain.ErrInvalid
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	path, e := exec.LookPath(cfg.FFmpegPath)
	if e != nil {
		return nil, ErrProvider
	}
	hc, e := httpClient(cfg.HTTPClient, cfg.Timeout)
	if e != nil {
		return nil, e
	}
	if len(cfg.AllowedMediaHosts) == 0 {
		cfg.AllowedMediaHosts = DefaultMusicMediaHosts()
	}
	cfg.AllowedMediaHosts = append([]string(nil), cfg.AllowedMediaHosts...)
	for _, h := range cfg.AllowedMediaHosts {
		if h == "" || strings.ContainsAny(h, "/:@\\ \r\n") {
			return nil, domain.ErrInvalid
		}
	}
	return &LarkAudioUploader{cfg: cfg, http: hc, ffmpeg: path}, nil
}
func (u *LarkAudioUploader) Fingerprint() string {
	return domain.StableID("lark-audio-v1", u.cfg.AppID, u.cfg.TenantID, u.cfg.BaseURL, u.ffmpeg, strings.Join(u.cfg.AllowedMediaHosts, ","))
}
func (u *LarkAudioUploader) Upload(ctx context.Context, s domain.Scope, source AudioSource) (string, error) {
	if s.Validate() != nil || s.AppID != u.cfg.AppID || (u.cfg.TenantID != "" && s.TenantID != u.cfg.TenantID) || source.SongID <= 0 {
		return "", domain.ErrUnauthorized
	}
	media, e := ValidateMusicMediaURL(source.URL, u.cfg.AllowedMediaHosts)
	if e != nil {
		return "", e
	}
	ctx, cancel := deadline(ctx)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", media.String(), nil)
	if e != nil {
		return "", ErrProvider
	}
	downloadClient := *u.http
	downloadClient.Jar = nil
	audio, e := readResponse(&downloadClient, req, 32<<20)
	if e != nil {
		return "", e
	}
	asset, e := u.UploadBytes(ctx, s, AudioBytes{SongID: source.SongID, Data: audio})
	return asset.FileKey, e
}

type AudioBytes struct {
	SongID int64
	Name   string
	Data   []byte
}
type AudioAsset struct {
	FileKey    string
	DurationMS int
}

// UploadBytes converts bounded source bytes and returns the actual upload receipt.
// It never uses SDK/global token caches; credentials are scoped to this uploader.
func (u *LarkAudioUploader) UploadBytes(ctx context.Context, s domain.Scope, source AudioBytes) (AudioAsset, error) {
	if s.Validate() != nil || s.AppID != u.cfg.AppID || (u.cfg.TenantID != "" && s.TenantID != u.cfg.TenantID) {
		return AudioAsset{}, domain.ErrUnauthorized
	}
	if source.SongID <= 0 || len(source.Data) == 0 || len(source.Data) > 32<<20 {
		return AudioAsset{}, domain.ErrInvalid
	}
	ctx, cancel := deadline(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, u.ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-protocol_whitelist", "pipe", "-threads", "1", "-i", "pipe:0", "-vn", "-c:a", "libopus", "-b:a", "64k", "-f", "opus", "pipe:1")
	cmd.Stdin = bytes.NewReader(source.Data)
	encoded := &boundedBuffer{limit: 16 << 20}
	cmd.Stdout = encoded
	cmd.Stderr = &boundedBuffer{limit: 8 << 10}
	if cmd.Run() != nil || encoded.Len() == 0 {
		return AudioAsset{}, ErrProvider
	}
	duration, e := opusDurationMS(encoded.Bytes())
	if e != nil {
		return AudioAsset{}, e
	}
	credentials, _ := json.Marshal(map[string]string{"app_id": u.cfg.AppID, "app_secret": u.cfg.AppSecret})
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(u.cfg.BaseURL, "/")+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(credentials))
	if e != nil {
		return AudioAsset{}, ErrProvider
	}
	req.Header.Set("Content-Type", "application/json")
	raw, e := readResponse(u.http, req, 64<<10)
	if e != nil {
		return AudioAsset{}, e
	}
	var auth struct {
		Code              int
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Code != 0 || auth.TenantAccessToken == "" || strings.ContainsAny(auth.TenantAccessToken, "\r\n") {
		return AudioAsset{}, ErrProvider
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("file_type", "opus")
	form.WriteField("duration", strconv.Itoa(duration))
	form.WriteField("file_name", strconv.FormatInt(source.SongID, 10)+".opus")
	file, e := form.CreateFormFile("file", strconv.FormatInt(source.SongID, 10)+".opus")
	if e != nil {
		return AudioAsset{}, ErrProvider
	}
	file.Write(encoded.Bytes())
	form.Close()
	req, e = http.NewRequestWithContext(ctx, "POST", strings.TrimRight(u.cfg.BaseURL, "/")+"/open-apis/im/v1/files", &body)
	if e != nil {
		return AudioAsset{}, ErrProvider
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+auth.TenantAccessToken)
	raw, e = readResponse(u.http, req, 64<<10)
	if e != nil {
		return AudioAsset{}, e
	}
	var upload struct {
		Code int
		Data struct {
			FileKey string `json:"file_key"`
		}
	}
	if json.Unmarshal(raw, &upload) != nil || upload.Code != 0 || strings.TrimSpace(upload.Data.FileKey) == "" {
		return AudioAsset{}, ErrProvider
	}
	return AudioAsset{FileKey: upload.Data.FileKey, DurationMS: duration}, nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrProvider
	}
	return b.Buffer.Write(p)
}

// DefaultMusicMediaHosts preserves NetEase and the supported Kuwo unblock family.
// Additional provider CDN families must be explicitly configured.
func DefaultMusicMediaHosts() []string {
	return []string{"music.126.net", "music.163.com", "kuwo.cn", "kuwo.com"}
}
func ValidateMusicMediaURL(raw string, hosts []string) (*url.URL, error) {
	media, e := url.Parse(raw)
	if e != nil || media.User != nil || media.Host == "" || media.Fragment != "" || (media.Scheme != "https" && media.Scheme != "http") {
		return nil, domain.ErrInvalid
	}
	if len(hosts) == 0 {
		hosts = DefaultMusicMediaHosts()
	}
	host := strings.ToLower(media.Hostname())
	for _, h := range hosts {
		h = strings.ToLower(h)
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return media, nil
		}
	}
	return nil, domain.ErrUnauthorized
}

// Opus granules use 48kHz irrespective of the input rate. Reading the final Ogg
// page gives measured duration without probing a remote URL or using temp files.
func opusDurationMS(b []byte) (int, error) {
	var preSkip uint64
	var granule uint64
	found := false
	for off := 0; off < len(b); {
		if off+27 > len(b) || string(b[off:off+4]) != "OggS" {
			return 0, ErrProvider
		}
		n := int(b[off+26])
		if off+27+n > len(b) {
			return 0, ErrProvider
		}
		size := 0
		for _, v := range b[off+27 : off+27+n] {
			size += int(v)
		}
		end := off + 27 + n + size
		if end > len(b) {
			return 0, ErrProvider
		}
		payload := b[off+27+n : end]
		if len(payload) >= 19 && bytes.HasPrefix(payload, []byte("OpusHead")) {
			preSkip = uint64(binary.LittleEndian.Uint16(payload[10:12]))
			found = true
		}
		g := binary.LittleEndian.Uint64(b[off+6 : off+14])
		if g != ^uint64(0) {
			granule = g
		}
		off = end
	}
	if !found || granule <= preSkip {
		return 0, ErrProvider
	}
	duration := (granule - preSkip) * 1000 / 48000
	if duration == 0 || duration > 24*60*60*1000 {
		return 0, ErrProvider
	}
	return int(duration), nil
}
