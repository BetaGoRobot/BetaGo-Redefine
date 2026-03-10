package messages

import (
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMetaInitPrefersOpenID(t *testing.T) {
	chatID := "oc_chat"
	chatType := "group"
	openID := "ou_open"
	userID := "cli_user"

	meta := metaInit(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:   &chatID,
				ChatType: &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
					UserId: &userID,
				},
			},
		},
	})

	if meta.UserID != openID {
		t.Fatalf("metaInit() user id = %q, want open id %q", meta.UserID, openID)
	}
}

func TestMetaInitFallsBackToUserID(t *testing.T) {
	chatID := "oc_chat"
	chatType := "group"
	userID := "cli_user"

	meta := metaInit(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:   &chatID,
				ChatType: &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					UserId: &userID,
				},
			},
		},
	})

	if meta.UserID != userID {
		t.Fatalf("metaInit() user id = %q, want fallback user id %q", meta.UserID, userID)
	}
}
