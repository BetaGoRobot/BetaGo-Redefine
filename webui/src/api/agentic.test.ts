import { describe, expect, it, vi } from 'vitest'
import {
  buildFullAgenticChanges,
  chunkChatIDs,
  summarizeRollout,
} from './agentic'
import { BotApi } from './client'
import type {
  AgenticBatchRequest,
  AgenticCapabilityState,
  AgenticUpdateRequest,
} from './types'
import type { BotInstance } from '../stores/filter'

const inheritedOffCapabilities = [
  'conversation_runtime',
  'callback_continuation',
  'parallel_evaluation',
  'agent_card',
].map((key) => ({
  key,
  label: key,
  override: 'inherit',
  baseline: false,
  effective: false,
  source: 'default',
  available: true,
})) as AgenticCapabilityState[]

const testBot: BotInstance = {
  id: 'bot-a',
  name: 'Bot A',
  baseURL: '',
}

describe('agentic rollout helpers', () => {
  it('restores all capabilities to inherit', () => {
    expect(buildFullAgenticChanges('inherit')).toEqual({
      conversation_runtime: 'inherit',
      callback_continuation: 'inherit',
      parallel_evaluation: 'inherit',
      agent_card: 'inherit',
    })
  })

  it('chunks rollout reads at 100 chats without mutating the input', () => {
    const chatIDs = Array.from({ length: 201 }, (_, index) => `oc_${index}`)
    expect(chunkChatIDs(chatIDs).map((chunk) => chunk.length)).toEqual([100, 100, 1])
    expect(chatIDs).toHaveLength(201)
  })

  it('labels inherited disabled state explicitly', () => {
    expect(summarizeRollout({
      bot: { id: 'bot-a', name: 'Bot A' },
      chat_id: 'oc_1',
      revision: 'r1',
      capabilities: inheritedOffCapabilities,
    })).toBe('4 项继承关闭')
  })
})

describe('BotApi agentic rollout contract', () => {
  it('reads a single chat through the bot-bound route', async () => {
    const api = new BotApi(testBot)
    const state = {
      bot: { id: 'bot-a', name: 'Bot A' },
      chat_id: 'oc_1',
      revision: 'r1',
      capabilities: inheritedOffCapabilities,
    }
    const get = vi.spyOn(api.http, 'get').mockResolvedValue({ data: state })

    await expect(api.getAgenticRollout('oc_1')).resolves.toEqual(state)
    expect(get).toHaveBeenCalledWith('/chats/oc_1/agentic-rollout')
  })

  it('sends batch reads as one comma-separated query parameter', async () => {
    const api = new BotApi(testBot)
    const get = vi.spyOn(api.http, 'get').mockResolvedValue({
      data: { items: [], total: 0 },
    })

    await api.getAgenticRollouts(['oc_2', 'oc_1'])

    expect(get).toHaveBeenCalledWith('/agentic-rollouts', {
      params: { chat_ids: 'oc_2,oc_1' },
    })
  })

  it('keeps single and batch mutation payloads tenant-free', async () => {
    const api = new BotApi(testBot)
    const put = vi.spyOn(api.http, 'put').mockResolvedValue({
      data: { dry_run: false, items: [] },
    })
    const post = vi.spyOn(api.http, 'post').mockResolvedValue({
      data: { dry_run: true, items: [] },
    })
    const update: AgenticUpdateRequest = {
      expected_revision: 'r1',
      changes: { conversation_runtime: 'enabled' },
    }
    const batch: AgenticBatchRequest = {
      dry_run: true,
      chat_ids: ['oc_1'],
      expected_revisions: { oc_1: 'r1' },
      changes: { agent_card: 'disabled' },
    }

    await api.updateAgenticRollout('oc_1', update)
    await api.batchAgenticRollout(batch)

    expect(put).toHaveBeenCalledWith('/chats/oc_1/agentic-rollout', update)
    expect(post).toHaveBeenCalledWith('/agentic-rollouts/batch', batch)
  })
})
