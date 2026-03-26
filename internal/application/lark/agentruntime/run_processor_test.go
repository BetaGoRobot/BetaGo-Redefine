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
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestContinuationProcessorProcessRunCompletesInitialReply(t *testing.T) {
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是首轮回复"}}), nil)

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
		initialReplyExecutor,
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
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "我先发起审批。"}}), nil)

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
		initialReplyExecutor,
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

func TestContinuationProcessorProcessRunDispatchesPendingApprovalDuringInitialStream(t *testing.T) {
	expiresAt := time.Date(2026, 3, 23, 11, 15, 0, 0, time.UTC)
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(
		&ark_dal.ModelStreamRespReasoning{
			CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:            "call_pending_1",
				FunctionName:      "send_message",
				Arguments:         `{"content":"hello"}`,
				Pending:           true,
				ApprovalType:      "capability",
				ApprovalTitle:     "审批发送消息",
				ApprovalSummary:   "将向当前群发送一条消息",
				ApprovalExpiresAt: expiresAt,
			},
		},
		&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{Reply: "我已经发起审批。"},
		},
	), nil)

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

	approvalSender := &plannerTestApprovalSender{}
	emitter := observingInitialReplyEmitter{
		onStart: func(ctx context.Context) {
			runtimecontext.RecordActiveAgenticReplyTarget(ctx, "om_root_agentic_reply", "card_root_agentic_reply")
		},
		onItem: func(item *ark_dal.ModelStreamRespReasoning) {
			if item == nil || item.CapabilityCall == nil || !item.CapabilityCall.Pending {
				return
			}
			if len(approvalSender.requests) != 1 {
				t.Fatalf("approval sender request count during stream = %d, want 1", len(approvalSender.requests))
			}
			if item.CapabilityCall.ApprovalStepID == "" || item.CapabilityCall.ApprovalToken == "" {
				t.Fatalf("pending trace missing reservation ids: %+v", item.CapabilityCall)
			}
		},
		result: agentruntime.InitialReplyEmissionResult{
			ResponseMessageID: "om_root_agentic_reply",
			ResponseCardID:    "card_root_agentic_reply",
			DeliveryMode:      agentruntime.ReplyDeliveryModeReply,
			Reply: agentruntime.CapturedInitialReply{
				ReplyText: "我已经发起审批。",
			},
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		initialReplyExecutor,
		agentruntime.WithInitialReplyEmitter(emitter),
		agentruntime.WithApprovalSender(approvalSender),
	)

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeMention,
				TriggerMessageID: "om_initial_pending_dispatch",
				InputText:        "@bot 发一条需要审批的消息",
				Now:              time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC),
			},
			Event:      testRunProcessorEvent(),
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	if len(approvalSender.requests) != 1 {
		t.Fatalf("approval sender request count = %d, want 1", len(approvalSender.requests))
	}
	req := approvalSender.requests[0]
	if req.Target.ReplyToMessageID != "om_root_agentic_reply" {
		t.Fatalf("approval reply target = %q, want %q", req.Target.ReplyToMessageID, "om_root_agentic_reply")
	}
	if !req.Target.ReplyInThread {
		t.Fatal("expected approval card to reply in thread to the root agentic card")
	}
	if req.Target.VisibleOpenID != "ou_actor" {
		t.Fatalf("approval visible open id = %q, want %q", req.Target.VisibleOpenID, "ou_actor")
	}
}

