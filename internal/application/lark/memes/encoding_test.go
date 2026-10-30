package memes

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

const completeEncodedWikiJSON = `{"cards":[{"full":{"use_id":"contrast.use","family_id":"contrast","name":"反差","review_status":"pending","input_relation":"A预期与实际相反","requirements":[],"applicability_notes":"","response_act":"吐槽反差","roles":{},"stance":{"voice":"bot_comment","reply_target":"conversation","evaluation_target":"conversation"},"template":"真有你的","anchors":[],"slots":{},"examples":{"positive":[],"near_miss":[]}},"projection":{"relations":[{"kind":"contrast","pattern":"A预期与实际相反"}],"emotion_chain":[],"keys":{"required_alias_groups":[],"related":[]}}}]}`

func TestEncodedWikiRejectsIncompleteModelOutputBeforeCompilation(t *testing.T) {
	raw := []byte(`{"cards":[{"full":{"use_id":"x","family_id":"x","name":"x","input_relation":"A预期与实际相反","response_act":"吐槽反差","template":"真有你的","stance":{"voice":"bot_comment","reply_target":"conversation","evaluation_target":"conversation"}},"projection":{"relations":[{"kind":"contrast","pattern":"A预期与实际相反"}]}}]}`)
	var encoded EncodedWiki
	if err := DecodeStrict(raw, &encoded); err == nil {
		source := WikiSource{ID: "wiki:test", Title: "反差", URL: "https://example.com/wiki/contrast", Description: "原始Wiki定义"}
		_, compileErr := CompileWikiEncoding(source, encoded)
		t.Fatalf("incomplete model structure reached compilation: compile error=%v", compileErr)
	}
}

func TestEncodedWikiRejectsMissingOrNullRequiredContainers(t *testing.T) {
	paths := []string{
		"cards", "cards.0.full", "cards.0.full.use_id", "cards.0.full.family_id", "cards.0.full.name",
		"cards.0.full.review_status", "cards.0.full.input_relation", "cards.0.full.requirements", "cards.0.full.response_act",
		"cards.0.full.applicability_notes", "cards.0.projection",
		"cards.0.projection.relations", "cards.0.projection.emotion_chain", "cards.0.projection.keys",
		"cards.0.projection.keys.required_alias_groups", "cards.0.projection.keys.related",
	}
	for _, path := range paths {
		for _, mutation := range []string{"missing", "null"} {
			t.Run(path+"/"+mutation, func(t *testing.T) {
				raw := mutateEncodingJSON(t, path, mutation == "missing", nil)
				var encoded EncodedWiki
				if err := DecodeStrict(raw, &encoded); err == nil {
					t.Fatalf("%s field %s accepted", mutation, path)
				}
			})
		}
	}
}

func TestEncodedWikiRejectsNullInsideArraysMapsAndOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		path  string
		value any
	}{
		{"cards.0.full.roles", map[string]any{"person": nil}},
		{"cards.0.full.slots", map[string]any{"value": nil}},
		{"cards.0.full.requirements", []any{nil}},
		{"cards.0.full.examples.positive", []any{nil}},
		{"cards.0.full.stance.voice", nil},
		{"cards.0.projection.relations", []any{nil}},
		{"cards.0.projection.keys.required_alias_groups", []any{nil}},
		{"cards.0.projection.keys.required_alias_groups", []any{[]any{nil}}},
		{"cards.0.projection.keys.related", []any{nil}},
		{"cards.0.projection.card_fingerprint", nil},
		{"cards.0.full.description", nil},
		{"cards.0.full.sources", nil},
	} {
		t.Run(tc.path+"/"+strconv.Itoa(len(mustJSON(t, tc.value))), func(t *testing.T) {
			var encoded EncodedWiki
			if err := DecodeStrict(mutateEncodingJSON(t, tc.path, false, tc.value), &encoded); err == nil {
				t.Fatal("nested null accepted", tc.path)
			}
		})
	}
}

