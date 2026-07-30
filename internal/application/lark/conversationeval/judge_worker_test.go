package conversationeval

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestJudgeProcessorLoadsAndPersistsNextVersion(t *testing.T) {
	input := judgeTestInput()
	source := &judgeInputSourceFake{input: &input}
	store := &judgeStoreFake{}
	judge, err := NewJudgeWithCompletion(
		JudgeConfig{ModelID: "judge-model"},
		store,
		func(context.Context, JudgeCompletionRequest) (json.RawMessage, error) {
			return validJudgeResultJSON("tie"), nil
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeWithCompletion() error = %v", err)
	}
	processor, err := NewJudgeProcessor(
		source,
		judge,
		func() time.Time { return input.Episode.PostWindowEnd.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewJudgeProcessor() error = %v", err)
	}
	if err := processor.ProcessNext(context.Background()); err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if source.at.IsZero() || len(store.judgments) != 1 ||
		store.judgments[0].EpisodeID != input.Episode.ID {
		t.Fatalf("source/stored judgment = %v / %#v", source.at, store.judgments)
	}
}

func TestJudgeProcessorLeavesMalformedCompletionRetryable(t *testing.T) {
	input := judgeTestInput()
	source := &judgeInputSourceFake{input: &input}
	store := &judgeStoreFake{}
	judge, err := NewJudgeWithCompletion(
		JudgeConfig{ModelID: "judge-model"},
		store,
		func(context.Context, JudgeCompletionRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"winner":"invalid"}`), nil
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeWithCompletion() error = %v", err)
	}
	processor, err := NewJudgeProcessor(source, judge, nil)
	if err != nil {
		t.Fatalf("NewJudgeProcessor() error = %v", err)
	}
	if err := processor.ProcessNext(context.Background()); err == nil {
		t.Fatal("ProcessNext() error = nil")
	}
	if len(store.judgments) != 0 {
		t.Fatalf("malformed result was persisted: %#v", store.judgments)
	}
}

func TestJudgeWorkerStopsIdleLoop(t *testing.T) {
	source := &judgeInputSourceFake{err: ErrJudgeInputNotFound}
	judge, err := NewJudgeWithCompletion(
		JudgeConfig{ModelID: "judge-model"},
		&judgeStoreFake{},
		func(context.Context, JudgeCompletionRequest) (json.RawMessage, error) {
			return nil, errors.New("must not be called")
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeWithCompletion() error = %v", err)
	}
	processor, err := NewJudgeProcessor(source, judge, nil)
	if err != nil {
		t.Fatalf("NewJudgeProcessor() error = %v", err)
	}
	worker, err := NewJudgeWorker(processor, JudgeWorkerOptions{
		Interval: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewJudgeWorker() error = %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for source.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if source.calls() == 0 {
		t.Fatal("worker did not poll judge input source")
	}
}

type judgeInputSourceFake struct {
	mu        sync.Mutex
	input     *JudgeInput
	err       error
	at        time.Time
	callCount int
}

func (f *judgeInputSourceFake) NextJudgeInput(
	_ context.Context,
	at time.Time,
) (*JudgeInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.at = at
	if f.err != nil {
		return nil, f.err
	}
	if f.input == nil {
		return nil, ErrJudgeInputNotFound
	}
	value := cloneCaptureValue(*f.input)
	f.input = nil
	return &value, nil
}

func (f *judgeInputSourceFake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}
