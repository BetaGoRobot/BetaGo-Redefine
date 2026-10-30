package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestLargeGroupPreferencesCannotStarveSmallConversationScene(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	f.engine.c.MaxContextBytes = 8192
	e := domain.CatalogEntry{Kind: domain.CatalogGroupConfig, Name: "reply_instruction", Revision: 1, Origin: f.task.Root, CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: strings.Repeat("x", 4096)}}}
	body, _ := json.Marshal(e)
	if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,creator,body,updated_at)VALUES($1,$2,$3,1,'admin',$4,clock_timestamp())`, f.task.Scope.Key(), e.Kind, e.Name, body); err != nil {
		t.Fatal(err)
	}
	raw, refs, _, err := f.engine.prompt(context.Background(), f.task)
	if err != nil || len(raw) > 4096 || !strings.Contains(raw, `"Truncated":true`) {
		t.Fatalf("preferences starved scene: bytes=%d err=%v", len(raw), err)
	}
	if len(refs) == 0 {
		t.Fatal("current request source lost")
	}
}
