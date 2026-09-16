import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import { apiClient, ApiError } from '@/api/client'
import type { ConfigResponse, RuntimeConfig } from '@/api/types'

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

export const useConfigStore = defineStore('config', () => {
  const snapshot = shallowRef<ConfigResponse | null>(null)
  const draft = shallowRef<RuntimeConfig | null>(null)
  const validating = shallowRef(false)
  const saving = shallowRef(false)
  const validationError = shallowRef('')
  const saveError = shallowRef('')
  const versionConflict = shallowRef(false)
  const dirty = computed(() => snapshot.value !== null && draft.value !== null && JSON.stringify(snapshot.value.runtime) !== JSON.stringify(draft.value))

  function load(value: ConfigResponse): void {
    snapshot.value = value
    draft.value = clone(value.runtime)
    validationError.value = ''
    saveError.value = ''
    versionConflict.value = false
  }

  function updateDraft(mutator: (value: RuntimeConfig) => void): void {
    if (!draft.value) return
    const next = clone(draft.value)
    mutator(next)
    draft.value = next
    validationError.value = ''
    saveError.value = ''
  }

  async function validate(token: string): Promise<boolean> {
    if (!draft.value) return false
    validating.value = true
    validationError.value = ''
    try {
      await apiClient.validateConfig(token, draft.value)
      return true
    } catch (cause) {
      validationError.value = cause instanceof ApiError ? `${cause.code}: ${cause.message}` : '配置校验失败。'
      return false
    } finally { validating.value = false }
  }

  async function save(token: string): Promise<ConfigResponse | null> {
    if (!draft.value || !snapshot.value) return null
    if (!(await validate(token))) return null
    saving.value = true
    saveError.value = ''
    versionConflict.value = false
    try {
      const response = await apiClient.updateConfig(token, snapshot.value.version, draft.value, 'admin:web-config')
      load(response.config)
      return response.config
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) versionConflict.value = true
      saveError.value = cause instanceof ApiError ? `${cause.code}: ${cause.message}` : '配置保存失败。'
      return null
    } finally { saving.value = false }
  }

  return { snapshot, draft, validating, saving, validationError, saveError, versionConflict, dirty, load, updateDraft, validate, save }
})
