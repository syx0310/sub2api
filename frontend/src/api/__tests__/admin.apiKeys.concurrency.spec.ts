import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('../client', () => ({
  apiClient: {
    get,
    post
  }
}))

import { getActiveConcurrency, queryConcurrency } from '@/api/admin/apiKeys'

describe('admin API key concurrency API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
  })

  it('queries exact counts for a caller-supplied batch', async () => {
    const response = {
      available: true,
      complete: true,
      collected_at: '2026-08-04T00:00:00Z',
      items: { '10': 2, '11': 0 }
    }
    post.mockResolvedValue({ data: response })

    await expect(queryConcurrency([10, 11])).resolves.toEqual(response)
    expect(post).toHaveBeenCalledWith('/admin/api-keys/concurrency/query', {
      api_key_ids: [10, 11]
    })
  })

  it('fetches the sparse active-key snapshot', async () => {
    const response = {
      available: true,
      complete: false,
      collected_at: '2026-08-04T00:00:00Z',
      items: { '10': 2 }
    }
    get.mockResolvedValue({ data: response })

    await expect(getActiveConcurrency()).resolves.toEqual(response)
    expect(get).toHaveBeenCalledWith('/admin/api-keys/concurrency')
  })
})
