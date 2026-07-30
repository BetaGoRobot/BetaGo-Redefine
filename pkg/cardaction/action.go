package cardaction

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

const (
	ActionField          = "action"
	IDField              = "id"
	CommandField         = "command"
	ViewField            = "view"
	KeyField             = "key"
	QueryField           = "query"
	ValueField           = "value"
	FormFieldField       = "form_field"
	SceneField           = "scene"
	PageField            = "page"
	PageSizeField        = "page_size"
	ScopeField           = "scope"
	ChatIDField          = "chat_id"
	UserIDField          = "user_id"
	TargetUserIDField    = "target_user_id"
	FeatureField         = "feature"
	PermissionPointField = "permission_point"
	ResourceChatIDField  = "resource_chat_id"
	ResourceUserIDField  = "resource_user_id"
	LegacyTypeField      = "type"

	ActionMusicPlay              = "music.play"
	ActionMusicVoicePlay         = "music.voice_play"
	ActionMusicAlbum             = "music.album"
	ActionMusicLyrics            = "music.lyrics"
	ActionMusicRefresh           = "music.refresh"
	ActionMusicListPage          = "music.list_page"
	ActionCardWithdraw           = "card.withdraw"
	ActionCommandOpenHelp        = "command.open_help"
	ActionCommandOpenForm        = "command.open_form"
	ActionCommandRefresh         = "command.refresh"
	ActionCommandSubmitForm      = "command.submit_form"
	ActionCommandSubmitTimeRange = "command.submit_time_range"
	ActionFeatureView            = "feature.view"
	ActionFeatureBlockChat       = "feature.block_chat"
	ActionFeatureUnblockChat     = "feature.unblock_chat"
	ActionFeatureBlockUser       = "feature.block_user"
	ActionFeatureUnblockUser     = "feature.unblock_user"
	ActionFeatureBlockChatUser   = "feature.block_chat_user"
	ActionFeatureUnblockChatUser = "feature.unblock_chat_user"
	ActionConfigSet              = "config.set"
	ActionConfigDelete           = "config.delete"
	ActionConfigViewScope        = "config.view_scope"
	ActionPermissionGrant        = "permission.grant"
	ActionPermissionRevoke       = "permission.revoke"
	ActionPermissionView         = "permission.view"
	ActionRateLimitView          = "ratelimit.view"
	ActionScheduleView           = "schedule.view"
	ActionSchedulePause          = "schedule.pause"
	ActionScheduleResume         = "schedule.resume"
	ActionScheduleDelete         = "schedule.delete"
	ActionScheduleEditConfirm    = "schedule.edit_confirm"
	ActionScheduleEditCancel     = "schedule.edit_cancel"
	ActionWordChunksView         = "wordcount.chunks.view"
	ActionWordChunkDetail        = "wordcount.chunk.detail"
	ActionAgentRuntimeResume     = "agent.runtime.resume"
	ActionAgentRuntimeReject     = "agent.runtime.reject"
	ActionLuckinOrderConfirm     = "luckin_order_confirm"
	ActionLuckinOrderCancel      = "luckin_order_cancel"
	ActionLuckinShopSelect       = "luckin_shop_select"
	ActionLuckinRegionSelect     = "luckin_region_select"
	ActionLuckinShopSearch       = "luckin_shop_search_card"
	ActionLuckinProductQuery     = "luckin_product_query"
	ActionLuckinProductSelect    = "luckin_product_select"
	ActionLuckinCartUpdate       = "luckin_cart_update"
	ActionLuckinCartRemove       = "luckin_cart_remove"
	ActionLuckinCartCheckout     = "luckin_cart_checkout"
	ActionLuckinCartContinue     = "luckin_cart_continue"
	ActionLuckinCouponApply      = "luckin_coupon_apply"
	ActionLuckinOrderStatus      = "luckin_order_status"
	ActionLuckinBindToken        = "luckin_bind_token"
	ActionLuckinUnbindToken      = "luckin_unbind_token"
	ActionLuckinViewScope        = "luckin_view_scope"

	RunIDField           = "run_id"
	StepIDField          = "step_id"
	InteractionIDField   = "interaction_id"
	RevisionField        = "revision"
	SourceField          = "source"
	TokenField           = "token"
	InteractionKindField = "interaction_kind"
	ContinueAgentField   = "continue_agent"
	ActionIDField        = "action_id"

	ApprovalDeliveryField = "approval_delivery"
	PendingOrderIDField   = "pending_order_id"
	PayloadHashField      = "payload_hash"

	LuckinDeptIDField       = "luckin_dept_id"
	LuckinDeptNameField     = "luckin_dept_name"
	LuckinLongitudeField    = "luckin_longitude"
	LuckinLatitudeField     = "luckin_latitude"
	LuckinProductIDField    = "luckin_product_id"
	LuckinSkuCodeField      = "luckin_sku_code"
	LuckinProductName       = "luckin_product_name"
	LuckinUnitPriceField    = "luckin_unit_price"
	LuckinImageKeyField     = "luckin_image_key"
	LuckinCustomizeField    = "luckin_customize"
	LuckinQueryFormField    = "luckin_query"
	LuckinLocationFormField = "luckin_location"
	LuckinProvinceFormField = "luckin_province"
	LuckinRegionFormField   = "luckin_region"
	LuckinQtyFormField      = "luckin_qty"
	LuckinTokenFormField    = "luckin_token"
	LuckinScopeFormField    = "luckin_scope"
	LuckinCouponFormField   = "luckin_coupon"
	LuckinCheckoutModeField = "luckin_checkout_mode"
	LuckinOrderIDField      = "luckin_order_id"
	LuckinStatusModeField   = "luckin_status_mode"
	// LuckinLineIDField 购物车每行的稳定唯一 ID（uuid）。+/-/删除按钮 payload 使用它定位条目，
	// 避免不同发起人加入同 SKU 时按 productID+skuCode 互相覆盖。
	LuckinLineIDField = "luckin_line_id"
	// LuckinSpecFormFieldPrefix + attributeId 组成规格选择表单字段名。
	LuckinSpecFormFieldPrefix = "luckin_spec_"
	// LuckinCouponFieldPrefix + 序号 组成确认卡上每个可用优惠券的勾选字段名。
	LuckinCouponFieldPrefix = "luckin_coupon_"
)

