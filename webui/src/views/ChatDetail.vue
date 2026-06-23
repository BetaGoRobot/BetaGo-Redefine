<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { EChartsOption } from 'echarts'
import { api } from '../api/client'
import type { ChatDetail, ConfigView, FeatureView, StatsResponse } from '../api/types'
import EChart from '../components/EChart.vue'

const props = defineProps<{ chatID: string }>()

const detail = ref<ChatDetail | null>(null)
const activeTab = ref('stats')

// ---------- 统计 ----------
const stats = ref<StatsResponse | null>(null)
const window = ref('7d')
const statsLoading = ref(false)

async function loadStats() {
  statsLoading.value = true
  try {
    stats.value = await api.getStats(props.chatID, window.value)
  } catch (e: any) {
    ElMessage.error('加载统计失败：' + (e?.response?.data?.error || e.message))
  } finally {
    statsLoading.value = false
  }
}

function pieOption(title: string, data: { group: string; total_tokens: number }[]): EChartsOption {
  return {
    title: { text: title, left: 'center', textStyle: { fontSize: 14 } },
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    series: [
      {
        type: 'pie',
        radius: ['40%', '70%'],
        data: data.map((d) => ({ name: d.group, value: d.total_tokens })),
      },
    ],
  }
}

const modelPie = computed(() => pieOption('按模型 Token 分布', stats.value?.token.by_model || []))
const kindPie = computed(() => pieOption('按类型 Token 分布', stats.value?.token.by_kind || []))

const dailyBar = computed<EChartsOption>(() => {
  const days = stats.value?.token.by_day || []
  return {
    title: { text: '每日 Token / 请求数', left: 'center', textStyle: { fontSize: 14 } },
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0, data: ['Token', '请求数'] },
    xAxis: { type: 'category', data: days.map((d) => d.day) },
    yAxis: [{ type: 'value', name: 'Token' }, { type: 'value', name: '请求' }],
    series: [
      { name: 'Token', type: 'bar', data: days.map((d) => d.total_tokens) },
      { name: '请求数', type: 'line', yAxisIndex: 1, data: days.map((d) => d.requests) },
    ],
  }
})

// ---------- 功能开关 ----------
const features = ref<FeatureView[]>([])
const featLoading = ref(false)

async function loadFeatures() {
  featLoading.value = true
  try {
    features.value = (await api.listFeatures(props.chatID)).items || []
  } catch (e: any) {
    ElMessage.error('加载功能开关失败：' + (e?.response?.data?.error || e.message))
  } finally {
    featLoading.value = false
  }
}

async function toggleFeature(f: FeatureView, val: boolean) {
  try {
    await api.setFeature(props.chatID, f.name, val)
    f.enabled = val
    ElMessage.success(`${f.name} 已${val ? '启用' : '禁用'}`)
  } catch (e: any) {
    ElMessage.error('保存失败：' + (e?.response?.data?.error || e.message))
    await loadFeatures()
  }
}

// ---------- 配置 ----------
const configs = ref<ConfigView[]>([])
const cfgLoading = ref(false)
// draft 值在不同控件间混用 string / number / boolean，统一用 any 承接，
// 保存时再按字符串提交给后端。
const drafts = ref<Record<string, any>>({})

async function loadConfigs() {
  cfgLoading.value = true
  try {
    configs.value = (await api.listConfigs(props.chatID)).items || []
    drafts.value = {}
    for (const c of configs.value) {
      // int 控件需要数值类型，其它类型保持字符串。
      drafts.value[c.key] = c.value_type === 'int' ? Number(c.value || 0) : c.value
    }
  } catch (e: any) {
    ElMessage.error('加载配置失败：' + (e?.response?.data?.error || e.message))
  } finally {
    cfgLoading.value = false
  }
}

async function saveConfig(c: ConfigView) {
  try {
    const value = String(drafts.value[c.key])
    await api.setConfig(props.chatID, c.key, value)
    c.value = value
    ElMessage.success(c.key + ' 已保存')
  } catch (e: any) {
    ElMessage.error('保存失败：' + (e?.response?.data?.error || e.message))
  }
}

async function resetConfig(c: ConfigView) {
  try {
    await api.deleteConfig(props.chatID, c.key)
    ElMessage.success(c.key + ' 已重置为默认')
    await loadConfigs()
  } catch (e: any) {
    ElMessage.error('重置失败：' + (e?.response?.data?.error || e.message))
  }
}

onMounted(async () => {
  try {
    detail.value = await api.getChat(props.chatID)
  } catch {
    // 详情失败不阻断其它 tab
  }
  await Promise.all([loadStats(), loadFeatures(), loadConfigs()])
})
</script>

<template>
  <div>
    <el-page-header @back="$router.push('/')" style="margin-bottom: 16px">
      <template #content>
        <div style="display: flex; align-items: center; gap: 10px">
          <el-avatar :src="detail?.avatar" :size="32" shape="square">{{ detail?.name?.[0] }}</el-avatar>
          <span style="font-weight: 600">{{ detail?.name || chatID }}</span>
          <el-tag v-if="detail" size="small">成员 {{ detail.member_count }}</el-tag>
          <el-text size="small" type="info">{{ chatID }}</el-text>
        </div>
      </template>
    </el-page-header>

    <el-tabs v-model="activeTab">
      <!-- 统计 -->
      <el-tab-pane label="统计 & Token" name="stats">
        <div style="display: flex; gap: 12px; margin-bottom: 12px; align-items: center">
          <el-radio-group v-model="window" @change="loadStats">
            <el-radio-button value="1d">1天</el-radio-button>
            <el-radio-button value="7d">7天</el-radio-button>
            <el-radio-button value="30d">30天</el-radio-button>
          </el-radio-group>
          <el-button :loading="statsLoading" @click="loadStats">刷新</el-button>
        </div>

        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="6"><el-statistic title="请求数" :value="stats?.token.total.requests || 0" /></el-col>
          <el-col :span="6"><el-statistic title="总 Token" :value="stats?.token.total.total_tokens || 0" /></el-col>
          <el-col :span="6"><el-statistic title="Prompt Token" :value="stats?.token.total.prompt_tokens || 0" /></el-col>
          <el-col :span="6">
            <el-statistic title="近期消息数" :value="stats?.messages.recent_count || 0" />
            <el-text v-if="stats && !stats.messages.available" size="small" type="warning">
              消息统计不可用：{{ stats.messages.unavailable_reason }}
            </el-text>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="12"><EChart :option="modelPie" /></el-col>
          <el-col :span="12"><EChart :option="kindPie" /></el-col>
        </el-row>
        <EChart :option="dailyBar" height="360px" />
      </el-tab-pane>

      <!-- 功能开关 -->
      <el-tab-pane label="功能开关" name="features">
        <el-table v-loading="featLoading" :data="features" stripe>
          <el-table-column prop="name" label="功能" min-width="160" />
          <el-table-column prop="description" label="描述" min-width="220" show-overflow-tooltip />
          <el-table-column prop="category" label="分类" width="120" />
          <el-table-column label="状态" width="120">
            <template #default="{ row }">
              <el-switch
                :model-value="row.enabled"
                @update:model-value="(v: boolean) => toggleFeature(row, v)"
              />
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- 配置 -->
      <el-tab-pane label="配置" name="configs">
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
          <el-table-column label="操作" width="160">
            <template #default="{ row }">
              <el-button size="small" type="primary" :disabled="row.read_only" @click="saveConfig(row)">
                保存
              </el-button>
              <el-button size="small" :disabled="row.read_only" @click="resetConfig(row)">重置</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>
