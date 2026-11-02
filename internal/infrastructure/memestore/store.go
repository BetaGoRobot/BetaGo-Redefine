// Package memestore persists immutable meme sources, runs and stage snapshots.
// It owns only its explicitly configured schema and never stores credentials.
package memestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrConflict        = errors.New("immutable meme record conflicts")
	ErrNotFound        = errors.New("meme record not found")
	ErrInvalid         = errors.New("invalid meme storage contract")
	ErrPublicationLock = errors.New("meme publication lock failed")
	schemaPattern      = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	indexPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,254}$`)
)

type Kind string

const (
	Source      Kind = "meme_sources"
	Card        Kind = "meme_card_versions"
	IndexCard   Kind = "meme_projection_records"
	Generation  Kind = "meme_index_generations"
	Publication Kind = "meme_publication_ledger"
	Knowledge   Kind = "meme_knowledge_snapshots"
	Withdrawal  Kind = "meme_knowledge_withdrawals"
)

var factKinds = []Kind{Source, Card, IndexCard, Generation, Publication, Knowledge, Withdrawal}

type Store struct {
	db     *sql.DB
	schema string
}
type Scoped struct {
	store *Store
	scope string
}
type Record struct {
	ID          string          `json:"id"`
	Scope       string          `json:"scope"`
	Fingerprint string          `json:"fingerprint"`
	Body        json.RawMessage `json:"body"`
	CreatedAt   time.Time       `json:"created_at"`
}
type GenerationManifest struct {
	Generation     string   `json:"generation"`
	Backend        string   `json:"backend,omitempty"`
	Verified       bool     `json:"verified"`
	CorpusHash     string   `json:"corpus_hash"`
	ProjectionHash string   `json:"projection_hash"`
	EmbeddingModel string   `json:"embedding_model"`
	Dimension      int      `json:"dimension"`
	RecordCount    int      `json:"record_count"`
	Indexes        []string `json:"indexes"`
}

func New(db *sql.DB, schema string) (*Store, error) {
	if db == nil || !schemaPattern.MatchString(schema) {
		return nil, ErrInvalid
	}
	return &Store{db: db, schema: schema}, nil
}
func (s *Store) table(name string) string      { return `"` + s.schema + `"."` + name + `"` }
func (s *Store) ForScope(scope string) *Scoped { return &Scoped{store: s, scope: scope} }
func (s *Scoped) valid() bool                  { return s != nil && s.store != nil && validID(s.scope) }

// WithPublicationLock reserves one PostgreSQL connection and transaction for
// the whole callback. The callback uses the normal pool for immutable facts;
// rollback/commit releases the advisory lock before its connection is reused.
func (s *Scoped) WithPublicationLock(ctx context.Context, key string, fn func(context.Context) error) error {
	if !s.valid() || !validID(key) || fn == nil {
		return ErrInvalid
	}
	connection, err := s.store.db.Conn(ctx)
	if err != nil {
		return publicationLockError(ctx)
	}
	defer connection.Close()
	// Keep the lock until the callback actually returns, even after cancellation.
	// Individual acquisition/callback operations still observe the caller context.
	tx, err := connection.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return publicationLockError(ctx)
	}
	defer tx.Rollback()
	lockKey := "meme-publication:" + s.store.schema + ":" + s.scope + ":" + key
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return publicationLockError(ctx)
	}
	if err = fn(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = tx.Commit(); err != nil {
		return publicationLockError(ctx)
	}
	return nil
}

func publicationLockError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrPublicationLock
}

func validID(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validKind(kind Kind) bool {
	for _, k := range factKinds {
		if k == kind {
			return true
		}
	}
	return false
}
func uniqueID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func canonical(value any) (json.RawMessage, string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 8<<20 {
		return nil, "", ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var decoded any
	if err = d.Decode(&decoded); err != nil {
		return nil, "", ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, "", ErrInvalid
	}
	raw, err = json.Marshal(decoded)
	if err != nil {
		return nil, "", ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "meme-migrate:"+s.schema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+s.schema+`"`); err != nil {
		return err
	}
	for _, kind := range factKinds {
		if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+s.table(string(kind))+` (
   scope_key text NOT NULL,id text NOT NULL,fingerprint text NOT NULL,body bytea NOT NULL,
   created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,id))`); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("meme_active_generation") + ` (scope_key text PRIMARY KEY,generation text NOT NULL,FOREIGN KEY(scope_key,generation) REFERENCES ` + s.table(string(Generation)) + `(scope_key,id))`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := s.migrateQuota(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Scoped) Put(ctx context.Context, kind Kind, id string, value any) (Record, error) {
	if !s.valid() || !validKind(kind) || !validID(id) {
		return Record{}, ErrInvalid
	}
	body, fingerprint, err := canonical(value)
	if err != nil {
		return Record{}, err
	}
	_, err = s.store.db.ExecContext(ctx, `INSERT INTO `+s.store.table(string(kind))+` (scope_key,id,fingerprint,body) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.scope, id, fingerprint, []byte(body))
	if err != nil {
		return Record{}, err
	}
	existing, err := s.Get(ctx, kind, id)
	if err != nil {
		return Record{}, err
	}
	if existing.Fingerprint != fingerprint {
		return Record{}, ErrConflict
	}
	return existing, nil
}
func (s *Scoped) Get(ctx context.Context, kind Kind, id string) (Record, error) {
	if !s.valid() || !validKind(kind) || !validID(id) {
		return Record{}, ErrInvalid
	}
	r := Record{ID: id, Scope: s.scope}
	err := s.store.db.QueryRowContext(ctx, `SELECT fingerprint,body,created_at FROM `+s.store.table(string(kind))+` WHERE scope_key=$1 AND id=$2`, s.scope, id).Scan(&r.Fingerprint, &r.Body, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	_, fingerprint, err := canonical(r.Body)
	if err != nil || fingerprint != r.Fingerprint {
		return Record{}, ErrConflict
	}
	return r, nil
}
func (s *Scoped) List(ctx context.Context, kind Kind, limit int) ([]Record, error) {
	if !s.valid() || !validKind(kind) || limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id,fingerprint,body,created_at FROM `+s.store.table(string(kind))+` WHERE scope_key=$1 ORDER BY created_at,id LIMIT $2`, s.scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r := Record{Scope: s.scope}
		if err = rows.Scan(&r.ID, &r.Fingerprint, &r.Body, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Scoped) Activate(ctx context.Context, id string) error {
	record, err := s.Get(ctx, Generation, id)
	if err != nil {
		return err
	}
	var manifest GenerationManifest
	if json.Unmarshal(record.Body, &manifest) != nil || !manifest.Verified || manifest.Generation != id || manifest.CorpusHash == "" || manifest.ProjectionHash == "" || manifest.Dimension < 1 || manifest.RecordCount < 1 || len(manifest.Indexes) != 4 {
		return ErrInvalid
	}
	for _, name := range manifest.Indexes {
		if !indexPattern.MatchString(name) {
			return ErrInvalid
		}
	}
	_, err = s.store.db.ExecContext(ctx, `INSERT INTO `+s.store.table("meme_active_generation")+`(scope_key,generation) VALUES ($1,$2) ON CONFLICT(scope_key) DO UPDATE SET generation=excluded.generation`, s.scope, id)
	return err
}
func (s *Scoped) Active(ctx context.Context) (GenerationManifest, error) {
	if !s.valid() {
		return GenerationManifest{}, ErrInvalid
	}
	var id string
	err := s.store.db.QueryRowContext(ctx, `SELECT generation FROM `+s.store.table("meme_active_generation")+` WHERE scope_key=$1`, s.scope).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return GenerationManifest{}, ErrNotFound
	}
	if err != nil {
		return GenerationManifest{}, err
	}
	r, err := s.Get(ctx, Generation, id)
	if err != nil {
		return GenerationManifest{}, err
	}
	var result GenerationManifest
	if err = json.Unmarshal(r.Body, &result); err != nil {
		return result, fmt.Errorf("%w: generation", ErrInvalid)
	}
	return result, nil
}
