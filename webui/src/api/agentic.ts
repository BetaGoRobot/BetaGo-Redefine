import type {
  AgenticCapabilityKey,
  AgenticChanges,
  AgenticChatState,
  AgenticOverride,
} from './types'

export const AGENTIC_CAPABILITY_KEYS: readonly AgenticCapabilityKey[] = [
  'conversation_runtime',
  'callback_continuation',
  'parallel_evaluation',
  'agent_card',
]

export const AGENTIC_CAPABILITY_COPY: Record<
  AgenticCapabilityKey,
  { title: string; description: string }
> = {
  conversation_runtime: {
    title: 'Conversation Runtime',
    description: '让 Agent 持续理解群聊上下文，并在合适的时机主动参与。',
  },
  callback_continuation: {
    title: '回调续接',
    description: '把卡片点击等交互重新作为 Agent 输入，继续原有任务。',
  },
  parallel_evaluation: {
    title: '并轨评测',
    description: '同步收集候选链路、前后向消息与用户反馈，评估回复质量。',
  },
  agent_card: {
    title: 'Agent Card',
    description: '允许 Agent 按需构造并实时投递原生互动卡片。',
  },
}

export function buildFullAgenticChanges(
  override: AgenticOverride,
): AgenticChanges {
  return Object.fromEntries(
    AGENTIC_CAPABILITY_KEYS.map((key) => [key, override]),
  ) as AgenticChanges
}

export function buildCurrentAgenticChanges(
  state: AgenticChatState,
): AgenticChanges {
  return Object.fromEntries(
    state.capabilities.map((capability) => [
      capability.key,
      capability.override,
    ]),
  ) as AgenticChanges
}

export function updateAgenticChange(
  changes: AgenticChanges,
  key: AgenticCapabilityKey,
  override: AgenticOverride,
): AgenticChanges {
  return { ...changes, [key]: override }
}

export function chunkChatIDs(
  chatIDs: readonly string[],
  size = 100,
): string[][] {
  if (!Number.isInteger(size) || size <= 0) {
    throw new Error('chunk size must be a positive integer')
  }
  const chunks: string[][] = []
  for (let index = 0; index < chatIDs.length; index += size) {
    chunks.push(chatIDs.slice(index, index + size))
  }
  return chunks
}

export function summarizeRollout(state: AgenticChatState): string {
  const total = state.capabilities.length
  const inheritedOff = state.capabilities.filter(
    (capability) =>
      capability.override === 'inherit' && !capability.effective,
  ).length
  if (total > 0 && inheritedOff === total) {
    return `${total} 项继承关闭`
  }

  const effective = state.capabilities.filter(
    (capability) => capability.effective,
  ).length
  const unavailable = state.capabilities.filter(
    (capability) => !capability.available,
  ).length
  const explicit = state.capabilities.filter(
    (capability) => capability.override !== 'inherit',
  ).length
  const parts = [`${effective}/${total} 项生效`]
  if (explicit > 0) parts.push(`${explicit} 项单独设置`)
  if (unavailable > 0) parts.push(`${unavailable} 项不可用`)
  return parts.join(' · ')
}
