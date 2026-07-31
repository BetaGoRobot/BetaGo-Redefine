import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AgenticCapabilityCard from './AgenticCapabilityCard.vue'
import AgenticBatchDrawer from './AgenticBatchDrawer.vue'
import AgenticRolloutPanel from './AgenticRolloutPanel.vue'
import { BotApi } from '../api/client'
import type {
  AgenticCapabilityKey,
  AgenticCapabilityState,
  AgenticChatState,
} from '../api/types'
import type { BotInstance } from '../stores/filter'

const keys: AgenticCapabilityKey[] = [
  'conversation_runtime',
  'callback_continuation',
  'parallel_evaluation',
  'agent_card',
]

function capability(
  key: AgenticCapabilityKey,
  patch: Partial<AgenticCapabilityState> = {},
): AgenticCapabilityState {
  return {
    key,
    label: key,
    override: 'inherit',
    baseline: false,
    effective: false,
    source: 'default',
    available: true,
    ...patch,
  }
}

function chatState(revision = 'r1'): AgenticChatState {
  return {
    bot: { id: 'bot-a', name: 'Bot A' },
    chat_id: 'oc_1',
    revision,
    capabilities: keys.map((key) => capability(key)),
  }
}

const bot: BotInstance = {
  id: 'bot-a',
  name: 'Bot A',
  baseURL: '',
}

