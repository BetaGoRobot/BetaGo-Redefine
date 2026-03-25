package cardaction

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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

func TestDispatchAsyncDetachesCancellation(t *testing.T) {
	reg := &registry{handlers: make(map[string]entry)}
	done := make(chan error, 1)
	reg.register(cardactionproto.ActionMusicRefresh, entry{
		mode: ModeAsync,
		async: func(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
			return func(taskCtx context.Context) {
				done <- taskCtx.Err()
			}, nil
		},
	})

	prev := defaultRegistry
	defaultRegistry = reg
	defer func() { defaultRegistry = prev }()

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := Dispatch(parentCtx, newEvent(cardactionproto.ActionMusicRefresh, nil), nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil inline response for async handler")
	}

	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("expected detached async context, got err %v", got)
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

type fakeRuntimeResumeDispatcher struct {
	seen agentruntime.ResumeEvent
	err  error
}

func (f *fakeRuntimeResumeDispatcher) Dispatch(ctx context.Context, event agentruntime.ResumeEvent) (*agentruntime.AgentRun, error) {
	f.seen = event
	return &agentruntime.AgentRun{ID: event.RunID}, f.err
}

type fakeRuntimeApprovalRejector struct {
	seen agentruntime.ResumeEvent
	err  error
}

func (f *fakeRuntimeApprovalRejector) RejectApproval(ctx context.Context, event agentruntime.ResumeEvent) (*agentruntime.AgentRun, error) {
	f.seen = event
	return &agentruntime.AgentRun{ID: event.RunID}, f.err
}

func TestDispatchRoutesAgentRuntimeResumeAction(t *testing.T) {
	fake := &fakeRuntimeResumeDispatcher{}
	ctx := withAgentRuntimeActionDeps(context.Background(), agentRuntimeActionDeps{
		resumeDispatcher: fake,
	})

	resp, err := Dispatch(ctx, newEvent(cardactionproto.ActionAgentRuntimeResume, map[string]any{
		cardactionproto.RunIDField:    "run_42",
		cardactionproto.RevisionField: "3",
		cardactionproto.SourceField:   string(agentruntime.ResumeSourceCallback),
		cardactionproto.TokenField:    "cb_token",
	}), &xhandler.BaseMetaData{OpenID: "ou_operator"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil inline response for runtime resume, got %+v", resp)
	}
	if fake.seen.RunID != "run_42" || fake.seen.Revision != 3 || fake.seen.Source != agentruntime.ResumeSourceCallback || fake.seen.Token != "cb_token" || fake.seen.ActorOpenID != "ou_operator" {
		t.Fatalf("unexpected runtime resume event: %+v", fake.seen)
	}
}

func TestDispatchApprovalResumeWithdrawsApprovalCard(t *testing.T) {
	fakeDispatcher := &fakeRuntimeResumeDispatcher{}
	withdrawnMessageID := ""
	ctx := withAgentRuntimeActionDeps(context.Background(), agentRuntimeActionDeps{
		resumeDispatcher: fakeDispatcher,
		deleteEphemeralApproval: func(ctx context.Context, messageID string) error {
			withdrawnMessageID = messageID
			return nil
		},
		withdrawApproval: withdrawApprovalImmediately,
	})

	resp, err := Dispatch(ctx, newEventWithMessage(cardactionproto.ActionAgentRuntimeResume, map[string]any{
		cardactionproto.RunIDField:    "run_approval",
		cardactionproto.StepIDField:   "step_approval",
		cardactionproto.RevisionField: "4",
		cardactionproto.SourceField:   string(agentruntime.ResumeSourceApproval),
		cardactionproto.TokenField:    "approval_token",
	}, "om_approval_card"), &xhandler.BaseMetaData{OpenID: "ou_operator"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "已批准，审批卡已撤回" {
		t.Fatalf("expected approval withdraw toast response, got %+v", resp)
	}
	if withdrawnMessageID != "om_approval_card" {
		t.Fatalf("withdrawn message id = %q, want %q", withdrawnMessageID, "om_approval_card")
	}
}

func TestDispatchRoutesAgentRuntimeRejectAction(t *testing.T) {
	fake := &fakeRuntimeApprovalRejector{}
	withdrawnMessageID := ""
	ctx := withAgentRuntimeActionDeps(context.Background(), agentRuntimeActionDeps{
		approvalRejector: fake,
		deleteEphemeralApproval: func(ctx context.Context, messageID string) error {
			withdrawnMessageID = messageID
			return nil
		},
		withdrawApproval: withdrawApprovalImmediately,
	})

	resp, err := Dispatch(ctx, newEventWithMessage(cardactionproto.ActionAgentRuntimeReject, map[string]any{
		cardactionproto.RunIDField:    "run_43",
		cardactionproto.StepIDField:   "step_43",
		cardactionproto.RevisionField: "5",
		cardactionproto.SourceField:   string(agentruntime.ResumeSourceApproval),
		cardactionproto.TokenField:    "approval_token",
	}, "om_reject_approval_card"), &xhandler.BaseMetaData{OpenID: "ou_reviewer"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "已拒绝，审批卡已撤回" {
		t.Fatalf("expected approval reject withdraw toast response, got %+v", resp)
	}
	if withdrawnMessageID != "om_reject_approval_card" {
		t.Fatalf("withdrawn message id = %q, want %q", withdrawnMessageID, "om_reject_approval_card")
	}
	if fake.seen.RunID != "run_43" || fake.seen.StepID != "step_43" || fake.seen.Revision != 5 || fake.seen.Source != agentruntime.ResumeSourceApproval || fake.seen.Token != "approval_token" || fake.seen.ActorOpenID != "ou_reviewer" {
		t.Fatalf("unexpected runtime reject event: %+v", fake.seen)
	}
}

func TestDispatchApprovalResumeWithdrawsMessageApprovalCardWhenFallbackDeliveryWasUsed(t *testing.T) {
	fakeDispatcher := &fakeRuntimeResumeDispatcher{}
	ephemeralDeletes := 0
	messageDeletedID := ""
	ctx := withAgentRuntimeActionDeps(context.Background(), agentRuntimeActionDeps{
		resumeDispatcher: fakeDispatcher,
		deleteEphemeralApproval: func(context.Context, string) error {
			ephemeralDeletes++
			return nil
		},
		deleteMessageApproval: func(ctx context.Context, messageID string) error {
			messageDeletedID = messageID
			return nil
		},
		withdrawApproval: withdrawApprovalImmediately,
	})

	resp, err := Dispatch(ctx, newEventWithMessage(cardactionproto.ActionAgentRuntimeResume, map[string]any{
		cardactionproto.RunIDField:            "run_approval",
		cardactionproto.StepIDField:           "step_approval",
		cardactionproto.RevisionField:         "4",
		cardactionproto.SourceField:           string(agentruntime.ResumeSourceApproval),
		cardactionproto.TokenField:            "approval_token",
		cardactionproto.ApprovalDeliveryField: string(agentruntime.ApprovalCardDeliveryMessage),
	}, "om_standard_approval_card"), &xhandler.BaseMetaData{OpenID: "ou_operator"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "已批准，审批卡已撤回" {
		t.Fatalf("expected approval withdraw toast response, got %+v", resp)
	}
	if messageDeletedID != "om_standard_approval_card" {
		t.Fatalf("message delete id = %q, want %q", messageDeletedID, "om_standard_approval_card")
	}
	if ephemeralDeletes != 0 {
		t.Fatalf("ephemeral delete calls = %d, want 0", ephemeralDeletes)
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

func newEventWithMessage(action string, extra map[string]any, messageID string) *callback.CardActionTriggerEvent {
	event := newEvent(action, extra)
	event.Event.Context = &callback.Context{
		OpenMessageID: messageID,
	}
	return event
}
