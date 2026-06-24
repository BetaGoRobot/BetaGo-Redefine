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

	// Membership 标记当前 bot 与该会话的关系：
	//   - "active":  机器人当前仍在该群 / 单聊默认视为 active；
	//   - "left":    历史出现过 token 记录但已不在 Chat.List 里（被踢 / 退群 / 群解散）；
	//   - "unknown": 未能从可信来源（Chat.List / 单聊前缀）确认。
	// 前端按此标记展示是否仍可发消息、并支持过滤“仅看在群”。
	Membership string `json:"membership,omitempty"`

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
	OwnerName   string `json:"owner_name,omitempty"`
	OwnerAvatar string `json:"owner_avatar,omitempty"`
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
	Avatar string `json:"avatar,omitempty"`
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

// ChatActivityFunc 返回某群在 since 之后按"周内小时"聚合的发言量。
//
// 默认实现走 OpenSearch：date_histogram 按小时桶聚合，再在客户端把
// 每个 UTC+8 时间点映射成 (周几, 小时) 维度，便于前端画热力图。返回
// nil（或函数为空）表示能力不可用，handler 会返回 503。
type ChatActivityFunc func(ctx context.Context, chatID string, since time.Time) (*ChatActivity, error)

// ChatKeywordsFunc 返回某群在 since 之后按词频排序的 Top 关键词。
// 默认实现走 OpenSearch nested + terms 聚合 raw_message_jieba_tag，
// 仅保留实词 (n* / v* / a* / i / l) 且词长 >1。topN 由调用方决定。
type ChatKeywordsFunc func(ctx context.Context, chatID string, since time.Time, topN int) (*ChatKeywords, error)

// ChatCommandsFunc 返回某群在 since 之后按命令使用次数排序的 Top 命令。
// 默认实现走 OpenSearch：bool filter is_command=true，再 terms 聚合 main_command。
// 同时回传命令调用总次数与首位命令占比，便于前端展示概览。
type ChatCommandsFunc func(ctx context.Context, chatID string, since time.Time, topN int) (*ChatCommands, error)

// ChatTopSendersFunc 返回某群在 since 之后按发言数排序的 Top 用户。
// 默认实现走 OpenSearch：filter chat_id + range，terms 聚合 user_id，
// 桶内子聚合 top_hits 取一条 user_name 作展示名。
type ChatTopSendersFunc func(ctx context.Context, chatID string, since time.Time, topN int) (*ChatTopSenders, error)

// ChatMessageKindsFunc 返回某群在 since 之后按消息类型 (message_type) 聚合
// 的分布。默认实现走 OpenSearch terms agg；空字符串桶折叠成 unknown。
type ChatMessageKindsFunc func(ctx context.Context, chatID string, since time.Time) (*ChatMessageKinds, error)

// ChatCommandTrendFunc 返回某群在 since 之后按"日"聚合的总消息数与命令调用数。
// 同一时间桶内回传 (total, command_count)，便于前端画双线/面积图，同时计算
// 命令占比趋势。
type ChatCommandTrendFunc func(ctx context.Context, chatID string, since time.Time) (*ChatCommandTrend, error)

// ChatTopMentionsFunc 返回某群在 since 之后被 @ 频次最高的用户。
//
// mentions 字段是 JSON 字符串塞在 text 列里，OpenSearch 无法直接 agg；
// 默认实现采用 search 取样：拉一批 mentions 非空的消息，客户端解析后
// 按 open_id 聚合。sampleSize 决定窗口内最多扫多少条样本。
type ChatTopMentionsFunc func(ctx context.Context, chatID string, since time.Time, sampleSize int, topN int) (*ChatTopMentions, error)

// ChatTopicTrendFunc 返回某群在 since 之后按"日"切片的词性主题趋势。
//
// 默认实现走 OpenSearch：date_histogram 按天聚合，nested 进 jieba_tag，
// 再 terms 按词性 tag 取桶；后端把细分词性折叠为名词/动词/形容词/其它四大类，
// 前端直接画堆叠面积图。
type ChatTopicTrendFunc func(ctx context.Context, chatID string, since time.Time) (*ChatTopicTrend, error)

// ChatActivity 是发言活跃度热力图的聚合结果。
type ChatActivity struct {
	WindowDays  int                  `json:"window_days"`
	Total       int64                `json:"total"`
	HourOfWeek  []HourOfWeekBucket   `json:"hour_of_week"`
}

// HourOfWeekBucket 描述「周 N · 第 H 小时」一格的发言量。
// DayOfWeek 取值 0..6，0 表示周一（与前端展示顺序一致）。
type HourOfWeekBucket struct {
	DayOfWeek int   `json:"dow"`
	Hour      int   `json:"hour"`
	Count     int64 `json:"count"`
}

