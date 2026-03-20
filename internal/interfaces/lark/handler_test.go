package lark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	otelinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type traceCaptureSubmitter struct {
	mu       sync.Mutex
	traceIDs []oteltrace.TraceID
}

func (s *traceCaptureSubmitter) Submit(ctx context.Context, _ string, _ func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traceIDs = append(s.traceIDs, oteltrace.SpanContextFromContext(ctx).TraceID())
	return nil
}

func (s *traceCaptureSubmitter) Snapshot() []oteltrace.TraceID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]oteltrace.TraceID(nil), s.traceIDs...)
}

type traceCaptureExecutor struct {
	inner *appruntime.Executor

	mu             sync.Mutex
	submitTraceIDs []oteltrace.TraceID
	taskTraceIDs   []oteltrace.TraceID
}

func (s *traceCaptureExecutor) Submit(ctx context.Context, name string, fn func(context.Context) error) error {
	s.mu.Lock()
	s.submitTraceIDs = append(s.submitTraceIDs, oteltrace.SpanContextFromContext(ctx).TraceID())
	s.mu.Unlock()

	return s.inner.Submit(ctx, name, func(taskCtx context.Context) error {
		s.mu.Lock()
		s.taskTraceIDs = append(s.taskTraceIDs, oteltrace.SpanContextFromContext(taskCtx).TraceID())
		s.mu.Unlock()
		return fn(taskCtx)
	})
}

func (s *traceCaptureExecutor) Snapshot() ([]oteltrace.TraceID, []oteltrace.TraceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]oteltrace.TraceID(nil), s.submitTraceIDs...), append([]oteltrace.TraceID(nil), s.taskTraceIDs...)
}

type messageTestOperator struct {
	xhandler.OperatorBase[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	runFn func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) error
}

type messageTestRunner struct {
	runFn func(context.Context, *larkim.P2MessageReceiveV1)
}

func (r *messageTestRunner) Run(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if r != nil && r.runFn != nil {
		r.runFn(ctx, event)
	}
}

type processorMessageRunner struct {
	processor *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
}

func (r *processorMessageRunner) Run(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if r == nil || r.processor == nil {
		return
	}
	r.processor.NewExecution().WithCtx(ctx).WithData(event).Run()
}

func (o *messageTestOperator) Name() string {
	return "message_test_operator"
}

func (o *messageTestOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) error {
	if o.runFn != nil {
		return o.runFn(ctx, event, meta)
	}
	return nil
}

func TestMessageV2HandlerStartsNewRootTracePerCall(t *testing.T) {
	restore := installHandlerTestTracer(t)
	defer restore()

	submitter := &traceCaptureSubmitter{}
	handlerSet := NewHandlerSet(HandlerSetOptions{
		MessageProcessor: &messageTestRunner{},
		MessageExecutor:  submitter,
	})

	parentCtx, parentSpan := otelinfra.StartNamed(context.Background(), "parent")
	defer parentSpan.End()

	if err := handlerSet.MessageV2Handler(parentCtx, newMessageEvent("msg-1")); err != nil {
		t.Fatalf("MessageV2Handler() error = %v", err)
	}
	if err := handlerSet.MessageV2Handler(parentCtx, newMessageEvent("msg-2")); err != nil {
		t.Fatalf("MessageV2Handler() error = %v", err)
	}

	traceIDs := submitter.Snapshot()
	if len(traceIDs) != 2 {
		t.Fatalf("expected 2 trace IDs, got %d", len(traceIDs))
	}
	assertValidTraceID(t, traceIDs[0], "first message trace")
	assertValidTraceID(t, traceIDs[1], "second message trace")
	if traceIDs[0] == traceIDs[1] {
		t.Fatalf("expected independent root traces, got shared trace %s", traceIDs[0])
	}
	if traceIDs[0] == parentSpan.SpanContext().TraceID() || traceIDs[1] == parentSpan.SpanContext().TraceID() {
		t.Fatalf("expected entry traces to ignore parent trace %s", parentSpan.SpanContext().TraceID())
	}
}

