package replay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	ops "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	ErrReplayTargetNotFound     = errors.New("replay target message not found")
	ErrReplayTargetChatMismatch = errors.New("replay target chat_id mismatch")
)

type replayMessageLoader func(context.Context, string) (*larkim.Message, error)

type replayIntentInputOptions struct {
	ContextEnabled *bool
	HistoryLimit   *int
	ProfileLimit   *int
}

type replayIntentInputPreview struct {
	Input          string
	ContextEnabled bool
	HistoryLimit   int
	ProfileLimit   int
	HistoryLines   []string
	ProfileLines   []string
}

type replayIntentInputBuilder func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, string, replayIntentInputOptions) replayIntentInputPreview
type replayIntentAnalyzer func(context.Context, string) (*intent.IntentAnalysis, error)

type IntentReplayService struct {
	loadMessage         replayMessageLoader
	buildIntentInput    replayIntentInputBuilder
	analyzeIntent       replayIntentAnalyzer
	standardPlanBuilder func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error)
	agenticPlanBuilder  func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error)
	executeTurn         func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error)
}

type loadedReplayTarget struct {
	Target  ReplayTarget
	Message *larkim.Message
	Event   *larkim.P2MessageReceiveV1
}

type ReplayBuildOptions struct {
	HistoryLimit   *int
	ProfileLimit   *int
	DisableHistory bool
	DisableProfile bool
}

type ReplayRunOptions struct {
	ReplayBuildOptions
	LiveModel bool
}

func (s IntentReplayService) Replay(ctx context.Context, chatID, messageID string, options ReplayRunOptions) (ReplayReport, error) {
	loaded, err := s.loadReplayTarget(ctx, chatID, messageID)
	if err != nil {
		return ReplayReport{}, err
	}
	cases, err := s.buildCases(ctx, loaded, options.ReplayBuildOptions)
	if err != nil {
		return ReplayReport{}, err
	}
	if options.LiveModel {
		cases, err = s.populateIntentAnalysis(ctx, cases)
		if err != nil {
			return ReplayReport{}, err
		}
		cases, err = s.replayConversation(ctx, loaded, cases, true)
		if err != nil {
			return ReplayReport{}, err
		}
	}

	return ReplayReport{
		Target:             loaded.Target,
		RuntimeObservation: s.buildRuntimeObservation(ctx, loaded),
		Cases:              cases,
		Diff:               buildReplayDiff(cases),
	}, nil
}

func (s IntentReplayService) LoadTarget(ctx context.Context, chatID, messageID string) (ReplayTarget, error) {
	loaded, err := s.loadReplayTarget(ctx, chatID, messageID)
	if err != nil {
		return ReplayTarget{}, err
	}
	return loaded.Target, nil
}

func (s IntentReplayService) loadReplayTarget(ctx context.Context, chatID, messageID string) (loadedReplayTarget, error) {
	chatID = strings.TrimSpace(chatID)
	messageID = strings.TrimSpace(messageID)

	message, err := s.messageLoader()(ctx, messageID)
	if err != nil {
		return loadedReplayTarget{}, err
	}
	target, err := normalizeReplayTarget(chatID, messageID, message)
	if err != nil {
		return loadedReplayTarget{}, err
	}
	return loadedReplayTarget{
		Target:  target,
		Message: message,
		Event:   eventFromFetchedMessage(message, target.ChatType),
	}, nil
}

func (s IntentReplayService) messageLoader() replayMessageLoader {
	if s.loadMessage != nil {
		return s.loadMessage
	}
	return defaultReplayMessageLoader
}

func (s IntentReplayService) intentInputBuilder() replayIntentInputBuilder {
	if s.buildIntentInput != nil {
		return s.buildIntentInput
	}
	return defaultReplayIntentInputBuilder
}

func (s IntentReplayService) analyzer() replayIntentAnalyzer {
	if s.analyzeIntent != nil {
		return s.analyzeIntent
	}
	return intent.AnalyzeMessage
}

