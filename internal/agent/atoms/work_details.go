package atoms

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const todoTagsSchema = `{"type":"array","maxItems":20,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":64}}`
const todoDateSchema = `{"type":"string","minLength":20,"maxLength":64}`
const todoInputSchema = `{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":1024},"description":{"type":"string","maxLength":8192},"priority":{"type":"string","enum":["low","medium","high","urgent"]},"due_at":` + todoDateSchema + `,"tags":` + todoTagsSchema + `,"assignee_id":{"type":"string","maxLength":1024}},"required":["title"],"additionalProperties":false}`
const todoUpdateSchema = `{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128},"title":{"type":"string","minLength":1,"maxLength":1024},"description":{"type":"string","maxLength":8192},"status":{"type":"string","enum":["open","completed","canceled"]},"priority":{"type":"string","enum":["","low","medium","high","urgent"]},"due_at":` + todoDateSchema + `,"clear_due_at":{"type":"boolean"},"tags":` + todoTagsSchema + `,"add_tags":` + todoTagsSchema + `,"remove_tags":` + todoTagsSchema + `,"assignee_id":{"type":"string","maxLength":1024}},"required":["id"],"additionalProperties":false}`
const todoQuerySchema = `{"type":"object","properties":{"status":{"type":"string","enum":["open","completed","canceled"]},"priority":{"type":"string","enum":["low","medium","high","urgent"]},"creator_id":{"type":"string","minLength":1,"maxLength":1024},"assignee_id":{"type":"string","minLength":1,"maxLength":1024},"unassigned":{"type":"boolean"},"tags":` + todoTagsSchema + `,"due_before":` + todoDateSchema + `,"due_after":` + todoDateSchema + `,"overdue":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":50},"cursor":{"type":"string","minLength":1,"maxLength":2048}},"additionalProperties":false}`

func todoDetailsDefinitions(o Options) []Definition {
	if o.Tasks == nil {
		return nil
	}
	return []Definition{
		{Name: "todo_detail", Description: "Read one complete group-visible todo by ID, including immutable creator, priority, due time, tags, assignee, status and source. No cross-group reads.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128}},"required":["id"],"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a domain.TodoDeleteInput
			if json.Unmarshal([]byte(raw), &a) != nil {
				return "", domain.ErrInvalid
			}
			item, err := o.Tasks.TodoDetail(ctx, in.Task.Scope, a.ID)
			if err != nil {
				return "", err
			}
			return encodeCommunity(todoView(item))
		}},
		{Name: "todo_delete", Description: "Delete the current speaker's own todo by marking it canceled; the record, immutable creator and audit source remain readable. Other members' items cannot be deleted. Ordinary action without approval.", SideEffect: true, AutoExecute: true, Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128}},"required":["id"],"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a domain.TodoDeleteInput
			if json.Unmarshal([]byte(raw), &a) != nil {
				return "", domain.ErrInvalid
			}
			item, err := o.Tasks.DeleteTodo(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a.ID)
			if err != nil {
				return "", err
			}
			return encodeCommunity(todoView(item))
		}},
	}
}
