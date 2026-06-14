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
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
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
import { getTierResetConfig, triggerTierReset } from '../api'

const timezoneOptions = [
  { value: 'Asia/Shanghai', labelKey: 'China Timezone' },
  { value: 'Local', labelKey: 'Server Timezone' },
]

const resetDayOptions = Array.from({ length: 31 }, (_, index) => index + 1)

function normalizeTimezone(timezone?: string) {
  return timezone === 'Local' ? 'Local' : 'Asia/Shanghai'
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
    resetDay: 10,
    resetHour: 0,
    resetMinute: 0,
    resetSecond: 0,
    timezone: 'Asia/Shanghai',
  })

  const { data, isLoading } = useQuery({
    queryKey: ['commission-tier-reset-config'],
    queryFn: getTierResetConfig,
  })
  const config = data?.data

  useEffect(() => {
    if (!config) return
    setForm({
      enabled: config.enabled,
      resetDay: config.reset_day,
      resetHour: config.reset_hour,
      resetMinute: config.reset_minute,
      resetSecond: config.reset_second,
      timezone: normalizeTimezone(config.timezone),
    })
  }, [config])

  const isDirty =
    !!config &&
    (form.enabled !== config.enabled ||
      form.resetDay !== config.reset_day ||
      form.resetHour !== config.reset_hour ||
      form.resetMinute !== config.reset_minute ||
      form.resetSecond !== config.reset_second ||
      form.timezone !== normalizeTimezone(config.timezone))

  const saveMutation = useMutation({
    mutationFn: async () => {
      if (!config) return
      const updates: Array<{ key: string; value: string }> = []
      if (form.enabled !== config.enabled) {
        updates.push({
          key: 'commission_tier_reset_setting.enabled',
          value: String(form.enabled),
        })
      }
      if (form.resetDay !== config.reset_day) {
        updates.push({
          key: 'commission_tier_reset_setting.reset_day',
          value: String(form.resetDay),
        })
      }
      if (form.resetHour !== config.reset_hour) {
        updates.push({
          key: 'commission_tier_reset_setting.reset_hour',
          value: String(form.resetHour),
        })
      }
      if (form.resetMinute !== config.reset_minute) {
        updates.push({
          key: 'commission_tier_reset_setting.reset_minute',
          value: String(form.resetMinute),
        })
      }
      if (form.resetSecond !== config.reset_second) {
        updates.push({
          key: 'commission_tier_reset_setting.reset_second',
          value: String(form.resetSecond),
        })
      }
      if (form.timezone !== normalizeTimezone(config.timezone)) {
        updates.push({
          key: 'commission_tier_reset_setting.timezone',
          value: form.timezone,
        })
      }
      for (const update of updates) {
        const res = await updateSystemOption(update)
        if (!res.success) throw new Error(res.message)
      }
    },
    onSuccess: () => {
      toast.success(t('Setting updated successfully'))
      qc.invalidateQueries({ queryKey: ['commission-tier-reset-config'] })
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
                  disabled={isLoading}
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
              <div className='grid grid-cols-3 gap-2'>
                <div className='space-y-1.5'>
                  <Label>{t('Hour')}</Label>
                  <Input
                    type='number'
                    min={0}
                    max={23}
                    value={form.resetHour}
                    onChange={(event) => {
                      const value = Number(event.target.value)
                      if (Number.isFinite(value)) {
                        setForm((f) => ({
                          ...f,
                          resetHour: Math.min(23, Math.max(0, value)),
                        }))
                      }
                    }}
                    disabled={isLoading}
                  />
                </div>
                <div className='space-y-1.5'>
                  <Label>{t('Minute')}</Label>
                  <Input
                    type='number'
                    min={0}
                    max={59}
                    value={form.resetMinute}
                    onChange={(event) => {
                      const value = Number(event.target.value)
                      if (Number.isFinite(value)) {
                        setForm((f) => ({
                          ...f,
                          resetMinute: Math.min(59, Math.max(0, value)),
                        }))
                      }
                    }}
                    disabled={isLoading}
                  />
                </div>
                <div className='space-y-1.5'>
                  <Label>{t('Second')}</Label>
                  <Input
                    type='number'
                    min={0}
                    max={59}
                    value={form.resetSecond}
                    onChange={(event) => {
                      const value = Number(event.target.value)
                      if (Number.isFinite(value)) {
                        setForm((f) => ({
                          ...f,
                          resetSecond: Math.min(59, Math.max(0, value)),
                        }))
                      }
                    }}
                    disabled={isLoading}
                  />
                </div>
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
