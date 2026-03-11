package permission

import (
	"context"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

var (
	currentBotIdentity        = botidentity.Current
	currentBootstrapAdminOpen = func() string {
		cfg := infraConfig.Get()
		if cfg == nil || cfg.LarkConfig == nil {
			return ""
		}
		return strings.TrimSpace(cfg.LarkConfig.BootstrapAdminOpenID)
	}
	permissionGrantExists = permissioninfra.Exists
)

func EnsureManageAllowed(ctx context.Context, actorOpenID string) error {
	actorOpenID = strings.TrimSpace(actorOpenID)
	if actorOpenID == "" {
		return errors.New("permission management requires operator identity")
	}

	identity := currentBotIdentity()
	if err := identity.Validate(); err != nil {
		return err
	}

	if bootstrapAdmin := currentBootstrapAdminOpen(); bootstrapAdmin != "" && actorOpenID == bootstrapAdmin {
		return nil
	}

	for _, point := range []string{
		permissioninfra.PermissionPointManage,
		permissioninfra.PermissionPointConfigWrite,
	} {
		ok, err := permissionGrantExists(ctx, permissioninfra.GrantFilter{
			SubjectType:     permissioninfra.SubjectTypeUser,
			SubjectID:       actorOpenID,
			PermissionPoint: point,
			Scope:           permissioninfra.ScopeGlobal,
			AppID:           identity.AppID,
			BotOpenID:       identity.BotOpenID,
		})
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}

	logs.L().Ctx(ctx).Warn("permission manage denied",
		zap.String("actor_user_id", actorOpenID),
		zap.String("app_id", identity.AppID),
		zap.String("bot_open_id", identity.BotOpenID),
	)
	return errors.New("only bootstrap admin or users with permission.manage@global / config.write@global can manage permissions")
}

func CurrentBootstrapAdminOpenID() string {
	return currentBootstrapAdminOpen()
}
