package config

import (
	"context"
	"errors"
	"strings"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

const permissionPointConfigWrite = permissioninfra.PermissionPointConfigWrite

var permissionGrantExists = permissioninfra.Exists

func ensureGlobalConfigMutationAllowed(ctx context.Context, actorOpenID, fallbackOpenID string) error {
	actorOpenID = strings.TrimSpace(actorOpenID)
	if actorOpenID == "" {
		actorOpenID = strings.TrimSpace(fallbackOpenID)
	}
	if actorOpenID == "" {
		return errors.New("global config modification requires operator identity")
	}

	identity := currentBotIdentity()
	if err := identity.Validate(); err != nil {
		return err
	}
	ok, err := permissionGrantExists(ctx, permissioninfra.GrantFilter{
		SubjectType:     permissioninfra.SubjectTypeUser,
		SubjectID:       actorOpenID,
		PermissionPoint: permissionPointConfigWrite,
		Scope:           string(ScopeGlobal),
		AppID:           identity.AppID,
		BotOpenID:       identity.BotOpenID,
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("only users with permission point config.write@global can modify global config")
	}
	return nil
}