func TestContinuationProcessorProcessRunUsesReplyTargetForThreadFollowUp(t *testing.T) {
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是线程内回复"}}), nil)

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

	seen := agentruntime.InitialReplyTarget{}
	seenOK := false
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		initialReplyExecutor,
		agentruntime.WithInitialReplyEmitter(captureInitialReplyTargetEmitter{
			onEmit: func(ctx context.Context) {
				seen, seenOK = agentruntime.InitialReplyTargetFromContext(ctx)
			},
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_thread_reply",
				ResponseCardID:    "card_thread_reply",
				DeliveryMode:      agentruntime.ReplyDeliveryModeReply,
				TargetMessageID:   "om_thread_followup",
				Reply: agentruntime.CapturedInitialReply{
					ReplyText: "这是线程内回复",
				},
			},
		}),
	)

	event := testRunProcessorEvent()
	threadID := "omt_topic_1"
	msgID := "om_thread_followup"
	event.Event.Message.ThreadId = &threadID
	event.Event.Message.MessageId = &msgID

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeFollowUp,
				TriggerMessageID: "om_thread_followup",
				InputText:        "在这个话题里继续",
				Now:              time.Date(2026, 3, 19, 9, 2, 0, 0, time.UTC),
			},
			Event:      event,
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	if !seenOK {
		t.Fatal("expected initial reply target in context")
	}
	if seen.Mode != agentruntime.InitialReplyTargetModeReply {
		t.Fatalf("target mode = %q, want %q", seen.Mode, agentruntime.InitialReplyTargetModeReply)
	}
	if seen.MessageID != "om_thread_followup" {
		t.Fatalf("target message id = %q, want %q", seen.MessageID, "om_thread_followup")
	}
	if seen.CardID != "" {
		t.Fatalf("target card id = %q, want empty", seen.CardID)
	}
	if !seen.ReplyInThread {
		t.Fatal("expected reply_in_thread target")
	}
}

func TestContinuationProcessorProcessRunUsesDirectReplyTargetForTopLevelMessage(t *testing.T) {
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是首轮回复"}}), nil)

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

	seen := agentruntime.InitialReplyTarget{}
	seenOK := false
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		initialReplyExecutor,
		agentruntime.WithInitialReplyEmitter(captureInitialReplyTargetEmitter{
			onEmit: func(ctx context.Context) {
				seen, seenOK = agentruntime.InitialReplyTargetFromContext(ctx)
			},
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_direct_reply",
				ResponseCardID:    "card_direct_reply",
				DeliveryMode:      agentruntime.ReplyDeliveryModeReply,
				TargetMessageID:   "om_initial_complete",
				Reply: agentruntime.CapturedInitialReply{
					ReplyText: "这是首轮回复",
				},
			},
		}),
	)

	event := testRunProcessorEvent()
	msgID := "om_initial_complete"
	event.Event.Message.MessageId = &msgID
	event.Event.Message.ThreadId = nil
	event.Event.Message.ParentId = nil

	err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeCommandBridge,
				TriggerMessageID: "om_initial_complete",
				InputText:        "/bb 帮我总结",
				Now:              time.Date(2026, 3, 19, 9, 1, 0, 0, time.UTC),
			},
			Event:      event,
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	})
	if err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	if !seenOK {
		t.Fatal("expected initial reply target in context")
	}
	if seen.Mode != agentruntime.InitialReplyTargetModeReply {
		t.Fatalf("target mode = %q, want %q", seen.Mode, agentruntime.InitialReplyTargetModeReply)
	}
	if seen.MessageID != "om_initial_complete" {
		t.Fatalf("target message id = %q, want %q", seen.MessageID, "om_initial_complete")
	}
	if seen.ReplyInThread {
		t.Fatal("top-level direct reply should not force reply_in_thread")
	}
}

func TestContinuationProcessorProcessRunExecutesQueuedCapabilityTailSequentially(t *testing.T) {
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "我先开始处理。"}}), nil)

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
		initialReplyExecutor,
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
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是首轮回复"}}), nil)

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
		initialReplyExecutor,
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
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(
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
		initialReplyExecutor,
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
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(
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
		initialReplyExecutor,
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
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "继续处理"}}), nil)

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
		initialReplyExecutor,
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
		initialReplyExecutor,
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
	if seen.Mode != agentruntime.InitialReplyTargetModePatch {
		t.Fatalf("target mode = %q, want %q", seen.Mode, agentruntime.InitialReplyTargetModePatch)
	}
	if seen.MessageID != "om_reply_seed" {
		t.Fatalf("target message id = %q, want %q", seen.MessageID, "om_reply_seed")
	}
	if seen.CardID != "card_reply_seed" {
		t.Fatalf("target card id = %q, want %q", seen.CardID, "card_reply_seed")
	}
	if seen.ReplyInThread {
		t.Fatal("attached follow-up model reply should patch root instead of replying in thread")
	}
}

