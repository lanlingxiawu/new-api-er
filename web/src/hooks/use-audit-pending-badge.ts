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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import type { SidebarBadges } from '@/components/layout/types'
import type { ApiResponse } from '@/features/profile/types'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import {
  createServerError,
  getServerErrorStatus,
} from '@/lib/server-error-message'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

export function auditPendingScope(user: AuthUser | null) {
  if (
    user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(user, 'audit', 'read') &&
    hasPermission(user, 'audit_security', 'read')
  ) {
    return 'security'
  }
  return user?.is_employee ? 'customers' : undefined
}

export function useAuditPendingBadge(): SidebarBadges['auditPending'] {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const scope = auditPendingScope(user)
  const query = useQuery({
    queryKey:
      scope === 'security'
        ? [
            'audit-security',
            user?.id,
            user?.role,
            user?.permissions,
            'pending-count',
            sid,
          ]
        : [
            'customer-audit',
            user?.id,
            'pending-count',
            sid,
            user?.role,
            user?.is_employee,
          ],
    queryFn: async ({ signal }) => {
      const path =
        scope === 'security'
          ? '/api/audit/security/pending-count'
          : '/api/user/employee/customers/security/pending-count'
      const { data } = await api.get<ApiResponse<{ count: number }>>(path, {
        signal,
        // Query owns deduplication and cancellation. Reusing a cancelled
        // HTTP promise during a remount would suppress the initial badge.
        disableDuplicate: true,
      })
      const count = data.data?.count
      if (
        !data.success ||
        count == null ||
        !Number.isInteger(count) ||
        count < 0 ||
        count > 100
      ) {
        throw createServerError(data, t('Failed to load audit records'))
      }
      return count
    },
    enabled: (query) =>
      !!scope && !!sid && getServerErrorStatus(query.state.error) !== 403,
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false, errorRedirect: false },
  })
  if (!scope || !sid || query.isError || !query.data) return undefined
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const total =
    query.data >= 100
      ? t('At least {{total}}', { total: formatNumber(100, locale) })
      : formatNumber(query.data, locale)
  return {
    value: query.data >= 100 ? `${formatNumber(99, locale)}+` : total,
    label:
      scope === 'security'
        ? t('Unprocessed security alerts: {{total}}', { total })
        : t('Unprocessed customer risk records: {{total}}', { total }),
    search: {
      scope,
      tab: scope === 'customers' ? 'risk' : undefined,
      view: 'unresolved',
    },
    onNavigate: () => {
      void query.refetch({ cancelRefetch: false })
    },
  }
}
