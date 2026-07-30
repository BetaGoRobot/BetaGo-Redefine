package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

type authoringCompilerStub struct {
	calls int
}

func (c *authoringCompilerStub) CompileJSON(
	*BoundCardSpec,
) (json.RawMessage, error) {
	c.calls++
	return json.RawMessage(`{"schema":"2.0"}`), nil
}

func (c *authoringCompilerStub) CompileRedactedJSON(
	*BoundCardSpec,
) (json.RawMessage, error) {
	return json.RawMessage(`{"schema":"2.0"}`), nil
}

type authoringRunResolverStub struct {
	calls int
	run   *agentruntime.AgentRun
}

func (r *authoringRunResolverStub) ResolveAuthoringRun(
	context.Context,
	agentcardtool.ComposeContext,
	string,
) (*agentruntime.AgentRun, error) {
	r.calls++
	return r.run, nil
}

type authoringDeliveryStub struct {
	calls   int
	request BindRequest
}

func (d *authoringDeliveryStub) ComposeAndSend(
	_ context.Context,
	request BindRequest,
) (*CardSurface, error) {
	d.calls++
	d.request = request
	return &CardSurface{
		ID: "surface-1", MessageID: "message-1",
		InteractionID: "interaction-1", Revision: 5,
		Status: SurfaceStatusSent,
	}, nil
}

func TestRolloutAuthoringComposerShadowCompilesWithoutResolvingOrSending(
	t *testing.T,
) {
	compiler := &authoringCompilerStub{}
	resolver := &authoringRunResolverStub{}
	delivery := &authoringDeliveryStub{}
	composer, err := NewRolloutAuthoringComposer(
		RolloutAuthoringComposerOptions{
			Compiler: compiler, RunResolver: resolver, Delivery: delivery,
			Shadow: true, ProjectionIndex: "conversation-events",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := composer.Compose(
		context.Background(),
		validAuthoringRequest(ActionModeUI),
	)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.calls != 1 || resolver.calls != 0 || delivery.calls != 0 {
		t.Fatalf(
			"calls compiler/resolver/delivery = %d/%d/%d",
			compiler.calls, resolver.calls, delivery.calls,
		)
	}
	if surface.Status != SurfaceStatusShadow || surface.MessageID != "" ||
		surface.ID == "" {
		t.Fatalf("shadow surface = %#v", surface)
	}
}

func TestRolloutAuthoringComposerBindsActiveRunAndSends(t *testing.T) {
	run := &agentruntime.AgentRun{
		ID: "run-1", Revision: 4, Status: agentruntime.RunStatusRunning,
	}
	resolver := &authoringRunResolverStub{run: run}
	delivery := &authoringDeliveryStub{}
	composer, err := NewRolloutAuthoringComposer(
		RolloutAuthoringComposerOptions{
			Compiler: &authoringCompilerStub{}, RunResolver: resolver,
			Delivery: delivery, ProjectionIndex: "conversation-events",
			CanSend: func(chatID string) bool { return chatID == "chat-1" },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := validAuthoringRequest(ActionModeUI)
	surface, err := composer.Compose(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if surface.MessageID != "message-1" || resolver.calls != 1 ||
		delivery.calls != 1 {
		t.Fatalf("result/calls = %#v / %d / %d", surface, resolver.calls, delivery.calls)
	}
	got := delivery.request
	if got.RunID != run.ID || got.ExpectedRunRevision != run.Revision ||
		got.ChatID != request.Context.ChatID ||
		got.ReplyToMessageID != request.Context.ReplyToMessageID ||
		got.ExpectedActorOpenID != request.Context.ActorOpenID ||
		got.ActorPolicy != ActorPolicyOwner ||
		got.InteractionKind != "agent_card" ||
		got.IdempotencyKey != request.IdempotencyKey ||
		got.Projection.IndexAlias != "conversation-events" ||
		!json.Valid(got.Projection.Payload) {
		t.Fatalf("bind request = %#v", got)
	}
}

func TestRolloutAuthoringComposerFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		options RolloutAuthoringComposerOptions
		request AuthoringComposeRequest
		want    error
	}{
		{
			name: "chat denied",
			options: RolloutAuthoringComposerOptions{
				Compiler: &authoringCompilerStub{},
				RunResolver: &authoringRunResolverStub{
					run: &agentruntime.AgentRun{
						ID: "run-1", Status: agentruntime.RunStatusRunning,
					},
				},
				Delivery:        &authoringDeliveryStub{},
				CanSend:         func(string) bool { return false },
				ProjectionIndex: "events",
			},
			request: validAuthoringRequest(ActionModeUI),
			want:    ErrAuthoringNotAllowed,
		},
		{
			name: "untrusted capability confirmation",
			options: RolloutAuthoringComposerOptions{
				Compiler: &authoringCompilerStub{},
				RunResolver: &authoringRunResolverStub{
					run: &agentruntime.AgentRun{
						ID: "run-1", Status: agentruntime.RunStatusRunning,
					},
				},
				Delivery:        &authoringDeliveryStub{},
				CanSend:         func(string) bool { return true },
				ProjectionIndex: "events",
			},
			request: validAuthoringRequest(ActionModeCapabilityConfirm),
			want:    ErrTrustedCapabilityRequired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			composer, err := NewRolloutAuthoringComposer(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := composer.Compose(
				context.Background(),
				test.request,
			); !errors.Is(err, test.want) {
				t.Fatalf("Compose() error = %v, want %v", err, test.want)
			}
		})
	}
}

func validAuthoringRequest(mode ActionMode) AuthoringComposeRequest {
	return AuthoringComposeRequest{
		Context: agentcardtool.ComposeContext{
			ChatID: "chat-1", ActorOpenID: "actor-1",
			ReplyToMessageID: "message-trigger",
			TriggerEventID:   "event-trigger",
		},
		Purpose:         "collect a choice",
		InteractionMode: mode,
		Spec: CardSpec{
			Version: VersionV1, Title: "Choose",
			Blocks: []Block{PlainText("intro", "Choose one")},
			Actions: []Action{{
				Kind: ActionButton, ID: "choose", Label: "Choose",
				Mode: mode, Intent: "choose",
			}},
		},
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
		IdempotencyKey: "compose-key",
	}
}
