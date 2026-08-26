package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

type taskResultReviewerFake struct {
	calls      int
	decision   TaskResultDecision
	err        error
	result     string
	finishedAt time.Time
	beforeCall func()
}

func (f *taskResultReviewerFake) Review(_ context.Context, _ *model.ScheduledTask, result string, finishedAt time.Time) (TaskResultDecision, error) {
	f.calls++
	f.result = result
	f.finishedAt = finishedAt
	if f.beforeCall != nil {
		f.beforeCall()
	}
	return f.decision, f.err
}

type taskNotifierFake struct {
	calls       int
	content     string
	deliveryKey string
	err         error
}

func (f *taskNotifierFake) Notify(_ context.Context, _ *model.ScheduledTask, content, deliveryKey string) error {
	f.calls++
	f.content = content
	f.deliveryKey = deliveryKey
	return f.err
}

type schedulerReviewService struct {
	noopService
	result        string
	execErr       error
	finalizeErr   error
	executeCalls  int
	finalizeCalls int
	finalized     bool
}

func (s *schedulerReviewService) Available() bool { return true }

func (s *schedulerReviewService) ExecuteTask(context.Context, *model.ScheduledTask) (string, error) {
	s.executeCalls++
	return s.result, s.execErr
}

func (s *schedulerReviewService) FinalizeTaskExecution(context.Context, *model.ScheduledTask, string, error, time.Time) error {
	s.finalizeCalls++
	s.finalized = true
	return s.finalizeErr
}

func TestSchedulerSkipsResultReviewWhenNotifyResultDisabled(t *testing.T) {
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{Send: true, Content: "must not send", Reason: "disabled"}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)

	err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-1", NotifyResult: false,
	}, "raw result", time.Now())
	if err != nil {
		t.Fatalf("reviewAndNotifyResult() error = %v", err)
	}
	if reviewer.calls != 0 || notifier.calls != 0 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 0/0", reviewer.calls, notifier.calls)
	}
}

func TestSchedulerSkipsResultReviewForSendMessage(t *testing.T) {
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{
		Send: true, Content: "must not send", Reason: "duplicate reminder",
	}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)

	err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-send-message", ToolName: "send_message", NotifyResult: true,
	}, "消息发送成功", time.Now())
	if err != nil {
		t.Fatalf("reviewAndNotifyResult() error = %v", err)
	}
	if reviewer.calls != 0 || notifier.calls != 0 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 0/0", reviewer.calls, notifier.calls)
	}
}

func TestSchedulerSendsModelReviewedResult(t *testing.T) {
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{
		Send: true, Content: "炫彩蛋已刷新，售价 160 万，限购 1 个。", Reason: "时效性结果",
	}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)
	finishedAt := time.Date(2026, 8, 26, 0, 15, 10, 0, time.UTC)

	err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-2", NotifyResult: true,
	}, `{"raw":"result"}`, finishedAt)
	if err != nil {
		t.Fatalf("reviewAndNotifyResult() error = %v", err)
	}
	if reviewer.calls != 1 || reviewer.result != `{"raw":"result"}` || !reviewer.finishedAt.Equal(finishedAt) {
		t.Fatalf("reviewer call = %d/%q/%s", reviewer.calls, reviewer.result, reviewer.finishedAt)
	}
	if notifier.calls != 1 || notifier.content != reviewer.decision.Content {
		t.Fatalf("notifier call = %d/%q, want model content %q", notifier.calls, notifier.content, reviewer.decision.Content)
	}
	if !strings.Contains(notifier.deliveryKey, "task-2") || !strings.HasSuffix(notifier.deliveryKey, "-result") {
		t.Fatalf("notifier delivery key = %q", notifier.deliveryKey)
	}
}

func TestSchedulerHonorsSilentModelDecision(t *testing.T) {
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{Reason: "没有有意义的更新"}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)

	if err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-3", NotifyResult: true,
	}, "raw result", time.Now()); err != nil {
		t.Fatalf("reviewAndNotifyResult() error = %v", err)
	}
	if reviewer.calls != 1 || notifier.calls != 0 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 1/0", reviewer.calls, notifier.calls)
	}
}

func TestSchedulerReviewsEmptySuccessfulResult(t *testing.T) {
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{Reason: "空结果无需通知"}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)

	if err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-4", NotifyResult: true,
	}, "", time.Now()); err != nil {
		t.Fatalf("reviewAndNotifyResult() error = %v", err)
	}
	if reviewer.calls != 1 || reviewer.result != "" {
		t.Fatalf("reviewer calls/result = %d/%q, want 1/empty", reviewer.calls, reviewer.result)
	}
}

