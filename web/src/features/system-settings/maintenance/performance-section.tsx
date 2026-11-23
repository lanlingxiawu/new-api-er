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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ProfilingControls } from '@/features/profiling/components/profiling-controls'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { updateSystemOptionGroup } from '../api'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { useSettingsPageAccess } from '../components/settings-page-access-context'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { CacheGuidance } from './cache-guidance'
import { PerformanceOverview } from './performance-overview'
import { formatBytes, type PerformanceStats } from './performance-stats'

/**
 * IMPORTANT: react-hook-form 7 interprets dotted `name` strings as nested
 * paths. If we declare the schema with literal flat keys like
 * `'performance_setting.disk_cache_enabled'`, the form state diverges from
 * what zod validates and saves silently turn into no-ops. So we model the
 * form internally with proper nested objects and only flatten back to the
 * server-side key format right before persisting.
 */
const perfSchema = z.object({
  performance_setting: z.object({
    body_governance_enabled: z.boolean(),
    body_memory_budget_mb: z.coerce.number().int().min(0),
    disk_cache_enabled: z.boolean(),
    disk_cache_threshold_mb: z.coerce.number().min(1),
    disk_cache_max_size_mb: z.coerce.number().min(100),
    disk_cache_path: z.string(),
    monitor_enabled: z.boolean(),
    monitor_cpu_threshold: z.coerce.number().min(0),
    monitor_memory_threshold: z.coerce.number().min(0).max(100),
    monitor_disk_threshold: z.coerce.number().min(0).max(100),
  }),
})

type PerfFormInput = z.input<typeof perfSchema>
type PerfFormValues = z.output<typeof perfSchema>

type FlatPerfDefaults = {
  'performance_setting.body_governance_enabled': boolean
  'performance_setting.body_memory_budget_mb': number
  'performance_setting.disk_cache_enabled': boolean
  'performance_setting.disk_cache_threshold_mb': number
  'performance_setting.disk_cache_max_size_mb': number
  'performance_setting.disk_cache_path': string
  'performance_setting.monitor_enabled': boolean
  'performance_setting.monitor_cpu_threshold': number
  'performance_setting.monitor_memory_threshold': number
  'performance_setting.monitor_disk_threshold': number
}

const buildFormDefaults = (defaults: FlatPerfDefaults): PerfFormInput => ({
  performance_setting: {
    body_governance_enabled:
      defaults['performance_setting.body_governance_enabled'],
    body_memory_budget_mb:
      defaults['performance_setting.body_memory_budget_mb'],
    disk_cache_enabled: defaults['performance_setting.disk_cache_enabled'],
    disk_cache_threshold_mb:
      defaults['performance_setting.disk_cache_threshold_mb'],
    disk_cache_max_size_mb:
      defaults['performance_setting.disk_cache_max_size_mb'],
    disk_cache_path: defaults['performance_setting.disk_cache_path'] ?? '',
    monitor_enabled: defaults['performance_setting.monitor_enabled'],
    monitor_cpu_threshold:
      defaults['performance_setting.monitor_cpu_threshold'],
    monitor_memory_threshold:
      defaults['performance_setting.monitor_memory_threshold'],
    monitor_disk_threshold:
      defaults['performance_setting.monitor_disk_threshold'],
  },
})

const normalizeFormValues = (values: PerfFormValues): FlatPerfDefaults => ({
  'performance_setting.body_governance_enabled':
    values.performance_setting.body_governance_enabled,
  'performance_setting.body_memory_budget_mb':
    values.performance_setting.body_memory_budget_mb,
  'performance_setting.disk_cache_enabled':
    values.performance_setting.disk_cache_enabled,
  'performance_setting.disk_cache_threshold_mb':
    values.performance_setting.disk_cache_threshold_mb,
  'performance_setting.disk_cache_max_size_mb':
    values.performance_setting.disk_cache_max_size_mb,
  'performance_setting.disk_cache_path':
    values.performance_setting.disk_cache_path ?? '',
  'performance_setting.monitor_enabled':
    values.performance_setting.monitor_enabled,
  'performance_setting.monitor_cpu_threshold':
    values.performance_setting.monitor_cpu_threshold,
  'performance_setting.monitor_memory_threshold':
    values.performance_setting.monitor_memory_threshold,
  'performance_setting.monitor_disk_threshold':
    values.performance_setting.monitor_disk_threshold,
})

interface Props {
  defaultValues: FlatPerfDefaults
}

