package domain_test

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestCanonicalArgumentsRejectAmbiguousOrCallerControlledIdentity(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `[]`, `{"scope":{"ChatID":"other"}}`, `{"nested":{"actor_id":"forged"}}`, `{"chat-id":"other"}`, `{"x":1} {"x":2}`} {
		if _, e := domain.CanonicalArguments(raw); e == nil {
			t.Errorf("unsafe args accepted: %s", raw)
		}
	}
	canonical, e := domain.CanonicalArguments(` {"z":2,"a":"中文"} `)
	if e != nil || canonical != `{"a":"中文","z":2}` {
		t.Fatalf("unstable canonical args: %q %v", canonical, e)
	}
}
