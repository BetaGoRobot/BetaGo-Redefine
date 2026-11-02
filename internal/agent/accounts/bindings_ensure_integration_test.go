package accounts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestEnsureBindingReusesConfirmedFormAcrossTasksWithoutExtendingExpiry(t *testing.T) {
	f := setup(t)
	p := principal()
	b := issued(t, f.Store, p, "first-task", "private-form")
	again, err := f.EnsureBinding(context.Background(), p, "followup-task")
	if err != nil || again != b {
		t.Fatalf("follow-up must reuse exact private receipt and expiry: %+v %+v %v", b, again, err)
	}
}

func TestEnsureBindingScopesReuseToExactPrincipal(t *testing.T) {
	f := setup(t)
	p := principal()
	b := issued(t, f.Store, p, "first-task", "private-form")
	for _, change := range []func(*Principal){
		func(p *Principal) { p.ActorID = "another-actor" },
		func(p *Principal) { p.Scope.ChatID = "another-chat" },
		func(p *Principal) { p.Scope.Visibility = "private" },
		func(p *Principal) { p.Scope.TenantID = "another-tenant" },
		func(p *Principal) { p.Scope.AppID = "another-app" },
	} {
		other := p
		change(&other)
		next, err := f.EnsureBinding(context.Background(), other, "followup-task")
		if err != nil || next.ID == b.ID || next.ID == "" || next.MessageID != "" {
			t.Fatalf("private form crossed exact requester scope: %+v %v", next, err)
		}
	}
}

func TestEnsureBindingExcludesConsumedAndExpiredForms(t *testing.T) {
	for _, state := range []string{"consumed", "expired"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			p := principal()
			b := issued(t, f.Store, p, "first-task", "old-private-form")
			if state == "consumed" {
				if err := f.CompleteBinding(context.Background(), p, b.ID, b.MessageID, "", true); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.db.Exec(`UPDATE `+f.table("private_bindings")+` SET expires_at=clock_timestamp()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
					t.Fatal(err)
				}
			}
			// A repeated expired or consumed request cannot be repurposed.
			if _, err := f.EnsureBinding(context.Background(), p, "first-task"); !errors.Is(err, ErrBindingRejected) {
				t.Fatalf("used request key must remain rejected: %v", err)
			}
			next, err := f.EnsureBinding(context.Background(), p, "followup-task")
			if err != nil || next.ID == b.ID || next.ID == "" || next.MessageID != "" {
				t.Fatalf("inactive form was reused: %+v %v", next, err)
			}
		})
	}
}

func TestEnsureBindingUnconfirmedReservationBlocksAllTaskKeys(t *testing.T) {
	f := setup(t)
	p := principal()
	first, err := f.EnsureBinding(context.Background(), p, "first-task")
	if err != nil || first.ID == "" {
		t.Fatalf("cannot reserve first private form: %+v %v", first, err)
	}
	for _, key := range []string{"first-task", "other-task"} {
		if _, err := f.EnsureBinding(context.Background(), p, key); !errors.Is(err, domain.ErrEffectUnknown) {
			t.Fatalf("unconfirmed form allowed another send for %s: %v", key, err)
		}
	}
	if err := f.SetBindingMessage(context.Background(), p, first.ID, "actual-private-receipt"); err != nil {
		t.Fatal(err)
	}
	again, err := f.EnsureBinding(context.Background(), p, "third-task")
	if err != nil || again.ID != first.ID || again.MessageID != "actual-private-receipt" {
		t.Fatalf("confirmed reservation not reusable: %+v %v", again, err)
	}
}

func TestEnsureBindingConcurrentTasksReserveOnlyOneForm(t *testing.T) {
	f := setup(t)
	p := principal()
	type result struct {
		binding Binding
		err     error
	}
	results := make(chan result, 12)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			<-start
			b, err := f.EnsureBinding(context.Background(), p, fmt.Sprintf("task-%d", i))
			results <- result{b, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	reserved, unknown := 0, 0
	for r := range results {
		if r.err == nil && r.binding.ID != "" && r.binding.MessageID == "" {
			reserved++
		} else if errors.Is(r.err, domain.ErrEffectUnknown) {
			unknown++
		} else {
			t.Fatalf("unexpected reservation result: %+v %v", r.binding, r.err)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM ` + f.table("private_bindings")).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 || unknown != 11 || count != 1 {
		t.Fatalf("concurrent tasks created duplicate forms: reserved=%d unknown=%d rows=%d", reserved, unknown, count)
	}
}
