import { beforeEach, describe, expect, it } from 'vitest'
import {
  isAutheliaMode,
  readWebUIAuthRuntime,
  stripBrowserCredentials,
} from './runtime'

describe('WebUI auth runtime', () => {
  beforeEach(() => {
    delete window.__BETAGO_CONFIG__
  })

  it('keeps legacy mode as the compatibility default', () => {
    expect(readWebUIAuthRuntime()).toEqual({
      mode: 'legacy',
      sessionPath: '/auth/session',
      loginPath: '/auth/login',
    })
    expect(isAutheliaMode()).toBe(false)
  })

  it('reads secure Authelia paths from runtime config', () => {
    window.__BETAGO_CONFIG__ = {
      authMode: 'authelia',
      sessionPath: '/management/session',
      loginPath: '/management/login',
    }

    expect(readWebUIAuthRuntime()).toEqual({
      mode: 'authelia',
      sessionPath: '/management/session',
      loginPath: '/management/login',
    })
    expect(isAutheliaMode()).toBe(true)
  })

  it('removes every browser credential field without mutating public metadata', () => {
    expect(stripBrowserCredentials({
      id: 'bot-one',
      name: 'Bot One',
      baseURL: 'http://private-upstream:8090',
      token: 'token-sentinel',
      api_key: 'api-key-sentinel',
      apiKey: 'camel-api-key-sentinel',
      secret: 'secret-sentinel',
      password: 'password-sentinel',
      remark: 'Primary',
    })).toEqual({
      id: 'bot-one',
      name: 'Bot One',
      baseURL: '',
      remark: 'Primary',
    })
  })
})
