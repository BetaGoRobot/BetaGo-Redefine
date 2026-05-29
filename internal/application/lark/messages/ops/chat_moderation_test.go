package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestSkipIfChatModeratedSkipsRestrictedGroup(t *testing.T) {
	restore := stubChatModerationPermission("only_owner", nil)
	defer restore()

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	err := skipIfChatModerated(context.Background(), "testOp", event, &xhandler.BaseMetaData{})
	if !errors.Is(err, xerror.ErrStageSkip) {
		t.Fatalf("skipIfChatModerated() error = %v, want stage skip", err)
	}
}

func TestSkipIfChatModeratedAllowsAllMembers(t *testing.T) {
	restore := stubChatModerationPermission("all_members", nil)
	defer restore()

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := skipIfChatModerated(context.Background(), "testOp", event, &xhandler.BaseMetaData{}); err != nil {
		t.Fatalf("skipIfChatModerated() error = %v, want nil", err)
	}
}

func TestSkipIfChatModeratedAllowsP2PWithoutLookup(t *testing.T) {
	restore := stubChatModerationPermission("only_owner", errors.New("unexpected lookup"))
	defer restore()

	event := testMessageEvent("p2p", "oc_chat", "ou_actor")
	if err := skipIfChatModerated(context.Background(), "testOp", event, &xhandler.BaseMetaData{}); err != nil {
		t.Fatalf("skipIfChatModerated() error = %v, want nil", err)
	}
}

func TestSkipIfChatModeratedAllowsWhenLookupFails(t *testing.T) {
	restore := stubChatModerationPermission("", errors.New("lark unavailable"))
	defer restore()

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := skipIfChatModerated(context.Background(), "testOp", event, &xhandler.BaseMetaData{}); err != nil {
		t.Fatalf("skipIfChatModerated() error = %v, want nil", err)
	}
}

func TestCommandOperatorPreRunSkipsRestrictedGroup(t *testing.T) {
	restore := stubChatModerationPermission("moderator_list", nil)
	defer restore()

	event := testCommandMessageEvent("group", "oc_chat", "ou_actor")
	err := (&CommandOperator{}).PreRun(context.Background(), event, &xhandler.BaseMetaData{})
	if !errors.Is(err, xerror.ErrStageSkip) {
		t.Fatalf("CommandOperator.PreRun() error = %v, want stage skip", err)
	}
}

func stubChatModerationPermission(permission string, err error) func() {
	original := getChatModerationPermission
	getChatModerationPermission = func(context.Context, string) (string, error) {
		return permission, err
	}
	return func() {
		getChatModerationPermission = original
	}
}

func testCommandMessageEvent(chatType, chatID, openID string) *larkim.P2MessageReceiveV1 {
	event := testMessageEvent(chatType, chatID, openID)
	content := `{"text":"/help"}`
	event.Event.Message.Content = &content
	return event
}
