<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import type { AgenticChatState, ChatSummary } from '../api/types'
import { BotApi, aggregate, type WithBot } from '../api/client'
import {
  chunkChatIDs,
  summarizeRollout,
} from '../api/agentic'
import {
  useFilterStore,
  METRIC_LABEL,
  type DimensionKey,
  type BotInstance,
} from '../stores/filter'
import { buildSparkline, buildDonut } from '../composables/useChartOptions'
import EChart from '../components/EChart.vue'
import GlobalFilterBar from '../components/GlobalFilterBar.vue'
import AgenticBatchDrawer from '../components/AgenticBatchDrawer.vue'

const router = useRouter()
const store = useFilterStore()

const loading = ref(false)
type BotChat = WithBot<ChatSummary>
const chats = ref<BotChat[]>([])
const keyword = ref('')
const tableRef = ref<{
  clearSelection: () => void
  toggleRowSelection: (row: BotChat, selected?: boolean) => void
}>()
const selectedRows = ref<BotChat[]>([])
const batchDrawerOpen = ref(false)
const rolloutMap = ref<Record<string, AgenticChatState>>({})
const rolloutLoading = ref(false)
const rolloutErrors = ref<Record<string, string>>({})

// ---------- 过滤面板 ----------
const typeFilter = ref<'all' | 'p2p' | 'group' | 'unknown'>('all')
const extFilter = ref<'all' | 'internal' | 'external'>('all')
const membershipFilter = ref<'all' | 'active' | 'left'>('all')
const botFilter = ref<string>('all') // 'all' 或 bot_id
const minTokens = ref<number>()
const maxTokens = ref<number>()

// ---------- 迷你趋势数据：按 bot + chat 的每日 token（仅加载 Top 部分） ----------
interface ChatSpark {
  chat_id: string
  bot_id: string
  tokenSeries: number[]
  byModel: { group: string; total_tokens: number; requests: number; prompt_tokens: number; completion_tokens: number }[]
  byKind: { group: string; total_tokens: number; requests: number; prompt_tokens: number; completion_tokens: number }[]
}
const sparkMap = ref<Record<string, ChatSpark>>({})
const sparkLoading = ref(false)

const botOptions = computed(() => [
  { value: 'all', label: '全部机器人' },
  ...store.selectedBots.map((b) => ({ value: b.id, label: `${b.robotName || b.name} · ${b.id}` })),
])

const rolloutBotID = computed(() => {
  if (store.currentBotID) return store.currentBotID
  if (botFilter.value !== 'all') return botFilter.value
  if (store.selectedBots.length === 1) return store.selectedBots[0].id
  return ''
})

const rolloutBot = computed(() =>
  rolloutBotID.value ? store.getBot(rolloutBotID.value) : undefined,
)

function rowKey(row: BotChat): string {
  return `${row.bot_id}::${row.chat_id}`
}

const selectedStates = computed(() =>
  selectedRows.value
    .map((row) => rolloutMap.value[rowKey(row)])
    .filter((state): state is AgenticChatState => !!state),
)

const hiddenSelectionCount = computed(() => {
  const visible = new Set(filtered.value.map(rowKey))
  return selectedRows.value.filter((row) => !visible.has(rowKey(row))).length
})

const filtered = computed<BotChat[]>(() => {
  const kw = keyword.value.trim().toLowerCase()
  const dimFilters = store.currentDimensionFilters
  return chats.value.filter((c) => {
    if (botFilter.value !== 'all' && c.bot_id !== botFilter.value) return false
    if (store.currentBotID && c.bot_id !== store.currentBotID) return false
    if (kw && !(c.name.toLowerCase().includes(kw) || c.chat_id.toLowerCase().includes(kw))) return false
    if (typeFilter.value !== 'all' && chatKind(c) !== typeFilter.value) return false
    if (extFilter.value === 'internal' && c.external) return false
    if (extFilter.value === 'external' && !c.external) return false
    if (membershipFilter.value === 'active' && c.membership === 'left') return false
    if (membershipFilter.value === 'left' && c.membership !== 'left') return false
    const t = Number(c.metrics?.total_tokens || 0)
    if (minTokens.value != null && t < minTokens.value) return false
    if (maxTokens.value != null && t > maxTokens.value) return false
    if (dimFilters.length) {
      const spark = sparkMap.value[`${c.bot_id}::${c.chat_id}`]
      if (!spark) return false
      for (const f of dimFilters) {
        const pool = f.dimension === 'model' ? spark.byModel : spark.byKind
        if (!pool.some((p) => p.group === f.value)) return false
      }
    }
    return true
  })
})

// ---------- 统计摘要卡片 ----------
const summary = computed(() => {
  const list = filtered.value
  const totalTokens = list.reduce((a, c) => a + Number(c.metrics?.total_tokens || 0), 0)
  const totalMsgs = list.reduce((a, c) => a + Number(c.metrics?.recent_messages || 0), 0)
  const totalMembers = list.reduce((a, c) => a + Number(c.metrics?.member_count || 0), 0)
  return {
    count: list.length,
    totalTokens,
    totalMsgs,
    totalMembers,
    botsCount: new Set(list.map((c) => c.bot_id)).size,
  }
})

