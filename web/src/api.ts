import { ref } from 'vue'
import type { Envelope, Session } from './types/api'

export interface FieldError {
  field: string
  code: string
  message: string
}

/** Optional parts of the error envelope beyond code and message. */
export interface ApiErrorExtra {
  category?: string
  hint?: string
  retryable?: boolean
  action?: string
  fields?: FieldError[]
  details?: Record<string, unknown>
  requestId?: string
}

function inferCategory(status: number): string {
  if (status === 401 || status === 403) return 'auth'
  if (status === 404) return 'not_found'
  if (status === 409) return 'conflict'
  if (status === 429) return 'rate_limit'
  if (status >= 500 || status === 0) return 'internal'
  if (status >= 400) return 'validation'
  return 'internal'
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly extra: ApiErrorExtra = {},
  ) {
    super(message)
    this.name = 'ApiError'
  }
  get category(): string {
    return this.extra.category || inferCategory(this.status)
  }
  get fields(): FieldError[] {
    return this.extra.fields ?? []
  }
  get requestId(): string | undefined {
    return this.extra.requestId
  }
  get action(): string {
    return this.extra.action || 'none'
  }
  get retryable(): boolean {
    return this.extra.retryable ?? false
  }
  get hint(): string | undefined {
    return this.extra.hint
  }
  get details(): Record<string, unknown> {
    return this.extra.details ?? {}
  }
}

export const session = ref<Session | null>(null)

const unauthorizedListeners = new Set<() => void>()

/** Register a callback for 401 responses; returns an unsubscribe function. */
export function onUnauthorized(listener: () => void) {
  unauthorizedListeners.add(listener)
  return () => unauthorizedListeners.delete(listener)
}

const rateLimitListeners = new Set<(waitSeconds: number | null) => void>()

/** Register a callback for 429 responses; returns an unsubscribe function. */
export function onRateLimited(listener: (waitSeconds: number | null) => void) {
  rateLimitListeners.add(listener)
  return () => rateLimitListeners.delete(listener)
}

export const REQUEST_TIMEOUT_MS = 30_000

export interface ApiOptions extends Omit<RequestInit, 'body' | 'headers'> {
  body?: BodyInit | null
  headers?: Record<string, string>
  timeoutMs?: number
}

export async function api<T = unknown>(path: string, options: ApiOptions = {}): Promise<Envelope<T>> {
  const method = (options.method || 'GET').toUpperCase()
  const headers: Record<string, string> = { 'Content-Type': 'application/json', ...(options.headers || {}) }
  if (method !== 'GET' && method !== 'HEAD' && session.value?.csrf_token)
    headers['X-CSRF-Token'] = session.value.csrf_token
  const { timeoutMs = REQUEST_TIMEOUT_MS, ...init } = options
  const signal = init.signal ?? AbortSignal.timeout(timeoutMs)
  let response: Response
  try {
    response = await fetch('/api/v1' + path, { credentials: 'include', ...init, method, headers, signal })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'TimeoutError') throw new ApiError(0, 'TIMEOUT', '请求超时')
    throw new ApiError(0, 'NETWORK', '无法连接服务')
  }
  const payload = (await response.json().catch(() => ({}))) as Partial<Envelope<T>> & {
    error?: { code?: string; message?: string } & ApiErrorExtra
    request_id?: string
  }
  if (!response.ok) {
    const code = payload.error?.code || 'HTTP_' + response.status
    if (response.status === 401 && path !== '/auth/login') {
      session.value = session.value ? { ...session.value, authenticated: false } : null
      unauthorizedListeners.forEach((listener) => listener())
    }
    const e = payload.error ?? {}
    if (response.status === 429) {
      const seconds = Number(e.details?.retry_after_seconds ?? response.headers.get('Retry-After'))
      if (Number.isFinite(seconds) && seconds > 0) e.details = { ...e.details, retry_after_seconds: seconds }
      rateLimitListeners.forEach((listener) => listener(Number.isFinite(seconds) && seconds > 0 ? seconds : null))
    }
    throw new ApiError(response.status, code, e.message || '请求失败', {
      category: e.category,
      hint: e.hint,
      retryable: e.retryable,
      action: e.action,
      fields: e.fields,
      details: e.details,
      requestId: payload.request_id || response.headers.get('X-Request-ID') || undefined,
    })
  }
  return payload as Envelope<T>
}

export function errorMessage(error: unknown, fallback = '请求失败'): string {
  if (error instanceof Error) return error.message || fallback
  return fallback
}
