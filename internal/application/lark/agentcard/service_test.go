package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceSendsDraftReplyAndPersistsMessageID(t *testing.T) {
	surface := serviceSurface(SurfaceStatusDraft)
	binder := &fakeBinder{result: &BindResult{
		Surface: surface, CompiledJSON: json.RawMessage(`{"schema":"2.0"}`),
	}}
	store := &fakeDeliveryStore{}
	client := &fakeSurfaceClient{replyMessageID: "om-card-1"}
	service := NewService(binder, store, client)

	result, err := service.ComposeAndSend(context.Background(), BindRequest{
		IdempotencyKey: "compose-1",
	})
	if err != nil {
		t.Fatalf("ComposeAndSend() error = %v", err)
	}
	if result.MessageID != "om-card-1" ||
		result.Status != SurfaceStatusSent ||
		client.replyCalls != 1 || client.createCalls != 0 ||
		store.sentCalls != 1 {
		t.Fatalf(
			"result=%#v client=%#v store=%#v",
			result,
			client,
			store,
		)
	}
}

func TestServiceHandlesDefinitiveAmbiguousAndMissingMessageID(t *testing.T) {
	tests := []struct {
		name          string
		client        *fakeSurfaceClient
		wantErr       error
		wantFailed    int
		wantUncertain int
	}{
		{
			name:    "definitive failure",
			client:  &fakeSurfaceClient{replyErr: errors.New("rejected")},
			wantErr: ErrSurfaceDeliveryFailed, wantFailed: 1,
		},
		{
			name:    "ambiguous timeout",
			client:  &fakeSurfaceClient{replyErr: ErrSurfaceDeliveryAmbiguous},
			wantErr: ErrSurfaceDeliveryPending, wantUncertain: 1,
		},
		{
			name:    "missing message id",
			client:  &fakeSurfaceClient{},
			wantErr: ErrSurfaceDeliveryFailed, wantFailed: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binder := &fakeBinder{result: &BindResult{
				Surface:      serviceSurface(SurfaceStatusDraft),
				CompiledJSON: json.RawMessage(`{"schema":"2.0"}`),
			}}
			store := &fakeDeliveryStore{}
			service := NewService(binder, store, test.client)
			_, err := service.ComposeAndSend(
				context.Background(),
				BindRequest{IdempotencyKey: "compose-1"},
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ComposeAndSend() error = %v, want %v", err, test.wantErr)
			}
			if store.failedCalls != test.wantFailed ||
				store.uncertainCalls != test.wantUncertain {
				t.Fatalf("store = %#v", store)
			}
		})
	}
}

func TestServiceDoesNotResendCompletedComposeReplay(t *testing.T) {
	surface := serviceSurface(SurfaceStatusSent)
	surface.MessageID = "om-existing"
	service := NewService(
		&fakeBinder{result: &BindResult{
			Surface: surface, CompiledJSON: json.RawMessage(`{"schema":"2.0"}`),
		}},
		&fakeDeliveryStore{},
		&fakeSurfaceClient{},
	)
	result, err := service.ComposeAndSend(
		context.Background(),
		BindRequest{IdempotencyKey: "compose-1"},
	)
	if err != nil {
		t.Fatalf("ComposeAndSend() error = %v", err)
	}
	if result.MessageID != "om-existing" {
		t.Fatalf("result = %#v", result)
	}
	client := service.client.(*fakeSurfaceClient)
	if client.replyCalls != 0 || client.createCalls != 0 {
		t.Fatalf("compose replay resent card: %#v", client)
	}
}

func TestServiceCreatesCardWhenThereIsNoReplyTarget(t *testing.T) {
	surface := serviceSurface(SurfaceStatusDraft)
	surface.ReplyToMessageID = ""
	client := &fakeSurfaceClient{createMessageID: "om-created"}
	service := NewService(
		&fakeBinder{result: &BindResult{
			Surface: surface, CompiledJSON: json.RawMessage(`{"schema":"2.0"}`),
		}},
		&fakeDeliveryStore{},
		client,
	)
	if _, err := service.ComposeAndSend(
		context.Background(),
		BindRequest{IdempotencyKey: "compose-1"},
	); err != nil {
		t.Fatalf("ComposeAndSend() error = %v", err)
	}
	if client.createCalls != 1 || client.replyCalls != 0 {
		t.Fatalf("client calls = %#v", client)
	}
}

