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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { ChevronDown, Download } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getUserProfile } from '@/features/profile/api'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import { getExportJobs } from '../../export/api'
import { ExportTaskList } from '../../export/components/export-task-list'
import { AUDIT_ACTIVITY_VERSION, type AuditFilters } from '../api'
import {
  auditExportBase,
  auditExportSnapshot,
  createAuditExport,
  type AuditExportFilters,
} from './api'
import { auditFilterSummary } from './filter-summary'

type Props = {
  scope: 'all' | 'self'
  params: AuditFilters
  canQuery: boolean
  onAccessDenied?: () => Promise<void>
}

export function AuditExportButton(props: Props) {
  const user = useAuthStore((state) => state.auth.user)
  return (
    <ScopedAuditExportButton
      key={`${user?.id}:${user?.role}:${props.scope}:${JSON.stringify(user?.permissions)}`}
      {...props}
    />
  )
}

function ScopedAuditExportButton(props: Props) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed =
    props.scope === 'self' || (!!user && hasPermission(user, 'audit', 'export'))
  const [snapshot, setSnapshot] = useState<AuditExportFilters>()
  const [initialTab, setInitialTab] = useState<'new' | 'jobs'>('new')
  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='sm'
        disabled={!allowed}
        title={
          !allowed
            ? t('Audit export permission has not been granted.')
            : undefined
        }
        onClick={() => {
          setInitialTab(
            props.canQuery && !(props.scope === 'self' && props.params.ip_risk)
              ? 'new'
              : 'jobs'
          )
          // Keep the risk filter visible while allowing access to existing jobs.
          setSnapshot({
            ...auditExportSnapshot(
              { ...props.params, ip_risk: undefined },
              props.scope
            ),
            ip_risk: props.params.ip_risk,
          })
        }}
      >
        <Download data-icon='inline-start' />
        {t('Export')}
      </Button>
      {!allowed && (
        <span className='text-muted-foreground text-xs' role='status'>
          {t('Audit export permission has not been granted.')}
        </span>
      )}
      {snapshot && (
        <AuditExportPanel
          {...props}
          snapshot={snapshot}
          initialTab={initialTab}
          onClose={() => setSnapshot(undefined)}
        />
      )}
    </>
  )
}

