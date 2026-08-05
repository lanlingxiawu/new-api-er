/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useLocation } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { resolveSidebarView } from '@/components/layout/lib/sidebar-view-registry'
import type { NavGroup, ResolvedSidebarView } from '@/components/layout/types'
import {
  firstVisibleSettingsSection,
  scopeFromSystemSettingsUrl,
} from '@/features/system-settings/access'
import { adminMenuFromUrl } from '@/lib/admin-menu-access'
import {
  canViewAdminMenu,
  canViewSystemSettingsScope,
} from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarConfig } from './use-sidebar-config'
import { useSidebarData } from './use-sidebar-data'

/** Sentinel key used for the root navigation in animation `key=` props */
const ROOT_VIEW_KEY = '__root'

/**
 * Resolve the active sidebar view for the current location.
 *
 * - Returns the matching nested {@link SidebarView} (with its nav
 *   groups) when the URL belongs to a registered drill-in workspace.
 * - Otherwise returns the root navigation, narrowed by:
 *     · admin-only group visibility (role-based);
 *     · `useSidebarConfig` (admin × user `sidebar_modules` overlay).
 *
 * Nested views are intentionally NOT passed through `useSidebarConfig`
 * — those filters target known dashboard URLs only, and gating is
 * already enforced at the route level (`beforeLoad` redirects).
 */
export function useSidebarView(): ResolvedSidebarView {
  const { t } = useTranslation()
  const pathname = useLocation({ select: (l) => l.pathname })
  const userRole = useAuthStore((s) => s.auth.user?.role)
  const user = useAuthStore((s) => s.auth.user)
  const rootSidebarData = useSidebarData()
  const configFilteredRoot = useSidebarConfig(rootSidebarData.navGroups)

  const rootNavGroups = useMemo<NavGroup[]>(() => {
    const role = userRole ?? ROLE.GUEST
    const isAdmin = role >= ROLE.ADMIN
    return configFilteredRoot
      .filter((group) => (group.id === 'admin' ? isAdmin : true))
      .map((group) => {
        const firstVisibleSettings = firstVisibleSettingsSection(user)
        const items = group.items
          .filter((item) => {
            const menu = adminMenuFromUrl(String(item.url))
            return (
              (item.requiredRole === undefined || role >= item.requiredRole) &&
              (!menu || canViewAdminMenu(user, menu)) &&
              (item.url !== '/system-settings/site' || firstVisibleSettings)
            )
          })
          .map((item) =>
            item.url === '/system-settings/site' && firstVisibleSettings
              ? {
                  ...item,
                  url: `/system-settings/${firstVisibleSettings.group}/${firstVisibleSettings.section}`,
                }
              : item
          )
        return { ...group, items }
      })
  }, [configFilteredRoot, user, userRole])

  const view = resolveSidebarView(pathname)

  if (view) {
    const navGroups = view.getNavGroups(t)
    const visibleNavGroups =
      view.id === 'system-settings'
        ? navGroups
            .map((group) => ({
              ...group,
              items: group.items
                .map((item) => {
                  if (!item.items) return item
                  const items = item.items.filter((child) => {
                    const scope = scopeFromSystemSettingsUrl(String(child.url))
                    return scope
                      ? canViewSystemSettingsScope(user, scope)
                      : false
                  })
                  return { ...item, items }
                })
                .filter((item) => !item.items || item.items.length > 0),
            }))
            .filter((group) => group.items.length > 0)
        : navGroups
    return {
      key: view.id,
      view,
      navGroups: visibleNavGroups,
    }
  }

  return {
    key: ROOT_VIEW_KEY,
    view: null,
    navGroups: rootNavGroups,
  }
}