func TestEncodedWikiAcceptsExplicitEmptyContainersAndProgramBoundSourceFields(t *testing.T) {
	var encoded EncodedWiki
	if err := DecodeStrict([]byte(completeEncodedWikiJSON), &encoded); err != nil {
		t.Fatal(err)
	}
	source := WikiSource{ID: "wiki:test", Title: "反差", URL: "https://example.com/wiki/contrast", Description: "原始Wiki定义"}
	records, err := CompileWikiEncoding(source, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(records[0].Full.Slots) != 0 || records[0].Full.Description != source.Description || len(records[0].Full.Sources) != 1 || records[0].Projection.CardFingerprint == "" {
		t.Fatal("explicit zero-slot or program-bound source fields failed")
	}
}

func TestEncodedWikiAcceptsSemanticOnlyReferenceAndUnmatchedExpressionHints(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(completeEncodedWikiJSON), &root); err != nil {
		t.Fatal(err)
	}
	full := root["cards"].([]any)[0].(map[string]any)["full"].(map[string]any)
	for _, key := range []string{"roles", "stance", "template", "anchors", "slots", "examples"} {
		delete(full, key)
	}
	var encoded EncodedWiki
	if err := DecodeStrict(mustJSON(t, root), &encoded); err != nil {
		t.Fatal("semantic-only reference rejected", err)
	}
	source := WikiSource{ID: "wiki:test", Title: "反差", URL: "https://example.com/wiki/contrast", Description: "原始Wiki定义"}
	records, err := CompileWikiEncoding(source, encoded)
	if err != nil {
		t.Fatal("reference required expression renderer metadata", err)
	}
	if len(records) != 1 || records[0].Full.Description != source.Description {
		t.Fatal("source reference lost")
	}
	card := records[0].Full
	card.Template = "这下{unbound_hint}了"
	card.Anchors = []string{"和模板不同的原梗短语"}
	card.Stance = Stance{ReplyTarget: "described_person", EvaluationTarget: "described_event"}
	card.Slots = map[string]Slot{"other_hint": {Meaning: "可选表达说明", BindTo: "described_person"}}
	card.Examples = Examples{Positive: []Example{{Explanation: "可选参考材料"}}}
	projection := records[0].Projection
	projection.CardFingerprint, _ = Fingerprint(card)
	index, err := CompileCard(card, projection)
	if err != nil {
		t.Fatal("optional hint enforced as output contract", err)
	}
	if strings.Contains(index.Relation, card.Template) || strings.Contains(index.Relation, card.Anchors[0]) {
		t.Fatal("expression hint leaked into index projection")
	}
}

func TestEncodedWikiOptionalHintsRemainTypedAndRejectNullWhenPresent(t *testing.T) {
	for _, key := range []string{"roles", "stance", "template", "anchors", "slots", "examples"} {
		t.Run(key+"/null", func(t *testing.T) {
			var encoded EncodedWiki
			if err := DecodeStrict(mutateEncodingJSON(t, "cards.0.full."+key, false, nil), &encoded); err == nil {
				t.Fatal("optional hint null accepted", key)
			}
		})
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"roles", []any{}}, {"stance", "voice"}, {"template", 17}, {"anchors", map[string]any{}}, {"slots", []any{}}, {"examples", []any{}}, {"slots", map[string]any{"value": map[string]any{"max_chars": "many"}}}} {
		var encoded EncodedWiki
		if err := DecodeStrict(mutateEncodingJSON(t, "cards.0.full."+tc.key, false, tc.value), &encoded); err == nil {
			t.Fatal("optional hint wrong type accepted", tc.key)
		}
	}
}

func TestEncodedWikiRequirementAlternativesStayMeaningful(t *testing.T) {
	for _, value := range []any{[]any{map[string]any{"id": "condition", "any_of": []any{}}}, []any{map[string]any{"id": "condition", "any_of": []any{" "}}}} {
		var encoded EncodedWiki
		if err := DecodeStrict(mutateEncodingJSON(t, "cards.0.full.requirements", false, value), &encoded); err != nil {
			t.Fatal(err)
		}
		if _, err := CompileWikiEncoding(WikiSource{ID: "wiki:test", Title: "反差", URL: "https://example.com/wiki/contrast", Description: "原始Wiki定义"}, encoded); err == nil {
			t.Fatal("empty condition alternatives accepted")
		}
	}
}

func TestAbsentExpressionHintsAreOmittedFromReferenceJSON(t *testing.T) {
	card := UseCard{UseID: "reference.use", FamilyID: "reference", Name: "参考", ReviewStatus: "pending", InputRelation: "A的预期与实际相反", Requirements: []Requirement{}, ResponseAct: "感叹反差", Sources: []SourceMetadata{{Kind: "wiki", Scope: "来源原文"}}}
	raw := mustJSON(t, card)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"roles", "stance", "template", "anchors", "slots", "examples"} {
		if _, exists := object[key]; exists {
			t.Fatal("empty optional expression hint serialized", key, string(raw))
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func mutateEncodingJSON(t *testing.T, path string, remove bool, value any) []byte {
	t.Helper()
	var root any
	if err := json.Unmarshal([]byte(completeEncodedWikiJSON), &root); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(path, ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil {
				t.Fatal(err)
			}
			current = node[i]
		default:
			t.Fatal("invalid test path", path)
		}
	}
	object, ok := current.(map[string]any)
	if !ok {
		t.Fatal("test path must end in object field", path)
	}
	last := parts[len(parts)-1]
	if remove {
		delete(object, last)
	} else {
		object[last] = value
	}
	return mustJSON(t, root)
}
