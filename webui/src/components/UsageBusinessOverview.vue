<script setup lang="ts">
import { computed } from 'vue'
import type { EChartsOption } from 'echarts'
import type { TokenGroupCount, TokenStats } from '../api/types'
import { buildDonut, buildTopBar } from '../composables/useChartOptions'
import {
  attributionLabel,
  operationLabel,
  sceneColor,
  sceneLabel,
} from '../usage/taxonomy'
import EChart from './EChart.vue'

const props = defineProps<{ stats: TokenStats | null }>()
const emit = defineEmits<{
  (event: 'drill', dimension: 'business_scene' | 'business_operation', value: string): void
}>()

const hasData = computed(() => Number(props.stats?.total?.requests || 0) > 0)
const total = computed(() => props.stats?.total)
const toolSummary = computed(() => props.stats?.tool_summary)

function formatNumber(value: number | undefined): string {
  return Number(value || 0).toLocaleString('zh-CN')
}

function formatPercent(value: number | undefined): string {
  return `${(Number(value || 0) * 100).toFixed(1)}%`
}

function labeledGroups(
  groups: TokenGroupCount[] | undefined,
  labeler: (value: string) => string,
): TokenGroupCount[] {
  return (groups || []).map((item) => ({ ...item, group: labeler(item.group) }))
}

const sceneChart = computed<EChartsOption>(() => {
  const option = buildDonut({
    title: '业务场景 · Token 分布',
    data: labeledGroups(props.stats?.by_business_scene, sceneLabel),
    metric: 'total_tokens',
  })
  option.color = (props.stats?.by_business_scene || []).map((item) => sceneColor(item.group))
  return option
})

const operationChart = computed<EChartsOption>(() => buildTopBar({
  title: '业务动作 · Token Top 10',
  data: labeledGroups(props.stats?.by_business_operation, operationLabel).slice(0, 10),
  metric: 'total_tokens',
}))

const sceneByLabel = computed(() => new Map(
  (props.stats?.by_business_scene || []).map((item) => [sceneLabel(item.group), item.group]),
))
const operationByLabel = computed(() => new Map(
  (props.stats?.by_business_operation || []).map((item) => [operationLabel(item.group), item.group]),
))

function drillScene(params: { name?: string }) {
  const value = params?.name ? sceneByLabel.value.get(params.name) : undefined
  if (value) emit('drill', 'business_scene', value)
}

function drillOperation(params: { name?: string }) {
  const value = params?.name ? operationByLabel.value.get(params.name) : undefined
  if (value) emit('drill', 'business_operation', value)
}
</script>

