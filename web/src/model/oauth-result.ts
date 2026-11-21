/** A pasted callback URL or code#state carries everything; a bare code might not. */
export function authorizationResultComplete(text: string): boolean {
  const value = text.trim()
  if (!value || /\s/.test(value)) return false
  if (value.includes('://')) return /[?&]code=[^&]+/.test(value) && /[?&]state=[^&]+/.test(value)
  const parts = value.split('#')
  return parts.length === 2 && !!parts[0] && !!parts[1]
}
