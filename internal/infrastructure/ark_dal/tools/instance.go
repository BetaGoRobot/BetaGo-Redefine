package tools

import (
	"context"

	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/gg/gresult"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

type (
	HandlerFunc[T any] func(ctx context.Context, args string, input FCMeta[T]) gresult.R[string]
	Impl[T any]        struct {
		FunctionCallMap map[string]*FunctionCallUnit[T]
		WebsearchTool   *responses.ResponsesTool
		handlerFunc     HandlerFunc[T]
	}
)

func New[T any]() *Impl[T] {
	return &Impl[T]{
		FunctionCallMap: make(map[string]*FunctionCallUnit[T]),
	}
}

func (h *Impl[T]) Add(unit *FunctionCallUnit[T]) *Impl[T] {
	h.FunctionCallMap[unit.FunctionName] = unit
	return h
}

func (h *Impl[T]) Handle(unit HandlerFunc[T]) *Impl[T] {
	h.handlerFunc = unit
	return h
}

func (h *Impl[T]) HandlerMap() map[string]HandlerFunc[T] {
	res := make(map[string]HandlerFunc[T])
	for _, unit := range h.FunctionCallMap {
		res[unit.FunctionName] = h.handlerFunc
	}
	return res
}

func (h *Impl[T]) Get(name string) (*FunctionCallUnit[T], bool) {
	f, ok := h.FunctionCallMap[name]
	return f, ok
}

func (h *Impl[T]) WebSearch() *Impl[T] {
	h.WebsearchTool = &responses.ResponsesTool{
		Union: &responses.ResponsesTool_ToolWebSearch{
			ToolWebSearch: &responses.ToolWebSearch{
				Type:  responses.ToolType_web_search,
				Limit: gptr.Of[int64](10),
			},
		},
	}
	return h
}

func (h *Impl[T]) Tools() []*responses.ResponsesTool {
	tools := make([]*responses.ResponsesTool, 0)
	for _, unit := range h.FunctionCallMap {
		tools = append(tools, &responses.ResponsesTool{
			Union: &responses.ResponsesTool_ToolFunction{
				ToolFunction: &responses.ToolFunction{
					Name:        unit.FunctionName,
					Type:        responses.ToolType_function,
					Description: gptr.Of(unit.Description),
					Parameters:  &responses.Bytes{Value: unit.Parameters.JSON()},
				},
			},
		})
	}
	if h.WebsearchTool != nil {
		tools = append(tools, h.WebsearchTool)
	}

	return tools
}
