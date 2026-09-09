package luckinaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
)

type orderFinder interface {
	FindOrder(context.Context, string, string, string) (luckin.OrderRecord, error)
}

type orderStatusService struct {
	orders orderFinder
	tokens luckin.CredentialStore
	draft  luckin.DraftService
}

// storedOrderFinder resolves the database lazily because card actions are
// registered before infrastructure initialization.
type storedOrderFinder struct{}

func (storedOrderFinder) FindOrder(ctx context.Context, appID, botOpenID, orderID string) (luckin.OrderRecord, error) {
	database := infraDB.DB()
	if database == nil {
		return luckin.OrderRecord{}, errors.New("订单记录暂不可用")
	}
	return mcpstore.NewOrderRepository(database).FindOrder(ctx, appID, botOpenID, orderID)
}

func (s orderStatusService) detail(ctx context.Context, req luckin.CredentialRequest, orderID string) (luckin.OrderDetail, error) {
	if s.orders == nil {
		return luckin.OrderDetail{}, errors.New("订单记录暂不可用")
	}
	if strings.TrimSpace(req.AppID) == "" || strings.TrimSpace(req.BotOpenID) == "" ||
		strings.TrimSpace(req.ChatID) == "" || strings.TrimSpace(orderID) == "" {
		return luckin.OrderDetail{}, errors.New("订单查询缺少租户、聊天或订单信息")
	}
	record, err := s.orders.FindOrder(ctx, req.AppID, req.BotOpenID, orderID)
	if err != nil {
		return luckin.OrderDetail{}, fmt.Errorf("查询订单记录失败: %w", err)
	}
	if record.AppID != req.AppID || record.BotOpenID != req.BotOpenID ||
		record.OrderID != orderID || record.ChatID != req.ChatID {
		return luckin.OrderDetail{}, errors.New("订单记录与当前应用、聊天或订单不匹配")
	}
	cred, err := loadOrderCredential(ctx, s.tokens, record)
	if err != nil {
		return luckin.OrderDetail{}, err
	}
	return s.draft.OrderDetail(ctx, cred, record.OrderID)
}

// loadOrderCredential uses the account that created the persisted order. The
// requester, initiator and current card operator may all be different people.
func loadOrderCredential(ctx context.Context, tokens luckin.CredentialStore, record luckin.OrderRecord) (luckin.Credential, error) {
	if tokens == nil || strings.TrimSpace(record.AppID) == "" || strings.TrimSpace(record.BotOpenID) == "" ||
		strings.TrimSpace(string(record.CredentialScope.Type)) == "" || strings.TrimSpace(record.CredentialScope.ID) == "" {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	cred, err := tokens.FindToken(ctx, luckin.CredentialLookup{
		Provider: luckin.ProviderLuckin, AppID: record.AppID,
		BotOpenID: record.BotOpenID, Scope: record.CredentialScope,
	})
	if err != nil {
		return luckin.Credential{}, err
	}
	if strings.TrimSpace(cred.Token) == "" {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	return cred, nil
}
