<script setup lang="ts" vapor>
import { onMounted, shallowRef, watch } from 'vue'
import { storeToRefs } from 'pinia'

import AppRail from '@/app/AppRail.vue'
import { ApiError, apiClient } from '@/api/client'
import type { ConfigResponse } from '@/api/types'
import ConfigView from '@/features/config/ConfigView.vue'
import LoginPanel from '@/features/auth/LoginPanel.vue'
import OperationsView from '@/features/operations/OperationsView.vue'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const { authenticated, busy, error, token } = storeToRefs(session)
const route = shallowRef(normalizeRoute(window.location.pathname))
const config = shallowRef<ConfigResponse | null>(null)
const loading = shallowRef(false)
const loadError = shallowRef('')

function normalizeRoute(path: string): string {
  const value = path.replace(/^\/admin/, '') || '/'
  return ['/', '/config', '/operations', '/history'].includes(value) ? value : '/'
}

function navigate(path: string): void {
  const next = normalizeRoute(path)
  window.history.pushState({}, '', `/admin${next === '/' ? '/' : next}`)
  route.value = next
}

function onPopState(): void { route.value = normalizeRoute(window.location.pathname) }

async function authenticate(value: string): Promise<void> {
  session.begin()
  try {
    config.value = await apiClient.getConfig(value)
    session.complete(value)
    loadError.value = ''
  } catch (cause) {
    const message = cause instanceof ApiError && cause.status === 401 ? '令牌无效或已失效。' : '无法连接 ForgeBot 管理接口。'
    session.clear()
    session.setError(message)
  } finally { busy.value = false }
}

async function loadConfig(): Promise<void> {
  if (!token.value) return
  loading.value = true
  loadError.value = ''
  try {
    config.value = await apiClient.getConfig(token.value)
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 401) {
      session.clear()
      session.setError('会话已失效，请重新验证管理令牌。')
    } else { loadError.value = cause instanceof Error ? cause.message : '配置加载失败。' }
  } finally { loading.value = false }
}

function logout(): void { session.clear(); config.value = null; navigate('/') }

onMounted(() => {
  window.addEventListener('popstate', onPopState)
  if (authenticated.value) void loadConfig()
})
watch(token, (value, previous) => { if (value && value !== previous) void loadConfig() })
</script>

<template>
  <LoginPanel
    v-if="!authenticated"
    :busy="busy"
    :error="error"
    @authenticate="authenticate"
  />
  <main
    v-else
    class="admin-shell"
  >
    <AppRail
      :current="route"
      :state="config?.runtime_status.state ?? 'loading'"
      @navigate="navigate"
      @logout="logout"
    />
    <section
      class="workspace"
      aria-live="polite"
    >
      <header class="workspace-header">
        <div>
          <p class="eyebrow">
            FORGEBOT / CONTROL PLANE
          </p><h1>{{ route === '/' ? '运行概览' : route === '/config' ? '结构化配置' : route === '/operations' ? '运行维护' : '版本历史' }}</h1>
        </div>
        <div class="header-meta">
          <span class="meta-label">VERSION</span><strong>{{ config?.version ?? '—' }}</strong>
        </div>
      </header>
      <p
        v-if="loading"
        class="notice"
      >
        正在读取当前运行快照…
      </p>
      <p
        v-else-if="loadError"
        class="notice notice--error"
        role="alert"
      >
        {{ loadError }}
      </p>
      <template v-if="route === '/' && config">
        <section class="signal-overview">
          <div
            class="signal-line"
            aria-hidden="true"
          >
            <span class="signal-line__node signal-line__node--active" /><span class="signal-line__node" /><span class="signal-line__node" /><span class="signal-line__node" />
          </div><div class="signal-copy">
            <span class="meta-label">RUNTIME SIGNAL</span><strong>{{ config.runtime_status.state }}</strong><p>{{ config.runtime_status.action }} · {{ config.runtime_status.paused ? '普通消息已暂停' : '普通消息处理中' }}</p>
          </div>
        </section>
        <section class="overview-grid">
          <article class="metric">
            <span class="meta-label">LLM / MODEL</span><strong>{{ config.runtime.llm.model || '未设置' }}</strong><small>{{ config.runtime.llm.protocol }}</small>
          </article><article class="metric">
            <span class="meta-label">CONNECTORS</span><strong>{{ config.runtime_status.connectors.length }}</strong><small>{{ config.runtime.platforms.connectors.length }} configured</small>
          </article><article class="metric">
            <span class="meta-label">SEARCH INDEX</span><strong>{{ config.runtime_status.search.documents }}</strong><small>{{ config.runtime_status.search.sections }} sections</small>
          </article><article class="metric">
            <span class="meta-label">LLM API KEY</span><strong>{{ config.llm_api_key.configured ? 'READY' : 'MISSING' }}</strong><small>write-only secret</small>
          </article>
        </section>
      </template>
      <OperationsView
        v-else-if="route === '/operations' && config"
        :token="token"
        :reflection-enabled="Boolean(config.runtime.reflection.enabled)"
        :search-enabled="Boolean(config.runtime.search.enabled)"
      />
      <ConfigView
        v-else-if="route === '/config' && config"
        :token="token"
        :initial="config"
        @updated="config = $event"
      />
      <section
        v-else
        class="empty-view"
      >
        <span class="meta-label">{{ route.slice(1).toUpperCase() || 'OVERVIEW' }}</span><h2>这一页将在下一阶段接入。</h2><p>应用壳、认证、路由和强类型请求层已经可用，功能模块将沿既定任务清单逐步填充。</p>
      </section>
    </section>
  </main>
