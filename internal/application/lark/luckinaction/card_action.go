package luckinaction

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func Register() {
	service := luckin.NewConfirmationService(
		pendingStore{},
		credentialStore{},
		mcpclient.New(mcpclient.ClientOptions{}),
		luckin.ServerURL,
	)
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinOrderConfirm, handleConfirm(service))
	appcardaction.RegisterSyncIfAbsent(cardactionproto.ActionLuckinOrderCancel, handleCancel(service))
}

func handleConfirm(service luckin.ConfirmationService) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		hash, err := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		if err != nil {
			return nil, err
		}
		card, err := service.Confirm(ctx, luckin.ConfirmRequest{
			PendingOrderID: id,
			PayloadHash:    hash,
			OperatorOpenID: actionCtx.OpenID(),
			ChatID:         actionCtx.ChatID(),
			Now:            time.Now(),
		})
		if err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		return appcardaction.InfoToastWithRawCardPayload("瑞幸订单已创建", card), nil
	}
}

func handleCancel(service luckin.ConfirmationService) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		hash, err := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		if err != nil {
			return nil, err
		}
		if err := service.Cancel(ctx, luckin.CancelRequest{
			PendingOrderID: id,
			PayloadHash:    hash,
			OperatorOpenID: actionCtx.OpenID(),
			ChatID:         actionCtx.ChatID(),
			Now:            time.Now(),
		}); err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		return appcardaction.InfoToast("瑞幸订单已取消"), nil
	}
}

type credentialStore struct{}

func (credentialStore) FindToken(ctx context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	if lookup.Scope.Type == luckin.ScopeSystem {
		token := strings.TrimSpace(os.Getenv("LUCKIN_MCP_TOKEN"))
		if token == "" {
			return luckin.Credential{}, luckin.ErrCredentialNotFound
		}
		return luckin.Credential{
			Provider:  luckin.ProviderLuckin,
			Scope:     lookup.Scope,
			Token:     token,
			TokenHint: luckin.MaskToken(token),
		}, nil
	}
	db := infraDB.DB()
	if db == nil {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	key := strings.TrimSpace(os.Getenv("MCP_CREDENTIALS_KEY"))
	if key == "" {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	codec, err := mcpstore.NewTokenCodec(key)
	if err != nil {
		return luckin.Credential{}, err
	}
	return mcpstore.NewCredentialRepository(db, codec).FindToken(ctx, lookup)
}

type pendingStore struct{}

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

func newPendingRepo() (*mcpstore.PendingOrderRepository, error) {
	db := infraDB.DB()
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	return mcpstore.NewPendingOrderRepository(db), nil
}
