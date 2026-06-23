package webui

import (
	"context"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
)

// ConfigManager 抽象 application/config.Manager 中 WebUI 需要的能力。
//
// *config.Manager 已实现以下全部方法；抽象成接口仅为便于测试注入替身，
// 避免在单测里依赖真实数据库与全局单例。
type ConfigManager interface {
	GetAllFeatures() []appconfig.Feature
	IsFeatureEnabled(ctx context.Context, feature string, defaultEnabled bool, chatID, openID string) bool
	BlockFeature(ctx context.Context, feature string, scope appconfig.ConfigScope, chatID, openID, remark string) error
	UnblockFeature(ctx context.Context, feature string, scope appconfig.ConfigScope, chatID, openID string) error
	GetString(ctx context.Context, key appconfig.ConfigKey, chatID, openID string) string
	GetInt(ctx context.Context, key appconfig.ConfigKey, chatID, openID string) int
	GetBool(ctx context.Context, key appconfig.ConfigKey, chatID, openID string) bool
	SetString(ctx context.Context, key appconfig.ConfigKey, scope appconfig.ConfigScope, chatID, openID, value string) error
	DeleteConfig(ctx context.Context, key appconfig.ConfigKey, scope appconfig.ConfigScope, chatID, openID string) error
}

// ChatSummary 是群列表项，强调头像 ID/URL 等基础信息。
type ChatSummary struct {
	ChatID      string `json:"chat_id"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	Description string `json:"description"`
	ChatStatus  string `json:"chat_status"`
	External    bool   `json:"external"`
	Tenant      string `json:"tenant_key,omitempty"`

	// Metrics 仅在列表接口带 ?metrics=1 时填充，承载排序所需的派生指标。
	Metrics *ChatMetrics `json:"metrics,omitempty"`
}

// ChatMetrics 是群列表的可排序聚合指标。
//
// WindowDays 表示统计窗口；RecentMessages 为近 N 天发言量（依赖 OpenSearch，
// 不可用时为 0）；MemberCount 为群成员量；TotalTokens 为近 N 天 token 消耗总量；
// TokensPerMember / TokensPerMessage 为派生人均与单条均值（除数为 0 时取 0）。
type ChatMetrics struct {
	WindowDays       int     `json:"window_days"`
	RecentMessages   int     `json:"recent_messages"`
	MemberCount      int     `json:"member_count"`
	TotalTokens      int64   `json:"total_tokens"`
	TokensPerMember  float64 `json:"tokens_per_member"`
	TokensPerMessage float64 `json:"tokens_per_message"`
}

// ChatDetail 是单个群的详细信息。
type ChatDetail struct {
	ChatSummary
	OwnerID     string `json:"owner_id,omitempty"`
	ChatMode    string `json:"chat_mode,omitempty"`
	MemberCount int    `json:"member_count"`
}

// ChatService 抽象群列表/详情的获取来源（默认实现走 Lark OpenAPI）。
type ChatService interface {
	ListChats(ctx context.Context) ([]ChatSummary, error)
	GetChat(ctx context.Context, chatID string) (*ChatDetail, error)
}

// ChatMember 是群成员在 WebUI 中的展示项。
type ChatMember struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
	Tenant string `json:"tenant_key,omitempty"`
}

// MemberCountFunc 返回某群成员数；默认实现走 Lark OpenAPI（带缓存）。
// 为空表示该能力不可用，列表指标里的成员数会保持为 0。
type MemberCountFunc func(ctx context.Context, chatID string) (int, error)

// MemberListFunc 返回某群成员列表；默认实现复用 larkuser 的成员缓存。
// 为空表示该能力不可用，详情接口的成员列表会为空。
type MemberListFunc func(ctx context.Context, chatID string) ([]ChatMember, error)

// MessageStatsFunc 返回某群在 since 之后的消息数量；默认实现走 OpenSearch。
// 为空表示该能力不可用，统计接口会返回降级标记而非报错。
type MessageStatsFunc func(ctx context.Context, chatID string, since time.Time) (int, error)

// FeatureView 是功能开关在 WebUI 中的展示与状态。
type FeatureView struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	DefaultEnabled bool   `json:"default_enabled"`
	Enabled        bool   `json:"enabled"`
}

// ConfigEnumOptionView 描述枚举型配置的可选项。
type ConfigEnumOptionView struct {
	Text  string `json:"text"`
	Value string `json:"value"`
}

// ConfigView 是单条配置在 WebUI 中的展示与生效值。
type ConfigView struct {
	Key         string                 `json:"key"`
	Description string                 `json:"description"`
	ValueType   string                 `json:"value_type"`
	Value       string                 `json:"value"`
	IntMin      int                    `json:"int_min,omitempty"`
	IntMax      int                    `json:"int_max,omitempty"`
	ReadOnly    bool                   `json:"read_only"`
	AllowCustom bool                   `json:"allow_custom"`
	EnumOptions []ConfigEnumOptionView `json:"enum_options,omitempty"`
}

// TokenStats 是 token 消耗的多维聚合结果。
type TokenStats struct {
	WindowDays int                `json:"window_days"`
	Total      TokenTotals        `json:"total"`
	ByModel    []TokenGroupCount  `json:"by_model"`
	ByKind     []TokenGroupCount  `json:"by_kind"`
	BySource   []TokenGroupCount  `json:"by_source_type"`
	ByStatus   []TokenGroupCount  `json:"by_status"`
	ByDay      []TokenDailyPoint  `json:"by_day"`
}

// TokenTotals 是窗口内的总量汇总。
type TokenTotals struct {
	Requests         int64 `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// TokenGroupCount 是按某一维度分组的聚合项。
type TokenGroupCount struct {
	Group            string `json:"group"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

// TokenDailyPoint 是按天的时间序列点。
type TokenDailyPoint struct {
	Day         string `json:"day"`
	Requests    int64  `json:"requests"`
	TotalTokens int64  `json:"total_tokens"`
}

// MessageStats 是消息量统计（依赖 OpenSearch，可能降级）。
type MessageStats struct {
	WindowDays   int    `json:"window_days"`
	Available    bool   `json:"available"`
	RecentCount  int    `json:"recent_count"`
	Unavailable  string `json:"unavailable_reason,omitempty"`
}

// StatsResponse 是统计聚合接口的总返回。
type StatsResponse struct {
	ChatID   string       `json:"chat_id"`
	Token    TokenStats   `json:"token"`
	Messages MessageStats `json:"messages"`
}
