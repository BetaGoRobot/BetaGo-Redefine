// Command backfill_token_bot_id 把当前 bot 的 llm_token_usage_records.bot_id
// 列从空字符串回刷成 botidentity.Current() 给出的标识。
//
// 工作流：
//  1. 加载与 larkrobot 相同的配置与基础设施（db / opensearch）。
//  2. 从当前 bot 进程独占的 lark_msg_index 聚合"近 N 天出现过的 chat_id 集合"
//     作为权威归属白名单。
//  3. 在 token 表里把 bot_id='' 且 chat_id 命中白名单的行 UPDATE 成当前 bot id。
//
// 强假设：每个 bot 进程使用独立的 lark_msg_index。如果两个 bot 共用一份索引，
// 数据归属本身就无法切分，需要先在 OpenSearch 侧做隔离。
//
// 用法：
//
//	BETAGO_CONFIG_PATH=/path/to/config.toml \
//	    go run ./cmd/tools/backfill_token_bot_id [-window-days 60] [-dry-run]
//
// 必须每个 bot 实例分别执行一次。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"gorm.io/gorm"
)

func main() {
	var (
		windowDays int
		dryRun     bool
		batchSize  int
	)
	flag.IntVar(&windowDays, "window-days", 90, "回刷窗口天数；窗口太短会漏掉早期的 chat_id")
	flag.BoolVar(&dryRun, "dry-run", false, "只打印将要更新的行数，不真正写库")
	flag.IntVar(&batchSize, "batch-size", 5000, "每批 UPDATE 的 chat_id 上限，防止 IN 子句过大")
	flag.Parse()

	if err := run(context.Background(), windowDays, dryRun, batchSize); err != nil {
		fmt.Fprintf(os.Stderr, "backfill failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, windowDays int, dryRun bool, batchSize int) error {
	cfgPath := os.Getenv("BETAGO_CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = ".dev/config.toml"
	}
	cfg, err := infraConfig.LoadFileE(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || cfg.LarkConfig == nil || strings.TrimSpace(cfg.LarkConfig.AppID) == "" {
		return errors.New("config missing lark_config.app_id; cannot identify bot")
	}
	botID := "lark:" + strings.TrimSpace(botidentity.Current().AppID)
	if botID == "lark:" {
		return errors.New("bot identity unavailable; check lark_config.app_id")
	}

	db.Init(cfg.DBConfig)
	gormDB := db.DB()
	if gormDB == nil {
		return errors.New("db not initialized")
	}
	opensearch.Init(cfg.OpensearchConfig)
	if ok, reason := opensearch.Status(); !ok {
		return fmt.Errorf("opensearch not ready: %s", reason)
	}

	since := time.Now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	chatIDs, err := authoritativeChatIDs(ctx, since)
	if err != nil {
		return fmt.Errorf("aggregate chat ids from opensearch: %w", err)
	}
	if len(chatIDs) == 0 {
		fmt.Println("no chat ids found in lark_msg_index for the requested window; nothing to backfill")
		return nil
	}
	fmt.Printf("bot=%s window_days=%d candidate_chat_ids=%d\n", botID, windowDays, len(chatIDs))

	// 安全断言：候选 chat_id 不应已被其他 bot 认领。若发现冲突，必须先排查
	// OpenSearch 索引是否真的按 bot 隔离，再决定是否继续。
	if conflicts, err := findConflicts(ctx, gormDB, botID, chatIDs, batchSize); err != nil {
		return fmt.Errorf("conflict check: %w", err)
	} else if len(conflicts) > 0 {
		sample := conflicts
		if len(sample) > 5 {
			sample = sample[:5]
		}
		return fmt.Errorf("aborting: %d chat_id already owned by other bot_id, sample=%v", len(conflicts), sample)
	}

	total, err := updateRows(ctx, gormDB, botID, chatIDs, batchSize, dryRun)
	if err != nil {
		return fmt.Errorf("update rows: %w", err)
	}
	if dryRun {
		fmt.Printf("dry-run: would have updated %d rows\n", total)
	} else {
		fmt.Printf("backfilled %d rows to bot_id=%s\n", total, botID)
	}
	return nil
}

// authoritativeChatIDs 聚合当前 bot 进程独占索引中的 chat_id 集合。
func authoritativeChatIDs(ctx context.Context, since time.Time) ([]string, error) {
	req := map[string]any{
		"size": 0,
		"query": map[string]any{
			"range": map[string]any{
				"create_time_v2": map[string]any{
					"gte": since.Format(time.RFC3339),
				},
			},
		},
		"aggs": map[string]any{
			"chat_ids": map[string]any{
				"terms": map[string]any{
					"field": "chat_id",
					"size":  10000,
				},
			},
		},
	}
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, "", ""), req)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Aggregations) == 0 {
		return nil, nil
	}
	var aggs struct {
		ChatIDs struct {
			Buckets []struct {
				Key string `json:"key"`
			} `json:"buckets"`
		} `json:"chat_ids"`
	}
	if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(aggs.ChatIDs.Buckets))
	for _, b := range aggs.ChatIDs.Buckets {
		id := strings.TrimSpace(b.Key)
		if id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// findConflicts 返回已经被别的 bot_id 认领过的 (chat_id, bot_id) 对。
func findConflicts(ctx context.Context, gormDB *gorm.DB, botID string, chatIDs []string, batchSize int) ([]string, error) {
	type row struct {
		ChatID string
		BotID  string
	}
	conflicts := make([]string, 0)
	for start := 0; start < len(chatIDs); start += batchSize {
		end := start + batchSize
		if end > len(chatIDs) {
			end = len(chatIDs)
		}
		batch := chatIDs[start:end]
		var rows []row
		err := gormDB.WithContext(ctx).
			Table("llm_token_usage_records").
			Select("DISTINCT chat_id, bot_id").
			Where("chat_id IN ?", batch).
			Where("bot_id <> ''").
			Where("bot_id <> ?", botID).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			conflicts = append(conflicts, fmt.Sprintf("%s=%s", r.ChatID, r.BotID))
		}
	}
	return conflicts, nil
}

// updateRows 把候选 chat_id 中 bot_id='' 的行 UPDATE 成当前 bot id。
// dryRun=true 时只 SELECT COUNT，不真正写库。
func updateRows(ctx context.Context, gormDB *gorm.DB, botID string, chatIDs []string, batchSize int, dryRun bool) (int64, error) {
	var total int64
	for start := 0; start < len(chatIDs); start += batchSize {
		end := start + batchSize
		if end > len(chatIDs) {
			end = len(chatIDs)
		}
		batch := chatIDs[start:end]
		if dryRun {
			var n int64
			err := gormDB.WithContext(ctx).
				Table("llm_token_usage_records").
				Where("bot_id = ''").
				Where("chat_id IN ?", batch).
				Count(&n).Error
			if err != nil {
				return total, err
			}
			total += n
			continue
		}
		res := gormDB.WithContext(ctx).
			Table("llm_token_usage_records").
			Where("bot_id = ''").
			Where("chat_id IN ?", batch).
			Update("bot_id", botID)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
	}
	return total, nil
}
