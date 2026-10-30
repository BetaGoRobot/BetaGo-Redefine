package engine

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// Task materials follow only verified explicit message edges. Group recency and
// provider topic IDs never extend the set of task-bound media dependencies.
func (e *Engine) taskMaterials(ctx context.Context, t domain.Task) ([]domain.Message, error) {
	since, err := e.c.Store.CatalogHistorySince(ctx, t.Scope)
	if err != nil {
		return nil, err
	}
	var out []domain.Message
	seen := map[string]bool{}
	active := map[string]bool{}
	var visit func(domain.Message, int) error
	visit = func(m domain.Message, depth int) error {
		id := m.Source.MessageID
		if active[id] {
			return domain.ErrInvalid
		}
		if seen[id] {
			return nil
		}
		if depth >= 64 || len(out) >= 128 {
			return domain.ErrInvalid
		}
		if m.Source.Scope != t.Scope {
			return domain.ErrUnauthorized
		}
		if m.Deleted {
			return ErrMediaFailed
		}
		if m.Source.OccurredAt.Before(since) {
			return nil
		}
		seen[id] = true
		active[id] = true
		out = append(out, m)
		parents := []string{m.ParentID, m.RootID}
		used := map[string]bool{}
		for _, parent := range parents {
			if parent == "" || parent == id || used[parent] {
				continue
			}
			used[parent] = true
			p, err := e.c.Store.Message(ctx, t.Scope, parent)
			if errors.Is(err, domain.ErrNotFound) {
				_, receipt, bindingErr := e.c.Store.TaskForMessage(ctx, t.Scope, parent)
				if bindingErr == nil && receipt != nil {
					continue
				}
				if bindingErr != nil && !errors.Is(bindingErr, domain.ErrNotFound) {
					return bindingErr
				}
				if e.c.ResolveMessage != nil {
					if err = e.c.ResolveMessage(ctx, t.Scope, parent); err != nil {
						return err
					}
					p, err = e.c.Store.Message(ctx, t.Scope, parent)
				}
			}
			if errors.Is(err, domain.ErrNotFound) {
				return ErrNotReady
			}
			if err != nil {
				return err
			}
			if err = visit(p, depth+1); err != nil {
				return err
			}
		}
		delete(active, id)
		return nil
	}
	for _, ref := range t.Sources {
		m, err := e.c.Store.Message(ctx, t.Scope, ref.MessageID)
		if err != nil {
			return nil, err
		}
		if m.Source != ref || m.Deleted {
			return nil, domain.ErrStaleSource
		}
		if err = visit(m, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}
