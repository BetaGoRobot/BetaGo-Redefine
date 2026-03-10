package permission

import (
	"encoding/json"
	"strings"
	"testing"

	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

func TestBuildScopeControlDoesNotNestFormInColumn(t *testing.T) {
	grant := permissioninfra.Grant{Remark: "bootstrap grant"}

	element := buildScopeControl("permission.manage", "global", "ou_target", grant, true)
	if tag, ok := element["tag"].(string); !ok || tag != "column_set" {
		t.Fatalf("expected scope control to be a column_set, got %#v", element["tag"])
	}

	columns, ok := element["columns"].([]any)
	if !ok || len(columns) != 2 {
		t.Fatalf("expected two columns in scope control, got %#v", element["columns"])
	}

	jsonBytes, err := json.Marshal(element)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(jsonBytes), `"tag":"form"`) {
		t.Fatalf("scope control should not contain nested form: %s", string(jsonBytes))
	}
}
