package agentruntime

import (
	"context"
	"iter"
	"reflect"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestHandleAgenticChatResponseDelegatesToRuntimeCutover(t *testing.T) {
	originalCutoverBuilder := runtimeAgenticCutoverBuilder
	originalCardSender := runtimeAgenticCardSender
	defer func() {
		runtimeAgenticCutoverBuilder = originalCutoverBuilder
		runtimeAgenticCardSender = originalCardSender
		SetChatGenerationPlanExecutor(nil)
	}()

	fakeCutover := &fakeRuntimeAgenticCutover{}
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}},
		),
	}
	runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler {
		return fakeCutover
	}
	SetChatGenerationPlanExecutor(fakeExecutor)
	runtimeAgenticCardSender = func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) error {
		t.Fatal("expected runtime cutover to bypass direct card sender")
		return nil
	}

	event := testChatResponseEvent()
	now := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
	plan := ChatGenerationPlan{
		ModelID:                     "ep-test-agentic",
		Mode:                        appconfig.ChatModeAgentic,
		Size:                        20,
		Files:                       []string{"https://example.com/image.png"},
		Args:                        []string{"帮我总结"},
		EnableDeferredToolCollector: true,
	}
	err := handleAgenticChatResponse(context.Background(), event, plan, now, InitialRunOwnership{})
	if err != nil {
		t.Fatalf("handleAgenticChatResponse() error = %v", err)
	}

	if fakeCutover.request == nil {
		t.Fatal("expected runtime cutover handler to be called")
	}
	if !reflect.DeepEqual(fakeCutover.request.Plan, plan) {
		t.Fatalf("unexpected plan: %+v", fakeCutover.request.Plan)
	}
	if fakeCutover.request.StartedAt != now {
		t.Fatalf("started at = %v, want %v", fakeCutover.request.StartedAt, now)
	}
	if fakeExecutor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", fakeExecutor.calls)
	}
	if !reflect.DeepEqual(fakeExecutor.lastPlan, plan) {
		t.Fatalf("executor plan = %+v, want %+v", fakeExecutor.lastPlan, plan)
	}
	if fakeCutover.drainCount != 2 {
		t.Fatalf("drain count = %d, want 2", fakeCutover.drainCount)
	}
}

func TestHandleAgenticChatResponseDefersGenerationUntilRuntimeHandlerConsumesIt(t *testing.T) {
	originalCutoverBuilder := runtimeAgenticCutoverBuilder
	defer func() {
		runtimeAgenticCutoverBuilder = originalCutoverBuilder
		SetChatGenerationPlanExecutor(nil)
	}()

	fakeCutover := &fakeRuntimeAgenticCutover{skipGenerate: true}
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "noop"}}),
	}
	runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler {
		return fakeCutover
	}
	SetChatGenerationPlanExecutor(fakeExecutor)

	err := handleAgenticChatResponse(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
		Mode:    appconfig.ChatModeAgentic,
		Size:    20,
		Args:    []string{"帮我总结"},
	}, time.Date(2026, 3, 18, 14, 30, 0, 0, time.UTC), InitialRunOwnership{})
	if err != nil {
		t.Fatalf("handleAgenticChatResponse() error = %v", err)
	}
	if fakeExecutor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", fakeExecutor.calls)
	}
}

func TestHandleAgenticChatResponseForcesAgenticPlanModeOnFallbackPath(t *testing.T) {
	originalCutoverBuilder := runtimeAgenticCutoverBuilder
	originalCardSender := runtimeAgenticCardSender
	defer func() {
		runtimeAgenticCutoverBuilder = originalCutoverBuilder
		runtimeAgenticCardSender = originalCardSender
		SetChatGenerationPlanExecutor(nil)
	}()

	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是最终回复"}}),
	}
	SetChatGenerationPlanExecutor(fakeExecutor)
	runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
	runtimeAgenticCardSender = func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) error {
		for range seq {
		}
		return nil
	}

	err := handleAgenticChatResponse(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
	}, time.Date(2026, 3, 18, 14, 31, 0, 0, time.UTC), InitialRunOwnership{})
	if err != nil {
		t.Fatalf("handleAgenticChatResponse() error = %v", err)
	}
	if fakeExecutor.lastPlan.Mode != appconfig.ChatModeAgentic {
		t.Fatalf("executor plan mode = %q, want %q", fakeExecutor.lastPlan.Mode, appconfig.ChatModeAgentic)
	}
}

type fakeRuntimeAgenticCutover struct {
	request      *RuntimeAgenticCutoverRequest
	drainCount   int
	skipGenerate bool
}

func (f *fakeRuntimeAgenticCutover) Handle(ctx context.Context, req RuntimeAgenticCutoverRequest) error {
	reqCopy := req
	f.request = &reqCopy
	if f.skipGenerate {
		return nil
	}
	stream, err := req.Plan.Generate(ctx, req.Event)
	if err != nil {
		return err
	}
	for item := range stream {
		if item != nil {
			f.drainCount++
		}
	}
	return nil
}

func testChatResponseEvent() *larkim.P2MessageReceiveV1 {
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime"
	chatType := "group"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:    &chatID,
				MessageId: &msgID,
				ChatType:  &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}

type fakeChatGenerationPlanExecutor struct {
	calls     int
	lastPlan  ChatGenerationPlan
	lastEvent *larkim.P2MessageReceiveV1
	result    iter.Seq[*ark_dal.ModelStreamRespReasoning]
	err       error
}

func (f *fakeChatGenerationPlanExecutor) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	f.calls++
	f.lastPlan = plan
	f.lastEvent = event
	return f.result, f.err
}

func seqFromItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}
