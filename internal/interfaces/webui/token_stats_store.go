package webui

import (
	"context"
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

// withBot 返回带 bot_id 过滤的基础查询。bot_id 为空时不加过滤（用于回刷脚本场景）。
func (s *tokenStatsStore) withBot(ctx context.Context) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&model.LlmTokenUsageRecord{})
	if s.botID != "" {
		q = q.Where("bot_id = ?", s.botID)
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
	Group            string
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

func (s *tokenStatsStore) total(ctx context.Context, chatID string, since time.Time) (TokenTotals, error) {
	var row aggRow
	err := s.base(ctx, chatID, since).
		Select("COUNT(*) AS requests, " +
			"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens, " +
			"COALESCE(SUM(total_tokens),0) AS total_tokens").
		Scan(&row).Error
	if err != nil {
		return TokenTotals{}, err
	}
	return TokenTotals{
		Requests:         row.Requests,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
	}, nil
}

// groupBy 按指定列分组聚合 token 用量。column 必须是受控的列名常量。
func (s *tokenStatsStore) groupBy(ctx context.Context, chatID string, since time.Time, column string) ([]TokenGroupCount, error) {
	var rows []aggRow
	err := s.base(ctx, chatID, since).
		Select(column+" AS \"group\", "+
			"COUNT(*) AS requests, "+
			"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, "+
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens, "+
			"COALESCE(SUM(total_tokens),0) AS total_tokens").
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
			Group:            group,
			Requests:         r.Requests,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			TotalTokens:      r.TotalTokens,
		})
	}
	return out, nil
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
	if stats.ByModel, err = s.groupBy(ctx, chatID, since, "model"); err != nil {
		return stats, err
	}
	if stats.ByKind, err = s.groupBy(ctx, chatID, since, "kind"); err != nil {
		return stats, err
	}
	if stats.BySource, err = s.groupBy(ctx, chatID, since, "source_type"); err != nil {
		return stats, err
	}
	if stats.ByStatus, err = s.groupBy(ctx, chatID, since, "status"); err != nil {
		return stats, err
	}
	if stats.ByDay, err = s.byDay(ctx, chatID, since); err != nil {
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
