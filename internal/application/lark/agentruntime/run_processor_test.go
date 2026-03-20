package agentruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestContinuationProcessorProcessRunCompletesInitialReply(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是首轮回复"}}), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	processor := agentruntime.NewContinuationProcessor(coordinator)
	processor = agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithInitialReplyEmitter(fakeInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_complete",
				ResponseCardID:    "card_reply_complete",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先看上下文",
					ReplyText:   "这是首轮回复",
				},
			},
		}),
	)
	startedAt := time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_complete",
				InputText:        "/bb 帮我总结",
				Now:              startedAt,
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_complete")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}
	if run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", run.Status, agentruntime.RunStatusCompleted)
	}
	if run.LastResponseID != "om_reply_complete" {
		t.Fatalf("last response id = %q, want %q", run.LastResponseID, "om_reply_complete")
	}
	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("step count = %d, want 3", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindPlan || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[1])
	}
	planInput := map[string]any{}
	if err := json.Unmarshal(steps[1].InputJSON, &planInput); err != nil {
		t.Fatalf("json.Unmarshal(plan input) error = %v", err)
	}
	if planInput["thought_text"] != "先看上下文" {
		t.Fatalf("plan thought_text = %#v, want %q", planInput["thought_text"], "先看上下文")
	}
	if planInput["reply_text"] != "这是首轮回复" {
		t.Fatalf("plan reply_text = %#v, want %q", planInput["reply_text"], "这是首轮回复")
	}
}

func TestContinuationProcessorProcessRunExecutesQueuedCapabilityAfterInitialReply(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "我先发起审批。"}}), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	registry := agentruntime.NewCapabilityRegistry()
	if err := registry.Register(&approvalOnlyCapability{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithInitialReplyEmitter(fakeInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_pending",
				ResponseCardID:    "card_reply_pending",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先申请审批",
					ReplyText:   "我先发起审批。",
					PendingCapability: &agentruntime.CapturedInitialPendingCapability{
						CallID:         "call_pending",
						CapabilityName: "approval_only",
						Arguments:      `{}`,
					},
				},
			},
		}),
	)
	startedAt := time.Date(2026, 3, 19, 9, 5, 0, 0, time.UTC)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_pending",
				InputText:        "/bb 帮我执行危险操作",
				Now:              startedAt,
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_pending")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}
	if run.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status = %q, want %q", run.Status, agentruntime.RunStatusWaitingApproval)
	}
	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) < 2 || steps[1].Kind != agentruntime.StepKindPlan {
		t.Fatalf("expected plan step before approval path, got %+v", steps)
	}
	planInput := map[string]any{}
	if err := json.Unmarshal(steps[1].InputJSON, &planInput); err != nil {
		t.Fatalf("json.Unmarshal(plan input) error = %v", err)
	}
	pending, ok := planInput["pending_capability"].(map[string]any)
	if !ok {
		t.Fatalf("plan pending capability = %#v, want object", planInput["pending_capability"])
	}
	if pending["capability_name"] != "approval_only" {
		t.Fatalf("plan pending capability_name = %#v, want %q", pending["capability_name"], "approval_only")
	}
	last := steps[len(steps)-1]
	if last.Kind != agentruntime.StepKindApprovalRequest {
		t.Fatalf("last step kind = %q, want %q", last.Kind, agentruntime.StepKindApprovalRequest)
	}
}

