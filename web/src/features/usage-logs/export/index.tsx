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
import { getRouteApi } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { canViewAdminMenu } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { EmployeeTemplateManager } from './components/employee-template-manager'
import { ExportTaskList } from './components/export-task-list'
import {
  NewExportSheet,
  type NewExportPrefill,
} from './components/new-export-sheet'
import { QuotaExportSheet } from './components/quota-export-sheet'
import { SelfExportSheet } from './components/self-export-sheet'
import { quotaExportSummary } from './quota-export'
import {
  mergeQuotaTaskListFilters,
  quotaTaskListFilters,
} from './task-list-filters'
import type { AnomalyFilters, ExportJobListFilters } from './types'

const route = getRouteApi('/_authenticated/usage-logs/$section')

/** Search params carry epoch milliseconds; tolerate ISO strings too. */
function toDate(value?: number | string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

export function LogExportCenter(props: { self?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [sheetOpen, setSheetOpen] = useState(false)
  const [maxRangeSec, setMaxRangeSec] = useState<number>()
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const filters: ExportJobListFilters = search.exportFilters ?? {}
  const anomalyPrefill = useMemo<AnomalyFilters | undefined>(() => {
    const filters: AnomalyFilters = {}
    if (search.anomalyUsageSource) {
      filters.usage_source = [search.anomalyUsageSource]
    }
    if (search.anomalyEndReason) {
      filters.stream_end_reason = [search.anomalyEndReason]
    }
    if (search.anomalyMinRetry) {
      filters.min_retry_count = search.anomalyMinRetry
    }
    return Object.keys(filters).length ? filters : undefined
  }, [
    search.anomalyUsageSource,
    search.anomalyEndReason,
    search.anomalyMinRetry,
  ])

  const prefill = useMemo<NewExportPrefill>(
    () => ({
      start: toDate(search.startTime),
      end: toDate(search.endTime),
      logType: Array.isArray(search.type) ? search.type[0] : search.type,
      model: search.model || undefined,
      username: search.username || undefined,
      token: search.token || undefined,
      channel: search.channel || undefined,
      group: search.group || undefined,
      // 「查同类」跳转：带着该行可观察的特征进来，直接落进异常排查模板。
      anomaly: anomalyPrefill,
      templateId: anomalyPrefill ? 'builtin:anomaly' : undefined,
    }),
    [search, anomalyPrefill]
  )

  const active = props.self ? 'usage' : (search.exportSource ?? 'usage')
  useEffect(() => setSheetOpen(false), [active, props.self])
  let scope: 'quota_admin' | 'usage_self' | 'usage_admin' = 'usage_admin'
  let apiBase = '/api/log/export'
  if (active === 'quota') {
    scope = 'quota_admin'
    apiBase = '/api/data/quota/export'
  } else if (props.self) {
    scope = 'usage_self'
    apiBase = '/api/log/self/export'
  }
  const listFilters =
    active === 'quota' ? quotaTaskListFilters(filters) : filters
  return (
    <>
      <ExportTaskList
        responsive
        toolbarLeading={
          !props.self && (
            <ToggleGroup
              value={[active]}
              aria-label={`${t('Usage Logs')} / ${t('Quota Data')}`}
              variant='outline'
              size='sm'
              onValueChange={(value) => {
                const next = value.find((item) => item !== active)
                if (next !== 'usage' && next !== 'quota') return
                void navigate({
                  search: (previous) => ({ ...previous, exportSource: next }),
                })
                setSheetOpen(false)
              }}
            >
              <ToggleGroupItem value='usage'>{t('Usage Logs')}</ToggleGroupItem>
              <ToggleGroupItem value='quota'>{t('Quota Data')}</ToggleGroupItem>
            </ToggleGroup>
          )
        }
        key={`${user?.id}:${scope}`}
        dataSource={active}
        scope={scope}
        userId={user?.id}
        self={props.self}
        apiBase={apiBase}
        filters={listFilters}
        summary={
          active === 'quota'
            ? (job) => quotaExportSummary(job.quota_filters)
            : undefined
        }
        onApplyFilters={(value) => {
          const next =
            active === 'quota'
              ? mergeQuotaTaskListFilters(filters, value)
              : value
          void navigate({
            search: (previous) => ({
              ...previous,
              exportFilters: Object.keys(next).length ? next : undefined,
            }),
            replace: true,
          })
        }}
        actions={(data, unavailable) => (
          <>
            <Button
              type='button'
              size='sm'
              disabled={unavailable}
              onClick={() => {
                setMaxRangeSec(data?.max_range_sec)
                setSheetOpen(true)
              }}
            >
              <Plus data-icon='inline-start' />
              {t('New Export')}
            </Button>
            {active === 'usage' &&
              !props.self &&
              canViewAdminMenu(user, 'employees') && (
                <EmployeeTemplateManager />
              )}
          </>
        )}
      />
      {props.self && sheetOpen && (
        <SelfExportSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          maxRangeSec={maxRangeSec}
        />
      )}
      {!props.self && active === 'quota' && sheetOpen && (
        <QuotaExportSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          maxRangeSec={maxRangeSec}
        />
      )}
      {!props.self && active !== 'quota' && (
        <NewExportSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          prefill={prefill}
        />
      )}
    </>
  )
}
