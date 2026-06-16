package messages

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMessageStagePolicySkipsCommandInRestrictedGroup(t *testing.T) {
	restore := stubMessagePolicyModeration("only_owner", nil)
	defer restore()

	event := testPolicyMessageEvent("group", "/help", false)
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	filter := newMessageStageFilter()

	if filter(context.Background(), &ops.RecordMsgOperator{}, event, meta) != true {
		t.Fatal("record stage should remain allowed")
	}
	if filter(context.Background(), &ops.ReactMsgOperator{}, event, meta) != true {
		t.Fatal("reaction stage should remain allowed")
	}
	if filter(context.Background(), &ops.CommandOperator{}, event, meta) != false {
		t.Fatal("command stage should be filtered in restricted group")
	}
}

func TestMessageStagePolicyAllowsCommandInOpenGroup(t *testing.T) {
	restore := stubMessagePolicyModeration("all_members", nil)
	defer restore()

	event := testPolicyMessageEvent("group", "/help", false)
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	filter := newMessageStageFilter()

	if filter(context.Background(), &ops.CommandOperator{}, event, meta) != true {
		t.Fatal("command stage should be allowed in open group")
	}
	if filter(context.Background(), &ops.ChatMsgOperator{}, event, meta) != false {
		t.Fatal("chat stage should be filtered for command messages")
	}
}

func TestMessageStagePolicySkipsAmbientRepliesInRestrictedGroupButKeepsReaction(t *testing.T) {
	restore := stubMessagePolicyModeration("moderator_list", nil)
	defer restore()

	event := testPolicyMessageEvent("group", "hello", false)
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	filter := newMessageStageFilter()

	if filter(context.Background(), &ops.ReactMsgOperator{}, event, meta) != true {
		t.Fatal("reaction stage should remain allowed")
	}
	if filter(context.Background(), &ops.RepeatMsgOperator{}, event, meta) != false {
		t.Fatal("repeat stage should be filtered in restricted group")
	}
	if filter(context.Background(), &ops.WordReplyMsgOperator{}, event, meta) != false {
		t.Fatal("word reply stage should be filtered in restricted group")
	}
	if filter(context.Background(), &ops.ChatMsgOperator{}, event, meta) != false {
		t.Fatal("chat stage should be filtered in restricted group")
	}
}

func TestMessageStagePolicyAllowsP2PReplyButSkipsAmbientChat(t *testing.T) {
	restore := stubMessagePolicyModeration("all_members", nil)
	defer restore()

	event := testPolicyMessageEvent("p2p", "hello", false)
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	filter := newMessageStageFilter()

	if filter(context.Background(), &ops.ReplyChatOperator{}, event, meta) != true {
		t.Fatal("reply chat stage should be allowed for p2p")
	}
	if filter(context.Background(), &ops.ChatMsgOperator{}, event, meta) != false {
		t.Fatal("ambient chat stage should be filtered for p2p")
	}
}

func stubMessagePolicyModeration(permission string, err error) func() {
	original := messagePolicyModerationPermission
	messagePolicyModerationPermission = func(context.Context, string) (string, error) {
		return permission, err
	}
	return func() {
		messagePolicyModerationPermission = original
	}
}

func testPolicyMessageEvent(chatType, text string, mentioned bool) *larkim.P2MessageReceiveV1 {
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_test"
	msgType := larkim.MsgTypeText
	content := `{"text":"` + text + `"}`
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:      &chatID,
				ChatType:    &chatType,
				MessageId:   &msgID,
				MessageType: &msgType,
				Content:     &content,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
	if mentioned {
		botOpenID := "ou_bot"
		name := "Bot"
		event.Event.Message.Mentions = []*larkim.MentionEvent{{
			Id:   &larkim.UserId{OpenId: &botOpenID},
			Name: &name,
		}}
	}
	return event
}
