package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func TestRequirementTechnicalIDsPreserveValidIdentityAndConditionLogic(t *testing.T) {
	encoded := identityFixture(t)
	encoded.Cards[0].Full.Requirements = []memes.Requirement{{ID: "Valid.ID", AnyOf: []string{"A", "B"}}, {ID: "condition_2", AnyOf: []string{"C"}}}
	before := encoded.Cards[0]
	before.Full.UseID, before.Full.FamilyID, before.Full.Description, before.Full.ReviewStatus = "", "", "", ""
	before.Full.Sources = nil
	before.Projection.CardFingerprint = ""
	digest, _ := memes.Fingerprint(before)
	sourceHash, _ := memes.Fingerprint(testSource().ID)
	records, err := compileContentIdentities(testSource(), encoded)
	if err != nil || records[0].UseID != "wiki_"+sourceHash[:12]+"_"+digest[:16] || !reflect.DeepEqual(records[0].Full.Requirements, encoded.Cards[0].Full.Requirements) {
		t.Fatalf("valid v2 identity or requirement logic changed: %+v %v", records, err)
	}
	encoded.Cards[0].Full.Requirements = []memes.Requirement{{ID: "非法", AnyOf: []string{"A", "B"}}, {ID: "condition_1", AnyOf: []string{"C"}}, {ID: "repeat", AnyOf: []string{"D"}}, {ID: "repeat", AnyOf: []string{"E"}}}
	original, _ := json.Marshal(encoded)
	records, err = compileContentIdentities(testSource(), encoded)
	if err != nil {
		t.Fatalf("opaque requirement IDs rejected: %v", err)
	}
	seen := map[string]bool{}
	for i, requirement := range records[0].Full.Requirements {
		if requirement.ID == "" || seen[requirement.ID] || !reflect.DeepEqual(requirement.AnyOf, encoded.Cards[0].Full.Requirements[i].AnyOf) {
			t.Fatalf("lost requirement AND/OR logic: %+v", records[0].Full.Requirements)
		}
		if i == 1 && requirement.ID != "condition_1" {
			t.Fatal("rewrote a legal unique ID")
		}
		if i != 1 && !strings.HasPrefix(requirement.ID, "condition_") {
			t.Fatalf("invalid replacement: %q", requirement.ID)
		}
		seen[requirement.ID] = true
	}
	after, _ := json.Marshal(encoded)
	if string(original) != string(after) {
		t.Fatal("canonicalization mutated the model draft")
	}
	again, err := compileContentIdentities(testSource(), encoded)
	if err != nil || again[0].UseID != records[0].UseID {
		t.Fatal("requirement normalization is not stable")
	}
}

