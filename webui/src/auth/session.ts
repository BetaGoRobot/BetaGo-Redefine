import { ref, type Ref } from 'vue'
import { isAutheliaMode, readWebUIAuthRuntime } from './runtime'

const AUTH_REQUIRED_EVENT = 'betago:auth-required'
const AUTH_COMPLETE_MESSAGE = 'betago:auth-complete'

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>

type PopupWindow = Pick<Window, 'closed' | 'close'>
type OpenWindow = (
  url?: string | URL,
  target?: string,
  features?: string,
) => PopupWindow | null

export interface ManagementSession {
  authenticated: Ref<boolean>
  checking: Ref<boolean>
  loginBusy: Ref<boolean>
  probe: () => Promise<boolean>
  beginLogin: () => boolean
  dispose: () => void
}

interface ManagementSessionOptions {
  fetcher?: Fetcher
  openWindow?: OpenWindow
  pollIntervalMs?: number
}

export function createManagementSession(
  options: ManagementSessionOptions = {},
): ManagementSession {
  const secure = isAutheliaMode()
  const fetcher = options.fetcher || window.fetch.bind(window)
  const openWindow = options.openWindow || window.open.bind(window)
  const pollIntervalMs = options.pollIntervalMs ?? 1_200
  const authenticated = ref(!secure)
  const checking = ref(false)
  const loginBusy = ref(false)
  let popup: PopupWindow | null = null
  let pollTimer: number | undefined

  function stopPolling() {
    if (pollTimer !== undefined) {
      window.clearInterval(pollTimer)
      pollTimer = undefined
    }
  }

  async function probe(): Promise<boolean> {
    if (!secure) {
      authenticated.value = true
      return true
    }
    checking.value = true
    try {
      const response = await fetcher(readWebUIAuthRuntime().sessionPath, {
        cache: 'no-store',
        credentials: 'include',
        redirect: 'manual',
      })
      if (!response.ok) {
        authenticated.value = false
        return false
      }
      const contentType = response.headers.get('content-type') || ''
      if (!contentType.toLowerCase().includes('application/json')) {
        authenticated.value = false
        return false
      }
      const body = await response.json() as { authenticated?: boolean }
      authenticated.value = body.authenticated === true
      return authenticated.value
    } catch {
      authenticated.value = false
      return false
    } finally {
      checking.value = false
    }
  }

  function completeLogin() {
    stopPolling()
    loginBusy.value = false
    if (popup && !popup.closed) popup.close()
    popup = null
  }

  function startPolling() {
    stopPolling()
    pollTimer = window.setInterval(async () => {
      if (popup?.closed) {
        popup = null
      }
      if (await probe()) completeLogin()
    }, pollIntervalMs)
  }

  function beginLogin(): boolean {
    if (!secure) {
      authenticated.value = true
      return true
    }
    const runtime = readWebUIAuthRuntime()
    const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`
    const separator = runtime.loginPath.includes('?') ? '&' : '?'
    const loginURL = `${runtime.loginPath}${separator}return=${encodeURIComponent(returnTo)}`
    popup = openWindow(
      loginURL,
      'betago-authelia-login',
      'popup=yes,width=520,height=720,resizable=yes,scrollbars=yes',
    )
    if (!popup) {
      // Keep probing while the gate offers a user-initiated new-tab fallback.
      // No write callback is retained or replayed.
      loginBusy.value = true
      startPolling()
      return false
    }
    loginBusy.value = true
    startPolling()
    return true
  }

  function onAuthRequired() {
    if (secure) authenticated.value = false
  }

  function onMessage(event: MessageEvent) {
    if (
      event.origin !== window.location.origin ||
      event.data !== AUTH_COMPLETE_MESSAGE
    ) return
    void probe().then((ok) => {
      if (ok) completeLogin()
    })
  }

  window.addEventListener(AUTH_REQUIRED_EVENT, onAuthRequired)
  window.addEventListener('message', onMessage)

  return {
    authenticated,
    checking,
    loginBusy,
    probe,
    beginLogin,
    dispose() {
      stopPolling()
      window.removeEventListener(AUTH_REQUIRED_EVENT, onAuthRequired)
      window.removeEventListener('message', onMessage)
    },
  }
}

export const managementSession = createManagementSession()
