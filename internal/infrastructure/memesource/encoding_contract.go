package memesource

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

const contentIdentityVersion = "wiki_encode_v2_content_ids"

// This source-specific addition makes the existing semantic validator explicit
// to the encoder. It changes neither the shared schema nor any retrieval budget.
const sourceEncodingContract = `
离线卡片的具体语义契约：每张full.name、full.input_relation、full.response_act必须是非空的具体说明。每张projection.relations至少包含一条完整、有方向的适用关系，和该卡full.input_relation表达同一适用机制；kind只能是goal_means、contrast、before_after、benefit_cost、attribution、role_pattern、promise_credibility、opportunity_action。不能用emotion_chain或keys代替relations，也不能补造原文没有的适用关系。use_id和family_id只是技术字符串占位，可填写use/family；程序会按Wiki来源和用法语义重建稳定ID，不依赖模型技术ID区分用法。其他必填语义和来源约束照原契约执行。`

func encodingContract(options SyncOptions) (string, json.RawMessage, string, error) {
	version := options.EncodingVersion
	if version == "" {
		version = contentIdentityVersion
	}
	prompt := memes.SystemPrompt(memes.StageEncode) + sourceEncodingContract
	schema := memes.StageSchema(memes.StageEncode)
	if strings.TrimSpace(prompt) == "" || !json.Valid(schema) {
		return "", nil, "", errors.New("Wiki encoding contract unavailable")
	}
	hash, err := memes.Fingerprint(struct {
		ModelID, Version, Prompt string
		Schema                   json.RawMessage
	}{options.ModelID, version, prompt, schema})
	return prompt, schema, hash, err
}

// Model technical IDs and program-bound provenance never define a persisted
// identity. Different semantic uses remain distinct even if the model repeats
// an ID; exact semantic duplicates are one use. All semantic checks still run.
func compileContentIdentities(source Source, encoded memes.EncodedWiki) ([]memes.IndexRecord, error) {
	if err := encodingSemanticGuard(source, encoded, true); err != nil {
		return nil, err
	}
	if err := encodingFormatCheck(encoded); err != nil {
		return nil, err
	}
	sourceHash, err := memes.Fingerprint(source.ID)
	if err != nil {
		return nil, err
	}
	familyID := "wiki_" + sourceHash[:12]
	canonical := memes.EncodedWiki{Cards: make([]memes.EncodedUse, 0, len(encoded.Cards))}
	seen := map[string]bool{}
	ids := map[string]string{}
	for _, use := range encoded.Cards {
		// Examples is tagged omitzero. Non-nil empty slices encode as {}, then
		// disappear after JSON freezing; canonicalize before assigning hashes.
		if len(use.Full.Examples.Positive) == 0 {
			use.Full.Examples.Positive = nil
		}
		if len(use.Full.Examples.NearMiss) == 0 {
			use.Full.Examples.NearMiss = nil
		}
		use.Full.Requirements = canonicalRequirements(use.Full.Requirements)
		use.Full.UseID = ""
		use.Full.FamilyID = ""
		use.Full.Description = ""
		use.Full.ReviewStatus = ""
		use.Full.Sources = nil
		use.Projection.CardFingerprint = ""
		digest, hashErr := memes.Fingerprint(use)
		if hashErr != nil {
			return nil, hashErr
		}
		if seen[digest] {
			continue
		}
		seen[digest] = true
		id := familyID + "_" + digest[:16]
		if prior, exists := ids[id]; exists && prior != digest {
			return nil, errors.New("content-derived use_id collision")
		}
		ids[id] = digest
		use.Full.UseID = id
		use.Full.FamilyID = familyID
		use.Full.ReviewStatus = "pending"
		canonical.Cards = append(canonical.Cards, use)
	}
	return memes.CompileWikiEncoding(source.WikiSource(), canonical)
}