func TestContinuationProcessorInitialRunFallsBackToStartActorOpenIDForRootMention(t *testing.T) {
	initialReplyExecutor := runProcessorInitialReplyExecutorOption(seqFromRunProcessorItems(&ark_dal.ModelStreamRespReasoning{
		ContentStruct: ark_dal.ContentStruct{Reply: "这是 root 回复"},
	}), nil)

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

	var seen agentruntime.InitialReplyEmissionRequest
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		initialReplyExecutor,
		agentruntime.WithInitialReplyEmitter(captureInitialReplyRequestEmitter{
			onEmit: func(req agentruntime.InitialReplyEmissionRequest) {
				seen = req
			},
			result: agentruntime.InitialReplyEmissionResult{
				ResponseMessageID: "om_root_reply",
				ResponseCardID:    "card_root_reply",
				DeliveryMode:      agentruntime.ReplyDeliveryModeReply,
				Reply: agentruntime.CapturedInitialReply{
					ThoughtText: "先整理上下文",
					ReplyText:   "这是 root 回复",
				},
			},
		}),
	)

	chatID := "oc_chat"
	msgID := "om_runtime_missing_sender"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:    &chatID,
				MessageId: &msgID,
				ChatType:  &chatType,
			},
		},
	}

	if err := processor.ProcessRun(context.Background(), agentruntime.RunProcessorInput{
		Initial: &agentruntime.InitialRunInput{
			Start: agentruntime.StartShadowRunRequest{
				ChatID:           "oc_chat",
				ActorOpenID:      "ou_actor",
				TriggerType:      agentruntime.TriggerTypeMention,
				TriggerMessageID: "om_initial_missing_sender",
				InputText:        "@bot 帮我处理一下",
				Now:              time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
			},
			Event:      event,
			Plan:       agentruntime.ChatGenerationPlan{ModelID: "ep-test-agentic"},
			OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		},
	}); err != nil {
		t.Fatalf("ProcessRun() error = %v", err)
	}

	if seen.MentionOpenID != "ou_actor" {
		t.Fatalf("mention open id = %q, want %q", seen.MentionOpenID, "ou_actor")
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

type captureInitialReplyRequestEmitter struct {
	onEmit func(agentruntime.InitialReplyEmissionRequest)
	result agentruntime.InitialReplyEmissionResult
	err    error
}

func (e captureInitialReplyRequestEmitter) EmitInitialReply(_ context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	if e.onEmit != nil {
		e.onEmit(req)
	}
	return e.result, e.err
}

type observingInitialReplyEmitter struct {
	onStart func(context.Context)
	onItem  func(*ark_dal.ModelStreamRespReasoning)
	result  agentruntime.InitialReplyEmissionResult
	err     error
}

func (e observingInitialReplyEmitter) EmitInitialReply(ctx context.Context, req agentruntime.InitialReplyEmissionRequest) (agentruntime.InitialReplyEmissionResult, error) {
	if e.onStart != nil {
		e.onStart(ctx)
	}
	for item := range req.Stream {
		if e.onItem != nil {
			e.onItem(item)
		}
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

func runProcessorInitialReplyExecutorOption(
	result iter.Seq[*ark_dal.ModelStreamRespReasoning],
	err error,
) agentruntime.ContinuationProcessorOption {
	return agentruntime.WithInitialReplyExecutorFactory(func(input agentruntime.InitialRunInput, emitter agentruntime.InitialReplyEmitter) (agentruntime.InitialReplyExecutor, error) {
		mode := input.OutputMode
		if mode == "" {
			mode = agentruntime.InitialReplyOutputModeAgentic
		}
		return agentruntime.NewDefaultInitialReplyExecutorWithGenerator(
			mode,
			input.Event,
			input.Plan,
			emitter,
			func(context.Context, *larkim.P2MessageReceiveV1, agentruntime.ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
				return result, err
			},
		), nil
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
