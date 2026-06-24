<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import { BotApi } from '../api/client'
import type {
  ChatActivity,
  ChatCommands,
  ChatCommandTrend,
  ChatDetail as ChatDetailType,
  ChatKeywords,
  ChatMember,
  ChatMessageKinds,
  ChatTopicTrend,
  ChatTopMentions,
  ChatTopSenders,
  ConfigView,
  FeatureView,
  StatsResponse,
} from '../api/types'
import {
  useFilterStore,
  METRIC_LABEL,
  DIMENSION_LABEL,
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

const props = defineProps<{ chatID: string; botID?: string }>()
const store = useFilterStore()

const bot = computed<BotInstance | undefined>(() => {
  const id = props.botID || store.currentBotID || store.selectedBots[0]?.id
  if (!id) return undefined
  return store.getBot(id)
})
function resolveBot(): BotInstance {
  if (!bot.value) throw new Error('未指定机器人，请先在右上角选择')
  return bot.value
}
const botLabel = computed(() => {
  const b = bot.value
  return b ? (b.robotName || b.name) : ''
})
const detail = ref<ChatDetailType | null>(null)
const activeTab = ref('stats')
const stats = ref<StatsResponse | null>(null)
const statsLoading = ref(false)
const activity = ref<ChatActivity | null>(null)
const activityLoading = ref(false)
const activityError = ref('')
const keywords = ref<ChatKeywords | null>(null)
const keywordsLoading = ref(false)
const keywordsError = ref('')
const commands = ref<ChatCommands | null>(null)
const commandsLoading = ref(false)
const commandsError = ref('')
const topSenders = ref<ChatTopSenders | null>(null)
const topSendersLoading = ref(false)
const topSendersError = ref('')
const messageKinds = ref<ChatMessageKinds | null>(null)
const messageKindsLoading = ref(false)
const messageKindsError = ref('')
const commandTrend = ref<ChatCommandTrend | null>(null)
const commandTrendLoading = ref(false)
const commandTrendError = ref('')
const topMentions = ref<ChatTopMentions | null>(null)
const topMentionsLoading = ref(false)
const topMentionsError = ref('')
const topicTrend = ref<ChatTopicTrend | null>(null)
const topicTrendLoading = ref(false)
const topicTrendError = ref('')
const features = ref<FeatureView[]>([])
const featLoading = ref(false)
const configs = ref<ConfigView[]>([])
const cfgLoading = ref(false)
const drafts = ref<Record<string, any>>({})
const members = ref<ChatMember[]>([])
const memberLoading = ref(false)
const memberKeyword = ref('')
const viewMode = ref<'overview' | 'deep'>('overview')
const focusDimension = ref<DimensionKey>('model')
const focusValue = ref<string | null>(null)
function clearFocus() {
  focusValue.value = null
  viewMode.value = 'overview'
}

async function loadStats() {
  statsLoading.value = true
  try {
    stats.value = await new BotApi(resolveBot()).getStats(props.chatID, store.window)
  } catch (e: any) {
    ElMessage.error('加载统计失败：' + (e?.response?.data?.error || e.message))
  } finally {
    statsLoading.value = false
  }
}
async function loadActivity() {
  activityLoading.value = true
  activityError.value = ''
  try {
    activity.value = await new BotApi(resolveBot()).getActivity(props.chatID, store.window)
  } catch (e: any) {
    activity.value = null
    activityError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    activityLoading.value = false
  }
}
async function loadKeywords() {
  keywordsLoading.value = true
  keywordsError.value = ''
  try {
    keywords.value = await new BotApi(resolveBot()).getKeywords(props.chatID, store.window, 80)
  } catch (e: any) {
    keywords.value = null
    keywordsError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    keywordsLoading.value = false
  }
}
async function loadCommands() {
  commandsLoading.value = true
  commandsError.value = ''
  try {
    commands.value = await new BotApi(resolveBot()).getCommands(props.chatID, store.window, 20)
  } catch (e: any) {
    commands.value = null
    commandsError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    commandsLoading.value = false
  }
}
async function loadTopSenders() {
  topSendersLoading.value = true
  topSendersError.value = ''
  try {
    topSenders.value = await new BotApi(resolveBot()).getTopSenders(props.chatID, store.window, 20)
  } catch (e: any) {
    topSenders.value = null
    topSendersError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    topSendersLoading.value = false
  }
}
async function loadMessageKinds() {
  messageKindsLoading.value = true
  messageKindsError.value = ''
  try {
    messageKinds.value = await new BotApi(resolveBot()).getMessageKinds(props.chatID, store.window)
  } catch (e: any) {
    messageKinds.value = null
    messageKindsError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    messageKindsLoading.value = false
  }
}
async function loadCommandTrend() {
  commandTrendLoading.value = true
  commandTrendError.value = ''
  try {
    commandTrend.value = await new BotApi(resolveBot()).getCommandTrend(props.chatID, store.window)
  } catch (e: any) {
    commandTrend.value = null
    commandTrendError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    commandTrendLoading.value = false
  }
}
async function loadTopMentions() {
  topMentionsLoading.value = true
  topMentionsError.value = ''
  try {
    topMentions.value = await new BotApi(resolveBot()).getTopMentions(props.chatID, store.window, 20, 500)
  } catch (e: any) {
    topMentions.value = null
    topMentionsError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    topMentionsLoading.value = false
  }
}
async function loadTopicTrend() {
  topicTrendLoading.value = true
  topicTrendError.value = ''
  try {
    topicTrend.value = await new BotApi(resolveBot()).getTopicTrend(props.chatID, store.window)
  } catch (e: any) {
    topicTrend.value = null
    topicTrendError.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    topicTrendLoading.value = false
  }
}
async function loadFeatures() {
  featLoading.value = true
  try {
    features.value = (await new BotApi(resolveBot()).listFeatures(props.chatID)).items || []
  } catch (e: any) {
    ElMessage.error('加载功能开关失败：' + (e?.response?.data?.error || e.message))
  } finally {
    featLoading.value = false
  }
}
async function loadConfigs() {
  cfgLoading.value = true
  try {
    configs.value = (await new BotApi(resolveBot()).listConfigs(props.chatID)).items || []
    drafts.value = {}
    for (const c of configs.value) {
      drafts.value[c.key] = c.value_type === 'int' ? Number(c.value || 0) : c.value
    }
  } catch (e: any) {
    ElMessage.error('加载配置失败：' + (e?.response?.data?.error || e.message))
  } finally {
    cfgLoading.value = false
  }
}
async function loadMembers() {
  memberLoading.value = true
  try {
    members.value = (await new BotApi(resolveBot()).listMembers(props.chatID)).items || []
  } catch (e: any) {
    ElMessage.error('加载群成员失败：' + (e?.response?.data?.error || e.message))
  } finally {
    memberLoading.value = false
  }
}
function filteredMembers() {
  const kw = memberKeyword.value.trim().toLowerCase()
  if (!kw) return members.value
  return members.value.filter(
    (m) => m.name.toLowerCase().includes(kw) || m.open_id.toLowerCase().includes(kw),
  )
}
async function toggleFeature(f: FeatureView, val: boolean) {
  try {
    await new BotApi(resolveBot()).setFeature(props.chatID, f.name, val)
    f.enabled = val
    ElMessage.success(`${f.name} 已${val ? '启用' : '禁用'}`)
  } catch (e: any) {
    ElMessage.error('保存失败：' + (e?.response?.data?.error || e.message))
    await loadFeatures()
  }
}
async function saveConfig(c: ConfigView) {
  try {
    const value = String(drafts.value[c.key])
    await new BotApi(resolveBot()).setConfig(props.chatID, c.key, value)
    c.value = value
    ElMessage.success(c.key + ' 已保存')
  } catch (e: any) {
    ElMessage.error('保存失败：' + (e?.response?.data?.error || e.message))
  }
}
async function resetConfig(c: ConfigView) {
  try {
    await new BotApi(resolveBot()).deleteConfig(props.chatID, c.key)
    ElMessage.success(c.key + ' 已重置为默认')
    await loadConfigs()
  } catch (e: any) {
    ElMessage.error('重置失败：' + (e?.response?.data?.error || e.message))
  }
}
function onDonutClick(dim: DimensionKey) {
  return (params: any) => {
    if (!params?.name) return
    focusDimension.value = dim
    focusValue.value = params.name
    viewMode.value = 'deep'
    ElMessage.success(`聚焦：${DIMENSION_LABEL[dim]} = ${params.name}`)
  }
}
function onTopClick(params: any) {
  if (!params?.name) return
  focusDimension.value = 'model'
  focusValue.value = params.name
  viewMode.value = 'deep'
}
const primary = computed<MetricKey>(() => store.primaryMetric)
const secondary = computed<MetricKey>(() => store.secondaryMetric)
function getGroup(dim: DimensionKey) {
  switch (dim) {
    case 'model': return stats.value?.token.by_model || []
    case 'kind': return stats.value?.token.by_kind || []
    case 'source_type': return stats.value?.token.by_source_type || []
    case 'status': return stats.value?.token.by_status || []
  }
}
const trendOption = computed<EChartsOption>(() => {
  const base = stats.value?.token.by_day || []
  if (viewMode.value === 'deep' && focusValue.value) {
    return buildTrend({
      title: `${DIMENSION_LABEL[focusDimension.value]} = ${focusValue.value} · ${METRIC_LABEL[primary.value]} 趋势`,
      points: base as any,
      primary: primary.value,
      secondary: secondary.value,
    })
  }
  const byGroup = getGroup(focusDimension.value)
  if (byGroup.length > 1 && byGroup.length <= 8) {
    const days = base.map((d) => d.day)
    const totalPerDay = new Map<string, number>(base.map((d) => [d.day, Number(d.total_tokens || 0)]))
    const groupSum = byGroup.reduce((a, b) => a + Number((b as any)[primary.value] || 0), 0) || 1
    const seriesMap = byGroup.slice(0, 8).map((g) => {
      const weight = Number((g as any)[primary.value] || 0) / groupSum
      return {
        name: g.group,
        data: days.map((d) => Math.round((totalPerDay.get(d) || 0) * weight)),
      }
    })
    return buildTrend({
      title: `每日趋势 · 按${DIMENSION_LABEL[focusDimension.value]}分解（堆叠面积）`,
      points: base as any,
      primary: primary.value,
      secondary: secondary.value,
      seriesMap,
    })
  }
  return buildTrend({
    title: `${METRIC_LABEL[primary.value]} & ${METRIC_LABEL[secondary.value]} 每日趋势`,
    points: base as any,
    primary: primary.value,
    secondary: secondary.value,
    stacked: primary.value === 'total_tokens',
  })
})
const modelDonut = computed<EChartsOption>(() =>
  buildDonut({ title: `按模型 · ${METRIC_LABEL[primary.value]}`, data: stats.value?.token.by_model || [], metric: primary.value }),
)
const kindDonut = computed<EChartsOption>(() =>
  buildDonut({ title: `按类型 · ${METRIC_LABEL[primary.value]}`, data: stats.value?.token.by_kind || [], metric: primary.value }),
)
const sourceDonut = computed<EChartsOption>(() =>
  buildDonut({ title: `按来源 · ${METRIC_LABEL[primary.value]}`, data: stats.value?.token.by_source_type || [], metric: primary.value }),
)
const statusDonut = computed<EChartsOption>(() =>
  buildDonut({ title: `按状态 · ${METRIC_LABEL[primary.value]}`, data: stats.value?.token.by_status || [], metric: primary.value }),
)
const funnelOption = computed<EChartsOption>(() => {
  const t = stats.value?.token.total
  return buildFunnel({
    title: '请求 → Token 转化漏斗',
    stages: [
      { name: '请求数', value: Number(t?.requests || 0) },
      { name: 'Prompt Token', value: Number(t?.prompt_tokens || 0) },
      { name: 'Completion Token', value: Number(t?.completion_tokens || 0) },
      { name: '总 Token', value: Number(t?.total_tokens || 0) },
    ],
  })
})
const radarOption = computed<EChartsOption>(() =>
  buildRadar({
    title: 'Top 模型 · 多指标雷达',
    groups: stats.value?.token.by_model.slice().sort((a, b) => Number(b.total_tokens) - Number(a.total_tokens)).slice(0, 6) || [],
    metrics: ['requests', 'prompt_tokens', 'completion_tokens', 'total_tokens'] as MetricKey[],
  }),
)
const sunburstOption = computed<EChartsOption>(() => {
  const byModel = stats.value?.token.by_model || []
  const byKind = stats.value?.token.by_kind || []
  const outer = byModel.slice(0, 8).map((m) => {
    const totalM = Number((m as any)[primary.value] || 0) || 1
    const sumKind = byKind.reduce((a, k) => a + Number((k as any)[primary.value] || 0), 0) || 1
    return {
      name: m.group,
      children: byKind.map((k) => {
        const ratio = Number((k as any)[primary.value] || 0) / sumKind
        return { name: k.group, value: Math.round(totalM * ratio) }
      }),
    }
  })
  return buildSunburst({ title: `模型 × 类型 · ${METRIC_LABEL[primary.value]}`, outer })
})
const heatmapOption = computed<EChartsOption>(() => {
  const days = (stats.value?.token.by_day || []).map((d) => d.day)
  const models = (stats.value?.token.by_model || [])
    .slice().sort((a, b) => Number(b.total_tokens) - Number(a.total_tokens)).slice(0, 8).map((m) => m.group)
  const totalsDay = new Map<string, number>((stats.value?.token.by_day || []).map((d) => [d.day, Number(d.total_tokens || 0)]))
  const modelTotal = new Map<string, number>((stats.value?.token.by_model || []).map((m) => [m.group, Number((m as any)[primary.value] || 0)]))
  const sumModels = [...modelTotal.values()].reduce((a, b) => a + b, 0) || 1
  const data: [number, number, number][] = []
  models.forEach((m, y) => {
    const mRatio = (modelTotal.get(m) || 0) / sumModels
    days.forEach((d, x) => {
      const val = (totalsDay.get(d) || 0) * mRatio
      data.push([x, y, Math.round(val)])
    })
  })
  return buildHeatmap({
    title: `Top 模型 × 日 · ${METRIC_LABEL[primary.value]} 热力`,
    x: days, y: models, data,
    xLabel: '日期', yLabel: '模型',
  })
})
const topModelBar = computed<EChartsOption>(() =>
  buildTopBar({ title: `Top 模型 · ${METRIC_LABEL[primary.value]}`, data: stats.value?.token.by_model || [], metric: primary.value }),
)

// 周内小时活跃度热力图：dow=0..6（周一..周日）× hour=0..23。
// 后端固定返回 168 个桶，缺数据时 count=0。
const ACTIVITY_DOW_LABELS = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const ACTIVITY_HOUR_LABELS = Array.from({ length: 24 }, (_, h) => `${h.toString().padStart(2, '0')}时`)
const activityHeatmap = computed<EChartsOption>(() => {
  const buckets = activity.value?.hour_of_week || []
  const data: [number, number, number][] = buckets.map((b) => [b.hour, b.dow, b.count])
  return buildHeatmap({
    title: `发言活跃度 · 周内小时 (UTC+8) · 共 ${(activity.value?.total || 0).toLocaleString()} 条`,
    x: ACTIVITY_HOUR_LABELS,
    y: ACTIVITY_DOW_LABELS,
    data,
    xLabel: '小时',
    yLabel: '星期',
  })
})

// 关键词 Top-N：后端按词性过滤了名词/动词/形容词等实词，count 为命中文档数。
// 这里渲染成横向条形图，预留 wordcloud 接入空间但不引第三方依赖。
const keywordsBar = computed<EChartsOption>(() => {
  const items = (keywords.value?.items || []).slice(0, 30)
  const sorted = items.slice().sort((a, b) => a.count - b.count)
  return {
    title: {
      text: `Top 关键词 · ${sorted.length} 词（按文档数）`,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      valueFormatter: (v: any) => (typeof v === 'number' ? v.toLocaleString() + ' 条' : v),
    },
    grid: { left: 110, right: 32, top: 48, bottom: 24 },
    xAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: {
      type: 'category',
      data: sorted.map((k) => k.word),
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        barWidth: 12,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 1,
            y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(94,216,166,0.35)' },
              { offset: 1, color: '#5AD8A6' },
            ],
          },
          borderRadius: [0, 6, 6, 0],
        },
        data: sorted.map((k) => k.count),
      },
    ],
  }
})

