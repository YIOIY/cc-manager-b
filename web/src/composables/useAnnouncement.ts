import { computed, ref } from 'vue'
import { api } from '../api'

export const announcementBody = ref('')
export const announcementUpdatedAt = ref('')
export const openAnnouncementEditor = ref(false)

let loaded = false
let inflight: Promise<void> | null = null

/** Fetch the site-wide announcement once (or again after publish). */
export function loadAnnouncement(force = false): Promise<void> {
  if (loaded && !force) return Promise.resolve()
  if (inflight) return inflight
  inflight = api<{ body: string; updated_at?: string }>('/announcement')
    .then((r) => {
      announcementBody.value = r.data.body || ''
      announcementUpdatedAt.value = r.data.updated_at || ''
      loaded = true
    })
    .catch(() => undefined)
    .finally(() => {
      inflight = null
    })
  return inflight
}

export function openAnnouncementEdit() {
  openAnnouncementEditor.value = true
}

/** Long enough to prefer a marquee in the header strip. */
export const announcementNeedsMarquee = computed(() => announcementBody.value.trim().length >= 36)
