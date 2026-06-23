<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import type { ChatSummary } from '../api/types'
import { BotApi, aggregate, type WithBot } from '../api/client'
import {
  useFilterStore,
  METRIC_LABEL,
  type DimensionKey,
  type BotInstance,
} from '../stores/filter'
import { buildSparkline, buildDonut } from '../composables/useChartOptions'
import EChart from '../components/EChart.vue'
import GlobalFilterBar from '../components/GlobalFilterBar.vue'

const router = useRouter()
const store = useFilterStore()

const loading = ref(false)
type BotChat = WithBot<ChatSummary>
const chats = ref<BotChat[]>([])
const keyword = ref('')

// ---------- 过滤面板 ----------
const typeFilter = ref<'all' | 'p2p' | 'group' | 'unknown'>('all')
const extFilter = ref<'all' | 'internal' | 'external'>('all')
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

const filtered = computed<BotChat[]>(() => {
  const kw = keyword.value.trim().toLowerCase()
  const dimFilters = store.currentDimensionFilters
  return chats.value.filter((c) => {
    if (botFilter.value !== 'all' && c.bot_id !== botFilter.value) return false
    if (store.currentBotID && c.bot_id !== store.currentBotID) return false
    if (kw && !(c.name.toLowerCase().includes(kw) || c.chat_id.toLowerCase().includes(kw))) return false
    if (typeFilter.value !== 'all' && c.chat_status !== typeFilter.value) return false
    if (extFilter.value === 'internal' && c.external) return false
    if (extFilter.value === 'external' && !c.external) return false
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
  const p2p = filtered.value.filter((c) => c.chat_status === 'p2p').length
  const group = filtered.value.filter((c) => c.chat_status === 'group').length
  const unknown = filtered.value.filter((c) => !c.chat_status || c.chat_status === 'unknown').length
  return buildDonut({
    title: '会话类型分布',
    data: [
      { group: '单聊', total_tokens: p2p, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
      { group: '群聊', total_tokens: group, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
      { group: '未知/无权限', total_tokens: unknown, requests: 0, prompt_tokens: 0, completion_tokens: 0 },
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

function open(c: BotChat) {
  store.enterChat(c.bot_id, c.chat_id, c.name)
  router.push({
    name: 'chat-detail',
    params: { chatID: c.chat_id },
    query: { bot: c.bot_id },
  })
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
    chats.value = flat
  } catch (e: any) {
    ElMessage.error('加载会话列表失败：' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
  await loadSparks()
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

onMounted(load)
watch([() => store.window, () => store.selectedBotIDs.slice().sort().join(',')], load)
</script>

<template>
  <div>
    <GlobalFilterBar />

    <div v-loading="loading">
      <!-- 摘要卡片 -->
      <el-row :gutter="12" style="margin-bottom: 12px">
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
      <el-card shadow="never" class="panel" style="margin-bottom: 12px">
        <div style="display: flex; gap: 16px; align-items: flex-start; flex-wrap: wrap">
          <div style="flex: 1; min-width: 520px">
            <div style="display: flex; gap: 12px; flex-wrap: wrap; align-items: center">
              <el-input v-model="keyword" placeholder="按群名或 chat_id 搜索" clearable style="max-width: 280px" />
              <el-select v-model="botFilter" placeholder="机器人" style="width: 200px">
                <el-option
                  v-for="o in botOptions"
                  :key="o.value"
                  :value="o.value"
                  :label="o.label"
                />
              </el-select>
              <el-select v-model="typeFilter" placeholder="会话类型" style="width: 140px">
                <el-option value="all" label="全部类型" />
                <el-option value="group" label="群聊" />
                <el-option value="p2p" label="单聊" />
                <el-option value="unknown" label="未知/无权限" />
              </el-select>
              <el-select v-model="extFilter" placeholder="内外群" style="width: 140px">
                <el-option value="all" label="全部" />
                <el-option value="internal" label="内部" />
                <el-option value="external" label="外部" />
              </el-select>
              <el-input-number
                v-model="minTokens"
                placeholder="最小 Token"
                :min="0"
                :controls="false"
                style="width: 160px"
              />
              <el-input-number
                v-model="maxTokens"
                placeholder="最大 Token"
                :min="0"
                :controls="false"
                style="width: 160px"
              />
              <el-button
                @click="() => { keyword = ''; typeFilter = 'all'; extFilter = 'all'; botFilter = 'all'; minTokens = undefined; maxTokens = undefined }"
              >清除过滤</el-button>
              <el-button :loading="loading" type="primary" @click="load">刷新</el-button>
            </div>

            <!-- 下钻激活过滤器 -->
            <div v-if="store.currentDimensionFilters.length" style="margin-top: 12px; display: flex; gap: 8px; flex-wrap: wrap; align-items: center">
              <span style="color: #909399; font-size: 12px">激活的维度过滤：</span>
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
            <div style="margin-top: 12px; display: flex; gap: 8px; flex-wrap: wrap; align-items: center">
              <span style="color: #909399; font-size: 12px">快速筛选（可叠加）：</span>
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
          <div style="display: flex; gap: 8px; min-width: 520px">
            <div style="width: 170px">
              <EChart :option="perBotDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
            <div style="width: 170px">
              <EChart :option="topModelDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
            <div style="width: 170px">
              <EChart :option="statusDistribution" height="180px" :dataZoom="false" :toolbox="false" />
            </div>
          </div>
        </div>
      </el-card>

      <!-- 主数据表格 -->
      <el-card shadow="never" class="panel">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px">
          <span style="font-weight: 600; color: #303133">
            共 {{ filtered.length }} 个会话
          </span>
          <el-tag v-if="sparkLoading" type="info" size="small">迷你趋势加载中…</el-tag>
        </div>

        <el-table
          v-loading="loading"
          :data="filtered"
          stripe
          :default-sort="{ prop: 'total_tokens', order: 'descending' }"
          @row-click="open"
          style="cursor: pointer"
        >
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
          <el-table-column prop="name" label="名称" min-width="180" show-overflow-tooltip />
          <el-table-column label="类型" width="110">
            <template #default="{ row }">
              <el-tag v-if="row.chat_status === 'p2p'" size="small" type="info" effect="plain">单聊</el-tag>
              <el-tag v-else-if="row.chat_status === 'group'" size="small" type="success" effect="plain">群聊</el-tag>
              <el-tooltip
                v-else
                content="机器人无此群权限，请确认机器人是否已加入该群或授权范围。"
              >
                <el-tag size="small" type="warning" effect="dark">无权限</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column label="外部" width="70">
            <template #default="{ row }">
              <el-tag v-if="row.external" size="small" type="warning" effect="plain">外</el-tag>
              <span v-else style="color:#c0c4cc">—</span>
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
      </el-card>
    </div>
  </div>
</template>

<style scoped>
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
}
.panel :deep(.el-card__body) {
  padding: 14px 16px;
}
.bot-dot {
  flex: 0 0 auto;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.08);
}
</style>
