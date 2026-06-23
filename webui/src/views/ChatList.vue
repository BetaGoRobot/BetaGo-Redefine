<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api } from '../api/client'
import type { ChatSummary } from '../api/types'

const router = useRouter()
const loading = ref(false)
const chats = ref<ChatSummary[]>([])
const keyword = ref('')

async function load() {
  loading.value = true
  try {
    const resp = await api.listChats()
    chats.value = resp.items || []
  } catch (e: any) {
    ElMessage.error('加载群列表失败：' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
}

function filtered() {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return chats.value
  return chats.value.filter(
    (c) => c.name.toLowerCase().includes(kw) || c.chat_id.toLowerCase().includes(kw),
  )
}

function open(chat: ChatSummary) {
  router.push({ name: 'chat-detail', params: { chatID: chat.chat_id } })
}

onMounted(load)
</script>

<template>
  <div>
    <div style="display: flex; gap: 12px; margin-bottom: 16px">
      <el-input v-model="keyword" placeholder="按群名或 chat_id 搜索" clearable style="max-width: 320px" />
      <el-button :loading="loading" @click="load">刷新</el-button>
      <span style="line-height: 32px; color: #909399">共 {{ chats.length }} 个群</span>
    </div>

    <el-table v-loading="loading" :data="filtered()" stripe @row-click="open" style="cursor: pointer">
      <el-table-column label="头像" width="80">
        <template #default="{ row }">
          <el-avatar :src="row.avatar" :size="40" shape="square">{{ row.name?.[0] }}</el-avatar>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="群名称" min-width="160" />
      <el-table-column prop="chat_id" label="Chat ID" min-width="220" show-overflow-tooltip />
      <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="row.chat_status === 'normal' ? 'success' : 'info'">
            {{ row.chat_status || '-' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="外部群" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.external" size="small" type="warning">外部</el-tag>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button size="small" type="primary" link @click.stop="open(row)">查看</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>
