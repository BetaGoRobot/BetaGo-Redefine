/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface ImportMetaEnv {
  readonly VITE_API_BASE: string
  readonly VITE_DEV_BACKEND: string
  readonly VITE_BOTS: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

interface BetaGoRuntimeConfig {
  /** 等价于 VITE_BOTS：JSON 字符串或已解析数组。 */
  bots?: string | unknown[]
  /** 同源 /api 之外的默认 baseURL，可选。 */
  apiBase?: string
  /** legacy 保持浏览器 bearer；authelia 使用服务端凭据与管理 Session。 */
  authMode?: 'legacy' | 'authelia'
  /** 受 Authelia 保护的管理 Session 探测路径。 */
  sessionPath?: string
  /** 受 Authelia 保护的弹窗登录桥接路径。 */
  loginPath?: string
}

interface Window {
  __BETAGO_CONFIG__?: BetaGoRuntimeConfig
}
