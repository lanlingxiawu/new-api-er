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
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import { getDatabasePoolRuntimeStatus, updateSystemOptionGroup } from '../api'
import { SettingsSection } from '../components/settings-section'
import type { DatabasePoolStats, SystemTuningSettings } from '../types'

type ConfigGroupModule =
  | 'rate_limit_setting'
  | 'db_pool_setting'
  | 'user_session_setting'
type ConfigGroupValues = Record<string, string | number | boolean>

type ConfigGroupField = {
  key: string
  label: string
  type?: 'switch'
  min?: number
  max?: number
}

type ConfigGroupSectionProps = {
  title: string
  description?: string
  module: ConfigGroupModule
  fields: ConfigGroupField[]
  defaults: ConfigGroupValues
  statusContent?: ReactNode
}

/**
 * These groups are saved through `PUT /api/option/group` instead of the per-key
 * endpoint because the backend validates the whole group at once — a window
 * without its request count, or idle connections above open connections, can
 * only be rejected when both fields arrive together.
 */
function ConfigGroupSection({
  title,
  description,
  module,
  fields,
  defaults,
  statusContent,
}: ConfigGroupSectionProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [values, setValues] = useState(defaults)

  useEffect(() => setValues(defaults), [defaults])

  const mutation = useMutation({
    mutationFn: updateSystemOptionGroup,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message)
        return
      }
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      if (module === 'db_pool_setting') {
        queryClient.invalidateQueries({ queryKey: ['database-pool-stats'] })
      }
      if (response.data?.applied === false) {
        toast.warning(
          t('Saved, but this node could not fully apply the settings')
        )
        return
      }
      toast.success(t('Setting updated successfully'))
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const save = () =>
    mutation.mutate({
      module,
      values: Object.fromEntries(
        fields.map(({ key }) => [key, String(values[key])])
      ),
    })

  return (
    <SettingsSection title={t(title)}>
      <div className='space-y-4'>
        {statusContent}
        {description && (
          <p className='text-muted-foreground text-sm'>{t(description)}</p>
        )}
        <div className='grid gap-4 md:grid-cols-3'>
          {fields.map((field) => {
            const id = `${module}-${field.key}`
            return (
              <div className='space-y-2' key={field.key}>
                <Label htmlFor={id}>{t(field.label)}</Label>
                {field.type === 'switch' ? (
                  <Switch
                    id={id}
                    checked={Boolean(values[field.key])}
                    onCheckedChange={(checked) =>
                      setValues((current) => ({
                        ...current,
                        [field.key]: checked,
                      }))
                    }
                  />
                ) : (
                  <Input
                    id={id}
                    type='number'
                    min={field.min}
                    max={field.max}
                    value={Number(values[field.key])}
                    onChange={(event) =>
                      setValues((current) => ({
                        ...current,
                        [field.key]: Number(event.target.value),
                      }))
                    }
                  />
                )}
              </div>
            )
          })}
        </div>
        <div className='flex justify-end'>
          <Button disabled={mutation.isPending} onClick={save}>
            {mutation.isPending ? t('Saving...') : t('Save')}
          </Button>
        </div>
      </div>
    </SettingsSection>
  )
}