func TestContinuationProcessorProcessRunExecutesQueuedCapabilityTailSequentially(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "我先开始处理。"}}), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText: "echo:done",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:       "om_initial_queue",
			CardID:          "card_initial_queue",
			DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
			TargetMessageID: "om_initial_queue",
			TargetCardID:    "card_initial_queue",
		},
	}
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithInitialReplyEmitter(fakeInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_initial_queue",
				ResponseCardID:    "card_initial_queue",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先排两个能力调用",
					ReplyText:   "我先开始处理。",
					PendingCapability: &agentruntime.CapturedInitialPendingCapability{
						CallID:         "call_queue_1",
						CapabilityName: "echo_cap",
						Arguments:      `{"text":"one"}`,
						QueueTail: []agentruntime.CapturedInitialPendingCapability{
							{
								CallID:         "call_queue_2",
								CapabilityName: "echo_cap",
								Arguments:      `{"text":"two"}`,
							},
						},
					},
				},
			},
		}),
	)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_queue_chain",
				InputText:        "/bb 串行执行两个能力",
				Now:              time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_queue_chain")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}
	if run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", run.Status, agentruntime.RunStatusCompleted)
	}
	if capability.executeCount != 2 {
		t.Fatalf("capability execute count = %d, want 2", capability.executeCount)
	}
	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 9 {
		t.Fatalf("step count = %d, want 9", len(steps))
	}
	if steps[3].Kind != agentruntime.StepKindCapabilityCall || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected first queued capability step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected first observe step: %+v", steps[4])
	}
	if steps[5].Kind != agentruntime.StepKindCapabilityCall || steps[5].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected second queued capability step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected second observe step: %+v", steps[6])
	}
	if steps[7].Kind != agentruntime.StepKindPlan || steps[8].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected final plan/reply tail: %+v", steps)
	}
	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
}

func TestContinuationProcessorProcessRunPersistsCompletedCapabilityPreviousResponseID(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是首轮回复"}}), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithInitialReplyEmitter(fakeInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_with_tool",
				ResponseCardID:    "card_reply_with_tool",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先查历史再回答",
					ReplyText:   "这是带工具结果的首轮回复",
					CapabilityCalls: []agentruntime.CompletedCapabilityCall{
						{
							CallID:             "call_history_1",
							CapabilityName:     "search_history",
							Arguments:          `{"q":"日报"}`,
							Output:             "找到日报记录",
							PreviousResponseID: "resp_tool_1",
						},
					},
				},
			},
		}),
	)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_complete_with_tool",
				InputText:        "/bb 帮我总结",
				Now:              time.Date(2026, 3, 20, 13, 30, 0, 0, time.UTC),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_complete_with_tool")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) < 2 {
		t.Fatalf("step count = %d, want at least 2", len(steps))
	}
	var capabilityStep *agentruntime.AgentStep
	for _, step := range steps {
		if step != nil && step.Kind == agentruntime.StepKindCapabilityCall {
			capabilityStep = step
			break
		}
	}
	if capabilityStep == nil {
		t.Fatalf("expected capability step, got %+v", steps)
	}

	input := struct {
		Continuation *struct {
			PreviousResponseID string `json:"previous_response_id"`
		} `json:"continuation"`
	}{}
	if err := json.Unmarshal(capabilityStep.InputJSON, &input); err != nil {
		t.Fatalf("json.Unmarshal(capability input) error = %v", err)
	}
	if input.Continuation == nil || input.Continuation.PreviousResponseID != "resp_tool_1" {
		t.Fatalf("capability continuation = %+v, want previous_response_id resp_tool_1", input.Continuation)
	}
}

