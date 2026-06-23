import axios from 'axios'
import type {
  ChatDetail,
  ChatMember,
  ChatSummary,
  ConfigView,
  FeatureView,
  ListResponse,
  StatsResponse,
} from './types'

const TOKEN_KEY = 'betago_webui_token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string) {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

// 后端基础地址：VITE_API_BASE 为空时走 vite dev proxy（相对路径 /api）。
const http = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '',
})

// 写操作自动附带 Bearer Token。
http.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers = config.headers || {}
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

export const api = {
  listChats(opts?: { metrics?: boolean; window?: string }) {
    const params: Record<string, string> = {}
    if (opts?.metrics) params.metrics = '1'
    if (opts?.window) params.window = opts.window
    return http.get<ListResponse<ChatSummary>>('/api/chats', { params }).then((r) => r.data)
  },
  getChat(chatID: string) {
    return http.get<ChatDetail>(`/api/chats/${encodeURIComponent(chatID)}`).then((r) => r.data)
  },
  listMembers(chatID: string) {
    return http
      .get<ListResponse<ChatMember>>(`/api/chats/${encodeURIComponent(chatID)}/members`)
      .then((r) => r.data)
  },
  getStats(chatID: string, window = '7d') {
    return http
      .get<StatsResponse>(`/api/chats/${encodeURIComponent(chatID)}/stats`, { params: { window } })
      .then((r) => r.data)
  },
  listFeatures(chatID: string) {
    return http
      .get<ListResponse<FeatureView>>(`/api/chats/${encodeURIComponent(chatID)}/features`)
      .then((r) => r.data)
  },
  setFeature(chatID: string, name: string, enabled: boolean) {
    return http
      .put(`/api/chats/${encodeURIComponent(chatID)}/features/${encodeURIComponent(name)}`, {
        enabled,
      })
      .then((r) => r.data)
  },
  listConfigs(chatID: string) {
    return http
      .get<ListResponse<ConfigView>>(`/api/chats/${encodeURIComponent(chatID)}/configs`)
      .then((r) => r.data)
  },
  setConfig(chatID: string, key: string, value: string) {
    return http
      .put(`/api/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`, { value })
      .then((r) => r.data)
  },
  deleteConfig(chatID: string, key: string) {
    return http
      .delete(`/api/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`)
      .then((r) => r.data)
  },
}
