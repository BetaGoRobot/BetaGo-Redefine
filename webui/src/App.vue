<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useFilterStore } from './stores/filter'

const route = useRoute()
const store = useFilterStore()

const selectedBots = computed(() => store.selectedBots)
const activeCount = computed(() =>
  selectedBots.value.filter((bot) => bot.healthy === true).length,
)
const chatsActive = computed(() =>
  route.name === 'chats' || route.name === 'chat-detail',
)
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <div class="app-header__inner">
        <router-link class="app-brand" to="/" aria-label="BetaGo Agent Operations 首页">
          <span class="app-brand__mark" aria-hidden="true">
            <span />
            <span />
          </span>
          <span class="app-brand__copy">
            <strong>BetaGo</strong>
            <small>Agent operations</small>
          </span>
        </router-link>

        <nav class="app-nav" aria-label="主要导航">
          <router-link
            :to="{ name: 'dashboard' }"
            :class="{ 'is-active': route.name === 'dashboard' }"
            :aria-current="route.name === 'dashboard' ? 'page' : undefined"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M4 13h6V4H4v9Zm0 7h6v-4H4v4Zm10 0h6v-9h-6v9Zm0-16v4h6V4h-6Z" />
            </svg>
            <span>总览</span>
          </router-link>
          <router-link
            :to="{ name: 'chats' }"
            :class="{ 'is-active': chatsActive }"
            :aria-current="chatsActive ? 'page' : undefined"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M5 5h14v10H9l-4 4V5Zm3 4h8M8 12h5" />
            </svg>
            <span>会话</span>
          </router-link>
        </nav>

        <div
          class="app-runtime-status"
          :class="{ 'is-ready': activeCount > 0, 'is-empty': !selectedBots.length }"
        >
          <span class="app-runtime-status__pulse" aria-hidden="true" />
          <span v-if="selectedBots.length">
            {{ selectedBots.length }} 个 Bot
            <small>· {{ activeCount }} 在线</small>
          </span>
          <span v-else>
            尚未连接 Bot
            <small>· 等待配置</small>
          </span>
        </div>
      </div>
    </header>

    <main class="app-workspace">
      <router-view v-slot="{ Component }">
        <component :is="Component" :key="String(route.name)" class="route-view" />
      </router-view>
    </main>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
}

.app-header {
  position: sticky;
  z-index: 40;
  top: 0;
  border-bottom: 1px solid rgb(203 207 197 / 72%);
  background: rgb(244 242 236 / 88%);
  backdrop-filter: blur(18px) saturate(135%);
}

.app-header__inner {
  display: grid;
  grid-template-columns: minmax(11rem, 1fr) auto minmax(11rem, 1fr);
  align-items: center;
  width: min(100%, 100rem);
  min-height: 4.6rem;
  margin: 0 auto;
  padding: 0.65rem clamp(1rem, 3vw, 2.5rem);
}

.app-brand {
  display: inline-flex;
  align-items: center;
  justify-self: start;
  gap: 0.7rem;
  width: max-content;
  color: var(--ops-pine-950);
  text-decoration: none;
}

.app-brand__mark {
  position: relative;
  display: grid;
  place-items: center;
  width: 2.4rem;
  height: 2.4rem;
  overflow: hidden;
  border-radius: 0.78rem 0.78rem 0.78rem 0.28rem;
  background: var(--ops-pine-900);
  box-shadow: 0 0.55rem 1.1rem rgb(20 59 54 / 20%);
  transform: rotate(-2deg);
}

.app-brand__mark::before,
.app-brand__mark::after {
  position: absolute;
  content: "";
}

.app-brand__mark::before {
  width: 1.05rem;
  height: 0.72rem;
  border: 2px solid var(--ops-lime);
  border-radius: 0.28rem;
}

.app-brand__mark::after {
  top: 0.5rem;
  width: 2px;
  height: 0.35rem;
  background: var(--ops-lime);
}

