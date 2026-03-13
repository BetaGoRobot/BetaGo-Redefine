package cardaction

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	cardhandlers "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/card_handlers"
	commandapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	appratelimit "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	apppermission "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/permission"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"go.uber.org/zap"
)

var registerBuiltinsOnce sync.Once

func RegisterBuiltins() {
	registerBuiltinsOnce.Do(func() {
		RegisterAsync(cardactionproto.ActionMusicPlay, handleMusicPlay)
		RegisterAsync(cardactionproto.ActionMusicAlbum, handleMusicAlbum)
		RegisterAsync(cardactionproto.ActionMusicLyrics, handleMusicLyrics)
		RegisterAsync(cardactionproto.ActionMusicRefresh, handleMusicRefresh)
		RegisterAsync(cardactionproto.ActionMusicListPage, handleMusicListPage)
		RegisterAsync(cardactionproto.ActionCardWithdraw, handleCardWithdraw)
		RegisterSync(cardactionproto.ActionCommandOpenForm, handleCommandOpenForm)
		RegisterAsync(cardactionproto.ActionCommandRefresh, handleCommandRefresh)
		RegisterAsync(cardactionproto.ActionCommandSubmitForm, handleCommandSubmitForm)
		RegisterAsync(cardactionproto.ActionCommandSubmitTimeRange, handleCommandSubmitTimeRange)
		RegisterSync(cardactionproto.ActionFeatureView, handleFeatureView)
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
		RegisterSync(cardactionproto.ActionRateLimitView, handleRateLimitView)
		RegisterSync(cardactionproto.ActionScheduleView, handleScheduleView)
		RegisterSync(cardactionproto.ActionSchedulePause, handleScheduleAction)
		RegisterSync(cardactionproto.ActionScheduleResume, handleScheduleAction)
		RegisterSync(cardactionproto.ActionScheduleDelete, handleScheduleAction)
	})
}

func handleMusicPlay(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	musicID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		musicIDInt, err := strconv.Atoi(musicID)
		if err != nil {
			logs.L().Ctx(runCtx).Error("[handleMusicPlay] Atoi musicID failed...", zap.Error(err))
			return
		}
		cardhandlers.SendMusicCard(runCtx, actionCtx.MetaData, musicIDInt, actionCtx.MessageID(), 1)
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
		musicIDInt, err := strconv.Atoi(musicID)
		if err != nil {
			logs.L().Ctx(runCtx).Error("[handleMusicLyrics] Atoi musicID failed...", zap.Error(err))
			return
		}
		cardhandlers.HandleFullLyrics(runCtx, actionCtx.MetaData, musicIDInt, actionCtx.MessageID())
	}, nil
}

func handleMusicRefresh(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	musicID, err := actionCtx.Action.RequiredString(cardactionproto.IDField)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		musicIDInt, err := strconv.Atoi(musicID)
		if err != nil {
			logs.L().Ctx(runCtx).Error("[handleMusicRefresh] Atoi musicID failed...", zap.Error(err))
			return
		}
		cardhandlers.HandleRefreshMusic(runCtx, musicIDInt, actionCtx.MessageID())
	}, nil
}

