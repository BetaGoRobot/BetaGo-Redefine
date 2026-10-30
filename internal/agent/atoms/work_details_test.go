package atoms_test

import (
	"database/sql"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestTodoSchemasExposeDetailsAndExplicitClearWithoutIdentityOverride(t *testing.T) {
	s, _ := pgstore.New(&sql.DB{}, "schema")
	r, err := atoms.New(atoms.Options{Tasks: s})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"todo_create": `{"title":"x","priority":"urgent","due_at":"2026-11-01T12:00:00+08:00","tags":["ops"],"assignee_id":"self"}`, "todo_update": `{"id":"x","priority":"","clear_due_at":true,"tags":[],"assignee_id":""}`, "todo_list": `{"creator_id":"alice","assignee_id":"bob","tags":["ops"],"overdue":true,"limit":2,"cursor":"cursor"}`, "todo_detail": `{"id":"x"}`, "todo_delete": `{"id":"x"}`} {
		if _, err := namedTool(t, r, name).Validate(raw); err != nil {
			t.Fatalf("%s rich schema: %v", name, err)
		}
	}
	for name, raw := range map[string]string{"todo_create": `{"title":"x","priority":"root"}`, "todo_update": `{"id":"x","due_at":null}`, "todo_list": `{"scope":"other"}`} {
		if _, err := namedTool(t, r, name).Validate(raw); err == nil {
			t.Fatalf("%s accepted invalid %s", name, raw)
		}
	}
}
