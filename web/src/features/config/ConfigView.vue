<script setup lang="ts" vapor>
import { computed, shallowRef } from 'vue'
import { storeToRefs } from 'pinia'

import { apiClient, ApiError } from '@/api/client'
import type { ConfigResponse, RuntimeConfig } from '@/api/types'
import { useConfigStore } from '@/stores/config'

const props = defineProps<{ token: string; initial: ConfigResponse }>()
const emit = defineEmits<{ updated: [config: ConfigResponse] }>()
const store = useConfigStore()
const { draft, dirty, validating, saving, validationError, saveError, versionConflict } = storeToRefs(store)
const secretValue = shallowRef('')
const secretBusy = shallowRef(false)
const secretMessage = shallowRef('')

if (!store.snapshot) store.load(props.initial)

const jsonPreview = computed(() => draft.value ? JSON.stringify(draft.value, null, 2) : '')

function setField(path: 'name' | 'system_prompt' | 'model' | 'base_url' | 'anthropic_version', value: string): void {
  store.updateDraft((next: RuntimeConfig) => {
    if (path === 'name' || path === 'system_prompt') next.bot[path] = value
    else next.llm[path] = value
  })
}

function setGateEnabled(value: boolean): void { store.updateDraft((next) => { next.gate.enabled = value }) }

async function save(): Promise<void> {
  const updated = await store.save(props.token)
  if (updated) emit('updated', updated)
}

async function replaceKey(): Promise<void> {
  if (!secretValue.value.trim()) return
  secretBusy.value = true
  secretMessage.value = ''
  try {
    await apiClient.replaceAPIKey(props.token, secretValue.value)
    secretValue.value = ''
    secretMessage.value = 'API Key 已替换。'
  } catch (cause) { secretMessage.value = cause instanceof ApiError ? cause.message : 'API Key 替换失败。' }
  finally { secretBusy.value = false }
}

async function deleteKey(): Promise<void> {
  secretBusy.value = true
  secretMessage.value = ''
  try { await apiClient.deleteAPIKey(props.token); secretMessage.value = 'API Key 已删除。' }
  catch (cause) { secretMessage.value = cause instanceof ApiError ? cause.message : 'API Key 删除失败。' }
  finally { secretBusy.value = false }
}
</script>

<template>
  <section class="config-view">
    <header class="config-toolbar">
      <div><span class="meta-label">CONFIG / DRAFT</span><p>{{ dirty ? '有未保存修改' : '草稿与服务端同步' }} · version {{ store.snapshot?.version }}</p></div>
      <div class="toolbar-actions">
        <button
          type="button"
          :disabled="validating || !dirty"
          @click="store.validate(token)"
        >
          {{ validating ? '校验中…' : '校验' }}
        </button><button
          class="primary-button"
          type="button"
          :disabled="saving || !dirty"
          @click="save"
        >
          {{ saving ? '应用中…' : '应用配置' }}
        </button>
      </div>
    </header>
    <p
      v-if="validationError"
      class="config-error"
      role="alert"
    >
      {{ validationError }}
    </p>
    <p
      v-if="saveError"
      class="config-error"
      role="alert"
    >
      {{ saveError }} <span v-if="versionConflict">请重新加载后合并草稿。</span>
    </p>
    <div class="config-sections">
      <section class="config-section">
        <span class="section-index">01 / BOT</span><h2>Bot 与提示词</h2><label>显示名称<input
          :value="draft?.bot.name"
          @input="setField('name', ($event.target as HTMLInputElement).value)"
        ></label><label>系统提示词<textarea
          :value="draft?.bot.system_prompt"
          rows="5"
          @input="setField('system_prompt', ($event.target as HTMLTextAreaElement).value)"
        /></label>
      </section>
      <section class="config-section">
        <span class="section-index">02 / LLM</span><h2>模型与供应商</h2><label>协议<select :value="draft?.llm.protocol"><option value="openai_responses">OpenAI Responses</option><option value="openai_completions">OpenAI Completions</option><option value="anthropic_messages">Anthropic Messages</option></select></label><label>Base URL<input
          :value="draft?.llm.base_url"
          @input="setField('base_url', ($event.target as HTMLInputElement).value)"
        ></label><label>模型<input
          :value="draft?.llm.model"
          @input="setField('model', ($event.target as HTMLInputElement).value)"
        ></label><label>Anthropic Version<input
          :value="draft?.llm.anthropic_version"
          @input="setField('anthropic_version', ($event.target as HTMLInputElement).value)"
        ></label><div class="secret-row">
          <span>API Key <strong>{{ initial.llm_api_key.configured ? '已配置' : '未配置' }}</strong></span><input
            v-model="secretValue"
            type="password"
            autocomplete="new-password"
            placeholder="只写，不可读"
          ><button
            type="button"
            :disabled="secretBusy || !secretValue.trim()"
            @click="replaceKey"
          >
            替换
          </button><button
            type="button"
            :disabled="secretBusy || !initial.llm_api_key.configured"
            @click="deleteKey"
          >
            删除
          </button>
        </div><p
          v-if="secretMessage"
          class="field-message"
        >
          {{ secretMessage }}
        </p>
      </section>
      <section class="config-section">
        <span class="section-index">03 / GATE</span><h2>消息 Gate</h2><label class="switch-row"><input
          type="checkbox"
          :checked="Boolean(draft?.gate.enabled)"
          @change="setGateEnabled(($event.target as HTMLInputElement).checked)"
        ><span>启用消息 Gate</span></label><p class="section-help">
          Gate 的缓冲、去重和主动介入参数保留在同一份运行时草稿中，后续页面会提供细粒度控件。
        </p>
      </section>
      <section class="config-section config-section--advanced">
        <span class="section-index">04—09 / ADVANCED</span><h2>其他领域</h2><p>结构化检索、平台群组、管理员、反思、MCP 与 Skill 使用同一份安全草稿。高级字段暂以 JSON 预览呈现，敏感值只保留 configured 状态。</p><details><summary>查看完整草稿 JSON</summary><pre>{{ jsonPreview }}</pre></details>
      </section>
    </div>
  </section>
