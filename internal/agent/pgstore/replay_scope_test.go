package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func replayFixture(t *testing.T) (fixture, domain.Task, domain.Job, []pgstore.ToolPolicy) {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	root := event("replay-scope-root", 1)
	accept(t, f, root)
	task, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "replay-scope-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	policies := []pgstore.ToolPolicy{
		{Name: "personal_delivery", SideEffect: true, AutoExecute: true, Digest: "personal-delivery-v1"},
		{Name: "ordinary_effect", SideEffect: true, AutoExecute: true, Digest: "ordinary-effect-v1"},
	}
	if err := f.SyncToolPolicies(ctx, policies); err != nil {
		t.Fatal(err)
	}
	return f, task, j, policies
}

// Only updates this fixture's persisted authoritative task. A caller's copied
// Task value must never select another actor's receipt or execution namespace.
func saveReplayTask(t *testing.T, f fixture, task domain.Task) {
	t.Helper()
	body, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".tasks SET revision=$1,body=$2 WHERE scope_key=$3 AND task_id=$4`, task.Revision, body, task.Scope.Key(), task.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPerActorTurnScopeReusesOnlySameAuthenticatedActorAndRevision(t *testing.T) {
	f, task, j, policies := replayFixture(t)
	ctx := context.Background()
	p, ordinary := policies[0], policies[1]
	first, reused, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
	if err != nil || reused || first.ActorID != task.LastActorID {
		t.Fatal("actor turn effect was not reserved", err)
	}
	if _, _, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatal("inflight same-request effect replayed", err)
	}
	if err := f.FinishTool(ctx, j, task, first, `{"message_id":"personal-first"}`, nil); err != nil {
		t.Fatal(err)
	}
	baseline, _, err := f.BeginTool(ctx, j, task, ordinary.Name, `{}`, "", true, ordinary.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.FinishTool(ctx, j, task, baseline, `{"message_id":"task-wide"}`, nil); err != nil {
		t.Fatal(err)
	}
	forged := task
	forged.LastActorID = "forged-model-actor"
	again, reused, err := f.BeginToolForActorTurn(ctx, j, forged, p.Name, `{}`, "", true, p.Digest)
	if err != nil || !reused || again.Key != first.Key || again.ActorID != task.LastActorID || again.Result != firstResult() {
		t.Fatal("caller changed authenticated actor/replay identity", err)
	}
	task.LastActorID = "second-authenticated-actor"
	saveReplayTask(t, f, task)
	second, reused, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
	if err != nil || reused || second.Key == first.Key || second.ActorID != task.LastActorID {
		t.Fatal("later authenticated actor reused another actor's private receipt", err)
	}
	if err := f.FinishTool(ctx, j, task, second, `{"message_id":"personal-second"}`, nil); err != nil {
		t.Fatal(err)
	}
	task.Revision++
	saveReplayTask(t, f, task)
	third, reused, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
	if err != nil || reused || third.Key == second.Key || third.Key == first.Key {
		t.Fatal("later revision reused consumed private form", err)
	}
	if err := f.FinishTool(ctx, j, task, third, `{"message_id":"personal-third"}`, nil); err != nil {
		t.Fatal(err)
	}
	again, reused, err = f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
	if err != nil || !reused || again.Key != third.Key {
		t.Fatal("same revised request did not reuse receipt", err)
	}
	unchanged, reused, err := f.BeginTool(ctx, j, task, ordinary.Name, `{}`, "", true, ordinary.Digest)
	if err != nil || !reused || unchanged.Key != baseline.Key || unchanged.ActorID != baseline.ActorID {
		t.Fatal("existing effects lost task-wide replay protection", err)
	}
}

func firstResult() string { return `{"message_id":"personal-first"}` }

func TestPerActorTurnUnknownEffectsStillBlockLaterActorsAndRevisions(t *testing.T) {
	f, task, j, policies := replayFixture(t)
	ctx := context.Background()
	p := policies[0]
	x, _, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.FinishTool(ctx, j, task, x, "", domain.ErrEffectUnknown); err != nil {
		t.Fatal(err)
	}
	task.LastActorID = "later-actor"
	task.Revision++
	saveReplayTask(t, f, task)
	if _, _, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatal("new actor/revision bypassed uncertain effect barrier", err)
	}
	if _, _, err := f.BeginTool(ctx, j, task, policies[1].Name, `{}`, "", true, policies[1].Digest); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatal("ordinary effect bypassed uncertain private delivery", err)
	}
}

func TestPerActorTurnPreservesPolicyFenceAndSourceGuards(t *testing.T) {
	for _, guard := range []string{"policy", "fence", "source"} {
		t.Run(guard, func(t *testing.T) {
			f, task, j, policies := replayFixture(t)
			ctx := context.Background()
			p := policies[0]
			x, _, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.FinishTool(ctx, j, task, x, `{"message_id":"private"}`, nil); err != nil {
				t.Fatal(err)
			}
			want := domain.ErrUnauthorized
			switch guard {
			case "policy":
				if _, err := f.db.Exec(`UPDATE "`+f.schema+`".tool_policies SET enabled=false WHERE tool_name=$1`, p.Name); err != nil {
					t.Fatal(err)
				}
			case "fence":
				j.Fence++
				want = domain.ErrStaleClaim
			case "source":
				changed := event("edited-replay-source", 2)
				accept(t, f, changed)
				want = domain.ErrStaleSource
			}
			if _, _, err := f.BeginToolForActorTurn(ctx, j, task, p.Name, `{}`, "", true, p.Digest); !errors.Is(err, want) {
				t.Fatal("completed receipt bypassed live guard", guard, err)
			}
		})
	}
}

func TestPerActorTurnCannotClassifyReadAsEffect(t *testing.T) {
	f, task, j, policies := replayFixture(t)
	if _, _, err := f.BeginToolForActorTurn(context.Background(), j, task, policies[0].Name, `{}`, "", false, policies[0].Digest); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("per-actor-turn read accepted", err)
	}
}

func TestPerActorTurnRegistryFlagBindsGeneratedAndExplicitPolicyDigests(t *testing.T) {
	for _, explicit := range []string{"", "adapter-fingerprint"} {
		fingerprint := func(flag, effect bool) (string, error) {
			r, err := atoms.New(atoms.Options{Extensions: []atoms.Definition{{Name: "private_delivery", Digest: explicit, SideEffect: effect, AutoExecute: effect, PerActorTurn: flag, Run: func(context.Context, atoms.Invocation, string) (string, error) { return `{}`, nil }}}})
			if err != nil {
				return "", err
			}
			for _, d := range r.Definitions() {
				if d.Name == "private_delivery" {
					return d.Digest, nil
				}
			}
			return "", domain.ErrNotFound
		}
		ordinary, err := fingerprint(false, true)
		if err != nil {
			t.Fatal(err)
		}
		personal, err := fingerprint(true, true)
		if err != nil || personal == ordinary {
			t.Fatal("policy digest does not bind replay scope", err)
		}
		if _, err := fingerprint(true, false); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("registry accepted scoped read", err)
		}
	}
}
