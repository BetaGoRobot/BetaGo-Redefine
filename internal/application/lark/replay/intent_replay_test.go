package replay

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestIntentReplayLoadTargetNormalizesMessageFields(t *testing.T) {
	service := IntentReplayService{
		loadMessage: func(_ context.Context, gotMessageID string) (*larkim.Message, error) {
			if gotMessageID != "om_target" {
				t.Fatalf("loadMessage() got message id %q, want %q", gotMessageID, "om_target")
			}
			return &larkim.Message{
				MessageId: strPtr("om_target"),
				ChatId:    strPtr("oc_chat"),
				MsgType:   strPtr("text"),
				Body: &larkim.MessageBody{
					Content: strPtr(`{"text":"帮我总结今天讨论"}`),
				},
				Sender: &larkim.Sender{
					Id:         strPtr("ou_actor"),
					IdType:     strPtr("open_id"),
					SenderType: strPtr("user"),
				},
			}, nil
		},
	}

	target, err := service.LoadTarget(context.Background(), "oc_chat", "om_target")
	if err != nil {
		t.Fatalf("LoadTarget() error = %v", err)
	}
	if target.ChatID != "oc_chat" {
		t.Fatalf("target.ChatID = %q, want %q", target.ChatID, "oc_chat")
	}
	if target.MessageID != "om_target" {
		t.Fatalf("target.MessageID = %q, want %q", target.MessageID, "om_target")
	}
	if target.OpenID != "ou_actor" {
		t.Fatalf("target.OpenID = %q, want %q", target.OpenID, "ou_actor")
	}
	if target.ChatType != "group" {
		t.Fatalf("target.ChatType = %q, want %q", target.ChatType, "group")
	}
	if target.Text != "帮我总结今天讨论" {
		t.Fatalf("target.Text = %q, want %q", target.Text, "帮我总结今天讨论")
	}
}

func TestIntentReplayLoadTargetReturnsExplicitNotFound(t *testing.T) {
	service := IntentReplayService{
		loadMessage: func(context.Context, string) (*larkim.Message, error) {
			return nil, ErrReplayTargetNotFound
		},
	}

	_, err := service.LoadTarget(context.Background(), "oc_chat", "om_missing")
	if !errors.Is(err, ErrReplayTargetNotFound) {
		t.Fatalf("LoadTarget() error = %v, want ErrReplayTargetNotFound", err)
	}
}

func TestIntentReplayLoadTargetRejectsChatMismatch(t *testing.T) {
	service := IntentReplayService{
		loadMessage: func(context.Context, string) (*larkim.Message, error) {
			return &larkim.Message{
				MessageId: strPtr("om_target"),
				ChatId:    strPtr("oc_other"),
				MsgType:   strPtr("text"),
				Body:      &larkim.MessageBody{Content: strPtr(`{"text":"帮我总结今天讨论"}`)},
			}, nil
		},
	}

	_, err := service.LoadTarget(context.Background(), "oc_chat", "om_target")
	if !errors.Is(err, ErrReplayTargetChatMismatch) {
		t.Fatalf("LoadTarget() error = %v, want ErrReplayTargetChatMismatch", err)
	}
}

