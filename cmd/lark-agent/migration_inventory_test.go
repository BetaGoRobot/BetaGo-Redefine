package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

type migrationInventoryRow struct {
	legacy       string
	group        string
	tools        []string
	durableFinal bool
}

// Exact baseline: 55 fixed definitions + 8 Luckin definitions + provider-native
// web search, from the 2026-10-22 inventory sections 4/5. Each legacy row occurs
// once even when the new capability is composed from several atomic leaves.
var migrationInventory = []migrationInventoryRow{
	{"gold_price_get", "fixed", []string{"gold_price_get", "compose_card", "feishu_send_card"}, false},
	{"stock_zh_a_get", "fixed", []string{"stock_zh_a_get", "compose_card", "feishu_send_card"}, false},
	{"finance_market_data_get", "fixed", []string{"finance_market_data_get"}, false},
	{"finance_news_get", "fixed", []string{"finance_news_get"}, false},
	{"economy_indicator_get", "fixed", []string{"economy_indicator_get"}, false},
	{"finance_tool_discover", "fixed", []string{"finance_tool_discover"}, false},
	{"music_search", "fixed", []string{"music_search", "music_song_detail", "music_album_detail", "music_playlist_detail", "music_send_card", "music_send_search_card", "music_send_audio"}, false},
	{"oneword_get", "fixed", []string{"oneword_get"}, false},
	{"get_chat_members", "fixed", []string{"feishu_group_members"}, false},
	{"get_recent_active_members", "fixed", []string{"group_message_statistics", "feishu_group_members"}, false},
	{"search_history", "fixed", []string{"search_literal", "search_discussion", "search_memory", "feishu_message_history", "feishu_read_message"}, false},
	{"talkrate_get", "fixed", []string{"group_message_statistics", "compose_card", "feishu_send_card"}, false},
	{"word_cloud_get", "fixed", []string{"group_message_statistics", "discussion_list", "compose_card", "feishu_send_card"}, false},
	{"word_cloud_graph_get", "fixed", []string{"group_message_statistics", "compose_card", "feishu_send_card"}, false},
	{"word_chunks_get", "fixed", []string{"discussion_list"}, false},
	{"word_chunk_detail_get", "fixed", []string{"discussion_get"}, false},
	{"create_todo", "fixed", []string{"todo_create"}, false},
	{"update_todo", "fixed", []string{"todo_update", "todo_detail"}, false},
	{"list_todos", "fixed", []string{"todo_list", "todo_detail"}, false},
	{"delete_todo", "fixed", []string{"todo_delete"}, false},
	{"create_schedule", "fixed", []string{"schedule_create", "reminder_create"}, false},
	{"list_schedules", "fixed", []string{"schedule_list", "reminder_list"}, false},
	{"query_schedule", "fixed", []string{"schedule_query"}, false},
	{"delete_schedule", "fixed", []string{"schedule_update", "reminder_cancel"}, false},
	{"pause_schedule", "fixed", []string{"schedule_update"}, false},
	{"resume_schedule", "fixed", []string{"schedule_update"}, false},
	{"edit_schedule", "fixed", []string{"schedule_update"}, false},
	{"image_add", "fixed", []string{"catalog_put"}, false},
	{"image_get", "fixed", []string{"catalog_get", "catalog_list", "feishu_send_image"}, false},
	{"image_delete", "fixed", []string{"catalog_delete"}, false},
	{"word_add", "fixed", []string{"catalog_put"}, false},
	{"word_get", "fixed", []string{"catalog_get", "catalog_list"}, false},
	{"reply_add", "fixed", []string{"catalog_put"}, false},
	{"reply_get", "fixed", []string{"catalog_get", "catalog_list"}, false},
	{"send_message", "fixed", nil, true},
	{"add_emoji_reaction", "fixed", []string{"feishu_add_reaction"}, false},
	{"revert_message", "fixed", []string{"feishu_retract_message"}, false},
	{"config_set", "fixed", []string{"catalog_put"}, false},
	{"config_list", "fixed", []string{"catalog_get", "catalog_list"}, false},
	{"config_delete", "fixed", []string{"catalog_delete"}, false},
	{"feature_list", "fixed", []string{"catalog_feature_list"}, false},
	{"feature_block", "fixed", []string{"catalog_put"}, false},
	{"feature_unblock", "fixed", []string{"catalog_put", "catalog_delete"}, false},
	{"mute_robot", "fixed", []string{"catalog_put"}, false},
	{"permission_manage", "fixed", []string{"catalog_permission_query", "catalog_put", "catalog_delete"}, false},
	{"store_correction", "fixed", []string{"catalog_put", "catalog_get"}, false},
	{"set_chat_context", "fixed", []string{"catalog_put"}, false},
	{"set_history_cutoff", "fixed", []string{"catalog_put"}, false},
	{"discover_card_components", "fixed", []string{"discover_card_components"}, false},
	{"compose_card", "fixed", []string{"compose_card", "feishu_send_card"}, false},
	{"ratelimit_list", "fixed", []string{"runtime_limits_get", "catalog_capacity_stats"}, false},
	{"ratelimit_stats_get", "fixed", []string{"catalog_execution_stats"}, false},
	{"research_read_url", "fixed", []string{"research_read_url", "read_webpage"}, false},
	{"research_extract_evidence", "fixed", []string{"research_extract_evidence"}, false},
	{"research_source_ledger", "fixed", []string{"research_source_ledger"}, false},
	{"luckin_bind_token_guide", "luckin", []string{"luckin_bind_token_guide", "luckin_account_bind"}, false},
	{"luckin_shop_search", "luckin", []string{"luckin_shop_search"}, false},
	{"luckin_product_search", "luckin", []string{"luckin_product_search"}, false},
	{"luckin_product_detail", "luckin", []string{"luckin_product_detail"}, false},
	{"luckin_product_switch", "luckin", []string{"luckin_product_switch"}, false},
	{"luckin_order_preview", "luckin", []string{"luckin_order_preview"}, false},
	{"luckin_order_detail", "luckin", []string{"luckin_order_detail"}, false},
	{"luckin_order_prepare_create", "luckin", []string{"luckin_order_prepare_create", "luckin_order_create"}, false},
	{"provider_native_web_search", "web", []string{"web_search", "read_webpage"}, false},
}

