export type WebUIAuthMode = 'legacy' | 'authelia'

export interface WebUIAuthRuntime {
  mode: WebUIAuthMode
  sessionPath: string
  loginPath: string
}

export function readWebUIAuthRuntime(): WebUIAuthRuntime {
  const config = typeof window !== 'undefined'
    ? window.__BETAGO_CONFIG__
    : undefined
  return {
    mode: config?.authMode === 'authelia' ? 'authelia' : 'legacy',
    sessionPath: config?.sessionPath || '/auth/session',
    loginPath: config?.loginPath || '/auth/login',
  }
}

export function isAutheliaMode(): boolean {
  return readWebUIAuthRuntime().mode === 'authelia'
}

type BrowserBotMetadata = {
  [key: string]: unknown
  id?: unknown
  name?: unknown
  baseURL?: unknown
  remark?: unknown
  healthy?: unknown
  robotName?: unknown
  instance?: unknown
  color?: unknown
  source?: unknown
}

/**
 * Produce an allowlisted browser-owned Bot record.
 *
 * Secure mode intentionally discards internal upstreams, credentials, and
 * unknown future fields instead of trying to recognize every possible secret
 * field name.
 */
export function stripBrowserCredentials<T extends object>(
  value: T,
): Record<string, unknown> {
  const source = value as BrowserBotMetadata
  const publicValue: Record<string, unknown> = {}
  for (const key of [
    'id',
    'name',
    'remark',
    'healthy',
    'robotName',
    'instance',
    'color',
    'source',
  ] as const) {
    if (source[key] !== undefined) {
      publicValue[key] = source[key]
    }
  }
  publicValue.baseURL = ''
  return publicValue
}
