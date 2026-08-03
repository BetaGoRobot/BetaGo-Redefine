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
import UsageBusinessOverview from '../components/UsageBusinessOverview.vue'
import { mergeUsageStats } from '../usage/aggregation'

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

const businessStats = computed(() => {
  if (!topChatStats.value.length) return null
  return mergeUsageStats(topChatStats.value.map((item) => item.token))
})

function onBusinessDrill(dimension: 'business_scene' | 'business_operation', value: string) {
  store.pushDrill({ dimension, value, label: value })
  ElMessage.info(`已下钻：${dimension}=${value}`)
}

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
    business_scene: '按业务场景',
    business_operation: '按业务动作',
    model: '按模型',
    kind: '按类型',
    source_type: '按来源',
    source: '按原始来源',
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
  <div class="dashboard-page">
    <header class="page-intro">
      <div>
        <p class="page-intro__eyebrow">Fleet intelligence</p>
        <h1>运营总览</h1>
        <p class="page-intro__copy">
          聚合所有已选 Bot 的会话、请求与 Token 趋势，从全局信号快速进入异常维度。
        </p>
      </div>
      <div v-if="store.selectedBots.length" class="dashboard-live-note">
        <span aria-hidden="true" />
        {{ store.selectedBots.filter((bot) => bot.healthy === true).length }}
        个实例在线
      </div>
    </header>

    <GlobalFilterBar />

    <section v-if="!store.selectedBots.length" class="dashboard-empty">
      <div class="dashboard-empty__art" aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
      <p class="dashboard-empty__eyebrow">No signal yet</p>
      <h2>先连接一个机器人源</h2>
      <p>
        从上方“机器人源”添加或选择实例。连接后，这里会自动汇总会话、
        Token、模型与请求质量信号。
      </p>
      <div class="dashboard-empty__steps">
        <span><b>01</b> 添加地址</span>
        <span><b>02</b> 完成探活</span>
        <span><b>03</b> 选择分析窗口</span>
      </div>
    </section>

    <div v-else v-loading="loading" class="dashboard-content">
      <UsageBusinessOverview :stats="businessStats" @drill="onBusinessDrill" />

      <header class="technical-section-heading">
        <div>
          <p>Technical dimensions</p>
          <h2>技术维度与运行质量</h2>
        </div>
        <span>保留模型、调用类型、原始来源与状态下钻</span>
      </header>

      <section class="metric-grid" aria-label="关键指标">
        <el-card shadow="never" class="metric-card">
          <span class="metric-card__index">01</span>
          <el-statistic title="机器人实例" :value="store.selectedBots.length" />
          <div class="metric-card__foot">
            在线 {{ store.selectedBots.filter((bot) => bot.healthy === true).length }}
          </div>
        </el-card>
        <el-card shadow="never" class="metric-card">
          <span class="metric-card__index">02</span>
          <el-statistic title="总 Token" :value="agg.total.total_tokens" />
          <div class="metric-card__foot">
              Prompt {{ agg.total.prompt_tokens.toLocaleString() }} ·
              Completion {{ agg.total.completion_tokens.toLocaleString() }}
          </div>
        </el-card>
        <el-card shadow="never" class="metric-card">
          <span class="metric-card__index">03</span>
          <el-statistic title="总请求数" :value="agg.total.requests" />
          <div class="metric-card__foot">
            单请求平均
            {{ agg.total.requests ? Math.round(agg.total.total_tokens / agg.total.requests) : 0 }}
            Token
          </div>
        </el-card>
        <el-card shadow="never" class="metric-card">
          <span class="metric-card__index">04</span>
          <el-statistic title="已拉取会话" :value="totalFetches" />
          <div class="metric-card__foot">
            每实例 Top {{ MAX_CHATS_PER_BOT }} 会话
          </div>
        </el-card>
      </section>

      <el-card shadow="never" class="chart-panel chart-panel--hero">
        <EChart :option="trendOption" height="340px" />
      </el-card>

      <section class="chart-grid chart-grid--four">
        <el-card shadow="never" class="chart-panel">
          <EChart :option="modelDonut" height="300px" @click="drillDonut('model')" />
        </el-card>
        <el-card shadow="never" class="chart-panel">
          <EChart :option="kindDonut" height="300px" @click="drillDonut('kind')" />
        </el-card>
        <el-card shadow="never" class="chart-panel">
          <EChart :option="sourceDonut" height="300px" @click="drillDonut('source_type')" />
        </el-card>
        <el-card shadow="never" class="chart-panel">
          <EChart :option="statusDonut" height="300px" @click="drillDonut('status')" />
        </el-card>
      </section>

      <section class="chart-grid chart-grid--primary">
        <el-card shadow="never" class="chart-panel">
          <EChart :option="perBotBar" height="360px" @click="onTopBotClick" />
          <div class="chart-hint">点击条形进入该 Bot 的会话列表</div>
        </el-card>
        <el-card shadow="never" class="chart-panel">
          <EChart :option="funnelOption" height="360px" />
        </el-card>
      </section>

      <section class="chart-grid chart-grid--split">
        <el-card shadow="never" class="chart-panel">
          <EChart :option="radarOption" height="360px" />
        </el-card>
        <el-card shadow="never" class="chart-panel">
          <EChart :option="sunburstOption" height="360px" />
        </el-card>
      </section>

      <el-card shadow="never" class="chart-panel">
        <EChart :option="heatmapOption" height="400px" />
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.dashboard-page {
  min-height: 70vh;
}

