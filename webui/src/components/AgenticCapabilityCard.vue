<script setup lang="ts">
import { computed } from 'vue'
import type {
  AgenticCapabilityState,
  AgenticOverride,
} from '../api/types'
import { AGENTIC_CAPABILITY_COPY } from '../api/agentic'
import AgenticStatusBadge from './AgenticStatusBadge.vue'

const props = withDefaults(defineProps<{
  state: AgenticCapabilityState
  modelValue: AgenticOverride
  saving?: boolean
}>(), {
  saving: false,
})

const emit = defineEmits<{
  'update:modelValue': [value: AgenticOverride]
}>()

const overrideOptions = [
  { label: '继承', value: 'inherit' },
  { label: '开启', value: 'enabled' },
  { label: '关闭', value: 'disabled' },
]

const copy = computed(() => AGENTIC_CAPABILITY_COPY[props.state.key])

const overrideLabel = computed(() => {
  if (props.modelValue === 'enabled') return '单独开启'
  if (props.modelValue === 'disabled') return '单独关闭'
  return '继承'
})

const sourceLabel = computed(() => {
  switch (props.state.source) {
    case 'chat_override': return '当前群聊覆盖'
    case 'global_config': return 'Bot 全局配置'
    case 'toml': return '部署配置'
    default: return '系统默认'
  }
})

function updateOverride(value: string | number | boolean) {
  emit('update:modelValue', value as AgenticOverride)
}
</script>

<template>
  <article
    class="agentic-capability"
    :class="{
      'is-unavailable': !state.available,
      'is-dirty': modelValue !== state.override,
    }"
  >
    <header class="agentic-capability__header">
      <div>
        <p class="agentic-capability__eyebrow">{{ state.key }}</p>
        <h3>{{ copy.title }}</h3>
      </div>
      <AgenticStatusBadge :state="state" />
    </header>

    <p class="agentic-capability__description">{{ copy.description }}</p>

    <div class="agentic-capability__meta">
      <span>{{ overrideLabel }}</span>
      <span aria-hidden="true">·</span>
      <span>来源：{{ sourceLabel }}</span>
      <span v-if="modelValue !== state.override" class="agentic-capability__dirty">
        待保存
      </span>
    </div>

    <el-segmented
      :model-value="modelValue"
      :options="overrideOptions"
      :disabled="saving || !state.available"
      block
      :aria-label="`${state.label} 灰度状态`"
      @update:model-value="updateOverride"
    />

    <p v-if="!state.available" class="agentic-capability__reason">
      暂不可开启：{{ state.reason || '当前部署未初始化此能力' }}
    </p>
  </article>
</template>

<style scoped>
.agentic-capability {
  display: grid;
  gap: 0.9rem;
  min-width: 0;
  padding: 1.15rem;
  border: 1px solid #e5e2d9;
  border-radius: 1rem;
  background: #fff;
  box-shadow: 0 0.6rem 1.8rem rgb(20 59 54 / 5%);
  transition:
    border-color 160ms ease,
    box-shadow 160ms ease,
    transform 160ms ease;
}

.agentic-capability:hover {
  transform: translateY(-1px);
  border-color: #c8d7ce;
  box-shadow: 0 0.9rem 2rem rgb(20 59 54 / 8%);
}

.agentic-capability.is-dirty {
  border-color: #8eae3e;
  box-shadow: inset 0 0 0 1px rgb(215 255 115 / 70%);
}

.agentic-capability.is-unavailable {
  background: #fbfaf7;
}

.agentic-capability__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.agentic-capability__eyebrow {
  margin: 0 0 0.3rem;
  color: #76817c;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.68rem;
  letter-spacing: 0.055em;
  text-transform: uppercase;
}

h3 {
  margin: 0;
  color: #183d38;
  font-size: 1rem;
  line-height: 1.35;
}

.agentic-capability__description,
.agentic-capability__reason {
  margin: 0;
  color: #65706b;
  font-size: 0.84rem;
  line-height: 1.65;
}

.agentic-capability__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  color: #7a837e;
  font-size: 0.74rem;
}

.agentic-capability__dirty {
  margin-left: auto;
  color: #436112;
  font-weight: 700;
}

.agentic-capability__reason {
  color: #a5492d;
}

:deep(.el-segmented) {
  --el-segmented-item-selected-bg-color: #143b36;
  --el-segmented-item-selected-color: #fff;
  min-height: 2.75rem;
}

@media (max-width: 767px) {
  .agentic-capability {
    padding: 1rem;
  }

  .agentic-capability__header {
    align-items: flex-start;
  }
}
</style>
