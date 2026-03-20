package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"go.opentelemetry.io/otel/attribute"

	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/gg/gresult"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// event-handler模式

type (
	callID               = string
	funcName             = string
	ResponsesImpl[T any] struct {
		meta tools.FCMeta[T]

		handlers               map[funcName]tools.HandlerFunc[T]
		tools                  []*responses.ResponsesTool
		lastCallID             callID
		functionCallMap        map[callID]funcName
		functionInput          map[callID]any
		functionResult         map[callID]gresult.R[string]
		pendingCapabilityCalls []CapabilityCallTrace

		lastRespID string
		textOutput textOutput
	}
	textOutput struct {
		ReasoningTextDelta string
		NormalTextDelta    string

		reasoningText strings.Builder
		normalText    strings.Builder
	}
)

type ContentStruct struct {
	Decision             string `json:"decision"`
	Thought              string `json:"thought"`
	ReferenceFromWeb     string `json:"reference_from_web"`
	ReferenceFromHistory string `json:"reference_from_history"`
	Reply                string `json:"reply"`
}

func (s *ContentStruct) BuildOutput() string {
	output := strings.Builder{}
	if s.Decision != "" {
		output.WriteString(fmt.Sprintf("- 决策: %s\n", s.Decision))
	}
	if s.Thought != "" {
		output.WriteString(fmt.Sprintf("- 思考: %s\n", s.Thought))
	}
	if s.Reply != "" {
		output.WriteString(fmt.Sprintf("- 回复: %s\n", s.Reply))
	}
	if s.ReferenceFromWeb != "" {
		output.WriteString(fmt.Sprintf("- 参考网络: %s\n", s.ReferenceFromWeb))
	}
	if s.ReferenceFromHistory != "" {
		output.WriteString(fmt.Sprintf("- 参考历史: %s\n", s.ReferenceFromHistory))
	}
	return output.String()
}

type ReplyUnit struct {
	ID      string
	Content string
}

type CapabilityCallTrace struct {
	CallID             string
	FunctionName       string
	Arguments          string
	Output             string
	PreviousResponseID string
	Pending            bool
	ApprovalType       string
	ApprovalTitle      string
	ApprovalSummary    string
	ApprovalExpiresAt  time.Time
}

type ToolOutputInput struct {
	CallID string
	Output string
}

type ResponseTurnRequest struct {
	ModelID            string
	SystemPrompt       string
	UserPrompt         string
	Files              []string
	PreviousResponseID string
	ToolOutput         *ToolOutputInput
}

type ToolCallIntent struct {
	CallID       string
	FunctionName string
	Arguments    string
}

type ResponseTurnSnapshot struct {
	ResponseID string
	ToolCall   *ToolCallIntent
}

type ModelStreamRespReasoning struct {
	ReasoningContent string
	Content          string
	ContentStruct    ContentStruct
	CapabilityCall   *CapabilityCallTrace
	Reply2Show       *ReplyUnit
}

type ModelStreamRespReasoningResult struct {
	ReasoningContent strings.Builder
	Content          strings.Builder
	ContentStruct    ContentStruct
	Reply2Show       *ReplyUnit
}

func New[T any](chatID, openID string, data *T) *ResponsesImpl[T] {
	return &ResponsesImpl[T]{
		meta: tools.FCMeta[T]{
			ChatID: chatID, OpenID: openID, Data: data,
		},
		handlers:               make(map[string]tools.HandlerFunc[T]),
		tools:                  make([]*responses.ResponsesTool, 0),
		functionCallMap:        make(map[callID]funcName),
		functionInput:          make(map[callID]any),
		functionResult:         make(map[callID]gresult.R[string]),
		pendingCapabilityCalls: make([]CapabilityCallTrace, 0),
	}
}

func (r *ResponsesImpl[T]) RegisterHandler(event string, handler tools.HandlerFunc[T]) *ResponsesImpl[T] {
	r.handlers[event] = handler
	return r
}

