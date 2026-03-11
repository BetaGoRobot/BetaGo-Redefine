package handlers

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestCurrentOpenIDPrefersOpenID(t *testing.T) {
	openID := "ou_open"
	legacyUserID := "cli_legacy"

	got := currentOpenID(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
					UserId: &legacyUserID,
				},
			},
		},
	}, nil)

	if got != openID {
		t.Fatalf("currentOpenID() = %q, want %q", got, openID)
	}
}

func TestCurrentOpenIDUsesMetaOpenID(t *testing.T) {
	meta := &xhandler.BaseMetaData{
		OpenID: "ou_open",
	}

	if got := currentOpenID(nil, meta); got != "ou_open" {
		t.Fatalf("currentOpenID() = %q, want %q", got, "ou_open")
	}
}
