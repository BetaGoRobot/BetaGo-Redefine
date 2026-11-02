package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func identityFixture(t *testing.T) memes.EncodedWiki {
	t.Helper()
	response, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
	var encoded memes.EncodedWiki
	if err := json.Unmarshal(response.JSON, &encoded); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func runIdentitySync(t *testing.T, encoded memes.EncodedWiki) (SyncReport, *fakePublisher, error) {
	t.Helper()
	raw, _ := json.Marshal(encoded)
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
		return memes.CompletionResponse{JSON: raw}, nil
	})
	report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}, Complete: true}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	return report, publisher, err
}

func TestProgramIdentityKeepsDifferentUsesDespiteDuplicateOrInvalidModelIDs(t *testing.T) {
	encoded := identityFixture(t)
	one := encoded.Cards[0]
	one.Full.UseID = "MODEL ID\nINVALID"
	one.Full.FamilyID = "INVALID FAMILY"
	two := one
	two.Full.Name = "不同用法"
	two.Full.InputRelation = "另一种完整关系"
	two.Full.ResponseAct = "另一种回应"
	two.Projection.Relations = []memes.Relation{{Kind: "contrast", Pattern: "另一种完整关系"}}
	encoded.Cards = []memes.EncodedUse{one, two}
	report, publisher, err := runIdentitySync(t, encoded)
	if err != nil || report.CardCount != 2 || publisher.calls != 1 {
		t.Fatalf("technical IDs rejected valid semantics: %+v %v", report, err)
	}
	a, b := publisher.records[0], publisher.records[1]
	if a.UseID == b.UseID || a.Full.FamilyID != b.Full.FamilyID || !strings.HasPrefix(a.UseID, a.Full.FamilyID+"_") || a.UseID != strings.ToLower(a.UseID) {
		t.Fatalf("program identity invalid: %s %s families=%s/%s", a.UseID, b.UseID, a.Full.FamilyID, b.Full.FamilyID)
	}
}

func TestProgramIdentityDeduplicatesAndIgnoresModelMetadata(t *testing.T) {
	encoded := identityFixture(t)
	one := encoded.Cards[0]
	two := one
	two.Full.UseID = "ANOTHER_TECH_ID"
	two.Full.FamilyID = "ANOTHER_FAMILY"
	two.Full.ReviewStatus = "another status"
	two.Full.Description = "ignored generated description"
	two.Full.Sources = []memes.SourceMetadata{}
	two.Projection.CardFingerprint = "ignored fingerprint"
	encoded.Cards = []memes.EncodedUse{one, two}
	report, publisher, err := runIdentitySync(t, encoded)
	if err != nil || report.CardCount != 1 {
		t.Fatalf("identical semantics were not deduplicated: %+v %v", report, err)
	}
	first := publisher.records[0].UseID
	one.Full.UseID = ""
	one.Full.FamilyID = ""
	one.Full.ReviewStatus = "different"
	one.Full.Description = "different"
	one.Projection.CardFingerprint = "different"
	encoded.Cards = []memes.EncodedUse{one}
	_, second, err := runIdentitySync(t, encoded)
	if err != nil || second.records[0].UseID != first {
		t.Fatalf("nonsemantic model fields changed stable identity: %v", err)
	}
}

func TestProgramIdentityStillRejectsMissingSemanticCoreFields(t *testing.T) {
	for _, field := range []string{"name", "input_relation", "response_act"} {
		t.Run(field, func(t *testing.T) {
			encoded := identityFixture(t)
			switch field {
			case "name":
				encoded.Cards[0].Full.Name = ""
			case "input_relation":
				encoded.Cards[0].Full.InputRelation = ""
			case "response_act":
				encoded.Cards[0].Full.ResponseAct = ""
			}
			report, publisher, err := runIdentitySync(t, encoded)
			if !errors.Is(err, ErrIncomplete) || report.Active || publisher.calls != 0 {
				t.Fatalf("missing semantics bypassed validation: %+v %v", report, err)
			}
		})
	}
}
