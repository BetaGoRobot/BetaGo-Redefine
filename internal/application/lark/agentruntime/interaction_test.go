package agentruntime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHashInteractionTokenTrimsBeforeHashing(t *testing.T) {
	plain := HashInteractionToken("secret-token")
	padded := HashInteractionToken(" \tsecret-token\n")
	if plain == "" {
		t.Fatal("HashInteractionToken() = empty")
	}
	if plain != padded {
		t.Fatalf("trimmed hashes differ: plain=%q padded=%q", plain, padded)
	}
	if len(plain) != 64 {
		t.Fatalf("hash length = %d, want 64 hex characters", len(plain))
	}
}

func TestMatchInteractionToken(t *testing.T) {
	hash := HashInteractionToken("secret-token")
	tests := []struct {
		name  string
		token string
		hash  string
		want  bool
	}{
		{name: "correct", token: " secret-token ", hash: hash, want: true},
		{name: "wrong token", token: "wrong-token", hash: hash, want: false},
		{name: "bad hash", token: "secret-token", hash: "not-hex", want: false},
		{name: "wrong hash length", token: "secret-token", hash: "abcd", want: false},
		{name: "empty token", token: "", hash: HashInteractionToken(""), want: false},
		{name: "whitespace token", token: " \t\n", hash: HashInteractionToken(""), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchInteractionToken(tt.token, tt.hash); got != tt.want {
				t.Fatalf("MatchInteractionToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInteractionContractsExposeTypedRuntimeFields(t *testing.T) {
	now := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	projection := ProjectionDocument{
		IndexAlias: "agent-conversations",
		DocumentID: "run_1",
		Payload:    json.RawMessage(`{"run_id":"run_1"}`),
	}
	start := StartInteractionRequest{
		RunID:           "run_1",
		StepID:          "step_1",
		InteractionID:   "ix_1",
		Revision:        3,
		TokenHash:       "token_hash",
		InteractionKind: "approval",
		ExpiresAt:       now.Add(time.Hour),
		Projection:      projection,
	}
	resolve := ResolveInteractionRequest{
		RunID:         "run_1",
		StepID:        "step_1",
		InteractionID: "ix_1",
		Revision:      3,
		TokenHash:     "token_hash",
		Action:        "confirm",
		Outcome:       "approved",
		EventID:       "evt_1",
		SourceRef:     "om_1",
		ResolvedAt:    now,
		Projection:    projection,
	}
	claim := StepClaim{WorkerID: "worker_1", LeaseTTL: time.Minute, Now: now}

	if start.Projection.DocumentID != "run_1" {
		t.Fatalf("start projection document = %q", start.Projection.DocumentID)
	}
	if resolve.Outcome != "approved" || resolve.EventID != "evt_1" {
		t.Fatalf("resolve fields incomplete: %+v", resolve)
	}
	if claim.LeaseTTL != time.Minute {
		t.Fatalf("claim lease TTL = %v", claim.LeaseTTL)
	}
}
