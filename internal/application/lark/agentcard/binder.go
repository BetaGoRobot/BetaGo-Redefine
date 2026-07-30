package agentcard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

const binderKeyMinBytes = 32

type ArtifactCompiler interface {
	CompileJSON(*BoundCardSpec) (json.RawMessage, error)
	CompileRedactedJSON(*BoundCardSpec) (json.RawMessage, error)
}

type BinderOptions struct {
	Store      InteractionStore
	Compiler   ArtifactCompiler
	BindingKey []byte
	Policy     PolicyConfig
	Now        func() time.Time
}

type Binder struct {
	store      InteractionStore
	compiler   ArtifactCompiler
	bindingKey []byte
	policy     PolicyConfig
	now        func() time.Time
}

type TrustedCapability struct {
	name  string
	input json.RawMessage
}

func NewTrustedCapability(name string, input json.RawMessage) (TrustedCapability, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TrustedCapability{}, fmt.Errorf("capability name is required")
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if !json.Valid(input) {
		return TrustedCapability{}, fmt.Errorf("capability input must be valid JSON")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(input, &object) != nil || object == nil {
		return TrustedCapability{}, fmt.Errorf("capability input must be a JSON object")
	}
	return TrustedCapability{
		name: name, input: append(json.RawMessage(nil), input...),
	}, nil
}

type BindRequest struct {
	RunID               string
	ExpectedRunRevision int64
	ChatID              string
	ReplyToMessageID    string
	ExpectedActorOpenID string
	ActorPolicy         ActorPolicyMode
	InteractionKind     string
	IdempotencyKey      string
	ExpiresAt           time.Time
	Spec                CardSpec
	TrustedCapabilities map[string]TrustedCapability
	Projection          agentruntime.ProjectionDocument
}

type BindResult struct {
	Surface      *CardSurface
	CompiledJSON json.RawMessage
}

type cardContractError struct {
	kind   error
	issues []ValidationIssue
}

func (e *cardContractError) Error() string {
	return fmt.Sprintf("%s (%d issues)", e.kind.Error(), len(e.issues))
}

func (e *cardContractError) Unwrap() error { return e.kind }

func (e *cardContractError) Issues() []ValidationIssue {
	return append([]ValidationIssue(nil), e.issues...)
}

func NewBinder(options BinderOptions) (*Binder, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("agent card interaction store is required")
	}
	if options.Compiler == nil {
		return nil, fmt.Errorf("agent card compiler is required")
	}
	if len(options.BindingKey) < binderKeyMinBytes {
		return nil, fmt.Errorf("agent card binding key must contain at least %d bytes", binderKeyMinBytes)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Binder{
		store: options.Store, compiler: options.Compiler,
		bindingKey: append([]byte(nil), options.BindingKey...),
		policy:     options.Policy, now: now,
	}, nil
}

func (b *Binder) BindAndBegin(
	ctx context.Context,
	request BindRequest,
) (*BindResult, error) {
	if err := request.validate(b.now()); err != nil {
		return nil, err
	}
	if issues := ValidateCardSpec(request.Spec); len(issues) != 0 {
		return nil, &cardContractError{kind: ErrCardValidationFailed, issues: issues}
	}
	if issues := CheckPolicy(request.Spec, b.policy); len(issues) != 0 {
		return nil, &cardContractError{kind: ErrCardPolicyDenied, issues: issues}
	}
	specJSON, err := json.Marshal(request.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal card spec: %w", err)
	}
	specDigest := sha256.Sum256(specJSON)
	ids := b.deriveIdentifiers(request.RunID, request.IdempotencyKey)
	revision := request.ExpectedRunRevision + 1
	token := b.deriveSecret("token", ids.interactionID)
	tokenHash := agentruntime.HashInteractionToken(token)
	descriptors, bindings, err := buildBindings(request, ids, revision, token)
	if err != nil {
		return nil, err
	}
	bound, err := NewBoundCardSpec(request.Spec, LifecycleInteractive, bindings)
	if err != nil {
		return nil, err
	}
	compiled, err := b.compiler.CompileJSON(bound)
	if err != nil {
		return nil, ErrCardCompileFailed
	}
	redacted, err := b.compiler.CompileRedactedJSON(bound)
	if err != nil {
		return nil, ErrCardCompileFailed
	}
	if !json.Valid(compiled) || !json.Valid(redacted) {
		return nil, fmt.Errorf("compiler returned invalid JSON")
	}
	if strings.Contains(string(redacted), token) {
		return nil, fmt.Errorf("compiler failed to redact runtime token")
	}
	policy := ActorPolicy{Mode: request.ActorPolicy, OpenID: request.ExpectedActorOpenID}
	if policy.Mode == "" {
		policy.Mode = ActorPolicyOwner
	}
	trustedInput, err := json.Marshal(TrustedWaitInput{
		Version: 1, ComposeKey: request.IdempotencyKey,
		SpecDigest:  hex.EncodeToString(specDigest[:]),
		ActorPolicy: policy, ActionBindings: descriptors,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal trusted wait input: %w", err)
	}
	persist := BeginCardInteractionRequest{
		SurfaceID: ids.surfaceID, RunID: request.RunID, StepID: ids.stepID,
		InteractionID: ids.interactionID, IdempotencyKey: request.IdempotencyKey,
		ExpectedRunRevision: request.ExpectedRunRevision, Revision: revision,
		TokenHash: tokenHash, InteractionKind: request.InteractionKind,
		ExpiresAt: request.ExpiresAt, ExpectedActorOpenID: request.ExpectedActorOpenID,
		ChatID: request.ChatID, ReplyToMessageID: request.ReplyToMessageID,
		SpecVersion: request.Spec.Version, SpecJSON: string(specJSON),
		CompiledJSONRedacted: string(redacted), TrustedInput: trustedInput,
		Projection: request.Projection,
	}
	surface, err := b.store.BeginCardInteraction(ctx, persist)
	if err != nil {
		return nil, err
	}
	return &BindResult{
		Surface:      surface,
		CompiledJSON: append(json.RawMessage(nil), compiled...),
	}, nil
}

func (r BindRequest) validate(now time.Time) error {
	for name, value := range map[string]string{
		"run_id": r.RunID, "chat_id": r.ChatID,
		"interaction_kind": r.InteractionKind, "idempotency_key": r.IdempotencyKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if r.ExpectedRunRevision < 0 {
		return fmt.Errorf("expected run revision must not be negative")
	}
	if r.ExpiresAt.IsZero() || !r.ExpiresAt.After(now) {
		return fmt.Errorf("interaction expiry must be in the future")
	}
	switch r.ActorPolicy {
	case "", ActorPolicyOwner:
		if strings.TrimSpace(r.ExpectedActorOpenID) == "" {
			return fmt.Errorf("owner actor policy requires expected actor open id")
		}
	case ActorPolicyAnyMember:
		if r.ExpectedActorOpenID != "" {
			return fmt.Errorf("any-member actor policy cannot pin an actor")
		}
	default:
		return fmt.Errorf("unknown actor policy %q", r.ActorPolicy)
	}
	return r.Projection.Validate()
}

func buildBindings(
	request BindRequest,
	ids derivedIdentifiers,
	revision int64,
	token string,
) ([]TrustedActionDescriptor, map[string]RuntimeBinding, error) {
	descriptors := make([]TrustedActionDescriptor, 0, len(request.Spec.Actions))
	bindings := make(map[string]RuntimeBinding, len(request.Spec.Actions))
	for _, action := range request.Spec.Actions {
		capability, hasCapability := request.TrustedCapabilities[action.ID]
		if action.Mode == ActionModeCapabilityConfirm && !hasCapability {
			return nil, nil, fmt.Errorf(
				"capability confirmation action %q has no trusted server binding",
				action.ID,
			)
		}
		if action.Mode != ActionModeCapabilityConfirm && hasCapability {
			return nil, nil, fmt.Errorf(
				"action %q cannot receive a trusted capability binding",
				action.ID,
			)
		}
		descriptor := TrustedActionDescriptor{
			ActionID: action.ID, Mode: action.Mode, Intent: action.Intent,
			ContinueAgent: action.Mode != ActionModeServer,
		}
		if hasCapability {
			descriptor.CapabilityName = capability.name
			descriptor.CapabilityInput = append(
				json.RawMessage(nil),
				capability.input...,
			)
		}
		descriptors = append(descriptors, descriptor)
		bindings[action.ID] = RuntimeBinding{
			RunID: request.RunID, StepID: ids.stepID,
			InteractionID: ids.interactionID, Revision: revision,
			Token: token, InteractionKind: request.InteractionKind,
			TrustedCapability: append(json.RawMessage(nil), capability.input...),
		}
	}
	for actionID := range request.TrustedCapabilities {
		found := false
		for _, action := range request.Spec.Actions {
			if action.ID == actionID {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf(
				"trusted capability references unknown action %q",
				actionID,
			)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].ActionID < descriptors[j].ActionID
	})
	return descriptors, bindings, nil
}

type derivedIdentifiers struct {
	surfaceID     string
	stepID        string
	interactionID string
}

func (b *Binder) deriveIdentifiers(runID, idempotencyKey string) derivedIdentifiers {
	seed := runID + "\x00" + idempotencyKey
	return derivedIdentifiers{
		surfaceID:     "card_surface_" + b.deriveHex("surface", seed),
		stepID:        "step_card_wait_" + b.deriveHex("step", seed),
		interactionID: "interaction_card_" + b.deriveHex("interaction", seed),
	}
}

func (b *Binder) deriveHex(purpose, value string) string {
	mac := hmac.New(sha256.New, b.bindingKey)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (b *Binder) deriveSecret(purpose, value string) string {
	mac := hmac.New(sha256.New, b.bindingKey)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
