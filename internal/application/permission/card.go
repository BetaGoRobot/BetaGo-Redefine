package permission

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type ActionResponse struct {
	Success bool
	Message string
}

func BuildPermissionCardJSON(ctx context.Context, chatID, actorUserID, targetUserID string) (map[string]any, error) {
	if err := EnsureManageAllowed(ctx, actorUserID); err != nil {
		return nil, err
	}
	actorUserID = strings.TrimSpace(actorUserID)
	targetUserID = strings.TrimSpace(targetUserID)
	if targetUserID == "" {
		targetUserID = actorUserID
	}

	identity := botidentity.Current()
	grants, err := permissioninfra.ListBySubject(ctx, permissioninfra.ListFilter{
		SubjectType: permissioninfra.SubjectTypeUser,
		SubjectID:   targetUserID,
		AppID:       identity.AppID,
		BotOpenID:   identity.BotOpenID,
	})
	if err != nil {
		return nil, err
	}

	registered := permissioninfra.ListPointDefinitions()
	indexed := indexGrants(grants)
	covered := make(map[string]struct{})

	elements := make([]any, 0, len(registered)*3+8)
	elements = append(elements, larkmsg.Markdown(fmt.Sprintf(
		"当前机器人: app=`%s` bot=`%s`\n操作者: `%s`\n目标用户: `%s`",
		shortID(identity.AppID),
		shortID(identity.BotOpenID),
		shortID(actorUserID),
		shortID(targetUserID),
	)))
	if bootstrapAdmin := CurrentBootstrapAdminOpenID(); bootstrapAdmin != "" {
		elements = append(elements, larkmsg.HintMarkdown(fmt.Sprintf("bootstrap admin: `%s`", shortID(bootstrapAdmin))))
	}
	elements = append(elements, buildTargetUserForm(actorUserID, targetUserID))
	elements = append(elements, larkmsg.Divider())

	for idx, point := range registered {
		elements = append(elements, buildPointSection(point, targetUserID, indexed, covered))
		if idx < len(registered)-1 {
			elements = append(elements, larkmsg.Divider())
		}
	}

	extra := extraGrants(grants, covered)
	if len(extra) > 0 {
		elements = append(elements, larkmsg.Divider())
		elements = append(elements, larkmsg.Markdown("**其他已存在授权**\n这些授权已存在于数据库中，但当前没有注册到权限点清单。"))
		for idx, grant := range extra {
			elements = append(elements, buildExtraGrantSection(grant))
			if idx < len(extra)-1 {
				elements = append(elements, larkmsg.Divider())
			}
		}
	}

	elements = append(elements, larkmsg.HintMarkdown("点击按钮会直接授权或回收；bootstrap admin 仅来自 config.toml，不会自动写入数据库。"))
	card := larkmsg.NewCardV2("权限面板", elements, larkmsg.CardV2Options{
		HeaderTemplate:  "wathet",
		VerticalSpacing: "8px",
	})
	return map[string]any(card), nil
}

func HandleAction(ctx context.Context, req *ActionRequest) (*ActionResponse, error) {
	if req == nil {
		return &ActionResponse{Success: false, Message: "permission action request is nil"}, fmt.Errorf("permission action request is nil")
	}
	if err := EnsureManageAllowed(ctx, req.ActorUserID); err != nil {
		return &ActionResponse{Success: false, Message: err.Error()}, err
	}
	req.TargetUserID = strings.TrimSpace(req.TargetUserID)
	req.PermissionPoint = strings.TrimSpace(req.PermissionPoint)
	req.Scope = strings.TrimSpace(req.Scope)
	if req.TargetUserID == "" || req.PermissionPoint == "" || req.Scope == "" {
		return &ActionResponse{Success: false, Message: "permission action is incomplete"}, fmt.Errorf("permission action is incomplete")
	}

	identity := botidentity.Current()
	switch req.Action {
	case ActionGrant:
		def, ok := permissioninfra.LookupPointDefinition(req.PermissionPoint)
		if !ok {
			return &ActionResponse{Success: false, Message: fmt.Sprintf("unknown permission point: %s", req.PermissionPoint)}, fmt.Errorf("unknown permission point: %s", req.PermissionPoint)
		}
		if !permissioninfra.SupportsScope(def, req.Scope) {
			return &ActionResponse{Success: false, Message: fmt.Sprintf("permission point %s does not support scope %s", req.PermissionPoint, req.Scope)}, fmt.Errorf("permission point %s does not support scope %s", req.PermissionPoint, req.Scope)
		}
		if err := permissioninfra.Upsert(ctx, req.grantModel(identity.AppID, identity.BotOpenID)); err != nil {
			return &ActionResponse{Success: false, Message: err.Error()}, err
		}
		return &ActionResponse{
			Success: true,
			Message: fmt.Sprintf("已授予 `%s@%s` 给 `%s`", req.PermissionPoint, req.Scope, req.TargetUserID),
		}, nil
	case ActionRevoke:
		if err := permissioninfra.Revoke(ctx, req.grantFilter(identity.AppID, identity.BotOpenID)); err != nil {
			return &ActionResponse{Success: false, Message: err.Error()}, err
		}
		return &ActionResponse{
			Success: true,
			Message: fmt.Sprintf("已回收 `%s@%s` 的授权，目标用户 `%s`", req.PermissionPoint, req.Scope, req.TargetUserID),
		}, nil
	default:
		return &ActionResponse{Success: false, Message: fmt.Sprintf("unsupported permission action: %s", req.Action)}, fmt.Errorf("unsupported permission action: %s", req.Action)
	}
}

