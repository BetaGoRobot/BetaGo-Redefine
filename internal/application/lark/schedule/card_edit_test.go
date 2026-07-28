package schedule

import (
	"context"
	"reflect"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestBuildRuntimeEditConfirmCardUsesOnlyTrustedEnvelopeFields(t *testing.T) {
	task := &model.ScheduledTask{ID: "task-secret", Name: "旧名称", Timezone: "Asia/Shanghai"}
	newValues := map[string]any{editFieldName: "new-value-secret"}
	envelope := agentruntime.RuntimeEnvelope{
		RunID:           "run-1",
		StepID:          "step-1",
		InteractionID:   "interaction-1",
		Revision:        7,
		Token:           "opaque-runtime-token",
		InteractionKind: "schedule_edit",
		ContinueAgent:   true,
	}

	card, err := buildRuntimeEditConfirmCard(context.Background(), task, newValues, envelope)
	if err != nil {
		t.Fatalf("buildRuntimeEditConfirmCard() error = %v", err)
	}
	payloads := collectActionPayloads(card)
	wantConfirm := map[string]any{
		cardactionproto.ActionField:          cardactionproto.ActionScheduleEditConfirm,
		cardactionproto.RunIDField:           "run-1",
		cardactionproto.StepIDField:          "step-1",
		cardactionproto.InteractionIDField:   "interaction-1",
		cardactionproto.RevisionField:        "7",
		cardactionproto.TokenField:           "opaque-runtime-token",
		cardactionproto.InteractionKindField: "schedule_edit",
		cardactionproto.ContinueAgentField:   "true",
	}
	wantCancel := map[string]any{
		cardactionproto.ActionField:          cardactionproto.ActionScheduleEditCancel,
		cardactionproto.RunIDField:           "run-1",
		cardactionproto.StepIDField:          "step-1",
		cardactionproto.InteractionIDField:   "interaction-1",
		cardactionproto.RevisionField:        "7",
		cardactionproto.TokenField:           "opaque-runtime-token",
		cardactionproto.InteractionKindField: "schedule_edit",
		cardactionproto.ContinueAgentField:   "true",
	}
	if got := payloads[cardactionproto.ActionScheduleEditConfirm]; !reflect.DeepEqual(got, wantConfirm) {
		t.Fatal("runtime confirm payload does not contain exactly the trusted envelope fields")
	}
	if got := payloads[cardactionproto.ActionScheduleEditCancel]; !reflect.DeepEqual(got, wantCancel) {
		t.Fatal("runtime cancel payload does not contain exactly the trusted envelope fields")
	}
}

func TestBuildEditConfirmCardKeepsLegacyPayloadFields(t *testing.T) {
	task := &model.ScheduledTask{ID: "task-legacy", Name: "旧名称", Timezone: "Asia/Shanghai"}
	card, err := buildEditConfirmCard(
		context.Background(),
		task,
		map[string]any{editFieldName: "新名称"},
		"legacy-edit-token",
	)
	if err != nil {
		t.Fatalf("buildEditConfirmCard() error = %v", err)
	}

	payloads := collectActionPayloads(card)
	wantConfirm := map[string]any{
		cardactionproto.ActionField: editConfirmAction,
		editTokenField:              "legacy-edit-token",
		taskCardViewIDField:         "task-legacy",
	}
	wantCancel := map[string]any{
		cardactionproto.ActionField: editCancelAction,
		editTokenField:              "legacy-edit-token",
	}
	if got := payloads[editConfirmAction]; !reflect.DeepEqual(got, wantConfirm) {
		t.Fatal("legacy confirm payload fields changed")
	}
	if got := payloads[editCancelAction]; !reflect.DeepEqual(got, wantCancel) {
		t.Fatal("legacy cancel payload fields changed")
	}
}

func collectActionPayloads(value any) map[string]map[string]any {
	result := make(map[string]map[string]any)
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if action, ok := typed[cardactionproto.ActionField].(string); ok {
				result[action] = typed
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}
