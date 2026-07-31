<script setup lang="ts">
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

const store = useFilterStore()
const windows: TimeWindow[] = ['1d', '7d', '30d']
const metrics: MetricKey[] = [
  'total_tokens',
  'prompt_tokens',
  'completion_tokens',
  'requests',
]

function onJump(index: number) {
  store.jumpToDrillIndex(index)
}

function tagForStep(step: { dimension: string }) {
  if (step.dimension === 'bot') return 'BOT'
  if (step.dimension === 'chat') return 'CHAT'
  if (step.dimension === 'global') return 'ALL'
  return DIMENSION_LABEL[step.dimension as DimensionKey] || 'FILTER'
}
</script>

<template>
  <section class="filter-dock" aria-label="全局分析筛选">
    <div class="filter-dock__controls">
      <div class="filter-dock__group filter-dock__group--bot">
        <span class="filter-dock__label">数据源</span>
        <BotPicker />
      </div>

      <div class="filter-dock__group">
        <span class="filter-dock__label">时间窗口</span>
        <el-radio-group
          :model-value="store.window"
          aria-label="时间窗口"
          @update:model-value="store.setWindow"
        >
          <el-radio-button v-for="window in windows" :key="window" :value="window">
            {{ WINDOW_LABEL[window] }}
          </el-radio-button>
        </el-radio-group>
      </div>

      <div class="filter-dock__group">
        <span class="filter-dock__label">主指标</span>
        <el-select
          :model-value="store.primaryMetric"
          aria-label="主指标"
          @update:model-value="store.setPrimaryMetric"
        >
          <el-option
            v-for="metric in metrics"
            :key="metric"
            :value="metric"
            :label="METRIC_LABEL[metric]"
          />
        </el-select>
      </div>

      <div class="filter-dock__group">
        <span class="filter-dock__label">对照指标</span>
        <el-select
          :model-value="store.secondaryMetric"
          aria-label="对照指标"
          @update:model-value="store.setSecondaryMetric"
        >
          <el-option
            v-for="metric in metrics"
            :key="metric"
            :value="metric"
            :label="METRIC_LABEL[metric]"
          />
        </el-select>
      </div>
    </div>

    <div v-if="store.drillPath.length > 1" class="filter-dock__drill">
      <span class="filter-dock__drill-label">当前视角</span>
      <el-breadcrumb separator="/">
        <el-breadcrumb-item
          v-for="(step, index) in store.drillPath"
          :key="index"
          class="drill-crumb"
          @click="onJump(index)"
        >
          <span class="drill-crumb__kind">{{ tagForStep(step) }}</span>
          <span class="drill-crumb__label">{{ step.label }}</span>
        </el-breadcrumb-item>
      </el-breadcrumb>
      <el-button
        v-if="store.drillPath.length > 2"
        text
        type="primary"
        @click="store.resetDrill"
      >
        清除下钻
      </el-button>
    </div>
  </section>
</template>

<style scoped>
.filter-dock {
  margin-bottom: 1rem;
  border: 1px solid var(--ops-border);
  border-radius: var(--ops-radius-md);
  background:
    linear-gradient(115deg, rgb(255 254 250 / 98%), rgb(250 249 245 / 94%));
  box-shadow: var(--ops-shadow-sm);
}

.filter-dock__controls {
  display: grid;
  grid-template-columns: minmax(12rem, 1.25fr) minmax(15rem, 1.3fr) minmax(9rem, 0.8fr) minmax(9rem, 0.8fr);
  align-items: end;
  gap: 0.75rem;
  padding: 0.8rem;
}

.filter-dock__group {
  display: grid;
  gap: 0.35rem;
  min-width: 0;
}

.filter-dock__label,
.filter-dock__drill-label {
  color: var(--ops-muted);
  font-size: 0.65rem;
  font-weight: 800;
  letter-spacing: 0.075em;
  text-transform: uppercase;
}

.filter-dock__group :deep(.el-select),
.filter-dock__group :deep(.el-dropdown),
.filter-dock__group :deep(.el-dropdown .el-button) {
  width: 100%;
}

.filter-dock__group :deep(.el-radio-group) {
  display: flex;
  width: 100%;
}

.filter-dock__group :deep(.el-radio-button) {
  flex: 1;
}

.filter-dock__group :deep(.el-radio-button__inner) {
  width: 100%;
  padding-inline: 0.65rem;
}

.filter-dock__drill {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  min-height: 3rem;
  padding: 0.55rem 0.8rem;
  border-top: 1px solid var(--ops-border);
  background: rgb(220 233 228 / 24%);
}

.filter-dock__drill :deep(.el-breadcrumb) {
  flex: 1;
  min-width: 0;
}

.drill-crumb {
  cursor: pointer;
}

.drill-crumb__kind {
  margin-right: 0.35rem;
  padding: 0.16rem 0.32rem;
  border-radius: 0.3rem;
  background: var(--ops-pine-100);
  color: var(--ops-pine-700);
  font-size: 0.58rem;
  font-weight: 850;
  letter-spacing: 0.05em;
}

.drill-crumb__label {
  color: var(--ops-ink);
  font-size: 0.78rem;
  font-weight: 650;
}

@media (max-width: 1023px) {
  .filter-dock__controls {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 767px) {
  .filter-dock__controls {
    grid-template-columns: 1fr;
    padding: 0.7rem;
  }

  .filter-dock__drill {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .filter-dock__drill :deep(.el-breadcrumb) {
    order: 3;
    width: 100%;
  }
}
</style>
