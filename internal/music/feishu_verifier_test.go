package music

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type feishuVerifierTransport func(*http.Request) (*http.Response, error)

func (f feishuVerifierTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func feishuJSON(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestFeishuVerifierUsesV3FormWithoutInventedH5Redirect(t *testing.T) {
	calls := 0
	jar, _ := cookiejar.New(nil)
	for _, origin := range []string{"https://accounts.feishu.cn", "https://open.feishu.cn"} {
		u, _ := url.Parse(origin)
		jar.SetCookies(u, []*http.Cookie{{Name: "unrelated_session", Value: "fixture-only"}})
	}
	transport := feishuVerifierTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" {
			t.Error("shared client cookie leaked to Feishu")
		}
		if calls == 1 {
			if r.URL.Scheme != "https" || r.URL.Host != "accounts.feishu.cn" || r.URL.Path != "/oauth/v3/token" {
				t.Errorf("old or incorrect token endpoint: %s", r.URL.Host+r.URL.Path)
			}
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("token exchange must use standard form POST")
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("token request added a second authentication mechanism")
			}
			if err := r.ParseForm(); err != nil {
				t.Error("token form cannot be decoded")
			}
			want := map[string]string{"grant_type": "authorization_code", "code": "temporary+code/&=中文", "client_id": "cli_fixture", "client_secret": "private-fixture-secret"}
			if len(r.PostForm) != len(want) {
				t.Error("H5 exchange added redirect, PKCE or unrelated fields")
			}
			for key, value := range want {
				if r.PostForm.Get(key) != value {
					t.Errorf("missing or changed form field %s", key)
				}
			}
			if r.URL.RawQuery != "" {
				t.Error("credentials entered token URL query")
			}
			return feishuJSON(r, `{"code":0,"access_token":"fixture-user-token"}`), nil
		}
		if r.URL.Scheme != "https" || r.URL.Host != "open.feishu.cn" || r.URL.Path != "/open-apis/authen/v1/user_info" {
			t.Errorf("incorrect user info endpoint: %s", r.URL.Host+r.URL.Path)
		}
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-user-token" {
			t.Error("userinfo did not use only the exchanged user token")
		}
		return feishuJSON(r, `{"code":0,"data":{"open_id":"verified-open-id","tenant_key":"verified-enterprise"}}`), nil
	})
	original := &http.Client{Timeout: 3 * time.Second, Jar: jar, Transport: transport}
	identity, err := (FeishuVerifier{AppID: "cli_fixture", AppSecret: "private-fixture-secret", RedirectURI: "https://music.test/play", Client: original}).Verify(context.Background(), "temporary+code/&=中文")
	if err != nil || identity != (Identity{AppID: "cli_fixture", TenantKey: "verified-enterprise", Subject: "verified-open-id"}) {
		t.Fatal("server-verified identity did not survive the H5 code exchange")
	}
	if calls != 2 {
		t.Errorf("exchange used %d requests", calls)
	}
	if original.Jar != jar || original.Timeout != 3*time.Second || original.CheckRedirect != nil {
		t.Fatal("verifier mutated the shared HTTP client")
	}
}

func TestFeishuVerifierRejectsCredentialBearingRedirects(t *testing.T) {
	for _, stage := range []string{"token", "userinfo"} {
		t.Run(stage, func(t *testing.T) {
			followed, exposed := false, false
			outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				followed = true
				body, _ := io.ReadAll(r.Body)
				exposed = strings.Contains(string(body), "fixture-secret") || r.Header.Get("Authorization") != ""
				fmt.Fprint(w, `{"code":0,"access_token":"fixture-user-token","data":{"open_id":"attacker","tenant_key":"attacker-tenant"}}`)
			}))
			defer outsider.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				isToken := strings.Contains(r.URL.Path, "token")
				if (stage == "token" && isToken) || (stage == "userinfo" && !isToken) {
					http.Redirect(w, r, outsider.URL+"/capture", http.StatusTemporaryRedirect)
					return
				}
				fmt.Fprint(w, `{"code":0,"access_token":"fixture-user-token"}`)
			}))
			defer upstream.Close()
			originalPolicyCalled := false
			original := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { originalPolicyCalled = true; return nil }}
			_, err := (FeishuVerifier{AppID: "cli_fixture", AppSecret: "fixture-secret", BaseURL: upstream.URL, Client: original}).Verify(context.Background(), "fixture-code")
			if err == nil || followed || exposed || originalPolicyCalled {
				t.Fatal("credential-bearing redirect was followed instead of rejected")
			}
			_ = original.CheckRedirect(nil, nil)
			if !originalPolicyCalled {
				t.Fatal("verifier replaced the caller's redirect policy")
			}
		})
	}
}