func TestMessageAndReactionHandlersUseIndependentRootTraces(t *testing.T) {
	restore := installHandlerTestTracer(t)
	defer restore()

	messageSubmitter := &traceCaptureSubmitter{}
	reactionSubmitter := &traceCaptureSubmitter{}
	handlerSet := NewHandlerSet(HandlerSetOptions{
		MessageProcessor:  &messageTestRunner{},
		ReactionProcessor: &xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]{},
		MessageExecutor:   messageSubmitter,
		ReactionExecutor:  reactionSubmitter,
	})

	parentCtx, parentSpan := otelinfra.StartNamed(context.Background(), "parent")
	defer parentSpan.End()

	if err := handlerSet.MessageV2Handler(parentCtx, newMessageEvent("msg-root")); err != nil {
		t.Fatalf("MessageV2Handler() error = %v", err)
	}
	if err := handlerSet.MessageReactionHandler(parentCtx, newReactionEvent("msg-root")); err != nil {
		t.Fatalf("MessageReactionHandler() error = %v", err)
	}

	messageTraceIDs := messageSubmitter.Snapshot()
	reactionTraceIDs := reactionSubmitter.Snapshot()
	if len(messageTraceIDs) != 1 || len(reactionTraceIDs) != 1 {
		t.Fatalf("unexpected trace capture counts: message=%d reaction=%d", len(messageTraceIDs), len(reactionTraceIDs))
	}
	assertValidTraceID(t, messageTraceIDs[0], "message trace")
	assertValidTraceID(t, reactionTraceIDs[0], "reaction trace")
	if messageTraceIDs[0] == reactionTraceIDs[0] {
		t.Fatalf("expected message/reaction handlers to have distinct root traces, got %s", messageTraceIDs[0])
	}
	if messageTraceIDs[0] == parentSpan.SpanContext().TraceID() || reactionTraceIDs[0] == parentSpan.SpanContext().TraceID() {
		t.Fatalf("expected entry traces to ignore parent trace %s", parentSpan.SpanContext().TraceID())
	}
}

