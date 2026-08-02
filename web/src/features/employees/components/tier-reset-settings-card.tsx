import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RotateCcw, Settings2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { updateSystemOption } from '@/features/system-settings/api'
import {
  getTierResetConfig,
  switchCommissionPeriod,
  triggerTierReset,
} from '../api'

const timezoneOptions = [
  { value: 'Asia/Shanghai', labelKey: 'China Timezone' },
  { value: 'Local', labelKey: 'Server Timezone' },
]

const periodModeOptions = [
  { value: 'reset_day', labelKey: 'By reset day' },
  { value: 'natural_month', labelKey: 'By natural month' },
] as const

type PeriodMode = (typeof periodModeOptions)[number]['value']

const resetDayOptions = Array.from({ length: 31 }, (_, index) => index + 1)

function normalizeTimezone(timezone?: string) {
  return timezone === 'Local' ? 'Local' : 'Asia/Shanghai'
}

function normalizePeriodMode(mode?: string): PeriodMode {
  return mode === 'natural_month' ? 'natural_month' : 'reset_day'
}

function formatTs(ts?: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

export function TierResetSettingsCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [form, setForm] = useState({
    enabled: false,
    periodMode: 'reset_day' as PeriodMode,
    resetDay: 10,
    timezone: 'Asia/Shanghai',
  })
  // 切换口径时的一次性动作选项（不持久化）：默认不重置等级、纳入本期数据。
  const [resetTiersOnSwitch, setResetTiersOnSwitch] = useState(false)
  const [includePeriodData, setIncludePeriodData] = useState(true)

  const { data, isLoading } = useQuery({
    queryKey: ['commission-tier-reset-config'],
    queryFn: getTierResetConfig,
  })
  const config = data?.data

  useEffect(() => {
    if (!config) return
    setForm({
      enabled: config.enabled,
      periodMode: normalizePeriodMode(config.period_mode),
      resetDay: config.reset_day,
      timezone: normalizeTimezone(config.timezone),
    })
  }, [config])

  const isNaturalMonth = form.periodMode === 'natural_month'

  // 影响周期边界的改动（统计方式 / 重置日 / 时区），决定是否需要"安全切换"迁移。
  const periodBoundaryChanged =
    !!config &&
    (form.periodMode !== normalizePeriodMode(config.period_mode) ||
      form.resetDay !== config.reset_day ||
      form.timezone !== normalizeTimezone(config.timezone))

  const isDirty =
    !!config &&
    (form.enabled !== config.enabled ||
      form.periodMode !== normalizePeriodMode(config.period_mode) ||
      form.resetDay !== config.reset_day ||
      form.timezone !== normalizeTimezone(config.timezone))

  const saveMutation = useMutation({
    mutationFn: async () => {
      if (!config) return
      const boundaryChanged =
        form.periodMode !== normalizePeriodMode(config.period_mode) ||
        form.resetDay !== config.reset_day ||
        form.timezone !== normalizeTimezone(config.timezone)
      const updates: Array<{ key: string; value: string }> = []
      if (form.enabled !== config.enabled) {
        updates.push({
          key: 'commission_tier_reset_setting.enabled',
          value: String(form.enabled),
        })
      }
      if (form.periodMode !== normalizePeriodMode(config.period_mode)) {
        updates.push({
          key: 'commission_tier_reset_setting.period_mode',
          value: form.periodMode,
        })
      }
      if (form.resetDay !== config.reset_day) {
        updates.push({
          key: 'commission_tier_reset_setting.reset_day',
          value: String(form.resetDay),
        })
      }
      if (form.timezone !== normalizeTimezone(config.timezone)) {
        updates.push({
          key: 'commission_tier_reset_setting.timezone',
          value: form.timezone,
        })
      }
      updates.push(
        { key: 'commission_tier_reset_setting.reset_hour', value: '0' },
        { key: 'commission_tier_reset_setting.reset_minute', value: '0' },
        { key: 'commission_tier_reset_setting.reset_second', value: '0' }
      )
      for (const update of updates) {
        const res = await updateSystemOption(update)
        if (!res.success) throw new Error(res.message)
      }
      // 口径变化时执行"安全切换"：把本期对齐到新边界，按两个开关分别控制等级与数据。
      if (boundaryChanged) {
        const res = await switchCommissionPeriod({
          reset_tiers: resetTiersOnSwitch,
          include_period_data: includePeriodData,
        })
        if (!res.success) throw new Error(res.message)
      }
    },
    onSuccess: () => {
      toast.success(t('Setting updated successfully'))
      qc.invalidateQueries({ queryKey: ['commission-tier-reset-config'] })
      qc.invalidateQueries({ queryKey: ['employees'] })
      setSettingsOpen(false)
    },
    onError: (error: unknown) => {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    },
  })

  const handleResetNow = async () => {
    setResetting(true)
    try {
      const res = await triggerTierReset()
      if (!res.success) throw new Error(res.message)
      toast.success(
        t('Reset completed, {{count}} employee(s) processed', {
          count: res.data?.processed ?? 0,
        })
      )
      qc.invalidateQueries({ queryKey: ['commission-tier-reset-config'] })
      qc.invalidateQueries({ queryKey: ['employees'] })
      setConfirmOpen(false)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setResetting(false)
    }
  }

  return (
    <>
      <Button
        type='button'
        size='sm'
        variant='outline'
        onClick={() => setSettingsOpen(true)}
        disabled={isLoading}
      >
        <Settings2 className='size-4' />
        {t('Monthly Reset')}
      </Button>

      <Dialog open={settingsOpen} onOpenChange={setSettingsOpen}>
        <DialogContent className='sm:max-w-[560px]'>
          <DialogHeader>
            <DialogTitle>{t('Monthly Tier & Performance Reset')}</DialogTitle>
          </DialogHeader>

          <div className='space-y-6 py-3'>
            <div className='flex items-center justify-between gap-4 pb-2'>
              <Label>{t('Enable monthly auto reset')}</Label>
              <Switch
                checked={form.enabled}
                onCheckedChange={(checked) =>
                  setForm((f) => ({ ...f, enabled: checked }))
                }
                disabled={isLoading}
              />
            </div>

            <div className='space-y-1.5'>
              <Label>{t('Statistics method')}</Label>
              <Select
                items={periodModeOptions.map((option) => ({
                  value: option.value,
                  label: t(option.labelKey),
                }))}
                value={form.periodMode}
                onValueChange={(value) => {
                  if (value)
                    {setForm((f) => ({
                      ...f,
                      periodMode: value as PeriodMode,
                    }))}
                }}
                disabled={isLoading}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {periodModeOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {t(option.labelKey)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <p className='text-muted-foreground text-xs'>
                {isNaturalMonth
                  ? t(
                      'Count each calendar month from the 1st to the last day; reset runs on the 1st of each month.'
                    )
                  : t(
                      'A period runs from the reset day to the next reset day; if the month has fewer days, the last day is used.'
                    )}
              </p>
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1.5'>
                <Label>{t('Monthly reset day')}</Label>
                <Select
                  items={resetDayOptions.map((day) => ({
                    value: String(day),
                    label: String(day),
                  }))}
                  value={String(form.resetDay)}
                  onValueChange={(value) => {
                    const day = Number(value)
                    if (Number.isFinite(day)) {
                      setForm((f) => ({ ...f, resetDay: day }))
                    }
                  }}
                  disabled={isLoading || isNaturalMonth}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {resetDayOptions.map((day) => (
                        <SelectItem key={day} value={String(day)}>
                          {day}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <div className='space-y-1.5'>
                <Label>{t('Timezone')}</Label>
                <Select
                  items={timezoneOptions.map((option) => ({
                    value: option.value,
                    label: t(option.labelKey),
                  }))}
                  value={form.timezone}
                  onValueChange={(value) => {
                    if (value) setForm((f) => ({ ...f, timezone: value }))
                  }}
                  disabled={isLoading}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {timezoneOptions.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {t(option.labelKey)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            </div>

            {periodBoundaryChanged && (
              <div className='bg-muted/40 space-y-3 rounded-md border p-3'>
                <div className='text-sm font-medium'>
                  {t('On applying this change')}
                </div>
                <label className='flex items-start gap-2.5'>
                  <Checkbox
                    checked={includePeriodData}
                    onCheckedChange={(checked) =>
                      setIncludePeriodData(checked === true)
                    }
                    disabled={isLoading}
                    className='mt-0.5'
                  />
                  <span className='text-sm'>
                    {t('Include current-period data')}
                    <span className='text-muted-foreground block text-xs'>
                      {t(
                        'Fold data that belongs to the new current period into it, so nothing looks lost.'
                      )}
                    </span>
                  </span>
                </label>
                <label className='flex items-start gap-2.5'>
                  <Checkbox
                    checked={resetTiersOnSwitch}
                    onCheckedChange={(checked) =>
                      setResetTiersOnSwitch(checked === true)
                    }
                    disabled={isLoading}
                    className='mt-0.5'
                  />
                  <span className='text-sm'>
                    {t('Reset employee tiers')}
                    <span className='text-muted-foreground block text-xs'>
                      {t(
                        'Reset all employees to the lowest tier of their group. Off keeps current tiers.'
                      )}
                    </span>
                  </span>
                </label>
              </div>
            )}

            <div className='text-muted-foreground grid gap-2 text-sm sm:grid-cols-2'>
              <div>
                {t('Last reset time')}: {formatTs(config?.last_reset_at)}
              </div>
              <div>
                {t('Next reset time')}: {formatTs(config?.next_reset_at)}
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              onClick={() => setConfirmOpen(true)}
            >
              <RotateCcw className='size-4' />
              {t('Reset Now')}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={() => setSettingsOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={() => saveMutation.mutate()}
              disabled={!isDirty || saveMutation.isPending}
            >
              {saveMutation.isPending ? t('Saving...') : t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Confirm reset now')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'Reset all employees to the lowest tier, clear current-period performance and commission, without affecting historical records, ledgers, pending or settled balances. This action cannot be undone.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={resetting}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              onClick={handleResetNow}
              disabled={resetting}
            >
              {resetting ? t('Saving...') : t('Reset Now')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
