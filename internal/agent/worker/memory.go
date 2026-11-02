package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/hindsight"
)

func stageName(name string, c domain.Context) string {
	return fmt.Sprintf("%s:%s:%d", name, c.ID, c.Version)
}
func (w *Worker) publish(ctx context.Context, j domain.Job, c domain.Context, name string, write, clean func(context.Context) error) error {
	return w.c.Store.Publication(ctx, j.Scope, "context:"+c.ID, func(ctx context.Context) error {
		current, err := w.c.Store.Context(ctx, j.Scope, c.ID)
		if err != nil {
			return err
		}
		if current.Invalidated || current.Version != c.Version {
			return domain.ErrStaleSource
		}
		if err = w.valid(ctx, j, c.Sources); err != nil {
			return err
		}
		stage := stageName(name, c)
		if _, err = w.c.Store.Stage(ctx, j.Source, stage); err == nil {
			return nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err = write(ctx); err != nil {
			return err
		}
		current, err = w.c.Store.Context(ctx, j.Scope, c.ID)
		if err == nil && (current.Invalidated || current.Version != c.Version) {
			err = domain.ErrStaleSource
		}
		if err == nil {
			err = w.valid(ctx, j, c.Sources)
		}
		if err != nil {
			if cleanErr := clean(ctx); cleanErr != nil {
				return cleanErr
			}
			return err
		}
		return w.c.Store.PutStage(ctx, j, domain.PipelineStage{Source: j.Source, Name: stage, ContextID: c.ID, ContextVersion: c.Version}, c.Sources)
	})
}
func (w *Worker) embed(ctx context.Context, j domain.Job) error {
	c, err := w.jobContext(ctx, j)
	if err != nil {
		return err
	}
	if w.c.Embedder == nil {
		return fmt.Errorf("embedding model unavailable")
	}
	vectors, err := w.c.Embedder.Embed(ctx, []string{c.Text})
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("context embedding count")
	}
	return w.publish(ctx, j, c, "embed", func(ctx context.Context) error { return w.c.Index.UpsertContext(ctx, c, vectors[0]) }, func(ctx context.Context) error { return w.c.Index.DeleteContext(ctx, c.Scope, c.ID) })
}
func (w *Worker) memory(ctx context.Context, j domain.Job) error {
	c, err := w.jobContext(ctx, j)
	if err != nil {
		return err
	}
	if w.c.Memory == nil {
		return fmt.Errorf("memory store unavailable")
	}
	return w.publish(ctx, j, c, "memory", func(ctx context.Context) error { return w.c.Memory.Retain(ctx, c) }, func(ctx context.Context) error {
		return w.c.Memory.DeleteDocument(ctx, c.Scope, hindsight.DocumentID(c.Scope, c.ID))
	})
}