func TestContinuationProcessorProcessRunDurablyRecordsInitialCapabilityTraceBeforeReply(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(
			&ark_dal.ModelStreamRespReasoning{
				CapabilityCall: &ark_dal.CapabilityCallTrace{
					CallID:             "call_history_turn_1",
					FunctionName:       "search_history",
					Arguments:          `{"q":"日报"}`,
					Output:             "找到日报记录",
					PreviousResponseID: "resp_turn_1",
				},
			},
			&ark_dal.ModelStreamRespReasoning{
				ContentStruct: ark_dal.ContentStruct{
					Reply: "这是带工具结果的首轮回复",
				},
			},
		), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithInitialReplyEmitter(drainingInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_trace",
				ResponseCardID:    "card_reply_trace",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
			},
		}),
	)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_trace_record",
				InputText:        "/bb 帮我总结",
				Now:              time.Date(2026, 3, 20, 14, 0, 0, 0, time.UTC),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_trace_record")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("step count = %d, want 5", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected capability step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindObserve || steps[2].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected observe step: %+v", steps[2])
	}
	if steps[3].Kind != agentruntime.StepKindPlan || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindReply || steps[4].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[4])
	}

	input := struct {
		Continuation *struct {
			PreviousResponseID string `json:"previous_response_id"`
		} `json:"continuation"`
	}{}
	if err := json.Unmarshal(steps[1].InputJSON, &input); err != nil {
		t.Fatalf("json.Unmarshal(capability input) error = %v", err)
	}
	if input.Continuation == nil || input.Continuation.PreviousResponseID != "resp_turn_1" {
		t.Fatalf("capability continuation = %+v, want previous_response_id resp_turn_1", input.Continuation)
	}
}

func TestContinuationProcessorProcessRunAdvancesRunCursorWhenInitialTraceRecordedBeforeFailure(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(
			&ark_dal.ModelStreamRespReasoning{
				CapabilityCall: &ark_dal.CapabilityCallTrace{
					CallID:             "call_history_turn_1",
					FunctionName:       "search_history",
					Arguments:          `{"q":"日报"}`,
					Output:             "找到日报记录",
					PreviousResponseID: "resp_turn_1",
				},
			},
		), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithInitialReplyEmitter(failingAfterDrainInitialReplyEmitter{
			err: errors.New("stream interrupted"),
		}),
	)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_trace_failure",
				InputText:        "/bb 帮我总结",
				Now:              time.Date(2026, 3, 20, 14, 5, 0, 0, time.UTC),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err == nil {
		t.Fatal("expected ProcessRun() error")
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_trace_failure")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted run")
	}
	if run.CurrentStepIndex != 2 {
		t.Fatalf("current step index = %d, want 2 after trace record", run.CurrentStepIndex)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("step count = %d, want 3", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall {
		t.Fatalf("capability step kind = %q, want %q", steps[1].Kind, agentruntime.StepKindCapabilityCall)
	}
	if steps[2].Kind != agentruntime.StepKindObserve {
		t.Fatalf("observe step kind = %q, want %q", steps[2].Kind, agentruntime.StepKindObserve)
	}
}

func TestContinuationProcessorProcessRunAttachCarriesActiveReplyTargetIntoInitialExecutor(t *testing.T) {
	setRunProcessorAgenticInitialReplyStream(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "继续处理"}}), nil)
	defer agentruntime.SetAgenticInitialReplyStreamGenerator(nil)

	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		identity,
	)
	registry := agentruntime.NewCapabilityRegistry()
	if err := registry.Register(&approvalOnlyCapability{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithInitialReplyEmitter(fakeInitialReplyEmitter{
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_seed",
				ResponseCardID:    "card_reply_seed",
				DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先看上下文",
					ReplyText:   "这是第一轮回复",
					PendingCapability: &agentruntime.CapturedInitialPendingCapability{
						CallID:         "call_seed_pending",
						CapabilityName: "approval_only",
						Arguments:      `{}`,
					},
				},
			},
		}),
	)
	startedAt := time.Date(2026, 3, 19, 9, 10, 0, 0, time.UTC)

	if err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_attach_seed",
				InputText:        "/bb 先发起审批",
				Now:              startedAt,
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	}); err != nil {
		t.Fatalf("seed ProcessRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).FindOrCreateChatSession(context.Background(), identity.AppID, identity.BotOpenID, "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run, err := agentstore.NewRunRepository(db).FindByTriggerMessage(context.Background(), session.ID, "om_initial_attach_seed")
	if err != nil {
		t.Fatalf("FindByTriggerMessage() error = %v", err)
	}
	if run == nil {
		t.Fatal("expected persisted seed run")
	}
	if run.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("seed run status = %q, want %q", run.Status, agentruntime.RunStatusWaitingApproval)
	}

	seen := agentruntime.InitialReplyTarget{}
	seenOK := false
	processor = agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithInitialReplyEmitter(captureInitialReplyTargetEmitter{
			onEmit: func(ctx context.Context) {
				seen, seenOK = agentruntime.InitialReplyTargetFromContext(ctx)
			},
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_reply_seed",
				ResponseCardID:    "card_reply_seed",
				DeliveryMode:      agentruntime.ReplyDeliveryModePatch,
				TargetMessageID:   "om_reply_seed",
				TargetCardID:      "card_reply_seed",
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "继续处理",
					ReplyText:   "这是更新后的回复",
				},
			},
		}),
	)
	if err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeFollowUp,
				TriggerMessageID: "om_initial_attach_followup",
				AttachToRunID:    run.ID,
				InputText:        "继续",
				Now:              startedAt.Add(time.Minute),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	}); err != nil {
		t.Fatalf("attach ProcessRun() error = %v", err)
	}

	if !seenOK {
		t.Fatal("expected initial reply target in context")
	}
	if seen.MessageID != "om_reply_seed" {
		t.Fatalf("target message id = %q, want %q", seen.MessageID, "om_reply_seed")
	}
	if seen.CardID != "card_reply_seed" {
		t.Fatalf("target card id = %q, want %q", seen.CardID, "card_reply_seed")
	}
}

