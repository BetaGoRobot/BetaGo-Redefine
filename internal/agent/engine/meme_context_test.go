package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

type memeSceneModel struct{ requests []modelapi.ChatRequest }

func (m *memeSceneModel) Generate(_ context.Context, request modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, request)
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"observe","messages":[]}`}}, nil
}

type memeScene struct {
	CurrentUtterance   capturedSceneMessage
	RecentConversation []capturedSceneMessage
	MemeCandidates     *struct {
		Untrusted bool
		Items     []memes.Candidate
	}
	Truncated bool
}

func TestMemeCandidatesReachNativeModelWithoutForcingDelivery(t *testing.T) {
	_, logs := observeEngine(t)
	f := newAutoEffectFixture(t, true, false)
	query := "private-current-marker 今天又加班到凌晨，打工人实在顶不住了"
	current := sceneAccept(t, f, "meme-current", "member", query, "", time.Now().UTC().Add(-time.Millisecond))
	var err error
	f.task, err = f.store.CreateTask(context.Background(), current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.job, err = f.store.ClaimJob(context.Background(), []domain.JobKind{domain.JobAgentTurn}, "meme-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	model := &memeSceneModel{}
	f.engine.c.Model, f.engine.c.ConversationOutput = model, true
	if err = f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("local retrieval caused %d model calls", len(model.requests))
	}
	var scene memeScene
	system := ""
	for _, message := range model.requests[0].Messages {
		content, _ := message.Content.(string)
		if message.Role == "user" {
			if err = json.Unmarshal([]byte(content), &scene); err != nil {
				t.Fatal(err)
			}
		}
		if message.Role == "system" {
			system += content
		}
	}
	if scene.CurrentUtterance.Text != query || scene.MemeCandidates == nil || !scene.MemeCandidates.Untrusted {
		t.Fatal("model scene lost the utterance or optional untrusted meme reference block")
	}
	found := false
	for _, item := range scene.MemeCandidates.Items {
		if item.ID == "chime:打工人" && strings.Contains(item.Meaning, "上班族") {
			found = true
		}
	}
	if !found || !strings.Contains(system, "MemeCandidates") || !strings.Contains(system, "可选参考") {
		t.Fatal("actual embedded reference and its optional-use instruction did not reach model")
	}
	if _, err = f.store.ClaimJob(context.Background(), []domain.JobKind{domain.JobDelivery}, "meme-delivery", time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("observe with available meme candidates queued an ordinary delivery: %v", err)
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.task.Scope, current.Source.MessageID)
	if err != nil || task.Status != "completed" {
		t.Fatalf("observe did not complete normally: status=%s err=%v", task.Status, err)
	}
	meta, _ := memes.Default()
	if !strings.Contains(logs.String(), `"msg":"agent.memes.loaded"`) || !strings.Contains(logs.String(), `"corpus_count":1477`) || !strings.Contains(logs.String(), meta.Metadata().SHA256) || !strings.Contains(logs.String(), `"candidate_count":`) {
		t.Fatal("safe corpus and candidate diagnostics were not recorded")
	}
	if !strings.Contains(logs.String(), "private-current-marker") {
		t.Fatal("business scene content missing")
	}
	for _, private := range []string{"library.json", "/mnt/"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("diagnostics exposed private or reference data: %q", private)
		}
	}
}

func TestMemeCandidatesAreTrimmedBeforeConversationOrCorrections(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	f.task.Request = "打工人，小丑竟是我自己，我释怀的笑了，阎王点名，骨灰薯片"
	sceneAccept(t, f, "important-conversation", "member", "important-human-context "+strings.Repeat("context ", 180), "", time.Now().UTC().Add(-time.Millisecond))
	entry := domain.CatalogEntry{Kind: domain.CatalogCorrectionKind, Name: "meme-correction", Revision: 1, Origin: f.task.Root, CatalogValue: domain.CatalogValue{Correction: &domain.CatalogCorrection{Text: "important-bound-correction"}}}
	body, _ := json.Marshal(entry)
	if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,creator,body,updated_at)VALUES($1,$2,$3,1,'admin',$4,clock_timestamp())`, f.task.Scope.Key(), entry.Kind, entry.Name, body); err != nil {
		t.Fatal(err)
	}
	full, refs, _, err := f.engine.prompt(context.Background(), f.task)
	if err != nil {
		t.Fatal(err)
	}
	var original memeScene
	if err = json.Unmarshal([]byte(full), &original); err != nil {
		t.Fatal(err)
	}
	if original.MemeCandidates == nil || len(original.MemeCandidates.Items) == 0 {
		t.Fatal("budget probe has no retrieved candidates")
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal([]byte(full), &envelope); err != nil {
		t.Fatal(err)
	}
	delete(envelope, "MemeCandidates")
	withoutCandidates, _ := json.Marshal(envelope)
	f.engine.c.MaxContextBytes = (len(withoutCandidates) + 32) * 2
	trimmed, trimmedRefs, _, err := f.engine.prompt(context.Background(), f.task)
	if err != nil {
		t.Fatal(err)
	}
	var got memeScene
	if err = json.Unmarshal([]byte(trimmed), &got); err != nil {
		t.Fatal(err)
	}
	if got.MemeCandidates != nil || !got.Truncated || len(got.RecentConversation) != len(original.RecentConversation) || len(trimmedRefs) != len(refs) || !strings.Contains(trimmed, "important-human-context") || !strings.Contains(trimmed, "important-bound-correction") {
		t.Fatal("optional candidates displaced core conversation or its source bindings")
	}
}
