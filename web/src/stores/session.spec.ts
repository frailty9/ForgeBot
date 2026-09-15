import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useSessionStore } from '@/stores/session'

describe('session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('toggles the prototype session state', () => {
    const session = useSessionStore()

    expect(session.authenticated).toBe(false)

    session.toggle()

    expect(session.authenticated).toBe(true)
  })
})
