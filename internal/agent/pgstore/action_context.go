package pgstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// ActionReceipt is bounded evidence about an operation, never permission to
// repeat it. Private actions are visible only to their verified actor and chat.
type ActionReceipt struct {
	Tool, Status, OriginMessageID string
	OccurredAt                    time.Time
	Result                        map[string]any     `json:",omitempty"`
	Source                        domain.SourceRef   `json:"-"`
	Sources                       []domain.SourceRef `json:"-"`
}

func (s *Store) RecentActionReceipts(ctx context.Context, scope domain.Scope, actor string, anchor time.Time, limit int) ([]ActionReceipt, error) {
	if (domain.SourceRef{Scope: scope, AuthorID: actor, MessageID: "action", Version: 1, OccurredAt: anchor}).Validate() != nil || limit < 1 || limit > 8 {
		return nil, domain.ErrInvalid
	}
	since, err := s.CatalogHistorySince(ctx, scope)
	if err != nil {
		return nil, err
	}
	start := anchor.Add(-15 * time.Minute)
	if since.After(start) {
		start = since
	}
	clock := rfc3339NanoEpochSQL(`(convert_from(x.body,'UTF8')::jsonb->>'UpdatedAt')`)
	rows, err := s.db.QueryContext(ctx, `SELECT x.body,t.body,m.body FROM `+s.table("tool_executions")+` x
		JOIN `+s.table("tool_policies")+` p ON p.tool_name=convert_from(x.body,'UTF8')::jsonb->>'Tool' AND p.side_effect
		JOIN `+s.table("tasks")+` t ON t.scope_key=x.scope_key AND t.task_id=x.task_id
		JOIN `+s.table("messages")+` m ON m.scope_key=t.scope_key AND m.message_id=t.root_message_id
		WHERE x.scope_key=$1 AND convert_from(x.body,'UTF8')::jsonb->>'ActorID'=$2
		AND x.status IN ('completed','completed_late','unknown') AND NOT m.deleted
		AND m.occurred_ns >= $3::numeric AND m.occurred_ns <= $4::numeric AND m.updated_ns <= $4::numeric
		AND `+clock+` >= $3::numeric AND `+clock+` <= $4::numeric
		ORDER BY `+clock+` DESC,x.execution_key DESC LIMIT $5`, scope.Key(), actor, exactTimeKey(start), exactTimeKey(anchor), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActionReceipt{}
	for rows.Next() {
		var raw, rootRaw, original []byte
		if err = rows.Scan(&raw, &rootRaw, &original); err != nil {
			return nil, err
		}
		var x domain.ToolExecution
		var task domain.Task
		var message domain.Message
		if err = json.Unmarshal(raw, &x); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(original, &message); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(rootRaw, &task); err != nil {
			return nil, err
		}
		if x.Scope != scope || x.ActorID != actor || message.Source.Scope != scope || message.Source.Validate() != nil {
			return nil, domain.ErrInvalid
		}
		if task.Root != message.Source || task.Revision != x.Revision {
			continue
		}
		r := ActionReceipt{Tool: x.Tool, Status: x.Status, OriginMessageID: message.Source.MessageID, OccurredAt: x.UpdatedAt, Source: message.Source}
		seen := map[domain.SourceRef]bool{}
		for _, set := range [][]domain.SourceRef{{task.Root}, task.Sources, task.ContextSources} {
			for _, ref := range set {
				if !seen[ref] {
					r.Sources = append(r.Sources, ref)
					seen[ref] = true
				}
			}
		}
		// An uncertain operation may contain a partial response. It cannot
		// supply a confirmed message ID or a success outcome to the next turn.
		if x.Status == "completed" || x.Status == "completed_late" {
			r.Result = actionReceiptResult(x.Result)
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	validActions := out[:0]
	for _, action := range out {
		if len(action.Sources) > 256 {
			continue
		}
		valid, err := s.ValidSources(ctx, domain.Query{Scope: scope, Anchor: anchor, Mode: domain.QueryLiteral, Limit: 1}, action.Sources)
		if err != nil {
			return nil, err
		}
		if valid {
			validActions = append(validActions, action)
		}
	}
	return validActions, nil
}

func actionReceiptResult(raw string) map[string]any {
	var value map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"message_id", "expires_at", "account_status", "visibility", "delivery_location", "status", "outcome"} {
		var text string
		if json.Unmarshal(value[key], &text) != nil || text == "" || len(text) > 1024 {
			continue
		}
		valid := false
		switch key {
		case "message_id":
			valid = len(text) <= 256
		case "expires_at":
			_, err := time.Parse(time.RFC3339Nano, text)
			valid = err == nil
		case "account_status":
			valid = text == "bound" || text == "unbound" || text == "invalid"
		case "visibility":
			valid = text == "current_actor_only" || text == "group"
		case "delivery_location":
			valid = text == "current_chat"
		default:
			for _, allowed := range []string{"sent", "reused", "already_bound", "rejected", "unknown", "completed", "delivered"} {
				if text == allowed {
					valid = true
					break
				}
			}
		}
		if valid {
			out[key] = text
		}
	}
	for _, key := range []string{"created", "replay_blocked"} {
		var flag bool
		if v, ok := value[key]; ok && json.Unmarshal(v, &flag) == nil {
			out[key] = flag
		}
	}
	return out
}
