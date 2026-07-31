<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  useFilterStore,
  type BotInstance,
} from '../stores/filter'
import { BotApi } from '../api/client'
import { isAutheliaMode } from '../auth/runtime'

const store = useFilterStore()
const secureAuthMode = isAutheliaMode()
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
      token: secureAuthMode ? undefined : editing.value.token || undefined,
      remark: editing.value.remark,
    })
    ElMessage.success('已更新')
  } else {
    store.addBot({
      name: editing.value.name.trim(),
      baseURL: editing.value.baseURL || '',
      token: secureAuthMode ? undefined : editing.value.token || undefined,
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
      `✅ ${h.robot_name || bot.name} 可用${
        h.auth
          ? secureAuthMode ? '（后端已保护）' : '（需 Token）'
          : '（免鉴权）'
      }`,
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
    <el-button class="bot-picker-trigger" plain>
      <span class="bot-picker-trigger__icon" aria-hidden="true">
        <span />
        <span />
      </span>
      <span>机器人源</span>
      <span class="bot-picker-trigger__count">
        {{ selectedBotIDs.length }}/{{ bots.length }}
      </span>
    </el-button>
    <template #dropdown>
      <el-dropdown-menu class="bot-picker-menu">
        <div class="bot-picker-menu__toolbar">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="!allSelected && selectedBotIDs.length > 0"
            @change="toggleAll"
          >全选</el-checkbox>
          <span class="bot-picker-menu__spacer" />
          <el-button size="small" @click.stop="probeAll">全部探活</el-button>
          <el-button
            v-if="!secureAuthMode"
            size="small"
            type="primary"
            plain
            @click.stop="openAdd"
          >+ 添加机器人</el-button>
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
              <code class="code">
                {{ secureAuthMode ? '服务端托管' : bot.baseURL || '(同源 /api)' }}
              </code>
              <span v-if="bot.remark" class="subtle">· {{ bot.remark }}</span>
              <span v-if="!secureAuthMode && bot.token" class="subtle">· Token 已配置</span>
            </div>
          </div>
          <div class="bot-actions" @click.stop>
            <el-button size="small" link :loading="probeLoading === bot.id" @click="probe(bot)">
              探活
            </el-button>
            <el-button
              v-if="!secureAuthMode"
              size="small"
              link
              type="primary"
              @click="openEdit(bot)"
            >编辑</el-button>
            <el-button
              v-if="!secureAuthMode"
              size="small"
              link
              type="danger"
              @click="tryRemove(bot)"
            >移除</el-button>
          </div>
        </div>
        <div v-if="!bots.length" class="bot-picker-empty">
          <strong>还没有机器人源</strong>
          <span>添加一个 WebUI 地址后即可开始查看运营数据。</span>
        </div>
      </el-dropdown-menu>
    </template>
  </el-dropdown>

  <!-- 新增/编辑 dialog -->
  <el-dialog
    v-model="dialogVisible"
    class="bot-picker-dialog"
    :title="isEdit ? '编辑机器人' : '添加机器人'"
    width="min(32rem, calc(100vw - 2rem))"
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
      <el-form-item v-if="!secureAuthMode" label="管理 Token">
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
.bot-picker-trigger {
  justify-content: flex-start;
  border-color: var(--ops-border-strong);
  background: var(--ops-surface);
  color: var(--ops-pine-900);
}

.bot-picker-trigger__icon {
  position: relative;
  display: inline-flex;
  width: 1rem;
  height: 1rem;
}

.bot-picker-trigger__icon::before,
.bot-picker-trigger__icon::after,
.bot-picker-trigger__icon span {
  position: absolute;
  width: 0.34rem;
  height: 0.34rem;
  border-radius: 0.13rem;
  background: currentColor;
  content: "";
}

.bot-picker-trigger__icon::before {
  top: 0.05rem;
  left: 0.05rem;
}

.bot-picker-trigger__icon::after {
  right: 0.05rem;
  bottom: 0.05rem;
}

.bot-picker-trigger__icon span:first-child {
  top: 0.05rem;
  right: 0.05rem;
  opacity: 0.45;
}

.bot-picker-trigger__icon span:last-child {
  bottom: 0.05rem;
  left: 0.05rem;
  opacity: 0.45;
}

.bot-picker-trigger__count {
  margin-left: auto;
  padding: 0.17rem 0.42rem;
  border-radius: 999px;
  background: var(--ops-pine-100);
  color: var(--ops-pine-700);
  font-size: 0.66rem;
  font-variant-numeric: tabular-nums;
  font-weight: 800;
}

.bot-picker-menu {
  width: min(31rem, calc(100vw - 1rem));
  padding: 0.55rem;
  border: 1px solid var(--ops-border);
  border-radius: var(--ops-radius-md);
  background: var(--ops-surface);
  box-shadow: var(--ops-shadow-md);
}

.bot-picker-menu__toolbar {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  min-height: 3rem;
  margin-bottom: 0.3rem;
  padding: 0.35rem 0.45rem 0.6rem;
  border-bottom: 1px solid var(--ops-border);
}

.bot-picker-menu__spacer {
  flex: 1;
}

.bot-row {
  display: flex;
  gap: 0.65rem;
  align-items: center;
  min-height: 3.6rem;
  padding: 0.55rem 0.45rem;
  border-radius: var(--ops-radius-sm);
  cursor: pointer;
  transition:
    background 0.15s ease,
    transform 0.15s ease;
}
.bot-row:hover {
  background: #f0f5f2;
  transform: translateX(1px);
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
  color: var(--ops-ink);
  font-size: 0.8rem;
  font-weight: 720;
}
.name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bot-sub {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin-top: 0.2rem;
  color: var(--ops-muted);
  font-size: 0.68rem;
}
.code {
  max-width: 18rem;
  overflow: hidden;
  padding: 0.08rem 0.35rem;
  border-radius: 0.3rem;
  background: #eff1ec;
  color: #56645f;
  font-family: var(--ops-font-mono);
  font-size: 0.65rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subtle {
  color: var(--ops-muted-light);
  font-size: 0.68rem;
}
.bot-actions {
  flex: 0 0 auto;
  display: flex;
  gap: 2px;
}

.bot-picker-empty {
  display: grid;
  justify-items: center;
  gap: 0.35rem;
  padding: 2rem 1rem;
  color: var(--ops-muted);
  text-align: center;
}

.bot-picker-empty::before {
  display: grid;
  place-items: center;
  width: 2.8rem;
  height: 2.8rem;
  margin-bottom: 0.35rem;
  border-radius: 0.9rem;
  background: var(--ops-pine-100);
  color: var(--ops-pine-700);
  content: "+";
  font-size: 1.5rem;
  font-weight: 500;
}

.bot-picker-empty strong {
  color: var(--ops-pine-900);
  font-size: 0.85rem;
}

.bot-picker-empty span {
  max-width: 28ch;
  font-size: 0.72rem;
  line-height: 1.5;
}

@media (max-width: 767px) {
  .bot-picker-menu__toolbar {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .bot-picker-menu__spacer {
    display: none;
  }

  .bot-picker-menu__toolbar :deep(.el-button) {
    flex: 1;
    margin: 0;
  }

  .bot-row {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .bot-actions {
    width: 100%;
    padding-left: 3rem;
  }
}
</style>
