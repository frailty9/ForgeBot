import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

export const useSessionStore = defineStore('session', () => {
  const authenticated = shallowRef(false)

  function toggle(): void {
    authenticated.value = !authenticated.value
  }

  return { authenticated, toggle }
})
