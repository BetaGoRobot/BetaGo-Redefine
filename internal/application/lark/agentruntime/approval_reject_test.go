package agentruntime_test

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
)

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
