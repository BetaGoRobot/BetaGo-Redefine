package agentcard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

const maxCapabilityPayloadBytes = 64 * 1024

type CapabilityPermissionContext struct {
	Scope       string `json:"scope"`
	ChatID      string `json:"chat_id"`
	ActorOpenID string `json:"actor_open_id,omitempty"`
}

type ResultProjectionPolicy struct {
	ContinueAgent       bool          `json:"continue_agent"`
	SuccessSurfaceState SurfaceStatus `json:"success_surface_status"`
	FailureSurfaceState SurfaceStatus `json:"failure_surface_status"`
}

type CapabilityInvocation struct {
	Name           string                      `json:"name"`
	Version        string                      `json:"version"`
	Input          json.RawMessage             `json:"input"`
	IdempotencyKey string                      `json:"idempotency_key"`
	Permission     CapabilityPermissionContext `json:"permission"`
	ActorPolicy    ActorPolicy                 `json:"actor_policy"`
	ResultPolicy   ResultProjectionPolicy      `json:"result_policy"`
}

type CapabilityExecutionRequest struct {
	StepID        string
	RunID         string
	SourceStepID  string
	InteractionID string
	ActionID      string
	Lease         agentruntime.StepLease
	Invocation    CapabilityInvocation
	StartedAt     time.Time
}

type CapabilityExecutionState struct {
	Terminal    bool
	Invocation  *CapabilityInvocation
	SurfaceSpec *CardSpec
}

type CapabilityExecutionCompletion struct {
	Request              CapabilityExecutionRequest
	Succeeded            bool
	Output               json.RawMessage
	ErrorText            string
	CompiledJSONRedacted string
	FinishedAt           time.Time
}

type CapabilityExecutionStore interface {
	BeginCapabilityExecution(
		context.Context,
		CapabilityExecutionRequest,
	) (CapabilityExecutionState, error)
	CompleteCapabilityExecution(
		context.Context,
		CapabilityExecutionCompletion,
	) error
}

type CapabilityExecutor interface {
	Execute(context.Context, CapabilityInvocation) (json.RawMessage, error)
}

type CapabilityServiceOptions struct {
	Store    CapabilityExecutionStore
	Executor CapabilityExecutor
	Compiler ArtifactCompiler
	Now      func() time.Time
}

type CapabilityService struct {
	store    CapabilityExecutionStore
	executor CapabilityExecutor
	compiler ArtifactCompiler
	now      func() time.Time
}

