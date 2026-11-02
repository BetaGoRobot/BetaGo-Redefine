package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

type presentationRetriever struct{ result retrieval.Result }

func (r presentationRetriever) Retrieve(context.Context, domain.Query) (retrieval.Result, error) {
	return r.result, nil
}

type presentationModel struct {
	tool        string
	args        string
	requests    []modelapi.ChatRequest
	observation string
}

func (m *presentationModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, r)
	for _, message := range r.Messages {
		if message.Role == "tool" {
			m.observation, _ = message.Content.(string)
			return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "read"}}, nil
		}
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "read-evidence", Type: "function", Function: modelapi.ToolFunction{Name: m.tool, Arguments: m.arguments()}}}}}, nil
}

func (m *presentationModel) arguments() string {
	if m.args != "" {
		return m.args
	}
	return `{"query":"乙"}`
}

type capturedToolPresentation struct {
	SourceMessages []capturedSceneMessage
	Evidence       []struct {
		ID, DerivedText  string
		SourceMessageIDs []string
		Truncated        bool
	}
	Participants []struct{ MemberRef, ID, Name, Role, NameOrigin string }
	Truncated    bool
}

func TestNativeToolPresentationSeparatesEachOriginalFromDerivedAttribution(t *testing.T) {
	for _, name := range []string{"search_literal", "search_discussion", "search_memory"} {
		t.Run(name, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false)
			ctx := context.Background()
			now := time.Now().UTC().Add(-time.Minute)
			target := sceneAccept(t, f, "target-original", "member-b", "我在做课程", "", now)
			third := sceneAccept(t, f, "third-original", "member-c", "@_user_1 很会讲课", "", now.Add(time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "member-b", Name: "乙"})
			rawResult := retrieval.Result{Evidence: []domain.Evidence{{ID: "derived-1", Text: target.Text + "\n" + third.Text, DerivedText: "丙评价乙擅长讲课", Sources: []domain.SourceRef{target.Source, third.Source}}, {ID: "derived-2", Text: target.Text + "\n" + third.Text, DerivedText: "乙在做课程", Sources: []domain.SourceRef{target.Source, third.Source}}}}
			reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Retrieval: presentationRetriever{rawResult}})
			if err != nil {
				t.Fatal(err)
			}
			model := &presentationModel{tool: name}
			f.engine.c.Registry = reg
			f.engine.c.Model = model
			f.engine.c.GroupMembers = sceneMemberReader(func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
				return domain.GroupMembers{Members: []domain.GroupMember{{ID: "member-b", Name: "乙", IDType: "open_id"}, {ID: "member-c", Name: "丙", IDType: "open_id"}}}, nil
			})
			if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
				t.Fatal(err)
			}
			if err = f.execute(ctx); err != nil {
				t.Fatal(err)
			}
			var got capturedToolPresentation
			if err = json.Unmarshal([]byte(model.observation), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.SourceMessages) != 2 || len(got.Evidence) != 2 {
				t.Fatalf("model must receive deduplicated originals beside interpretations: sources=%d evidence=%d", len(got.SourceMessages), len(got.Evidence))
			}
			byID := map[string]capturedSceneMessage{}
			for _, m := range got.SourceMessages {
				byID[m.MessageID] = m
			}
			if byID[third.Source.MessageID].SpeakerName != "丙" || byID[third.Source.MessageID].Text != "@乙 很会讲课" || byID[target.Source.MessageID].SpeakerName != "乙" {
				t.Fatalf("third-party quote became target identity: %+v", byID)
			}
			for _, ev := range got.Evidence {
				if ev.DerivedText == "" || len(ev.SourceMessageIDs) != 2 {
					t.Fatalf("derived fact not related to exact original messages: %+v", ev)
				}
			}
			for _, key := range []string{`"Scope"`, `"Version"`, `"Sources"`, `"source_refs"`} {
				if strings.Contains(model.observation, key) {
					t.Fatalf("private proof in model tool view: %s", key)
				}
			}
			var ledger domain.ToolExecution
			var ledgerBody []byte
			if err = f.db.QueryRow(`SELECT body FROM "`+f.schema+`".tool_executions WHERE scope_key=$1 AND task_id=$2`, f.task.Scope.Key(), f.task.ID).Scan(&ledgerBody); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(ledgerBody, &ledger); err != nil {
				t.Fatal(err)
			}
			var kept retrieval.Result
			if json.Unmarshal([]byte(ledger.Result), &kept) != nil || len(kept.Evidence) != 2 || len(kept.Evidence[0].Sources) != 2 || kept.Evidence[0].Text != rawResult.Evidence[0].Text {
				t.Fatal("presentation replaced raw durable receipt")
			}
			pinned, err := f.store.Task(ctx, f.task.Scope, f.task.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []domain.SourceRef{target.Source, third.Source} {
				found := false
				for _, ref := range pinned.ContextSources {
					found = found || ref == want
				}
				if !found {
					t.Fatal("tool presentation discarded source authority")
				}
			}
		})
	}
}
func TestNativeToolPresentationHasIndependentTextAndMaterialBounds(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	result := retrieval.Result{}
	for i := 0; i < 70; i++ {
		m := sceneAccept(t, f, fmt.Sprintf("bounded-%02d", i), "author", strings.Repeat("原文", 1000), "", time.Now().UTC().Add(-time.Minute))
		result.Evidence = append(result.Evidence, domain.Evidence{ID: fmt.Sprint(i), Text: "original", DerivedText: strings.Repeat("归纳", 100), Sources: []domain.SourceRef{m.Source}})
	}
	reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Retrieval: presentationRetriever{result}})
	if err != nil {
		t.Fatal(err)
	}
	model := &presentationModel{tool: "search_memory"}
	f.engine.c.Registry = reg
	f.engine.c.Model = model
	if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
		t.Fatal(err)
	}
	if err = f.execute(ctx); err != nil {
		t.Fatal(err)
	}
	var got capturedToolPresentation
	if err = json.Unmarshal([]byte(model.observation), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.SourceMessages) > 64 || len(model.observation) > f.engine.c.MaxContextBytes/4 {
		t.Fatalf("unbounded/unmarked reading view: bytes=%d sources=%d truncated=%v", len(model.observation), len(got.SourceMessages), got.Truncated)
	}
	pinned, err := f.store.Task(ctx, f.task.Scope, f.task.ID)
	if err != nil || len(pinned.ContextSources) < 71 {
		t.Fatalf("display trim changed all raw source pins: %d %v", len(pinned.ContextSources), err)
	}
}

