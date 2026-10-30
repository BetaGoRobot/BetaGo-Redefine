package hindsight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const provenanceKey = "agent_provenance"

type sourceDTO struct {
	Scope      domain.Scope `json:"scope"`
	MessageID  string       `json:"message_id"`
	Version    string       `json:"version"`
	AuthorID   string       `json:"author_id"`
	OccurredAt time.Time    `json:"occurred_at"`
}
type provenance struct {
	SchemaVersion  int          `json:"schema_version"`
	Scope          domain.Scope `json:"scope"`
	ContextID      string       `json:"context_id"`
	ContextVersion string       `json:"context_version"`
	Sources        []sourceDTO  `json:"sources"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}
type memoryItem struct {
	Content    string            `json:"content"`
	Timestamp  string            `json:"timestamp"`
	DocumentID string            `json:"document_id"`
	Metadata   map[string]string `json:"metadata"`
	Tags       []string          `json:"tags"`
}
type retainRequest struct {
	Items []memoryItem `json:"items"`
	Async bool         `json:"async"`
}

func (c *Client) Retain(ctx context.Context, document domain.Context) error {
	// Preflight before validation/serialization/network calls bounds allocation
	// for caller-controlled strings and collections, including escaped metadata.
	if len(document.Sources) == 0 || len(document.Sources) > 256 || len(document.Text) == 0 || int64(len(document.Text)) > c.maxRequest || !utf8.ValidString(document.Text) {
		return fmt.Errorf("%w: hindsight context budget", domain.ErrInvalid)
	}
	budget := int64(len(document.Text) + len(document.ID) + 256)
	for _, s := range document.Sources {
		budget += int64(len(s.MessageID) + len(s.AuthorID) + len(s.Scope.TenantID) + len(s.Scope.AppID) + len(s.Scope.ChatID) + len(s.Scope.Visibility) + 192)
		if budget > c.maxRequest {
			return fmt.Errorf("%w: hindsight context budget", domain.ErrInvalid)
		}
	}
	if err := document.Validate(); err != nil {
		return err
	}
	if document.Invalidated {
		return fmt.Errorf("%w: invalidated hindsight context", domain.ErrInvalid)
	}
	p := provenance{SchemaVersion: 1, Scope: document.Scope, ContextID: document.ID, ContextVersion: strconv.FormatInt(document.Version, 10), CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt, Sources: make([]sourceDTO, len(document.Sources))}
	for i, s := range document.Sources {
		if s.OccurredAt.After(document.UpdatedAt) {
			return fmt.Errorf("%w: hindsight source time", domain.ErrInvalid)
		}
		p.Sources[i] = sourceDTO{Scope: s.Scope, MessageID: s.MessageID, Version: strconv.FormatInt(s.Version, 10), AuthorID: s.AuthorID, OccurredAt: s.OccurredAt}
	}
	metadata, err := c.encode(p)
	if err != nil {
		return err
	}
	data, err := c.encode(retainRequest{Items: []memoryItem{{Content: document.Text, Timestamp: document.UpdatedAt.Format(time.RFC3339Nano), DocumentID: DocumentID(document.Scope, document.ID), Metadata: map[string]string{provenanceKey: string(metadata)}, Tags: []string{document.Scope.Key()}}}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.ensureBank(ctx, document.Scope); err != nil {
		return err
	}
	var ack struct {
		Success    *bool  `json:"success"`
		BankID     string `json:"bank_id"`
		ItemsCount *int   `json:"items_count"`
		Async      *bool  `json:"async"`
	}
	if err := c.request(ctx, http.MethodPost, bankPath(document.Scope)+"/memories", data, &ack); err != nil {
		return err
	}
	if ack.Success == nil || !*ack.Success || ack.BankID != BankID(document.Scope) || ack.ItemsCount == nil || *ack.ItemsCount != 1 || ack.Async == nil || *ack.Async {
		return errors.New("hindsight retain acknowledgement invalid")
	}
	return nil
}
