import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function source(path: string): string {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
}

describe('WebUI visual system', () => {
  it('loads one global theme and one application navigation shell', () => {
    const main = source('src/main.ts')
    const app = source('src/App.vue')

    expect(main).toContain("import './styles/theme.css'")
    expect(main).toContain("import zhCn from 'element-plus/es/locale/lang/zh-cn'")
    expect(main).toContain('.use(ElementPlus, { locale: zhCn })')
    expect(app).toContain('class="app-shell"')
    expect(app).toContain('class="app-nav"')
    expect(app).toContain('class="app-workspace"')
  })

  it('uses the shared filter dock without duplicating route navigation', () => {
    const filter = source('src/components/GlobalFilterBar.vue')

    expect(filter).toContain('class="filter-dock"')
    expect(filter).toContain('class="filter-dock__controls"')
    expect(filter).not.toContain('总览仪表盘')
    expect(filter).not.toContain('会话列表')
  })

  it('gives Dashboard an intentional empty state and responsive grids', () => {
    const dashboard = source('src/views/Dashboard.vue')

    expect(dashboard).toContain('class="dashboard-page"')
    expect(dashboard).toContain('class="dashboard-empty"')
    expect(dashboard).toContain('class="metric-grid"')
    expect(dashboard).toContain('class="chart-grid chart-grid--four"')
  })

  it('renders mobile chat cards instead of relying on table overflow', () => {
    const chatList = source('src/views/ChatList.vue')

    expect(chatList).toContain('class="desktop-chat-table"')
    expect(chatList).toContain('class="mobile-chat-list"')
    expect(chatList).toContain('class="mobile-chat-card"')
  })

  it('keeps management login controls touch-friendly on mobile', () => {
    const app = source('src/App.vue')
    const gate = source('src/components/ManagementGate.vue')

    expect(app).toMatch(
      /\.app-auth-status,[\s\S]*\.app-runtime-status \{[\s\S]*min-height: 2\.75rem/,
    )
    expect(gate).toContain('min-height: 2.75rem')
    expect(gate).toContain('font-size: 0.875rem')
  })
})
