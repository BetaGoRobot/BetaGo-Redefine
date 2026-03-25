package agentruntime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
)

func TestContinuationProcessorClearActiveRunSlotTriggersPendingScopeSweep(t *testing.T) {
	sessionRepo := &replyCompletionTestSessionRepo{
		session: &AgentSession{
			ID:          "session_1",
			ChatID:      "oc_chat",
			ActiveRunID: "run_1",
		},
	}
	store := &replyCompletionTestCoordinationStore{
		activeRunID: "run_1",
	}
	processor := &ContinuationProcessor{
		coordinator: &RunCoordinator{
			sessionRepo:  sessionRepo,
			runtimeStore: store,
			activeRunTTL: time.Minute,
		},
	}

	triggered := make(chan struct{}, 1)
	RegisterPendingScopeSweepTrigger(func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() {
		RegisterPendingScopeSweepTrigger(nil)
	})

	err := processor.clearActiveRunSlot(context.Background(), &AgentRun{
		ID:          "run_1",
		SessionID:   "session_1",
		ActorOpenID: "ou_actor",
	}, time.Date(2026, 3, 24, 6, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("clearActiveRunSlot() error = %v", err)
	}
	if len(store.notifyCalls) != 1 || store.notifyCalls[0] != "oc_chat::ou_actor" {
		t.Fatalf("NotifyPendingInitialRun() calls = %+v, want [oc_chat::ou_actor]", store.notifyCalls)
	}
	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected clearActiveRunSlot to trigger pending scope sweep")
	}
}

func TestRunProjectionPrefersActiveTargetsOverSuperseded(t *testing.T) {
	projection := NewRunProjection(&AgentRun{CurrentStepIndex: 3}, []*AgentStep{
		mustProjectionReplyStep(t, "step_root_active", 0, "om_root_active", "card_root_active", ReplyLifecycleStateActive),
		mustProjectionReplyStep(t, "step_root_superseded", 1, "om_root_superseded", "card_root_superseded", ReplyLifecycleStateSuperseded),
		mustProjectionReplyStep(t, "step_latest_active", 2, "om_latest_active", "", ReplyLifecycleStateActive),
		mustProjectionReplyStep(t, "step_latest_superseded", 3, "om_latest_superseded", "", ReplyLifecycleStateSuperseded),
	})

	root := projection.RootReplyTarget()
	if root.StepID != "step_root_active" || root.MessageID != "om_root_active" || root.CardID != "card_root_active" {
		t.Fatalf("RootReplyTarget() = %+v", root)
	}

	latest := projection.LatestReplyTarget()
	if latest.StepID != "step_latest_active" || latest.MessageID != "om_latest_active" {
		t.Fatalf("LatestReplyTarget() = %+v", latest)
	}

	latestModel := projection.LatestModelReplyTarget()
	if latestModel.StepID != "step_latest_active" || latestModel.MessageID != "om_latest_active" {
		t.Fatalf("LatestModelReplyTarget() = %+v", latestModel)
	}
}

func TestRunProjectionFallsBackToSupersededTargetsWhenNeeded(t *testing.T) {
	projection := NewRunProjection(&AgentRun{CurrentStepIndex: 1}, []*AgentStep{
		mustProjectionReplyStep(t, "step_superseded_only", 0, "om_only_reply", "card_only_reply", ReplyLifecycleStateSuperseded),
	})

	root := projection.RootReplyTarget()
	if root.StepID != "step_superseded_only" || root.MessageID != "om_only_reply" || root.CardID != "card_only_reply" {
		t.Fatalf("RootReplyTarget() fallback = %+v", root)
	}

	latest := projection.LatestReplyTarget()
	if latest.StepID != "step_superseded_only" || latest.MessageID != "om_only_reply" {
		t.Fatalf("LatestReplyTarget() fallback = %+v", latest)
	}
}

