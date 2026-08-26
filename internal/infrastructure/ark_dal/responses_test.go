package ark_dal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/bytedance/gg/gresult"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
)

func TestToolCallContinuationOutput(t *testing.T) {
	t.Run("success preserves existing encoding", func(t *testing.T) {
		got := toolCallContinuationOutput(gresult.OK("found"))
		want := utils.MustMarshalString("found")
		if got != want {
			t.Fatalf("toolCallContinuationOutput() = %q, want %q", got, want)
		}
	})

	type errorEnvelope struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Instruction string `json:"instruction"`
	}
	tests := []struct {
		name      string
		result    gresult.R[string]
		wantError string
	}{
		{
			name: "safe tool feedback",
			result: gresult.Err[string](xerror.WithToolFeedback(
				errors.New("database password=secret"),
				"缺少 run_at",
			)),
			wantError: "缺少 run_at",
		},
		{
			name:      "plain internal error",
			result:    gresult.Err[string](errors.New("database password=secret")),
			wantError: "工具执行失败",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolCallContinuationOutput(tt.result)
			if strings.Contains(got, "password") || strings.Contains(got, "secret") {
				t.Fatalf("toolCallContinuationOutput() leaked internal error: %q", got)
			}

			var envelope errorEnvelope
			if err := json.Unmarshal([]byte(got), &envelope); err != nil {
				t.Fatalf("toolCallContinuationOutput() = %q, want JSON object: %v", got, err)
			}
			if envelope.OK {
				t.Fatal("toolCallContinuationOutput() ok = true, want false")
			}
			if envelope.Error != tt.wantError {
				t.Fatalf("toolCallContinuationOutput() error = %q, want %q", envelope.Error, tt.wantError)
			}
			for _, phrase := range []string{"不要假设", "纠正参数", "询问用户"} {
				if !strings.Contains(envelope.Instruction, phrase) {
					t.Errorf("toolCallContinuationOutput() instruction = %q, want phrase %q", envelope.Instruction, phrase)
				}
			}
		})
	}
}

func TestResponsesImplDrainPendingStreamItemsEmitsDeltaAndCapabilityTrace(t *testing.T) {
	resp := New[string]("oc_chat", "ou_actor", nil)
	resp.textOutput.ReasoningTextDelta = "先分析"
	resp.textOutput.NormalTextDelta = "{\"reply\":\"hi\"}"
	resp.pendingCapabilityCalls = append(resp.pendingCapabilityCalls, CapabilityCallTrace{
		CallID:       "call_1",
		FunctionName: "send_message",
		Arguments:    `{"text":"hi"}`,
		Output:       "ok",
	})

	items := resp.drainPendingStreamItems()
	if len(items) != 2 {
		t.Fatalf("drainPendingStreamItems() len = %d, want 2", len(items))
	}
	if items[0].ReasoningContent != "先分析" {
		t.Fatalf("reasoning delta = %q, want %q", items[0].ReasoningContent, "先分析")
	}
	if items[0].Content != "{\"reply\":\"hi\"}" {
		t.Fatalf("content delta = %q, want %q", items[0].Content, "{\"reply\":\"hi\"}")
	}
	if items[1].CapabilityCall == nil {
		t.Fatal("expected capability trace item")
	}
	if items[1].CapabilityCall.CallID != "call_1" {
		t.Fatalf("trace call id = %q, want %q", items[1].CapabilityCall.CallID, "call_1")
	}
	if items[1].CapabilityCall.FunctionName != "send_message" {
		t.Fatalf("trace function name = %q, want %q", items[1].CapabilityCall.FunctionName, "send_message")
	}
	if got := len(resp.drainPendingStreamItems()); got != 0 {
		t.Fatalf("drainPendingStreamItems() second len = %d, want 0", got)
	}
}

