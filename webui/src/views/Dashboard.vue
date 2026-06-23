<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import type {
  ChatSummary,
  StatsResponse,
  TokenDailyPoint,
  TokenGroupCount,
} from '../api/types'
import { BotApi, aggregate as aggregateCalls, type WithBot } from '../api/client'
import {
  useFilterStore,
  METRIC_LABEL,
  type DimensionKey,
  type MetricKey,
  type BotInstance,
} from '../stores/filter'
import {
  buildDonut,
  buildFunnel,
  buildHeatmap,
  buildRadar,
  buildSunburst,
  buildTopBar,
  buildTrend,
} from '../composables/useChartOptions'
import EChart from '../components/EChart.vue'
import GlobalFilterBar from '../components/GlobalFilterBar.vue'

const router = useRouter()
const store = useFilterStore()

const loading = ref(false)
const topChatStats = ref<WithBot<StatsResponse>[]>([])
const allChats = ref<WithBot<ChatSummary & { metrics?: any }>[]>([])
const totalFetches = ref(0)

/**
 * 多 bot 聚合：把每个 bot 的 token stats 合并成虚拟"全局 stats"，
 * 保留 bot 维度以便在图表中分解。
 */
interface AggregateStats {
  total: {
    requests: number
    prompt_tokens: number
    completion_tokens: number
    total_tokens: number
  }
  by_model: (TokenGroupCount & { bot_id?: string; bot_name?: string })[]
  by_kind: TokenGroupCount[]
  by_source_type: TokenGroupCount[]
  by_status: TokenGroupCount[]
  by_day: (TokenDailyPoint & { prompt_tokens?: number; completion_tokens?: number })[]
  per_bot: {
    bot_id: string
    bot_name: string
    bot_color?: string
    total_tokens: number
    requests: number
    prompt_tokens: number
    completion_tokens: number
    chats_count: number
    dailySeries: { day: string; total_tokens: number; requests: number }[]
  }[]
}

function sumGroupKey(arr: any[], key: MetricKey) {
  return arr.reduce((acc, g) => acc + Number((g as any)[key] || 0), 0)
}

function mergeGroup(groups: TokenGroupCount[][]): TokenGroupCount[] {
  const map = new Map<string, any>()
  for (const list of groups) {
    for (const g of list) {
      const cur = map.get(g.group)
      if (!cur) map.set(g.group, { ...g })
      else {
        cur.requests += Number(g.requests)
        cur.prompt_tokens += Number(g.prompt_tokens)
        cur.completion_tokens += Number(g.completion_tokens)
        cur.total_tokens += Number(g.total_tokens)
      }
    }
  }
  return [...map.values()]
}

function mergeDays(list: WithBot<StatsResponse>[]): AggregateStats['by_day'] {
  const map = new Map<string, any>()
  for (const s of list) {
    for (const d of s.token.by_day) {
      const cur = map.get(d.day) || { requests: 0, total_tokens: 0 }
      cur.requests += Number(d.requests)
      cur.total_tokens += Number(d.total_tokens)
      map.set(d.day, cur)
    }
  }
  return [...map.entries()]
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([day, v]) => ({ day, ...v }))
}

