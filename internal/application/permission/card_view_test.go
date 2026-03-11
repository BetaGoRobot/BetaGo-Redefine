package permission

import (
	"encoding/json"
	"strings"
	"testing"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

func TestBuildTargetUserFormUsesFilledPrimarySubmit(t *testing.T) {
	element := buildTargetUserForm("ou_actor", "ou_target", PermissionCardViewOptions{})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"select_person"`) || !strings.Contains(jsonStr, `"element_id":"permission_target_picker"`) {
		t.Fatalf("expected target user picker in permission card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"当前目标"`) || !strings.Contains(jsonStr, `"tag":"person"`) || !strings.Contains(jsonStr, `"user_id":"ou_target"`) {
		t.Fatalf("expected visible current target person in permission card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"initial_option":"ou_target"`) {
		t.Fatalf("expected picker initial option in permission card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"查看自己"`) {
		t.Fatalf("expected self shortcut button in permission card: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"input"`) {
		t.Fatalf("did not expect legacy openid input in permission card: %s", jsonStr)
	}
}

func TestBuildScopeControlUsesFilledPrimaryGrant(t *testing.T) {
	element := buildScopeControl("permission.manage", "global", "ou_target", permissioninfra.Grant{}, false, PermissionCardViewOptions{})
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"授予"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for grant action: %s", jsonStr)
	}
}
