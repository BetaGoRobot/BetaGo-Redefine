package handlers

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

func masterKnowledgeResult(t *testing.T, dialogue []memes.Turn) memes.ReferenceResult {
	t.Helper()
	card := memes.UseCard{UseID: "sea", FamilyID: "sea", Name: "海岛奇兵", Description: "资料解释退守孤岛却宣称反攻的历史影射。", ReviewStatus: "pending", InputRelation: "退守与反攻", Requirements: []memes.Requirement{}, ResponseAct: "讽刺反差", Sources: []memes.SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵"}}}
	fp, _ := memes.Fingerprint(card)
	record, err := memes.CompileCard(card, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "contrast", Pattern: "处境和宣言的反差"}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := memes.CompileKnowledgeSnapshot(memes.KnowledgeBinding{Scope: "wiki", Generation: "g", CorpusHash: "c", ProjectionHash: "p"}, []memes.IndexRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	r, sources, err := memes.ValidateRequest(memes.Request{RequestID: "r", Dialogue: dialogue})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := memes.SelectKnowledge(r, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	c := memes.GroundedKnowledgeContext(selection)
	c.BindKnowledgeSnapshot(snapshot.Fingerprint)
	for _, source := range sources {
		c.SourceIDs = append(c.SourceIDs, source.SourceID)
	}
	c.Interpretations = []memes.GroundedHypothesis{{ID: "hypothesis_" + strings.Repeat("b", 64), Meaning: "可能为历史影射", SourceIDs: []int{sources[0].SourceID}}}
	return memes.ReferenceResult{Status: memes.ReferenceStatusError, Sources: sources, ContextV2: c, Reason: memes.ReasonTimeout}
}

func TestMemeKnowledgeSurvivesMasterProtocolRecoveryAndV1StaysIndependent(t *testing.T) {
	event := referenceEvent()
	raw := `{"text":"海岛奇兵，损一句。"}`
	event.Event.Message.Content = &raw
	installReferenceProvider(t, func(_ context.Context, _, _ string, dialogue []memes.Turn) memes.ReferenceResult {
		return masterKnowledgeResult(t, dialogue)
	})
	calls := 0
	executor := referenceExecutor{do: func(_ context.Context, system, user string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		calls++
		if !strings.Contains(user, "反攻的历史影射") || !strings.Contains(system, memereference.KnowledgeInstruction) {
			t.Fatal("master model or recovery lost validated priors")
		}
		if calls == 1 {
			return referenceStream(`{"reply":`)
		}
		return referenceStream("地图缩水，口号倒是满级了。")
	}}
	seq, err := executeTwoPhaseReferenceGenerator(context.Background(), event, StandardChatPlan{SystemPrompt: "base", UserPrompt: "human"}, llmusage.Scope{}, executor)
	if err != nil {
		t.Fatal(err)
	}
	for data := range seq {
		if !strings.Contains(data.Content, "地图缩水") {
			t.Fatalf("recovery reply lost: %+v", data)
		}
	}
	if calls != 2 {
		t.Fatalf("expected one ordinary recovery: %d", calls)
	}
	ConfigureMemeReferences(func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
		return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "v1", Name: "v1"}}}
	})
	plan := withMemeReferencePlan(context.Background(), event, StandardChatPlan{SystemPrompt: "base", UserPrompt: "human"})
	if strings.Contains(plan.SystemPrompt, memereference.KnowledgeInstruction) {
		t.Fatal("V2 behavior activated in V1")
	}
}

func TestMemeKnowledgeMasterRecoveryChecksWithdrawalAndBlocksBareIDs(t *testing.T) {
	event := referenceEvent()
	raw := `{"text":"海岛奇兵，损一句。"}`
	event.Event.Message.Content = &raw
	withdrawn, checks := false, 0
	ConfigureMemeReferenceRuntime(func(_ context.Context, _, _ string, dialogue []memes.Turn) memes.ReferenceResult {
		return masterKnowledgeResult(t, dialogue)
	}, func(_ context.Context, _ string, c *memes.GroundedContext, cards []memes.UseCard) (*memes.GroundedContext, []memes.UseCard, error) {
		checks++
		if withdrawn {
			return nil, nil, nil
		}
		return c, cards, nil
	})
	t.Cleanup(func() { ConfigureMemeReferences(nil) })
	plan := withMemeReferencePlan(context.Background(), event, StandardChatPlan{SystemPrompt: "base", UserPrompt: "human"})
	for _, id := range plan.memeReferences.identifiers {
		if safeMemeStateReply("内部"+id, plan.memeReferences) {
			t.Fatalf("bare internal ID allowed: %s", id)
		}
	}
	withdrawn = true
	system, user := recoverMemeReferenceContext(context.Background(), plan.memeReferences)
	if checks != 2 || strings.Contains(system, memereference.KnowledgeInstruction) || strings.Contains(user, "反攻的历史影射") {
		t.Fatal("withdrawn knowledge reused in ordinary recovery")
	}
}
