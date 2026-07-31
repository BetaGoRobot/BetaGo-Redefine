import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/views/ChatDetail.vue'),
  'utf8',
)

describe('ChatDetail agentic integration', () => {
  it('adds a bot-bound Agentic rollout tab', () => {
    expect(source).toContain('name="agentic"')
    expect(source).toContain('<AgenticRolloutPanel')
    expect(source).toContain(':bot="bot"')
    expect(source).toContain(':chat-id="props.chatID"')
  })

  it('keeps rollout-owned settings out of the generic config table', () => {
    expect(source).toContain("management_surface !== 'agentic_rollout'")
    expect(source).toContain(':data="genericConfigs"')
  })

  it('uses the approved warm operations visual hooks', () => {
    expect(source).toContain('class="chat-detail-ops"')
    expect(source).toContain('class="chat-detail-header"')
    expect(source).toContain('--ops-pine-900')
  })

  it('keeps protected data behind management mode', () => {
    expect(source).toContain('<ManagementGate')
    expect(source).toContain('loadProtectedIdentityInsights')
    expect(source).toContain('loadProtectedTab')
    expect(source).toContain('managementSession.authenticated')
    expect(source).not.toMatch(
      /await Promise\.all\(\[[\s\S]*loadStats\(\),[\s\S]*loadMembers\(\),[\s\S]*\]\)/,
    )
  })
})
