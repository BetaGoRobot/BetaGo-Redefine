package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

type runtimeEditTaskServiceFake struct {
	noopService
	task        *model.ScheduledTask
	updateErr   error
	updateCalls int
	lastUpdate  *UpdateTaskRequest
}

func (f *runtimeEditTaskServiceFake) GetTask(context.Context, string) (*model.ScheduledTask, error) {
	return f.task, nil
}

func (f *runtimeEditTaskServiceFake) UpdateTask(_ context.Context, req *UpdateTaskRequest) (*model.ScheduledTask, error) {
	f.updateCalls++
	f.lastUpdate = req
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.task, nil
}

func TestRuntimeScheduleEditCapabilityValidatesBeforeClaim(t *testing.T) {
	oldChecker := scheduleManageAllowed
	scheduleManageAllowed = func(context.Context, string) error { return errors.New("denied") }
	t.Cleanup(func() { scheduleManageAllowed = oldChecker })

	fake := &runtimeEditTaskServiceFake{
		task: &model.ScheduledTask{ID: "task-1", ChatID: "oc-chat", CreatorID: "ou-owner"},
	}
	capability := NewRuntimeScheduleEditCapability(fake)
	input := runtimeScheduleEditTrustedInput()

	if err := capability.ValidateScheduleEdit(context.Background(), "ou-owner", input); err != nil {
		t.Fatalf("ValidateScheduleEdit(owner) error = %v", err)
	}
	if err := capability.ValidateScheduleEdit(context.Background(), "ou-other", input); err == nil {
		t.Fatal("ValidateScheduleEdit(other) error = nil, want permission rejection")
	}
	input.ChatID = "oc-other"
	if err := capability.ValidateScheduleEdit(context.Background(), "ou-owner", input); err == nil {
		t.Fatal("ValidateScheduleEdit(cross-chat) error = nil, want trusted chat rejection")
	}
}

func TestRuntimeScheduleEditCapabilityBuildsAbsoluteUpdateFromTrustedInput(t *testing.T) {
	fake := &runtimeEditTaskServiceFake{
		task: &model.ScheduledTask{ID: "task-1", ChatID: "oc-chat", CreatorID: "ou-owner", Name: "new-name"},
	}
	capability := NewRuntimeScheduleEditCapability(fake)
	input := runtimeScheduleEditTrustedInput()

	result, err := capability.ExecuteScheduleEdit(context.Background(), "ou-owner", input)
	if err != nil {
		t.Fatalf("ExecuteScheduleEdit() error = %v", err)
	}
	if fake.updateCalls != 1 || fake.lastUpdate == nil ||
		fake.lastUpdate.ID != "task-1" || fake.lastUpdate.ActorOpenID != "ou-owner" ||
		fake.lastUpdate.Name == nil || *fake.lastUpdate.Name != "new-name" {
		t.Fatalf("absolute update request = %#v", fake.lastUpdate)
	}
	var outcome map[string]any
	if err := json.Unmarshal(result, &outcome); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if outcome["status"] != "updated" || outcome["task_id"] != "task-1" {
		t.Fatalf("result = %#v", outcome)
	}
}

func TestRuntimeScheduleEditCapabilityTreatsNoOpReplayAsSuccess(t *testing.T) {
	fake := &runtimeEditTaskServiceFake{
		task:      &model.ScheduledTask{ID: "task-1", ChatID: "oc-chat", CreatorID: "ou-owner", Name: "new-name"},
		updateErr: ErrNoScheduleFieldsToUpdate,
	}
	capability := NewRuntimeScheduleEditCapability(fake)

	result, err := capability.ExecuteScheduleEdit(context.Background(), "ou-owner", runtimeScheduleEditTrustedInput())

	if err != nil {
		t.Fatalf("ExecuteScheduleEdit(no-op replay) error = %v", err)
	}
	if !json.Valid(result) || fake.updateCalls != 1 {
		t.Fatalf("result valid=%v update calls=%d", json.Valid(result), fake.updateCalls)
	}
}

func TestRuntimeScheduleEditCapabilityPropagatesUpdateFailure(t *testing.T) {
	updateErr := errors.New("update failed")
	fake := &runtimeEditTaskServiceFake{
		task:      &model.ScheduledTask{ID: "task-1", ChatID: "oc-chat", CreatorID: "ou-owner"},
		updateErr: updateErr,
	}
	capability := NewRuntimeScheduleEditCapability(fake)
	if _, err := capability.ExecuteScheduleEdit(
		context.Background(), "ou-owner", runtimeScheduleEditTrustedInput(),
	); !errors.Is(err, updateErr) {
		t.Fatalf("ExecuteScheduleEdit() error = %v, want update failure", err)
	}
}

func TestRuntimeScheduleEditCapabilityRejectsNilTaskResults(t *testing.T) {
	fake := &runtimeEditTaskServiceFake{}
	capability := NewRuntimeScheduleEditCapability(fake)
	input := runtimeScheduleEditTrustedInput()

	if err := capability.ValidateScheduleEdit(context.Background(), "ou-owner", input); err == nil {
		t.Fatal("ValidateScheduleEdit(nil task) error = nil")
	}
	if _, err := capability.ExecuteScheduleEdit(context.Background(), "ou-owner", input); err == nil {
		t.Fatal("ExecuteScheduleEdit(nil task) error = nil")
	}
}

func runtimeScheduleEditTrustedInput() agentruntime.ScheduleEditTrustedInput {
	name := "new-name"
	return agentruntime.ScheduleEditTrustedInput{
		Version: 1, TaskID: "task-1", InitiatorOpenID: "ou-owner", ChatID: "oc-chat",
		NewValues: agentruntime.ScheduleEditValues{Name: &name},
	}
}
