import { ref } from 'vue'
import { api } from '../api'

// 用户之声 can be opened from anywhere (the header button, an error notice),
// so the dialog's state lives here and the dialog is mounted once in the shell.

export interface FeedbackPrefill {
  title?: string
  /** Set when opened from an error notice. */
  errorCode?: string
  requestId?: string
}

const open = ref(false)
const prefill = ref<FeedbackPrefill | null>(null)

export function openFeedback(seed?: FeedbackPrefill) {
  prefill.value = seed ?? null
  open.value = true
}

export function useFeedbackDialog() {
  return { open, prefill }
}

/** Bumped after a submission or a handling so open lists reload. */
export const feedbackChanged = ref(0)

/** Feedback still waiting, within what the user may see; shown on the navigation. */
export const feedbackOpenCount = ref(0)

export async function refreshFeedbackSummary() {
  try {
    feedbackOpenCount.value = (await api<{ open: number }>('/feedback/summary')).data.open
  } catch {
    // The number is a convenience; the page itself reports load failures.
  }
}
