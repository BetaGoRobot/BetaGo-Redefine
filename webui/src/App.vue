<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useFilterStore } from './stores/filter'

const route = useRoute()
const store = useFilterStore()

const selectedBots = computed(() => store.selectedBots)
const activeCount = computed(() =>
  selectedBots.value.filter((b) => b.healthy === true).length,
)

function routeName(n: string) {
  return route.name === n
}
</script>

<template>
  <el-container style="min-height: 100vh">
    <el-header
      style="display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid #ebeef5; gap: 12px; flex-wrap: wrap"
    >
      <div style="display: flex; align-items: center; gap: 14px; flex-wrap: wrap">
        <router-link
          to="/"
          style="font-size: 18px; font-weight: 600; text-decoration: none; color: #303133"
        >
          🤖 BetaGo 管理后台
        </router-link>
        <el-tag
          v-if="selectedBots.length"
          type="success"
          effect="plain"
          size="small"
        >
          已选 {{ selectedBots.length }} / {{ store.bots.length }} 个机器人
          <template v-if="activeCount" >
            · 在线 {{ activeCount }}
          </template>
        </el-tag>
        <el-tag v-else type="warning" effect="plain" size="small">
          未配置机器人
        </el-tag>
      </div>

      <div style="display: flex; gap: 8px; align-items: center">
        <router-link
          v-if="!routeName('dashboard')"
          :to="{ name: 'dashboard' }"
          style="text-decoration: none"
        >
          <el-button size="small" plain :type="routeName('dashboard') ? 'primary' : 'default'">
            📊 仪表盘
          </el-button>
        </router-link>
        <router-link
          v-if="!routeName('chats')"
          :to="{ name: 'chats' }"
          style="text-decoration: none"
        >
          <el-button size="small" plain :type="routeName('chats') ? 'primary' : 'default'">
            💬 会话
          </el-button>
        </router-link>
      </div>
    </el-header>
    <el-main>
      <router-view />
    </el-main>
  </el-container>
</template>