func TestNativeCommunityReadingViewUsesTheSameMessageAttribution(t *testing.T) {
	for _, name := range []string{"feishu_read_message", "feishu_message_history", "feishu_message_context"} {
		t.Run(name, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false)
			ctx := context.Background()
			now := time.Now().UTC().Add(-time.Minute)
			target := sceneAccept(t, f, "community-target", "member-b", "我在做课程", "", now)
			third := sceneAccept(t, f, "community-third", "member-c", "@_user_1 很会讲课", target.Source.MessageID, now.Add(time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "member-b", Name: "乙"})
			third.Source.Version++
			third.EventID = "community-media"
			third.UpdatedAt = time.Now().UTC().Add(-time.Millisecond)
			third.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "source-image-resource", Sequence: 3}}
			if _, err := f.store.Accept(ctx, domain.Event{ID: third.EventID, Type: domain.EventMessage, Message: third, OccurredAt: third.UpdatedAt}); err != nil {
				t.Fatal(err)
			}

			reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Group: communitySourceGroup{store: f.store}})
			if err != nil {
				t.Fatal(err)
			}
			args := `{"message_id":"community-third"}`
			if name == "feishu_message_history" {
				args = `{"limit":2}`
			}
			model := &presentationModel{tool: name, args: args}
			f.engine.c.Registry = reg
			f.engine.c.Model = model
			f.engine.c.GroupMembers = sceneMemberReader(func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
				return domain.GroupMembers{Members: []domain.GroupMember{{ID: "member-b", Name: "乙", IDType: "open_id"}, {ID: "member-c", Name: "丙", IDType: "open_id"}}}, nil
			})
			if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
				t.Fatal(err)
			}
			if err = f.execute(ctx); err != nil {
				t.Fatal(err)
			}
			var got capturedToolPresentation
			if err = json.Unmarshal([]byte(model.observation), &got); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range got.SourceMessages {
				if m.MessageID == third.Source.MessageID {
					found = true
					if m.SpeakerName != "丙" || m.Text != "@乙 很会讲课" || len(m.Mentions) != 1 {
						t.Fatalf("community original lost author/mention: %+v", m)
					}
				}
			}
			if !found {
				t.Fatalf("%s bypassed shared presenter", name)
			}
			var materialEnvelope struct {
				SourceMessages []struct {
					MessageID string
					Media     []struct {
						ResourceID string
						Sequence   int
					}
				}
			}
			if json.Unmarshal([]byte(model.observation), &materialEnvelope) != nil {
				t.Fatal("invalid material media")
			}
			mediaFound := false
			for _, m := range materialEnvelope.SourceMessages {
				if m.MessageID == third.Source.MessageID && len(m.Media) == 1 && m.Media[0].ResourceID == "source-image-resource" && m.Media[0].Sequence == 3 {
					mediaFound = true
				}
			}
			if !mediaFound {
				t.Fatal("message attribution dropped existing attachment locator")
			}

			for _, key := range []string{`"Scope"`, `"Version"`, `"source_refs"`, `"Sources"`} {
				if strings.Contains(model.observation, key) {
					t.Fatalf("community proof leaked: %s", key)
				}
			}
			if name == "feishu_message_history" {
				var envelope struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				if json.Unmarshal([]byte(model.observation), &envelope) != nil || len(envelope.Data["coverage"]) == 0 || len(envelope.Data["has_more"]) == 0 {
					t.Fatal("history pagination/coverage lost")
				}
			}
		})
	}
}

