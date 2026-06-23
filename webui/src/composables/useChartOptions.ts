import type { EChartsOption } from 'echarts'
import type { TokenDailyPoint, TokenGroupCount } from '../api/types'
import type { MetricKey } from '../stores/filter'
import { METRIC_LABEL } from '../stores/filter'

/** 通用调色板（现代、中性） */
const PALETTE = [
  '#5B8FF9', '#5AD8A6', '#F6BD16', '#E86452', '#6DC8EC',
  '#945FB9', '#FF9845', '#1E9493', '#FF99C3', '#5D7092',
]

function baseAxisLabelStyle() {
  return { color: '#606266', fontSize: 11 }
}

function baseGrid(padding = { left: 52, right: 56, top: 48, bottom: 48 }) {
  return padding
}

function formatNumber(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(n)
}

export interface DonutOption {
  title: string
  data: TokenGroupCount[]
  metric: MetricKey
  height?: string
  onClick?: (groupName: string) => void
}

/** 现代环形饼图：带内圈合计、渐变着色、hover 放大 */
export function buildDonut(opt: DonutOption): EChartsOption {
  const raw = (opt.data || []).filter((d) => (d as any)[opt.metric] > 0)
  const sum = raw.reduce((acc, d) => acc + ((d as any)[opt.metric] || 0), 0)
  const centerText = `{a|${formatNumber(sum)}}\n{b|${METRIC_LABEL[opt.metric]}}`
  return {
    color: PALETTE,
    title: {
      text: opt.title,
      left: 'center',
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'item',
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      formatter: (p: any) => {
        const v = p.value as number
        const pct = sum ? ((v / sum) * 100).toFixed(1) : 0
        return `<b>${p.name}</b><br/>${METRIC_LABEL[opt.metric]}: ${v.toLocaleString()}<br/>占比: ${pct}%`
      },
    },
    legend: {
      bottom: 0,
      type: 'scroll',
      textStyle: baseAxisLabelStyle(),
      itemWidth: 10,
      itemHeight: 10,
    },
    graphic: {
      type: 'text',
      left: 'center',
      top: '42%',
      style: {
        rich: {
          a: { fontSize: 22, fontWeight: 700, fill: '#303133' },
          b: { fontSize: 11, fill: '#909399', padding: [4, 0, 0, 0] },
        },
        text: centerText,
        textAlign: 'center' as const,
        x: 'center',
        y: 'middle',
      } as any,
    },
    series: [
      {
        type: 'pie',
        radius: ['45%', '70%'],
        center: ['50%', '50%'],
        avoidLabelOverlap: true,
        itemStyle: {
          borderRadius: 6,
          borderColor: '#fff',
          borderWidth: 2,
        },
        label: {
          show: raw.length <= 8,
          formatter: '{b}\n{d}%',
          color: '#606266',
          fontSize: 11,
          lineHeight: 14,
        },
        labelLine: { length: 8, length2: 6, smooth: true },
        emphasis: {
          label: { show: true, fontSize: 12, fontWeight: 600 },
          itemStyle: { shadowBlur: 12, shadowColor: 'rgba(0,0,0,0.12)' },
          scale: true,
          scaleSize: 8,
        },
        data: raw.map((d) => ({
          name: d.group,
          value: (d as any)[opt.metric] as number,
        })),
      },
    ],
  }
}

export interface TrendOption {
  title: string
  points: TokenDailyPoint[]
  /** 主指标 */
  primary: MetricKey
  /** 次指标（可与主指标相同） */
  secondary: MetricKey
  /** 是否堆叠 prompt/completion（只有 primary 是 total_tokens 时生效） */
  stacked?: boolean
  /** 每日分组项（按 model 等）：用于堆叠面积分解 */
  seriesMap?: { name: string; data: (number | null)[] }[]
}

