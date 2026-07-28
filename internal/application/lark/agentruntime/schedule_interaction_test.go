package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleEditTrustedInputRoundTrip(t *testing.T) {
	runAt := time.Date(2026, 8, 1, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	req := StartScheduleEditRequest{
		TaskID:          "task-1",
		ActorOpenID:     "ou-actor",
		ChatID:          "oc-chat",
		SourceMessageID: "om-source",
		NewValues: map[string]any{
			"name":            "新名称",
			"cron_expr":       "0 10 * * *",
			"timezone":        "Asia/Shanghai",
			"run_at":          runAt,
			"message":         "新消息",
			"notify_on_error": true,
			"notify_result":   false,
			"skip_holidays":   true,
		},
	}

	raw, err := EncodeScheduleEditTrustedInput(req)
	if err != nil {
		t.Fatalf("EncodeScheduleEditTrustedInput() error = %v", err)
	}
	got, err := DecodeScheduleEditTrustedInput(raw)
	if err != nil {
		t.Fatalf("DecodeScheduleEditTrustedInput() error = %v", err)
	}
	if got.Version != 1 || got.TaskID != req.TaskID || got.InitiatorOpenID != req.ActorOpenID ||
		got.ChatID != req.ChatID || got.SourceMessageID != req.SourceMessageID {
		t.Fatalf("trusted metadata = %#v", got)
	}
	wantValues := ScheduleEditValues{
		Name:          stringPointer("新名称"),
		CronExpr:      stringPointer("0 10 * * *"),
		Timezone:      stringPointer("Asia/Shanghai"),
		RunAt:         &runAt,
		Message:       stringPointer("新消息"),
		NotifyOnError: boolPointer(true),
		NotifyResult:  boolPointer(false),
		SkipHolidays:  boolPointer(true),
	}
	if got.NewValues.RunAt == nil || !got.NewValues.RunAt.Equal(runAt) {
		t.Fatalf("trusted run_at = %v, want same instant as %v", got.NewValues.RunAt, runAt)
	}
	got.NewValues.RunAt = nil
	wantValues.RunAt = nil
	if !reflect.DeepEqual(got.NewValues, wantValues) {
		t.Fatalf("trusted values = %#v, want %#v", got.NewValues, wantValues)
	}
}

func TestEncodeScheduleEditTrustedInputRejectsUnsupportedOrEmptyValues(t *testing.T) {
	tests := []StartScheduleEditRequest{
		{TaskID: "task-1", ActorOpenID: "ou-actor", ChatID: "oc-chat", NewValues: map[string]any{}},
		{TaskID: "task-1", ActorOpenID: "ou-actor", ChatID: "oc-chat", NewValues: map[string]any{"unknown": "value"}},
		{TaskID: "task-1", ActorOpenID: "ou-actor", ChatID: "oc-chat", NewValues: map[string]any{"name": 42}},
	}
	for _, req := range tests {
		if _, err := EncodeScheduleEditTrustedInput(req); err == nil {
			t.Fatal("EncodeScheduleEditTrustedInput() error = nil, want invalid trusted input")
		}
	}
}

func TestDecodeScheduleEditTrustedInputRejectsTrailingJSON(t *testing.T) {
	raw := append(mustScheduleTrustedInput(t), []byte(`{"extra":true}`)...)
	if _, err := DecodeScheduleEditTrustedInput(raw); err == nil {
		t.Fatal("DecodeScheduleEditTrustedInput() accepted trailing JSON")
	}
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

type scheduleInteractionStoreFake struct {
	mu            sync.Mutex
	trusted       json.RawMessage
	outcome       ScheduleInteractionOutcome
	claimed       bool
	completed     bool
	inspectCalls  int
	claimCalls    int
	completeCalls int
	failCalls     int
	resolvedActor string
}

func (s *scheduleInteractionStoreFake) InspectScheduleInteraction(
	context.Context,
	ScheduleInteractionRequest,
) (ScheduleInteractionInspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inspectCalls++
	inspection := ScheduleInteractionInspection{
		TrustedInput: append(json.RawMessage(nil), s.trusted...),
	}
	if s.completed {
		outcome := s.outcome
		inspection.CompletedOutcome = &outcome
		inspection.ResolvedActorOpenID = s.resolvedActor
	}
	return inspection, nil
}

func (s *scheduleInteractionStoreFake) ClaimScheduleInteraction(
	_ context.Context,
	req ScheduleInteractionRequest,
) (ScheduleInteractionClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	switch {
	case s.completed:
		return ScheduleInteractionClaim{
			State: ScheduleClaimCompleted, Outcome: s.outcome,
			ResolvedActorOpenID: s.resolvedActor,
		}, nil
	case s.claimed:
		return ScheduleInteractionClaim{State: ScheduleClaimRunning}, nil
	default:
		s.claimed = true
		s.resolvedActor = req.ActorOpenID
		return ScheduleInteractionClaim{State: ScheduleClaimAcquired, TrustedInput: s.trusted}, nil
	}
}

func (s *scheduleInteractionStoreFake) CompleteScheduleInteraction(
	_ context.Context,
	req CompleteScheduleInteractionRequest,
) (ScheduleInteractionOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeCalls++
	s.completed = true
	s.claimed = false
	s.outcome = req.Outcome
	return req.Outcome, nil
}

func (s *scheduleInteractionStoreFake) FailScheduleInteraction(
	context.Context,
	FailScheduleInteractionRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCalls++
	s.claimed = false
	return nil
}

type scheduleEditCapabilityFake struct {
	validateErr   error
	executeErr    error
	validateCalls atomic.Int32
	executeCalls  atomic.Int32
	started       chan struct{}
	release       chan struct{}
}

func (f *scheduleEditCapabilityFake) ValidateScheduleEdit(
	context.Context,
	string,
	ScheduleEditTrustedInput,
) error {
	f.validateCalls.Add(1)
	return f.validateErr
}

func (f *scheduleEditCapabilityFake) ExecuteScheduleEdit(
	context.Context,
	string,
	ScheduleEditTrustedInput,
) (json.RawMessage, error) {
	f.executeCalls.Add(1)
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		<-f.release
	}
	return json.RawMessage(`{"status":"updated"}`), f.executeErr
}

type runSubmitterFake struct {
	calls  atomic.Int32
	mu     sync.Mutex
	errors []error
}

func (f *runSubmitterFake) SubmitRun(context.Context, string) error {
	call := int(f.calls.Add(1))
	f.mu.Lock()
	defer f.mu.Unlock()
	if call <= len(f.errors) {
		return f.errors[call-1]
	}
	return nil
}

func TestScheduleInteractionServiceValidatesPermissionBeforeClaim(t *testing.T) {
	trusted := mustScheduleTrustedInput(t)
	store := &scheduleInteractionStoreFake{trusted: trusted}
	permissionErr := errors.New("permission denied")
	capability := &scheduleEditCapabilityFake{validateErr: permissionErr}
	service := NewScheduleInteractionService(store, capability, &runSubmitterFake{})

	_, err := service.Resolve(context.Background(), validScheduleInteractionRequest())

	if !errors.Is(err, permissionErr) {
		t.Fatalf("Resolve() error = %v, want permission error", err)
	}
	if store.claimCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("store mutations = claim:%d complete:%d, want zero", store.claimCalls, store.completeCalls)
	}
}

