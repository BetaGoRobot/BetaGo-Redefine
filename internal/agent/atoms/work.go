package atoms

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func todoView(t domain.Todo) any {
	return map[string]any{"id": t.ID, "creator_id": t.CreatedBy, "title": t.Title, "description": t.Description, "status": t.Status, "priority": t.Priority, "due_at": t.DueAt, "tags": t.Tags, "assignee_id": t.AssigneeID, "created_at": t.CreatedAt, "assignment_policy": "self_or_unassigned", "origin": t.Origin, "updated_at": t.UpdatedAt}
}
func reminderView(r domain.Reminder) any {
	deliveryState := "not_attempted"
	if !r.SendStartedAt.IsZero() {
		deliveryState = "unconfirmed"
	}
	if r.MessageID != "" {
		deliveryState = "confirmed"
	}
	return map[string]any{"delivery_state": deliveryState, "id": r.ID, "creator_id": r.CreatedBy, "text": r.Text, "status": r.Status, "due_at": r.DueAt, "origin": r.Origin, "updated_at": r.UpdatedAt}
}

func workDefinitions(o Options) []Definition {
	if o.Tasks == nil {
		return nil
	}
	definitions := []Definition{
		{Name: "todo_create", Description: "Create a group-visible todo owned by the server-verified current speaker. Priority low/medium/high/urgent (default medium), optional RFC3339 due_at and exact tags. Assignee defaults to the creator; only assignment to self is supported. Saves without approval. A due date does not schedule a reminder.", SideEffect: true, AutoExecute: true, Parameters: json.RawMessage(todoInputSchema), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.TodoInput
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.CreateTodo(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a)
			if err != nil {
				return "", err
			}
			return encodeCommunity(todoView(out))
		}},
		{Name: "todo_list", Description: "List group-visible todos with creator/assignee/status/priority, all-tags and inclusive due_before/due_after RFC3339 filters. overdue selects open dated items before the first page's anchor. unassigned selects no assignee. Sorted by creation time descending with stable cursor pagination; use returned next_cursor with unchanged filters. Other members' items are readable but only the creator may mutate.", Parameters: json.RawMessage(todoQuerySchema), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.TodoQuery
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			page, err := o.Tasks.QueryTodos(ctx, in.Task.Scope, a)
			if err != nil {
				return "", err
			}
			rows := []any{}
			for _, item := range page.Items {
				rows = append(rows, todoView(item))
			}
			return encodeCommunity(map[string]any{"items": rows, "next_cursor": page.NextCursor})
		}},
		{Name: "todo_update", Description: "Update only the current speaker's own todo. Unset fields stay unchanged. Empty description clears it; priority empty resets medium; clear_due_at=true clears the deadline (cannot combine due_at). tags=[] clears tags; add_tags/remove_tags change tags incrementally and cannot combine with tags. assignee_id empty unassigns; otherwise only self is allowed. Can complete, cancel or reopen. Creator is immutable.", SideEffect: true, AutoExecute: true, Parameters: json.RawMessage(todoUpdateSchema), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.TodoUpdateInput
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.UpdateTodo(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a.ID, a.TodoChange)
			if err != nil {
				return "", err
			}
			return encodeCommunity(todoView(out))
		}},
		{Name: "reminder_create", Description: "Schedule one plain-text reminder in this group for the current speaker, without approval. due_at is a future RFC3339 timestamp with timezone, within 366 days. Independent of this conversation task; no arbitrary tool execution. Use current_time when relative timing needs calculation.", SideEffect: true, AutoExecute: true, Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"due_at":{"type":"string","minLength":20,"maxLength":64}},"required":["text","due_at"],"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.ReminderInput
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.CreateReminder(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a)
			if err != nil {
				return "", err
			}
			return encodeCommunity(reminderView(out))
		}},
		{Name: "reminder_list", Description: "List this group's one-time reminders and their actual delivery status. Overdue reminders become expired. delivery_state=unconfirmed means an attempt may already have arrived; expiry/cancellation alone does not prove non-delivery. Only the creator can cancel an item.", Parameters: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["pending","sent","canceled","expired"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			if a.Limit == 0 {
				a.Limit = 20
			}
			rows, err := o.Tasks.ListReminders(ctx, in.Task.Scope, a.Status, a.Limit)
			if err != nil {
				return "", err
			}
			out := []any{}
			for _, item := range rows {
				out = append(out, reminderView(item))
			}
			return encodeCommunity(map[string]any{"items": out})
		}},
		{Name: "reminder_cancel", Description: "Cancel an unsent reminder owned by the current speaker in this group, without approval. Other members' reminders cannot be canceled. A send already in flight may still arrive.", SideEffect: true, AutoExecute: true, Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128}},"required":["id"],"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.ReminderCancelInput
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.CancelReminder(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a.ID)
			if err != nil {
				return "", err
			}
			return encodeCommunity(reminderView(out))
		}},
	}
	return append(definitions, todoDetailsDefinitions(o)...)
}
