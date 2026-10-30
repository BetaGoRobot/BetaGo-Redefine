package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/presentation"
)

type schedulePolicyChecker interface {
	CheckToolPolicy(context.Context, string, string, bool, ...bool) error
}

func scheduleRenderer(registry *atoms.Registry, policies schedulePolicyChecker) func(context.Context, domain.ScheduleRun) (string, string, error) {
	return func(ctx context.Context, run domain.ScheduleRun) (string, string, error) {
		if registry == nil || policies == nil || run.Query == nil || !domain.ScheduleQueryAllowed(run.Query.Name) || run.Scope.Validate() != nil {
			return "", "", domain.ErrInvalid
		}
		for _, d := range registry.Definitions() {
			if d.Name != run.Query.Name {
				continue
			}
			if d.SideEffect {
				return "", "", domain.ErrUnauthorized
			}
			if err := policies.CheckToolPolicy(ctx, d.Name, d.Digest, d.SideEffect, d.AutoExecute); err != nil {
				return "", "", err
			}
			args, err := d.Validate(string(run.Query.Arguments))
			if err != nil {
				return "", "", err
			}
			in := atoms.Invocation{ActorID: run.Origin.AuthorID, Task: domain.Task{ID: domain.StableID(run.ScheduleID, fmt.Sprint(run.Revision), fmt.Sprint(run.Index)), Scope: run.Scope, Root: run.Origin, RequesterID: run.Origin.AuthorID}}
			if err = d.Authorize(ctx, in); err != nil {
				return "", "", err
			}
			raw, err := d.Run(ctx, in, args)
			if err != nil {
				return "", "", err
			}
			content, err := scheduleQueryCard(run.Query.Name, raw)
			return "interactive", content, err
		}
		return "", "", domain.ErrNotFound
	}
}

func scheduleQueryCard(name, raw string) (string, error) {
	if len(raw) > 256<<10 {
		return "", domain.ErrInvalid
	}
	var result struct {
		Available *bool `json:"available"`
		Records   []struct {
			Data map[string]any `json:"data"`
		} `json:"records"`
		Items     []map[string]any `json:"items"`
		Tracks    []map[string]any `json:"tracks"`
		Truncated bool             `json:"truncated"`
		Total     int              `json:"total"`
		Provider  string           `json:"provider"`
		Coverage  string           `json:"coverage"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if d.Decode(&result) != nil {
		return "", domain.ErrInvalid
	}
	if result.Available != nil && !*result.Available {
		return "", fmt.Errorf("scheduled data provider unavailable")
	}
	records := []map[string]any{}
	for _, record := range result.Records {
		records = append(records, record.Data)
	}
	records = append(records, result.Items...)
	records = append(records, result.Tracks...)
	titles := map[string]string{"gold_price_get": "定时金价", "stock_zh_a_get": "定时股票行情", "finance_market_data_get": "定时行情", "finance_news_get": "定时财经资讯", "economy_indicator_get": "定时经济指标", "stock_hot_search_get": "定时股票热搜", "oneword_get": "定时一言", "music_search": "定时音乐搜索", "music_song_detail": "歌曲详情", "music_album_detail": "专辑详情", "music_playlist_detail": "歌单详情"}
	title := titles[name]
	if title == "" {
		return "", domain.ErrInvalid
	}
	card := presentation.Card{Title: title}
	if len(records) == 0 {
		card.Sections = []presentation.Section{{Kind: "markdown", Text: "本次查询没有返回可展示的记录。"}}
	} else {
		keys := []string{}
		for k, v := range records[0] {
			if _, ok := scheduleCell(v); ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) > 6 {
			keys = keys[:6]
		}
		if len(keys) == 0 {
			return "", domain.ErrInvalid
		}
		columns := make([]string, len(keys))
		for i, k := range keys {
			columns[i] = scheduleDisplayText(k, 64)
		}
		rows := [][]string{}
		for i, record := range records {
			if i == 12 {
				break
			}
			row := make([]string, len(keys))
			for j, k := range keys {
				row[j], _ = scheduleCell(record[k])
			}
			rows = append(rows, row)
		}
		card.Sections = append(card.Sections, presentation.Section{Kind: "table", Columns: columns, Rows: rows})
		if len(records) > len(rows) || result.Truncated || result.Total > len(rows) {
			card.Sections = append(card.Sections, presentation.Section{Kind: "markdown", Text: fmt.Sprintf("本卡展示 %d 条记录；上游结果或展示已截断。", len(rows))})
		}
	}
	if result.Provider != "" {
		card.Sections = append(card.Sections, presentation.Section{Kind: "markdown", Text: "来源：" + scheduleDisplayText(result.Provider, 128)})
	}
	if result.Coverage != "" {
		card.Sections = append(card.Sections, presentation.Section{Kind: "markdown", Text: "覆盖：" + scheduleDisplayText(result.Coverage, 512)})
	}
	b, err := presentation.Compose(card)
	return string(b), err
}
func scheduleCell(value any) (string, bool) {
	var text string
	switch v := value.(type) {
	case nil:
		return "", true
	case string:
		text = v
	case json.Number:
		text = string(v)
	case bool:
		text = fmt.Sprint(v)
	case []any:
		var parts []string
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		text = strings.Join(parts, " / ")
	default:
		return "", false
	}
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
		return "", false
	}
	return scheduleDisplayText(text, 256), true
}
func scheduleDisplayText(text string, max int) string {
	text = strings.NewReplacer("<", "＜", ">", "＞", "](", "]（", "]:", "]：", "http://", "http：//", "https://", "https：//", "\x00", "", "\r", " ", "\n", " ").Replace(text)
	if len(text) > max {
		text = text[:max]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}
