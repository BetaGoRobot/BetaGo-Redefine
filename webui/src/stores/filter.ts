import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

/** 单个 Bot 实例定义（一个 bot 对应一个后端 WebUI 地址）。 */
export interface BotInstance {
  /** 本地唯一 id，用于区分不同 bot；浏览器请求会走 `/bot/<id>/api/*` 同源前缀。 */
  id: string
  /** 展示名称 */
  name: string
  /** 后端 WebUI 内网地址，仅用于运维登记与 Caddy 容器内反代渲染。
   *  浏览器**不会**直接使用此字段——所有请求一律走 `/bot/<id>/api/*` 同源前缀，
   *  由 webui 容器内的 Caddy（见 script/webui/docker-entrypoint.sh）反代到这里。 */
  baseURL: string
  /** 写操作鉴权 token（空表示不鉴权，仅内网环境适用）。 */
  token?: string
  /** 备注，可选 */
  remark?: string
  /** 最近一次探活结果。健康接口返回 2xx 才是 true。 */
  healthy?: boolean
  /** 机器人标识（从 /api/health 读取），未知时为空字符串。 */
  robotName?: string
  /** Lark AppID 或部署实例名（从 /api/health 读取）。 */
  instance?: string
  /** 颜色标签，用于图表区分不同 bot。 */
  color?: string
  /** 来源：'localstorage'（用户自建）/ 'env'（来自 VITE_BOTS 预设，不可手动编辑删除） */
  source?: 'localstorage' | 'env'
}

const BOT_STORAGE_KEY = 'betago_webui_bots_v1'
const SELECTED_BOTS_KEY = 'betago_webui_selected_bots_v1'

const DEFAULT_PALETTE = [
  '#5B8FF9', '#5AD8A6', '#F6BD16', '#E86452', '#6DC8EC',
  '#945FB9', '#FF9845', '#1E9493', '#FF99C3', '#5D7092',
]

function loadUserBotsFromStorage(): BotInstance[] {
  try {
    const raw = localStorage.getItem(BOT_STORAGE_KEY)
    if (raw) return (JSON.parse(raw) as BotInstance[]).map((b) => ({ ...b, source: b.source || ('localstorage' as const) }))
  } catch { /* ignore */ }
  // 不再自动塞 default-local：浏览器只跟 webui 同域通信，bot 列表必须由
  // 容器 entrypoint 通过 VITE_BOTS / window.__BETAGO_CONFIG__.bots 注入，
  // 或由用户在「机器人源」面板手动添加。
  return []
}

function loadInitialBots(): BotInstance[] {
  const envBots = parseEnvBotPresets()
  const userBots = loadUserBotsFromStorage()
  // localStorage 为空 + 有 env 预设：默认全选 env 预设
  return mergeBots(envBots, userBots)
}

function loadInitialSelected(merged: BotInstance[]): string[] {
  try {
    const raw = localStorage.getItem(SELECTED_BOTS_KEY)
    if (raw) {
      const prev = JSON.parse(raw) as string[]
      if (Array.isArray(prev) && prev.length) {
        const valid = prev.filter((id) => merged.some((b) => b.id === id))
        if (valid.length) return valid
      }
    }
  } catch { /* ignore */ }
  const envIds = merged.filter((b) => b.source === 'env').map((b) => b.id)
  if (envIds.length) return envIds
  return merged.slice(0, 1).map((b) => b.id)
}

/** 解析 bot 预设数组的中间结构。 */
interface EnvBotPreset {
  id: string
  name: string
  baseURL?: string
  token?: string
  remark?: string
}

/**
 * 读取 bot 预设来源。优先级：
 *   1. 运行时配置 window.__BETAGO_CONFIG__.bots（部署容器渲染 /config.js 注入）；
 *   2. 构建期 import.meta.env.VITE_BOTS（dev 或自定义 build 时使用）。
 *
 * 运行时来源允许两种形态：
 *   - 数组（推荐，避免再做一次 JSON 解析）；
 *   - JSON 字符串（与 VITE_BOTS 同格式）。
 */
function readBotPresetSource(): EnvBotPreset[] | null {
  const runtime = (typeof window !== 'undefined' ? window.__BETAGO_CONFIG__?.bots : undefined)
  if (Array.isArray(runtime)) {
    return runtime as EnvBotPreset[]
  }
  if (typeof runtime === 'string' && runtime.trim()) {
    try {
      const arr = JSON.parse(runtime)
      if (Array.isArray(arr)) return arr as EnvBotPreset[]
    } catch (e) {
      console.warn('[store/filter] 解析 window.__BETAGO_CONFIG__.bots 失败：', e)
    }
  }
  const buildTime = (import.meta.env.VITE_BOTS as string) || ''
  if (buildTime.trim()) {
    try {
      const arr = JSON.parse(buildTime)
      if (Array.isArray(arr)) return arr as EnvBotPreset[]
    } catch (e) {
      console.warn('[store/filter] 解析 VITE_BOTS 失败，已忽略预设：', e)
    }
  }
  return null
}

