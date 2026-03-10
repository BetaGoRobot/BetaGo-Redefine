package config

import (
	"fmt"
	"strings"

	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func BuildFeatureActionValue(action FeatureAction, feature, chatID, userID string) map[string]string {
	name, ok := featureActionName(action)
	if !ok {
		return nil
	}

	return cardaction.New(name).
		WithValue(cardaction.FeatureField, feature).
		WithValue(cardaction.ChatIDField, chatID).
		WithValue(cardaction.UserIDField, userID).
		Payload()
}

func BuildConfigActionValue(action ConfigAction, key, value, scope, chatID, userID string) map[string]string {
	builder, ok := newConfigActionBuilder(action, key, scope, chatID, userID)
	if !ok {
		return nil
	}
	if action == ConfigActionSet {
		builder.WithValue(cardaction.ValueField, value)
	}
	return builder.Payload()
}

func BuildConfigFormActionValue(key, scope, chatID, userID, formField string) map[string]string {
	builder, ok := newConfigActionBuilder(ConfigActionSet, key, scope, chatID, userID)
	if !ok {
		return nil
	}
	builder.WithFormField(formField)
	return builder.Payload()
}

func BuildConfigInputActionValue(key, scope, chatID, userID string) map[string]string {
	builder, ok := newConfigActionBuilder(ConfigActionSet, key, scope, chatID, userID)
	if !ok {
		return nil
	}
	return builder.Payload()
}

func newConfigActionBuilder(action ConfigAction, key, scope, chatID, userID string) (*cardaction.Builder, bool) {
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
		WithValue(cardaction.ScopeField, scope).
		WithValue(cardaction.ChatIDField, chatID).
		WithValue(cardaction.UserIDField, userID)
	return builder, true
}

func BuildConfigViewValue(scope, chatID, userID string) map[string]string {
	return cardaction.New(cardaction.ActionConfigViewScope).
		WithValue(cardaction.ScopeField, scope).
		WithValue(cardaction.ChatIDField, chatID).
		WithValue(cardaction.UserIDField, userID).
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
	userID, _ := parsed.String(cardaction.UserIDField)

	return &FeatureActionRequest{
		Action:  action,
		Feature: feature,
		ChatID:  chatID,
		UserID:  userID,
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
	userID, _ := parsed.String(cardaction.UserIDField)
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
		Action: action,
		Key:    key,
		Value:  value,
		Scope:  scope,
		ChatID: chatID,
		UserID: userID,
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
	Scope  string `json:"scope"`
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id"`
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
	userID, _ := parsed.String(cardaction.UserIDField)
	return &ConfigViewRequest{
		Scope:  scope,
		ChatID: chatID,
		UserID: userID,
	}, nil
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
