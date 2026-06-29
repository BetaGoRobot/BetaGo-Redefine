package luckinaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/geocode"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"go.uber.org/zap"
)

func Register() {
	images := newImageUploader()
	session := newSessionStore()
	service := luckin.NewConfirmationServiceWithTracking(
		pendingStore{},
		credentialStore{},
		mcpclient.New(mcpclient.ClientOptions{}),
		luckinServerURL(),
		newOrderTracker(),
		images,
		luckinOrderPollConfig(),
	)
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinOrderConfirm, handleConfirm(service, session))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinOrderCancel, handleCancel(service, session))

	draft := luckin.NewDraftService(mcpclient.New(mcpclient.ClientOptions{}), luckinServerURL())
	geocoder := newGeocoder()
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinShopSelect, handleShopSelect(session))
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinRegionSelect, handleRegionSelect(session))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinShopSearch, handleShopSearch(session, draft, geocoder, credentialStore{}))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinProductQuery, handleProductQuery(session, draft, credentialStore{}, images))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinProductSelect, handleProductSelect(session, draft, credentialStore{}, images))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinCartUpdate, handleCartUpdate(session))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinCartRemove, handleCartRemove(session))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinCartCheckout, handleCartCheckout(session, draft, pendingStore{}, credentialStore{}))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinCouponApply, handleCouponApply(session, draft, pendingStore{}, credentialStore{}))
	appcardaction.RegisterAsyncIfAbsent(cardactionproto.ActionLuckinOrderStatus, handleOrderStatus(credentialStore{}, draft))

	writer := newCredentialWriter()
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinBindToken, handleBindToken(writer, larkmsg.DeleteEphemeralMessage))
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinUnbindToken, handleUnbindToken(writer))
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinViewScope, handleViewScope(func(ctx context.Context, req luckin.CredentialRequest) (luckin.Credential, error) {
		return resolveCredential(ctx, credentialStore{}, req)
	}))
}

func handleConfirm(service luckin.ConfirmationService, session luckin.SessionStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		hash, err := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		if err != nil {
			return nil, err
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		key, sess, ok := requireSession(ctx, session, actionCtx)
		operatorOpenID := actionCtx.OpenID()
		initiatorOpenID := operatorOpenID
		deleteSessionOnSuccess := false
		if ok {
			// 「确认下单」临界点：只有发起人可推进，避免别人抢点。
			if !luckin.IsInitiator(sess, operatorOpenID) {
				return func(context.Context) {}, errInitiatorOnly()
			}
			initiatorOpenID = sess.InitiatorOpenID
			operatorOpenID = sess.InitiatorOpenID
			deleteSessionOnSuccess = true
		}
		// 拆单后的子卡不再依赖原购物车 session，直接按 pending order 执行。
		req := luckin.ConfirmRequest{
			PendingOrderID: id,
			PayloadHash:    hash,
			OperatorOpenID: operatorOpenID,
			ChatID:         actionCtx.ChatID(),
			MessageID:      msgID,
			Now:            time.Now(),
		}
		return func(runCtx context.Context) {
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderProcessingCard("正在为你创建瑞幸订单…"), initiatorOpenID))
			}
			var card map[string]any
			lockErr := mcpstore.WithSessionLock(runCtx, msgID, func() error {
				c, err := service.Confirm(runCtx, req)
				if err != nil {
					return err
				}
				card = c
				// 下单成功后清空当前点单流程，避免重复下单（同一张卡再点会拿不到 cart）。
				if deleteSessionOnSuccess && session != nil {
					session.DeleteSession(runCtx, key)
				}
				return nil
			})
			if lockErr != nil {
				logs.L().Ctx(runCtx).Warn("luckin confirm order failed", zap.String("pending_id", id), zap.Error(lockErr))
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("创建订单失败："+lockErr.Error()), initiatorOpenID))
				}
				return
			}
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(card, initiatorOpenID))
			}
		}, nil
	}
}