// 命令使用 Top-N：横向条形图 + 标题里展示命令调用总次数。
// 数据维度是命令调用文档数，与关键词共用同一种视觉风格但用不同色。
const commandsBar = computed<EChartsOption>(() => {
  const items = (commands.value?.items || []).slice(0, 20)
  const sorted = items.slice().sort((a, b) => a.count - b.count)
  const total = commands.value?.total || 0
  return {
    title: {
      text: `命令调用 · ${sorted.length} 项 · 共 ${total.toLocaleString()} 次`,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      valueFormatter: (v: any) => {
        if (typeof v !== 'number') return v
        const pct = total > 0 ? ` (${((v / total) * 100).toFixed(1)}%)` : ''
        return `${v.toLocaleString()} 次${pct}`
      },
    },
    grid: { left: 140, right: 48, top: 48, bottom: 24 },
    xAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: {
      type: 'category',
      data: sorted.map((c) => c.command),
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        barWidth: 12,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 1,
            y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(255,152,69,0.35)' },
              { offset: 1, color: '#FF9845' },
            ],
          },
          borderRadius: [0, 6, 6, 0],
        },
        data: sorted.map((c) => c.count),
      },
    ],
  }
})

// Top 发言用户：横向条形图 + tooltip 展示在群内占比。
// y 轴为用户名（user_name 缺失时回落 open_id），便于直观识别。
const sendersBar = computed<EChartsOption>(() => {
  const items = (topSenders.value?.items || []).slice(0, 20)
  const sorted = items.slice().sort((a, b) => a.count - b.count)
  const total = topSenders.value?.total || 0
  return {
    title: {
      text: `Top 发言用户 · ${sorted.length} 人 · 群内共 ${total.toLocaleString()} 条`,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      formatter: (params: any) => {
        const arr = Array.isArray(params) ? params : [params]
        const p = arr[0]
        if (!p) return ''
        const idx = p.dataIndex as number
        const item = sorted[idx]
        if (!item) return ''
        const pct = total > 0 ? ((item.count / total) * 100).toFixed(1) + '%' : '-'
        return `${item.user_name}<br/><span style="color:#909399;font-size:11px">${item.open_id}</span><br/>${item.count.toLocaleString()} 条 · ${pct}`
      },
    },
    grid: { left: 140, right: 56, top: 48, bottom: 24 },
    xAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: {
      type: 'category',
      data: sorted.map((s) => s.user_name),
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        barWidth: 12,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 1,
            y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(148,95,185,0.35)' },
              { offset: 1, color: '#945FB9' },
            ],
          },
          borderRadius: [0, 6, 6, 0],
        },
        data: sorted.map((s) => s.count),
      },
    ],
  }
})