// Bounds mirror ValidateRateLimitSetting / ValidateDBPoolSetting so out-of-range
// values are caught by the input before a request is sent.
const rateLimitFields: ConfigGroupField[] = [
  { key: 'global_api_enabled', label: 'Global API enabled', type: 'switch' },
  { key: 'global_api_num', label: 'Global API requests', min: 1, max: 100000 },
  {
    key: 'global_api_duration_sec',
    label: 'Global API window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'global_api_user_enabled',
    label: 'Per-user API enabled',
    type: 'switch',
  },
  {
    key: 'global_api_user_num',
    label: 'Per-user API requests',
    min: 1,
    max: 100000,
  },
  {
    key: 'global_api_user_duration_sec',
    label: 'Per-user API window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'global_web_enabled',
    label: 'Web asset limit enabled',
    type: 'switch',
  },
  { key: 'global_web_num', label: 'Web asset requests', min: 1, max: 100000 },
  {
    key: 'global_web_duration_sec',
    label: 'Web asset window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'critical_enabled',
    label: 'Critical action limit enabled',
    type: 'switch',
  },
  {
    key: 'critical_num',
    label: 'Critical action requests',
    min: 1,
    max: 100000,
  },
  {
    key: 'critical_duration_sec',
    label: 'Critical action window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'auth_refresh_enabled',
    label: 'Session refresh limit enabled',
    type: 'switch',
  },
  {
    key: 'auth_refresh_num',
    label: 'Session refresh requests',
    min: 1,
    max: 100000,
  },
  {
    key: 'auth_refresh_ip_num',
    label: 'Refresh requests without session',
    min: 1,
    max: 100000,
  },
  {
    key: 'auth_refresh_duration_sec',
    label: 'Session refresh window (seconds)',
    min: 1,
    max: 1200,
  },
  { key: 'search_enabled', label: 'Search limit enabled', type: 'switch' },
  { key: 'search_num', label: 'Search requests', min: 1, max: 100000 },
  {
    key: 'search_duration_sec',
    label: 'Search window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'log_export_enabled',
    label: 'Log export limit enabled',
    type: 'switch',
  },
  { key: 'log_export_num', label: 'Log export requests', min: 1, max: 10000 },
  {
    key: 'log_export_duration_sec',
    label: 'Log export window (seconds)',
    min: 1,
    max: 1200,
  },
  {
    key: 'redis_timeout_ms',
    label: 'Redis lookup timeout (milliseconds)',
    min: 5,
    max: 5000,
  },
]

const dbPoolFields: ConfigGroupField[] = [
  {
    key: 'max_idle_conns',
    label: 'Main database idle connections',
    min: 1,
    max: 10000,
  },
  {
    key: 'max_open_conns',
    label: 'Main database open connections',
    min: 1,
    max: 100000,
  },
  {
    key: 'max_lifetime_sec',
    label: 'Connection lifetime (seconds)',
    min: 1,
    max: 86400,
  },
  {
    key: 'log_max_idle_conns',
    label: 'Log database idle connections (0 inherits)',
    min: 0,
    max: 10000,
  },
  {
    key: 'log_max_open_conns',
    label: 'Log database open connections (0 inherits)',
    min: 0,
    max: 100000,
  },
]

const userSessionFields: ConfigGroupField[] = [
  {
    key: 'active_limit',
    label: 'Active sessions per user',
    min: 1,
    max: 10000,
  },
  {
    key: 'issuance_limit',
    label: 'Session issuances per user',
    min: 1,
    max: 100000,
  },
  {
    key: 'issuance_window_sec',
    label: 'Session issuance window (seconds)',
    min: 1,
    max: 31536000,
  },
  {
    key: 'revoked_retention_days',
    label: 'Revoked session retention (days)',
    min: 1,
    max: 365,
  },
  {
    key: 'hourly_alert_threshold',
    label: 'Hourly session issuance alert threshold',
    min: 1,
    max: 10000000,
  },
]

function groupDefaults(
  settings: SystemTuningSettings,
  module: ConfigGroupModule,
  fields: ConfigGroupField[]
): ConfigGroupValues {
  const source = settings as unknown as ConfigGroupValues
  return Object.fromEntries(
    fields.map(({ key }) => [key, source[`${module}.${key}`]])
  )
}

export function RateLimitHotConfigSection({
  settings,
}: {
  settings: SystemTuningSettings
}) {
  return (
    <ConfigGroupSection
      title='Gateway Rate Limiting'
      description='For example: admin API requests, page assets, login and payment actions, session refresh, token or log searches, and log exports.'
      module='rate_limit_setting'
      fields={rateLimitFields}
      defaults={groupDefaults(settings, 'rate_limit_setting', rateLimitFields)}
    />
  )
}

