import { onScopeDispose, ref } from 'vue'

/**
 * Runs fn every `intervalMs` while the page is visible, using a setTimeout
 * chain so a slow request never overlaps the next tick.
 */
export function usePolling(fn: () => Promise<unknown> | unknown, intervalMs: number) {
  const active = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let generation = 0

  function schedule(gen: number) {
    timer = setTimeout(async () => {
      if (gen !== generation || !active.value) return
      if (typeof document === 'undefined' || document.visibilityState !== 'hidden') {
        try {
          await fn()
        } catch {
          /* fn reports its own errors */
        }
      }
      if (gen === generation && active.value) schedule(gen)
    }, intervalMs)
  }
  function start() {
    if (active.value) return
    active.value = true
    schedule(++generation)
  }
  function stop() {
    active.value = false
    generation++
    if (timer) clearTimeout(timer)
    timer = undefined
  }
  function onVisibility() {
    if (document.visibilityState === 'visible' && active.value) {
      stop()
      start()
    }
  }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
  onScopeDispose(() => {
    stop()
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
  })
  return { start, stop, active }
}