var (
	ErrNilCardAction            = errors.New("card action is nil")
	ErrMissingAction            = errors.New("card action name is missing")
	ErrPartialRuntimeEnvelope   = errors.New("card action has a partial runtime envelope")
	ErrMalformedRuntimeEnvelope = errors.New("card action has a malformed runtime envelope")

	legacyActionAliases = map[string]string{
		"song":        ActionMusicPlay,
		"album":       ActionMusicAlbum,
		"lyrics":      ActionMusicLyrics,
		"refresh":     ActionMusicRefresh,
		"withdraw":    ActionCardWithdraw,
		"refresh_obj": ActionCommandRefresh,
	}
)

type Parsed struct {
	Name       string
	Tag        string
	NameField  string
	Value      map[string]any
	FormValue  map[string]any
	InputValue string
	Option     string
	Options    []string
	Checked    bool
	Runtime    *RuntimeEnvelope
	Source     CallbackSource
}

type RuntimeEnvelope struct {
	RunID           string
	StepID          string
	InteractionID   string
	Revision        int64
	Token           string
	InteractionKind string
	ContinueAgent   bool
	ActionID        string
}

type CallbackSource struct {
	EventID        string
	MessageID      string
	ChatID         string
	OperatorOpenID string
}

func Parse(event *callback.CardActionTriggerEvent) (*Parsed, error) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return nil, ErrNilCardAction
	}

	value := maps.Clone(event.Event.Action.Value)
	formValue := maps.Clone(event.Event.Action.FormValue)

	var name string
	var found bool
	if legacyType, ok := stringValue(value, LegacyTypeField); ok {
		if name, ok := legacyActionName(legacyType, value); ok {
			parsed := newParsed(name, event, value, formValue)
			if err := parseRuntimeEnvelope(parsed); err != nil {
				return nil, err
			}
			return parsed, nil
		}
	}

	if name, found = stringValue(value, ActionField); found {
		parsed := newParsed(name, event, value, formValue)
		if err := parseRuntimeEnvelope(parsed); err != nil {
			return nil, err
		}
		return parsed, nil
	}

	if len(formValue) > 0 {
		if _, ok := stringValue(value, CommandField); ok {
			parsed := newParsed(ActionCommandSubmitTimeRange, event, value, formValue)
			if err := parseRuntimeEnvelope(parsed); err != nil {
				return nil, err
			}
			return parsed, nil
		}
	}

	return nil, ErrMissingAction
}

