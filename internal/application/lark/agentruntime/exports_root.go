package agentruntime

import (
	"context"
	"errors"
	"iter"
	"strings"
	"time"

	approvaldef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/approval"
	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	initialstate "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialstate"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ApprovalCardDelivery aliases the approval-card delivery mode exposed by the
// approval subpackage on the root agentruntime API.
type ApprovalCardDelivery = approvaldef.ApprovalCardDelivery

// RequestApprovalInput aliases the input used to create or reserve an approval request.
type RequestApprovalInput = approvaldef.RequestApprovalInput

// ApprovalRequest aliases the persisted approval request payload shown to reviewers.
type ApprovalRequest = approvaldef.ApprovalRequest

// ApprovalReservationDecisionOutcome aliases the outcome recorded for a reserved approval.
type ApprovalReservationDecisionOutcome = approvaldef.ApprovalReservationDecisionOutcome

// ApprovalReservationDecision aliases the recorded decision attached to a reserved approval.
type ApprovalReservationDecision = approvaldef.ApprovalReservationDecision

// ApprovalReservation aliases the durable reservation state used before an approval becomes active.
type ApprovalReservation = approvaldef.ApprovalReservation

// ActivateReservedApprovalInput aliases the activation input for converting a reservation into a waiting approval.
type ActivateReservedApprovalInput = approvaldef.ActivateReservedApprovalInput

// ApprovalCardTarget aliases the Lark delivery target for approval cards.
type ApprovalCardTarget = approvaldef.ApprovalCardTarget

// ApprovalSender aliases the contract used to deliver approval cards.
type ApprovalSender = approvaldef.ApprovalSender

// ApprovalCardState aliases the rendered approval-card state.
type ApprovalCardState = approvaldef.ApprovalCardState

// LarkApprovalSender aliases the production approval-card sender backed by Lark APIs.
type LarkApprovalSender = approvaldef.LarkApprovalSender

// CapabilityKind aliases the high-level kind of runtime capability.
type CapabilityKind = capdef.Kind

// SideEffectLevel aliases the side-effect classification attached to a capability.
type SideEffectLevel = capdef.SideEffectLevel

// CapabilityScope aliases the chat/runtime scope in which a capability may execute.
type CapabilityScope = capdef.Scope

// CapabilityMeta aliases the capability metadata surfaced by the registry.
type CapabilityMeta = capdef.Meta

// CapabilityRequest aliases the normalized execution request passed into a capability.
type CapabilityRequest = capdef.Request

// CapabilityResult aliases the structured output returned by a capability execution.
type CapabilityResult = capdef.Result

// CapabilityApprovalSpec aliases the approval requirements derived for a capability call.
type CapabilityApprovalSpec = capdef.ApprovalSpec

// CapabilityContinuationInput aliases the continuation context delivered to resumed capability work.
type CapabilityContinuationInput = capdef.ContinuationInput

// CapabilityCallInput aliases the serialized input for a capability call step.
type CapabilityCallInput = capdef.CallInput

// CompletedCapabilityCall aliases the completed capability-call trace recorded by the runtime.
type CompletedCapabilityCall = capdef.CompletedCall

// QueuedCapabilityCall aliases the queued capability-call state recorded between reply turns.
type QueuedCapabilityCall = capdef.QueuedCall

// PlanPendingCapability aliases the pending-capability summary embedded in plan steps.
type PlanPendingCapability = capdef.PlanPending

// Capability aliases the execution contract implemented by runtime capabilities.
type Capability = capdef.Capability

// CapabilityRegistry aliases the runtime capability registry implementation.
type CapabilityRegistry = capdef.Registry

// CapabilityReplyPlanningRequest aliases the input used to plan a reply after capability execution.
type CapabilityReplyPlanningRequest = capdef.ReplyPlanningRequest

// CapabilityReplyPlan aliases the reply plan produced after capability execution.
type CapabilityReplyPlan = capdef.ReplyPlan

// CapabilityReplyPlanner aliases the contract used to plan post-capability replies.
type CapabilityReplyPlanner = capdef.ReplyPlanner

// InitialRunOwnership aliases the ownership metadata propagated through initial-run contexts.
type InitialRunOwnership = initialstate.RunOwnership

// InitialReplyOutputMode aliases the output mode used while emitting an initial reply.
type InitialReplyOutputMode = initialstate.ReplyOutputMode

// InitialReplyTargetMode aliases the strategy used to target the initial reply.
type InitialReplyTargetMode = initialstate.ReplyTargetMode

// InitialReplyTarget aliases the reply target captured in the initial-run context.
type InitialReplyTarget = initialstate.ReplyTarget

// CapturedInitialPendingCapability aliases the pending capability information extracted from an initial reply stream.
type CapturedInitialPendingCapability = initialstate.CapturedPendingCapability

// CapturedInitialReply aliases the normalized initial reply snapshot captured from streaming output.
type CapturedInitialReply = initialstate.CapturedReply

// InitialChatToolOutput aliases the tool output fed back into an initial chat turn.
type InitialChatToolOutput = chatflow.InitialChatToolOutput

// InitialChatTurnRequest aliases the request passed into one initial chat turn execution.
type InitialChatTurnRequest = chatflow.InitialChatTurnRequest

// InitialChatToolCall aliases the tool-call snapshot emitted by an initial chat turn.
type InitialChatToolCall = chatflow.InitialChatToolCall

// InitialChatTurnSnapshot aliases the summarized snapshot of one initial chat turn.
type InitialChatTurnSnapshot = chatflow.InitialChatTurnSnapshot

// InitialChatTurnResult aliases the streamed result of one initial chat turn.
type InitialChatTurnResult = chatflow.InitialChatTurnResult

// InitialChatGenerationRequest aliases the request used to build an initial chat execution plan.
type InitialChatGenerationRequest = chatflow.InitialChatGenerationRequest

// InitialChatExecutionPlan aliases the compiled plan for an initial chat execution.
type InitialChatExecutionPlan = chatflow.InitialChatExecutionPlan

const (
	// ApprovalCardDeliveryEphemeral sends the approval card as an ephemeral response.
	ApprovalCardDeliveryEphemeral = approvaldef.ApprovalCardDeliveryEphemeral
	// ApprovalCardDeliveryMessage sends the approval card as a normal message.
	ApprovalCardDeliveryMessage = approvaldef.ApprovalCardDeliveryMessage

	// ApprovalReservationDecisionApproved marks a reservation decision as approved.
	ApprovalReservationDecisionApproved = approvaldef.ApprovalReservationDecisionApproved
	// ApprovalReservationDecisionRejected marks a reservation decision as rejected.
	ApprovalReservationDecisionRejected = approvaldef.ApprovalReservationDecisionRejected

	// ApprovalCardStatePending renders the approval card in its pending state.
	ApprovalCardStatePending = approvaldef.ApprovalCardStatePending
	// ApprovalCardStateApproved renders the approval card in its approved state.
	ApprovalCardStateApproved = approvaldef.ApprovalCardStateApproved
	// ApprovalCardStateRejected renders the approval card in its rejected state.
	ApprovalCardStateRejected = approvaldef.ApprovalCardStateRejected
	// ApprovalCardStateExpired renders the approval card in its expired state.
	ApprovalCardStateExpired = approvaldef.ApprovalCardStateExpired

	// CapabilityKindCommand identifies a command-bridge capability.
	CapabilityKindCommand = capdef.KindCommand
	// CapabilityKindTool identifies a tool-backed capability.
	CapabilityKindTool = capdef.KindTool
	// CapabilityKindCardAction identifies a capability triggered by a card action.
	CapabilityKindCardAction = capdef.KindCardAction
	// CapabilityKindSchedule identifies a scheduled capability.
	CapabilityKindSchedule = capdef.KindSchedule
	// CapabilityKindInternal identifies a runtime-internal capability.
	CapabilityKindInternal = capdef.KindInternal

	// SideEffectLevelNone marks a capability as read-only from the user's perspective.
	SideEffectLevelNone = capdef.SideEffectLevelNone
	// SideEffectLevelChatWrite marks a capability as mutating chat-visible state.
	SideEffectLevelChatWrite = capdef.SideEffectLevelChatWrite
	// SideEffectLevelExternalWrite marks a capability as mutating an external system.
	SideEffectLevelExternalWrite = capdef.SideEffectLevelExternalWrite
	// SideEffectLevelAdminWrite marks a capability as performing high-impact administrative writes.
	SideEffectLevelAdminWrite = capdef.SideEffectLevelAdminWrite

	// CapabilityScopeP2P limits a capability to direct-message conversations.
	CapabilityScopeP2P = capdef.ScopeP2P
	// CapabilityScopeGroup limits a capability to group-chat conversations.
	CapabilityScopeGroup = capdef.ScopeGroup
	// CapabilityScopeSchedule limits a capability to schedule-triggered execution.
	CapabilityScopeSchedule = capdef.ScopeSchedule
	// CapabilityScopeCallback limits a capability to callback-triggered execution.
	CapabilityScopeCallback = capdef.ScopeCallback

	// InitialReplyOutputModeAgentic emits the initial reply through the agentic card flow.
	InitialReplyOutputModeAgentic = initialstate.ReplyOutputModeAgentic
	// InitialReplyOutputModeStandard emits the initial reply through the standard text flow.
	InitialReplyOutputModeStandard = initialstate.ReplyOutputModeStandard
	// InitialReplyTargetModePatch requests patching an existing root reply target.
	InitialReplyTargetModePatch = initialstate.ReplyTargetModePatch
	// InitialReplyTargetModeReply requests replying to an existing message target.
	InitialReplyTargetModeReply = initialstate.ReplyTargetModeReply
)

const defaultCapabilityApprovalTTL = 15 * time.Minute

// ContinuationProcessorOption mutates the runtime continuation processor during construction.
type ContinuationProcessorOption func(*ContinuationProcessor)

var (
	// ErrApprovalExpired is returned when an approval request or reservation has already expired.
	ErrApprovalExpired = approvaldef.ErrApprovalExpired
	// ErrApprovalStateConflict is returned when the run state does not match the requested approval transition.
	ErrApprovalStateConflict = approvaldef.ErrApprovalStateConflict
	// ErrApprovalReservationNotFound is returned when a reserved approval token cannot be loaded.
	ErrApprovalReservationNotFound = approvaldef.ErrApprovalReservationNotFound

	// ErrRunSlotOccupied reports that the execution slot for the current actor/chat is already busy.
	ErrRunSlotOccupied = errors.New("agent runtime run slot occupied")
	// ErrActiveRunLimitExceeded reports that the actor/chat already has too many active runs.
	ErrActiveRunLimitExceeded = errors.New("agent runtime active run limit exceeded")
	// ErrPendingInitialRunQueueFull reports that the pending initial-run queue cannot accept more work.
	ErrPendingInitialRunQueueFull = errors.New("agent runtime pending initial run queue full")

	// NewCapabilityRegistry constructs an empty capability registry.
	NewCapabilityRegistry        = capdef.NewRegistry
	normalizeCapabilityReplyPlan = capdef.NormalizeReplyPlan
	// NewDefaultCapabilityReplyPlanner constructs the default post-capability reply planner.
	NewDefaultCapabilityReplyPlanner = capdef.NewDefaultReplyPlanner
	// NewLarkApprovalSender constructs the production Lark-backed approval sender.
	NewLarkApprovalSender = approvaldef.NewLarkApprovalSender
	// NewLarkApprovalSenderForTest constructs a Lark approval sender with injectable transports.
	NewLarkApprovalSenderForTest = approvaldef.NewLarkApprovalSenderForTest
	// BuildApprovalCard renders the approval card payload for a request and state.
	BuildApprovalCard = approvaldef.BuildApprovalCard

	// WithInitialRunOwnership stores initial-run ownership metadata on a context.
	WithInitialRunOwnership = initialstate.WithRunOwnership
	// InitialRunOwnershipFromContext loads initial-run ownership metadata from a context.
	InitialRunOwnershipFromContext = initialstate.RunOwnershipFromContext
	// WithInitialReplyTarget stores the preferred initial reply target on a context.
	WithInitialReplyTarget = initialstate.WithReplyTarget
	// InitialReplyTargetFromContext loads the preferred initial reply target from a context.
	InitialReplyTargetFromContext = initialstate.ReplyTargetFromContext
)

const (
	// DefaultMaxActiveRunsPerActorChat is the default cap on concurrent active runs per actor/chat pair.
	DefaultMaxActiveRunsPerActorChat int64 = 5
	// DefaultMaxPendingInitialRunsPerActorChat is the default cap on queued initial runs per actor/chat pair.
	DefaultMaxPendingInitialRunsPerActorChat int64 = DefaultMaxActiveRunsPerActorChat
	// DefaultMaxExecutionLeasesPerActorChat is the default cap on concurrent execution leases per actor/chat pair.
	DefaultMaxExecutionLeasesPerActorChat int64 = 2
)

// AgenticOutputKind describes whether an emitted agentic stream is a user reply
// or a side-effect update.
type AgenticOutputKind string

const (
	AgenticOutputKindModelReply AgenticOutputKind = "model_reply"
	AgenticOutputKindSideEffect AgenticOutputKind = "side_effect"
)

// InitialReplyEmissionRequest describes how an initial reply should be emitted,
// including target selection, output mode, and the stream to send.
type InitialReplyEmissionRequest struct {
	OutputKind      AgenticOutputKind
	Mode            InitialReplyOutputMode
	MentionOpenID   string
	Message         *larkim.EventMessage
	TargetMode      InitialReplyTargetMode
	TargetMessageID string
	TargetCardID    string
	ReplyInThread   bool
	Stream          iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

// InitialReplyEmissionResult captures the identifiers and reply snapshot produced
// when an initial reply is emitted.
type InitialReplyEmissionResult struct {
	Reply             CapturedInitialReply `json:"reply"`
	ResponseMessageID string               `json:"response_message_id,omitempty"`
	ResponseCardID    string               `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode    `json:"delivery_mode,omitempty"`
	TargetMessageID   string               `json:"target_message_id,omitempty"`
	TargetCardID      string               `json:"target_card_id,omitempty"`
}

// InitialReplyEmitter emits the user-visible result of the initial runtime turn.
type InitialReplyEmitter interface {
	EmitInitialReply(context.Context, InitialReplyEmissionRequest) (InitialReplyEmissionResult, error)
}

// InitialReplyStreamGenerator produces the stream consumed by an initial reply emitter.
type InitialReplyStreamGenerator func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)

// InitialTraceRecorder records capability traces and reply-plan metadata while an initial run executes.
type InitialTraceRecorder interface {
	RecordCompletedCapabilityCall(context.Context, CompletedCapabilityCall) error
	RecordReplyTurnPlan(context.Context, CapabilityReplyPlan, *InitialChatToolCall) error
}

// WithCapabilityRegistry injects the capability registry used by continuation processing.
func WithCapabilityRegistry(registry *CapabilityRegistry) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.registry = registry
		}
	}
}

// WithCapabilityReplyPlanner injects the planner used to turn capability results into user-facing replies.
func WithCapabilityReplyPlanner(planner CapabilityReplyPlanner) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.capabilityReplyPlanner = planner
		}
	}
}

// WithRunLeasePolicy injects the execution-lease and run-heartbeat timing used
// by the continuation processor.
func WithRunLeasePolicy(policy RunLeasePolicy) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.runLeasePolicy = policy
		}
	}
}

func resolveCapabilityApprovalSpec(step *AgentStep, meta CapabilityMeta, input CapabilityCallInput, now time.Time) CapabilityApprovalSpec {
	return capdef.ResolveApprovalSpec(
		strings.TrimSpace(step.CapabilityName),
		strings.TrimSpace(meta.Description),
		input,
		now,
		defaultCapabilityApprovalTTL,
	)
}

func normalizeAgenticOutputKind(kind AgenticOutputKind) AgenticOutputKind {
	switch strings.TrimSpace(string(kind)) {
	case string(AgenticOutputKindSideEffect):
		return AgenticOutputKindSideEffect
	default:
		return AgenticOutputKindModelReply
	}
}

func initialReplyCapabilityScope(event *larkim.P2MessageReceiveV1) CapabilityScope {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return CapabilityScopeGroup
	}
	if strings.EqualFold(message.ChatType(event), "p2p") {
		return CapabilityScopeP2P
	}
	return CapabilityScopeGroup
}

func initialReplyActorOpenID(event *larkim.P2MessageReceiveV1) string {
	return message.ActorOpenID(event)
}

func resolveAgenticInitialReplyDelivery(
	req InitialReplyEmissionRequest,
	canPatch bool,
	canReply bool,
) initialcore.Delivery {
	return initialcore.ResolveDelivery(initialcore.DeliveryRequest{
		OutputKind:      string(normalizeAgenticOutputKind(req.OutputKind)),
		TargetMode:      string(req.TargetMode),
		TargetMessageID: req.TargetMessageID,
		TargetCardID:    req.TargetCardID,
		CanPatch:        canPatch,
		CanReply:        canReply,
	})
}
