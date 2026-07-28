package agentruntime

import (
	"encoding/json"
	"errors"
	"strings"
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
		{name: "uppercase hash", token: "secret-token", hash: strings.ToUpper(hash), want: true},
		{name: "wrong token", token: "wrong-token", hash: hash, want: false},
		{name: "bad hash", token: "secret-token", hash: "not-hex", want: false},
		{name: "empty hash", token: "secret-token", hash: "", want: false},
		{name: "odd hash", token: "secret-token", hash: strings.Repeat("a", 63), want: false},
		{name: "wrong hash length", token: "secret-token", hash: "abcd", want: false},
		{name: "overlong hash", token: "secret-token", hash: strings.Repeat("a", 66), want: false},
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

func TestProjectionDocumentValidate(t *testing.T) {
	valid := validProjectionDocument()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	tests := []struct {
		name   string
		mutate func(*ProjectionDocument)
	}{
		{name: "missing index alias", mutate: func(d *ProjectionDocument) { d.IndexAlias = "" }},
		{name: "non-canonical index alias", mutate: func(d *ProjectionDocument) { d.IndexAlias = " index" }},
		{name: "missing document id", mutate: func(d *ProjectionDocument) { d.DocumentID = "" }},
		{name: "non-canonical document id", mutate: func(d *ProjectionDocument) { d.DocumentID = "doc " }},
		{name: "missing payload", mutate: func(d *ProjectionDocument) { d.Payload = nil }},
		{name: "bad payload", mutate: func(d *ProjectionDocument) { d.Payload = json.RawMessage(`{"secret":`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := valid
			tt.mutate(&document)
			requireInvalidRuntimeContract(t, document.Validate(), "secret")
		})
	}
}

func TestStartInteractionRequestValidate(t *testing.T) {
	valid := validStartInteractionRequest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	uppercaseHash := valid
	uppercaseHash.TokenHash = strings.ToUpper(valid.TokenHash)
	if err := uppercaseHash.Validate(); err != nil {
		t.Fatalf("uppercase TokenHash Validate() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		mutate func(*StartInteractionRequest)
	}{
		{name: "missing run id", mutate: func(r *StartInteractionRequest) { r.RunID = "" }},
		{name: "non-canonical run id", mutate: func(r *StartInteractionRequest) { r.RunID = " run_1" }},
		{name: "missing step id", mutate: func(r *StartInteractionRequest) { r.StepID = "" }},
		{name: "non-canonical step id", mutate: func(r *StartInteractionRequest) { r.StepID = "step_1 " }},
		{name: "missing interaction id", mutate: func(r *StartInteractionRequest) { r.InteractionID = "" }},
		{name: "non-canonical interaction id", mutate: func(r *StartInteractionRequest) { r.InteractionID = "\tix_1" }},
		{name: "invalid revision", mutate: func(r *StartInteractionRequest) { r.Revision = 0 }},
		{name: "empty token hash", mutate: func(r *StartInteractionRequest) { r.TokenHash = "" }},
		{name: "odd token hash", mutate: func(r *StartInteractionRequest) { r.TokenHash = strings.Repeat("a", 63) }},
		{name: "overlong token hash", mutate: func(r *StartInteractionRequest) { r.TokenHash = strings.Repeat("a", 66) }},
		{name: "non-hex token hash", mutate: func(r *StartInteractionRequest) { r.TokenHash = strings.Repeat("g", 64) }},
		{name: "missing interaction kind", mutate: func(r *StartInteractionRequest) { r.InteractionKind = "" }},
		{name: "non-canonical interaction kind", mutate: func(r *StartInteractionRequest) { r.InteractionKind = "approval " }},
		{name: "missing expiry", mutate: func(r *StartInteractionRequest) { r.ExpiresAt = time.Time{} }},
		{name: "invalid projection", mutate: func(r *StartInteractionRequest) { r.Projection.Payload = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			requireInvalidRuntimeContract(t, req.Validate(), "secret-token", string(req.Projection.Payload))
		})
	}
}

func TestResolveInteractionRequestValidate(t *testing.T) {
	valid := validResolveInteractionRequest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	withSourceRef := valid
	withSourceRef.EventID = ""
	withSourceRef.SourceRef = "om_1"
	if err := withSourceRef.Validate(); err != nil {
		t.Fatalf("source-only Validate() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		mutate func(*ResolveInteractionRequest)
	}{
		{name: "missing run id", mutate: func(r *ResolveInteractionRequest) { r.RunID = "" }},
		{name: "non-canonical run id", mutate: func(r *ResolveInteractionRequest) { r.RunID = " run_1" }},
		{name: "missing step id", mutate: func(r *ResolveInteractionRequest) { r.StepID = "" }},
		{name: "missing interaction id", mutate: func(r *ResolveInteractionRequest) { r.InteractionID = "" }},
		{name: "invalid revision", mutate: func(r *ResolveInteractionRequest) { r.Revision = -1 }},
		{name: "missing presented token", mutate: func(r *ResolveInteractionRequest) { r.PresentedToken = "" }},
		{name: "non-canonical presented token", mutate: func(r *ResolveInteractionRequest) { r.PresentedToken = " opaque-token" }},
		{name: "missing action", mutate: func(r *ResolveInteractionRequest) { r.Action = "" }},
		{name: "non-canonical action", mutate: func(r *ResolveInteractionRequest) { r.Action = "confirm " }},
		{name: "missing outcome", mutate: func(r *ResolveInteractionRequest) { r.Outcome = nil }},
		{name: "bad outcome", mutate: func(r *ResolveInteractionRequest) { r.Outcome = json.RawMessage(`{"secret":`) }},
		{name: "missing source references", mutate: func(r *ResolveInteractionRequest) { r.EventID, r.SourceRef = "", "" }},
		{name: "non-canonical event id", mutate: func(r *ResolveInteractionRequest) { r.EventID = " evt_1" }},
		{name: "non-canonical source ref", mutate: func(r *ResolveInteractionRequest) { r.SourceRef = " om_1" }},
		{name: "missing resolved at", mutate: func(r *ResolveInteractionRequest) { r.ResolvedAt = time.Time{} }},
		{name: "invalid projection", mutate: func(r *ResolveInteractionRequest) { r.Projection.DocumentID = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			requireInvalidRuntimeContract(t, req.Validate(), valid.PresentedToken, string(req.Outcome))
		})
	}
}

func TestStepClaimValidate(t *testing.T) {
	valid := StepClaim{WorkerID: "worker_1", LeaseTTL: time.Minute, Now: fixedRuntimeTime()}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	tests := []struct {
		name   string
		mutate func(*StepClaim)
	}{
		{name: "missing worker", mutate: func(c *StepClaim) { c.WorkerID = "" }},
		{name: "non-canonical worker", mutate: func(c *StepClaim) { c.WorkerID = " worker_1" }},
		{name: "zero lease", mutate: func(c *StepClaim) { c.LeaseTTL = 0 }},
		{name: "negative lease", mutate: func(c *StepClaim) { c.LeaseTTL = -time.Second }},
		{name: "missing now", mutate: func(c *StepClaim) { c.Now = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := valid
			tt.mutate(&claim)
			requireInvalidRuntimeContract(t, claim.Validate())
		})
	}
}

func TestCompleteStepRequestValidate(t *testing.T) {
	valid := CompleteStepRequest{
		StepID: "step_1", WorkerID: "worker_1", AttemptCount: 2,
		Output: json.RawMessage(`{"status":"done"}`), FinishedAt: fixedRuntimeTime(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	tests := []struct {
		name   string
		mutate func(*CompleteStepRequest)
	}{
		{name: "missing step", mutate: func(r *CompleteStepRequest) { r.StepID = "" }},
		{name: "non-canonical step", mutate: func(r *CompleteStepRequest) { r.StepID = "step_1 " }},
		{name: "missing worker", mutate: func(r *CompleteStepRequest) { r.WorkerID = "" }},
		{name: "non-canonical worker", mutate: func(r *CompleteStepRequest) { r.WorkerID = " worker_1" }},
		{name: "zero attempt", mutate: func(r *CompleteStepRequest) { r.AttemptCount = 0 }},
		{name: "negative attempt", mutate: func(r *CompleteStepRequest) { r.AttemptCount = -1 }},
		{name: "missing output", mutate: func(r *CompleteStepRequest) { r.Output = nil }},
		{name: "bad output", mutate: func(r *CompleteStepRequest) { r.Output = json.RawMessage(`{"secret":`) }},
		{name: "missing finished at", mutate: func(r *CompleteStepRequest) { r.FinishedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			requireInvalidRuntimeContract(t, req.Validate(), "secret")
		})
	}
}

func TestRetryStepRequestValidate(t *testing.T) {
	valid := RetryStepRequest{
		StepID: "step_1", WorkerID: "worker_1", AttemptCount: 2,
		ErrorText: "temporary failure", RetryAt: fixedRuntimeTime(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	tests := []struct {
		name   string
		mutate func(*RetryStepRequest)
	}{
		{name: "missing step", mutate: func(r *RetryStepRequest) { r.StepID = "" }},
		{name: "non-canonical step", mutate: func(r *RetryStepRequest) { r.StepID = " step_1" }},
		{name: "missing worker", mutate: func(r *RetryStepRequest) { r.WorkerID = "" }},
		{name: "non-canonical worker", mutate: func(r *RetryStepRequest) { r.WorkerID = "worker_1 " }},
		{name: "zero attempt", mutate: func(r *RetryStepRequest) { r.AttemptCount = 0 }},
		{name: "negative attempt", mutate: func(r *RetryStepRequest) { r.AttemptCount = -1 }},
		{name: "missing error", mutate: func(r *RetryStepRequest) { r.ErrorText = "" }},
		{name: "whitespace error", mutate: func(r *RetryStepRequest) { r.ErrorText = " \t" }},
		{name: "missing retry at", mutate: func(r *RetryStepRequest) { r.RetryAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			requireInvalidRuntimeContract(t, req.Validate())
		})
	}
}

func TestStoreSentinelsAreStableDomainErrors(t *testing.T) {
	sentinels := []error{
		ErrLeaseLost,
		ErrActiveRunConflict,
		ErrInteractionConflict,
		ErrInteractionExpired,
		ErrInteractionTokenMismatch,
		ErrTerminalRun,
	}
	for _, sentinel := range sentinels {
		if sentinel == nil || !errors.Is(sentinel, sentinel) {
			t.Fatalf("invalid domain sentinel: %v", sentinel)
		}
	}
}

func TestReclaimStaleStepsRequestValidate(t *testing.T) {
	valid := ReclaimStaleStepsRequest{Now: fixedRuntimeTime(), Limit: 100}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	tests := []struct {
		name   string
		mutate func(*ReclaimStaleStepsRequest)
	}{
		{name: "missing now", mutate: func(r *ReclaimStaleStepsRequest) { r.Now = time.Time{} }},
		{name: "zero limit", mutate: func(r *ReclaimStaleStepsRequest) { r.Limit = 0 }},
		{name: "negative limit", mutate: func(r *ReclaimStaleStepsRequest) { r.Limit = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			requireInvalidRuntimeContract(t, req.Validate())
		})
	}
}

func validProjectionDocument() ProjectionDocument {
	return ProjectionDocument{
		IndexAlias: "agent-conversations",
		DocumentID: "run_1",
		Payload:    json.RawMessage(`{"run_id":"run_1"}`),
	}
}

func validStartInteractionRequest() StartInteractionRequest {
	return StartInteractionRequest{
		RunID:           "run_1",
		StepID:          "step_1",
		InteractionID:   "ix_1",
		Revision:        3,
		TokenHash:       HashInteractionToken("secret-token"),
		InteractionKind: "approval",
		ExpiresAt:       fixedRuntimeTime().Add(time.Hour),
		Projection:      validProjectionDocument(),
	}
}

func validResolveInteractionRequest() ResolveInteractionRequest {
	return ResolveInteractionRequest{
		RunID:          "run_1",
		StepID:         "step_1",
		InteractionID:  "ix_1",
		Revision:       3,
		PresentedToken: "opaque-token",
		Action:         "confirm",
		Outcome:        json.RawMessage(`{"status":"approved"}`),
		EventID:        "evt_1",
		ResolvedAt:     fixedRuntimeTime(),
		Projection:     validProjectionDocument(),
	}
}

func fixedRuntimeTime() time.Time {
	return time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
}

func requireInvalidRuntimeContract(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Validate() error = nil, want rejection")
	}
	if !errors.Is(err, ErrInvalidRuntimeContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidRuntimeContract", err)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("Validate() error leaked sensitive value: %v", err)
		}
	}
}