func handleCancel(service luckin.ConfirmationService, session luckin.SessionStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		hash, err := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		if err != nil {
			return nil, err
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		_, sess, ok := requireSession(ctx, session, actionCtx)
		operatorOpenID := actionCtx.OpenID()
		initiatorOpenID := operatorOpenID
		if ok {
			if !luckin.IsInitiator(sess, operatorOpenID) {
				return func(context.Context) {}, errInitiatorOnly()
			}
			initiatorOpenID = sess.InitiatorOpenID
			operatorOpenID = sess.InitiatorOpenID
		}
		req := luckin.CancelRequest{
			PendingOrderID: id,
			PayloadHash:    hash,
			OperatorOpenID: operatorOpenID,
			ChatID:         actionCtx.ChatID(),
			Now:            time.Now(),
		}
		return func(runCtx context.Context) {
			lockErr := mcpstore.WithSessionLock(runCtx, msgID, func() error {
				return service.Cancel(runCtx, req)
			})
			if lockErr != nil {
				logs.L().Ctx(runCtx).Warn("luckin cancel order failed", zap.String("pending_id", id), zap.Error(lockErr))
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("取消失败："+lockErr.Error()), initiatorOpenID))
				}
				return
			}
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderProcessingCard("瑞幸订单已取消"), initiatorOpenID))
			}
		}, nil
	}
}

type credentialStore struct{}

func (credentialStore) FindToken(ctx context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	// 仅支持个人凭证：不再支持系统/群级 token。
	db := infraDB.DB()
	if db == nil {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	key := luckinCredentialsKey()
	if key == "" {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	codec, err := mcpstore.NewTokenCodec(key)
	if err != nil {
		return luckin.Credential{}, err
	}
	return mcpstore.NewCredentialRepository(db, codec).FindToken(ctx, lookup)
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

func luckinOrderConfig() *infraConfig.LuckinMCPConfig {
	return luckinRuntimeConfig()
}

func newGeocoder() luckin.Geocoder {
	amapKey := ""
	if cfg := luckinRuntimeConfig(); cfg != nil {
		amapKey = strings.TrimSpace(cfg.AmapKey)
	}
	return geocode.NewCached(
		geocode.NewAmapProvider(amapKey),
		geocode.NewNominatimProvider(),
	)
}

type pendingStore struct{}

func (pendingStore) CreatePendingOrder(ctx context.Context, order luckin.PendingOrder) error {
	repo, err := newPendingRepo()
	if err != nil {
		return err
	}
	return repo.CreatePendingOrder(ctx, order)
}

func (pendingStore) FindPendingOrder(ctx context.Context, id string) (luckin.PendingOrder, error) {
	repo, err := newPendingRepo()
	if err != nil {
		return luckin.PendingOrder{}, err
	}
	return repo.FindPendingOrder(ctx, id)
}

func (pendingStore) MarkConfirmed(ctx context.Context, id, payloadHash, confirmedByOpenID string, resultJSON json.RawMessage, now time.Time) error {
	repo, err := newPendingRepo()
	if err != nil {
		return err
	}
	return repo.MarkConfirmed(ctx, id, payloadHash, confirmedByOpenID, resultJSON, now)
}

func (pendingStore) MarkCancelled(ctx context.Context, id, payloadHash, operatorOpenID, chatID string, now time.Time) error {
	repo, err := newPendingRepo()
	if err != nil {
		return err
	}
	return repo.MarkCancelled(ctx, id, payloadHash, operatorOpenID, chatID, now)
}

func (pendingStore) UpdateDraft(ctx context.Context, order luckin.PendingOrder, now time.Time) error {
	repo, err := newPendingRepo()
	if err != nil {
		return err
	}
	return repo.UpdateDraft(ctx, order, now)
}

func newPendingRepo() (*mcpstore.PendingOrderRepository, error) {
	db := infraDB.DB()
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	return mcpstore.NewPendingOrderRepository(db), nil
}
