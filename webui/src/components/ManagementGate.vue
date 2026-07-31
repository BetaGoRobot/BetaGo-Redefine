<script setup lang="ts">
import { computed, ref } from 'vue'
import { managementSession } from '../auth/session'
import { readWebUIAuthRuntime } from '../auth/runtime'

withDefaults(defineProps<{
  title?: string
  description?: string
  compact?: boolean
}>(), {
  title: '登录后管理',
  description: '公开数据仍可浏览。此区域包含敏感信息或管理操作，需要通过 Authelia 登录。',
  compact: false,
})

const fallbackVisible = ref(false)
const authenticated = managementSession.authenticated
const loginBusy = managementSession.loginBusy
const loginURL = computed(() => {
  const runtime = readWebUIAuthRuntime()
  const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`
  const separator = runtime.loginPath.includes('?') ? '&' : '?'
  return `${runtime.loginPath}${separator}return=${encodeURIComponent(returnTo)}`
})

function login() {
  fallbackVisible.value = !managementSession.beginLogin()
}
</script>

<template>
  <slot v-if="authenticated" />
  <section
    v-else
    class="management-gate"
    :class="{ 'is-compact': compact }"
    aria-live="polite"
  >
    <span class="management-gate__lock" aria-hidden="true" />
    <div class="management-gate__copy">
      <strong>{{ title }}</strong>
      <p>{{ description }}</p>
    </div>
    <div class="management-gate__actions">
      <el-button
        data-test="login"
        type="primary"
        :loading="loginBusy && !fallbackVisible"
        @click="login"
      >
        登录后管理
      </el-button>
      <a
        v-if="fallbackVisible"
        :href="loginURL"
        target="_blank"
      >
        在新标签页登录
      </a>
    </div>
  </section>
</template>

<style scoped>
.management-gate {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 1rem;
  min-height: 10rem;
  padding: clamp(1rem, 3vw, 1.6rem);
  border: 1px dashed var(--ops-border-strong);
  border-radius: var(--ops-radius-md);
  background:
    radial-gradient(circle at 100% 0%, rgb(215 255 115 / 15%), transparent 13rem),
    var(--ops-surface);
  box-shadow: var(--ops-shadow-sm);
}

.management-gate.is-compact {
  min-height: 7rem;
}

.management-gate__lock {
  position: relative;
  width: 2.7rem;
  height: 2.7rem;
  border-radius: 0.85rem;
  background: var(--ops-pine-900);
  box-shadow: 0 0.6rem 1.2rem rgb(20 59 54 / 15%);
}

.management-gate__lock::before {
  position: absolute;
  top: 0.52rem;
  left: 0.83rem;
  width: 0.85rem;
  height: 0.75rem;
  border: 2px solid var(--ops-lime);
  border-bottom: 0;
  border-radius: 0.55rem 0.55rem 0 0;
  content: "";
}

.management-gate__lock::after {
  position: absolute;
  top: 1.18rem;
  left: 0.7rem;
  width: 1.3rem;
  height: 0.95rem;
  border-radius: 0.3rem;
  background: var(--ops-lime);
  content: "";
}

.management-gate__copy strong {
  color: var(--ops-pine-950);
  font-size: 0.95rem;
}

.management-gate__copy p {
  max-width: 58ch;
  margin: 0.32rem 0 0;
  color: var(--ops-muted);
  font-size: 0.76rem;
  line-height: 1.55;
}

.management-gate__actions {
  display: grid;
  justify-items: center;
  gap: 0.45rem;
}

.management-gate__actions a {
  color: var(--ops-pine-700);
  font-size: 0.72rem;
  font-weight: 700;
  text-underline-offset: 0.18rem;
}

@media (max-width: 767px) {
  .management-gate {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .management-gate__actions {
    grid-column: 1 / -1;
    justify-items: stretch;
    width: 100%;
  }
}
</style>
