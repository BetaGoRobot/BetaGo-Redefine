import { defineConfig, loadEnv, type ProxyOptions } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

/**
 * 解析环境变量中的 VITE_BOTS（JSON 数组）为预设列表。
 * 返回结构：{ id, name, baseURL, token?, remark? }[]
 */
interface BotPreset {
  id: string
  name: string
  baseURL?: string
  token?: string
  remark?: string
}

function parseBotPresets(raw: string | undefined): BotPreset[] {
  if (!raw || !raw.trim()) return []
  try {
    const arr = JSON.parse(raw)
    if (!Array.isArray(arr)) return []
    return arr
      .filter((b) => b && typeof b === 'object' && typeof b.id === 'string')
      .map((b) => ({
        id: String(b.id),
        name: String(b.name || b.id),
        baseURL: b.baseURL == null ? undefined : String(b.baseURL),
        token: b.token == null ? undefined : String(b.token),
        remark: b.remark == null ? undefined : String(b.remark),
      }))
  } catch {
    return []
  }
}

// 容器内反代版：浏览器永远只跟 webui 同源通信。
//   - dev 模式下 vite 模拟容器里 Caddy 的两段反代：
//       /api/*          → 默认后端（VITE_DEV_BACKEND）
//       /bot/<id>/api/* → 各 bot 内网 baseURL（来自 VITE_BOTS）
//   - 生产部署下，前端不直接连 baseURL，由容器内 Caddy 接 reverse_proxy。
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const defaultBackend = env.VITE_DEV_BACKEND || 'http://localhost:8090'
  const presets = parseBotPresets(env.VITE_BOTS)

  // 构建多源代理表
  const proxy: Record<string, string | ProxyOptions> = {
    '/api': {
      target: defaultBackend,
      changeOrigin: true,
    },
  }

  for (const p of presets) {
    const target = (p.baseURL && p.baseURL.trim()) || defaultBackend
    const key = `/bot/${encodeURIComponent(p.id)}/api`
    proxy[key] = {
      target,
      changeOrigin: true,
      rewrite: (path) => path.replace(key, '/api'),
    }
  }

  return {
    plugins: [vue()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: {
      port: 5173,
      proxy,
    },
  }
})
