package conversationeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"
)

const (
	ContextSourceHistory   = "history"
	ContextSourceRetrieved = "retrieved"
	ContextSourceEvent     = "event"

	ContextKindMessage = "message"
	ContextKindChunk   = "chunk"
	ContextKindEvent   = "event"
)

func ContentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func EstimateTokens(content string) int {
	runes := utf8.RuneCountInString(content)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func SafeMetadata(values map[string]string) json.RawMessage {
	if len(values) == 0 {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
