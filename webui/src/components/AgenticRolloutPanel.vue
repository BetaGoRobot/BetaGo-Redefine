<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { BotApi } from '../api/client'
import {
  AGENTIC_CAPABILITY_KEYS,
  buildCurrentAgenticChanges,
  buildFullAgenticChanges,
  updateAgenticChange,
} from '../api/agentic'
import type {
  AgenticChanges,
  AgenticChatState,
  AgenticOverride,
} from '../api/types'
import type { BotInstance } from '../stores/filter'
import AgenticCapabilityCard from './AgenticCapabilityCard.vue'

const props = defineProps<{
  bot: BotInstance
  chatID: string
}>()

const state = ref<AgenticChatState | null>(null)
const draft = ref<AgenticChanges>({})
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const conflict = ref(false)

const dirtyChanges = computed<AgenticChanges>(() => {
  if (!state.value) return {}
  const changes: AgenticChanges = {}
  for (const capability of state.value.capabilities) {
    const desired = draft.value[capability.key] ?? capability.override
    if (desired !== capability.override) {
      changes[capability.key] = desired
    }
  }
  return changes
})

const dirtyCount = computed(() => Object.keys(dirtyChanges.value).length)

const allEffective = computed(
  () =>
    state.value?.capabilities.length === AGENTIC_CAPABILITY_KEYS.length &&
    state.value.capabilities.every((capability) => capability.effective),
)

async function load(resetDraft = true) {
  loading.value = true
  loadError.value = ''
  try {
    const next = await new BotApi(props.bot).getAgenticRollout(props.chatID)
    state.value = next
    if (resetDraft) {
      draft.value = buildCurrentAgenticChanges(next)
    }
  } catch (error: any) {
    loadError.value =
      error?.response?.data?.error || error?.message || '灰度状态加载失败'
  } finally {
    loading.value = false
  }
}

function setCapability(key: keyof AgenticChanges, value: AgenticOverride) {
  draft.value = updateAgenticChange(draft.value, key, value)
  conflict.value = false
}

function applyPreset(value: AgenticOverride) {
  draft.value = buildFullAgenticChanges(value)
  conflict.value = false
}

async function save() {
  if (!state.value || dirtyCount.value === 0) return
  saving.value = true
  conflict.value = false
  try {
    await new BotApi(props.bot).updateAgenticRollout(props.chatID, {
      expected_revision: state.value.revision,
      changes: dirtyChanges.value,
    })
    ElMessage.success('Agentic 灰度已保存')
    await load(true)
  } catch (error: any) {
    if (error?.response?.status === 409) {
      const desired = { ...draft.value }
      await load(true)
      draft.value = desired
      conflict.value = true
      return
    }
    ElMessage.error(
      '保存失败：' +
        (error?.response?.data?.error || error?.message || '未知错误'),
    )
  } finally {
    saving.value = false
  }
}

watch(
  [() => props.bot.id, () => props.chatID],
  () => load(),
  { immediate: true },
)
</script>

<template>
  <section class="agentic-panel">
    <header class="agentic-panel__hero">
      <div>
        <p class="agentic-panel__eyebrow">LIVE POLICY · {{ bot.robotName || bot.name }}</p>
        <h2>让这个会话更 Agentic</h2>
        <p>
          四项能力独立灰度。默认继承且关闭；只有显式开启后才进入实时链路。
        </p>
      </div>
      <div class="agentic-panel__hero-status">
        <span>{{ allEffective ? 'Full Agentic 已生效' : '按能力渐进开放' }}</span>
        <small>{{ chatID }}</small>
      </div>
    </header>

    <div class="agentic-panel__presets" aria-label="Agentic 灰度预设">
      <div>
        <strong>快捷预设</strong>
        <span>预设会展开成四项显式修改，保存前仍可逐项调整。</span>
      </div>
      <div class="agentic-panel__preset-actions">
        <el-button
          data-test="restore-inherit"
          :disabled="saving"
          @click="applyPreset('inherit')"
        >
          全部恢复继承
        </el-button>
        <el-button
          data-test="full-agentic"
          class="agentic-primary"
          :disabled="saving"
          @click="applyPreset('enabled')"
        >
          Full Agentic
        </el-button>
      </div>
    </div>

    <el-skeleton v-if="loading && !state" :rows="5" animated />

    <div v-else-if="loadError" class="agentic-panel__notice is-error">
      <div>
        <strong>灰度状态暂不可用</strong>
        <p>{{ loadError }}</p>
      </div>
      <el-button @click="load()">重试</el-button>
    </div>

    <template v-else-if="state">
      <div class="agentic-panel__grid">
        <AgenticCapabilityCard
          v-for="capability in state.capabilities"
          :key="capability.key"
          :state="capability"
          :model-value="draft[capability.key] ?? capability.override"
          :saving="saving"
          @update:model-value="setCapability(capability.key, $event)"
        />
      </div>

      <div v-if="conflict" class="agentic-panel__notice is-warning">
        <div>
          <strong>服务端状态已变化</strong>
          <p>已刷新最新 revision，并保留你的草稿。请核对后再次保存。</p>
        </div>
      </div>

      <footer class="agentic-panel__footer">
        <div>
          <strong>
            {{ dirtyCount ? `${dirtyCount} 项待保存` : '当前没有未保存修改' }}
          </strong>
          <span>提交使用 revision 乐观锁，冲突不会覆盖其他人的操作。</span>
        </div>
        <el-button
          data-test="save-rollout"
          class="agentic-primary"
          :loading="saving"
          :disabled="dirtyCount === 0"
          @click="save"
        >
          保存灰度策略
        </el-button>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.agentic-panel {
  --agentic-pine-900: #143b36;
  --agentic-pine-700: #25534d;
  --agentic-lime: #d7ff73;
  --agentic-canvas: #f8f7f3;
  --agentic-border: #e6e3da;
  display: grid;
  gap: 1rem;
  padding: clamp(0.9rem, 2vw, 1.35rem);
  border: 1px solid var(--agentic-border);
  border-radius: 1.25rem;
  background:
    radial-gradient(circle at 90% 0%, rgb(215 255 115 / 22%), transparent 22rem),
    var(--agentic-canvas);
}

