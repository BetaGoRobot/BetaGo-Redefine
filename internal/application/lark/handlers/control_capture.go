package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	excludeReasonHistoryLimit = "history_limit"
	excludeReasonNoContext    = "no_context"
	excludeReasonMissingMsgID = "missing_message_id"
	excludeReasonChunkMissing = "chunk_missing"
	excludeReasonChunkInvalid = "chunk_invalid"
	excludeReasonDeduplicated = "deduplicated"
)

func recordStandardChatPlan(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan StandardChatPlan,
) {
	anchorID, anchorAt := standardChatAnchor(event)
	historyItems, retrievedItems, excludedItems := uniqueCapturedContext(plan)
	tokenEstimate := conversationeval.EstimateTokens(plan.SystemPrompt) +
		conversationeval.EstimateTokens(plan.UserPrompt)
	snapshot := conversationeval.ContextSnapshot{
		SchemaVersion:   conversationeval.SchemaVersion,
		AnchorEventID:   anchorID,
		AnchorAt:        anchorAt,
		Messages:        historyItems,
		Retrieved:       retrievedItems,
		Events:          []conversationeval.ContextItem{},
		SystemPrompt:    plan.SystemPrompt,
		UserPrompt:      plan.UserPrompt,
		TokenEstimate:   tokenEstimate,
		TokenBudget:     tokenEstimate,
		Truncated:       len(excludedItems) > 0,
		DegradedSources: uniqueNonEmptyStrings(plan.DegradedSources),
	}
	conversationeval.FromContext(ctx).RecordContext(ctx, snapshot, excludedItems)
}

func uniqueNonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueCapturedContext(
	plan StandardChatPlan,
) (
	[]conversationeval.ContextItem,
	[]conversationeval.ContextItem,
	[]conversationeval.ExcludedContextItem,
) {
	seen := make(map[string]struct{})
	counts := make(map[string]int)
	normalize := func(item conversationeval.ContextItem) conversationeval.ContextItem {
		key := item.Source + "\x00" + item.SourceID
		counts[key]++
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			return item
		}
		canonicalSourceID := item.SourceID
		for {
			item.SourceID = fmt.Sprintf("%s#%d", canonicalSourceID, counts[key])
			candidateKey := item.Source + "\x00" + item.SourceID
			if _, exists := seen[candidateKey]; !exists {
				seen[candidateKey] = struct{}{}
				break
			}
			counts[key]++
		}
		item.ID = item.Source + ":" + item.SourceID
		var metadata map[string]string
		if len(item.Metadata) > 0 {
			_ = json.Unmarshal(item.Metadata, &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata["canonical_source_id"] = canonicalSourceID
		item.Metadata = conversationeval.SafeMetadata(metadata)
		return item
	}

	historyItems := make([]conversationeval.ContextItem, 0, len(plan.HistoryItems))
	for _, item := range plan.HistoryItems {
		historyItems = append(historyItems, normalize(item))
	}
	retrievedItems := make([]conversationeval.ContextItem, 0, len(plan.RetrievedItems))
	for _, item := range plan.RetrievedItems {
		retrievedItems = append(retrievedItems, normalize(item))
	}
	excludedItems := make([]conversationeval.ExcludedContextItem, 0, len(plan.ExcludedItems))
	for _, excluded := range plan.ExcludedItems {
		excluded.ContextItem = normalize(excluded.ContextItem)
		excludedItems = append(excludedItems, excluded)
	}
	return historyItems, retrievedItems, excludedItems
}

func standardChatAnchor(event *larkim.P2MessageReceiveV1) (string, time.Time) {
	anchorID := "unknown"
	anchorAt := time.UnixMilli(1).UTC()
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return anchorID, anchorAt
	}
	if value := strings.TrimSpace(pointerString(event.Event.Message.MessageId)); value != "" {
		anchorID = value
	}
	if raw := strings.TrimSpace(pointerString(event.Event.Message.CreateTime)); raw != "" {
		if milliseconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			anchorAt = time.UnixMilli(milliseconds)
		}
	}
	return anchorID, anchorAt
}

