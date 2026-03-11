package config

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

func useTestBotIdentity(t *testing.T) {
	t.Helper()
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_test_app",
			BotOpenID: "ou_test_bot",
		}
	}
	t.Cleanup(func() {
		currentBotIdentity = oldIdentity
	})
}

func TestEnsureGlobalConfigMutationAllowedUsesActorOpenID(t *testing.T) {
	oldChecker := permissionGrantExists
	defer func() { permissionGrantExists = oldChecker }()
	useTestBotIdentity(t)

	var calledWith permissioninfra.GrantFilter
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		calledWith = filter
		return true, nil
	}

	if err := ensureGlobalConfigMutationAllowed(context.Background(), "ou_actor", ""); err != nil {
		t.Fatalf("ensureGlobalConfigMutationAllowed() error = %v", err)
	}
	if calledWith.SubjectID != "ou_actor" {
		t.Fatalf("expected checker to use actor open id, got %q", calledWith.SubjectID)
	}
	if calledWith.PermissionPoint != permissionPointConfigWrite {
		t.Fatalf("expected permission point %q, got %q", permissionPointConfigWrite, calledWith.PermissionPoint)
	}
	if calledWith.Scope != string(ScopeGlobal) {
		t.Fatalf("expected global scope, got %q", calledWith.Scope)
	}
	if calledWith.SubjectType != permissioninfra.SubjectTypeUser {
		t.Fatalf("expected subject type %q, got %q", permissioninfra.SubjectTypeUser, calledWith.SubjectType)
	}
	if calledWith.AppID != "cli_test_app" || calledWith.BotOpenID != "ou_test_bot" {
		t.Fatalf("expected bot identity cli_test_app/ou_test_bot, got %q/%q", calledWith.AppID, calledWith.BotOpenID)
	}
	if calledWith.ResourceChatID != "" || calledWith.ResourceUserID != "" {
		t.Fatalf("expected empty resource ids for global scope, got chat=%q user=%q", calledWith.ResourceChatID, calledWith.ResourceUserID)
	}
}

func TestEnsureGlobalConfigMutationAllowedFallsBackToRequestOpenID(t *testing.T) {
	oldChecker := permissionGrantExists
	defer func() { permissionGrantExists = oldChecker }()
	useTestBotIdentity(t)

	calledWith := ""
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		calledWith = filter.SubjectID
		return true, nil
	}

	if err := ensureGlobalConfigMutationAllowed(context.Background(), "", "ou_fallback"); err != nil {
		t.Fatalf("ensureGlobalConfigMutationAllowed() error = %v", err)
	}
	if calledWith != "ou_fallback" {
		t.Fatalf("expected checker to use fallback open id, got %q", calledWith)
	}
}

func TestEnsureGlobalConfigMutationAllowedRejectsMissingPermissionGrant(t *testing.T) {
	oldChecker := permissionGrantExists
	defer func() { permissionGrantExists = oldChecker }()
	useTestBotIdentity(t)

	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		return false, nil
	}

	err := ensureGlobalConfigMutationAllowed(context.Background(), "ou_actor", "")
	if err == nil {
		t.Fatalf("expected permission error")
	}
	if err.Error() != "only users with permission point config.write@global can modify global config" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureGlobalConfigMutationAllowedPropagatesLookupError(t *testing.T) {
	oldChecker := permissionGrantExists
	defer func() { permissionGrantExists = oldChecker }()
	useTestBotIdentity(t)

	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		return false, errors.New("lookup failed")
	}

	err := ensureGlobalConfigMutationAllowed(context.Background(), "ou_actor", "")
	if err == nil {
		t.Fatalf("expected lookup error")
	}
	if err.Error() != "lookup failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureGlobalConfigMutationAllowedRejectsMissingActorIdentity(t *testing.T) {
	err := ensureGlobalConfigMutationAllowed(context.Background(), " ", " ")
	if err == nil {
		t.Fatalf("expected identity error")
	}
	if err.Error() != "global config modification requires operator identity" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureGlobalConfigMutationAllowedRejectsMissingBotIdentity(t *testing.T) {
	oldChecker := permissionGrantExists
	oldIdentity := currentBotIdentity
	defer func() {
		permissionGrantExists = oldChecker
		currentBotIdentity = oldIdentity
	}()

	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		t.Fatal("permission lookup should not be called without bot identity")
		return false, nil
	}

	err := ensureGlobalConfigMutationAllowed(context.Background(), "ou_actor", "")
	if err == nil {
		t.Fatalf("expected bot identity error")
	}
	if err.Error() != "lark bot identity is missing: app_id and bot_open_id are both empty" {
		t.Fatalf("unexpected error: %v", err)
	}
}
