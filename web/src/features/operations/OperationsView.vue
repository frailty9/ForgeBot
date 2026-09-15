<script setup lang="ts" vapor>
import { computed, shallowRef } from 'vue'
import { storeToRefs } from 'pinia'

import { useOperationsStore, type OperationName } from '@/stores/operations'

const props = defineProps<{
  token: string
  reflectionEnabled: boolean
  searchEnabled: boolean
}>()

const operations = useOperationsStore()
const { pending, result, error } = storeToRefs(operations)
const reason = shallowRef('')
const restartOpen = shallowRef(false)
const lastOperation = shallowRef<OperationName | null>(null)
const resultLabel = computed(() => {
  if (lastOperation.value === 'reloadSearch') return '索引重载结果'
  if (lastOperation.value === 'scanReflection') return '反思扫描结果'
  return 'Runtime 状态'
})

async function run(name: OperationName): Promise<void> {
  if (name === 'restart' && !restartOpen.value) {
    restartOpen.value = true
    return
  }
  restartOpen.value = false
  lastOperation.value = name
  await operations.run(name, props.token, reason.value.trim())
  reason.value = ''
}
</script>

<template>
  <section class="operations-view">
    <div class="operations-intro">
      <span class="meta-label">OPERATIONS / CONTROLLED ACTIONS</span>
      <p>每个动作都直接作用于当前 runtime，执行期间不会重复提交同一操作。</p>
    </div>
    <div class="operation-list">
      <article class="operation-unit">
        <div><span class="operation-code">01</span><h2>软重启 Runtime</h2><p>重建运行时生成代，不退出 ForgeBot 进程。</p></div>
        <div class="operation-actions">
          <input
            v-if="restartOpen"
            v-model="reason"
            aria-label="重启原因"
            placeholder="可选：填写重启原因"
          >
          <button
            type="button"
            :disabled="pending !== null"
            @click="run('restart')"
          >
            {{ pending === 'restart' ? '重启中…' : restartOpen ? '确认重启' : '准备重启' }}
          </button>
        </div>
      </article>
      <article class="operation-unit">
        <div><span class="operation-code">02</span><h2>重载结构化索引</h2><p>重新扫描文档根目录，变更后需要执行此动作。</p></div>
        <button
          type="button"
          :disabled="pending !== null || !props.searchEnabled"
          @click="run('reloadSearch')"
        >
          {{ pending === 'reloadSearch' ? '重载中…' : props.searchEnabled ? '重载索引' : '检索未启用' }}
        </button>
      </article>
      <article class="operation-unit">
        <div><span class="operation-code">03</span><h2>扫描反思队列</h2><p>将已完成的管理员回答交给反思管线判断是否沉淀为文档。</p></div>
        <button
          type="button"
          :disabled="pending !== null || !props.reflectionEnabled"
          @click="run('scanReflection')"
        >
          {{ pending === 'scanReflection' ? '扫描中…' : props.reflectionEnabled ? '扫描队列' : '反思未启用' }}
        </button>
      </article>
    </div>
    <section
      v-if="error || result"
      class="operation-result"
      :class="{ 'operation-result--error': error }"
      role="status"
    >
      <span class="meta-label">{{ error ? 'FAILED' : resultLabel }}</span>
      <p v-if="error">
        {{ error }}
      </p>
      <pre v-else>{{ JSON.stringify(result, null, 2) }}</pre>
    </section>
  </section>
</template>

<style scoped lang="scss">
.operations-view { max-width: 62rem; padding-top: clamp(2rem, 6vw, 5rem); }
.operations-intro { display: flex; align-items: baseline; justify-content: space-between; gap: 2rem; border-bottom: 1px solid var(--structure); padding-bottom: 1rem; }.operations-intro p { max-width: 28rem; margin: 0; color: var(--ink-muted); line-height: 1.6; }
.operation-list { display: grid; gap: 1px; margin-top: 2rem; border: 1px solid var(--structure); background: var(--structure); }.operation-unit { display: flex; align-items: center; justify-content: space-between; gap: 2rem; padding: 1.5rem; background: var(--surface); }.operation-code { color: var(--signal); font: 500 0.72rem/1 var(--font-mono); }.operation-unit h2 { margin: 0.7rem 0 0.45rem; font-size: 1.3rem; }.operation-unit p { margin: 0; color: var(--ink-muted); font-size: 0.9rem; }.operation-actions { display: flex; flex-wrap: wrap; justify-content: end; gap: 0.5rem; }.operation-unit button { border: 1px solid var(--ink); padding: 0.7rem 0.9rem; background: var(--ink); color: var(--surface); cursor: pointer; white-space: nowrap; }.operation-unit button:disabled { cursor: not-allowed; opacity: 0.45; }.operation-unit input { min-width: 12rem; border: 1px solid var(--structure); padding: 0.7rem; background: transparent; }.operation-result { margin-top: 2rem; border-left: 3px solid var(--healthy); padding: 1rem 1.25rem; background: rgb(46 139 109 / 8%); }.operation-result--error { border-color: var(--danger); background: rgb(201 75 69 / 8%); }.operation-result p { margin-bottom: 0; }.operation-result pre { overflow: auto; margin: 0.75rem 0 0; font: 0.78rem/1.5 var(--font-mono); }
@media (max-width: 620px) { .operations-intro, .operation-unit { align-items: start; flex-direction: column; }.operation-actions { width: 100%; justify-content: start; }.operation-actions input { flex: 1; }.operation-unit button { width: 100%; } }
</style>