function AuditExportPanel(
  props: Props & {
    snapshot: AuditExportFilters
    initialTab: 'new' | 'jobs'
    onClose: () => void
  }
) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const scope = props.scope === 'self' ? 'audit_self' : 'audit_admin'
  const apiBase = auditExportBase(props.scope)
  const prefix = ['log-export-jobs', 'audit', scope, user?.id]
  const [tab, setTab] = useState<string>(props.initialTab)
  const [denied, setDenied] = useState(false)
  const deniedHandled = useRef(false)
  const [filters, setFilters] = useState(props.snapshot)
  const [options, setOptions] = useState({
    header: true,
    csv_bom: true,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  })
  const capability = useQuery({
    queryKey: [...prefix, 'capabilities'],
    queryFn: () =>
      getExportJobs(false, props.scope === 'self', {}, '', apiBase),
    staleTime: 0,
    enabled: !denied,
    retry: false,
  })
  const deniedCallback = props.onAccessDenied
  const userId = user?.id
  const handleDenied = useCallback(async () => {
    if (deniedHandled.current) return
    deniedHandled.current = true
    setDenied(true)
    const queryKey = ['log-export-jobs', 'audit', scope, userId]
    await queryClient.cancelQueries({ queryKey })
    queryClient.removeQueries({ queryKey })
    try {
      const profile = await getUserProfile()
      const current = useAuthStore.getState().auth.user
      if (
        profile.success &&
        profile.data &&
        current &&
        current.id === userId &&
        profile.data.id === userId
      ) {
        useAuthStore.getState().auth.setUser({
          ...current,
          role: profile.data.role,
          permissions: profile.data.permissions,
        })
        if (
          props.scope === 'all' &&
          !hasPermission(profile.data, 'audit', 'read')
        ) {
          await deniedCallback?.()
        }
      }
    } catch {
      // Keep export contents closed if the current permissions cannot be read.
    }
  }, [queryClient, scope, userId, props.scope, deniedCallback])
  useEffect(() => {
    if (
      isAxiosError(capability.error) &&
      capability.error.response?.status === 403
    ) {
      void handleDenied()
    }
  }, [capability.error, handleDenied])
  useEffect(
    () => () => {
      const queryKey = ['log-export-jobs', 'audit', scope, user?.id]
      void queryClient.cancelQueries({ queryKey })
      queryClient.removeQueries({ queryKey })
    },
    [queryClient, scope, user?.id]
  )
  const create = useMutation({
    mutationFn: () => createAuditExport(props.scope, filters, options),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: prefix })
      setTab('jobs')
    },
    onError: (error) => {
      if (isAxiosError(error) && error.response?.status === 403) {
        void handleDenied()
      } else toast.error(error.message || t('Export failed'))
    },
  })
  const start = filters.start_timestamp
  const end = filters.end_timestamp
  const maxRange = capability.data?.max_range_sec
  const validRange =
    start !== undefined &&
    end !== undefined &&
    start > 0 &&
    end >= start &&
    maxRange !== undefined &&
    end - start <= maxRange
  const riskAllowed =
    !filters.ip_risk ||
    (props.scope === 'all' &&
      !!user &&
      hasPermission(user, 'audit_security', 'read') &&
      hasPermission(user, 'audit_security', 'export') &&
      capability.data?.risk_export_enabled)
  const valid =
    validRange &&
    riskAllowed &&
    capability.data?.enabled &&
    !capability.isFetching &&
    !capability.error &&
    !denied
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Export audit logs')}
      contentClassName='max-h-[90dvh] overflow-y-hidden sm:w-[calc(100%-3rem)] sm:max-w-5xl'
      contentHeight='min(60dvh, 26rem)'
      scrollAreaClassName='overflow-y-hidden lg:h-[min(44dvh,20rem)]'
      bodyClassName='flex h-full min-h-0 flex-col gap-4'
      footerClassName='flex-row justify-end sm:py-4'
      footer={
        tab === 'new' ? (
          <>
            <Button variant='outline' onClick={props.onClose}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={!valid || create.isPending}
              aria-busy={create.isPending}
              onClick={() => {
                if (valid && !create.isPending) create.mutate()
              }}
            >
              {create.isPending ? t('Creating...') : t('Start export')}
            </Button>
          </>
        ) : (
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        )
      }
    >
      <Tabs value={tab} onValueChange={setTab} className='shrink-0'>
        <TabsList aria-label={t('Export options')}>
          <TabsTrigger value='new'>{t('New Export')}</TabsTrigger>
          <TabsTrigger value='jobs'>{t('Export history')}</TabsTrigger>
        </TabsList>
      </Tabs>
      <div className='flex min-h-0 min-w-0 flex-1 [scrollbar-gutter:stable] flex-col gap-4 overflow-y-auto overscroll-contain'>
        {denied && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Export access changed. Review your permissions before exporting again.'
            )}
          </p>
        )}
        {!denied && tab === 'jobs' && (
          <ExportTaskList
            responsive
            dataSource='audit'
            scope={scope}
            apiBase={apiBase}
            userId={user?.id}
            self={props.scope === 'self'}
            onAccessDenied={handleDenied}
            summary={(job) =>
              props.scope === 'self' &&
              job.audit_projection_version !== AUDIT_ACTIVITY_VERSION
                ? t('This export uses an older format. Create a new export.')
                : auditFilterSummary(job.audit_filters ?? {}, t, props.scope)
            }
          />
        )}
        {!denied && tab === 'new' && (
          <>
            <Field>
              <FieldLabel>{t('Date Range')}</FieldLabel>
              <CompactDateTimeRangePicker
                start={start ? new Date(start * 1000) : undefined}
                end={end ? new Date(end * 1000) : undefined}
                maxRangeSec={maxRange}
                disabled={create.isPending}
                onChange={(range) =>
                  setFilters({
                    ...filters,
                    start_timestamp: range.start
                      ? Math.floor(range.start.getTime() / 1000)
                      : undefined,
                    end_timestamp: range.end
                      ? Math.floor(range.end.getTime() / 1000)
                      : undefined,
                  })
                }
              />
            </Field>
            {maxRange !== undefined && !validRange && (
              <p role='alert' className='text-destructive text-sm'>
                {t('The time range cannot exceed {{days}} days', {
                  days: maxRange / 86400,
                })}
              </p>
            )}
            <Field>
              <FieldLabel>{t('Export filters')}</FieldLabel>
              <p className='text-muted-foreground text-sm break-words'>
                {auditFilterSummary(props.snapshot, t, props.scope)}
              </p>
            </Field>
            <Collapsible className='space-y-4'>
              <CollapsibleTrigger
                render={
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='group'
                  />
                }
              >
                {t('More options')}
                <ChevronDown
                  aria-hidden
                  className='size-3.5 group-data-[panel-open]:rotate-180'
                />
              </CollapsibleTrigger>
              <CollapsibleContent className='space-y-4'>
                <Field>
                  <FieldLabel htmlFor='audit-export-timezone'>
                    {t('Timezone')}
                  </FieldLabel>
                  <Input
                    id='audit-export-timezone'
                    value={options.timezone}
                    disabled={create.isPending}
                    onChange={(event) =>
                      setOptions({ ...options, timezone: event.target.value })
                    }
                  />
                </Field>
                <FieldLabel>
                  <Checkbox
                    checked={options.header}
                    disabled={create.isPending}
                    onCheckedChange={(checked) =>
                      setOptions({ ...options, header: checked === true })
                    }
                  />
                  {t('Include header row')}
                </FieldLabel>
                <FieldLabel>
                  <Checkbox
                    checked={options.csv_bom}
                    disabled={create.isPending}
                    onCheckedChange={(checked) =>
                      setOptions({ ...options, csv_bom: checked === true })
                    }
                  />
                  {t('Excel compatible')}
                </FieldLabel>
              </CollapsibleContent>
            </Collapsible>
            {!riskAllowed && (
              <p role='status' className='text-muted-foreground text-sm'>
                {t('Risk export requires security export permission.')}
              </p>
            )}
            {capability.data?.enabled === false && (
              <p role='status' className='text-muted-foreground text-sm'>
                {t('Log export is currently turned off')}
              </p>
            )}
            {capability.error && (
              <div className='space-y-2'>
                <p role='alert' className='text-destructive text-sm'>
                  {t('Unable to load export settings. Try again.')}
                </p>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={capability.isFetching}
                  onClick={() => void capability.refetch()}
                >
                  {t('Retry')}
                </Button>
              </div>
            )}
            {denied && (
              <p role='alert' className='text-destructive text-sm'>
                {t(
                  'Export access changed. Review your permissions before exporting again.'
                )}
              </p>
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}