func formatCases() []struct {
	name, diagnostic string
	mutate           func(*memes.EncodedWiki)
} {
	return []struct {
		name, diagnostic string
		mutate           func(*memes.EncodedWiki)
	}{
		{"no cards", "$.cards: expected 1..8 uses, got 0", func(e *memes.EncodedWiki) { e.Cards = []memes.EncodedUse{} }},
		{"too many cards", "$.cards: expected 1..8 uses, got 9", func(e *memes.EncodedWiki) {
			one := e.Cards[0]
			e.Cards = make([]memes.EncodedUse, 9)
			for i := range e.Cards {
				e.Cards[i] = one
			}
		}},
		{"empty any_of", "$.cards[0].full.requirements[0].any_of: expected at least 1 alternative, got 0", func(e *memes.EncodedWiki) {
			e.Cards[0].Full.Requirements = []memes.Requirement{{ID: "valid", AnyOf: []string{}}}
		}},
		{"empty condition branch", "$.cards[0].full.requirements[0].any_of[0]: must be nonempty", func(e *memes.EncodedWiki) {
			e.Cards[0].Full.Requirements = []memes.Requirement{{ID: "valid", AnyOf: []string{" "}}}
		}},
		{"relation kind", "$.cards[0].projection.relations[0].kind: outside allowed values", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Relations[0].Kind = "private-source-output-secret" }},
		{"empty relation pattern", "$.cards[0].projection.relations[0].pattern: must be nonempty", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Relations[0].Pattern = " " }},
		{"long relation pattern", "$.cards[0].projection.relations[0].pattern: expected at most 1024 runes, got 1025", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Relations[0].Pattern = strings.Repeat("中", 1025) }},
		{"no relations", "$.cards[0].projection.relations: expected at least 1 relation, got 0", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Relations = []memes.Relation{} }},
		{"empty subject", "$.cards[0].projection.emotion_chain[0].subject: must be nonempty", func(e *memes.EncodedWiki) {
			e.Cards[0].Projection.EmotionChain = []memes.EmotionChain{{Subject: " ", States: []string{"joy"}}}
		}},
		{"no states", "$.cards[0].projection.emotion_chain[0].states: expected 1..8 states, got 0", func(e *memes.EncodedWiki) {
			e.Cards[0].Projection.EmotionChain = []memes.EmotionChain{{Subject: "同一主体", States: []string{}}}
		}},
		{"too many states", "$.cards[0].projection.emotion_chain[0].states: expected 1..8 states, got 9", func(e *memes.EncodedWiki) {
			e.Cards[0].Projection.EmotionChain = []memes.EmotionChain{{Subject: "同一主体", States: strings.Fields(strings.Repeat("joy ", 9))}}
		}},
		{"emotion enum", "$.cards[0].projection.emotion_chain[0].states[0]: outside allowed values", func(e *memes.EncodedWiki) {
			e.Cards[0].Projection.EmotionChain = []memes.EmotionChain{{Subject: "同一主体", States: []string{"private-source-output-secret"}}}
		}},
		{"empty alias group", "$.cards[0].projection.keys.required_alias_groups[0]: expected at least 1 alias, got 0", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Keys.RequiredAliasGroups = [][]string{{}} }},
		{"empty alias", "$.cards[0].projection.keys.required_alias_groups[0][0]: must be nonempty", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Keys.RequiredAliasGroups = [][]string{{" "}} }},
		{"empty related key", "$.cards[0].projection.keys.related[0]: must be nonempty", func(e *memes.EncodedWiki) { e.Cards[0].Projection.Keys.Related = []string{" "} }},
	}
}

func TestSyncFormatRepairUsesOnlyRemainingCallAndReportsExactSafeConstraint(t *testing.T) {
	for _, test := range formatCases() {
		t.Run(test.name, func(t *testing.T) {
			valid := identityFixture(t)
			rawValid, _ := json.Marshal(valid)
			test.mutate(&valid)
			invalid, _ := json.Marshal(valid)
			for _, repairSucceeds := range []bool{false, true} {
				calls := []memes.CompletionRequest{}
				store := newMemoryStore()
				publisher := &fakePublisher{store: store, verified: true}
				model := completionFunc(func(_ context.Context, request memes.CompletionRequest) (memes.CompletionResponse, error) {
					calls = append(calls, request)
					if repairSucceeds && len(calls) == 2 {
						return memes.CompletionResponse{JSON: rawValid}, nil
					}
					return memes.CompletionResponse{JSON: invalid}, nil
				})
				report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}, Complete: true}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
				if len(calls) != 2 || calls[0].Repair || !calls[1].Repair || string(calls[0].Schema) != string(calls[1].Schema) || calls[1].Stage != memes.StageEncode {
					t.Fatalf("format repair did not use same bounded stage: calls=%d report=%+v err=%v", len(calls), report, err)
				}
				var repair struct {
					OriginalInput  json.RawMessage `json:"original_input"`
					InvalidOutput  string          `json:"invalid_output"`
					StructureError string          `json:"structure_error"`
				}
				if json.Unmarshal(calls[1].InputJSON, &repair) != nil || string(repair.OriginalInput) != string(calls[0].InputJSON) || repair.InvalidOutput != string(invalid) || !strings.Contains(repair.StructureError, test.diagnostic) {
					t.Fatalf("repair changed source or lost exact format diagnosis: %+v", repair)
				}
				if repairSucceeds {
					if err != nil || !report.Active || publisher.calls != 1 {
						t.Fatalf("valid format repair failed: %+v %v", report, err)
					}
				} else {
					if !errors.Is(err, ErrIncomplete) || report.Active || publisher.calls != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, test.diagnostic) || strings.Contains(report.Errors[0].Error, "private-source-output-secret") {
						t.Fatalf("unsafe/non-specific invalid format accepted: %+v %v", report, err)
					}
				}
			}
		})
	}
}

