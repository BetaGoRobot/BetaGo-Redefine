package cardaction

import (
	"context"
	"testing"
	"time"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestCardJSONHelpersReturnRawCallbackCards(t *testing.T) {
	cardData := map[string]any{"schema": "2.0"}

	resp := InfoToastWithRawCardPayload("ok", cardData)
	if resp == nil || resp.Card == nil {
		t.Fatalf("expected card response")
	}
	if resp.Card.Type != "raw" {
		t.Fatalf("expected raw callback card type, got %q", resp.Card.Type)
	}

	resp = RawCardPayloadOnly(cardData)
	if resp == nil || resp.Card == nil {
		t.Fatalf("expected card response")
	}
	if resp.Card.Type != "raw" {
		t.Fatalf("expected raw callback card type, got %q", resp.Card.Type)
	}
}

func TestDispatchAsyncRunsTaskWithoutInlineResponse(t *testing.T) {
	reg := &registry{handlers: make(map[string]entry)}
	done := make(chan string, 1)
	reg.register(cardactionproto.ActionMusicPlay, entry{
		mode: ModeAsync,
		async: func(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
			id, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
			if err != nil {
				return nil, err
			}
			return func(context.Context) {
				done <- id
			}, nil
		},
	})

	prev := defaultRegistry
	defaultRegistry = reg
	defer func() { defaultRegistry = prev }()

	resp, err := Dispatch(context.Background(), newEvent(cardactionproto.ActionMusicPlay, map[string]any{
		cardactionproto.IDField: "42",
	}), nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil inline response for async handler")
	}

	select {
	case got := <-done:
		if got != "42" {
			t.Fatalf("expected task payload 42, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected async task to run")
	}
}

func TestDispatchSyncReturnsResponseInline(t *testing.T) {
	reg := &registry{handlers: make(map[string]entry)}
	reg.register(cardactionproto.ActionConfigViewScope, entry{
		mode: ModeSync,
		sync: func(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
			return InfoToast("ok"), nil
		},
	})

	prev := defaultRegistry
	defaultRegistry = reg
	defer func() { defaultRegistry = prev }()

	resp, err := Dispatch(context.Background(), newEvent(cardactionproto.ActionConfigViewScope, nil), nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "ok" {
		t.Fatalf("expected inline toast response, got %+v", resp)
	}
}

func newEvent(action string, extra map[string]any) *callback.CardActionTriggerEvent {
	value := map[string]any{
		cardactionproto.ActionField: action,
	}
	for k, v := range extra {
		value[k] = v
	}

	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Action: &callback.CallBackAction{
				Value: value,
			},
		},
	}
}
