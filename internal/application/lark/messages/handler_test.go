package messages

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"unsafe"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestResolveChatNameUsesUserNameForP2P(t *testing.T) {
	oldGetUserNameByID := getUserNameByID
	t.Cleanup(func() {
		getUserNameByID = oldGetUserNameByID
	})
	getUserNameByID = func(ctx context.Context, chatID, openID string) (string, error) {
		if chatID != "oc_chat" || openID != "ou_open" {
			t.Fatalf("getUserNameByID(%q, %q), want oc_chat/ou_open", chatID, openID)
		}
		return "Alice", nil
	}

	if got := resolveChatName(context.Background(), "oc_chat", true, "ou_open"); got != "[单聊]Alice" {
		t.Fatalf("resolveChatName() = %q, want %q", got, "[单聊]Alice")
	}
}

func TestMetaInitPrefersOpenID(t *testing.T) {
	chatID := "oc_chat"
	chatType := "p2p"
	openID := "ou_open"
	legacyUserID := "cli_user"

	meta := metaInit(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:   &chatID,
				ChatType: &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
					UserId: &legacyUserID,
				},
			},
		},
	})

	if meta.OpenID != openID {
		t.Fatalf("metaInit() open id = %q, want %q", meta.OpenID, openID)
	}
}

func TestMetaInitReturnsEmptyWithoutOpenID(t *testing.T) {
	chatID := "oc_chat"
	chatType := "p2p"
	legacyUserID := "cli_user"

	meta := metaInit(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:   &chatID,
				ChatType: &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					UserId: &legacyUserID,
				},
			},
		},
	})

	if meta.OpenID != "" {
		t.Fatalf("metaInit() open id = %q, want empty string when only legacy user id %q exists", meta.OpenID, legacyUserID)
	}
}

func TestNewMessageProcessorBuildsUnifiedPipeline(t *testing.T) {
	handler := NewMessageProcessor(config.NewManager())
	if handler == nil {
		t.Fatal("expected message handler")
	}

	field := reflect.ValueOf(handler).Elem().FieldByName("processor")
	if !field.IsValid() || field.IsNil() {
		t.Fatal("expected unified processor field")
	}
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()

	processor := field.Interface().(*xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData])
	stageTypes := asyncStageTypes(processor)

	expected := []string{
		"*ops.RecordMsgOperator",
		"*ops.RepeatMsgOperator",
		"*ops.ReactMsgOperator",
		"*ops.WordReplyMsgOperator",
		"*ops.ReplyChatOperator",
		"*ops.CommandOperator",
		"*ops.ChatMsgOperator",
	}
	if len(stageTypes) != len(expected) {
		t.Fatalf("unified pipeline stage count = %d, want %d; stages=%+v", len(stageTypes), len(expected), stageTypes)
	}
	for _, want := range expected {
		found := slices.Contains(stageTypes, want)
		if !found {
			t.Fatalf("expected stage %q in unified pipeline, got %+v", want, stageTypes)
		}
	}
}

func asyncStageTypes(processor *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) []string {
	if processor == nil {
		return nil
	}
	field := reflect.ValueOf(processor).Elem().FieldByName("asyncStages")
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	types := make([]string, 0, field.Len())
	for i := 0; i < field.Len(); i++ {
		types = append(types, fmt.Sprintf("%T", field.Index(i).Interface()))
	}
	return types
}
