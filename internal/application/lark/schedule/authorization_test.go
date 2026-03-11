package schedule

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestEnsureTaskMutationAllowedAllowsCreator(t *testing.T) {
	task := &model.ScheduledTask{CreatorID: "ou_creator"}

	if err := EnsureTaskMutationAllowed(context.Background(), "ou_creator", task); err != nil {
		t.Fatalf("expected creator to be allowed, got %v", err)
	}
}

func TestEnsureTaskMutationAllowedAllowsPrivilegedUser(t *testing.T) {
	oldChecker := scheduleManageAllowed
	scheduleManageAllowed = func(context.Context, string) error { return nil }
	t.Cleanup(func() { scheduleManageAllowed = oldChecker })

	task := &model.ScheduledTask{CreatorID: "ou_creator"}
	if err := EnsureTaskMutationAllowed(context.Background(), "ou_admin", task); err != nil {
		t.Fatalf("expected privileged user to be allowed, got %v", err)
	}
}

func TestEnsureTaskMutationAllowedRejectsUnknownUser(t *testing.T) {
	oldChecker := scheduleManageAllowed
	scheduleManageAllowed = func(context.Context, string) error { return errors.New("denied") }
	t.Cleanup(func() { scheduleManageAllowed = oldChecker })

	task := &model.ScheduledTask{CreatorID: "ou_creator"}
	err := EnsureTaskMutationAllowed(context.Background(), "ou_other", task)
	if err == nil {
		t.Fatal("expected permission error")
	}
	if err.Error() != "only schedule creator or privileged users can modify schedule" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureTaskMutationAllowedRejectsEmptyOperator(t *testing.T) {
	task := &model.ScheduledTask{CreatorID: "ou_creator"}

	err := EnsureTaskMutationAllowed(context.Background(), "", task)
	if err == nil {
		t.Fatal("expected empty operator to be rejected")
	}
	if err.Error() != "schedule mutation requires operator identity" {
		t.Fatalf("unexpected error: %v", err)
	}
}