func buildTargetUserForm(actorUserID, targetUserID string) map[string]any {
	return map[string]any{
		"tag":                "form",
		"name":               "permission_target_form",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			larkmsg.ColumnSet([]any{
				larkmsg.Column([]any{
					larkmsg.Markdown("**查看目标用户**\n输入 OpenID 后刷新卡片"),
				}, larkmsg.ColumnOptions{
					Width:         "weighted",
					Weight:        2,
					VerticalAlign: "top",
				}),
				larkmsg.Column([]any{
					map[string]any{
						"tag":           "input",
						"name":          cardactionproto.TargetUserIDField,
						"width":         "fill",
						"placeholder":   map[string]any{"tag": "plain_text", "content": "输入目标用户 OpenID"},
						"default_value": targetUserID,
					},
					larkmsg.ButtonRow("none",
						larkmsg.Button("查看用户", larkmsg.ButtonOptions{
							Type:           "primary",
							Name:           "btn_permission_view",
							FormActionType: "submit",
							Payload:        toAnyMap(BuildPermissionViewValue(targetUserID)),
						}),
						larkmsg.Button("查看自己", larkmsg.ButtonOptions{
							Type:    "default",
							Payload: toAnyMap(BuildPermissionViewValue(actorUserID)),
						}),
					),
				}, larkmsg.ColumnOptions{
					Width:         "weighted",
					Weight:        3,
					VerticalAlign: "top",
				}),
			}, larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			}),
		},
	}
}

func buildPointSection(def permissioninfra.PointDefinition, targetUserID string, indexed map[string]permissioninfra.Grant, covered map[string]struct{}) map[string]any {
	scopeBlocks := make([]any, 0, len(def.SupportedScopes))
	for _, scope := range def.SupportedScopes {
		key := grantKey(def.Point, scope, "", "")
		grant, granted := indexed[key]
		if granted {
			covered[key] = struct{}{}
		}
		scopeBlocks = append(scopeBlocks, buildScopeControl(def.Point, scope, targetUserID, grant, granted))
	}

	return larkmsg.ColumnSet([]any{
		larkmsg.Column([]any{
			larkmsg.Markdown(fmt.Sprintf("**%s**  `%s`\n%s\n分类: `%s`", def.Name, def.Point, def.Description, def.Category)),
		}, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        3,
			VerticalAlign: "top",
		}),
		larkmsg.Column(scopeBlocks, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        2,
			VerticalAlign: "top",
		}),
	}, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "16px",
		FlexMode:          "stretch",
	})
}

