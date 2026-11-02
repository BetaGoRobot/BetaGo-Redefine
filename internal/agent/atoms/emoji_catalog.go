package atoms

import (
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/larkemoji"
)

// This is model-visible tool knowledge and validation, not a prompt-only list.
var reactionSchema = func() json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 1024},
			"emoji":      map[string]any{"type": "string", "enum": larkemoji.Types(), "description": "Case-sensitive maintained Feishu reaction identifiers. Pick the one that fits this moment; e.g. COMFORT for solidarity, HIGHFIVE for celebration, LOL for laughter. A reaction may be the whole contribution."},
		},
		"required": []string{"message_id", "emoji"}, "additionalProperties": false,
	})
	return b
}()
