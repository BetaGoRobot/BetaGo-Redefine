// API 返回类型定义，与后端 internal/interfaces/webui/types.go 一一对应。

export interface HealthResponse {
  ok: boolean
  auth: boolean
  timestamp: number
  robot_name: string
  instance?: string
}

export interface ChatMetrics {
  window_days: number
  recent_messages: number
  member_count: number
  total_tokens: number
  tokens_per_member: number
  tokens_per_message: number
}

export type Membership = 'active' | 'left' | 'unknown'

export interface ChatSummary {
  chat_id: string
  name: string
  avatar: string
  description: string
  chat_status: string
  external: boolean
  tenant_key?: string
  /** 当前 bot 与该会话的关系：active=在群 / left=已离开 / unknown=无法确认。 */
  membership?: Membership
  metrics?: ChatMetrics
}

export interface ChatMember {
  open_id: string
  name: string
  avatar?: string
  tenant_key?: string
}

export interface ChatDetail extends ChatSummary {
  owner_id?: string
  owner_name?: string
  owner_avatar?: string
  chat_mode?: string
  member_count: number
}

export interface FeatureView {
  name: string
  description: string
  category: string
  default_enabled: boolean
  enabled: boolean
}

export interface ConfigEnumOption {
  text: string
  value: string
}

export interface ConfigView {
  key: string
  description: string
  value_type: string
  value: string
  int_min?: number
  int_max?: number
  read_only: boolean
  allow_custom: boolean
  enum_options?: ConfigEnumOption[]
}

export interface TokenTotals {
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export interface TokenGroupCount {
  group: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export interface TokenDailyPoint {
  day: string
  requests: number
  total_tokens: number
}

export interface TokenStats {
  window_days: number
  total: TokenTotals
  by_model: TokenGroupCount[]
  by_kind: TokenGroupCount[]
  by_source_type: TokenGroupCount[]
  by_status: TokenGroupCount[]
  by_day: TokenDailyPoint[]
}

export interface MessageStats {
  window_days: number
  available: boolean
  recent_count: number
  unavailable_reason?: string
}

export interface StatsResponse {
  chat_id: string
  token: TokenStats
  messages: MessageStats
}

export interface ListResponse<T> {
  items: T[]
  total: number
}

export interface HourOfWeekBucket {
  dow: number
  hour: number
  count: number
}

export interface ChatActivity {
  window_days: number
  total: number
  hour_of_week: HourOfWeekBucket[]
}

export interface KeywordCount {
  word: string
  count: number
}

export interface ChatKeywords {
  window_days: number
  items: KeywordCount[]
}

export interface CommandCount {
  command: string
  count: number
}

export interface ChatCommands {
  window_days: number
  total: number
  items: CommandCount[]
}

export interface SenderRank {
  open_id: string
  user_name: string
  count: number
}

export interface ChatTopSenders {
  window_days: number
  total: number
  items: SenderRank[]
}

export interface MessageKindCount {
  kind: string
  count: number
}

export interface ChatMessageKinds {
  window_days: number
  total: number
  items: MessageKindCount[]
}

export interface ChatCommandTrend {
  window_days: number
  days: string[]
  total: number[]
  commands: number[]
}

export interface MentionRank {
  open_id: string
  user_name: string
  count: number
}

export interface ChatTopMentions {
  window_days: number
  sampled: number
  truncated: boolean
  items: MentionRank[]
}

export interface TopicTrendSeries {
  tag: string
  values: number[]
}

export interface ChatTopicTrend {
  window_days: number
  days: string[]
  series: TopicTrendSeries[]
}