func TestResponsesImplDefersToolContinuationUntilCompletedAndAggregatesTurnUsage(t *testing.T) {
	restoreArkRuntimeForModelTest(t, "normal-model")
	var requests []*responses.ResponsesRequest
	previousCreator := responsesStreamCreator
	responsesStreamCreator = func(
		_ context.Context,
		request *responses.ResponsesRequest,
		_ llmusage.Scope,
	) (*arkutils.ResponsesStreamReader, error) {
		requests = append(requests, request)
		return nil, nil
	}
	t.Cleanup(func() { responsesStreamCreator = previousCreator })

	impl := New[string]("oc_chat", "ou_actor", nil).WithModelID("normal-model")
	impl.beginUsageTurn(llmusage.Scope{
		SourceType: llmusage.SourceTypeUser, Source: "chat",
		BusinessScene: llmusage.SceneConversation, BusinessOperation: llmusage.OperationChatReply,
	}, "normal-model")
	impl.lastRespID = "resp-plan"
	impl.functionCallMap["call-1"] = "lookup"
	impl.functionCallMap["call-2"] = "broken_tool"
	impl.handlers["lookup"] = func(context.Context, string, tools.FCMeta[string]) gresult.R[string] {
		return gresult.OK("found")
	}
	impl.handlers["broken_tool"] = func(context.Context, string, tools.FCMeta[string]) gresult.R[string] {
		return gresult.Err[string](errors.New("tool failed"))
	}
	arguments := `{"query":"weather"}`
	argsEvent := &responses.Event{Event: &responses.Event_FunctionCallArgumentsDone{
		FunctionCallArgumentsDone: &responses.FunctionCallArgumentsDoneEvent{
			Type: responses.EventType_response_function_call_arguments_done, ItemId: "call-1", Arguments: &arguments,
		},
	}}
	if _, err := impl.Handle(context.Background(), nil, argsEvent, llmusage.Scope{}, "normal-model"); err != nil {
		t.Fatalf("Handle(arguments done) error = %v", err)
	}
	brokenArguments := `{"query":"broken"}`
	brokenEvent := &responses.Event{Event: &responses.Event_FunctionCallArgumentsDone{
		FunctionCallArgumentsDone: &responses.FunctionCallArgumentsDoneEvent{
			Type: responses.EventType_response_function_call_arguments_done, ItemId: "call-2", Arguments: &brokenArguments,
		},
	}}
	if _, err := impl.Handle(context.Background(), nil, brokenEvent, llmusage.Scope{}, "normal-model"); err != nil {
		t.Fatalf("Handle(second arguments done) error = %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("continuation started before response.completed: %+v", requests)
	}

	collector := llmusage.NewCollector()
	ctx := llmusage.WithObserver(context.Background(), collector)
	if _, err := impl.Handle(ctx, nil, completedResponseEvent("resp-plan", 10, 2, 12), llmusage.Scope{}, "normal-model"); err != nil {
		t.Fatalf("Handle(plan completed) error = %v", err)
	}
	if len(requests) != 1 || requests[0].GetPreviousResponseId() != "resp-plan" {
		t.Fatalf("continuation requests = %+v", requests)
	}
	if got := len(requests[0].GetInput().GetListValue().GetListValue()); got != 2 {
		t.Fatalf("continuation tool outputs = %d, want 2", got)
	}
	impl.OnCompleted(ctx, completedResponseEvent("resp-final", 20, 5, 25), llmusage.Scope{}, "normal-model")
	impl.recordStreamUsage(ctx, llmusage.Scope{}, "normal-model")
	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("usage record count = %d, want 1: %+v", len(records), records)
	}
	if records[0].TotalTokens != 37 || records[0].PromptTokens != 30 || records[0].CompletionTokens != 7 {
		t.Fatalf("aggregated usage = %+v", records[0])
	}
	if len(records[0].ToolCalls) != 2 || records[0].ToolCalls[0].Name != "lookup" || records[0].ToolCalls[1].Status != llmusage.ToolStatusError {
		t.Fatalf("tool calls = %+v", records[0].ToolCalls)
	}
}

func completedResponseEvent(id string, prompt, completion, total int64) *responses.Event {
	return &responses.Event{Event: &responses.Event_ResponseCompleted{
		ResponseCompleted: &responses.ResponseCompletedEvent{Response: &responses.ResponseObject{
			Id: id, Usage: &responses.Usage{InputTokens: prompt, OutputTokens: completion, TotalTokens: total},
		}, Type: responses.EventType_response_completed},
	}}
}

