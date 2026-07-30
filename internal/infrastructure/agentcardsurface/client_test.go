package agentcardsurface

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
)

func TestNormalizeCardAcceptsRawJSONAndRejectsNonObject(t *testing.T) {
	normalized, err := normalizeCard(json.RawMessage(`{"schema":"2.0"}`))
	if err != nil {
		t.Fatalf("normalizeCard() error = %v", err)
	}
	if normalized["schema"] != "2.0" {
		t.Fatalf("normalized = %#v", normalized)
	}
	if _, err := normalizeCard(json.RawMessage(`[]`)); err == nil {
		t.Fatal("normalizeCard() accepted a JSON array")
	}
}

func TestClassifyDeliveryErrorMarksTimeoutAmbiguous(t *testing.T) {
	err := classifyDeliveryError(context.DeadlineExceeded)
	if !errors.Is(err, agentcard.ErrSurfaceDeliveryAmbiguous) {
		t.Fatalf("classifyDeliveryError() = %v", err)
	}
}

func TestCreateIdempotencyKeyUsesPublicInteractionID(t *testing.T) {
	card := map[string]any{
		"elements": []any{
			map[string]any{
				"value": map[string]any{
					"interaction_id": "interaction-1",
					"token":          "must-not-influence-idempotency",
				},
			},
		},
	}
	first := createIdempotencyKey(card)
	card["elements"].([]any)[0].(map[string]any)["value"].(map[string]any)["token"] = "changed"
	second := createIdempotencyKey(card)
	if first == "" || first != second {
		t.Fatalf("idempotency keys = %q and %q", first, second)
	}
}
