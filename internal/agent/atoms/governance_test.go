package atoms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type governanceFixture struct {
	reads int
	state domain.CatalogRuntime
	err   error
}

func (g *governanceFixture) CatalogRuntime(context.Context, domain.Scope) (domain.CatalogRuntime, error) {
	g.reads++
	return g.state, g.err
}

func TestAdvertisedToolSnapshotDoesNotCacheSubsequentExecutionAuthority(t *testing.T) {
	g := &governanceFixture{state: domain.CatalogRuntime{Features: map[string]bool{"music": true}}}
	r, err := New(Options{Governance: g, Extensions: []Definition{{Name: "music_lookup", Run: func(context.Context, Invocation, string) (string, error) { return `{}`, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	in := Invocation{Task: domain.Task{Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}}}
	defs, err := r.AvailableDefinitions(context.Background(), in)
	if err != nil || g.reads != 1 {
		t.Fatalf("advertisement repeatedly fetched state: %d %v", g.reads, err)
	}
	g.state.Features["music"] = false
	for _, d := range defs {
		if d.Name == "music_lookup" {
			if _, err = d.Run(context.Background(), in, `{}`); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatal("cached advertisement allowed revoked execution")
			}
			return
		}
	}
	t.Fatal("configured lookup absent")
}

func TestGroupGovernanceChecksEffectiveStateBeforeEachToolInvocation(t *testing.T) {
	g := &governanceFixture{state: domain.CatalogRuntime{Features: map[string]bool{"music": false}}}
	calls := 0
	r, err := New(Options{Governance: g, Extensions: []Definition{{Name: "music_effect", SideEffect: true, AutoExecute: true, Run: func(context.Context, Invocation, string) (string, error) { calls++; return `{}`, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	var d Definition
	for _, x := range r.Definitions() {
		if x.Name == "music_effect" {
			d = x
		}
	}
	in := Invocation{Task: domain.Task{Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}}}
	if err = d.Authorize(context.Background(), in); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("disabled feature authorized: %v", err)
	}
	if _, err = d.Run(context.Background(), in, `{}`); !errors.Is(err, domain.ErrUnauthorized) || calls != 0 {
		t.Fatalf("disabled effect ran: %v calls=%d", err, calls)
	}
	g.state.Features["music"] = true
	if _, err = d.Run(context.Background(), in, `{}`); err != nil || calls != 1 {
		t.Fatalf("enabled effect missing: %v", err)
	}
	g.state.MuteUntil = time.Now().Add(time.Hour)
	if err = d.Authorize(context.Background(), in); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("muted group allowed side effect")
	}
	g.err = errors.New("private database failure")
	if _, err = d.Run(context.Background(), in, `{}`); err == nil || calls != 1 {
		t.Fatal("governance failure opened access")
	}
}