// 消息类型分布：复用 buildDonut，把 MessageKindCount 适配成 TokenGroupCount。
// 仅 total_tokens 一列承载 count，其它字段补 0；buildDonut 仅按指定 metric 取值。
const messageKindsDonut = computed<EChartsOption>(() => {
  const items = messageKinds.value?.items || []
  const data = items.map((k) => ({
    group: k.kind,
    total_tokens: k.count,
    requests: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
  }))
  return buildDonut({
    title: `消息类型 · 共 ${(messageKinds.value?.total || 0).toLocaleString()} 条`,
    data,
    metric: 'total_tokens',
  })
})

// 命令使用 vs 总消息每日时序：双线 + 命令占比折线（右侧 y 轴），
// 用一份后端聚合一次性出"总量、命令量、命令占比" 三条信号。
const commandTrendOption = computed<EChartsOption>(() => {
  const days = commandTrend.value?.days || []
  const total = commandTrend.value?.total || []
  const commands = commandTrend.value?.commands || []
  const ratio = days.map((_, i) => {
    const t = total[i] || 0
    if (!t) return 0
    return Math.round((commands[i] / t) * 1000) / 10 // 0.1% 精度
  })
  return {
    title: {
      text: '命令使用 · 每日总量 vs 命令量',
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
    },
    legend: {
      top: 8,
      right: 16,
      icon: 'roundRect',
      textStyle: { color: '#606266' },
    },
    grid: { left: 56, right: 64, top: 56, bottom: 36 },
    xAxis: {
      type: 'category',
      data: days,
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: [
      {
        type: 'value',
        name: '消息数',
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
        axisLabel: { color: '#606266', fontSize: 11 },
      },
      {
        type: 'value',
        name: '命令占比',
        position: 'right',
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { formatter: '{value}%', color: '#606266', fontSize: 11 },
      },
    ],
    series: [
      {
        name: '总消息',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: '#5B8FF9' },
        areaStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 0,
            y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(91,143,249,0.25)' },
              { offset: 1, color: 'rgba(91,143,249,0)' },
            ],
          },
        },
        data: total,
      },
      {
        name: '命令调用',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: '#FF9845' },
        data: commands,
      },
      {
        name: '命令占比 %',
        type: 'line',
        yAxisIndex: 1,
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 1.5, color: '#945FB9', type: 'dashed' },
        data: ratio,
      },
    ],
  }
})

