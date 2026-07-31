import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createManagementSession } from './session'

describe('management session', () => {
  beforeEach(() => {
    window.__BETAGO_CONFIG__ = {
      authMode: 'authelia',
      sessionPath: '/auth/session',
      loginPath: '/auth/login',
    }
  })

  it('starts locked and unlocks only after a successful session probe', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ authenticated: true }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    ))
    const session = createManagementSession({ fetcher })

    expect(session.authenticated.value).toBe(false)
    await expect(session.probe()).resolves.toBe(true)
    expect(fetcher).toHaveBeenCalledWith('/auth/session', {
      cache: 'no-store',
      credentials: 'include',
      redirect: 'manual',
    })
    expect(session.authenticated.value).toBe(true)
  })

  it('returns to read-only mode when an authenticated request expires', async () => {
    const session = createManagementSession({
      fetcher: vi.fn().mockResolvedValue(new Response(
        JSON.stringify({ authenticated: true }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )),
    })
    await session.probe()

    window.dispatchEvent(new CustomEvent('betago:auth-required'))

    expect(session.authenticated.value).toBe(false)
  })

  it('opens a login bridge without accepting or replaying a write callback', () => {
    const openWindow = vi.fn().mockReturnValue({ closed: false, close: vi.fn() })
    const session = createManagementSession({
      fetcher: vi.fn(),
      openWindow,
    })

    session.beginLogin()

    expect(openWindow).toHaveBeenCalledTimes(1)
    expect(openWindow.mock.calls[0][0]).toMatch(/^\/auth\/login\?return=/)
    expect(openWindow.mock.calls[0][1]).toBe('betago-authelia-login')
  })
})
