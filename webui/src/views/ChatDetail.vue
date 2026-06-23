<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import { BotApi } from '../api/client'
import type {
  ChatDetail as ChatDetailType,
  ChatMember,
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
  await Promise.all([loadStats(), loadFeatures(), loadConfigs(), loadMembers()])
}

onMounted(initAll)
watch(() => store.window, loadStats)
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
