export type RuntimeState = 'ready' | 'applying' | 'restarting' | 'degraded' | 'stopped'

export interface ApiErrorBody {
  code: string
  message: string
  field?: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly field: string | undefined

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.field = body.field
  }
}

export interface SecretStatus {
  configured: boolean
  version: number
  updated_at?: string
}

export interface RuntimeStatus {
  state: RuntimeState
  action: string
  version: number
  updated_at: string
  error?: string
  paused: boolean
  connectors: Array<{ id: string; state: string; error?: string }>
  search: {
    enabled: boolean
    backend?: string
    state: string
    documents: number
    sections: number
    last_reload_at?: string
    last_error?: string
    warning_count?: number
  }
  reflection: {
    enabled: boolean
    active: boolean
  }
}

export interface RuntimeConfig {
  log: { level: string; format: string }
  bot: { name: string; system_prompt?: string }
  gate: Record<string, unknown>
  llm: {
    protocol: string
    base_url: string
    api_key_ref: { configured: boolean; action?: string; value?: string }
    model: string
    anthropic_version?: string
    timeout: string
  }
  search: Record<string, unknown>
  platforms: {
    connectors: Array<Record<string, unknown>>
    groups: Array<Record<string, unknown>>
  }
  administration: Record<string, unknown>
  reflection: Record<string, unknown>
  operations: Record<string, unknown>
  extensions: Record<string, unknown>
}

export interface ConfigResponse {
  version: number
  updated_at: string
  static: {
    server: { addr: string; read_timeout: string; write_timeout: string; shutdown_timeout: string }
    runtime_config_path: string
  }
  runtime: RuntimeConfig
  runtime_status: RuntimeStatus
  llm_api_key: SecretStatus
}

export interface HealthResponse {
  status: string
}
