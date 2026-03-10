package permission

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

func TestEnsureManageAllowedAllowsBootstrapAdmin(t *testing.T) {
	oldBootstrap := currentBootstrapAdminOpen
	oldIdentity := currentBotIdentity
	defer func() {
		currentBootstrapAdminOpen = oldBootstrap
		currentBotIdentity = oldIdentity
	}()

	currentBootstrapAdminOpen = func() string { return "ou_bootstrap" }
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{AppID: "cli_test_app", BotOpenID: "ou_test_bot"}
	}

	if err := EnsureManageAllowed(context.Background(), "ou_bootstrap"); err != nil {
		t.Fatalf("EnsureManageAllowed() error = %v", err)
	}
}

func TestEnsureManageAllowedAllowsGrantedPermission(t *testing.T) {
	oldBootstrap := currentBootstrapAdminOpen
	oldIdentity := currentBotIdentity
	oldExists := permissionGrantExists
	defer func() {
		currentBootstrapAdminOpen = oldBootstrap
		currentBotIdentity = oldIdentity
		permissionGrantExists = oldExists
	}()

	currentBootstrapAdminOpen = func() string { return "" }
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{AppID: "cli_test_app", BotOpenID: "ou_test_bot"}
	}

	called := false
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		called = true
		if filter.PermissionPoint != permissioninfra.PermissionPointManage {
			t.Fatalf("unexpected permission point: %s", filter.PermissionPoint)
		}
		if filter.AppID != "cli_test_app" || filter.BotOpenID != "ou_test_bot" {
			t.Fatalf("unexpected identity: %s/%s", filter.AppID, filter.BotOpenID)
		}
		return true, nil
	}

	if err := EnsureManageAllowed(context.Background(), "ou_actor"); err != nil {
		t.Fatalf("EnsureManageAllowed() error = %v", err)
	}
	if !called {
		t.Fatal("expected permission checker to be called")
	}
}

func TestEnsureManageAllowedRejectsUnauthorizedUser(t *testing.T) {
	oldBootstrap := currentBootstrapAdminOpen
	oldIdentity := currentBotIdentity
	oldExists := permissionGrantExists
	defer func() {
		currentBootstrapAdminOpen = oldBootstrap
		currentBotIdentity = oldIdentity
		permissionGrantExists = oldExists
	}()

	currentBootstrapAdminOpen = func() string { return "" }
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{AppID: "cli_test_app", BotOpenID: "ou_test_bot"}
	}
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		return false, nil
	}

	err := EnsureManageAllowed(context.Background(), "ou_actor")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "only bootstrap admin or users with permission.manage@global / config.write@global can manage permissions" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureManageAllowedRejectsMissingBotIdentity(t *testing.T) {
	oldBootstrap := currentBootstrapAdminOpen
	oldIdentity := currentBotIdentity
	oldExists := permissionGrantExists
	defer func() {
		currentBootstrapAdminOpen = oldBootstrap
		currentBotIdentity = oldIdentity
		permissionGrantExists = oldExists
	}()

	currentBootstrapAdminOpen = func() string { return "ou_bootstrap" }
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	permissionGrantExists = func(ctx context.Context, filter permissioninfra.GrantFilter) (bool, error) {
		t.Fatal("permission lookup should not be called without bot identity")
		return false, nil
	}

	err := EnsureManageAllowed(context.Background(), "ou_bootstrap")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "lark bot identity is missing: app_id and bot_open_id are both empty" {
		t.Fatalf("unexpected error: %v", err)
	}
}
