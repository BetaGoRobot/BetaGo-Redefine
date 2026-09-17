import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ModelOptionsEditor from './ModelOptionsEditor.vue'

describe('ModelOptionsEditor', () => {
  it('edits model options and emits typed configuration on save', async () => {
    const wrapper = mount(ModelOptionsEditor, {
      props: { value: '{"ep-target":{"service_tier":"flex","reasoning_effort":"high"}}' },
    })
    expect((wrapper.get('[aria-label="模型或 Endpoint ID"]').element as HTMLInputElement).value).toBe('ep-target')
    await wrapper.get('[aria-label="服务等级"]').setValue('default')
    await wrapper.get('[aria-label="推理强度"]').setValue('')
    await wrapper.get('[data-test="save"]').trigger('click')
    expect(JSON.parse(wrapper.emitted('save')![0][0] as string)).toEqual({ 'ep-target': { service_tier: 'default' } })
  })

  it('rejects duplicate or empty model IDs before saving', async () => {
    const wrapper = mount(ModelOptionsEditor, { props: { value: '{"target":{"service_tier":"flex"}}' } })
    await wrapper.get('[data-test="add"]').trigger('click')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    await wrapper.findAll('[aria-label="模型或 Endpoint ID"]')[1].setValue('target')
    expect(wrapper.text()).toContain('模型 ID 重复')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
  })

  it('supports clearing all overrides and resetting to inherited settings', async () => {
    const wrapper = mount(ModelOptionsEditor, { props: { value: '{"target":{"service_tier":"flex"}}' } })
    await wrapper.get('[aria-label="删除模型配置"]').trigger('click')
    await wrapper.get('[data-test="save"]').trigger('click')
    expect(wrapper.emitted('save')![0]).toEqual(['{}'])
    await wrapper.get('[data-test="reset"]').trigger('click')
    expect(wrapper.emitted('reset')).toHaveLength(1)
    await wrapper.setProps({ value: '{"inherited":{"reasoning_effort":"low"}}' })
    expect((wrapper.get('[aria-label="模型或 Endpoint ID"]').element as HTMLInputElement).value).toBe('inherited')
  })

  it('does not silently discard unsupported configuration', () => {
    const wrapper = mount(ModelOptionsEditor, { props: { value: '{"target":{"unknown":"value"}}' } })
    expect(wrapper.get('[role="alert"]').text()).toContain('无法编辑')
    expect(wrapper.get('[data-test="save"]').attributes('disabled')).toBeDefined()
  })

  it('discards unsaved edits on reset even when the inherited value is unchanged', async () => {
    const wrapper = mount(ModelOptionsEditor, { props: { value: '{"target":{"service_tier":"flex"}}' } })
    await wrapper.get('[aria-label="服务等级"]').setValue('fast')
    await wrapper.get('[data-test="reset"]').trigger('click')
    expect((wrapper.get('[aria-label="服务等级"]').element as HTMLSelectElement).value).toBe('flex')
  })
})
