package musicmedia

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/media"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func testWAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(16036))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	for _, v := range []uint16{1, 1} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	for _, v := range []uint32{8000, 16000} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	for _, v := range []uint16{2, 16} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	return b.Bytes()
}
func testScope() domain.Scope {
	return domain.Scope{AppID: "music-app", TenantID: "music-tenant", ChatID: "music-chat", Visibility: "group"}
}
func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" || os.Getenv("AGENT_TEST_MINIO_ENDPOINT") == "" {
		t.Skip("isolated PostgreSQL + MinIO required")
	}
	db, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal("fixture SQL open failed")
	}
	if e = db.Ping(); e != nil {
		t.Fatal("isolated SQL unavailable")
	}
	schema := fmt.Sprintf("music_test_%d", time.Now().UnixNano())
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); db.Close() })
	return db, schema
}
func TestRealMusicPublicationMultipartCacheRestartAndScopedAssets(t *testing.T) {
	db, schema := testDB(t)
	var details, lyrics, urls, covers, downloads, images, audios atomic.Int32
	var failAudio atomic.Bool
	var origin string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/origin/") {
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("credential forwarded to media origin")
			}
			if r.URL.Path == "/origin/cover.png" {
				covers.Add(1)
				w.Header().Set("Content-Type", "image/png")
				w.Write([]byte("\x89PNG\r\n\x1a\nsynthetic cover"))
				return
			}
			downloads.Add(1)
			w.Header().Set("Content-Type", "audio/wav")
			w.Write(testWAV())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/song/detail":
			details.Add(1)
			io.WriteString(w, `{"code":200,"songs":[{"id":9,"name":"unselected"},{"id":42,"name":"chosen","dt":1000,"ar":[{"name":"artist"}],"al":{"id":8,"name":"album","picUrl":"`+origin+`/origin/cover.png"}}]}`)
		case "/lyric":
			lyrics.Add(1)
			io.WriteString(w, `{"code":200,"lrc":{"lyric":"[00:00.00]full first\n[00:00.50]full second"}}`)
		case "/song/url/v1":
			urls.Add(1)
			r.ParseForm()
			if r.Form.Get("unblock") != "true" {
				t.Error("unblock missing")
			}
			io.WriteString(w, `{"code":200,"data":[{"id":42,"url":"`+origin+`/origin/track.wav"}]}`)
		case "/open-apis/auth/v3/tenant_access_token/internal":
			io.WriteString(w, `{"code":0,"tenant_access_token":"synthetic-token","expire":7200}`)
		case "/open-apis/im/v1/images":
			images.Add(1)
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
				return
			}
			f, _, e := r.FormFile("image")
			if e != nil {
				t.Error(e)
				return
			}
			f.Close()
			io.WriteString(w, fmt.Sprintf(`{"code":0,"data":{"image_key":"image-%d"}}`, images.Load()))
		case "/open-apis/im/v1/files":
			audios.Add(1)
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
				return
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
				return
			}
			b, _ := io.ReadAll(f)
			f.Close()
			if !bytes.Contains(b, []byte("OpusHead")) || r.FormValue("duration") != "1000" {
				t.Error("native audio bytes or duration lost")
			}
			if failAudio.Load() {
				http.Error(w, "synthetic uncertain upload", http.StatusServiceUnavailable)
				return
			}
			io.WriteString(w, `{"code":0,"data":{"file_key":"native-audio"}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer fake.Close()
	origin = fake.URL
	provider, e := business.NewMusicClient(business.MusicConfig{BaseURL: origin, Cookie: "private-provider-cookie"})
	if e != nil {
		t.Fatal(e)
	}
	image, e := lark.NewClient(lark.ClientConfig{AppID: "music-app", AppSecret: "secret", BotOpenID: "bot", TenantID: "music-tenant", BaseURL: origin})
	if e != nil {
		t.Fatal(e)
	}
	audio, e := business.NewLarkAudioUploader(business.LarkAudioConfig{AppID: "music-app", AppSecret: "secret", BaseURL: origin})
	if e != nil {
		t.Fatal(e)
	}
	bucket := fmt.Sprintf("music-test-%d", time.Now().UnixNano())
	var svc *Service
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.Handler().ServeHTTP(w, r) }))
	defer public.Close()
	cfg := Config{DB: db, Schema: schema, Provider: provider, ObjectStore: media.MinIOConfig{Endpoint: os.Getenv("AGENT_TEST_MINIO_ENDPOINT"), AccessKey: os.Getenv("AGENT_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("AGENT_TEST_MINIO_SECRET_KEY"), Bucket: bucket}, PublicURL: public.URL, PlayerURL: "https://player.example.test/player?theme=dark", SigningKey: bytes.Repeat([]byte{7}, 32), ImageUploader: image, AudioUploader: audio, AllowedMediaHosts: []string{"127.0.0.1"}, AllowedImageHosts: []string{"127.0.0.1"}}
	svc, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Initialize(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for o := range svc.objects.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			svc.objects.RemoveObject(context.Background(), bucket, o.Key, minio.RemoveObjectOptions{})
		}
		svc.objects.RemoveBucket(context.Background(), bucket)
	})
	song, e := svc.PrepareSong(context.Background(), testScope(), 42, false)
	if e != nil || song.Item.ID != 42 || song.ImageKey != "image-1" || song.Item.DurationMS != 1000 || !strings.Contains(song.Lyrics.Full, "full second") {
		t.Fatalf("prepare %+v %v", song, e)
	}
	player, e := url.Parse(song.PlayerURL)
	if e != nil || player.Query().Get("target") != song.ManifestURL || player.Query().Get("theme") != "dark" {
		t.Fatalf("legacy player target contract %s", song.PlayerURL)
	}
	check := func(raw string, method string, headers map[string]string) (int, []byte, http.Header) {
		t.Helper()
		r, _ := http.NewRequest(method, raw, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}
	code, b, h := check(song.ManifestURL, "GET", map[string]string{"Origin": "https://player.example.test"})
	var manifest struct {
		Title, LyricsURL, MusicURL, PictureURL, Album string
		Artist                                        []struct{ Name string }
		Duration                                      int
	}
	e = json.Unmarshal(b, &manifest)
	if code != 200 || e != nil || manifest.Title != "chosen" || manifest.Album != "album" || manifest.Duration != 1000 || len(manifest.Artist) != 1 || manifest.Artist[0].Name != "artist" || manifest.MusicURL != song.MusicURL || h.Get("Access-Control-Allow-Origin") != "https://player.example.test" {
		t.Fatalf("manifest contract code=%d body=%s header=%v", code, b, h)
	}
	code, b, h = check(song.MusicURL, "GET", map[string]string{"Range": "bytes=0-3"})
	if code != 206 || string(b) != "RIFF" || h.Get("Content-Range") == "" || h.Get("Content-Type") != "audio/wav" {
		t.Fatalf("range %d %q %v", code, b, h)
	}
	code, b, h = check(song.MusicURL, "HEAD", nil)
	if code != 200 || len(b) != 0 || h.Get("Content-Length") != "16044" || h.Get("ETag") == "" {
		t.Fatalf("head %d bytes=%d %v", code, len(b), h)
	}
	code, _, _ = check(song.MusicURL+"tamper", "GET", nil)
	if code != 403 {
		t.Fatalf("tampered token accepted %d", code)
	}
	u, _ := url.Parse(song.MusicURL)
	u.Path = "/music/assets/"
	code, _, _ = check(u.String(), "GET", nil)
	if code != 403 {
		t.Fatalf("unsigned asset accepted %d", code)
	}
	native, e := svc.UploadAudio(context.Background(), testScope(), 42, false)
	if e != nil || native.FileKey != "native-audio" || native.DurationMS != 1000 {
		t.Fatalf("native audio %+v %v", native, e)
	}
	svc, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Initialize(context.Background()); e != nil {
		t.Fatal(e)
	}
	cached, e := svc.PrepareSong(context.Background(), testScope(), 42, false)
	if e != nil || cached.ImageKey != song.ImageKey {
		t.Fatalf("restart cache %+v %v", cached, e)
	}
	_, e = svc.UploadAudio(context.Background(), testScope(), 42, false)
	if e != nil {
		t.Fatal(e)
	}
	if details.Load() != 1 || lyrics.Load() != 1 || urls.Load() != 1 || covers.Load() != 1 || downloads.Load() != 1 || images.Load() != 1 || audios.Load() != 1 {
		t.Fatalf("cache reissued calls details=%d lyrics=%d urls=%d covers=%d downloads=%d images=%d audios=%d", details.Load(), lyrics.Load(), urls.Load(), covers.Load(), downloads.Load(), images.Load(), audios.Load())
	}
	other := testScope()
	other.ChatID = "other-chat"
	cover, e := svc.ResolveCover(context.Background(), other, "song", 42, origin+"/origin/cover.png")
	if e != nil || cover.ImageKey == song.ImageKey || images.Load() != 2 {
		t.Fatalf("scope cache aliased %+v %v", cover, e)
	}
	_, e = svc.PrepareSong(context.Background(), testScope(), 42, true)
	if e != nil || urls.Load() != 2 || downloads.Load() != 2 {
		t.Fatalf("refresh reused source urls=%d downloads=%d err=%v", urls.Load(), downloads.Load(), e)
	}
	refreshAudio, ok := any(svc).(interface {
		RefreshAudio(context.Context, domain.Scope, int64) (Audio, error)
	})
	if !ok {
		t.Fatal("explicit audio refresh cannot bypass current-content upload cache")
	}
	refreshed, e := refreshAudio.RefreshAudio(context.Background(), testScope(), 42)
	if e != nil || refreshed.FileKey != "native-audio" || audios.Load() != 2 {
		t.Fatalf("unchanged source skipped explicit native refresh: %+v %v uploads=%d", refreshed, e, audios.Load())
	}
	failAudio.Store(true)
	if _, e = refreshAudio.RefreshAudio(context.Background(), testScope(), 42); e == nil || audios.Load() != 3 {
		t.Fatalf("uncertain native refresh reported success: %v uploads=%d", e, audios.Load())
	}
	if _, e = svc.UploadAudio(context.Background(), testScope(), 42, false); !errors.Is(e, ErrUploadUnknown) || audios.Load() != 3 {
		t.Fatalf("ordinary native upload retried unknown: %v uploads=%d", e, audios.Load())
	}
	failAudio.Store(false)
	recovered, e := refreshAudio.RefreshAudio(context.Background(), testScope(), 42)
	if e != nil || recovered.FileKey != "native-audio" || audios.Load() != 4 {
		t.Fatalf("explicit native refresh failed to recover unknown: %+v %v uploads=%d", recovered, e, audios.Load())
	}
	if details.Load() != 2 || lyrics.Load() != 2 || urls.Load() != 2 || covers.Load() != 3 || downloads.Load() != 2 || images.Load() != 3 {
		t.Fatalf("native refresh refetched prepared source details=%d lyrics=%d urls=%d covers=%d downloads=%d images=%d", details.Load(), lyrics.Load(), urls.Load(), covers.Load(), downloads.Load(), images.Load())
	}

	imageOther, e := lark.NewClient(lark.ClientConfig{AppID: "other-app", AppSecret: "other-secret", BotOpenID: "other-bot", TenantID: "music-tenant", BaseURL: origin})
	if e != nil {
		t.Fatal(e)
	}
	otherConfig := cfg
	otherConfig.ImageUploader = imageOther
	otherService, e := New(otherConfig)
	if e != nil {
		t.Fatal(e)
	}
	otherApp := testScope()
	otherApp.AppID = "other-app"
	appCover, e := otherService.ResolveCover(context.Background(), otherApp, "song", 42, origin+"/origin/cover.png")
	if e != nil || appCover.ImageKey == song.ImageKey || images.Load() != 4 {
		t.Fatalf("application cache reused another app's image: %+v %v", appCover, e)
	}

}

func TestRealMusicUploadLockWorksWithSingleDatabaseConnection(t *testing.T) {
	db, schema := testDB(t)
	db.SetMaxOpenConns(1)
	db.Exec(`CREATE SCHEMA "` + schema + `"`)
	svc := &Service{cfg: Config{DB: db, Schema: schema}}
	if _, e := db.Exec(`CREATE TABLE ` + svc.table("uploads") + ` (cache_key text PRIMARY KEY,status text NOT NULL,body bytea NOT NULL,updated_at timestamptz DEFAULT clock_timestamp())`); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, e := svc.upload(ctx, "selected-asset", false, func(context.Context) ([]byte, error) { return []byte("actual-receipt"), nil })
	if e != nil || string(b) != "actual-receipt" {
		t.Fatalf("asset locking exhausted pool: %q %v", b, e)
	}
}

func TestMusicAssetExpiryPurposeAndMethodAreVerified(t *testing.T) {
	svc := &Service{cfg: Config{PublicURL: "https://agent.example.test", SigningKey: bytes.Repeat([]byte("s"), 32)}}
	for _, expiry := range []time.Time{time.Now().Add(-time.Second), time.Unix(0, 0)} {
		u, _ := url.Parse(svc.assetURL("music/v1/scope/audio/object", expiry))
		w := httptest.NewRecorder()
		svc.Handler().ServeHTTP(w, httptest.NewRequest("GET", u.String(), nil))
		if w.Code != 403 {
			t.Fatalf("expired link accepted %d", w.Code)
		}
	}
	valid, _ := url.Parse(svc.assetURL("music/v1/scope/audio/object", time.Now().Add(time.Minute)))
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, httptest.NewRequest("POST", valid.String(), nil))
	if w.Code != 405 {
		t.Fatalf("asset mutation method accepted %d", w.Code)
	}
	another := &Service{cfg: Config{SigningKey: bytes.Repeat([]byte("x"), 32)}}
	w = httptest.NewRecorder()
	another.Handler().ServeHTTP(w, httptest.NewRequest("GET", valid.String(), nil))
	if w.Code != 403 {
		t.Fatalf("foreign signing key accepted %d", w.Code)
	}
	w = httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://agent.example.test/music/assets/not-a-signature", nil))
	if w.Code != 403 {
		t.Fatal("unsigned read accepted")
	}
}

func TestRealMusicUnknownUploadSurvivesRestartAndNeedsExplicitRefresh(t *testing.T) {
	db, schema := testDB(t)
	db.Exec(`CREATE SCHEMA "` + schema + `"`)
	svc := &Service{cfg: Config{DB: db, Schema: schema}}
	if _, e := db.Exec(`CREATE TABLE ` + svc.table("uploads") + ` (cache_key text PRIMARY KEY,status text NOT NULL,body bytea NOT NULL,updated_at timestamptz DEFAULT clock_timestamp())`); e != nil {
		t.Fatal(e)
	}
	calls := 0
	failed := func(context.Context) ([]byte, error) { calls++; return nil, business.ErrProvider }
	if _, e := svc.upload(context.Background(), "unknown-selected-upload", false, failed); e == nil {
		t.Fatal("provider failure reported successful")
	}
	restarted := &Service{cfg: svc.cfg}
	if _, e := restarted.upload(context.Background(), "unknown-selected-upload", false, failed); !errors.Is(e, ErrUploadUnknown) || calls != 1 {
		t.Fatalf("unknown outcome reuploaded: calls=%d err=%v", calls, e)
	}
	if _, e := restarted.upload(context.Background(), "unknown-selected-upload", true, func(context.Context) ([]byte, error) { calls++; return []byte("confirmed-receipt"), nil }); e != nil || calls != 2 {
		t.Fatalf("explicit refresh did not recover: %v", e)
	}
	b, e := svc.upload(context.Background(), "unknown-selected-upload", false, failed)
	if e != nil || string(b) != "confirmed-receipt" || calls != 2 {
		t.Fatalf("actual refreshed receipt not durable: %q %v", b, e)
	}
}

func TestRealMusicConcurrentUploadUsesOneActualReceipt(t *testing.T) {
	db, schema := testDB(t)
	db.SetMaxOpenConns(2)
	db.Exec(`CREATE SCHEMA "` + schema + `"`)
	svc := &Service{cfg: Config{DB: db, Schema: schema}}
	if _, e := db.Exec(`CREATE TABLE ` + svc.table("uploads") + ` (cache_key text PRIMARY KEY,status text NOT NULL,body bytea NOT NULL,updated_at timestamptz DEFAULT clock_timestamp())`); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			b, e := svc.upload(ctx, "same-scope-and-version", false, func(context.Context) ([]byte, error) { calls.Add(1); return []byte("actual-receipt"), nil })
			if e != nil || string(b) != "actual-receipt" {
				t.Errorf("concurrent receipt %q %v", b, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate external uploads %d", calls.Load())
	}
}

func TestMusicPlayerCanRunWithoutNativeAudioAdapter(t *testing.T) {
	provider, e := business.NewMusicClient(business.MusicConfig{BaseURL: "https://music.example.test"})
	if e != nil {
		t.Fatal(e)
	}
	image, e := lark.NewClient(lark.ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot"})
	if e != nil {
		t.Fatal(e)
	}
	svc, e := New(Config{DB: &sql.DB{}, Schema: "music_test", Provider: provider, ImageUploader: image, ObjectStore: media.MinIOConfig{Endpoint: "minio.example.test:9000", AccessKey: "synthetic", SecretKey: "synthetic", Bucket: "music-test"}, PublicURL: "https://agent.example.test", PlayerURL: "https://player.example.test", SigningKey: bytes.Repeat([]byte("s"), 32)})
	if e != nil {
		t.Fatalf("optional native audio disabled player publication: %v", e)
	}
	_, e = svc.UploadAudio(context.Background(), testScope(), 42, false)
	if e == nil || e.Error() != "music native audio unavailable" {
		t.Fatalf("disabled audio did not fail before network/storage: %v", e)
	}
}

func TestRealMusicOriginFailureRetriesAndExplicitRefreshReuploads(t *testing.T) {
	db, schema := testDB(t)
	var recovered atomic.Bool
	var downloads, uploads atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cover.png" {
			downloads.Add(1)
			if !recovered.Load() {
				http.Error(w, "synthetic outage", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG\r\n\x1a\nrecovered cover"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			io.WriteString(w, `{"code":0,"tenant_access_token":"refresh-token","expire":7200}`)
		case "/open-apis/im/v1/images":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
				return
			}
			if r.Header.Get("Authorization") != "Bearer refresh-token" || r.FormValue("image_type") != "message" {
				t.Error("refresh upload lost token or purpose")
			}
			f, _, e := r.FormFile("image")
			if e != nil {
				t.Error(e)
				return
			}
			b, _ := io.ReadAll(f)
			f.Close()
			if !bytes.Contains(b, []byte("recovered cover")) {
				t.Error("refresh uploaded stale cover")
			}
			uploads.Add(1)
			io.WriteString(w, `{"code":0,"data":{"image_key":"recovered-image"}}`)
		default:
			t.Errorf("unexpected origin path %s", r.URL.Path)
		}
	}))
	defer origin.Close()
	provider, e := business.NewMusicClient(business.MusicConfig{BaseURL: origin.URL})
	if e != nil {
		t.Fatal(e)
	}
	image, e := lark.NewClient(lark.ClientConfig{AppID: "music-app", AppSecret: "secret", BotOpenID: "bot", TenantID: "music-tenant", BaseURL: origin.URL})
	if e != nil {
		t.Fatal(e)
	}
	bucket := fmt.Sprintf("music-refresh-%d", time.Now().UnixNano())
	svc, e := New(Config{DB: db, Schema: schema, Provider: provider, ImageUploader: image, ObjectStore: media.MinIOConfig{Endpoint: os.Getenv("AGENT_TEST_MINIO_ENDPOINT"), AccessKey: os.Getenv("AGENT_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("AGENT_TEST_MINIO_SECRET_KEY"), Bucket: bucket}, PublicURL: "https://agent.example.test", PlayerURL: "https://player.example.test", SigningKey: bytes.Repeat([]byte("s"), 32), AllowedImageHosts: []string{"127.0.0.1"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.Initialize(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for o := range svc.objects.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			svc.objects.RemoveObject(context.Background(), bucket, o.Key, minio.RemoveObjectOptions{})
		}
		svc.objects.RemoveBucket(context.Background(), bucket)
	})
	raw := origin.URL + "/cover.png"
	if _, e = svc.ResolveCover(context.Background(), testScope(), "song", 42, raw); e == nil || downloads.Load() != 1 || uploads.Load() != 0 {
		t.Fatalf("failed origin became uploaded cover: %v downloads=%d uploads=%d", e, downloads.Load(), uploads.Load())
	}
	recovered.Store(true)
	if cover, e := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw); e != nil || cover.ImageKey != "recovered-image" || downloads.Load() != 2 || uploads.Load() != 1 {
		t.Fatalf("ordinary resolve did not recover before-upload failure: %v downloads=%d uploads=%d", e, downloads.Load(), uploads.Load())
	}
	refresh, ok := any(svc).(interface {
		RefreshCover(context.Context, domain.Scope, string, int64, string) (Cover, error)
	})
	if !ok {
		t.Fatal("explicit cover refresh cannot recover unknown upload")
	}
	cover, e := refresh.RefreshCover(context.Background(), testScope(), "song", 42, raw)
	if e != nil || cover.ImageKey != "recovered-image" || cover.URL == "" || downloads.Load() != 3 || uploads.Load() != 2 {
		t.Fatalf("explicit refresh did not recover actual receipt: %+v %v downloads=%d uploads=%d", cover, e, downloads.Load(), uploads.Load())
	}
	cached, e := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw)
	if e != nil || cached.ImageKey != cover.ImageKey || downloads.Load() != 3 || uploads.Load() != 2 {
		t.Fatalf("refreshed cover was not durable: %+v %v", cached, e)
	}
}

type musicTransport func(*http.Request) (*http.Response, error)

func (f musicTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRealMusicConcurrentInitializeOwnsFreshBucket(t *testing.T) {
	db, schema := testDB(t)
	provider, e := business.NewMusicClient(business.MusicConfig{BaseURL: "https://music.example.test"})
	if e != nil {
		t.Fatal(e)
	}
	image, e := lark.NewClient(lark.ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot"})
	if e != nil {
		t.Fatal(e)
	}
	bucket := fmt.Sprintf("music-init-race-%d", time.Now().UnixNano())
	cfg := Config{DB: db, Schema: schema, Provider: provider, ImageUploader: image, ObjectStore: media.MinIOConfig{Endpoint: os.Getenv("AGENT_TEST_MINIO_ENDPOINT"), AccessKey: os.Getenv("AGENT_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("AGENT_TEST_MINIO_SECRET_KEY"), Bucket: bucket}, PublicURL: "https://agent.example.test", PlayerURL: "https://player.example.test", SigningKey: bytes.Repeat([]byte("s"), 32)}
	svc, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	transport, e := minio.DefaultTransport(false)
	if e != nil {
		t.Fatal(e)
	}
	defer transport.CloseIdleConnections()
	release := make(chan struct{})
	var absentChecks atomic.Int32
	// Both independent initializers receive a real absent response before either
	// can create the bucket, deterministically reproducing the startup race.
	raced, e := minio.New(cfg.ObjectStore.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.ObjectStore.AccessKey, cfg.ObjectStore.SecretKey, ""), Transport: musicTransport(func(r *http.Request) (*http.Response, error) {
		resp, e := transport.RoundTrip(r)
		if e == nil && (r.Method == http.MethodHead || (r.Method == http.MethodGet && r.URL.Query().Has("location"))) && strings.TrimRight(r.URL.Path, "/") == "/"+bucket && resp.StatusCode == http.StatusNotFound {
			if absentChecks.Add(1) == 2 {
				close(release)
			}
			select {
			case <-release:
			case <-r.Context().Done():
				resp.Body.Close()
				return nil, r.Context().Err()
			}
		}
		return resp, e
	})})
	if e != nil {
		t.Fatal(e)
	}
	svc.objects = raced
	t.Cleanup(func() { svc.objects.RemoveBucket(context.Background(), bucket) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- svc.Initialize(ctx) }()
	}
	for i := 0; i < 2; i++ {
		if e := <-results; e != nil {
			t.Errorf("concurrent initializer rejected this owner's bucket: %v", e)
		}
	}
	if absentChecks.Load() != 2 {
		t.Fatalf("race not exercised: absent checks=%d", absentChecks.Load())
	}
	exists, e := svc.objects.BucketExists(ctx, bucket)
	if e != nil || !exists {
		t.Fatalf("fresh owned bucket unavailable: exists=%t err=%v", exists, e)
	}
}

func TestRealMusicBucketExistsConflictRequiresAccessibleBucket(t *testing.T) {
	for _, tc := range []struct {
		name, code         string
		accessible, wantOK bool
		wantChecks         int32
	}{
		{"owned", "BucketAlreadyOwnedByYou", false, true, 1},
		{"existing-accessible", "BucketAlreadyExists", true, true, 2},
		{"existing-denied", "BucketAlreadyExists", false, false, 2},
		{"other-error", "AccessDenied", true, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, schema := testDB(t)
			var checks atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodHead:
					if checks.Add(1) == 1 {
						w.WriteHeader(http.StatusNotFound)
					} else if tc.accessible {
						w.WriteHeader(http.StatusOK)
					} else {
						w.WriteHeader(http.StatusForbidden)
					}
				case http.MethodPut:
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusConflict)
					io.WriteString(w, `<Error><Code>`+tc.code+`</Code><Message>synthetic conflict</Message></Error>`)
				default:
					t.Errorf("unexpected bucket operation %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer endpoint.Close()
			objects, e := minio.New(strings.TrimPrefix(endpoint.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("synthetic", "synthetic", ""), Region: "us-east-1"})
			if e != nil {
				t.Fatal(e)
			}
			svc := &Service{cfg: Config{DB: db, Schema: schema, ObjectStore: media.MinIOConfig{Bucket: "music-conflict-test", Timeout: 5 * time.Second}}, objects: objects}
			e = svc.Initialize(context.Background())
			if (e == nil) != tc.wantOK || checks.Load() != tc.wantChecks {
				t.Fatalf("bucket conflict handling code=%s err=%v access-checks=%d", tc.code, e, checks.Load())
			}
		})
	}
}
