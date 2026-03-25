package agentruntime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"gorm.io/gorm"
)

func TestRunCoordinatorRequestApprovalTransitionsRunToWaitingApproval(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createRunningRun(t, db, coordinator)
	requestedAt := time.Date(2026, 3, 18, 17, 30, 0, 0, time.UTC)
	approval, err := coordinator.RequestApproval(context.Background(), agentruntime.RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		ExpiresAt:      requestedAt.Add(10 * time.Minute),
		RequestedAt:    requestedAt,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if approval.RunID != run.ID || approval.StepID == "" || approval.Token == "" {
		t.Fatalf("unexpected approval request: %+v", approval)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusWaitingApproval)
	}
	if updatedRun.WaitingReason != agentruntime.WaitingReasonApproval || updatedRun.WaitingToken != approval.Token {
		t.Fatalf("unexpected waiting state after approval request: %+v", updatedRun)
	}
	if updatedRun.CurrentStepIndex != 1 {
		t.Fatalf("current step index = %d, want 1", updatedRun.CurrentStepIndex)
	}
	if approval.Revision != updatedRun.Revision {
		t.Fatalf("approval revision = %d, run revision = %d", approval.Revision, updatedRun.Revision)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindApprovalRequest || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected approval step: %+v", steps[1])
	}
	if steps[1].ExternalRef != approval.Token {
		t.Fatalf("approval step external ref = %q, want %q", steps[1].ExternalRef, approval.Token)
	}
}

func TestRunCoordinatorResumeRunRejectsExpiredApproval(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createRunningRun(t, db, coordinator)
	approval, err := coordinator.RequestApproval(context.Background(), agentruntime.RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		ExpiresAt:      time.Date(2026, 3, 18, 17, 35, 0, 0, time.UTC),
		RequestedAt:    time.Date(2026, 3, 18, 17, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}

	_, err = coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:      approval.RunID,
		StepID:     approval.StepID,
		Revision:   approval.Revision,
		Source:     agentruntime.ResumeSourceApproval,
		Token:      approval.Token,
		OccurredAt: time.Date(2026, 3, 18, 17, 40, 0, 0, time.UTC),
	})
	if !errors.Is(err, agentruntime.ErrApprovalExpired) {
		t.Fatalf("ResumeRun() expired approval error = %v, want %v", err, agentruntime.ErrApprovalExpired)
	}
}

func TestRunCoordinatorResumeRunQueuesWaitingApprovalRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createRunningRun(t, db, coordinator)
	approval, err := coordinator.RequestApproval(context.Background(), agentruntime.RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		ExpiresAt:      time.Date(2026, 3, 18, 17, 45, 0, 0, time.UTC),
		RequestedAt:    time.Date(2026, 3, 18, 17, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}

	updated, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:      approval.RunID,
		StepID:     approval.StepID,
		Revision:   approval.Revision,
		Source:     agentruntime.ResumeSourceApproval,
		Token:      approval.Token,
		OccurredAt: time.Date(2026, 3, 18, 17, 40, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}
	if updated.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updated.Status, agentruntime.RunStatusQueued)
	}
}

