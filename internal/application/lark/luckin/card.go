package luckin

import cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"

func ScopeLabel(scope CredentialScope) string {
	switch scope.Type {
	case ScopePersonal:
		return "个人瑞幸账号"
	case ScopeChat:
		return "群聊默认瑞幸账号"
	case ScopeSystem:
		return "系统默认瑞幸账号"
	default:
		return "未知瑞幸账号"
	}
}

func BuildPendingOrderCard(order PendingOrder) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": "**瑞幸订单确认**"},
				map[string]any{"tag": "markdown", "content": "账号作用域：" + ScopeLabel(order.CredentialScope)},
				map[string]any{"tag": "markdown", "content": "点击确认将创建瑞幸订单，但不会自动支付。"},
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "确认下单"},
					"type": "primary",
					"behaviors": []any{map[string]any{
						"type": "callback",
						"value": map[string]any{
							cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderConfirm,
							cardactionproto.PendingOrderIDField: order.ID,
							cardactionproto.PayloadHashField:    order.PayloadHash,
						},
					}},
				},
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "取消"},
					"type": "default",
					"behaviors": []any{map[string]any{
						"type": "callback",
						"value": map[string]any{
							cardactionproto.ActionField:         cardactionproto.ActionLuckinOrderCancel,
							cardactionproto.PendingOrderIDField: order.ID,
						},
					}},
				},
			},
		},
	}
}
