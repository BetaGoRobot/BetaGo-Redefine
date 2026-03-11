package reaction

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMetaInitPrefersOpenID(t *testing.T) {
	openID := "ou_open"
	legacyOpenIDName := "cli_legacy"

	meta := metaInit(&larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			UserId: &larkim.UserId{
				OpenId: &openID,
				UserId: &legacyOpenIDName,
			},
		},
	})

	if meta.OpenID != openID {
		t.Fatalf("metaInit() open id = %q, want %q", meta.OpenID, openID)
	}
}

func TestMetaInitReturnsEmptyWithoutOpenID(t *testing.T) {
	legacyUserID := "cli_legacy"

	meta := metaInit(&larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			UserId: &larkim.UserId{
				UserId: &legacyUserID,
			},
		},
	})

	if meta.OpenID != "" {
		t.Fatalf("metaInit() open id = %q, want empty string when only legacy user id %q exists", meta.OpenID, legacyUserID)
	}
}

func TestReactionOpenIDUsesMetaOpenID(t *testing.T) {
	meta := &xhandler.BaseMetaData{
		OpenID: "ou_open",
	}

	if got := reactionOpenID(nil, meta); got != "ou_open" {
		t.Fatalf("reactionOpenID() = %q, want %q", got, "ou_open")
	}
}