func handleMusicListPage(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	scene, err := actionCtx.Action.RequiredString(cardactionproto.SceneField)
	if err != nil {
		return nil, err
	}
	query, err := actionCtx.Action.RequiredString(cardactionproto.QueryField)
	if err != nil {
		return nil, err
	}
	pageRaw, err := actionCtx.Action.RequiredString(cardactionproto.PageField)
	if err != nil {
		return nil, err
	}
	pageSizeRaw, err := actionCtx.Action.RequiredString(cardactionproto.PageSizeField)
	if err != nil {
		return nil, err
	}
	page, err := strconv.Atoi(pageRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid page")
	}
	pageSize, err := strconv.Atoi(pageSizeRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid page_size")
	}
	msgID := strings.TrimSpace(actionCtx.MessageID())
	if msgID == "" {
		return nil, fmt.Errorf("message id is required")
	}
	return func(runCtx context.Context) {
		neteaseapi.CancelMusicListStream(runCtx, msgID)
		err := neteaseapi.StreamMusicListCardForRequest(runCtx, neteaseapi.MusicListRequest{
			Scene:    neteaseapi.MusicListScene(scene),
			Query:    query,
			Page:     page,
			PageSize: pageSize,
		}, func(sendCtx context.Context, cardContent *larktpl.TemplateCardContent) (string, error) {
			if err := larkmsg.PatchCard(sendCtx, cardContent, msgID); err != nil {
				return "", err
			}
			return msgID, nil
		}, func(patchCtx context.Context, patchMsgID string, cardContent *larktpl.TemplateCardContent) error {
			return larkmsg.PatchCard(patchCtx, cardContent, patchMsgID)
		})
		if err != nil {
			logs.L().Ctx(runCtx).Warn("stream music list page card failed", zap.String("message_id", msgID), zap.Error(err))
		}
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

func handleCommandOpenForm(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	rawCommand, err := actionCtx.Action.RequiredString(cardactionproto.CommandField)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	cardData, err := commandapp.BuildCommandFormCardJSON(commandapp.LarkRootCommand, rawCommand)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(cardData), nil
}

func handleCommandSubmitForm(ctx context.Context, actionCtx *Context) (AsyncTask, error) {
	rawCommand, err := actionCtx.Action.RequiredString(cardactionproto.CommandField)
	if err != nil {
		return nil, err
	}
	formValues := make(map[string]any, len(actionCtx.Action.FormValue))
	for key, value := range actionCtx.Action.FormValue {
		formValues[key] = value
	}
	nextCommand, err := commandapp.BuildCommandFormRawCommand(commandapp.LarkRootCommand, rawCommand, formValues)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context) {
		cardhandlers.ExecuteRawCommandFromCard(runCtx, actionCtx.Event, nextCommand)
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
	card, cardErr := appconfig.BuildFeatureCardWithOptions(ctx, actionCtx.ChatID(), actionCtx.OpenID(), appconfig.FeatureCardViewOptions{
		LastModifierOpenID: actionCtx.OpenID(),
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
	})
	if cardErr != nil {
		return InfoToast(resp.Message), nil
	}
	return InfoToastWithCard(resp.Message, card), nil
}

func handleFeatureView(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appconfig.ParseFeatureViewRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(actionCtx.ChatID())
	}
	openID := strings.TrimSpace(req.OpenID)
	if openID == "" {
		openID = strings.TrimSpace(actionCtx.OpenID())
	}
	card, err := appconfig.BuildFeatureCardWithOptions(ctx, chatID, openID, appconfig.FeatureCardViewOptions{
		LastModifierOpenID: req.LastModifierOpenID,
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
	})
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(map[string]any(card)), nil
}

func handleConfigAction(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appconfig.ParseConfigActionRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	req.ActorOpenID = actionCtx.OpenID()

	resp, err := appconfig.HandleConfigAction(ctx, req)
	if err != nil {
		return ErrorToast(resp.Message), nil
	}
	card, cardErr := appconfig.BuildConfigCardJSONWithOptions(ctx, req.Scope, req.ChatID, req.OpenID, appconfig.ConfigCardViewOptions{
		BypassCache:        true,
		LastModifierOpenID: actionCtx.OpenID(),
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
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
	card, err := appconfig.BuildConfigCardJSONWithOptions(ctx, req.Scope, req.ChatID, req.OpenID, appconfig.ConfigCardViewOptions{
		BypassCache:        true,
		LastModifierOpenID: req.LastModifierOpenID,
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
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
	req.ActorOpenID = actionCtx.OpenID()

	resp, err := apppermission.HandleAction(ctx, req)
	if err != nil {
		return ErrorToast(resp.Message), nil
	}
	card, cardErr := apppermission.BuildPermissionCardJSONWithOptions(ctx, actionCtx.ChatID(), actionCtx.OpenID(), req.TargetOpenID, apppermission.PermissionCardViewOptions{
		LastModifierOpenID: actionCtx.OpenID(),
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
	})
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
	card, err := apppermission.BuildPermissionCardJSONWithOptions(ctx, actionCtx.ChatID(), actionCtx.OpenID(), req.TargetOpenID, apppermission.PermissionCardViewOptions{
		LastModifierOpenID: req.LastModifierOpenID,
		MessageID:          actionCtx.MessageID(),
		PendingHistory:     []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
	})
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(card), nil
}

func handleRateLimitView(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := appratelimit.ParseStatsViewRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(actionCtx.ChatID())
	}
	if chatID == "" && actionCtx.MetaData != nil {
		chatID = strings.TrimSpace(actionCtx.MetaData.ChatID)
	}
	card, err := appratelimit.BuildStatsCardJSONWithOptions(ctx, chatID, appratelimit.StatsCardOptions{
		MessageID:      actionCtx.MessageID(),
		PendingHistory: []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)},
	})
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(card), nil
}

func handleScheduleView(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := scheduleapp.ParseTaskViewRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}

	chatID := strings.TrimSpace(actionCtx.ChatID())
	if chatID == "" && actionCtx.MetaData != nil {
		chatID = strings.TrimSpace(actionCtx.MetaData.ChatID)
	}
	req.View.MessageID = actionCtx.MessageID()
	req.View.PendingHistory = []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)}
	card, err := scheduleapp.BuildTaskCardPayloadForView(ctx, chatID, req.View, true)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}
	return RawCardPayloadOnly(card), nil
}

