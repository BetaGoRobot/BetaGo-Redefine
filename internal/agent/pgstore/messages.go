package pgstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func contentDigest(body, raw []byte) string {
	h := sha256.New()
	h.Write(body)
	h.Write([]byte{0})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// Accept commits ingress facts and durable continuations together. It makes no
// remote calls. Source mutations are serialized even before a current row exists.
func (s *Store) Accept(ctx context.Context, input domain.Event) (AcceptResult, error) {
	return s.accept(ctx, input, false)
}

// AcceptHistorical records verified human backfill facts without participation
// or automatic media/context/memory work. Callers must construct backfill
// metadata only after canonical provider verification.
func (s *Store) AcceptHistorical(ctx context.Context, input domain.Event) (AcceptResult, error) {
	var meta domain.IngressMetadata
	if input.Type != domain.EventMessage || input.Message.Deleted || json.Unmarshal(input.Message.Raw, &meta) != nil || meta.Kind != "backfill" || meta.SenderType != "user" || meta.Control != nil || meta.Addressed {
		return AcceptResult{}, fmt.Errorf("%w: historical human backfill metadata", domain.ErrInvalid)
	}
	return s.accept(ctx, input, true)
}

// historicalSameFacts excludes transport/event representations, retaining all
// canonical source, content, media, links and mutation facts.
func historicalSameFacts(a, b domain.Message) bool {
	// nil and empty media both express no canonical media resources.
	if len(a.Media) == 0 {
		a.Media = nil
	}
	if len(b.Media) == 0 {
		b.Media = nil
	}
	same := a.Source.Scope == b.Source.Scope && a.Source.MessageID == b.Source.MessageID && a.Source.Version == b.Source.Version && a.Source.AuthorID == b.Source.AuthorID && a.Source.OccurredAt.Equal(b.Source.OccurredAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.ParentID == b.ParentID && a.RootID == b.RootID && a.ThreadID == b.ThreadID && a.Text == b.Text && a.Deleted == b.Deleted && reflect.DeepEqual(a.Media, b.Media)
	if !same || a.Text != "" || len(a.Media) > 0 {
		return same
	}
	// Opaque originals have no normalized text/media. Their actual content is
	// still a canonical source fact; transport/audit envelopes are not.
	aKind, aContent, aOK := historicalOpaqueFacts(a.Raw)
	bKind, bContent, bOK := historicalOpaqueFacts(b.Raw)
	return aOK && bOK && aKind == bKind && aContent == bContent
}

func historicalOpaqueFacts(raw json.RawMessage) (string, string, bool) {
	var meta domain.IngressMetadata
	if json.Unmarshal(raw, &meta) != nil {
		return "", "", false
	}
	provider := meta.Provider
	var archive struct {
		Source string `json:"source"`
		Row    struct {
			RawBody string `json:"raw_body"`
		} `json:"row"`
	}
	if json.Unmarshal(provider, &archive) != nil {
		return "", "", false
	}
	if archive.Source == "opensearch_archive" {
		provider = json.RawMessage(archive.Row.RawBody)
	}
	var envelope struct {
		Event   json.RawMessage `json:"event"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(provider, &envelope) != nil {
		return "", "", false
	}
	if len(envelope.Event) > 0 {
		provider = envelope.Event
		if json.Unmarshal(provider, &envelope) != nil {
			return "", "", false
		}
	}
	if len(envelope.Message) > 0 {
		provider = envelope.Message
	}
	var original struct {
		MessageType string  `json:"message_type"`
		MsgType     string  `json:"msg_type"`
		Content     *string `json:"content"`
		Body        struct {
			Content *string `json:"content"`
		} `json:"body"`
	}
	if json.Unmarshal(provider, &original) != nil {
		return "", "", false
	}
	kind := original.MessageType
	if kind == "" {
		kind = original.MsgType
	}
	if meta.MessageType != "" {
		if kind != "" && kind != meta.MessageType {
			return "", "", false
		}
		kind = meta.MessageType
	}
	content := original.Content
	if content == nil {
		content = original.Body.Content
	}
	if kind == "" || content == nil {
		return "", "", false
	}
	canonical := *content
	if json.Valid([]byte(canonical)) {
		decoder := json.NewDecoder(strings.NewReader(canonical))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return "", "", false
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", "", false
		}
		canonical = string(encoded)
	}
	return kind, canonical, true
}

func (s *Store) accept(ctx context.Context, input domain.Event, historical bool) (AcceptResult, error) {
	e := input.Clone()
	if err := e.Validate(); err != nil {
		return AcceptResult{}, err
	}
	eventBody, err := encode(e)
	if err != nil {
		return AcceptResult{}, err
	}
	eventDigest := contentDigest(eventBody, e.Message.Raw)
	var result AcceptResult
	err = s.transact(ctx, func(tx *sql.Tx) error {
		key := e.Message.Source.Scope.Key()
		id := e.Message.Source.MessageID
		if err := lockSource(ctx, tx, e.Message.Source.Scope, id); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("events")+`(scope_key,event_id,payload_digest,body,raw_content,occurred_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, key, e.ID, eventDigest, eventBody, nonNil(e.Message.Raw), e.OccurredAt)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			var existing string
			if err = tx.QueryRowContext(ctx, `SELECT payload_digest FROM `+s.table("events")+` WHERE scope_key=$1 AND event_id=$2`, key, e.ID).Scan(&existing); err != nil {
				return err
			}
			if existing != eventDigest {
				return fmt.Errorf("%w: event identity reused", domain.ErrConflict)
			}
			result.Duplicate = true
			return nil
		}
		current, currentErr := s.messageTx(ctx, tx, e.Message.Source.Scope, id)
		if currentErr != nil && !errors.Is(currentErr, domain.ErrNotFound) {
			return currentErr
		}
		if historical {
			var oldBody, oldRaw []byte
			versionErr := tx.QueryRowContext(ctx, `SELECT body,raw_content FROM `+s.table("message_versions")+` WHERE scope_key=$1 AND message_id=$2 AND version=$3`, key, id, e.Message.Source.Version).Scan(&oldBody, &oldRaw)
			if versionErr == nil {
				old, err := decodeMessage(oldBody, oldRaw)
				if err != nil {
					return err
				}
				if !historicalSameFacts(old, e.Message) {
					return fmt.Errorf("%w: source version reused", domain.ErrConflict)
				}
				result.Duplicate = true
				return nil
			}
			if !errors.Is(versionErr, sql.ErrNoRows) {
				return versionErr
			}
		}
		m := e.Message.Clone()
		if !m.Deleted && currentErr == nil && !current.Deleted && (m.Source.AuthorID != current.Source.AuthorID || !m.Source.OccurredAt.Equal(current.Source.OccurredAt)) {
			return fmt.Errorf("%w: immutable original source identity", domain.ErrConflict)
		}
		// A deletion actor is never substituted for the original author. Preserve
		// known original provenance; an unseen deletion keeps it unresolved.
		if m.Deleted && currentErr == nil {
			m.Source.AuthorID = current.Source.AuthorID
			m.Source.OccurredAt = current.Source.OccurredAt
			m.ParentID = current.ParentID
			m.RootID = current.RootID
			m.ThreadID = current.ThreadID
		}
		body, err := encode(m)
		if err != nil {
			return err
		}
		incomingBody, err := encode(e.Message)
		if err != nil {
			return err
		}
		versionDigest := contentDigest(incomingBody, e.Message.Raw)
		r, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("message_versions")+`(scope_key,message_id,version,event_id,payload_digest,body,raw_content) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, key, id, m.Source.Version, e.ID, versionDigest, body, nonNil(m.Raw))
		if err != nil {
			return err
		}
		n, err = r.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			var existing string
			if err = tx.QueryRowContext(ctx, `SELECT payload_digest FROM `+s.table("message_versions")+` WHERE scope_key=$1 AND message_id=$2 AND version=$3`, key, id, m.Source.Version).Scan(&existing); err != nil {
				return err
			}
			if existing != versionDigest {
				return fmt.Errorf("%w: source version reused", domain.ErrConflict)
			}
		}
		result.Current = currentErr != nil || (m.Source.Version > current.Source.Version && !m.UpdatedAt.Before(current.UpdatedAt) && (!current.Deleted || m.Deleted))
		payload, _ := json.Marshal(map[string]any{"event_id": e.ID, "message_id": id, "version": m.Source.Version})
		add := func(kind domain.JobKind, jobPayload json.RawMessage) error {
			priority := 0
			if historical && kind == domain.JobIndexMessage {
				priority = 300
			}
			jid, err := s.enqueueTx(ctx, tx, domain.JobSpec{Kind: kind, Scope: m.Source.Scope, Source: m.Source, Payload: jobPayload, Priority: priority, DedupeKey: "event:" + contentDigest([]byte(e.ID), nil)})
			if err == nil {
				result.JobIDs = append(result.JobIDs, jid)
			}
			return err
		}
		// Routing remains durable for all distinct events; its consumer validates
		// current source/version before acting on a late event.
		if !historical {
			if err = add(domain.JobRoute, payload); err != nil {
				return err
			}
		}
		if !result.Current {
			return nil
		}
		var occurred, occurredNS any
		if !m.Source.OccurredAt.IsZero() {
			occurred = m.Source.OccurredAt
			occurredNS = exactTimeKey(m.Source.OccurredAt)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("messages")+`(scope_key,message_id,version,deleted,occurred_at,updated_at,body,raw_content,occurred_ns,updated_ns) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(scope_key,message_id) DO UPDATE SET version=excluded.version,deleted=excluded.deleted,occurred_at=excluded.occurred_at,updated_at=excluded.updated_at,body=excluded.body,raw_content=excluded.raw_content,occurred_ns=excluded.occurred_ns,updated_ns=excluded.updated_ns`, key, id, m.Source.Version, m.Deleted, occurred, m.UpdatedAt, body, nonNil(m.Raw), occurredNS, exactTimeKey(m.UpdatedAt))
		if err != nil {
			return err
		}
		if err = add(domain.JobIndexMessage, payload); err != nil {
			return err
		}
		if currentErr == nil || m.Deleted {
			ids, err := s.invalidateContextsTx(ctx, tx, m.Source.Scope, id)
			if err != nil {
				return err
			}
			cleanup, _ := json.Marshal(map[string]any{"message_id": id, "version": m.Source.Version, "context_ids": ids})
			if err = add(domain.JobCleanup, cleanup); err != nil {
				return err
			}
		}
		if !m.Deleted && !historical {
			if len(m.Media) > 0 {
				if err = add(domain.JobMedia, payload); err != nil {
					return err
				}
			}
			if err = add(domain.JobContexts, payload); err != nil {
				return err
			}
			if err = add(domain.JobMemory, payload); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AcceptResult{}, err
	}
	return result, nil
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// PostgreSQL timestamptz rounds to microseconds. A numeric epoch key preserves
// exact provider ordering and indexed anchor filters, including dates outside
// UnixNano's 1678–2262 range. The JSON body preserves the original timestamp.
func exactTimeKey(t time.Time) string {
	n := new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(1_000_000_000))
	return n.Add(n, big.NewInt(int64(t.Nanosecond()))).String()
}

func (s *Store) messageTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string) (domain.Message, error) {
	var body, raw []byte
	err := tx.QueryRowContext(ctx, `SELECT body,raw_content FROM `+s.table("messages")+` WHERE scope_key=$1 AND message_id=$2`, scope.Key(), id).Scan(&body, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Message{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Message{}, err
	}
	return decodeMessage(body, raw)
}

func (s *Store) Message(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	if err := scope.Validate(); err != nil {
		return domain.Message{}, err
	}
	if id == "" {
		return domain.Message{}, fmt.Errorf("%w: message id", domain.ErrInvalid)
	}
	var body, raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT body,raw_content FROM `+s.table("messages")+` WHERE scope_key=$1 AND message_id=$2`, scope.Key(), id).Scan(&body, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Message{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Message{}, err
	}
	return decodeMessage(body, raw)
}

func (s *Store) Versions(ctx context.Context, scope domain.Scope, id string) ([]domain.Message, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body,raw_content FROM `+s.table("message_versions")+` WHERE scope_key=$1 AND message_id=$2 ORDER BY version`, scope.Key(), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Message
	for rows.Next() {
		var body, raw []byte
		if err = rows.Scan(&body, &raw); err != nil {
			return nil, err
		}
		m, err := decodeMessage(body, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Recent(ctx context.Context, scope domain.Scope, anchor time.Time, limit int) ([]domain.Message, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if anchor.IsZero() || limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("%w: recent anchor or limit", domain.ErrInvalid)
	}
	since, err := s.CatalogHistorySince(ctx, scope)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body,raw_content FROM `+s.table("messages")+` WHERE scope_key=$1 AND NOT deleted AND occurred_ns<=$2::numeric AND updated_ns<=$2::numeric AND occurred_ns>=$3::numeric ORDER BY occurred_ns DESC,message_id LIMIT $4`, scope.Key(), exactTimeKey(anchor), exactTimeKey(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Message
	for rows.Next() {
		var body, raw []byte
		if err = rows.Scan(&body, &raw); err != nil {
			return nil, err
		}
		m, err := decodeMessage(body, raw)
		if err != nil {
			return nil, err
		}
		if m.Source.OccurredAt.After(anchor) || m.UpdatedAt.After(anchor) {
			continue
		}
		out = append(out, m)
		if len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}

func sourceMatches(q domain.Query, ref domain.SourceRef, m domain.Message) bool {
	return ref.Scope == q.Scope && m.Source.Scope == q.Scope && !m.Deleted && ref.MessageID == m.Source.MessageID && ref.Version == m.Source.Version && ref.AuthorID == m.Source.AuthorID && ref.OccurredAt.Equal(m.Source.OccurredAt) && !m.Source.OccurredAt.After(q.Anchor) && !m.UpdatedAt.After(q.Anchor) && (q.AuthorID == "" || q.AuthorID == m.Source.AuthorID)
}

func (s *Store) ValidSources(ctx context.Context, q domain.Query, refs []domain.SourceRef) (bool, error) {
	if err := q.Validate(); err != nil {
		return false, err
	}
	if len(refs) == 0 {
		return false, nil
	}
	// One repeatable-read snapshot validates the complete evidence dependency set.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	since, err := s.catalogHistorySinceTx(ctx, tx, q.Scope)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		if ref.Validate() != nil || ref.Scope != q.Scope {
			return false, nil
		}
		if ref.OccurredAt.Before(since) {
			return false, nil
		}
		m, err := s.messageTx(ctx, tx, q.Scope, ref.MessageID)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !sourceMatches(q, ref, m) {
			return false, nil
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