// ChatKeywords 是关键词云聚合结果。
type ChatKeywords struct {
	WindowDays int              `json:"window_days"`
	Items      []KeywordCount   `json:"items"`
}

// KeywordCount 描述一个词在窗口内出现的文档数（一条消息只算一次）。
type KeywordCount struct {
	Word  string `json:"word"`
	Count int64  `json:"count"`
}

// ChatCommands 是命令使用情况聚合结果。
//
// Total 是窗口内所有 is_command=true 的消息数；Items 按 main_command 维度
// 聚合排序后的 Top topN，"unknown" 命令会折叠为空字符串桶并与 main_command
// 缺失的消息一起合入。
type ChatCommands struct {
	WindowDays int            `json:"window_days"`
	Total      int64          `json:"total"`
	Items      []CommandCount `json:"items"`
}

// CommandCount 描述一个命令在窗口内被调用的次数。
type CommandCount struct {
	Command string `json:"command"`
	Count   int64  `json:"count"`
}

// ChatTopSenders 是发言量排行榜聚合结果。Total 为窗口内总消息数（含未上榜
// 的尾巴），便于前端展示 Top 用户的占比。
type ChatTopSenders struct {
	WindowDays int          `json:"window_days"`
	Total      int64        `json:"total"`
	Items      []SenderRank `json:"items"`
}

// SenderRank 描述一个用户在窗口内的发言数。OpenID 是聚合用的稳定 key；
// UserName 取桶内任意一条样本，缺失时回退 "你"（机器人自身）或 OpenID。
type SenderRank struct {
	OpenID   string `json:"open_id"`
	UserName string `json:"user_name"`
	Count    int64  `json:"count"`
}

// ChatMessageKinds 是消息类型分布聚合结果。Total 是窗口内消息总数，
// Items 是按类型聚合的 (message_type, count)，count 之和等于 Total。
type ChatMessageKinds struct {
	WindowDays int                `json:"window_days"`
	Total      int64              `json:"total"`
	Items      []MessageKindCount `json:"items"`
}

// MessageKindCount 描述一个消息类型 (text / image / file / post / audio / ...)
// 在窗口内出现的次数。空字符串会被归并为 "unknown"。
type MessageKindCount struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// ChatCommandTrend 是命令调用与总消息数的每日时序对比。
//
// Days 长度等于桶数，按时间升序；与 Total / Commands 数组下标一一对应，
// 前端无需解析时间字符串重新对齐。
type ChatCommandTrend struct {
	WindowDays int      `json:"window_days"`
	Days       []string `json:"days"`
	Total      []int64  `json:"total"`
	Commands   []int64  `json:"commands"`
}

// ChatTopMentions 是 @ 互动榜聚合结果。Sampled 为本次实际扫描的消息数，
// Truncated 表示是否还有未覆盖到的样本（窗口内 mentions 非空消息数 > sampleSize）。
type ChatTopMentions struct {
	WindowDays int             `json:"window_days"`
	Sampled    int64           `json:"sampled"`
	Truncated  bool            `json:"truncated"`
	Items      []MentionRank   `json:"items"`
}

// MentionRank 描述一个用户在窗口样本中被 @ 的次数。
// OpenID 是稳定 key（飞书 mention.id.open_id）；UserName 取样本里第一个非空 name；
// 缺失时回退 OpenID。同一条消息里多次 @ 同一人按多次计入。
type MentionRank struct {
	OpenID   string `json:"open_id"`
	UserName string `json:"user_name"`
	Count    int64  `json:"count"`
}

// ChatTopicTrend 是按"日"切片的词性主题趋势。
//
// Days 长度等于桶数（按时间升序）；Series 每条对应一个词性大类，
// values 数组下标与 Days 对齐。前端直接堆叠面积图渲染。
type ChatTopicTrend struct {
	WindowDays int                 `json:"window_days"`
	Days       []string            `json:"days"`
	Series     []TopicTrendSeries  `json:"series"`
}

// TopicTrendSeries 是一个词性大类（名词 / 动词 / 形容词 / 其它）的每日词频。
// Tag 用前端友好的中文标签；Values 长度与外层 Days 对齐。
type TopicTrendSeries struct {
	Tag    string  `json:"tag"`
	Values []int64 `json:"values"`
}

// RecentChatIDsFunc 返回当前 bot 在 since 之后实际产生过消息的 chat_id 集合，
// 用作列表页对 token 表里浮现的额外 chat 做 bot 维度过滤的白名单。
// 默认实现走 OpenSearch（按当前 bot 进程独占的 lark_msg_index 聚合 chat_id），
// 因此天然按 bot 隔离。返回 nil 表示能力不可用，调用方退回到不过滤的旧行为。
type RecentChatIDsFunc func(ctx context.Context, since time.Time) (map[string]struct{}, error)

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
