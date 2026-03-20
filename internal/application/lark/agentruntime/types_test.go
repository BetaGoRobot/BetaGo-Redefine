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
