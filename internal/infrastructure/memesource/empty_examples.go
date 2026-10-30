package memesource

import (
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

// legacyEmptyExamplesHashes recognizes the exact old omitzero/JSON-freeze
// mismatch. Both original hash preimages must verify; arbitrary damage, changed
// source bindings or semantic fields still fail closed.
func legacyEmptyExamplesHashes(old, canonical []memes.IndexRecord) bool {
	if len(old) != len(canonical) {
		return false
	}
	changed := false
	for i, record := range old {
		a, _ := memes.Fingerprint(record)
		b, _ := memes.Fingerprint(canonical[i])
		if a == b {
			continue
		}
		if len(record.Full.Examples.Positive) != 0 || len(record.Full.Examples.NearMiss) != 0 {
			return false
		}
		normalized := record
		normalized.Fingerprint = canonical[i].Fingerprint
		normalized.Projection.CardFingerprint = canonical[i].Projection.CardFingerprint
		a, _ = memes.Fingerprint(normalized)
		if a != b {
			return false
		}
		fullRaw, _ := json.Marshal(record.Full)
		var full map[string]json.RawMessage
		if json.Unmarshal(fullRaw, &full) != nil {
			return false
		}
		if _, exists := full["examples"]; exists {
			return false
		}
		full["examples"] = json.RawMessage(`{}`)
		legacyCardHash, _ := memes.Fingerprint(full)
		if legacyCardHash != record.Projection.CardFingerprint {
			return false
		}
		fullRaw, _ = json.Marshal(full)
		recordRaw, _ := json.Marshal(record)
		var original map[string]json.RawMessage
		if json.Unmarshal(recordRaw, &original) != nil {
			return false
		}
		original["full"] = fullRaw
		original["fingerprint"] = json.RawMessage(`""`)
		legacyRecordHash, _ := memes.Fingerprint(original)
		if legacyRecordHash != record.Fingerprint {
			return false
		}
		changed = true
	}
	return changed
}
