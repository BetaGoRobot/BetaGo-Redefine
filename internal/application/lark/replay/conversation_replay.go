package replay

import (
	"context"
	"fmt"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

const replayConversationWindowSize = 20

func (s IntentReplayService) replayConversation(
	ctx context.Context,
	loaded loadedReplayTarget,
	cases []ReplayCase,
	liveModel bool,
) ([]ReplayCase, error) {
	out := make([]ReplayCase, 0, len(cases))
	for _, item := range cases {
		mode := replayConversationMode(item)
		if mode == "" {
			out = append(out, item)
			continue
		}

		plan, err := s.buildConversationPlan(ctx, loaded, item, mode)
		if err != nil {
			return nil, fmt.Errorf("replay build conversation %s: %w", item.Name, err)
		}

		item.Conversation = &ReplayConversation{
			Mode:         mode,
			Prompt:       strings.TrimSpace(plan.Prompt),
			UserInput:    strings.TrimSpace(plan.UserInput),
			MaxToolTurns: plan.MaxToolTurns,
		}
		if !liveModel {
			out = append(out, item)
			continue
		}

		turn, err := s.turnExecutor()(ctx, chatflow.InitialChatTurnRequest{Plan: plan})
		if err != nil {
			return nil, fmt.Errorf("replay execute conversation %s: %w", item.Name, err)
		}

		rawContent, reasoningContent, output := collectReplayConversationOutput(turn.Stream)
		if output != nil {
			item.Conversation.Output = output
			item.Conversation.Output.RawContent = rawContent
			item.Conversation.Output.ReasoningContent = reasoningContent
		}

		if snapshot := turn.Snapshot; snapshot != nil {
			if toolCall := snapshot().ToolCall; toolCall != nil {
				item.Conversation.ToolIntent = &ReplayToolIntent{
					WouldCallTools: true,
					CallID:         strings.TrimSpace(toolCall.CallID),
					FunctionName:   strings.TrimSpace(toolCall.FunctionName),
					Arguments:      strings.TrimSpace(toolCall.Arguments),
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s IntentReplayService) buildConversationPlan(
	ctx context.Context,
	loaded loadedReplayTarget,
	item ReplayCase,
	mode string,
) (chatflow.InitialChatExecutionPlan, error) {
	req := chatflow.InitialChatGenerationRequest{
		Event:           loaded.Event,
		ReasoningEffort: replayConversationEffort(item, mode),
		Size:            replayConversationWindowSize,
		Input:           []string{loaded.Target.Text},
	}
	if s.usesDefaultPlanBuilder(mode) {
		req.ModelID = replayConversationModelID(ctx, loaded.Target, mode)
		req.Tools = handlers.BuildRuntimeCapabilityTools()
	}

	if mode == string(appconfig.ChatModeAgentic) {
		return s.agenticBuilder()(ctx, req)
	}
	return s.standardBuilder()(ctx, req)
}

func (s IntentReplayService) usesDefaultPlanBuilder(mode string) bool {
	if strings.EqualFold(strings.TrimSpace(mode), string(appconfig.ChatModeAgentic)) {
		return s.agenticPlanBuilder == nil
	}
	return s.standardPlanBuilder == nil
}

func (s IntentReplayService) standardBuilder() func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
	if s.standardPlanBuilder != nil {
		return s.standardPlanBuilder
	}
	return chatflow.BuildInitialChatExecutionPlan
}

func (s IntentReplayService) agenticBuilder() func(context.Context, chatflow.InitialChatGenerationRequest) (chatflow.InitialChatExecutionPlan, error) {
	if s.agenticPlanBuilder != nil {
		return s.agenticPlanBuilder
	}
	return chatflow.BuildAgenticChatExecutionPlan
}

func (s IntentReplayService) turnExecutor() func(context.Context, chatflow.InitialChatTurnRequest) (chatflow.InitialChatTurnResult, error) {
	if s.executeTurn != nil {
		return s.executeTurn
	}
	return chatflow.ExecuteInitialChatTurn
}

func replayConversationMode(item ReplayCase) string {
	if item.RouteDecision != nil && strings.TrimSpace(item.RouteDecision.FinalMode) != "" {
		return strings.TrimSpace(item.RouteDecision.FinalMode)
	}
	if item.IntentAnalysis != nil {
		mode := item.IntentAnalysis.InteractionMode.Normalize()
		if mode != "" {
			return string(mode)
		}
	}
	return ""
}

func replayConversationModelID(ctx context.Context, target ReplayTarget, mode string) string {
	accessor := appconfig.NewAccessor(ctx, strings.TrimSpace(target.ChatID), strings.TrimSpace(target.OpenID))
	if accessor == nil {
		return ""
	}
	modelID := chatflow.ResolveModelID(accessor, mode)
	if strings.TrimSpace(modelID) != "" {
		return strings.TrimSpace(modelID)
	}
	if strings.EqualFold(strings.TrimSpace(mode), string(appconfig.ChatModeAgentic)) {
		return strings.TrimSpace(accessor.ChatNormalModel())
	}
	return strings.TrimSpace(accessor.ChatReasoningModel())
}

func replayConversationEffort(item ReplayCase, mode string) responses.ReasoningEffort_Enum {
	if item.IntentAnalysis != nil {
		return intent.NormalizeReasoningEffort(item.IntentAnalysis.ReasoningEffort, item.IntentAnalysis.InteractionMode.Normalize())
	}
	if strings.EqualFold(strings.TrimSpace(mode), string(appconfig.ChatModeAgentic)) {
		return intent.DefaultReasoningEffort(intent.InteractionModeAgentic)
	}
	return intent.DefaultReasoningEffort(intent.InteractionModeStandard)
}

func collectReplayConversationOutput(stream func(func(*ark_dal.ModelStreamRespReasoning) bool)) (string, string, *ReplayConversationOutput) {
	if stream == nil {
		return "", "", nil
	}

	var rawBuilder strings.Builder
	var reasoningBuilder strings.Builder
	for item := range stream {
		if item == nil {
			continue
		}
		rawBuilder.WriteString(item.Content)
		reasoningBuilder.WriteString(item.ReasoningContent)
	}

	rawContent := strings.TrimSpace(rawBuilder.String())
	reasoningContent := strings.TrimSpace(reasoningBuilder.String())
	if rawContent == "" && reasoningContent == "" {
		return "", "", nil
	}

	parsed := message.ParseContentStruct(rawContent)
	return rawContent, reasoningContent, &ReplayConversationOutput{
		Decision:             strings.TrimSpace(parsed.Decision),
		Thought:              strings.TrimSpace(parsed.Thought),
		Reply:                strings.TrimSpace(parsed.Reply),
		ReferenceFromWeb:     strings.TrimSpace(parsed.ReferenceFromWeb),
		ReferenceFromHistory: strings.TrimSpace(parsed.ReferenceFromHistory),
	}
}