func NewCapabilityService(options CapabilityServiceOptions) (*CapabilityService, error) {
	if options.Store == nil || options.Executor == nil {
		return nil, errors.New("capability execution store and executor are required")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CapabilityService{
		store: options.Store, executor: options.Executor,
		compiler: options.Compiler, now: options.Now,
	}, nil
}

func (s *CapabilityService) ProcessCapabilityStep(
	ctx context.Context,
	step *agentruntime.AgentStep,
	lease agentruntime.StepLease,
) error {
	if s == nil || s.store == nil || s.executor == nil || step == nil {
		return errors.New("capability service is not configured")
	}
	decoded, err := decodeCapabilityStep(step)
	if err != nil {
		return err
	}
	if lease.StepID != step.ID || lease.WorkerID != step.WorkerID ||
		lease.AttemptCount != step.AttemptCount || lease.LeaseTTL <= 0 ||
		lease.Now.IsZero() {
		return agentruntime.ErrLeaseLost
	}
	request := CapabilityExecutionRequest{
		StepID: step.ID, RunID: step.RunID,
		SourceStepID:  decoded.SourceStepID,
		InteractionID: decoded.InteractionID, ActionID: decoded.ActionID,
		Lease: lease, Invocation: decoded.Invocation, StartedAt: s.now().UTC(),
	}
	state, err := s.store.BeginCapabilityExecution(ctx, request)
	if err != nil {
		return err
	}
	if state.Terminal {
		return nil
	}
	if state.Invocation != nil {
		request.Invocation = cloneCapabilityInvocation(*state.Invocation)
	}
	var successCard, failureCard json.RawMessage
	if state.SurfaceSpec != nil {
		if s.compiler == nil {
			return errors.New("capability terminal card compiler is not configured")
		}
		successCard, err = compileCapabilityTerminalCard(
			s.compiler,
			*state.SurfaceSpec,
			SurfaceStatusResolved,
		)
		if err != nil {
			return err
		}
		failureCard, err = compileCapabilityTerminalCard(
			s.compiler,
			*state.SurfaceSpec,
			SurfaceStatusFailed,
		)
		if err != nil {
			return err
		}
	}
	output, executeErr := s.executor.Execute(ctx, cloneCapabilityInvocation(request.Invocation))
	completion := CapabilityExecutionCompletion{
		Request: request, Succeeded: executeErr == nil, FinishedAt: s.now().UTC(),
	}
	if executeErr != nil {
		completion.ErrorText = boundedCapabilityError(executeErr.Error())
		completion.Output = json.RawMessage(`{}`)
	} else {
		if len(output) == 0 {
			output = json.RawMessage(`{}`)
		}
		if len(output) > maxCapabilityPayloadBytes || !json.Valid(output) {
			completion.ErrorText = "capability returned an invalid result document"
			completion.Output = json.RawMessage(`{}`)
			completion.Succeeded = false
		} else {
			completion.Output = append(json.RawMessage(nil), output...)
		}
	}
	if completion.Succeeded {
		completion.CompiledJSONRedacted = string(successCard)
	} else {
		completion.CompiledJSONRedacted = string(failureCard)
	}
	return s.store.CompleteCapabilityExecution(ctx, completion)
}

func compileCapabilityTerminalCard(
	compiler ArtifactCompiler,
	spec CardSpec,
	status SurfaceStatus,
) (json.RawMessage, error) {
	state, err := lifecycleStateForStatus(status)
	if err != nil {
		return nil, err
	}
	bound, err := NewBoundCardSpec(spec, state, nil)
	if err != nil {
		return nil, err
	}
	compiled, err := compiler.CompileRedactedJSON(bound)
	if err != nil || !json.Valid(compiled) || jsonDocumentContainsToken(compiled) {
		return nil, ErrCardCompileFailed
	}
	return compiled, nil
}

func DecodeCapabilityInvocation(
	step *agentruntime.AgentStep,
) (CapabilityInvocation, error) {
	decoded, err := decodeCapabilityStep(step)
	if err != nil {
		return CapabilityInvocation{}, err
	}
	return cloneCapabilityInvocation(decoded.Invocation), nil
}

type capabilityStepInput struct {
	Version       int                     `json:"version"`
	SourceStepID  string                  `json:"source_step_id"`
	InteractionID string                  `json:"interaction_id"`
	ActionID      string                  `json:"action_id"`
	Descriptor    TrustedActionDescriptor `json:"descriptor"`
}

type decodedCapabilityStep struct {
	SourceStepID  string
	InteractionID string
	ActionID      string
	Invocation    CapabilityInvocation
}

func decodeCapabilityStep(step *agentruntime.AgentStep) (decodedCapabilityStep, error) {
	if step == nil || step.Kind != agentruntime.StepKindCapabilityCall ||
		strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.RunID) == "" ||
		len(step.InputJSON) == 0 || len(step.InputJSON) > maxCapabilityPayloadBytes {
		return decodedCapabilityStep{}, errors.New("invalid capability step")
	}
	decoder := json.NewDecoder(io.LimitReader(
		bytes.NewBufferString(step.InputJSON),
		maxCapabilityPayloadBytes+1,
	))
	decoder.DisallowUnknownFields()
	var input capabilityStepInput
	if err := decoder.Decode(&input); err != nil {
		return decodedCapabilityStep{}, fmt.Errorf("decode capability step: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return decodedCapabilityStep{}, errors.New("capability step must contain one JSON document")
	}
	descriptor := input.Descriptor
	if input.Version != 1 || strings.TrimSpace(input.SourceStepID) == "" ||
		strings.TrimSpace(input.InteractionID) == "" ||
		strings.TrimSpace(input.ActionID) == "" ||
		input.ActionID != descriptor.ActionID ||
		step.CapabilityName != descriptor.CapabilityName ||
		descriptor.Mode != ActionModeCapabilityConfirm ||
		!descriptor.ContinueAgent ||
		descriptor.Intent == "" ||
		descriptor.CapabilityName == "" ||
		descriptor.CapabilityVersion == "" ||
		descriptor.IdempotencyKey == "" ||
		!strings.HasPrefix(descriptor.IdempotencyKey, "cardcap_") ||
		descriptor.PermissionContext.Scope != descriptor.CapabilityName ||
		descriptor.PermissionContext.ChatID == "" ||
		descriptor.ActorPolicy.Mode == "" ||
		descriptor.ResultProjectionPolicy.SuccessSurfaceState != SurfaceStatusResolved ||
		descriptor.ResultProjectionPolicy.FailureSurfaceState != SurfaceStatusFailed ||
		!descriptor.ResultProjectionPolicy.ContinueAgent {
		return decodedCapabilityStep{}, errors.New("invalid trusted capability descriptor")
	}
	if descriptor.ActorPolicy.Mode == ActorPolicyOwner {
		if descriptor.ActorPolicy.OpenID == "" ||
			descriptor.PermissionContext.ActorOpenID != descriptor.ActorPolicy.OpenID {
			return decodedCapabilityStep{}, errors.New("owner capability actor does not match permission context")
		}
	} else if descriptor.ActorPolicy.Mode != ActorPolicyAnyMember ||
		descriptor.ActorPolicy.OpenID != "" ||
		descriptor.PermissionContext.ActorOpenID != "" {
		return decodedCapabilityStep{}, errors.New("invalid capability actor policy")
	}
	if len(descriptor.CapabilityInput) == 0 ||
		len(descriptor.CapabilityInput) > maxCapabilityPayloadBytes ||
		!json.Valid(descriptor.CapabilityInput) {
		return decodedCapabilityStep{}, errors.New("invalid trusted capability input")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(descriptor.CapabilityInput, &object) != nil || object == nil {
		return decodedCapabilityStep{}, errors.New("trusted capability input must be an object")
	}
	return decodedCapabilityStep{
		SourceStepID:  input.SourceStepID,
		InteractionID: input.InteractionID,
		ActionID:      input.ActionID,
		Invocation: CapabilityInvocation{
			Name: descriptor.CapabilityName, Version: descriptor.CapabilityVersion,
			Input:          append(json.RawMessage(nil), descriptor.CapabilityInput...),
			IdempotencyKey: descriptor.IdempotencyKey,
			Permission:     descriptor.PermissionContext,
			ActorPolicy:    descriptor.ActorPolicy,
			ResultPolicy:   descriptor.ResultProjectionPolicy,
		},
	}, nil
}

func cloneCapabilityInvocation(input CapabilityInvocation) CapabilityInvocation {
	input.Input = append(json.RawMessage(nil), input.Input...)
	return input
}

func boundedCapabilityError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "capability execution failed"
	}
	if len(value) > 4096 {
		return value[:4096]
	}
	return value
}
