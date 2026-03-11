package permission

import (
	"encoding/json"
	"strings"
	"testing"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

func TestBuildTargetUserFormUsesFilledPrimarySubmit(t *testing.T) {
	element := buildTargetUserForm("ou_actor", "ou_target")
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"查看用户"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for target view submit: %s", jsonStr)
	}
}

func TestBuildScopeControlUsesFilledPrimaryGrant(t *testing.T) {
	element := buildScopeControl("permission.manage", "global", "ou_target", permissioninfra.Grant{}, false)
	raw, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"授予"`) || !strings.Contains(jsonStr, `"type":"primary_filled"`) {
		t.Fatalf("expected filled primary style for grant action: %s", jsonStr)
	}
}