function parseEnvBotPresets(): BotInstance[] {
  const arr = readBotPresetSource()
  if (!arr) return []
  return arr
    .filter((b) => b && typeof b === 'object' && typeof b.id === 'string')
    .map((b, i) => {
      // baseURL 仅作为运维登记字段；浏览器一律走 /bot/<id>/api/* 同源前缀。
      const baseURL = typeof b.baseURL === 'string' ? b.baseURL : ''
      const id = String(b.id)
      return {
        id,
        name: String(b.name || b.id),
        baseURL,
        token: typeof b.token === 'string' ? b.token : undefined,
        remark: typeof b.remark === 'string' ? b.remark : undefined,
        color: DEFAULT_PALETTE[i % DEFAULT_PALETTE.length],
        source: 'env' as const,
      }
    })
}

/** 合并来源：env 预设 + localStorage 用户 bot。重名 id 以用户本地为准。 */
function mergeBots(envBots: BotInstance[], userBots: BotInstance[]): BotInstance[] {
  const map = new Map<string, BotInstance>()
  for (const b of envBots) map.set(b.id, b)
  for (const b of userBots) map.set(b.id, { ...map.get(b.id), ...b })
  return [...map.values()]
}

/** 可选的时间窗口 */
export type TimeWindow = '1d' | '7d' | '30d'

/** 可用于展示的数值指标 */
export type MetricKey =
  | 'total_tokens'
  | 'prompt_tokens'
  | 'completion_tokens'
  | 'requests'

/** 分组维度键 */
export type DimensionKey = 'model' | 'kind' | 'source_type' | 'status'

export interface DrillStep {
  /** 维度键 */
  dimension: DimensionKey | 'chat' | 'global' | 'bot'
  /** 选中的维度值（如 model="gpt-4"，chat="oc_xxx"，bot="default-local"） */
  value?: string
  /** 展示用名称 */
  label: string
}

export const METRIC_LABEL: Record<MetricKey, string> = {
  total_tokens: '总 Token',
  prompt_tokens: 'Prompt Token',
  completion_tokens: 'Completion Token',
  requests: '请求数',
}

export const DIMENSION_LABEL: Record<DimensionKey, string> = {
  model: '模型',
  kind: '类型',
  source_type: '来源',
  status: '状态',
}

export const WINDOW_LABEL: Record<TimeWindow, string> = {
  '1d': '1 天',
  '7d': '7 天',
  '30d': '30 天',
}

