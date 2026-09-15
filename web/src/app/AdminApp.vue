<script setup lang="ts" vapor>
import { storeToRefs } from 'pinia'

import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const { authenticated } = storeToRefs(session)
</script>

<template>
  <main class="admin-shell min-h-screen">
    <aside
      class="signal-rail"
      aria-label="ForgeBot 管理导航"
    >
      <span
        class="signal-rail__mark"
        aria-hidden="true"
      >F</span>
      <span class="signal-rail__state" />
    </aside>

    <section class="workspace">
      <p class="eyebrow">
        FORGEBOT / CONTROL PLANE
      </p>
      <h1>运行控制台</h1>
      <p class="lede">
        Vapor 渲染链路已就绪，管理功能将在后续阶段接入。
      </p>
      <button
        class="session-button"
        type="button"
        @click="session.toggle()"
      >
        {{ authenticated ? '清除原型会话' : '验证 Pinia 会话' }}
      </button>
      <output
        class="session-state"
        aria-live="polite"
      >
        SESSION / {{ authenticated ? 'ACTIVE' : 'LOCKED' }}
      </output>
    </section>
  </main>
</template>

<style scoped lang="scss">
.admin-shell {
  display: grid;
  grid-template-columns: 4.5rem minmax(0, 1fr);
  min-width: 20rem;
  background:
    linear-gradient(90deg, transparent 0 95%, rgb(23 26 29 / 4%) 95%),
    var(--surface);
  background-size: 2rem 100%;
  color: var(--ink);
}

.signal-rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1.5rem;
  padding-block: 1.25rem;
  background: var(--ink);
  color: var(--surface);

  &__mark {
    display: grid;
    width: 2.25rem;
    aspect-ratio: 1;
    place-items: center;
    border: 1px solid currentColor;
    font: 500 1rem/1 var(--font-mono);
  }

  &__state {
    width: 0.625rem;
    aspect-ratio: 1;
    border-radius: 50%;
    background: var(--signal);
    box-shadow: 0 0 0 0.25rem rgb(231 111 46 / 18%);
  }
}

.workspace {
  align-self: center;
  max-width: 52rem;
  padding: clamp(2rem, 8vw, 8rem);

  h1 {
    max-width: 8ch;
    margin: 0;
    font-size: clamp(3rem, 9vw, 7rem);
    font-weight: 600;
    letter-spacing: -0.075em;
    line-height: 0.92;
  }
}

.eyebrow,
.session-state {
  font-family: var(--font-mono);
  letter-spacing: 0.08em;
}

.eyebrow {
  margin-block: 0 1.5rem;
  color: var(--signal);
  font-size: 0.75rem;
}

.lede {
  max-width: 34rem;
  margin-block: 2rem;
  color: var(--ink-muted);
  font-size: 1.125rem;
  line-height: 1.8;
}

.session-button {
  border: 1px solid var(--ink);
  padding: 0.75rem 1rem;
  background: var(--ink);
  color: var(--surface);
  font: inherit;
  cursor: pointer;

  &:focus-visible {
    outline: 3px solid var(--signal);
    outline-offset: 3px;
  }
}

.session-state {
  display: block;
  margin-top: 1.5rem;
  font-size: 0.75rem;
}
</style>
