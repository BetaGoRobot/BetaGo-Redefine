package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func ContextID(scope domain.Scope, messageID string) string {
	sum := sha256.Sum256([]byte(scope.Key() + ":explicit-context:" + messageID))
	return hex.EncodeToString(sum[:])
}

type contextPayload struct {
	ID      string `json:"context_id"`
	Version int64  `json:"context_version"`
}

func (w *Worker) buildContext(ctx context.Context, m domain.Message) (domain.Context, error) {
	deletedBoundary := false
	chain := []domain.Message{m}
	seen := map[string]bool{m.Source.MessageID: true}
	for len(chain) < 64 {
		last := chain[len(chain)-1]
		parent := last.ParentID
		// Feishu ThreadID (omt_...) identifies a topic, never a message.
		// Only explicit parent/root message locators can extend source provenance.
		if parent == "" && last.RootID != last.Source.MessageID {
			parent = last.RootID
		}
		if parent == "" {
			break
		}
		if seen[parent] {
			return domain.Context{}, fmt.Errorf("explicit context cycle")
		}
		seen[parent] = true
		p, err := w.c.Store.Message(ctx, m.Source.Scope, parent)
		if errors.Is(err, domain.ErrNotFound) {
			// Provider events may arrive out of order. Keep the continuation durable
			// until its explicit ancestor exists; publishing here would permanently
			// omit an unobserved dependency from invalidation and memory provenance.
			return domain.Context{}, ErrNotReady
		}
		if err != nil {
			return domain.Context{}, err
		}
		if p.Deleted {
			deletedBoundary = true
			break
		}
		chain = append(chain, p)
	}
	if len(chain) == 64 {
		return domain.Context{}, fmt.Errorf("explicit context depth limit")
	}
	c := domain.Context{ID: ContextID(m.Source.Scope, m.Source.MessageID), Scope: m.Source.Scope, Version: 1, CreatedAt: m.Source.OccurredAt, UpdatedAt: m.UpdatedAt}
	var text strings.Builder
	if deletedBoundary {
		text.WriteString("[discussion boundary: an explicit ancestor was deleted; its content is omitted]\n")
	}
	for i := len(chain) - 1; i >= 0; i-- {
		item := chain[i]
		if len(item.Text) > 1<<20-text.Len() {
			return c, fmt.Errorf("context text budget exceeded")
		}
		c.Sources = append(c.Sources, item.Source)
		if item.Source.OccurredAt.Before(c.CreatedAt) {
			c.CreatedAt = item.Source.OccurredAt
		}
		if item.UpdatedAt.After(c.UpdatedAt) {
			c.UpdatedAt = item.UpdatedAt
		}
		fmt.Fprintf(&text, "[message=%s author=%s at=%s]\n%s\n", item.Source.MessageID, item.Source.AuthorID, item.Source.OccurredAt.Format(time.RFC3339Nano), item.Text)
		if len(item.Media) > 0 {
			a, err := w.c.Store.Media(ctx, item.Source)
			if errors.Is(err, domain.ErrNotFound) {
				return c, ErrNotReady
			}
			if err != nil {
				return c, err
			}
			for _, part := range a.Parts {
				fmt.Fprintf(&text, "[media resource=%s page=%d start=%s end=%s]\n%s\n", part.Object.ResourceID, part.Page, part.Start, part.End, part.Text)
			}
		}
		if text.Len() > 1<<20 {
			return c, fmt.Errorf("context text budget exceeded")
		}
	}
	c.Text = text.String()
	return c, nil
}
func (w *Worker) contexts(ctx context.Context, j domain.Job) error {
	m, err := w.current(ctx, j)
	if err != nil {
		return err
	}
	if m.Deleted {
		return domain.ErrStaleSource
	}
	c, err := w.buildContext(ctx, m)
	if err != nil {
		return err
	}
	return w.c.Store.Publication(ctx, j.Scope, "context:"+c.ID, func(ctx context.Context) error {
		if err := w.valid(ctx, j, c.Sources); err != nil {
			return err
		}
		existing, err := w.c.Store.Context(ctx, j.Scope, c.ID)
		if err == nil {
			if !existing.Invalidated && existing.Text == c.Text && reflect.DeepEqual(existing.Sources, c.Sources) {
				c = existing
			} else {
				c.Version = existing.Version + 1
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err = w.c.Store.PutContext(ctx, c); err != nil {
			return err
		}
		if err = w.c.Store.PutStage(ctx, j, domain.PipelineStage{Source: j.Source, Name: "contexts", ContextID: c.ID, ContextVersion: c.Version}, c.Sources); err != nil {
			return err
		}
		payload, _ := json.Marshal(contextPayload{c.ID, c.Version})
		for _, kind := range []domain.JobKind{domain.JobEmbedContext, domain.JobMemory} {
			if _, err = w.c.Store.Enqueue(ctx, domain.JobSpec{Kind: kind, Scope: j.Scope, Source: j.Source, Payload: payload, DedupeKey: fmt.Sprintf("context:%s:%d", c.ID, c.Version)}); err != nil {
				return err
			}
		}
		return nil
	})
}
func (w *Worker) jobContext(ctx context.Context, j domain.Job) (domain.Context, error) {
	m, err := w.current(ctx, j)
	if err != nil {
		return domain.Context{}, err
	}
	if m.Deleted {
		return domain.Context{}, domain.ErrStaleSource
	}
	var p contextPayload
	if json.Unmarshal(j.Payload, &p) != nil {
		return domain.Context{}, domain.ErrInvalid
	}
	if p.ID == "" {
		stage, err := w.c.Store.Stage(ctx, j.Source, "contexts")
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Context{}, ErrNotReady
		}
		if err != nil {
			return domain.Context{}, err
		}
		p = contextPayload{stage.ContextID, stage.ContextVersion}
	}
	c, err := w.c.Store.Context(ctx, j.Scope, p.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return c, ErrNotReady
	}
	if err != nil {
		return c, err
	}
	if c.Invalidated || c.Version != p.Version {
		return c, domain.ErrStaleSource
	}
	return c, w.valid(ctx, j, c.Sources)
}
