import { describe, expect, it } from 'vitest'
import type { TokenStats } from '../api/types'
import { mergeUsageStats } from './aggregation'
import { attributionLabel, operationLabel, sceneLabel } from './taxonomy'

function stats(overrides: Partial<TokenStats> = {}): TokenStats {
  return {
    window_days: 7,
    total: {
      requests: 1, prompt_tokens: 30, completion_tokens: 10, total_tokens: 40,
      tool_calls: 1, turns_with_tools: 1, tool_successes: 1, tool_errors: 0, tool_related_tokens: 40,
    },
    by_business_scene: [{ group: 'conversation', requests: 1, prompt_tokens: 30, completion_tokens: 10, total_tokens: 40, tool_calls: 1, tool_related_tokens: 40 }],
    by_business_operation: [], by_attribution_mode: [], by_model: [], by_kind: [],
    by_source_type: [], by_raw_source: [], by_status: [], by_day: [],
    tool_summary: { calls: 1, turns_with_tools: 1, successes: 1, errors: 0, success_rate: 1, average_duration_ms: 20, p95_duration_ms: 20, tool_related_tokens: 40 },
    by_tool: [{ group: 'search_history', calls: 1, successes: 1, errors: 0, success_rate: 1, average_duration_ms: 20, p95_duration_ms: 20 }],
    ...overrides,
  }
}

describe('usage aggregation', () => {
  it('merges additive business and tool metrics across chats', () => {
    const second = stats({
      total: { requests: 2, prompt_tokens: 60, completion_tokens: 20, total_tokens: 80, tool_calls: 2, turns_with_tools: 1, tool_successes: 1, tool_errors: 1, tool_related_tokens: 80 },
      tool_summary: { calls: 2, turns_with_tools: 1, successes: 1, errors: 1, success_rate: 0.5, average_duration_ms: 50, p95_duration_ms: 80, tool_related_tokens: 80 },
      by_tool: [{ group: 'search_history', calls: 2, successes: 1, errors: 1, success_rate: 0.5, average_duration_ms: 50, p95_duration_ms: 80 }],
    })
    const merged = mergeUsageStats([stats(), second])
    expect(merged.total.tool_calls).toBe(3)
    expect(merged.total.turns_with_tools).toBe(2)
    expect(merged.tool_summary.tool_related_tokens).toBe(120)
    expect(merged.by_tool[0]).toMatchObject({ group: 'search_history', calls: 3, successes: 2, errors: 1 })
    expect(merged.by_business_scene[0].total_tokens).toBe(80)
  })
})

describe('usage taxonomy', () => {
  it('uses consistent labels for explicit, legacy and unknown attribution', () => {
    expect(sceneLabel('conversation')).toBe('对话生成')
    expect(attributionLabel('legacy_mapping')).toBe('历史映射')
    expect(sceneLabel('new_value')).toBe('待归类')
    expect(operationLabel('tool_planning')).toBe('工具规划')
  })
})
