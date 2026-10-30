package engine

import (
	"context"
	"encoding/json"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	knowledge "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func nativeKnowledgeResult(t *testing.T, dialogue []knowledge.Turn) knowledge.ReferenceResult {
	t.Helper()
	card := knowledge.UseCard{UseID: "sea", FamilyID: "sea", Name: "海岛奇兵", Description: "资料解释退守孤岛却宣称反攻的历史影射。", ReviewStatus: "pending", InputRelation: "退守与反攻", Requirements: []knowledge.Requirement{}, ResponseAct: "讽刺反差", Sources: []knowledge.SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵"}}}
	fp, _ := knowledge.Fingerprint(card)
	record, err := knowledge.CompileCard(card, knowledge.Projection{CardFingerprint: fp, Relations: []knowledge.Relation{{Kind: "contrast", Pattern: "处境和宣言的反差"}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := knowledge.CompileKnowledgeSnapshot(knowledge.KnowledgeBinding{Scope: "wiki", Generation: "g", CorpusHash: "c", ProjectionHash: "p"}, []knowledge.IndexRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	r, sources, err := knowledge.ValidateRequest(knowledge.Request{RequestID: "r", Dialogue: dialogue})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := knowledge.SelectKnowledge(r, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	c := knowledge.GroundedKnowledgeContext(selection)
	c.BindKnowledgeSnapshot(snapshot.Fingerprint)
	for _, source := range sources {
		c.SourceIDs = append(c.SourceIDs, source.SourceID)
	}
	c.Interpretations = []knowledge.GroundedHypothesis{{ID: "hypothesis_" + strings.Repeat("b", 64), Meaning: "可能为历史影射", SourceIDs: []int{sources[0].SourceID}}}
	return knowledge.ReferenceResult{Status: knowledge.ReferenceStatusError, Sources: sources, ContextV2: c, Reason: knowledge.ReasonTimeout}
}

func TestMemeKnowledgePriorOnlyReachesNativeModelAndRestoredScene(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	current := sceneAccept(t, f, "knowledge-current", "member-a", "三年从钢铁雄心玩成海岛奇兵，损一句。", "", time.Now().UTC().Add(-time.Second))
	var err error
	f.task, err = f.store.CreateTask(context.Background(), current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.engine.c.MemeReferences = func(_ context.Context, _, _ string, dialogue []knowledge.Turn) knowledge.ReferenceResult {
		return nativeKnowledgeResult(t, dialogue)
	}
	scene, _, _, err := f.engine.prompt(context.Background(), f.task)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		MemeKnowledge  *knowledge.GroundedContext
		MemeReferences []knowledge.UseCard
	}
	if json.Unmarshal([]byte(scene), &parsed) != nil || parsed.MemeKnowledge == nil || len(parsed.MemeKnowledge.PriorKnowledge) != 1 || len(parsed.MemeReferences) != 0 || !strings.Contains(scene, "反攻的历史影射") {
		t.Fatal("native environment lost prior-only knowledge")
	}
	model := &memeSceneModel{}
	wrapper := &memeReferenceChat{client: model, currentMessageID: current.Source.MessageID}
	request := modelapi.ChatRequest{Messages: []modelapi.ChatMessage{{Role: "system", Content: "base"}, {Role: "user", Content: scene}}}
	if _, err = wrapper.Generate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 || !strings.Contains(model.requests[0].Messages[0].Content.(string), memereference.KnowledgeInstruction) {
		t.Fatal("restored V2 scene missing expression instruction")
	}
	if request.Messages[0].Content != "base" {
		t.Fatal("wrapper mutated persisted request")
	}
	for _, id := range memereference.GroundedIdentifiers(parsed.MemeKnowledge) {
		if wrapper.safeKnowledgePlan(domain.ConversationPlan{Summary: "内部" + id}) {
			t.Fatalf("bare internal ID allowed: %s", id)
		}
	}
	checks := 0
	wrapper.revalidate = func(_ context.Context, _ string, c *knowledge.GroundedContext, _ []knowledge.UseCard) (*knowledge.GroundedContext, []knowledge.UseCard, error) {
		checks++
		clean := *c
		clean.PriorKnowledge = nil
		clean.Interpretations = nil
		return &clean, nil, nil
	}
	if _, err = wrapper.Generate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || strings.Contains(model.requests[1].Messages[1].Content.(string), "反攻的历史影射") {
		t.Fatal("withdrawn saved context reached model")
	}
	wrapper.revalidate = nil
	request.Messages[1].Content = `{"CurrentUtterance":{"MessageID":"knowledge-current"},"MemeReferences":[]}`
	if _, err = wrapper.Generate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(model.requests[2].Messages[0].Content.(string), memereference.KnowledgeInstruction) {
		t.Fatal("V2 instruction leaked into V1 scene")
	}
}
