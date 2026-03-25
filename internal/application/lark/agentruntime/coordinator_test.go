package agentruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initial"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/testsupport/pgtest"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type observingPendingInitialRunProcessor struct {
	mu      sync.Mutex
	results []error
	handled chan agentruntime.RunProcessorInput
	roots   chan runtimecontext.AgenticReplyTarget
}

func (p *observingPendingInitialRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	if p.handled != nil {
		p.handled <- input
	}
	if p.roots != nil {
		root, _ := runtimecontext.RootAgenticReplyTarget(ctx)
		p.roots <- root
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.results) == 0 {
		return nil
	}
	result := p.results[0]
	p.results = p.results[1:]
	return result
}

func mustMarshalPendingInitialRun(t *testing.T, item initialcore.PendingRun) []byte {
	t.Helper()
	raw, err := initialcore.MarshalPendingRun(item)
	if err != nil {
		t.Fatalf("MarshalPendingRun() error = %v", err)
	}
	return raw
}

func TestRunCoordinatorStartShadowRunCreatesRunAndInitialStep(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	startedAt := time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC)
	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_01",
		InputText:        "@bot 帮我总结一下",
		Now:              startedAt,
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}
	if run.SessionID == "" || run.ID == "" {
		t.Fatalf("StartShadowRun() returned incomplete run: %+v", run)
	}
	if run.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", run.Status, agentruntime.RunStatusQueued)
	}
	if run.WorkerID != "" {
		t.Fatalf("worker id = %q, want empty for freshly queued run", run.WorkerID)
	}
	if run.HeartbeatAt == nil || !run.HeartbeatAt.Equal(startedAt) {
		t.Fatalf("heartbeat_at = %+v, want %s", run.HeartbeatAt, startedAt)
	}
	if run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(*run.HeartbeatAt) {
		t.Fatalf("lease_expires_at = %+v, want after heartbeat_at %s", run.LeaseExpiresAt, startedAt)
	}
}

func TestRunCoordinatorStartShadowRunIsIdempotentByTriggerMessage(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	req := agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_02",
		InputText:        "@bot 帮我总结一下",
		Now:              time.Date(2026, 3, 18, 13, 5, 0, 0, time.UTC),
	}
	first, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("StartShadowRun() first call error = %v", err)
	}
	second, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("StartShadowRun() second call error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("StartShadowRun() created duplicate run: %q vs %q", first.ID, second.ID)
	}
}

func TestRunCoordinatorStartShadowRunAllowsDifferentActorsInSameChat(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	first, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor_a",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_actor_a",
		InputText:        "@bot actor a",
		Now:              time.Date(2026, 3, 24, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() actor_a error = %v", err)
	}

	second, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor_b",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_actor_b",
		InputText:        "@bot actor b",
		Now:              time.Date(2026, 3, 24, 2, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() actor_b error = %v", err)
	}

	reloadedFirst, err := agentstore.NewRunRepository(db).GetByID(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("GetByID() first error = %v", err)
	}
	if reloadedFirst.Status != agentruntime.RunStatusQueued {
		t.Fatalf("first run status = %q, want %q", reloadedFirst.Status, agentruntime.RunStatusQueued)
	}

	reloadedSecond, err := agentstore.NewRunRepository(db).GetByID(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("GetByID() second error = %v", err)
	}
	if reloadedSecond.Status != agentruntime.RunStatusQueued {
		t.Fatalf("second run status = %q, want %q", reloadedSecond.Status, agentruntime.RunStatusQueued)
	}
}

func TestRunCoordinatorStartShadowRunRejectsWhenActiveRunLimitExceededForSameActor(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	for i := int64(0); i < agentruntime.DefaultMaxActiveRunsPerActorChat; i++ {
		_, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat",
			ActorOpenID:      "ou_actor",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: fmt.Sprintf("om_message_actor_%d", i+1),
			InputText:        "@bot active run",
			Now:              time.Date(2026, 3, 24, 2, 1, int(i), 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("StartShadowRun() seed[%d] error = %v", i, err)
		}
	}

	_, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_actor_overflow",
		InputText:        "@bot overflow",
		Now:              time.Date(2026, 3, 24, 2, 1, 59, 0, time.UTC),
	})
	if !errors.Is(err, agentruntime.ErrActiveRunLimitExceeded) {
		t.Fatalf("StartShadowRun() overflow error = %v, want ErrActiveRunLimitExceeded", err)
	}
}

