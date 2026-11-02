// Package domain defines the new group agent's facts and small dependency ports.
// It does not depend on transports, configuration, storage, or model SDKs.
package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrMusicAudioUnsupported = errors.New("music card audio unsupported")
	ErrInvalid               = errors.New("invalid agent input")
	ErrNotFound              = errors.New("agent record not found")
	ErrConflict              = errors.New("agent immutable record conflict")
	ErrStaleClaim            = errors.New("agent job claim expired or superseded")
	ErrStaleSource           = errors.New("agent source no longer current")
)

type Scope struct{ TenantID, AppID, ChatID, Visibility string }

func (s Scope) Validate() error {
	for name, value := range map[string]string{"tenant": s.TenantID, "app": s.AppID, "chat": s.ChatID, "visibility": s.Visibility} {
		if !validID(value) {
			return fmt.Errorf("%w: %s scope", ErrInvalid, name)
		}
	}
	return nil
}

// Key is a stable, unambiguous hash of all four scope components. Call Validate
// at trust boundaries; the key itself is not an authorization decision.
func (s Scope) Key() string { return digest([]string{s.TenantID, s.AppID, s.ChatID, s.Visibility}) }

func validID(s string) bool {
	return s != "" && len(s) <= 1024 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type SourceRef struct {
	Scope      Scope
	MessageID  string
	Version    int64
	AuthorID   string
	OccurredAt time.Time
}

func (s SourceRef) Validate() error {
	if err := s.Scope.Validate(); err != nil {
		return err
	}
	if !validID(s.MessageID) || !validID(s.AuthorID) || s.Version <= 0 || s.OccurredAt.IsZero() {
		return fmt.Errorf("%w: source reference", ErrInvalid)
	}
	return nil
}

type MediaType string
type MediaSource string

const (
	MediaImage      MediaType   = "image"
	MediaAudio      MediaType   = "audio"
	MediaVideo      MediaType   = "video"
	MediaFile       MediaType   = "file"
	MediaSourceLark MediaSource = "lark"
)

// ResourceID is a durable provider resource locator, never a signed download URL.
// Source author identity remains in Message.Source, separate from the requester.
type Media struct {
	Type                       MediaType
	Source                     MediaSource
	ResourceID, MIME, FileName string
	Sequence                   int
}

type Message struct {
	Source                            SourceRef
	EventID, ParentID, ThreadID, Text string
	// RootID is a message locator; ThreadID is provider topic metadata (omt_...).
	// Omitting an empty RootID preserves the previous serialized message shape.
	RootID    string `json:",omitempty"`
	UpdatedAt time.Time
	Deleted   bool
	Raw       json.RawMessage
	Media     []Media
}

func (m Message) Clone() Message {
	m.Raw = append(json.RawMessage(nil), m.Raw...)
	m.Media = append([]Media(nil), m.Media...)
	return m
}

type EventType string

const (
	EventMessage EventType = "message"
	EventDelete  EventType = "delete"
)

// ID is immutable within Scope. Source.Version is a monotonic provider mutation
// sequence; UpdatedAt is the exact source mutation time, not ingestion time.
// OccurredAt records the event's exact provider timestamp independently.
type Event struct {
	ID         string
	Type       EventType
	Message    Message
	OccurredAt time.Time
}

func (e Event) Clone() Event { e.Message = e.Message.Clone(); return e }

func (e Event) Validate() error {
	if e.Type == EventDelete {
		if err := e.Message.Source.Scope.Validate(); err != nil {
			return err
		}
		if !validID(e.Message.Source.MessageID) || e.Message.Source.Version <= 0 || (e.Message.Source.AuthorID != "" && !validID(e.Message.Source.AuthorID)) {
			return fmt.Errorf("%w: tombstone source", ErrInvalid)
		}
	} else if err := e.Message.Source.Validate(); err != nil {
		return err
	}
	if !validID(e.ID) || e.Message.EventID != e.ID || e.OccurredAt.IsZero() || e.Message.UpdatedAt.IsZero() || e.Message.UpdatedAt.Before(e.Message.Source.OccurredAt) {
		return fmt.Errorf("%w: event identity or timestamps", ErrInvalid)
	}
	if (e.Type != EventMessage && e.Type != EventDelete) || (e.Type == EventDelete) != e.Message.Deleted {
		return fmt.Errorf("%w: event mutation type", ErrInvalid)
	}
	if len(e.Message.Raw) > MaxPayloadBytes || (len(e.Message.Raw) > 0 && !json.Valid(e.Message.Raw)) {
		return fmt.Errorf("%w: raw event", ErrInvalid)
	}
	for _, m := range e.Message.Media {
		if !validID(m.ResourceID) || m.Sequence < 0 || (m.Type != MediaImage && m.Type != MediaAudio && m.Type != MediaVideo && m.Type != MediaFile) {
			return fmt.Errorf("%w: media locator", ErrInvalid)
		}
	}
	return nil
}

type QueryMode string

const (
	QueryLiteral    QueryMode = "literal"
	QueryDiscussion QueryMode = "discussion"
	QueryMemory     QueryMode = "memory"
)

type Query struct {
	Scope          Scope
	Anchor         time.Time
	Text, AuthorID string
	Mode           QueryMode
	Limit          int
}

func (q Query) Validate() error {
	if err := q.Scope.Validate(); err != nil {
		return err
	}
	if q.Anchor.IsZero() || q.Limit <= 0 || q.Limit > 1000 || (q.Mode != QueryLiteral && q.Mode != QueryDiscussion && q.Mode != QueryMemory) {
		return fmt.Errorf("%w: query anchor, limit, or mode", ErrInvalid)
	}
	return nil
}

type Evidence struct {
	ID, Text, Channel string
	// After retrieval verification, Text holds canonical PG originals and
	// DerivedText holds source-validated context, media, or memory interpretation.
	// DerivedText never grants authority over approvals or other business facts.
	// Candidate ports may supply derived content in Text; retrieval normalizes
	// that legacy index shape once on entry. Memory producers use DerivedText.
	DerivedText string
	Sources     []SourceRef
	Score       float64
}

type SourceReader interface {
	Message(context.Context, Scope, string) (Message, error)
	Recent(context.Context, Scope, time.Time, int) ([]Message, error)
	ValidSources(context.Context, Query, []SourceRef) (bool, error)
}

type Model interface {
	Embed(context.Context, []string) ([][]float32, error)
	Rerank(context.Context, string, []Evidence) ([]Evidence, error)
}

// Context is derived discussion evidence. ID is stable across rebuilds and
// Version increases on replacement. Every dependency is retained in Sources.
type Context struct {
	ID                   string
	Scope                Scope
	Version              int64
	Text                 string
	Sources              []SourceRef
	CreatedAt, UpdatedAt time.Time
	Invalidated          bool
	InvalidatedAt        *time.Time
}

func (c Context) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !validID(c.ID) || c.Version <= 0 || len(c.Sources) == 0 || c.CreatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
		return fmt.Errorf("%w: context metadata or sources", ErrInvalid)
	}
	for _, s := range c.Sources {
		if err := s.Validate(); err != nil {
			return err
		}
		if s.Scope != c.Scope {
			return fmt.Errorf("%w: context source scope", ErrInvalid)
		}
	}
	return nil
}

type JobKind string

const (
	JobIndexMessage JobKind = "index_message"
	JobRoute        JobKind = "route"
	JobMedia        JobKind = "media"
	JobContexts     JobKind = "contexts"
	JobMemory       JobKind = "memory"
	JobCleanup      JobKind = "cleanup"
	JobAgentTurn    JobKind = "agent_turn"
	JobDelivery     JobKind = "delivery"
	// JobPresentation continues a server-owned card interaction or media fill.
	// It uses the same durable queue and lease fences as other application work.
	JobPresentation JobKind = "presentation"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)
const MaxPayloadBytes = 1 << 20

type JobSpec struct {
	Kind                   JobKind
	Scope                  Scope
	Source                 SourceRef
	Payload                json.RawMessage
	DedupeKey              string
	Priority, MaxAttempts  int
	AvailableAt, ExpiresAt time.Time
}

type Job struct {
	ID int64
	JobSpec
	Status                           JobStatus
	Owner                            string
	Fence                            int64
	Attempt                          int
	LeaseUntil, CreatedAt, UpdatedAt time.Time
	LastError                        string
}