// 词性主题趋势：堆叠面积图。后端把细分词性折叠为名词/动词/形容词/其它实词四类。
const TOPIC_PALETTE = ['#5B8FF9', '#5AD8A6', '#F6BD16', '#945FB9']
const topicTrendOption = computed<EChartsOption>(() => {
  const days = topicTrend.value?.days || []
  const series = topicTrend.value?.series || []
  return {
    color: TOPIC_PALETTE,
    title: {
      text: `词性主题趋势 · ${days.length} 天 × ${series.length} 类`,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
    },
    legend: {
      top: 8,
      right: 16,
      icon: 'roundRect',
      textStyle: { color: '#606266' },
    },
    grid: { left: 56, right: 32, top: 56, bottom: 36 },
    xAxis: {
      type: 'category',
      data: days,
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    series: series.map((s) => ({
      name: s.tag,
      type: 'line',
      stack: 'topic',
      smooth: true,
      showSymbol: false,
      lineStyle: { width: 1.2 },
      areaStyle: { opacity: 0.5 },
      data: s.values,
    })),
  }
})

// 被 @ 用户排行：与 sendersBar 同一种横向条形结构，色系换洋红用作区分。
// tooltip 同时显示样本数与是否被截断（窗口内 mentions 非空消息数 > sampleSize）。
const mentionsBar = computed<EChartsOption>(() => {
  const items = (topMentions.value?.items || []).slice(0, 20)
  const sorted = items.slice().sort((a, b) => a.count - b.count)
  const sampled = topMentions.value?.sampled || 0
  const truncated = topMentions.value?.truncated || false
  const titleSuffix = truncated ? `（取样 ${sampled} 条，含截断）` : `（取样 ${sampled} 条）`
  return {
    title: {
      text: `被 @ 用户 · ${sorted.length} 人 ${titleSuffix}`,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      formatter: (params: any) => {
        const arr = Array.isArray(params) ? params : [params]
        const p = arr[0]
        if (!p) return ''
        const item = sorted[p.dataIndex as number]
        if (!item) return ''
        return `${item.user_name}<br/><span style="color:#909399;font-size:11px">${item.open_id}</span><br/>${item.count.toLocaleString()} 次`
      },
    },
    grid: { left: 140, right: 56, top: 48, bottom: 24 },
    xAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    yAxis: {
      type: 'category',
      data: sorted.map((m) => m.user_name),
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { color: '#606266', fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        barWidth: 12,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 1,
            y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(255,153,195,0.35)' },
              { offset: 1, color: '#FF99C3' },
            ],
          },
          borderRadius: [0, 6, 6, 0],
        },
        data: sorted.map((m) => m.count),
      },
    ],
  }
})
const deepCrossDim = computed<DimensionKey>(() => {
  const order: DimensionKey[] = ['kind', 'source_type', 'status', 'model']
  return order.find((d) => d !== focusDimension.value) || 'kind'
})
const deepCrossOption = computed<EChartsOption>(() => {
  const baseGroup = getGroup(deepCrossDim.value)
  return buildTopBar({
    title: `${DIMENSION_LABEL[focusDimension.value]}=${focusValue.value || '-'} 下的 ${DIMENSION_LABEL[deepCrossDim.value]} 分布`,
    data: baseGroup,
    metric: primary.value,
  })
})

