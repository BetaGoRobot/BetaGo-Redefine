package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestSendMessageToolSpecGuidesMentionsSparingly(t *testing.T) {
	desc := SendMessage.ToolSpec().Desc
	for _, want := range []string{
		"<at user_id=\"open_id\">姓名</at>",
		"@姓名",
		"只有在需要某个具体成员响应",
		"普通群通知不要一上来就 @",
		"只是延续当前对话，不必为了点名而强行 @",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("desc = %q, want contain %q", desc, want)
		}
	}
}

func TestScheduledSendMessageUsesCreatePathForTopicChat(t *testing.T) {
	originalReply := sendMessageReplyText
	originalCreate := sendMessageCreateText
	t.Cleanup(func() {
		sendMessageReplyText = originalReply
		sendMessageCreateText = originalCreate
	})

	sendMessageReplyText = func(context.Context, string, string, string, bool) (*larkim.ReplyMessageResp, error) {
		t.Fatal("scheduled send_message should create a message instead of replying to the source message")
		return nil, nil
	}
	created := false
	sendMessageCreateText = func(ctx context.Context, content, msgID, chatID string) error {
		created = true
		if chatID != "oc_topic" {
			t.Fatalf("chatID = %q, want oc_topic", chatID)
		}
		if !strings.Contains(content, "提醒") {
			t.Fatalf("content = %q, want normalized reminder text", content)
		}
		return nil
	}

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId: new("om_source"),
				ChatId:    new("oc_topic"),
				ChatType:  new("topic_group"),
			},
		},
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_topic"}

	if err := ScheduledSendMessage.Handle(context.Background(), event, meta, SendMessageArgs{Content: "提醒"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !created {
		t.Fatal("expected scheduled send_message to create a message")
	}
}