.app-brand__mark span {
  position: absolute;
  top: 1.13rem;
  width: 0.16rem;
  height: 0.16rem;
  border-radius: 50%;
  background: var(--ops-lime);
}

.app-brand__mark span:first-child {
  left: 0.83rem;
}

.app-brand__mark span:last-child {
  right: 0.83rem;
}

.app-brand__copy {
  display: grid;
  line-height: 1;
}

.app-brand__copy strong {
  font-size: 1.03rem;
  font-weight: 800;
  letter-spacing: -0.025em;
}

.app-brand__copy small {
  margin-top: 0.32rem;
  color: var(--ops-muted);
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.105em;
  text-transform: uppercase;
}

.app-nav {
  display: flex;
  align-items: center;
  justify-self: center;
  gap: 0.35rem;
  padding: 0.3rem;
  border: 1px solid var(--ops-border);
  border-radius: 999px;
  background: rgb(255 254 250 / 78%);
  box-shadow: var(--ops-shadow-sm);
}

.app-nav a {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  min-width: 6.6rem;
  min-height: 2.45rem;
  padding: 0.55rem 0.9rem;
  border-radius: 999px;
  color: var(--ops-muted);
  font-size: 0.82rem;
  font-weight: 720;
  text-decoration: none;
  transition:
    color 160ms ease,
    background 160ms ease,
    transform 160ms ease;
}

.app-nav a:hover {
  color: var(--ops-pine-900);
  transform: translateY(-1px);
}

.app-nav a.is-active {
  background: var(--ops-pine-900);
  color: #fff;
  box-shadow: 0 0.45rem 1rem rgb(20 59 54 / 18%);
}

.app-nav svg {
  width: 1rem;
  height: 1rem;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.app-nav a:first-child svg {
  fill: currentColor;
  stroke: none;
}

.app-runtime-status {
  display: inline-flex;
  align-items: center;
  justify-self: end;
  gap: 0.55rem;
  max-width: 13rem;
  min-height: 2.5rem;
  padding: 0.5rem 0.78rem;
  border: 1px solid var(--ops-border);
  border-radius: 999px;
  background: rgb(255 254 250 / 76%);
  color: var(--ops-pine-900);
  font-size: 0.75rem;
  font-weight: 740;
}

.app-runtime-status small {
  color: var(--ops-muted);
  font-size: inherit;
  font-weight: 550;
}

.app-runtime-status__pulse {
  flex: 0 0 auto;
  width: 0.52rem;
  height: 0.52rem;
  border-radius: 50%;
  background: var(--ops-amber);
  box-shadow: 0 0 0 0.22rem rgb(215 154 71 / 13%);
}

.app-runtime-status.is-ready .app-runtime-status__pulse {
  background: var(--ops-teal);
  box-shadow: 0 0 0 0.22rem rgb(74 143 121 / 14%);
}

.app-runtime-status.is-empty {
  color: var(--ops-muted);
}

.app-workspace {
  width: min(100%, 100rem);
  margin: 0 auto;
  padding: clamp(1rem, 2.5vw, 2.2rem) clamp(0.75rem, 3vw, 2.5rem) 3rem;
}

@media (max-width: 767px) {
  .app-header__inner {
    grid-template-columns: 1fr auto;
    gap: 0.65rem;
    min-height: auto;
    padding: 0.65rem 0.8rem;
  }

  .app-brand__copy small,
  .app-runtime-status small {
    display: none;
  }

  .app-runtime-status {
    min-height: 2.4rem;
    padding-inline: 0.65rem;
  }

  .app-nav {
    grid-column: 1 / -1;
    grid-row: 2;
    width: 100%;
  }

  .app-nav a {
    flex: 1;
    min-width: 0;
    min-height: 2.75rem;
  }

  .app-workspace {
    padding: 1rem 0.65rem 2rem;
  }
}
</style>
