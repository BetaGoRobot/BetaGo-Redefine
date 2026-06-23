// API 返回类型定义，与后端 internal/interfaces/webui/types.go 一一对应。

export interface ChatSummary {
  chat_id: string
  name: string
  avatar: string
  description: string
  chat_status: string
  external: boolean
  tenant_key?: string
}

export interface ChatDetail extends ChatSummary {
  owner_id?: string
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
