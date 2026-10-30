package handlers

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

type memeReferenceState struct {
	cards          []memes.UseCard
	chatID         string
	messageID      string
	startedAt      time.Time
	originalSystem string
	originalUser   string
	baseSystem     string
	baseUser       string
	knowledge      *memes.GroundedContext
	revalidate     memereference.Revalidator
	identifiers    []string
}

var memeReferenceLog = func(ctx context.Context, message string, fields ...zap.Field) {
	logs.L().Ctx(ctx).Info(message, fields...)
}

var masterMemeReferences struct {
	sync.RWMutex
	provider   memereference.Provider
	revalidate memereference.Revalidator
}

// ConfigureMemeReferences installs the bootstrap-owned lookup. A nil provider
// keeps ordinary MASTER generation unchanged.
func ConfigureMemeReferences(provider memereference.Provider) {
	ConfigureMemeReferenceRuntime(provider, nil)
}
func ConfigureMemeReferenceRuntime(provider memereference.Provider, revalidate memereference.Revalidator) {
	masterMemeReferences.Lock()
	masterMemeReferences.provider = provider
	masterMemeReferences.revalidate = revalidate
	masterMemeReferences.Unlock()
}

func withMemeReferencePlan(ctx context.Context, event *larkim.P2MessageReceiveV1, plan StandardChatPlan) StandardChatPlan {
	masterMemeReferences.RLock()
	provider := masterMemeReferences.provider
	revalidate := masterMemeReferences.revalidate
	masterMemeReferences.RUnlock()
	if provider == nil {
		return plan
	}
	messages := plan.messageList
	if plan.noMemeReferenceHistory {
		messages = nil
	}
	dialogue := originalMemeReferenceDialogue(event, messages)
	if len(dialogue) == 0 {
		return plan
	}
	const replyReserve = 3 * time.Second
	lookupDeadline := time.Now().Add(12 * time.Second)
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) <= replyReserve {
			return plan
		}
		if earlier := deadline.Add(-replyReserve); earlier.Before(lookupDeadline) {
			lookupDeadline = earlier
		}
	}
	lookupCtx, cancel := context.WithDeadline(ctx, lookupDeadline)
	defer cancel()
	if lookupCtx.Err() != nil {
		return plan
	}
	started := time.Now()
	result := provider(lookupCtx, currentChatID(event, nil), currentMessageID(event), dialogue)
	if revalidate != nil {
		clean, cards, err := revalidate(ctx, currentChatID(event, nil), result.ContextV2, result.Cards)
		if err != nil {
			result.ContextV2 = nil
			result.Cards = nil
			result.Status = memes.ReferenceStatusError
			result.Reason = "SOURCE_POLICY_UNAVAILABLE"
			result.Degraded = true
		} else {
			result.ContextV2 = clean
			result.Cards = cards
			if result.Status == memes.ReferenceStatusReady && len(cards) == 0 {
				result.Status = memes.ReferenceStatusEmpty
			}
		}
	}
	cards := memereference.PromptCards(result)
	ids, names := make([]string, 0, len(cards)), make([]string, 0, len(cards))
	for _, card := range cards {
		ids = append(ids, card.UseID)
		names = append(names, card.Name)
	}
	memeReferenceLog(ctx, "meme reference retrieval",
		zap.Any("grounded", memereference.GroundedDiagnostics(result)),
		zap.String("chat_id", currentChatID(event, nil)), zap.String("message_id", currentMessageID(event)),
		zap.String("status", string(result.Status)), zap.Bool("degraded", result.Degraded),
		zap.Strings("candidate_ids", ids), zap.Strings("candidate_names", names),
		zap.Strings("channel_statuses", referenceChannelStatuses(result.Retrieval)),
		zap.Int("candidate_count", len(cards)), zap.Int64("duration_ms", time.Since(started).Milliseconds()),
	)
	if block := memereference.BuildContextBlock(result) + memereference.BuildBlock(result); block != "" {
		plan.memeReferences = &memeReferenceState{cards: cards, chatID: currentChatID(event, nil), messageID: currentMessageID(event), startedAt: time.Now(), originalSystem: plan.SystemPrompt, originalUser: plan.UserPrompt}
		plan.memeReferences.baseSystem = plan.SystemPrompt
		plan.memeReferences.baseUser = plan.UserPrompt
		plan.memeReferences.knowledge = memereference.PromptContext(result)
		plan.memeReferences.revalidate = revalidate
		plan.memeReferences.identifiers = memereference.GroundedIdentifiers(plan.memeReferences.knowledge)
		if prior := memereference.BuildContextBlock(result); prior != "" {
			plan.memeReferences.originalSystem += "\n\n" + memereference.KnowledgeInstruction
			plan.memeReferences.originalUser += prior
			plan.SystemPrompt += "\n\n" + memereference.KnowledgeInstruction
		}
		plan.SystemPrompt += "\n\n" + memereference.Instruction + "\n\n" + memereference.SelectionInstruction
		plan.UserPrompt += block
	}
	return plan
}

