package engine

import (
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// ApprovalCard renders complete frozen bytes as plain text, and binds v2
// callback behavior to string-valued task, revision and approval identifiers.
func ApprovalCard(t domain.Task, a domain.Approval) (string, error) {
	value := func(action string) map[string]string {
		return map[string]string{"action": action, "task_id": t.ID, "revision": fmt.Sprint(t.Revision), "approval_id": a.ID}
	}
	button := func(label, kind, action string) map[string]any {
		return map[string]any{"tag": "button", "text": map[string]string{"tag": "plain_text", "content": label}, "type": kind, "behaviors": []any{map[string]any{"type": "callback", "value": value(action)}}}
	}
	column := func(b any) map[string]any {
		return map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{b}}
	}
	text := fmt.Sprintf("任务 %s · 版本 %d\n负责人 %s\n工具 %s\n完整参数：\n%s", t.ID, t.Revision, t.OwnerID, a.Tool, a.Arguments)
	card := map[string]any{"schema": "2.0", "header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "确认执行操作"}, "template": "orange"}, "body": map[string]any{"elements": []any{map[string]any{"tag": "div", "text": map[string]string{"tag": "plain_text", "content": text}}, map[string]any{"tag": "column_set", "columns": []any{column(button("确认", "primary", "approve")), column(button("取消任务", "danger", "cancel"))}}}}}
	b, e := json.Marshal(card)
	if len(b) > 28<<10 {
		return "", domain.ErrInvalid
	}
	return string(b), e
}
