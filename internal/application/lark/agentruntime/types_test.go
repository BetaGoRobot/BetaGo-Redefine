package agentruntime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidateRunStatusTransition(t *testing.T) {
	if err := ValidateRunStatusTransition(RunStatusQueued, RunStatusRunning); err != nil {
		t.Fatalf("expected queued -> running to be valid, got %v", err)
	}
	if err := ValidateRunStatusTransition(RunStatusRunning, RunStatusQueued); err != nil {
		t.Fatalf("expected running -> queued to be valid, got %v", err)
	}
	if err := ValidateRunStatusTransition(RunStatusQueued, RunStatusFailed); err != nil {
		t.Fatalf("expected queued -> failed to be valid for stale repair, got %v", err)
	}
	if err := ValidateRunStatusTransition(RunStatusWaitingApproval, RunStatusCompleted); err == nil {
		t.Fatalf("expected waiting_approval -> completed to be invalid")
	}
	if err := ValidateRunStatusTransition(RunStatusCompleted, RunStatusRunning); err == nil {
		t.Fatalf("expected completed -> running to be invalid")
	}
}

func TestValidateStepStatusTransition(t *testing.T) {
	if err := ValidateStepStatusTransition(StepStatusQueued, StepStatusRunning); err != nil {
		t.Fatalf("expected queued -> running to be valid, got %v", err)
	}
	if err := ValidateStepStatusTransition(StepStatusRunning, StepStatusCompleted); err != nil {
		t.Fatalf("expected running -> completed to be valid, got %v", err)
	}
	if err := ValidateStepStatusTransition(StepStatusCompleted, StepStatusRunning); err == nil {
		t.Fatalf("expected completed -> running to be invalid")
	}
}

func TestAgentRunJSONRoundTrip(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	run := AgentRun{
		ID:               "run_1",
		SessionID:        "session_1",
		TriggerType:      TriggerTypeMention,
		TriggerMessageID: "om_msg",
		ActorOpenID:      "ou_actor",
		Status:           RunStatusWaitingApproval,
		WaitingReason:    WaitingReasonApproval,
		Revision:         3,
		StartedAt:        &now,
	}

	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded AgentRun
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.Status != RunStatusWaitingApproval {
		t.Fatalf("expected waiting approval status, got %q", decoded.Status)
	}
	if decoded.WaitingReason != WaitingReasonApproval {
		t.Fatalf("expected waiting reason approval, got %q", decoded.WaitingReason)
	}
	if decoded.TriggerType != TriggerTypeMention {
		t.Fatalf("expected mention trigger, got %q", decoded.TriggerType)
	}
}

func TestRunStatusIsTerminal(t *testing.T) {
	if !RunStatusCompleted.IsTerminal() {
		t.Fatalf("expected completed to be terminal")
	}
	if RunStatusRunning.IsTerminal() {
		t.Fatalf("expected running to be non-terminal")
	}
}

func TestAgentRunLivenessHelpers(t *testing.T) {
	policy := RunLeasePolicy{
		TTL:               30 * time.Second,
		HeartbeatInterval: 10 * time.Second,
	}
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	heartbeatAt := now.Add(-11 * time.Second)
	leaseExpiresAt := now.Add(19 * time.Second)

	run := AgentRun{
		ID:             "run_liveness",
		Status:         RunStatusRunning,
		WorkerID:       "worker_1",
		HeartbeatAt:    &heartbeatAt,
		LeaseExpiresAt: &leaseExpiresAt,
	}

	if !run.HasExecutionLease() {
		t.Fatal("HasExecutionLease() = false, want true")
	}
	if run.IsLeaseExpired(now) {
		t.Fatal("IsLeaseExpired() = true, want false")
	}
	if !run.NeedsHeartbeat(now, policy) {
		t.Fatal("NeedsHeartbeat() = false, want true")
	}
	if state := run.LivenessState(now); state != RunLivenessHealthy {
		t.Fatalf("LivenessState() = %q, want %q", state, RunLivenessHealthy)
	}

	expiredAt := now.Add(-time.Second)
	run.LeaseExpiresAt = &expiredAt
	if !run.IsLeaseExpired(now) {
		t.Fatal("IsLeaseExpired() = false, want true")
	}
	if state := run.LivenessState(now); state != RunLivenessExpired {
		t.Fatalf("expired LivenessState() = %q, want %q", state, RunLivenessExpired)
	}

	run.WorkerID = ""
	run.HeartbeatAt = nil
	run.LeaseExpiresAt = nil
	if run.HasExecutionLease() {
		t.Fatal("HasExecutionLease() = true, want false")
	}
	if state := run.LivenessState(now); state != RunLivenessUnknown {
		t.Fatalf("untracked LivenessState() = %q, want %q", state, RunLivenessUnknown)
	}
}