// referenceChannelStatuses logs only bounded routing labels. Raw query text,
// evidence, remote errors and hit bodies never enter this diagnostic view.
func referenceChannelStatuses(snapshot memes.RetrievalSnapshot) []string {
	queries := snapshot.RawQueries
	if len(queries) == 0 {
		queries = snapshot.Queries
	}
	const maxChannels = 13 // three hybrid fields plus one description vector query
	statuses := make([]string, 0, min(len(queries)*2, maxChannels))
	appendStatus := func(field, channel, status string) {
		switch channel {
		case "vector", "fulltext":
		default:
			channel = "unknown"
		}
		switch status {
		case "ok", "error", "partial", "skipped", "timeout":
		default:
			status = "unknown"
		}
		statuses = append(statuses, field+":"+channel+":"+status)
	}
	for _, query := range queries {
		field := string(query.Query.Field)
		switch query.Query.Field {
		case memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys, memes.FieldDescription:
		default:
			field = "unknown"
		}
		if len(query.Channels) == 0 {
			appendStatus(field, "unknown", query.Status)
		}
		for _, channel := range query.Channels {
			if len(statuses) == maxChannels {
				return statuses
			}
			appendStatus(field, channel.Channel, channel.Status)
		}
		if len(statuses) == maxChannels {
			return statuses
		}
	}
	return statuses
}

// originalMemeReferenceDialogue supplies source text, never rendered history,
// retrieved summaries, keyword reply tasks, or model-supplied dialogue.
func originalMemeReferenceDialogue(event *larkim.P2MessageReceiveV1, messages history.OpensearchMsgLogList) []memes.Turn {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	chatID, messageID, speaker := currentChatID(event, nil), currentMessageID(event), currentOpenID(event, nil)
	if chatID == "" || messageID == "" || speaker == "" {
		return nil
	}
	anchor, err := eventAnchorTime(event)
	if err != nil {
		return nil
	}
	message := event.Event.Message
	raw, ok := history.OriginalText(pointerString(message.MessageType), pointerString(message.Content))
	if !ok {
		return nil
	}
	local := make(history.OpensearchMsgLogList, 0, len(messages))
	for _, item := range messages {
		if item == nil || item.ChatID != chatID || item.MessageID == "" || item.MessageID == messageID || item.OpenID == "" || item.RawText == nil || strings.TrimSpace(*item.RawText) == "" {
			continue
		}
		occurredAt, reason := parseCausalContextTime(item.CreateTimeV2, anchor)
		if reason != "" || !occurredAt.Before(anchor) {
			continue
		}
		local = append(local, item)
	}
	slices.SortStableFunc(local, func(a, b *history.OpensearchMsgLog) int {
		at, _ := parseContextTimeValue(a.CreateTimeV2)
		bt, _ := parseContextTimeValue(b.CreateTimeV2)
		if at.Before(bt) {
			return -1
		}
		if at.After(bt) {
			return 1
		}
		return strings.Compare(a.MessageID, b.MessageID)
	})
	dialogue := make([]memes.Turn, 0, min(len(local)+1, memes.MaxMessages))
	seen := make(map[string]bool)
	for _, item := range local {
		if !seen[item.MessageID] {
			dialogue = append(dialogue, memes.Turn{TurnID: item.MessageID, Speaker: item.OpenID, Text: *item.RawText})
			seen[item.MessageID] = true
		}
	}
	dialogue = append(dialogue, memes.Turn{TurnID: messageID, Speaker: speaker, Text: raw})
	if len(dialogue) > memes.MaxMessages {
		dialogue = dialogue[len(dialogue)-memes.MaxMessages:]
	}
	withinBudget := func() bool {
		runes, bytes := 0, 0
		for _, turn := range dialogue {
			runes += len([]rune(turn.Text))
			bytes += len(turn.Text)
		}
		return runes <= memes.MaxInputRunes && bytes <= memes.MaxInputBytes
	}
	for len(dialogue) > 1 && !withinBudget() {
		dialogue = dialogue[1:]
	}
	if !withinBudget() {
		return nil
	}
	return dialogue
}