func TestRunProjectionExposesContinuationContextInputs(t *testing.T) {
	projection := NewRunProjection(&AgentRun{CurrentStepIndex: 4}, []*AgentStep{
		mustProjectionReplyStep(t, "step_reply_old", 0, "om_old_reply", "", ReplyLifecycleStateActive),
		mustProjectionCapabilityStep(t, "step_capability", 1, "om_capability_reply"),
		mustProjectionReplyStep(t, "step_reply_superseded", 2, "om_superseded_reply", "", ReplyLifecycleStateSuperseded),
		{ID: "step_plan", Index: 3, Kind: StepKindPlan, ExternalRef: "plan_ref"},
		{ID: "step_wait", Index: 4, Kind: StepKindWait, ExternalRef: "wait_ref"},
	})

	current := projection.CurrentStep()
	if current == nil || current.ID != "step_wait" {
		t.Fatalf("CurrentStep() = %+v", current)
	}

	previous := projection.PreviousStepBefore(current.Index)
	if previous == nil || previous.ID != "step_plan" {
		t.Fatalf("PreviousStepBefore() = %+v", previous)
	}

	target := projection.LatestReplyTargetBefore(current.Index)
	if target.StepID != "step_capability" || target.MessageID != "om_capability_reply" {
		t.Fatalf("LatestReplyTargetBefore() = %+v", target)
	}
}

func TestRunProjectionBuildsContinuationContext(t *testing.T) {
	waitPayload := mustProjectionJSON(t, map[string]any{"title": "日报推送"})
	run := &AgentRun{
		CurrentStepIndex: 3,
		TriggerType:      TriggerTypeScheduleResume,
		WaitingReason:    WaitingReasonSchedule,
	}
	projection := NewRunProjection(run, []*AgentStep{
		mustProjectionReplyStep(t, "step_reply", 0, "om_reply", "card_reply", ReplyLifecycleStateActive),
		{ID: "step_wait", Index: 1, Kind: StepKindWait, ExternalRef: "wait_ref", OutputJSON: waitPayload},
		{ID: "step_resume", Index: 3, Kind: StepKindResume, ExternalRef: "resume_ref"},
	})

	ctx := projection.ContinuationContext(nil, ResumeEvent{
		Source:      ResumeSourceSchedule,
		Summary:     "定时触发",
		PayloadJSON: json.RawMessage(`{"cron":"0 9 * * *"}`),
	})
	if ctx.TriggerType != TriggerTypeScheduleResume || ctx.WaitingReason != WaitingReasonSchedule {
		t.Fatalf("ContinuationContext() trigger/waiting = %+v", ctx)
	}
	if ctx.ResumeStepID != "step_resume" || ctx.ResumeStepExternalRef != "resume_ref" {
		t.Fatalf("ContinuationContext() resume step = %+v", ctx)
	}
	if ctx.PreviousStepKind != StepKindWait || ctx.PreviousStepTitle != "日报推送" || ctx.PreviousStepExternalRef != "wait_ref" {
		t.Fatalf("ContinuationContext() previous step = %+v", ctx)
	}
	if ctx.LatestReplyMessageID != "om_reply" || ctx.LatestReplyCardID != "card_reply" {
		t.Fatalf("ContinuationContext() latest reply = %+v", ctx)
	}
	if string(ctx.ResumePayloadJSON) != `{"cron":"0 9 * * *"}` {
		t.Fatalf("ContinuationContext() payload = %s", string(ctx.ResumePayloadJSON))
	}
}

