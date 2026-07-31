import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import ManagementGate from './ManagementGate.vue'
import { managementSession } from '../auth/session'

const buttonStub = {
  props: ['loading'],
  emits: ['click'],
  template: '<button data-test="login" @click="$emit(\'click\')"><slot /></button>',
}

afterEach(() => {
  managementSession.authenticated.value = true
  vi.restoreAllMocks()
})

describe('ManagementGate', () => {
  it('does not mount protected content before login', async () => {
    managementSession.authenticated.value = false
    const beginLogin = vi
      .spyOn(managementSession, 'beginLogin')
      .mockReturnValue(true)
    const wrapper = mount(ManagementGate, {
      slots: {
        default: '<div data-test="secret">protected content</div>',
      },
      global: {
        stubs: { ElButton: buttonStub },
      },
    })

    expect(wrapper.text()).toContain('登录后管理')
    expect(wrapper.find('[data-test="secret"]').exists()).toBe(false)

    await wrapper.get('[data-test="login"]').trigger('click')
    expect(beginLogin).toHaveBeenCalledTimes(1)

    managementSession.authenticated.value = true
    await nextTick()
    expect(wrapper.find('[data-test="secret"]').exists()).toBe(true)
  })
})
