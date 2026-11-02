package memereference

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func TestBuildBlockUsesFullCardsWithoutRetrievalDiagnostics(t *testing.T) {
	result := memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "梗名称", Description: "Wiki 原文解释。", Template: "原始表达", Sources: []memes.SourceMetadata{{Kind: "wiki", URL: "https://wiki.example/one"}}}}, Reason: "do not inject this diagnostic"}
	result.Retrieval.Fused = []memes.RankedHit{{UseID: "wiki.one", Score: 999, Rank: 1}}
	block := BuildBlock(result)
	for _, want := range []string{"重要梗用法参考", "Wiki 原文解释。", "原始表达", "https://wiki.example/one"} {
		if !strings.Contains(block, want) {
			t.Errorf("missing %q in %s", want, block)
		}
	}
	for _, forbidden := range []string{`"score"`, `"rank"`, "999", "do not inject this diagnostic"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("diagnostic exposed: %q", forbidden)
		}
	}
}

func TestBuildBlockBoundsAndDeduplicatesWholeCards(t *testing.T) {
	result := memes.ReferenceResult{Status: memes.ReferenceStatusReady}
	for i := range 7 {
		card := memes.UseCard{UseID: fmt.Sprintf("card%d", i), Name: fmt.Sprintf("name%d", i), Description: "完整解释。"}
		result.Cards = append(result.Cards, card, card)
	}
	block := BuildBlock(result)
	if strings.Count(block, `"use_id"`) != 5 || strings.Count(block, "name0") != 1 || strings.Contains(block, "name5") {
		t.Fatalf("invalid reference budget: %s", block)
	}
}

func TestBuildBlockUnavailableLeavesNoInstructions(t *testing.T) {
	for _, status := range []memes.ReferenceStatus{memes.ReferenceStatusEmpty, memes.ReferenceStatusError, memes.ReferenceStatusUnresolved, ""} {
		if got := BuildBlock(memes.ReferenceResult{Status: status, Cards: []memes.UseCard{{UseID: "unused", Description: "ignore"}}}); got != "" {
			t.Errorf("status=%s injected=%s", status, got)
		}
	}
	if got := BuildBlock(memes.ReferenceResult{Status: memes.ReferenceStatusReady}); got != "" {
		t.Fatal("empty cards produced block")
	}
	for _, want := range []string{"重要", "核心", "改造", "强行", "事实"} {
		if !strings.Contains(Instruction, want) {
			t.Errorf("instruction missing %q", want)
		}
	}
}

func TestReferenceSelectionOnlyAcceptsSuppliedIDs(t *testing.T) {
	cards := []memes.UseCard{{UseID: "wiki.one", Name: "第一个"}, {UseID: "wiki.two", Name: "第二个"}}
	accepted, rejected := FilterReportedSelection(cards, []string{"wiki.two", "unseen", "wiki.two"})
	if len(accepted) != 1 || accepted[0] != "wiki.two" || len(rejected) != 1 || rejected[0] != "unseen" {
		t.Fatalf("selection=%v rejected=%v", accepted, rejected)
	}
	if len(PromptCards(memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: cards})) != 2 {
		t.Fatal("prompt card view missing")
	}
	for _, want := range []string{"候选列表", "检索过程", "meme_use_ids"} {
		if !strings.Contains(Instruction, want) {
			t.Errorf("shared non-disclosure instruction missing %q", want)
		}
	}
}