func TestSchedulerReviewFailureUsesErrorNotificationWhenEnabled(t *testing.T) {
	reviewErr := errors.New("model unavailable")
	reviewer := &taskResultReviewerFake{err: reviewErr}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)
	task := &model.ScheduledTask{
		ID: "task-5", Name: "商人播报", ToolName: "research_read_url",
		ToolArgs: `{"token":"must-not-leak"}`, NotifyResult: true, NotifyOnError: true,
	}
	rawResult := `{"secret":"must-not-leak"}`

	err := scheduler.reviewAndNotifyResult(context.Background(), task, rawResult, time.Now())
	if !errors.Is(err, reviewErr) {
		t.Fatalf("reviewAndNotifyResult() error = %v, want review error", err)
	}
	if reviewer.calls != 1 || notifier.calls != 1 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 1/1", reviewer.calls, notifier.calls)
	}
	if !strings.Contains(notifier.content, "定时任务结果审核失败") ||
		strings.Contains(notifier.content, "must-not-leak") ||
		strings.Contains(notifier.content, reviewErr.Error()) {
		t.Fatalf("error notification = %q", notifier.content)
	}
}

func TestSchedulerNotificationFailureDoesNotRetryReview(t *testing.T) {
	deliveryErr := errors.New("delivery failed")
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{Send: true, Content: "播报", Reason: "send"}}
	notifier := &taskNotifierFake{err: deliveryErr}
	scheduler := newSchedulerWithDependencies(nil, nil, reviewer, notifier)

	err := scheduler.reviewAndNotifyResult(context.Background(), &model.ScheduledTask{
		ID: "task-6", NotifyResult: true,
	}, "raw result", time.Now())
	if !errors.Is(err, deliveryErr) {
		t.Fatalf("reviewAndNotifyResult() error = %v, want delivery error", err)
	}
	if reviewer.calls != 1 || notifier.calls != 1 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 1/1", reviewer.calls, notifier.calls)
	}
}

func TestSchedulerExecuteTaskFinalizesBeforeReviewAndDoesNotRerunTool(t *testing.T) {
	service := &schedulerReviewService{result: `{"items":["炫彩蛋"]}`}
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{Send: true, Content: "炫彩蛋已刷新。", Reason: "send"}}
	reviewer.beforeCall = func() {
		if !service.finalized {
			t.Fatal("review started before raw task result was finalized")
		}
	}
	notifier := &taskNotifierFake{err: errors.New("delivery failed")}
	scheduler := newSchedulerWithDependencies(service, nil, reviewer, notifier)
	task := &model.ScheduledTask{
		ID: "task-7", Name: "商人播报", ToolName: "research_read_url", NotifyResult: true,
	}

	scheduler.executeTask(context.Background(), task)

	if service.executeCalls != 1 || service.finalizeCalls != 1 {
		t.Fatalf("execute/finalize calls = %d/%d, want 1/1", service.executeCalls, service.finalizeCalls)
	}
	if reviewer.calls != 1 || notifier.calls != 1 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 1/1", reviewer.calls, notifier.calls)
	}
}

func TestSchedulerExecuteTaskDoesNotReviewToolFailure(t *testing.T) {
	service := &schedulerReviewService{execErr: errors.New("tool failed")}
	reviewer := &taskResultReviewerFake{}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(service, nil, reviewer, notifier)
	task := &model.ScheduledTask{ID: "task-8", NotifyResult: true, NotifyOnError: false}

	scheduler.executeTask(context.Background(), task)

	if service.executeCalls != 1 || service.finalizeCalls != 1 || reviewer.calls != 0 || notifier.calls != 0 {
		t.Fatalf("execute/finalize/reviewer/notifier calls = %d/%d/%d/%d", service.executeCalls, service.finalizeCalls, reviewer.calls, notifier.calls)
	}
}

func TestSchedulerExecuteTaskDoesNotReviewWhenFinalizationFails(t *testing.T) {
	service := &schedulerReviewService{
		result:      `{"items":["炫彩蛋"]}`,
		finalizeErr: errors.New("database unavailable"),
	}
	reviewer := &taskResultReviewerFake{decision: TaskResultDecision{
		Send: true, Content: "must not send", Reason: "send",
	}}
	notifier := &taskNotifierFake{}
	scheduler := newSchedulerWithDependencies(service, nil, reviewer, notifier)
	task := &model.ScheduledTask{ID: "task-9", NotifyResult: true, NotifyOnError: true}

	scheduler.executeTask(context.Background(), task)

	if service.executeCalls != 1 || service.finalizeCalls != 1 {
		t.Fatalf("execute/finalize calls = %d/%d, want 1/1", service.executeCalls, service.finalizeCalls)
	}
	if reviewer.calls != 0 || notifier.calls != 0 {
		t.Fatalf("reviewer/notifier calls = %d/%d, want 0/0", reviewer.calls, notifier.calls)
	}
}
