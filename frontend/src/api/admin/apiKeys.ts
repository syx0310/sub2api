/**
 * Admin API Keys API endpoints
 * Handles API key management for administrators
 */

import { apiClient } from '../client'
import type { ApiKey } from '@/types'

export interface UpdateApiKeyGroupResult {
  api_key: ApiKey
  auto_granted_group_access: boolean
  granted_group_id?: number
  granted_group_name?: string
}

export interface ApiKeyConcurrencyResponse {
  available: boolean
  complete: boolean
  collected_at: string
  items: Record<string, number>
}

/**
 * Update an API key's group binding
 * @param id - API Key ID
 * @param groupId - Group ID (0 to unbind, positive to bind, null/undefined to skip)
 * @returns Updated API key with auto-grant info
 */
export async function updateApiKeyGroup(id: number, groupId: number | null): Promise<UpdateApiKeyGroupResult> {
  const { data } = await apiClient.put<UpdateApiKeyGroupResult>(`/admin/api-keys/${id}`, {
    group_id: groupId === null ? 0 : groupId
  })
  return data
}

/** Query exact concurrency for up to 500 API key IDs. */
export async function queryConcurrency(apiKeyIds: number[]): Promise<ApiKeyConcurrencyResponse> {
  const { data } = await apiClient.post<ApiKeyConcurrencyResponse>('/admin/api-keys/concurrency/query', {
    api_key_ids: apiKeyIds
  })
  return data
}

/** Get the sparse, index-backed snapshot of API keys with non-zero concurrency. */
export async function getActiveConcurrency(): Promise<ApiKeyConcurrencyResponse> {
  const { data } = await apiClient.get<ApiKeyConcurrencyResponse>('/admin/api-keys/concurrency')
  return data
}

export const apiKeysAPI = {
  updateApiKeyGroup,
  queryConcurrency,
  getActiveConcurrency
}

export default apiKeysAPI
