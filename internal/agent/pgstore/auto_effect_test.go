package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func automaticPolicy(t *testing.T, name string) pgstore.ToolPolicy {
	t.Helper()
	var p pgstore.ToolPolicy
	if err := json.Unmarshal([]byte(`{"Name":"`+name+`","SideEffect":true,"AutoExecute":true,"Digest":"auto-v1"}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAutomaticPolicyMigrationUpgradesExistingTableWithConservativeDefault(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`DROP TABLE "` + f.schema + `".tool_policies; CREATE TABLE "` + f.schema + `".tool_policies(tool_name text PRIMARY KEY,side_effect boolean NOT NULL,enabled boolean NOT NULL DEFAULT true,policy_digest text NOT NULL); INSERT INTO "` + f.schema + `".tool_policies VALUES('legacy',true,false,'v1')`); err != nil {
		t.Fatal(err)
	}
	if err := f.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var auto, enabled bool
	if err := f.db.QueryRow(`SELECT auto_execute,enabled FROM "`+f.schema+`".tool_policies WHERE tool_name='legacy'`).Scan(&auto, &enabled); err != nil {
		t.Fatal(err)
	}
	if auto || enabled {
		t.Fatal("migration auto-granted/re-enabled a legacy policy")
	}
	var nullable, defaultValue string
	if err := f.db.QueryRow(`SELECT is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='tool_policies' AND column_name='auto_execute'`, f.schema).Scan(&nullable, &defaultValue); err != nil || nullable != "NO" || defaultValue != "false" {
		t.Fatalf("unsafe migration default: nullable=%s default=%s error=%v", nullable, defaultValue, err)
	}
}

func TestAutomaticBeginUsesStoredGrantAndCannotFreezeApproval(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := event("auto", 1)
	accept(t, f, root)
	task, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "auto", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := automaticPolicy(t, "ordinary_action")
	actor := task.LastActorID
	task.LastActorID = "untrusted-caller-actor"
	if err = f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{p, {Name: "high_effect", SideEffect: true, Digest: "high-v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.FreezeApproval(ctx, j, task, p.Name, `{"amount":1}`, p.Digest); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("automatic-only policy froze approval: %v", err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, "high_effect", `{"amount":1}`, "", false, "high-v1"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("caller read flag waived stored side effect: %v", err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, "high_effect", `{"amount":1}`, "", true, "high-v1"); err == nil {
		t.Fatal("high effect executed without approval")
	}
	x, reused, err := f.BeginTool(ctx, j, task, p.Name, `{"amount":1}`, "", true, p.Digest)
	if err != nil || reused || x.Status != "running_effect" || x.ActorID != actor || x.ApprovalID != "" {
		t.Fatalf("stored automatic grant not used: %+v %v %v", x, reused, err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, p.Name, `{"amount":1}`, "", true, p.Digest); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatalf("running auto action replayed: %v", err)
	}
	if err = f.FinishTool(ctx, j, task, x, `{"receipt":"ok"}`, nil); err != nil {
		t.Fatal(err)
	}
	x, reused, err = f.BeginTool(ctx, j, task, p.Name, `{"amount":1}`, "", true, p.Digest)
	if err != nil || !reused || x.Result != `{"receipt":"ok"}` {
		t.Fatalf("completed receipt not reused: %+v %v %v", x, reused, err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".tool_policies SET enabled=false WHERE tool_name=$1`, p.Name); err != nil {
		t.Fatal(err)
	}
	if err = f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{p}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, p.Name, `{"amount":1}`, "", true, p.Digest); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revocation bypassed via completed reuse or sync: %v", err)
	}
}