func (p *Parsed) String(key string) (string, bool) {
	return stringValue(p.Value, key)
}

func (p *Parsed) RequiredString(key string) (string, error) {
	value, ok := p.String(key)
	if !ok {
		return "", fmt.Errorf("card action missing string field %q", key)
	}
	return value, nil
}

func (p *Parsed) FormString(key string) (string, bool) {
	return stringValue(p.FormValue, key)
}

func (p *Parsed) SelectedOption() string {
	if p == nil {
		return ""
	}
	if option := strings.TrimSpace(p.Option); option != "" {
		return option
	}
	if len(p.Options) == 1 {
		return strings.TrimSpace(p.Options[0])
	}
	return ""
}

func newParsed(name string, event *callback.CardActionTriggerEvent, value, formValue map[string]any) *Parsed {
	parsed := &Parsed{
		Name:      name,
		Value:     value,
		FormValue: formValue,
	}
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return parsed
	}

	action := event.Event.Action
	parsed.Tag = action.Tag
	parsed.NameField = action.Name
	parsed.InputValue = action.InputValue
	parsed.Option = action.Option
	parsed.Options = append(parsed.Options, action.Options...)
	parsed.Checked = action.Checked
	if event.Event.Context != nil {
		parsed.Source.MessageID = strings.TrimSpace(event.Event.Context.OpenMessageID)
		parsed.Source.ChatID = strings.TrimSpace(event.Event.Context.OpenChatID)
	}
	if event.Event.Operator != nil {
		parsed.Source.OperatorOpenID = strings.TrimSpace(event.Event.Operator.OpenID)
	}
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		parsed.Source.EventID = strings.TrimSpace(event.EventV2Base.Header.EventID)
	}
	return parsed
}

func parseRuntimeEnvelope(parsed *Parsed) error {
	if parsed == nil {
		return nil
	}
	fields := []string{
		RunIDField, StepIDField, InteractionIDField, RevisionField,
		TokenField, InteractionKindField, ContinueAgentField,
	}
	if parsed.Name == ActionAgentRuntimeResume {
		fields = append(fields, ActionIDField)
	}
	present := 0
	for _, field := range fields {
		if _, ok := parsed.Value[field]; ok {
			present++
		}
	}
	if present == 0 {
		return nil
	}
	if present != len(fields) {
		return ErrPartialRuntimeEnvelope
	}
	runID, ok := nonemptyStringValue(parsed.Value, RunIDField)
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	stepID, ok := nonemptyStringValue(parsed.Value, StepIDField)
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	interactionID, ok := nonemptyStringValue(parsed.Value, InteractionIDField)
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	revision, ok := integerValue(parsed.Value[RevisionField])
	if !ok || revision <= 0 {
		return ErrMalformedRuntimeEnvelope
	}
	token, ok := nonemptyStringValue(parsed.Value, TokenField)
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	kind, ok := nonemptyStringValue(parsed.Value, InteractionKindField)
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	continues, ok := booleanValue(parsed.Value[ContinueAgentField])
	if !ok {
		return ErrMalformedRuntimeEnvelope
	}
	actionID := ""
	if parsed.Name == ActionAgentRuntimeResume {
		actionID, ok = nonemptyStringValue(parsed.Value, ActionIDField)
		if !ok {
			return ErrMalformedRuntimeEnvelope
		}
	}
	parsed.Runtime = &RuntimeEnvelope{
		RunID: runID, StepID: stepID, InteractionID: interactionID,
		Revision: revision, Token: token, InteractionKind: kind,
		ContinueAgent: continues, ActionID: actionID,
	}
	return nil
}

