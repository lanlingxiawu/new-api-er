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
import { Plus, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
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
import { handleServerError } from '@/lib/handle-server-error'

import { getGroups } from '../../users/api'
import { updateSystemOptionGroup } from '../api'
import { useSettingsPageAccess } from '../components/settings-page-access-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// Mirrors MaxGroupRetryTimes in setting/operation_setting/group_retry_setting.go.
const MAX_GROUP_RETRY_TIMES = 20
const DISABLED = -1
const WHOLE_NUMBER = /^-?\d+$/

type Row = {
  // Stable key so editing a group name does not remount the row.
  id: number
  group: string
  // Kept as the raw input text: a number input reports "" while the admin has
  // typed only "-", and coercing that to 0 would make -1 impossible to type.
  times: string
  statusMode: 'inherit' | 'custom' | 'none'
  statusCodes: string
}

function parseRows(raw: string, statusRaw: string): Row[] {
  try {
    const parsed = JSON.parse(raw || '{}') as Record<string, number>
    const statuses = JSON.parse(statusRaw || '{}') as Record<string, string>
    const groups = [
      ...new Set([...Object.keys(parsed), ...Object.keys(statuses)]),
    ]
    return groups.map((group, index) => {
      let statusMode: Row['statusMode'] = 'inherit'
      if (Object.hasOwn(statuses, group)) {
        statusMode = statuses[group].trim() ? 'custom' : 'none'
      }
      return {
        id: index,
        group,
        times: String(parsed[group] ?? 0),
        statusMode,
        statusCodes: statuses[group] ?? '',
      }
    })
  } catch {
    return []
  }
}

function describeTimes(raw: string, t: (key: string) => string): string {
  if (!WHOLE_NUMBER.test(raw)) return ''
  const times = Number(raw)
  if (times === DISABLED) {
    return t('No retry: this group is tried once.')
  }
  if (times === 0) return t('Follows the system default retry count.')
  return ''
}

export function GroupRetrySection(props: {
  value: string
  statusRules: string
  statusEnabled: boolean
}) {
  const { t } = useTranslation()
  const { scope } = useSettingsPageAccess()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const [rows, setRows] = useState<Row[]>(() =>
    parseRows(props.value, props.statusRules)
  )
  const [enabled, setEnabled] = useState(props.statusEnabled)
  const [nextId, setNextId] = useState(
    () => parseRows(props.value, props.statusRules).length
  )
  const dirty = useRef({ counts: false, statuses: false })

  useEffect(() => {
    // Saving one module must not erase the other module's unsaved edits.
    if (dirty.current.counts || dirty.current.statuses) return
    const parsed = parseRows(props.value, props.statusRules)
    setRows(parsed)
    setNextId(parsed.length)
    setEnabled(props.statusEnabled)
  }, [props.value, props.statusRules, props.statusEnabled])

  const statusMutation = useMutation({
    mutationFn: (rules: string) =>
      updateSystemOptionGroup({
        scope,
        module: 'group_retry_status_setting',
        values: { enabled: String(enabled), rules },
      }),
    onSuccess: (response) => {
      if (!response.success) return
      dirty.current.statuses = false
      queryClient.invalidateQueries({ queryKey: ['system-options', scope] })
      toast.success(t('Retry status codes saved'))
    },
    onError: handleServerError,
  })
  const busy = updateOption.isPending || statusMutation.isPending

  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
    staleTime: 5 * 60 * 1000,
  })
  const groups = useMemo(
    () => [
      ...new Set([
        ...(groupsData?.data ?? []),
        ...rows.map((row) => row.group).filter(Boolean),
      ]),
    ],
    [groupsData, rows]
  )

  // Duplicate group names would silently drop entries on serialization, so the
  // row is flagged before it can be saved.
  const duplicates = useMemo(() => {
    const seen = new Set<string>()
    const dupes = new Set<string>()
    for (const row of rows) {
      if (!row.group) continue
      if (seen.has(row.group)) dupes.add(row.group)
      seen.add(row.group)
    }
    return dupes
  }, [rows])

  const rowError = (row: Row): string => {
    if (!row.group) return t('Select a group.')
    if (duplicates.has(row.group)) return t('This group is already configured.')
    if (!WHOLE_NUMBER.test(row.times)) return t('Enter a whole number.')
    const times = Number(row.times)
    if (times < DISABLED || times > MAX_GROUP_RETRY_TIMES) {
      return t('Must be -1, or between 0 and 20.')
    }
    return ''
  }

  const save = () => {
    const firstError = rows.map(rowError).find((message) => message !== '')
    if (firstError) {
      toast.error(firstError)
      return
    }
    // An empty editor saves "{}" rather than deleting the key: that keeps
    // "explicitly cleared" distinguishable from "never configured".
    const payload = JSON.stringify(
      Object.fromEntries(rows.map((row) => [row.group, Number(row.times)]))
    )
    updateOption.mutate(
      { key: 'GroupRetryTimes', value: payload },
      {
        onSuccess: (response) => {
          if (response.success) dirty.current.counts = false
        },
      }
    )
  }

  const statusError = (row: Row): string => {
    if (!row.group) return t('Select a group.')
    if (duplicates.has(row.group)) return t('This group is already configured.')
    if (row.statusMode !== 'custom') return ''
    const segments = row.statusCodes
      .replaceAll('，', ',')
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
    const valid =
      segments.length > 0 &&
      segments.length <= 128 &&
      segments.every((segment) => {
        const match = /^(\d{3})(?:\s*-\s*(\d{3}))?$/.exec(segment)
        if (!match) return false
        const start = Number(match[1])
        const end = Number(match[2] ?? match[1])
        return start >= 100 && end <= 599 && start <= end
      })
    return valid
      ? ''
      : t('Enter HTTP codes from 100 to 599, for example 429,500-503.')
  }

  const saveStatuses = () => {
    const error = rows.map(statusError).find(Boolean)
    if (error) {
      toast.error(error)
      return
    }
    const rules = JSON.stringify(
      Object.fromEntries(
        rows
          .filter((row) => row.statusMode !== 'inherit')
          .map((row) => [
            row.group,
            row.statusMode === 'none' ? '' : row.statusCodes,
          ])
      )
    )
    if (
      rows.filter((row) => row.statusMode !== 'inherit').length > 256 ||
      new TextEncoder().encode(rules).length > 65536
    ) {
      toast.error(t('Retry rules must fit within 64 KiB and 256 groups.'))
      return
    }
    statusMutation.mutate(rules)
  }

  return (
    <SettingsSection title={t('Per-group retry attempts')}>
      <div className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Groups listed here use their own retry count instead of the system default. A group that is not listed follows the system default.'
          )}
        </p>
        <div className='flex items-center gap-3'>
          <Switch
            id='group-retry-status-enabled'
            checked={enabled}
            disabled={busy}
            onCheckedChange={(checked) => {
              dirty.current.statuses = true
              setEnabled(checked)
            }}
          />
          <Label htmlFor='group-retry-status-enabled'>
            {t('Enable per-group retry status codes')}
          </Label>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Custom codes replace the default status rules. Successful responses, 504 and 524 are never retried; retry counts and time limits still apply.'
          )}
        </p>

        {rows.length === 0 && (
          <p className='text-muted-foreground text-sm'>
            {t('No group overrides. Every group follows the system default.')}
          </p>
        )}

        <div className='space-y-3'>
          {rows.map((row) => {
            const error = rowError(row)
            const hint = describeTimes(row.times, t)
            return (
              <div className='space-y-1' key={row.id}>
                <div className='flex flex-wrap items-end gap-3'>
                  <div className='flex min-w-0 flex-1 flex-col gap-2'>
                    <Label htmlFor={`group-retry-name-${row.id}`}>
                      {t('Group')}
                    </Label>
                    <Select
                      items={groups.map((group) => ({
                        value: group,
                        label: group,
                      }))}
                      value={row.group || null}
                      disabled={busy}
                      onValueChange={(next) => {
                        if (next === null) return
                        dirty.current = { counts: true, statuses: true }
                        setRows((current) =>
                          current.map((item) =>
                            item.id === row.id ? { ...item, group: next } : item
                          )
                        )
                      }}
                    >
                      <SelectTrigger
                        id={`group-retry-name-${row.id}`}
                        className='w-full'
                      >
                        <SelectValue placeholder={t('Select a group')} />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {groups.map((group) => (
                            <SelectItem key={group} value={group}>
                              {group}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className='flex w-40 flex-col gap-2'>
                    <Label htmlFor={`group-retry-times-${row.id}`}>
                      {t('Retry attempts')}
                    </Label>
                    <Input
                      id={`group-retry-times-${row.id}`}
                      type='number'
                      min={DISABLED}
                      max={MAX_GROUP_RETRY_TIMES}
                      step={1}
                      value={row.times}
                      disabled={busy}
                      onChange={(event) => {
                        dirty.current.counts = true
                        setRows((current) =>
                          current.map((item) =>
                            item.id === row.id
                              ? { ...item, times: event.target.value }
                              : item
                          )
                        )
                      }}
                    />
                  </div>
                  <div className='flex min-w-48 flex-1 flex-col gap-2'>
                    <Label htmlFor={`group-retry-mode-${row.id}`}>
                      {t('Retry status codes')}
                    </Label>
                    <Select
                      value={row.statusMode}
                      disabled={busy}
                      items={[
                        {
                          value: 'inherit',
                          label: t('Inherit default status rules'),
                        },
                        { value: 'custom', label: t('Custom status codes') },
                        {
                          value: 'none',
                          label: t('Do not retry HTTP status errors'),
                        },
                      ]}
                      onValueChange={(next) => {
                        if (
                          next !== 'inherit' &&
                          next !== 'custom' &&
                          next !== 'none'
                        ) {
                          return
                        }
                        dirty.current.statuses = true
                        setRows((current) =>
                          current.map((item) =>
                            item.id === row.id
                              ? { ...item, statusMode: next }
                              : item
                          )
                        )
                      }}
                    >
                      <SelectTrigger
                        id={`group-retry-mode-${row.id}`}
                        className='w-full'
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='inherit'>
                            {t('Inherit default status rules')}
                          </SelectItem>
                          <SelectItem value='custom'>
                            {t('Custom status codes')}
                          </SelectItem>
                          <SelectItem value='none'>
                            {t('Do not retry HTTP status errors')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    {row.statusMode === 'custom' && (
                      <Input
                        aria-label={t('Retry status codes')}
                        placeholder='429,500-503'
                        value={row.statusCodes}
                        disabled={busy}
                        onChange={(event) => {
                          dirty.current.statuses = true
                          setRows((current) =>
                            current.map((item) =>
                              item.id === row.id
                                ? { ...item, statusCodes: event.target.value }
                                : item
                            )
                          )
                        }}
                      />
                    )}
                  </div>
                  <Button
                    variant='outline'
                    size='icon'
                    aria-label={t('Remove')}
                    disabled={busy}
                    onClick={() => {
                      dirty.current = { counts: true, statuses: true }
                      setRows((current) =>
                        current.filter((item) => item.id !== row.id)
                      )
                    }}
                  >
                    <Trash2 className='size-4' />
                  </Button>
                </div>
                {error ? (
                  <p className='text-destructive text-xs'>{error}</p>
                ) : (
                  hint && (
                    <p className='text-muted-foreground text-xs'>{hint}</p>
                  )
                )}
                {statusError(row) && (
                  <p className='text-destructive text-xs'>{statusError(row)}</p>
                )}
              </div>
            )
          })}
        </div>

        <div className='flex items-center justify-between'>
          <Button
            variant='outline'
            disabled={busy}
            onClick={() => {
              dirty.current = { counts: true, statuses: true }
              setRows((current) => [
                ...current,
                {
                  id: nextId,
                  group: '',
                  times: '0',
                  statusMode: 'inherit',
                  statusCodes: '',
                },
              ])
              setNextId((current) => current + 1)
            }}
          >
            <Plus className='mr-2 size-4' />
            {t('Add group')}
          </Button>
          <div className='flex flex-wrap gap-2'>
            <Button disabled={busy} onClick={save}>
              {updateOption.isPending ? t('Saving...') : t('Save retry counts')}
            </Button>
            <Button disabled={busy} onClick={saveStatuses}>
              {statusMutation.isPending
                ? t('Saving...')
                : t('Save retry status codes')}
            </Button>
          </div>
        </div>
      </div>
    </SettingsSection>
  )
}