export function DBPoolHotConfigSection({
  settings,
}: {
  settings: SystemTuningSettings
}) {
  const { t } = useTranslation()
  const statusQuery = useQuery({
    queryKey: ['database-pool-stats'],
    queryFn: getDatabasePoolRuntimeStatus,
  })
  const status = statusQuery.data?.success ? statusQuery.data.data : undefined
  const unavailable = statusQuery.isError || statusQuery.data?.success === false

  return (
    <ConfigGroupSection
      title='Database Connection Pool'
      module='db_pool_setting'
      fields={dbPoolFields}
      defaults={groupDefaults(settings, 'db_pool_setting', dbPoolFields)}
      statusContent={
        <div className='flex flex-col gap-3 pb-2'>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <p className='text-sm font-medium'>{t('Runtime status')}</p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Current application node snapshot, not database server totals'
                )}
              </p>
            </div>
            <Button
              variant='outline'
              size='sm'
              disabled={statusQuery.isFetching}
              onClick={() => statusQuery.refetch()}
            >
              {statusQuery.isFetching ? t('Refreshing...') : t('Refresh')}
            </Button>
          </div>

          {statusQuery.isPending && (
            <p className='text-muted-foreground text-sm'>
              {t('Loading connection pool status...')}
            </p>
          )}
          {unavailable && (
            <p className='text-destructive text-sm'>
              {t(
                'Connection pool status is temporarily unavailable. Try refreshing again.'
              )}
            </p>
          )}
          {status && (
            <>
              <div className='grid gap-3 lg:grid-cols-2'>
                <DatabasePoolStatsCard
                  title={t('Main database')}
                  stats={status.main}
                />
                {status.log && (
                  <DatabasePoolStatsCard
                    title={t('Log database')}
                    stats={status.log}
                  />
                )}
              </div>
              {status.log_reuses_main && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The log database uses the main database connection pool.'
                  )}
                </p>
              )}
              <p className='text-muted-foreground text-xs'>
                {t('Sampled at')}:{' '}
                {new Date(status.sampled_at).toLocaleString()}
              </p>
            </>
          )}
        </div>
      }
    />
  )
}

function DatabasePoolStatsCard({
  title,
  stats,
}: {
  title: string
  stats: DatabasePoolStats
}) {
  const { t } = useTranslation()
  const usage =
    stats.max_open_connections > 0
      ? `${Math.round((stats.in_use / stats.max_open_connections) * 100)}%`
      : t('Unlimited')
  const rows = [
    [t('Connections in use'), stats.in_use.toLocaleString()],
    [t('Open connections'), stats.open_connections.toLocaleString()],
    [t('Idle connections'), stats.idle.toLocaleString()],
    [
      t('Maximum open connections'),
      stats.max_open_connections || t('Unlimited'),
    ],
    [t('Pool usage'), usage],
    [t('Cumulative wait count'), stats.wait_count.toLocaleString()],
    [
      t('Cumulative wait duration'),
      `${stats.wait_duration_ms.toLocaleString()} ms`,
    ],
    [t('Closed by idle limit'), stats.max_idle_closed.toLocaleString()],
    [t('Closed by idle timeout'), stats.max_idle_time_closed.toLocaleString()],
    [t('Closed by lifetime limit'), stats.max_lifetime_closed.toLocaleString()],
  ]

  return (
    <div className='bg-muted/20 flex flex-col gap-3 rounded-lg border p-4 text-sm'>
      <p className='font-medium'>{title}</p>
      <div className='flex flex-col gap-2'>
        {rows.map(([label, value]) => (
          <div className='flex items-center justify-between gap-4' key={label}>
            <span className='text-muted-foreground'>{label}</span>
            <span className='text-right font-medium tabular-nums'>{value}</span>
          </div>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Wait and closed connection values are cumulative since this process started.'
        )}
      </p>
    </div>
  )
}

export function UserSessionHotConfigSection({
  settings,
}: {
  settings: SystemTuningSettings
}) {
  return (
    <ConfigGroupSection
      title='Login Session Policy'
      module='user_session_setting'
      fields={userSessionFields}
      defaults={groupDefaults(
        settings,
        'user_session_setting',
        userSessionFields
      )}
    />
  )
}