func nonemptyStringValue(values map[string]any, key string) (string, bool) {
	value, ok := stringValue(values, key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		result := int64(typed)
		return result, float64(result) == typed
	case string:
		result, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return result, err == nil
	default:
		return 0, false
	}
}

func booleanValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		result, err := strconv.ParseBool(strings.TrimSpace(typed))
		return result, err == nil
	default:
		return false, false
	}
}

func stringValue(values map[string]any, key string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	value, ok := values[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func legacyActionName(legacyType string, value map[string]any) (string, bool) {
	if aliased, ok := legacyActionAliases[legacyType]; ok {
		return aliased, true
	}

	switch legacyType {
	case "feature_action":
		operation, ok := stringValue(value, ActionField)
		if !ok {
			return "", false
		}
		switch operation {
		case "block_chat":
			return ActionFeatureBlockChat, true
		case "unblock_chat":
			return ActionFeatureUnblockChat, true
		case "block_user":
			return ActionFeatureBlockUser, true
		case "unblock_user":
			return ActionFeatureUnblockUser, true
		case "block_chat_user":
			return ActionFeatureBlockChatUser, true
		case "unblock_chat_user":
			return ActionFeatureUnblockChatUser, true
		default:
			return "", false
		}
	case "config_action":
		operation, ok := stringValue(value, ActionField)
		if !ok {
			return "", false
		}
		if operation == "set" {
			return ActionConfigSet, true
		}
		if operation == "delete" {
			return ActionConfigDelete, true
		}
	}

	return "", false
}

type Builder struct {
	values map[string]string
}

func New(name string) *Builder {
	return &Builder{
		values: map[string]string{
			ActionField: name,
		},
	}
}

func (b *Builder) WithValue(key, value string) *Builder {
	b.values[key] = value
	return b
}

func (b *Builder) WithID(id string) *Builder {
	return b.WithValue(IDField, id)
}

func (b *Builder) WithRunID(runID string) *Builder {
	return b.WithValue(RunIDField, runID)
}

func (b *Builder) WithStepID(stepID string) *Builder {
	return b.WithValue(StepIDField, stepID)
}

func (b *Builder) WithInteractionID(interactionID string) *Builder {
	return b.WithValue(InteractionIDField, interactionID)
}

func (b *Builder) WithRevision(revision string) *Builder {
	return b.WithValue(RevisionField, revision)
}

func (b *Builder) WithSource(source string) *Builder {
	return b.WithValue(SourceField, source)
}

func (b *Builder) WithToken(token string) *Builder {
	return b.WithValue(TokenField, token)
}

func (b *Builder) WithInteractionKind(interactionKind string) *Builder {
	return b.WithValue(InteractionKindField, interactionKind)
}

func (b *Builder) WithContinueAgent(continueAgent bool) *Builder {
	return b.WithValue(ContinueAgentField, strconv.FormatBool(continueAgent))
}

func (b *Builder) WithCommand(command string) *Builder {
	return b.WithValue(CommandField, command)
}

func (b *Builder) WithFormField(field string) *Builder {
	return b.WithValue(FormFieldField, field)
}

func (b *Builder) Payload() map[string]string {
	return maps.Clone(b.values)
}
