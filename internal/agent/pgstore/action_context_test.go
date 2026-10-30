package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestRecentActionReceiptsUseVerifiedActorScopeAndWhitelistedResults(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	anchor := time.Now().UTC()
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "binding", SideEffect: true, Digest: "v1"}, {Name: "read", Digest: "v1"}}); err != nil {
		t.Fatal(err)
	}
	add := func(id, actor, tool, status string, age time.Duration, foreign bool) {
		t.Helper()
		at := anchor.Add(-age)
		e := event(id, 1)
		e.Message.Source.MessageID, e.Message.Source.AuthorID = id, actor
		e.Message.Source.OccurredAt, e.Message.UpdatedAt, e.OccurredAt = at, at, at
		if foreign {
			e.Message.Source.Scope.ChatID = "foreign"
		}
		accept(t, f, e)
		task, err := f.CreateTask(ctx, e.Message, anchor.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		x := domain.ToolExecution{Key: id, TaskID: task.ID, Revision: task.Revision, Scope: task.Scope, Tool: tool, ActorID: actor, Status: status, CreatedAt: at, UpdatedAt: at,
			Result: `{"message_id":"private-receipt","account_status":"bound","visibility":"current_actor_only","delivery_location":"current_chat","token":"secret","binding_url":"https://secret.invalid","data":{"password":"secret"}}`}
		if status == "unknown" {
			x.Result = `{"message_id":"unconfirmed","status":"sent"}`
		}
		body, _ := json.Marshal(x)
		if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body) VALUES($1,$2,$3,$4,$5)`, task.Scope.Key(), task.ID, id, status, body); err != nil {
			t.Fatal(err)
		}
	}
	add("confirmed", "alice", "binding", "completed", time.Second, false)
	add("unknown", "alice", "binding", "unknown", 2*time.Second, false)
	add("another-actor", "bob", "binding", "completed", time.Second, false)
	add("another-chat", "alice", "binding", "completed", time.Second, true)
	add("old", "alice", "binding", "completed", 16*time.Minute, false)
	add("future", "alice", "binding", "completed", -time.Second, false)
	add("read", "alice", "read", "completed", time.Second, false)
	add("running", "alice", "binding", "running_effect", time.Second, false)
	got, err := f.RecentActionReceipts(ctx, testScope(), "alice", anchor, 8)
	if err != nil || len(got) != 2 {
		t.Fatalf("incorrect receipt coverage: %+v %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"secret", "binding_url", "password", "another-actor", "another-chat", "unconfirmed"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("private/unconfirmed data escaped: %s", forbidden)
		}
	}
	if got[0].OriginMessageID != "confirmed" || got[0].Status != "completed" || got[0].Result["message_id"] != "private-receipt" || got[0].Result["delivery_location"] != "current_chat" || got[1].Status != "unknown" || len(got[1].Result) != 0 {
		t.Fatalf("receipt meaning changed: %+v", got)
	}
	for _, r := range got {
		if r.Source.Scope != testScope() || r.Source.AuthorID != "alice" {
			t.Fatal("source proof absent")
		}
	}
	edit := event("confirmed-edit", 2)
	edit.Message.Source.MessageID, edit.Message.Source.AuthorID = "confirmed", "alice"
	edit.Message.Source.OccurredAt = anchor.Add(-time.Second)
	edit.Message.UpdatedAt, edit.OccurredAt = anchor.Add(-time.Second/2), anchor.Add(-time.Second/2)
	accept(t, f, edit)
	got, err = f.RecentActionReceipts(ctx, testScope(), "alice", anchor, 8)
	if err != nil || len(got) != 1 || got[0].Status != "unknown" {
		t.Fatalf("edited root relabeled historical action: %+v %v", got, err)
	}
	dep := continuityHuman(t, f, "action-dependency", "alice", "message", anchor.Add(-3*time.Second))
	refs, _ := json.Marshal([]domain.SourceRef{dep.Source})
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".tasks SET body=convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{ContextSources}',$1::jsonb)::text,'UTF8') WHERE root_message_id='unknown'`, string(refs)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".messages SET deleted=true,body=convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{Deleted}','true'::jsonb)::text,'UTF8') WHERE message_id='action-dependency'`); err != nil {
		t.Fatal(err)
	}
	got, err = f.RecentActionReceipts(ctx, testScope(), "alice", anchor, 8)
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted dependency escaped action validation: %+v %v", got, err)
	}
	seedHistoryBarrier(t, f, anchor.Add(-time.Second/2))
	got, err = f.RecentActionReceipts(ctx, testScope(), "alice", anchor, 8)
	if err != nil || len(got) != 0 {
		t.Fatalf("history barrier bypassed: %+v %v", got, err)
	}
}

func TestRecentActionReceiptsBoundAndValidateInput(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	anchor := time.Now().UTC()
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "write", SideEffect: true, Digest: "v1"}}); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		id := fmt.Sprintf("action-%02d", i)
		root := continuityHuman(t, f, id, "alice", "message", anchor.Add(-time.Duration(i+1)*time.Second))
		task, err := f.CreateTask(ctx, root, anchor.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		x := domain.ToolExecution{Key: id, TaskID: task.ID, Revision: task.Revision, Scope: task.Scope, ActorID: "alice", Tool: "write", Status: "completed", CreatedAt: root.Source.OccurredAt, UpdatedAt: root.Source.OccurredAt, Result: `{"created":true}`}
		body, _ := json.Marshal(x)
		if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body) VALUES($1,$2,$3,'completed',$4)`, task.Scope.Key(), task.ID, id, body); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.RecentActionReceipts(ctx, testScope(), "alice", anchor, 3)
	if err != nil || len(got) != 3 || got[0].OriginMessageID != "action-00" || got[2].OriginMessageID != "action-02" {
		t.Fatalf("unbounded/unordered history: %+v %v", got, err)
	}
	for _, limit := range []int{0, 9} {
		if _, err = f.RecentActionReceipts(ctx, testScope(), "alice", anchor, limit); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err = f.RecentActionReceipts(ctx, testScope(), "", anchor, 3); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("empty actor accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.RecentActionReceipts(canceled, testScope(), "alice", anchor, 3); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
