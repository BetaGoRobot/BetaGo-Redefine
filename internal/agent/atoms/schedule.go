package atoms

import (
	"context"
	"encoding/json"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	jsonschema "github.com/google/jsonschema-go/jsonschema"
)

func scheduleView(s domain.Schedule) any {
	return map[string]any{"id": s.ID, "creator_id": s.CreatedBy, "status": s.Status, "revision": s.Revision, "plan": s.Plan, "next_index": s.NextIndex, "next_at": s.NextAt(), "origin": s.Origin, "updated_at": s.UpdatedAt, "timing": "Elapsed seconds anchored at start_at; missed slots are skipped, without catch-up bursts. Completed means no future slots; inspect run receipts for actual delivery."}
}
func scheduleRunView(r domain.ScheduleRun) any {
	delivery := "not_attempted"
	if !r.SendStartedAt.IsZero() {
		delivery = "unconfirmed"
	}
	if r.MessageID != "" {
		delivery = "confirmed"
	}
	return map[string]any{"revision": r.Revision, "index": r.Index, "status": r.Status, "due_at": r.DueAt, "notify_until": r.NotifyUntil, "delivery_state": delivery, "message_id": r.MessageID, "failure_reason": r.FailureReason}
}

// ScheduleDefinitions composes the seven legacy management operations into
// create/list/query and one owner-only typed update operation.
func ScheduleDefinitions(o Options) []Definition {
	if o.Tasks == nil {
		return nil
	}
	createSchema, updateSchema, queries := scheduleSchemas(o)
	return []Definition{
		{Name: "schedule_create", Description: "Create the current speaker's recurring text notification or one configured data-query card in this group. start_at requires an explicit RFC3339 timezone; interval_seconds >=60; runs 1..366; final slot within 366 days. Query accepts only one configured read leaf with validated typed arguments. No model replay or tool chain. Independent of the conversation task. Ordinary own-work action; no approval. Missed slots are skipped after downtime.", SideEffect: true, AutoExecute: true, Parameters: createSchema, Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a domain.ScheduleInput
			if publicDataDecode(raw, &a) != nil || !scheduleQueryConfigured(a.Query, queries) {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.CreateSchedule(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a)
			if err != nil {
				return "", err
			}
			return encodeCommunity(scheduleView(out))
		}},
		{Name: "schedule_list", Description: "List group-visible recurring notification plans with their creators, statuses and actual next slot. Only the creator can change a plan. A completed plan has no future slots; delivery receipts are available through schedule_query.", Parameters: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["active","paused","canceled","completed","invalidated"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if publicDataDecode(raw, &a) != nil {
				return "", domain.ErrInvalid
			}
			if a.Limit == 0 {
				a.Limit = 20
			}
			rows, err := o.Tasks.ListSchedules(ctx, in.Task.Scope, a.Status, a.Limit)
			if err != nil {
				return "", err
			}
			items := []any{}
			for _, r := range rows {
				items = append(items, scheduleView(r))
			}
			return encodeCommunity(map[string]any{"items": items, "limit": a.Limit, "may_have_more": len(items) == a.Limit})
		}},
		{Name: "schedule_query", Description: "Read one recurring notification plan and bounded occurrence history in the current group. Confirmed provider message IDs prove delivery; an unconfirmed send may already have arrived. Pause/cancel can race with an in-flight send.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128},"limit":{"type":"integer","minimum":1,"maximum":50}},"required":["id"],"additionalProperties":false}`), Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a struct {
				ID    string `json:"id"`
				Limit int    `json:"limit"`
			}
			if publicDataDecode(raw, &a) != nil {
				return "", domain.ErrInvalid
			}
			if a.Limit == 0 {
				a.Limit = 20
			}
			plan, err := o.Tasks.QuerySchedule(ctx, in.Task.Scope, a.ID)
			if err != nil {
				return "", err
			}
			rows, err := o.Tasks.ScheduleRuns(ctx, in.Task.Scope, a.ID, a.Limit)
			if err != nil {
				return "", err
			}
			items := []any{}
			for _, r := range rows {
				items = append(items, scheduleRunView(r))
			}
			return encodeCommunity(map[string]any{"schedule": scheduleView(plan), "runs": items, "limit": a.Limit, "may_have_more": len(items) == a.Limit})
		}},
		{Name: "schedule_update", Description: "Change the current speaker's own recurring notification: pause, resume, cancel (legacy delete), or edit typed text/timing fields. Only edit accepts changed fields. New start_at must be future; resume skips missed slots. Cancel is durable and preserves occurrence receipts. An already in-flight send may still arrive.", SideEffect: true, AutoExecute: true, Parameters: updateSchema, Run: func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a domain.ScheduleUpdateInput
			if publicDataDecode(raw, &a) != nil || !scheduleQueryConfigured(a.Query, queries) {
				return "", domain.ErrInvalid
			}
			out, err := o.Tasks.UpdateSchedule(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a)
			if err != nil {
				return "", err
			}
			return encodeCommunity(scheduleView(out))
		}},
	}
}

