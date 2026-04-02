package ops

import (
	"context"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type fakeIntentRecognizeAccessor struct {
	enabled bool
	mode    appconfig.ChatMode
}

func (f fakeIntentRecognizeAccessor) IntentRecognitionEnabled() bool { return f.enabled }
func (f fakeIntentRecognizeAccessor) ChatMode() appconfig.ChatMode   { return f.mode }

func TestIntentRecognizeOperatorRunStoresAnalysis(t *testing.T) {
	op := &IntentRecognizeOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig {
			return fakeIntentRecognizeAccessor{enabled: true, mode: appconfig.ChatModeStandard}
		},
		recentContextLoader: func(context.Context, *larkim.P2MessageReceiveV1, int) ([]string, error) {
			return []string{
				"[2026-04-02 10:00:01](ou_a) <甲>: 先按旧方案拆接口",
				"[2026-04-02 10:00:05](ou_b) <乙>: 那降级策略也补一下",
				"[2026-04-02 10:00:09](ou_c) <丙>: 指标要单独看",
			}, nil
		},
		analyzer: func(_ context.Context, text string, recent []string) (*intent.IntentAnalysis, error) {
			if text != "hello" {
				t.Fatalf("text = %q, want %q", text, "hello")
			}
			if len(recent) != 3 {
				t.Fatalf("recent len = %d, want 3", len(recent))
			}
			return &intent.IntentAnalysis{
				IntentType:      intent.IntentTypeQuestion,
				NeedReply:       true,
				ReplyConfidence: 88,
				Reason:          "ask",
				SuggestAction:   intent.SuggestActionChat,
			}, nil
		},
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	analysis, ok := GetIntentAnalysisFromMeta(meta)
	if !ok {
		t.Fatal("expected intent analysis stored in meta")
	}
	if analysis.IntentType != intent.IntentTypeQuestion {
		t.Fatalf("IntentType = %q, want %q", analysis.IntentType, intent.IntentTypeQuestion)
	}
}

func TestIntentRecognizeOperatorRunSkipsWhenDisabled(t *testing.T) {
	op := &IntentRecognizeOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig {
			return fakeIntentRecognizeAccessor{enabled: false, mode: appconfig.ChatModeStandard}
		},
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := op.Run(context.Background(), event, meta); err == nil {
		t.Fatal("expected stage skip error")
	}
}

func testMessageEvent(chatType, chatID, openID string) *larkim.P2MessageReceiveV1 {
	text := `{"text":"hello"}`
	msgID := "om_test"
	msgType := larkim.MsgTypeText
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatType:    &chatType,
				ChatId:      &chatID,
				MessageId:   &msgID,
				MessageType: &msgType,
				Content:     &text,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}
