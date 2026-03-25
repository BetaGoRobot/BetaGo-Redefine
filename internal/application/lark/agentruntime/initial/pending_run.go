package initial

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// RootTarget carries initial-run flow state.
type RootTarget struct {
	MessageID string `json:"message_id,omitempty"`
	CardID    string `json:"card_id,omitempty"`
}

// Event re-exports the corresponding initial-run flow type on this package surface.
type Event = message.Envelope

// PendingRun carries initial-run flow state.
type PendingRun struct {
	Start       agentruntime.StartShadowRunRequest  `json:"start"`
	Plan        agentruntime.ChatGenerationPlan     `json:"plan,omitempty"`
	OutputMode  agentruntime.InitialReplyOutputMode `json:"output_mode,omitempty"`
	Event       Event                               `json:"event,omitempty"`
	RootTarget  RootTarget                          `json:"root_target,omitempty"`
	RequestedAt time.Time                           `json:"requested_at,omitempty"`
}

// RunEnqueuer defines a initial-run flow contract.
type RunEnqueuer interface {
	EnqueuePendingInitialRun(context.Context, PendingRun) (int64, error)
}

// NewPendingRun implements initial-run flow behavior.
func NewPendingRun(input agentruntime.InitialRunInput, rootTarget RootTarget) (PendingRun, error) {
	if input.Event == nil || input.Event.Event == nil || input.Event.Event.Message == nil {
		return PendingRun{}, fmt.Errorf("pending initial run event is required")
	}
	item := PendingRun{
		Start:       input.Start,
		Plan:        chatflow.ClonePlan(input.Plan),
		OutputMode:  input.OutputMode,
		Event:       message.CaptureEnvelope(input.Event),
		RootTarget:  normalizeRootTarget(rootTarget),
		RequestedAt: normalizeObservedAt(input.Start.Now),
	}
	if item.OutputMode == "" {
		item.OutputMode = agentruntime.InitialReplyOutputModeAgentic
	}
	if err := item.Validate(); err != nil {
		return PendingRun{}, err
	}
	return item, nil
}

// Validate implements initial-run flow behavior.
func (p PendingRun) Validate() error {
	if strings.TrimSpace(p.Start.ChatID) == "" {
		return fmt.Errorf("pending initial run chat_id is required")
	}
	if strings.TrimSpace(p.Start.ActorOpenID) == "" {
		return fmt.Errorf("pending initial run actor_open_id is required")
	}
	if strings.TrimSpace(p.Start.TriggerMessageID) == "" {
		return fmt.Errorf("pending initial run trigger_message_id is required")
	}
	if strings.TrimSpace(p.Event.ChatID) == "" {
		return fmt.Errorf("pending initial event chat_id is required")
	}
	if strings.TrimSpace(p.Event.MessageID) == "" {
		return fmt.Errorf("pending initial event message_id is required")
	}
	return nil
}

// ChatID implements initial-run flow behavior.
func (p PendingRun) ChatID() string {
	return strings.TrimSpace(p.Start.ChatID)
}

// ActorOpenID implements initial-run flow behavior.
func (p PendingRun) ActorOpenID() string {
	return strings.TrimSpace(p.Start.ActorOpenID)
}

// ApplyRootTarget implements initial-run flow behavior.
func (p PendingRun) ApplyRootTarget(ctx context.Context) context.Context {
	root := normalizeRootTarget(p.RootTarget)
	if root.MessageID == "" && root.CardID == "" {
		return ctx
	}
	state := runtimecontext.AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		state = runtimecontext.NewAgenticReplyTargetState()
		ctx = runtimecontext.WithAgenticReplyTargetState(ctx, state)
	}
	runtimecontext.SeedRootAgenticReplyTarget(ctx, root.MessageID, root.CardID)
	runtimecontext.RecordActiveAgenticReplyTarget(ctx, root.MessageID, root.CardID)
	return ctx
}

// BuildInitialRunInput implements initial-run flow behavior.
func (p PendingRun) BuildInitialRunInput(startedAt time.Time) agentruntime.InitialRunInput {
	start := p.Start
	start.Now = normalizeObservedAt(startedAt)
	if start.Now.IsZero() {
		start.Now = time.Now().UTC()
	}
	return agentruntime.InitialRunInput{
		Start:      start,
		Event:      buildPendingInitialMessageEvent(p.Event),
		Plan:       chatflow.ClonePlan(p.Plan),
		OutputMode: p.OutputMode,
	}
}

// MarshalPendingRun implements initial-run flow behavior.
func MarshalPendingRun(item PendingRun) ([]byte, error) {
	return json.Marshal(item)
}

// UnmarshalPendingRun implements initial-run flow behavior.
func UnmarshalPendingRun(raw []byte) (PendingRun, error) {
	item := PendingRun{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return item, nil
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return PendingRun{}, err
	}
	if item.OutputMode == "" {
		item.OutputMode = agentruntime.InitialReplyOutputModeAgentic
	}
	return item, nil
}

func buildPendingInitialMessageEvent(event Event) *larkim.P2MessageReceiveV1 {
	return message.BuildMessageReceiveEvent(event)
}

func normalizeRootTarget(target RootTarget) RootTarget {
	return RootTarget{
		MessageID: strings.TrimSpace(target.MessageID),
		CardID:    strings.TrimSpace(target.CardID),
	}
}

func normalizeObservedAt(observedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return time.Now().UTC()
	}
	return observedAt.UTC()
}