func defaultReplayMessageLoader(ctx context.Context, messageID string) (*larkim.Message, error) {
	resp := larkmsg.GetMsgFullByID(ctx, strings.TrimSpace(messageID))
	if resp == nil || !resp.Success() || resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0] == nil {
		return nil, fmt.Errorf("%w: message_id=%s", ErrReplayTargetNotFound, strings.TrimSpace(messageID))
	}
	return resp.Data.Items[0], nil
}

func normalizeReplayTarget(chatID, messageID string, message *larkim.Message) (ReplayTarget, error) {
	if message == nil {
		return ReplayTarget{}, fmt.Errorf("%w: message_id=%s", ErrReplayTargetNotFound, messageID)
	}

	actualChatID := strings.TrimSpace(valueOrEmpty(message.ChatId))
	if actualChatID == "" {
		actualChatID = chatID
	}
	if chatID != "" && actualChatID != "" && !strings.EqualFold(actualChatID, chatID) {
		return ReplayTarget{}, fmt.Errorf("%w: want=%s got=%s", ErrReplayTargetChatMismatch, chatID, actualChatID)
	}

	actualMessageID := strings.TrimSpace(valueOrEmpty(message.MessageId))
	if actualMessageID == "" {
		actualMessageID = messageID
	}

	target := ReplayTarget{
		ChatID:    actualChatID,
		MessageID: actualMessageID,
		OpenID:    replaySenderOpenID(message),
		ChatType:  normalizeReplayChatType(chatID, actualChatID),
		Text:      messageTextFromFetchedMessage(message),
	}
	return target, nil
}

func (s IntentReplayService) buildCases(ctx context.Context, loaded loadedReplayTarget, options ReplayBuildOptions) ([]ReplayCase, error) {
	meta := &xhandler.BaseMetaData{
		ChatID: loaded.Target.ChatID,
		OpenID: loaded.Target.OpenID,
	}
	builder := s.intentInputBuilder()

	baselinePreview := builder(ctx, loaded.Event, meta, loaded.Target.Text, replayIntentInputOptions{
		ContextEnabled: boolPtr(false),
		HistoryLimit:   intPtr(0),
		ProfileLimit:   intPtr(0),
	})
	augmentedPreview := builder(ctx, loaded.Event, meta, loaded.Target.Text, buildAugmentedIntentInputOptions(options))

	return []ReplayCase{
		newReplayCase(ReplayCaseBaseline, baselinePreview),
		newReplayCase(ReplayCaseAugmented, augmentedPreview),
	}, nil
}

func buildAugmentedIntentInputOptions(options ReplayBuildOptions) replayIntentInputOptions {
	result := replayIntentInputOptions{
		ContextEnabled: boolPtr(true),
	}
	if options.HistoryLimit != nil {
		result.HistoryLimit = intPtr(*options.HistoryLimit)
	}
	if options.ProfileLimit != nil {
		result.ProfileLimit = intPtr(*options.ProfileLimit)
	}
	if options.DisableHistory {
		result.HistoryLimit = intPtr(0)
	}
	if options.DisableProfile {
		result.ProfileLimit = intPtr(0)
	}
	return result
}

func newReplayCase(name ReplayCaseName, preview replayIntentInputPreview) ReplayCase {
	return ReplayCase{
		Name:                 name,
		IntentContextEnabled: preview.ContextEnabled,
		HistoryLimit:         preview.HistoryLimit,
		ProfileLimit:         preview.ProfileLimit,
		IntentInput:          preview.Input,
		IntentContext: ReplayIntentContext{
			HistoryLines: preview.HistoryLines,
			ProfileLines: preview.ProfileLines,
		},
	}
}

