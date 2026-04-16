package ark_dal

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/gg/gptr"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
	"go.uber.org/zap"
)

func (r *ResponsesImpl[T]) StreamTurn(
	ctx context.Context,
	req ResponseTurnRequest,
) (iter.Seq[*ModelStreamRespReasoning], func() ResponseTurnSnapshot, error) {
	if names := responseToolNames(req.AdditionalTools); len(names) > 0 {
		logs.L().Ctx(ctx).Info("manual response turn additional tools",
			zap.Int("tool_count", len(names)),
			zap.Strings("tool_names", names),
		)
	}
	body, err := r.buildTurnRequest(req)
	if err != nil {
		return nil, nil, err
	}
	resp, err := CreateResponsesStream(ctx, body)
	if err != nil {
		return nil, nil, err
	}

	snapshot := ResponseTurnSnapshot{}
	return func(yield func(*ModelStreamRespReasoning) bool) {
			ctx, span := otel.StartNamed(ctx, "ark.responses.turn_stream")
			defer span.End()
			defer r.SyncResult(ctx)

			for {
				event, recvErr := resp.Recv()
				if recvErr == io.EOF {
					snapshot.ResponseID = strings.TrimSpace(r.lastRespID)
					return
				}
				if recvErr != nil {
					logs.L().Ctx(ctx).Error("manual response turn receive error", zap.Error(recvErr))
					return
				}

				toolCall, handleErr := r.handleManualTurnEvent(ctx, event)
				if handleErr != nil {
					logs.L().Ctx(ctx).Error("manual response turn handle event error", zap.Error(handleErr))
					return
				}
				if !r.flushPendingStreamItems(ctx, yield) {
					cleanupResponsesStream(resp)
					return
				}
				if toolCall != nil {
					callCopy := *toolCall
					snapshot.ResponseID = strings.TrimSpace(r.lastRespID)
					snapshot.ToolCall = &callCopy
					cleanupResponsesStream(resp)
					return
				}
			}
		}, func() ResponseTurnSnapshot {
			copied := snapshot
			if snapshot.ToolCall != nil {
				callCopy := *snapshot.ToolCall
				copied.ToolCall = &callCopy
			}
			return copied
		}, nil
}

func (r *ResponsesImpl[T]) buildTurnRequest(req ResponseTurnRequest) (*responses.ResponsesRequest, error) {
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return nil, fmt.Errorf("response turn model id is required")
	}
	reasoningEffort := normalizeResponseTurnReasoningEffort(req.ReasoningEffort)
	mergedTools := mergeResponseTools(r.tools, req.AdditionalTools)

	var input *responses.ResponsesInput
	if previousResponseID := strings.TrimSpace(req.PreviousResponseID); previousResponseID != "" {
		if req.ToolOutput == nil {
			return nil, fmt.Errorf("response turn tool output is required when previous_response_id is set")
		}
		callID := strings.TrimSpace(req.ToolOutput.CallID)
		if callID == "" {
			return nil, fmt.Errorf("response turn tool output call id is required")
		}
		input = &responses.ResponsesInput{
			Union: &responses.ResponsesInput_ListValue{
				ListValue: &responses.InputItemList{
					ListValue: []*responses.InputItem{
						{
							Union: &responses.InputItem_FunctionToolCallOutput{
								FunctionToolCallOutput: &responses.ItemFunctionToolCallOutput{
									CallId: callID,
									Output: utils.MustMarshalString(req.ToolOutput.Output),
									Type:   responses.ItemType_function_call_output,
								},
							},
						},
					},
				},
			},
		}
		return &responses.ResponsesRequest{
			Model:              modelID,
			PreviousResponseId: gptr.Of(previousResponseID),
			Input:              input,
			Store:              gptr.Of(true),
			Tools:              mergedTools,
			// Text: &responses.ResponsesText{
			// 	Format: &responses.TextFormat{
			// 		Type: responses.TextType_json_object,
			// 	},
			// },
			Reasoning: &responses.ResponsesReasoning{
				Effort: reasoningEffort,
			},
			Stream:            gptr.Of(true),
			ParallelToolCalls: gptr.Of(true),
			MaxToolCalls:      gptr.Of(int64(10)),
		}, nil
	}

	items := baseInputItem(req.SystemPrompt, req.UserPrompt)
	if len(req.Files) > 0 {
		items = append(items, buildImageInputMessages(req.Files...)...)
	}
	input = &responses.ResponsesInput{
		Union: &responses.ResponsesInput_ListValue{
			ListValue: &responses.InputItemList{
				ListValue: items,
			},
		},
	}
	return &responses.ResponsesRequest{
		Model: modelID,
		Input: input,
		Store: gptr.Of(true),
		Tools: mergedTools,
		// Text: &responses.ResponsesText{
		// 	Format: &responses.TextFormat{
		// 		Type: responses.TextType_json_object,
		// 	},
		// },
		Reasoning: &responses.ResponsesReasoning{
			Effort: reasoningEffort,
		},
		Stream:            gptr.Of(true),
		ParallelToolCalls: gptr.Of(true),
		MaxToolCalls:      gptr.Of(int64(10)),
	}, nil
}

func responseToolNames(tools []*responses.ResponsesTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if name := responseToolName(tool); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func normalizeResponseTurnReasoningEffort(effort responses.ReasoningEffort_Enum) responses.ReasoningEffort_Enum {
	switch effort {
	case responses.ReasoningEffort_minimal,
		responses.ReasoningEffort_low,
		responses.ReasoningEffort_medium,
		responses.ReasoningEffort_high:
		return effort
	default:
		return responses.ReasoningEffort_medium
	}
}

func (r *ResponsesImpl[T]) handleManualTurnEvent(ctx context.Context, event *responses.Event) (*ToolCallIntent, error) {
	if id := event.GetResponse().GetResponse().GetId(); id != "" {
		r.lastRespID = id
	}

	switch eventType := event.GetEventType(); eventType {
	case responses.EventType_response_output_item_added.String():
		r.OnCallStart(ctx, event)
		return nil, nil
	case responses.EventType_response_function_call_arguments_done.String():
		argsDoneEvent := event.GetFunctionCallArgumentsDone()
		if argsDoneEvent == nil {
			return nil, fmt.Errorf("function call arguments done event is empty")
		}
		callID := strings.TrimSpace(argsDoneEvent.GetItemId())
		functionName := strings.TrimSpace(r.functionCallMap[callID])
		if functionName == "" {
			return nil, fmt.Errorf("manual response turn missing function name for call id %s", callID)
		}
		return &ToolCallIntent{
			CallID:       callID,
			FunctionName: functionName,
			Arguments:    strings.TrimSpace(argsDoneEvent.GetArguments()),
		}, nil
	case responses.EventType_response_reasoning_summary_text_delta.String():
		r.OnReasoningDelta(ctx, event)
		return nil, nil
	case responses.EventType_response_output_text_delta.String():
		r.OnNormalDelta(ctx, event)
		return nil, nil
	default:
		r.OnOthers(ctx, event)
		return nil, nil
	}
}

func cleanupResponsesStream(resp *arkutils.ResponsesStreamReader) {
	if resp == nil {
		return
	}
	if resp.Cleanup != nil {
		resp.Cleanup()
		resp.Cleanup = nil
	}
	resp.IsFinished = true
}
