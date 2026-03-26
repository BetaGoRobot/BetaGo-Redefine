package ops

import (
	"context"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

type fakeIntentRecognizeAccessor struct {
	enabled bool
	mode    appconfig.ChatMode
}

func (f fakeIntentRecognizeAccessor) IntentRecognitionEnabled() bool { return f.enabled }

func (f fakeIntentRecognizeAccessor) ChatMode() appconfig.ChatMode { return f.mode }

func TestIntentRecognizeOperatorFetchSkipsAnalyzerForContinuation(t *testing.T) {
	calls := 0
	op := &IntentRecognizeOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig {
			return fakeIntentRecognizeAccessor{enabled: true, mode: appconfig.ChatModeStandard}
		},
		runtimeObserver: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
			return agentruntime.ShadowObservation{
				PolicyDecision: agentruntime.PolicyDecision{
					EnterRuntime:  true,
					TriggerType:   agentruntime.TriggerTypeFollowUp,
					AttachToRunID: "run_active",
					Reason:        "attach_follow_up",
				},
			}, true
		},
		analyzer: func(context.Context, string) (*intent.IntentAnalysis, error) {
			calls++
			return nil, nil
		},
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := op.Fetch(context.Background(), event, meta); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("analyzer calls = %d, want 0", calls)
	}

	analysis, ok := GetIntentAnalysisFromMeta(meta)
	if !ok {
		t.Fatal("expected intent analysis stored in meta")
	}
	if analysis.InteractionMode != intent.InteractionModeAgentic {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, intent.InteractionModeAgentic)
	}
	if !analysis.NeedReply {
		t.Fatal("NeedReply should be true for continuation")
	}
	if analysis.Reason != "attach_follow_up" {
		t.Fatalf("Reason = %q, want %q", analysis.Reason, "attach_follow_up")
	}
	if analysis.ReplyMode != intent.ReplyModeDirect {
		t.Fatalf("ReplyMode = %q, want %q", analysis.ReplyMode, intent.ReplyModeDirect)
	}
	if analysis.UserWillingness != 100 {
		t.Fatalf("UserWillingness = %d, want 100", analysis.UserWillingness)
	}
	if analysis.InterruptRisk != 0 {
		t.Fatalf("InterruptRisk = %d, want 0", analysis.InterruptRisk)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_medium {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_medium)
	}
	if mode, ok := meta.IntentInteractionMode(); !ok || mode != intent.InteractionModeAgentic {
		t.Fatalf("meta interaction mode = %q, want %q", mode, intent.InteractionModeAgentic)
	}
}

func TestIntentRecognizeOperatorFetchUsesAnalyzerForNonContinuationMessage(t *testing.T) {
	calls := 0
	op := &IntentRecognizeOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig {
			return fakeIntentRecognizeAccessor{enabled: true, mode: appconfig.ChatModeStandard}
		},
		runtimeObserver: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
			return agentruntime.ShadowObservation{
				PolicyDecision: agentruntime.PolicyDecision{
					EnterRuntime: true,
					TriggerType:  agentruntime.TriggerTypeMention,
					Reason:       "explicit_mention",
				},
			}, true
		},
		analyzer: func(context.Context, string) (*intent.IntentAnalysis, error) {
			calls++
			return &intent.IntentAnalysis{
				IntentType:      intent.IntentTypeQuestion,
				NeedReply:       true,
				ReplyConfidence: 92,
				Reason:          "模型判断复杂请求",
				SuggestAction:   intent.SuggestActionChat,
				InteractionMode: intent.InteractionModeAgentic,
				ReplyMode:       intent.ReplyModePassiveReply,
				UserWillingness: 72,
				InterruptRisk:   21,
				ReasoningEffort: responses.ReasoningEffort_high,
			}, nil
		},
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	event := testMessageEvent("p2p", "oc_chat", "ou_actor")
	if err := op.Fetch(context.Background(), event, meta); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("analyzer calls = %d, want 1", calls)
	}

	analysis, ok := GetIntentAnalysisFromMeta(meta)
	if !ok {
		t.Fatal("expected intent analysis stored in meta")
	}
	if analysis.InteractionMode != intent.InteractionModeAgentic {
		t.Fatalf("InteractionMode = %q, want %q", analysis.InteractionMode, intent.InteractionModeAgentic)
	}
	if analysis.ReplyMode != intent.ReplyModeDirect {
		t.Fatalf("ReplyMode = %q, want %q", analysis.ReplyMode, intent.ReplyModeDirect)
	}
	if analysis.UserWillingness != 100 {
		t.Fatalf("UserWillingness = %d, want 100", analysis.UserWillingness)
	}
	if analysis.InterruptRisk != 0 {
		t.Fatalf("InterruptRisk = %d, want 0", analysis.InterruptRisk)
	}
	if analysis.ReasoningEffort != responses.ReasoningEffort_high {
		t.Fatalf("ReasoningEffort = %v, want %v", analysis.ReasoningEffort, responses.ReasoningEffort_high)
	}
}

func TestIntentRecognizeOperatorFetchSeedsConfiguredModeWhenDisabled(t *testing.T) {
	op := &IntentRecognizeOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig {
			return fakeIntentRecognizeAccessor{enabled: false, mode: appconfig.ChatModeAgentic}
		},
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	event := testMessageEvent("group", "oc_chat", "ou_actor")
	if err := op.Fetch(context.Background(), event, meta); err == nil {
		t.Fatal("expected stage skip error")
	}
	if mode, ok := meta.IntentInteractionMode(); !ok || mode != intent.InteractionModeAgentic {
		t.Fatalf("meta interaction mode = %q, want %q", mode, intent.InteractionModeAgentic)
	}
}
