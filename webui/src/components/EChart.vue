<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts'
import type { EChartsOption, SetOptionOpts } from 'echarts'

const props = withDefaults(
  defineProps<{
    option: EChartsOption
    height?: string
    /** 是否合并 option（false = 每次全量替换） */
    merge?: boolean
    /** setOption 配置，覆盖 merge/notMerge/lazyUpdate */
    setOptionOpts?: SetOptionOpts
    /** 是否启用内置 toolbox（下载 / 刷新 / 数据视图） */
    toolbox?: boolean
    /** 是否启用内置 dataZoom（底部滑块 + 内部滚轮） */
    dataZoom?: boolean
    /** 主题名 */
    theme?: string | object
  }>(),
  {
    height: '320px',
    merge: false,
    toolbox: true,
    dataZoom: true,
  },
)

const emit = defineEmits<{
  (e: 'init', chart: echarts.ECharts): void
  (e: 'click', params: any): void
  (e: 'legendselectchanged', params: any): void
  (e: 'datazoom', params: any): void
}>()

const el = ref<HTMLDivElement>()
let chart: echarts.ECharts | null = null
let ro: ResizeObserver | null = null

function render() {
  if (!el.value || !chart) return
  const opts: SetOptionOpts = props.setOptionOpts ?? { notMerge: !props.merge, lazyUpdate: true }
  chart.setOption(props.option, opts)
}

function resize() {
  chart?.resize()
}

function injectDefaultFeatures() {
  if (!chart) return
  const opt: EChartsOption = {}
  if (props.toolbox && !props.option.toolbox) {
    opt.toolbox = {
      right: 12,
      top: 6,
      showTitle: false,
      feature: {
        saveAsImage: { title: '导出 PNG', pixelRatio: 2 },
        restore: { title: '重置' },
        dataView: { title: '数据', lang: ['数据视图', '关闭', '刷新'], readOnly: true },
      },
      iconStyle: { borderColor: '#909399' },
    }
  }
  if (props.dataZoom && !props.option.dataZoom) {
    const hasCategory =
      (props.option.xAxis as any)?.type === 'category' ||
      Array.isArray(props.option.xAxis)
    const hasTime =
      (props.option.xAxis as any)?.type === 'time' ||
      (props.option.xAxis && Array.isArray(props.option.xAxis) && (props.option.xAxis as any[]).some((a) => a.type === 'time'))
    if (hasCategory || hasTime) {
      opt.dataZoom = [
        { type: 'inside', xAxisIndex: 0, start: 0, end: 100 },
        {
          type: 'slider',
          xAxisIndex: 0,
          start: 0,
          end: 100,
          height: 16,
          bottom: 4,
          borderColor: 'transparent',
          backgroundColor: '#f2f6fc',
          fillerColor: 'rgba(64,158,255,0.15)',
          handleStyle: { color: '#409EFF' },
        },
      ]
    }
  }
  if (Object.keys(opt).length) chart.setOption(opt, { notMerge: false })
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value, props.theme)
  render()
  injectDefaultFeatures()
  emit('init', chart)

  ro = new ResizeObserver(() => requestAnimationFrame(resize))
  ro.observe(el.value)
  window.addEventListener('resize', resize)

  chart.on('click', (p) => emit('click', p))
  chart.on('legendselectchanged', (p) => emit('legendselectchanged', p))
  chart.on('datazoom', (p) => emit('datazoom', p))
})

watch(() => props.option, () => {
  render()
  injectDefaultFeatures()
}, { deep: true })

onBeforeUnmount(() => {
  window.removeEventListener('resize', resize)
  ro?.disconnect()
  ro = null
  chart?.dispose()
  chart = null
})

defineExpose({
  getInstance: () => chart,
  resize,
})
</script>

<template>
  <div
    ref="el"
    :style="{ width: '100%', height, background: '#fff', borderRadius: '8px' }"
  ></div>
</template>
