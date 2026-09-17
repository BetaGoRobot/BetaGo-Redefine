<script setup lang="ts">
import { computed, ref, watch } from 'vue'

const props = withDefaults(defineProps<{ value: string; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ save: [value: string]; reset: [] }>()

type ModelRow = { id: number; model: string; serviceTier: string; reasoningEffort: string }
const rows = ref<ModelRow[]>([])
const loadError = ref('')
let nextID = 0
const serviceTiers = ['', 'auto', 'default', 'fast', 'flex']
const reasoningEfforts = ['', 'minimal', 'low', 'medium', 'high']

function loadValue(value: string) {
  loadError.value = ''
  try {
    const parsed = JSON.parse(value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('配置格式错误')
    rows.value = Object.entries(parsed).map(([model, raw]) => {
      if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('模型参数格式错误')
      const option = raw as Record<string, unknown>
      if (Object.keys(option).some((key) => !['service_tier', 'reasoning_effort'].includes(key))) {
        throw new Error('存在不支持的参数')
      }
      const tier = option.service_tier === undefined ? '' : option.service_tier
      const effort = option.reasoning_effort === undefined ? '' : option.reasoning_effort
      if (typeof tier !== 'string' || !serviceTiers.includes(tier) || typeof effort !== 'string' || !reasoningEfforts.includes(effort)) {
        throw new Error('存在不支持的参数值')
      }
      return { id: nextID++, model, serviceTier: tier, reasoningEffort: effort }
    })
  } catch (error) {
    rows.value = []
    loadError.value = '无法编辑当前配置：' + (error instanceof Error ? error.message : String(error))
  }
}
watch(() => props.value, loadValue, { immediate: true })

function reset() {
  // Discard the local draft even if the inherited value returned by the API
  // equals the current saved value (and therefore does not trigger a watch).
  loadValue(props.value)
  emit('reset')
}

const validationError = computed(() => {
  if (loadError.value) return loadError.value
  const seen = new Set<string>()
  for (const row of rows.value) {
    const model = row.model.trim()
    if (!model) return '请填写模型或 Endpoint ID'
    if (seen.has(model)) return '模型 ID 重复，请为每个模型保留一行'
    seen.add(model)
  }
  return ''
})

function addModel() {
  rows.value.push({ id: nextID++, model: '', serviceTier: '', reasoningEffort: '' })
}

function save() {
  if (props.disabled || validationError.value) return
  const options = Object.fromEntries(rows.value.map((row) => [row.model.trim(), {
    ...(row.serviceTier ? { service_tier: row.serviceTier } : {}),
    ...(row.reasoningEffort ? { reasoning_effort: row.reasoningEffort } : {}),
  }]))
  emit('save', JSON.stringify(options))
}
</script>

<template>
  <section class="model-options" aria-label="Ark 模型请求参数">
    <div class="model-options-heading">
      <div>
        <h3>模型请求参数</h3>
        <p>按模型或 Endpoint ID 精确匹配，应用于普通和流式请求。</p>
      </div>
      <button type="button" data-test="add" :disabled="disabled || !!loadError" @click="addModel">＋ 添加模型</button>
    </div>
    <p class="model-options-hint">保存会覆盖当前作用域的整组模型参数；重置后继承上级配置。参数留空时沿用请求默认行为。</p>
    <div v-for="(row, index) in rows" :key="row.id" class="model-options-row">
      <label>
        <span>模型 / Endpoint ID</span>
        <input v-model="row.model" aria-label="模型或 Endpoint ID" placeholder="模型名称或 ep-…" :disabled="disabled" />
      </label>
      <label>
        <span>服务等级</span>
        <select v-model="row.serviceTier" aria-label="服务等级" :disabled="disabled">
          <option value="">沿用请求默认</option>
          <option v-for="tier in serviceTiers.filter(Boolean)" :key="tier" :value="tier">{{ tier }}</option>
        </select>
      </label>
      <label>
        <span>推理强度</span>
        <select v-model="row.reasoningEffort" aria-label="推理强度" :disabled="disabled">
          <option value="">沿用现有策略</option>
          <option v-for="effort in reasoningEfforts.filter(Boolean)" :key="effort" :value="effort">{{ effort }}</option>
        </select>
      </label>
      <button type="button" class="remove-model" aria-label="删除模型配置" :disabled="disabled" @click="rows.splice(index, 1)">删除</button>
    </div>
    <p v-if="!rows.length && !loadError" class="model-options-empty">暂无模型参数覆盖。添加模型后可设置 flex 服务等级和推理强度。</p>
    <p v-if="validationError" role="alert" class="model-options-error">{{ validationError }}</p>
    <div class="model-options-actions">
      <button type="button" class="save-options" data-test="save" :disabled="disabled || !!validationError" @click="save">保存模型参数</button>
      <button type="button" data-test="reset" :disabled="disabled" @click="reset">重置为继承</button>
    </div>
  </section>
</template>

<style scoped>
.model-options { margin-bottom: 1.5rem; padding: 1.25rem; border: 1px solid var(--ops-border); border-radius: var(--ops-radius-md); background: var(--ops-surface); }
.model-options-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 1rem; }
h3 { margin: 0 0 0.5rem; color: var(--ops-ink); }
p { margin: 0; line-height: 1.6; color: var(--ops-muted); }
.model-options-hint { margin: 1rem 0; font-size: 0.875rem; }
.model-options-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 0.75rem; padding: 1rem 0; border-top: 1px solid var(--ops-border); }
label { display: grid; gap: 0.5rem; min-width: 0; color: var(--ops-ink); font-size: 0.875rem; }
input, select, button { min-height: 2.75rem; padding: 0.6rem 0.75rem; border: 1px solid var(--ops-border-strong); border-radius: var(--ops-radius-sm); background: var(--ops-surface-strong); color: var(--ops-ink); font-size: 1rem; }
input, select { width: 100%; min-width: 0; }
button { cursor: pointer; }
button:disabled { opacity: 0.5; cursor: not-allowed; }
.remove-model { align-self: end; color: var(--ops-danger); }
.model-options-empty { padding: 1rem 0; }
.model-options-error { margin-top: 1rem; color: var(--ops-danger); }
.model-options-actions { display: flex; flex-wrap: wrap; gap: 0.75rem; margin-top: 1rem; }
.save-options { background: var(--ops-pine-700); color: white; border-color: var(--ops-pine-700); }
@media (min-width: 768px) {
  .model-options-row { grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr) auto; }
}
</style>
