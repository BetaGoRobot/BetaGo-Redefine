package pgstore

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// Activity is descriptive context, never a rate limit, task permission or
// instruction. Card edits share one message ID and count as one notification.
type ConversationActivity struct {
	AsOf, WindowStart               time.Time
	OwnNotifications, OwnReplyTurns int
	// When false, these counts are lower bounds: legacy receipts have no
	// durable first-confirmation time and may belong to the recent window.
	NotificationsComplete bool
	// OwnReactions counts completed reaction-add actions, separate from
	// physical message notifications and ordinary reply turns.
	OwnReactions                 int
	HumanMessages, HumanSpeakers int
	InFlight                     []ConversationInFlight
	InFlightTruncated            bool
}

type ConversationInFlight struct {
	Root    domain.SourceRef
	Request string
	Phase   string
	Sources []domain.SourceRef
}

// ConversationActivity separates actual delivery burden from the current
// request and shows live, same-group work without exposing execution authority.
func (s *Store) ConversationActivity(ctx context.Context, current domain.Task, anchor time.Time) (ConversationActivity, error) {
	activity := ConversationActivity{AsOf: anchor, WindowStart: anchor.Add(-2 * time.Minute), InFlight: []ConversationInFlight{}}
	if current.Scope.Validate() != nil || current.Root.Validate() != nil || current.Root.Scope != current.Scope || strings.TrimSpace(current.ID) == "" || anchor.IsZero() || current.Root.OccurredAt.After(anchor) {
		return activity, domain.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return activity, err
	}
	confirmedClock := rfc3339NanoEpochSQL(`NULLIF(convert_from(body,'UTF8')::jsonb->>'ConfirmedAt','0001-01-01T00:00:00Z')`)
	updatedClock := rfc3339NanoEpochSQL(`(convert_from(body,'UTF8')::jsonb->>'UpdatedAt')`)
	// Group every confirmed frame before applying the time window. A PATCH or
	// late lifecycle reconciliation does not create another physical message.
	// Legacy UpdatedAt is only an upper bound on an unknown receipt time; it
	// can prove a notification is old, but cannot prove a recent notification.
	err := s.db.QueryRowContext(ctx, `WITH receipt_rows AS (
		SELECT message_id,task_id,revision,convert_from(body,'UTF8')::jsonb->>'Kind' AS kind,
		`+confirmedClock+` AS confirmed_ns,`+updatedClock+` AS updated_ns
		FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND status IN ('delivered','delivered_late') AND message_id<>''
	), physical_messages AS (
		SELECT message_id,min(confirmed_ns) AS first_ns,
		min(updated_ns) FILTER (WHERE confirmed_ns IS NULL) AS unknown_updated_ns
		FROM receipt_rows GROUP BY message_id
	), reply_turns AS (
		SELECT task_id,revision,min(first_ns) AS first_ns,min(unknown_updated_ns) AS unknown_updated_ns
		FROM receipt_rows JOIN physical_messages USING(message_id) WHERE kind='final' GROUP BY task_id,revision
	) SELECT
		(SELECT count(*) FROM physical_messages WHERE unknown_updated_ns IS NULL AND first_ns >= $2::numeric AND first_ns <= $3::numeric),
		(SELECT count(*) FROM reply_turns WHERE unknown_updated_ns IS NULL AND first_ns >= $2::numeric AND first_ns <= $3::numeric),
		NOT EXISTS(SELECT 1 FROM physical_messages WHERE unknown_updated_ns >= $2::numeric AND (first_ns IS NULL OR first_ns >= $2::numeric))`, current.Scope.Key(), exactTimeKey(activity.WindowStart), exactTimeKey(anchor)).Scan(&activity.OwnNotifications, &activity.OwnReplyTurns, &activity.NotificationsComplete)
	if err != nil {
		return activity, err
	}
	reactionClock := rfc3339NanoEpochSQL(`(convert_from(body,'UTF8')::jsonb->>'UpdatedAt')`)
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+s.table("tool_executions")+`
		WHERE scope_key=$1 AND status IN ('completed','completed_late') AND convert_from(body,'UTF8')::jsonb->>'Tool'='feishu_add_reaction'
		AND `+reactionClock+` >= $2::numeric AND `+reactionClock+` <= $3::numeric`, current.Scope.Key(), exactTimeKey(activity.WindowStart), exactTimeKey(anchor)).Scan(&activity.OwnReactions)
	if err != nil {
		return activity, err
	}

	err = s.db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT convert_from(body,'UTF8')::jsonb->'Source'->>'AuthorID')
		FROM `+s.table("messages")+` WHERE scope_key=$1 AND NOT deleted AND occurred_ns >= $2::numeric AND occurred_ns <= $3::numeric AND updated_ns <= $3::numeric
		AND (CASE WHEN length(raw_content)>0 THEN convert_from(raw_content,'UTF8')::jsonb ELSE '{}'::jsonb END)->>'sender_type'='user'
		AND (CASE WHEN length(raw_content)>0 THEN convert_from(raw_content,'UTF8')::jsonb ELSE '{}'::jsonb END)->>'kind' IN ('message','refresh')`, current.Scope.Key(), exactTimeKey(activity.WindowStart), exactTimeKey(anchor)).Scan(&activity.HumanMessages, &activity.HumanSpeakers)
	if err != nil {
		return activity, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body,phase FROM (SELECT t.body,t.task_id,`+s.conversationPhaseSQL()+` AS phase FROM `+s.table("tasks")+` t WHERE t.scope_key=$1 AND t.task_id<>$2 AND t.status IN ('ready','ready_delivery','waiting') AND t.expires_at>$3) candidates WHERE phase<>'' ORDER BY (convert_from(body,'UTF8')::jsonb->>'UpdatedAt')::timestamptz DESC,task_id LIMIT 9`, current.Scope.Key(), current.ID, anchor)
	if err != nil {
		return activity, err
	}
	type candidate struct {
		task  domain.Task
		phase string
	}
	var candidates []candidate
	for rows.Next() {
		var raw []byte
		var task domain.Task
		var phase string
		if err = rows.Scan(&raw, &phase); err == nil {
			err = json.Unmarshal(raw, &task)
		}
		if err != nil {
			rows.Close()
			return activity, err
		}
		candidates = append(candidates, candidate{task, phase})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return activity, err
	}
	activity.InFlightTruncated = len(candidates) > 8
	for _, candidate := range candidates {
		task := candidate.task
		if len(activity.InFlight) == 8 {
			break
		}
		if task.Scope != current.Scope || task.Root.Scope != current.Scope || task.Root.Validate() != nil || task.CreatedAt.After(anchor) || task.UpdatedAt.After(anchor) || !task.ExpiresAt.After(anchor) {
			continue
		}
		refs := append([]domain.SourceRef{task.Root}, task.Sources...)
		valid, checkErr := s.ValidSources(ctx, domain.Query{Scope: current.Scope, Anchor: anchor, Mode: domain.QueryLiteral, Text: "in-flight request", Limit: 1}, refs)
		if checkErr != nil {
			return activity, checkErr
		}
		if !valid {
			continue
		}

		request := task.Request
		if len(request) > 2048 {
			request = request[:2048]
			for !utf8.ValidString(request) {
				request = request[:len(request)-1]
			}
			request += "…"
		}
		activity.InFlight = append(activity.InFlight, ConversationInFlight{Root: task.Root, Request: request, Phase: candidate.phase, Sources: refs})
	}
	return activity, nil
}