func newContextItem(
	source, sourceID, kind, content string,
	rank int,
	occurredAt time.Time,
	metadata map[string]string,
) conversationeval.ContextItem {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		sourceID = strings.TrimPrefix(conversationeval.ContentSHA256(content), "sha256:")
	}
	return conversationeval.ContextItem{
		ID:          source + ":" + sourceID,
		Source:      source,
		SourceID:    sourceID,
		Kind:        kind,
		Content:     content,
		ContentHash: conversationeval.ContentSHA256(content),
		Rank:        rank,
		TokenCount:  conversationeval.EstimateTokens(content),
		Selected:    true,
		OccurredAt:  occurredAt,
		Metadata:    conversationeval.SafeMetadata(metadata),
	}
}

func excludedContextItem(item conversationeval.ContextItem, reason string) conversationeval.ExcludedContextItem {
	item.Selected = false
	item.ExcludeReason = reason
	return conversationeval.ExcludedContextItem{ContextItem: item}
}

func parseContextTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.UnixMilli(1).UTC()
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed
		}
	}
	if milliseconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(milliseconds)
	}
	return time.UnixMilli(1).UTC()
}

func captureHistoryPrompt(
	messageList history.OpensearchMsgLogList,
	promptLines []string,
	noContext bool,
) ([]conversationeval.ContextItem, []conversationeval.ExcludedContextItem) {
	byLine := make(map[string][]*history.OpensearchMsgLog, len(messageList))
	for _, message := range messageList {
		if message == nil {
			continue
		}
		line := strings.TrimSpace(message.ToLine())
		byLine[line] = append(byLine[line], message)
	}

	selected := make([]conversationeval.ContextItem, 0, len(promptLines))
	selectedMessageIDs := make(map[string]struct{}, len(promptLines))
	for index, rawLine := range promptLines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var item conversationeval.ContextItem
		candidates := byLine[line]
		if len(candidates) == 0 {
			for _, message := range messageList {
				if message == nil {
					continue
				}
				if _, alreadySelected := selectedMessageIDs[message.MessageID]; alreadySelected {
					continue
				}
				if strings.Contains(line, strings.TrimSpace(message.ToLine())) {
					candidates = []*history.OpensearchMsgLog{message}
					break
				}
			}
		}
		if len(candidates) > 0 {
			message := candidates[0]
			if queued := byLine[strings.TrimSpace(message.ToLine())]; len(queued) > 0 {
				byLine[strings.TrimSpace(message.ToLine())] = queued[1:]
			}
			item = historyMessageContextItem(message, index+1, line)
			selectedMessageIDs[message.MessageID] = struct{}{}
		} else {
			item = newContextItem(
				conversationeval.ContextSourceHistory,
				fmt.Sprintf("prompt-line-%d-%s", index+1, strings.TrimPrefix(conversationeval.ContentSHA256(line), "sha256:")[:12]),
				conversationeval.ContextKindMessage,
				line,
				index+1,
				time.UnixMilli(1).UTC(),
				map[string]string{"synthetic": "thread_structure"},
			)
		}
		selected = append(selected, item)
	}

	reason := excludeReasonHistoryLimit
	if noContext {
		reason = excludeReasonNoContext
	}
	excluded := make([]conversationeval.ExcludedContextItem, 0)
	for _, message := range messageList {
		if message == nil {
			continue
		}
		if _, ok := selectedMessageIDs[message.MessageID]; ok {
			continue
		}
		excluded = append(excluded, excludedContextItem(
			historyMessageContextItem(message, 0, message.ToLine()),
			reason,
		))
	}
	return selected, excluded
}

