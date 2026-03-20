package cardaction

import (
	"errors"
	"fmt"
	"maps"
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
	ActionWordChunksView         = "wordcount.chunks.view"
	ActionWordChunkDetail        = "wordcount.chunk.detail"
	ActionAgentRuntimeResume     = "agent.runtime.resume"
	ActionAgentRuntimeReject     = "agent.runtime.reject"

	RunIDField    = "run_id"
	StepIDField   = "step_id"
	RevisionField = "revision"
	SourceField   = "source"
	TokenField    = "token"
)

var (
	ErrNilCardAction = errors.New("card action is nil")
	ErrMissingAction = errors.New("card action name is missing")

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
}

func Parse(event *callback.CardActionTriggerEvent) (*Parsed, error) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return nil, ErrNilCardAction
	}

	value := maps.Clone(event.Event.Action.Value)
	formValue := maps.Clone(event.Event.Action.FormValue)

	if legacyType, ok := stringValue(value, LegacyTypeField); ok {
		if name, ok := legacyActionName(legacyType, value); ok {
			return newParsed(name, event, value, formValue), nil
		}
	}

	if name, ok := stringValue(value, ActionField); ok {
		return newParsed(name, event, value, formValue), nil
	}

	if len(formValue) > 0 {
		if _, ok := stringValue(value, CommandField); ok {
			return newParsed(ActionCommandSubmitTimeRange, event, value, formValue), nil
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
	return parsed
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

func (b *Builder) WithRevision(revision string) *Builder {
	return b.WithValue(RevisionField, revision)
}

func (b *Builder) WithSource(source string) *Builder {
	return b.WithValue(SourceField, source)
}

func (b *Builder) WithToken(token string) *Builder {
	return b.WithValue(TokenField, token)
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
