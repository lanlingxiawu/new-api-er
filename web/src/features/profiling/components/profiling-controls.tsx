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
import { Fragment, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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

function ProfileButton(props: {
  label: string
  raw: string
  loading: boolean
  disabled: boolean
  onClick: () => void
}) {
  return (
    <Button
      size='sm'
      variant='outline'
      disabled={props.disabled}
      onClick={props.onClick}
    >
      {props.loading ? (
        <Loader2 className='size-3.5 animate-spin' />
      ) : (
        <Download className='size-3.5' />
      )}
      {props.label}
      <span className='text-muted-foreground font-mono text-[11px]'>
        {props.raw}
      </span>
    </Button>
  )
}

/**
 * pprof 开关 + profile 下载。开关即时生效（单个布尔值，没必要攒着一起保存），
 * 下载按钮读的是服务端真实状态而不是表单值。
 */
export function ProfilingControls() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const requestSaveConfirmation = useSettingsSaveConfirmation()
  const [seconds, setSeconds] = useState('30')

  const statusQuery = useQuery({
    queryKey: ['profiling', 'status'],
    queryFn: getPprofStatus,
  })

  const status = statusQuery.data?.data
  const enabled = status?.enabled ?? false
  const profiles = status?.profiles ?? []

  const toggle = async (checked: boolean) => {
    if (checked === enabled) return

    await requestSaveConfirmation(async () => {
      await updateOption.mutateAsync({
        key: 'pprof_setting.enabled',
        value: checked,
      })
      await queryClient.invalidateQueries({ queryKey: ['profiling', 'status'] })
    })
  }

  // key 用 profile 名，但 goroutine 文本变体用合成 key 'goroutine:text' 区分，
  // 这样每个按钮的 loading 状态互不干扰。
  const download = useMutation({
    mutationFn: (key: string) => {
      if (key === GOROUTINE_TEXT_KEY) {
        return downloadProfile('goroutine', { debug: 2 })
      }
      const value = NEEDS_SECONDS.has(key) ? Number(seconds) || 30 : undefined
      return downloadProfile(key, { seconds: value })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const startDownload = (key: string) => {
    if (NEEDS_SECONDS.has(key)) {
      const value = Number(seconds)
      if (!Number.isFinite(value) || value <= 0 || value > MAX_SECONDS) {
        toast.error(
          t('Sampling duration can be at most {{max}} seconds', {
            max: MAX_SECONDS,
          })
        )
        return
      }
    }
    download.mutate(key)
  }

  return (
    <div className='space-y-4'>
      <div>
        <h4 className='font-medium'>{t('Profiling (pprof)')}</h4>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Download Go pprof profiles for offline analysis with go tool pprof. Takes effect immediately, no restart.'
          )}
        </p>
      </div>

      {statusQuery.isLoading ? (
        <Skeleton className='h-[104px] w-full' />
      ) : (
        <>
          <SettingsSwitchField
            checked={enabled}
            disabled={updateOption.isPending}
            onCheckedChange={toggle}
            label={t('Enable profiling downloads')}
            description={t(
              'A heap dump contains upstream keys and user tokens held in memory — keep this off unless you are actively investigating.'
            )}
          />

          <div className='space-y-2'>
            <div className='flex flex-wrap items-center gap-2'>
              <Input
                type='number'
                min={1}
                max={MAX_SECONDS}
                value={seconds}
                onChange={(event) => setSeconds(event.target.value)}
                className='w-24'
                disabled={!enabled}
                aria-label={t('CPU / trace sampling seconds')}
              />
              {profiles.map((name) => {
                const labelKey = PROFILE_LABEL_KEYS[name]
                const label = labelKey ? t(labelKey) : name
                return (
                  <Fragment key={name}>
                    <ProfileButton
                      label={label}
                      raw={name}
                      loading={
                        download.isPending && download.variables === name
                      }
                      disabled={!enabled || download.isPending}
                      onClick={() => startDownload(name)}
                    />
                    {/* goroutine 额外给一个文本格式：?debug=2 输出可读全栈，
                        排查"某几个协程卡死"时比 protobuf 直观 */}
                    {name === 'goroutine' ? (
                      <ProfileButton
                        label={t('Goroutine text')}
                        raw='debug=2'
                        loading={
                          download.isPending &&
                          download.variables === GOROUTINE_TEXT_KEY
                        }
                        disabled={!enabled || download.isPending}
                        onClick={() => startDownload(GOROUTINE_TEXT_KEY)}
                      />
                    ) : null}
                  </Fragment>
                )
              })}
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
