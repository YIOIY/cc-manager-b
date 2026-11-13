import { beforeEach, expect, test, vi } from 'vitest'

const state = { signedIn: false, permissions: new Set<string>() }
vi.mock('../src/composables/useSession', () => ({
  can: (p: string) => state.permissions.has(p),
  signedIn: () => state.signedIn,
  sessionReady: async () => {},
}))

import { firstAllowedRoute } from '../src/router'

beforeEach(() => {
  state.signedIn = false
  state.permissions = new Set()
})

test('before login the landing page stays the dashboard, so signing in opens it', () => {
  // The help page needs no permission; it must not capture people who have not signed in yet.
  expect(firstAllowedRoute()).toBe('dashboard')
})

test('a signed-in user lands on the first page they may open', () => {
  state.signedIn = true
  state.permissions = new Set(['dashboard.view', 'accounts.view'])
  expect(firstAllowedRoute()).toBe('dashboard')
  state.permissions = new Set(['accounts.view'])
  expect(firstAllowedRoute()).toBe('accounts')
})

test('a signed-in user without any page permission still has the help page', () => {
  state.signedIn = true
  expect(firstAllowedRoute()).toBe('help')
})
