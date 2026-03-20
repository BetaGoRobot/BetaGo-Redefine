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

type fakeRuntimeApprovalRequestLoader struct {
	request *agentruntime.ApprovalRequest
	err     error
}

func (f *fakeRuntimeApprovalRequestLoader) LoadApprovalRequest(ctx context.Context, runID, stepID string) (*agentruntime.ApprovalRequest, error) {
	return f.request, f.err
}

func TestDispatchRoutesAgentRuntimeResumeAction(t *testing.T) {
	prev := buildAgentRuntimeResumeDispatcher
	fake := &fakeRuntimeResumeDispatcher{}
	buildAgentRuntimeResumeDispatcher = func(context.Context) agentRuntimeResumeDispatcher {
		return fake
	}
	defer func() { buildAgentRuntimeResumeDispatcher = prev }()

	resp, err := Dispatch(context.Background(), newEvent(cardactionproto.ActionAgentRuntimeResume, map[string]any{
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

func TestDispatchApprovalResumeReturnsResolvedCard(t *testing.T) {
	prevDispatcher := buildAgentRuntimeResumeDispatcher
	prevLoader := buildAgentRuntimeApprovalRequestLoader
	fakeDispatcher := &fakeRuntimeResumeDispatcher{}
	fakeLoader := &fakeRuntimeApprovalRequestLoader{
		request: &agentruntime.ApprovalRequest{
			RunID:          "run_approval",
			StepID:         "step_approval",
			Revision:       4,
			ApprovalType:   "side_effect",
			Title:          "审批发送消息",
			Summary:        "将向群里发送一条消息",
			CapabilityName: "send_message",
			Token:          "approval_token",
			ExpiresAt:      time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),
		},
	}
	buildAgentRuntimeResumeDispatcher = func(context.Context) agentRuntimeResumeDispatcher {
		return fakeDispatcher
	}
	buildAgentRuntimeApprovalRequestLoader = func(context.Context) agentRuntimeApprovalRequestLoader {
		return fakeLoader
	}
	defer func() {
		buildAgentRuntimeResumeDispatcher = prevDispatcher
		buildAgentRuntimeApprovalRequestLoader = prevLoader
	}()

	resp, err := Dispatch(context.Background(), newEvent(cardactionproto.ActionAgentRuntimeResume, map[string]any{
		cardactionproto.RunIDField:    "run_approval",
		cardactionproto.StepIDField:   "step_approval",
		cardactionproto.RevisionField: "4",
		cardactionproto.SourceField:   string(agentruntime.ResumeSourceApproval),
		cardactionproto.TokenField:    "approval_token",
	}), &xhandler.BaseMetaData{OpenID: "ou_operator"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Card == nil || resp.Card.Data == nil {
		t.Fatalf("expected resolved approval card response, got %+v", resp)
	}
}

func TestDispatchRoutesAgentRuntimeRejectAction(t *testing.T) {
	prev := buildAgentRuntimeApprovalRejector
	prevLoader := buildAgentRuntimeApprovalRequestLoader
	fake := &fakeRuntimeApprovalRejector{}
	fakeLoader := &fakeRuntimeApprovalRequestLoader{
		request: &agentruntime.ApprovalRequest{
			RunID:          "run_43",
			StepID:         "step_43",
			Revision:       5,
			ApprovalType:   "side_effect",
			Title:          "审批发送消息",
			Summary:        "将向群里发送一条消息",
			CapabilityName: "send_message",
			Token:          "approval_token",
			ExpiresAt:      time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),
		},
	}
	buildAgentRuntimeApprovalRejector = func(context.Context) agentRuntimeApprovalRejector {
		return fake
	}
	buildAgentRuntimeApprovalRequestLoader = func(context.Context) agentRuntimeApprovalRequestLoader {
		return fakeLoader
	}
	defer func() {
		buildAgentRuntimeApprovalRejector = prev
		buildAgentRuntimeApprovalRequestLoader = prevLoader
	}()

	resp, err := Dispatch(context.Background(), newEvent(cardactionproto.ActionAgentRuntimeReject, map[string]any{
		cardactionproto.RunIDField:    "run_43",
		cardactionproto.StepIDField:   "step_43",
		cardactionproto.RevisionField: "5",
		cardactionproto.SourceField:   string(agentruntime.ResumeSourceApproval),
		cardactionproto.TokenField:    "approval_token",
	}), &xhandler.BaseMetaData{OpenID: "ou_reviewer"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp == nil || resp.Card == nil || resp.Card.Data == nil {
		t.Fatalf("expected resolved approval card response for reject, got %+v", resp)
	}
	if fake.seen.RunID != "run_43" || fake.seen.StepID != "step_43" || fake.seen.Revision != 5 || fake.seen.Source != agentruntime.ResumeSourceApproval || fake.seen.Token != "approval_token" || fake.seen.ActorOpenID != "ou_reviewer" {
		t.Fatalf("unexpected runtime reject event: %+v", fake.seen)
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
