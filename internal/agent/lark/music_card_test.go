package lark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestMusicCardUpdateUsesPinnedLocalReceiptAndFrozenCardKitFrames(t *testing.T) {
	converts, patches, guards := 0, 0, 0
	body := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[{"tag":"markdown","content":"lyrics"}]}}`
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", ValidateMusicCard: func(_ context.Context, s domain.Scope, id string) error {
		guards++
		if s != clientScope() || id != "own-music-card" {
			return domain.ErrUnauthorized
		}
		return nil
	}, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"fixture","expire":7200}`), nil
		}
		if r.Method == http.MethodGet {
			t.Fatal("pinned music frame unnecessarily reread original from Lark")
		}
		if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
			converts++
			return response(`{"code":0,"data":{"card_id":"music-entity"}}`), nil
		}
		var frame struct {
			Card     struct{ Type, Data string }
			Uuid     string
			Sequence int
		}
		if r.Method != http.MethodPut || r.URL.Path != "/open-apis/cardkit/v1/cards/music-entity" || json.NewDecoder(r.Body).Decode(&frame) != nil || frame.Card.Data != body || frame.Card.Type != "card_json" || frame.Uuid != "frame-uuid" || frame.Sequence != 2 {
			t.Fatal("music patch changed frozen target, UUID, sequence or bytes")
		}
		patches++
		return response(`{"code":200770}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = c.UpdateMusicCard(context.Background(), clientScope(), "own-music-card", body, "frame-uuid", 2); err != nil {
			t.Fatal(err)
		}
	}
	if guards != 2 || converts != 1 || patches != 2 {
		t.Fatal("music update lost local ownership checks or cached entity identity")
	}
}
func TestMusicCardUpdateRejectsMissingOwnershipAndInvalidFramesBeforeNetwork(t *testing.T) {
	for _, name := range []string{"no_validator", "foreign", "invalid_json", "no_update_multi", "empty_uuid", "sequence_zero"} {
		t.Run(name, func(t *testing.T) {
			cfg := ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid music patch reached network")
				return nil, errors.New("unavailable")
			})}, ValidateMusicCard: func(context.Context, domain.Scope, string) error { return nil }}
			body := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[]}}`
			uuid, sequence := "uuid", 1
			switch name {
			case "no_validator":
				cfg.ValidateMusicCard = nil
			case "foreign":
				cfg.ValidateMusicCard = func(context.Context, domain.Scope, string) error { return domain.ErrUnauthorized }
			case "invalid_json":
				body = "{"
			case "no_update_multi":
				body = `{"schema":"2.0"}`
			case "empty_uuid":
				uuid = ""
			case "sequence_zero":
				sequence = 0
			}
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.UpdateMusicCard(context.Background(), clientScope(), "message", body, uuid, sequence); err == nil {
				t.Fatal("invalid or unowned music frame accepted")
			}
		})
	}
}

func TestMusicCardUpdateClassifiesOnlyExplicitAudioRejection(t *testing.T) {
	for _, tc := range []struct {
		name, body, reply string
		network, want     bool
	}{
		{name: "explicit_code", body: `{"tag":"audio","file_key":"file"}`, reply: `{"code":230099,"msg":"Failed to create card content, ext=ErrCode: 300260; ErrMsg: audio elem don't support forward"}`, want: true},
		{name: "direct_code", body: `{"tag":"audio","file_key":"file"}`, reply: `{"code":300260,"msg":"unsupported"}`, want: true},
		{name: "network_ambiguous", body: `{"tag":"audio","file_key":"file"}`, network: true},
		{name: "other_api_error", body: `{"tag":"audio","file_key":"file"}`, reply: `{"code":230099,"msg":"another validation error"}`},
		{name: "not_audio", body: `{"tag":"markdown","content":"audio elem don't support forward"}`, reply: `{"code":230099,"msg":"ErrCode: 300260"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", ValidateMusicCard: func(context.Context, domain.Scope, string) error { return nil }, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/auth/") {
					return response(`{"code":0,"tenant_access_token":"fixture","expire":7200}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "id_convert") {
					return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
				}
				if tc.network {
					return nil, errors.New("connection lost after sending audio elem don't support forward")
				}
				return response(tc.reply), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			content := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[` + tc.body + `]}}`
			err = c.UpdateMusicCard(context.Background(), clientScope(), "own", content, "uuid", 1)
			if err == nil || errors.Is(err, domain.ErrMusicAudioUnsupported) != tc.want {
				t.Fatalf("want explicit unsupported %v; got %v", tc.want, err)
			}
		})
	}
}
