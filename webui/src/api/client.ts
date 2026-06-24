import axios, { AxiosInstance } from 'axios'
import type {
  ChatActivity,
  ChatCommands,
  ChatDetail,
  ChatKeywords,
  ChatMember,
  ChatSummary,
  ConfigView,
  FeatureView,
  HealthResponse,
  ListResponse,
  StatsResponse,
} from './types'
import type { BotInstance } from '../stores/filter'

/** 构造一个绑定到某个 bot 实例的 axios 客户端。 */
export function createBotClient(bot: Pick<BotInstance, 'baseURL' | 'token'>): AxiosInstance {
  const http = axios.create({
    baseURL: bot.baseURL || (import.meta.env.VITE_API_BASE as string) || '',
    timeout: 30_000,
  })
  http.interceptors.request.use((config) => {
    const token = bot.token
    if (token) {
      config.headers = config.headers || {}
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  })
  return http
}

// 为了兼容"单一默认 token"入口，保留 getToken/setToken（绑定到 default-local bot）。
export const DEFAULT_BOT_ID = 'default-local'

export function getToken(): string {
  // 从 store 中取 default-local；此处做轻量 fallback 读 localStorage 兼容旧调用方。
  try {
    const raw = localStorage.getItem('betago_webui_bots_v1')
    if (raw) {
      const bots = JSON.parse(raw) as BotInstance[]
      return bots.find((b) => b.id === DEFAULT_BOT_ID)?.token || ''
    }
  } catch { /* ignore */ }
  return localStorage.getItem('betago_webui_token') || ''
}

export function setToken(token: string) {
  // 同步更新到 store 中的 default-local bot；首次不存在时创建。
  try {
    const raw = localStorage.getItem('betago_webui_bots_v1')
    const bots: BotInstance[] = raw ? JSON.parse(raw) : []
    const idx = bots.findIndex((b) => b.id === DEFAULT_BOT_ID)
    if (idx >= 0) {
      bots[idx].token = token || undefined
    } else {
      bots.unshift({
        id: DEFAULT_BOT_ID,
        name: '默认本地',
        baseURL: '',
        token: token || undefined,
        color: '#5B8FF9',
      })
    }
    localStorage.setItem('betago_webui_bots_v1', JSON.stringify(bots))
  } catch { /* ignore */ }
  // 兼容老键
  if (token) localStorage.setItem('betago_webui_token', token)
  else localStorage.removeItem('betago_webui_token')
}

/** 单个 bot 的 API 调用集合。所有方法返回带 bot 标识的结果。 */
export class BotApi {
  readonly http: AxiosInstance
  readonly bot: BotInstance

  constructor(bot: BotInstance) {
    this.bot = bot
    this.http = createBotClient(bot)
  }

  async health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/api/health').then((r) => r.data)
  }
  async listChats(opts?: { metrics?: boolean; window?: string }) {
    const params: Record<string, string> = {}
    if (opts?.metrics) params.metrics = '1'
    if (opts?.window) params.window = opts.window
    return this.http
      .get<ListResponse<ChatSummary>>('/api/chats', { params })
      .then((r) => r.data)
  }
  async getChat(chatID: string) {
    return this.http
      .get<ChatDetail>(`/api/chats/${encodeURIComponent(chatID)}`)
      .then((r) => r.data)
  }
  async listMembers(chatID: string) {
    return this.http
      .get<ListResponse<ChatMember>>(`/api/chats/${encodeURIComponent(chatID)}/members`)
      .then((r) => r.data)
  }
  async getStats(chatID: string, window = '7d') {
    return this.http
      .get<StatsResponse>(`/api/chats/${encodeURIComponent(chatID)}/stats`, { params: { window } })
      .then((r) => r.data)
  }
  async getActivity(chatID: string, window = '7d') {
    return this.http
      .get<ChatActivity>(`/api/chats/${encodeURIComponent(chatID)}/insights/activity`, { params: { window } })
      .then((r) => r.data)
  }
  async getKeywords(chatID: string, window = '7d', top = 80) {
    return this.http
      .get<ChatKeywords>(`/api/chats/${encodeURIComponent(chatID)}/insights/keywords`, {
        params: { window, top },
      })
      .then((r) => r.data)
  }
  async getCommands(chatID: string, window = '7d', top = 20) {
    return this.http
      .get<ChatCommands>(`/api/chats/${encodeURIComponent(chatID)}/insights/commands`, {
        params: { window, top },
      })
      .then((r) => r.data)
  }
  async listFeatures(chatID: string) {
    return this.http
      .get<ListResponse<FeatureView>>(`/api/chats/${encodeURIComponent(chatID)}/features`)
      .then((r) => r.data)
  }
  async setFeature(chatID: string, name: string, enabled: boolean) {
    return this.http
      .put(`/api/chats/${encodeURIComponent(chatID)}/features/${encodeURIComponent(name)}`, { enabled })
      .then((r) => r.data)
  }
  async listConfigs(chatID: string) {
    return this.http
      .get<ListResponse<ConfigView>>(`/api/chats/${encodeURIComponent(chatID)}/configs`)
      .then((r) => r.data)
  }
  async setConfig(chatID: string, key: string, value: string) {
    return this.http
      .put(`/api/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`, { value })
      .then((r) => r.data)
  }
  async deleteConfig(chatID: string, key: string) {
    return this.http
      .delete(`/api/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`)
      .then((r) => r.data)
  }
}

/** 给 BotApi 调用结果附加 bot_id 维度。 */
export type WithBot<T> = T & { bot_id: string; bot_name: string; bot_color?: string }

export function tagBot<T>(bot: BotInstance, data: T): WithBot<T> {
  return Object.assign({ bot_id: bot.id, bot_name: bot.robotName || bot.name, bot_color: bot.color }, data)
}

/**
 * 在多个 bot 上并行执行一次请求，返回成功结果数组。
 * 失败的 bot 会被静默丢弃（调用方可通过 store.bots[i].healthy 观察）。
 */
export async function aggregate<T>(
  bots: BotInstance[],
  run: (api: BotApi, bot: BotInstance) => Promise<T>,
  onError?: (bot: BotInstance, err: unknown) => void,
): Promise<WithBot<T>[]> {
  const results = await Promise.allSettled(
    bots.map(async (bot) => {
      const api = new BotApi(bot)
      const v = await run(api, bot)
      return tagBot(bot, v)
    }),
  )
  const out: WithBot<T>[] = []
  results.forEach((r, i) => {
    if (r.status === 'fulfilled') out.push(r.value)
    else if (onError) onError(bots[i], r.reason)
  })
  return out
}

/**
 * 旧的单 bot 默认客户端：保留作为简洁入口，
 * 绑定到 default-local bot，供 App.vue 等简单调用方使用。
 */
export const api = {
  listChats(opts?: { metrics?: boolean; window?: string }) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).listChats(opts)
  },
  getChat(chatID: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).getChat(chatID)
  },
  listMembers(chatID: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).listMembers(chatID)
  },
  getStats(chatID: string, window = '7d') {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).getStats(chatID, window)
  },
  listFeatures(chatID: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).listFeatures(chatID)
  },
  setFeature(chatID: string, name: string, enabled: boolean) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).setFeature(chatID, name, enabled)
  },
  listConfigs(chatID: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).listConfigs(chatID)
  },
  setConfig(chatID: string, key: string, value: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).setConfig(chatID, key, value)
  },
  deleteConfig(chatID: string, key: string) {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).deleteConfig(chatID, key)
  },
  health() {
    return new BotApi({ id: DEFAULT_BOT_ID, name: '默认', baseURL: '', color: '#5B8FF9' }).health()
  },
}
