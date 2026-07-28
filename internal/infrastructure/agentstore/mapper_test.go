package agentstore

import (
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

func TestRunModelRoundTripPreservesRuntimeFields(t *testing.T) {
	now := time.Date(2026, 7, 28, 9, 10, 11, 12, time.UTC)
	run := &agentruntime.AgentRun{
		ID:               "run-1",
		SessionID:        "session-1",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_1",
		TriggerEventID:   "event-1",
		ActorOpenID:      "ou_actor",
		ParentRunID:      "run-parent",
		Status:           agentruntime.RunStatusWaitingApproval,
		Goal:             "answer",
		InputText:        "hello",
		CurrentStepIndex: 7,
		WaitingReason:    agentruntime.WaitingReasonApproval,
		WaitingToken:     "token-hash",
		LastResponseID:   "response-1",
		ResultSummary:    "summary",
		ErrorText:        "error",
		Revision:         3,
		StartedAt:        now,
		FinishedAt:       now.Add(time.Minute),
		CreatedAt:        now.Add(2 * time.Minute),
		UpdatedAt:        now.Add(3 * time.Minute),
		WorkerID:         "worker-1",
		HeartbeatAt:      now.Add(4 * time.Minute),
		LeaseExpiresAt:   now.Add(5 * time.Minute),
		RepairAttempts:   2,
		ActivationSource: "mention",
		TopicFingerprint: "topic-1",
		LastRelevantAt:   now.Add(6 * time.Minute),
	}

	roundTrip := toRuntimeRun(toDBRun(run))
	if !reflect.DeepEqual(roundTrip, run) {
		t.Fatalf("roundtrip mismatch:\n got: %#v\nwant: %#v", roundTrip, run)
	}
}

func TestStepModelRoundTripPreservesRuntimeFields(t *testing.T) {
	now := time.Date(2026, 7, 28, 9, 10, 11, 12, time.UTC)
	step := &agentruntime.AgentStep{
		ID:             "step-1",
		RunID:          "run-1",
		Index:          2,
		Kind:           agentruntime.StepKindCapabilityCall,
		Status:         agentruntime.StepStatusFailed,
		CapabilityName: "send_message",
		InputJSON:      `{"content":"hello"}`,
		OutputJSON:     `{"message_id":"om_reply"}`,
		ErrorText:      "boom",
		ExternalRef:    "om_reply",
		StartedAt:      now,
		FinishedAt:     now.Add(time.Minute),
		CreatedAt:      now.Add(2 * time.Minute),
		DedupeKey:      "message:event-1",
		AttemptCount:   3,
		WorkerID:       "worker-1",
		LeaseExpiresAt: now.Add(3 * time.Minute),
		RetryOfStepID:  "step-original",
	}

	roundTrip := toRuntimeStep(toDBStep(step))
	if !reflect.DeepEqual(roundTrip, step) {
		t.Fatalf("roundtrip mismatch:\n got: %#v\nwant: %#v", roundTrip, step)
	}
}
