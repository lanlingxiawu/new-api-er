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
import { Download, Loader2 } from 'lucide-react'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { useSettingsSaveConfirmation } from '@/features/system-settings/components/settings-save-confirmation'
import { useUpdateOption } from '@/features/system-settings/hooks/use-update-option'

import { downloadProfile, getPprofStatus } from '../api'

// CPU / trace 采样需要秒数；其余是即时快照。
const NEEDS_SECONDS = new Set(['profile', 'trace'])
const MAX_SECONDS = 120
// goroutine 文本变体的合成 key（?debug=2，输出可读全栈）
const GOROUTINE_TEXT_KEY = 'goroutine:text'

/**
 * profile 的可读名。按钮上显示它，原始名（pprof 的端点名）放在括号里保留：
 * 用户查 go tool pprof 文档、对照 URL 时靠的都是原始名，丢了就对不上号。
 */
const PROFILE_LABEL_KEYS: Record<string, string> = {
  profile: 'CPU sampling',
  heap: 'Heap memory',
  goroutine: 'Goroutine stacks',
  allocs: 'Cumulative allocations',
  block: 'Blocking',
  mutex: 'Mutex contention',
  threadcreate: 'Thread creation',
  trace: 'Execution trace',
}

/**
 * pprof 开关 + profile 下载。开关即时生效（单个布尔值，没必要攒着一起保存），
 * 下载按钮读的是服务端真实状态而不是表单值。
 */
export function ProfilingControls({ active = true }: { active?: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // Profiling is root-only, outside the delegated performance settings scope.
  const updateOption = useUpdateOption('')
  const requestSaveConfirmation = useSettingsSaveConfirmation()
  const [seconds, setSeconds] = useState('30')
  const [profile, setProfile] = useState('profile')
  const profileId = useId()
  const secondsId = useId()
  const downloadInFlight = useRef(false)
  const toggleInFlight = useRef(false)

  const statusQuery = useQuery({
    queryKey: ['profiling', 'status'],
    queryFn: getPprofStatus,
    enabled: active,
  })

  const status = statusQuery.data?.data
  const enabled = status?.enabled ?? false
  const profiles = (status?.profiles ?? []).flatMap((name) =>
    name === 'goroutine' ? [name, GOROUTINE_TEXT_KEY] : [name]
  )
  const selectedProfile = profiles.includes(profile)
    ? profile
    : (profiles[0] ?? '')
  const profileItems = profiles.map((value) => ({
    value,
    label:
      value === GOROUTINE_TEXT_KEY
        ? t('Goroutine text')
        : t(PROFILE_LABEL_KEYS[value] ?? value),
  }))

  const toggle = async (checked: boolean) => {
    if (
      !status ||
      statusQuery.isError ||
      checked === enabled ||
      toggleInFlight.current ||
      downloadInFlight.current
    ) {
      return
    }
    toggleInFlight.current = true
    try {
      await requestSaveConfirmation(async () => {
        await updateOption.mutateAsync({
          key: 'pprof_setting.enabled',
          value: checked,
        })
        await queryClient.invalidateQueries({
          queryKey: ['profiling', 'status'],
        })
      })
    } finally {
      toggleInFlight.current = false
    }
  }

  // key 用 profile 名，但 goroutine 文本变体用合成 key 'goroutine:text' 区分，
  // 这样每个按钮的 loading 状态互不干扰。
  const download = useMutation({
    mutationFn: (key: string) => {
      if (key === GOROUTINE_TEXT_KEY) {
        return downloadProfile('goroutine', { debug: 2 })
      }
      const value = NEEDS_SECONDS.has(key) ? Number(seconds) : undefined
      return downloadProfile(key, { seconds: value })
    },
    onError: (error: Error) => toast.error(error.message),
    onSettled: () => {
      downloadInFlight.current = false
    },
  })

  const startDownload = (key: string) => {
    if (
      !enabled ||
      statusQuery.isError ||
      !profiles.includes(key) ||
      downloadInFlight.current ||
      toggleInFlight.current
    ) {
      return
    }
    if (NEEDS_SECONDS.has(key)) {
      const value = Number(seconds)
      if (!Number.isInteger(value) || value <= 0 || value > MAX_SECONDS) {
        toast.error(
          t('Enter a whole number of seconds between 1 and {{max}}', {
            max: MAX_SECONDS,
          })
        )
        return
      }
    }
    downloadInFlight.current = true
    download.mutate(key)
  }

  return (
    <div className='space-y-4'>
      <div>
        <h4 className='font-medium'>{t('Profiling (pprof)')}</h4>
      </div>

      {statusQuery.isLoading && <Skeleton className='h-[104px] w-full' />}
      {!statusQuery.isLoading && statusQuery.isError && (
        <ErrorState
          className='min-h-36'
          title={t('Failed to load profiling status')}
          onRetry={() => void statusQuery.refetch()}
        />
      )}
      {!statusQuery.isLoading && !statusQuery.isError && (
        <>
          <SettingsSwitchField
            checked={enabled}
            disabled={updateOption.isPending || download.isPending || !status}
            onCheckedChange={toggle}
            label={t('Enable profiling downloads')}
            description={t(
              'A heap dump contains upstream keys and user tokens held in memory — keep this off unless you are actively investigating.'
            )}
          />

          <div className='space-y-2'>
            <div className='flex flex-col gap-3 sm:flex-row sm:items-end'>
              <Field className='min-w-0 sm:flex-1'>
                <FieldLabel htmlFor={profileId}>{t('Profile type')}</FieldLabel>
                <Select
                  items={profileItems}
                  value={selectedProfile}
                  onValueChange={(value) => {
                    if (value) setProfile(value)
                  }}
                  disabled={
                    !enabled || download.isPending || updateOption.isPending
                  }
                >
                  <SelectTrigger id={profileId}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {profileItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          <span>{item.label}</span>
                          <span className='text-muted-foreground font-mono text-xs'>
                            {item.value === GOROUTINE_TEXT_KEY
                              ? 'debug=2'
                              : item.value}
                          </span>
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              {NEEDS_SECONDS.has(selectedProfile) && (
                <Field className='sm:w-36'>
                  <FieldLabel htmlFor={secondsId}>
                    {t('Sampling seconds')}
                  </FieldLabel>
                  <Input
                    id={secondsId}
                    type='number'
                    min={1}
                    max={MAX_SECONDS}
                    step={1}
                    value={seconds}
                    onChange={(event) => setSeconds(event.target.value)}
                    disabled={
                      !enabled || download.isPending || updateOption.isPending
                    }
                  />
                </Field>
              )}
              <Button
                type='button'
                variant='outline'
                disabled={
                  !enabled ||
                  !selectedProfile ||
                  download.isPending ||
                  updateOption.isPending
                }
                onClick={() => startDownload(selectedProfile)}
                aria-busy={download.isPending}
              >
                {download.isPending ? (
                  <Loader2 className='animate-spin' />
                ) : (
                  <Download />
                )}
                {download.isPending
                  ? t('Preparing download...')
                  : t('Download')}
              </Button>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Analyse a downloaded file locally with go tool pprof; trace files use go tool trace.'
              )}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(
                'cpu and trace sample for the seconds above; block and mutex are empty unless their rates were enabled at startup.'
              )}
            </p>
          </div>
        </>
      )}
    </div>
  )
}
