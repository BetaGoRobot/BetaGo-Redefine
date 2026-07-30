package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

type capabilityExecutionStoreFake struct {
	begun       CapabilityExecutionRequest
	completed   CapabilityExecutionCompletion
	replay      bool
	beginCalls  int
	finishCalls int
}

func (f *capabilityExecutionStoreFake) BeginCapabilityExecution(
	_ context.Context,
	request CapabilityExecutionRequest,
) (CapabilityExecutionState, error) {
	f.beginCalls++
	f.begun = request
	return CapabilityExecutionState{Terminal: f.replay}, nil
}

func (f *capabilityExecutionStoreFake) CompleteCapabilityExecution(
	_ context.Context,
	completion CapabilityExecutionCompletion,
) error {
	f.finishCalls++
	f.completed = completion
	return nil
}

type capabilityExecutorFake struct {
	invocation CapabilityInvocation
	output     json.RawMessage
	err        error
	calls      int
}

func (f *capabilityExecutorFake) Execute(
	_ context.Context,
	invocation CapabilityInvocation,
) (json.RawMessage, error) {
	f.calls++
	f.invocation = invocation
	return append(json.RawMessage(nil), f.output...), f.err
}

func TestCapabilityServiceExecutesOnlyPersistedTrustedDescriptor(t *testing.T) {
	now := time.Date(2026, 7, 30, 2, 3, 4, 0, time.UTC)
	store := &capabilityExecutionStoreFake{}
	executor := &capabilityExecutorFake{
		output: json.RawMessage(`{"task_id":"task-trusted","updated":true}`),
	}
	service, err := NewCapabilityService(CapabilityServiceOptions{
		Store: store, Executor: executor, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCapabilityService() error = %v", err)
	}
	step := &agentruntime.AgentStep{
		ID: "step-capability", RunID: "run-1",
		Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		CapabilityName: "schedule.update", WorkerID: "worker-1", AttemptCount: 2,
		InputJSON: `{
			"version":1,
			"source_step_id":"step-card-action",
			"interaction_id":"interaction-1",
			"action_id":"confirm",
			"descriptor":{
				"action_id":"confirm",
				"mode":"capability_confirm",
				"intent":"schedule.update",
				"continue_agent":true,
				"capability_name":"schedule.update",
				"capability_version":"v1",
				"capability_input":{"task_id":"task-trusted","mutation":{"enabled":false}},
				"idempotency_key":"cardcap_123",
				"permission_context":{
					"scope":"schedule.update",
					"chat_id":"chat-trusted",
					"actor_open_id":"owner-trusted"
				},
				"actor_policy":{"mode":"owner","open_id":"owner-trusted"},
				"result_projection_policy":{
					"continue_agent":true,
					"success_surface_status":"resolved",
					"failure_surface_status":"failed"
				}
			}
		}`,
	}
	lease := agentruntime.StepLease{
		StepID: step.ID, WorkerID: "worker-1", AttemptCount: 2,
		LeaseTTL: time.Minute, Now: now,
	}
	if err := service.ProcessCapabilityStep(
		context.Background(),
		step,
		lease,
	); err != nil {
		t.Fatalf("ProcessCapabilityStep() error = %v", err)
	}
	wantInput := json.RawMessage(`{"task_id":"task-trusted","mutation":{"enabled":false}}`)
	if executor.calls != 1 ||
		executor.invocation.Name != "schedule.update" ||
		executor.invocation.Version != "v1" ||
		executor.invocation.IdempotencyKey != "cardcap_123" ||
		!reflect.DeepEqual(executor.invocation.Input, wantInput) ||
		executor.invocation.Permission.ChatID != "chat-trusted" ||
		executor.invocation.Permission.ActorOpenID != "owner-trusted" {
		t.Fatalf("trusted invocation = %#v", executor.invocation)
	}
	if store.beginCalls != 1 || store.finishCalls != 1 ||
		!store.completed.Succeeded ||
		!reflect.DeepEqual(store.completed.Output, executor.output) ||
		store.completed.FinishedAt != now {
		t.Fatalf("begin=%#v complete=%#v", store.begun, store.completed)
	}
}

func TestCapabilityServiceRejectsCallbackShapedOverridesInStepPayload(t *testing.T) {
	for _, field := range []string{
		`"capability_name":"permission.manage"`,
		`"object_id":"forged-object"`,
		`"target_user":"forged-user"`,
		`"target_chat":"forged-chat"`,
		`"schedule_mutation":{"delete":true}`,
		`"permission_scope":"admin"`,
		`"amount":999999`,
		`"destructive":true`,
	} {
		t.Run(field, func(t *testing.T) {
			raw := `{
				"version":1,
				"source_step_id":"step-card-action",
				"interaction_id":"interaction-1",
				"action_id":"confirm",
				` + field + `,
				"descriptor":{
					"action_id":"confirm",
					"mode":"capability_confirm",
					"intent":"schedule.update",
					"continue_agent":true,
					"capability_name":"schedule.update",
					"capability_version":"v1",
					"capability_input":{"task_id":"task-trusted"},
					"idempotency_key":"cardcap_123",
					"permission_context":{"scope":"schedule.update","chat_id":"chat-1","actor_open_id":"owner-1"},
					"actor_policy":{"mode":"owner","open_id":"owner-1"},
					"result_projection_policy":{
						"continue_agent":true,
						"success_surface_status":"resolved",
						"failure_surface_status":"failed"
					}
				}
			}`
			_, err := DecodeCapabilityInvocation(&agentruntime.AgentStep{
				ID: "step-capability", RunID: "run-1",
				Kind:           agentruntime.StepKindCapabilityCall,
				CapabilityName: "schedule.update", InputJSON: raw,
			})
			if err == nil {
				t.Fatalf("DecodeCapabilityInvocation() accepted override %s", field)
			}
		})
	}
}

func TestCapabilityServicePersistsFailureAndDoesNotRetryTheSideEffect(t *testing.T) {
	now := time.Date(2026, 7, 30, 3, 4, 5, 0, time.UTC)
	store := &capabilityExecutionStoreFake{}
	executor := &capabilityExecutorFake{err: errors.New("provider rejected mutation")}
	service, err := NewCapabilityService(CapabilityServiceOptions{
		Store: store, Executor: executor, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	step := validCapabilityStep()
	if err := service.ProcessCapabilityStep(context.Background(), step, agentruntime.StepLease{
		StepID: step.ID, WorkerID: step.WorkerID,
		AttemptCount: step.AttemptCount, LeaseTTL: time.Minute, Now: now,
	}); err != nil {
		t.Fatalf("ProcessCapabilityStep() error = %v", err)
	}
	if store.finishCalls != 1 || store.completed.Succeeded ||
		store.completed.ErrorText != "provider rejected mutation" {
		t.Fatalf("completion = %#v", store.completed)
	}

	store.replay = true
	if err := service.ProcessCapabilityStep(context.Background(), step, agentruntime.StepLease{
		StepID: step.ID, WorkerID: step.WorkerID,
		AttemptCount: step.AttemptCount, LeaseTTL: time.Minute, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || store.finishCalls != 1 {
		t.Fatalf("executor calls=%d completion calls=%d", executor.calls, store.finishCalls)
	}
}

func validCapabilityStep() *agentruntime.AgentStep {
	return &agentruntime.AgentStep{
		ID: "step-capability", RunID: "run-1",
		Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		CapabilityName: "schedule.update", WorkerID: "worker-1", AttemptCount: 1,
		InputJSON: `{
			"version":1,
			"source_step_id":"step-card-action",
			"interaction_id":"interaction-1",
			"action_id":"confirm",
			"descriptor":{
				"action_id":"confirm",
				"mode":"capability_confirm",
				"intent":"schedule.update",
				"continue_agent":true,
				"capability_name":"schedule.update",
				"capability_version":"v1",
				"capability_input":{"task_id":"task-trusted"},
				"idempotency_key":"cardcap_123",
				"permission_context":{"scope":"schedule.update","chat_id":"chat-1","actor_open_id":"owner-1"},
				"actor_policy":{"mode":"owner","open_id":"owner-1"},
				"result_projection_policy":{
					"continue_agent":true,
					"success_surface_status":"resolved",
					"failure_surface_status":"failed"
				}
			}
		}`,
	}
}