export const useFilterStore = defineStore('filter', () => {
  // ---------- Bot registry ----------
  const initialBots = loadInitialBots()
  const bots = ref<BotInstance[]>(initialBots)
  const selectedBotIDs = ref<string[]>(loadInitialSelected(initialBots))

  const selectedBots = computed(() =>
    bots.value.filter((b) => selectedBotIDs.value.includes(b.id)),
  )

  watch(
    bots,
    (v) => {
      // 只持久化用户自建的 bot，env 预设由环境变量注入
      const persist = v.filter((b) => b.source !== 'env')
      localStorage.setItem(BOT_STORAGE_KEY, JSON.stringify(persist))
      // 删掉不存在于列表中的 selectedBotIDs
      selectedBotIDs.value = selectedBotIDs.value.filter((id) =>
        v.some((b) => b.id === id),
      )
      if (!selectedBotIDs.value.length && v.length) {
        selectedBotIDs.value = [v[0].id]
      }
    },
    { deep: true },
  )
  watch(
    selectedBotIDs,
    (v) => localStorage.setItem(SELECTED_BOTS_KEY, JSON.stringify(v)),
    { deep: true },
  )

  function addBot(bot: Omit<BotInstance, 'id' | 'color'> & { id?: string; color?: string }) {
    const id = bot.id || `bot-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    if (bots.value.some((b) => b.id === id)) return bots.value.find((b) => b.id === id)!
    const taken = new Set(bots.value.map((b) => b.color))
    const color = bot.color || DEFAULT_PALETTE.find((c) => !taken.has(c)) || `#${Math.floor(Math.random() * 16777215).toString(16)}`
    const nb: BotInstance = { id, color, healthy: undefined, ...bot }
    bots.value = [...bots.value, nb]
    if (!selectedBotIDs.value.length) selectedBotIDs.value = [id]
    return nb
  }

  function removeBot(id: string) {
    bots.value = bots.value.filter((b) => b.id !== id)
    selectedBotIDs.value = selectedBotIDs.value.filter((x) => x !== id)
  }

  function updateBot(id: string, patch: Partial<BotInstance>) {
    const idx = bots.value.findIndex((b) => b.id === id)
    if (idx >= 0) {
      bots.value = [
        ...bots.value.slice(0, idx),
        { ...bots.value[idx], ...patch },
        ...bots.value.slice(idx + 1),
      ]
    }
  }

  function toggleBot(id: string) {
    if (selectedBotIDs.value.includes(id)) {
      if (selectedBotIDs.value.length > 1) {
        selectedBotIDs.value = selectedBotIDs.value.filter((x) => x !== id)
      }
    } else {
      selectedBotIDs.value = [...selectedBotIDs.value, id]
    }
  }

  function setSelectedBots(ids: string[]) {
    const valid = ids.filter((id) => bots.value.some((b) => b.id === id))
    selectedBotIDs.value = valid.length ? valid : [bots.value[0]?.id].filter(Boolean) as string[]
  }

  /** 重新从 VITE_BOTS 环境变量导入缺失的预设（保持现有用户 bot 不变）。
   *  返回导入数量。 */
  function importEnvPresets(): { added: number; total: number } {
    const envBots = parseEnvBotPresets()
    if (!envBots.length) return { added: 0, total: 0 }
    let added = 0
    const merged = new Map(bots.value.map((b) => [b.id, b]))
    for (const preset of envBots) {
      if (!merged.has(preset.id)) {
        merged.set(preset.id, preset)
        added++
      } else {
        // 已存在：只刷新 env 预设字段（proxyPath / 颜色），保留用户已改的 token/name
        const cur = merged.get(preset.id)!
        merged.set(preset.id, {
          ...preset,
          ...cur,
          proxyPath: preset.proxyPath || cur.proxyPath,
          source: preset.source,
        })
      }
    }
    bots.value = [...merged.values()]
    return { added, total: envBots.length }
  }

  function getBot(id: string): BotInstance | undefined {
    return bots.value.find((b) => b.id === id)
  }

  // ---------- 时间窗口 / 指标 ----------
  const window = ref<TimeWindow>('7d')
  const primaryMetric = ref<MetricKey>('total_tokens')
  const secondaryMetric = ref<MetricKey>('requests')

  // ---------- 下钻路径 ----------
  const drillPath = ref<DrillStep[]>([{ dimension: 'global', label: '全部' }])

  const currentChatID = computed<string | undefined>(() => {
    for (let i = drillPath.value.length - 1; i >= 0; i--) {
      if (drillPath.value[i].dimension === 'chat') return drillPath.value[i].value
    }
    return undefined
  })

  const currentBotID = computed<string | undefined>(() => {
    for (let i = drillPath.value.length - 1; i >= 0; i--) {
      if (drillPath.value[i].dimension === 'bot') return drillPath.value[i].value
    }
    return undefined
  })

  const currentDimensionFilters = computed(() => {
    return drillPath.value.filter((s): s is DrillStep & { dimension: DimensionKey; value: string } => (
      s.dimension !== 'global' && s.dimension !== 'chat' && s.dimension !== 'bot' && !!s.value
    ))
  })

  function setWindow(w: TimeWindow) {
    window.value = w
  }

  function setPrimaryMetric(m: MetricKey) {
    primaryMetric.value = m
  }

  function setSecondaryMetric(m: MetricKey) {
    secondaryMetric.value = m
  }

  function pushDrill(step: DrillStep) {
    drillPath.value = [...drillPath.value, step]
  }

  function jumpToDrillIndex(idx: number) {
    drillPath.value = drillPath.value.slice(0, idx + 1)
  }

  function resetDrill() {
    drillPath.value = [{ dimension: 'global', label: '全部' }]
  }

  function enterBot(botID: string) {
    const bot = getBot(botID)
    if (!bot) return
    const base: DrillStep[] = [{ dimension: 'global', label: '全部' }]
    if (bots.value.length > 1) {
      base.push({ dimension: 'bot', value: botID, label: bot.robotName || bot.name })
    }
    drillPath.value = base
  }

  function enterChat(botID: string, chatID: string, label: string) {
    enterBot(botID)
    drillPath.value = [
      ...drillPath.value,
      { dimension: 'chat', value: chatID, label },
    ]
  }

  return {
    // bots
    bots,
    selectedBotIDs,
    selectedBots,
    addBot,
    removeBot,
    updateBot,
    toggleBot,
    setSelectedBots,
    importEnvPresets,
    getBot,
    // filter metrics
    window,
    primaryMetric,
    secondaryMetric,
    setWindow,
    setPrimaryMetric,
    setSecondaryMetric,
    // drill
    drillPath,
    currentChatID,
    currentBotID,
    currentDimensionFilters,
    pushDrill,
    jumpToDrillIndex,
    resetDrill,
    enterBot,
    enterChat,
  }
})
