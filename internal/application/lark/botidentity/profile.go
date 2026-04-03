package botidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	"go.uber.org/zap"
)

// Profile describes the current bot instance for prompt/runtime identity hints.
type Profile struct {
	AppID     string
	BotOpenID string
	BotName   string
}

var profileLoader = loadProfileFromLark

func CurrentProfile(ctx context.Context) Profile {
	return resolveProfile(ctx, Current())
}

func PromptIdentityLines(profile Profile) []string {
	profile = normalizeProfile(Identity{AppID: profile.AppID, BotOpenID: profile.BotOpenID}, profile)
	if strings.TrimSpace(profile.BotOpenID) == "" && strings.TrimSpace(profile.BotName) == "" {
		return nil
	}
	lines := []string{
		fmt.Sprintf("self_open_id: %s", strings.TrimSpace(profile.BotOpenID)),
		fmt.Sprintf("self_app_id: %s", strings.TrimSpace(profile.AppID)),
		fmt.Sprintf("self_name, 也就是你的昵称。这个昵称和你的能力完全无关，只是昵称。: %s", strings.TrimSpace(profile.BotName)),
		"如果历史里的 sender user_id/open_id 等于 self_open_id，那条消息就是你自己之前发的。",
		"如果 mention target open_id 等于 self_open_id，那是在 @你。",
	}
	return lines
}

func getProfileCache(ctx context.Context, identity Identity) (Profile, error) {
	if !identity.Valid() {
		return Profile{}, errors.New("bot identity is missing")
	}
	return cache.GetOrExecute(ctx, profileCacheKey(identity), func() (Profile, error) {
		profile, err := profileLoader(ctx, identity)
		if err != nil {
			return Profile{}, err
		}
		return normalizeProfile(identity, profile), nil
	})
}

func resolveProfile(ctx context.Context, identity Identity) Profile {
	fallback := fallbackProfile(identity)
	if !identity.Valid() {
		return fallback
	}
	profile, err := getProfileCache(ctx, identity)
	if err != nil {
		logs.L().Ctx(ctx).Warn("load bot profile failed",
			zap.String("app_id", identity.AppID),
			zap.String("bot_open_id", identity.BotOpenID),
			zap.Error(err),
		)
		return fallback
	}
	return mergeProfile(fallback, profile)
}

func profileCacheKey(identity Identity) string {
	return identity.NamespaceKey("lark_bot_profile", strings.TrimSpace(identity.AppID))
}

func fallbackProfile(identity Identity) Profile {
	return Profile{
		AppID:     strings.TrimSpace(identity.AppID),
		BotOpenID: strings.TrimSpace(identity.BotOpenID),
		BotName:   configuredBotName(),
	}
}

func normalizeProfile(identity Identity, profile Profile) Profile {
	return mergeProfile(Profile{
		AppID:     strings.TrimSpace(identity.AppID),
		BotOpenID: strings.TrimSpace(identity.BotOpenID),
	}, profile)
}

func mergeProfile(base, override Profile) Profile {
	merged := base
	if trimmed := strings.TrimSpace(override.AppID); trimmed != "" {
		merged.AppID = trimmed
	}
	if trimmed := strings.TrimSpace(override.BotOpenID); trimmed != "" {
		merged.BotOpenID = trimmed
	}
	if trimmed := strings.TrimSpace(override.BotName); trimmed != "" {
		merged.BotName = trimmed
	}
	return merged
}

func configuredBotName() string {
	cfg := infraConfig.Get()
	if cfg == nil || cfg.BaseInfo == nil {
		return ""
	}
	return strings.TrimSpace(cfg.BaseInfo.RobotName)
}

func loadProfileFromLark(ctx context.Context, identity Identity) (Profile, error) {
	profile := Profile{
		AppID:     strings.TrimSpace(identity.AppID),
		BotOpenID: strings.TrimSpace(identity.BotOpenID),
	}
	if profile.AppID == "" {
		return profile, nil
	}
	client := lark_dal.Client()
	if client == nil || client.Application == nil || client.Application.V6 == nil || client.Application.V6.Application == nil {
		return Profile{}, errors.New("lark application client is not configured")
	}
	resp, err := client.Application.V6.Application.Get(ctx, larkapplication.NewGetApplicationReqBuilder().
		AppId(profile.AppID).
		Lang(larkapplication.I18nKeyZhCn).
		Build(),
	)
	if err != nil {
		return Profile{}, err
	}
	if resp == nil || !resp.Success() {
		if resp == nil {
			return Profile{}, errors.New("get application returned nil response")
		}
		return Profile{}, errors.New(resp.Error())
	}
	if resp.Data == nil || resp.Data.App == nil {
		return profile, nil
	}
	return Profile{
		AppID:     firstNonEmpty(profile.AppID, pointerString(resp.Data.App.AppId)),
		BotOpenID: profile.BotOpenID,
		BotName:   applicationName(resp.Data.App),
	}, nil
}

func applicationName(app *larkapplication.Application) string {
	if app == nil {
		return ""
	}
	if name := strings.TrimSpace(pointerString(app.AppName)); name != "" {
		return name
	}
	preferred := []string{
		strings.TrimSpace(pointerString(app.PrimaryLanguage)),
		larkapplication.I18nKeyZhCn,
		larkapplication.I18nKeyEnUs,
		larkapplication.I18nKeyJaJp,
	}
	for _, key := range preferred {
		if name := applicationI18nName(app.I18n, key); name != "" {
			return name
		}
	}
	for _, item := range app.I18n {
		if name := strings.TrimSpace(pointerString(item.Name)); name != "" {
			return name
		}
	}
	return ""
}

func applicationI18nName(items []*larkapplication.AppI18nInfo, wantKey string) string {
	wantKey = strings.TrimSpace(wantKey)
	if wantKey == "" {
		return ""
	}
	for _, item := range items {
		if item == nil || strings.TrimSpace(pointerString(item.I18nKey)) != wantKey {
			continue
		}
		if name := strings.TrimSpace(pointerString(item.Name)); name != "" {
			return name
		}
	}
	return ""
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
