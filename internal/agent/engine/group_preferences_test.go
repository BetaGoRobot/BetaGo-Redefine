package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestConfiguredGroupInstructionAndSourceBoundCorrectionReachActualModelScene(t *testing.T) {
	f := newConversationFixture(t, 131072, true)
	m := f.message(t, "preferences-request", "今天如何称呼小李？", "", true)
	for _, entry := range []domain.CatalogEntry{
		{Kind: domain.CatalogGroupConfig, Name: "reply_instruction", Revision: 1, Origin: m.Source, CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: "讨论技术时简短说明证据"}}},
		{Kind: domain.CatalogCorrectionKind, Name: "nickname", Revision: 1, Origin: m.Source, CatalogValue: domain.CatalogValue{Correction: &domain.CatalogCorrection{Text: "小李希望被称为李同学"}}},
	} {
		body, _ := json.Marshal(entry)
		if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,creator,body,updated_at)VALUES($1,$2,$3,1,'admin',$4,clock_timestamp())`, f.scope.Key(), entry.Kind, entry.Name, body); err != nil {
			t.Fatal(err)
		}
	}
	f.model.answer = `{"choice":"observe","messages":[]}`
	f.step(t, domain.JobAgentTurn)
	seen := false
	for _, request := range f.model.requests {
		for _, msg := range request.Messages {
			content, _ := msg.Content.(string)
			if msg.Role == "user" && strings.Contains(content, "GroupPreferences") && strings.Contains(content, "小李希望被称为李同学") && strings.Contains(content, "讨论技术时简短说明证据") {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatal("stored configuration/correction never reached model")
	}
	task, _, err := f.store.TaskForMessage(context.Background(), f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.ContextSources) == 0 {
		t.Fatal("preferences lost source binding")
	}
}
