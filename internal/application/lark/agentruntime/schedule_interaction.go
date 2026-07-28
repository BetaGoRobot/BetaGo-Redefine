package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

type ScheduleInteractionAction string

const (
	ScheduleInteractionConfirm ScheduleInteractionAction = "schedule.edit_confirm"
	ScheduleInteractionCancel  ScheduleInteractionAction = "schedule.edit_cancel"
)

var (
	ErrScheduleInteractionRunning   = errors.New("schedule interaction execution is already running")
	ErrScheduleInteractionClaimLost = errors.New("schedule interaction execution claim was lost")
)

type ScheduleInteractionRequest struct {
	RunID          string
	StepID         string
	InteractionID  string
	Revision       int64
	PresentedToken string
	ActorOpenID    string
	Action         ScheduleInteractionAction
	EventID        string
	SourceRef      string
	ResolvedAt     time.Time
	ClaimID        string
	RunningTTL     time.Duration
	Projection     ProjectionDocument
}

func (r ScheduleInteractionRequest) Validate() error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "run_id", value: r.RunID},
		{name: "step_id", value: r.StepID},
		{name: "interaction_id", value: r.InteractionID},
		{name: "presented_token", value: r.PresentedToken},
		{name: "actor_open_id", value: r.ActorOpenID},
		{name: "claim_id", value: r.ClaimID},
	}
	for _, field := range fields {
		if err := validateCanonical(field.name, field.value); err != nil {
			return err
		}
	}
	if r.Action != ScheduleInteractionConfirm && r.Action != ScheduleInteractionCancel {
		return invalidRuntimeContract("schedule interaction action is invalid")
	}
	if r.Revision <= 0 {
		return invalidRuntimeContract("revision must be positive")
	}
	if r.EventID == "" && r.SourceRef == "" {
		return invalidRuntimeContract("event_id or source_ref is required")
	}
	if r.EventID != "" {
		if err := validateCanonical("event_id", r.EventID); err != nil {
			return err
		}
	}
	if r.SourceRef != "" {
		if err := validateCanonical("source_ref", r.SourceRef); err != nil {
			return err
		}
	}
	if r.ResolvedAt.IsZero() {
		return invalidRuntimeContract("resolved_at is required")
	}
	if r.RunningTTL <= 0 {
		return invalidRuntimeContract("running_ttl must be positive")
	}
	return r.Projection.Validate()
}

type ScheduleClaimState string

const (
	ScheduleClaimAcquired  ScheduleClaimState = "acquired"
	ScheduleClaimRunning   ScheduleClaimState = "running"
	ScheduleClaimCompleted ScheduleClaimState = "completed"
)

type ScheduleInteractionOutcome struct {
	Status        string                    `json:"status"`
	TaskID        string                    `json:"task_id"`
	InteractionID string                    `json:"interaction_id"`
	Action        ScheduleInteractionAction `json:"action"`
	Result        json.RawMessage           `json:"result"`
}

type ScheduleInteractionClaim struct {
	State        ScheduleClaimState
	TrustedInput json.RawMessage
	Outcome      ScheduleInteractionOutcome
}

type ScheduleInteractionInspection struct {
	TrustedInput     json.RawMessage
	CompletedOutcome *ScheduleInteractionOutcome
}

type CompleteScheduleInteractionRequest struct {
	Request ScheduleInteractionRequest
	Outcome ScheduleInteractionOutcome
}

type FailScheduleInteractionRequest struct {
	Request   ScheduleInteractionRequest
	ErrorText string
}

type ScheduleInteractionStore interface {
	InspectScheduleInteraction(context.Context, ScheduleInteractionRequest) (ScheduleInteractionInspection, error)
	ClaimScheduleInteraction(context.Context, ScheduleInteractionRequest) (ScheduleInteractionClaim, error)
	CompleteScheduleInteraction(context.Context, CompleteScheduleInteractionRequest) (ScheduleInteractionOutcome, error)
	FailScheduleInteraction(context.Context, FailScheduleInteractionRequest) error
}

type ScheduleEditCapability interface {
	ValidateScheduleEdit(context.Context, string, ScheduleEditTrustedInput) error
	ExecuteScheduleEdit(context.Context, string, ScheduleEditTrustedInput) (json.RawMessage, error)
}

