package cardaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	uuid "github.com/satori/go.uuid"
)

const (
	defaultScheduleInteractionRunningTTL = 30 * time.Second
	defaultScheduleInteractionIndexAlias = "agent_conversation_events"
)

type scheduleInteractionResolver interface {
	Resolve(context.Context, agentruntime.ScheduleInteractionRequest) (agentruntime.ScheduleInteractionOutcome, error)
}

type ScheduleInteractionDispatcherOptions struct {
	Now        func() time.Time
	NewClaimID func() string
	RunningTTL time.Duration
	IndexAlias string
}

type ScheduleInteractionDispatcher struct {
	resolver   scheduleInteractionResolver
	now        func() time.Time
	newClaimID func() string
	runningTTL time.Duration
	indexAlias string
}

func NewScheduleInteractionDispatcher(
	resolver scheduleInteractionResolver,
	options ScheduleInteractionDispatcherOptions,
) (*ScheduleInteractionDispatcher, error) {
	if isNilScheduleInteractionResolver(resolver) {
		return nil, errors.New("schedule interaction resolver is required")
	}
	if options.RunningTTL < 0 {
		return nil, errors.New("schedule interaction running TTL must not be negative")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewClaimID == nil {
		options.NewClaimID = func() string { return uuid.NewV4().String() }
	}
	if options.RunningTTL == 0 {
		options.RunningTTL = defaultScheduleInteractionRunningTTL
	}
	if strings.TrimSpace(options.IndexAlias) == "" {
		options.IndexAlias = defaultScheduleInteractionIndexAlias
	}
	if strings.TrimSpace(options.IndexAlias) != options.IndexAlias {
		return nil, errors.New("schedule interaction index alias must not have surrounding whitespace")
	}
	return &ScheduleInteractionDispatcher{
		resolver: resolver, now: options.Now, newClaimID: options.NewClaimID,
		runningTTL: options.RunningTTL, indexAlias: options.IndexAlias,
	}, nil
}

func (d *ScheduleInteractionDispatcher) CanHandle(action *cardactionproto.Parsed) bool {
	if action == nil ||
		(action.Name != cardactionproto.ActionScheduleEditConfirm &&
			action.Name != cardactionproto.ActionScheduleEditCancel) {
		return false
	}
	runtimeFields := [...]string{
		cardactionproto.RunIDField,
		cardactionproto.StepIDField,
		cardactionproto.InteractionIDField,
		cardactionproto.RevisionField,
		cardactionproto.TokenField,
		cardactionproto.InteractionKindField,
		cardactionproto.ContinueAgentField,
	}
	for _, field := range runtimeFields {
		if _, exists := action.Value[field]; exists {
			return true
		}
	}
	return false
}

func (d *ScheduleInteractionDispatcher) Dispatch(
	ctx context.Context,
	request ContinuationRequest,
) (*callback.CardActionTriggerResponse, error) {
	if d == nil || isNilScheduleInteractionResolver(d.resolver) {
		return nil, errors.New("schedule interaction dispatcher is not configured")
	}
	if request.Action == nil || !d.CanHandle(request.Action) {
		return nil, ErrUnhandledAction
	}
	req, err := d.resolveRequest(request)
	if err != nil {
		return nil, err
	}
	outcome, err := d.resolver.Resolve(ctx, req)
	if errors.Is(err, agentruntime.ErrScheduleInteractionRunning) {
		return InfoToast("操作正在处理中，请稍后重试"), nil
	}
	if err != nil {
		return nil, err
	}
	return scheduleInteractionTerminalResponse(outcome), nil
}

func (d *ScheduleInteractionDispatcher) resolveRequest(
	request ContinuationRequest,
) (agentruntime.ScheduleInteractionRequest, error) {
	runID, err := request.Action.RequiredString(cardactionproto.RunIDField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	stepID, err := request.Action.RequiredString(cardactionproto.StepIDField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	interactionID, err := request.Action.RequiredString(cardactionproto.InteractionIDField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	revisionText, err := request.Action.RequiredString(cardactionproto.RevisionField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	revision, err := strconv.ParseInt(revisionText, 10, 64)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, errors.New("card action has invalid runtime revision")
	}
	token, err := request.Action.RequiredString(cardactionproto.TokenField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	interactionKind, err := request.Action.RequiredString(cardactionproto.InteractionKindField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	if interactionKind != "schedule_edit" {
		return agentruntime.ScheduleInteractionRequest{}, errors.New("card action has invalid interaction kind")
	}
	continueAgent, err := request.Action.RequiredString(cardactionproto.ContinueAgentField)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	continues, err := strconv.ParseBool(continueAgent)
	if err != nil || !continues {
		return agentruntime.ScheduleInteractionRequest{}, errors.New("card action does not request agent continuation")
	}
	action, err := scheduleInteractionAction(request.Action.Name)
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	actorOpenID, eventID, sourceRef := scheduleCallbackIdentity(request)
	now := d.now().UTC()
	claimID := strings.TrimSpace(d.newClaimID())
	projectionPayload, err := json.Marshal(map[string]any{
		"run_id": runID, "interaction_id": interactionID, "revision": revision,
		"type": "schedule_interaction", "occurred_at": now,
	})
	if err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	resolved := agentruntime.ScheduleInteractionRequest{
		RunID: runID, StepID: stepID, InteractionID: interactionID, Revision: revision,
		PresentedToken: token, ActorOpenID: actorOpenID, Action: action,
		EventID: eventID, SourceRef: sourceRef, ResolvedAt: now, ClaimID: claimID,
		RunningTTL: d.runningTTL,
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: d.indexAlias, DocumentID: runID, Payload: projectionPayload,
		},
	}
	if err := resolved.Validate(); err != nil {
		return agentruntime.ScheduleInteractionRequest{}, err
	}
	return resolved, nil
}

func scheduleInteractionAction(name string) (agentruntime.ScheduleInteractionAction, error) {
	switch name {
	case cardactionproto.ActionScheduleEditConfirm:
		return agentruntime.ScheduleInteractionConfirm, nil
	case cardactionproto.ActionScheduleEditCancel:
		return agentruntime.ScheduleInteractionCancel, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnhandledAction, name)
	}
}

func scheduleCallbackIdentity(request ContinuationRequest) (actorOpenID, eventID, sourceRef string) {
	if request.Event != nil {
		if request.Event.EventV2Base != nil && request.Event.EventV2Base.Header != nil {
			eventID = strings.TrimSpace(request.Event.EventV2Base.Header.EventID)
		}
		if request.Event.Event != nil {
			if request.Event.Event.Operator != nil {
				actorOpenID = strings.TrimSpace(request.Event.Event.Operator.OpenID)
			}
			if request.Event.Event.Context != nil {
				sourceRef = strings.TrimSpace(request.Event.Event.Context.OpenMessageID)
			}
		}
	}
	if actorOpenID == "" && request.Meta != nil {
		actorOpenID = strings.TrimSpace(request.Meta.OpenID)
	}
	return actorOpenID, eventID, sourceRef
}

func scheduleInteractionTerminalResponse(
	outcome agentruntime.ScheduleInteractionOutcome,
) *callback.CardActionTriggerResponse {
	cancelled := outcome.Status == "cancelled_by_user" ||
		outcome.Action == agentruntime.ScheduleInteractionCancel
	title := "Schedule 已更新"
	message := "✅ 修改已确认，Schedule 已更新。"
	toast := "Schedule 已更新"
	template := "green"
	if cancelled {
		title = "Schedule 已取消"
		message = "已取消本次 Schedule 修改。"
		toast = "已取消修改"
		template = "grey"
	}
	card := larkmsg.NewCardV2(title, []any{
		larkmsg.Markdown(message),
		larkmsg.HintMarkdown("该交互已结束，无需再次操作。"),
	}, larkmsg.CardV2Options{HeaderTemplate: template, VerticalSpacing: "8px", Padding: "12px"})
	return InfoToastWithRawCardPayload(toast, card)
}

func isNilScheduleInteractionResolver(resolver scheduleInteractionResolver) bool {
	if resolver == nil {
		return true
	}
	value := reflect.ValueOf(resolver)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}