.agentic-panel__hero {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 2rem;
  padding: clamp(1.25rem, 3vw, 2rem);
  border-radius: 1rem;
  background: var(--agentic-pine-900);
  color: #f8fbf8;
  overflow: hidden;
}

.agentic-panel__eyebrow {
  margin: 0 0 0.55rem;
  color: var(--agentic-lime) !important;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.72rem !important;
  letter-spacing: 0.09em;
}

.agentic-panel__hero h2 {
  margin: 0;
  font-size: clamp(1.45rem, 3vw, 2.15rem);
  letter-spacing: -0.03em;
}

.agentic-panel__hero p {
  max-width: 42rem;
  margin: 0.7rem 0 0;
  color: #bdd0ca;
  font-size: 0.9rem;
  line-height: 1.7;
}

.agentic-panel__hero-status {
  display: grid;
  gap: 0.35rem;
  min-width: 12rem;
  padding: 0.85rem 1rem;
  border: 1px solid rgb(215 255 115 / 35%);
  border-radius: 0.85rem;
  background: rgb(255 255 255 / 7%);
}

.agentic-panel__hero-status span {
  color: var(--agentic-lime);
  font-weight: 750;
}

.agentic-panel__hero-status small {
  color: #a8bbb5;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.agentic-panel__presets,
.agentic-panel__footer,
.agentic-panel__notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.1rem;
  border: 1px solid var(--agentic-border);
  border-radius: 0.9rem;
  background: #fff;
}

.agentic-panel__presets > div:first-child,
.agentic-panel__footer > div:first-child {
  display: grid;
  gap: 0.25rem;
}

.agentic-panel__presets span,
.agentic-panel__footer span {
  color: #737d78;
  font-size: 0.78rem;
}

.agentic-panel__preset-actions {
  display: flex;
  gap: 0.65rem;
}

.agentic-primary {
  --el-button-bg-color: var(--agentic-lime);
  --el-button-border-color: var(--agentic-lime);
  --el-button-text-color: #173b35;
  --el-button-hover-bg-color: #c9f158;
  --el-button-hover-border-color: #c9f158;
  --el-button-active-bg-color: #bce447;
  font-weight: 750;
}

.agentic-panel__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
}

.agentic-panel__notice {
  justify-content: flex-start;
}

.agentic-panel__notice p {
  margin: 0.25rem 0 0;
  font-size: 0.82rem;
}

.agentic-panel__notice.is-warning {
  border-color: #e7d59b;
  background: #fff9e8;
  color: #775d11;
}

.agentic-panel__notice.is-error {
  border-color: #e9bcae;
  background: #fff4ef;
  color: #8e3e26;
}

@media (max-width: 900px) {
  .agentic-panel__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 767px) {
  .agentic-panel__hero,
  .agentic-panel__presets,
  .agentic-panel__footer {
    align-items: stretch;
    flex-direction: column;
  }

  .agentic-panel__hero-status {
    min-width: 0;
  }

  .agentic-panel__preset-actions {
    display: grid;
    grid-template-columns: 1fr;
  }

  .agentic-panel__preset-actions :deep(.el-button),
  .agentic-panel__footer :deep(.el-button) {
    width: 100%;
    min-height: 2.75rem;
    margin: 0;
  }
}
</style>