func executeTwoPhaseReferenceGenerator(ctx context.Context, event *larkim.P2MessageReceiveV1, plan StandardChatPlan, scope llmusage.Scope, executor standardChatExecutor) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	plan = withMemeReferencePlan(ctx, event, plan)
	if conversationeval.CaptureEnabled(ctx) {
		recordStandardChatPlan(ctx, event, plan)
	}
	stream, err := executor.Do(ctx, scope, plan.SystemPrompt, plan.UserPrompt, plan.Files...)
	if err != nil {
		return nil, err
	}
	if plan.memeReferences == nil {
		return stream, nil
	}
	return wrapMemeReferenceOutput(ctx, stream, plan, true, func() (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		system, user := recoverMemeReferenceContext(ctx, plan.memeReferences)
		return executor.Do(ctx, scope, system, user, plan.Files...)
	}), nil
}

func recoverMemeReferenceContext(ctx context.Context, state *memeReferenceState) (string, string) {
	if state.revalidate == nil {
		return state.originalSystem, state.originalUser
	}
	clean, cards, err := state.revalidate(ctx, state.chatID, state.knowledge, state.cards)
	if err != nil {
		clean = nil
		cards = nil
	}
	result := memes.ReferenceResult{Status: memes.ReferenceStatusReady, ContextV2: clean, Cards: cards}
	state.knowledge = memereference.PromptContext(result)
	state.cards = memereference.PromptCards(result)
	state.identifiers = append(state.identifiers, memereference.GroundedIdentifiers(state.knowledge)...)
	system, user := state.baseSystem, state.baseUser
	if block := memereference.BuildContextBlock(result); block != "" {
		system += "\n\n" + memereference.KnowledgeInstruction
		user += block
	}
	return system, user
}

func safeMemeStateReply(text string, state *memeReferenceState) bool {
	return safeReferenceVisibleReply(text, state.cards) && !memereference.ContainsSuppliedIdentifier(text, state.identifiers)
}

type decodedMemeReferenceReply struct {
	Reply           string
	UseIDs          []string
	SelectionStatus string
	JSONObject      bool
}

// decodeMemeReferenceReply extracts only actual reply text. A malformed envelope
// can retain an already complete reply string, but cannot claim attribution.
func decodeMemeReferenceReply(raw string) decodedMemeReferenceReply {
	result := decodedMemeReferenceReply{SelectionStatus: "parse_failed"}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) == nil && object != nil {
		result.JSONObject = true
		var reply *string
		if json.Unmarshal(object["reply"], &reply) != nil || reply == nil {
			return result
		}
		result.Reply = *reply
		result.SelectionStatus = "unreported"
		if selection, present := object["meme_use_ids"]; present {
			if string(selection) == "null" || json.Unmarshal(selection, &result.UseIDs) != nil || len(result.UseIDs) > memes.CandidateBudget {
				result.UseIDs = nil
				result.SelectionStatus = "parse_failed"
			} else {
				result.SelectionStatus = "reported"
			}
		}
	} else {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "{") {
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			if _, err := decoder.Token(); err == nil {
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						break
					}
					var value json.RawMessage
					if decoder.Decode(&value) != nil {
						break
					}
					if key == "reply" {
						_ = json.Unmarshal(value, &result.Reply)
					}
				}
			}
		} else if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "```") {
			result.Reply = trimmed
		}
	}
	if referenceProtocolInText(result.Reply) {
		result.Reply = ""
		result.UseIDs = nil
		result.SelectionStatus = "parse_failed"
	}
	return result
}

func referenceProtocolInText(text string) bool {
	for _, token := range []string{"meme_use_ids", "MemeKnowledge", `"prior_knowledge"`, `"entry_fingerprint"`, `"anchor_ids"`, `"knowledge_ids"`, "=== 梗背景与语境参考 ===", `"use_id"`, `"input_relation"`, `"requirements"`, "=== 重要梗用法参考 ===", "候选列表", "检索过程", "选择过程"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func safeReferenceVisibleReply(text string, cards []memes.UseCard) bool {
	if referenceProtocolInText(text) {
		return false
	}
	for _, card := range cards {
		if card.UseID != "" && strings.Contains(text, card.UseID) {
			return false
		}
		if card.Name == "" {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, card.Name) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "• ") {
				return false
			}
			end := 0
			for end < len(trimmed) && trimmed[end] >= '0' && trimmed[end] <= '9' {
				end++
			}
			if end > 0 && (strings.HasPrefix(trimmed[end:], ".") || strings.HasPrefix(trimmed[end:], ")") || strings.HasPrefix(trimmed[end:], "、")) {
				return false
			}
			for _, narration := range []string{"候选", "检索", "排名", "我选择", "我选了", "我选用"} {
				if strings.Contains(line, narration) {
					return false
				}
			}
		}
	}
	return true
}

