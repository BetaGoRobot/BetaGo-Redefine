package lark

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestMusicImageUploadUsesScopedActualMultipartAndFreshToken(t *testing.T) {
	tokens, uploads := 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokens++
			io.WriteString(w, `{"code":0,"tenant_access_token":"own-music-token","expire":7200}`)
		case "/open-apis/im/v1/images":
			uploads++
			if r.Header.Get("Authorization") != "Bearer own-music-token" {
				t.Error("foreign/global token used")
			}
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
				return
			}
			if r.FormValue("image_type") != "message" {
				t.Error("wrong upload purpose")
			}
			f, _, e := r.FormFile("image")
			if e != nil {
				t.Error(e)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if !bytes.Equal(b, []byte("synthetic-image")) {
				t.Error("wrong image bytes")
			}
			io.WriteString(w, `{"code":0,"data":{"image_key":"real-image"}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer s.Close()
	c, e := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", TenantID: "tenant", AllowedChatIDs: []string{"chat"}, BaseURL: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	scope := domain.Scope{AppID: "app", TenantID: "tenant", ChatID: "chat", Visibility: "group"}
	key, e := c.UploadMusicImage(context.Background(), scope, []byte("synthetic-image"))
	if e != nil || key != "real-image" || uploads != 1 || tokens != 1 {
		t.Fatalf("image %q %v uploads=%d tokens=%d", key, e, uploads, tokens)
	}
	scope.AppID = "foreign"
	if _, e = c.UploadMusicImage(context.Background(), scope, []byte("synthetic-image")); e == nil || uploads != 1 {
		t.Fatal("foreign application uploaded")
	}
}

func TestMusicImageEmptyReceiptFails(t *testing.T) {
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		return response(`{"code":0,"data":{"image_key":""}}`), nil
	})
	if _, e := c.UploadMusicImage(context.Background(), clientScope(), []byte("image")); e == nil {
		t.Fatal("empty image receipt reported as success")
	}
}
