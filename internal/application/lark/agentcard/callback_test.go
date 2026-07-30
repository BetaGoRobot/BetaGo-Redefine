package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestCallbackDispatcherClaimsCompleteEnvelopeAndReturnsFastToast(t *testing.T) {
	spec := callbackSpec()
	specJSON, _ := json.Marshal(spec)
	store := &callbackStoreFake{surface: &CardSurface{
		ID: "surface-1", RunID: "run-1", WaitStepID: "step-1",
		InteractionID: "interaction-1", ChatID: "chat-1",
		MessageID: "message-1", SpecJSON: string(specJSON),
		Status: SurfaceStatusSent, Revision: 3,
	}}
	dispatcher, err := NewCallbackDispatcher(CallbackDispatcherOptions{
		Store: store, Compiler: &terminalArtifactCompiler{},
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewCallbackDispatcher() error = %v", err)
	}
	action := callbackParsed()
	response, err := dispatcher.Dispatch(
		context.Background(),
		appcardaction.ContinuationRequest{Action: action},
	)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if response == nil || response.Toast == nil ||
		response.Toast.Content != "已提交" || response.Card == nil ||
		store.claimCalls != 1 {
		t.Fatalf("response=%#v store=%#v", response, store)
	}
	if store.request.ActionID != "submit" ||
		store.request.ActorOpenID != "actor-1" ||
		store.request.MessageID != "message-1" ||
		store.request.DesiredStatus != SurfaceStatusSubmitted ||
		strings.Contains(store.request.CompiledJSONRedacted, "token") {
		t.Fatalf("claim request = %#v", store.request)
	}
}

func TestCallbackDispatcherRejectsSurfaceMismatchBeforeClaim(t *testing.T) {
	specJSON, _ := json.Marshal(callbackSpec())
	store := &callbackStoreFake{surface: &CardSurface{
		ID: "surface-1", RunID: "run-1", WaitStepID: "step-1",
		InteractionID: "interaction-1", ChatID: "chat-1",
		MessageID: "different-message", SpecJSON: string(specJSON),
		Status: SurfaceStatusSent, Revision: 3,
	}}
	dispatcher, _ := NewCallbackDispatcher(CallbackDispatcherOptions{
		Store: store, Compiler: &terminalArtifactCompiler{},
	})
	if _, err := dispatcher.Dispatch(
		context.Background(),
		appcardaction.ContinuationRequest{Action: callbackParsed()},
	); !errors.Is(err, ErrCardConflict) {
		t.Fatalf("Dispatch() error = %v, want conflict", err)
	}
	if store.claimCalls != 0 {
		t.Fatalf("claim calls = %d, want 0", store.claimCalls)
	}
}

func TestNormalizeActionOutcomeValidatesWhitelistRequiredAndOptions(t *testing.T) {
	spec := callbackSpec()
	outcome, err := NormalizeActionOutcome(
		spec,
		"submit",
		map[string]any{"reason": "approved", "choices": []any{"b", "a"}},
		"",
		"",
		"",
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("NormalizeActionOutcome() error = %v", err)
	}
	if !strings.Contains(string(outcome), `"choices":["a","b"]`) {
		t.Fatalf("normalized outcome = %s", outcome)
	}
	for name, values := range map[string]map[string]any{
		"unknown field":    {"reason": "approved", "forged": "secret"},
		"missing required": {"choices": []string{"a"}},
		"forged option":    {"reason": "approved", "choices": []string{"forged"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeActionOutcome(
				spec,
				"submit",
				values,
				"",
				"",
				"",
				nil,
				false,
			); !errors.Is(err, ErrCardConflict) {
				t.Fatalf("NormalizeActionOutcome() error = %v", err)
			}
		})
	}
}

func TestNormalizeCancelIsFirstClassAndDropsFormValues(t *testing.T) {
	spec := callbackSpec()
	outcome, err := NormalizeActionOutcome(
		spec,
		"cancel",
		map[string]any{"forged": "ignored"},
		"",
		"",
		"",
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("NormalizeActionOutcome(cancel) error = %v", err)
	}
	if !strings.Contains(string(outcome), `"action_kind":"cancel"`) ||
		!strings.Contains(string(outcome), `"form_values":{}`) {
		t.Fatalf("cancel outcome = %s", outcome)
	}
}

func callbackSpec() CardSpec {
	return CardSpec{
		Version: VersionV1, Title: "提交",
		Blocks: []Block{
			TextInput("reason_input", InputField{
				FieldID: "reason", FormID: "form", Label: "原因", Required: true,
			}, TextInputConfig{MinLength: 2, MaxLength: 20}),
			MultiSelect("choices_input", InputField{
				FieldID: "choices", FormID: "form", Label: "选择",
			}, []SelectOption{{Label: "A", Value: "a"}, {Label: "B", Value: "b"}}),
		},
		Actions: []Action{
			{
				Kind: ActionSubmit, ID: "submit", Label: "提交",
				Mode: ActionModeUI, Intent: "submit_form", FormRef: "form",
			},
			{
				Kind: ActionCancel, ID: "cancel", Label: "取消",
				Mode: ActionModeUI, Intent: "cancel",
			},
		},
	}
}

func callbackParsed() *cardactionproto.Parsed {
	return &cardactionproto.Parsed{
		Name: cardactionproto.ActionAgentRuntimeResume,
		Runtime: &cardactionproto.RuntimeEnvelope{
			RunID: "run-1", StepID: "step-1", InteractionID: "interaction-1",
			Revision: 3, Token: "opaque-token", InteractionKind: "agent_card",
			ContinueAgent: true, ActionID: "submit",
		},
		FormValue: map[string]any{"reason": "approved", "choices": []any{"a"}},
		Source: cardactionproto.CallbackSource{
			EventID: "event-1", MessageID: "message-1", ChatID: "chat-1",
			OperatorOpenID: "actor-1",
		},
	}
}

type callbackStoreFake struct {
	surface    *CardSurface
	claimCalls int
	request    ClaimActionRequest
}

func (s *callbackStoreFake) GetByInteraction(
	context.Context,
	GetSurfaceRequest,
) (*CardSurface, error) {
	return s.surface, nil
}

func (s *callbackStoreFake) ClaimAction(
	_ context.Context,
	request ClaimActionRequest,
) (*ActionClaim, error) {
	s.claimCalls++
	s.request = request
	return &ActionClaim{
		Surface: s.surface,
		Descriptor: TrustedActionDescriptor{
			ActionID: request.ActionID, Mode: ActionModeUI,
			Intent: "submit_form", ContinueAgent: true,
		},
		Outcome: json.RawMessage(`{"status":"submitted"}`),
	}, nil
}