func TestRunCoordinatorCancelRunMarksRunCancelled(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_03",
		InputText:        "@bot 帮我总结一下",
		Now:              time.Date(2026, 3, 18, 13, 10, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	if err := coordinator.CancelRun(context.Background(), run.ID, "superseded"); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}

	got, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != agentruntime.RunStatusCancelled {
		t.Fatalf("run status = %q, want %q", got.Status, agentruntime.RunStatusCancelled)
	}
	if got.ErrorText != "superseded" {
		t.Fatalf("run error text = %q, want %q", got.ErrorText, "superseded")
	}
}

func TestRunCoordinatorCompleteRunWithReplyAppendsReplyStepAndClearsActiveSlot(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_complete_reply",
		InputText:        "@bot 帮我总结一下",
		Now:              time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	completedAt := time.Date(2026, 3, 18, 13, 21, 0, 0, time.UTC)
	completedRun, err := coordinator.CompleteRunWithReply(context.Background(), agentruntime.CompleteRunWithReplyInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		ThoughtText:       "先看上下文，再给结果",
		ReplyText:         "这是最终回复",
		ResponseMessageID: "om_runtime_reply",
		ResponseCardID:    "card_runtime_reply",
		DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
		CompletedAt:       completedAt,
	})
	if err != nil {
		t.Fatalf("CompleteRunWithReply() error = %v", err)
	}
	if completedRun.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", completedRun.Status, agentruntime.RunStatusCompleted)
	}
	if completedRun.CurrentStepIndex != 1 {
		t.Fatalf("current step index = %d, want 1", completedRun.CurrentStepIndex)
	}
	if completedRun.ResultSummary != "这是最终回复" {
		t.Fatalf("result summary = %q, want %q", completedRun.ResultSummary, "这是最终回复")
	}
	if completedRun.LastResponseID != "om_runtime_reply" {
		t.Fatalf("last response id = %q, want %q", completedRun.LastResponseID, "om_runtime_reply")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	if steps[0].Kind != agentruntime.StepKindDecide || steps[0].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected decide step: %+v", steps[0])
	}
	if steps[1].Kind != agentruntime.StepKindReply || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[1])
	}
	if steps[1].ExternalRef != "card_runtime_reply" {
		t.Fatalf("reply step external ref = %q, want %q", steps[1].ExternalRef, "card_runtime_reply")
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[1].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["thought_text"] != "先看上下文，再给结果" {
		t.Fatalf("thought_text = %#v, want %q", replyOutput["thought_text"], "先看上下文，再给结果")
	}
	if replyOutput["reply_text"] != "这是最终回复" {
		t.Fatalf("reply_text = %#v, want %q", replyOutput["reply_text"], "这是最终回复")
	}
	if replyOutput["delivery_mode"] != string(agentruntime.ReplyDeliveryModeCreate) {
		t.Fatalf("delivery_mode = %#v, want %q", replyOutput["delivery_mode"], agentruntime.ReplyDeliveryModeCreate)
	}
	if replyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateActive) {
		t.Fatalf("lifecycle_state = %#v, want %q", replyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateActive)
	}

	session, err := agentstore.NewSessionRepository(db).GetByID(context.Background(), completedRun.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != "" {
		t.Fatalf("session active run = %q, want empty", session.ActiveRunID)
	}

}

