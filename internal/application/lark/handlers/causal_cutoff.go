package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/osqueryutil"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var errInvalidEventCreateTime = errors.New("event message create_time must be valid positive milliseconds")

func eventAnchorTime(event *larkim.P2MessageReceiveV1) (time.Time, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil ||
		event.Event.Message.CreateTime == nil {
		return time.Time{}, errInvalidEventCreateTime
	}
	raw := strings.TrimSpace(*event.Event.Message.CreateTime)
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || milliseconds <= 0 {
		return time.Time{}, errInvalidEventCreateTime
	}
	return time.UnixMilli(milliseconds), nil
}

func buildHistoryQuery(chatID, cutoffTime string, anchor time.Time) *osquery.BoolQuery {
	return osquery.Bool().Must(
		osquery.Term("chat_id", chatID),
		causalTimeRange("create_time_v2", cutoffTime, anchor),
	)
}

func buildThreadExpansionQuery(threadID, cutoffTime string, anchor time.Time) *osquery.BoolQuery {
	return osquery.Bool().Must(
		osquery.Term("thread_id", threadID),
		causalTimeRange("create_time_v2", cutoffTime, anchor),
	)
}

func buildParentExpansionQuery(messageIDs []string, cutoffTime string, anchor time.Time) *osquery.BoolQuery {
	return osquery.Bool().Must(
		osqueryutil.TermsFromStrings("message_id", messageIDs),
		causalTimeRange("create_time_v2", cutoffTime, anchor),
	)
}

func buildChunkQuery(messageID, cutoffTime string, anchor time.Time) *osquery.BoolQuery {
	return osquery.Bool().Must(
		osquery.Term("msg_ids", messageID),
		causalTimeRange("timestamp_v2", cutoffTime, anchor),
	)
}

func causalTimeRange(field, cutoffTime string, anchor time.Time) *osquery.RangeQuery {
	value := osquery.Range(field).Lte(anchor)
	if cutoffTime = strings.TrimSpace(cutoffTime); cutoffTime != "" {
		value = value.Gte(cutoffTime)
	}
	return value
}

func retrievalAnchorEnd(anchor time.Time) string {
	return anchor.Format(time.RFC3339)
}

type droppedHistoryMessage struct {
	Message *history.OpensearchMsgLog
	Reason  string
}

func filterHistoryAtAnchor(
	messageList history.OpensearchMsgLogList,
	anchor time.Time,
) (history.OpensearchMsgLogList, []droppedHistoryMessage) {
	kept := make(history.OpensearchMsgLogList, 0, len(messageList))
	dropped := make([]droppedHistoryMessage, 0)
	for _, message := range messageList {
		if message == nil {
			continue
		}
		_, reason := parseCausalContextTime(message.CreateTimeV2, anchor)
		if reason != "" {
			dropped = append(dropped, droppedHistoryMessage{Message: message, Reason: reason})
			continue
		}
		kept = append(kept, message)
	}
	return kept, dropped
}

func parseCausalContextTime(value string, anchor time.Time) (time.Time, string) {
	occurredAt, ok := parseContextTimeValue(value)
	if !ok {
		return time.Time{}, excludeReasonInvalidTimestamp
	}
	if occurredAt.After(anchor) {
		return occurredAt, excludeReasonAfterAnchor
	}
	return occurredAt, ""
}
