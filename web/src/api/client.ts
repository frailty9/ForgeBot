import { ApiError, type ConfigResponse, type HealthResponse, type RuntimeStatus } from '@/api/types'

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
}

export const apiClient = new ApiClient()