</template>

<style scoped lang="scss">
.config-view { max-width: 70rem; padding-top: 2rem; }.config-toolbar { display: flex; align-items: end; justify-content: space-between; gap: 2rem; border-bottom: 1px solid var(--structure); padding-bottom: 1rem; }.config-toolbar p { margin: 0.5rem 0 0; color: var(--ink-muted); }.toolbar-actions { display: flex; gap: 0.5rem; }.toolbar-actions button, .secret-row button { border: 1px solid var(--ink); padding: 0.65rem 0.8rem; background: transparent; color: var(--ink); cursor: pointer; }.toolbar-actions .primary-button { background: var(--ink); color: var(--surface); }.toolbar-actions button:disabled, .secret-row button:disabled { cursor: not-allowed; opacity: 0.45; }.config-error { border-left: 3px solid var(--danger); padding: 0.7rem 1rem; background: rgb(201 75 69 / 8%); color: var(--danger); }.config-sections { display: grid; gap: 1rem; margin-top: 2rem; }.config-section { display: grid; gap: 0.8rem; border-top: 2px solid var(--ink); padding: 1.25rem 0 2rem; }.section-index, .meta-label { color: var(--signal); font: 500 0.72rem/1 var(--font-mono); letter-spacing: 0.08em; }.config-section h2 { margin: 0 0 0.75rem; font-size: 1.6rem; }.config-section label { display: grid; gap: 0.4rem; max-width: 42rem; font-size: 0.88rem; font-weight: 600; }.config-section input:not([type='checkbox']), .config-section select, .config-section textarea { width: 100%; border: 1px solid var(--structure); padding: 0.7rem; background: transparent; color: var(--ink); font-weight: 400; }.config-section textarea { resize: vertical; }.config-section input:focus-visible, .config-section select:focus-visible, .config-section textarea:focus-visible { outline: 3px solid var(--signal); outline-offset: 2px; }.secret-row { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; max-width: 42rem; border-top: 1px solid var(--structure); padding-top: 1rem; }.secret-row span { margin-right: auto; }.secret-row strong { color: var(--healthy); font-weight: 500; }.secret-row input { max-width: 16rem; border: 1px solid var(--structure); padding: 0.65rem; background: transparent; }.field-message, .section-help { margin: 0; color: var(--ink-muted); font-size: 0.85rem; line-height: 1.6; }.switch-row { display: flex !important; grid-template-columns: auto 1fr; align-items: center; }.switch-row input { width: 1rem !important; }.config-section details { max-width: 100%; }.config-section summary { cursor: pointer; color: var(--signal); }.config-section pre { overflow: auto; max-height: 28rem; margin: 0.75rem 0 0; border: 1px solid var(--structure); padding: 1rem; background: rgb(23 26 29 / 4%); font: 0.75rem/1.5 var(--font-mono); }
@media (max-width: 650px) { .config-toolbar { align-items: start; flex-direction: column; }.toolbar-actions { width: 100%; }.toolbar-actions button { flex: 1; }.secret-row { align-items: stretch; flex-direction: column; }.secret-row span { margin-right: 0; }.secret-row input { max-width: none; }.secret-row button { width: 100%; } }
</style>
