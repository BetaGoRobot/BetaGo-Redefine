<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getToken, setToken } from './api/client'

const hasToken = ref(!!getToken())

async function editToken() {
  try {
    const { value } = await ElMessageBox.prompt('请输入管理 Token（写操作需要）', '设置 Token', {
      inputValue: getToken(),
      inputPlaceholder: '留空则清除',
    })
    setToken((value || '').trim())
    hasToken.value = !!getToken()
    ElMessage.success('Token 已更新')
  } catch {
    // 用户取消
  }
}
</script>

<template>
  <el-container style="min-height: 100vh">
    <el-header style="display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid #ebeef5">
      <router-link to="/" style="font-size: 18px; font-weight: 600; text-decoration: none; color: #303133">
        🤖 BetaGo 管理后台
      </router-link>
      <el-button size="small" :type="hasToken ? 'success' : 'warning'" @click="editToken">
        {{ hasToken ? 'Token 已设置' : '设置 Token' }}
      </el-button>
    </el-header>
    <el-main>
      <router-view />
    </el-main>
  </el-container>
</template>
