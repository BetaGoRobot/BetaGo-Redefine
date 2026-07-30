package agentcard

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBoundCardSpecAlwaysRedactsTokenAndTrustedCapability(t *testing.T) {
	spec := validValidationSpec()
	bound, err := NewBoundCardSpec(spec, LifecycleInteractive, map[string]RuntimeBinding{
		"confirm": {
			RunID: "run-1", StepID: "step-1", InteractionID: "interaction-1",
			Revision: 2, Token: "plaintext-super-secret-token",
			InteractionKind: "capability_confirm",
			TrustedCapability: json.RawMessage(
				`{"schedule_id":"trusted-internal-schedule"}`,
			),
		},
	})
	if err != nil {
		t.Fatalf("NewBoundCardSpec() error = %v", err)
	}
	for name, value := range map[string]string{
		"json":   string(mustMarshalBound(t, bound)),
		"string": bound.String(),
		"fmt":    fmt.Sprintf("%v", bound),
	} {
		if strings.Contains(value, "plaintext-super-secret-token") ||
			strings.Contains(value, "trusted-internal-schedule") {
			t.Fatalf("%s leaked private binding: %s", name, value)
		}
		if !strings.Contains(value, "[REDACTED]") {
			t.Fatalf("%s has no redaction marker: %s", name, value)
		}
	}
	payload, err := bound.CallbackPayload(spec.Actions[0])
	if err != nil {
		t.Fatalf("CallbackPayload() error = %v", err)
	}
	if payload["token"] != "plaintext-super-secret-token" ||
		payload["action"] != RuntimeResumeAction ||
		payload["action_id"] != "confirm" {
		t.Fatalf("runtime payload = %#v", payload)
	}
}

func mustMarshalBound(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}