func TestIntentReplayBuildCasesBuildsBaselineAndAugmented(t *testing.T) {
	var calls []replayIntentInputOptions
	service := IntentReplayService{
		buildIntentInput: func(_ context.Context, _ *larkim.P2MessageReceiveV1, _ *xhandler.BaseMetaData, currentText string, options replayIntentInputOptions) replayIntentInputPreview {
			calls = append(calls, options)
			if len(calls) == 1 {
				return replayIntentInputPreview{
					Input:          currentText,
					ContextEnabled: false,
				}
			}
			return replayIntentInputPreview{
				Input:          currentText + "\n\n最近上下文(新到旧):\n[10:00] <Alice>: 先看上周数据",
				ContextEnabled: true,
				HistoryLimit:   4,
				ProfileLimit:   2,
				HistoryLines:   []string{"[10:00] <Alice>: 先看上周数据"},
				ProfileLines:   []string{"画像线索: role=pm"},
			}
		},
	}

	loaded := loadedReplayTarget{
		Target: ReplayTarget{
			ChatID:   "oc_chat",
			OpenID:   "ou_actor",
			ChatType: "group",
			Text:     "帮我总结今天讨论",
		},
		Event: &larkim.P2MessageReceiveV1{},
	}

	cases, err := service.buildCases(context.Background(), loaded, ReplayBuildOptions{})
	if err != nil {
		t.Fatalf("buildCases() error = %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("len(cases) = %d, want 2", len(cases))
	}
	if cases[0].Name != ReplayCaseBaseline || cases[0].IntentContextEnabled {
		t.Fatalf("baseline case = %+v", cases[0])
	}
	if cases[1].Name != ReplayCaseAugmented || !cases[1].IntentContextEnabled {
		t.Fatalf("augmented case = %+v", cases[1])
	}
	if cases[1].HistoryLimit != 4 || cases[1].ProfileLimit != 2 {
		t.Fatalf("augmented limits = %d/%d, want 4/2", cases[1].HistoryLimit, cases[1].ProfileLimit)
	}
	if len(calls) != 2 {
		t.Fatalf("builder call count = %d, want 2", len(calls))
	}
	if calls[0].ContextEnabled == nil || *calls[0].ContextEnabled {
		t.Fatalf("baseline context override = %+v, want false", calls[0])
	}
	if calls[0].HistoryLimit == nil || *calls[0].HistoryLimit != 0 {
		t.Fatalf("baseline history override = %+v, want 0", calls[0])
	}
	if calls[0].ProfileLimit == nil || *calls[0].ProfileLimit != 0 {
		t.Fatalf("baseline profile override = %+v, want 0", calls[0])
	}
	if calls[1].ContextEnabled == nil || !*calls[1].ContextEnabled {
		t.Fatalf("augmented context override = %+v, want true", calls[1])
	}
}

func TestIntentReplayBuildCasesSupportsIndependentHistoryAndProfileOverrides(t *testing.T) {
	var augmentedCall replayIntentInputOptions
	service := IntentReplayService{
		buildIntentInput: func(_ context.Context, _ *larkim.P2MessageReceiveV1, _ *xhandler.BaseMetaData, currentText string, options replayIntentInputOptions) replayIntentInputPreview {
			if options.ContextEnabled != nil && *options.ContextEnabled {
				augmentedCall = options
			}
			return replayIntentInputPreview{
				Input:          currentText,
				ContextEnabled: options.ContextEnabled != nil && *options.ContextEnabled,
				HistoryLimit:   derefInt(options.HistoryLimit),
				ProfileLimit:   derefInt(options.ProfileLimit),
			}
		},
	}

	loaded := loadedReplayTarget{
		Target: ReplayTarget{
			ChatID: "oc_chat",
			OpenID: "ou_actor",
			Text:   "继续推进",
		},
		Event: &larkim.P2MessageReceiveV1{},
	}

	profileLimit := 3
	cases, err := service.buildCases(context.Background(), loaded, ReplayBuildOptions{
		DisableHistory: true,
		ProfileLimit:   &profileLimit,
	})
	if err != nil {
		t.Fatalf("buildCases() error = %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("len(cases) = %d, want 2", len(cases))
	}
	if augmentedCall.HistoryLimit == nil || *augmentedCall.HistoryLimit != 0 {
		t.Fatalf("augmented history override = %+v, want 0", augmentedCall)
	}
	if augmentedCall.ProfileLimit == nil || *augmentedCall.ProfileLimit != 3 {
		t.Fatalf("augmented profile override = %+v, want 3", augmentedCall)
	}
	if cases[1].HistoryLimit != 0 || cases[1].ProfileLimit != 3 {
		t.Fatalf("augmented case limits = %d/%d, want 0/3", cases[1].HistoryLimit, cases[1].ProfileLimit)
	}
}

func TestIntentReplayReplayDryRunLeavesIntentAnalysisEmpty(t *testing.T) {
	service := IntentReplayService{
		loadMessage: func(context.Context, string) (*larkim.Message, error) {
			return &larkim.Message{
				MessageId: strPtr("om_target"),
				ChatId:    strPtr("oc_chat"),
				MsgType:   strPtr("text"),
				Body:      &larkim.MessageBody{Content: strPtr(`{"text":"帮我总结今天讨论"}`)},
				Sender:    &larkim.Sender{Id: strPtr("ou_actor")},
			}, nil
		},
		buildIntentInput: func(_ context.Context, _ *larkim.P2MessageReceiveV1, _ *xhandler.BaseMetaData, currentText string, options replayIntentInputOptions) replayIntentInputPreview {
			if options.ContextEnabled != nil && *options.ContextEnabled {
				return replayIntentInputPreview{
					Input:          currentText + "\n\n最近上下文(新到旧):\n[10:00] <Alice>: 先看上周数据",
					ContextEnabled: true,
					HistoryLimit:   4,
				}
			}
			return replayIntentInputPreview{
				Input:          currentText,
				ContextEnabled: false,
			}
		},
	}

	report, err := service.Replay(context.Background(), "oc_chat", "om_target", ReplayRunOptions{})
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(report.Cases) != 2 {
		t.Fatalf("len(report.Cases) = %d, want 2", len(report.Cases))
	}
	if report.Cases[0].IntentAnalysis != nil || report.Cases[1].IntentAnalysis != nil {
		t.Fatalf("dry-run cases should not include intent analysis: %+v", report.Cases)
	}
	if !report.Diff.IntentInputChanged {
		t.Fatalf("report.Diff = %+v, want intent input diff", report.Diff)
	}
}

func TestIntentReplayReplayLiveModelPopulatesAnalysesAndDiff(t *testing.T) {
	var analyzedInputs []string
	service := IntentReplayService{
		loadMessage: func(context.Context, string) (*larkim.Message, error) {
			return &larkim.Message{
				MessageId: strPtr("om_target"),
				ChatId:    strPtr("oc_chat"),
				MsgType:   strPtr("text"),
				Body:      &larkim.MessageBody{Content: strPtr(`{"text":"帮我总结今天讨论"}`)},
				Sender:    &larkim.Sender{Id: strPtr("ou_actor")},
			}, nil
		},
		buildIntentInput: func(_ context.Context, _ *larkim.P2MessageReceiveV1, _ *xhandler.BaseMetaData, currentText string, options replayIntentInputOptions) replayIntentInputPreview {
			if options.ContextEnabled != nil && *options.ContextEnabled {
				return replayIntentInputPreview{
					Input:          currentText + "\n\n用户画像线索:\n画像线索: role=pm",
					ContextEnabled: true,
					ProfileLimit:   1,
					ProfileLines:   []string{"画像线索: role=pm"},
				}
			}
			return replayIntentInputPreview{
				Input:          currentText,
				ContextEnabled: false,
			}
		},
		analyzeIntent: func(_ context.Context, input string) (*intent.IntentAnalysis, error) {
			analyzedInputs = append(analyzedInputs, input)
			if strings.Contains(input, "画像线索") {
				return &intent.IntentAnalysis{
					IntentType:      intent.IntentTypeQuestion,
					NeedReply:       true,
					InteractionMode: intent.InteractionModeAgentic,
					NeedsHistory:    true,
					NeedsWeb:        false,
				}, nil
			}
			return &intent.IntentAnalysis{
				IntentType:      intent.IntentTypeQuestion,
				NeedReply:       true,
				InteractionMode: intent.InteractionModeStandard,
				NeedsHistory:    false,
				NeedsWeb:        false,
			}, nil
		},
		standardPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{
				ChatID:    "oc_chat",
				OpenID:    "ou_actor",
				Prompt:    "standard prompt",
				UserInput: "标准输入",
			}, nil
		},
		agenticPlanBuilder: func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
			return chatflow.InitialChatExecutionPlan{
				ChatID:       "oc_chat",
				OpenID:       "ou_actor",
				Prompt:       "agentic prompt",
				UserInput:    "增强输入",
				MaxToolTurns: 6,
			}, nil
		},
		executeTurn: func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error) {
			return chatflow.InitialChatTurnResult{
				Stream: func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
					yield(&ark_dal.ModelStreamRespReasoning{Content: `{"decision":"reply","reply":"ok"}`})
				},
			}, nil
		},
	}

	report, err := service.Replay(context.Background(), "oc_chat", "om_target", ReplayRunOptions{LiveModel: true})
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(analyzedInputs) != 2 {
		t.Fatalf("analyze call count = %d, want 2", len(analyzedInputs))
	}
	if report.Cases[0].IntentAnalysis == nil || report.Cases[1].IntentAnalysis == nil {
		t.Fatalf("live replay should populate intent analyses: %+v", report.Cases)
	}
	if report.Cases[0].RouteDecision == nil || report.Cases[1].RouteDecision == nil {
		t.Fatalf("live replay should populate route decisions: %+v", report.Cases)
	}
	if !report.Diff.InteractionModeChanged {
		t.Fatalf("report.Diff = %+v, want interaction mode changed", report.Diff)
	}
	if !report.Diff.RouteChanged {
		t.Fatalf("report.Diff = %+v, want route changed", report.Diff)
	}
	changed := strings.Join(report.Diff.ChangedFieldNames(), ",")
	for _, want := range []string{"intent_analysis.interaction_mode", "route_decision.final_mode"} {
		if !strings.Contains(changed, want) {
			t.Fatalf("changed fields = %q, want contain %q", changed, want)
		}
	}
}

func TestIntentReplayBuildRuntimeObservationDetectsCommandBridge(t *testing.T) {
	service := IntentReplayService{}
	loaded := loadedReplayTarget{
		Target: ReplayTarget{
			ChatID:   "oc_chat",
			OpenID:   "ou_actor",
			ChatType: "group",
			Text:     "/bb 帮我总结今天讨论",
		},
		Event: &larkim.P2MessageReceiveV1{},
	}

	observation := service.buildRuntimeObservation(context.Background(), loaded)
	if observation.TriggerType != "command_bridge" {
		t.Fatalf("TriggerType = %q, want %q", observation.TriggerType, "command_bridge")
	}
	if !observation.EligibleForAgentic {
		t.Fatalf("EligibleForAgentic = false, want true")
	}
}

func strPtr[T any](value T) *T {
	return &value
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
