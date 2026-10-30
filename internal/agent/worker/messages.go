package worker

import (
	"context"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (w *Worker) indexMessage(ctx context.Context, j domain.Job) error {
	return w.c.Store.Publication(ctx, j.Scope, "message:"+j.Source.MessageID, func(ctx context.Context) error {
		m, err := w.current(ctx, j)
		if err != nil {
			return err
		}
		if err = w.c.Index.UpsertMessage(ctx, m); err != nil {
			return err
		}
		latest, err := w.current(ctx, j)
		if err != nil || latest.Source.Version != m.Source.Version {
			if cleanErr := w.c.Index.DeleteMessage(ctx, j.Scope, j.Source.MessageID); cleanErr != nil {
				return cleanErr
			}
			if err != nil {
				return err
			}
			return domain.ErrStaleSource
		}
		return nil
	})
}
func (w *Worker) media(ctx context.Context, j domain.Job) error {
	m, err := w.current(ctx, j)
	if err != nil {
		return err
	}
	if m.Deleted {
		return domain.ErrStaleSource
	}
	if _, err = w.c.Store.Media(ctx, m.Source); err == nil {
		return nil
	} else if err != domain.ErrNotFound {
		return err
	}
	if w.c.Media == nil {
		return fmt.Errorf("media processor unavailable")
	}
	a, err := w.c.Media.Process(ctx, m)
	if err != nil {
		return err
	}
	return w.c.Store.PutMedia(ctx, j, a)
}