const topModelDistribution = computed<EChartsOption>(() => {
  const agg: Record<string, number> = {}
  for (const s of Object.values(sparkMap.value)) {
    for (const m of s.byModel) agg[m.group] = (agg[m.group] || 0) + Number(m.total_tokens || 0)
  }
  const arr = Object.entries(agg).map(([k, v]) => ({
    group: k,
    total_tokens: v,
    requests: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
  }))
  return buildDonut({
    title: '模型分布（当前过滤）',
    data: arr,
    metric: 'total_tokens',
  })
})

const statusDistribution = computed<EChartsOption>(() => {
  const p2p = filtered.value.filter((c) => chatKind(c) === 'p2p').length
  const group = filtered.value.filter((c) => chatKind(c) === 'group').length
  const unknown = filtered.value.filter((c) => chatKind(c) === 'unknown').length
  return buildDonut({
    title: '会话类型分布',
    data: [
      { group: '单聊', total_tokens: p2p, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
      { group: '群聊', total_tokens: group, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
      { group: '未知', total_tokens: unknown, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
    ],
    metric: 'total_tokens',
  })
})

const perBotDistribution = computed<EChartsOption>(() => {
  const map: Record<string, number> = {}
  for (const c of filtered.value) {
    map[c.bot_name] = (map[c.bot_name] || 0) + Number(c.metrics?.total_tokens || 0)
  }
  const arr = Object.entries(map).map(([k, v]) => ({
    group: k,
    total_tokens: v,
    requests: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
  }))
  return buildDonut({
    title: '按机器人分布',
    data: arr,
    metric: 'total_tokens',
  })
})

function sparkOption(c: BotChat): EChartsOption {
  const s = sparkMap.value[`${c.bot_id}::${c.chat_id}`]
  if (!s) return buildSparkline({ values: [0] })
  return buildSparkline({ values: s.tokenSeries, positive: true })
}

// chatKind 用 chat_id 前缀判断会话类型：
//   - oc_ 群聊
//   - ou_ 单聊（机器人对人）
//   - 其它前缀 / 缺失 → unknown（极少出现，通常是后端补充项里没拿到 ID 类型）
// 注意：后端 chat_status 字段来自 Lark ListChat.chat_status（normal/dissolved
// 等生命周期状态），与"是否群聊"无关；详情接口的 chat_mode 才是真正的类型字段，
// 但列表接口（larkim ListChat）没该字段。
function chatKind(c: { chat_id?: string }): 'p2p' | 'group' | 'unknown' {
  const id = (c.chat_id || '').trim()
  if (id.startsWith('oc_')) return 'group'
  if (id.startsWith('ou_')) return 'p2p'
  return 'unknown'
}

function open(c: BotChat) {
  store.enterChat(c.bot_id, c.chat_id, c.name)
  router.push({
    name: 'chat-detail',
    params: { chatID: c.chat_id },
    query: { bot: c.bot_id },
  })
}

function handleRowClick(
  row: BotChat,
  column?: { type?: string },
  event?: MouseEvent,
) {
  if (
    column?.type === 'selection' ||
    (event?.target as HTMLElement | null)?.closest?.('.el-checkbox')
  ) {
    return
  }
  open(row)
}

function handleSelectionChange(rows: BotChat[]) {
  if (!rolloutBotID.value) {
    selectedRows.value = []
    return
  }
  selectedRows.value = rows.filter(
    (row) => row.bot_id === rolloutBotID.value,
  )
}

function isRowSelected(row: BotChat): boolean {
  const key = rowKey(row)
  return selectedRows.value.some((selected) => rowKey(selected) === key)
}

function toggleMobileSelection(row: BotChat, selected: boolean) {
  tableRef.value?.toggleRowSelection(row, selected)
}

function openBatchDrawer() {
  const botIDs = new Set(selectedRows.value.map((row) => row.bot_id))
  if (
    !rolloutBot.value ||
    botIDs.size !== 1 ||
    !botIDs.has(rolloutBot.value.id)
  ) {
    ElMessage.error('批量灰度只能作用于当前选择的单个 Bot')
    return
  }
  if (selectedStates.value.length !== selectedRows.value.length) {
    ElMessage.warning('部分会话的灰度状态仍在加载，请稍后重试')
    return
  }
  batchDrawerOpen.value = true
}

async function refreshRolloutsAfterConflict() {
  await loadRollouts()
}

async function handleBatchCommitted() {
  await loadRollouts()
  tableRef.value?.clearSelection()
  selectedRows.value = []
}

function rolloutSummary(row: BotChat): string {
  const state = rolloutMap.value[rowKey(row)]
  if (state) return summarizeRollout(state)
  if (rolloutErrors.value[row.bot_id]) return '灰度状态不可用'
  return rolloutLoading.value ? '灰度状态加载中…' : '尚未读取灰度状态'
}

// ---------- 加载 ----------
const MAX_CHATS_WITH_SPARK = 30

async function load() {
  const bots = store.selectedBots
  if (!bots.length) {
    ElMessage.warning('请先选择至少一个机器人（右上角「机器人源」按钮）')
    chats.value = []
    return
  }
  loading.value = true
  try {
    const listResp = await aggregate(
      bots,
      (api) => api.listChats({ metrics: true, window: store.window }),
      (bot, err) => {
        ElMessage.warning(`「${bot.name}」拉取失败：${(err as any)?.message || err}`)
      },
    )
    const flat: BotChat[] = []
    for (const r of listResp) {
      for (const c of r.items) {
        flat.push({ ...c, bot_id: r.bot_id, bot_name: r.bot_name, bot_color: r.bot_color })
      }
    }
    // 跨 bot 元数据合并：被踢出某群的 bot 拿不到该群的当前 name/avatar，
    // 而别的 active bot 能。这里挑出每个 chat_id 任意一个 membership=active 的
    // 数据当权威 name/avatar/external/member_count，把 left 行的展示字段覆盖掉，
    // 但 bot_id / token / 发言量等"属于该 bot 自身"的数据保持不变。
    const meta = new Map<string, BotChat>()
    for (const c of flat) {
      const cur = meta.get(c.chat_id)
      const isActive = (c.membership ?? 'active') === 'active'
      if (!cur) {
        meta.set(c.chat_id, c)
      } else if (isActive && (cur.membership ?? 'active') !== 'active') {
        meta.set(c.chat_id, c)
      }
    }
    for (const c of flat) {
      const authoritative = meta.get(c.chat_id)
      if (!authoritative || authoritative === c) continue
      if (!c.name || c.name === c.chat_id) c.name = authoritative.name
      if (!c.avatar) c.avatar = authoritative.avatar
      if (!c.description) c.description = authoritative.description
      // 外部 / tenant / 成员数仅当当前条目缺失时才借用，避免多 bot 数据噪音。
      if (c.external === undefined) c.external = authoritative.external
      if (!c.tenant_key) c.tenant_key = authoritative.tenant_key
      if (c.metrics && !c.metrics.member_count && authoritative.metrics?.member_count) {
        c.metrics.member_count = authoritative.metrics.member_count
      }
    }
    chats.value = flat
  } catch (e: any) {
    ElMessage.error('加载会话列表失败：' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
  await Promise.all([loadSparks(), loadRollouts()])
}

async function loadSparks() {
  sparkLoading.value = true
  try {
    const topN: { bot: BotInstance; chatID: string }[] = chats.value
      .slice()
      .sort((a, b) => Number(b.metrics?.total_tokens || 0) - Number(a.metrics?.total_tokens || 0))
      .slice(0, MAX_CHATS_WITH_SPARK)
      .map((c) => {
        const bot = store.getBot(c.bot_id)!
        return { bot, chatID: c.chat_id }
      })
    const results = await Promise.allSettled(
      topN.map(({ bot, chatID }) =>
        new BotApi(bot).getStats(chatID, store.window).then((s) => ({
          bot_id: bot.id,
          chat_id: chatID,
          tokenSeries: s.token.by_day.map((d) => Number(d.total_tokens || 0)),
          byModel: s.token.by_model.map((m) => ({
            group: m.group,
            total_tokens: Number(m.total_tokens),
            requests: Number(m.requests),
            prompt_tokens: Number(m.prompt_tokens),
            completion_tokens: Number(m.completion_tokens),
          })),
          byKind: s.token.by_kind.map((m) => ({
            group: m.group,
            total_tokens: Number(m.total_tokens),
            requests: Number(m.requests),
            prompt_tokens: Number(m.prompt_tokens),
            completion_tokens: Number(m.completion_tokens),
          })),
        })),
      ),
    )
    const newMap: Record<string, ChatSpark> = {}
    for (const r of results) {
      if (r.status !== 'fulfilled') continue
      const k = `${r.value.bot_id}::${r.value.chat_id}`
      newMap[k] = r.value as ChatSpark
    }
    sparkMap.value = newMap
  } catch (e) {
    console.warn('load sparks error', e)
  } finally {
    sparkLoading.value = false
  }
}

async function loadRollouts() {
  rolloutLoading.value = true
  rolloutErrors.value = {}
  const next: Record<string, AgenticChatState> = {}
  const groups = new Map<string, BotChat[]>()
  for (const chat of chats.value) {
    const rows = groups.get(chat.bot_id) || []
    rows.push(chat)
    groups.set(chat.bot_id, rows)
  }
  await Promise.all(
    [...groups.entries()].map(async ([botID, rows]) => {
      const bot = store.getBot(botID)
      if (!bot) return
      try {
        const uniqueIDs = [...new Set(rows.map((row) => row.chat_id))]
        for (const chunk of chunkChatIDs(uniqueIDs)) {
          const response = await new BotApi(bot).getAgenticRollouts(chunk)
          for (const state of response.items) {
            next[`${botID}::${state.chat_id}`] = state
          }
        }
      } catch (error: any) {
        rolloutErrors.value = {
          ...rolloutErrors.value,
          [botID]:
            error?.response?.data?.error ||
            error?.message ||
            '灰度状态读取失败',
        }
      }
    }),
  )
  rolloutMap.value = next
  rolloutLoading.value = false
}

onMounted(load)
watch([() => store.window, () => store.selectedBotIDs.slice().sort().join(',')], load)
watch(rolloutBotID, () => {
  tableRef.value?.clearSelection()
  selectedRows.value = []
})
</script>

<template>
  <div class="chat-ops-page">
    <header class="page-intro">
      <div>
        <p class="page-intro__eyebrow">Conversation operations</p>
        <h1>会话运营</h1>
        <p class="page-intro__copy">
          在同一视角下筛选多 Bot 会话、定位消耗趋势，并安全推进 Agentic 灰度。
        </p>
      </div>
    </header>

    <GlobalFilterBar />

    <div v-loading="loading">
      <!-- 摘要卡片 -->
      <el-row :gutter="12" class="chat-ops-summary">
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="匹配会话数" :value="summary.count" />
            <div class="kpi-sub">跨 {{ summary.botsCount }} 个机器人</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="累计 Token" :value="summary.totalTokens" />
            <div class="kpi-sub">{{ METRIC_LABEL[store.primaryMetric] }} 主视图</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="近期消息" :value="summary.totalMsgs" />
            <div class="kpi-sub">{{ store.window }} 窗口</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="累计成员量" :value="summary.totalMembers" />
            <div class="kpi-sub">各群去重求和</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 过滤面板 + 分布摘要 -->
      <el-card shadow="never" class="chat-ops-toolbar panel">
        <div class="chat-filter-layout">
          <div class="chat-filter-main">
            <div class="chat-filter-controls">
              <el-input
                v-model="keyword"
                class="chat-filter-control chat-filter-control--search"
                placeholder="按群名或 chat_id 搜索"
                clearable
              />
              <el-select v-model="botFilter" class="chat-filter-control chat-filter-control--bot" placeholder="机器人">
                <el-option
                  v-for="o in botOptions"
                  :key="o.value"
                  :value="o.value"
                  :label="o.label"
                />
              </el-select>
              <el-select v-model="typeFilter" class="chat-filter-control" placeholder="会话类型">
                <el-option value="all" label="全部类型" />
                <el-option value="group" label="群聊" />
                <el-option value="p2p" label="单聊" />
                <el-option value="unknown" label="未知" />
              </el-select>
              <el-select v-model="extFilter" class="chat-filter-control" placeholder="内外群">
                <el-option value="all" label="全部" />
                <el-option value="internal" label="内部" />
                <el-option value="external" label="外部" />
              </el-select>
              <el-select v-model="membershipFilter" class="chat-filter-control" placeholder="是否在群">
                <el-option value="all" label="全部" />
                <el-option value="active" label="仅看在群" />
                <el-option value="left" label="仅看已离开" />
              </el-select>
              <el-input-number
                v-model="minTokens"
                placeholder="最小 Token"
                :min="0"
                :controls="false"
                class="chat-filter-control chat-filter-control--number"
              />
              <el-input-number
                v-model="maxTokens"
                placeholder="最大 Token"
                :min="0"
                :controls="false"
                class="chat-filter-control chat-filter-control--number"
              />
              <el-button
                @click="() => { keyword = ''; typeFilter = 'all'; extFilter = 'all'; membershipFilter = 'all'; botFilter = 'all'; minTokens = undefined; maxTokens = undefined }"
              >清除过滤</el-button>
              <el-button :loading="loading" type="primary" @click="load">刷新</el-button>
            </div>

            <!-- 下钻激活过滤器 -->
            <div v-if="store.currentDimensionFilters.length" class="active-filters">
              <span>激活的维度过滤</span>
              <el-tag
                v-for="(f, i) in store.currentDimensionFilters"
                :key="i"
                closable
                type="info"
                @close="() => store.jumpToDrillIndex(store.drillPath.indexOf(f) - 1)"
              >
                {{ f.dimension }} = {{ f.label }}
              </el-tag>
            </div>

            <!-- 快速筛选 -->
            <div class="quick-filters">
              <span>可叠加维度</span>
              <el-button
                v-for="dim of (['model','kind','source_type','status'] as DimensionKey[])"
                :key="dim"
                size="small"
                :type="store.currentDimensionFilters.some(f => f.dimension === dim) ? 'primary' : 'default'"
                plain
                @click="ElMessage.info(`请从仪表盘或会话详情页的「${dim}」图表项点击进入下钻`)"
              >{{ dim }}</el-button>
            </div>
          </div>

          <!-- 分布饼图 -->
          <div class="chat-distributions">
            <div class="chat-distributions__chart">
              <EChart :option="perBotDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
            <div class="chat-distributions__chart">
              <EChart :option="topModelDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
            <div class="chat-distributions__chart">
              <EChart :option="statusDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
          </div>
        </div>
      </el-card>

      <!-- 主数据表格 -->
      <el-card shadow="never" class="panel chat-ops-table">
        <div class="chat-ops-table__header">
          <div>
            <strong>共 {{ filtered.length }} 个会话</strong>
            <span v-if="rolloutBotID">
              当前 Bot 可批量灰度
              <template v-if="selectedRows.length">
                · 已选 {{ selectedRows.length }} 项
              </template>
              <template v-if="hiddenSelectionCount">
                · {{ hiddenSelectionCount }} 项被过滤器隐藏
              </template>
            </span>
            <span v-else>全部 Bot 视图仅支持查看，选择一个 Bot 后可批量灰度。</span>
          </div>
          <div class="chat-ops-table__actions">
            <el-tag v-if="sparkLoading" type="info" size="small">
              迷你趋势加载中…
            </el-tag>
            <el-tag v-if="rolloutLoading" type="info" size="small">
              Agentic 状态加载中…
            </el-tag>
            <el-button
              v-if="rolloutBotID"
              class="chat-ops-agentic-action"
              :disabled="
                !selectedRows.length ||
                selectedStates.length !== selectedRows.length
              "
              @click="openBatchDrawer"
            >
              批量 Agentic 灰度
            </el-button>
          </div>
        </div>

        <div class="desktop-chat-table">
          <el-table
            ref="tableRef"
            v-loading="loading"
            :data="filtered"
            :row-key="rowKey"
            stripe
            :default-sort="{ prop: 'total_tokens', order: 'descending' }"
            @selection-change="handleSelectionChange"
            @row-click="handleRowClick"
          >
          <el-table-column
            v-if="rolloutBotID"
            type="selection"
            reserve-selection
            width="48"
            fixed="left"
          />
          <el-table-column label="Bot" width="200" fixed="left">
            <template #default="{ row }">
              <div style="display: flex; align-items: center; gap: 6px">
                <span
                  class="bot-dot"
                  :style="{ background: row.bot_color || '#909399' }"
                />
                <el-text size="small" :style="{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }">
                  {{ row.bot_name }}
                </el-text>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="头像" width="60" align="center">
            <template #default="{ row }">
              <el-avatar :src="row.avatar" :size="32" shape="square">{{ row.name?.[0] }}</el-avatar>
            </template>
          </el-table-column>
          <el-table-column prop="name" label="名称" min-width="180" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="chat-identity">
                <strong>{{ row.name }}</strong>
                <span>{{ row.chat_id }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="Agentic 灰度" min-width="190">
            <template #default="{ row }">
              <div
                class="rollout-summary"
                :class="{ 'is-ready': !!rolloutMap[rowKey(row)] }"
              >
                <span class="rollout-summary__dot" aria-hidden="true" />
                <span>{{ rolloutSummary(row) }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="类型" width="110">
            <template #default="{ row }">
              <el-tag v-if="chatKind(row) === 'p2p'" size="small" type="info" effect="plain">单聊</el-tag>
              <el-tag v-else-if="chatKind(row) === 'group'" size="small" type="success" effect="plain">群聊</el-tag>
              <el-tag v-else size="small" type="info" effect="plain">未知</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="外部" width="70">
            <template #default="{ row }">
              <el-tag v-if="row.external" size="small" type="warning" effect="plain">外</el-tag>
              <span v-else style="color:#c0c4cc">—</span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag v-if="row.membership === 'left'" size="small" type="danger" effect="plain">已离开</el-tag>
              <el-tag v-else-if="row.membership === 'unknown'" size="small" type="info" effect="plain">未知</el-tag>
              <el-tag v-else size="small" type="success" effect="plain">在群</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="近趋势" width="130" align="center">
            <template #default="{ row }">
              <EChart
                :option="sparkOption(row)"
                height="36px"
                :dataZoom="false"
                :toolbox="false"
                :merge="true"
              />
            </template>
          </el-table-column>
          <el-table-column
            label="近期发言量"
            width="130"
            sortable
            :sort-method="(a: any, b: any) => Number(a.metrics?.recent_messages || 0) - Number(b.metrics?.recent_messages || 0)"
          >
            <template #default="{ row }">
              <span style="font-variant-numeric: tabular-nums">{{
                row.metrics?.recent_messages ?? '-'
              }}</span>
            </template>
          </el-table-column>
          <el-table-column
            label="群成员量"
            width="120"
            sortable
            :sort-method="(a: any, b: any) => Number(a.metrics?.member_count || 0) - Number(b.metrics?.member_count || 0)"
          >
            <template #default="{ row }">{{ row.metrics?.member_count ?? '-' }}</template>
          </el-table-column>
          <el-table-column
            label="Token 总量"
            width="150"
            sortable
            :sort-method="(a: any, b: any) => Number(a.metrics?.total_tokens || 0) - Number(b.metrics?.total_tokens || 0)"
          >
            <template #default="{ row }">
              <el-tag
                size="small"
                effect="plain"
                :type="(Number(row.metrics?.total_tokens) || 0) > 1_000_000 ? 'danger'
                     : (Number(row.metrics?.total_tokens) || 0) > 100_000 ? 'warning'
                     : 'success'"
              >
                {{ row.metrics?.total_tokens != null ? Number(row.metrics.total_tokens).toLocaleString() : '-' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            label="人均 Token"
            width="130"
            sortable
            :sort-method="(a: any, b: any) => Number(a.metrics?.tokens_per_member || 0) - Number(b.metrics?.tokens_per_member || 0)"
          >
            <template #default="{ row }">{{ row.metrics?.tokens_per_member?.toFixed?.(1) ?? row.metrics?.tokens_per_member ?? '-' }}</template>
          </el-table-column>
          <el-table-column
            label="单条均 Token"
            width="140"
            sortable
            :sort-method="(a: any, b: any) => Number(a.metrics?.tokens_per_message || 0) - Number(b.metrics?.tokens_per_message || 0)"
          >
            <template #default="{ row }">{{ row.metrics?.tokens_per_message?.toFixed?.(1) ?? row.metrics?.tokens_per_message ?? '-' }}</template>
          </el-table-column>

          <el-table-column label="操作" width="100" fixed="right">
            <template #default="{ row }">
              <el-button size="small" type="primary" link @click.stop="open(row)">查看详情 →</el-button>
            </template>
          </el-table-column>
          </el-table>
        </div>

        <div v-if="filtered.length" class="mobile-chat-list">
          <article
            v-for="row in filtered"
            :key="rowKey(row)"
            class="mobile-chat-card"
            :class="{ 'is-selected': isRowSelected(row) }"
          >
            <header class="mobile-chat-card__header">
              <el-checkbox
                v-if="rolloutBotID"
                :model-value="isRowSelected(row)"
                :aria-label="`选择 ${row.name}`"
                @change="(value: boolean) => toggleMobileSelection(row, value)"
              />
              <el-avatar :src="row.avatar" :size="42" shape="square">
                {{ row.name?.[0] }}
              </el-avatar>
              <div class="mobile-chat-card__identity">
                <strong>{{ row.name }}</strong>
                <span>{{ row.chat_id }}</span>
              </div>
              <span class="mobile-chat-card__type">
                {{ chatKind(row) === 'group' ? '群聊' : chatKind(row) === 'p2p' ? '单聊' : '未知' }}
              </span>
            </header>

            <div class="mobile-chat-card__bot">
              <span
                class="bot-dot"
                :style="{ background: row.bot_color || '#909399' }"
              />
              <span>{{ row.bot_name }}</span>
              <span class="mobile-chat-card__membership">
                {{ row.membership === 'left' ? '已离开' : row.membership === 'unknown' ? '归属未知' : '在群' }}
              </span>
            </div>

            <div
              class="rollout-summary mobile-chat-card__rollout"
              :class="{ 'is-ready': !!rolloutMap[rowKey(row)] }"
            >
              <span class="rollout-summary__dot" aria-hidden="true" />
              <span>{{ rolloutSummary(row) }}</span>
            </div>

            <dl class="mobile-chat-card__metrics">
              <div>
                <dt>近期消息</dt>
                <dd>{{ row.metrics?.recent_messages ?? '—' }}</dd>
              </div>
              <div>
                <dt>成员</dt>
                <dd>{{ row.metrics?.member_count ?? '—' }}</dd>
              </div>
              <div>
                <dt>Token</dt>
                <dd>
                  {{ row.metrics?.total_tokens != null
                    ? Number(row.metrics.total_tokens).toLocaleString()
                    : '—' }}
                </dd>
              </div>
            </dl>

            <el-button class="mobile-chat-card__action" @click="open(row)">
              查看会话详情
              <span aria-hidden="true">→</span>
            </el-button>
          </article>
        </div>

        <div v-else class="chat-list-empty">
          <strong>{{ chats.length ? '没有匹配当前筛选的会话' : '尚未读取到会话' }}</strong>
          <span>
            {{ chats.length ? '调整筛选条件后再试。' : '选择可用的机器人源并刷新列表。' }}
          </span>
        </div>
      </el-card>
    </div>

    <AgenticBatchDrawer
      v-if="rolloutBot"
      v-model="batchDrawerOpen"
      :bot="rolloutBot"
      :states="selectedStates"
      @refresh="refreshRolloutsAfterConflict"
      @committed="handleBatchCommitted"
    />
  </div>
</template>

<style scoped>
.chat-ops-page {
  --ops-pine-900: #143b36;
  --ops-pine-700: #25534d;
  --ops-lime: #d7ff73;
  --ops-canvas: #f8f7f3;
  --ops-surface: #ffffff;
  --ops-border: #e6e3da;
  --ops-muted: #737d78;
  min-height: 100%;
  padding: clamp(0.25rem, 1vw, 0.75rem);
  border-radius: 1.25rem;
  background:
    radial-gradient(circle at 100% 0%, rgb(215 255 115 / 13%), transparent 25rem),
    linear-gradient(180deg, var(--ops-canvas), transparent 22rem);
}

.chat-ops-summary {
  margin-bottom: 0.85rem;
  padding: 0.35rem;
  border: 1px solid var(--ops-border);
  border-radius: 1rem;
  background: var(--ops-pine-900);
  box-shadow: 0 1rem 2.5rem rgb(20 59 54 / 10%);
}

.chat-ops-summary :deep(.el-col) {
  display: flex;
}

.chat-ops-summary .kpi-card {
  width: 100%;
  border: 0;
  background: transparent;
  color: #fff;
}

.chat-ops-summary .kpi-card :deep(.el-statistic__head),
.chat-ops-summary .kpi-card .kpi-sub {
  color: #a9beb7;
}

.chat-ops-summary .kpi-card :deep(.el-statistic__number) {
  color: #fff;
}

.kpi-card :deep(.el-card__body) {
  padding: 14px 20px;
}
.kpi-sub {
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
}
.panel {
  border: 1px solid var(--ops-border);
  border-radius: 1rem;
  background: var(--ops-surface);
}
.panel :deep(.el-card__body) {
  padding: 14px 16px;
}

.chat-ops-toolbar {
  margin-bottom: 0.85rem;
  box-shadow: 0 0.7rem 2rem rgb(20 59 54 / 5%);
}

.chat-filter-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(26rem, 33rem);
  gap: 1rem;
  align-items: start;
}

.chat-filter-main {
  min-width: 0;
}

.chat-filter-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.6rem;
}

.chat-filter-control {
  width: 8.5rem;
}

.chat-filter-control--search {
  width: min(100%, 17.5rem);
}

.chat-filter-control--bot {
  width: 12.5rem;
}

.chat-filter-control--number {
  width: 9.5rem;
}

.active-filters,
.quick-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.45rem;
  margin-top: 0.7rem;
}

.active-filters > span:first-child,
.quick-filters > span:first-child {
  color: var(--ops-muted);
  font-size: 0.67rem;
  font-weight: 750;
  letter-spacing: 0.045em;
}

.chat-distributions {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--ops-border);
  border-radius: 0.8rem;
  background: #faf9f5;
}

.chat-distributions__chart {
  min-width: 0;
  border-right: 1px solid var(--ops-border);
}

.chat-distributions__chart:last-child {
  border-right: 0;
}

.chat-ops-table {
  overflow: hidden;
}

.chat-ops-table__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 0.8rem;
}

.chat-ops-table__header > div:first-child {
  display: grid;
  gap: 0.2rem;
}

.chat-ops-table__header strong {
  color: var(--ops-pine-900);
}

.chat-ops-table__header span {
  color: var(--ops-muted);
  font-size: 0.76rem;
}

.chat-ops-table__actions {
  display: flex;
  align-items: center;
  gap: 0.55rem;
}

.chat-ops-agentic-action {
  --el-button-bg-color: var(--ops-lime);
  --el-button-border-color: var(--ops-lime);
  --el-button-text-color: #173b35;
  --el-button-hover-bg-color: #c9f158;
  --el-button-hover-border-color: #c9f158;
  font-weight: 750;
}

.chat-identity {
  display: grid;
  min-width: 0;
}

.chat-identity strong,
.chat-identity span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-identity strong {
  color: #263f3b;
  font-size: 0.86rem;
}

.chat-identity span {
  color: #8a938e;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.68rem;
}

.rollout-summary {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  color: #7b847f;
  font-size: 0.75rem;
  line-height: 1.4;
}

.rollout-summary__dot {
  flex: 0 0 auto;
  width: 0.48rem;
  height: 0.48rem;
  border-radius: 50%;
  background: #b8beb9;
}

.rollout-summary.is-ready {
  color: #285a50;
}

.rollout-summary.is-ready .rollout-summary__dot {
  background: #4a8f79;
  box-shadow: 0 0 0 0.2rem rgb(74 143 121 / 12%);
}

.bot-dot {
  flex: 0 0 auto;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.08);
}

.desktop-chat-table {
  cursor: pointer;
}

.mobile-chat-list {
  display: none;
}

.chat-list-empty {
  display: grid;
  justify-items: center;
  gap: 0.35rem;
  padding: 3rem 1rem;
  color: var(--ops-muted);
  text-align: center;
}

.chat-list-empty::before {
  display: grid;
  place-items: center;
  width: 3rem;
  height: 3rem;
  margin-bottom: 0.35rem;
  border-radius: 1rem;
  background: var(--ops-pine-100);
  color: var(--ops-pine-700);
  content: "—";
  font-family: var(--ops-font-mono);
  font-weight: 800;
}

.chat-list-empty strong {
  color: var(--ops-pine-900);
  font-size: 0.9rem;
}

.chat-list-empty span {
  font-size: 0.75rem;
}

@media (max-width: 1023px) {
  .chat-ops-summary :deep(.el-col) {
    width: 50%;
    max-width: 50%;
    flex: 0 0 50%;
  }

  .chat-filter-layout {
    grid-template-columns: 1fr;
  }

  .chat-ops-table :deep(.el-card__body) {
    overflow-x: auto;
  }
}

@media (max-width: 767px) {
  .chat-ops-page {
    padding: 0;
  }

  .chat-ops-summary :deep(.el-col) {
    width: 50%;
    max-width: 50%;
    flex-basis: 50%;
  }

  .chat-ops-summary .kpi-card :deep(.el-card__body) {
    padding: 0.8rem;
  }

  .chat-ops-summary .kpi-card :deep(.el-statistic__number) {
    font-size: 1.2rem;
  }

  .chat-ops-table__header,
  .chat-ops-table__actions {
    align-items: stretch;
    flex-direction: column;
  }

  .chat-ops-table__actions :deep(.el-button) {
    min-height: 2.75rem;
    width: 100%;
    margin: 0;
  }

  .chat-filter-control,
  .chat-ops-toolbar :deep(.el-input),
  .chat-ops-toolbar :deep(.el-select),
  .chat-ops-toolbar :deep(.el-input-number) {
    width: 100% !important;
    max-width: none !important;
  }

  .chat-filter-controls {
    display: grid;
    grid-template-columns: 1fr;
  }

  .chat-distributions {
    display: none;
  }

  .chat-distributions__chart {
    border-right: 0;
    border-bottom: 1px solid var(--ops-border);
  }

  .chat-distributions__chart:last-child {
    border-bottom: 0;
  }

  .desktop-chat-table {
    display: none;
  }

  .mobile-chat-list {
    display: grid;
    gap: 0.7rem;
  }

  .mobile-chat-card {
    display: grid;
    gap: 0.75rem;
    padding: 0.85rem;
    border: 1px solid var(--ops-border);
    border-radius: 0.9rem;
    background: #fffefa;
    box-shadow: 0 0.4rem 1rem rgb(20 59 54 / 5%);
  }

  .mobile-chat-card.is-selected {
    border-color: var(--ops-teal);
    box-shadow:
      0 0 0 0.18rem rgb(74 143 121 / 12%),
      0 0.5rem 1.2rem rgb(20 59 54 / 8%);
  }

  .mobile-chat-card__header {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    min-width: 0;
  }

  .mobile-chat-card__identity {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .mobile-chat-card__identity strong,
  .mobile-chat-card__identity span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mobile-chat-card__identity strong {
    color: var(--ops-pine-950);
    font-size: 0.88rem;
  }

  .mobile-chat-card__identity span {
    margin-top: 0.2rem;
    color: var(--ops-muted-light);
    font-family: var(--ops-font-mono);
    font-size: 0.62rem;
  }

  .mobile-chat-card__type,
  .mobile-chat-card__membership {
    padding: 0.25rem 0.45rem;
    border-radius: 999px;
    background: #eef2ed;
    color: #65726d;
    font-size: 0.64rem;
    font-weight: 700;
  }

  .mobile-chat-card__bot {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--ops-muted);
    font-size: 0.72rem;
  }

  .mobile-chat-card__membership {
    margin-left: auto;
  }

  .mobile-chat-card__rollout {
    width: 100%;
    padding: 0.55rem 0.65rem;
    border-radius: 0.6rem;
    background: #f5f6f1;
  }

  .mobile-chat-card__metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.45rem;
    margin: 0;
  }

  .mobile-chat-card__metrics div {
    min-width: 0;
    padding: 0.55rem;
    border: 1px solid var(--ops-border);
    border-radius: 0.6rem;
    background: #faf9f5;
  }

  .mobile-chat-card__metrics dt {
    color: var(--ops-muted);
    font-size: 0.62rem;
  }

  .mobile-chat-card__metrics dd {
    margin: 0.2rem 0 0;
    overflow: hidden;
    color: var(--ops-pine-900);
    font-size: 0.8rem;
    font-variant-numeric: tabular-nums;
    font-weight: 750;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mobile-chat-card__action {
    width: 100%;
    min-height: 2.75rem;
    margin: 0;
    border-color: var(--ops-pine-100);
    background: var(--ops-pine-100);
    color: var(--ops-pine-900);
  }

  .mobile-chat-card__action span {
    margin-left: auto;
  }
}
</style>
