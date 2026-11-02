package business

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func silentWAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+16000))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(8000))
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	return b.Bytes()
}
func TestAudioConvertsSelectedProviderTrackAndUploadsOpus(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg absent")
	}
	uploads := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/track.wav":
			w.Write(silentWAV())
		case "/open-apis/auth/v3/tenant_access_token/internal":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["app_id"] != "app" || in["app_secret"] != "secret" {
				t.Error("wrong app credentials")
			}
			io.WriteString(w, `{"code":0,"tenant_access_token":"tenant-token"}`)
		case "/open-apis/im/v1/files":
			uploads++
			if r.Header.Get("Authorization") != "Bearer tenant-token" {
				t.Error("upload auth missing")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("file_type") != "opus" {
				t.Error("not opus")
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if !bytes.HasPrefix(b, []byte("OggS")) || !bytes.Contains(b, []byte("OpusHead")) {
				t.Error("not converted opus bytes")
			}
			io.WriteString(w, `{"code":0,"data":{"file_key":"file-opus"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer s.Close()
	u, e := NewLarkAudioUploader(LarkAudioConfig{AppID: "app", AppSecret: "secret", TenantID: "tenant", BaseURL: s.URL, AllowedMediaHosts: []string{"127.0.0.1"}})
	if e != nil {
		t.Fatal(e)
	}
	key, e := u.Upload(context.Background(), domain.Scope{AppID: "app", TenantID: "tenant", ChatID: "chat", Visibility: "group"}, AudioSource{SongID: 123, URL: s.URL + "/track.wav"})
	if e != nil || key != "file-opus" || uploads != 1 {
		t.Fatalf("audio upload: %s %v count=%d", key, e, uploads)
	}
}
func TestAudioRejectsUnapprovedMediaHostBeforeFetch(t *testing.T) {
	u, e := NewLarkAudioUploader(LarkAudioConfig{AppID: "app", AppSecret: "secret", TenantID: "t"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = u.Upload(context.Background(), domain.Scope{AppID: "app", TenantID: "t", ChatID: "chat", Visibility: "group"}, AudioSource{SongID: 1, URL: "http://127.0.0.1/private"})
	if e == nil {
		t.Fatal("arbitrary media host accepted")
	}
}

func TestAudioBytesUploadReportsMeasuredDurationAndMultipart(t *testing.T) {
	uploads := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth/") {
			io.WriteString(w, `{"code":0,"tenant_access_token":"byte-token"}`)
			return
		}
		if r.URL.Path != "/open-apis/im/v1/files" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		if r.FormValue("duration") != "1000" || r.FormValue("file_type") != "opus" {
			t.Errorf("duration or type missing: %v", r.Form)
		}
		f, _, e := r.FormFile("file")
		if e != nil {
			t.Error(e)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if !bytes.Contains(b, []byte("OpusHead")) {
			t.Error("source not transcoded")
		}
		uploads++
		io.WriteString(w, `{"code":0,"data":{"file_key":"measured-file"}}`)
	}))
	defer s.Close()
	u, e := NewLarkAudioUploader(LarkAudioConfig{AppID: "app", AppSecret: "secret", BaseURL: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	a, e := u.UploadBytes(context.Background(), domain.Scope{AppID: "app", TenantID: "t", ChatID: "c", Visibility: "group"}, AudioBytes{SongID: 42, Name: "selected", Data: silentWAV()})
	if e != nil || a.FileKey != "measured-file" || a.DurationMS != 1000 || uploads != 1 {
		t.Fatalf("asset %+v %v uploads=%d", a, e, uploads)
	}
}

func TestMusicMediaURLPolicySupportsKuwoAndRejectsLookalikes(t *testing.T) {
	for _, raw := range []string{"https://m701.music.126.net/42.flac", "https://sycdn.kuwo.cn/42.mp3"} {
		if _, e := ValidateMusicMediaURL(raw, nil); e != nil {
			t.Errorf("supported source %s: %v", raw, e)
		}
	}
	for _, raw := range []string{"http://127.0.0.1/private", "https://kuwo.cn.evil.test/a", "https://user:secret@sycdn.kuwo.cn/a", "https://sycdn.kuwo.cn/a#frag"} {
		if _, e := ValidateMusicMediaURL(raw, nil); e == nil {
			t.Errorf("unsafe source %s", raw)
		}
	}
	if _, e := ValidateMusicMediaURL("https://cdn.alternate.test/a", []string{"alternate.test"}); e != nil {
		t.Fatal(e)
	}
}

func TestAudioRejectsBlankActualUploadReceipt(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth/") {
			io.WriteString(w, `{"code":0,"tenant_access_token":"scoped-token"}`)
			return
		}
		io.WriteString(w, `{"code":0,"data":{"file_key":"   "}}`)
	}))
	defer s.Close()
	uploader, e := NewLarkAudioUploader(LarkAudioConfig{AppID: "app", AppSecret: "secret", BaseURL: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := uploader.UploadBytes(context.Background(), domain.Scope{AppID: "app", TenantID: "tenant", ChatID: "chat", Visibility: "group"}, AudioBytes{SongID: 42, Data: silentWAV()})
	if e == nil || receipt.FileKey != "" {
		t.Fatalf("blank handle became successful receipt: %+v %v", receipt, e)
	}
}