</template>

<style scoped lang="scss">
.admin-shell { display: grid; grid-template-columns: 5rem minmax(0, 1fr); min-height: 100vh; background: var(--surface); color: var(--ink); }
.workspace { min-width: 0; padding: clamp(1.5rem, 5vw, 5rem); }
.workspace-header { display: flex; align-items: end; justify-content: space-between; gap: 2rem; border-bottom: 1px solid var(--structure); padding-bottom: 1.5rem; }
.eyebrow, .meta-label { color: var(--signal); font: 500 0.72rem/1 var(--font-mono); letter-spacing: 0.08em; }
.workspace-header h1 { margin: 0.8rem 0 0; font-size: clamp(2.2rem, 5vw, 4.8rem); letter-spacing: -0.07em; line-height: 0.95; }
.header-meta { display: grid; gap: 0.5rem; text-align: right; }.header-meta strong { font: 500 1rem/1 var(--font-mono); }
.notice { margin: 1rem 0; color: var(--ink-muted); }.notice--error { color: var(--danger); }
.signal-overview { display: grid; grid-template-columns: minmax(8rem, 1fr) minmax(14rem, 1fr); gap: 2rem; align-items: center; margin: clamp(3rem, 10vw, 8rem) 0 4rem; }
.signal-line { display: flex; align-items: center; }.signal-line::before { height: 2px; flex: 1; background: var(--structure); content: ''; }.signal-line__node { width: 0.8rem; height: 0.8rem; margin-left: -1px; border: 2px solid var(--structure); border-radius: 50%; background: var(--surface); }.signal-line__node--active { border-color: var(--signal); background: var(--signal); box-shadow: 0 0 0 0.4rem rgb(231 111 46 / 14%); }
.signal-copy { display: grid; gap: 0.55rem; }.signal-copy strong { font: 500 clamp(2rem, 5vw, 4rem)/1 var(--font-mono); }.signal-copy p { margin: 0; color: var(--ink-muted); }
.overview-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 1px; border: 1px solid var(--structure); background: var(--structure); }.metric { display: grid; gap: 0.65rem; min-height: 9rem; padding: 1.25rem; background: var(--surface); }.metric strong { font: 500 1.45rem/1.1 var(--font-mono); overflow-wrap: anywhere; }.metric small { color: var(--ink-muted); }
.empty-view { max-width: 42rem; margin: clamp(5rem, 15vw, 12rem) 0; }.empty-view h2 { max-width: 12ch; margin: 1.5rem 0; font-size: clamp(2rem, 5vw, 4rem); letter-spacing: -0.07em; line-height: 0.95; }.empty-view p { max-width: 32rem; color: var(--ink-muted); line-height: 1.8; }
@media (max-width: 800px) { .overview-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 560px) { .admin-shell { grid-template-columns: 1fr; }.app-rail { min-height: auto; flex-direction: row; align-items: center; overflow-x: auto; }.rail-brand { flex: 0 0 auto; margin: 0.75rem; }.rail-nav { display: flex; }.rail-footer { margin: 0 0.75rem 0 auto; padding: 0; }.rail-state, .logout-button { display: none; }.workspace { padding: 1.5rem 1rem; }.workspace-header { align-items: start; }.signal-overview { grid-template-columns: 1fr; margin-block: 3rem; } }
</style>
