package cardaction

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type continuationDispatcherFake struct {
	canHandle bool
	response  *callback.CardActionTriggerResponse
	err       error

	mu            sync.Mutex
	canHandleCall int
	dispatchCall  int
	request       ContinuationRequest
}

func (f *continuationDispatcherFake) CanHandle(action *cardactionproto.Parsed) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canHandleCall++
	return f.canHandle && action != nil
}

func (f *continuationDispatcherFake) Dispatch(_ context.Context, request ContinuationRequest) (*callback.CardActionTriggerResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatchCall++
	f.request = request
	return f.response, f.err
}

func (f *continuationDispatcherFake) snapshot() (int, int, ContinuationRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canHandleCall, f.dispatchCall, f.request
}

func registerSyncForTest(t *testing.T, action string, handler SyncHandler) {
	t.Helper()
	RegisterSync(action, handler)
	cleanupRegisteredAction(t, action)
}

func registerAsyncForTest(t *testing.T, action string, handler AsyncHandler) {
	t.Helper()
	RegisterAsync(action, handler)
	cleanupRegisteredAction(t, action)
}

func cleanupRegisteredAction(t *testing.T, action string) {
	t.Helper()
	t.Cleanup(func() {
		defaultRegistry.mu.Lock()
		defer defaultRegistry.mu.Unlock()
		delete(defaultRegistry.handlers, action)
	})
}

func TestRegistryTestRegistrationCleanup(t *testing.T) {
	actionName := uniqueActionName("cleanup")

	t.Run("registered during test", func(t *testing.T) {
		registerSyncForTest(t, actionName, func(context.Context, *Context) (*callback.CardActionTriggerResponse, error) {
			return nil, nil
		})
		if _, ok := defaultRegistry.handler(actionName); !ok {
			t.Fatalf("handler %q is not registered", actionName)
		}
	})

	if _, ok := defaultRegistry.handler(actionName); ok {
		t.Fatalf("handler %q leaked after test cleanup", actionName)
	}
}

func TestDispatchWithOptionsPrefersRuntimeContinuationOverV1(t *testing.T) {
	actionName := uniqueActionName("runtime_precedence")
	v1Called := false
	registerSyncForTest(t, actionName, func(context.Context, *Context) (*callback.CardActionTriggerResponse, error) {
		v1Called = true
		return InfoToast("legacy"), nil
	})

	event := runtimeActionEvent(actionName)
	meta := &xhandler.BaseMetaData{ChatID: "chat-1", OpenID: "user-1"}
	want := InfoToast("continued")
	continuation := &continuationDispatcherFake{canHandle: true, response: want}

	got, err := DispatchWithOptions(context.Background(), event, meta, DispatchOptions{
		Continuation: continuation,
	})
	if err != nil {
		t.Fatalf("DispatchWithOptions() error = %v", err)
	}
	if got != want {
		t.Fatalf("DispatchWithOptions() response = %#v, want %#v", got, want)
	}
	if v1Called {
		t.Fatal("V1 handler called before runtime continuation")
	}

	canHandleCalls, dispatchCalls, request := continuation.snapshot()
	if canHandleCalls != 1 || dispatchCalls != 1 {
		t.Fatalf("continuation calls = CanHandle %d, Dispatch %d; want 1, 1", canHandleCalls, dispatchCalls)
	}
	if request.Event != event || request.Meta != meta {
		t.Fatalf("continuation request = %#v, want original event/meta", request)
	}
	if request.Action == nil || request.Action.Name != actionName {
		t.Fatalf("continuation parsed action = %#v, want name %q", request.Action, actionName)
	}
}

func TestDispatchWithOptionsFallsBackToV1WhenContinuationCannotHandle(t *testing.T) {
	actionName := uniqueActionName("runtime_fallback")
	want := InfoToast("legacy")
	registerSyncForTest(t, actionName, func(context.Context, *Context) (*callback.CardActionTriggerResponse, error) {
		return want, nil
	})
	continuation := &continuationDispatcherFake{canHandle: false}

	got, err := DispatchWithOptions(context.Background(), runtimeActionEvent(actionName), nil, DispatchOptions{
		Continuation: continuation,
	})
	if err != nil {
		t.Fatalf("DispatchWithOptions() error = %v", err)
	}
	if got != want {
		t.Fatalf("DispatchWithOptions() response = %#v, want V1 response %#v", got, want)
	}
	_, dispatchCalls, _ := continuation.snapshot()
	if dispatchCalls != 0 {
		t.Fatalf("continuation Dispatch() calls = %d, want 0", dispatchCalls)
	}
}

func TestDispatchWithOptionsLegacyPayloadUsesV1(t *testing.T) {
	actionName := uniqueActionName("legacy")
	want := InfoToast("legacy")
	registerSyncForTest(t, actionName, func(context.Context, *Context) (*callback.CardActionTriggerResponse, error) {
		return want, nil
	})

	got, err := DispatchWithOptions(context.Background(), actionEvent(map[string]any{
		cardactionproto.ActionField: actionName,
	}), nil, DispatchOptions{})
	if err != nil {
		t.Fatalf("DispatchWithOptions() error = %v", err)
	}
	if got != want {
		t.Fatalf("DispatchWithOptions() response = %#v, want %#v", got, want)
	}
}

func TestDispatchWithOptionsReturnsContinuationErrorUnchanged(t *testing.T) {
	actionName := uniqueActionName("runtime_error")
	registerSyncForTest(t, actionName, func(context.Context, *Context) (*callback.CardActionTriggerResponse, error) {
		t.Fatal("V1 handler must not run after continuation error")
		return nil, nil
	})
	wantErr := errors.New("continuation unavailable")
	continuation := &continuationDispatcherFake{canHandle: true, err: wantErr}

	_, err := DispatchWithOptions(context.Background(), runtimeActionEvent(actionName), nil, DispatchOptions{
		Continuation: continuation,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("DispatchWithOptions() error = %v, want %v", err, wantErr)
	}
}

func TestDispatchPreservesAsyncV1Behavior(t *testing.T) {
	actionName := uniqueActionName("async")
	taskStarted := make(chan struct{}, 1)
	registerAsyncForTest(t, actionName, func(context.Context, *Context) (AsyncTask, error) {
		return func(context.Context) {
			taskStarted <- struct{}{}
		}, nil
	})

	response, err := Dispatch(context.Background(), actionEvent(map[string]any{
		cardactionproto.ActionField: actionName,
	}), nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if response != nil {
		t.Fatalf("Dispatch() response = %#v, want nil for async handler", response)
	}
	select {
	case <-taskStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for V1 async task")
	}
}

func uniqueActionName(suffix string) string {
	return "test.continuation." + suffix + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func runtimeActionEvent(actionName string) *callback.CardActionTriggerEvent {
	return actionEvent(map[string]any{
		cardactionproto.ActionField:          actionName,
		cardactionproto.RunIDField:           "run-1",
		cardactionproto.StepIDField:          "step-1",
		cardactionproto.InteractionIDField:   "interaction-1",
		cardactionproto.RevisionField:        "2",
		cardactionproto.TokenField:           "opaque-token",
		cardactionproto.InteractionKindField: "capability_confirm",
		cardactionproto.ContinueAgentField:   "true",
	})
}

func actionEvent(value map[string]any) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Action: &callback.CallBackAction{Value: value},
		},
	}
}
