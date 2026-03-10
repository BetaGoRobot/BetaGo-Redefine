package cardaction

import (
	"context"
	"sync"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	cardhandlers "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/card_handlers"
	apppermission "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/permission"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

var registerBuiltinsOnce sync.Once

func RegisterBuiltins() {
	registerBuiltinsOnce.Do(func() {
		RegisterAsync(cardactionproto.ActionMusicPlay, handleMusicPlay)
		RegisterAsync(cardactionproto.ActionMusicAlbum, handleMusicAlbum)
		RegisterAsync(cardactionproto.ActionMusicLyrics, handleMusicLyrics)
		RegisterAsync(cardactionproto.ActionMusicRefresh, handleMusicRefresh)
		RegisterAsync(cardactionproto.ActionCardWithdraw, handleCardWithdraw)
		RegisterAsync(cardactionproto.ActionCommandRefresh, handleCommandRefresh)
		RegisterAsync(cardactionproto.ActionCommandSubmitTimeRange, handleCommandSubmitTimeRange)
		RegisterSync(cardactionproto.ActionFeatureBlockChat, handleFeatureAction)
		RegisterSync(cardactionproto.ActionFeatureUnblockChat, handleFeatureAction)
		RegisterSync(cardactionproto.ActionFeatureBlockUser, handleFeatureAction)
		RegisterSync(cardactionproto.ActionFeatureUnblockUser, handleFeatureAction)
		RegisterSync(cardactionproto.ActionFeatureBlockChatUser, handleFeatureAction)
		RegisterSync(cardactionproto.ActionFeatureUnblockChatUser, handleFeatureAction)
		RegisterSync(cardactionproto.ActionConfigSet, handleConfigAction)
		RegisterSync(cardactionproto.ActionConfigDelete, handleConfigAction)
		RegisterSync(cardactionproto.ActionConfigViewScope, handleConfigView)
		RegisterSync(cardactionproto.ActionPermissionGrant, handlePermissionAction)
		RegisterSync(cardactionproto.ActionPermissionRevoke, handlePermissionAction)
		RegisterSync(cardactionproto.ActionPermissionView, handlePermissionView)
	})
}

func handleMusicPlay(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	musicID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		cardhandlers.SendMusicCard(runCtx, actionCtx.MetaData, musicID, actionCtx.MessageID(), 1)
	}, nil
}

func handleMusicAlbum(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	albumID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		cardhandlers.SendAlbumCard(runCtx, actionCtx.MetaData, albumID, actionCtx.MessageID())
	}, nil
}

func handleMusicLyrics(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	musicID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		cardhandlers.HandleFullLyrics(runCtx, actionCtx.MetaData, musicID, actionCtx.MessageID())
	}, nil
}

func handleMusicRefresh(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	musicID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		cardhandlers.HandleRefreshMusic(runCtx, musicID, actionCtx.MessageID())
	}, nil
}

func handleCardWithdraw(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	return func(runCtx context.Context) {
		cardhandlers.HandleWithDraw(runCtx, actionCtx.Event)
	}, nil
}

func handleCommandRefresh(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	return func(runCtx context.Context) {
		cardhandlers.HandleRefreshObj(runCtx, actionCtx.Event)
	}, nil
}

func handleCommandSubmitTimeRange(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	return func(runCtx context.Context) {
		cardhandlers.HandleSubmit(runCtx, actionCtx.Event)
	}, nil
}

func handleFeatureAction(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appconfig.ParseFeatureActionRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}

	resp, err := appconfig.HandleFeatureAction(ctx, req)
	if err != nil {
		return ErrorToast(resp.Message), nil
	}
	card, cardErr := appconfig.BuildFeatureCard(ctx, actionCtx.ChatID(), actionCtx.UserID())
	if cardErr != nil {
		return InfoToast(resp.Message), nil
	}
	return InfoToastWithCard(resp.Message, card), nil
}

func handleConfigAction(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appconfig.ParseConfigActionRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	req.ActorUserID = actionCtx.UserID()

	resp, err := appconfig.HandleConfigAction(ctx, req)
	if err != nil {
		return ErrorToast(resp.Message), nil
	}
	card, cardErr := appconfig.BuildConfigCardJSONWithOptions(ctx, req.Scope, req.ChatID, req.UserID, appconfig.ConfigCardViewOptions{
		BypassCache: true,
	})
	if cardErr != nil {
		return InfoToast(resp.Message), nil
	}
	return InfoToastWithRawCardPayload(resp.Message, card), nil
}

func handleConfigView(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appconfig.ParseConfigViewRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	card, err := appconfig.BuildConfigCardJSONWithOptions(ctx, req.Scope, req.ChatID, req.UserID, appconfig.ConfigCardViewOptions{
		BypassCache: true,
	})
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(card), nil
}

func handlePermissionAction(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := apppermission.ParseActionRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	req.ActorUserID = actionCtx.UserID()

	resp, err := apppermission.HandleAction(ctx, req)
	if err != nil {
		return ErrorToast(resp.Message), nil
	}
	card, cardErr := apppermission.BuildPermissionCardJSON(ctx, actionCtx.ChatID(), actionCtx.UserID(), req.TargetUserID)
	if cardErr != nil {
		return InfoToast(resp.Message), nil
	}
	return InfoToastWithRawCardPayload(resp.Message, card), nil
}

func handlePermissionView(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := apppermission.ParseViewRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	card, err := apppermission.BuildPermissionCardJSON(ctx, actionCtx.ChatID(), actionCtx.UserID(), req.TargetUserID)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(card), nil
}
