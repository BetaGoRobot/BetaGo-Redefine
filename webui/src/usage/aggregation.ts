import type {
  TokenGroupCount,
  TokenStats,
  ToolGroupCount,
  ToolSummary,
} from '../api/types'

const zeroTokenGroup = (group: string): TokenGroupCount => ({
  group,
  requests: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  tool_calls: 0,
  turns_with_tools: 0,
  tool_related_tokens: 0,
})

export function mergeTokenGroups(groups: TokenGroupCount[][]): TokenGroupCount[] {
  const merged = new Map<string, TokenGroupCount>()
  for (const list of groups) {
    for (const item of list || []) {
      const target = merged.get(item.group) || zeroTokenGroup(item.group)
      target.requests += Number(item.requests || 0)
      target.prompt_tokens += Number(item.prompt_tokens || 0)
      target.completion_tokens += Number(item.completion_tokens || 0)
      target.total_tokens += Number(item.total_tokens || 0)
      target.tool_calls = Number(target.tool_calls || 0) + Number(item.tool_calls || 0)
      target.turns_with_tools = Number(target.turns_with_tools || 0) + Number(item.turns_with_tools || 0)
      target.tool_related_tokens = Number(target.tool_related_tokens || 0) + Number(item.tool_related_tokens || 0)
      merged.set(item.group, target)
    }
  }
  return [...merged.values()].sort((a, b) => b.total_tokens - a.total_tokens)
}

export function mergeToolGroups(groups: ToolGroupCount[][]): ToolGroupCount[] {
  const merged = new Map<string, ToolGroupCount>()
  for (const list of groups) {
    for (const item of list || []) {
      const previous = merged.get(item.group)
      const calls = Number(item.calls || 0)
      if (!previous) {
        merged.set(item.group, { ...item })
        continue
      }
      const totalCalls = previous.calls + calls
      previous.average_duration_ms = totalCalls
        ? ((previous.average_duration_ms * previous.calls) + (Number(item.average_duration_ms || 0) * calls)) / totalCalls
        : 0
      previous.calls = totalCalls
      previous.successes += Number(item.successes || 0)
      previous.errors += Number(item.errors || 0)
      previous.success_rate = totalCalls ? previous.successes / totalCalls : 0
      previous.p95_duration_ms = Math.max(previous.p95_duration_ms, Number(item.p95_duration_ms || 0))
    }
  }
  return [...merged.values()].sort((a, b) => b.calls - a.calls)
}

function mergeToolSummary(items: ToolSummary[]): ToolSummary {
  const summary: ToolSummary = {
    calls: 0, turns_with_tools: 0, successes: 0, errors: 0, success_rate: 0,
    average_duration_ms: 0, p95_duration_ms: 0, tool_related_tokens: 0,
  }
  for (const item of items) {
    const calls = Number(item?.calls || 0)
    const nextCalls = summary.calls + calls
    summary.average_duration_ms = nextCalls
      ? ((summary.average_duration_ms * summary.calls) + (Number(item?.average_duration_ms || 0) * calls)) / nextCalls
      : 0
    summary.calls = nextCalls
    summary.turns_with_tools += Number(item?.turns_with_tools || 0)
    summary.successes += Number(item?.successes || 0)
    summary.errors += Number(item?.errors || 0)
    summary.p95_duration_ms = Math.max(summary.p95_duration_ms, Number(item?.p95_duration_ms || 0))
    summary.tool_related_tokens += Number(item?.tool_related_tokens || 0)
  }
  summary.success_rate = summary.calls ? summary.successes / summary.calls : 0
  return summary
}

export function mergeUsageStats(items: TokenStats[]): TokenStats {
  const first = items[0]
  const total = {
    requests: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0,
    tool_calls: 0, turns_with_tools: 0, tool_successes: 0, tool_errors: 0, tool_related_tokens: 0,
  }
  for (const item of items) {
    for (const key of Object.keys(total) as (keyof typeof total)[]) {
      total[key] += Number(item.total?.[key] || 0)
    }
  }
  return {
    window_days: first?.window_days || 0,
    total,
    by_business_scene: mergeTokenGroups(items.map((item) => item.by_business_scene || [])),
    by_business_operation: mergeTokenGroups(items.map((item) => item.by_business_operation || [])),
    by_attribution_mode: mergeTokenGroups(items.map((item) => item.by_attribution_mode || [])),
    by_model: mergeTokenGroups(items.map((item) => item.by_model || [])),
    by_kind: mergeTokenGroups(items.map((item) => item.by_kind || [])),
    by_source_type: mergeTokenGroups(items.map((item) => item.by_source_type || [])),
    by_raw_source: mergeTokenGroups(items.map((item) => item.by_raw_source || [])),
    by_status: mergeTokenGroups(items.map((item) => item.by_status || [])),
    by_day: [],
    tool_summary: mergeToolSummary(items.map((item) => item.tool_summary)),
    by_tool: mergeToolGroups(items.map((item) => item.by_tool || [])),
  }
}