func TestRunProjectionFindsReplayableCapabilityStepForApprovalResume(t *testing.T) {
	projection := NewRunProjection(&AgentRun{CurrentStepIndex: 4}, []*AgentStep{
		{ID: "step_cap_done", Index: 1, Kind: StepKindCapabilityCall, Status: StepStatusCompleted},
		{ID: "step_cap_running", Index: 2, Kind: StepKindCapabilityCall, Status: StepStatusRunning},
		{ID: "step_resume", Index: 4, Kind: StepKindResume, Status: StepStatusQueued},
	})

	step := projection.ReplayableCapabilityStepBefore(4, ResumeSourceApproval)
	if step == nil || step.ID != "step_cap_running" {
		t.Fatalf("ReplayableCapabilityStepBefore() = %+v", step)
	}

	if projection.ReplayableCapabilityStepBefore(4, ResumeSourceSchedule) != nil {
		t.Fatal("ReplayableCapabilityStepBefore() should be nil for non-approval resumes")
	}
}

func mustProjectionReplyStep(t *testing.T, id string, index int, messageID, cardID string, state ReplyLifecycleState) *AgentStep {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"response_message_id": messageID,
		"response_card_id":    cardID,
		"lifecycle_state":     string(state),
	})
	if err != nil {
		t.Fatalf("Marshal() reply step error = %v", err)
	}
	return &AgentStep{
		ID:          id,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  raw,
		ExternalRef: messageID,
	}
}

func mustProjectionCapabilityStep(t *testing.T, id string, index int, messageID string) *AgentStep {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"compatible_reply_message_id": messageID,
		"compatible_reply_kind":       "message",
	})
	if err != nil {
		t.Fatalf("Marshal() capability step error = %v", err)
	}
	return &AgentStep{
		ID:         id,
		Index:      index,
		Kind:       StepKindCapabilityCall,
		Status:     StepStatusCompleted,
		OutputJSON: raw,
	}
}

func mustProjectionJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return raw
}

