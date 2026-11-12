// Copy text to the clipboard. Browsers only expose navigator.clipboard on
// secure origins (HTTPS or localhost); a deployment reached over plain HTTP
// (http://<ip>:18080) has none, so fall back to the older selection-based
// copy, which still works there because it runs inside the click handler.
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Permission refused or not focused: try the fallback below.
    }
  }
  return copyWithSelection(text)
}

function copyWithSelection(text: string): boolean {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') return false
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '0'
  area.style.left = '0'
  area.style.opacity = '0'
  document.body.appendChild(area)
  const previous = document.activeElement as HTMLElement | null
  area.focus()
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  previous?.focus?.()
  return ok
}
