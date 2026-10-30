package pgstore

import (
	"context"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// History reads current locally captured originals. Provider history is not
// implied; edits/deletions are represented by the authoritative current row.
func (s *Store) History(ctx context.Context, q domain.HistoryQuery) (domain.HistoryPage, error) {
	out := domain.HistoryPage{Messages: []domain.Message{}}
	if q.Scope.Validate() != nil || q.Anchor.IsZero() || q.Limit < 1 || q.Limit > 100 || (!q.After.IsZero() && !q.Before.IsZero() && !q.After.Before(q.Before)) {
		return out, domain.ErrInvalid
	}
	if q.Cursor != nil && (q.Cursor.Time.IsZero() || q.Cursor.MessageID == "" || len(q.Cursor.MessageID) > 1024) {
		return out, domain.ErrInvalid
	}
	since, err := s.CatalogHistorySince(ctx, q.Scope)
	if err != nil {
		return out, err
	}
	// History's after bound is exclusive; a visibility cutoff is inclusive.
	if !since.IsZero() && q.After.Before(since) {
		q.After = since.Add(-1)
	}
	args := []any{q.Scope.Key(), exactTimeKey(q.Anchor)}
	where := `scope_key=$1 AND NOT deleted AND occurred_ns<=$2::numeric AND updated_ns<=$2::numeric`
	bind := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if !q.After.IsZero() {
		where += ` AND occurred_ns>` + bind(exactTimeKey(q.After)) + `::numeric`
	}
	if !q.Before.IsZero() {
		where += ` AND occurred_ns<` + bind(exactTimeKey(q.Before)) + `::numeric`
	}
	if q.AuthorID != "" {
		where += ` AND convert_from(body,'UTF8')::jsonb->'Source'->>'AuthorID'=` + bind(q.AuthorID)
	}
	if q.Cursor != nil {
		at := bind(exactTimeKey(q.Cursor.Time))
		id := bind(q.Cursor.MessageID)
		where += ` AND (occurred_ns<` + at + `::numeric OR (occurred_ns=` + at + `::numeric AND message_id>` + id + `))`
	}
	limit := bind(q.Limit + 1)
	rows, err := s.db.QueryContext(ctx, `SELECT body,raw_content FROM `+s.table("messages")+` WHERE `+where+` ORDER BY occurred_ns DESC,message_id ASC LIMIT `+limit, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var body, raw []byte
		if err = rows.Scan(&body, &raw); err != nil {
			return out, err
		}
		m, e := decodeMessage(body, raw)
		if e != nil {
			return out, e
		}
		if m.Source.Scope != q.Scope || m.Deleted || m.Source.Validate() != nil {
			return out, domain.ErrStaleSource
		}
		out.Messages = append(out.Messages, m)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Messages) > q.Limit {
		out.HasMore = true
		out.Messages = out.Messages[:q.Limit]
		last := out.Messages[len(out.Messages)-1]
		out.Next = &domain.HistoryCursor{Time: last.Source.OccurredAt, MessageID: last.Source.MessageID}
	}
	return out, nil
}