func scheduleQueryConfigured(q *domain.ScheduleQuery, defs map[string]*jsonschema.Resolved) bool {
	if q == nil {
		return true
	}
	v := defs[q.Name]
	if v == nil {
		return false
	}
	var value any
	return json.Unmarshal(q.Arguments, &value) == nil && v.Validate(value) == nil
}
func scheduleSchemas(o Options) (json.RawMessage, json.RawMessage, map[string]*jsonschema.Resolved) {
	queries := map[string]*jsonschema.Resolved{}
	branches := []any{}
	for _, d := range o.Extensions {
		if !domain.ScheduleQueryAllowed(d.Name) || d.SideEffect || d.Run == nil {
			continue
		}
		var sc jsonschema.Schema
		if json.Unmarshal(d.Parameters, &sc) != nil {
			continue
		}
		v, err := sc.Resolve(nil)
		if err != nil {
			continue
		}
		queries[d.Name] = v
		branches = append(branches, map[string]any{"type": "object", "properties": map[string]any{"tool_name": map[string]any{"type": "string", "const": d.Name}, "arguments": d.Parameters}, "required": []string{"tool_name", "arguments"}, "additionalProperties": false})
	}
	query := map[string]any{"type": "object", "oneOf": branches}
	createProps := map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}, "start_at": map[string]any{"type": "string", "minLength": 20, "maxLength": 64}, "interval_seconds": map[string]any{"type": "integer", "minimum": 60, "maximum": 31622400}, "runs": map[string]any{"type": "integer", "minimum": 1, "maximum": 366}}
	if len(branches) > 0 {
		createProps["query"] = query
	}
	create := map[string]any{"type": "object", "properties": createProps, "required": []string{"start_at", "interval_seconds", "runs"}, "additionalProperties": false, "oneOf": []any{map[string]any{"required": []string{"text"}}, map[string]any{"required": []string{"query"}}}}
	if len(branches) == 0 {
		create["required"] = []string{"text", "start_at", "interval_seconds", "runs"}
		delete(create, "oneOf")
	}
	updateProps := map[string]any{}
	for k, v := range createProps {
		updateProps[k] = v
	}
	updateProps["id"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	updateProps["action"] = publicEnum("pause", "resume", "cancel", "edit")
	changes := []any{}
	for _, key := range []string{"text", "query", "start_at", "interval_seconds", "runs"} {
		if _, ok := updateProps[key]; ok {
			changes = append(changes, map[string]any{"required": []string{key}})
		}
	}
	update := map[string]any{"type": "object", "properties": updateProps, "required": []string{"id", "action"}, "additionalProperties": false, "not": map[string]any{"required": []string{"text", "query"}}, "if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "edit"}}}, "then": map[string]any{"anyOf": changes}, "else": map[string]any{"not": map[string]any{"anyOf": changes}}}
	a, _ := json.Marshal(create)
	b, _ := json.Marshal(update)
	return a, b, queries
}
