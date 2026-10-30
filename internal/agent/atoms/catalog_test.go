package atoms_test

import (
	"database/sql"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestCatalogSchemasDenyArbitrarySettingsAuthorityAndMismatchedKinds(t *testing.T) {
	store, err := pgstore.New(&sql.DB{}, "catalog_schema_test")
	if err != nil {
		t.Fatal(err)
	}
	r, err := atoms.New(atoms.Options{Extensions: atoms.CatalogDefinitions(store)})
	if err != nil {
		t.Fatal(err)
	}
	put := namedTool(t, r, "catalog_put")
	if !put.SideEffect || put.AutoExecute {
		t.Fatal("catalog mutation lost explicit approval")
	}
	for _, raw := range []string{
		`{"kind":"phrase","name":"hello-phrase","expected_revision":0,"phrase":{"text":"hello","weight":3,"enabled":true}}`,
		`{"kind":"group_config","name":"reply_instruction","expected_revision":0,"config":{"value":"brief"}}`,
		`{"kind":"reply_rule","name":"hello","expected_revision":0,"rule":{"pattern":"hello","match":"contains","reply":"hi","probability":0.5,"weight":3,"enabled":true}}`,
	} {
		if _, err := put.Validate(raw); err != nil {
			t.Fatalf("valid typed input rejected: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"kind":"group_config","name":"api_key","expected_revision":0,"config":{"value":"secret"}}`,
		`{"kind":"feature","name":"shell","expected_revision":0,"feature":{"enabled":true}}`,
		`{"kind":"feature","name":"music","expected_revision":0,"feature":{"enabled":true},"actor_id":"admin"}`,
		`{"kind":"group_config","name":"reply_instruction","expected_revision":0,"feature":{"enabled":true}}`,
		`{"kind":"permission","name":"x","expected_revision":0,"permission":{"subject_id":"alice","point":"permission.manage"}}`,
		`{"kind":"reply_rule","name":"hello","expected_revision":0,"rule":{"pattern":"hello","match":"regexp","reply":"hi","probability":1,"weight":1,"enabled":true}}`,
	} {
		if _, err := put.Validate(raw); err == nil {
			t.Fatalf("unsafe input accepted: %s", raw)
		}
	}
	for _, name := range []string{"catalog_get", "catalog_list", "catalog_permission_query", "catalog_capacity_stats", "catalog_execution_stats", "catalog_feature_list"} {
		d := namedTool(t, r, name)
		if d.SideEffect || d.AutoExecute {
			t.Fatalf("read has effects: %s", name)
		}
	}
}
