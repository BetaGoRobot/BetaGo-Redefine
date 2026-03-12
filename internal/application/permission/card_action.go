package permission

import (
	"fmt"
	"strings"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

const permissionLastModifierField = "permission_last_modifier_open_id"

const (
	permissionViewSelectField      = "permission_view_select_field"
	permissionViewSelectTargetUser = "target_open_id"
)

type Action string

const (
	ActionGrant  Action = "grant"
	ActionRevoke Action = "revoke"
)

type ViewRequest struct {
	TargetOpenID       string
	LastModifierOpenID string
}

type ActionRequest struct {
	Action             Action
	PermissionPoint    string
	Scope              string
	TargetOpenID       string
	ResourceChatID     string
	ResourceUserID     string
	ActorOpenID        string
	LastModifierOpenID string
}

func BuildPermissionViewValue(targetOpenID string) map[string]string {
	return BuildPermissionViewValueWithOptions(targetOpenID, "")
}

func BuildPermissionViewValueWithOptions(targetOpenID, lastModifierOpenID string) map[string]string {
	return cardactionproto.New(cardactionproto.ActionPermissionView).
		WithValue(cardactionproto.TargetUserIDField, strings.TrimSpace(targetOpenID)).
		WithValue(permissionLastModifierField, strings.TrimSpace(lastModifierOpenID)).
		Payload()
}

func BuildPermissionTargetPickerValue(lastModifierOpenID string) map[string]string {
	return cardactionproto.New(cardactionproto.ActionPermissionView).
		WithValue(permissionLastModifierField, strings.TrimSpace(lastModifierOpenID)).
		WithValue(permissionViewSelectField, permissionViewSelectTargetUser).
		Payload()
}

func BuildPermissionGrantValue(point, scope, targetOpenID, resourceChatID, resourceUserID string) map[string]string {
	return BuildPermissionGrantValueWithOptions(point, scope, targetOpenID, resourceChatID, resourceUserID, "")
}

func BuildPermissionGrantValueWithOptions(point, scope, targetOpenID, resourceChatID, resourceUserID, lastModifierOpenID string) map[string]string {
	return buildPermissionActionValue(cardactionproto.ActionPermissionGrant, point, scope, targetOpenID, resourceChatID, resourceUserID, lastModifierOpenID)
}

func BuildPermissionRevokeValue(point, scope, targetOpenID, resourceChatID, resourceUserID string) map[string]string {
	return BuildPermissionRevokeValueWithOptions(point, scope, targetOpenID, resourceChatID, resourceUserID, "")
}

func BuildPermissionRevokeValueWithOptions(point, scope, targetOpenID, resourceChatID, resourceUserID, lastModifierOpenID string) map[string]string {
	return buildPermissionActionValue(cardactionproto.ActionPermissionRevoke, point, scope, targetOpenID, resourceChatID, resourceUserID, lastModifierOpenID)
}

func buildPermissionActionValue(actionName, point, scope, targetOpenID, resourceChatID, resourceUserID, lastModifierOpenID string) map[string]string {
	return cardactionproto.New(actionName).
		WithValue(cardactionproto.PermissionPointField, strings.TrimSpace(point)).
		WithValue(cardactionproto.ScopeField, strings.TrimSpace(scope)).
		WithValue(cardactionproto.TargetUserIDField, strings.TrimSpace(targetOpenID)).
		WithValue(cardactionproto.ResourceChatIDField, strings.TrimSpace(resourceChatID)).
		WithValue(cardactionproto.ResourceUserIDField, strings.TrimSpace(resourceUserID)).
		WithValue(permissionLastModifierField, strings.TrimSpace(lastModifierOpenID)).
		Payload()
}

func ParseViewRequest(parsed *cardactionproto.Parsed) (*ViewRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("permission view action is nil")
	}
	if parsed.Name != "" && parsed.Name != cardactionproto.ActionPermissionView {
		return nil, fmt.Errorf("unsupported permission view action: %s", parsed.Name)
	}
	targetOpenID := readActionValue(parsed, cardactionproto.TargetUserIDField)
	if shouldApplyPermissionTargetPicker(parsed) {
		if selected := parsed.SelectedOption(); selected != "" {
			targetOpenID = selected
		}
	}
	return &ViewRequest{
		TargetOpenID:       strings.TrimSpace(targetOpenID),
		LastModifierOpenID: strings.TrimSpace(readActionValue(parsed, permissionLastModifierField)),
	}, nil
}

func ParseActionRequest(parsed *cardactionproto.Parsed) (*ActionRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("permission action is nil")
	}

	var action Action
	switch parsed.Name {
	case cardactionproto.ActionPermissionGrant:
		action = ActionGrant
	case cardactionproto.ActionPermissionRevoke:
		action = ActionRevoke
	default:
		return nil, fmt.Errorf("unsupported permission action: %s", parsed.Name)
	}

	point, err := parsed.RequiredString(cardactionproto.PermissionPointField)
	if err != nil {
		return nil, err
	}
	scope, err := parsed.RequiredString(cardactionproto.ScopeField)
	if err != nil {
		return nil, err
	}
	targetOpenID, err := parsed.RequiredString(cardactionproto.TargetUserIDField)
	if err != nil {
		return nil, err
	}

	return &ActionRequest{
		Action:             action,
		PermissionPoint:    strings.TrimSpace(point),
		Scope:              strings.TrimSpace(scope),
		TargetOpenID:       strings.TrimSpace(targetOpenID),
		ResourceChatID:     strings.TrimSpace(readActionValue(parsed, cardactionproto.ResourceChatIDField)),
		ResourceUserID:     strings.TrimSpace(readActionValue(parsed, cardactionproto.ResourceUserIDField)),
		LastModifierOpenID: strings.TrimSpace(readActionValue(parsed, permissionLastModifierField)),
	}, nil
}

func (r *ActionRequest) grantModel(identityBotAppID, identityBotOpenID string) permissioninfra.Grant {
	return permissioninfra.Grant{
		SubjectType:     permissioninfra.SubjectTypeUser,
		SubjectID:       r.TargetOpenID,
		PermissionPoint: r.PermissionPoint,
		Scope:           r.Scope,
		AppID:           identityBotAppID,
		BotOpenID:       identityBotOpenID,
		ResourceChatID:  nullableString(r.ResourceChatID),
		ResourceUserID:  nullableString(r.ResourceUserID),
		Remark:          fmt.Sprintf("granted by %s via permission panel", r.ActorOpenID),
	}
}

func (r *ActionRequest) grantFilter(identityBotAppID, identityBotOpenID string) permissioninfra.GrantFilter {
	return permissioninfra.GrantFilter{
		SubjectType:     permissioninfra.SubjectTypeUser,
		SubjectID:       r.TargetOpenID,
		PermissionPoint: r.PermissionPoint,
		Scope:           r.Scope,
		AppID:           identityBotAppID,
		BotOpenID:       identityBotOpenID,
		ResourceChatID:  r.ResourceChatID,
		ResourceUserID:  r.ResourceUserID,
	}
}

func readActionValue(parsed *cardactionproto.Parsed, key string) string {
	if parsed == nil {
		return ""
	}
	if value, ok := parsed.FormString(key); ok {
		return value
	}
	if value, ok := parsed.String(key); ok {
		return value
	}
	return ""
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func shouldApplyPermissionTargetPicker(parsed *cardactionproto.Parsed) bool {
	if parsed == nil {
		return false
	}
	if strings.TrimSpace(parsed.Tag) != "select_person" {
		return false
	}
	return strings.TrimSpace(readActionValue(parsed, permissionViewSelectField)) == permissionViewSelectTargetUser
}
