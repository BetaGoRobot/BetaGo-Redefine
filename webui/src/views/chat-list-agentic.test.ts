import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/views/ChatList.vue'),
  'utf8',
)

describe('ChatList agentic rollout safety', () => {
  it('only exposes selection and batch actions in a single-bot view', () => {
    expect(source).toContain('v-if="rolloutBotID"')
    expect(source).toContain('type="selection"')
    expect(source).toContain('reserve-selection')
    expect(source).toContain('selectedRows')
  })

  it('uses a stable cross-bot row key and ignores selection-cell navigation', () => {
    expect(source).toContain(':row-key="rowKey"')
    expect(source).toContain('`${row.bot_id}::${row.chat_id}`')
    expect(source).toContain("column?.type === 'selection'")
  })

  it('loads rollout summaries in 100-chat chunks', () => {
    expect(source).toContain('chunkChatIDs')
    expect(source).toContain('getAgenticRollouts')
    expect(source).toContain('summarizeRollout')
  })

  it('uses semantic warm operations layout hooks', () => {
    expect(source).toContain('class="chat-ops-page"')
    expect(source).toContain('class="chat-ops-summary"')
    expect(source).toContain('class="chat-ops-toolbar')
  })
})
