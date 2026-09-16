<script setup lang="ts" vapor>
defineProps<{
  current: string
  state: string
}>()

const emit = defineEmits<{
  navigate: [path: string]
  logout: []
}>()

const links = [
  { path: '/', label: '概览', code: 'OV' },
  { path: '/config', label: '配置', code: 'CF' },
  { path: '/operations', label: '运维', code: 'OP' },
  { path: '/history', label: '历史', code: 'HI' },
]
</script>

<template>
  <aside
    class="app-rail"
    aria-label="ForgeBot 管理导航"
  >
    <div
      class="rail-brand"
      aria-label="ForgeBot"
    >
      F
    </div>
    <nav class="rail-nav">
      <button
        v-for="link in links"
        :key="link.path"
        class="rail-link"
        :class="{ 'rail-link--active': current === link.path }"
        type="button"
        :aria-current="current === link.path ? 'page' : undefined"
        @click="emit('navigate', link.path)"
      >
        <span class="rail-link__code">{{ link.code }}</span>
        <span class="rail-link__label">{{ link.label }}</span>
      </button>
    </nav>
    <div class="rail-footer">
      <span
        class="status-dot"
        :class="`status-dot--${state}`"
        aria-hidden="true"
      />
      <span class="rail-state">{{ state }}</span>
      <button
        class="logout-button"
        type="button"
        @click="emit('logout')"
      >
        退出
      </button>
    </div>
  </aside>
</template>

<style scoped lang="scss">
.app-rail { display: flex; min-height: 100vh; flex-direction: column; border-right: 1px solid rgb(242 239 232 / 18%); background: var(--ink); color: var(--surface); }
.rail-brand { display: grid; width: 2.75rem; height: 2.75rem; margin: 1.25rem auto 2rem; place-items: center; border: 1px solid currentColor; font: 500 1.1rem/1 var(--font-mono); }
.rail-nav { display: grid; gap: 0.25rem; }
.rail-link { display: grid; gap: 0.25rem; border: 0; border-left: 3px solid transparent; padding: 0.85rem 0.75rem; background: transparent; color: rgb(242 239 232 / 66%); text-align: left; cursor: pointer; }
.rail-link:hover, .rail-link--active { border-left-color: var(--signal); background: rgb(242 239 232 / 8%); color: var(--surface); }
.rail-link__code, .rail-state { font: 500 0.68rem/1 var(--font-mono); letter-spacing: 0.08em; }
.rail-link__label { font-size: 0.82rem; }
.rail-footer { display: grid; grid-template-columns: auto 1fr; gap: 0.5rem; align-items: center; margin-top: auto; padding: 1rem 0.75rem; }
.status-dot { width: 0.55rem; height: 0.55rem; border-radius: 50%; background: var(--structure); }
.status-dot--ready { background: var(--healthy); }.status-dot--degraded { background: var(--danger); }.status-dot--applying, .status-dot--restarting { background: var(--signal); }
.logout-button { grid-column: 1 / -1; border: 1px solid rgb(242 239 232 / 28%); padding: 0.45rem; background: transparent; color: var(--surface); font-size: 0.78rem; cursor: pointer; }
</style>