func TestScheduleInteractionServiceSameActorCompletedReplayBypassesChangedPermissionAndWakes(t *testing.T) {
	trusted := mustScheduleTrustedInput(t)
	store := &scheduleInteractionStoreFake{trusted: trusted}
	capability := &scheduleEditCapabilityFake{}
	submitter := &runSubmitterFake{}
	service := NewScheduleInteractionService(store, capability, submitter)
	req := validScheduleInteractionRequest()

	first, err := service.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve(first) error = %v", err)
	}
	capability.validateErr = errors.New("task state changed after completion")
	replay, err := service.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve(replay) error = %v", err)
	}

	if first.Status != "updated" || replay.Status != first.Status {
		t.Fatalf("outcomes = first:%#v replay:%#v", first, replay)
	}
	if capability.executeCalls.Load() != 1 || store.completeCalls != 1 {
		t.Fatalf("execute=%d complete=%d, want 1/1", capability.executeCalls.Load(), store.completeCalls)
	}
	if capability.validateCalls.Load() != 1 {
		t.Fatalf("validate calls = %d, want completed replay to bypass changed validator", capability.validateCalls.Load())
	}
	if submitter.calls.Load() != 2 {
		t.Fatalf("submit calls = %d, want idempotent wake on every same-actor replay", submitter.calls.Load())
	}
}

func TestScheduleInteractionServiceDifferentActorCompletedReplayRequiresPermission(t *testing.T) {
	tests := []struct {
		name          string
		permissionErr error
		wantErr       error
	}{
		{name: "unauthorized", permissionErr: errors.New("permission denied"), wantErr: errors.New("permission denied")},
		{name: "authorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trusted := mustScheduleTrustedInput(t)
			store := &scheduleInteractionStoreFake{
				trusted: trusted, completed: true, resolvedActor: "ou-original",
				outcome: ScheduleInteractionOutcome{
					Status: "updated", TaskID: "task-1", InteractionID: "interaction-1",
					Action: ScheduleInteractionConfirm, Result: json.RawMessage(`{"status":"updated"}`),
				},
			}
			capability := &scheduleEditCapabilityFake{validateErr: tt.permissionErr}
			submitter := &runSubmitterFake{}
			service := NewScheduleInteractionService(store, capability, submitter)
			req := validScheduleInteractionRequest()
			req.ActorOpenID = "ou-different"

			outcome, err := service.Resolve(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("Resolve() error = %v, want %v", err, tt.wantErr)
				}
				if outcome.Status != "" {
					t.Fatalf("unauthorized outcome = %#v", outcome)
				}
			} else {
				if err != nil || outcome.Status != "updated" {
					t.Fatalf("authorized replay = %#v, %v", outcome, err)
				}
			}
			if capability.validateCalls.Load() != 1 ||
				capability.executeCalls.Load() != 0 ||
				store.claimCalls != 0 || store.completeCalls != 0 ||
				submitter.calls.Load() != 0 {
				t.Fatalf("side effects validate=%d execute=%d claim=%d complete=%d wake=%d",
					capability.validateCalls.Load(), capability.executeCalls.Load(),
					store.claimCalls, store.completeCalls, submitter.calls.Load())
			}
		})
	}
}

