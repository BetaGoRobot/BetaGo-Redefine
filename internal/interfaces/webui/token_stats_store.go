package webui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

// tokenStatsStore 封装对 llm_token_usage_records 表的聚合查询。
//
// 所有查询都按 bot_id + chat_id + 时间窗口过滤；bot_id 用当前进程的身份做硬隔离，
// 避免多 bot 共用一张表时把别的 bot 的会话误吐进当前列表/统计。
type tokenStatsStore struct {
	db    *gorm.DB
	botID string
}

func newTokenStatsStore(db *gorm.DB, botID string) *tokenStatsStore {
	return &tokenStatsStore{db: db, botID: strings.TrimSpace(botID)}
}

func (s *tokenStatsStore) available() bool {
	return s != nil && s.db != nil
}

// withBot 返回带 bot_id 过滤的基础查询。
//
// 过渡期兼容：历史 llm_token_usage_records.bot_id 默认为 ”，回刷前列表 / 趋势
// 会全空。这里把 `bot_id = self` 与 `bot_id = ”` 一起放行，保证旧数据仍可见；
// 回刷脚本跑完之后空字符串记录消失，自然就只剩当前 bot 的精确数据。
// 单进程下其他 bot 的写入也不会进入本表的"未归属"段，因此这种放行是安全的。
func (s *tokenStatsStore) withBot(ctx context.Context) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&model.LlmTokenUsageRecord{})
	if s.botID != "" {
		q = q.Where("bot_id = ? OR bot_id = ''", s.botID)
	}
	return q
}

// base 返回带 bot_id + chat_id + 时间过滤的基础查询。
func (s *tokenStatsStore) base(ctx context.Context, chatID string, since time.Time) *gorm.DB {
	return s.withBot(ctx).
		Where("chat_id = ?", chatID).
		Where("created_at >= ?", since)
}

type aggRow struct {
	Group             string
	Requests          int64
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	ToolCalls         int64
	TurnsWithTools    int64
	ToolSuccesses     int64
	ToolErrors        int64
	ToolRelatedTokens int64
}

func (s *tokenStatsStore) total(ctx context.Context, chatID string, since time.Time) (TokenTotals, error) {
	var row aggRow
	err := s.base(ctx, chatID, since).
		Select("COUNT(*) AS requests, " +
			"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens, " +
			"COALESCE(SUM(total_tokens),0) AS total_tokens, " +
			"COALESCE(SUM(tool_call_count),0) AS tool_calls, " +
			"COALESCE(SUM(CASE WHEN tool_call_count > 0 THEN 1 ELSE 0 END),0) AS turns_with_tools, " +
			"COALESCE(SUM(tool_success_count),0) AS tool_successes, " +
			"COALESCE(SUM(tool_error_count),0) AS tool_errors, " +
			"COALESCE(SUM(CASE WHEN tool_call_count > 0 THEN total_tokens ELSE 0 END),0) AS tool_related_tokens").
		Scan(&row).Error
	if err != nil {
		return TokenTotals{}, err
	}
	return TokenTotals{
		Requests:          row.Requests,
		PromptTokens:      row.PromptTokens,
		CompletionTokens:  row.CompletionTokens,
		TotalTokens:       row.TotalTokens,
		ToolCalls:         row.ToolCalls,
		TurnsWithTools:    row.TurnsWithTools,
		ToolSuccesses:     row.ToolSuccesses,
		ToolErrors:        row.ToolErrors,
		ToolRelatedTokens: row.ToolRelatedTokens,
	}, nil
}

var tokenGroupColumns = map[string]struct{}{
	"business_scene": {}, "business_operation": {}, "attribution_mode": {},
	"model": {}, "kind": {}, "source_type": {}, "source": {}, "status": {},
}