func handleScheduleAction(ctx context.Context, actionCtx *Context) (*callback.CardActionTriggerResponse, error) {
	req, err := scheduleapp.ParseTaskActionRequest(actionCtx.Action)
	if err != nil {
		return ErrorToast(err.Error()), nil
	}

	chatID := strings.TrimSpace(actionCtx.ChatID())
	if chatID == "" && actionCtx.MetaData != nil {
		chatID = strings.TrimSpace(actionCtx.MetaData.ChatID)
	}
	if _, err := scheduleapp.GetTaskForChat(ctx, chatID, req.ID); err != nil {
		return ErrorToast(err.Error()), nil
	}

	var message string
	actorOpenID := strings.TrimSpace(actionCtx.OpenID())
	switch req.Action {
	case scheduleapp.TaskActionPause:
		if err := scheduleapp.GetService().PauseTask(ctx, req.ID, actorOpenID); err != nil {
			return ErrorToast(err.Error()), nil
		}
		message = fmt.Sprintf("⏸️ Schedule 已暂停：`%s`", req.ID)
	case scheduleapp.TaskActionResume:
		if _, err := scheduleapp.GetService().ResumeTask(ctx, req.ID, actorOpenID); err != nil {
			return ErrorToast(err.Error()), nil
		}
		message = fmt.Sprintf("▶️ Schedule 已恢复：`%s`", req.ID)
	case scheduleapp.TaskActionDelete:
		if err := scheduleapp.GetService().DeleteTask(ctx, req.ID, actorOpenID); err != nil {
			return ErrorToast(err.Error()), nil
		}
		message = fmt.Sprintf("🗑️ Schedule 已删除：`%s`", req.ID)
	default:
		return ErrorToast(fmt.Sprintf("unsupported schedule action: %s", req.Action)), nil
	}

	req.View.LastModifierOpenID = actorOpenID
	req.View.MessageID = actionCtx.MessageID()
	req.View.PendingHistory = []larkmsg.CardActionHistoryRecord{larkmsg.NewCardActionHistoryRecord(actionCtx.Event)}
	card, cardErr := scheduleapp.BuildTaskCardPayloadForView(ctx, chatID, req.View, req.Action == scheduleapp.TaskActionDelete)
	if cardErr != nil {
		return InfoToast(message), nil
	}
	return InfoToastWithRawCardPayload(message, card), nil
}