type approvalOnlyCapability struct{}

type fakeInitialReplyEmitter struct {
	result agentruntime.InitialReplyEmissionResult
	err    error
}

func (e fakeInitialReplyEmitter) EmitInitialReply(context.Context, agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	return e.result, e.err
}

type drainingInitialReplyEmitter struct {
	result agentruntime.InitialReplyEmissionResult
	err    error
}

func (e drainingInitialReplyEmitter) EmitInitialReply(_ context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	result := e.result
	for item := range req.Stream {
		if item == nil {
			continue
		}
		if reply := item.ContentStruct.Reply; reply != "" {
			result.Reply.ReplyText = reply
		}
		if thought := item.ContentStruct.Thought; thought != "" {
			result.Reply.ThoughtText = thought
		}
	}
	return result, e.err
}

type failingAfterDrainInitialReplyEmitter struct {
	err error
}

func (e failingAfterDrainInitialReplyEmitter) EmitInitialReply(_ context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	for range req.Stream {
	}
	return agentruntime.InitialReplyEmissionResult{}, e.err
}

type captureInitialReplyTargetEmitter struct {
	onEmit func(context.Context)
	result agentruntime.InitialReplyEmissionResult
	err    error
}

func (e captureInitialReplyTargetEmitter) EmitInitialReply(ctx context.Context, _ agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	if e.onEmit != nil {
		e.onEmit(ctx)
	}
	return e.result, e.err
}

func seqFromRunProcessorItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

func setRunProcessorAgenticInitialReplyStream(result iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	agentruntime.SetAgenticInitialReplyStreamGenerator(func(context.Context, *larkim.P2MessageReceiveV1, agentruntime.ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		return result, err
	})
}

func testRunProcessorEvent() *larkim.P2MessageReceiveV1 {
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime"
	chatType := "group"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:    &chatID,
				MessageId: &msgID,
				ChatType:  &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}

func (c *approvalOnlyCapability) Meta() agentruntime.CapabilityMeta {
	return agentruntime.CapabilityMeta{
		Name:             "approval_only",
		Kind:             agentruntime.CapabilityKindTool,
		Description:      "needs approval",
		SideEffectLevel:  agentruntime.SideEffectLevelChatWrite,
		RequiresApproval: true,
		AllowedScopes:    []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
		DefaultTimeout:   time.Second,
	}
}

func (c *approvalOnlyCapability) Execute(context.Context, agentruntime.CapabilityRequest) (agentruntime.CapabilityResult, error) {
	return agentruntime.CapabilityResult{OutputText: "should not run before approval"}, nil
}
