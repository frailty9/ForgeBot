import { ApiError, type ApplyResponse, type ConfigResponse, type HealthResponse, type RevisionMeta, type RuntimeStatus, type RuntimeConfig } from '@/api/types'

export { ApiError }

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function errorBody(value: unknown): { code: string; message: string; field?: string } {
  if (isRecord(value) && isRecord(value.error)) {
    const error = value.error
    return {
      code: typeof error.code === 'string' ? error.code : 'request_failed',
      message: typeof error.message === 'string' ? error.message : '请求失败',
      ...(typeof error.field === 'string' ? { field: error.field } : {}),
    }
  }
  return { code: 'request_failed', message: '服务端返回了无法识别的错误' }
}

export class ApiClient {
  constructor(private readonly baseUrl = '') {}

  async request<T>(path: string, options: RequestOptions = {}, token?: string): Promise<T> {
    const headers = new Headers(options.headers)
    headers.set('Accept', 'application/json')
    if (options.body !== undefined) {
      headers.set('Content-Type', 'application/json')
    }
    if (token) {
      headers.set('Authorization', `Bearer ${token}`)
    }
    const { body: requestBody, ...initOptions } = options
    const requestInit: RequestInit = { ...initOptions, headers }
    if (requestBody !== undefined) requestInit.body = JSON.stringify(requestBody)
    const response = await fetch(`${this.baseUrl}${path}`, requestInit)
    const text = await response.text()
    let payload: unknown = undefined
    if (text.trim() !== '') {
      try {
        payload = JSON.parse(text) as unknown
      } catch {
        throw new ApiError(response.status, { code: 'invalid_response', message: '服务端返回了无效 JSON' })
      }
    }
    if (!response.ok) {
      throw new ApiError(response.status, errorBody(payload))
    }
    return payload as T
  }

  getConfig(token: string, signal?: AbortSignal): Promise<ConfigResponse> {
    return this.request<ConfigResponse>('/v1/admin/config', signal === undefined ? {} : { signal }, token)
  }

  getRuntime(token: string, signal?: AbortSignal): Promise<RuntimeStatus> {
    return this.request<RuntimeStatus>('/v1/admin/runtime', signal === undefined ? {} : { signal }, token)
  }

  getHealth(signal?: AbortSignal): Promise<HealthResponse> {
    return this.request<HealthResponse>('/healthz', signal === undefined ? {} : { signal })
  }

  restart(token: string, reason: string): Promise<RuntimeStatus> {
    return this.request<RuntimeStatus>('/v1/admin/runtime/restart', { method: 'POST', body: { reason } }, token)
  }

  reloadSearch(token: string): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('/v1/admin/search/reload', { method: 'POST' }, token)
  }

  scanReflection(token: string): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('/v1/admin/reflection/scan', { method: 'POST' }, token)
  }

  validateConfig(token: string, runtime: RuntimeConfig): Promise<{ valid: boolean }> {
    return this.request<{ valid: boolean }>('/v1/admin/config/validate', { method: 'POST', body: { runtime } }, token)
  }

  updateConfig(token: string, expectedVersion: number, runtime: RuntimeConfig, source: string): Promise<ApplyResponse> {
    return this.request<ApplyResponse>('/v1/admin/config', { method: 'PUT', body: { expected_version: expectedVersion, runtime, source } }, token)
  }

  history(token: string): Promise<RevisionMeta[]> {
    return this.request<RevisionMeta[]>('/v1/admin/config/history', {}, token)
  }

  rollback(token: string, targetVersion: number, expectedVersion: number): Promise<ApplyResponse> {
    return this.request<ApplyResponse>('/v1/admin/config/rollback', { method: 'POST', body: { target_version: targetVersion, expected_version: expectedVersion, source: 'admin:web-rollback' } }, token)
  }

  replaceAPIKey(token: string, apiKey: string): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('/v1/admin/llm/api-key', { method: 'PUT', body: { api_key: apiKey } }, token)
  }

  deleteAPIKey(token: string): Promise<void> {
    return this.request<void>('/v1/admin/llm/api-key', { method: 'DELETE' }, token)
  }
}

export const apiClient = new ApiClient()