// Compute progress from durable, live or recoverable work before the bounded
// candidate limit. Terminal job rows must not hide another real request.
// An active final attempt remains live until its lease expires; only further
// claims need attempt < max_attempts and an available time before job expiry.
func (s *Store) conversationPhaseSQL() string {
	return `CASE WHEN t.status='waiting' THEN CASE WHEN EXISTS(
		SELECT 1 FROM ` + s.table("approvals") + ` a WHERE a.scope_key=t.scope_key AND a.task_id=t.task_id AND a.revision=t.revision AND convert_from(a.body,'UTF8')::jsonb->>'Status'='pending'
	) THEN 'waiting_for_approval' ELSE '' END ELSE (
		SELECT CASE
		WHEN t.status='ready_delivery' AND bool_or(j.kind='delivery') THEN 'delivering'
		WHEN t.status='ready' AND bool_or(j.kind='agent_turn' AND j.status='running' AND j.lease_until>$3) THEN 'preparing'
		WHEN t.status='ready' AND bool_or(j.kind='agent_turn') THEN 'queued'
		ELSE '' END FROM ` + s.table("jobs") + ` j
		WHERE j.scope_key=t.scope_key AND j.expires_at>$3
		AND ((j.status='running' AND j.lease_until>$3) OR (j.attempt<j.max_attempts AND ((j.status='pending' AND j.available_at<j.expires_at) OR (j.status='running' AND j.lease_until<=$3))))
		AND ((j.kind='agent_turn' AND convert_from(j.payload,'UTF8')::jsonb->>'task_id'=t.task_id AND (convert_from(j.payload,'UTF8')::jsonb->>'revision')::bigint=t.revision)
		OR (j.kind='delivery' AND convert_from(j.payload,'UTF8')::jsonb->>'delivery_id' IN (SELECT d.delivery_id FROM ` + s.table("deliveries") + ` d WHERE d.scope_key=t.scope_key AND d.task_id=t.task_id AND d.revision=t.revision AND d.status IN ('pending','delivered_late'))))
	) END`
}