/** 每日趋势：主指标柱状 + 次指标折线，或堆叠面积 */
export function buildTrend(opt: TrendOption): EChartsOption {
  const points = opt.points || []
  const days = points.map((p) => p.day)
  const primaryLabel = METRIC_LABEL[opt.primary]
  const secondaryLabel = METRIC_LABEL[opt.secondary]
  const sameAxis = opt.primary === opt.secondary

  const seriesList: any[] = []

  if (opt.stacked && !opt.seriesMap) {
    // 默认拆成 prompt + completion（前提是 points 带这两个字段）
    const hasSplit = points.some((p) => (p as any).prompt_tokens !== undefined)
    if (hasSplit) {
      seriesList.push({
        name: 'Prompt',
        type: 'bar',
        stack: 'tokens',
        barWidth: 16,
        itemStyle: { color: PALETTE[0], borderRadius: [0, 0, 0, 0] },
        emphasis: { focus: 'series' },
        data: points.map((p) => ((p as any).prompt_tokens ?? 0) as number),
      })
      seriesList.push({
        name: 'Completion',
        type: 'bar',
        stack: 'tokens',
        barWidth: 16,
        itemStyle: { color: PALETTE[1], borderRadius: [4, 4, 0, 0] },
        emphasis: { focus: 'series' },
        data: points.map((p) => ((p as any).completion_tokens ?? 0) as number),
      })
    } else {
      seriesList.push({
        name: primaryLabel,
        type: 'bar',
        barWidth: 16,
        itemStyle: { color: PALETTE[0], borderRadius: [4, 4, 0, 0] },
        emphasis: { focus: 'series' },
        data: points.map((p) => (p as any)[opt.primary] as number),
      })
    }
  } else if (opt.seriesMap && opt.seriesMap.length) {
    opt.seriesMap.forEach((s, i) => {
      seriesList.push({
        name: s.name,
        type: 'line',
        smooth: true,
        showSymbol: false,
        stack: 'area',
        areaStyle: { opacity: 0.25 },
        itemStyle: { color: PALETTE[i % PALETTE.length] },
        lineStyle: { width: 2 },
        emphasis: { focus: 'series' },
        data: s.data,
      })
    })
  } else {
    seriesList.push({
      name: primaryLabel,
      type: 'bar',
      barWidth: 16,
      itemStyle: {
        color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [
          { offset: 0, color: '#5B8FF9' },
          { offset: 1, color: 'rgba(91,143,249,0.35)' },
        ]},
        borderRadius: [4, 4, 0, 0],
      },
      emphasis: { focus: 'series' },
      data: points.map((p) => (p as any)[opt.primary] as number),
    })
  }

  if (!sameAxis && !opt.seriesMap) {
    seriesList.push({
      name: secondaryLabel,
      type: 'line',
      smooth: true,
      symbol: 'circle',
      symbolSize: 6,
      lineStyle: { width: 2, color: '#E86452' },
      itemStyle: { color: '#E86452' },
      areaStyle: { color: 'rgba(232,100,82,0.12)' },
      yAxisIndex: 1,
      emphasis: { focus: 'series' },
      data: points.map((p) => (p as any)[opt.secondary] as number),
    })
  }

  return {
    color: PALETTE,
    title: {
      text: opt.title,
      left: 16,
      top: 10,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross', crossStyle: { color: '#dcdfe6' } },
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      valueFormatter: (v: any) => (typeof v === 'number' ? v.toLocaleString() : v),
    },
    grid: baseGrid({ left: 56, right: 56, top: 52, bottom: 56 }),
    legend: {
      top: 10,
      right: 56,
      textStyle: baseAxisLabelStyle(),
      itemWidth: 10,
      itemHeight: 10,
    },
    xAxis: {
      type: 'category',
      data: days,
      boundaryGap: true,
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: { ...baseAxisLabelStyle(), hideOverlap: true },
    },
    yAxis: [
      {
        type: 'value',
        name: primaryLabel,
        nameTextStyle: { ...baseAxisLabelStyle(), color: '#909399' },
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
        axisLabel: { ...baseAxisLabelStyle(), formatter: formatNumber },
      },
      sameAxis || opt.seriesMap ? undefined : {
        type: 'value',
        name: secondaryLabel,
        nameTextStyle: { ...baseAxisLabelStyle(), color: '#909399' },
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { ...baseAxisLabelStyle(), formatter: formatNumber },
      },
    ].filter(Boolean) as any,
    series: seriesList,
  }
}

export interface HorizontalBarOption {
  title: string
  data: TokenGroupCount[]
  metric: MetricKey
  unit?: string
}