type RunSubmitter interface {
	SubmitRun(context.Context, string) error
}

type ScheduleInteractionService struct {
	store      ScheduleInteractionStore
	capability ScheduleEditCapability
	submitter  RunSubmitter
}

func NewScheduleInteractionService(
	store ScheduleInteractionStore,
	capability ScheduleEditCapability,
	submitter RunSubmitter,
) *ScheduleInteractionService {
	return &ScheduleInteractionService{store: store, capability: capability, submitter: submitter}
}

func (s *ScheduleInteractionService) Resolve(
	ctx context.Context,
	req ScheduleInteractionRequest,
) (ScheduleInteractionOutcome, error) {
	if err := req.Validate(); err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	if s == nil || isNilRuntimeDependency(s.store) || isNilRuntimeDependency(s.capability) {
		return ScheduleInteractionOutcome{}, errors.New("schedule interaction service is not configured")
	}
	inspection, err := s.store.InspectScheduleInteraction(ctx, req)
	if err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	if inspection.CompletedOutcome != nil {
		return *inspection.CompletedOutcome, nil
	}
	trusted, err := DecodeScheduleEditTrustedInput(inspection.TrustedInput)
	if err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	// Permission is checked before the durable claim so an unauthorized click
	// cannot occupy the interaction. Confirm execution validates again.
	if err := s.capability.ValidateScheduleEdit(ctx, req.ActorOpenID, trusted); err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	claim, err := s.store.ClaimScheduleInteraction(ctx, req)
	if err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	switch claim.State {
	case ScheduleClaimCompleted:
		return claim.Outcome, nil
	case ScheduleClaimRunning:
		return ScheduleInteractionOutcome{}, ErrScheduleInteractionRunning
	case ScheduleClaimAcquired:
	default:
		return ScheduleInteractionOutcome{}, errors.New("schedule interaction store returned an invalid claim state")
	}

	outcome := ScheduleInteractionOutcome{
		TaskID: trusted.TaskID, InteractionID: req.InteractionID, Action: req.Action,
		Result: json.RawMessage(`{}`),
	}
	if req.Action == ScheduleInteractionCancel {
		outcome.Status = "cancelled_by_user"
	} else {
		result, executeErr := s.capability.ExecuteScheduleEdit(ctx, req.ActorOpenID, trusted)
		if executeErr != nil {
			_ = s.store.FailScheduleInteraction(ctx, FailScheduleInteractionRequest{
				Request: req, ErrorText: "schedule edit capability execution failed",
			})
			return ScheduleInteractionOutcome{}, executeErr
		}
		outcome.Status = scheduleResultStatus(result)
		outcome.Result = append(json.RawMessage(nil), result...)
	}
	completed, err := s.store.CompleteScheduleInteraction(ctx, CompleteScheduleInteractionRequest{
		Request: req, Outcome: outcome,
	})
	if err != nil {
		_ = s.store.FailScheduleInteraction(ctx, FailScheduleInteractionRequest{
			Request: req, ErrorText: "schedule interaction finalization failed",
		})
		return ScheduleInteractionOutcome{}, err
	}
	if err := s.submit(ctx, req.RunID); err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	return completed, nil
}

func (s *ScheduleInteractionService) submit(ctx context.Context, runID string) error {
	if isNilRuntimeDependency(s.submitter) {
		return nil
	}
	return s.submitter.SubmitRun(ctx, runID)
}

func isNilRuntimeDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice, reflect.UnsafePointer:
		return reflected.IsNil()
	default:
		return false
	}
}

func scheduleResultStatus(result json.RawMessage) string {
	var payload struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(result, &payload) == nil && strings.TrimSpace(payload.Status) != "" {
		return strings.TrimSpace(payload.Status)
	}
	return "updated"
}

type ScheduleEditValues struct {
	Name          *string    `json:"name,omitempty"`
	CronExpr      *string    `json:"cron_expr,omitempty"`
	Timezone      *string    `json:"timezone,omitempty"`
	RunAt         *time.Time `json:"run_at,omitempty"`
	Message       *string    `json:"message,omitempty"`
	NotifyOnError *bool      `json:"notify_on_error,omitempty"`
	NotifyResult  *bool      `json:"notify_result,omitempty"`
	SkipHolidays  *bool      `json:"skip_holidays,omitempty"`
}

