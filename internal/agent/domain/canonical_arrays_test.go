package domain_test

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"testing"
)

func TestCanonicalArgumentsPreservesEmptyArrayVersusExplicitNull(t *testing.T) {
	for _, input := range []string{`{"tags":[]}`, `{"nested":{"items":[]},"tags":[[],null]}`} {
		out, err := domain.CanonicalArguments(input)
		if err != nil {
			t.Fatal(err)
		}
		if out != input {
			t.Fatalf("valid argument shape changed: %s -> %s", input, out)
		}
	}
}
