package agentcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

var (
	ErrAuthoringNotAllowed       = errors.New("agent card authoring is not allowed for this chat")
	ErrTrustedCapabilityRequired = errors.New("agent card capability confirmation requires a trusted server binding")
)

type AuthoringRunResolver interface {
	ResolveAuthoringRun(
		context.Context,
		agentcardtool.ComposeContext,
		string,
	) (*agentruntime.AgentRun, error)
}

type AuthoringDelivery interface {
	ComposeAndSend(context.Context, BindRequest) (*CardSurface, error)
}

type RolloutAuthoringComposerOptions struct {
	Compiler        ArtifactCompiler
	RunResolver     AuthoringRunResolver
	Delivery        AuthoringDelivery
	ProjectionIndex string
	Shadow          bool
	CanSend         func(string) bool
}

type RolloutAuthoringComposer struct {
	compiler        ArtifactCompiler
	runResolver     AuthoringRunResolver
	delivery        AuthoringDelivery
	projectionIndex string
	shadow          bool
	canSend         func(string) bool
}

func NewRolloutAuthoringComposer(
	options RolloutAuthoringComposerOptions,
) (*RolloutAuthoringComposer, error) {
	if options.Compiler == nil {
		return nil, errors.New("agent card authoring compiler is required")
	}
	if strings.TrimSpace(options.ProjectionIndex) == "" {
		return nil, errors.New("agent card projection index is required")
	}
	if !options.Shadow &&
		(options.RunResolver == nil || options.Delivery == nil ||
			options.CanSend == nil) {
		return nil, errors.New("agent card delivery dependencies are required")
	}
	return &RolloutAuthoringComposer{
		compiler: options.Compiler, runResolver: options.RunResolver,
		delivery:        options.Delivery,
		projectionIndex: strings.TrimSpace(options.ProjectionIndex),
		shadow:          options.Shadow, canSend: options.CanSend,
	}, nil
}

func (c *RolloutAuthoringComposer) Compose(
	ctx context.Context,
	request AuthoringComposeRequest,
) (*CardSurface, error) {
	if c == nil || c.compiler == nil {
		return nil, errors.New("agent card authoring composer is not configured")
	}
	if c.shadow {
		return c.compileShadow(request)
	}
	if c.canSend == nil || !c.canSend(request.Context.ChatID) {
		return nil, ErrAuthoringNotAllowed
	}
	if request.InteractionMode == ActionModeCapabilityConfirm {
		// The public DSL intentionally contains no executable arguments. A
		// capability-confirm card must first be enriched by a trusted,
		// server-side capability adapter.
		return nil, ErrTrustedCapabilityRequired
	}
	run, err := c.runResolver.ResolveAuthoringRun(
		ctx,
		request.Context,
		request.Purpose,
	)
	if err != nil {
		return nil, err
	}
	if run == nil || strings.TrimSpace(run.ID) == "" ||
		run.Revision < 0 || terminalAuthoringRun(run.Status) {
		return nil, fmt.Errorf("authoring run is unavailable")
	}
	projection, err := authoringProjection(c.projectionIndex, request)
	if err != nil {
		return nil, err
	}
	return c.delivery.ComposeAndSend(ctx, BindRequest{
		RunID: run.ID, ExpectedRunRevision: run.Revision,
		ChatID:              request.Context.ChatID,
		ReplyToMessageID:    request.Context.ReplyToMessageID,
		ExpectedActorOpenID: request.Context.ActorOpenID,
		ActorPolicy:         ActorPolicyOwner,
		InteractionKind:     "agent_card",
		IdempotencyKey:      request.IdempotencyKey,
		ExpiresAt:           request.ExpiresAt,
		Spec:                request.Spec,
		Projection:          projection,
	})
}

func (c *RolloutAuthoringComposer) compileShadow(
	request AuthoringComposeRequest,
) (*CardSurface, error) {
	digest := authoringDigest(request.IdempotencyKey)
	bindings := make(map[string]RuntimeBinding, len(request.Spec.Actions))
	for _, action := range request.Spec.Actions {
		bindings[action.ID] = RuntimeBinding{
			RunID: "shadow-run", StepID: "shadow-step",
			InteractionID: "shadow-interaction-" + digest,
			Revision:      1, Token: "shadow-token",
			InteractionKind: "agent_card_shadow",
		}
	}
	bound, err := NewBoundCardSpec(
		request.Spec,
		LifecycleInteractive,
		bindings,
	)
	if err != nil {
		return nil, err
	}
	compiled, err := c.compiler.CompileJSON(bound)
	if err != nil || !json.Valid(compiled) {
		return nil, ErrCardCompileFailed
	}
	return &CardSurface{
		ID: "shadow_surface_" + digest, Status: SurfaceStatusShadow,
		SpecVersion: request.Spec.Version, ChatID: request.Context.ChatID,
		ReplyToMessageID: request.Context.ReplyToMessageID,
		InteractionKind:  "agent_card_shadow", Revision: 1,
	}, nil
}

func authoringProjection(
	index string,
	request AuthoringComposeRequest,
) (agentruntime.ProjectionDocument, error) {
	payload, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		ChatID        string `json:"chat_id"`
		ActorOpenID   string `json:"actor_open_id"`
		TriggerEvent  string `json:"trigger_event_id,omitempty"`
		Purpose       string `json:"purpose"`
		ComposeKey    string `json:"compose_key"`
	}{
		SchemaVersion: 1, Type: "agent_card_wait",
		ChatID: request.Context.ChatID, ActorOpenID: request.Context.ActorOpenID,
		TriggerEvent: request.Context.TriggerEventID,
		Purpose:      request.Purpose, ComposeKey: request.IdempotencyKey,
	})
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	return agentruntime.ProjectionDocument{
		IndexAlias: index,
		DocumentID: "agent-card-wait-" + authoringDigest(request.IdempotencyKey),
		Payload:    payload,
	}, nil
}

func authoringDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func terminalAuthoringRun(status agentruntime.RunStatus) bool {
	switch status {
	case agentruntime.RunStatusCompleted, agentruntime.RunStatusFailed,
		agentruntime.RunStatusCancelled:
		return true
	default:
		return false
	}
}
