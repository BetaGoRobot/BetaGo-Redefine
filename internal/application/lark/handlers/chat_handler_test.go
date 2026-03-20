package handlers

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestAgenticChatHandleForcesAgenticPath(t *testing.T) {
	originalEntry := agenticChatEntryHandler
	defer func() {
		agenticChatEntryHandler = originalEntry
	}()

	called := 0
	agenticChatEntryHandler = agentruntime.NewChatEntryHandler(agentruntime.ChatEntryHandlerOptions{
		MentionChecker: func(*larkim.P2MessageReceiveV1) bool { return true },
		MuteChecker:    func(context.Context, string) (bool, error) { return false, nil },
		FileCollector:  func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) { return nil, nil },
		AgenticResponder: func(ctx context.Context, req agentruntime.ChatResponseRequest) error {
			called++
			return nil
		},
		StandardResponder: func(context.Context, agentruntime.ChatResponseRequest) error {
			t.Fatal("standard responder should not be called by AgenticChat")
			return nil
		},
	})

	meta := &xhandler.BaseMetaData{}
	if err := AgenticChat.Handle(context.Background(), testChatEvent(), meta, ChatArgs{Input: "帮我总结", Reason: true}); err != nil {
		t.Fatalf("AgenticChat.Handle() error = %v", err)
	}
	if called != 1 {
		t.Fatalf("agentic handler calls = %d, want 1", called)
	}
}

func TestGenerateChatSeqDelegatesToStandardGenerator(t *testing.T) {
	originalGenerator := standardChatSeqGenerator
	defer func() {
		standardChatSeqGenerator = originalGenerator
	}()

	var (
		capturedEvent   *larkim.P2MessageReceiveV1
		capturedModelID string
		capturedSize    *int
		capturedFiles   []string
		capturedInput   []string
	)
	standardChatSeqGenerator = func(ctx context.Context, event *larkim.P2MessageReceiveV1, modelID string, size *int, files []string, input ...string) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		capturedEvent = event
		capturedModelID = modelID
		capturedSize = size
		capturedFiles = append([]string(nil), files...)
		capturedInput = append([]string(nil), input...)
		return seqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "ok"}}), nil
	}

	size := 12
	files := []string{"https://example.com/a.png"}
	event := testChatEvent()
	stream, err := GenerateChatSeq(context.Background(), event, "ep-test", &size, files, "帮我总结")
	if err != nil {
		t.Fatalf("GenerateChatSeq() error = %v", err)
	}

	count := 0
	for range stream {
		count++
	}
	if count != 1 {
		t.Fatalf("stream item count = %d, want 1", count)
	}
	if capturedEvent != event {
		t.Fatal("expected event to be forwarded")
	}
	if capturedModelID != "ep-test" {
		t.Fatalf("model id = %q, want %q", capturedModelID, "ep-test")
	}
	if capturedSize != &size {
		t.Fatal("expected size pointer to be forwarded")
	}
	if len(capturedFiles) != len(files) || capturedFiles[0] != files[0] {
		t.Fatalf("files = %+v, want %+v", capturedFiles, files)
	}
	if len(capturedInput) != 1 || capturedInput[0] != "帮我总结" {
		t.Fatalf("input = %+v, want %+v", capturedInput, []string{"帮我总结"})
	}
}

func TestChatGenerationPlanGenerateReturnsNotConfiguredWithoutRegisteredExecutor(t *testing.T) {
	agentruntime.SetChatGenerationPlanExecutor(nil)

	_, err := (agentruntime.ChatGenerationPlan{}).Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when executor is not registered")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testChatEvent() *larkim.P2MessageReceiveV1 {
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

func seqFromItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

type fakeChatGenerationPlanExecutor struct {
	calls     int
	lastPlan  agentruntime.ChatGenerationPlan
	lastEvent *larkim.P2MessageReceiveV1
	result    iter.Seq[*ark_dal.ModelStreamRespReasoning]
	err       error
}

func (f *fakeChatGenerationPlanExecutor) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan agentruntime.ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	f.calls++
	f.lastPlan = plan
	f.lastEvent = event
	return f.result, f.err
}