func TestCardActionHandlerStartsNewRootTracePerCall(t *testing.T) {
	restore := installHandlerTestTracer(t)
	defer restore()

	prevMetaBuilder := buildCardActionMetaData
	prevRecorder := recordCardAction
	buildCardActionMetaData = func(context.Context, string, string) *xhandler.BaseMetaData {
		return &xhandler.BaseMetaData{ChatID: "chat-card", OpenID: "user-card"}
	}
	recordCardAction = func(context.Context, *callback.CardActionTriggerEvent) {}
	t.Cleanup(func() {
		buildCardActionMetaData = prevMetaBuilder
		recordCardAction = prevRecorder
	})

	var (
		mu       sync.Mutex
		traceIDs []oteltrace.TraceID
	)
	actionName := "test.trace." + strconv.FormatInt(time.Now().UnixNano(), 10)
	appcardaction.RegisterSync(actionName, func(ctx context.Context, _ *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		mu.Lock()
		traceIDs = append(traceIDs, oteltrace.SpanContextFromContext(ctx).TraceID())
		mu.Unlock()
		return appcardaction.InfoToast("ok"), nil
	})

	handlerSet := NewHandlerSet(HandlerSetOptions{})
	parentCtx, parentSpan := otelinfra.StartNamed(context.Background(), "parent")
	defer parentSpan.End()

	if _, err := handlerSet.CardActionHandler(parentCtx, newCardActionEvent("card-msg-1", actionName)); err != nil {
		t.Fatalf("CardActionHandler() error = %v", err)
	}
	if _, err := handlerSet.CardActionHandler(parentCtx, newCardActionEvent("card-msg-2", actionName)); err != nil {
		t.Fatalf("CardActionHandler() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(traceIDs) != 2 {
		t.Fatalf("expected 2 trace IDs, got %d", len(traceIDs))
	}
	assertValidTraceID(t, traceIDs[0], "first card action trace")
	assertValidTraceID(t, traceIDs[1], "second card action trace")
	if traceIDs[0] == traceIDs[1] {
		t.Fatalf("expected independent card action root traces, got shared trace %s", traceIDs[0])
	}
	if traceIDs[0] == parentSpan.SpanContext().TraceID() || traceIDs[1] == parentSpan.SpanContext().TraceID() {
		t.Fatalf("expected card action traces to ignore parent trace %s", parentSpan.SpanContext().TraceID())
	}
}

func TestCardActionHandlerReturnsErrorToastWhenDispatchFails(t *testing.T) {
	prevMetaBuilder := buildCardActionMetaData
	prevRecorder := recordCardAction
	buildCardActionMetaData = func(context.Context, string, string) *xhandler.BaseMetaData {
		return &xhandler.BaseMetaData{ChatID: "chat-card", OpenID: "user-card"}
	}
	recordCardAction = func(context.Context, *callback.CardActionTriggerEvent) {}
	t.Cleanup(func() {
		buildCardActionMetaData = prevMetaBuilder
		recordCardAction = prevRecorder
	})

	actionName := "test.error." + strconv.FormatInt(time.Now().UnixNano(), 10)
	appcardaction.RegisterSync(actionName, func(context.Context, *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		return nil, errors.New("resume dispatcher unavailable")
	})

	handlerSet := NewHandlerSet(HandlerSetOptions{})
	resp, err := handlerSet.CardActionHandler(context.Background(), newCardActionEvent("card-msg-error", actionName))
	if err != nil {
		t.Fatalf("CardActionHandler() error = %v", err)
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("expected error toast response, got %+v", resp)
	}
	if resp.Toast.Type != "error" {
		t.Fatalf("toast type = %q, want %q", resp.Toast.Type, "error")
	}
	if !strings.Contains(resp.Toast.Content, "卡片操作失败") {
		t.Fatalf("toast content = %q, want contain 卡片操作失败", resp.Toast.Content)
	}
}

func TestMessageV2HandlerPreservesSameTraceAcrossEntryExecutorAndProcessor(t *testing.T) {
	restore := installHandlerTestTracer(t)
	defer restore()

	executor := appruntime.NewExecutor(appruntime.ExecutorConfig{
		Name:        "handler_test_executor",
		Workers:     1,
		QueueSize:   4,
		TaskTimeout: time.Second,
	})
	if err := executor.Start(context.Background()); err != nil {
		t.Fatalf("executor.Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = executor.Stop(context.Background())
	})

	capturingExecutor := &traceCaptureExecutor{inner: executor}
	done := make(chan struct{}, 1)

	var (
		mu              sync.Mutex
		preRunTraceID   oteltrace.TraceID
		addTraceLikeID  oteltrace.TraceID
		operatorTraceID oteltrace.TraceID
	)

	processor := (&xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{}).
		WithMetaDataProcess(func(*larkim.P2MessageReceiveV1) *xhandler.BaseMetaData {
			return &xhandler.BaseMetaData{}
		}).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) {
			mu.Lock()
			preRunTraceID = oteltrace.SpanContextFromContext(p).TraceID()
			mu.Unlock()

			_, span := otelinfra.Start(p)
			mu.Lock()
			addTraceLikeID = span.SpanContext().TraceID()
			mu.Unlock()
			span.End()
		}).
		AddAsync(&messageTestOperator{
			runFn: func(ctx context.Context, _ *larkim.P2MessageReceiveV1, _ *xhandler.BaseMetaData) error {
				mu.Lock()
				operatorTraceID = oteltrace.SpanContextFromContext(ctx).TraceID()
				mu.Unlock()
				done <- struct{}{}
				return nil
			},
		})

	handlerSet := NewHandlerSet(HandlerSetOptions{
		MessageProcessor: &processorMessageRunner{processor: processor},
		MessageExecutor:  capturingExecutor,
	})

	parentCtx, parentSpan := otelinfra.StartNamed(context.Background(), "parent")
	defer parentSpan.End()

	if err := handlerSet.MessageV2Handler(parentCtx, newMessageEvent("propagation-msg")); err != nil {
		t.Fatalf("MessageV2Handler() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for message processor execution")
	}

	submitTraceIDs, taskTraceIDs := capturingExecutor.Snapshot()
	if len(submitTraceIDs) != 1 || len(taskTraceIDs) != 1 {
		t.Fatalf("unexpected executor trace capture counts: submit=%d task=%d", len(submitTraceIDs), len(taskTraceIDs))
	}

	expectedTraceID := submitTraceIDs[0]
	assertValidTraceID(t, expectedTraceID, "submit trace")
	assertValidTraceID(t, taskTraceIDs[0], "executor task trace")

	mu.Lock()
	defer mu.Unlock()
	assertValidTraceID(t, preRunTraceID, "pre-run trace")
	assertValidTraceID(t, addTraceLikeID, "add-trace-like trace")
	assertValidTraceID(t, operatorTraceID, "operator trace")

	if expectedTraceID != taskTraceIDs[0] {
		t.Fatalf("expected executor submit/task traces to match, got submit=%s task=%s", expectedTraceID, taskTraceIDs[0])
	}
	if expectedTraceID != preRunTraceID || expectedTraceID != addTraceLikeID || expectedTraceID != operatorTraceID {
		t.Fatalf(
			"expected one trace across handler path, got submit=%s task=%s preRun=%s addTrace=%s operator=%s",
			expectedTraceID,
			taskTraceIDs[0],
			preRunTraceID,
			addTraceLikeID,
			operatorTraceID,
		)
	}
	if expectedTraceID == parentSpan.SpanContext().TraceID() {
		t.Fatalf("expected entry trace to ignore parent trace %s", parentSpan.SpanContext().TraceID())
	}
}