func TestRunCoordinatorCompleteRunWithReplyNotifiesPendingInitialWorker(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	activeRun, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_active",
		InputText:        "@bot 任务一",
		Now:              time.Date(2026, 3, 24, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() active error = %v", err)
	}

	raw := mustMarshalPendingInitialRun(t, initialcore.PendingRun{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           "oc_chat",
			ActorOpenID:      "ou_actor",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_message_pending",
			InputText:        "@bot 任务二",
			Now:              time.Date(2026, 3, 24, 6, 0, 5, 0, time.UTC),
		},
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Args:    []string{"任务二"},
		},
		OutputMode: agentruntime.InitialReplyOutputModeAgentic,
		Event: initialcore.Event{
			ChatID:      "oc_chat",
			MessageID:   "om_message_pending",
			ChatType:    "group",
			ActorOpenID: "ou_actor",
		},
		RootTarget: initialcore.RootTarget{
			MessageID: "om_pending_root",
			CardID:    "card_pending_root",
		},
		RequestedAt: time.Date(2026, 3, 24, 6, 0, 5, 0, time.UTC),
	})
	if _, err := store.EnqueuePendingInitialRun(context.Background(), "oc_chat", "ou_actor", raw, agentruntime.DefaultMaxPendingInitialRunsPerActorChat); err != nil {
		t.Fatalf("EnqueuePendingInitialRun() error = %v", err)
	}
	processor := &observingPendingInitialRunProcessor{
		handled: make(chan agentruntime.RunProcessorInput, 1),
		roots:   make(chan runtimecontext.AgenticReplyTarget, 1),
	}
	worker := initialcore.NewRunWorker(store, processor)
	worker.Start()
	defer worker.Stop()

	select {
	case handled := <-processor.handled:
		t.Fatalf("pending run started before completion-triggered notify: %+v", handled)
	case <-time.After(300 * time.Millisecond):
	}

	_, err = coordinator.CompleteRunWithReply(context.Background(), agentruntime.CompleteRunWithReplyInput{
		RunID:             activeRun.ID,
		Revision:          activeRun.Revision,
		ThoughtText:       "任务一完成",
		ReplyText:         "done",
		ResponseMessageID: "om_runtime_reply_done",
		ResponseCardID:    "card_runtime_reply_done",
		DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
		CompletedAt:       time.Date(2026, 3, 24, 6, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CompleteRunWithReply() error = %v", err)
	}

	select {
	case handled := <-processor.handled:
		if handled.Initial == nil {
			t.Fatalf("handled input = %+v, want initial run", handled)
		}
		if handled.Initial.Start.TriggerMessageID != "om_message_pending" {
			t.Fatalf("initial trigger message = %q, want %q", handled.Initial.Start.TriggerMessageID, "om_message_pending")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected pending initial worker to start queued run after active run completion")
	}

	select {
	case root := <-processor.roots:
		if root.MessageID != "om_pending_root" || root.CardID != "card_pending_root" {
			t.Fatalf("root target = %+v, want message=%q card=%q", root, "om_pending_root", "card_pending_root")
		}
	case <-time.After(time.Second):
		t.Fatal("expected pending initial worker to preserve pending root target")
	}
}

func TestRunCoordinatorCompleteRunWithReplyAppendsCapabilityCallStepsBeforeReply(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_capability_trace",
		InputText:        "@bot 发条消息",
		Now:              time.Date(2026, 3, 18, 13, 22, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	completedRun, err := coordinator.CompleteRunWithReply(context.Background(), agentruntime.CompleteRunWithReplyInput{
		RunID:    run.ID,
		Revision: run.Revision,
		CapabilityCalls: []agentruntime.CompletedCapabilityCall{
			{
				CallID:         "call_1",
				CapabilityName: "send_message",
				Arguments:      `{"text":"hi"}`,
				Output:         "ok",
			},
		},
		ReplyText:   "发送完成",
		CompletedAt: time.Date(2026, 3, 18, 13, 23, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CompleteRunWithReply() error = %v", err)
	}
	if completedRun.CurrentStepIndex != 3 {
		t.Fatalf("current step index = %d, want 3", completedRun.CurrentStepIndex)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("step count = %d, want 4", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected capability step: %+v", steps[1])
	}
	if steps[1].CapabilityName != "send_message" {
		t.Fatalf("capability step name = %q, want %q", steps[1].CapabilityName, "send_message")
	}
	if steps[1].ExternalRef != "call_1" {
		t.Fatalf("capability step external ref = %q, want %q", steps[1].ExternalRef, "call_1")
	}
	if len(steps[1].InputJSON) == 0 {
		t.Fatal("expected capability step input json")
	}
	output := map[string]any{}
	if err := json.Unmarshal(steps[1].OutputJSON, &output); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v", err)
	}
	if output["output_text"] != "ok" {
		t.Fatalf("output_text = %#v, want %q", output["output_text"], "ok")
	}
	if output["external_ref"] != "call_1" {
		t.Fatalf("external_ref = %#v, want %q", output["external_ref"], "call_1")
	}
	if steps[2].Kind != agentruntime.StepKindObserve || steps[2].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected observe step: %+v", steps[2])
	}
	observeOutput := map[string]any{}
	if err := json.Unmarshal(steps[2].OutputJSON, &observeOutput); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observeOutput["capability_name"] != "send_message" {
		t.Fatalf("observe capability_name = %#v, want %q", observeOutput["capability_name"], "send_message")
	}
	if observeOutput["output_text"] != "ok" {
		t.Fatalf("observe output_text = %#v, want %q", observeOutput["output_text"], "ok")
	}
	if steps[3].Kind != agentruntime.StepKindReply || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[3])
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[3].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateActive) {
		t.Fatalf("lifecycle_state = %#v, want %q", replyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateActive)
	}
}

func TestRunCoordinatorContinueRunWithReplyAppendsObserveStepBeforeQueuedCapability(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_pending_with_observe",
		InputText:        "@bot 先查再发",
		Now:              time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	continuedAt := time.Date(2026, 3, 20, 10, 1, 0, 0, time.UTC)
	continuedRun, err := coordinator.ContinueRunWithReply(context.Background(), agentruntime.ContinueRunWithReplyInput{
		RunID:    run.ID,
		Revision: run.Revision,
		CapabilityCalls: []agentruntime.CompletedCapabilityCall{
			{
				CallID:         "call_history_1",
				CapabilityName: "search_history",
				Arguments:      `{"q":"agentic"}`,
				Output:         "查到 3 条记录",
			},
		},
		ReplyText:         "我已经发起审批，待批准后继续发送。",
		ResponseMessageID: "om_runtime_pending_reply",
		ResponseCardID:    "card_runtime_pending_reply",
		DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
		QueuedCapability: &agentruntime.QueuedCapabilityCall{
			CallID:         "call_pending_1",
			CapabilityName: "send_message",
			Input: agentruntime.CapabilityCallInput{
				Request: agentruntime.CapabilityRequest{
					Scope:       agentruntime.CapabilityScopeGroup,
					ChatID:      "oc_chat",
					PayloadJSON: []byte(`{"content":"hi"}`),
				},
				Approval: &agentruntime.CapabilityApprovalSpec{
					Type:      "capability",
					Title:     "审批发送消息",
					Summary:   "将向当前群发送一条消息",
					ExpiresAt: continuedAt.Add(15 * time.Minute),
				},
			},
		},
		ContinuedAt: continuedAt,
	})
	if err != nil {
		t.Fatalf("ContinueRunWithReply() error = %v", err)
	}
	if continuedRun.CurrentStepIndex != 4 {
		t.Fatalf("current step index = %d, want 4", continuedRun.CurrentStepIndex)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("step count = %d, want 5", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall {
		t.Fatalf("unexpected capability step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected observe step: %+v", steps[2])
	}
	if steps[3].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindCapabilityCall || steps[4].Status != agentruntime.StepStatusQueued {
		t.Fatalf("unexpected queued capability step: %+v", steps[4])
	}
}

func TestRunCoordinatorContinueRunWithReplyQueuesPendingCapabilityAfterReply(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_pending_capability",
		InputText:        "@bot 发条消息并等待审批",
		Now:              time.Date(2026, 3, 18, 13, 24, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	continuedAt := time.Date(2026, 3, 18, 13, 25, 0, 0, time.UTC)
	continuedRun, err := coordinator.ContinueRunWithReply(context.Background(), agentruntime.ContinueRunWithReplyInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		ThoughtText:       "需要发送群消息，先发起审批",
		ReplyText:         "我已经发起审批，待批准后继续发送。",
		ResponseMessageID: "om_runtime_pending_reply",
		ResponseCardID:    "card_runtime_pending_reply",
		DeliveryMode:      agentruntime.ReplyDeliveryModeCreate,
		QueuedCapability: &agentruntime.QueuedCapabilityCall{
			CallID:         "call_pending_1",
			CapabilityName: "send_message",
			Input: agentruntime.CapabilityCallInput{
				Request: agentruntime.CapabilityRequest{
					Scope:       agentruntime.CapabilityScopeGroup,
					ChatID:      "oc_chat",
					PayloadJSON: []byte(`{"content":"hi"}`),
				},
				Approval: &agentruntime.CapabilityApprovalSpec{
					Type:      "capability",
					Title:     "审批发送消息",
					Summary:   "将向当前群发送一条消息",
					ExpiresAt: continuedAt.Add(15 * time.Minute),
				},
			},
		},
		ContinuedAt: continuedAt,
	})
	if err != nil {
		t.Fatalf("ContinueRunWithReply() error = %v", err)
	}
	if continuedRun.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", continuedRun.Status, agentruntime.RunStatusQueued)
	}
	if continuedRun.CurrentStepIndex != 2 {
		t.Fatalf("current step index = %d, want 2", continuedRun.CurrentStepIndex)
	}
	if continuedRun.LastResponseID != "om_runtime_pending_reply" {
		t.Fatalf("last response id = %q, want %q", continuedRun.LastResponseID, "om_runtime_pending_reply")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("step count = %d, want 3", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindReply || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindCapabilityCall || steps[2].Status != agentruntime.StepStatusQueued {
		t.Fatalf("unexpected queued capability step: %+v", steps[2])
	}
	if steps[2].CapabilityName != "send_message" {
		t.Fatalf("capability step name = %q, want %q", steps[2].CapabilityName, "send_message")
	}
	if steps[2].ExternalRef != "call_pending_1" {
		t.Fatalf("capability step external ref = %q, want %q", steps[2].ExternalRef, "call_pending_1")
	}
	if raw := strings.TrimSpace(string(steps[2].OutputJSON)); raw != "" && raw != "{}" {
		t.Fatalf("queued capability output should be empty, got %s", raw)
	}

	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[1].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["response_message_id"] != "om_runtime_pending_reply" {
		t.Fatalf("response_message_id = %#v, want %q", replyOutput["response_message_id"], "om_runtime_pending_reply")
	}
	if replyOutput["response_card_id"] != "card_runtime_pending_reply" {
		t.Fatalf("response_card_id = %#v, want %q", replyOutput["response_card_id"], "card_runtime_pending_reply")
	}
	if replyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateActive) {
		t.Fatalf("lifecycle_state = %#v, want %q", replyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateActive)
	}

	session, err := agentstore.NewSessionRepository(db).GetByID(context.Background(), continuedRun.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != continuedRun.ID {
		t.Fatalf("session active run = %q, want %q", session.ActiveRunID, continuedRun.ID)
	}

}

func TestRunCoordinatorStartShadowRunAttachReopensExistingRunWithNewDecideStep(t *testing.T) {
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

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingApproval, agentruntime.WaitingReasonApproval, "approval_token")
	attachedAt := time.Date(2026, 3, 19, 9, 30, 0, 0, time.UTC)

	attachedRun, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeFollowUp,
		TriggerMessageID: "om_attach_followup",
		AttachToRunID:    waitingRun.ID,
		InputText:        "继续刚才的话题",
		Now:              attachedAt,
	})
	if err != nil {
		t.Fatalf("StartShadowRun() attach error = %v", err)
	}

	if attachedRun.ID != waitingRun.ID {
		t.Fatalf("attached run id = %q, want %q", attachedRun.ID, waitingRun.ID)
	}
	if attachedRun.Status != agentruntime.RunStatusQueued {
		t.Fatalf("attached run status = %q, want %q", attachedRun.Status, agentruntime.RunStatusQueued)
	}
	if attachedRun.CurrentStepIndex != waitingRun.CurrentStepIndex+1 {
		t.Fatalf("current step index = %d, want %d", attachedRun.CurrentStepIndex, waitingRun.CurrentStepIndex+1)
	}
	if attachedRun.WaitingReason != agentruntime.WaitingReasonNone {
		t.Fatalf("waiting reason = %q, want empty", attachedRun.WaitingReason)
	}
	if attachedRun.WaitingToken != "" {
		t.Fatalf("waiting token = %q, want empty", attachedRun.WaitingToken)
	}
	if attachedRun.InputText != "继续刚才的话题" {
		t.Fatalf("input text = %q, want %q", attachedRun.InputText, "继续刚才的话题")
	}
	if attachedRun.HeartbeatAt == nil || !attachedRun.HeartbeatAt.Equal(attachedAt) {
		t.Fatalf("heartbeat_at = %+v, want %s", attachedRun.HeartbeatAt, attachedAt)
	}
	if attachedRun.LeaseExpiresAt == nil || !attachedRun.LeaseExpiresAt.After(*attachedRun.HeartbeatAt) {
		t.Fatalf("lease_expires_at = %+v, want after heartbeat_at %s", attachedRun.LeaseExpiresAt, attachedAt)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	last := steps[len(steps)-1]
	if last.Kind != agentruntime.StepKindDecide {
		t.Fatalf("last step kind = %q, want %q", last.Kind, agentruntime.StepKindDecide)
	}
	if last.Status != agentruntime.StepStatusQueued {
		t.Fatalf("last step status = %q, want %q", last.Status, agentruntime.StepStatusQueued)
	}
	if last.Index != attachedRun.CurrentStepIndex {
		t.Fatalf("last step index = %d, want %d", last.Index, attachedRun.CurrentStepIndex)
	}

	session, err := agentstore.NewSessionRepository(db).GetByID(context.Background(), waitingRun.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != waitingRun.ID {
		t.Fatalf("session active run = %q, want %q", session.ActiveRunID, waitingRun.ID)
	}
	if session.LastMessageID != "om_attach_followup" {
		t.Fatalf("session last message id = %q, want %q", session.LastMessageID, "om_attach_followup")
	}
	if session.LastActorOpenID != "ou_actor" {
		t.Fatalf("session last actor open id = %q, want %q", session.LastActorOpenID, "ou_actor")
	}

}

func TestRunCoordinatorStartShadowRunAttachIsIdempotentByLastMessage(t *testing.T) {
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

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingApproval, agentruntime.WaitingReasonApproval, "approval_token")
	req := agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeFollowUp,
		TriggerMessageID: "om_attach_followup_dup",
		AttachToRunID:    waitingRun.ID,
		InputText:        "继续刚才的话题",
		Now:              time.Date(2026, 3, 19, 9, 31, 0, 0, time.UTC),
	}

	first, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("StartShadowRun() first attach error = %v", err)
	}
	second, err := coordinator.StartShadowRun(context.Background(), req)
	if err != nil {
		t.Fatalf("StartShadowRun() second attach error = %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("attached run ids differ: %q vs %q", first.ID, second.ID)
	}
	if first.CurrentStepIndex != second.CurrentStepIndex {
		t.Fatalf("current step index changed on duplicate attach: %d vs %d", first.CurrentStepIndex, second.CurrentStepIndex)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
}

func TestResumeEventValidate(t *testing.T) {
	validCallback := agentruntime.ResumeEvent{
		RunID:    "run_01",
		Revision: 2,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token",
	}
	if err := validCallback.Validate(); err != nil {
		t.Fatalf("Validate() callback error = %v", err)
	}

	validSchedule := agentruntime.ResumeEvent{
		RunID:    "run_02",
		Revision: 3,
		Source:   agentruntime.ResumeSourceSchedule,
	}
	if err := validSchedule.Validate(); err != nil {
		t.Fatalf("Validate() schedule error = %v", err)
	}

	cases := []struct {
		name  string
		event agentruntime.ResumeEvent
	}{
		{
			name: "missing run id",
			event: agentruntime.ResumeEvent{
				Revision: 1,
				Source:   agentruntime.ResumeSourceCallback,
				Token:    "cb_token",
			},
		},
		{
			name: "unknown source",
			event: agentruntime.ResumeEvent{
				RunID:    "run_03",
				Revision: 1,
				Source:   "unknown",
			},
		},
		{
			name: "callback missing token",
			event: agentruntime.ResumeEvent{
				RunID:    "run_04",
				Revision: 1,
				Source:   agentruntime.ResumeSourceCallback,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.event.Validate(); err == nil {
				t.Fatalf("Validate() error = nil for %+v", tc.event)
			}
		})
	}
}

func TestRunCoordinatorResumeRunRejectsRevisionMismatch(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	_, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:    run.ID,
		Revision: run.Revision - 1,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token",
	})
	if !errors.Is(err, agentstore.ErrRevisionConflict) {
		t.Fatalf("ResumeRun() revision error = %v, want %v", err, agentstore.ErrRevisionConflict)
	}
}

func TestRunCoordinatorResumeRunRejectsCancelledRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	if err := coordinator.CancelRun(context.Background(), run.ID, "cancelled before resume"); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}

	_, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:    run.ID,
		Revision: run.Revision + 1,
		Source:   agentruntime.ResumeSourceCallback,
		Token:    "cb_token",
	})
	if !errors.Is(err, agentruntime.ErrResumeStateConflict) {
		t.Fatalf("ResumeRun() cancelled run error = %v, want %v", err, agentruntime.ErrResumeStateConflict)
	}
}

