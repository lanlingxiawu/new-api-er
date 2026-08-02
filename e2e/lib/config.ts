export const DEFAULT_BASE =
  process.env.E2E_DEFAULT_BASE_URL ?? 'http://127.0.0.1:5177'
export const CLASSIC_BASE =
  process.env.E2E_CLASSIC_BASE_URL ?? 'http://127.0.0.1:5173'

export type Role = 'root' | 'admin' | 'common' | 'employee' | 'guest'

export const CREDS: Record<Exclude<Role, 'guest'>, { username: string; password: string }> = {
  root: { username: 'e2e_root', password: 'Test1234!' },
  admin: { username: 'e2e_admin', password: 'Test1234!' },
  common: { username: 'e2e_common', password: 'Test1234!' },
  employee: { username: 'e2e_emp', password: 'Test1234!' },
}

export const ROLES: Role[] = ['guest', 'common', 'employee', 'admin', 'root']

// storageState file path per ui/role
export function statePath(ui: string, role: Role) {
  return `./.auth/${ui}-${role}.json`
}

// ---- DEFAULT UI routes (TanStack file router) ----
export const DEFAULT_PUBLIC = [
  '/',
  '/sign-in',
  '/sign-up',
  '/register',
  '/forgot-password',
  '/reset',
  '/otp',
  '/pricing',
  '/rankings',
  '/about',
  '/privacy-policy',
  '/user-agreement',
  '/401',
  '/403',
  '/404',
  '/500',
  '/503',
]
export const DEFAULT_AUTHED = [
  '/dashboard',
  '/playground',
  '/keys',
  '/usage-logs',
  '/request-logs',
  '/profile',
  '/wallet',
  '/security',
  '/subscriptions',
  '/commission',
  '/commission-overview',
  '/customer-console',
  '/marketplace',
  '/console/topup',
  '/console/log',
  // admin-oriented
  '/channels',
  '/users',
  '/models',
  '/node-pool',
  '/employees',
  '/customers',
  '/redemption-codes',
  '/system-settings',
  '/system-info',
]
// pages that must be blocked for a common user (admin-only)
export const DEFAULT_ADMIN_ONLY = [
  '/channels',
  '/users',
  '/models',
  '/node-pool',
  '/employees',
  '/customers',
  '/redemption-codes',
  '/system-settings',
  '/system-info',
]

// ---- CLASSIC UI routes ----
export const CLASSIC_PUBLIC = [
  '/',
  '/login',
  '/register',
  '/reset',
  '/pricing',
  '/about',
  '/privacy-policy',
  '/user-agreement',
  '/forbidden',
  '/chat2link',
]
export const CLASSIC_AUTHED = [
  '/console',
  '/console/playground',
  '/console/token',
  '/console/log',
  '/console/request-log',
  '/console/midjourney',
  '/console/task',
  '/console/topup',
  '/console/personal',
  '/console/subscription',
  '/console/commission',
  '/console/commission-overview',
  '/console/customer-console',
  '/console/chat',
  // admin-oriented
  '/console/channel',
  '/console/user',
  '/console/setting',
  '/console/redemption',
  '/console/models',
  '/console/deployment',
  '/console/node-pool',
  '/console/employees',
]
export const CLASSIC_ADMIN_ONLY = [
  '/console/channel',
  '/console/user',
  '/console/setting',
  '/console/redemption',
  '/console/models',
  '/console/deployment',
  '/console/node-pool',
  '/console/employees',
]
