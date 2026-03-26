package handlers

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	financeMarketDataResultKey = "finance_market_data_result"
	financeNewsResultKey       = "finance_news_result"
	economyIndicatorResultKey  = "economy_indicator_result"
)

type (
	FinanceMarketDataArgs = aktool.FinanceMarketDataRequest
	FinanceNewsArgs       = aktool.FinanceNewsRequest
	EconomyIndicatorArgs  = aktool.FinanceEconomyIndicatorRequest

	financeMarketDataHandler struct {
		provider aktool.FinanceProvider
	}
	financeNewsHandler struct {
		provider aktool.FinanceProvider
	}
	economyIndicatorHandler struct {
		provider aktool.FinanceProvider
	}
)

var (
	FinanceMarketData = financeMarketDataHandler{provider: aktool.NewFinanceProvider("")}
	FinanceNews       = financeNewsHandler{provider: aktool.NewFinanceProvider("")}
	EconomyIndicator  = economyIndicatorHandler{provider: aktool.NewFinanceProvider("")}
)

func (h financeMarketDataHandler) ParseTool(raw string) (FinanceMarketDataArgs, error) {
	parsed := FinanceMarketDataArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return FinanceMarketDataArgs{}, err
	}
	return parsed, nil
}

func (h financeMarketDataHandler) ToolSpec() xcommand.ToolSpec {
	def, _ := aktool.LookupFinanceToolDefinition("finance_market_data_get")
	return xcommand.ToolSpec{
		Name:   def.Name,
		Desc:   def.Description,
		Params: def.Schema,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(financeMarketDataResultKey)
			return result
		},
	}
}

func (h financeMarketDataHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FinanceMarketDataArgs) error {
	result, err := h.provider.GetMarketData(ctx, arg)
	if err != nil {
		return err
	}
	metaData.SetExtra(financeMarketDataResultKey, utils.MustMarshalString(result))
	return nil
}

func (h financeNewsHandler) ParseTool(raw string) (FinanceNewsArgs, error) {
	parsed := FinanceNewsArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return FinanceNewsArgs{}, err
	}
	return parsed, nil
}

func (h financeNewsHandler) ToolSpec() xcommand.ToolSpec {
	def, _ := aktool.LookupFinanceToolDefinition("finance_news_get")
	return xcommand.ToolSpec{
		Name:   def.Name,
		Desc:   def.Description,
		Params: def.Schema,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(financeNewsResultKey)
			return result
		},
	}
}

func (h financeNewsHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FinanceNewsArgs) error {
	result, err := h.provider.GetNews(ctx, arg)
	if err != nil {
		return err
	}
	metaData.SetExtra(financeNewsResultKey, utils.MustMarshalString(result))
	return nil
}

func (h economyIndicatorHandler) ParseTool(raw string) (EconomyIndicatorArgs, error) {
	parsed := EconomyIndicatorArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return EconomyIndicatorArgs{}, err
	}
	return parsed, nil
}

func (h economyIndicatorHandler) ToolSpec() xcommand.ToolSpec {
	def, _ := aktool.LookupFinanceToolDefinition("economy_indicator_get")
	return xcommand.ToolSpec{
		Name:   def.Name,
		Desc:   def.Description,
		Params: def.Schema,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(economyIndicatorResultKey)
			return result
		},
	}
}

func (h economyIndicatorHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg EconomyIndicatorArgs) error {
	result, err := h.provider.GetEconomyIndicator(ctx, arg)
	if err != nil {
		return err
	}
	metaData.SetExtra(economyIndicatorResultKey, utils.MustMarshalString(result))
	return nil
}
