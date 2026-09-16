<script setup lang="ts" vapor>
import { onMounted, shallowRef } from 'vue'

import { apiClient, ApiError } from '@/api/client'
import type { ConfigResponse, RevisionMeta } from '@/api/types'

const props = defineProps<{ token: string; current: ConfigResponse }>()
const emit = defineEmits<{ updated: [config: ConfigResponse] }>()
const revisions = shallowRef<RevisionMeta[]>([])
const loading = shallowRef(false)
const rollingBack = shallowRef<number | null>(null)
const error = shallowRef('')
const message = shallowRef('')

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try { revisions.value = await apiClient.history(props.token) }
  catch (cause) { error.value = cause instanceof ApiError ? cause.message : '历史版本加载失败。' }
  finally { loading.value = false }
}

async function rollback(revision: RevisionMeta): Promise<void> {
  if (revision.version === props.current.version || !window.confirm(`从版本 ${revision.version} 创建新的回滚版本？`)) return
  rollingBack.value = revision.version
  error.value = ''
  message.value = ''
  try {
    const response = await apiClient.rollback(props.token, revision.version, props.current.version)
    emit('updated', response.config)
    message.value = `已从版本 ${revision.version} 创建回滚版本。`
    await load()
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.status === 409 ? '版本已变化，当前草稿未被覆盖，请重新加载历史。' : cause instanceof Error ? cause.message : '回滚失败。'
  } finally { rollingBack.value = null }
}

onMounted(() => { void load() })
</script>

<template>
  <section class="history-view">
    <header class="history-header">
      <div><span class="meta-label">HISTORY / IMMUTABLE TIMELINE</span><p>回滚会创建新版本，不会删除现有历史。</p></div><button
        type="button"
        :disabled="loading"
        @click="load"
      >
        {{ loading ? '读取中…' : '刷新历史' }}
      </button>
    </header>
    <p
      v-if="error"
      class="history-message history-message--error"
      role="alert"
    >
      {{ error }}
    </p>
    <p
      v-if="message"
      class="history-message"
      role="status"
    >
      {{ message }}
    </p>
    <ol class="timeline">
      <li
        v-for="revision in revisions"
        :key="revision.version"
        class="timeline-item"
        :class="{ 'timeline-item--current': revision.version === current.version }"
      >
        <div
          class="timeline-marker"
          aria-hidden="true"
        />
        <div class="timeline-content">
          <div class="timeline-meta">
            <span>VERSION {{ revision.version }}</span><time :datetime="revision.updated_at">{{ new Date(revision.updated_at).toLocaleString() }}</time>
          </div><strong>{{ revision.version === current.version ? '当前运行版本' : '历史运行版本' }}</strong><small>{{ revision.source || 'unknown source' }}</small>
        </div>
        <button
          v-if="revision.version !== current.version"
          type="button"
          :disabled="rollingBack !== null"
          @click="rollback(revision)"
        >
          {{ rollingBack === revision.version ? '回滚中…' : '创建回滚版本' }}
        </button>
      </li>
    </ol>
    <p
      v-if="!loading && revisions.length === 0"
      class="history-empty"
    >
      当前没有可用版本历史。
    </p>
  </section>
</template>

<style scoped lang="scss">
.history-view { max-width: 70rem; padding-top: 2rem; }.history-header { display: flex; align-items: end; justify-content: space-between; gap: 2rem; border-bottom: 1px solid var(--structure); padding-bottom: 1rem; }.history-header p { margin: 0.5rem 0 0; color: var(--ink-muted); }.history-header button, .timeline-item button { border: 1px solid var(--ink); padding: 0.65rem 0.8rem; background: transparent; color: var(--ink); cursor: pointer; }.history-header button:disabled, .timeline-item button:disabled { cursor: not-allowed; opacity: 0.45; }.meta-label { color: var(--signal); font: 500 0.72rem/1 var(--font-mono); letter-spacing: 0.08em; }.history-message { border-left: 3px solid var(--healthy); padding: 0.7rem 1rem; background: rgb(46 139 109 / 8%); }.history-message--error { border-color: var(--danger); background: rgb(201 75 69 / 8%); color: var(--danger); }.timeline { display: grid; gap: 0; margin: 3rem 0 0; padding: 0; list-style: none; }.timeline-item { display: grid; grid-template-columns: 1.5rem minmax(0, 1fr) auto; gap: 1rem; align-items: center; min-height: 7rem; border-left: 1px solid var(--structure); margin-left: 0.35rem; padding: 1rem 0 1rem 1.5rem; }.timeline-marker { width: 0.75rem; height: 0.75rem; margin-left: -1.9rem; border: 2px solid var(--structure); border-radius: 50%; background: var(--surface); }.timeline-item--current .timeline-marker { border-color: var(--signal); background: var(--signal); box-shadow: 0 0 0 0.35rem rgb(231 111 46 / 14%); }.timeline-content { display: grid; gap: 0.4rem; }.timeline-meta { display: flex; gap: 1rem; color: var(--signal); font: 500 0.72rem/1 var(--font-mono); }.timeline-content strong { font-size: 1.05rem; }.timeline-content small { color: var(--ink-muted); font: 0.75rem/1.3 var(--font-mono); }.history-empty { color: var(--ink-muted); }
@media (max-width: 620px) { .history-header { align-items: start; flex-direction: column; }.timeline-item { grid-template-columns: 1.5rem minmax(0, 1fr); }.timeline-item button { grid-column: 2; justify-self: start; } }
</style>