<template>
  <section class="business-overview" aria-labelledby="business-overview-title">
    <header class="business-overview__header">
      <div>
        <p class="business-overview__eyebrow">Business attribution</p>
        <h2 id="business-overview-title">业务用量概览</h2>
        <p>按真实业务目的归集模型成本；工具指标按逻辑回合统计，不把 Token 强行分摊到单个工具。</p>
      </div>
      <div v-if="hasData" class="provenance-list" aria-label="归因方式">
        <span
          v-for="item in stats?.by_attribution_mode || []"
          :key="item.group"
          :class="['provenance-badge', `provenance-badge--${item.group}`]"
        >
          {{ attributionLabel(item.group) }} · {{ formatNumber(item.requests) }} 次
        </span>
      </div>
    </header>

    <div v-if="!hasData" class="business-overview__empty">
      <span aria-hidden="true">∿</span>
      <strong>暂无业务归因数据</strong>
      <p>新产生的模型调用会自动按业务场景与动作归集；历史记录将标记为“历史映射”。</p>
    </div>

    <template v-else>
      <div class="business-kpis" aria-label="业务关键指标">
        <article>
          <span>业务总 Token</span>
          <strong>{{ formatNumber(total?.total_tokens) }}</strong>
          <small>{{ formatNumber(total?.requests) }} 个逻辑回合</small>
        </article>
        <article>
          <span>工具调用</span>
          <strong>{{ formatNumber(toolSummary?.calls) }}</strong>
          <small>{{ formatNumber(toolSummary?.turns_with_tools) }} 个工具回合 · {{ formatNumber(toolSummary?.successes) }} 成功 · {{ formatNumber(toolSummary?.errors) }} 失败</small>
        </article>
        <article>
          <span>工具成功率</span>
          <strong>{{ formatPercent(toolSummary?.success_rate) }}</strong>
          <small>平均 {{ Math.round(toolSummary?.average_duration_ms || 0) }} ms</small>
        </article>
        <article>
          <span>含工具回合 Token</span>
          <strong>{{ formatNumber(toolSummary?.tool_related_tokens) }}</strong>
          <small>按回合去重，不等于各工具独占成本</small>
        </article>
        <article>
          <span>P95 工具耗时</span>
          <strong>{{ Math.round(toolSummary?.p95_duration_ms || 0) }} ms</strong>
          <small>窗口内工具执行尾延迟</small>
        </article>
      </div>

      <div class="business-charts">
        <article class="business-panel">
          <EChart :option="sceneChart" height="320px" @click="drillScene" />
          <div class="business-label-strip" aria-label="业务场景列表">
            <span v-for="item in stats?.by_business_scene || []" :key="item.group">
              <i :style="{ background: sceneColor(item.group) }" />
              {{ sceneLabel(item.group) }}
            </span>
          </div>
        </article>
        <article class="business-panel">
          <EChart :option="operationChart" height="320px" @click="drillOperation" />
          <div class="business-label-strip" aria-label="业务动作列表">
            <span v-for="item in (stats?.by_business_operation || []).slice(0, 10)" :key="item.group">
              {{ operationLabel(item.group) }}
            </span>
          </div>
        </article>
      </div>

      <article class="tool-ranking business-panel">
        <div class="tool-ranking__heading">
          <div>
            <span>Tool execution</span>
            <h3>工具执行排行</h3>
          </div>
          <p>仅展示调用质量与耗时，不保存参数、输出或原始错误。</p>
        </div>
        <div v-if="stats?.by_tool?.length" class="tool-ranking__list">
          <div v-for="tool in stats.by_tool.slice(0, 10)" :key="tool.group" class="tool-row">
            <div class="tool-row__name">
              <strong>{{ tool.group }}</strong>
              <span>{{ formatNumber(tool.calls) }} 次调用</span>
            </div>
            <div class="tool-row__quality">
              <span>{{ formatNumber(tool.successes) }} 成功 · {{ formatNumber(tool.errors) }} 失败</span>
              <b>{{ formatPercent(tool.success_rate) }}</b>
            </div>
            <div class="tool-row__latency">
              <span>平均 {{ Math.round(tool.average_duration_ms) }} ms</span>
              <span>P95 {{ Math.round(tool.p95_duration_ms) }} ms</span>
            </div>
          </div>
        </div>
        <p v-else class="tool-ranking__empty">当前窗口没有工具调用。</p>
      </article>
    </template>
  </section>
</template>

<style scoped>
.business-overview {
  display: grid;
  gap: 0.9rem;
}

.business-overview__header {
  display: flex;
  flex-direction: column;
  gap: 0.8rem;
  padding: 0.15rem 0.1rem;
}

.business-overview__eyebrow,
.tool-ranking__heading span {
  margin: 0 0 0.28rem;
  color: var(--ops-teal);
  font-family: var(--ops-font-mono);
  font-size: 0.65rem;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.business-overview__header h2,
.tool-ranking__heading h3 {
  margin: 0;
  color: var(--ops-pine-950);
  letter-spacing: -0.025em;
}

.business-overview__header h2 {
  font-size: clamp(1.25rem, 3vw, 1.7rem);
}

.business-overview__header p:not(.business-overview__eyebrow),
.tool-ranking__heading p {
  max-width: 46rem;
  margin: 0.4rem 0 0;
  color: var(--ops-muted);
  font-size: 0.75rem;
  line-height: 1.65;
}

.provenance-list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.42rem;
}

.provenance-badge {
  padding: 0.42rem 0.58rem;
  border: 1px solid var(--ops-border);
  border-radius: 999px;
  background: var(--ops-surface);
  color: var(--ops-muted);
  font-size: 0.67rem;
  font-weight: 700;
}