func TestScheduleInteractionServiceRetriesIdempotentWakeAfterCommit(t *testing.T) {
	trusted := mustScheduleTrustedInput(t)
	store := &scheduleInteractionStoreFake{trusted: trusted}
	capability := &scheduleEditCapabilityFake{}
	wakeErr := errors.New("wake queue unavailable")
	submitter := &runSubmitterFake{errors: []error{wakeErr, nil, nil}}
	service := NewScheduleInteractionService(store, capability, submitter)
	req := validScheduleInteractionRequest()

	if _, err := service.Resolve(context.Background(), req); !errors.Is(err, wakeErr) {
		t.Fatalf("Resolve(first) error = %v, want wake failure", err)
	}
	if !store.completed {
		t.Fatal("wake failure rolled back an already committed outcome")
	}
	second, err := service.Resolve(context.Background(), req)
	if err != nil || second.Status != "updated" {
		t.Fatalf("Resolve(second) = %#v, %v", second, err)
	}
	third, err := service.Resolve(context.Background(), req)
	if err != nil || third.Status != "updated" {
		t.Fatalf("Resolve(third) = %#v, %v", third, err)
	}
	if capability.executeCalls.Load() != 1 || store.completeCalls != 1 ||
		submitter.calls.Load() != 3 {
		t.Fatalf("execute=%d complete=%d wake=%d, want 1/1/3",
			capability.executeCalls.Load(), store.completeCalls, submitter.calls.Load())
	}
}

func TestScheduleInteractionServiceCancelSkipsExecution(t *testing.T) {
	store := &scheduleInteractionStoreFake{trusted: mustScheduleTrustedInput(t)}
	capability := &scheduleEditCapabilityFake{}
	service := NewScheduleInteractionService(store, capability, &runSubmitterFake{})
	req := validScheduleInteractionRequest()
	req.Action = ScheduleInteractionCancel

	outcome, err := service.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve(cancel) error = %v", err)
	}
	if outcome.Status != "cancelled_by_user" {
		t.Fatalf("cancel status = %q, want cancelled_by_user", outcome.Status)
	}
	if capability.executeCalls.Load() != 0 {
		t.Fatalf("cancel execute calls = %d, want 0", capability.executeCalls.Load())
	}
}

func TestScheduleInteractionServiceTreatsTypedNilSubmitterAsAbsent(t *testing.T) {
	store := &scheduleInteractionStoreFake{trusted: mustScheduleTrustedInput(t)}
	capability := &scheduleEditCapabilityFake{}
	var submitter *runSubmitterFake
	service := NewScheduleInteractionService(store, capability, submitter)

	if _, err := service.Resolve(context.Background(), validScheduleInteractionRequest()); err != nil {
		t.Fatalf("Resolve(typed nil submitter) error = %v", err)
	}
}

func TestScheduleInteractionServiceConcurrentConfirmExecutesOnce(t *testing.T) {
	store := &scheduleInteractionStoreFake{trusted: mustScheduleTrustedInput(t)}
	capability := &scheduleEditCapabilityFake{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	service := NewScheduleInteractionService(store, capability, &runSubmitterFake{})
	req := validScheduleInteractionRequest()
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Resolve(context.Background(), req)
		firstDone <- err
	}()
	<-capability.started

	_, loserErr := service.Resolve(context.Background(), req)
	close(capability.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("winner Resolve() error = %v", err)
	}

	if !errors.Is(loserErr, ErrScheduleInteractionRunning) {
		t.Fatalf("loser Resolve() error = %v, want ErrScheduleInteractionRunning", loserErr)
	}
	if capability.executeCalls.Load() != 1 {
		t.Fatalf("execute calls = %d, want 1", capability.executeCalls.Load())
	}
}

func validScheduleInteractionRequest() ScheduleInteractionRequest {
	return ScheduleInteractionRequest{
		RunID: "run-1", StepID: "step-wait-1", InteractionID: "interaction-1",
		Revision: 2, PresentedToken: "opaque-token", ActorOpenID: "ou-actor",
		Action: ScheduleInteractionConfirm, EventID: "event-1",
		ResolvedAt: time.Now().UTC(), ClaimID: "claim-1", RunningTTL: time.Minute,
		Projection: ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: "run-1",
			Payload: json.RawMessage(`{"state":"queued"}`),
		},
	}
}

func mustScheduleTrustedInput(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := EncodeScheduleEditTrustedInput(StartScheduleEditRequest{
		TaskID: "task-1", ActorOpenID: "ou-actor", ChatID: "oc-chat",
		NewValues: map[string]any{"name": "new-name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
