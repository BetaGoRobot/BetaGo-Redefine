<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { BotApi } from '../api/client'
import {
  AGENTIC_CAPABILITY_COPY,
  AGENTIC_CAPABILITY_KEYS,
  buildFullAgenticChanges,
  updateAgenticChange,
} from '../api/agentic'
import type {
  AgenticBatchResult,
  AgenticCapabilityKey,
  AgenticChanges,
  AgenticChatState,
  AgenticOverride,
} from '../api/types'
import type { BotInstance } from '../stores/filter'

const props = defineProps<{
  modelValue: boolean
  bot: BotInstance
  states: AgenticChatState[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  committed: []
  refresh: []
}>()

const draft = ref<AgenticChanges>({})
const preview = ref<AgenticBatchResult | null>(null)
const previewing = ref(false)
const committing = ref(false)
const errorMessage = ref('')
const conflict = ref(false)
const drawerSize = ref('40rem')

const batchOptions = [
  { label: '不修改', value: '' },
  { label: '继承', value: 'inherit' },
  { label: '开启', value: 'enabled' },
  { label: '关闭', value: 'disabled' },
]

const selectedCount = computed(() => props.states.length)
const changeCount = computed(() => Object.keys(draft.value).length)
const canPreview = computed(
  () => selectedCount.value > 0 && changeCount.value > 0,
)
const canCommit = computed(
  () => !!preview.value && !errorMessage.value && !conflict.value,
)

function updateDrawerSize() {
  drawerSize.value = window.innerWidth < 768 ? '100%' : '40rem'
}

function resetWorkflow() {
  draft.value = {}
  preview.value = null
  errorMessage.value = ''
  conflict.value = false
}

function close() {
  emit('update:modelValue', false)
}

function applyPreset(value: AgenticOverride) {
  draft.value = buildFullAgenticChanges(value)
  preview.value = null
  errorMessage.value = ''
  conflict.value = false
}

function setCapability(
  key: AgenticCapabilityKey,
  value: AgenticOverride | '',
) {
  if (value === '') {
    const next = { ...draft.value }
    delete next[key]
    draft.value = next
  } else {
    draft.value = updateAgenticChange(draft.value, key, value)
  }
  preview.value = null
  errorMessage.value = ''
  conflict.value = false
}

function revisionsFromStates(states: AgenticChatState[]) {
  return Object.fromEntries(
    states.map((state) => [state.chat_id, state.revision]),
  )
}

async function runPreview() {
  if (!canPreview.value) return
  previewing.value = true
  preview.value = null
  errorMessage.value = ''
  conflict.value = false
  try {
    preview.value = await new BotApi(props.bot).batchAgenticRollout({
      dry_run: true,
      chat_ids: props.states.map((state) => state.chat_id),
      expected_revisions: revisionsFromStates(props.states),
      changes: { ...draft.value },
    })
  } catch (error: any) {
    errorMessage.value =
      error?.response?.data?.error || error?.message || '预览失败'
    if (error?.response?.status === 409) {
      conflict.value = true
      emit('refresh')
    }
  } finally {
    previewing.value = false
  }
}

async function commit() {
  if (!preview.value || !canCommit.value) return
  committing.value = true
  errorMessage.value = ''
  try {
    const previewStates = preview.value.items.map((item) => item.before)
    await new BotApi(props.bot).batchAgenticRollout({
      dry_run: false,
      chat_ids: previewStates.map((state) => state.chat_id),
      expected_revisions: revisionsFromStates(previewStates),
      changes: { ...draft.value },
    })
    ElMessage.success(`已更新 ${previewStates.length} 个会话`)
    emit('committed')
    close()
  } catch (error: any) {
    errorMessage.value =
      error?.response?.data?.error || error?.message || '批量提交失败'
    if (error?.response?.status === 409) {
      conflict.value = true
      preview.value = null
      emit('refresh')
    }
  } finally {
    committing.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) resetWorkflow()
  },
)