.provenance-badge--explicit {
  border-color: rgb(47 125 109 / 30%);
  background: rgb(47 125 109 / 8%);
  color: #286b5e;
}

.provenance-badge--legacy_mapping,
.provenance-badge--unknown {
  border-color: rgb(189 124 42 / 28%);
  background: rgb(189 124 42 / 8%);
  color: #946020;
}

.business-kpis {
  display: grid;
  grid-template-columns: 1fr;
  overflow: hidden;
  border: 1px solid var(--ops-pine-800);
  border-radius: var(--ops-radius-md);
  background: var(--ops-pine-900);
  box-shadow: var(--ops-shadow-md);
}

.business-kpis article {
  min-width: 0;
  padding: 0.95rem 1rem;
  border-bottom: 1px solid rgb(255 255 255 / 10%);
}

.business-kpis article:last-child {
  border-bottom: 0;
}

.business-kpis span,
.business-kpis small {
  display: block;
  color: #a9beb7;
  font-size: 0.68rem;
}

.business-kpis strong {
  display: block;
  margin: 0.25rem 0;
  color: #fff;
  font-size: clamp(1.28rem, 4vw, 1.7rem);
  font-variant-numeric: tabular-nums;
}

.business-charts {
  display: grid;
  grid-template-columns: 1fr;
  gap: 0.9rem;
}

.business-panel {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--ops-border);
  border-radius: var(--ops-radius-md);
  background: var(--ops-surface);
  box-shadow: var(--ops-shadow-sm);
}

.business-label-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  padding: 0 0.75rem 0.75rem;
}

.business-label-strip span {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.28rem 0.42rem;
  border-radius: 999px;
  background: rgb(20 59 54 / 5%);
  color: var(--ops-muted);
  font-size: 0.64rem;
}

.business-label-strip i {
  width: 0.42rem;
  height: 0.42rem;
  border-radius: 50%;
}

.tool-ranking {
  padding: 1rem;
}

.tool-ranking__heading {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  margin-bottom: 0.8rem;
}

.tool-ranking__list {
  display: grid;
  gap: 0.5rem;
}

.tool-row {
  display: grid;
  grid-template-columns: 1fr;
  gap: 0.48rem;
  min-height: 44px;
  padding: 0.72rem;
  border: 1px solid var(--ops-border);
  border-radius: 0.72rem;
  background: rgb(255 254 250 / 72%);
}

.tool-row__name,
.tool-row__quality,
.tool-row__latency {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.65rem;
}

.tool-row strong {
  overflow: hidden;
  color: var(--ops-pine-950);
  font-family: var(--ops-font-mono);
  font-size: 0.76rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-row span,
.tool-ranking__empty {
  color: var(--ops-muted);
  font-size: 0.69rem;
}

.tool-row__quality b {
  color: var(--ops-teal);
  font-size: 0.73rem;
}

.business-overview__empty {
  display: grid;
  justify-items: center;
  min-height: 13rem;
  padding: 2.5rem 1rem;
  border: 1px dashed var(--ops-border);
  border-radius: var(--ops-radius-md);
  background: var(--ops-surface);
  text-align: center;
}

.business-overview__empty > span {
  color: var(--ops-teal);
  font-size: 2.2rem;
}

.business-overview__empty strong {
  color: var(--ops-pine-950);
}

.business-overview__empty p {
  max-width: 32rem;
  margin: 0.45rem 0 0;
  color: var(--ops-muted);
  font-size: 0.75rem;
}

@media (min-width: 48rem) {
  .business-overview__header,
  .tool-ranking__heading {
    flex-direction: row;
    align-items: flex-end;
    justify-content: space-between;
  }

  .business-kpis {
    grid-template-columns: repeat(5, minmax(0, 1fr));
  }

  .business-kpis article {
    border-right: 1px solid rgb(255 255 255 / 10%);
    border-bottom: 0;
  }

  .business-kpis article:last-child {
    border-right: 0;
  }

  .business-charts {
    grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.1fr);
  }

  .tool-row {
    grid-template-columns: minmax(10rem, 1.2fr) minmax(9rem, 1fr) minmax(10rem, 0.9fr);
    align-items: center;
  }
}
</style>
