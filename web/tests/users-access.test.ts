import { expect, test } from 'vitest'
import {
  ADMIN_ROLE,
  canGrantWith,
  effectivePermissionCount,
  moduleSummary,
  pageModules,
  presetPermissions,
  roleSnapshot,
  togglePermission,
  visiblePages,
} from '../src/components/users/access'
import type { Permission, PermissionModule, RoleRecord, SessionUser } from '../src/types/api'

const catalogue: PermissionModule[] = [
  {
    key: 'accounts',
    label: '账号',
    page: '账号库存',
    permissions: [
      { key: 'accounts.view', label: '查看' },
      { key: 'accounts.create', label: '新建' },
      { key: 'accounts.import', label: '导入' },
    ],
  },
  {
    key: 'proxies',
    label: '代理',
    page: '代理池',
    permissions: [
      { key: 'proxies.view', label: '查看' },
      { key: 'proxies.test', label: '检测' },
    ],
  },
  {
    key: 'users',
    label: '用户',
    page: '用户与权限',
    permissions: [
      { key: 'users.view', label: '查看' },
      { key: 'users.create', label: '新建' },
    ],
  },
  {
    key: 'bills',
    label: '账单',
    permissions: [{ key: 'bills.sync', label: '同步' }],
  },
]
const role = (id: string, permissions: Permission[]): RoleRecord =>
  ({ id, name: id, description: '', builtin: false, permissions, version: 1, user_count: 0 }) as RoleRecord
const everything = catalogue.flatMap((m) => m.permissions.map((p) => p.key))
const grantAll = () => true

test('visiblePages and moduleSummary follow the catalogue order and only count granted modules', () => {
  const r = role('ops', ['proxies.view', 'proxies.test', 'accounts.view'])
  expect(pageModules(catalogue).map((m) => m.key)).toEqual(['accounts', 'proxies', 'users'])
  expect(visiblePages(r, catalogue)).toEqual(['账号库存', '代理池'])
  expect(moduleSummary(r, catalogue)).toEqual([
    { key: 'accounts', label: '账号', total: 3, granted: 1 },
    { key: 'proxies', label: '代理', total: 2, granted: 2 },
  ])
})

test('hiding a page drops every permission of that module', () => {
  const next = togglePermission(
    ['accounts.view', 'accounts.create', 'accounts.import', 'proxies.view'],
    'accounts.view',
    false,
    catalogue,
    grantAll,
  )
  expect(next).toEqual(['proxies.view'])
})

test('enabling an action turns the page on when the actor may grant it', () => {
  expect(togglePermission([], 'proxies.test', true, catalogue, grantAll)).toEqual(['proxies.test', 'proxies.view'])
  const cannotGrantView = (p: Permission) => p !== 'proxies.view'
  expect(togglePermission([], 'proxies.test', true, catalogue, cannotGrantView)).toEqual(['proxies.test'])
  // modules without a page are left alone
  expect(togglePermission(['bills.sync'], 'bills.sync', false, catalogue, grantAll)).toEqual([])
  // toggling an existing permission on is idempotent
  expect(togglePermission(['accounts.view'], 'accounts.view', true, catalogue, grantAll)).toEqual(['accounts.view'])
})

test('presets: readonly excludes users/roles views, role: copies only grantable permissions', () => {
  expect(presetPermissions('readonly', everything, [], grantAll)).toEqual(['accounts.view', 'proxies.view'])
  expect(presetPermissions('all', everything, [], grantAll)).toEqual(everything)
  expect(presetPermissions('none', everything, [], grantAll)).toEqual([])
  const src = role('src', ['accounts.view', 'users.create'])
  expect(presetPermissions('role:src', everything, [src], (p) => p !== 'users.create')).toEqual(['accounts.view'])
  expect(presetPermissions('role:missing', everything, [src], grantAll)).toBeNull()
  expect(presetPermissions('bogus', everything, [src], grantAll)).toBeNull()
})

test('canGrantWith: admins grant anything, others only what they hold', () => {
  const admin = { is_admin: true, permissions: [] } as unknown as SessionUser
  const viewer = { is_admin: false, permissions: ['accounts.view'] } as unknown as SessionUser
  expect(canGrantWith(admin)('users.create')).toBe(true)
  expect(canGrantWith(viewer)('accounts.view')).toBe(true)
  expect(canGrantWith(viewer)('users.create')).toBe(false)
  expect(canGrantWith(null)('accounts.view')).toBe(false)
})

test('effectivePermissionCount unions roles and treats the admin role as everything', () => {
  const roles = [role('a', ['accounts.view', 'accounts.create']), role('b', ['accounts.view', 'proxies.view'])]
  expect(effectivePermissionCount([], roles, catalogue)).toBeNull()
  expect(effectivePermissionCount(['a', 'b'], roles, catalogue)).toBe(3)
  expect(effectivePermissionCount([ADMIN_ROLE], roles, catalogue)).toBe(everything.length)
})

test('roleSnapshot ignores whitespace and permission order', () => {
  expect(roleSnapshot(' ops ', 'd', ['proxies.view', 'accounts.view'])).toBe(
    roleSnapshot('ops', 'd ', ['accounts.view', 'proxies.view']),
  )
  expect(roleSnapshot('ops', 'd', ['accounts.view'])).not.toBe(roleSnapshot('ops', 'd', []))
})