const agg = computed<AggregateStats>(() => {
  const list = topChatStats.value
  const totals = { requests: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
  for (const s of list) {
    totals.requests += Number(s.token.total.requests)
    totals.prompt_tokens += Number(s.token.total.prompt_tokens)
    totals.completion_tokens += Number(s.token.total.completion_tokens)
    totals.total_tokens += Number(s.token.total.total_tokens)
  }
  const perBotAgg: Record<string, AggregateStats['per_bot'][number]> = {}
  for (const s of list) {
    const bot = (s as any).bot_id
    const color = (s as any).bot_color
    const name = (s as any).bot_name
    if (!perBotAgg[bot]) {
      perBotAgg[bot] = {
        bot_id: bot,
        bot_name: name,
        bot_color: color,
        total_tokens: 0,
        requests: 0,
        prompt_tokens: 0,
        completion_tokens: 0,
        chats_count: 0,
        dailySeries: [],
      }
    }
    perBotAgg[bot].total_tokens += Number(s.token.total.total_tokens)
    perBotAgg[bot].requests += Number(s.token.total.requests)
    perBotAgg[bot].prompt_tokens += Number(s.token.total.prompt_tokens)
    perBotAgg[bot].completion_tokens += Number(s.token.total.completion_tokens)
    perBotAgg[bot].chats_count += 1
    for (const d of s.token.by_day) {
      perBotAgg[bot].dailySeries.push({
        day: d.day,
        total_tokens: Number(d.total_tokens),
        requests: Number(d.requests),
      })
    }
  }
  return {
    total: totals,
    by_model: mergeGroup(list.map((s) => s.token.by_model)),
    by_kind: mergeGroup(list.map((s) => s.token.by_kind)),
    by_source_type: mergeGroup(list.map((s) => s.token.by_source_type)),
    by_status: mergeGroup(list.map((s) => s.token.by_status)),
    by_day: mergeDays(list),
    per_bot: Object.values(perBotAgg).sort((a, b) => b.total_tokens - a.total_tokens),
  }
})

// ---------- Chart Options ----------
const primary = computed<MetricKey>(() => store.primaryMetric)
const secondary = computed<MetricKey>(() => store.secondaryMetric)

const trendOption = computed<EChartsOption>(() => {
  const points = agg.value.by_day as any
  const bots = agg.value.per_bot
  if (bots.length > 1 && bots.length <= 8) {
    // 多 bot：按 bot 分解为堆叠面积
    const days: string[] = points.map((p: any) => p.day as string)
    const totalPerDay: Map<string, number> = new Map()
    for (const d of days) {
      const sum = agg.value.per_bot.reduce((a: number, b) => {
        const v = b.dailySeries.find((p) => p.day === d)
        return a + (v ? Number((v as any)[primary.value] || v.total_tokens || 0) : 0)
      }, 0)
      totalPerDay.set(d, sum)
    }
    const botSums = new Map<string, number>()
    for (const b of bots) {
      botSums.set(
        b.bot_id,
        b.dailySeries.reduce((a: number, v: any) => {
          return a + Number(v[primary.value] ?? v.total_tokens ?? 0)
        }, 0),
      )
    }
    const sumBots = [...botSums.values()].reduce((a, b) => a + b, 0) || 1
    const seriesMap = bots.slice(0, 8).map((b) => {
      const ratio = (botSums.get(b.bot_id) || 0) / sumBots
      return {
        name: b.bot_name,
        data: days.map((d: string) => Math.round((totalPerDay.get(d) || 0) * ratio)),
      }
    })
    return buildTrend({
      title: `${METRIC_LABEL[primary.value]} 每日趋势 · 按机器人分解（堆叠面积）`,
      points,
      primary: primary.value,
      secondary: secondary.value,
      seriesMap,
    })
  }
  return buildTrend({
    title: `${METRIC_LABEL[primary.value]} & ${METRIC_LABEL[secondary.value]} 每日趋势（全量 ${bots.length} 机器人）`,
    points,
    primary: primary.value,
    secondary: secondary.value,
    stacked: primary.value === 'total_tokens' && secondary.value !== 'total_tokens',
  })
})

function buildDonutFor(dim: DimensionKey, data: TokenGroupCount[]): EChartsOption {
  const label: Record<DimensionKey, string> = {
    model: '按模型',
    kind: '按类型',
    source_type: '按来源',
    status: '按状态',
  }
  return buildDonut({
    title: `${label[dim]} · ${METRIC_LABEL[primary.value]}`,
    data,
    metric: primary.value,
  })
}

const modelDonut = computed<EChartsOption>(() => buildDonutFor('model', agg.value.by_model))
const kindDonut = computed<EChartsOption>(() => buildDonutFor('kind', agg.value.by_kind))
const sourceDonut = computed<EChartsOption>(() => buildDonutFor('source_type', agg.value.by_source_type))
const statusDonut = computed<EChartsOption>(() => buildDonutFor('status', agg.value.by_status))

const perBotBar = computed<EChartsOption>(() =>
  buildTopBar({
    title: `Top 机器人 · ${METRIC_LABEL[primary.value]}`,
    data: agg.value.per_bot.map((b) => ({
      group: b.bot_name,
      total_tokens: b.total_tokens,
      requests: b.requests,
      prompt_tokens: b.prompt_tokens,
      completion_tokens: b.completion_tokens,
    })) as any,
    metric: primary.value,
  }),
)

const funnelOption = computed<EChartsOption>(() =>
  buildFunnel({
    title: '请求 → Token 转化漏斗（多机器人合计）',
    stages: [
      { name: '请求数', value: agg.value.total.requests },
      { name: 'Prompt Token', value: agg.value.total.prompt_tokens },
      { name: 'Completion Token', value: agg.value.total.completion_tokens },
      { name: '总 Token', value: agg.value.total.total_tokens },
    ],
  }),
)

const radarOption = computed<EChartsOption>(() =>
  buildRadar({
    title: 'Top 模型 · 多指标雷达',
    groups: agg.value.by_model
      .slice()
      .sort((a, b) => Number(b.total_tokens) - Number(a.total_tokens))
      .slice(0, 6) as any,
    metrics: ['requests', 'prompt_tokens', 'completion_tokens', 'total_tokens'] as MetricKey[],
  }),
)

const sunburstOption = computed<EChartsOption>(() => {
  const bots = agg.value.per_bot.slice(0, 6)
  const outer = bots.map((b) => {
    const byKind = topChatStats.value
      .filter((s) => (s as any).bot_id === b.bot_id)
    const kinds = mergeGroup(byKind.map((s) => s.token.by_kind))
    const sumKinds = sumGroupKey(kinds, primary.value) || 1
    const botVal = Number((b as any)[primary.value] || b.total_tokens)
    return {
      name: b.bot_name.length > 14 ? b.bot_name.slice(0, 14) + '…' : b.bot_name,
      children: kinds.map((k) => {
        const ratio = Number((k as any)[primary.value] || 0) / sumKinds
        return { name: k.group, value: Math.max(1, Math.round(botVal * ratio)) }
      }),
    }
  })
  return buildSunburst({
    title: `机器人 × 类型 · ${METRIC_LABEL[primary.value]}（Top 6 机器人）`,
    outer,
  })
})

const heatmapOption = computed<EChartsOption>(() => {
  const bots = agg.value.per_bot.slice(0, 8)
  const days = agg.value.by_day.map((d) => d.day)
  // 近似：按 bot 的 dailySeries 对齐
  const dailyMap = new Map<string, Map<string, number>>()
  for (const b of bots) {
    const dm = new Map<string, number>()
    for (const p of b.dailySeries) {
      dm.set(p.day, Number((p as any)[primary.value] || p.total_tokens || 0))
    }
    dailyMap.set(b.bot_id, dm)
  }
  const data: [number, number, number][] = []
  bots.forEach((b, y) => {
    const dm = dailyMap.get(b.bot_id) || new Map()
    days.forEach((day, x) => {
      data.push([x, y, Math.round(dm.get(day) || 0)])
    })
  })
  return buildHeatmap({
    title: `机器人 × 每日 · ${METRIC_LABEL[primary.value]} 热力`,
    x: days,
    y: bots.map((b) => b.bot_name),
    data,
    xLabel: '日期',
    yLabel: '机器人',
  })
})

// ---------- 下钻点击 ----------
function drillDonut(dim: DimensionKey) {
  return (params: any) => {
    if (!params?.name) return
    store.pushDrill({ dimension: dim, value: params.name, label: params.name })
    ElMessage.info(`已下钻：${dim}=${params.name}`)
  }
}

function onTopBotClick(params: any) {
  if (!params?.name) return
  const matched = agg.value.per_bot.find((b) => b.bot_name === params.name)
  if (matched) {
    store.enterBot(matched.bot_id)
    router.push({ name: 'chats' })
  }
}

// ---------- 加载 ----------
const MAX_CHATS_PER_BOT = 20

async function load() {
  const bots = store.selectedBots
  if (!bots.length) {
    ElMessage.warning('请先选择至少一个机器人（右上角「机器人源」按钮）')
    allChats.value = []
    topChatStats.value = []
    return
  }
  loading.value = true
  try {
    // 1) 每个 bot 并发拉取聊天列表（带指标）
    const listResp = await aggregateCalls<{
      items: (ChatSummary & { metrics?: any })[]
      total: number
    }>(
      bots,
      (api: BotApi, _bot: BotInstance) => {
        return api.listChats({ metrics: true, window: store.window })
      },
      (bot: BotInstance, err: unknown) => {
        ElMessage.warning(`「${bot.name}」拉取聊天列表失败：${(err as any)?.message || err}`)
      },
    )
    // 扁平化为带 bot 标记的 chats
    const flat: WithBot<ChatSummary & { metrics?: any }>[] = []
    for (const r of listResp) {
      for (const c of r.items) flat.push({ ...r, ...c })
    }
    allChats.value = flat

    // 2) 对每个 bot 的 Top N chat 拉取 stats
    const targets: { bot: BotInstance; chatID: string }[] = []
    for (const bot of bots) {
      const botChats = flat
        .filter((c) => c.bot_id === bot.id)
        .sort((a, b) => Number(b.metrics?.total_tokens || 0) - Number(a.metrics?.total_tokens || 0))
        .slice(0, MAX_CHATS_PER_BOT)
      for (const c of botChats) targets.push({ bot, chatID: c.chat_id })
    }
    totalFetches.value = targets.length
    const statsResults = await Promise.allSettled(
      targets.map(({ bot, chatID }) =>
        new BotApi(bot).getStats(chatID, store.window).then((s) => ({
          ...s,
          bot_id: bot.id,
          bot_name: bot.robotName || bot.name,
          bot_color: bot.color,
        })),
      ),
    )
    topChatStats.value = statsResults
      .filter((r) => r.status === 'fulfilled')
      .map((r) => (r as PromiseFulfilledResult<WithBot<StatsResponse>>).value)
  } catch (e: any) {
    ElMessage.error('加载仪表盘失败：' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch([() => store.window, () => store.selectedBotIDs.slice().sort().join(',')], load)
</script>

<template>
  <div>
    <GlobalFilterBar />

    <div v-loading="loading" style="opacity: loading ? 0.6 : 1">
      <!-- KPI 行 -->
      <el-row :gutter="12" style="margin-bottom: 12px">
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="机器人实例数" :value="store.selectedBots.length" />
            <div class="kpi-sub">在线 {{ store.selectedBots.filter(b => b.healthy === true).length }}</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="总 Token" :value="agg.total.total_tokens" />
            <div class="kpi-sub">
              Prompt {{ agg.total.prompt_tokens.toLocaleString() }} ·
              Completion {{ agg.total.completion_tokens.toLocaleString() }}
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="总请求数" :value="agg.total.requests" />
            <div class="kpi-sub">
              单请求平均 Token
              {{ agg.total.requests ? Math.round(agg.total.total_tokens / agg.total.requests) : 0 }}
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="kpi-card">
            <el-statistic title="拉取会话 / Top N" :value="totalFetches" />
            <div class="kpi-sub">每实例最多 {{ MAX_CHATS_PER_BOT }} 个按 Token 排序</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 全局趋势 -->
      <el-card shadow="never" class="panel" style="margin-bottom: 12px">
        <EChart :option="trendOption" height="340px" />
      </el-card>

      <!-- 四维度饼图 -->
      <el-row :gutter="12" style="margin-bottom: 12px">
        <el-col :span="6">
          <el-card shadow="never" class="panel donut-panel">
            <EChart :option="modelDonut" height="300px" @click="drillDonut('model')" />
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="panel donut-panel">
            <EChart :option="kindDonut" height="300px" @click="drillDonut('kind')" />
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="panel donut-panel">
            <EChart :option="sourceDonut" height="300px" @click="drillDonut('source_type')" />
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="never" class="panel donut-panel">
            <EChart :option="statusDonut" height="300px" @click="drillDonut('status')" />
          </el-card>
        </el-col>
      </el-row>

      <!-- 按机器人排行 + 漏斗 -->
      <el-row :gutter="12" style="margin-bottom: 12px">
        <el-col :span="16">
          <el-card shadow="never" class="panel">
            <EChart :option="perBotBar" height="360px" @click="onTopBotClick" />
            <div class="chart-hint">💡 点击条形可跳转到该机器人的会话列表</div>
          </el-card>
        </el-col>
        <el-col :span="8">
          <el-card shadow="never" class="panel">
            <EChart :option="funnelOption" height="360px" />
          </el-card>
        </el-col>
      </el-row>

      <!-- Top 模型雷达 + 机器人×类型旭日 -->
      <el-row :gutter="12" style="margin-bottom: 12px">
        <el-col :span="12">
          <el-card shadow="never" class="panel">
            <EChart :option="radarOption" height="360px" />
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card shadow="never" class="panel">
            <EChart :option="sunburstOption" height="360px" />
          </el-card>
        </el-col>
      </el-row>

      <!-- 机器人 × 日 热力 -->
      <el-card shadow="never" class="panel" style="margin-bottom: 12px">
        <EChart :option="heatmapOption" height="400px" />
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.kpi-card :deep(.el-card__body) {
  padding: 16px 20px;
}
.kpi-sub {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
}
.panel {
  border: 1px solid #f2f6fc;
  position: relative;
}
.panel :deep(.el-card__body) {
  padding: 8px 8px 12px 8px;
}
.donut-panel {
  min-height: 300px;
}
.chart-hint {
  position: absolute;
  right: 16px;
  top: 14px;
  font-size: 12px;
  color: #909399;
}
</style>