func historyMessageContextItem(
	message *history.OpensearchMsgLog,
	rank int,
	content string,
) conversationeval.ContextItem {
	if message == nil {
		return newContextItem(
			conversationeval.ContextSourceHistory, "", conversationeval.ContextKindMessage,
			content, rank, time.UnixMilli(1).UTC(), nil,
		)
	}
	return newContextItem(
		conversationeval.ContextSourceHistory,
		message.MessageID,
		conversationeval.ContextKindMessage,
		strings.TrimSpace(content),
		rank,
		parseContextTime(message.CreateTimeV2),
		map[string]string{
			"message_id": message.MessageID,
			"open_id":    message.OpenID,
			"parent_id":  message.ParentID,
			"root_id":    message.RootID,
			"thread_id":  message.ThreadID,
		},
	)
}

func captureControlStream(
	ctx context.Context,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
	startedAt time.Time,
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		capture := conversationeval.FromContext(ctx)
		traces := make([]conversationeval.ToolTrace, 0)
		traceIndexes := make(map[string]int)
		for data := range stream {
			if data == nil {
				continue
			}
			if call := data.CapabilityCall; call != nil {
				trace := capabilityToolTrace(call)
				capture.RecordToolPlan(ctx, trace)
				key := strings.TrimSpace(trace.CallID)
				if key == "" {
					key = fmt.Sprintf("%s:%d", trace.Name, len(traces))
				}
				if index, ok := traceIndexes[key]; ok {
					traces[index] = mergeToolTrace(traces[index], trace)
				} else {
					traceIndexes[key] = len(traces)
					traces = append(traces, trace)
				}
			}
			if decision := strings.TrimSpace(data.ContentStruct.Decision); decision != "" {
				recordControlOutput(ctx, capture, data, traces, startedAt)
			}
			if !yield(data) {
				return
			}
		}
	}
}

func recordControlOutput(
	ctx context.Context,
	capture conversationeval.Capture,
	final *ark_dal.ModelStreamRespReasoning,
	traces []conversationeval.ToolTrace,
	startedAt time.Time,
) {
	decision := conversationeval.OutputDecision(strings.ToLower(strings.TrimSpace(final.ContentStruct.Decision)))
	if decision != conversationeval.OutputDecisionReply && decision != conversationeval.OutputDecisionSkip {
		return
	}
	capture.RecordOutput(ctx, conversationeval.Output{
		Decision: decision,
		Reply:    final.ContentStruct.Reply,
		Thought:  final.ContentStruct.Thought,
		References: conversationeval.References{
			Web:     final.ContentStruct.ReferenceFromWeb,
			History: final.ContentStruct.ReferenceFromHistory,
		},
		CapabilityCalls: traces,
		Latency:         time.Since(startedAt),
	})
}

func recordDeliveryMessageID(ctx context.Context, messageID string) {
	if messageID = strings.TrimSpace(messageID); messageID != "" {
		conversationeval.FromContext(ctx).RecordDelivery(ctx, messageID)
	}
}

func recordTextDelivery(
	ctx context.Context,
	reply string,
	response *larkim.ReplyMessageResp,
) {
	if strings.TrimSpace(reply) == "" ||
		response == nil || response.Data == nil || response.Data.MessageId == nil {
		return
	}
	recordDeliveryMessageID(ctx, *response.Data.MessageId)
}

func capabilityToolTrace(call *ark_dal.CapabilityCallTrace) conversationeval.ToolTrace {
	return conversationeval.ToolTrace{
		CallID:       strings.TrimSpace(call.CallID),
		Name:         strings.TrimSpace(call.FunctionName),
		Arguments:    canonicalToolArguments(call.Arguments),
		Output:       call.Output,
		OutputSource: conversationeval.ToolOutputSourceCapability,
		Pending:      call.Pending,
	}
}

func canonicalToolArguments(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(arguments), &value) == nil {
		encoded, _ := json.Marshal(value)
		return encoded
	}
	encoded, _ := json.Marshal(arguments)
	return encoded
}

func mergeToolTrace(previous, next conversationeval.ToolTrace) conversationeval.ToolTrace {
	if next.Name != "" {
		previous.Name = next.Name
	}
	if len(next.Arguments) > 0 {
		previous.Arguments = next.Arguments
	}
	if next.Output != "" {
		previous.Output = next.Output
	}
	previous.Pending = next.Pending
	return previous
}
