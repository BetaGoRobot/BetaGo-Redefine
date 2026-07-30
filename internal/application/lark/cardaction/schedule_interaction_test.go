package cardaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type scheduleInteractionResolverFake struct {
	outcome agentruntime.ScheduleInteractionOutcome
	err     error
	calls   int
	request agentruntime.ScheduleInteractionRequest
}

func (f *scheduleInteractionResolverFake) Resolve(
	_ context.Context,
	req agentruntime.ScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionOutcome, error) {
	f.calls++
	f.request = req
	return f.outcome, f.err
}

func TestScheduleInteractionDispatcherResolvesRuntimeConfirmAndReturnsTerminalCard(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	resolver := &scheduleInteractionResolverFake{outcome: agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: "interaction-1",
		Action: agentruntime.ScheduleInteractionConfirm, Result: json.RawMessage(`{"status":"updated"}`),
	}}
	dispatcher, err := NewScheduleInteractionDispatcher(resolver, ScheduleInteractionDispatcherOptions{
		Now: func() time.Time { return now },
		NewClaimID: func() string {
			return "claim-1"
		},
		RunningTTL: time.Minute,
		IndexAlias: "agent-conversations",
	})
	if err != nil {
		t.Fatalf("NewScheduleInteractionDispatcher() error = %v", err)
	}
	event := scheduleRuntimeActionEvent(cardactionproto.ActionScheduleEditConfirm)
	parsed, err := cardactionproto.Parse(event)
	if err != nil {
		t.Fatal(err)
	}
	if !dispatcher.CanHandle(parsed) {
		t.Fatal("CanHandle(runtime confirm) = false")
	}

	response, err := dispatcher.Dispatch(context.Background(), ContinuationRequest{
		Event: event, Action: parsed,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	req := resolver.request
	if req.RunID != "run-1" || req.StepID != "step-1" ||
		req.InteractionID != "interaction-1" || req.Revision != 2 ||
		req.PresentedToken != "opaque-token" || req.ActorOpenID != "ou-actor" ||
		req.Action != agentruntime.ScheduleInteractionConfirm ||
		req.EventID != "event-1" || req.SourceRef != "om-card-1" ||
		req.ResolvedAt != now || req.ClaimID != "claim-1" ||
		req.Projection.IndexAlias != "agent-conversations" {
		t.Fatalf("resolver request = %#v", req)
	}
	if response == nil || response.Toast == nil || response.Toast.Type != "info" ||
		response.Card == nil || response.Card.Type != "raw" {
		t.Fatalf("response = %#v", response)
	}
	cardJSON, err := json.Marshal(response.Card.Data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cardJSON), cardactionproto.ActionScheduleEditConfirm) ||
		strings.Contains(string(cardJSON), cardactionproto.ActionScheduleEditCancel) ||
		!strings.Contains(string(cardJSON), "已更新") {
		t.Fatalf("terminal card still actionable or missing state: %s", cardJSON)
	}
}

func TestScheduleInteractionDispatcherReturnsCancelledTerminalCard(t *testing.T) {
	resolver := &scheduleInteractionResolverFake{outcome: agentruntime.ScheduleInteractionOutcome{
		Status: "cancelled_by_user", TaskID: "task-1", InteractionID: "interaction-1",
		Action: agentruntime.ScheduleInteractionCancel, Result: json.RawMessage(`{}`),
	}}
	dispatcher := mustScheduleInteractionDispatcher(t, resolver)
	event := scheduleRuntimeActionEvent(cardactionproto.ActionScheduleEditCancel)
	parsed, _ := cardactionproto.Parse(event)

	response, err := dispatcher.Dispatch(context.Background(), ContinuationRequest{Event: event, Action: parsed})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resolver.request.Action != agentruntime.ScheduleInteractionCancel {
		t.Fatalf("resolver action = %q", resolver.request.Action)
	}
	cardJSON, _ := json.Marshal(response.Card.Data)
	if !strings.Contains(string(cardJSON), "已取消") {
		t.Fatalf("cancel terminal card = %s", cardJSON)
	}
}