func TestRunCoordinatorResumeRunQueuesWaitingCallbackRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	updated, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceCallback,
		Token:      "cb_token",
		OccurredAt: time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}
	if updated.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updated.Status, agentruntime.RunStatusQueued)
	}
	if updated.WaitingReason != agentruntime.WaitingReasonNone || updated.WaitingToken != "" {
		t.Fatalf("unexpected waiting state after resume: %+v", updated)
	}
	if updated.CurrentStepIndex != 1 {
		t.Fatalf("current step index = %d, want 1", updated.CurrentStepIndex)
	}
	if updated.HeartbeatAt == nil || !updated.HeartbeatAt.Equal(time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC)) {
		t.Fatalf("heartbeat_at = %+v, want resume occurred_at", updated.HeartbeatAt)
	}
	if updated.LeaseExpiresAt == nil || !updated.LeaseExpiresAt.After(*updated.HeartbeatAt) {
		t.Fatalf("lease_expires_at = %+v, want after heartbeat_at %s", updated.LeaseExpiresAt, updated.HeartbeatAt)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindResume || steps[1].Status != agentruntime.StepStatusQueued {
		t.Fatalf("unexpected resume step: %+v", steps[1])
	}
}

func TestRunCoordinatorResumeRunQueuesWaitingScheduleRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "")
	updated, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 13, 25, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}
	if updated.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updated.Status, agentruntime.RunStatusQueued)
	}
}

func openCoordinatorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := pgtest.OpenTempSchema(t)
	if err := agentstore.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func openCoordinatorRedisStore(t *testing.T) *redis_dal.AgentRuntimeStore {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	return redis_dal.NewAgentRuntimeStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), botidentity.Identity{
		AppID:     "cli_app",
		BotOpenID: "ou_bot",
	})
}

func createWaitingRun(t *testing.T, db *gorm.DB, coordinator *agentruntime.RunCoordinator, status agentruntime.RunStatus, waitingReason agentruntime.WaitingReason, waitingToken string) *agentruntime.AgentRun {
	t.Helper()

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_wait_" + string(status),
		InputText:        "@bot 帮我总结一下",
		Now:              time.Date(2026, 3, 18, 13, 15, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	updated, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusRunning
		current.UpdatedAt = time.Date(2026, 3, 18, 13, 15, 30, 0, time.UTC)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() running error = %v", err)
	}

	waitingRun, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), updated.ID, updated.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = status
		current.WaitingReason = waitingReason
		current.WaitingToken = waitingToken
		current.WorkerID = ""
		current.HeartbeatAt = nil
		current.LeaseExpiresAt = nil
		current.UpdatedAt = time.Date(2026, 3, 18, 13, 16, 0, 0, time.UTC)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() waiting error = %v", err)
	}
	return waitingRun
}