/** 水平条形图：用于 Top N 排行 */
export function buildTopBar(opt: HorizontalBarOption): EChartsOption {
  const sorted = [...(opt.data || [])]
    .sort((a, b) => ((a as any)[opt.metric] || 0) - ((b as any)[opt.metric] || 0))
    .slice(-15)
  return {
    color: PALETTE,
    title: {
      text: opt.title,
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
      valueFormatter: (v: any) => (typeof v === 'number' ? v.toLocaleString() + (opt.unit ? ` ${opt.unit}` : '') : v),
    },
    grid: baseGrid({ left: 140, right: 56, top: 48, bottom: 24 }),
    xAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { lineStyle: { color: '#f2f6fc', type: 'dashed' } },
      axisLabel: { ...baseAxisLabelStyle(), formatter: formatNumber },
    },
    yAxis: {
      type: 'category',
      data: sorted.map((d) => d.group),
      axisLine: { lineStyle: { color: '#dcdfe6' } },
      axisTick: { show: false },
      axisLabel: baseAxisLabelStyle(),
    },
    series: [
      {
        type: 'bar',
        barWidth: 12,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0, y: 0, x2: 1, y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(91,143,249,0.35)' },
              { offset: 1, color: '#5B8FF9' },
            ],
          },
          borderRadius: [0, 6, 6, 0],
        },
        label: {
          show: true,
          position: 'right',
          ...baseAxisLabelStyle(),
          formatter: (p: any) => formatNumber(p.value),
        },
        emphasis: { focus: 'series' },
        data: sorted.map((d) => (d as any)[opt.metric] as number),
      },
    ],
  }
}

export interface RadarOption {
  title: string
  groups: TokenGroupCount[]
  metrics: MetricKey[]
}

/** 雷达图：多指标多维度对比（例如每个 model 在 requests / prompt / completion / total 的分布） */
export function buildRadar(opt: RadarOption): EChartsOption {
  const groups = (opt.groups || []).slice(0, 6)
  if (!groups.length) return { title: { text: opt.title, left: 'center', top: 8 } }
  const indicators = opt.metrics.map((m) => {
    const max = Math.max(...groups.map((g) => ((g as any)[m] || 0) as number), 1)
    return { name: METRIC_LABEL[m], max: Math.ceil(max * 1.2) }
  })
  return {
    color: PALETTE,
    title: {
      text: opt.title,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
    },
    legend: {
      bottom: 0,
      type: 'scroll',
      textStyle: baseAxisLabelStyle(),
      itemWidth: 10,
      itemHeight: 10,
    },
    radar: {
      center: ['50%', '55%'],
      radius: '60%',
      indicator: indicators as any,
      splitLine: { lineStyle: { color: '#ebeef5' } },
      splitArea: { areaStyle: { color: ['#fff', '#f7faff'] } },
      axisName: { color: '#606266', fontSize: 11 },
    },
    series: [
      {
        type: 'radar',
        emphasis: { focus: 'self' },
        data: groups.map((g, i) => ({
          name: g.group,
          value: opt.metrics.map((m) => (g as any)[m] as number),
          symbol: 'circle',
          symbolSize: 4,
          lineStyle: { width: 2, color: PALETTE[i % PALETTE.length] },
          itemStyle: { color: PALETTE[i % PALETTE.length] },
          areaStyle: { color: PALETTE[i % PALETTE.length], opacity: 0.18 },
        })),
      },
    ],
  }
}

export interface FunnelOption {
  title: string
  stages: { name: string; value: number }[]
}

/** 漏斗图：请求 → Prompt → Completion → Total Token 转化漏斗 */
export function buildFunnel(opt: FunnelOption): EChartsOption {
  return {
    color: PALETTE,
    title: {
      text: opt.title,
      left: 'center',
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      trigger: 'item',
      formatter: '{b}: {c}',
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
    },
    legend: {
      bottom: 0,
      type: 'scroll',
      textStyle: baseAxisLabelStyle(),
      itemWidth: 10,
      itemHeight: 10,
    },
    series: [
      {
        type: 'funnel',
        left: '10%',
        width: '80%',
        top: 50,
        bottom: 32,
        minSize: '30%',
        sort: 'descending',
        gap: 2,
        label: { show: true, position: 'inside', color: '#fff', fontWeight: 600, formatter: '{b}\n{c}' },
        itemStyle: { borderColor: '#fff', borderWidth: 2 },
        emphasis: { focus: 'series' },
        data: opt.stages,
      },
    ],
  }
}

