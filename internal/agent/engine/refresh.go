package engine

import (
	"context"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// RefreshTask reconciles provider facts through an injected atomic reader. It
// never changes task authority; subsequent PG guards decide whether a turn,
// frozen effect or delivery still matches the current source revision.
func (e *Engine) RefreshTask(ctx context.Context, scope domain.Scope, id string) error {
	if e.c.RefreshSource == nil {
		return nil
	}
	t, err := e.c.Store.Task(ctx, scope, id)
	if err != nil {
		return err
	}
	refs := append(append([]domain.SourceRef(nil), t.Sources...), t.ContextSources...)
	unique := map[string]domain.SourceRef{}
	for _, ref := range refs {
		if ref.Scope != scope {
			return domain.ErrUnauthorized
		}
		unique[ref.Scope.Key()+":"+ref.MessageID] = ref
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan domain.SourceRef)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for i := 0; i < min(4, len(unique)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				if er := e.c.RefreshSource(ctx, ref); er != nil {
					once.Do(func() { first = er; cancel() })
				}
			}
		}()
	}
	for _, ref := range unique {
		select {
		case jobs <- ref:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	wg.Wait()
	return first
}
