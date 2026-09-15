<script setup lang="ts" vapor>
import { shallowRef } from 'vue'

defineProps<{
  busy: boolean
  error: string
}>()

const emit = defineEmits<{
  authenticate: [token: string]
}>()

const token = shallowRef('')

function submit(): void {
  const value = token.value.trim()
  if (value === '') return
  emit('authenticate', value)
  token.value = ''
}
</script>

<template>
  <main class="login-layout">
    <section class="login-intro">
      <p class="eyebrow">
        FORGEBOT / CONTROL PLANE
      </p>
      <h1>把运行状态<br><em>锻造成</em>清晰信号。</h1>
      <p class="lede">
        本地管理控制台。配置、连接器和运行时操作都在同一个受保护的边界内。
      </p>
    </section>
    <form
      class="login-panel"
      @submit.prevent="submit"
    >
      <div class="panel-index">
        AUTH / 01
      </div>
      <h2>连接实例</h2>
      <label
        class="field-label"
        for="admin-token"
      >管理令牌</label>
      <input
        id="admin-token"
        v-model="token"
        class="token-input"
        type="password"
        autocomplete="current-password"
        placeholder="输入 FORGEBOT_ADMIN_TOKEN"
        :disabled="busy"
      >
      <p class="field-hint">
        令牌仅保存于当前标签页的 sessionStorage，关闭后自动失效。
      </p>
      <p
        v-if="error"
        class="form-error"
        role="alert"
      >
        {{ error }}
      </p>
      <button
        class="primary-button"
        type="submit"
        :disabled="busy || token.trim() === ''"
      >
        {{ busy ? '验证中…' : '验证并进入' }}
      </button>
    </form>
  </main>
</template>

<style scoped lang="scss">
.login-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(20rem, 28rem);
  gap: clamp(2rem, 8vw, 9rem);
  align-items: center;
  min-height: 100vh;
  padding: clamp(2rem, 8vw, 8rem);
}

.login-intro { max-width: 42rem; }
.eyebrow, .panel-index { color: var(--signal); font: 500 0.75rem/1 var(--font-mono); letter-spacing: 0.08em; }
.login-intro h1 { margin: 1.5rem 0; font-size: clamp(3rem, 8vw, 7rem); letter-spacing: -0.08em; line-height: 0.94; }
.login-intro h1 em { color: var(--signal); font-style: normal; }
.lede { max-width: 32rem; color: var(--ink-muted); font-size: 1.05rem; line-height: 1.8; }
.login-panel { border-top: 4px solid var(--ink); padding: 1.5rem; background: rgb(255 255 255 / 42%); box-shadow: 0 1.25rem 3rem rgb(23 26 29 / 8%); }
.login-panel h2 { margin: 2rem 0; font-size: 1.8rem; letter-spacing: -0.04em; }
.field-label { display: block; margin-bottom: 0.5rem; font-weight: 600; }
.token-input { width: 100%; border: 1px solid var(--structure); padding: 0.85rem; background: var(--surface); color: var(--ink); }
.token-input:focus-visible { outline: 3px solid var(--signal); outline-offset: 2px; }
.field-hint { color: var(--ink-muted); font-size: 0.82rem; line-height: 1.5; }
.form-error { color: var(--danger); font-size: 0.85rem; }
.primary-button { width: 100%; margin-top: 1rem; border: 0; padding: 0.9rem 1rem; background: var(--ink); color: var(--surface); cursor: pointer; }
.primary-button:disabled { cursor: wait; opacity: 0.55; }
.primary-button:focus-visible { outline: 3px solid var(--signal); outline-offset: 3px; }

@media (max-width: 760px) {
  .login-layout { grid-template-columns: 1fr; padding: 2rem 1.25rem; }
  .login-intro h1 { font-size: clamp(3rem, 17vw, 5rem); }
}
</style>
