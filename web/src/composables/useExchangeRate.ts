import { ref } from 'vue'
import { api } from '../api'
import type { ExchangeRate } from '../types/api'

// One rate for the whole page: every card that shows a cost shares it, so a
// list of accounts makes a single request. A failure is silent (the card
// then shows RMB only) and is retried a minute later.
const REFRESH_MS = 30 * 60 * 1000
const RETRY_MS = 60 * 1000

const rate = ref<ExchangeRate | null>(null)
let pending: Promise<void> | null = null
let nextLoadAt = 0

function ensureLoaded() {
  if (pending || Date.now() < nextLoadAt) return
  pending = api<ExchangeRate>('/exchange-rate')
    .then((r) => {
      rate.value = r.data
      nextLoadAt = Date.now() + REFRESH_MS
    })
    .catch(() => {
      nextLoadAt = Date.now() + RETRY_MS
    })
    .finally(() => {
      pending = null
    })
}

export function useExchangeRate() {
  ensureLoaded()
  return { rate }
}

/** Test helper: forget the cached rate. */
export function resetExchangeRate() {
  rate.value = null
  pending = null
  nextLoadAt = 0
}
