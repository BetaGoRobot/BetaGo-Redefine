package config

import (
	"fmt"
	"strings"

	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

const (
	configLastModifierField  = "config_last_modifier_open_id"
	featureLastModifierField = "feature_last_modifier_open_id"
	configSelectedKeyField   = "config_selected_key"
	configViewKeyFormField   = "config_view_key_form_field"
)

type ConfigViewState struct {
	Scope              string
	ChatID             string
	OpenID             string
	LastModifierOpenID string
	SelectedKey        string
}

type FeatureViewState struct {
	ChatID             string
	OpenID             string
	LastModifierOpenID string
}

func BuildFeatureActionValue(action FeatureAction, feature, chatID, openID string) map[string]string {
	return BuildFeatureActionValueWithState(action, feature, FeatureViewState{
		ChatID: chatID,
		OpenID: openID,
	})
}

func BuildFeatureActionValueWithState(action FeatureAction, feature string, state FeatureViewState) map[string]string {
	name, ok := featureActionName(action)
	if !ok {
		return nil
	}

	return cardaction.New(name).
		WithValue(cardaction.FeatureField, feature).
		WithValue(cardaction.ChatIDField, state.ChatID).
		WithValue(cardaction.UserIDField, state.OpenID).
		WithValue(featureLastModifierField, state.LastModifierOpenID).
		Payload()
}

func BuildFeatureViewValue(chatID, openID string) map[string]string {
	return BuildFeatureViewValueWithState(FeatureViewState{
		ChatID: chatID,
		OpenID: openID,
	})
}

func BuildFeatureViewValueWithState(state FeatureViewState) map[string]string {
	return cardaction.New(cardaction.ActionFeatureView).
		WithValue(cardaction.ChatIDField, state.ChatID).
		WithValue(cardaction.UserIDField, state.OpenID).
		WithValue(featureLastModifierField, state.LastModifierOpenID).
		Payload()
}

func BuildConfigActionValue(action ConfigAction, key, value, scope, chatID, openID string) map[string]string {
	return BuildConfigActionValueWithState(action, key, value, ConfigViewState{
		Scope:  scope,
		ChatID: chatID,
		OpenID: openID,
	})
}

func BuildConfigActionValueWithState(action ConfigAction, key, value string, state ConfigViewState) map[string]string {
	builder, ok := newConfigActionBuilder(action, key, state)
	if !ok {
		return nil
	}
	if action == ConfigActionSet {
		builder.WithValue(cardaction.ValueField, value)
	}
	return builder.Payload()
}

func BuildConfigFormActionValue(key, scope, chatID, openID, formField string) map[string]string {
	return BuildConfigFormActionValueWithState(key, ConfigViewState{
		Scope:  scope,
		ChatID: chatID,
		OpenID: openID,
	}, formField)
}

func BuildConfigFormActionValueWithState(key string, state ConfigViewState, formField string) map[string]string {
	builder, ok := newConfigActionBuilder(ConfigActionSet, key, state)
	if !ok {
		return nil
	}
	builder.WithFormField(formField)
	return builder.Payload()
}

func BuildConfigInputActionValue(key, scope, chatID, openID string) map[string]string {
	builder, ok := newConfigActionBuilder(ConfigActionSet, key, ConfigViewState{
		Scope:  scope,
		ChatID: chatID,
		OpenID: openID,
	})
	if !ok {
		return nil
	}
	return builder.Payload()
}

func newConfigActionBuilder(action ConfigAction, key string, state ConfigViewState) (*cardaction.Builder, bool) {
	var actionName string
	switch action {
	case ConfigActionSet:
		actionName = cardaction.ActionConfigSet
	case ConfigActionDelete:
		actionName = cardaction.ActionConfigDelete
	default:
		return nil, false
	}

	builder := cardaction.New(actionName).
		WithValue(cardaction.KeyField, key).
		WithValue(cardaction.ScopeField, state.Scope).
		WithValue(cardaction.ChatIDField, state.ChatID).
		WithValue(cardaction.UserIDField, state.OpenID).
		WithValue(configLastModifierField, state.LastModifierOpenID).
		WithValue(configSelectedKeyField, state.SelectedKey)
	return builder, true
}

func BuildConfigViewValue(scope, chatID, openID string) map[string]string {
	return BuildConfigViewValueWithState(ConfigViewState{
		Scope:  scope,
		ChatID: chatID,
		OpenID: openID,
	})
}

func BuildConfigViewValueWithState(state ConfigViewState) map[string]string {
	builder := cardaction.New(cardaction.ActionConfigViewScope).
		WithValue(cardaction.ScopeField, state.Scope).
		WithValue(cardaction.ChatIDField, state.ChatID).
		WithValue(cardaction.UserIDField, state.OpenID).
		WithValue(configLastModifierField, state.LastModifierOpenID).
		WithValue(configSelectedKeyField, state.SelectedKey)
	return builder.Payload()
}

func BuildConfigViewFormValueWithState(state ConfigViewState, keyFormField string) map[string]string {
	return cardaction.New(cardaction.ActionConfigViewScope).
		WithValue(cardaction.ScopeField, state.Scope).
		WithValue(cardaction.ChatIDField, state.ChatID).
		WithValue(cardaction.UserIDField, state.OpenID).
		WithValue(configLastModifierField, state.LastModifierOpenID).
		WithValue(configSelectedKeyField, state.SelectedKey).
		WithValue(configViewKeyFormField, keyFormField).
		Payload()
}

func ParseFeatureActionRequest(parsed *cardaction.Parsed) (*FeatureActionRequest, error) {
	action, ok := featureActionFromName(parsed.Name)
	if !ok {
		return nil, fmt.Errorf("unsupported feature action: %s", parsed.Name)
	}

	feature, err := parsed.RequiredString(cardaction.FeatureField)
	if err != nil {
		return nil, err
	}

	chatID, _ := parsed.String(cardaction.ChatIDField)
	openID, _ := parsed.String(cardaction.UserIDField)

	return &FeatureActionRequest{
		Action:             action,
		Feature:            feature,
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: readConfigActionValue(parsed, featureLastModifierField),
	}, nil
}

func ParseConfigActionRequest(parsed *cardaction.Parsed) (*ConfigActionRequest, error) {
	if parsed.Name != cardaction.ActionConfigSet && parsed.Name != cardaction.ActionConfigDelete {
		return nil, fmt.Errorf("unsupported config action: %s", parsed.Name)
	}

	key, err := parsed.RequiredString(cardaction.KeyField)
	if err != nil {
		return nil, err
	}
	scope, err := parsed.RequiredString(cardaction.ScopeField)
	if err != nil {
		return nil, err
	}
	chatID, _ := parsed.String(cardaction.ChatIDField)
	openID, _ := parsed.String(cardaction.UserIDField)
	action := ConfigActionSet
	value := ""
	if parsed.Name == cardaction.ActionConfigDelete {
		action = ConfigActionDelete
	} else {
		if value, _ = parsed.String(cardaction.ValueField); value == "" {
			value = parsed.InputValue
		}
		if value == "" {
			formField, _ := parsed.String(cardaction.FormFieldField)
			value, err = resolveConfigFormValue(parsed, formField)
			if err != nil {
				return nil, err
			}
		}
	}
	value = strings.TrimSpace(value)
	if action == ConfigActionSet && value == "" {
		return nil, fmt.Errorf("config action %s has empty value", parsed.Name)
	}

	return &ConfigActionRequest{
		Action:             action,
		Key:                key,
		Value:              value,
		Scope:              scope,
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: readConfigActionValue(parsed, configLastModifierField),
		SelectedKey:        readConfigActionValue(parsed, configSelectedKeyField),
	}, nil
}

func resolveConfigFormValue(parsed *cardaction.Parsed, formField string) (string, error) {
	if formField != "" {
		if value, ok := parsed.FormString(formField); ok {
			return value, nil
		}
		return "", fmt.Errorf("config form field %q is missing", formField)
	}

	for _, raw := range parsed.FormValue {
		if value, ok := raw.(string); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("config action missing value")
}

type ConfigViewRequest struct {
	Scope              string `json:"scope"`
	ChatID             string `json:"chat_id"`
	OpenID             string `json:"user_id"`
	LastModifierOpenID string `json:"last_modifier_open_id,omitempty"`
	SelectedKey        string `json:"selected_key,omitempty"`
}

type FeatureViewRequest struct {
	ChatID             string `json:"chat_id"`
	OpenID             string `json:"user_id"`
	LastModifierOpenID string `json:"last_modifier_open_id,omitempty"`
}

func ParseConfigViewRequest(parsed *cardaction.Parsed) (*ConfigViewRequest, error) {
	if parsed.Name != cardaction.ActionConfigViewScope {
		return nil, fmt.Errorf("unsupported config view action: %s", parsed.Name)
	}
	scope, err := parsed.RequiredString(cardaction.ScopeField)
	if err != nil {
		return nil, err
	}
	chatID, _ := parsed.String(cardaction.ChatIDField)
	openID, _ := parsed.String(cardaction.UserIDField)
	selectedKey := readConfigActionValue(parsed, configSelectedKeyField)
	if strings.TrimSpace(selectedKey) == "" {
		keyFormField, _ := parsed.String(configViewKeyFormField)
		selectedKey, _ = resolveConfigFormValue(parsed, keyFormField)
	}
	return &ConfigViewRequest{
		Scope:              scope,
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: readConfigActionValue(parsed, configLastModifierField),
		SelectedKey:        strings.TrimSpace(selectedKey),
	}, nil
}

func ParseFeatureViewRequest(parsed *cardaction.Parsed) (*FeatureViewRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("feature view action is nil")
	}
	if parsed.Name != cardaction.ActionFeatureView {
		return nil, fmt.Errorf("unsupported feature view action: %s", parsed.Name)
	}
	chatID, _ := parsed.String(cardaction.ChatIDField)
	openID, _ := parsed.String(cardaction.UserIDField)
	return &FeatureViewRequest{
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: readConfigActionValue(parsed, featureLastModifierField),
	}, nil
}

func readConfigActionValue(parsed *cardaction.Parsed, key string) string {
	if parsed == nil {
		return ""
	}
	if value, ok := parsed.FormString(key); ok {
		return strings.TrimSpace(value)
	}
	if value, ok := parsed.String(key); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func featureActionName(action FeatureAction) (string, bool) {
	switch action {
	case FeatureActionBlockChat:
		return cardaction.ActionFeatureBlockChat, true
	case FeatureActionUnblockChat:
		return cardaction.ActionFeatureUnblockChat, true
	case FeatureActionBlockUser:
		return cardaction.ActionFeatureBlockUser, true
	case FeatureActionUnblockUser:
		return cardaction.ActionFeatureUnblockUser, true
	case FeatureActionBlockChatUser:
		return cardaction.ActionFeatureBlockChatUser, true
	case FeatureActionUnblockChatUser:
		return cardaction.ActionFeatureUnblockChatUser, true
	default:
		return "", false
	}
}

func featureActionFromName(name string) (FeatureAction, bool) {
	switch name {
	case cardaction.ActionFeatureBlockChat:
		return FeatureActionBlockChat, true
	case cardaction.ActionFeatureUnblockChat:
		return FeatureActionUnblockChat, true
	case cardaction.ActionFeatureBlockUser:
		return FeatureActionBlockUser, true
	case cardaction.ActionFeatureUnblockUser:
		return FeatureActionUnblockUser, true
	case cardaction.ActionFeatureBlockChatUser:
		return FeatureActionBlockChatUser, true
	case cardaction.ActionFeatureUnblockChatUser:
		return FeatureActionUnblockChatUser, true
	default:
		return "", false
	}
}
