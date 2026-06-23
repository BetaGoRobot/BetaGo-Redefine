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
const windowSel = ref('7d')

async function load() {
  loading.value = true
  try {
    // 带指标拉取：成员量 / 近 N 天发言量 / token 总量 / 人均与单条均值。
    const resp = await api.listChats({ metrics: true, window: windowSel.value })
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

// el-table sortable 的取值器：从 metrics 里取对应字段，缺失按 0。
function metricGetter(key: keyof NonNullable<ChatSummary['metrics']>) {
  return (row: ChatSummary) => row.metrics?.[key] ?? 0
}

onMounted(load)
</script>

<template>
  <div>
    <div style="display: flex; gap: 12px; margin-bottom: 16px; align-items: center">
      <el-input v-model="keyword" placeholder="按群名或 chat_id 搜索" clearable style="max-width: 280px" />
      <span style="color: #909399">统计窗口</span>
      <el-radio-group v-model="windowSel" @change="load">
        <el-radio-button value="1d">1天</el-radio-button>
        <el-radio-button value="7d">7天</el-radio-button>
        <el-radio-button value="30d">30天</el-radio-button>
      </el-radio-group>
      <el-button :loading="loading" @click="load">刷新</el-button>
      <span style="line-height: 32px; color: #909399">共 {{ chats.length }} 个会话</span>
    </div>

    <el-table
      v-loading="loading"
      :data="filtered()"
      stripe
      :default-sort="{ prop: 'total_tokens', order: 'descending' }"
      @row-click="open"
      style="cursor: pointer"
    >
      <el-table-column label="头像" width="70">
        <template #default="{ row }">
          <el-avatar :src="row.avatar" :size="36" shape="square">{{ row.name?.[0] }}</el-avatar>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
      <el-table-column prop="chat_id" label="Chat ID" min-width="180" show-overflow-tooltip />
      <el-table-column label="类型" width="80">
        <template #default="{ row }">
          <el-tag size="small" :type="row.chat_status === 'p2p' ? 'info' : 'success'">
            {{ row.chat_status === 'p2p' ? '单聊' : '群聊' }}
          </el-tag>
        </template>
      </el-table-column>

      <el-table-column
        label="近期发言量"
        width="130"
        sortable
        :sort-by="metricGetter('recent_messages')"
        prop="recent_messages"
      >
        <template #default="{ row }">{{ row.metrics?.recent_messages ?? '-' }}</template>
      </el-table-column>
      <el-table-column
        label="群成员量"
        width="120"
        sortable
        :sort-by="metricGetter('member_count')"
        prop="member_count"
      >
        <template #default="{ row }">{{ row.metrics?.member_count ?? '-' }}</template>
      </el-table-column>
      <el-table-column
        label="Token 总量"
        width="130"
        sortable
        :sort-by="metricGetter('total_tokens')"
        prop="total_tokens"
      >
        <template #default="{ row }">{{ row.metrics?.total_tokens ?? '-' }}</template>
      </el-table-column>
      <el-table-column
        label="人均 Token"
        width="130"
        sortable
        :sort-by="metricGetter('tokens_per_member')"
        prop="tokens_per_member"
      >
        <template #default="{ row }">{{ row.metrics?.tokens_per_member ?? '-' }}</template>
      </el-table-column>
      <el-table-column
        label="单条均 Token"
        width="140"
        sortable
        :sort-by="metricGetter('tokens_per_message')"
        prop="tokens_per_message"
      >
        <template #default="{ row }">{{ row.metrics?.tokens_per_message ?? '-' }}</template>
      </el-table-column>

      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" link @click.stop="open(row)">查看</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>
