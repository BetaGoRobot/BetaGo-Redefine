import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { useFilterStore } from '../stores/filter'

describe('secure Bot persistence', () => {
  beforeEach(() => {
    localStorage.clear()
    window.__BETAGO_CONFIG__ = {
      authMode: 'authelia',
      bots: [],
    }
    setActivePinia(createPinia())
  })

  it('purges legacy browser credentials during startup and future writes', async () => {
    localStorage.setItem('betago_webui_bots_v1', JSON.stringify([{
      id: 'legacy-bot',
      name: 'Legacy Bot',
      baseURL: 'http://private-upstream:8090',
      token: 'legacy-token-sentinel',
      remark: 'Primary',
    }]))

    const store = useFilterStore()

    expect(store.bots[0]).toMatchObject({
      id: 'legacy-bot',
      name: 'Legacy Bot',
      baseURL: '',
      remark: 'Primary',
    })
    expect(store.bots[0].token).toBeUndefined()
    expect(localStorage.getItem('betago_webui_bots_v1')).not.toContain(
      'legacy-token-sentinel',
    )

    store.updateBot('legacy-bot', {
      token: 'new-token-sentinel',
      baseURL: 'http://new-private-upstream:8090',
    })
    await nextTick()

    const persisted = localStorage.getItem('betago_webui_bots_v1') || ''
    expect(persisted).not.toContain('new-token-sentinel')
    expect(persisted).not.toContain('new-private-upstream')
  })
})
