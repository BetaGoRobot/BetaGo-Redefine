package ark_dal

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/bytedance/gg/gresult"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
)

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
