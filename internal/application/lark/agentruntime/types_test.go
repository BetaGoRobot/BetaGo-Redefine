package agentruntime

import "testing"

func TestRunStatusTransitionAllowsNormalLifecycle(t *testing.T) {
	cases := []struct {
		from RunStatus
		to   RunStatus
	}{
		{RunStatusQueued, RunStatusRunning},
		{RunStatusRunning, RunStatusWaitingApproval},
		{RunStatusWaitingApproval, RunStatusQueued},
		{RunStatusRunning, RunStatusCompleted},
		{RunStatusRunning, RunStatusFailed},
		{RunStatusWaitingCallback, RunStatusCancelled},
	}
	for _, tc := range cases {
		if err := ValidateRunTransition(tc.from, tc.to); err != nil {
			t.Fatalf("ValidateRunTransition(%q, %q) error = %v, want nil", tc.from, tc.to, err)
		}
	}
}

func TestRunStatusTransitionRejectsTerminalResume(t *testing.T) {
	if err := ValidateRunTransition(RunStatusCompleted, RunStatusRunning); err == nil {
		t.Fatal("expected completed run cannot return to running")
	}
	if err := ValidateRunTransition(RunStatusCancelled, RunStatusQueued); err == nil {
		t.Fatal("expected cancelled run cannot return to queued")
	}
}

func TestStepStatusTransitionAllowsNormalLifecycle(t *testing.T) {
	if err := ValidateStepTransition(StepStatusQueued, StepStatusRunning); err != nil {
		t.Fatalf("queued -> running error = %v", err)
	}
	if err := ValidateStepTransition(StepStatusRunning, StepStatusCompleted); err != nil {
		t.Fatalf("running -> completed error = %v", err)
	}
	if err := ValidateStepTransition(StepStatusRunning, StepStatusFailed); err != nil {
		t.Fatalf("running -> failed error = %v", err)
	}
}

func TestStepStatusTransitionRejectsTerminalResume(t *testing.T) {
	if err := ValidateStepTransition(StepStatusCompleted, StepStatusRunning); err == nil {
		t.Fatal("expected completed step cannot return to running")
	}
}

func TestNewRunInitializesQueuedRun(t *testing.T) {
	run := NewRun(NewRunRequest{
		SessionID:        "session-1",
		TriggerType:      TriggerTypeMention,
		TriggerMessageID: "om_1",
		ActorOpenID:      "ou_actor",
		Goal:             "answer question",
		InputText:        "help me",
	})
	if run.ID == "" {
		t.Fatal("expected generated run id")
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("run status = %q, want %q", run.Status, RunStatusQueued)
	}
	if run.SessionID != "session-1" || run.ActorOpenID != "ou_actor" {
		t.Fatalf("run identity fields not initialized: %+v", run)
	}
}

func TestNewStepInitializesQueuedStep(t *testing.T) {
	step := NewStep(NewStepRequest{
		RunID:          "run-1",
		Index:          1,
		Kind:           StepKindPlan,
		CapabilityName: "planner",
		InputJSON:      `{"goal":"x"}`,
	})
	if step.ID == "" {
		t.Fatal("expected generated step id")
	}
	if step.Status != StepStatusQueued {
		t.Fatalf("step status = %q, want %q", step.Status, StepStatusQueued)
	}
	if step.InputJSON != `{"goal":"x"}` {
		t.Fatalf("step input = %q", step.InputJSON)
	}
}
