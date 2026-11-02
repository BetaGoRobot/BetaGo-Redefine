package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestNextTurnReceivesActualActionReceiptWithoutPrivateProviderData(t *testing.T) {
	f := newConversationFixture(t, 131072, true)
	ctx := context.Background()
	first := f.message(t, "binding-request", "发绑定卡", "", true)
	f.model.answer = `{"choice":"reply","messages":[{"text":"卡片在本群，仅你可见。","reply_to_message_id":"binding-request"}]}`
	f.step(t, domain.JobAgentTurn)
	f.step(t, domain.JobDelivery)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, first.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "luckin_account_bind", Digest: "fixture", SideEffect: true, AutoExecute: true}}); err != nil {
		t.Fatal(err)
	}
	x := domain.ToolExecution{Key: "confirmed-binding", TaskID: task.ID, Revision: task.Revision, Scope: f.scope, ActorID: "member", Tool: "luckin_account_bind", Status: "completed", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Result: `{"message_id":"actual-private-card","account_status":"unbound","visibility":"current_actor_only","binding_url":"secret-url","token":"secret-credential"}`}
	body, _ := json.Marshal(x)
	if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body)VALUES($1,$2,$3,'completed',$4)`, f.scope.Key(), task.ID, x.Key, body); err != nil {
		t.Fatal(err)
	}
	f.message(t, "why-binding", "你刚才发了吗？", "", true)
	f.model.answer = `{"choice":"reply","messages":[{"text":"发了，在本群里仅你可见。","reply_to_message_id":"why-binding"}]}`
	f.step(t, domain.JobAgentTurn)
	var scene struct{ RecentActions []pgstore.ActionReceipt }
	for _, msg := range f.model.requests[len(f.model.requests)-1].Messages {
		if msg.Role != "user" {
			continue
		}
		raw, _ := msg.Content.(string)
		if strings.Contains(raw, "secret-url") || strings.Contains(raw, "secret-credential") {
			t.Fatal("private provider data entered next prompt")
		}
		if err = json.Unmarshal([]byte(raw), &scene); err != nil {
			t.Fatal(err)
		}
	}
	if len(scene.RecentActions) != 1 || scene.RecentActions[0].OriginMessageID != first.Source.MessageID || scene.RecentActions[0].Result["message_id"] != "actual-private-card" {
		t.Fatalf("action continuity absent: %+v", scene.RecentActions)
	}
	last, _, err := f.store.TaskForMessage(ctx, f.scope, "why-binding")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range last.ContextSources {
		found = found || ref == first.Source
	}
	if !found {
		t.Fatal("action trigger was not retained as invalidatable backend source proof")
	}
}
