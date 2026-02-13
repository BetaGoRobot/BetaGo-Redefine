package ark_dal

import (
	"context"
	"io"
	"iter"
	"maps"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/gg/gresult"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
	"go.uber.org/zap"
)

// event-handler模式

type (
	callID               = string
	funcName             = string
	ResponsesImpl[T any] struct {
		meta tools.FCMeta[T]

		handlers        map[funcName]tools.HandlerFunc[T]
		tools           []*responses.ResponsesTool
		lastCallID      callID
		functionCallMap map[callID]funcName
		functionInput   map[callID]any
		functionResult  map[callID]gresult.R[string]

		lastRespID string
		textOutput textOutput
	}
	textOutput struct {
		ReasoningText string
		NormalText    string
	}
)

func New[T any](chatID, userID string, data *T) *ResponsesImpl[T] {
	return &ResponsesImpl[T]{
		meta: tools.FCMeta[T]{
			ChatID: chatID, UserID: userID, Data: data,
		},
		handlers:        make(map[string]tools.HandlerFunc[T]),
		tools:           make([]*responses.ResponsesTool, 0),
		functionCallMap: make(map[callID]funcName),
		functionInput:   make(map[callID]any),
		functionResult:  make(map[callID]gresult.R[string]),
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
		r.functionCallMap[call.GetCallId()] = functionName
		r.lastCallID = call.GetCallId()
	}
}

func (r *ResponsesImpl[T]) OnCallArgs(ctx context.Context, event *responses.Event) (resp *arkutils.ResponsesStreamReader, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	argsDoneEvent := event.GetFunctionCallArgumentsDone()
	args := argsDoneEvent.GetArguments()
	r.functionInput[r.lastCallID] = args
	handlerName := r.functionCallMap[r.lastCallID]
	logs.L().Ctx(ctx).Info("OnCallArgs",
		zap.String("args", args),
		zap.String("handlerName", handlerName),
	)
	if handler, ok := r.handlers[handlerName]; ok {
		res := handler(ctx, args, r.meta)
		r.functionResult[r.lastCallID] = res
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
		resp, err = client.CreateResponsesStream(ctx, &responses.ResponsesRequest{
			Model:              arkConfig.NormalModel,
			PreviousResponseId: gptr.Of(r.lastRespID),
			Input:              message,
		})
		if err != nil {
			return
		}
	} else {
		logs.L().Ctx(ctx).Warn("no handler found for function call",
			zap.String("function_name", handlerName),
			zap.String("args", args),
		)
	}
	return resp, err
}

func (r *ResponsesImpl[T]) OnReasoningDelta(ctx context.Context, event *responses.Event) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	part := event.GetReasoningText()
	r.textOutput.ReasoningText = part.GetDelta()
}

func (r *ResponsesImpl[T]) OnNormalDelta(ctx context.Context, event *responses.Event) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	part := event.GetText()
	r.textOutput.NormalText = part.GetDelta()
}

func (r *ResponsesImpl[T]) OnOthers(ctx context.Context, event *responses.Event) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	// 其他事件，直接忽略
	// 简单记录一下eventType
}

func (r *ResponsesImpl[T]) Handle(ctx context.Context, resp *arkutils.ResponsesStreamReader, event *responses.Event) (newRes *arkutils.ResponsesStreamReader, err error) {
	// 开启一个新的Span用于追踪流式接收过程
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc()+".StreamIter")
	defer span.End()
	defer func() { span.RecordError(err) }() // 这里的err需要捕获闭包内的错误

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
	fields := make([]zap.Field, 0)
	for callID, res := range r.functionResult {
		funcName := r.functionCallMap[callID]
		fields = append(fields, zap.String(funcName+"_"+callID, res.String()))
	}
	logs.L().Ctx(ctx).Info("ResponsesCallResult", fields...)
}

func (r *ResponsesImpl[T]) Do(ctx context.Context, sysPrompt, userPrompt string, files ...string) (it iter.Seq[string], err error) {
	ctx, subSpan := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer subSpan.End()
	defer func() { subSpan.RecordError(err) }() // 这里的err需要捕获闭包内的错误

	items := baseInputItem(sysPrompt, userPrompt)
	var req *responses.ResponsesRequest
	if len(files) > 0 {
		items = append(items, buildImageInputMessages(files...)...)
	} else {
		input := &responses.ResponsesInput{
			Union: &responses.ResponsesInput_ListValue{
				ListValue: &responses.InputItemList{
					ListValue: items,
				},
			},
		}
		req = &responses.ResponsesRequest{
			Model:       arkConfig.NormalModel,
			Input:       input,
			Store:       gptr.Of(true),
			Tools:       r.tools,
			Temperature: gptr.Of(0.1),
			Text: &responses.ResponsesText{
				Format: &responses.TextFormat{
					Type: responses.TextType_json_object,
				},
			},
			Stream: gptr.Of(true),
		}
	}

	resp, err := client.CreateResponsesStream(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to create responses stream", zap.Error(err))
		return nil, err
	}

	return func(yield func(string) bool) {
		subCtx, subSpan := otel.T().Start(ctx, reflecting.GetCurrentFunc()+".StreamIter")
		defer subSpan.End()
		defer func() { subSpan.RecordError(err) }() // 这里的err需要捕获闭包内的错误
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

			if !yield(r.textOutput.NormalText) {
				return
			}
		}
	}, nil
}