func TestRunCoordinatorStartRunExecutionSetsLiveness(t *testing.T) {
	coordinator, runRepo, run := newRunLivenessTestCoordinator(t)
	startedAt := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	updated, err := coordinator.startRunExecution(context.Background(), run, "worker_exec", startedAt, RunLeasePolicy{
		TTL:               30 * time.Second,
		HeartbeatInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("startRunExecution() error = %v", err)
	}
	if updated.Status != RunStatusRunning {
		t.Fatalf("run status = %q, want %q", updated.Status, RunStatusRunning)
	}
	if updated.WorkerID != "worker_exec" {
		t.Fatalf("worker_id = %q, want %q", updated.WorkerID, "worker_exec")
	}
	if updated.HeartbeatAt == nil || !updated.HeartbeatAt.Equal(startedAt) {
		t.Fatalf("heartbeat_at = %+v, want %s", updated.HeartbeatAt, startedAt)
	}
	if updated.LeaseExpiresAt == nil || !updated.LeaseExpiresAt.Equal(startedAt.Add(30*time.Second)) {
		t.Fatalf("lease_expires_at = %+v, want %s", updated.LeaseExpiresAt, startedAt.Add(30*time.Second))
	}

	reloaded, err := runRepo.GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if reloaded.WorkerID != "worker_exec" {
		t.Fatalf("reloaded worker_id = %q, want %q", reloaded.WorkerID, "worker_exec")
	}
}

func TestContinuationProcessorWithRunExecutionHeartbeatRenewsLease(t *testing.T) {
	coordinator, runRepo, run := newRunLivenessTestCoordinator(t)
	processor := &ContinuationProcessor{
		coordinator: coordinator,
		runLeasePolicy: RunLeasePolicy{
			TTL:               40 * time.Millisecond,
			HeartbeatInterval: 10 * time.Millisecond,
		},
	}

	startedAt := time.Now().UTC()
	err := processor.withRunExecutionHeartbeat(withExecutionWorkerID(context.Background(), "worker_exec"), run, startedAt, func(currentRun *AgentRun) error {
		if currentRun == nil || currentRun.Status != RunStatusRunning {
			t.Fatalf("current run = %+v, want running run", currentRun)
		}
		time.Sleep(35 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("withRunExecutionHeartbeat() error = %v", err)
	}

	reloaded, err := runRepo.GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if reloaded.HeartbeatAt == nil {
		t.Fatal("heartbeat_at = nil, want renewed timestamp")
	}
	if reloaded.LeaseExpiresAt == nil {
		t.Fatal("lease_expires_at = nil, want renewed timestamp")
	}
	if !reloaded.HeartbeatAt.After(startedAt) {
		t.Fatalf("heartbeat_at = %s, want after %s", reloaded.HeartbeatAt, startedAt)
	}
	if !reloaded.LeaseExpiresAt.After(*reloaded.HeartbeatAt) {
		t.Fatalf("lease_expires_at = %s, want after heartbeat_at %s", reloaded.LeaseExpiresAt, reloaded.HeartbeatAt)
	}
}

func newRunLivenessTestCoordinator(t *testing.T) (*RunCoordinator, *runLivenessTestRunRepo, *AgentRun) {
	t.Helper()
	runRepo := &runLivenessTestRunRepo{runs: make(map[string]*AgentRun)}
	run := &AgentRun{
		ID:               "run_liveness_internal",
		SessionID:        "session_liveness_internal",
		TriggerType:      TriggerTypeMention,
		TriggerMessageID: "om_liveness_internal",
		ActorOpenID:      "ou_actor",
		Status:           RunStatusQueued,
		CurrentStepIndex: 0,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	runRepo.runs[run.ID] = cloneRunForTest(run)

	coordinator := NewRunCoordinator(
		nil,
		runRepo,
		nil,
		nil,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)
	return coordinator, runRepo, run
}

type runLivenessTestRunRepo struct {
	mu   sync.Mutex
	runs map[string]*AgentRun
}

func (r *runLivenessTestRunRepo) ensure() {
	if r.runs == nil {
		r.runs = make(map[string]*AgentRun)
	}
}

func (r *runLivenessTestRunRepo) Create(_ context.Context, run *AgentRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	r.runs[run.ID] = cloneRunForTest(run)
	return nil
}

func (r *runLivenessTestRunRepo) GetByID(_ context.Context, id string) (*AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	if run, ok := r.runs[id]; ok {
		return cloneRunForTest(run), nil
	}
	return nil, nil
}

func (r *runLivenessTestRunRepo) FindByTriggerMessage(context.Context, string, string) (*AgentRun, error) {
	return nil, nil
}

func (r *runLivenessTestRunRepo) FindLatestActiveBySessionActor(context.Context, string, string) (*AgentRun, error) {
	return nil, nil
}

func (r *runLivenessTestRunRepo) CountActiveBySessionActor(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (r *runLivenessTestRunRepo) UpdateStatus(_ context.Context, runID string, fromRevision int64, mutate func(*AgentRun) error) (*AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	current, ok := r.runs[runID]
	if !ok {
		return nil, nil
	}
	if current.Revision != fromRevision {
		return nil, nil
	}
	next := cloneRunForTest(current)
	if mutate != nil {
		if err := mutate(next); err != nil {
			return nil, err
		}
	}
	next.Revision = current.Revision + 1
	r.runs[runID] = cloneRunForTest(next)
	return cloneRunForTest(next), nil
}

func cloneRunForTest(run *AgentRun) *AgentRun {
	if run == nil {
		return nil
	}
	cloned := *run
	if run.StartedAt != nil {
		startedAt := *run.StartedAt
		cloned.StartedAt = &startedAt
	}
	if run.FinishedAt != nil {
		finishedAt := *run.FinishedAt
		cloned.FinishedAt = &finishedAt
	}
	if run.HeartbeatAt != nil {
		heartbeatAt := *run.HeartbeatAt
		cloned.HeartbeatAt = &heartbeatAt
	}
	if run.LeaseExpiresAt != nil {
		leaseExpiresAt := *run.LeaseExpiresAt
		cloned.LeaseExpiresAt = &leaseExpiresAt
	}
	return &cloned
}