// groupBy 按指定列分组聚合 token 用量。column 必须属于固定白名单。
func (s *tokenStatsStore) groupBy(ctx context.Context, chatID string, since time.Time, column string) ([]TokenGroupCount, error) {
	if _, ok := tokenGroupColumns[column]; !ok {
		return nil, fmt.Errorf("unsupported token group column %q", column)
	}
	var rows []aggRow
	err := s.base(ctx, chatID, since).
		Select(column + " AS \"group\", " +
			"COUNT(*) AS requests, " +
			"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens, " +
			"COALESCE(SUM(total_tokens),0) AS total_tokens, " +
			"COALESCE(SUM(tool_call_count),0) AS tool_calls, " +
			"COALESCE(SUM(CASE WHEN tool_call_count > 0 THEN 1 ELSE 0 END),0) AS turns_with_tools, " +
			"COALESCE(SUM(CASE WHEN tool_call_count > 0 THEN total_tokens ELSE 0 END),0) AS tool_related_tokens").
		Group(column).
		Order("total_tokens DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]TokenGroupCount, 0, len(rows))
	for _, r := range rows {
		group := r.Group
		if strings.TrimSpace(group) == "" {
			group = "unknown"
		}
		out = append(out, TokenGroupCount{
			Group:             group,
			Requests:          r.Requests,
			PromptTokens:      r.PromptTokens,
			CompletionTokens:  r.CompletionTokens,
			TotalTokens:       r.TotalTokens,
			ToolCalls:         r.ToolCalls,
			TurnsWithTools:    r.TurnsWithTools,
			ToolRelatedTokens: r.ToolRelatedTokens,
		})
	}
	return out, nil
}

func (s *tokenStatsStore) toolBase(ctx context.Context, chatID string, since time.Time) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&model.LlmToolCallRecord{}).
		Where("chat_id = ?", chatID).
		Where("called_at >= ?", since)
	if s.botID != "" {
		q = q.Where("bot_id = ? OR bot_id = ''", s.botID)
	}
	return q
}

type toolAggRow struct {
	Group             string
	Calls             int64
	Successes         int64
	Errors            int64
	AverageDurationMs float64
	P95DurationMs     float64
}

func (s *tokenStatsStore) toolSummary(ctx context.Context, chatID string, since time.Time, totals TokenTotals) (ToolSummary, error) {
	var row toolAggRow
	err := s.toolBase(ctx, chatID, since).
		Select("COUNT(*) AS calls, " +
			"COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS successes, " +
			"COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END),0) AS errors, " +
			"COALESCE(AVG(duration_ms),0) AS average_duration_ms, " +
			"COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms),0) AS p95_duration_ms").
		Scan(&row).Error
	if err != nil {
		return ToolSummary{}, err
	}
	return ToolSummary{
		Calls: row.Calls, TurnsWithTools: totals.TurnsWithTools,
		Successes: row.Successes, Errors: row.Errors,
		SuccessRate:       successRate(row.Successes, row.Calls),
		AverageDurationMs: row.AverageDurationMs, P95DurationMs: row.P95DurationMs,
		ToolRelatedTokens: totals.ToolRelatedTokens,
	}, nil
}

func (s *tokenStatsStore) byTool(ctx context.Context, chatID string, since time.Time) ([]ToolGroupCount, error) {
	var rows []toolAggRow
	err := s.toolBase(ctx, chatID, since).
		Select("tool_name AS \"group\", COUNT(*) AS calls, " +
			"COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS successes, " +
			"COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END),0) AS errors, " +
			"COALESCE(AVG(duration_ms),0) AS average_duration_ms, " +
			"COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms),0) AS p95_duration_ms").
		Group("tool_name").
		Order("calls DESC, tool_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]ToolGroupCount, 0, len(rows))
	for _, row := range rows {
		group := strings.TrimSpace(row.Group)
		if group == "" {
			group = "unknown"
		}
		out = append(out, ToolGroupCount{
			Group: group, Calls: row.Calls, Successes: row.Successes, Errors: row.Errors,
			SuccessRate:       successRate(row.Successes, row.Calls),
			AverageDurationMs: row.AverageDurationMs, P95DurationMs: row.P95DurationMs,
		})
	}
	return out, nil
}

func successRate(successes, calls int64) float64 {
	if calls <= 0 {
		return 0
	}
	return float64(successes) / float64(calls)
}

type dailyRow struct {
	Day         time.Time
	Requests    int64
	TotalTokens int64
}

func (s *tokenStatsStore) byDay(ctx context.Context, chatID string, since time.Time) ([]TokenDailyPoint, error) {
	var rows []dailyRow
	err := s.base(ctx, chatID, since).
		Select("bucket_day AS day, " +
			"COUNT(*) AS requests, " +
			"COALESCE(SUM(total_tokens),0) AS total_tokens").
		Group("bucket_day").
		Order("bucket_day ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]TokenDailyPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, TokenDailyPoint{
			Day:         r.Day.Format("2006-01-02"),
			Requests:    r.Requests,
			TotalTokens: r.TotalTokens,
		})
	}
	return out, nil
}

