package larktpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestNewCardContentV2InjectsBaseVarsIntoTypedTemplate(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[otel_config]\ngrafana_url = \"https://grafana.example.com/explore\"\njaeger_data_source_id = \"jaeger\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := config.LoadFileE(configPath); err != nil {
		t.Fatalf("load config: %v", err)
	}

	tpl := TemplateVersionV2[MusicListCardVars]{
		TemplateVersion: &model.TemplateVersion{
			TemplateID: "tpl-music-list",
		},
	}
	vars := &MusicListCardVars{
		ObjectList1: []*MusicListCardItem{
			{
				Field1:     "**稻香**\n**周杰伦**",
				Field2:     ImageKeyRef{ImgKey: "img_123"},
				ButtonInfo: "点击播放",
				ElementID:  "1",
				ButtonVal:  map[string]string{"action": "music.play", "id": "1"},
			},
		},
		Query: "[稻香]",
	}
	tpl = tpl.WithData(vars)

	card := NewCardContentV2(context.Background(), tpl)

	if vars.RefreshTime == "" {
		t.Fatalf("expected typed vars to receive refresh_time")
	}
	if vars.JaegerTraceURL == "" {
		t.Fatalf("expected typed vars to receive jaeger trace url")
	}
	if got := card.Data.TemplateVariable["query"]; got != "[稻香]" {
		t.Fatalf("expected query to be preserved, got %#v", got)
	}
	if _, ok := card.Data.TemplateVariable["refresh_time"]; !ok {
		t.Fatalf("expected refresh_time in template variables")
	}
	if _, ok := card.Data.TemplateVariable["withdraw_object"]; !ok {
		t.Fatalf("expected withdraw_object in template variables")
	}
}
