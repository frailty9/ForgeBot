import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiClient } from '@/api/client'

describe('ApiClient', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('adds bearer auth and JSON encodes write bodies', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"valid":true}', { status: 200 }))

    await new ApiClient().validateConfig('admin-token', { bot: { name: 'ForgeBot' } } as never)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = fetchMock.mock.calls[0]
    if (!call) throw new Error('fetch was not called')
    const [, init] = call
    expect((init?.headers as Headers).get('Authorization')).toBe('Bearer admin-token')
    expect(init?.body).toContain('ForgeBot')
  })

  it('normalizes structured error responses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"error":{"code":"version_conflict","message":"changed"}}', { status: 409 }))

    await expect(new ApiClient().getHealth()).rejects.toMatchObject({ status: 409, code: 'version_conflict', message: 'changed' })
  })
})
