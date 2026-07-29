package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestEventAnchorTimeRequiresValidMilliseconds(t *testing.T) {
	valid := "1785301200123"
	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Message: &larkim.EventMessage{CreateTime: &valid},
	}}
	got, err := eventAnchorTime(event)
	if err != nil {
		t.Fatalf("eventAnchorTime() error = %v", err)
	}
	if want := time.UnixMilli(1785301200123); !got.Equal(want) {
		t.Fatalf("eventAnchorTime() = %s, want %s", got, want)
	}

	for name, event := range map[string]*larkim.P2MessageReceiveV1{
		"nil": nil,
		"missing": {Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{},
		}},
		"invalid": {Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{CreateTime: chatHandlerStrPtr("bad")},
		}},
		"zero": {Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{CreateTime: chatHandlerStrPtr("0")},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eventAnchorTime(event); err == nil {
				t.Fatal("eventAnchorTime() error is nil")
			}
		})
	}
}

func TestCausalQueriesAlwaysApplyAnchorUpperBound(t *testing.T) {
	anchor := time.UnixMilli(1785301200123)
	cutoff := "2026-07-01 00:00:00"
	tests := []struct {
		name  string
		field string
		query *osquery.BoolQuery
	}{
		{
			name: "history", field: "create_time_v2",
			query: buildHistoryQuery("oc_chat", cutoff, anchor),
		},
		{
			name: "thread expansion", field: "create_time_v2",
			query: buildThreadExpansionQuery("omt_thread", cutoff, anchor),
		},
		{
			name: "parent expansion", field: "create_time_v2",
			query: buildParentExpansionQuery([]string{"om_parent"}, cutoff, anchor),
		},
		{
			name: "chunk", field: "timestamp_v2",
			query: buildChunkQuery("om_topic", cutoff, anchor),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rangeValues := queryRangeValues(t, test.query, test.field)
			gotUpper, ok := rangeValues["lte"].(time.Time)
			if !ok || !gotUpper.Equal(anchor) {
				t.Fatalf("range lte = %#v, want %s", rangeValues["lte"], anchor)
			}
			if got := rangeValues["gte"]; got != cutoff {
				t.Fatalf("range gte = %#v, want %q", got, cutoff)
			}
		})
	}
}

func TestCausalQueryWithoutConfiguredCutoffStillAppliesAnchor(t *testing.T) {
	anchor := time.UnixMilli(1785301200123)
	values := queryRangeValues(t, buildHistoryQuery("oc_chat", "", anchor), "create_time_v2")
	if _, exists := values["gte"]; exists {
		t.Fatalf("unexpected gte without cutoff: %#v", values)
	}
	if got, ok := values["lte"].(time.Time); !ok || !got.Equal(anchor) {
		t.Fatalf("range lte = %#v, want %s", values["lte"], anchor)
	}
}

func TestRetrievalAnchorEndUsesRFC3339(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	if got, want := retrievalAnchorEnd(anchor), anchor.Format(time.RFC3339); got != want {
		t.Fatalf("retrievalAnchorEnd() = %q, want %q", got, want)
	}
}

func TestContextAtAnchorValidatesAndContextAfterAnchorFails(t *testing.T) {
	anchor := time.UnixMilli(1785301200123)
	item := newContextItem(
		conversationeval.ContextSourceHistory,
		"om_at_anchor",
		conversationeval.ContextKindMessage,
		"exact boundary",
		1,
		anchor,
		nil,
	)
	snapshot := conversationeval.ContextSnapshot{
		SchemaVersion: conversationeval.SchemaVersion,
		AnchorEventID: "om_anchor",
		AnchorAt:      anchor,
		Messages:      []conversationeval.ContextItem{item},
		Retrieved:     []conversationeval.ContextItem{},
		Events:        []conversationeval.ContextItem{},
		SystemPrompt:  "system",
		UserPrompt:    "exact boundary",
		TokenEstimate: 10,
		TokenBudget:   10,
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("ContextSnapshot at anchor Validate() error = %v", err)
	}
	snapshot.Messages[0].OccurredAt = anchor.Add(time.Millisecond)
	if err := snapshot.Validate(); err == nil {
		t.Fatal("ContextSnapshot after anchor Validate() error is nil")
	}
}