func collectReferenceOutput(stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) string {
	var content strings.Builder
	for data := range stream {
		if data != nil {
			content.WriteString(data.Content)
		}
	}
	return content.String()
}

func safeRejectedReferenceIDs(ids []string) []string {
	result := make([]string, 0, min(len(ids), memes.CandidateBudget))
	for _, id := range ids {
		if len(result) == memes.CandidateBudget {
			break
		}
		safe := id != "" && len(id) <= 128
		for _, char := range id {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._:/-", char)) {
				safe = false
				break
			}
		}
		if !safe {
			id = "[invalid_id]"
		}
		result = append(result, id)
	}
	return result
}

// Requests with references use a private JSON envelope. Only decoded natural
// reply text crosses this boundary; candidates and selection IDs stay internal.
func wrapMemeReferenceOutput(ctx context.Context, stream iter.Seq[*ark_dal.ModelStreamRespReasoning], plan StandardChatPlan, plain bool, recover func() (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		state := plan.memeReferences
		raw := collectReferenceOutput(stream)
		decoded := decodeMemeReferenceReply(raw)
		output := ark_dal.ContentStruct{Decision: "reply", Reply: decoded.Reply}
		if !plain && decoded.JSONObject {
			var standard ark_dal.ContentStruct
			if json.Unmarshal([]byte(raw), &standard) == nil && standard.Decision == "skip" {
				output.Decision = "skip"
				output.Reply = ""
			}
		}
		if !safeMemeStateReply(output.Reply, state) {
			output.Reply = ""
			decoded.SelectionStatus = "parse_failed"
			decoded.UseIDs = nil
		}
		if output.Decision != "skip" && output.Reply == "" && (!decoded.JSONObject || decoded.SelectionStatus == "parse_failed") {
			decoded.SelectionStatus = "parse_failed"
			decoded.UseIDs = nil
			if recover != nil {
				if fallback, err := recover(); err == nil {
					fallbackRaw := collectReferenceOutput(fallback)
					if plain {
						output.Reply = strings.TrimSpace(fallbackRaw)
						if output.Reply == "" {
							output.Decision = "skip"
						}
					} else {
						output.Reply = decodeMemeReferenceReply(fallbackRaw).Reply
						var ordinary ark_dal.ContentStruct
						if json.Unmarshal([]byte(fallbackRaw), &ordinary) == nil && ordinary.Decision == "skip" {
							output.Decision = "skip"
							output.Reply = ""
						}
					}
				}
			}
			if output.Decision != "skip" && (output.Reply == "" || !safeMemeStateReply(output.Reply, state)) {
				output.Reply = "这句我没接明白，可以再说具体一点吗？"
			}
		}
		accepted, rejected := []string{}, []string{}
		status := decoded.SelectionStatus
		if status == "reported" {
			accepted, rejected = memereference.FilterReportedSelection(state.cards, decoded.UseIDs)
			if len(rejected) > 0 {
				status = "reported_with_rejections"
			} else if len(accepted) == 0 {
				status = "reported_unused"
			}
		}
		if output.Decision == "skip" || output.Reply == "" {
			accepted = []string{}
			if status != "parse_failed" {
				status = "skipped"
			}
		}
		memeReferenceLog(ctx, "meme reference selection",
			zap.String("chat_id", state.chatID), zap.String("message_id", state.messageID),
			zap.String("selection_source", "model_reported"), zap.String("selection_status", status),
			zap.String("reply_decision", output.Decision),
			zap.Strings("selected_ids", accepted), zap.Strings("rejected_ids", safeRejectedReferenceIDs(rejected)),
			zap.Int("selected_count", len(accepted)), zap.Int("rejected_count", len(rejected)),
			zap.Int("reply_chars", len([]rune(output.Reply))), zap.Int64("duration_ms", time.Since(state.startedAt).Milliseconds()),
		)
		if normalized, err := mention.NormalizeReplyText(ctx, plan.chatID, plan.messageList, output.Reply); err == nil {
			output.Reply = normalized
		}
		final := &ark_dal.ModelStreamRespReasoning{ContentStruct: output}
		if plain {
			final.Content = output.Reply
		}
		yield(final)
	}
}