func (r *ResponsesImpl[T]) WithTools(tools *tools.Impl[T]) *ResponsesImpl[T] {
	r.tools = append(r.tools, tools.Tools()...)
	maps.Copy(r.handlers, tools.HandlerMap())
	return r
}

func (r *ResponsesImpl[T]) OnCallStart(ctx context.Context, event *responses.Event) {
	item := event.GetItem()
	if call := item.GetItem().GetFunctionToolCall(); call != nil {
		functionName := call.GetName()
		r.functionCallMap[call.GetId()] = functionName
		r.lastCallID = call.GetId()
	}
}

func (r *ResponsesImpl[T]) OnCallArgs(ctx context.Context, event *responses.Event) (resp *arkutils.ResponsesStreamReader, err error) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.tool_args")
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	argsDoneEvent := event.GetFunctionCallArgumentsDone()
	args := argsDoneEvent.GetArguments()
	callID := argsDoneEvent.GetItemId()
	span.SetAttributes(attribute.String("call.id", callID))
	span.SetAttributes(otel.PreviewAttrs("call.args", args, 256)...)
	r.functionInput[callID] = args
	handlerName := r.functionCallMap[callID]
	span.SetAttributes(attribute.String("handler.name", handlerName))
	logs.L().Ctx(ctx).Info("OnCallArgs",
		zap.String("args_preview", otel.PreviewString(args, 256)),
		zap.String("handlerName", handlerName),
	)
	if handler, ok := r.handlers[handlerName]; ok {
		res := handler(ctx, args, r.meta)
		r.functionResult[callID] = res
		traceOutput := strings.TrimSpace(res.Value())
		if traceOutput == "" && res.IsErr() && res.Err() != nil {
			traceOutput = strings.TrimSpace(res.Err().Error())
		}
		if deferred, ok := runtimecontext.PopDeferredToolCall(ctx); ok && !res.IsErr() {
			if traceOutput == "" {
				traceOutput = strings.TrimSpace(deferred.PlaceholderOutput)
			}
			r.pendingCapabilityCalls = append(r.pendingCapabilityCalls, CapabilityCallTrace{
				CallID:            callID,
				FunctionName:      handlerName,
				Arguments:         strings.TrimSpace(args),
				Output:            traceOutput,
				Pending:           true,
				ApprovalType:      strings.TrimSpace(deferred.ApprovalType),
				ApprovalTitle:     strings.TrimSpace(deferred.ApprovalTitle),
				ApprovalSummary:   strings.TrimSpace(deferred.ApprovalSummary),
				ApprovalExpiresAt: deferred.ApprovalExpiresAt.UTC(),
			})
		} else {
			r.pendingCapabilityCalls = append(r.pendingCapabilityCalls, CapabilityCallTrace{
				CallID:       callID,
				FunctionName: handlerName,
				Arguments:    strings.TrimSpace(args),
				Output:       traceOutput,
			})
		}
		if res.IsErr() {
			logs.L().Ctx(ctx).Error("function call failed",
				zap.String("function_name", handlerName),
				zap.String("args", args),
				zap.Error(res.Err()),
			)
		}
		message := &responses.ResponsesInput{
			Union: &responses.ResponsesInput_ListValue{
				ListValue: &responses.InputItemList{ListValue: []*responses.InputItem{
					{
						Union: &responses.InputItem_FunctionToolCallOutput{
							FunctionToolCallOutput: &responses.ItemFunctionToolCallOutput{
								CallId: argsDoneEvent.GetItemId(),
								Output: utils.MustMarshalString(res.Value()),
								Type:   responses.ItemType_function_call_output,
							},
						},
					},
				}},
			},
		}
		_, cfg, cfgErr := runtimeClient()
		if cfgErr != nil {
			return nil, cfgErr
		}
		resp, err = CreateResponsesStream(ctx, &responses.ResponsesRequest{
			Model:              cfg.NormalModel,
			PreviousResponseId: gptr.Of(r.lastRespID),
			Input:              message,
			Store:              gptr.Of(true),
			Tools:              r.tools,
			// Text: &responses.ResponsesText{
			// 	Format: &responses.TextFormat{
			// 		Type: responses.TextType_json_object,
			// 	},
			// },
			Reasoning: &responses.ResponsesReasoning{
				Effort: responses.ReasoningEffort_medium,
			},
			Stream: gptr.Of(true),
		})
		if err != nil {
			return
		}
	} else {
		logs.L().Ctx(ctx).Warn("no handler found for function call",
			zap.String("function_name", handlerName),
			zap.String("args", args),
		)
		return nil, errors.New("no handler found for function call: " + handlerName)
	}
	return resp, err
}

