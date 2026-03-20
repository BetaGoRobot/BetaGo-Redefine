package ark_dal

import "testing"

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
