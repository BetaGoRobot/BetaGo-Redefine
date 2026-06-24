import axios, { AxiosInstance } from 'axios'
import type {
  ChatActivity,
  ChatCommands,
  ChatCommandTrend,
  ChatDetail,
  ChatKeywords,
  ChatMember,
  ChatMessageKinds,
  ChatSummary,
  ChatTopMentions,
  ChatTopicTrend,
  ChatTopSenders,
  ConfigView,
  FeatureView,
  HealthResponse,
  ListResponse,
  StatsResponse,
} from './types'
import type { BotInstance } from '../stores/filter'

/**
 * 浏览器只跟 webui 容器同源通信。
 *
 * 每个 bot 的请求通过 `/bot/<id>/api/*` 同源前缀走，由 webui 容器内的 Caddy
 * 反代到对应 bot 的内网地址（见 script/webui/docker-entrypoint.sh）。
 *
 * 运行时配置 (window.__BETAGO_CONFIG__.apiBase) 或构建期 VITE_API_BASE 显式指定
 * 时会作为统一前缀拼到所有 bot 路径前；正常生产部署应留空。
 *
 * 不再支持"浏览器直连 bot 后端公网地址"——那种用法既不安全，也无法在多 bot
 * 场景下做去主从化聚合。
 */
function readApiBase(): string {
  const runtime =
    (typeof window !== 'undefined' ? window.__BETAGO_CONFIG__?.apiBase : undefined) || ''
  if (runtime) return runtime.replace(/\/+$/, '')
  const buildTime = (import.meta.env.VITE_API_BASE as string) || ''
  return buildTime.replace(/\/+$/, '')
}

function botProxyPath(botID: string): string {
  return `/bot/${encodeURIComponent(botID)}/api`
}

/** 构造一个绑定到某个 bot 实例的 axios 客户端。 */
export function createBotClient(bot: Pick<BotInstance, 'id' | 'token'>): AxiosInstance {
  const apiBase = readApiBase()
  const baseURL = `${apiBase}${botProxyPath(bot.id)}`
  const http = axios.create({
    baseURL,
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

/** 单个 bot 的 API 调用集合。所有方法返回带 bot 标识的结果。 */
export class BotApi {
  readonly http: AxiosInstance
  readonly bot: BotInstance

  constructor(bot: BotInstance) {
    this.bot = bot
    this.http = createBotClient(bot)
  }

  async health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/health').then((r) => r.data)
  }
  async listChats(opts?: { metrics?: boolean; window?: string }) {
    const params: Record<string, string> = {}
    if (opts?.metrics) params.metrics = '1'
    if (opts?.window) params.window = opts.window
    return this.http
      .get<ListResponse<ChatSummary>>('/chats', { params })
      .then((r) => r.data)
  }
  async getChat(chatID: string) {
    return this.http
      .get<ChatDetail>(`/chats/${encodeURIComponent(chatID)}`)
      .then((r) => r.data)
  }
  async listMembers(chatID: string) {
    return this.http
      .get<ListResponse<ChatMember>>(`/chats/${encodeURIComponent(chatID)}/members`)
      .then((r) => r.data)
  }
  async getStats(chatID: string, window = '7d') {
    return this.http
      .get<StatsResponse>(`/chats/${encodeURIComponent(chatID)}/stats`, { params: { window } })
      .then((r) => r.data)
  }
  async getActivity(chatID: string, window = '7d') {
    return this.http
      .get<ChatActivity>(`/chats/${encodeURIComponent(chatID)}/insights/activity`, { params: { window } })
      .then((r) => r.data)
  }
  async getKeywords(chatID: string, window = '7d', top = 80) {
    return this.http
      .get<ChatKeywords>(`/chats/${encodeURIComponent(chatID)}/insights/keywords`, {
        params: { window, top },
      })
      .then((r) => r.data)
  }
  async getCommands(chatID: string, window = '7d', top = 20) {
    return this.http
      .get<ChatCommands>(`/chats/${encodeURIComponent(chatID)}/insights/commands`, {
        params: { window, top },
      })
      .then((r) => r.data)
  }
  async getTopSenders(chatID: string, window = '7d', top = 20) {
    return this.http
      .get<ChatTopSenders>(`/chats/${encodeURIComponent(chatID)}/insights/top_senders`, {
        params: { window, top },
      })
      .then((r) => r.data)
  }
  async getMessageKinds(chatID: string, window = '7d') {
    return this.http
      .get<ChatMessageKinds>(`/chats/${encodeURIComponent(chatID)}/insights/message_kinds`, {
        params: { window },
      })
      .then((r) => r.data)
  }
  async getCommandTrend(chatID: string, window = '7d') {
    return this.http
      .get<ChatCommandTrend>(`/chats/${encodeURIComponent(chatID)}/insights/command_trend`, {
        params: { window },
      })
      .then((r) => r.data)
  }
  async getTopMentions(chatID: string, window = '7d', top = 20, sample = 500) {
    return this.http
      .get<ChatTopMentions>(`/chats/${encodeURIComponent(chatID)}/insights/top_mentions`, {
        params: { window, top, sample },
      })
      .then((r) => r.data)
  }
  async getTopicTrend(chatID: string, window = '7d') {
    return this.http
      .get<ChatTopicTrend>(`/chats/${encodeURIComponent(chatID)}/insights/topic_trend`, {
        params: { window },
      })
      .then((r) => r.data)
  }
  async listFeatures(chatID: string) {
    return this.http
      .get<ListResponse<FeatureView>>(`/chats/${encodeURIComponent(chatID)}/features`)
      .then((r) => r.data)
  }
  async setFeature(chatID: string, name: string, enabled: boolean) {
    return this.http
      .put(`/chats/${encodeURIComponent(chatID)}/features/${encodeURIComponent(name)}`, { enabled })
      .then((r) => r.data)
  }
  async listConfigs(chatID: string) {
    return this.http
      .get<ListResponse<ConfigView>>(`/chats/${encodeURIComponent(chatID)}/configs`)
      .then((r) => r.data)
  }
  async setConfig(chatID: string, key: string, value: string) {
    return this.http
      .put(`/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`, { value })
      .then((r) => r.data)
  }
  async deleteConfig(chatID: string, key: string) {
    return this.http
      .delete(`/chats/${encodeURIComponent(chatID)}/configs/${encodeURIComponent(key)}`)
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
