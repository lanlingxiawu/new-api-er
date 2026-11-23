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
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getUserProfile } from '@/features/profile/api'
import {
  ADMIN_PERMISSION_RESOURCES,
  ADMIN_PERMISSION_ACTIONS,
  hasPermission,
} from '@/lib/admin-permissions'
import type { AuditSearch, AuditScope } from '@/lib/audit-navigation'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { AuditFilters } from './api'
import { AuditLogViewer } from './components/audit-log-viewer'
import { EmployeeCustomerAudit } from './components/employee-customer-audit'
import { SecurityMonitor } from './security/monitor'
import { SelfSecurity } from './self'

export function AuditLogs(
  props: {
    search?: AuditSearch
    pendingVisit?: string
    onSearchChange?: (search: AuditSearch) => void
  } = {}
) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const [localSearch, setLocalSearch] = useState<AuditSearch>({})
  const search = props.search ?? localSearch
  const onSearchChange = props.onSearchChange
  const changeSearch = useCallback(
    (next: AuditSearch) => {
      setLocalSearch(next)
      onSearchChange?.(next)
    },
    [onSearchChange]
  )
  const scope = search.scope ?? 'all'
  const setScope = useCallback(
    (scope: AuditScope) => changeSearch({ ...search, scope }),
    [search, changeSearch]
  )
  const [accessRevoked, setAccessRevoked] = useState(false)
  const [investigation, setInvestigation] = useState<Partial<AuditFilters>>()
  const canReadAll =
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(
      user,
      ADMIN_PERMISSION_RESOURCES.AUDIT,
      ADMIN_PERMISSION_ACTIONS.READ
    )
  const canReadSecurity =
    canReadAll && !!user && hasPermission(user, 'audit_security', 'read')
  const canReadCustomers = !!user?.is_employee
  let effectiveScope = scope
  if (scope === 'customers') {
    effectiveScope = canReadCustomers ? 'customers' : 'self'
  } else if (!canReadAll || accessRevoked) {
    effectiveScope = scope === 'my-security' ? 'my-security' : 'self'
  } else if (scope === 'security' && !canReadSecurity) {
    effectiveScope = 'all'
  }
  const userId = user?.id
  const handleAccessDenied = useCallback(async () => {
    setAccessRevoked(true)
    setScope('self')
    const current = useAuthStore.getState().auth.user
    if (current && current.id === userId) {
      useAuthStore.getState().auth.setUser({
        ...current,
        permissions: {
          ...current.permissions,
          admin_permissions: {
            ...current.permissions?.admin_permissions,
            audit: { read: false },
          },
        },
      })
    }
    await queryClient.cancelQueries({ queryKey: ['audit', userId, 'all'] })
    queryClient.removeQueries({ queryKey: ['audit', userId, 'all'] })
    try {
      const profile = await getUserProfile()
      const latest = useAuthStore.getState().auth.user
      if (
        profile.success &&
        profile.data &&
        latest &&
        profile.data.id === userId &&
        latest.id === userId
      ) {
        useAuthStore.getState().auth.setUser({
          ...latest,
          role: profile.data.role,
          permissions: profile.data.permissions,
        })
      }
    } catch {
      // Keep the denied scope closed when refreshing capabilities fails.
    }
  }, [queryClient, userId, setScope])
  let content
  if (effectiveScope === 'customers') {
    content = (
      <EmployeeCustomerAudit
        key={`${userId}:${user?.role}:${user?.is_employee}:${props.pendingVisit ?? ''}`}
        tab={search.tab ?? 'audit'}
        riskView={
          search.view === 'unresolved' || search.view === 'handled'
            ? search.view
            : 'id'
        }
        onTabChange={(tab) =>
          changeSearch({ ...search, scope: 'customers', tab })
        }
        onRiskViewChange={(view) =>
          changeSearch({ ...search, scope: 'customers', tab: 'risk', view })
        }
      />
    )
  } else if (effectiveScope === 'my-security') {
    content = <SelfSecurity key={userId} userID={userId ?? 0} />
  } else if (effectiveScope === 'security') {
    content = (
      <SecurityMonitor
        key={`${userId}:${props.pendingVisit ?? ''}:${search.view ?? 'id'}`}
        initialView={search.view}
        onViewChange={(view) =>
          changeSearch({ ...search, scope: 'security', view })
        }
        onInvestigate={(event) => {
          setInvestigation({
            request_id: event.request_id || undefined,
            ip: event.request_id ? undefined : event.ip,
            start_timestamp: Math.max(1, event.at - 900),
            end_timestamp: event.at + 900,
          })
          setScope('all')
        }}
      />
    )
  } else {
    content = (
      <AuditLogViewer
        key={`${userId}:${user?.role}:${user?.is_employee}:${effectiveScope}`}
        scope={effectiveScope}
        ownDetail={
          effectiveScope === 'self' &&
          !accessRevoked &&
          !!user &&
          user.role >= ROLE.SUPER_ADMIN &&
          canReadAll
        }
        initialFilters={effectiveScope === 'all' ? investigation : undefined}
        onAccessDenied={handleAccessDenied}
      />
    )
  }
  return (
    <SectionPageLayout fixedContent stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Audit Logs')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Tabs
          className='max-w-full min-w-0'
          value={effectiveScope}
          onValueChange={(value) => {
            if (value === 'customers' && canReadCustomers) {
              setScope('customers')
              return
            }
            if (value === 'my-security') {
              setScope('my-security')
              return
            }
            if (value === 'security' && canReadSecurity) {
              setScope('security')
              return
            }
            setScope(value === 'all' && canReadAll ? 'all' : 'self')
          }}
        >
          <TabsList
            aria-label={t('View scope')}
            className='max-w-full flex-wrap group-data-horizontal/tabs:h-auto'
          >
            {canReadAll && !accessRevoked && (
              <TabsTrigger value='all'>{t('All')}</TabsTrigger>
            )}
            <TabsTrigger value='self'>{t('Only Mine')}</TabsTrigger>
            {canReadCustomers && (
              <TabsTrigger value='customers'>{t('My Customers')}</TabsTrigger>
            )}
            <TabsTrigger value='my-security'>{t('My security')}</TabsTrigger>
            {canReadSecurity && !accessRevoked && (
              <TabsTrigger value='security'>
                {t('Security monitoring')}
              </TabsTrigger>
            )}
          </TabsList>
        </Tabs>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-3'>
          {accessRevoked && effectiveScope === 'self' && (
            <p role='status' className='text-muted-foreground shrink-0 text-xs'>
              {t('Audit access changed. Showing only your records.')}
            </p>
          )}
          <div className='min-h-0 flex-1'>{content}</div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
