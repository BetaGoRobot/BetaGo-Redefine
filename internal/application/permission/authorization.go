package permission

import (
	"context"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
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

func EnsureManageAllowed(ctx context.Context, actorUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	if actorUserID == "" {
		return errors.New("permission management requires operator identity")
	}

	identity := currentBotIdentity()
	if err := identity.Validate(); err != nil {
		return err
	}

	if bootstrapAdmin := currentBootstrapAdminOpen(); bootstrapAdmin != "" && actorUserID == bootstrapAdmin {
		return nil
	}

	for _, point := range []string{
		permissioninfra.PermissionPointManage,
		permissioninfra.PermissionPointConfigWrite,
	} {
		ok, err := permissionGrantExists(ctx, permissioninfra.GrantFilter{
			SubjectType:     permissioninfra.SubjectTypeUser,
			SubjectID:       actorUserID,
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

	return errors.New("only bootstrap admin or users with permission.manage@global / config.write@global can manage permissions")
}

func CurrentBootstrapAdminOpenID() string {
	return currentBootstrapAdminOpen()
}