type inventoryRetriever struct{ t *testing.T }

func (r inventoryRetriever) Retrieve(context.Context, domain.Query) (retrieval.Result, error) {
	r.t.Error("inventory construction invoked retrieval")
	return retrieval.Result{}, domain.ErrInvalid
}

type inventorySearcher struct{ t *testing.T }

func (s inventorySearcher) Search(context.Context, string, int) (web.SearchResult, error) {
	s.t.Error("inventory construction invoked web search")
	return web.SearchResult{}, domain.ErrInvalid
}

type inventoryTransport struct{ t *testing.T }

type inventoryMusicPresenter struct{ t *testing.T }

func (p inventoryMusicPresenter) Send(context.Context, business.Identity, string, musiccards.Request) (musiccards.Receipt, error) {
	p.t.Error("inventory invoked music presentation")
	return musiccards.Receipt{}, domain.ErrInvalid
}

func (n inventoryTransport) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Error("inventory construction invoked Lark HTTP")
	return nil, fmt.Errorf("inventory has no network")
}

func inventoryRegistry(t *testing.T, full bool) *atoms.Registry {
	t.Helper()
	store, err := pgstore.New(&sql.DB{}, "inventory_only")
	if err != nil {
		t.Fatal(err)
	}
	client, err := lark.NewClient(lark.ClientConfig{AppID: "inventory-app", AppSecret: "inventory-not-a-secret", BotOpenID: "inventory-bot", TenantID: "inventory-tenant", AllowedChatIDs: []string{"inventory-chat"}, HTTPClient: &http.Client{Transport: inventoryTransport{t}}})
	if err != nil {
		t.Fatal(err)
	}
	c := config.Config{App: config.App{TenantID: "inventory-tenant", Visibility: "group"}, Lark: config.Lark{AppID: "inventory-app", AppSecret: "inventory-not-a-secret"}, Runtime: config.Runtime{MaxConcurrentTasks: 4, MaxTurns: 8, MaxContextBytes: 65536, TaskTimeoutSeconds: 1800, TurnLeaseSeconds: 60}, Worker: config.Worker{Concurrency: 4, PollMilliseconds: 250}}
	if full {
		// Only executable discovery occurs at registration. A test-owned executable
		// avoids any dependency on host FFmpeg; no audio process is started.
		ffmpeg := filepath.Join(t.TempDir(), "ffmpeg-inventory")
		if err = os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		c.Capabilities = config.Capabilities{AKShareURL: "http://127.0.0.1:1", HitokotoURL: "http://127.0.0.1:2", MusicURL: "http://127.0.0.1:3", AudioEnabled: true, FFmpegPath: ffmpeg, LuckinURL: "http://127.0.0.1:4", LuckinBindingURL: "https://binding.invalid", LuckinSigningKey: strings.Repeat("inventory", 4), LuckinCredentialsKey: strings.Repeat("a1", 32)}
	}
	opts := atoms.Options{Sources: store, Tasks: store, Group: client, Governance: store, Retrieval: inventoryRetriever{t}, WebPages: web.NewReader()}
	if full {
		opts.WebSearch = inventorySearcher{t}
	}
	accountStore, err := accounts.New(&sql.DB{}, "inventory_only", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	parts := &components{store: store, lark: client, accounts: accountStore}
	if full {
		parts.musicCards = inventoryMusicPresenter{t}
	}
	opts.Extensions, err = capabilityDefinitions(c, parts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Extensions = append(opts.Extensions, atoms.PresentationDefinitions(atoms.PresentationOptions{Messenger: client, Retractor: client, Sources: store, Tasks: store})...)
	opts.Extensions = append(opts.Extensions, atoms.ResearchDefinitions(opts)...)
	registry, err := atoms.New(opts)
	if err != nil {
		t.Fatalf("actual full registry schema/collision/policy construction: %v", err)
	}
	return registry
}

func TestMigrationInventoryAll64LegacyCapabilitiesHaveActualIndependentRoutes(t *testing.T) {
	registry := inventoryRegistry(t, true)
	registered := map[string]atoms.Definition{}
	for _, d := range registry.Definitions() {
		if d.Run == nil || d.Digest == "" || len(d.Parameters) == 0 {
			t.Fatalf("placeholder capability %s", d.Name)
		}
		registered[d.Name] = d
	}
	t.Logf("full configured independent registry: %d actual tools", len(registered))
	if len(migrationInventory) != 64 {
		t.Fatalf("inventory=%d want 55+8+1", len(migrationInventory))
	}
	seen := map[string]bool{}
	groups := map[string]int{}
	for _, row := range migrationInventory {
		if seen[row.legacy] {
			t.Fatalf("duplicate old capability %s", row.legacy)
		}
		seen[row.legacy] = true
		groups[row.group]++
		t.Run(row.legacy, func(t *testing.T) {
			if row.durableFinal {
				if row.legacy != "send_message" || len(row.tools) != 0 {
					t.Fatal("invalid durable-final mapping")
				}
				if _, ok := registered["feishu_send_message"]; ok {
					t.Fatal("ordinary reply exposes duplicate generic send effect")
				}
				if err := (domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: "reply", ReplyToMessageID: "inventory-root"}}}).Validate(); err != nil {
					t.Fatalf("durable conversation contract unavailable: %v", err)
				}
				return
			}
			if len(row.tools) == 0 {
				t.Fatal("unmapped capability")
			}
			for _, name := range row.tools {
				if _, ok := registered[name]; !ok {
					t.Errorf("legacy %s requires missing actual tool %s", row.legacy, name)
				}
			}
		})
	}
	if groups["fixed"] != 55 || groups["luckin"] != 8 || groups["web"] != 1 {
		t.Fatalf("baseline category counts %v", groups)
	}
	if len(registry.Policies()) != len(registered) {
		t.Fatal("registered capability missing persisted policy")
	}
	for _, p := range registry.Policies() {
		d := registered[p.Name]
		if p.Digest == "" || p.SideEffect != d.SideEffect || p.AutoExecute != d.AutoExecute {
			t.Fatalf("policy lost for %s", p.Name)
		}
	}
	for _, name := range []string{"luckin_order_create", "catalog_put", "catalog_delete", "feishu_retract_message"} {
		d := registered[name]
		if !d.SideEffect || d.AutoExecute {
			t.Fatalf("high-impact/admin mutation ungated %s", name)
		}
	}
}

