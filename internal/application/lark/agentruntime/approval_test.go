package agentruntime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"gorm.io/gorm"
)

func TestApprovalRequestValidateAndApprovePayload(t *testing.T) {
	req := agentruntime.ApprovalRequest{
		RunID:          "run_approval",
		StepID:         "step_approval",
		Revision:       3,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		Token:          "approval_token",
		ExpiresAt:      time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),
	}

	if err := req.Validate(time.Date(2026, 3, 18, 17, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	payload := req.ApprovePayload()
	if payload[cardactionproto.ActionField] != cardactionproto.ActionAgentRuntimeResume {
		t.Fatalf("approve action = %q", payload[cardactionproto.ActionField])
	}
	if payload[cardactionproto.RunIDField] != "run_approval" ||
		payload[cardactionproto.StepIDField] != "step_approval" ||
		payload[cardactionproto.RevisionField] != "3" ||
		payload[cardactionproto.SourceField] != string(agentruntime.ResumeSourceApproval) ||
		payload[cardactionproto.TokenField] != "approval_token" {
		t.Fatalf("unexpected approve payload: %+v", payload)
	}

	if err := req.Validate(time.Date(2026, 3, 18, 18, 1, 0, 0, time.UTC)); !errors.Is(err, agentruntime.ErrApprovalExpired) {
		t.Fatalf("expired Validate() error = %v, want %v", err, agentruntime.ErrApprovalExpired)
	}
}

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
