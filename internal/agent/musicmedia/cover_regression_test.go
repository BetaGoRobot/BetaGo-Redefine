package musicmedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/media"
	"github.com/minio/minio-go/v7"
)

type coverUploadFunc func(context.Context, domain.Scope, []byte) (string, error)

func (f coverUploadFunc) UploadMusicImage(ctx context.Context, scope domain.Scope, b []byte) (string, error) {
	return f(ctx, scope, b)
}

func coverFixture(t *testing.T, origin http.Handler, upload coverUploadFunc) (*Service, string) {
	t.Helper()
	db, schema := testDB(t)
	server := httptest.NewServer(origin)
	t.Cleanup(server.Close)
	provider, err := business.NewMusicClient(business.MusicConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	bucket := fmt.Sprintf("cover-regression-%d", time.Now().UnixNano())
	svc, err := New(Config{DB: db, Schema: schema, Provider: provider, ImageUploader: upload,
		ObjectStore: media.MinIOConfig{Endpoint: os.Getenv("AGENT_TEST_MINIO_ENDPOINT"), AccessKey: os.Getenv("AGENT_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("AGENT_TEST_MINIO_SECRET_KEY"), Bucket: bucket},
		PublicURL:   "https://agent.example.test", PlayerURL: "https://player.example.test", SigningKey: bytes.Repeat([]byte("s"), 32), AllowedImageHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for object := range svc.objects.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err != nil {
				t.Error(object.Err)
				continue
			}
			if err := svc.objects.RemoveObject(context.Background(), bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
				t.Error(err)
			}
		}
		if err := svc.objects.RemoveBucket(context.Background(), bucket); err != nil {
			t.Error(err)
		}
	})
	return svc, server.URL + "/cover.jpg"
}

func coverJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRealMusicCoverUsesImageContentInsteadOfDeclaredType(t *testing.T) {
	data := coverJPEG(t)
	for _, declared := range []string{"image/jpg", "image/jpg; charset=UTF-8", "image/jpeg", "application/octet-stream", "text/plain"} {
		t.Run(declared, func(t *testing.T) {
			var uploads atomic.Int32
			svc, raw := coverFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", declared)
				w.Write(data)
			}), func(_ context.Context, _ domain.Scope, b []byte) (string, error) {
				uploads.Add(1)
				if !bytes.Equal(b, data) {
					t.Error("image bytes changed before upload")
				}
				return "actual-image-key", nil
			})
			cover, err := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw)
			if err != nil || cover.ImageKey != "actual-image-key" || uploads.Load() != 1 {
				t.Fatalf("valid JPEG rejected for declared type %q: cover=%+v uploads=%d err=%v", declared, cover, uploads.Load(), err)
			}
			var mime string
			if err := svc.cfg.DB.QueryRow(`SELECT mime FROM ` + svc.table("objects") + ` WHERE purpose='cover'`).Scan(&mime); err != nil || mime != "image/jpeg" {
				t.Fatalf("stored image MIME was not identified from content: %q %v", mime, err)
			}
		})
	}
}

func TestRealMusicCoverPreUploadFailureAllowsOrdinaryRetry(t *testing.T) {
	data := coverJPEG(t)
	for _, failure := range []string{"origin_error", "html_labeled_as_jpeg", "storage_error"} {
		t.Run(failure, func(t *testing.T) {
			var recovered atomic.Bool
			var uploads, downloads atomic.Int32
			svc, raw := coverFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads.Add(1)
				w.Header().Set("Content-Type", "image/jpeg")
				if !recovered.Load() {
					switch failure {
					case "origin_error":
						w.WriteHeader(http.StatusBadGateway)
						return
					case "html_labeled_as_jpeg":
						io.WriteString(w, "<html>upstream error</html>")
						return
					}
				}
				w.Write(data)
			}), func(context.Context, domain.Scope, []byte) (string, error) {
				uploads.Add(1)
				return "actual-image-key", nil
			})
			bucket := svc.cfg.ObjectStore.Bucket
			if failure == "storage_error" {
				svc.cfg.ObjectStore.Bucket = "absent-cover-bucket"
			}
			if _, err := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw); err == nil || uploads.Load() != 0 {
				t.Fatalf("pre-upload failure reached uploader: uploads=%d err=%v", uploads.Load(), err)
			}
			var admitted int
			if err := svc.cfg.DB.QueryRow(`SELECT count(*) FROM ` + svc.table("uploads")).Scan(&admitted); err != nil || admitted != 0 {
				t.Fatalf("pre-upload failure was recorded as an unknown external effect: receipts=%d err=%v", admitted, err)
			}
			recovered.Store(true)
			svc.cfg.ObjectStore.Bucket = bucket
			cover, err := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw)
			if err != nil || cover.ImageKey != "actual-image-key" || uploads.Load() != 1 || downloads.Load() != 2 {
				t.Fatalf("ordinary retry could not recover pre-upload failure: cover=%+v uploads=%d downloads=%d err=%v", cover, uploads.Load(), downloads.Load(), err)
			}
		})
	}
}

func TestRealMusicCoverLostUploadReceiptStillRequiresRefresh(t *testing.T) {
	var uploads, downloads atomic.Int32
	data := coverJPEG(t)
	svc, raw := coverFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(data)
	}), func(context.Context, domain.Scope, []byte) (string, error) {
		if uploads.Add(1) == 1 {
			return "", business.ErrProvider
		}
		return "refreshed-image-key", nil
	})
	if _, err := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw); err == nil {
		t.Fatal("lost upload receipt reported success")
	}
	if _, err := svc.ResolveCover(context.Background(), testScope(), "song", 42, raw); !errors.Is(err, ErrUploadUnknown) || uploads.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("ordinary retry repeated uncertain upload: uploads=%d downloads=%d err=%v", uploads.Load(), downloads.Load(), err)
	}
	cover, err := svc.RefreshCover(context.Background(), testScope(), "song", 42, raw)
	if err != nil || cover.ImageKey != "refreshed-image-key" || uploads.Load() != 2 {
		t.Fatalf("explicit refresh failed: cover=%+v uploads=%d err=%v", cover, uploads.Load(), err)
	}
}
