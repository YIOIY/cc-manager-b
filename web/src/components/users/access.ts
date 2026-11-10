import type { Permission, PermissionModule, RoleRecord, SessionUser } from '../../types/api'

export const ADMIN_ROLE = 'role-admin'

/** The ".view" permission that reveals a module's page in the navigation. */
export const viewKey = (mod: PermissionModule) => (mod.key + '.view') as Permission

export const pageModules = (catalogue: PermissionModule[]) => catalogue.filter((m) => !!m.page)

export const totalPermissions = (catalogue: PermissionModule[]) =>
  catalogue.reduce((n, m) => n + m.permissions.length, 0)

export const moduleOf = (catalogue: PermissionModule[], key: Permission) =>
  catalogue.find((m) => key.startsWith(m.key + '.'))

/** Page names a role can see, in catalogue order. */
export function visiblePages(role: RoleRecord, catalogue: PermissionModule[]): string[] {
  return pageModules(catalogue)
    .filter((m) => role.permissions.includes(viewKey(m)))
    .map((m) => m.page as string)
}

export interface ModuleSummary {
  key: string
  label: string
  total: number
  granted: number
}

/** Per-module granted/total counts for the modules a role touches. */
export function moduleSummary(role: RoleRecord, catalogue: PermissionModule[]): ModuleSummary[] {
  return catalogue
    .map((m) => ({
      key: m.key,
      label: m.label,
      total: m.permissions.length,
      granted: m.permissions.filter((p) => role.permissions.includes(p.key)).length,
    }))
    .filter((m) => m.granted > 0)
}

/** Whether the current user may grant a permission: admins grant anything, others only what they hold. */
export const canGrantWith = (me: SessionUser | null | undefined) => (p: Permission) =>
  !!me?.is_admin || !!me?.permissions.includes(p)

/** Size of the union of the selected roles' permissions; null when nothing is selected. */
export function effectivePermissionCount(
  roleIds: string[],
  roles: RoleRecord[],
  catalogue: PermissionModule[],
): number | null {
  if (!roleIds.length) return null
  if (roleIds.includes(ADMIN_ROLE)) return totalPermissions(catalogue)
  const set = new Set<string>()
  for (const r of roles) if (roleIds.includes(r.id)) r.permissions.forEach((p) => set.add(p))
  return set.size
}

/**
 * Toggle one permission and apply the page dependency rules: hiding a page
 * drops every action reachable only from it, enabling an action turns the
 * page on (when the actor may grant it). Returns a new array.
 */
export function togglePermission(
  current: Permission[],
  key: Permission,
  on: boolean,
  catalogue: PermissionModule[],
  canGrant: (p: Permission) => boolean,
): Permission[] {
  let next = on ? (current.includes(key) ? [...current] : [...current, key]) : current.filter((p) => p !== key)
  const mod = moduleOf(catalogue, key)
  if (!mod?.page) return next
  const view = viewKey(mod)
  if (key === view && !on) next = next.filter((p) => !p.startsWith(mod.key + '.'))
  else if (key !== view && on && !next.includes(view) && canGrant(view)) next.push(view)
  return next
}

/** Preset menu commands: readonly / all / none / role:<id>. Returns null for unknown commands. */
export function presetPermissions(
  cmd: string,
  grantable: Permission[],
  roles: RoleRecord[],
  canGrant: (p: Permission) => boolean,
): Permission[] | null {
  if (cmd === 'all') return [...grantable]
  if (cmd === 'none') return []
  if (cmd === 'readonly')
    return grantable.filter((p) => p.endsWith('.view') && !p.startsWith('users.') && !p.startsWith('roles.'))
  if (cmd.startsWith('role:')) {
    const src = roles.find((r) => r.id === cmd.slice(5))
    return src ? src.permissions.filter(canGrant) : null
  }
  return null
}

/** Stable fingerprint of a role form, used for the dirty check. */
export const roleSnapshot = (name: string, description: string, permissions: Permission[]) =>
  JSON.stringify({ n: name.trim(), d: description.trim(), p: [...permissions].sort() })
