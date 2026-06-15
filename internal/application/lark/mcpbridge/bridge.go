package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type RegisterOptions struct {
	Policies []luckin.ToolPolicy
	Client   *mcpclient.Client
}

type rawArgs struct {
	JSON json.RawMessage
}

type handler struct {
	policy luckin.ToolPolicy
	client *mcpclient.Client
}

func Register(ins *arktools.Impl[larkim.P2MessageReceiveV1], opts RegisterOptions) {
	if ins == nil {
		return
	}
	for _, policy := range opts.Policies {
		if !policy.DirectLLM {
			continue
		}
		xcommand.RegisterTool(ins, handler{policy: policy, client: opts.Client})
	}
}

func (h handler) ParseTool(raw string) (rawArgs, error) {
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return rawArgs{}, fmt.Errorf("invalid tool arguments JSON")
	}
	return rawArgs{JSON: json.RawMessage(raw)}, nil
}

func (h handler) ToolSpec() xcommand.ToolSpec {
	params := arktools.NewParams("object")
	params.AdditionalProperties = true
	return xcommand.ToolSpec{
		Name:   h.policy.RobotToolName,
		Desc:   h.policy.Description,
		Params: params,
		Result: func(metaData *xhandler.BaseMetaData) string {
			if metaData == nil {
				return ""
			}
			result, _ := metaData.GetExtra(h.policy.RobotToolName + "_result")
			return result
		},
	}
}

func (h handler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg rawArgs) error {
	if metaData == nil {
		return nil
	}
	metaData.SetExtra(h.policy.RobotToolName+"_result", string(arg.JSON))
	return nil
}
