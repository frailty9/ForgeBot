import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useConfigStore } from '@/stores/config'
import type { ConfigResponse } from '@/api/types'

const snapshot: ConfigResponse = {
  version: 4,
  updated_at: '2026-09-15T00:00:00Z',
  static: { server: { addr: '127.0.0.1:8080', read_timeout: '10s', write_timeout: '30s', shutdown_timeout: '15s' }, runtime_config_path: 'runtime.json' },
  runtime: { log: { level: 'info', format: 'text' }, bot: { name: 'ForgeBot' }, gate: { enabled: false }, llm: { protocol: 'openai_responses', base_url: '', api_key_ref: { configured: true }, model: 'test', timeout: '60s' }, search: {}, platforms: { connectors: [], groups: [] }, administration: {}, reflection: {}, operations: {}, extensions: {} },
  runtime_status: { state: 'ready', action: 'none', version: 4, updated_at: '2026-09-15T00:00:00Z', paused: false, connectors: [], search: { enabled: false, state: 'disabled', documents: 0, sections: 0 }, reflection: { enabled: false, active: false } },
  llm_api_key: { configured: true, version: 1 },
}

describe('config store', () => {
  beforeEach(() => { setActivePinia(createPinia()) })

  it('separates editable draft from the server snapshot', () => {
    const store = useConfigStore()
    store.load(snapshot)
    store.updateDraft((draft) => { draft.bot.name = 'Changed' })

    expect(store.snapshot?.runtime.bot.name).toBe('ForgeBot')
    expect(store.draft?.bot.name).toBe('Changed')
    expect(store.dirty).toBe(true)
  })
})
