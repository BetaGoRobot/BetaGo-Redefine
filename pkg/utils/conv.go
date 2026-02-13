package utils

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

func MustInt(s string) int64 {
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return i
}

func GenTraceURL(traceID string) string {
	url, err := GenerateTraceURL(traceID)
	if err != nil {
		logs.L().Error("GenerateTraceURL err", zap.Error(err))
	}
	return url
}

type PaneDetail struct {
	Datasource string    `json:"datasource"` // 外层的数据源 UID
	Queries    []Query   `json:"queries"`
	Range      TimeRange `json:"range"`
	Compact    bool      `json:"compact,omitempty"` // 可选
}

type DatasourceRef struct {
	Type string `json:"type"`
	Uid  string `json:"uid"`
}

type Query struct {
	RefId      string        `json:"refId"`
	Datasource DatasourceRef `json:"datasource"`
	Query      string        `json:"query"` // 这里存放 TraceID
}

type TimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func GenerateTraceURL(traceID string) (string, error) {
	baseURL := config.Get().OtelConfig.GrafanaURL
	dsUID := "1"

	pane := PaneDetail{
		Datasource: dsUID, Queries: []Query{
			{
				RefId: "A",
				Datasource: DatasourceRef{
					Type: "jaeger",
					Uid:  dsUID,
				},
				Query: traceID,
			},
		}, Range: TimeRange{
			From: "now-7d",
			To:   "now",
		}, Compact: false,
	}
	panesMap := map[string]PaneDetail{"traceView": pane}

	jsonBytes, err := sonic.Marshal(panesMap)
	if err != nil {
		return "", fmt.Errorf("JSON marshal error: %w", err)
	}
	params := url.Values{}
	params.Add("schemaVersion", "1")
	params.Add("orgId", "1")
	params.Add("panes", string(jsonBytes))
	return fmt.Sprintf("%s?%s", baseURL, params.Encode()), nil
}