func (r *ResponsesImpl[T]) OnReasoningDelta(ctx context.Context, event *responses.Event) {
	// ctx, span := otel.Start(ctx)
	// defer span.End()

	part := event.GetReasoningText()
	r.textOutput.ReasoningTextDelta = part.GetDelta()
	r.textOutput.reasoningText.WriteString(part.GetDelta())
}

func (r *ResponsesImpl[T]) OnNormalDelta(ctx context.Context, event *responses.Event) {
	// ctx, span := otel.Start(ctx)
	// defer span.End()

	part := event.GetText()
	r.textOutput.NormalTextDelta = part.GetDelta()
	r.textOutput.normalText.WriteString(part.GetDelta())
}

func (r *ResponsesImpl[T]) OnOthers(ctx context.Context, event *responses.Event) {
	trace.SpanFromContext(ctx).AddEvent(
		"ignored_event",
		trace.WithAttributes(attribute.String("event.type", event.GetEventType())),
	)
}

func (r *ResponsesImpl[T]) Handle(ctx context.Context, resp *arkutils.ResponsesStreamReader, event *responses.Event) (newRes *arkutils.ResponsesStreamReader, err error) {
	if id := event.GetResponse().GetResponse().GetId(); id != "" {
		r.lastRespID = id
	}

	switch eventType := event.GetEventType(); eventType {
	case responses.EventType_response_output_item_added.String():
		r.OnCallStart(ctx, event)
	case responses.EventType_response_function_call_arguments_done.String():
		return r.OnCallArgs(ctx, event)
	case responses.EventType_response_reasoning_summary_text_delta.String():
		r.OnReasoningDelta(ctx, event)
	case responses.EventType_response_output_text_delta.String():
		r.OnNormalDelta(ctx, event)
	default:
		r.OnOthers(ctx, event)
	}
	// 默认情况下，不会替换resp
	return resp, nil
}

func (r *ResponsesImpl[T]) SyncResult(ctx context.Context) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.sync")
	defer span.End()

	var (
		fields  = make([]zap.Field, 0)
		fcSlice = make([]string, 0)
	)

	previewCap := len(r.functionResult)
	if previewCap > 10 {
		previewCap = 10
	}
	resultPreview := make([]string, 0, previewCap)
	for callID, res := range r.functionResult {
		funcName := r.functionCallMap[callID]
		preview := funcName + "_" + callID + "==>" + otel.PreviewString(res.Value(), 128)
		fcSlice = append(fcSlice, preview)
		fields = append(fields, zap.String(funcName+"_"+callID, res.Value()))
		if len(resultPreview) < 10 {
			resultPreview = append(resultPreview, preview)
		}
	}
	span.SetAttributes(attribute.Int("function_results.count", len(fcSlice)))
	if len(resultPreview) > 0 {
		span.SetAttributes(attribute.StringSlice("function_results.preview", resultPreview))
	}
	outputText := utils.MustMarshalString(r.textOutput)
	span.SetAttributes(otel.PreviewAttrs("output", outputText, 256)...)
	logs.L().Ctx(ctx).Info("ResponsesCallResult", fields...)
}

