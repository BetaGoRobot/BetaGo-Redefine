import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import type { TokenStats } from '../api/types'
import UsageBusinessOverview from './UsageBusinessOverview.vue'

const populated: TokenStats = {
  window_days: 7,
  total: {
    requests: 4, prompt_tokens: 90, completion_tokens: 30, total_tokens: 120,
    tool_calls: 3, turns_with_tools: 2, tool_successes: 2, tool_errors: 1, tool_related_tokens: 100,
  },
  by_business_scene: [
    { group: 'conversation', requests: 3, prompt_tokens: 70, completion_tokens: 25, total_tokens: 95, tool_calls: 2, tool_related_tokens: 80 },
    { group: 'unknown', requests: 1, prompt_tokens: 20, completion_tokens: 5, total_tokens: 25, tool_calls: 1, tool_related_tokens: 20 },
  ],
  by_business_operation: [
    { group: 'tool_planning', requests: 2, prompt_tokens: 30, completion_tokens: 10, total_tokens: 40, tool_calls: 0, tool_related_tokens: 0 },
  ],
  by_attribution_mode: [
    { group: 'explicit', requests: 3, prompt_tokens: 70, completion_tokens: 25, total_tokens: 95, tool_calls: 2, tool_related_tokens: 80 },
    { group: 'legacy_mapping', requests: 1, prompt_tokens: 20, completion_tokens: 5, total_tokens: 25, tool_calls: 1, tool_related_tokens: 20 },
  ],
  by_model: [], by_kind: [], by_source_type: [], by_raw_source: [], by_status: [], by_day: [],
  tool_summary: { calls: 3, turns_with_tools: 2, successes: 2, errors: 1, success_rate: 2 / 3, average_duration_ms: 42, p95_duration_ms: 80, tool_related_tokens: 100 },
  by_tool: [
    { group: 'search_history', calls: 2, successes: 2, errors: 0, success_rate: 1, average_duration_ms: 30, p95_duration_ms: 40 },
    { group: 'finance', calls: 1, successes: 0, errors: 1, success_rate: 0, average_duration_ms: 66, p95_duration_ms: 66 },
  ],
}

const global = {
  stubs: {
    EChart: { props: ['option'], emits: ['click'], template: '<div class="chart-stub" />' },
  },
}

describe('UsageBusinessOverview', () => {
  it('shows business KPIs, provenance and tool execution quality', () => {
    const wrapper = mount(UsageBusinessOverview, { props: { stats: populated }, global })
    expect(wrapper.text()).toContain('业务用量概览')
    expect(wrapper.text()).toContain('对话生成')
    expect(wrapper.text()).toContain('工具规划')
    expect(wrapper.text()).toContain('历史映射')
    expect(wrapper.text()).toContain('search_history')
    expect(wrapper.text()).toContain('2 成功 · 0 失败')
    expect(wrapper.text()).toContain('含工具回合 Token')
  })

  it('shows an intentional empty state', () => {
    const wrapper = mount(UsageBusinessOverview, { props: { stats: null }, global })
    expect(wrapper.text()).toContain('暂无业务归因数据')
  })
})

describe('business usage view integration', () => {
  it('puts business analytics before retained technical dimensions', () => {
    for (const view of ['Dashboard.vue', 'ChatDetail.vue']) {
      const source = readFileSync(resolve(process.cwd(), `src/views/${view}`), 'utf8')
      expect(source).toContain('<UsageBusinessOverview')
      expect(source.indexOf('<UsageBusinessOverview')).toBeLessThan(source.indexOf('Technical dimensions'))
      expect(source).toContain('by_model')
      expect(source).toContain('by_source_type')
    }
  })

  it('adds business-scene filtering without removing list filters', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/views/ChatList.vue'), 'utf8')
    expect(source).toContain('sceneFilter')
    expect(source).toContain('by_business_scene')
    expect(source).toContain("case 'model'")
    expect(source).toContain("case 'source_type'")
  })
})
