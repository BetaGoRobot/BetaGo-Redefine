package chatflow

import (
	"context"
	"errors"
	"iter"
	"strings"

	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ExecuteInitialChatTurn implements chat flow behavior.
func ExecuteInitialChatTurn(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
	plan := req.Plan
	if strings.TrimSpace(plan.ModelID) == "" {
		return InitialChatTurnResult{}, errors.New("chat model id is required")
	}
	if strings.TrimSpace(plan.ChatID) == "" {
		return InitialChatTurnResult{}, errors.New("chat id is required")
	}
	if strings.TrimSpace(plan.OpenID) == "" {
		return InitialChatTurnResult{}, errors.New("open id is required")
	}
	if strings.TrimSpace(req.PreviousResponseID) == "" &&
		strings.TrimSpace(plan.Prompt) == "" &&
		(plan.Event == nil || plan.Event.Event == nil || plan.Event.Event.Message == nil) {
		return InitialChatTurnResult{}, errors.New("chat event or prompt is required")
	}
	if strings.TrimSpace(req.PreviousResponseID) == "" && strings.TrimSpace(plan.Prompt) == "" {
		return InitialChatTurnResult{}, errors.New("prompt is required")
	}
	if plan.Tools == nil {
		return InitialChatTurnResult{}, errors.New("chat tools are required")
	}

	turn := ark_dal.New(plan.ChatID, plan.OpenID, plan.Event).WithTools(plan.Tools)
	stream, snapshot, err := turn.StreamTurn(ctx, ark_dal.ResponseTurnRequest{
		ModelID:            strings.TrimSpace(plan.ModelID),
		SystemPrompt:       plan.Prompt,
		UserPrompt:         plan.UserInput,
		ReasoningEffort:    plan.ReasoningEffort,
		Files:              append([]string(nil), plan.Files...),
		PreviousResponseID: strings.TrimSpace(req.PreviousResponseID),
		ToolOutput:         mapInitialChatToolOutput(req.ToolOutput),
	})
	if err != nil {
		return InitialChatTurnResult{}, err
	}
	return InitialChatTurnResult{
		Stream: stream,
		Snapshot: func() InitialChatTurnSnapshot {
			raw := snapshot()
			mapped := InitialChatTurnSnapshot{
				ResponseID: strings.TrimSpace(raw.ResponseID),
			}
			if raw.ToolCall != nil {
				mapped.ToolCall = &InitialChatToolCall{
					CallID:       strings.TrimSpace(raw.ToolCall.CallID),
					FunctionName: strings.TrimSpace(raw.ToolCall.FunctionName),
					Arguments:    strings.TrimSpace(raw.ToolCall.Arguments),
				}
			}
			return mapped
		},
	}, nil
}

// ExecuteInitialChatExecutionPlan implements chat flow behavior.
func ExecuteInitialChatExecutionPlan(ctx context.Context, plan InitialChatExecutionPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	turn, err := ExecuteInitialChatTurn(ctx, InitialChatTurnRequest{Plan: plan})
	if err != nil {
		return nil, err
	}
	return turn.Stream, nil
}

// FinalizeInitialChatStream implements chat flow behavior.
func FinalizeInitialChatStream(
	ctx context.Context,
	plan InitialChatExecutionPlan,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return finalizeGeneratedChatStream(ctx, plan.ChatID, plan.MessageList, stream)
}

// ChatIDFromEvent implements chat flow behavior.
func ChatIDFromEvent(event *larkim.P2MessageReceiveV1) string {
	return message.ChatID(event)
}

// OpenIDFromEvent implements chat flow behavior.
func OpenIDFromEvent(event *larkim.P2MessageReceiveV1) string {
	return botidentity.MessageSenderOpenID(event)
}

func mapInitialChatToolOutput(output *InitialChatToolOutput) *ark_dal.ToolOutputInput {
	if output == nil {
		return nil
	}
	return &ark_dal.ToolOutputInput{
		CallID: strings.TrimSpace(output.CallID),
		Output: output.Output,
	}
}

func finalizeGeneratedChatStream(
	ctx context.Context,
	chatID string,
	messageList history.OpensearchMsgLogList,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		contentBuilder := &strings.Builder{}

		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range stream {
			lastData = data
			contentBuilder.WriteString(data.Content)

			if !yield(data) {
				return
			}
		}

		fullContent := contentBuilder.String()
		lastData.ContentStruct = message.ParseContentStruct(fullContent)
		if normalizedReply, normalizeErr := mention.NormalizeReplyText(ctx, chatID, messageList, lastData.ContentStruct.Reply); normalizeErr == nil {
			lastData.ContentStruct.Reply = normalizedReply
		}
		if !yield(lastData) {
			return
		}
	}
}