func (r *ResponsesImpl[T]) drainPendingStreamItems() []*ModelStreamRespReasoning {
	items := make([]*ModelStreamRespReasoning, 0, 1+len(r.pendingCapabilityCalls))
	if r.textOutput.ReasoningTextDelta != "" || r.textOutput.NormalTextDelta != "" {
		items = append(items, &ModelStreamRespReasoning{
			ReasoningContent: r.textOutput.ReasoningTextDelta,
			Content:          r.textOutput.NormalTextDelta,
		})
	}
	r.textOutput.ReasoningTextDelta = ""
	r.textOutput.NormalTextDelta = ""

	for _, trace := range r.pendingCapabilityCalls {
		traceCopy := trace
		items = append(items, &ModelStreamRespReasoning{
			CapabilityCall: &traceCopy,
		})
	}
	r.pendingCapabilityCalls = r.pendingCapabilityCalls[:0]
	return items
}

func (r *ResponsesImpl[T]) Do(ctx context.Context, sysPrompt, userPrompt string, files ...string) (it iter.Seq[*ModelStreamRespReasoning], err error) {
	_, cfg, err := runtimeClient()
	if err != nil {
		return nil, err
	}
	ctx, span := otel.StartNamed(ctx, "ark.responses.run")
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	var (
		req     *responses.ResponsesRequest
		modelID = cfg.NormalModel
		items   = baseInputItem(sysPrompt, userPrompt)
	)

	span.SetAttributes(
		attribute.Key("model_id").String(modelID),
		attribute.Key("files.count").Int(len(files)),
	)
	span.SetAttributes(otel.PreviewAttrs("sys_prompt", sysPrompt, 256)...)
	span.SetAttributes(otel.PreviewAttrs("user_prompt", userPrompt, 256)...)
	if len(files) > 0 {
		filePreview := files
		if len(filePreview) > 10 {
			filePreview = filePreview[:10]
		}
		span.SetAttributes(attribute.StringSlice("files.preview", filePreview))
	}
	if len(files) > 0 {
		items = append(items, buildImageInputMessages(files...)...)
	}
	input := &responses.ResponsesInput{
		Union: &responses.ResponsesInput_ListValue{
			ListValue: &responses.InputItemList{
				ListValue: items,
			},
		},
	}
	req = &responses.ResponsesRequest{
		Model: modelID,
		Input: input,
		Store: gptr.Of(true),
		Tools: r.tools,
		// Text: &responses.ResponsesText{
		// 	Format: &responses.TextFormat{
		// 		Type: responses.TextType_json_object,
		// 	},
		// },
		Reasoning: &responses.ResponsesReasoning{
			Effort: responses.ReasoningEffort_medium,
		},
		Stream: gptr.Of(true),
	}

	resp, err := CreateResponsesStream(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to create responses stream", zap.Error(err))
		return nil, err
	}

	return func(yield func(*ModelStreamRespReasoning) bool) {
		subCtx, subSpan := otel.StartNamed(ctx, "ark.responses.stream")
		defer subSpan.End()
		defer func() { otel.RecordError(subSpan, err) }()
		defer r.SyncResult(subCtx)

		for {
			event, err := resp.Recv()

			if err == io.EOF {
				return
			}

			if err != nil {
				logs.L().Ctx(subCtx).Error("stream receive error", zap.Error(err))
				return
			}

			resp, err = r.Handle(subCtx, resp, event)
			if err != nil {
				logs.L().Ctx(subCtx).Error("handle event error", zap.String("last_resp_id", r.lastRespID), zap.Error(err))
				return
			}
			if resp == nil {
				// 忽略空resp
				logs.L().Ctx(subCtx).Warn("ignore empty resp", zap.String("last_resp_id", r.lastRespID))
				continue
			}

			for _, item := range r.drainPendingStreamItems() {
				if !yield(item) {
					return
				}
			}
		}
	}, nil
}