onMounted(() => {
  updateDrawerSize()
  window.addEventListener('resize', updateDrawerSize)
})
onBeforeUnmount(() => window.removeEventListener('resize', updateDrawerSize))
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    :size="drawerSize"
    direction="rtl"
    class="agentic-batch-drawer"
    :with-header="false"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="batch-drawer">
      <header class="batch-drawer__header">
        <div>
          <p>AGENTIC BATCH · {{ bot.robotName || bot.name }}</p>
          <h2>批量灰度 {{ selectedCount }} 个会话</h2>
          <span>仅作用于当前 Bot。提交前必须先经过服务端 dry-run。</span>
        </div>
        <el-button aria-label="关闭批量灰度抽屉" @click="close">关闭</el-button>
      </header>

      <section class="batch-drawer__presets">
        <div>
          <strong>快捷预设</strong>
          <span>可继续逐项改成“不修改”。</span>
        </div>
        <div>
          <el-button
            data-test="batch-restore-inherit"
            @click="applyPreset('inherit')"
          >
            全部恢复继承
          </el-button>
          <el-button
            data-test="batch-full-agentic"
            class="batch-primary"
            @click="applyPreset('enabled')"
          >
            Full Agentic
          </el-button>
        </div>
      </section>

      <section class="batch-drawer__capabilities">
        <article
          v-for="key in AGENTIC_CAPABILITY_KEYS"
          :key="key"
          class="batch-capability"
        >
          <div>
            <strong>{{ AGENTIC_CAPABILITY_COPY[key].title }}</strong>
            <span>{{ AGENTIC_CAPABILITY_COPY[key].description }}</span>
          </div>
          <el-segmented
            :model-value="draft[key] || ''"
            :options="batchOptions"
            :disabled="previewing || committing"
            :aria-label="`${AGENTIC_CAPABILITY_COPY[key].title} 批量状态`"
            @update:model-value="
              setCapability(key, $event as AgenticOverride | '')
            "
          />
        </article>
      </section>

      <output data-test="batch-draft" class="batch-drawer__test-state" aria-hidden="true">
        {{ JSON.stringify(draft) }}
      </output>

      <el-alert
        v-if="conflict"
        title="状态已变化"
        description="已保留批量草稿，请等待列表刷新后重新预览。"
        type="warning"
        :closable="false"
        show-icon
      />
      <el-alert
        v-else-if="errorMessage"
        title="本次操作未执行"
        :description="errorMessage"
        type="error"
        :closable="false"
        show-icon
      />

      <section v-if="preview" class="batch-drawer__preview">
        <header>
          <div>
            <strong>Dry-run 已通过</strong>
            <span>{{ preview.items.length }} 个会话将被原子更新</span>
          </div>
          <span class="batch-drawer__ready">可以提交</span>
        </header>
        <el-table :data="preview.items" size="small" max-height="240">
          <el-table-column prop="chat_id" label="Chat ID" min-width="180" />
          <el-table-column label="变更后生效项" width="120">
            <template #default="{ row }">
              {{
                row.after.capabilities.filter(
                  (capability: any) => capability.effective,
                ).length
              }}/4
            </template>
          </el-table-column>
        </el-table>
      </section>
    </div>

    <template #footer>
      <footer class="batch-drawer__footer">
        <div>
          <strong>{{ changeCount }} 项能力将修改</strong>
          <span v-if="!preview">先预览，确认后才能提交。</span>
          <span v-else>预览通过；提交仍会再次校验 revision。</span>
        </div>
        <div>
          <el-button
            data-test="batch-preview"
            :loading="previewing"
            :disabled="!canPreview || committing"
            @click="runPreview"
          >
            预览变更
          </el-button>
          <el-button
            data-test="batch-commit"
            class="batch-primary"
            :loading="committing"
            :disabled="!canCommit || previewing"
            @click="commit"
          >
            确认批量提交
          </el-button>
        </div>
      </footer>
    </template>
  </el-drawer>
</template>

<style scoped>
.batch-drawer {
  --batch-pine: #143b36;
  --batch-lime: #d7ff73;
  --batch-border: #e6e3da;
  display: grid;
  gap: 1rem;
  min-height: 100%;
}

.batch-drawer__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 1.3rem;
  border-radius: 1rem;
  background: var(--batch-pine);
  color: #fff;
}

.batch-drawer__header p {
  margin: 0 0 0.5rem;
  color: var(--batch-lime);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.7rem;
  letter-spacing: 0.08em;
}

.batch-drawer__header h2 {
  margin: 0;
  font-size: 1.4rem;
}

.batch-drawer__header span {
  display: block;
  margin-top: 0.55rem;
  color: #b8cbc5;
  font-size: 0.82rem;
}

.batch-drawer__presets,
.batch-drawer__footer,
.batch-drawer__preview {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem;
  border: 1px solid var(--batch-border);
  border-radius: 0.9rem;
  background: #fff;
}

.batch-drawer__presets > div,
.batch-drawer__footer > div,
.batch-drawer__preview header > div {
  display: flex;
  align-items: center;
  gap: 0.65rem;
}

.batch-drawer__presets span,
.batch-drawer__footer span,
.batch-drawer__preview span,
.batch-capability span {
  color: #737d78;
  font-size: 0.76rem;
}

.batch-drawer__capabilities {
  display: grid;
  gap: 0.75rem;
}

.batch-capability {
  display: grid;
  gap: 0.8rem;
  padding: 1rem;
  border: 1px solid var(--batch-border);
  border-radius: 0.85rem;
  background: #faf9f6;
}

.batch-capability > div {
  display: grid;
  gap: 0.25rem;
}

.batch-drawer__preview {
  display: grid;
}

.batch-drawer__preview header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.batch-drawer__ready {
  padding: 0.35rem 0.65rem;
  border-radius: 999px;
  background: #eaf7f0;
  color: #17634f !important;
  font-weight: 700;
}

.batch-primary {
  --el-button-bg-color: var(--batch-lime);
  --el-button-border-color: var(--batch-lime);
  --el-button-text-color: #173b35;
  --el-button-hover-bg-color: #c9f158;
  --el-button-hover-border-color: #c9f158;
  font-weight: 750;
}

.batch-drawer__test-state {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

@media (max-width: 767px) {
  .batch-drawer__header,
  .batch-drawer__presets,
  .batch-drawer__footer {
    align-items: stretch;
    flex-direction: column;
  }

  .batch-drawer__presets > div,
  .batch-drawer__footer > div {
    display: grid;
  }

  .batch-drawer :deep(.el-button) {
    min-height: 2.75rem;
    margin: 0;
  }
}
</style>