func defaultReplayIntentInputBuilder(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	currentText string,
	options replayIntentInputOptions,
) replayIntentInputPreview {
	preview := ops.BuildIntentAnalyzeInputPreview(ctx, event, meta, currentText, ops.IntentAnalyzeInputBuildOptions{
		ContextEnabled: options.ContextEnabled,
		HistoryLimit:   options.HistoryLimit,
		ProfileLimit:   options.ProfileLimit,
	})
	return replayIntentInputPreview{
		Input:          preview.Input,
		ContextEnabled: preview.ContextEnabled,
		HistoryLimit:   preview.HistoryLimit,
		ProfileLimit:   preview.ProfileLimit,
		HistoryLines:   preview.HistoryLines,
		ProfileLines:   preview.ProfileLines,
	}
}

func (s IntentReplayService) populateIntentAnalysis(ctx context.Context, cases []ReplayCase) ([]ReplayCase, error) {
	analyzer := s.analyzer()
	out := make([]ReplayCase, 0, len(cases))
	for _, item := range cases {
		analysis, err := analyzer(ctx, strings.TrimSpace(item.IntentInput))
		if err != nil {
			return nil, fmt.Errorf("replay analyze %s: %w", item.Name, err)
		}
		if analysis != nil {
			analysis.Sanitize()
			item.IntentAnalysis = analysis
			item.RouteDecision = &ReplayRouteDecision{
				FinalMode: string(analysis.InteractionMode.Normalize()),
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func buildReplayDiff(cases []ReplayCase) ReplayDiff {
	baseline, augmented, ok := replayCasePair(cases)
	if !ok {
		return ReplayDiff{}
	}

	diff := ReplayDiff{}
	if strings.TrimSpace(baseline.IntentInput) != strings.TrimSpace(augmented.IntentInput) {
		diff.IntentInputChanged = true
		diff.ChangedFields = append(diff.ChangedFields, "intent_input")
	}

	if field := changedIntentAnalysisFields(baseline.IntentAnalysis, augmented.IntentAnalysis); len(field) > 0 {
		diff.ChangedFields = append(diff.ChangedFields, field...)
		for _, name := range field {
			if name == "intent_analysis.interaction_mode" {
				diff.InteractionModeChanged = true
			}
		}
	}

	if changedRouteDecision(baseline.RouteDecision, augmented.RouteDecision) {
		diff.RouteChanged = true
		diff.ChangedFields = append(diff.ChangedFields, "route_decision.final_mode")
	}
	if fields := changedConversationOutputFields(baseline.Conversation, augmented.Conversation); len(fields) > 0 {
		diff.GenerationChanged = true
		diff.ChangedFields = append(diff.ChangedFields, fields...)
	}
	if fields := changedToolIntentFields(baseline.Conversation, augmented.Conversation); len(fields) > 0 {
		diff.ToolIntentChanged = true
		diff.ChangedFields = append(diff.ChangedFields, fields...)
	}
	diff.ChangedFields = diff.ChangedFieldNames()
	return diff
}

func replayCasePair(cases []ReplayCase) (ReplayCase, ReplayCase, bool) {
	var baseline ReplayCase
	var augmented ReplayCase
	var baselineFound bool
	var augmentedFound bool
	for _, item := range cases {
		switch item.Name {
		case ReplayCaseBaseline:
			baseline = item
			baselineFound = true
		case ReplayCaseAugmented:
			augmented = item
			augmentedFound = true
		}
	}
	return baseline, augmented, baselineFound && augmentedFound
}

func changedIntentAnalysisFields(baseline, augmented *intent.IntentAnalysis) []string {
	if baseline == nil || augmented == nil {
		return nil
	}
	changed := make([]string, 0, 5)
	if baseline.IntentType != augmented.IntentType {
		changed = append(changed, "intent_analysis.intent_type")
	}
	if baseline.NeedReply != augmented.NeedReply {
		changed = append(changed, "intent_analysis.need_reply")
	}
	if baseline.InteractionMode.Normalize() != augmented.InteractionMode.Normalize() {
		changed = append(changed, "intent_analysis.interaction_mode")
	}
	if baseline.NeedsHistory != augmented.NeedsHistory {
		changed = append(changed, "intent_analysis.needs_history")
	}
	if baseline.NeedsWeb != augmented.NeedsWeb {
		changed = append(changed, "intent_analysis.needs_web")
	}
	return changed
}

func changedRouteDecision(baseline, augmented *ReplayRouteDecision) bool {
	if baseline == nil || augmented == nil {
		return false
	}
	return strings.TrimSpace(baseline.FinalMode) != strings.TrimSpace(augmented.FinalMode)
}

func changedConversationOutputFields(baseline, augmented *ReplayConversation) []string {
	baselineOutput := replayConversationOutput(baseline)
	augmentedOutput := replayConversationOutput(augmented)
	if baselineOutput == nil && augmentedOutput == nil {
		return nil
	}
	if baselineOutput == nil || augmentedOutput == nil {
		return []string{
			"conversation.output.decision",
			"conversation.output.reply",
			"conversation.output.reference_from_web",
			"conversation.output.reference_from_history",
		}
	}

	changed := make([]string, 0, 5)
	if strings.TrimSpace(baselineOutput.Decision) != strings.TrimSpace(augmentedOutput.Decision) {
		changed = append(changed, "conversation.output.decision")
	}
	if strings.TrimSpace(baselineOutput.Reply) != strings.TrimSpace(augmentedOutput.Reply) {
		changed = append(changed, "conversation.output.reply")
	}
	if strings.TrimSpace(baselineOutput.ReferenceFromWeb) != strings.TrimSpace(augmentedOutput.ReferenceFromWeb) {
		changed = append(changed, "conversation.output.reference_from_web")
	}
	if strings.TrimSpace(baselineOutput.ReferenceFromHistory) != strings.TrimSpace(augmentedOutput.ReferenceFromHistory) {
		changed = append(changed, "conversation.output.reference_from_history")
	}
	return changed
}

func changedToolIntentFields(baseline, augmented *ReplayConversation) []string {
	baselineIntent := replayToolIntent(baseline)
	augmentedIntent := replayToolIntent(augmented)
	if baselineIntent == nil && augmentedIntent == nil {
		return nil
	}
	if baselineIntent == nil || augmentedIntent == nil {
		return []string{
			"conversation.tool_intent.would_call_tools",
			"conversation.tool_intent.function_name",
			"conversation.tool_intent.arguments",
		}
	}

	changed := make([]string, 0, 3)
	if baselineIntent.WouldCallTools != augmentedIntent.WouldCallTools {
		changed = append(changed, "conversation.tool_intent.would_call_tools")
	}
	if strings.TrimSpace(baselineIntent.FunctionName) != strings.TrimSpace(augmentedIntent.FunctionName) {
		changed = append(changed, "conversation.tool_intent.function_name")
	}
	if strings.TrimSpace(baselineIntent.Arguments) != strings.TrimSpace(augmentedIntent.Arguments) {
		changed = append(changed, "conversation.tool_intent.arguments")
	}
	return changed
}

func replayConversationOutput(conversation *ReplayConversation) *ReplayConversationOutput {
	if conversation == nil {
		return nil
	}
	return conversation.Output
}

func replayToolIntent(conversation *ReplayConversation) *ReplayToolIntent {
	if conversation == nil {
		return nil
	}
	return conversation.ToolIntent
}

func (s IntentReplayService) buildRuntimeObservation(ctx context.Context, loaded loadedReplayTarget) ReplayRuntimeObservation {
	mentioned := replayIsMentioned(loaded.Event)
	replyToBot := s.isReplyToBot(ctx, loaded.Message)
	observer := agentruntime.NewShadowObserver(
		agentruntime.NewDefaultGroupPolicy(agentruntime.DefaultGroupPolicyConfig{}),
		nil,
		nil,
	)
	observation := observer.Observe(ctx, agentruntime.ShadowObserveInput{
		Now:         time.Now().UTC(),
		ChatID:      loaded.Target.ChatID,
		ChatType:    loaded.Target.ChatType,
		Mentioned:   mentioned,
		ReplyToBot:  replyToBot,
		IsCommand:   replayIsCommand(loaded.Target.Text),
		CommandName: replayCommandName(loaded.Target.Text),
		ActorOpenID: loaded.Target.OpenID,
		InputText:   strings.TrimSpace(loaded.Target.Text),
	})
	return ReplayRuntimeObservation{
		Mentioned:          mentioned,
		ReplyToBot:         replyToBot,
		TriggerType:        string(observation.TriggerType),
		EligibleForAgentic: observation.EnterRuntime,
	}
}

func (s IntentReplayService) isReplyToBot(ctx context.Context, message *larkim.Message) bool {
	if message == nil || message.ParentId == nil {
		return false
	}
	parent, err := s.messageLoader()(ctx, strings.TrimSpace(*message.ParentId))
	if err != nil || parent == nil || parent.Sender == nil || parent.Sender.Id == nil {
		return false
	}
	parentID := strings.TrimSpace(*parent.Sender.Id)
	if parentID == "" {
		return false
	}
	identity := botidentity.Current()
	return (identity.BotOpenID != "" && parentID == identity.BotOpenID) || (identity.AppID != "" && parentID == identity.AppID)
}

func replayIsMentioned(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil || len(event.Event.Message.Mentions) == 0 {
		return false
	}
	return larkmsg.IsMentioned(event.Event.Message.Mentions)
}

func replayIsCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), "/")
}

func replayCommandName(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	parts := strings.Fields(trimmed[1:])
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func messageTextFromFetchedMessage(message *larkim.Message) string {
	event := eventFromFetchedMessage(message, "")
	if event == nil {
		return ""
	}
	return strings.TrimSpace(larkmsg.PreGetTextMsg(context.Background(), event).GetText())
}

func eventFromFetchedMessage(message *larkim.Message, chatType string) *larkim.P2MessageReceiveV1 {
	if message == nil {
		return nil
	}

	eventMessage := &larkim.EventMessage{
		MessageId:   cloneString(message.MessageId),
		RootId:      cloneString(message.RootId),
		ParentId:    cloneString(message.ParentId),
		CreateTime:  cloneString(message.CreateTime),
		ChatId:      cloneString(message.ChatId),
		ChatType:    stringPtrIfNotEmpty(chatType),
		MessageType: cloneString(message.MsgType),
		Mentions:    mentionEventsFromFetchedMessage(message.Mentions),
	}
	if message.Body != nil {
		eventMessage.Content = cloneString(message.Body.Content)
	}

	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: eventMessage,
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: stringPtrIfNotEmpty(replaySenderOpenID(message)),
				},
			},
		},
	}
}

func mentionEventsFromFetchedMessage(mentions []*larkim.Mention) []*larkim.MentionEvent {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]*larkim.MentionEvent, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		out = append(out, &larkim.MentionEvent{
			Key:  cloneString(mention.Key),
			Name: cloneString(mention.Name),
			Id: &larkim.UserId{
				OpenId: cloneString(mention.Id),
			},
		})
	}
	return out
}

func replaySenderOpenID(message *larkim.Message) string {
	if message == nil || message.Sender == nil {
		return ""
	}
	return strings.TrimSpace(valueOrEmpty(message.Sender.Id))
}

func normalizeReplayChatType(requestedChatID, actualChatID string) string {
	if strings.TrimSpace(actualChatID) == "" && strings.TrimSpace(requestedChatID) == "" {
		return ""
	}
	// Current replay scope targets real group-chat samples. The message detail API
	// does not expose chat_type, so we pin the normalized target to group here.
	return "group"
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := strings.TrimSpace(*value)
	return &cloned
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}
