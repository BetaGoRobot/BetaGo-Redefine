package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

func TestBinderPersistsOnlyHashedTokenAndServerTrustedActions(t *testing.T) {
	store := &recordingInteractionStore{}
	compiler := &recordingArtifactCompiler{}
	binder, err := NewBinder(BinderOptions{
		Store:      store,
		Compiler:   compiler,
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
		Now:        func() time.Time { return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC) },
		Policy:     PolicyConfig{},
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	capability, err := NewTrustedCapability(
		"schedule.update",
		json.RawMessage(`{"task_id":"trusted-task","name":"trusted-name"}`),
	)
	if err != nil {
		t.Fatalf("NewTrustedCapability() error = %v", err)
	}

	result, err := binder.BindAndBegin(context.Background(), BindRequest{
		RunID: "run-1", ExpectedRunRevision: 4, ChatID: "chat-1",
		ReplyToMessageID: "message-1", ExpectedActorOpenID: "owner-1",
		InteractionKind: "agent_card", IdempotencyKey: "compose-1",
		ExpiresAt: time.Date(2026, 7, 30, 10, 10, 0, 0, time.UTC),
		Spec:      binderSpec(),
		TrustedCapabilities: map[string]TrustedCapability{
			"confirm": capability,
		},
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: "run-1",
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	})
	if err != nil {
		t.Fatalf("BindAndBegin() error = %v", err)
	}
	if result.Surface.ID == "" || result.Surface.InteractionID == "" ||
		result.Surface.Revision != 5 {
		t.Fatalf("result surface = %#v", result.Surface)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	persisted, _ := json.Marshal(store.request)
	if strings.Contains(string(persisted), compiler.plaintextToken) {
		t.Fatalf("begin request persisted plaintext token: %s", persisted)
	}
	if len(store.request.TokenHash) != 64 {
		t.Fatalf("token hash length = %d", len(store.request.TokenHash))
	}
	if strings.Contains(store.request.SpecJSON, "trusted-task") ||
		strings.Contains(store.request.CompiledJSONRedacted, compiler.plaintextToken) {
		t.Fatal("public artifacts contain trusted capability or plaintext token")
	}
	if !strings.Contains(string(store.request.TrustedInput), "trusted-task") ||
		!strings.Contains(string(store.request.TrustedInput), `"actor_policy"`) {
		t.Fatalf("trusted wait input = %s", store.request.TrustedInput)
	}
	if !strings.Contains(string(result.CompiledJSON), compiler.plaintextToken) {
		t.Fatalf("immediate compiled artifact did not contain runtime token: %s", result.CompiledJSON)
	}
}

func TestBinderDerivesStableInteractionForComposeReplay(t *testing.T) {
	store := &recordingInteractionStore{}
	compiler := &recordingArtifactCompiler{}
	binder, err := NewBinder(BinderOptions{
		Store: store, Compiler: compiler,
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	capability, err := NewTrustedCapability(
		"schedule.update",
		json.RawMessage(`{"task_id":"trusted-task"}`),
	)
	if err != nil {
		t.Fatalf("NewTrustedCapability() error = %v", err)
	}
	request := BindRequest{
		RunID: "run-1", ExpectedRunRevision: 1, ChatID: "chat-1",
		ExpectedActorOpenID: "owner-1",
		InteractionKind:     "agent_card", IdempotencyKey: "compose-replay",
		ExpiresAt: time.Now().UTC().Add(time.Hour), Spec: binderSpec(),
		TrustedCapabilities: map[string]TrustedCapability{"confirm": capability},
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: "run-1",
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	}
	first, err := binder.BindAndBegin(context.Background(), request)
	if err != nil {
		t.Fatalf("first BindAndBegin() error = %v", err)
	}
	firstRequest := store.request
	second, err := binder.BindAndBegin(context.Background(), request)
	if err != nil {
		t.Fatalf("second BindAndBegin() error = %v", err)
	}
	if first.Surface.ID != second.Surface.ID ||
		first.Surface.InteractionID != second.Surface.InteractionID ||
		firstRequest.StepID != store.request.StepID ||
		firstRequest.TokenHash != store.request.TokenHash {
		t.Fatalf("compose replay was unstable: first=%#v second=%#v", firstRequest, store.request)
	}
}

func TestBinderRejectsInvalidOrPolicyDeniedSpecBeforePersistence(t *testing.T) {
	store := &recordingInteractionStore{}
	binder, err := NewBinder(BinderOptions{
		Store: store, Compiler: &recordingArtifactCompiler{},
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	spec := binderSpec()
	spec.Blocks = append(spec.Blocks, TextInput(
		"secret", InputField{
			FieldID: "password", FormID: "form", Label: "密码",
		}, TextInputConfig{},
	))
	_, err = binder.BindAndBegin(context.Background(), BindRequest{
		RunID: "run-1", ExpectedRunRevision: 1, ChatID: "chat-1",
		ExpectedActorOpenID: "owner-1",
		InteractionKind:     "agent_card", IdempotencyKey: "compose-invalid",
		ExpiresAt: time.Now().UTC().Add(time.Hour), Spec: spec,
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: "run-1",
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	})
	if err == nil || !errors.Is(err, ErrCardPolicyDenied) {
		t.Fatalf("BindAndBegin() error = %v, want policy denied", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestBinderDoesNotExposeTokenFromCompilerError(t *testing.T) {
	compiler := &tokenLeakingCompiler{}
	binder, err := NewBinder(BinderOptions{
		Store: &recordingInteractionStore{}, Compiler: compiler,
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	capability, err := NewTrustedCapability(
		"schedule.update",
		json.RawMessage(`{"task_id":"trusted-task"}`),
	)
	if err != nil {
		t.Fatalf("NewTrustedCapability() error = %v", err)
	}
	_, err = binder.BindAndBegin(context.Background(), BindRequest{
		RunID: "run-1", ExpectedRunRevision: 1, ChatID: "chat-1",
		ExpectedActorOpenID: "owner-1", InteractionKind: "agent_card",
		IdempotencyKey: "compose-error", ExpiresAt: time.Now().UTC().Add(time.Hour),
		Spec:                binderSpec(),
		TrustedCapabilities: map[string]TrustedCapability{"confirm": capability},
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: "run-1",
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	})
	if err == nil {
		t.Fatal("BindAndBegin() unexpectedly succeeded")
	}
	if compiler.token == "" {
		t.Fatal("leaking compiler did not observe a token")
	}
	if strings.Contains(err.Error(), compiler.token) {
		t.Fatalf("compiler error exposed plaintext token: %v", err)
	}
}

func binderSpec() CardSpec {
	return CardSpec{
		Version: VersionV1, Title: "确认修改",
		Blocks: []Block{Markdown("summary", "请确认")},
		Actions: []Action{{
			Kind: ActionButton, ID: "confirm", Label: "确认",
			Mode: ActionModeCapabilityConfirm, Intent: "schedule.update",
		}},
	}
}

type recordingInteractionStore struct {
	calls   int
	request BeginCardInteractionRequest
}

func (s *recordingInteractionStore) BeginCardInteraction(
	_ context.Context,
	request BeginCardInteractionRequest,
) (*CardSurface, error) {
	s.calls++
	s.request = request
	return request.Surface(), nil
}

type recordingArtifactCompiler struct {
	plaintextToken string
}

type tokenLeakingCompiler struct {
	token string
}

func (c *tokenLeakingCompiler) CompileJSON(
	bound *BoundCardSpec,
) (json.RawMessage, error) {
	payload, err := bound.CallbackPayload(bound.Spec().Actions[0])
	if err != nil {
		return nil, err
	}
	c.token, _ = payload["token"].(string)
	return nil, errors.New("compiler leaked " + c.token)
}

func (c *tokenLeakingCompiler) CompileRedactedJSON(
	*BoundCardSpec,
) (json.RawMessage, error) {
	return nil, errors.New("not reached")
}

func (c *recordingArtifactCompiler) CompileJSON(bound *BoundCardSpec) (json.RawMessage, error) {
	action := bound.Spec().Actions[0]
	payload, err := bound.CallbackPayload(action)
	if err != nil {
		return nil, err
	}
	c.plaintextToken, _ = payload["token"].(string)
	return json.Marshal(map[string]any{"callback": payload})
}

func (c *recordingArtifactCompiler) CompileRedactedJSON(
	bound *BoundCardSpec,
) (json.RawMessage, error) {
	encoded, err := c.CompileJSON(bound)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(strings.ReplaceAll(
		string(encoded),
		c.plaintextToken,
		"[REDACTED]",
	)), nil
}
