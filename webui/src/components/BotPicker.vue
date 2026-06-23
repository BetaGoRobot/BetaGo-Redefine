<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  useFilterStore,
  type BotInstance,
} from '../stores/filter'
import { BotApi } from '../api/client'

const store = useFilterStore()
const bots = computed(() => store.bots)
const selectedBotIDs = computed(() => store.selectedBotIDs)
const toggleBot = (id: string) => store.toggleBot(id)
const allSelected = computed(
  () => store.bots.length > 0 && store.selectedBotIDs.length === store.bots.length,
)

function toggleAll() {
  if (allSelected.value) store.setSelectedBots([store.bots[0]?.id].filter(Boolean) as string[])
  else store.setSelectedBots(store.bots.map((b) => b.id))
}
const dialogVisible = ref(false)
const editing = ref<Partial<BotInstance>>({})
const isEdit = ref(false)

function openAdd() {
  editing.value = {
    name: '',
    baseURL: '',
    token: '',
    remark: '',
  }
  isEdit.value = false
  dialogVisible.value = true
}

function openEdit(bot: BotInstance) {
  editing.value = { ...bot }
  isEdit.value = true
  dialogVisible.value = true
}

function applyEdit() {
  if (!editing.value.name || !editing.value.name.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  if (isEdit.value && editing.value.id) {
    store.updateBot(editing.value.id, {
      name: editing.value.name.trim(),
      baseURL: editing.value.baseURL || '',
      token: editing.value.token || undefined,
      remark: editing.value.remark,
    })
    ElMessage.success('已更新')
  } else {
    store.addBot({
      name: editing.value.name.trim(),
      baseURL: editing.value.baseURL || '',
      token: editing.value.token || undefined,
      remark: editing.value.remark,
    })
    ElMessage.success('已添加')
  }
  dialogVisible.value = false
}

async function tryRemove(bot: BotInstance) {
  try {
    await ElMessageBox.confirm(
      `确认移除机器人「${bot.robotName || bot.name}」？将从候选列表中删除。`,
      '移除机器人',
      { type: 'warning' },
    )
    store.removeBot(bot.id)
  } catch { /* cancel */ }
}

// ---------- 探活 & 自动回填 robot_name ----------
const probeLoading = ref<string | null>(null)

async function probe(bot: BotInstance) {
  probeLoading.value = bot.id
  try {
    const h = await new BotApi(bot).health()
    store.updateBot(bot.id, {
      healthy: true,
      robotName: h.robot_name || bot.robotName,
    })
    ElMessage.success(
      `✅ ${h.robot_name || bot.name} 可用${h.auth ? '（需 Token）' : '（免鉴权）'}`,
    )
  } catch (e: any) {
    store.updateBot(bot.id, { healthy: false })
    ElMessage.error(`❌ ${bot.name} 连接失败：${e?.message || e}`)
  } finally {
    probeLoading.value = null
  }
}

async function probeAll() {
  await Promise.all(store.bots.map((b) => probe(b)))
}

// ---------- 选择辅助 ----------

onMounted(async () => {
  // 首次进入：尝试自动探活并回填 robot 名
  for (const b of store.bots) {
    if (!b.robotName || b.healthy === undefined) {
      try {
        const h = await new BotApi(b).health()
        store.updateBot(b.id, {
          healthy: true,
          robotName: h.robot_name || b.robotName,
        })
      } catch {
        store.updateBot(b.id, { healthy: false })
      }
    }
  }
})
</script>

<template>
  <el-dropdown trigger="click" @command="(c: any) => c()">
    <el-button type="primary" plain>
      <el-icon style="vertical-align: -2px"><svg viewBox="0 0 1024 1024" width="14" height="14"><path fill="currentColor" d="M832 512a32 32 0 1 1 64 0v320a64 64 0 0 1-64 64H192a64 64 0 0 1-64-64V192a64 64 0 0 1 64-64h320a32 32 0 1 1 0 64H192v640h640V512zM384 448a32 32 0 0 1-32-32V160a32 32 0 0 1 64 0v137.376L724.672 148.704a32 32 0 1 1 34.656 54.592L449.984 351.936 724.672 500.64a32 32 0 1 1-34.656 54.592L416 414.656V416a32 32 0 0 1-32 32z"/></svg></el-icon>
      <span style="margin-left: 4px">机器人源</span>
      <el-tag size="small" style="margin-left: 8px">{{ selectedBotIDs.length }}/{{ bots.length }}</el-tag>
    </el-button>
    <template #dropdown>
      <el-dropdown-menu style="min-width: 480px; padding: 8px">
        <div style="padding: 4px 8px 8px; display: flex; gap: 6px; align-items: center; border-bottom: 1px solid #f2f6fc; margin-bottom: 4px">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="!allSelected && selectedBotIDs.length > 0"
            @change="toggleAll"
          >全选</el-checkbox>
          <el-button size="small" @click.stop="probeAll">🔍 全部探活</el-button>
          <el-button size="small" type="primary" plain @click.stop="openAdd">+ 添加机器人</el-button>
        </div>
        <div v-for="bot in bots" :key="bot.id" class="bot-row">
          <el-checkbox
            :model-value="selectedBotIDs.includes(bot.id)"
            @change="() => toggleBot(bot.id)"
          />
          <span
            class="dot"
            :style="{ background: bot.color || '#909399' }"
          />
          <div class="bot-main" @click.stop="toggleBot(bot.id)">
            <div class="bot-title">
              <span class="name">{{ bot.robotName || bot.name }}</span>
              <el-tag v-if="bot.healthy === true" type="success" effect="plain" size="small">在线</el-tag>
              <el-tag v-else-if="bot.healthy === false" type="danger" effect="plain" size="small">离线</el-tag>
              <el-tag v-else type="info" effect="plain" size="small">未探测</el-tag>
              <span v-if="bot.instance" class="subtle">{{ bot.instance }}</span>
            </div>
            <div class="bot-sub">
              <code class="code">{{ bot.baseURL || '(同源 /api)' }}</code>
              <span v-if="bot.remark" class="subtle">· {{ bot.remark }}</span>
              <span v-if="bot.token" class="subtle">· 🔑 Token 已配置</span>
            </div>
          </div>
          <div class="bot-actions" @click.stop>
            <el-button size="small" link :loading="probeLoading === bot.id" @click="probe(bot)">
              探活
            </el-button>
            <el-button size="small" link type="primary" @click="openEdit(bot)">编辑</el-button>
            <el-button size="small" link type="danger" @click="tryRemove(bot)">移除</el-button>
          </div>
        </div>
        <div v-if="!bots.length" style="padding: 12px; text-align: center; color: #909399; font-size: 12px">
          还没有配置机器人，点击右上角「添加机器人」开始。
        </div>
      </el-dropdown-menu>
    </template>
  </el-dropdown>

  <!-- 新增/编辑 dialog -->
  <el-dialog
    v-model="dialogVisible"
    :title="isEdit ? '编辑机器人' : '添加机器人'"
    width="520px"
  >
    <el-form :model="editing" label-width="100px">
      <el-form-item label="名称" required>
        <el-input v-model="editing.name" placeholder="例如：运营群机器人" />
      </el-form-item>
      <el-form-item label="后端地址">
        <el-input
          v-model="editing.baseURL"
          placeholder="留空表示走同源 /api；例如 https://bot-foo.example.com"
        />
      </el-form-item>
      <el-form-item label="管理 Token">
        <el-input
          v-model="editing.token"
          type="password"
          show-password
          placeholder="写操作需要 Bearer Token，只读可留空"
        />
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="editing.remark" placeholder="可选：说明用途、机房、负责人" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dialogVisible = false">取消</el-button>
      <el-button type="primary" @click="applyEdit">保存</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.bot-row {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 8px 4px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}
.bot-row:hover {
  background: #f2f6fc;
}
.dot {
  flex: 0 0 auto;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.08);
}
.bot-main {
  flex: 1 1 auto;
  min-width: 0;
}
.bot-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: #303133;
}
.name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bot-sub {
  margin-top: 2px;
  font-size: 11px;
  color: #909399;
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.code {
  background: #f2f6fc;
  padding: 0 6px;
  border-radius: 3px;
  font-size: 11px;
  color: #606266;
}
.subtle {
  font-size: 11px;
  color: #a8abb2;
}
.bot-actions {
  flex: 0 0 auto;
  display: flex;
  gap: 2px;
}
</style>
