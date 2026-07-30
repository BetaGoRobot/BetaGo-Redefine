package larkmsg

import (
	"strings"
	"testing"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/bytedance/sonic"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestRedactedCardActionEventRemovesRuntimeAndFormSecrets(t *testing.T) {
	event := &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Token: "event-token",
			Context: &callback.Context{
				PreviewToken: "preview-token",
			},
			Action: &callback.CallBackAction{
				Value: map[string]any{
					cardactionproto.ActionField: "agent.runtime.resume",
					cardactionproto.TokenField:  "runtime-token",
				},
				FormValue:  map[string]any{"reason": "private form value"},
				InputValue: "private input",
			},
		},
	}
	redacted, err := redactedCardActionEvent(event)
	if err != nil {
		t.Fatalf("redactedCardActionEvent() error = %v", err)
	}
	encoded, _ := sonic.Marshal(redacted)
	text := string(encoded)
	for _, secret := range []string{
		"event-token", "preview-token", "runtime-token",
		"private form value", "private input",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("redacted event leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redacted event = %s", text)
	}
	original, _ := sonic.Marshal(event)
	if !strings.Contains(string(original), "runtime-token") {
		t.Fatal("redaction mutated the original callback event")
	}
}
