package botidentity

import (
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMessageSenderOpenID(t *testing.T) {
	openID := "ou_open"

	got := MessageSenderOpenID(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	})

	if got != openID {
		t.Fatalf("MessageSenderOpenID() = %q, want %q", got, openID)
	}
}

func TestMessageSenderOpenIDReturnsEmptyWithoutOpenID(t *testing.T) {
	legacyUserID := "cli_legacy"

	got := MessageSenderOpenID(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					UserId: &legacyUserID,
				},
			},
		},
	})

	if got != "" {
		t.Fatalf("MessageSenderOpenID() = %q, want empty string", got)
	}
}

func TestReactionOpenID(t *testing.T) {
	openID := "ou_open"

	got := ReactionOpenID(&larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			UserId: &larkim.UserId{
				OpenId: &openID,
			},
		},
	})

	if got != openID {
		t.Fatalf("ReactionOpenID() = %q, want %q", got, openID)
	}
}