async function initAll() {
  if (!bot.value) return
  try {
    detail.value = await new BotApi(resolveBot()).getChat(props.chatID)
    store.enterChat(bot.value!.id, props.chatID, detail.value?.name || props.chatID)
  } catch (e: any) {
    console.warn('getChat failed', e)
    ElMessage.warning('会话元信息不可用：' + (e?.message || e))
    store.enterBot(bot.value.id)
  }
  await Promise.all([
    loadStats(),
    loadFeatures(),
    loadConfigs(),
    loadMembers(),
    loadActivity(),
    loadKeywords(),
    loadCommands(),
    loadTopSenders(),
    loadMessageKinds(),
    loadCommandTrend(),
    loadTopMentions(),
    loadTopicTrend(),
  ])
}

onMounted(initAll)
watch(() => store.window, () => {
  loadStats()
  loadActivity()
  loadKeywords()
  loadCommands()
  loadTopSenders()
  loadMessageKinds()
  loadCommandTrend()
  loadTopMentions()
  loadTopicTrend()
})
watch([() => props.chatID, () => props.botID, () => bot.value?.id], async () => {
  if (!props.chatID) return
  detail.value = null
  focusValue.value = null
  viewMode.value = 'overview'
  await initAll()
})
</script>

<template>
  <div>
    <el-page-header @back="$router.push({ name: 'chats' })" style="margin-bottom: 8px">
      <template #content>
        <div style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap">
          <el-tag v-if="botLabel" type="info" effect="plain">
            <span
              class="bot-dot"
              :style="{ background: bot?.color || '#909399' }"
            />
            <span style="margin-left: 4px">{{ botLabel }}</span>
          </el-tag>
          <el-avatar v-if="detail?.avatar" :src="detail.avatar" :size="32" shape="square" />
          <h2 style="margin: 0; font-size: 18px">{{ detail?.name || '会话详情' }}</h2>
          <el-tag v-if="detail?.chat_status === 'p2p'" type="info" size="small" effect="plain">单聊</el-tag>
          <el-tag v-else-if="detail?.chat_status === 'group'" type="success" size="small" effect="plain">
            群聊 · {{ detail?.member_count }}人
          </el-tag>
          <el-tag v-else size="small" type="warning" effect="dark">元信息不可用</el-tag>
          <el-text type="info" size="small">{{ chatID }}</el-text>
        </div>
      </template>
    </el-page-header>

    <GlobalFilterBar />

    <el-tabs v-model="activeTab">
      <el-tab-pane label="📊 统计 & Token" name="stats">
        <div
          v-if="viewMode === 'deep' && focusValue"
          style="margin-bottom: 12px; padding: 10px 14px; background: #ecf5ff; border: 1px solid #d9ecff; border-radius: 6px; display: flex; gap: 10px; align-items: center; flex-wrap: wrap"
        >
          <span>🔍 正在下钻：
            <el-tag type="info" effect="dark">{{ DIMENSION_LABEL[focusDimension] }} = {{ focusValue }}</el-tag>
          </span>
          <el-button size="small" @click="clearFocus">返回总览</el-button>
          <span style="flex: 1" />
          <span style="color: #909399; font-size: 12px">💡 点击其他饼图/条形可切换聚焦维度</span>
        </div>

        <div style="display: flex; gap: 12px; margin-bottom: 12px; align-items: center; flex-wrap: wrap">
          <el-radio-group v-model="viewMode">
            <el-radio-button value="overview">总览模式</el-radio-button>
            <el-radio-button value="deep">下钻模式</el-radio-button>
          </el-radio-group>
          <template v-if="viewMode === 'overview'">
            <span style="color: #909399">堆叠分解维度</span>
            <el-select v-model="focusDimension" style="width: 140px">
              <el-option v-for="d of (['model','kind','source_type','status'] as DimensionKey[])" :key="d" :value="d" :label="DIMENSION_LABEL[d]" />
            </el-select>
          </template>
          <template v-else>
            <span style="color: #909399">聚焦维度</span>
            <el-select v-model="focusDimension" style="width: 140px">
              <el-option v-for="d of (['model','kind','source_type','status'] as DimensionKey[])" :key="d" :value="d" :label="DIMENSION_LABEL[d]" />
            </el-select>
            <el-select v-model="focusValue" placeholder="选择聚焦值" filterable style="width: 220px" clearable>
              <el-option
                v-for="g of getGroup(focusDimension)"
                :key="g.group"
                :value="g.group"
                :label="g.group + ' (' + Number((g as any)[primary]).toLocaleString() + ')'"
              />
            </el-select>
          </template>
          <el-button :loading="statsLoading" type="primary" @click="loadStats">刷新</el-button>
        </div>

        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="6">
            <el-card shadow="hover" class="kpi-card">
              <el-statistic title="请求数" :value="Number(stats?.token.total.requests || 0)" />
            </el-card>
          </el-col>
          <el-col :span="6">
            <el-card shadow="hover" class="kpi-card">
              <el-statistic title="总 Token" :value="Number(stats?.token.total.total_tokens || 0)" />
              <div class="kpi-sub">
                Prompt {{ Number(stats?.token.total.prompt_tokens || 0).toLocaleString() }} ·
                Completion {{ Number(stats?.token.total.completion_tokens || 0).toLocaleString() }}
              </div>
            </el-card>
          </el-col>
          <el-col :span="6">
            <el-card shadow="hover" class="kpi-card">
              <el-statistic
                title="平均每请求 Token"
                :value="stats?.token.total.requests ? Math.round(Number(stats.token.total.total_tokens) / Number(stats.token.total.requests)) : 0"
              />
              <div class="kpi-sub">效率指标</div>
            </el-card>
          </el-col>
          <el-col :span="6">
            <el-card shadow="hover" class="kpi-card">
              <el-statistic title="近期消息数" :value="stats?.messages.recent_count || 0" />
              <el-text v-if="stats && !stats.messages.available" size="small" type="warning">
                不可用：{{ stats.messages.unavailable_reason }}
              </el-text>
            </el-card>
          </el-col>
        </el-row>

        <el-card shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart :option="trendOption" height="360px" />
          <div class="chart-hint">💡 滚动滚轮缩放 · 拖动底部滑块切换区间</div>
        </el-card>

        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="6"><el-card shadow="never" class="panel"><EChart :option="modelDonut" height="300px" @click="onDonutClick('model')" /></el-card></el-col>
          <el-col :span="6"><el-card shadow="never" class="panel"><EChart :option="kindDonut" height="300px" @click="onDonutClick('kind')" /></el-card></el-col>
          <el-col :span="6"><el-card shadow="never" class="panel"><EChart :option="sourceDonut" height="300px" @click="onDonutClick('source_type')" /></el-card></el-col>
          <el-col :span="6"><el-card shadow="never" class="panel"><EChart :option="statusDonut" height="300px" @click="onDonutClick('status')" /></el-card></el-col>
        </el-row>

        <el-card v-if="viewMode === 'deep' && focusValue" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart :option="deepCrossOption" height="340px" />
        </el-card>

        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="16">
            <el-card shadow="never" class="panel">
              <EChart :option="topModelBar" height="360px" @click="onTopClick" />
              <div class="chart-hint">💡 点击条形进入下钻模式</div>
            </el-card>
          </el-col>
          <el-col :span="8"><el-card shadow="never" class="panel"><EChart :option="funnelOption" height="360px" /></el-card></el-col>
        </el-row>

        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="12"><el-card shadow="never" class="panel"><EChart :option="radarOption" height="360px" /></el-card></el-col>
          <el-col :span="12"><el-card shadow="never" class="panel"><EChart :option="sunburstOption" height="360px" /></el-card></el-col>
        </el-row>

        <el-card shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart :option="heatmapOption" height="400px" />
        </el-card>

        <el-card v-loading="activityLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart v-if="activity && activity.total > 0" :option="activityHeatmap" height="320px" />
          <div v-else-if="activityError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            发言活跃度不可用：{{ activityError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有发言记录
          </div>
        </el-card>

        <el-card v-loading="keywordsLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="keywords && keywords.items.length > 0"
            :option="keywordsBar"
            :height="`${Math.min(Math.max(keywords.items.length, 8), 30) * 22 + 80}px`"
          />
          <div v-else-if="keywordsError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            关键词不可用：{{ keywordsError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有可分析的实词
          </div>
        </el-card>

        <el-card v-loading="commandsLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="commands && commands.items.length > 0"
            :option="commandsBar"
            :height="`${Math.min(Math.max(commands.items.length, 6), 20) * 24 + 80}px`"
          />
          <div v-else-if="commandsError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            命令统计不可用：{{ commandsError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有命令调用
          </div>
        </el-card>

        <el-card v-loading="topSendersLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="topSenders && topSenders.items.length > 0"
            :option="sendersBar"
            :height="`${Math.min(Math.max(topSenders.items.length, 6), 20) * 24 + 80}px`"
          />
          <div v-else-if="topSendersError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            发言排行不可用：{{ topSendersError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有发言记录
          </div>
        </el-card>

        <el-card v-loading="messageKindsLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="messageKinds && messageKinds.items.length > 0"
            :option="messageKindsDonut"
            height="320px"
          />
          <div v-else-if="messageKindsError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            消息类型分布不可用：{{ messageKindsError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有消息记录
          </div>
        </el-card>

        <el-card v-loading="commandTrendLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="commandTrend && commandTrend.days.length > 0"
            :option="commandTrendOption"
            height="320px"
          />
          <div v-else-if="commandTrendError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            命令时序不可用：{{ commandTrendError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有可对比的日数据
          </div>
        </el-card>

        <el-card v-loading="topMentionsLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="topMentions && topMentions.items.length > 0"
            :option="mentionsBar"
            :height="`${Math.min(Math.max(topMentions.items.length, 6), 20) * 24 + 80}px`"
          />
          <div v-else-if="topMentionsError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            被 @ 排行不可用：{{ topMentionsError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有 @ 互动
          </div>
        </el-card>

        <el-card v-loading="topicTrendLoading" shadow="never" class="panel" style="margin-bottom: 12px">
          <EChart
            v-if="topicTrend && topicTrend.days.length > 0"
            :option="topicTrendOption"
            height="320px"
          />
          <div v-else-if="topicTrendError" style="padding: 24px; text-align: center; color: #c45656; font-size: 12px">
            主题趋势不可用：{{ topicTrendError }}
          </div>
          <div v-else style="padding: 24px; text-align: center; color: #909399; font-size: 12px">
            当前窗口内没有可分析的实词
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane :label="`👥 群成员${members.length ? ' (' + members.length + ')' : ''}`" name="members">
        <div style="display: flex; gap: 12px; margin-bottom: 12px; align-items: center">
          <el-input v-model="memberKeyword" placeholder="按名字或 open_id 搜索" clearable style="max-width: 280px" />
          <el-button :loading="memberLoading" @click="loadMembers">刷新</el-button>
          <span style="color: #909399">共 {{ members.length }} 名成员</span>
        </div>
        <el-table v-loading="memberLoading" :data="filteredMembers()" stripe>
          <el-table-column type="index" label="#" width="60" />
          <el-table-column prop="name" label="名字" min-width="180" show-overflow-tooltip />
          <el-table-column prop="open_id" label="Open ID" min-width="240" show-overflow-tooltip />
          <el-table-column prop="tenant_key" label="租户" min-width="160" show-overflow-tooltip />
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="🔌 功能开关" name="features">
        <el-table v-loading="featLoading" :data="features" stripe>
          <el-table-column prop="name" label="功能" min-width="160" />
          <el-table-column prop="description" label="描述" min-width="220" show-overflow-tooltip />
          <el-table-column prop="category" label="分类" width="120" />
          <el-table-column label="状态" width="200">
            <template #default="{ row }">
              <div style="display: flex; align-items: center; gap: 8px">
                <el-switch :model-value="row.enabled" @update:model-value="(v: boolean) => toggleFeature(row, v)" />
                <el-tag v-if="!row.enabled && row.default_enabled" size="small" type="warning" effect="plain">覆盖默认</el-tag>
                <el-tag v-else-if="row.enabled && !row.default_enabled" size="small" type="success" effect="plain">已启用</el-tag>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="⚙️ 配置" name="configs">
        <el-table v-loading="cfgLoading" :data="configs" stripe>
          <el-table-column prop="key" label="键" min-width="200" />
          <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
          <el-table-column label="值" min-width="240">
            <template #default="{ row }">
              <el-switch
                v-if="row.value_type === 'bool'"
                :model-value="drafts[row.key] === 'true'"
                :disabled="row.read_only"
                @update:model-value="(v: boolean) => (drafts[row.key] = v ? 'true' : 'false')"
              />
              <el-input-number
                v-else-if="row.value_type === 'int'"
                v-model="drafts[row.key]"
                :min="row.int_min"
                :max="row.int_max || undefined"
                :disabled="row.read_only"
                controls-position="right"
              />
              <el-select
                v-else-if="row.enum_options && row.enum_options.length"
                v-model="drafts[row.key]"
                :disabled="row.read_only"
                filterable
                :allow-create="row.allow_custom"
                style="width: 100%"
              >
                <el-option v-for="o in row.enum_options" :key="o.value" :label="o.text" :value="o.value" />
              </el-select>
              <el-input v-else v-model="drafts[row.key]" :disabled="row.read_only" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="180">
            <template #default="{ row }">
              <el-button size="small" type="primary" :disabled="row.read_only" @click="saveConfig(row)">保存</el-button>
              <el-button size="small" :disabled="row.read_only" @click="resetConfig(row)">重置</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
.bot-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.08);
  vertical-align: middle;
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
  border: 1px solid #f2f6fc;
  position: relative;
}
.panel :deep(.el-card__body) {
  padding: 10px 8px 14px 8px;
}
.chart-hint {
  position: absolute;
  right: 16px;
  top: 14px;
  font-size: 12px;
  color: #909399;
}
</style>
