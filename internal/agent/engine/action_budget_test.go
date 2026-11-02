package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestRecentActionReceiptsCannotStarveSmallConversationScene(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	f.engine.c.MaxContextBytes = 8192
	ctx := context.Background()
	for i := range 8 {
		at := time.Now().UTC().Add(-time.Duration(i+1) * time.Second)
		id := strings.Repeat("a", 1000) + fmt.Sprint(i)
		m := domain.Message{Source: domain.SourceRef{Scope: f.task.Scope, MessageID: id, AuthorID: f.task.LastActorID, Version: 1, OccurredAt: at}, EventID: id, Text: "发卡", UpdatedAt: at, Raw: json.RawMessage(`{"kind":"message","sender_type":"user"}`)}
		if _, err := f.store.Accept(ctx, domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
			t.Fatal(err)
		}
		task, err := f.store.CreateTask(ctx, m, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		x := domain.ToolExecution{Key: id, TaskID: task.ID, Revision: task.Revision, Scope: task.Scope, ActorID: f.task.LastActorID, Tool: "ordinary_action", Status: "completed", CreatedAt: at, UpdatedAt: at, Result: `{"message_id":"receipt","visibility":"current_actor_only"}`}
		body, _ := json.Marshal(x)
		if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".tool_executions(scope_key,task_id,execution_key,status,body)VALUES($1,$2,$3,'completed',$4)`, task.Scope.Key(), task.ID, id, body); err != nil {
			t.Fatal(err)
		}
	}
	raw, refs, _, err := f.engine.prompt(ctx, f.task)
	if err != nil || len(raw) > 4096 {
		t.Fatalf("actions starved scene: bytes=%d err=%v", len(raw), err)
	}
	var scene struct {
		RecentActions []pgstore.ActionReceipt
		Truncated     bool
	}
	if err = json.Unmarshal([]byte(raw), &scene); err != nil {
		t.Fatal(err)
	}
	if !scene.Truncated || len(scene.RecentActions) == 8 {
		t.Fatal("old action history was not trimmed")
	}
	kept := map[string]bool{}
	for _, r := range scene.RecentActions {
		kept[r.OriginMessageID] = true
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref.MessageID, strings.Repeat("a", 1000)) && !kept[ref.MessageID] {
			t.Fatal("trimmed action still pins its source")
		}
	}
}