func TestRunCoordinatorRejectApprovalCancelsWaitingRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createRunningRun(t, db, coordinator)
	approval, err := coordinator.RequestApproval(context.Background(), agentruntime.RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		ExpiresAt:      time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),
		RequestedAt:    time.Date(2026, 3, 18, 17, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}

	rejected, err := coordinator.RejectApproval(context.Background(), agentruntime.ResumeEvent{
		RunID:       approval.RunID,
		StepID:      approval.StepID,
		Revision:    approval.Revision,
		Source:      agentruntime.ResumeSourceApproval,
		Token:       approval.Token,
		ActorOpenID: "ou_reviewer",
		OccurredAt:  time.Date(2026, 3, 18, 17, 40, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RejectApproval() error = %v", err)
	}
	if rejected.Status != agentruntime.RunStatusCancelled {
		t.Fatalf("run status = %q, want %q", rejected.Status, agentruntime.RunStatusCancelled)
	}
	if rejected.WaitingReason != agentruntime.WaitingReasonNone || rejected.WaitingToken != "" {
		t.Fatalf("unexpected waiting state after reject: %+v", rejected)
	}
	if rejected.ErrorText == "" {
		t.Fatalf("expected cancellation reason after reject: %+v", rejected)
	}

	session, err := agentstore.NewSessionRepository(db).GetByID(context.Background(), rejected.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != "" {
		t.Fatalf("session active run = %q, want empty", session.ActiveRunID)
	}
}

func TestRunCoordinatorReserveApprovalLoadsFallbackAndConsumesRecordedDecision(t *testing.T) {
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
		TriggerMessageID: "om_reserved_approval",
		InputText:        "@bot 先准备一个需要审批的动作",
		Now:              time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	requestedAt := time.Date(2026, 3, 23, 10, 1, 0, 0, time.UTC)
	reserved, err := coordinator.ReserveApproval(context.Background(), agentruntime.RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		PayloadJSON:    []byte(`{"content":"hello"}`),
		ExpiresAt:      requestedAt.Add(10 * time.Minute),
		RequestedAt:    requestedAt,
	})
	if err != nil {
		t.Fatalf("ReserveApproval() error = %v", err)
	}
	if reserved.StepID == "" || reserved.Token == "" {
		t.Fatalf("unexpected reserved request: %+v", reserved)
	}

	loaded, err := coordinator.LoadApprovalRequest(context.Background(), run.ID, reserved.StepID)
	if err != nil {
		t.Fatalf("LoadApprovalRequest() fallback error = %v", err)
	}
	if loaded.StepID != reserved.StepID || loaded.Token != reserved.Token {
		t.Fatalf("loaded request = %+v, want %+v", loaded, reserved)
	}

	resumed, err := coordinator.ResumeRun(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		StepID:     reserved.StepID,
		Revision:   reserved.Revision,
		Source:     agentruntime.ResumeSourceApproval,
		Token:      reserved.Token,
		OccurredAt: requestedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ResumeRun() pre-activation error = %v", err)
	}
	if resumed != nil {
		t.Fatalf("ResumeRun() = %+v, want nil before activation", resumed)
	}

	runningRun, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusRunning
		current.UpdatedAt = requestedAt.Add(2 * time.Minute)
		current.StartedAt = ptrTime(requestedAt.Add(2 * time.Minute))
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() running error = %v", err)
	}

	activated, decision, err := coordinator.ActivateReservedApproval(context.Background(), agentruntime.ActivateReservedApprovalInput{
		RunID:       runningRun.ID,
		StepID:      reserved.StepID,
		Token:       reserved.Token,
		RequestedAt: requestedAt.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ActivateReservedApproval() error = %v", err)
	}
	if activated.StepID != reserved.StepID || activated.Token != reserved.Token {
		t.Fatalf("activated request = %+v, want preserved ids from %+v", activated, reserved)
	}
	if decision == nil || decision.Outcome != agentruntime.ApprovalReservationDecisionApproved {
		t.Fatalf("decision = %+v, want approved", decision)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusWaitingApproval)
	}
	if updatedRun.WaitingToken != reserved.Token {
		t.Fatalf("waiting token = %q, want %q", updatedRun.WaitingToken, reserved.Token)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindApprovalRequest {
		t.Fatalf("approval step kind = %q, want %q", steps[1].Kind, agentruntime.StepKindApprovalRequest)
	}
	if steps[1].ID != reserved.StepID {
		t.Fatalf("approval step id = %q, want %q", steps[1].ID, reserved.StepID)
	}
	if steps[1].ExternalRef != reserved.Token {
		t.Fatalf("approval external ref = %q, want %q", steps[1].ExternalRef, reserved.Token)
	}
}

func createRunningRun(t *testing.T, db *gorm.DB, coordinator *agentruntime.RunCoordinator) *agentruntime.AgentRun {
	t.Helper()

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_running_for_approval",
		InputText:        "@bot 执行一个需要审批的动作",
		Now:              time.Date(2026, 3, 18, 17, 20, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	updated, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusRunning
		current.UpdatedAt = time.Date(2026, 3, 18, 17, 21, 0, 0, time.UTC)
		current.StartedAt = ptrTime(time.Date(2026, 3, 18, 17, 21, 0, 0, time.UTC))
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() running error = %v", err)
	}
	return updated
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
