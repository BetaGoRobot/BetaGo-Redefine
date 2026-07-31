<script setup lang="ts">
import { computed } from 'vue'
import type { AgenticCapabilityState } from '../api/types'

const props = defineProps<{
  state: AgenticCapabilityState
}>()

const status = computed(() => {
  if (!props.state.available) {
    return { label: '当前不可用', tone: 'unavailable' }
  }
  if (props.state.effective) {
    return { label: '当前开启', tone: 'enabled' }
  }
  return { label: '当前关闭', tone: 'disabled' }
})
</script>

<template>
  <span class="agentic-status" :data-tone="status.tone">
    <span class="agentic-status__dot" aria-hidden="true" />
    {{ status.label }}
  </span>
</template>

<style scoped>
.agentic-status {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  min-height: 1.75rem;
  padding: 0.2rem 0.65rem;
  border: 1px solid #dcded8;
  border-radius: 999px;
  background: #f5f5f1;
  color: #57635e;
  font-size: 0.75rem;
  font-weight: 650;
  white-space: nowrap;
}

.agentic-status__dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  background: currentColor;
}

.agentic-status[data-tone='enabled'] {
  border-color: #aed5c5;
  background: #eaf7f0;
  color: #17634f;
}

.agentic-status[data-tone='disabled'] {
  border-color: #dedbd2;
  background: #f6f3ec;
  color: #6f746f;
}

.agentic-status[data-tone='unavailable'] {
  border-color: #ebc5b8;
  background: #fff1eb;
  color: #a5492d;
}
</style>
