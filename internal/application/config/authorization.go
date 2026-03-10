package config

import (
	"context"
	"errors"
	"strings"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

const permissionPointConfigWrite = "config.write"

var permissionGrantExists = permissioninfra.Exists

func ensureGlobalConfigMutationAllowed(ctx context.Context, actorUserID, fallbackUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	if actorUserID == "" {
		actorUserID = strings.TrimSpace(fallbackUserID)
	}
	if actorUserID == "" {
		return errors.New("global config modification requires operator identity")
	}

	ok, err := permissionGrantExists(ctx, permissioninfra.GrantFilter{
		SubjectType:     permissioninfra.SubjectTypeUser,
		SubjectID:       actorUserID,
		PermissionPoint: permissionPointConfigWrite,
		Scope:           string(ScopeGlobal),
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("only users with permission point config.write@global can modify global config")
	}
	return nil
}
