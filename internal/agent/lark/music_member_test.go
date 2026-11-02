package lark

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestMusicMemberUsesFreshScopedPagesAndNeverTrustsPartialAbsence(t *testing.T) {
	for _, mode := range []string{"found_second_page", "absent", "partial", "provider_rejected"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/auth/") {
					return response(`{"code":0,"tenant_access_token":"fixture","expire":7200}`), nil
				}
				if r.Method != "GET" || r.URL.Path != "/open-apis/im/v1/chats/group/members" || r.URL.Query().Get("member_id_type") != "open_id" {
					t.Fatal("membership request lost exact scope/ID type")
				}
				pages++
				if mode == "provider_rejected" {
					return response(`{"code":99991672}`), nil
				}
				if mode == "partial" {
					return response(`{"code":0,"data":{"items":[],"trigger_security_conf_limit":true,"has_more":false}}`), nil
				}
				if mode == "found_second_page" && pages == 1 {
					return response(`{"code":0,"data":{"items":[{"member_id":"other","member_id_type":"open_id"}],"has_more":true,"page_token":"next-page"}}`), nil
				}
				if mode == "found_second_page" {
					return response(`{"code":0,"data":{"items":[{"member_id":"alice","member_id_type":"open_id"}],"has_more":false}}`), nil
				}
				return response(`{"code":0,"data":{"items":[],"has_more":false}}`), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			found, err := c.MusicMember(context.Background(), clientScope(), "alice")
			if mode == "found_second_page" {
				if err != nil || !found || pages != 2 {
					t.Fatal("current member on later page not admitted")
				}
				return
			}
			if found {
				t.Fatal("absent or unproven member admitted")
			}
			if mode == "absent" && err != nil {
				t.Fatal("complete absence should be a definite false result")
			}
			if mode != "absent" && err == nil {
				t.Fatal("incomplete/failed membership read was mistaken for complete absence")
			}
		})
	}
}
func TestMusicMemberRejectsForeignScopeBeforeAnyProviderCall(t *testing.T) {
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("foreign scope reached provider")
		return nil, domain.ErrUnauthorized
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := clientScope()
	s.AppID = "foreign"
	if ok, err := c.MusicMember(context.Background(), s, "alice"); err == nil || ok {
		t.Fatal("foreign membership proof accepted")
	}
}
