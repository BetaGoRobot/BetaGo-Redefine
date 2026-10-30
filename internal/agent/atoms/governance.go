package atoms

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type GovernanceReader interface {
	CatalogRuntime(context.Context, domain.Scope) (domain.CatalogRuntime, error)
}

type governanceAccessReader interface {
	CatalogAccess(context.Context, domain.Scope) (domain.CatalogRuntime, error)
}
type governanceSnapshotKey struct{}
type governanceSnapshot struct {
	Scope domain.Scope
	State domain.CatalogRuntime
}

func accessSnapshot(ctx context.Context, g GovernanceReader, scope domain.Scope) (domain.CatalogRuntime, error) {
	if cached, ok := ctx.Value(governanceSnapshotKey{}).(governanceSnapshot); ok && cached.Scope == scope {
		return cached.State, nil
	}
	if reader, ok := g.(governanceAccessReader); ok {
		return reader.CatalogAccess(ctx, scope)
	}
	return g.CatalogRuntime(ctx, scope)
}

// AvailableDefinitions reuses one access snapshot only while advertising the
// tool list. Actual invocations receive the original context and recheck state.
func (r *Registry) AvailableDefinitions(ctx context.Context, in Invocation) ([]Definition, error) {
	if r.governance == nil {
		return r.Definitions(), nil
	}
	state, err := accessSnapshot(ctx, r.governance, in.Task.Scope)
	if err != nil {
		return nil, err
	}
	viewCtx := context.WithValue(ctx, governanceSnapshotKey{}, governanceSnapshot{Scope: in.Task.Scope, State: state})
	defs := []Definition{}
	for _, d := range r.Definitions() {
		if err = d.Authorize(viewCtx, in); err != nil {
			if err == domain.ErrUnauthorized {
				continue
			}
			return nil, err
		}
		defs = append(defs, d)
	}
	return defs, nil
}

func governedFeature(name string) string {
	switch {
	case strings.HasPrefix(name, "music_"):
		return "music"
	case strings.HasPrefix(name, "luckin_"):
		return "luckin"
	case strings.HasPrefix(name, "schedule_"), strings.HasPrefix(name, "reminder_"):
		return "reminders"
	case name == "web_search" || name == "read_webpage" || strings.HasPrefix(name, "research_"):
		return "web_search"
	case strings.HasPrefix(name, "finance_"), strings.HasPrefix(name, "gold_"), strings.HasPrefix(name, "stock_"), strings.HasPrefix(name, "economy_"), name == "oneword_get":
		return "public_data"
	}
	return ""
}
func managementTool(name string) bool {
	return strings.HasPrefix(name, "catalog_") || name == "permission_query" || name == "feature_list" || name == "current_time"
}

// Authorize runs before reserving an effect in the ledger. A denied operation
// has not started and must not become an uncertain external effect.
func (d Definition) Authorize(ctx context.Context, in Invocation) error {
	if d.authorize == nil {
		return nil
	}
	return d.authorize(ctx, in)
}
func governDefinition(d Definition, g GovernanceReader) Definition {
	if g == nil {
		return d
	}
	prior := d.authorize
	d.authorize = func(ctx context.Context, in Invocation) error {
		if in.Task.Scope.Validate() != nil {
			return domain.ErrUnauthorized
		}
		if prior != nil {
			if err := prior(ctx, in); err != nil {
				return err
			}
		}
		r, err := accessSnapshot(ctx, g, in.Task.Scope)
		if err != nil {
			return err
		}
		if managementTool(d.Name) {
			return nil
		}
		if d.SideEffect && r.Muted(time.Now().UTC()) {
			return domain.ErrUnauthorized
		}
		if feature := governedFeature(d.Name); feature != "" && !r.FeatureEnabled(feature) {
			return domain.ErrUnauthorized
		}
		return nil
	}
	run := d.Run
	d.Run = func(ctx context.Context, in Invocation, args string) (string, error) {
		if err := d.Authorize(ctx, in); err != nil {
			return "", err
		}
		raw, err := run(ctx, in, args)
		if err != nil {
			return "", err
		}
		refs, err := d.Sources(raw)
		if err != nil {
			return "", err
		}
		if len(refs) > 0 {
			r, e := accessSnapshot(ctx, g, in.Task.Scope)
			if e != nil {
				return "", e
			}
			for _, ref := range refs {
				if ref.Scope != in.Task.Scope || ref.OccurredAt.Before(r.HistorySince) {
					return "", domain.ErrStaleSource
				}
			}
		}
		return raw, nil
	}
	return d
}
