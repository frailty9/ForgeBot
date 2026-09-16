import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { apiClient, ApiError } from '@/api/client'

export type OperationName = 'restart' | 'reloadSearch' | 'scanReflection'

export const useOperationsStore = defineStore('operations', () => {
  const pending = shallowRef<OperationName | null>(null)
  const result = shallowRef<Record<string, unknown> | null>(null)
  const error = shallowRef('')

  async function run(name: OperationName, token: string, reason = ''): Promise<Record<string, unknown> | null> {
    if (pending.value !== null) return null
    pending.value = name
    result.value = null
    error.value = ''
    try {
      if (name === 'restart') result.value = await apiClient.restart(token, reason) as unknown as Record<string, unknown>
      if (name === 'reloadSearch') result.value = await apiClient.reloadSearch(token)
      if (name === 'scanReflection') result.value = await apiClient.scanReflection(token)
      return result.value
    } catch (cause) {
      error.value = cause instanceof ApiError ? cause.message : '运维操作失败。'
      return null
    } finally {
      pending.value = null
    }
  }

  return { pending, result, error, run }
})
