package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/hindsight"
)

func (w *Worker) cleanup(ctx context.Context, j domain.Job) error {
	if err := w.c.Store.CheckClaim(ctx, j); err != nil {
		return err
	}
	var payload struct {
		IDs []string `json:"context_ids"`
	}
	if json.Unmarshal(j.Payload, &payload) != nil || len(payload.IDs) > 4096 {
		return domain.ErrInvalid
	}
	// Message deliberately includes tombstones. ValidSources must never gate
	// cleanup, because deleted facts are exactly the facts that require removal.
	m, err := w.c.Store.Message(ctx, j.Scope, j.Source.MessageID)
	if err != nil {
		return err
	}
	if m.Deleted {
		if err = w.c.Store.Publication(ctx, j.Scope, "message:"+j.Source.MessageID, func(ctx context.Context) error { return w.c.Index.DeleteMessage(ctx, j.Scope, j.Source.MessageID) }); err != nil {
			return err
		}
	}
	for _, id := range payload.IDs {
		err = w.c.Store.Publication(ctx, j.Scope, "context:"+id, func(ctx context.Context) error {
			if err := w.c.Store.CheckClaim(ctx, j); err != nil {
				return err
			}
			c, err := w.c.Store.Context(ctx, j.Scope, id)
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			for _, channel := range []string{"embed", "memory"} {
				done, err := w.c.Store.CleanupDone(ctx, j, id, channel)
				if err != nil {
					return err
				}
				if done {
					continue
				}
				// Only a durable publication receipt for this exact current version proves
				// a channel was replaced. Rebuilding the PG context alone proves no remote write.
				replaced := false
				if !c.Invalidated && len(c.Sources) > 0 {
					anchor := c.Sources[len(c.Sources)-1]
					_, err = w.c.Store.Stage(ctx, anchor, stageName(channel, c))
					if err == nil {
						replaced = true
					} else if !errors.Is(err, domain.ErrNotFound) {
						return err
					}
				}
				if !replaced {
					if channel == "embed" {
						err = w.c.Index.DeleteContext(ctx, j.Scope, id)
					} else if w.c.Memory != nil {
						err = w.c.Memory.DeleteDocument(ctx, j.Scope, hindsight.DocumentID(j.Scope, id))
					} else {
						return fmt.Errorf("memory cleanup unavailable")
					}
					if err != nil {
						return err
					}
				}
				if err = w.c.Store.MarkCleanupDone(ctx, j, id, channel); err != nil {
					return err
				}
			}
			if !c.Invalidated {
				return nil
			}
			if len(c.Sources) == 0 {
				return nil
			}
			anchor := c.Sources[len(c.Sources)-1]
			current, err := w.c.Store.Message(ctx, j.Scope, anchor.MessageID)
			if err != nil {
				return err
			}
			if current.Deleted {
				return nil
			}
			_, err = w.c.Store.Enqueue(ctx, domain.JobSpec{Kind: domain.JobContexts, Scope: j.Scope, Source: current.Source, DedupeKey: fmt.Sprintf("cleanup:%d:%s:%d", j.ID, id, current.Source.Version)})
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
