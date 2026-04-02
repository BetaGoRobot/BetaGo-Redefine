package messages

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMetaInitPrefersOpenID(t *testing.T) {
	chatID := "oc_chat"
	chatType := "group"
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
	chatType := "group"
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
	for _, want := range expected {
		found := false
		for _, got := range stageTypes {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected stage %q in unified pipeline, got %+v", want, stageTypes)
		}
	}

	unexpected := []string{
		"*ops.AgentShadowOperator",
		"*ops.StandardCommandOperator",
		"*ops.AgenticCommandOperator",
		"*ops.AgenticReplyChatOperator",
		"*ops.AgenticChatMsgOperator",
	}
	for _, bad := range unexpected {
		for _, got := range stageTypes {
			if got == bad {
				t.Fatalf("unexpected legacy split stage %q in unified pipeline", bad)
			}
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