export function PerformanceSection(props: Props) {
  const { t } = useTranslation()
  const { scope, canEdit } = useSettingsPageAccess()
  const queryClient = useQueryClient()
  const updateOption = useMutation({
    mutationFn: (values: Record<string, string>) =>
      updateSystemOptionGroup({ module: 'performance_setting', values, scope }),
  })
  const requestSaveConfirmation = useSettingsSaveConfirmation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [activeTab, setActiveTab] = useState('overview')
  const [diagnosticsVisited, setDiagnosticsVisited] = useState(false)
  const [resetOpen, setResetOpen] = useState(false)
  const [maintenancePending, setMaintenancePending] = useState<
    'cleanup' | 'reset' | 'gc' | null
  >(null)
  const maintenanceInFlight = useRef(false)
  const [statsLoading, setStatsLoading] = useState(false)
  const [cleanupOpen, setCleanupOpen] = useState(false)
  const [cleanupPending, setCleanupPending] = useState(false)
  const [statsFailed, setStatsFailed] = useState(false)
  const [receivedAt, setReceivedAt] = useState(0)
  const statsRequest = useRef(0)
  const [stats, setStats] = useState<PerformanceStats | null>(null)

  const formDefaults = useMemo(
    () => buildFormDefaults(props.defaultValues),
    [props.defaultValues]
  )

  const form = useForm<PerfFormInput, unknown, PerfFormValues>({
    resolver: zodResolver(perfSchema),
    defaultValues: formDefaults,
  })

  const baselineRef = useRef<FlatPerfDefaults>(props.defaultValues)
  const baselineSerializedRef = useRef<string>(
    JSON.stringify(props.defaultValues)
  )

  useResetForm(form, buildFormDefaults(props.defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(props.defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = props.defaultValues
    baselineSerializedRef.current = serialized
  }, [props.defaultValues, form])

  const fetchStats = useCallback(async () => {
    const startedAt = Date.now()
    const request = ++statsRequest.current
    setStatsLoading(true)
    try {
      const res = await api.get('/api/performance/stats', { timeout: 10000 })
      if (request !== statsRequest.current) return
      if (!res.data.success) throw new Error('stats unavailable')
      setStats(res.data.data)
      setReceivedAt(startedAt)
      setStatsFailed(false)
    } catch {
      if (request === statsRequest.current) setStatsFailed(true)
    } finally {
      if (request === statsRequest.current) setStatsLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchStats()
  }, [fetchStats])

  const onSubmit = async (values: PerfFormValues) => {
    if (!canEdit) return
    if (
      values.performance_setting.body_governance_enabled &&
      values.performance_setting.body_memory_budget_mb <= 0
    ) {
      toast.error(t('Set a positive memory budget before enabling protection.'))
      return
    }
    const normalized = normalizeFormValues(values)
    const changedKeys = (
      Object.keys(normalized) as Array<keyof FlatPerfDefaults>
    ).filter((key) => normalized[key] !== baselineRef.current[key])

    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    await requestSaveConfirmation(async () => {
      try {
        const response = await updateOption.mutateAsync(
          Object.fromEntries(
            changedKeys.map((key) => [
              key.replace('performance_setting.', ''),
              String(normalized[key]),
            ])
          )
        )
        if (!response.success) {
          toast.error(t('Save failed'))
          return
        }
        if (response.data?.applied === false) {
          toast.warning(
            t('Saved, but this node could not fully apply the settings')
          )
          return
        }
      } catch {
        toast.error(t('Save failed'))
        return
      }
      toast.success(t('Setting updated successfully'))
      void queryClient.invalidateQueries({
        queryKey: ['system-options', scope],
      })

      baselineRef.current = normalized
      baselineSerializedRef.current = JSON.stringify(normalized)
      form.reset(buildFormDefaults(normalized))
      fetchStats()
    })
  }

  const clearDiskCache = async () => {
    if (!canEdit || maintenanceInFlight.current) return
    maintenanceInFlight.current = true
    setMaintenancePending('cleanup')
    setCleanupPending(true)
    try {
      const res = await api.delete('/api/performance/disk_cache')
      if (res.data.success) {
        toast.success(t('Disk cache cleared'))
        setCleanupOpen(false)
        fetchStats()
      } else {
        handleServerError(res.data)
      }
    } catch (error) {
      handleServerError(error, t('Cleanup failed'))
    } finally {
      maintenanceInFlight.current = false
      setMaintenancePending(null)
      setCleanupPending(false)
    }
  }

  const resetStats = async () => {
    if (!canEdit || maintenanceInFlight.current) return
    maintenanceInFlight.current = true
    setMaintenancePending('reset')
    try {
      const res = await api.post('/api/performance/reset_stats')
      if (res.data.success) {
        toast.success(t('Statistics reset'))
        setResetOpen(false)
        fetchStats()
      } else {
        handleServerError(res.data)
      }
    } catch (error) {
      handleServerError(error, t('Reset failed'))
    } finally {
      maintenanceInFlight.current = false
      setMaintenancePending(null)
    }
  }

  const forceGC = async () => {
    if (!canEdit || maintenanceInFlight.current) return
    maintenanceInFlight.current = true
    setMaintenancePending('gc')
    try {
      const res = await api.post('/api/performance/gc')
      if (res.data.success) {
        toast.success(t('GC executed'))
        fetchStats()
      } else {
        handleServerError(res.data)
      }
    } catch (error) {
      handleServerError(error, t('GC execution failed'))
    } finally {
      maintenanceInFlight.current = false
      setMaintenancePending(null)
    }
  }

  const diskEnabled = form.watch('performance_setting.disk_cache_enabled')
  const monitorEnabled = form.watch('performance_setting.monitor_enabled')
  const maxCacheSizeRaw = form.watch(
    'performance_setting.disk_cache_max_size_mb'
  )
  const maxCacheSizeMb =
    typeof maxCacheSizeRaw === 'number'
      ? maxCacheSizeRaw
      : Number(maxCacheSizeRaw) || 0

  const lowDiskSpace =
    diskEnabled &&
    stats?.disk_space_info &&
    stats.disk_space_info.free > 0 &&
    maxCacheSizeMb > 0 &&
    stats.disk_space_info.free < maxCacheSizeMb * 1024 * 1024

  return (
    <SettingsSection
      title={t('Performance Settings')}
      className='w-full min-w-0'
    >
      <Tabs
        value={activeTab}
        onValueChange={(value) => {
          setActiveTab(String(value))
          if (value === 'diagnostics') setDiagnosticsVisited(true)
        }}
        className='gap-5'
      >
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='max-w-full min-w-0 overflow-x-auto overflow-y-hidden pb-1'>
            <TabsList className='min-h-9 w-max'>
              <TabsTrigger value='overview'>
                {t('Runtime overview')}
              </TabsTrigger>
              <TabsTrigger value='configuration'>
                {t('Cache & protection')}
              </TabsTrigger>
              <TabsTrigger value='diagnostics'>
                {t('Diagnostics & maintenance')}
              </TabsTrigger>
            </TabsList>
          </div>
          {form.formState.isDirty && (
            <span className='text-muted-foreground text-xs'>
              {t('Unsaved changes')}
            </span>
          )}
        </div>
        <TabsContent value='overview' className='space-y-4'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <p className='text-muted-foreground text-xs' role='status'>
              {receivedAt > 0
                ? t('Updated at {{time}}', {
                    time: new Date(receivedAt).toLocaleTimeString(),
                  })
                : t('Performance Monitor')}
            </p>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={fetchStats}
              disabled={statsLoading}
            >
              <RefreshCw
                className={statsLoading ? 'animate-spin' : undefined}
              />
              {t('Refresh Stats')}
            </Button>
          </div>
          {statsFailed && stats && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t('Refresh failed. Showing the last available data.')}
              </AlertDescription>
            </Alert>
          )}
          {!stats && statsFailed && (
            <ErrorState
              title={t('Failed to load performance data')}
              onRetry={fetchStats}
            />
          )}
          {!stats && !statsFailed && (
            <div
              className='grid gap-4 lg:grid-cols-2'
              aria-label={t('Loading...')}
            >
              <Skeleton className='h-64' />
              <Skeleton className='h-64' />
              <Skeleton className='h-48' />
              <Skeleton className='h-48' />
            </div>
          )}
          {stats && <PerformanceOverview stats={stats} />}
        </TabsContent>
        <TabsContent
          value='configuration'
          keepMounted
          className='w-full min-w-0'
        >
          <Form {...form}>
            <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
              {activeTab === 'configuration' && (
                <SettingsPageFormActions
                  onSave={form.handleSubmit(onSubmit)}
                  isSaving={
                    updateOption.isPending || form.formState.isSubmitting
                  }
                />
              )}
              <fieldset
                disabled={!canEdit || updateOption.isPending}
                className='grid min-w-0 items-start gap-4 xl:grid-cols-2'
              >
                <section className='min-w-0 space-y-5 rounded-lg border p-4 sm:p-5'>
                  {/* Disk Cache Settings */}
                  <div>
                    <h4 className='font-medium'>{t('Disk Cache Settings')}</h4>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        'Request bodies are kept for forwarding and retries, then released when requests finish. This is not model prompt caching and does not change upstream cache-token billing.'
                      )}
                    </p>
                  </div>

                  <div className='grid grid-cols-1 items-start gap-x-6 gap-y-4 md:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='performance_setting.body_governance_enabled'
                      render={({ field }) => (
                        <SettingsSwitchItem className='self-start'>
                          <SettingsSwitchContent>
                            <FormLabel>
                              {t('Enable body cache protection')}
                            </FormLabel>
                          </SettingsSwitchContent>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              onCheckedChange={field.onChange}
                              disabled={!canEdit}
                            />
                          </FormControl>
                        </SettingsSwitchItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='performance_setting.body_memory_budget_mb'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Body memory budget (MiB)')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={0}
                              step={1}
                              {...safeNumberFieldProps(field)}
                              disabled={!canEdit}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Limits reserved body-cache buffers, not total process memory. When capacity is exhausted, new allocations may be rejected; active requests are not cleared.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                  <div className='grid grid-cols-1 items-start gap-x-6 gap-y-4 md:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='performance_setting.disk_cache_enabled'
                      render={({ field }) => (
                        <SettingsSwitchItem className='md:col-span-2'>
                          <SettingsSwitchContent>
                            <FormLabel>{t('Enable Disk Cache')}</FormLabel>
                          </SettingsSwitchContent>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              onCheckedChange={field.onChange}
                              disabled={!canEdit}
                            />
                          </FormControl>
                        </SettingsSwitchItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='performance_setting.disk_cache_threshold_mb'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t('Disk cache threshold (MiB)')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={1}
                              step={1}
                              {...safeNumberFieldProps(field)}
                              disabled={!canEdit || !diskEnabled}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Bodies reaching this size use disk. Lower values reduce retained memory but increase disk I/O. Units are MiB (1024 KiB).'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='performance_setting.disk_cache_max_size_mb'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Disk cache budget (MiB)')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={100}
                              step={1}
                              {...safeNumberFieldProps(field)}
                              disabled={!canEdit || !diskEnabled}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Leave space for databases, logs and files left by previous processes.'
                            )}
                          </FormDescription>
                          {stats?.disk_space_info &&
                            stats.disk_space_info.total > 0 && (
                              <FormDescription>
                                {t('Free: {{free}} / Total: {{total}}', {
                                  free: formatBytes(stats.disk_space_info.free),
                                  total: formatBytes(
                                    stats.disk_space_info.total
                                  ),
                                })}
                              </FormDescription>
                            )}
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>

                  {lowDiskSpace && (
                    <Alert variant='destructive'>
                      <AlertDescription>
                        {`${t('Warning')}: ${t('Available disk space')} (${formatBytes(stats?.disk_space_info?.free ?? 0)}) ${t('is less than the configured maximum cache size')} (${maxCacheSizeMb} MB). ${t('This may cause cache failures.')}`}
                      </AlertDescription>
                    </Alert>
                  )}

                  {!stats?.config?.is_running_in_container && (
                    <FormField
                      control={form.control}
                      name='performance_setting.disk_cache_path'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Cache Directory')}</FormLabel>
                          <FormControl>
                            <Input
                              placeholder={t(
                                'Leave empty to use system temp directory'
                              )}
                              value={field.value ?? ''}
                              onChange={(event) =>
                                field.onChange(event.target.value)
                              }
                              name={field.name}
                              onBlur={field.onBlur}
                              ref={field.ref}
                              disabled={!canEdit || !diskEnabled}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  )}

                  <p className='text-muted-foreground text-xs leading-relaxed'>
                    {t(
                      'With disk cache enabled, memory allocation failures can spill to disk. If the read buffer or disk capacity is unavailable, the request is rejected.'
                    )}
                  </p>
                </section>
                <div className='min-w-0 space-y-4'>
                  <section className='min-w-0 space-y-5 rounded-lg border p-4 sm:p-5'>
                    {/* System Performance Monitor */}
                    <div>
                      <h4 className='font-medium'>
                        {t('System Performance Monitoring')}
                      </h4>
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {t(
                          'When performance monitoring is enabled and system resource usage exceeds the set threshold, new Relay requests will be rejected.'
                        )}
                      </p>
                    </div>

                    <div className='grid grid-cols-1 items-start gap-x-6 gap-y-4 md:grid-cols-3'>
                      <FormField
                        control={form.control}
                        name='performance_setting.monitor_enabled'
                        render={({ field }) => (
                          <SettingsSwitchItem className='md:col-span-3'>
                            <SettingsSwitchContent>
                              <FormLabel>
                                {t('Enable Performance Monitoring')}
                              </FormLabel>
                            </SettingsSwitchContent>
                            <FormControl>
                              <Switch
                                checked={field.value}
                                onCheckedChange={field.onChange}
                                disabled={!canEdit}
                              />
                            </FormControl>
                          </SettingsSwitchItem>
                        )}
                      />
                      <FormField
                        control={form.control}
                        name='performance_setting.monitor_cpu_threshold'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('CPU Threshold (%)')}</FormLabel>
                            <FormControl>
                              <Input
                                type='number'
                                min={0}
                                step={1}
                                {...safeNumberFieldProps(field)}
                                disabled={!canEdit || !monitorEnabled}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={form.control}
                        name='performance_setting.monitor_memory_threshold'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Memory Threshold (%)')}</FormLabel>
                            <FormControl>
                              <Input
                                type='number'
                                min={0}
                                max={100}
                                step={1}
                                {...safeNumberFieldProps(field)}
                                disabled={!canEdit || !monitorEnabled}
                              />
                            </FormControl>
                            <FormDescription>
                              {t(
                                'System memory usage threshold, not the body-cache budget. This does not guarantee prevention of OOM.'
                              )}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={form.control}
                        name='performance_setting.monitor_disk_threshold'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Disk Threshold (%)')}</FormLabel>
                            <FormControl>
                              <Input
                                type='number'
                                min={0}
                                max={100}
                                step={1}
                                {...safeNumberFieldProps(field)}
                                disabled={!canEdit || !monitorEnabled}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </div>
                  </section>
                  <CacheGuidance
                    guidance={stats?.guidance}
                    receivedAt={receivedAt}
                    loading={statsLoading}
                    failed={statsFailed}
                    canEdit={canEdit}
                    pathChanged={
                      form.watch('performance_setting.disk_cache_path') !==
                      (stats?.config?.disk_cache_path ??
                        baselineRef.current[
                          'performance_setting.disk_cache_path'
                        ])
                    }
                    onRefresh={fetchStats}
                    onApply={(kind, value) => {
                      const fields = {
                        memory: 'body_memory_budget_mb',
                        disk: 'disk_cache_max_size_mb',
                        threshold: 'disk_cache_threshold_mb',
                      } as const
                      form.setValue(
                        `performance_setting.${fields[kind]}`,
                        value,
                        {
                          shouldDirty: true,
                          shouldValidate: true,
                        }
                      )
                    }}
                  />
                </div>
              </fieldset>
            </SettingsForm>
          </Form>
        </TabsContent>
        <TabsContent
          value='diagnostics'
          keepMounted
          className={
            isRoot
              ? 'grid w-full min-w-0 items-start gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]'
              : 'w-full min-w-0'
          }
        >
          {isRoot && diagnosticsVisited && (
            <section className='min-w-0 rounded-lg border p-4 sm:p-5'>
              <ProfilingControls active={activeTab === 'diagnostics'} />
            </section>
          )}
          <div className='min-w-0 space-y-4 rounded-lg border p-4 sm:p-5'>
            <h4 className='text-sm font-medium'>{t('Maintenance')}</h4>
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!canEdit || !!maintenancePending}
                onClick={() => setCleanupOpen(true)}
              >
                {t('Clean up inactive cache')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!canEdit || !!maintenancePending}
                onClick={() => setResetOpen(true)}
              >
                {t('Reset cache hit counters')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!canEdit || !!maintenancePending}
                onClick={forceGC}
              >
                {maintenancePending === 'gc' && <Spinner />}
                {t('Run GC')}
              </Button>
            </div>
          </div>
        </TabsContent>
      </Tabs>
      <ConfirmDialog
        open={cleanupOpen}
        onOpenChange={setCleanupOpen}
        title={t('Confirm cleanup of inactive disk cache?')}
        desc={t(
          'Only closed cache files owned by this process are cleaned. Active files and files left by earlier processes are not deleted.'
        )}
        confirmText={t('Confirm')}
        destructive
        isLoading={cleanupPending}
        disabled={!canEdit}
        handleConfirm={clearDiskCache}
      />
      <ConfirmDialog
        open={resetOpen}
        onOpenChange={setResetOpen}
        title={t('Reset cache hit counters')}
        desc={t(
          'Reset memory and disk cache hit counts for this process. Cached data and other runtime statistics are kept.'
        )}
        confirmText={t('Reset')}
        isLoading={maintenancePending === 'reset'}
        disabled={!canEdit}
        handleConfirm={resetStats}
      />
    </SettingsSection>
  )
}