func TestResponsesImplModelOverrideControlsInitialToolContinuationAndUsage(t *testing.T) {
	restoreArkRuntimeForModelTest(t, "normal-model")
	var requests []*responses.ResponsesRequest
	previousCreator := responsesStreamCreator
	responsesStreamCreator = func(
		_ context.Context,
		request *responses.ResponsesRequest,
		_ llmusage.Scope,
	) (*arkutils.ResponsesStreamReader, error) {
		requests = append(requests, request)
		return nil, nil
	}
	t.Cleanup(func() { responsesStreamCreator = previousCreator })

	impl := New[string]("oc_chat", "ou_actor", nil).
		WithModelID("override-model")
	if _, err := impl.Do(context.Background(), llmusage.Scope{}, "system", "user"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if len(requests) != 1 || requests[0].GetModel() != "override-model" {
		t.Fatalf("initial requests = %+v", requests)
	}

	impl.lastRespID = "resp_initial"
	impl.functionCallMap["call_1"] = "lookup"
	impl.handlers["lookup"] = func(context.Context, string, tools.FCMeta[string]) gresult.R[string] {
		return gresult.OK("found")
	}
	arguments := `{"query":"callback"}`
	event := &responses.Event{Event: &responses.Event_FunctionCallArgumentsDone{
		FunctionCallArgumentsDone: &responses.FunctionCallArgumentsDoneEvent{
			ItemId: "call_1", Arguments: &arguments,
		},
	}}
	if _, err := impl.OnCallArgs(context.Background(), event, llmusage.Scope{}); err != nil {
		t.Fatalf("OnCallArgs() error = %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("tool continuation started before response.completed: %+v", requests)
	}
	if _, err := impl.Handle(
		context.Background(), nil, completedResponseEvent("resp_initial", 0, 0, 0), llmusage.Scope{}, impl.ActiveModelID(),
	); err != nil {
		t.Fatalf("Handle(response.completed) error = %v", err)
	}
	if len(requests) != 2 || requests[1].GetModel() != "override-model" {
		t.Fatalf("tool continuation requests = %+v", requests)
	}
	if requests[1].GetPreviousResponseId() != "resp_initial" {
		t.Fatalf("tool continuation previous response = %q", requests[1].GetPreviousResponseId())
	}

	collector := llmusage.NewCollector()
	ctx := llmusage.WithObserver(context.Background(), collector)
	impl.streamUsage = &responses.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}
	impl.streamUsageRecorded = false
	impl.recordStreamUsage(ctx, llmusage.Scope{}, impl.ActiveModelID())
	records := collector.Records()
	if len(records) != 1 || records[0].Model != "override-model" {
		t.Fatalf("usage records = %+v", records)
	}
}

func TestResponsesImplEmptyModelOverrideKeepsNormalDefault(t *testing.T) {
	restoreArkRuntimeForModelTest(t, "normal-model")
	var capturedModel string
	previousCreator := responsesStreamCreator
	responsesStreamCreator = func(
		_ context.Context,
		request *responses.ResponsesRequest,
		_ llmusage.Scope,
	) (*arkutils.ResponsesStreamReader, error) {
		capturedModel = request.GetModel()
		return nil, nil
	}
	t.Cleanup(func() { responsesStreamCreator = previousCreator })

	if _, err := New[string]("", "", nil).WithModelID("  ").
		Do(context.Background(), llmusage.Scope{}, "system", "user"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if capturedModel != "normal-model" {
		t.Fatalf("request model = %q, want normal default", capturedModel)
	}
}

func TestResponsesImplModelOverrideControlsDoSyncRequest(t *testing.T) {
	restoreArkRuntimeForModelTest(t, "normal-model")
	stop := errors.New("stop after request")
	var capturedModel string
	previousCreator := responsesStreamCreator
	responsesStreamCreator = func(
		_ context.Context,
		request *responses.ResponsesRequest,
		_ llmusage.Scope,
	) (*arkutils.ResponsesStreamReader, error) {
		capturedModel = request.GetModel()
		return nil, stop
	}
	t.Cleanup(func() { responsesStreamCreator = previousCreator })

	_, err := New[string]("", "", nil).WithModelID("sync-model").
		DoSync(context.Background(), llmusage.Scope{}, "system", "user")
	if !errors.Is(err, stop) {
		t.Fatalf("DoSync() error = %v, want %v", err, stop)
	}
	if capturedModel != "sync-model" {
		t.Fatalf("DoSync request model = %q, want %q", capturedModel, "sync-model")
	}
}

func restoreArkRuntimeForModelTest(t *testing.T, normalModel string) {
	t.Helper()
	previousClient, previousConfig := client, arkConfig
	client = &arkruntime.Client{}
	arkConfig = &config.ArkConfig{NormalModel: normalModel}
	t.Cleanup(func() {
		client, arkConfig = previousClient, previousConfig
	})
}
