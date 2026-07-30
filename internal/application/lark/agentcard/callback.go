package agentcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type CallbackStore interface {
	GetByInteraction(context.Context, GetSurfaceRequest) (*CardSurface, error)
	ClaimAction(context.Context, ClaimActionRequest) (*ActionClaim, error)
}

type CallbackDispatcherOptions struct {
	Store    CallbackStore
	Compiler ArtifactCompiler
	Now      func() time.Time
}

type CallbackDispatcher struct {
	store    CallbackStore
	compiler ArtifactCompiler
	now      func() time.Time
}

func NewCallbackDispatcher(
	options CallbackDispatcherOptions,
) (*CallbackDispatcher, error) {
	if options.Store == nil || options.Compiler == nil {
		return nil, errors.New("agent card callback store and compiler are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &CallbackDispatcher{
		store: options.Store, compiler: options.Compiler, now: options.Now,
	}, nil
}

func (d *CallbackDispatcher) CanHandle(action *cardactionproto.Parsed) bool {
	return action != nil && action.Name == cardactionproto.ActionAgentRuntimeResume
}

func (d *CallbackDispatcher) Dispatch(
	ctx context.Context,
	request appcardaction.ContinuationRequest,
) (*callback.CardActionTriggerResponse, error) {
	if d == nil || d.store == nil || d.compiler == nil ||
		request.Action == nil || !d.CanHandle(request.Action) ||
		request.Action.Runtime == nil {
		return nil, appcardaction.ErrUnhandledAction
	}
	action := request.Action
	runtime := action.Runtime
	if runtime.InteractionKind != "agent_card" {
		return nil, errors.New("invalid agent card interaction kind")
	}
	if action.Source.MessageID == "" || action.Source.ChatID == "" ||
		action.Source.OperatorOpenID == "" {
		return nil, errors.New("agent card callback source is incomplete")
	}
	surface, err := d.store.GetByInteraction(ctx, GetSurfaceRequest{
		RunID: runtime.RunID, InteractionID: runtime.InteractionID,
	})
	if err != nil {
		return nil, err
	}
	if surface.WaitStepID != runtime.StepID ||
		surface.MessageID != action.Source.MessageID ||
		surface.ChatID != action.Source.ChatID ||
		surface.Revision != runtime.Revision {
		return nil, ErrCardConflict
	}
	var spec CardSpec
	if json.Unmarshal([]byte(surface.SpecJSON), &spec) != nil {
		return nil, ErrCardConflict
	}
	publicAction, ok := findCardAction(spec, runtime.ActionID)
	if !ok {
		return nil, ErrCardConflict
	}
	if _, err := NormalizeActionOutcome(
		spec,
		runtime.ActionID,
		action.FormValue,
		action.NameField,
		action.InputValue,
		action.Option,
		action.Options,
		action.Checked,
	); err != nil {
		return nil, err
	}
	desired := desiredStatusForAction(publicAction)
	state, err := lifecycleStateForStatus(desired)
	if err != nil {
		return nil, err
	}
	bound, err := NewBoundCardSpec(spec, state, nil)
	if err != nil {
		return nil, ErrCardConflict
	}
	compiled, err := d.compiler.CompileJSON(bound)
	if err != nil || !json.Valid(compiled) || jsonDocumentContainsToken(compiled) {
		return nil, ErrCardCompileFailed
	}
	sourceRef := callbackSourceRef(action)
	claim, err := d.store.ClaimAction(ctx, ClaimActionRequest{
		RunID: runtime.RunID, StepID: runtime.StepID,
		InteractionID:    runtime.InteractionID,
		ExpectedRevision: runtime.Revision, ActionID: runtime.ActionID,
		ActorOpenID: action.Source.OperatorOpenID,
		MessageID:   action.Source.MessageID, ChatID: action.Source.ChatID,
		PresentedToken: runtime.Token, InteractionKind: runtime.InteractionKind,
		ContinueAgent: runtime.ContinueAgent, SourceRef: sourceRef,
		EventID: action.Source.EventID, FormValues: cloneAnyMap(action.FormValue),
		InputName: action.NameField, InputValue: action.InputValue,
		SelectedOption:  action.Option,
		SelectedOptions: append([]string(nil), action.Options...),
		Checked:         action.Checked, CompiledJSONRedacted: string(compiled),
		DesiredStatus: desired, ClaimedAt: d.now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	var responseCard map[string]any
	if json.Unmarshal(compiled, &responseCard) != nil || responseCard == nil {
		return nil, ErrCardCompileFailed
	}
	switch claim.Descriptor.Mode {
	case ActionModeCapabilityConfirm:
		return appcardaction.InfoToastWithRawCardPayload(
			"已确认，正在处理",
			responseCard,
		), nil
	case ActionModeServer:
		return appcardaction.InfoToastWithRawCardPayload(
			"操作已完成",
			responseCard,
		), nil
	default:
		if publicAction.Kind == ActionCancel {
			return appcardaction.InfoToastWithRawCardPayload(
				"已取消",
				responseCard,
			), nil
		}
		return appcardaction.InfoToastWithRawCardPayload(
			"已提交",
			responseCard,
		), nil
	}
}

func NormalizeActionOutcome(
	spec CardSpec,
	actionID string,
	formValues map[string]any,
	inputName string,
	inputValue string,
	selectedOption string,
	selectedOptions []string,
	checked bool,
) (json.RawMessage, error) {
	action, ok := findCardAction(spec, actionID)
	if !ok {
		return nil, ErrCardConflict
	}
	fields := collectFormFields(spec, action.FormRef)
	values := cloneAnyMap(formValues)
	if values == nil {
		values = make(map[string]any)
	}
	if inputName != "" {
		if _, exists := values[inputName]; !exists {
			switch {
			case inputValue != "":
				values[inputName] = inputValue
			case len(selectedOptions) != 0:
				values[inputName] = append([]string(nil), selectedOptions...)
			case selectedOption != "":
				values[inputName] = selectedOption
			}
		}
	}
	if action.Kind == ActionCancel {
		values = map[string]any{}
	}
	normalized := make(map[string]any, len(values))
	for fieldID, raw := range values {
		field, exists := fields[fieldID]
		if !exists {
			return nil, ErrCardConflict
		}
		value, err := normalizeFieldValue(field, raw)
		if err != nil {
			return nil, ErrCardConflict
		}
		normalized[fieldID] = value
	}
	for fieldID, field := range fields {
		if field.required {
			value, exists := normalized[fieldID]
			if !exists || emptyNormalizedValue(value) {
				return nil, ErrCardConflict
			}
		}
	}
	encoded, err := json.Marshal(map[string]any{
		"version": 1, "action_id": action.ID, "intent": action.Intent,
		"action_kind": action.Kind, "action_mode": action.Mode,
		"form_values": normalized, "checked": checked,
	})
	if err != nil || len(encoded) > MaxFormResultBytes {
		return nil, ErrCardConflict
	}
	return encoded, nil
}

type formFieldDefinition struct {
	kind      BlockKind
	required  bool
	minLength int
	maxLength int
	options   map[string]struct{}
}

func collectFormFields(spec CardSpec, formID string) map[string]formFieldDefinition {
	result := make(map[string]formFieldDefinition)
	if formID == "" {
		return result
	}
	var visit func([]Block)
	visit = func(blocks []Block) {
		for _, block := range blocks {
			switch block.Kind {
			case BlockTextInput:
				if block.TextInput != nil &&
					block.TextInput.Field.FormID == formID {
					result[block.TextInput.Field.FieldID] = formFieldDefinition{
						kind: block.Kind, required: block.TextInput.Field.Required,
						minLength: block.TextInput.Config.MinLength,
						maxLength: block.TextInput.Config.MaxLength,
					}
				}
			case BlockSingleSelect, BlockMultiSelect:
				var selected *SelectBlock
				if block.Kind == BlockSingleSelect {
					selected = block.SingleSelect
				} else {
					selected = block.MultiSelect
				}
				if selected != nil && selected.Field.FormID == formID {
					options := make(map[string]struct{}, len(selected.Options))
					for _, option := range selected.Options {
						options[option.Value] = struct{}{}
					}
					result[selected.Field.FieldID] = formFieldDefinition{
						kind: block.Kind, required: selected.Field.Required,
						options: options,
					}
				}
			case BlockSection:
				if block.Section != nil {
					visit(block.Section.Blocks)
				}
			case BlockColumns:
				if block.Columns != nil {
					for _, column := range block.Columns.Columns {
						visit(column.Blocks)
					}
				}
			}
		}
	}
	visit(spec.Blocks)
	return result
}

func normalizeFieldValue(
	field formFieldDefinition,
	raw any,
) (any, error) {
	switch field.kind {
	case BlockTextInput:
		value, ok := raw.(string)
		if !ok || !utf8.ValidString(value) {
			return nil, ErrCardConflict
		}
		length := utf8.RuneCountInString(value)
		if length < field.minLength ||
			(field.maxLength > 0 && length > field.maxLength) {
			return nil, ErrCardConflict
		}
		return value, nil
	case BlockSingleSelect:
		value, ok := raw.(string)
		if !ok {
			return nil, ErrCardConflict
		}
		if _, allowed := field.options[value]; !allowed {
			return nil, ErrCardConflict
		}
		return value, nil
	case BlockMultiSelect:
		values, ok := stringSliceValue(raw)
		if !ok {
			return nil, ErrCardConflict
		}
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if _, allowed := field.options[value]; !allowed {
				return nil, ErrCardConflict
			}
			if _, duplicate := seen[value]; duplicate {
				return nil, ErrCardConflict
			}
			seen[value] = struct{}{}
		}
		sort.Strings(values)
		return values, nil
	default:
		return nil, ErrCardConflict
	}
}

func stringSliceValue(raw any) ([]string, bool) {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, value)
		}
		return result, true
	default:
		return nil, false
	}
}

func emptyNormalizedValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(typed) == 0
	default:
		return value == nil
	}
}

func findCardAction(spec CardSpec, actionID string) (Action, bool) {
	for _, action := range spec.Actions {
		if action.ID == actionID {
			return action, true
		}
	}
	return Action{}, false
}

func desiredStatusForAction(action Action) SurfaceStatus {
	if action.Kind == ActionCancel {
		return SurfaceStatusCancelled
	}
	switch action.Mode {
	case ActionModeCapabilityConfirm:
		return SurfaceStatusProcessing
	case ActionModeServer:
		return SurfaceStatusResolved
	default:
		return SurfaceStatusSubmitted
	}
}

func callbackSourceRef(action *cardactionproto.Parsed) string {
	if action.Source.EventID != "" {
		return "event:" + action.Source.EventID
	}
	form, _ := json.Marshal(action.FormValue)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		action.Source.MessageID,
		action.Source.OperatorOpenID,
		action.Runtime.ActionID,
		string(form),
	}, "\x00")))
	return "callback:" + hex.EncodeToString(sum[:])
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

var _ appcardaction.ContinuationDispatcher = (*CallbackDispatcher)(nil)
