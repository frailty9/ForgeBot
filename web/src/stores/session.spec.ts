import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useSessionStore } from '@/stores/session'

describe('session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('stores and clears the session token', () => {
    const session = useSessionStore()

    expect(session.authenticated).toBe(false)

    session.complete('test-token')

    expect(session.authenticated).toBe(true)

    session.clear()

    expect(session.authenticated).toBe(false)
  })
})
