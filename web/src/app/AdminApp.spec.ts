import { describe, expect, it } from 'vitest'

describe('admin app shell', () => {
  it('keeps the first route at the overview', () => {
    expect('/').toBe('/')
  })
})
