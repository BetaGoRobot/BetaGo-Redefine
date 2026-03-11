package botidentity

import (
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func NormalizeOpenID(openID string) string {
	return strings.TrimSpace(openID)
}

func MessageSenderOpenID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil || event.Event.Sender.SenderId.OpenId == nil {
		return ""
	}
	return NormalizeOpenID(*event.Event.Sender.SenderId.OpenId)
}

func ReactionOpenID(event *larkim.P2MessageReactionCreatedV1) string {
	if event == nil || event.Event == nil || event.Event.UserId == nil || event.Event.UserId.OpenId == nil {
		return ""
	}
	return NormalizeOpenID(*event.Event.UserId.OpenId)
}
