import type { AuthUser } from '@/stores/auth-store'

import { ROLE } from './roles'

export type AdminPermissionMatrix = Record<string, Record<string, boolean>>
export type AdminCapabilities = AdminPermissionMatrix

export const ADMIN_PERMISSION_RESOURCES = {
  CHANNEL: 'channel',
  PRICE_MONITOR: 'admin_menu.price_monitor',
  ADMIN_MENU_PREFIX: 'admin_menu.',
  SYSTEM_SETTINGS_PREFIX: 'system_settings.',
} as const

export const ADMIN_PERMISSION_ACTIONS = {
  READ: 'read',
  OPERATE: 'operate',
  WRITE: 'write',
  SENSITIVE_WRITE: 'sensitive_write',
  SECRET_VIEW: 'secret_view',
  VIEW: 'view',
  EDIT: 'edit',
} as const

// The role whose baseline grants are used as defaults in the permission editor.
export const ADMIN_ROLE_KEY = 'admin'

// The permission catalog (resources, actions, labels and role baselines) is owned
// by the backend authz package and fetched from GET /api/authz/catalog. It is
// intentionally NOT duplicated here so the schema stays defined in one place.
// These types mirror the backend JSON shape.
export interface PermissionActionDef {
  action: string
  label_key: string
  description_key: string
}

export interface PermissionResourceDef {
  resource: string
  label_key: string
  group?: string
  group_label_key?: string
  sort?: number
  actions: PermissionActionDef[]
}

export interface PermissionRoleDef {
  key: string
  name: string
  built_in: boolean
  superuser: boolean
  grants: AdminPermissionMatrix
}

export interface PermissionCatalog {
  resources: PermissionResourceDef[]
  roles: PermissionRoleDef[]
}

export const EMPTY_PERMISSION_CATALOG: PermissionCatalog = {
  resources: [],
  roles: [],
}

export function hasPermission(
  user: AuthUser | null | undefined,
  resource: string,
  action: string
): boolean {
  if (!user) return false
  if (user.role === ROLE.SUPER_ADMIN) return true
  return user.permissions?.admin_permissions?.[resource]?.[action] === true
}

export function systemSettingsResource(scope: string): string {
  return `${ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX}${scope}`
}

export function adminMenuResource(menu: string): string {
  return `${ADMIN_PERMISSION_RESOURCES.ADMIN_MENU_PREFIX}${menu}`
}

export function canViewAdminMenu(
  user: AuthUser | null | undefined,
  menu: string
): boolean {
  return hasPermission(
    user,
    adminMenuResource(menu),
    ADMIN_PERMISSION_ACTIONS.VIEW
  )
}

export function canViewSystemSettingsScope(
  user: AuthUser | null | undefined,
  scope: string
): boolean {
  return hasPermission(
    user,
    systemSettingsResource(scope),
    ADMIN_PERMISSION_ACTIONS.VIEW
  )
}

export function canEditSystemSettingsScope(
  user: AuthUser | null | undefined,
  scope: string
): boolean {
  return hasPermission(
    user,
    systemSettingsResource(scope),
    ADMIN_PERMISSION_ACTIONS.EDIT
  )
}

export function canViewAnySystemSettings(
  user: AuthUser | null | undefined
): boolean {
  if (!user) return false
  if (user.role === ROLE.SUPER_ADMIN) return true
  const permissions = user.permissions?.admin_permissions ?? {}
  return Object.entries(permissions).some(
    ([resource, actions]) =>
      resource.startsWith(ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX) &&
      actions[ADMIN_PERMISSION_ACTIONS.VIEW] === true
  )
}

// roleGrants returns the baseline grant matrix for the given role key.
export function roleGrants(
  catalog: PermissionCatalog,
  roleKey: string
): AdminPermissionMatrix {
  return catalog.roles.find((role) => role.key === roleKey)?.grants ?? {}
}

// normalizeAdminPermissions produces a full matrix for the catalog, filling any
// value missing from `value` with the admin role's baseline grant.
export function normalizeAdminPermissions(
  value: AdminPermissionMatrix | null | undefined,
  catalog: PermissionCatalog
): AdminPermissionMatrix {
  const baseline = roleGrants(catalog, ADMIN_ROLE_KEY)
  const normalized: AdminPermissionMatrix = {}
  for (const resource of catalog.resources) {
    const actions: Record<string, boolean> = {}
    for (const action of resource.actions) {
      actions[action.action] =
        value?.[resource.resource]?.[action.action] ??
        baseline[resource.resource]?.[action.action] ??
        false
    }
    if (
      resource.resource.startsWith(
        ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX
      ) ||
      resource.resource === ADMIN_PERMISSION_RESOURCES.PRICE_MONITOR
    ) {
      if (actions[ADMIN_PERMISSION_ACTIONS.VIEW] === false) {
        actions[ADMIN_PERMISSION_ACTIONS.EDIT] = false
      } else if (actions[ADMIN_PERMISSION_ACTIONS.EDIT] === true) {
        actions[ADMIN_PERMISSION_ACTIONS.VIEW] = true
      }
    }
    normalized[resource.resource] = actions
  }
  return normalized
}
