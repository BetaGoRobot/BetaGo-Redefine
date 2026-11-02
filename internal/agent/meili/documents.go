package meili

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func stableID(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func documentID(s domain.Scope, id string) string {
	return stableID(s.TenantID, s.AppID, s.ChatID, s.Visibility, id)
}
func authorHash(s domain.Scope, id string) string { return stableID("author", s.Key(), id) }

type scopeDTO struct {
	TenantID   string `json:"tenant_id"`
	AppID      string `json:"app_id"`
	ChatID     string `json:"chat_id"`
	Visibility string `json:"visibility"`
}

func scoped(s domain.Scope) scopeDTO { return scopeDTO{s.TenantID, s.AppID, s.ChatID, s.Visibility} }
func (s scopeDTO) domain() domain.Scope {
	return domain.Scope{TenantID: s.TenantID, AppID: s.AppID, ChatID: s.ChatID, Visibility: s.Visibility}
}

// Versions are decimal strings because engine numeric storage must never round
// an opaque provider mutation sequence above 2^53. Timestamps retain RFC3339Nano
// source identity; filtering uses two exact, small integers instead of UnixNano.
type sourceDTO struct {
	Scope      scopeDTO  `json:"scope"`
	MessageID  string    `json:"message_id"`
	Version    string    `json:"version"`
	AuthorID   string    `json:"author_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func source(s domain.SourceRef) sourceDTO {
	return sourceDTO{scoped(s.Scope), s.MessageID, strconv.FormatInt(s.Version, 10), s.AuthorID, s.OccurredAt}
}
func (s sourceDTO) domain() (domain.SourceRef, error) {
	v, err := strconv.ParseInt(s.Version, 10, 64)
	if err != nil {
		return domain.SourceRef{}, domain.ErrInvalid
	}
	ref := domain.SourceRef{Scope: s.Scope.domain(), MessageID: s.MessageID, Version: v, AuthorID: s.AuthorID, OccurredAt: s.OccurredAt}
	return ref, ref.Validate()
}

type metadata struct {
	ID            string `json:"id"`
	SchemaVersion int    `json:"schema_version"`
	ScopeHash     string `json:"scope_hash"`
	scopeDTO
	Text         string    `json:"text"`
	OccurredSec  int64     `json:"occurred_sec"`
	OccurredNano int       `json:"occurred_nano"`
	UpdatedSec   int64     `json:"updated_sec"`
	UpdatedNano  int       `json:"updated_nano"`
	UpdatedAt    time.Time `json:"updated_at"`
	Score        *float64  `json:"_rankingScore,omitempty"`
}

func meta(s domain.Scope, id, text string, occurred, updated time.Time) metadata {
	return metadata{ID: documentID(s, id), SchemaVersion: 1, ScopeHash: s.Key(), scopeDTO: scoped(s), Text: text, OccurredSec: occurred.Unix(), OccurredNano: occurred.Nanosecond(), UpdatedSec: updated.Unix(), UpdatedNano: updated.Nanosecond(), UpdatedAt: updated}
}

type messageDTO struct {
	metadata
	MessageID  string    `json:"message_id"`
	AuthorID   string    `json:"author_id"`
	AuthorHash string    `json:"author_hash"`
	Source     sourceDTO `json:"source"`
}

// UpsertMessage indexes live facts only. Tombstones are source-scoped deletes
// and do not require author information that delete events may not contain.
func (c *Client) UpsertMessage(ctx context.Context, m domain.Message) error {
	if m.Deleted {
		return c.DeleteMessage(ctx, m.Source.Scope, m.Source.MessageID)
	}
	if err := m.Source.Validate(); err != nil {
		return err
	}
	if m.UpdatedAt.IsZero() || m.UpdatedAt.Before(m.Source.OccurredAt) {
		return fmt.Errorf("%w: meili message mutation time", domain.ErrInvalid)
	}
	d := messageDTO{metadata: meta(m.Source.Scope, m.Source.MessageID, m.Text, m.Source.OccurredAt, m.UpdatedAt), MessageID: m.Source.MessageID, AuthorID: m.Source.AuthorID, AuthorHash: authorHash(m.Source.Scope, m.Source.AuthorID), Source: source(m.Source)}
	ctx, cancel := c.operation(ctx)
	defer cancel()
	return c.mutate(ctx, http.MethodPost, "/indexes/"+c.messages+"/documents", []messageDTO{d})
}

func validateID(s domain.Scope, id string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	return (domain.Scope{TenantID: id, AppID: "x", ChatID: "x", Visibility: "x"}).Validate()
}

func (c *Client) DeleteMessage(ctx context.Context, s domain.Scope, id string) error {
	if err := validateID(s, id); err != nil {
		return err
	}
	ctx, cancel := c.operation(ctx)
	defer cancel()
	return c.mutate(ctx, http.MethodDelete, "/indexes/"+c.messages+"/documents/"+documentID(s, id), nil)
}

type contextDTO struct {
	metadata
	ContextID      string               `json:"context_id"`
	Version        string               `json:"version"`
	Sources        []sourceDTO          `json:"sources"`
	CreatedAt      time.Time            `json:"created_at"`
	Invalidated    *bool                `json:"invalidated"`
	AuthorIDs      []string             `json:"author_ids"`
	AuthorHashes   []string             `json:"author_hashes"`
	EmbeddingModel string               `json:"embedding_model"`
	Vectors        map[string][]float32 `json:"_vectors,omitempty"`
}

func (c *Client) validateVector(vector []float32) error {
	if len(vector) != c.dimensions {
		return fmt.Errorf("%w: meili vector dimension", domain.ErrInvalid)
	}
	for _, x := range vector {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return fmt.Errorf("%w: meili vector component", domain.ErrInvalid)
		}
	}
	return nil
}

// UpsertContext accepts exactly one externally generated vector for one
// context. No embedding provider is called. Invalidated contexts are deleted.
func (c *Client) UpsertContext(ctx context.Context, d domain.Context, vector []float32) error {
	if d.Invalidated {
		return c.DeleteContext(ctx, d.Scope, d.ID)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if err := c.validateVector(vector); err != nil {
		return err
	}
	latest := d.Sources[0].OccurredAt
	refs := make([]sourceDTO, len(d.Sources))
	authors := make([]string, 0, len(d.Sources))
	authorHashes := make([]string, 0, len(d.Sources))
	seen := map[string]bool{}
	for i, s := range d.Sources {
		refs[i] = source(s)
		if s.OccurredAt.After(latest) {
			latest = s.OccurredAt
		}
		if !seen[s.AuthorID] {
			authors = append(authors, s.AuthorID)
			authorHashes = append(authorHashes, authorHash(d.Scope, s.AuthorID))
			seen[s.AuthorID] = true
		}
	}
	if latest.After(d.UpdatedAt) {
		return fmt.Errorf("%w: meili context source time", domain.ErrInvalid)
	}
	invalidated := false
	doc := contextDTO{metadata: meta(d.Scope, d.ID, d.Text, latest, d.UpdatedAt), ContextID: d.ID, Version: strconv.FormatInt(d.Version, 10), Sources: refs, CreatedAt: d.CreatedAt, Invalidated: &invalidated, AuthorIDs: authors, AuthorHashes: authorHashes, EmbeddingModel: c.model, Vectors: map[string][]float32{"external": vector}}
	ctx, cancel := c.operation(ctx)
	defer cancel()
	return c.mutate(ctx, http.MethodPost, "/indexes/"+c.contexts+"/documents", []contextDTO{doc})
}

func (c *Client) DeleteContext(ctx context.Context, s domain.Scope, id string) error {
	if err := validateID(s, id); err != nil {
		return err
	}
	ctx, cancel := c.operation(ctx)
	defer cancel()
	return c.mutate(ctx, http.MethodDelete, "/indexes/"+c.contexts+"/documents/"+documentID(s, id), nil)
}
