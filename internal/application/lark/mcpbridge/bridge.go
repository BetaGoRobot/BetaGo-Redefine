package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type CredentialResolver interface {
	Resolve(context.Context, luckin.CredentialRequest) (luckin.Credential, error)
}

type PendingOrderService interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

type PendingOrderCardSender interface {
	SendPendingOrderCard(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, luckin.PendingOrder) error
}

type RegisterOptions struct {
	Policies  []luckin.ToolPolicy
	Client    *mcpclient.Client
	Resolver  CredentialResolver
	Pending   PendingOrderService
	Sender    PendingOrderCardSender
	SystemURL string
}

type rawArgs struct {
	JSON json.RawMessage
}

type handler struct {
	policy    luckin.ToolPolicy
	client    *mcpclient.Client
	resolver  CredentialResolver
	pending   PendingOrderService
	sender    PendingOrderCardSender
	serverURL string
}

func Register(ins *arktools.Impl[larkim.P2MessageReceiveV1], opts RegisterOptions) {
	if ins == nil {
		return
	}
	for _, policy := range opts.Policies {
		if !policy.DirectLLM {
			continue
		}
		xcommand.RegisterTool(ins, handler{
			policy:    policy,
			client:    opts.Client,
			resolver:  opts.Resolver,
			pending:   opts.Pending,
			sender:    opts.Sender,
			serverURL: opts.SystemURL,
		})
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
	if h.resolver == nil {
		return fmt.Errorf("luckin credential resolver is not configured")
	}
	cred, err := h.resolver.Resolve(ctx, credentialRequestFromMessage(data, metaData))
	if err != nil {
		return err
	}
	if h.policy.HighRisk {
		if h.pending == nil {
			return fmt.Errorf("luckin pending order service is not configured")
		}
		identity := botidentity.Current()
		order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{
			AppID:              identity.AppID,
			BotOpenID:          identity.BotOpenID,
			ChatID:             metaData.ChatID,
			RequesterOpenID:    metaData.OpenID,
			Credential:         cred,
			CreateOrderPayload: arg.JSON,
			PreviewResult:      json.RawMessage(`{}`),
		})
		if err := h.pending.CreatePendingOrder(ctx, order); err != nil {
			return err
		}
		if h.sender != nil {
			if err := h.sender.SendPendingOrderCard(ctx, data, metaData, order); err != nil {
				return err
			}
		}
		metaData.SetExtra(h.policy.RobotToolName+"_result", "瑞幸订单确认卡片已发送，请由发起人确认后再创建订单")
		return nil
	}
	if h.client == nil {
		return fmt.Errorf("luckin mcp client is not configured")
	}
	res, err := h.client.CallTool(ctx, mcpclient.CallRequest{
		Server: mcpclient.ServerConfig{
			Name:    luckin.ServerName,
			URL:     h.remoteURL(),
			Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
			Timeout: luckin.DefaultTimeout(),
		},
		ToolName:  h.policy.MCPToolName,
		Arguments: arg.JSON,
	})
	if err != nil {
		return err
	}
	metaData.SetExtra(h.policy.RobotToolName+"_result", string(res.Content))
	return nil
}

func (h handler) remoteURL() string {
	if h.serverURL != "" {
		return h.serverURL
	}
	return luckin.ServerURL
}

func credentialRequestFromMessage(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) luckin.CredentialRequest {
	req := luckin.CredentialRequest{}
	identity := botidentity.Current()
	req.AppID = identity.AppID
	req.BotOpenID = identity.BotOpenID
	if metaData != nil {
		req.ChatID = metaData.ChatID
		req.OpenID = metaData.OpenID
		if metaData.IsP2P {
			req.ChatType = luckin.ChatTypePrivate
		} else {
			req.ChatType = luckin.ChatTypeGroup
		}
	}
	if data != nil && data.Event != nil {
		if data.Event.Message != nil {
			if req.ChatID == "" && data.Event.Message.ChatId != nil {
				req.ChatID = *data.Event.Message.ChatId
			}
			if data.Event.Message.ChatType != nil {
				if *data.Event.Message.ChatType == "p2p" {
					req.ChatType = luckin.ChatTypePrivate
				} else {
					req.ChatType = luckin.ChatTypeGroup
				}
			}
		}
		if req.OpenID == "" {
			req.OpenID = botidentity.MessageSenderOpenID(data)
		}
	}
	if req.ChatType == "" {
		req.ChatType = luckin.ChatTypeGroup
	}
	return req
}
