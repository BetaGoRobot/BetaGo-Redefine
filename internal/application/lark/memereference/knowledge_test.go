package memereference

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func priorResult(t *testing.T) memes.ReferenceResult {
	t.Helper()
	b := memes.KnowledgeBinding{Scope: "wiki", Generation: "g", CorpusHash: "c", ProjectionHash: "p"}
	card := memes.UseCard{UseID: "sea", FamilyID: "sea", Name: "海岛奇兵", Description: "来源说明退守与反攻的影射。", ReviewStatus: "pending", InputRelation: "退守却仍喊反攻", Requirements: []memes.Requirement{}, ResponseAct: "讽刺", Sources: []memes.SourceMetadata{{Kind: "wiki", Scope: "wiki", Title: "海岛奇兵", URL: "https://example.org/sea"}}}
	fp, _ := memes.Fingerprint(card)
	record, err := memes.CompileCard(card, memes.Projection{CardFingerprint: fp, Relations: []memes.Relation{{Kind: "contrast", Pattern: "处境与宣言的反差"}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := memes.CompileKnowledgeSnapshot(b, []memes.IndexRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	r, sources, err := memes.ValidateRequest(memes.Request{RequestID: "r", Dialogue: []memes.Turn{{TurnID: "t", Speaker: "human", Text: "海岛奇兵"}}})
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
	c.Interpretations = []memes.GroundedHypothesis{{ID: "hypothesis_" + strings.Repeat("a", 64), Meaning: "可能是历史影射", SourceIDs: []int{sources[0].SourceID}}}
	return memes.ReferenceResult{Status: memes.ReferenceStatusError, Reason: "private diagnostic", Sources: sources, ContextV2: c}
}

func TestSuppliedHypothesisIdentifierIsPrivateWithoutBanningOrdinaryModelLabels(t *testing.T) {
	r := priorResult(t)
	ids := GroundedIdentifiers(r.ContextV2)
	if !ContainsSuppliedIdentifier("用了"+r.ContextV2.Interpretations[0].ID, ids) {
		t.Fatal("hypothesis reference leaked")
	}
	if ContainsSuppliedIdentifier("HTML h1 是标题元素", ids) {
		t.Fatal("opaque refs turned ordinary technical terms into a ban")
	}
}

func TestVerifiedV2EnvironmentRetainsExpressionCapabilityWithoutKnowledgeHits(t *testing.T) {
	r := priorResult(t)
	r.ContextV2.PriorKnowledge = nil
	r.ContextV2.Observations = nil
	r.ContextV2.Interpretations = nil
	r.ContextV2.Stages.Knowledge = "no_anchor"
	if PromptContext(r) == nil {
		t.Fatal("no lexical hit disabled V2 expression fallback")
	}
	r.ContextV2.Stages.Knowledge = "policy_unavailable"
	if PromptContext(r) != nil {
		t.Fatal("policy failure supplied an unverified environment")
	}
}

func TestPriorKnowledgeSurvivesEmptyOrFailedExpansion(t *testing.T) {
	for _, status := range []memes.ReferenceStatus{memes.ReferenceStatusEmpty, memes.ReferenceStatusError, memes.ReferenceStatusUnresolved} {
		r := priorResult(t)
		r.Status = status
		block := BuildContextBlock(r)
		if !strings.Contains(block, "退守与反攻") || strings.Contains(block, "private diagnostic") {
			t.Fatalf("lost or polluted prior: %s", block)
		}
		if BuildBlock(r) != "" {
			t.Fatal("failed expansion exposed cards")
		}
		r.ContextV2.PriorKnowledge[0].Binding.Generation = "wrong"
		if BuildContextBlock(r) != "" {
			t.Fatal("mixed generation context injected")
		}
	}
}

func TestGroundedFormatterRejectsDanglingReferencesAndInvalidFingerprints(t *testing.T) {
	for _, mutate := range []func(*memes.GroundedContext){
		func(c *memes.GroundedContext) { c.Observations[0].EntryIDs = []string{"invented"} },
		func(c *memes.GroundedContext) { c.PriorKnowledge[0].EntryFingerprint = "" },
		func(c *memes.GroundedContext) {
			c.Interpretations = []memes.GroundedHypothesis{{ID: "h", SourceIDs: []int{999}}}
		},
		func(c *memes.GroundedContext) {
			c.Interpretations = []memes.GroundedHypothesis{{ID: "h", AnchorIDs: []string{"invented"}}}
		},
		func(c *memes.GroundedContext) { c.Observations[0].Text = "改写原文" },
	} {
		r := priorResult(t)
		mutate(r.ContextV2)
		if PromptContext(r) != nil {
			raw, _ := json.Marshal(r.ContextV2)
			t.Fatalf("malformed references injected %s", raw)
		}
	}
}
