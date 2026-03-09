package xcommand

import (
	"context"
	"errors"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/gg/gresult"
)

type (
	// CLIArgHandler keeps command parsing strongly typed all the way into Handle.
	CLIArgHandler[TData, TArgs any] interface {
		ParseCLI(args []string) (TArgs, error)
		Handle(ctx context.Context, data TData, metaData *xhandler.BaseMetaData, arg TArgs) error
	}

	// ToolArgHandler mirrors CLIArgHandler for function-call tools.
	ToolArgHandler[TMeta, TArgs any] interface {
		ParseTool(rawJSON string) (TArgs, error)
		Handle(ctx context.Context, data *TMeta, metaData *xhandler.BaseMetaData, arg TArgs) error
		ToolSpec() ToolSpec
	}

	ToolResultEncoder func(metaData *xhandler.BaseMetaData) string

	ToolSpec struct {
		Name   string
		Desc   string
		Params *arktools.Param
		Result ToolResultEncoder
	}
)

func BindCLI[TData, TArgs any](handler CLIArgHandler[TData, TArgs]) CommandFunc[TData] {
	return func(ctx context.Context, data TData, metaData *xhandler.BaseMetaData, args ...string) error {
		if handler == nil {
			return errors.New("cli handler is nil")
		}

		parsed, err := handler.ParseCLI(args)
		if err != nil {
			return err
		}
		return handler.Handle(ctx, data, metaData, parsed)
	}
}

func BindTool[TMeta, TArgs any](handler ToolArgHandler[TMeta, TArgs]) arktools.HandlerFunc[TMeta] {
	return func(ctx context.Context, rawJSON string, meta arktools.FCMeta[TMeta]) gresult.R[string] {
		if handler == nil {
			return gresult.Err[string](errors.New("tool handler is nil"))
		}

		parsed, err := handler.ParseTool(rawJSON)
		if err != nil {
			return gresult.Err[string](err)
		}

		metaData := &xhandler.BaseMetaData{
			ChatID: meta.ChatID,
			UserID: meta.UserID,
		}
		if err := handler.Handle(ctx, meta.Data, metaData, parsed); err != nil {
			return gresult.Err[string](err)
		}

		spec := handler.ToolSpec()
		if spec.Result != nil {
			if output := spec.Result(metaData); output != "" {
				return gresult.OK(output)
			}
		}
		return gresult.OK("执行成功")
	}
}

func RegisterTool[TMeta, TArgs any](ins *arktools.Impl[TMeta], handler ToolArgHandler[TMeta, TArgs]) {
	if handler == nil {
		return
	}

	spec := handler.ToolSpec()
	params := spec.Params
	if params == nil {
		params = arktools.NewParams("object")
	}

	unit := arktools.NewUnit[TMeta]().
		Name(spec.Name).
		Desc(spec.Desc).
		Params(params).
		Func(BindTool(handler))
	ins.Add(unit)
}