func TestFilterHistoryAtAnchorKeepsFutureMessagesOutOfPrompt(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 0, time.Local)
	before := &history.OpensearchMsgLog{
		CreateTimeV2: "2026-07-29 14:59:59", MessageID: "om_before",
		OpenID: "ou_a", UserName: "A", MsgList: []string{"before"},
	}
	after := &history.OpensearchMsgLog{
		CreateTimeV2: "2026-07-29 15:00:01", MessageID: "om_after",
		OpenID: "ou_b", UserName: "B", MsgList: []string{"after"},
	}

	kept, dropped := filterHistoryAtAnchor(
		history.OpensearchMsgLogList{before, after},
		anchor,
	)

	if len(kept) != 1 || kept[0].MessageID != "om_before" {
		t.Fatalf("kept history = %+v", kept)
	}
	if len(dropped) != 1 || dropped[0].Message.MessageID != "om_after" ||
		dropped[0].Reason != excludeReasonAfterAnchor {
		t.Fatalf("dropped history = %+v", dropped)
	}
	if prompt := strings.Join(kept.ToThreadLines(), "\n"); strings.Contains(prompt, "after") {
		t.Fatalf("prompt leaked anchor-after history: %q", prompt)
	}
}

func TestParseCausalContextTimeClassifiesBoundaryFutureAndInvalid(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 0, time.Local)
	tests := []struct {
		name   string
		value  string
		reason string
	}{
		{name: "boundary", value: "2026-07-29 15:00:00", reason: ""},
		{name: "future", value: "2026-07-29 15:00:01", reason: excludeReasonAfterAnchor},
		{name: "invalid", value: "bad", reason: excludeReasonInvalidTimestamp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, reason := parseCausalContextTime(test.value, anchor)
			if reason != test.reason {
				t.Fatalf("parseCausalContextTime() reason = %q, want %q", reason, test.reason)
			}
		})
	}
}

func TestCaptureDroppedHistoryMarksInvalidTimeDegradedWithoutFutureContext(t *testing.T) {
	invalid := &history.OpensearchMsgLog{
		CreateTimeV2: "bad", MessageID: "om_invalid",
		MsgList: []string{"invalid"},
	}
	future := &history.OpensearchMsgLog{
		CreateTimeV2: "2026-07-29 15:00:01", MessageID: "om_future",
		MsgList: []string{"future"},
	}
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 0, time.Local)
	excluded, degraded := captureDroppedHistory([]droppedHistoryMessage{
		{Message: invalid, Reason: excludeReasonInvalidTimestamp},
		{Message: future, Reason: excludeReasonAfterAnchor},
	}, anchor)

	if len(excluded) != 2 ||
		excluded[0].SourceID != "om_invalid" ||
		excluded[0].ExcludeReason != excludeReasonInvalidTimestamp {
		t.Fatalf("excluded history = %+v", excluded)
	}
	if excluded[1].SourceID != "om_future" ||
		excluded[1].ExcludeReason != excludeReasonAfterAnchor ||
		!excluded[1].OccurredAt.Equal(anchor) {
		t.Fatalf("future excluded history = %+v", excluded[1])
	}
	if !containsString(degraded, "history_time") ||
		!containsString(degraded, "history_causal_filter") {
		t.Fatalf("degraded sources = %#v", degraded)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func queryRangeValues(t *testing.T, query *osquery.BoolQuery, field string) map[string]any {
	t.Helper()
	root := query.Map()["bool"].(map[string]any)
	must := root["must"].([]map[string]any)
	for _, clause := range must {
		rangeClause, ok := clause["range"].(map[string]any)
		if !ok {
			continue
		}
		values, ok := rangeClause[field].(map[string]any)
		if ok {
			return values
		}
	}
	t.Fatalf("query has no range for %q: %#v", field, query.Map())
	return nil
}
