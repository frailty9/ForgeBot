import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

const storageKey = 'forgebot.admin.token'
const storage = typeof sessionStorage === 'undefined' ? null : sessionStorage

export const useSessionStore = defineStore('session', () => {
  const token = shallowRef(storage?.getItem(storageKey) ?? '')
  const busy = shallowRef(false)
  const error = shallowRef('')
  const authenticated = computed(() => token.value !== '')

  function setError(message: string): void { error.value = message }
  function begin(): void { busy.value = true; error.value = '' }
  function complete(value: string): void {
    token.value = value
    storage?.setItem(storageKey, value)
    busy.value = false
  }
  function clear(): void {
    token.value = ''
    busy.value = false
    error.value = ''
    storage?.removeItem(storageKey)
  }

  return { token, busy, error, authenticated, begin, complete, clear, setError }
})
