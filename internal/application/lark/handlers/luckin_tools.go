package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mcpbridge"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/geocode"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func registerLuckinTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	mcpbridge.Register(ins, mcpbridge.RegisterOptions{
		Policies:  luckin.ToolPolicies(),
		Client:    mcpclient.New(mcpclient.ClientOptions{}),
		Resolver:  luckinRuntimeResolver{},
		Pending:   luckinPendingOrderStore{},
		Sender:    luckinPendingOrderCardSender{},
		Cards:     luckinCardSender{},
		Session:   mcpstore.DefaultSessionStore(),
		Geocoder:  luckinGeocoder(),
		SystemURL: luckinServerURL(),
	})
}

func luckinGeocoder() luckin.Geocoder {
	amapKey := ""
	if cfg := luckinRuntimeConfig(); cfg != nil {
		amapKey = strings.TrimSpace(cfg.AmapKey)
	}
	return geocode.NewCached(
		geocode.NewAmapProvider(amapKey),
		geocode.NewNominatimProvider(),
	)
}

type luckinRuntimeResolver struct{}

func (luckinRuntimeResolver) Resolve(ctx context.Context, req luckin.CredentialRequest) (luckin.Credential, error) {
	store, err := newLuckinCredentialStore()
	if err != nil {
		return luckin.Credential{}, err
	}
	resolver := luckin.NewCredentialResolver(store, luckinSystemToken())
	return resolver.Resolve(ctx, req)
}

func newLuckinCredentialStore() (*mcpstore.CredentialRepository, error) {
	db := infraDB.DB()
	if db == nil {
		return nil, nil
	}
	key := luckinCredentialsKey()
	if key == "" {
		return nil, nil
	}
	codec, err := mcpstore.NewTokenCodec(key)
	if err != nil {
		return nil, err
	}
	return mcpstore.NewCredentialRepository(db, codec), nil
}

func luckinSystemToken() string {
	cfg := luckinRuntimeConfig()
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.SystemToken)
}

func luckinCredentialsKey() string {
	cfg := luckinRuntimeConfig()
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.CredentialsKey)
}

func luckinServerURL() string {
	cfg := luckinRuntimeConfig()
	if cfg == nil || strings.TrimSpace(cfg.ServerURL) == "" {
		return luckin.ServerURL
	}
	return strings.TrimSpace(cfg.ServerURL)
}

func luckinRuntimeConfig() *infraConfig.LuckinMCPConfig {
	cfg := infraConfig.Get()
	if cfg == nil {
		return nil
	}
	return cfg.LuckinMCPConfig
}

type luckinPendingOrderStore struct{}

func (luckinPendingOrderStore) CreatePendingOrder(ctx context.Context, order luckin.PendingOrder) error {
	repo, err := newLuckinPendingOrderRepository()
	if err != nil {
		return err
	}
	return repo.CreatePendingOrder(ctx, order)
}

func (luckinPendingOrderStore) FindPendingOrder(ctx context.Context, id string) (luckin.PendingOrder, error) {
	repo, err := newLuckinPendingOrderRepository()
	if err != nil {
		return luckin.PendingOrder{}, err
	}
	return repo.FindPendingOrder(ctx, id)
}

func (luckinPendingOrderStore) MarkConfirmed(ctx context.Context, id, payloadHash, confirmedByOpenID string, resultJSON json.RawMessage, now time.Time) error {
	repo, err := newLuckinPendingOrderRepository()
	if err != nil {
		return err
	}
	return repo.MarkConfirmed(ctx, id, payloadHash, confirmedByOpenID, resultJSON, now)
}

func (luckinPendingOrderStore) MarkCancelled(ctx context.Context, id, payloadHash, operatorOpenID, chatID string, now time.Time) error {
	repo, err := newLuckinPendingOrderRepository()
	if err != nil {
		return err
	}
	return repo.MarkCancelled(ctx, id, payloadHash, operatorOpenID, chatID, now)
}

func newLuckinPendingOrderRepository() (*mcpstore.PendingOrderRepository, error) {
	db := infraDB.DB()
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	return mcpstore.NewPendingOrderRepository(db), nil
}

type luckinPendingOrderCardSender struct{}

func (luckinPendingOrderCardSender) SendPendingOrderCard(ctx context.Context, data *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, order luckin.PendingOrder) error {
	return sendLuckinCard(ctx, data, meta, luckin.BuildPendingOrderCard(order))
}

type luckinCardSender struct{}

func (luckinCardSender) SendCard(ctx context.Context, data *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, card map[string]any) error {
	return sendLuckinCard(ctx, data, meta, card)
}

func sendLuckinCard(ctx context.Context, data *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, card map[string]any) error {
	msgID := ""
	if data != nil && data.Event != nil && data.Event.Message != nil && data.Event.Message.MessageId != nil {
		msgID = strings.TrimSpace(*data.Event.Message.MessageId)
	}
	if msgID != "" {
		return larkmsg.ReplyCardJSON(ctx, msgID, card, "_luckinCard", false)
	}
	chatID := ""
	if meta != nil {
		chatID = strings.TrimSpace(meta.ChatID)
	}
	if chatID == "" && data != nil && data.Event != nil && data.Event.Message != nil && data.Event.Message.ChatId != nil {
		chatID = strings.TrimSpace(*data.Event.Message.ChatId)
	}
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	return larkmsg.CreateCardJSON(ctx, chatID, card, fmt.Sprintf("luckin-card-%d", time.Now().UnixNano()), "_luckinCard")
}
