// Presentation layer for errors: turns anything thrown by the API client (or
// plain Errors) into what a notice or a form needs. Pure functions only; the
// global side effects (logout on 401, rate-limit toasts) stay in api.ts.
import { ApiError, type FieldError } from './api'

export interface ErrorView {
  title: string
  detail?: string
  requestId?: string
  action: string
  retryable: boolean
  fields: FieldError[]
  code: string
  category: string
}

export function describeError(err: unknown, fallback = '操作失败'): ErrorView {
  if (err instanceof ApiError) {
    return {
      title: err.message || fallback,
      detail: err.hint,
      requestId: err.requestId,
      action: err.action,
      retryable: err.retryable,
      fields: err.fields,
      code: err.code,
      category: err.category,
    }
  }
  const message = err instanceof Error ? err.message : ''
  return { title: message || fallback, action: 'none', retryable: false, fields: [], code: '', category: 'internal' }
}

/**
 * Maps field errors onto form keys. `map` translates JSON paths the API uses
 * (auth_json.refresh_token) to the form's own keys; unmapped paths are kept as is.
 */
export function applyFieldErrors(fields: FieldError[], map: Record<string, string> = {}): Record<string, string> {
  const out: Record<string, string> = {}
  for (const f of fields) {
    const key = map[f.field] ?? f.field
    if (!out[key]) out[key] = f.message
  }
  return out
}

/** Text a user can paste into a support request. */
export function errorClipboardText(view: ErrorView): string {
  const lines = [[view.code, view.title].filter(Boolean).join('  ')]
  if (view.detail) lines.push(view.detail)
  for (const f of view.fields) lines.push(`${f.field}: ${f.message}`)
  if (view.requestId) lines.push(`请求编号 ${view.requestId}`)
  lines.push(new Date().toISOString())
  return lines.join('\n')
}