func TestLifecycleManagerRendersExpiredSurfaceWithoutTokenAndPatches(t *testing.T) {
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	patchStore := &fakePatchStore{}
	client := &fakeSurfaceClient{}
	worker, err := NewPatchWorker(PatchWorkerOptions{
		Store: patchStore, Client: client, WorkerID: "inline-worker",
		LeaseTTL: time.Minute, RetryDelay: time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPatchWorker() error = %v", err)
	}
	lifecycleStore := &fakeLifecycleStore{patchStore: patchStore}
	manager := NewLifecycleManager(
		lifecycleStore,
		&terminalArtifactCompiler{},
		worker,
	)
	surface := serviceSurface(SurfaceStatusSent)
	surface.MessageID = "om-card"
	surface.SpecJSON = `{"version":"agent-card/v1","title":"确认","blocks":[]}`
	result, err := manager.AdvanceAndPatch(
		context.Background(),
		AdvanceSurfaceRequest{
			Surface: surface, To: SurfaceStatusExpired,
			SourceRef: "expiry:interaction-1", OccurredAt: now,
		},
	)
	if err != nil {
		t.Fatalf("AdvanceAndPatch() error = %v", err)
	}
	if result.Status != SurfaceStatusExpired ||
		result.PatchStatus != PatchStatusIdle ||
		client.patchCalls != 1 || patchStore.completeCalls != 1 {
		t.Fatalf(
			"result=%#v client=%#v store=%#v",
			result,
			client,
			patchStore,
		)
	}
	if strings.Contains(
		lifecycleStore.request.CompiledJSONRedacted,
		"token",
	) {
		t.Fatalf(
			"expired compiled artifact contains token: %s",
			lifecycleStore.request.CompiledJSONRedacted,
		)
	}
}

func TestPatchWorkerSchedulesRetryInsteadOfSendingReplacement(t *testing.T) {
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	store := &fakePatchStore{surface: &CardSurface{
		ID: "surface-1", Revision: 2, MessageID: "om-card",
		CompiledJSONRedacted: `{"schema":"2.0","state":"processing"}`,
		PatchStatus:          PatchStatusRunning, PatchAttemptCount: 1,
	}}
	client := &fakeSurfaceClient{patchErr: errors.New("unavailable")}
	worker, err := NewPatchWorker(PatchWorkerOptions{
		Store: store, Client: client, WorkerID: "worker-1",
		Now: func() time.Time { return now }, RetryDelay: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPatchWorker() error = %v", err)
	}
	if err := worker.Process(
		context.Background(),
		"surface-1",
		2,
	); !errors.Is(err, ErrSurfacePatchPending) {
		t.Fatalf("Process() error = %v", err)
	}
	if client.patchCalls != 1 || client.createCalls != 0 ||
		client.replyCalls != 0 || store.retryCalls != 1 ||
		store.completeCalls != 0 {
		t.Fatalf("client=%#v store=%#v", client, store)
	}
}

type fakeBinder struct {
	result *BindResult
	err    error
}

func (b *fakeBinder) BindAndBegin(
	context.Context,
	BindRequest,
) (*BindResult, error) {
	return b.result, b.err
}

type fakeDeliveryStore struct {
	sentCalls      int
	failedCalls    int
	uncertainCalls int
}

func (s *fakeDeliveryStore) MarkSurfaceSent(
	_ context.Context,
	request MarkSurfaceSentRequest,
) (*CardSurface, error) {
	s.sentCalls++
	surface := serviceSurface(SurfaceStatusSent)
	surface.ID = request.SurfaceID
	surface.MessageID = request.MessageID
	return surface, nil
}

func (s *fakeDeliveryStore) MarkSurfaceSendFailed(
	context.Context,
	MarkSurfaceSendFailedRequest,
) (*CardSurface, error) {
	s.failedCalls++
	return serviceSurface(SurfaceStatusFailed), nil
}

func (s *fakeDeliveryStore) MarkSurfaceSendUncertain(
	context.Context,
	MarkSurfaceSendUncertainRequest,
) (*CardSurface, error) {
	s.uncertainCalls++
	return serviceSurface(SurfaceStatusDraft), nil
}

type fakeSurfaceClient struct {
	replyMessageID  string
	createMessageID string
	replyErr        error
	createErr       error
	patchErr        error
	replyCalls      int
	createCalls     int
	patchCalls      int
}

func (c *fakeSurfaceClient) ReplyCard(
	context.Context,
	string,
	any,
) (string, error) {
	c.replyCalls++
	return c.replyMessageID, c.replyErr
}

func (c *fakeSurfaceClient) CreateCard(
	context.Context,
	string,
	any,
) (string, error) {
	c.createCalls++
	return c.createMessageID, c.createErr
}

func (c *fakeSurfaceClient) PatchCard(context.Context, string, any) error {
	c.patchCalls++
	return c.patchErr
}

func serviceSurface(status SurfaceStatus) *CardSurface {
	return &CardSurface{
		ID: "surface-1", RunID: "run-1", WaitStepID: "step-1",
		InteractionID: "interaction-1", ChatID: "chat-1",
		ReplyToMessageID: "om-source", Status: status, Revision: 2,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

type terminalArtifactCompiler struct{}

func (c *terminalArtifactCompiler) CompileJSON(
	bound *BoundCardSpec,
) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"schema": "2.0",
		"state":  bound.State(),
	})
}

func (c *terminalArtifactCompiler) CompileRedactedJSON(
	bound *BoundCardSpec,
) (json.RawMessage, error) {
	return c.CompileJSON(bound)
}

type fakeLifecycleStore struct {
	request    TransitionSurfaceRequest
	patchStore *fakePatchStore
}

func (s *fakeLifecycleStore) TransitionSurface(
	_ context.Context,
	request TransitionSurfaceRequest,
) (*CardSurface, error) {
	s.request = request
	surface := serviceSurface(request.To)
	surface.MessageID = "om-card"
	surface.SpecJSON = `{"version":"agent-card/v1","title":"确认","blocks":[]}`
	surface.CompiledJSONRedacted = request.CompiledJSONRedacted
	surface.PatchStatus = PatchStatusPending
	s.patchStore.surface = surface
	return surface, nil
}

type fakePatchStore struct {
	surface       *CardSurface
	completeCalls int
	retryCalls    int
}

func (s *fakePatchStore) ClaimPatch(
	context.Context,
	ClaimPatchRequest,
) (*CardSurface, error) {
	if s.surface == nil {
		return nil, ErrCardNotFound
	}
	copy := *s.surface
	copy.PatchStatus = PatchStatusRunning
	if copy.PatchAttemptCount == 0 {
		copy.PatchAttemptCount = 1
	}
	return &copy, nil
}

func (s *fakePatchStore) CompletePatch(
	context.Context,
	CompletePatchRequest,
) error {
	s.completeCalls++
	return nil
}

func (s *fakePatchStore) RetryPatch(
	context.Context,
	RetryPatchRequest,
) error {
	s.retryCalls++
	return nil
}