.dashboard-live-note {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.48rem 0.7rem;
  border: 1px solid var(--ops-border);
  border-radius: 999px;
  background: var(--ops-surface);
  color: var(--ops-pine-700);
  font-size: 0.72rem;
  font-weight: 750;
}

.dashboard-live-note span {
  width: 0.48rem;
  height: 0.48rem;
  border-radius: 50%;
  background: var(--ops-teal);
  box-shadow: 0 0 0 0.22rem rgb(74 143 121 / 14%);
}

.dashboard-empty {
  position: relative;
  display: grid;
  justify-items: center;
  min-height: 28rem;
  overflow: hidden;
  padding: clamp(2rem, 8vw, 5rem) 1.5rem;
  border: 1px solid var(--ops-border);
  border-radius: var(--ops-radius-lg);
  background:
    radial-gradient(circle at 50% 0%, rgb(215 255 115 / 22%), transparent 18rem),
    var(--ops-surface);
  box-shadow: var(--ops-shadow-md);
  text-align: center;
}

.dashboard-empty::before {
  position: absolute;
  inset: 0;
  opacity: 0.32;
  background-image:
    linear-gradient(rgb(20 59 54 / 6%) 1px, transparent 1px),
    linear-gradient(90deg, rgb(20 59 54 / 6%) 1px, transparent 1px);
  background-size: 2.4rem 2.4rem;
  content: "";
  mask-image: linear-gradient(to bottom, black, transparent 78%);
  pointer-events: none;
}

.dashboard-empty > * {
  position: relative;
}

.dashboard-empty__art {
  display: flex;
  align-items: flex-end;
  gap: 0.35rem;
  width: 5rem;
  height: 4.5rem;
  margin-bottom: 1.2rem;
  padding: 0.8rem;
  border-radius: 1.25rem 1.25rem 1.25rem 0.4rem;
  background: var(--ops-pine-900);
  box-shadow: 0 1rem 2rem rgb(20 59 54 / 20%);
}

.dashboard-empty__art span {
  flex: 1;
  border-radius: 999px 999px 0.2rem 0.2rem;
  background: var(--ops-lime);
}

.dashboard-empty__art span:nth-child(1) {
  height: 42%;
  opacity: 0.58;
}

.dashboard-empty__art span:nth-child(2) {
  height: 78%;
}

.dashboard-empty__art span:nth-child(3) {
  height: 58%;
  opacity: 0.78;
}

