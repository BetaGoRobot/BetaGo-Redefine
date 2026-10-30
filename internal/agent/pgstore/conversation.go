package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// ConversationReply is a confirmed ordinary assistant utterance, not a tool
// receipt or an approval. Its source dependencies remain internal evidence.
type ConversationReply struct {
	MessageID, ParentID, Text string
	OccurredAt                time.Time
	Sources                   []domain.SourceRef
}

// RecentRepliesForMember bounds delivery time and filters by the actual human
// reply target before the limit. Contextual sources do not establish a partner.
func (s *Store) RecentRepliesForMember(ctx context.Context, scope domain.Scope, memberID string, since, anchor time.Time, limit int) ([]ConversationReply, error) {
	if (domain.SourceRef{Scope: scope, MessageID: "parent", AuthorID: memberID, Version: 1, OccurredAt: anchor}).Validate() != nil || since.IsZero() || since.After(anchor) || limit < 1 || limit > 64 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM (SELECT DISTINCT ON (d.message_id) d.body,d.delivery_id,delivery_clock.delivered_ns FROM `+s.table("deliveries")+` d
		JOIN `+s.table("messages")+` m ON m.scope_key=d.scope_key AND m.message_id=convert_from(d.body,'UTF8')::jsonb->>'ParentID'
		CROSS JOIN LATERAL (SELECT `+rfc3339NanoEpochSQL(`(convert_from(d.body,'UTF8')::jsonb->>'UpdatedAt')`)+` AS delivered_ns) delivery_clock
		WHERE d.scope_key=$1 AND d.status='delivered' AND d.message_id<>''
		AND convert_from(d.body,'UTF8')::jsonb->>'Kind'='final'
		AND (convert_from(d.body,'UTF8')::jsonb->>'MessageType'='text' OR (convert_from(d.body,'UTF8')::jsonb->>'MessageType'='interactive' AND COALESCE(convert_from(d.body,'UTF8')::jsonb->>'ConversationText','')<>''))
		AND delivery_clock.delivered_ns<=$2::numeric AND delivery_clock.delivered_ns>=$5::numeric
		AND NOT m.deleted AND m.occurred_ns<=$2::numeric AND m.updated_ns<=$2::numeric
		AND convert_from(m.body,'UTF8')::jsonb->'Source'->>'AuthorID'=$3
		AND (CASE WHEN length(m.raw_content)>0 THEN convert_from(m.raw_content,'UTF8')::jsonb ELSE '{}'::jsonb END)->>'sender_type'='user'
		AND (CASE WHEN length(m.raw_content)>0 THEN convert_from(m.raw_content,'UTF8')::jsonb ELSE '{}'::jsonb END)->>'kind' IN ('message','refresh')
		ORDER BY d.message_id,COALESCE((convert_from(d.body,'UTF8')::jsonb->>'Sequence')::int,0) DESC,delivery_clock.delivered_ns DESC,d.delivery_id DESC) confirmed ORDER BY delivered_ns DESC,delivery_id DESC LIMIT $4`, scope.Key(), exactTimeKey(anchor), memberID, limit, exactTimeKey(since))
	if err != nil {
		return nil, err
	}
	replies, err := s.confirmedConversationReplies(ctx, scope, anchor, rows)
	if err != nil {
		return nil, err
	}
	// Recheck the decoded lower bound; shared validation checks upper bound
	// and sources independently of the SQL candidate selection.
	out := replies[:0]
	for _, reply := range replies {
		if !reply.OccurredAt.Before(since) {
			out = append(out, reply)
		}
	}
	return out, nil
}

// Split an internal RFC3339Nano SQL expression before timestamptz can round its
// fraction to microseconds. The whole-second cast retains the timezone offset;
// adding the decimal fraction as numeric preserves all nine nanosecond digits.
func rfc3339NanoEpochSQL(value string) string {
	return `(EXTRACT(EPOCH FROM regexp_replace(` + value + `,'[.][0-9]+','')::timestamptz)::numeric * 1000000000
		+ COALESCE(rpad(substring(` + value + ` from '[.]([0-9]+)'),9,'0')::numeric,0))`
}

func (s *Store) RecentReplies(ctx context.Context, scope domain.Scope, anchor time.Time, limit int) ([]ConversationReply, error) {
	if scope.Validate() != nil || anchor.IsZero() || limit < 1 || limit > 64 {
		return nil, domain.ErrInvalid
	}

	rows, err := s.db.QueryContext(ctx, `SELECT body FROM (
 SELECT DISTINCT ON (message_id) body,delivery_id,delivery_clock.delivered_ns FROM `+s.table("deliveries")+`
 CROSS JOIN LATERAL (SELECT `+rfc3339NanoEpochSQL(`(convert_from(body,'UTF8')::jsonb->>'UpdatedAt')`)+` AS delivered_ns) delivery_clock
 WHERE scope_key=$1 AND status='delivered' AND message_id<>'' AND convert_from(body,'UTF8')::jsonb->>'Kind'='final'
 AND (convert_from(body,'UTF8')::jsonb->>'MessageType'='text' OR (convert_from(body,'UTF8')::jsonb->>'MessageType'='interactive' AND COALESCE(convert_from(body,'UTF8')::jsonb->>'ConversationText','')<>''))
 AND delivery_clock.delivered_ns<=$2::numeric
 ORDER BY message_id,COALESCE((convert_from(body,'UTF8')::jsonb->>'Sequence')::int,0) DESC,delivery_clock.delivered_ns DESC,delivery_id DESC) confirmed
 ORDER BY delivered_ns DESC,delivery_id DESC LIMIT $3`, scope.Key(), exactTimeKey(anchor), limit)

	if err != nil {
		return nil, err
	}
	return s.confirmedConversationReplies(ctx, scope, anchor, rows)
}

// Both context and member-directed lookups share delivery/source validation.
func (s *Store) confirmedConversationReplies(ctx context.Context, scope domain.Scope, anchor time.Time, rows *sql.Rows) ([]ConversationReply, error) {
	var deliveries []domain.Delivery
	var err error
	for rows.Next() {
		var b []byte
		var d domain.Delivery
		if err = rows.Scan(&b); err == nil {
			err = json.Unmarshal(b, &d)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []ConversationReply
	for _, d := range deliveries {

		text, ordinary := domain.ConversationDeliveryText(d)
		if d.Scope != scope || d.Status != "delivered" || !ordinary || d.MessageID == "" || d.UpdatedAt.After(anchor) {
			continue
		}

		var refs []domain.SourceRef
		er := s.transact(ctx, func(tx *sql.Tx) error { var e error; refs, e = s.conversationDeliverySourcesTx(ctx, tx, d); return e })
		if errors.Is(er, domain.ErrNotFound) || errors.Is(er, domain.ErrStaleSource) {
			continue
		}
		if er != nil {
			return nil, er
		}

		// A previous answer cannot launder edited/deleted evidence back into a
		// new turn. The engine pins these dependencies again before generation.
		valid, er := s.ValidSources(ctx, domain.Query{Scope: scope, Anchor: anchor, Mode: domain.QueryLiteral, Limit: 1}, refs)
		if er != nil {
			return nil, er
		}
		if !valid {
			continue
		}
		out = append(out, ConversationReply{d.MessageID, d.ParentID, text, d.UpdatedAt, refs})
	}
	return out, nil
}

// Each new delivery freezes its evidence independently from mutable task state.
// This keeps an actually delivered utterance visible after an owner follow-up
// revises a partial batch, without reviving edited or deleted evidence.
func conversationSourceSnapshot(t domain.Task) []domain.SourceRef {
	var refs []domain.SourceRef
	seen := map[domain.SourceRef]bool{}
	for _, ref := range append(append([]domain.SourceRef{t.Root}, t.Sources...), t.ContextSources...) {
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	return refs
}
func (s *Store) conversationDeliverySourcesTx(ctx context.Context, tx *sql.Tx, d domain.Delivery) ([]domain.SourceRef, error) {
	refs := append([]domain.SourceRef(nil), d.Sources...)
	if len(refs) == 0 {
		// Old pending/delivered records have no immutable evidence snapshot. Only
		// their unchanged task revision is safe to use as source proof.
		t, err := s.taskTx(ctx, tx, d.Scope, d.TaskID, false)
		if err != nil {
			return nil, err
		}
		if t.Revision != d.Revision {
			return nil, domain.ErrStaleSource
		}
		refs = conversationSourceSnapshot(t)
	}
	for _, ref := range refs {
		if ref.Scope != d.Scope || ref.Validate() != nil {
			return nil, domain.ErrStaleSource
		}
	}
	return refs, nil
}
