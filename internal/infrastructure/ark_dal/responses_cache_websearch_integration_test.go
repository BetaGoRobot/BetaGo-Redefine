package ark_dal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestLiveCachingWithWebSearchReportsUnsupported(t *testing.T) {
	if os.Getenv("BETAGO_RUN_ARK_INTEGRATION") != "1" {
		t.Skip("set BETAGO_RUN_ARK_INTEGRATION=1 to run live Ark cache and web-search integration test")
	}

	cfg, err := config.LoadFileE(liveArkConfigPath(t))
	if err != nil {
		t.Fatalf("load Ark integration config: %v", err)
	}
	if cfg.ArkConfig == nil || strings.TrimSpace(cfg.ArkConfig.APIKey) == "" {
		t.Fatal("Ark integration API key is unavailable")
	}
	modelID := strings.TrimSpace(cfg.ArkConfig.NormalModel)
	if modelID == "" {
		t.Fatal("Ark integration normal model is unavailable")
	}

	client := arkruntime.NewClientWithApiKey(cfg.ArkConfig.APIKey)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	limit := int64(3)
	_, err = client.CreateResponses(ctx, &responses.ResponsesRequest{
		Model: modelID,
		Input: singleTextInput(
			responses.MessageRole_user,
			"请联网搜索火山引擎官网，只回答官网域名。",
		),
		Store: new(true),
		Tools: []*responses.ResponsesTool{{
			Union: &responses.ResponsesTool_ToolWebSearch{
				ToolWebSearch: &responses.ToolWebSearch{
					Type:  responses.ToolType_web_search,
					Limit: &limit,
				},
			},
		}},
		ToolChoice: &responses.ResponsesToolChoice{
			Union: &responses.ResponsesToolChoice_WebSearchToolChoice{
				WebSearchToolChoice: &responses.WebSearchToolChoice{
					Type: responses.ToolType_web_search,
				},
			},
		},
		Caching: &responses.ResponsesCaching{
			Type: responses.CacheType_enabled.Enum(),
		},
	})
	if err == nil {
		t.Fatal("create response with caching and web search succeeded; want unsupported-parameter error")
	}
	if !strings.Contains(err.Error(), "caching is not supported for build-in tools") {
		t.Fatalf("create response with caching and web search error = %v, want built-in-tools incompatibility", err)
	}
	t.Logf("Ark rejected caching with built-in web search as expected: %v", err)
}

func liveArkConfigPath(t *testing.T) string {
	t.Helper()

	configured := strings.TrimSpace(os.Getenv("BETAGO_CONFIG_PATH"))
	if configured == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	if _, err := os.Stat(configured); err == nil {
		return configured
	}

	fromPackage := filepath.Join("..", "..", "..", configured)
	if _, err := os.Stat(fromPackage); err != nil {
		t.Skipf("Ark integration config is unavailable: %v", err)
	}
	return fromPackage
}