func TestMigrationInventoryDoesNotAdvertiseUnconfiguredBusinessPlaceholders(t *testing.T) {
	registry := inventoryRegistry(t, false)
	for _, d := range registry.Definitions() {
		if strings.HasPrefix(d.Name, "music_") || strings.HasPrefix(d.Name, "luckin_") || strings.HasPrefix(d.Name, "finance_") || d.Name == "gold_price_get" || d.Name == "stock_zh_a_get" || d.Name == "oneword_get" || d.Name == "economy_indicator_get" || d.Name == "stock_hot_search_get" || d.Name == "web_search" {
			t.Errorf("unconfigured provider advertised %s", d.Name)
		}
	}
}

func TestMigrationInventoryDocumentMatchesAll64AuditedRows(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "architecture", "2026-10-27-tool-migration-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 6 {
			continue
		}
		if _, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
			continue
		}
		legacy := strings.Trim(strings.TrimSpace(parts[2]), "`")
		if _, exists := rows[legacy]; exists {
			t.Fatalf("duplicate documentation row %s", legacy)
		}
		rows[legacy] = parts[3]
	}
	if len(rows) != 64 {
		t.Fatalf("document rows=%d want64", len(rows))
	}
	for _, row := range migrationInventory {
		target, ok := rows[row.legacy]
		if !ok {
			t.Errorf("document omits %s", row.legacy)
			continue
		}
		for _, name := range row.tools {
			if !strings.Contains(target, "`"+name+"`") {
				t.Errorf("document mapping %s omits actual target %s", row.legacy, name)
			}
		}
		if row.durableFinal && !strings.Contains(target, "durable_final_delivery") {
			t.Error("ordinary send lacks durable final mapping")
		}
	}
}
