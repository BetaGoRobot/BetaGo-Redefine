<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import {
  useFilterStore,
  WINDOW_LABEL,
  METRIC_LABEL,
  DIMENSION_LABEL,
  type TimeWindow,
  type MetricKey,
  type DimensionKey,
} from '../stores/filter'
import BotPicker from './BotPicker.vue'

const route = useRoute()
const store = useFilterStore()

const onDashboard = computed(() => route.name === 'dashboard')
const onChats = computed(() => route.name === 'chats')

const windows: TimeWindow[] = ['1d', '7d', '30d']
const metrics: MetricKey[] = ['total_tokens', 'prompt_tokens', 'completion_tokens', 'requests']

function onJump(idx: number) {
  store.jumpToDrillIndex(idx)
}

function tagForStep(step: any) {
  if (step.dimension === 'bot') return '🤖'
  if (step.dimension === 'chat') return '💬'
  if (step.dimension === 'global') return '🌐'
  return DIMENSION_LABEL[step.dimension as DimensionKey] || '🔎'
}
</script>

<template>
  <div style="margin-bottom: 16px">
    <!-- 顶部：导航 tab + BotPicker + 筛选 -->
    <div style="display: flex; gap: 16px; align-items: center; flex-wrap: wrap">
      <el-radio-group :model-value="onDashboard ? 'dashboard' : onChats ? 'chats' : 'detail'">
        <el-radio-button
          value="dashboard"
          @click="$router.push({ name: 'dashboard' })"
        >📊 总览仪表盘</el-radio-button>
        <el-radio-button
          value="chats"
          @click="$router.push({ name: 'chats' })"
        >💬 会话列表</el-radio-button>
      </el-radio-group>

      <BotPicker />

      <el-divider direction="vertical" />

      <span style="color: #909399">时间窗口</span>
      <el-radio-group :model-value="store.window" @update:model-value="store.setWindow">
        <el-radio-button v-for="w in windows" :key="w" :value="w">
          {{ WINDOW_LABEL[w] }}
        </el-radio-button>
      </el-radio-group>

      <span style="color: #909399">主指标</span>
      <el-select :model-value="store.primaryMetric" style="width: 140px" @update:model-value="store.setPrimaryMetric">
        <el-option v-for="m in metrics" :key="m" :value="m" :label="METRIC_LABEL[m]" />
      </el-select>

      <span style="color: #909399">次指标</span>
      <el-select :model-value="store.secondaryMetric" style="width: 140px" @update:model-value="store.setSecondaryMetric">
        <el-option v-for="m in metrics" :key="m" :value="m" :label="METRIC_LABEL[m]" />
      </el-select>
    </div>

    <!-- 下钻面包屑 -->
    <div v-if="store.drillPath.length > 1" style="margin-top: 12px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap">
      <el-breadcrumb separator="/">
        <el-breadcrumb-item
          v-for="(step, idx) in store.drillPath"
          :key="idx"
          :to="idx === store.drillPath.length - 1 ? undefined : {}"
          :replace="true"
          @click.native.prevent="onJump(idx)"
          class="drill-crumb"
        >
          <span style="margin-right: 4px">{{ tagForStep(step) }}</span>
          <el-tag
            v-if="step.dimension !== 'global' && step.dimension !== 'chat' && step.dimension !== 'bot'"
            size="small"
            type="info"
            effect="plain"
          >{{ DIMENSION_LABEL[step.dimension as DimensionKey] || step.dimension }}</el-tag>
          <span style="margin-left: 4px; font-weight: 500">{{ step.label }}</span>
        </el-breadcrumb-item>
      </el-breadcrumb>
      <el-button
        v-if="store.drillPath.length > 2"
        size="small"
        text
        type="primary"
        @click="store.resetDrill"
      >回到全部</el-button>
    </div>
  </div>
</template>

<style scoped>
.drill-crumb {
  cursor: pointer;
}
</style>
