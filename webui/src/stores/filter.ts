import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

/** 单个 Bot 实例定义（一个 bot 对应一个后端 WebUI 地址）。 */
export interface BotInstance {
  /** 本地唯一 id，用于区分不同 bot */
  id: string
  /** 展示名称 */
  name: string
  /** 后端 WebUI 根地址，例如 https://bot-foo.example.com 或 http://localhost:8090
   *  留空表示走同源相对路径 `/api`（Vite 开发代理）。 */
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
}

const BOT_STORAGE_KEY = 'betago_webui_bots_v1'
const SELECTED_BOTS_KEY = 'betago_webui_selected_bots_v1'

const DEFAULT_PALETTE = [
  '#5B8FF9', '#5AD8A6', '#F6BD16', '#E86452', '#6DC8EC',
  '#945FB9', '#FF9845', '#1E9493', '#FF99C3', '#5D7092',
]

function loadBots(): BotInstance[] {
  try {
    const raw = localStorage.getItem(BOT_STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch { /* ignore */ }
  // 首次访问：提供一个"默认本地"bot
  return [
    {
      id: 'default-local',
      name: '默认本地',
      baseURL: '',
      remark: '走同源 /api（dev proxy 或同域部署）',
      color: DEFAULT_PALETTE[0],
    },
  ]
}

function loadSelected(): string[] {
  try {
    const raw = localStorage.getItem(SELECTED_BOTS_KEY)
    if (raw) return JSON.parse(raw)
  } catch { /* ignore */ }
  return []
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
  const bots = ref<BotInstance[]>(loadBots())
  const initialSelected = (() => {
    const prev = loadSelected()
    const defaults = loadBots()
    return prev.length ? prev : defaults.map((b) => b.id)
  })()
  const selectedBotIDs = ref<string[]>(initialSelected)

  const selectedBots = computed(() =>
    bots.value.filter((b) => selectedBotIDs.value.includes(b.id)),
  )

  watch(
    bots,
    (v) => {
      localStorage.setItem(BOT_STORAGE_KEY, JSON.stringify(v))
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