func TestScheduleInteractionDispatcherCompatibilityBoundary(t *testing.T) {
	dispatcher := mustScheduleInteractionDispatcher(t, &scheduleInteractionResolverFake{})
	legacy, _ := cardactionproto.Parse(actionEvent(map[string]any{
		cardactionproto.ActionField: cardactionproto.ActionScheduleEditConfirm,
		"edit_token":                "legacy-token",
	}))
	if dispatcher.CanHandle(legacy) {
		t.Fatal("CanHandle(legacy schedule edit) = true, want V1 fallback")
	}

	_, partialErr := cardactionproto.Parse(actionEvent(map[string]any{
		cardactionproto.ActionField: cardactionproto.ActionScheduleEditConfirm,
		cardactionproto.RunIDField:  "run-1",
	}))
	if !errors.Is(partialErr, cardactionproto.ErrPartialRuntimeEnvelope) {
		t.Fatalf("Parse(partial runtime envelope) error = %v", partialErr)
	}

	other, _ := cardactionproto.Parse(runtimeActionEvent("unrelated.action"))
	if dispatcher.CanHandle(other) {
		t.Fatal("CanHandle(unrelated runtime action) = true")
	}
}

func TestScheduleInteractionDispatcherLeavesLegacyCancelOnV1Path(t *testing.T) {
	RegisterBuiltins()
	resolver := &scheduleInteractionResolverFake{}
	dispatcher := mustScheduleInteractionDispatcher(t, resolver)
	event := actionEvent(map[string]any{
		cardactionproto.ActionField: cardactionproto.ActionScheduleEditCancel,
		"edit_token":                "legacy-token",
	})

	response, err := DispatchWithOptions(context.Background(), event, nil, DispatchOptions{
		Continuation: dispatcher,
	})
	if err != nil {
		t.Fatalf("DispatchWithOptions(legacy cancel) error = %v", err)
	}
	if resolver.calls != 0 || response == nil || response.Toast == nil ||
		response.Toast.Content != "已取消编辑" {
		t.Fatalf("legacy response = %#v, resolver calls = %d", response, resolver.calls)
	}
}

func TestScheduleInteractionDispatcherRunningReturnsSafeToast(t *testing.T) {
	resolver := &scheduleInteractionResolverFake{err: agentruntime.ErrScheduleInteractionRunning}
	dispatcher := mustScheduleInteractionDispatcher(t, resolver)
	event := scheduleRuntimeActionEvent(cardactionproto.ActionScheduleEditConfirm)
	parsed, _ := cardactionproto.Parse(event)

	response, err := dispatcher.Dispatch(context.Background(), ContinuationRequest{Event: event, Action: parsed})
	if err != nil {
		t.Fatalf("Dispatch(running) error = %v", err)
	}
	if response == nil || response.Toast == nil ||
		!strings.Contains(response.Toast.Content, "处理中") ||
		strings.Contains(response.Toast.Content, agentruntime.ErrScheduleInteractionRunning.Error()) {
		t.Fatalf("running response = %#v", response)
	}
}

func TestNewScheduleInteractionDispatcherRejectsTypedNilResolver(t *testing.T) {
	var resolver *scheduleInteractionResolverFake
	if _, err := NewScheduleInteractionDispatcher(resolver, ScheduleInteractionDispatcherOptions{}); err == nil {
		t.Fatal("NewScheduleInteractionDispatcher(typed nil) error = nil")
	}
}

func mustScheduleInteractionDispatcher(
	t *testing.T,
	resolver scheduleInteractionResolver,
) *ScheduleInteractionDispatcher {
	t.Helper()
	dispatcher, err := NewScheduleInteractionDispatcher(resolver, ScheduleInteractionDispatcherOptions{
		Now:        func() time.Time { return time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC) },
		NewClaimID: func() string { return "claim-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func scheduleRuntimeActionEvent(action string) *callback.CardActionTriggerEvent {
	event := runtimeActionEvent(action)
	event.EventV2Base = &larkevent.EventV2Base{
		Header: &larkevent.EventHeader{EventID: "event-1"},
	}
	event.Event.Operator = &callback.Operator{OpenID: "ou-actor"}
	event.Event.Context = &callback.Context{
		OpenMessageID: "om-card-1",
		OpenChatID:    "oc-untrusted",
	}
	event.Event.Action.Value[cardactionproto.InteractionKindField] = "schedule_edit"
	return event
}