.dashboard-empty__eyebrow {
  margin: 0 0 0.45rem;
  color: var(--ops-teal);
  font-size: 0.67rem;
  font-weight: 850;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.dashboard-empty h2 {
  margin: 0;
  color: var(--ops-pine-950);
  font-size: clamp(1.45rem, 3vw, 2rem);
  letter-spacing: -0.03em;
}

.dashboard-empty > p:not(.dashboard-empty__eyebrow) {
  max-width: 34rem;
  margin: 0.8rem 0 1.5rem;
  color: var(--ops-muted);
  font-size: 0.86rem;
  line-height: 1.7;
}

.dashboard-empty__steps {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0.55rem;
}

.dashboard-empty__steps span {
  padding: 0.48rem 0.7rem;
  border: 1px solid var(--ops-border);
  border-radius: 999px;
  background: rgb(255 254 250 / 78%);
  color: #52615c;
  font-size: 0.7rem;
  font-weight: 650;
}

.dashboard-empty__steps b {
  margin-right: 0.3rem;
  color: var(--ops-teal);
  font-family: var(--ops-font-mono);
  font-size: 0.62rem;
}

.dashboard-content {
  display: grid;
  gap: 0.9rem;
}

.technical-section-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: 0.75rem;
  padding-top: 1rem;
  border-top: 1px solid var(--ops-border);
}

.technical-section-heading p {
  margin: 0 0 0.2rem;
  color: var(--ops-teal);
  font-family: var(--ops-font-mono);
  font-size: 0.64rem;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.technical-section-heading h2 {
  margin: 0;
  color: var(--ops-pine-950);
  font-size: 1.15rem;
}

.technical-section-heading > span {
  color: var(--ops-muted);
  font-size: 0.7rem;
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  padding: 0.35rem;
  border: 1px solid var(--ops-pine-800);
  border-radius: var(--ops-radius-md);
  background: var(--ops-pine-900);
  box-shadow: var(--ops-shadow-md);
}

.metric-card {
  position: relative;
  min-width: 0;
  border: 0;
  border-right: 1px solid rgb(255 255 255 / 10%);
  border-radius: 0;
  background: transparent;
}

.metric-card:last-child {
  border-right: 0;
}

.metric-card :deep(.el-card__body) {
  padding: 1rem 1.15rem;
}

.metric-card :deep(.el-statistic__head),
.metric-card__foot {
  color: #a9beb7;
  font-size: 0.72rem;
}

.metric-card :deep(.el-statistic__number) {
  color: #fff;
  font-size: clamp(1.35rem, 2vw, 1.8rem);
  font-variant-numeric: tabular-nums;
  font-weight: 760;
}

.metric-card__index {
  position: absolute;
  top: 0.9rem;
  right: 1rem;
  color: rgb(215 255 115 / 48%);
  font-family: var(--ops-font-mono);
  font-size: 0.58rem;
  font-weight: 800;
}

.metric-card__foot {
  margin-top: 0.4rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chart-grid {
  display: grid;
  gap: 0.9rem;
}

.chart-grid--four {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.chart-grid--primary {
  grid-template-columns: minmax(0, 2fr) minmax(18rem, 1fr);
}

.chart-grid--split {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.chart-panel {
  position: relative;
  min-width: 0;
  overflow: hidden;
  border-color: var(--ops-border);
  background: var(--ops-surface);
  box-shadow: var(--ops-shadow-sm);
}

.chart-panel :deep(.el-card__body) {
  padding: 0.55rem;
}

.chart-panel--hero :deep(.el-card__body) {
  padding: 0.75rem;
}

.chart-hint {
  position: absolute;
  top: 0.9rem;
  right: 1rem;
  color: var(--ops-muted-light);
  font-size: 0.68rem;
}

@media (max-width: 1199px) {
  .chart-grid--four {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 1023px) {
  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .metric-card:nth-child(2) {
    border-right: 0;
  }

  .metric-card:nth-child(-n + 2) {
    border-bottom: 1px solid rgb(255 255 255 / 10%);
  }

  .chart-grid--primary {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 767px) {
  .dashboard-live-note {
    display: none;
  }

  .dashboard-empty {
    min-height: 24rem;
    padding-inline: 1rem;
  }

  .metric-grid,
  .chart-grid--four,
  .chart-grid--split {
    grid-template-columns: 1fr;
  }

  .metric-card,
  .metric-card:nth-child(2) {
    border-right: 0;
    border-bottom: 1px solid rgb(255 255 255 / 10%);
  }

  .metric-card:last-child {
    border-bottom: 0;
  }

  .chart-hint {
    position: static;
    padding: 0 0.75rem 0.75rem;
  }
}
</style>
