package agentruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	approvaldef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/approval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestActivateReservedApprovalKeepsReservationWhenAppendFails(t *testing.T) {
	store := newApprovalReservationStoreForTest(t)
	runRepo := &stubApprovalRunRepository{
		run: &AgentRun{
			ID:               "run_reserved_approval",
			Status:           RunStatusRunning,
			Revision:         7,
			CurrentStepIndex: 1,
			UpdatedAt:        time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC),
		},
	}
	appendErr := errors.New("append approval step failed")
	stepRepo := &stubApprovalStepRepository{
		appendErr: appendErr,
	}
	coordinator := &RunCoordinator{
		runRepo:      runRepo,
		stepRepo:     stepRepo,
		runtimeStore: store,
	}

	requestedAt := time.Date(2026, 3, 24, 10, 1, 0, 0, time.UTC)
	reservation := approvaldef.ApprovalReservation{
		RunID:          runRepo.run.ID,
		StepID:         "step_reserved_approval",
		Token:          "approval_token",
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		RequestedAt:    requestedAt,
		ExpiresAt:      requestedAt.Add(10 * time.Minute),
	}
	raw, err := reservation.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := store.SaveApprovalReservation(context.Background(), reservation.StepID, reservation.Token, raw, time.Hour); err != nil {
		t.Fatalf("SaveApprovalReservation() error = %v", err)
	}

	_, _, err = coordinator.ActivateReservedApproval(context.Background(), ActivateReservedApprovalInput{
		RunID:       reservation.RunID,
		StepID:      reservation.StepID,
		Token:       reservation.Token,
		RequestedAt: requestedAt.Add(time.Minute),
	})
	if !errors.Is(err, appendErr) {
		t.Fatalf("ActivateReservedApproval() error = %v, want %v", err, appendErr)
	}

	loaded, err := store.LoadApprovalReservation(context.Background(), reservation.StepID, reservation.Token)
	if err != nil {
		t.Fatalf("LoadApprovalReservation() error = %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("expected approval reservation to remain after append failure")
	}
}

func newApprovalReservationStoreForTest(t *testing.T) *redis_dal.AgentRuntimeStore {
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

type stubApprovalRunRepository struct {
	run *AgentRun
}

func (r *stubApprovalRunRepository) Create(context.Context, *AgentRun) error {
	return errors.New("unexpected Create call")
}

func (r *stubApprovalRunRepository) GetByID(_ context.Context, id string) (*AgentRun, error) {
	if r.run == nil || r.run.ID != id {
		return nil, nil
	}
	copied := *r.run
	return &copied, nil
}

func (r *stubApprovalRunRepository) FindByTriggerMessage(context.Context, string, string) (*AgentRun, error) {
	return nil, errors.New("unexpected FindByTriggerMessage call")
}

func (r *stubApprovalRunRepository) FindLatestActiveBySessionActor(context.Context, string, string) (*AgentRun, error) {
	return nil, errors.New("unexpected FindLatestActiveBySessionActor call")
}

func (r *stubApprovalRunRepository) CountActiveBySessionActor(context.Context, string, string) (int64, error) {
	return 0, errors.New("unexpected CountActiveBySessionActor call")
}

func (r *stubApprovalRunRepository) UpdateStatus(_ context.Context, runID string, fromRevision int64, mutate func(*AgentRun) error) (*AgentRun, error) {
	if r.run == nil || r.run.ID != runID {
		return nil, errors.New("run not found")
	}
	if r.run.Revision != fromRevision {
		return nil, errors.New("unexpected revision")
	}
	updated := *r.run
	if err := mutate(&updated); err != nil {
		return nil, err
	}
	updated.Revision++
	r.run = &updated
	copied := updated
	return &copied, nil
}

type stubApprovalStepRepository struct {
	appendErr error
}

func (r *stubApprovalStepRepository) Append(context.Context, *AgentStep) error {
	return r.appendErr
}

func (r *stubApprovalStepRepository) ListByRun(context.Context, string) ([]*AgentStep, error) {
	return nil, nil
}

func (r *stubApprovalStepRepository) UpdateStatus(context.Context, string, StepStatus, func(*AgentStep) error) (*AgentStep, error) {
	return nil, errors.New("unexpected UpdateStatus call")
}
