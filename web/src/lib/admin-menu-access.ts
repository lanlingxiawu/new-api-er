import { redirect } from '@tanstack/react-router'

import { canViewAdminMenu } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const ADMIN_MENU_IDS = {
  CHANNELS: 'channels',
  MODELS: 'models',
  USERS: 'users',
  REDEMPTION_CODES: 'redemption_codes',
  SUBSCRIPTIONS: 'subscriptions',
  EMPLOYEES: 'employees',
  BUSINESS_OVERVIEW: 'business_overview',
  REQUEST_LOGS: 'request_logs',
  SYSTEM_INFO: 'system_info',
} as const

export type AdminMenuId = (typeof ADMIN_MENU_IDS)[keyof typeof ADMIN_MENU_IDS]

export function requireAdminMenu(menu: AdminMenuId) {
  const user = useAuthStore.getState().auth.user
  if (!user || user.role < ROLE.ADMIN || !canViewAdminMenu(user, menu)) {
    throw redirect({ to: '/403' })
  }
}

const ADMIN_MENU_BY_URL: Array<{ prefix: string; menu: AdminMenuId }> = [
  { prefix: '/channels', menu: ADMIN_MENU_IDS.CHANNELS },
  { prefix: '/models', menu: ADMIN_MENU_IDS.MODELS },
  { prefix: '/users', menu: ADMIN_MENU_IDS.USERS },
  { prefix: '/redemption-codes', menu: ADMIN_MENU_IDS.REDEMPTION_CODES },
  { prefix: '/subscriptions', menu: ADMIN_MENU_IDS.SUBSCRIPTIONS },
  { prefix: '/employees', menu: ADMIN_MENU_IDS.EMPLOYEES },
  { prefix: '/commission-overview', menu: ADMIN_MENU_IDS.BUSINESS_OVERVIEW },
  { prefix: '/request-logs', menu: ADMIN_MENU_IDS.REQUEST_LOGS },
  { prefix: '/system-info', menu: ADMIN_MENU_IDS.SYSTEM_INFO },
]

export function adminMenuFromUrl(url: string): AdminMenuId | null {
  return (
    ADMIN_MENU_BY_URL.find(
      ({ prefix }) => url === prefix || url.startsWith(`${prefix}/`)
    )?.menu ?? null
  )
}