func buildScopeControl(point, scope, targetUserID string, grant permissioninfra.Grant, granted bool) map[string]any {
	statusText := fmt.Sprintf("scope: `%s`\n状态: `%s`", scope, ternary(granted, "granted", "not granted"))
	if granted && grant.Remark != "" {
		statusText += fmt.Sprintf("\n备注: %s", grant.Remark)
	}

	button := larkmsg.Button("授予", larkmsg.ButtonOptions{
		Type:    "primary",
		Payload: toAnyMap(BuildPermissionGrantValue(point, scope, targetUserID, deref(grant.ResourceChatID), deref(grant.ResourceUserID))),
	})
	if granted {
		button = larkmsg.Button("回收", larkmsg.ButtonOptions{
			Type:    "danger",
			Payload: toAnyMap(BuildPermissionRevokeValue(point, scope, targetUserID, deref(grant.ResourceChatID), deref(grant.ResourceUserID))),
		})
	}

	return larkmsg.ColumnSet([]any{
		larkmsg.Column([]any{
			larkmsg.Markdown(statusText),
		}, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        3,
			VerticalAlign: "top",
		}),
		larkmsg.Column([]any{
			button,
		}, larkmsg.ColumnOptions{
			Width:         "auto",
			VerticalAlign: "top",
		}),
	}, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "8px",
		FlexMode:          "stretch",
		Margin:            "0px 0px 4px 0px",
	})
}

func buildExtraGrantSection(grant permissioninfra.Grant) map[string]any {
	resource := rawGrantResourceText(grant)
	content := fmt.Sprintf("`%s@%s`\n%s", grant.PermissionPoint, grant.Scope, resource)
	if grant.Remark != "" {
		content += fmt.Sprintf("\n备注: %s", grant.Remark)
	}

	return larkmsg.ColumnSet([]any{
		larkmsg.Column([]any{
			larkmsg.Markdown(content),
		}, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        3,
			VerticalAlign: "top",
		}),
		larkmsg.Column([]any{
			larkmsg.ButtonRow("flow",
				larkmsg.Button("回收", larkmsg.ButtonOptions{
					Type: "danger",
					Payload: toAnyMap(BuildPermissionRevokeValue(
						grant.PermissionPoint,
						grant.Scope,
						grant.SubjectID,
						deref(grant.ResourceChatID),
						deref(grant.ResourceUserID),
					)),
				}),
			),
		}, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        1,
			VerticalAlign: "top",
		}),
	}, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "12px",
		FlexMode:          "stretch",
	})
}

func indexGrants(grants []permissioninfra.Grant) map[string]permissioninfra.Grant {
	result := make(map[string]permissioninfra.Grant, len(grants))
	for _, grant := range grants {
		result[grantKey(grant.PermissionPoint, grant.Scope, deref(grant.ResourceChatID), deref(grant.ResourceUserID))] = grant
	}
	return result
}

func extraGrants(grants []permissioninfra.Grant, covered map[string]struct{}) []permissioninfra.Grant {
	result := make([]permissioninfra.Grant, 0)
	for _, grant := range grants {
		key := grantKey(grant.PermissionPoint, grant.Scope, deref(grant.ResourceChatID), deref(grant.ResourceUserID))
		if _, ok := covered[key]; ok {
			continue
		}
		result = append(result, grant)
	}
	slices.SortFunc(result, func(a, b permissioninfra.Grant) int {
		switch {
		case a.PermissionPoint < b.PermissionPoint:
			return -1
		case a.PermissionPoint > b.PermissionPoint:
			return 1
		case a.Scope < b.Scope:
			return -1
		case a.Scope > b.Scope:
			return 1
		default:
			return 0
		}
	})
	return result
}

func grantKey(point, scope, resourceChatID, resourceUserID string) string {
	return strings.Join([]string{point, scope, resourceChatID, resourceUserID}, "|")
}

func rawGrantResourceText(grant permissioninfra.Grant) string {
	parts := make([]string, 0, 2)
	if grant.ResourceChatID != nil && strings.TrimSpace(*grant.ResourceChatID) != "" {
		parts = append(parts, "chat="+shortID(*grant.ResourceChatID))
	}
	if grant.ResourceUserID != nil && strings.TrimSpace(*grant.ResourceUserID) != "" {
		parts = append(parts, "user="+shortID(*grant.ResourceUserID))
	}
	if len(parts) == 0 {
		return "资源: `-`"
	}
	return "资源: `" + strings.Join(parts, " ") + "`"
}

func toAnyMap(payload map[string]string) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	keys := slices.Sorted(maps.Keys(payload))
	values := make(map[string]any, len(keys))
	for _, key := range keys {
		values[key] = payload[key]
	}
	return values
}

func sanitizeComponentName(value string) string {
	value = strings.NewReplacer("-", "_", ".", "_", ":", "_", " ", "_", "|", "_").Replace(value)
	if value == "" {
		return "field"
	}
	return value
}

func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func ternary[T any](cond bool, yes, no T) T {
	if cond {
		return yes
	}
	return no
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
