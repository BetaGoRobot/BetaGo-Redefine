package pgstore

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// MessageStatistics counts the complete local window in SQL. The separately
// bounded text sample is never described as a complete corpus when truncated.
func (s *Store) MessageStatistics(ctx context.Context, q domain.AnalyticsQuery) (out domain.MessageStatistics, err error) {
	if q.Scope.Validate() != nil || q.Start.IsZero() || !q.Start.Before(q.End) || q.End.Sub(q.Start) > 31*24*time.Hour || q.Anchor.IsZero() || q.End.After(q.Anchor) || q.Limit < 1 || q.Limit > 50 {
		return out, domain.ErrInvalid
	}
	out = domain.MessageStatistics{Start: q.Start, End: q.End, AsOf: q.Anchor, Coverage: "locally_captured_current_messages", Authors: []domain.CountEntry{}, Words: []domain.CountEntry{}, Sources: []domain.SourceRef{}, Tokenization: "unicode_letters_numbers_lowercase_han_bigrams"}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	since, err := s.catalogHistorySinceTx(ctx, tx, q.Scope)
	if err != nil {
		return out, err
	}
	if q.Start.Before(since) {
		q.Start = since
		if q.Start.After(q.End) {
			q.Start = q.End
		}
		out.Start = q.Start
	}
	where := `scope_key=$1 AND NOT deleted AND occurred_ns>=$2::numeric AND occurred_ns<$3::numeric AND updated_ns<=$4::numeric AND ($5='' OR convert_from(body,'UTF8')::jsonb->'Source'->>'AuthorID'=$5)`
	args := []any{q.Scope.Key(), exactTimeKey(q.Start), exactTimeKey(q.End), exactTimeKey(q.Anchor), q.AuthorID}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+s.table("messages")+` WHERE `+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT convert_from(body,'UTF8')::jsonb->'Source'->>'AuthorID' AS author,count(*) AS n FROM `+s.table("messages")+` WHERE `+where+` GROUP BY author ORDER BY n DESC,author LIMIT $6`, append(args, q.Limit+1)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c domain.CountEntry
		if err = rows.Scan(&c.Value, &c.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Authors = append(out.Authors, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Authors) > q.Limit {
		out.AuthorsTruncated = true
		out.Authors = out.Authors[:q.Limit]
	}
	rows, err = tx.QueryContext(ctx, `SELECT body,raw_content FROM `+s.table("messages")+` WHERE `+where+` ORDER BY occurred_ns DESC,message_id LIMIT 128`, args...)
	if err != nil {
		return out, err
	}
	counts := map[string]int64{}
	budget := 256 << 10
	textComplete := true
	for rows.Next() {
		var b, raw []byte
		if err = rows.Scan(&b, &raw); err != nil {
			rows.Close()
			return out, err
		}
		m, e := decodeMessage(b, raw)
		if e != nil {
			rows.Close()
			return out, e
		}
		if m.Source.Scope != q.Scope || m.Deleted {
			rows.Close()
			return out, domain.ErrStaleSource
		}
		if len(m.Text) > budget {
			textComplete = false
			break
		}
		budget -= len(m.Text)
		out.WordMessages++
		out.Sources = append(out.Sources, m.Source)
		for _, token := range analyticsTokens(m.Text) {
			counts[token]++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.WordsComplete = textComplete && int64(out.WordMessages) == out.Total
	for word, n := range counts {
		out.Words = append(out.Words, domain.CountEntry{Value: word, Count: n})
	}
	sort.Slice(out.Words, func(i, j int) bool {
		if out.Words[i].Count != out.Words[j].Count {
			return out.Words[i].Count > out.Words[j].Count
		}
		return out.Words[i].Value < out.Words[j].Value
	})
	if len(out.Words) > q.Limit {
		out.Words = out.Words[:q.Limit]
	}
	err = tx.Commit()
	return
}

func analyticsTokens(text string) []string {
	var out []string
	var latin, han []rune
	flush := func() {
		if len(latin) > 0 {
			out = append(out, strings.ToLower(string(latin)))
			latin = nil
		}
		if len(han) == 1 {
			out = append(out, string(han))
		}
		for i := 1; i < len(han); i++ {
			out = append(out, string(han[i-1:i+1]))
		}
		han = nil
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			if len(latin) > 0 {
				flush()
			}
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if len(han) > 0 {
				flush()
			}
			latin = append(latin, r)
		default:
			flush()
		}
	}
	flush()
	return out
}