func TestToolPresentationAfterRawReceiptRejectsChangedSourcesAndCancellation(t *testing.T) {
	for _, change := range []string{"edit", "delete", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := sceneAccept(t, f, "receipt-source", "source-author", "original", "", time.Now().UTC().Add(-time.Minute))
			rawResult := retrieval.Result{Evidence: []domain.Evidence{{ID: "receipt-evidence", Text: source.Text, DerivedText: "source-derived", Sources: []domain.SourceRef{source.Source}}}}
			reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Retrieval: presentationRetriever{rawResult}})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.SyncToolPolicies(ctx, reg.Policies()); err != nil {
				t.Fatal(err)
			}
			task, err := f.store.StartTurn(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
			if err != nil {
				t.Fatal(err)
			}
			var definition atoms.Definition
			for _, d := range reg.Definitions() {
				if d.Name == "search_memory" {
					definition = d
				}
			}
			bound := boundTool{definition: definition, store: f.store, job: f.job, task: task, present: func(callCtx context.Context, scope domain.Scope, name, raw string) (string, error) {
				if change == "cancel" {
					cancel()
				} else {
					source.Source.Version++
					source.EventID = "receipt-source-changed"
					source.UpdatedAt = time.Now().UTC()
					source.Text = "edited"
					kind := domain.EventMessage
					if change == "delete" {
						source.Deleted = true
						kind = domain.EventDelete
					}
					if _, err := f.store.Accept(context.Background(), domain.Event{ID: source.EventID, Type: kind, Message: source, OccurredAt: source.UpdatedAt}); err != nil {
						t.Fatal(err)
					}
				}
				return f.engine.presentTool(callCtx, scope, name, raw)
			}}
			result, err := bound.InvokableRun(ctx, `{"query":"original"}`)
			want := domain.ErrStaleSource
			if change == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || result != "" {
				t.Fatalf("post-receipt guard published changed evidence: result=%q error=%v", result, err)
			}
			var body []byte
			if err = f.db.QueryRow(`SELECT body FROM "`+f.schema+`".tool_executions WHERE task_id=$1`, task.ID).Scan(&body); err != nil {
				t.Fatal(err)
			}
			var receipt domain.ToolExecution
			if json.Unmarshal(body, &receipt) != nil || receipt.Status != "completed" {
				t.Fatal("presentation rewrote completed raw ledger")
			}
			var kept retrieval.Result
			if json.Unmarshal([]byte(receipt.Result), &kept) != nil || kept.Evidence[0].Sources[0] != rawResult.Evidence[0].Sources[0] {
				t.Fatal("presentation replaced immutable raw sources")
			}
		})
	}
}
