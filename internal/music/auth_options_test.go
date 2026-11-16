package music

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestPublicAuthOptionsExposeOnlyConfiguredCapabilities(t *testing.T) {
	db := testDB(t)
	for _, tc := range []struct {
		name    string
		opts    Options
		enabled bool
		appID   string
	}{
		{name: "guest only"},
		{name: "app without verifier", opts: Options{AppID: "cli_app"}},
		{name: "verifier without explicit app", opts: Options{Verifier: fakeVerifier{}}},
		{name: "configured", opts: Options{AppID: "cli_app", Verifier: fakeVerifier{}}, enabled: true, appID: "cli_app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewServer(db, tc.opts).HTTP()
			w := request(h, "", "GET", "/auth/options", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("public capability lookup requires a session: %d %s", w.Code, w.Body.String())
			}
			out := call(t, h, "", "GET", "/auth/options", nil, http.StatusOK)
			want := map[string]any{"guest": true, "feishu": map[string]any{"enabled": tc.enabled, "appID": tc.appID}}
			if !reflect.DeepEqual(out, want) {
				t.Fatalf("capability response leaked or misrepresented configuration: got %#v want %#v", out, want)
			}
			if len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("capability lookup issued a session or allowed stale auth configuration")
			}
		})
	}
}

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, string) (Identity, error) {
	return Identity{}, fmt.Errorf("invalid temporary code")
}

func TestFeishuExchangeDistinguishesConfigurationAndRejectedCode(t *testing.T) {
	db := testDB(t)
	for _, tc := range []struct {
		name   string
		opts   Options
		status int
		error  string
	}{
		{name: "not configured", status: 503, error: "feishu_not_configured"},
		{name: "app missing", opts: Options{Verifier: rejectingVerifier{}}, status: 503, error: "feishu_not_configured"},
		{name: "invalid code", opts: Options{AppID: "cli_app", Verifier: rejectingVerifier{}}, status: 401, error: "feishu_code_rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewServer(db, tc.opts).HTTP()
			out := call(t, h, "", "POST", "/auth/feishu/exchange", map[string]any{"code": "temporary-invalid-code"}, tc.status)
			if !reflect.DeepEqual(out, map[string]any{"error": tc.error}) {
				t.Fatalf("exchange leaked verifier details: %#v", out)
			}
		})
	}
}