export interface SparklineOption {
  values: number[]
  positive?: boolean
}

/** 迷你折线（Sparkline）：无坐标轴、无标签，用在表格行内 */
export function buildSparkline(opt: SparklineOption): EChartsOption {
  const color = opt.positive === false ? '#E86452' : '#5AD8A6'
  return {
    grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: 'category', show: false, boundaryGap: false, data: opt.values.map((_, i) => i) },
    yAxis: { type: 'value', show: false, min: (v: any) => v.min * 0.9, max: (v: any) => v.max * 1.1 },
    tooltip: { show: false },
    series: [
      {
        type: 'line',
        showSymbol: false,
        smooth: true,
        data: opt.values,
        lineStyle: { width: 2, color },
        areaStyle: {
          color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [
            { offset: 0, color: color + '55' },
            { offset: 1, color: color + '05' },
          ]},
        },
      },
    ],
  }
}

export interface HeatmapOption {
  title: string
  x: string[]
  y: string[]
  data: [number, number, number][]
  xLabel?: string
  yLabel?: string
}

/** 热力图：例如 model × day 的 token 矩阵 */
export function buildHeatmap(opt: HeatmapOption): EChartsOption {
  const max = Math.max(...opt.data.map(([, , v]) => v), 1)
  return {
    title: {
      text: opt.title,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      position: 'top',
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
      formatter: (p: any) => `${opt.yLabel || '行'}: ${opt.y[p.data[1]]}<br/>${opt.xLabel || '列'}: ${opt.x[p.data[0]]}<br/>值: ${(p.data[2] as number).toLocaleString()}`,
    },
    grid: { left: 120, right: 64, top: 52, bottom: 48 },
    xAxis: {
      type: 'category',
      data: opt.x,
      splitArea: { show: true },
      axisLabel: { ...baseAxisLabelStyle(), rotate: 30, hideOverlap: true },
    },
    yAxis: {
      type: 'category',
      data: opt.y,
      splitArea: { show: true },
      axisLabel: baseAxisLabelStyle(),
    },
    visualMap: {
      min: 0,
      max,
      calculable: true,
      orient: 'horizontal',
      left: 'center',
      bottom: 4,
      itemWidth: 12,
      itemHeight: 120,
      textStyle: baseAxisLabelStyle(),
      inRange: { color: ['#f2f6fc', '#5B8FF9', '#1d4ed8'] },
    },
    series: [
      {
        name: '热力',
        type: 'heatmap',
        data: opt.data,
        label: { show: false },
        emphasis: {
          itemStyle: { shadowBlur: 10, shadowColor: 'rgba(0,0,0,0.2)' },
        },
      },
    ],
  }
}

export interface SunburstOption {
  title: string
  /** 两层：父级是维度 A，子级是维度 B */
  outer: { name: string; children: { name: string; value: number }[] }[]
}

/** 旭日图：用于 model × kind / source 双层下钻 */
export function buildSunburst(opt: SunburstOption): EChartsOption {
  return {
    color: PALETTE,
    title: {
      text: opt.title,
      left: 16,
      top: 8,
      textStyle: { fontSize: 14, fontWeight: 600, color: '#303133' },
    },
    tooltip: {
      backgroundColor: 'rgba(255,255,255,0.96)',
      borderColor: '#ebeef5',
      textStyle: { color: '#303133' },
    },
    series: [
      {
        type: 'sunburst',
        center: ['50%', '55%'],
        radius: ['12%', '78%'],
        data: opt.outer,
        sort: undefined,
        emphasis: { focus: 'ancestor' },
        levels: [
          {},
          {
            r0: '12%',
            r: '45%',
            label: { rotate: 'tangential', fontSize: 11, color: '#fff', fontWeight: 600 },
            itemStyle: { borderWidth: 2, borderColor: '#fff' },
          },
          {
            r0: '45%',
            r: '78%',
            label: { align: 'right', fontSize: 10, color: '#303133' },
            itemStyle: { borderWidth: 1, borderColor: '#fff' },
          },
        ],
      },
    ],
  }
}
