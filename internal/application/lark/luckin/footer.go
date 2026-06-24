package luckin

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

// AppendInitiatorFooter 在卡片底部追加"由 @发起人 发起"的小灰字。
// 由发送/patch 卡片的统一入口调用，避免每个 Build*Card 都串入 OrderSession。
//
// 飞书侧 <at id="..."></at> 在 markdown 元素中可正常渲染为 mention，
// 在 hint markdown 中渲染为灰色文本但仍能解析 user_id；这里使用 hint 以低调收尾。
func AppendInitiatorFooter(card map[string]any, initiatorOpenID string) map[string]any {
	openID := strings.TrimSpace(initiatorOpenID)
	if openID == "" || card == nil {
		return card
	}
	body, ok := card["body"].(map[string]any)
	if !ok || body == nil {
		return card
	}
	elements, _ := body["elements"].([]any)
	elements = append(elements,
		larkmsg.Divider(),
		larkmsg.HintMarkdown("👤 由 "+larkmsg.AtUserMD(openID)+" 发起"),
	)
	body["elements"] = elements
	return card
}

// IsInitiator 判断该 OpenID 是否是当前点单流程的发起人。
// 发起人未锁定（空 OpenID）时返回 false，调用方应直接走"会话已过期"路径。
func IsInitiator(sess OrderSession, openID string) bool {
	openID = strings.TrimSpace(openID)
	target := strings.TrimSpace(sess.InitiatorOpenID)
	if openID == "" || target == "" {
		return false
	}
	return openID == target
}

// CanModifyLine 判断 openID 是否可以对某条购物车行做加减/删除：
// 该行的加入者本人，或者发起人。
func CanModifyLine(sess OrderSession, item CartItem, openID string) bool {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return false
	}
	if openID == strings.TrimSpace(item.AddedByOpenID) {
		return true
	}
	return openID == strings.TrimSpace(sess.InitiatorOpenID)
}
