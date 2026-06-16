package agentstore

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

func TestRunModelRoundTripPreservesRuntimeFields(t *testing.T) {
	run := agentruntime.NewRun(agentruntime.NewRunRequest{
		SessionID:        "session-1",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_1",
		ActorOpenID:      "ou_actor",
		Goal:             "answer",
		InputText:        "hello",
	})
	run.Status = agentruntime.RunStatusWaitingApproval
	run.WaitingReason = agentruntime.WaitingReasonApproval
	run.WaitingToken = "token-1"

	roundTrip := toRuntimeRun(toDBRun(run))
	if roundTrip.ID != run.ID {
		t.Fatalf("run id = %q, want %q", roundTrip.ID, run.ID)
	}
	if roundTrip.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("status = %q", roundTrip.Status)
	}
	if roundTrip.WaitingReason != agentruntime.WaitingReasonApproval || roundTrip.WaitingToken != "token-1" {
		t.Fatalf("waiting fields not preserved: %+v", roundTrip)
	}
}

func TestStepModelRoundTripPreservesRuntimeFields(t *testing.T) {
	step := agentruntime.NewStep(agentruntime.NewStepRequest{
		RunID:          "run-1",
		Index:          2,
		Kind:           agentruntime.StepKindCapabilityCall,
		CapabilityName: "send_message",
		InputJSON:      `{"content":"hello"}`,
		ExternalRef:    "om_reply",
	})
	step.Status = agentruntime.StepStatusFailed
	step.ErrorText = "boom"

	roundTrip := toRuntimeStep(toDBStep(step))
	if roundTrip.ID != step.ID {
		t.Fatalf("step id = %q, want %q", roundTrip.ID, step.ID)
	}
	if roundTrip.Kind != agentruntime.StepKindCapabilityCall || roundTrip.Status != agentruntime.StepStatusFailed {
		t.Fatalf("step state not preserved: %+v", roundTrip)
	}
	if roundTrip.ErrorText != "boom" || roundTrip.ExternalRef != "om_reply" {
		t.Fatalf("step output fields not preserved: %+v", roundTrip)
	}
}