const segmentedStub = {
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        data-test="enable"
        :disabled="disabled"
        @click="$emit('update:modelValue', 'enabled')"
      >enable</button>
    </div>
  `,
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('AgenticCapabilityCard', () => {
  it('shows inherited effective state and emits an explicit override', async () => {
    const wrapper = mount(AgenticCapabilityCard, {
      props: {
        state: capability('conversation_runtime'),
        modelValue: 'inherit',
      },
      global: {
        stubs: { ElSegmented: segmentedStub },
      },
    })

    expect(wrapper.text()).toContain('继承')
    expect(wrapper.text()).toContain('当前关闭')
    await wrapper.get('[data-test="enable"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['enabled'])
  })

  it('disables unavailable controls and shows the reason', () => {
    const wrapper = mount(AgenticCapabilityCard, {
      props: {
        state: capability('agent_card', {
          available: false,
          reason: 'agent_card_shadow_mode',
        }),
        modelValue: 'inherit',
      },
      global: {
        stubs: { ElSegmented: segmentedStub },
      },
    })

    expect(wrapper.get('[data-test="enable"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('agent_card_shadow_mode')
    expect(wrapper.text()).toContain('当前不可用')
  })
})

describe('AgenticRolloutPanel', () => {
  it('expands Full Agentic and restore presets to all four capabilities', async () => {
    vi.spyOn(BotApi.prototype, 'getAgenticRollout').mockResolvedValue(chatState())
    const wrapper = mount(AgenticRolloutPanel, {
      props: { bot, chatId: 'oc_1' },
      global: {
        stubs: {
          AgenticCapabilityCard: true,
          ElButton: {
            emits: ['click'],
            template: '<button @click="$emit(\'click\')"><slot /></button>',
          },
          ElSkeleton: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('[data-test="full-agentic"]').trigger('click')
    const fullValues = wrapper
      .findAllComponents({ name: 'AgenticCapabilityCard' })
      .map((card) => card.props('modelValue'))
    expect(fullValues).toEqual(['enabled', 'enabled', 'enabled', 'enabled'])

    await wrapper.get('[data-test="restore-inherit"]').trigger('click')
    const inheritedValues = wrapper
      .findAllComponents({ name: 'AgenticCapabilityCard' })
      .map((card) => card.props('modelValue'))
    expect(inheritedValues).toEqual(['inherit', 'inherit', 'inherit', 'inherit'])
  })

  it('submits only dirty keys and reloads authoritative state', async () => {
    const get = vi
      .spyOn(BotApi.prototype, 'getAgenticRollout')
      .mockResolvedValueOnce(chatState('r1'))
      .mockResolvedValueOnce(chatState('r2'))
    const update = vi
      .spyOn(BotApi.prototype, 'updateAgenticRollout')
      .mockResolvedValue({ dry_run: false, items: [] })
    const wrapper = mount(AgenticRolloutPanel, {
      props: { bot, chatId: 'oc_1' },
      global: {
        stubs: {
          ElSegmented: segmentedStub,
          ElButton: {
            props: ['disabled', 'loading'],
            emits: ['click'],
            template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
          },
          ElSkeleton: true,
        },
      },
    })
    await flushPromises()

    await wrapper.findAll('[data-test="enable"]')[0].trigger('click')
    await wrapper.get('[data-test="save-rollout"]').trigger('click')
    await flushPromises()

    expect(update).toHaveBeenCalledWith('oc_1', {
      expected_revision: 'r1',
      changes: { conversation_runtime: 'enabled' },
    })
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('keeps the draft after a stale revision and shows a conflict', async () => {
    vi.spyOn(BotApi.prototype, 'getAgenticRollout')
      .mockResolvedValueOnce(chatState('r1'))
      .mockResolvedValueOnce(chatState('r2'))
    vi.spyOn(BotApi.prototype, 'updateAgenticRollout').mockRejectedValue({
      response: { status: 409, data: { code: 'stale_revision' } },
    })
    const wrapper = mount(AgenticRolloutPanel, {
      props: { bot, chatId: 'oc_1' },
      global: {
        stubs: {
          AgenticCapabilityCard: true,
          ElButton: {
            props: ['disabled', 'loading'],
            emits: ['click'],
            template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
          },
          ElSkeleton: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('[data-test="full-agentic"]').trigger('click')
    await wrapper.get('[data-test="save-rollout"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('状态已变化')
    expect(
      wrapper
        .findAllComponents({ name: 'AgenticCapabilityCard' })
        .map((card) => card.props('modelValue')),
    ).toEqual(['enabled', 'enabled', 'enabled', 'enabled'])
  })
})

describe('AgenticBatchDrawer', () => {
  const drawerStubs = {
    ElDrawer: {
      props: ['modelValue'],
      emits: ['update:modelValue'],
      template: '<section><slot /><slot name="footer" /></section>',
    },
    ElSegmented: {
      props: ['modelValue', 'options', 'disabled'],
      emits: ['update:modelValue'],
      template: '<div />',
    },
    ElButton: {
      props: ['disabled', 'loading'],
      emits: ['click'],
      template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
    },
    ElAlert: {
      props: ['title', 'description'],
      template: '<div>{{ title }} {{ description }}</div>',
    },
    ElTable: true,
    ElTableColumn: true,
  }

  it('expands Full Agentic and restore presets for a batch', async () => {
    const wrapper = mount(AgenticBatchDrawer, {
      props: {
        modelValue: true,
        bot,
        states: [chatState()],
      },
      global: { stubs: drawerStubs },
    })

    await wrapper.get('[data-test="batch-full-agentic"]').trigger('click')
    expect(wrapper.get('[data-test="batch-draft"]').text()).toContain(
      '"conversation_runtime":"enabled"',
    )
    expect(wrapper.get('[data-test="batch-draft"]').text()).toContain(
      '"agent_card":"enabled"',
    )

    await wrapper.get('[data-test="batch-restore-inherit"]').trigger('click')
    expect(wrapper.get('[data-test="batch-draft"]').text()).toContain(
      '"parallel_evaluation":"inherit"',
    )
  })

  it('previews first and commits with revisions from the preview', async () => {
    const previewState = chatState('r1')
    const previewAfter = chatState('projected')
    const batch = vi
      .spyOn(BotApi.prototype, 'batchAgenticRollout')
      .mockResolvedValueOnce({
        dry_run: true,
        items: [{
          chat_id: 'oc_1',
          before: previewState,
          after: previewAfter,
        }],
      })
      .mockResolvedValueOnce({
        dry_run: false,
        items: [{
          chat_id: 'oc_1',
          before: previewState,
          after: previewAfter,
        }],
      })
    const wrapper = mount(AgenticBatchDrawer, {
      props: {
        modelValue: true,
        bot,
        states: [previewState],
      },
      global: { stubs: drawerStubs },
    })

    await wrapper.get('[data-test="batch-full-agentic"]').trigger('click')
    await wrapper.get('[data-test="batch-preview"]').trigger('click')
    await flushPromises()
    expect(batch.mock.calls[0][0]).toMatchObject({
      dry_run: true,
      expected_revisions: { oc_1: 'r1' },
    })

    await wrapper.get('[data-test="batch-commit"]').trigger('click')
    await flushPromises()
    expect(batch.mock.calls[1][0]).toMatchObject({
      dry_run: false,
      expected_revisions: { oc_1: 'r1' },
    })
    expect(wrapper.emitted('committed')).toHaveLength(1)
  })

  it('keeps the draft and requests refresh on commit conflict', async () => {
    const previewState = chatState('r1')
    vi.spyOn(BotApi.prototype, 'batchAgenticRollout')
      .mockResolvedValueOnce({
        dry_run: true,
        items: [{
          chat_id: 'oc_1',
          before: previewState,
          after: chatState('projected'),
        }],
      })
      .mockRejectedValueOnce({
        response: { status: 409, data: { code: 'stale_revision' } },
      })
    const wrapper = mount(AgenticBatchDrawer, {
      props: {
        modelValue: true,
        bot,
        states: [previewState],
      },
      global: { stubs: drawerStubs },
    })

    await wrapper.get('[data-test="batch-full-agentic"]').trigger('click')
    await wrapper.get('[data-test="batch-preview"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="batch-commit"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.text()).toContain('状态已变化')
    expect(wrapper.get('[data-test="batch-draft"]').text()).toContain(
      '"agent_card":"enabled"',
    )
  })
})