func TestSemanticFailureTakesPrecedenceOverRepairableFormatInAnyCard(t *testing.T) {
	for _, failure := range []string{"core", "relation evidence", "emotion evidence", "source"} {
		t.Run(failure, func(t *testing.T) {
			encoded := identityFixture(t)
			other := identityFixture(t).Cards[0]
			encoded.Cards[0].Projection.Relations[0].Kind = "invalid"
			encoded.Cards = append(encoded.Cards, other)
			source := testSource()
			switch failure {
			case "core":
				encoded.Cards[1].Full.InputRelation = ""
			case "relation evidence":
				encoded.Cards[1].Projection.Relations[0].Evidence = []memes.Evidence{{SourceID: 9, State: memes.Asserted}}
			case "emotion evidence":
				encoded.Cards[1].Projection.EmotionChain = []memes.EmotionChain{{Subject: "同一主体", States: []string{"joy"}, Evidence: []memes.Evidence{{SourceID: 9, State: memes.Asserted}}}}
			case "source":
				source.URL = ""
				source.Revision = ""
				source.Revision, _ = memes.Fingerprint(source)
			}
			raw, _ := json.Marshal(encoded)
			calls := 0
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
				calls++
				return memes.CompletionResponse{JSON: raw}, nil
			})
			report, err := Sync(context.Background(), Catalog{Sources: []Source{source}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
			if !errors.Is(err, ErrIncomplete) || calls != 1 || publisher.calls != 0 || report.Active {
				t.Fatalf("semantic failure received format repair: %+v %v calls=%d", report, err, calls)
			}
		})
	}
}

func TestExplicitEmptyCoreIsNotRepairedAlongsideMissingStructure(t *testing.T) {
	for _, field := range []string{"name", "input_relation", "response_act"} {
		t.Run(field, func(t *testing.T) {
			encoded := identityFixture(t)
			raw, _ := json.Marshal(encoded)
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			full := object["cards"].([]any)[0].(map[string]any)["full"].(map[string]any)
			delete(full, "requirements")
			full[field] = " "
			raw, _ = json.Marshal(object)
			calls := 0
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
				calls++
				return memes.CompletionResponse{JSON: raw}, nil
			})
			report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
			if !errors.Is(err, ErrIncomplete) || calls != 1 || publisher.calls != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, "card missing semantic field: "+field) {
				t.Fatalf("explicit empty core consumed repair alongside structure failure: %+v %v calls=%d", report, err, calls)
			}
		})
	}
}

func TestJSONThenLiteralFormatFailureSharesOneRepairBudget(t *testing.T) {
	encoded := identityFixture(t)
	encoded.Cards[0].Projection.EmotionChain = []memes.EmotionChain{{Subject: "同一主体", States: []string{"invalid"}}}
	second, _ := json.Marshal(encoded)
	calls := 0
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
		calls++
		if calls == 1 {
			return memes.CompletionResponse{JSON: json.RawMessage(`{"cards":`)}, nil
		}
		return memes.CompletionResponse{JSON: second}, nil
	})
	report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if !errors.Is(err, ErrIncomplete) || calls != 2 || publisher.calls != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, "emotion_chain[0].states[0]") {
		t.Fatalf("mixed format errors exceeded shared repair budget: %+v %v calls=%d", report, err, calls)
	}
}

func TestMalformedCardCannotHideAnotherCardsExplicitEmptyCore(t *testing.T) {
	raw := json.RawMessage(`{"cards":[{"full":7},{"full":{"name":"x","input_relation":" ","response_act":"x"}}]}`)
	calls := 0
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
		calls++
		return memes.CompletionResponse{JSON: raw}, nil
	})
	report, err := Sync(context.Background(), Catalog{Sources: []Source{testSource()}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if !errors.Is(err, ErrIncomplete) || calls != 1 || publisher.calls != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, "card missing semantic field: input_relation") {
		t.Fatalf("malformed card hid another card's explicit semantic failure: %+v %v calls=%d", report, err, calls)
	}
}
