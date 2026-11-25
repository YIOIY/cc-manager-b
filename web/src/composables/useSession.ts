import { computed, ref } from 'vue'
import { api, ApiError, errorMessage, onUnauthorized, session } from '../api'
import type { Permission, Session, SessionUser } from '../types/api'

const authenticated = ref(false)
const checked = ref(false)
const user = ref<SessionUser | null>(null)
let inflight: Promise<void> | null = null

/** Resolves once the initial session check has completed (or failed). */
export function sessionReady(): Promise<void> {
  if (checked.value) return Promise.resolve()
  return inflight ?? Promise.resolve()
}

/** True once somebody is signed in. */
export function signedIn(): boolean {
  return user.value !== null
}

/** True when the signed-in user holds the permission. */
export function can(permission: Permission): boolean {
  return !!user.value?.permissions.includes(permission)
}

/** True when the user-level上号 switch is on (defaults true for older payloads). */
export function onboardAllowed(): boolean {
  return user.value?.can_onboard !== false
}

/** True when the user may start Claude onboarding (RBAC + user switch). */
export function canOnboard(): boolean {
  return can('accounts.onboard') && onboardAllowed()
}

/** True when the signed-in user may mutate a row with this created_by_user_id. */
export function owns(createdByUserId?: string | null): boolean {
  const me = user.value
  if (!me?.id) return false
  if (createdByUserId && createdByUserId === me.id) return true
  return !createdByUserId && !!me.is_admin
}

onUnauthorized(() => {
  authenticated.value = false
  user.value = null
  // The server dropped our session; fetch a fresh anonymous one so the next
  // login carries a valid CSRF token instead of the revoked one.
  void refreshAnonymous()
})

let refreshing: Promise<void> | null = null
function refreshAnonymous(): Promise<void> {
  if (!refreshing) {
    refreshing = api<Session>('/auth/session')
      .then((r) => {
        session.value = r.data
        authenticated.value = !!r.data?.authenticated
        user.value = r.data?.user ?? null
      })
      .catch(() => undefined)
      .finally(() => {
        refreshing = null
      })
  }
  return refreshing
}

/** Shared session state: one instance for the whole app. */
export function useSession() {
  async function check() {
    const run = (async () => {
      try {
        const r = await api<Session>('/auth/session')
        session.value = r.data
        authenticated.value = !!r.data?.authenticated
        user.value = r.data?.user ?? null
      } finally {
        checked.value = true
      }
    })()
    inflight = run.catch(() => undefined)
    return run
  }
  async function login(username: string, password: string, captchaId: string, captcha: string) {
    if (refreshing) await refreshing
    const body = () =>
      JSON.stringify({
        username,
        password,
        captcha_id: captchaId,
        captcha,
      })
    const attempt = () => api<Session>('/auth/login', { method: 'POST', body: body() })
    try {
      await attempt()
    } catch (e) {
      // A stale anonymous session (expired or revoked) yields CSRF_FAILED;
      // obtain a new one and retry once before surfacing the error.
      if (!(e instanceof ApiError) || e.code !== 'CSRF_FAILED') throw e
      await refreshAnonymous()
      await attempt()
    }
    await check()
  }
  async function logout() {
    try {
      await api('/auth/logout', { method: 'POST' })
    } finally {
      authenticated.value = false
      session.value = null
      user.value = null
      // A fresh anonymous session provides the CSRF token for the next login.
      await check().catch(() => undefined)
    }
  }
  async function changePassword(current: string, next: string) {
    await api('/auth/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: current, new_password: next }),
    })
  }
  return {
    authenticated: computed(() => authenticated.value),
    checked: computed(() => checked.value),
    user: computed(() => user.value),
    can,
    onboardAllowed,
    canOnboard,
    owns,
    check,
    login,
    logout,
    changePassword,
    errorMessage,
  }
}