// collect 汇总所有 token 维度的聚合结果。
func (s *tokenStatsStore) collect(ctx context.Context, chatID string, since time.Time, windowDays int) (TokenStats, error) {
	stats := TokenStats{WindowDays: windowDays}
	total, err := s.total(ctx, chatID, since)
	if err != nil {
		return stats, err
	}
	stats.Total = total
	if stats.ByBusinessScene, err = s.groupBy(ctx, chatID, since, "business_scene"); err != nil {
		return stats, err
	}
	if stats.ByBusinessOperation, err = s.groupBy(ctx, chatID, since, "business_operation"); err != nil {
		return stats, err
	}
	if stats.ByAttributionMode, err = s.groupBy(ctx, chatID, since, "attribution_mode"); err != nil {
		return stats, err
	}
	if stats.ByModel, err = s.groupBy(ctx, chatID, since, "model"); err != nil {
		return stats, err
	}
	if stats.ByKind, err = s.groupBy(ctx, chatID, since, "kind"); err != nil {
		return stats, err
	}
	if stats.BySource, err = s.groupBy(ctx, chatID, since, "source_type"); err != nil {
		return stats, err
	}
	if stats.ByRawSource, err = s.groupBy(ctx, chatID, since, "source"); err != nil {
		return stats, err
	}
	if stats.ByStatus, err = s.groupBy(ctx, chatID, since, "status"); err != nil {
		return stats, err
	}
	if stats.ByDay, err = s.byDay(ctx, chatID, since); err != nil {
		return stats, err
	}
	if stats.ToolSummary, err = s.toolSummary(ctx, chatID, since, stats.Total); err != nil {
		return stats, err
	}
	if stats.ByTool, err = s.byTool(ctx, chatID, since); err != nil {
		return stats, err
	}
	return stats, nil
}

// chatTokenTotal 是某个 chat 在窗口内的 token 总量与名称（用于发现单聊 chat）。
type chatTokenTotal struct {
	ChatID      string
	ChatName    string
	TotalTokens int64
}

// totalsByChat 一次性按 chat_id 聚合窗口内当前 bot 全部群/单聊的 token 总量。
//
// 这是给列表页指标排序用的批量查询：用单条 GROUP BY chat_id 取代逐群查询，
// 顺带返回 chat_name，便于补全 Lark 群列表里取不到的单聊（p2p）。
// 通过 bot_id 过滤把当前 bot 的数据与其他 bot 隔离。
func (s *tokenStatsStore) totalsByChat(ctx context.Context, since time.Time) (map[string]chatTokenTotal, error) {
	type row struct {
		ChatID      string
		ChatName    string
		TotalTokens int64
	}
	var rows []row
	err := s.withBot(ctx).
		Where("created_at >= ?", since).
		Where("chat_id <> ''").
		Select("chat_id, MAX(chat_name) AS chat_name, COALESCE(SUM(total_tokens),0) AS total_tokens").
		Group("chat_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]chatTokenTotal, len(rows))
	for _, r := range rows {
		id := strings.TrimSpace(r.ChatID)
		if id == "" {
			continue
		}
		out[id] = chatTokenTotal{ChatID: id, ChatName: strings.TrimSpace(r.ChatName), TotalTokens: r.TotalTokens}
	}
	return out, nil
}
