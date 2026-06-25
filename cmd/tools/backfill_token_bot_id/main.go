// Command backfill_token_bot_id 把当前 bot 的 llm_token_usage_records.bot_id
// 列从空字符串回刷成 botidentity.Current() 给出的标识。
//
// 工作流：
//  1. 加载与 larkrobot 相同的配置与基础设施（db / opensearch）。
//  2. 在 lark_msg_index 中按 user_id == BotOpenID 过滤近 N 天的消息文档，聚合
//     出"当前 bot 真正发过言的 chat_id 集合"作为权威归属白名单。
//  3. 在 token 表里把 bot_id='' 且 chat_id 命中白名单的行 UPDATE 成当前 bot id。
//
// 注意：本脚本依赖"机器人发过言"这一信号——若某会话窗口内 bot 没主动发言
// （例如只接收消息或只补 token 计费），那个 chat_id 不会被回刷，会保留 bot_id=''。
// 这对历史归属来说是「稍微不准但一定不错归属」的取舍：宁可漏认领也不能错认领。
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
	identity := botidentity.Current()
	if identity.AppID == "" {
		return errors.New("bot identity unavailable; check lark_config.app_id")
	}
	if identity.BotOpenID == "" {
		return errors.New("config missing lark_config.bot_open_id; cannot match bot user_id in lark_msg_index")
	}
	botID := "lark:" + strings.TrimSpace(identity.AppID)

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
	chatIDs, err := authoritativeChatIDs(ctx, identity.BotOpenID, since)
	if err != nil {
		return fmt.Errorf("aggregate chat ids from opensearch: %w", err)
	}
	if len(chatIDs) == 0 {
		fmt.Println("no chat ids found where user_id == bot_open_id for the requested window; nothing to backfill")
		return nil
	}
	fmt.Printf("bot=%s bot_open_id=%s window_days=%d candidate_chat_ids=%d\n",
		botID, identity.BotOpenID, windowDays, len(chatIDs))

	// 安全断言：已被其他 bot 认领过的 chat_id 绝不能覆盖；共享群里多个 bot
	// 都发过言时，这类冲突是正常现象，因此只跳过冲突 chat_id，继续回刷其余记录。
	conflictSet, conflictPairs, err := findConflicts(ctx, gormDB, botID, chatIDs, batchSize)
	if err != nil {
		return fmt.Errorf("conflict check: %w", err)
	}
	if len(conflictPairs) > 0 {
		sample := conflictPairs
		if len(sample) > 5 {
			sample = sample[:5]
		}
		fmt.Printf("skipping %d conflicting chat_ids already owned by other bot_id, sample=%v\n",
			len(conflictSet), sample)
	}
	chatIDs = filterChatIDs(chatIDs, conflictSet)
	if len(chatIDs) == 0 {
		fmt.Println("no non-conflicting chat ids left after ownership check; nothing to backfill")
		return nil
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

// authoritativeChatIDs 聚合「user_id == botOpenID」近 N 天出现过的 chat_id 集合。
//
// 多 bot 共用同一份 lark_msg_index 时，单纯按时间过滤会把所有 bot 的会话混在一起；
// 用 user_id 过滤是因为机器人响应消息会以自身身份写入索引，因此 user_id 等于
// 当前 bot 的 BotOpenID 的文档就是「当前 bot 真正发过言」的会话。这种判定会
// 漏掉「窗口内 bot 没主动发言」的会话，但绝不会错认领别的 bot 的会话。
func authoritativeChatIDs(ctx context.Context, botOpenID string, since time.Time) ([]string, error) {
	req := map[string]any{
		"size": 0,
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"user_id": botOpenID}},
					{"range": map[string]any{
						"create_time_v2": map[string]any{
							"gte": since.Format(time.RFC3339),
						},
					}},
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

// findConflicts 返回已经被别的 bot_id 认领过的 chat_id 集合，以及展示用的
// (chat_id, bot_id) 样例对。
func findConflicts(ctx context.Context, gormDB *gorm.DB, botID string, chatIDs []string, batchSize int) (map[string]struct{}, []string, error) {
	type row struct {
		ChatID string
		BotID  string
	}
	conflictSet := make(map[string]struct{})
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
			return nil, nil, err
		}
		for _, r := range rows {
			chatID := strings.TrimSpace(r.ChatID)
			if chatID == "" {
				continue
			}
			conflictSet[chatID] = struct{}{}
			conflicts = append(conflicts, fmt.Sprintf("%s=%s", chatID, strings.TrimSpace(r.BotID)))
		}
	}
	return conflictSet, conflicts, nil
}

func filterChatIDs(chatIDs []string, exclude map[string]struct{}) []string {
	if len(exclude) == 0 {
		return chatIDs
	}
	filtered := make([]string, 0, len(chatIDs))
	for _, chatID := range chatIDs {
		if _, blocked := exclude[chatID]; blocked {
			continue
		}
		filtered = append(filtered, chatID)
	}
	return filtered
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