type ScheduleEditTrustedInput struct {
	Version         int                `json:"version"`
	TaskID          string             `json:"task_id"`
	InitiatorOpenID string             `json:"initiator_open_id"`
	ChatID          string             `json:"chat_id"`
	SourceMessageID string             `json:"source_message_id,omitempty"`
	NewValues       ScheduleEditValues `json:"new_values"`
}

func EncodeScheduleEditTrustedInput(req StartScheduleEditRequest) (json.RawMessage, error) {
	if err := validateCanonical("task_id", req.TaskID); err != nil {
		return nil, err
	}
	if err := validateCanonical("actor_open_id", req.ActorOpenID); err != nil {
		return nil, err
	}
	if err := validateCanonical("chat_id", req.ChatID); err != nil {
		return nil, err
	}
	if req.SourceMessageID != "" {
		if err := validateCanonical("source_message_id", req.SourceMessageID); err != nil {
			return nil, err
		}
	}
	values, err := scheduleEditValuesFromMap(req.NewValues)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ScheduleEditTrustedInput{
		Version:         1,
		TaskID:          req.TaskID,
		InitiatorOpenID: req.ActorOpenID,
		ChatID:          req.ChatID,
		SourceMessageID: req.SourceMessageID,
		NewValues:       values,
	})
}

func DecodeScheduleEditTrustedInput(raw json.RawMessage) (ScheduleEditTrustedInput, error) {
	var input ScheduleEditTrustedInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return ScheduleEditTrustedInput{}, invalidRuntimeContract("schedule edit trusted input is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ScheduleEditTrustedInput{}, invalidRuntimeContract("schedule edit trusted input is invalid")
	}
	if input.Version != 1 {
		return ScheduleEditTrustedInput{}, invalidRuntimeContract("schedule edit trusted input version is invalid")
	}
	if err := validateCanonical("task_id", input.TaskID); err != nil {
		return ScheduleEditTrustedInput{}, err
	}
	if err := validateCanonical("initiator_open_id", input.InitiatorOpenID); err != nil {
		return ScheduleEditTrustedInput{}, err
	}
	if err := validateCanonical("chat_id", input.ChatID); err != nil {
		return ScheduleEditTrustedInput{}, err
	}
	if input.SourceMessageID != "" {
		if err := validateCanonical("source_message_id", input.SourceMessageID); err != nil {
			return ScheduleEditTrustedInput{}, err
		}
	}
	if input.NewValues.empty() {
		return ScheduleEditTrustedInput{}, invalidRuntimeContract("schedule edit trusted values are empty")
	}
	return input, nil
}

func scheduleEditValuesFromMap(values map[string]any) (ScheduleEditValues, error) {
	var result ScheduleEditValues
	for key, value := range values {
		switch key {
		case "name":
			typed, ok := value.(string)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.Name = &typed
		case "cron_expr":
			typed, ok := value.(string)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.CronExpr = &typed
		case "timezone":
			typed, ok := value.(string)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.Timezone = &typed
		case "run_at":
			typed, ok := value.(time.Time)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.RunAt = &typed
		case "message":
			typed, ok := value.(string)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.Message = &typed
		case "notify_on_error":
			typed, ok := value.(bool)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.NotifyOnError = &typed
		case "notify_result":
			typed, ok := value.(bool)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.NotifyResult = &typed
		case "skip_holidays":
			typed, ok := value.(bool)
			if !ok {
				return ScheduleEditValues{}, invalidScheduleEditValue(key)
			}
			result.SkipHolidays = &typed
		default:
			return ScheduleEditValues{}, invalidRuntimeContract("schedule edit trusted input contains unsupported field")
		}
	}
	if result.empty() {
		return ScheduleEditValues{}, invalidRuntimeContract("schedule edit trusted values are empty")
	}
	return result, nil
}

func (v ScheduleEditValues) empty() bool {
	return v.Name == nil && v.CronExpr == nil && v.Timezone == nil && v.RunAt == nil &&
		v.Message == nil && v.NotifyOnError == nil && v.NotifyResult == nil && v.SkipHolidays == nil
}

func invalidScheduleEditValue(field string) error {
	return invalidRuntimeContract(fmt.Sprintf("schedule edit trusted field %q has invalid type", strings.TrimSpace(field)))
}
