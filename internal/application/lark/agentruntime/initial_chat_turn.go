package agentruntime

import (
	"context"
	"errors"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

type InitialChatToolOutput struct {
	CallID string
	Output string
}

type InitialChatTurnRequest struct {
	Plan               InitialChatExecutionPlan
	PreviousResponseID string
	ToolOutput         *InitialChatToolOutput
}

type InitialChatToolCall struct {
	CallID       string
	FunctionName string
	Arguments    string
}

type InitialChatTurnSnapshot struct {
	ResponseID string
	ToolCall   *InitialChatToolCall
}

type InitialChatTurnResult struct {
	Stream   iter.Seq[*ark_dal.ModelStreamRespReasoning]
	Snapshot func() InitialChatTurnSnapshot
}

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

func mapInitialChatToolOutput(output *InitialChatToolOutput) *ark_dal.ToolOutputInput {
	if output == nil {
		return nil
	}
	return &ark_dal.ToolOutputInput{
		CallID: strings.TrimSpace(output.CallID),
		Output: output.Output,
	}
}