func installHandlerTestTracer(t *testing.T) func() {
	t.Helper()
	installHandlerTestConfig(t)

	prevTracer := otelinfra.OtelTracer
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otelinfra.OtelTracer = tp.Tracer("handler-test")

	return func() {
		otelinfra.OtelTracer = prevTracer
		_ = tp.Shutdown(context.Background())
	}
}

func installHandlerTestConfig(t *testing.T) {
	t.Helper()

	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatalf("failed to resolve test file path")
	}
	configPath := filepath.Join(filepath.Dir(file), "..", "..", "..", ".dev", "config.toml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config path unavailable: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
	if _, err := appconfig.LoadFileE(configPath); err != nil {
		t.Fatalf("load config: %v", err)
	}
}

func newMessageEvent(messageID string) *larkim.P2MessageReceiveV1 {
	createTime := strconv.FormatInt(time.Now().UnixMilli(), 10)
	chatType := "group"
	openID := "ou_test_user"

	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
			Message: &larkim.EventMessage{
				MessageId:  &messageID,
				ChatId:     strPtr("oc_test_chat"),
				ChatType:   &chatType,
				CreateTime: &createTime,
			},
		},
	}
}

func newReactionEvent(messageID string) *larkim.P2MessageReactionCreatedV1 {
	reactionType := "THUMBSUP"
	return &larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			MessageId: &messageID,
			ReactionType: &larkim.Emoji{
				EmojiType: &reactionType,
			},
		},
	}
}

func newCardActionEvent(messageID, actionName string) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{
				OpenID: "ou_card_user",
			},
			Action: &callback.CallBackAction{
				Value: map[string]any{
					cardaction.ActionField: actionName,
				},
			},
			Context: &callback.Context{
				OpenMessageID: messageID,
				OpenChatID:    "oc_card_chat",
			},
		},
	}
}

func assertValidTraceID(t *testing.T, traceID oteltrace.TraceID, label string) {
	t.Helper()
	if !traceID.IsValid() {
		t.Fatalf("%s should be valid, got %s", label, traceID)
	}
}

func strPtr(value string) *string {
	return &value
}
