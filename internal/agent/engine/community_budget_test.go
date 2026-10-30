package engine

import (
	"context"
	"encoding/json"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"strings"
	"testing"
	"time"
)

func TestCommunityDenseMediaHistoryFitsNativeReceipt(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	task, err := f.store.StartTurn(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id := "dense-" + string(rune('a'+i))
		now := time.Now().UTC().Add(-time.Second)
		m := domain.Message{Source: domain.SourceRef{Scope: task.Scope, MessageID: id, AuthorID: "actual-author", Version: 1, OccurredAt: now}, EventID: id, UpdatedAt: now}
		for j := 0; j < 1000; j++ {
			m.Media = append(m.Media, domain.Media{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "image_" + strings.Repeat("x", 43), Sequence: j})
		}
		if _, err = f.store.Accept(ctx, domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Group: communitySourceGroup{store: f.store}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
		t.Fatal(err)
	}
	var history atoms.Definition
	for _, d := range reg.Definitions() {
		if d.Name == "feishu_message_history" {
			history = d
		}
	}
	raw, err := history.Run(ctx, atoms.Invocation{Task: task, Job: f.job}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encoded history bytes=%d, receipt cap=%d", len(raw), 256<<10)
	b := boundTool{definition: history, store: f.store, job: f.job, task: task}
	if _, err = b.InvokableRun(ctx, `{}`); err != nil {
		t.Fatalf("valid media history aborted native tool run instead of bounded observation: %v", err)
	}
}

func TestCommunityHistoryReplySourceBudgetFitsNativeTurn(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	task, err := f.store.StartTurn(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	refs := []domain.SourceRef{}
	for i := 0; i < 256; i++ {
		id := "dep_" + domain.StableID(string(rune(i)))[:16]
		at := time.Now().UTC().Add(-2 * time.Minute)
		m := domain.Message{Source: domain.SourceRef{Scope: task.Scope, MessageID: id, AuthorID: "actual-author", Version: 1, OccurredAt: at}, EventID: id, Text: "material", UpdatedAt: at}
		if _, err = f.store.Accept(ctx, domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, m.Source)
	}
	d := domain.Delivery{ID: "past-delivery", TaskID: "past-task", Scope: task.Scope, Revision: 1, Kind: "final", MessageType: "text", Content: `{"text":"source-backed reply"}`, Status: "delivered", MessageID: "past-reply", Sources: refs, UpdatedAt: time.Now().UTC().Add(-time.Millisecond)}
	body, _ := json.Marshal(d)
	if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".deliveries(scope_key,delivery_id,task_id,revision,message_id,status,body) VALUES($1,$2,$3,$4,$5,$6,$7)`, task.Scope.Key(), d.ID, d.TaskID, d.Revision, d.MessageID, d.Status, body); err != nil {
		t.Fatal(err)
	}
	reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Group: communitySourceGroup{store: f.store}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
		t.Fatal(err)
	}
	var history atoms.Definition
	for _, v := range reg.Definitions() {
		if v.Name == "feishu_message_history" {
			history = v
		}
	}
	raw, err := history.Run(ctx, atoms.Invocation{Task: task, Job: f.job}, `{"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := history.Sources(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encoded bytes=%d unique material references=%d receipt byte cap=%d turn ref cap=256", len(raw), len(got), 256<<10)
	b := boundTool{definition: history, store: f.store, job: f.job, task: task}
	if _, err = b.InvokableRun(ctx, `{"limit":1}`); err != nil {
		t.Fatalf("source-backed reply sample exceeded native source budget: %v", err)
	}
	var sample struct {
		Data struct {
			Replies   []json.RawMessage `json:"own_replies"`
			More      bool              `json:"own_replies_more"`
			Truncated bool              `json:"own_replies_truncated"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &sample) != nil || len(sample.Data.Replies) != 0 || !sample.Data.More || !sample.Data.Truncated {
		t.Fatal("unrepresentable source proof was not skipped as a whole")
	}
	// An earlier call in this same native turn can fill the PG context while
	// boundTool still holds the old Task value. Two small dependencies fit the
	// byte cap but must be checked against this current source ledger.
	full := append([]domain.SourceRef{task.Root}, refs[:255]...)
	if _, err = f.store.SetTurnContext(ctx, f.job, task, full, nil); err != nil {
		t.Fatal(err)
	}
	d.Sources = []domain.SourceRef{refs[0], refs[255]}
	body, _ = json.Marshal(d)
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".deliveries SET body=$1 WHERE delivery_id=$2`, body, d.ID); err != nil {
		t.Fatal(err)
	}
	recorder, _ := observeEngine(t)
	raw, err = b.InvokableRun(ctx, `{"limit":1}`)
	if err != nil {
		t.Fatalf("earlier native reads exhausted source slots but stale Task was used: %v", err)
	}
	if json.Unmarshal([]byte(raw), &sample) != nil || len(sample.Data.Replies) != 0 || !sample.Data.More || !sample.Data.Truncated {
		t.Fatal("cumulative source budget omitted its explicit whole-reply truncation")
	}
	span := recordedSpan(t, recorder, "agent.tool.invoke")
	if spanValue(span, "context_source_count").AsInt64() != 256 {
		t.Fatalf("telemetry used stale tool snapshot instead of current source ledger: %v", span.Attributes())
	}
}
