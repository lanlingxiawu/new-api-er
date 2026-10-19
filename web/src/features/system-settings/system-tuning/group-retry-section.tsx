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

import {
  NumericPresetInput,
  type NumericPreset,
} from '@/components/numeric-preset-input'
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
import {
  guardDraft,
  getGroupRetryGuardStatus,
  parseGuardDraft,
  type GuardDraft,
  type GuardRule,
} from './group-retry-guard'
import { GroupRetryGuardFields } from './group-retry-guard-fields'

// Mirrors MaxGroupRetryTimes in setting/operation_setting/group_retry_setting.go.
const MAX_GROUP_RETRY_TIMES = 10
const DISABLED = -1
const WHOLE_NUMBER = /^-?\d+$/
const RETRY_TIMES_PRESETS: NumericPreset[] = [
  { value: DISABLED, label: 'No retry' },
  { value: 0, label: 'Follow system default' },
]

type Row = {
  // Stable key so editing a group name does not remount the row.
  id: number
  group: string
  // Kept as the raw input text: a number input reports "" while the admin has
  // typed only "-", and coercing that to 0 would make -1 impossible to type.
  times: string
  statusMode: 'inherit' | 'custom' | 'none'
  statusCodes: string
  guard?: GuardDraft
}

function parseRows(raw: string, statusRaw: string, guardRaw: string): Row[] {
  try {
    const parsed = JSON.parse(raw || '{}') as Record<string, number>
    const statuses = JSON.parse(statusRaw || '{}') as Record<string, string>
    const guards = JSON.parse(guardRaw || '{}') as Record<string, GuardRule>
    const groups = [
      ...new Set([
        ...Object.keys(parsed),
        ...Object.keys(statuses),
        ...Object.keys(guards),
      ]),
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
        guard: guards[group] ? guardDraft(guards[group]) : undefined,
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
  guardRules: string
  guardEnabled: boolean
}) {
  const { t } = useTranslation()
  const { scope } = useSettingsPageAccess()
  const queryClient = useQueryClient()
  const [rows, setRows] = useState<Row[]>(() =>
    parseRows(props.value, props.statusRules, props.guardRules)
  )
  const [enabled, setEnabled] = useState(props.statusEnabled)
  const [guardEnabled, setGuardEnabled] = useState(props.guardEnabled)
  const [openGroupId, setOpenGroupId] = useState<number | null>(null)
  const [submitted, setSubmitted] = useState(false)
  const [removed, setRemoved] = useState<{ row: Row; index: number } | null>(
    null
  )
  const [nextId, setNextId] = useState(
    () => parseRows(props.value, props.statusRules, props.guardRules).length
  )
  const dirty = useRef(false)

  useEffect(() => {
    if (dirty.current) return
    const parsed = parseRows(props.value, props.statusRules, props.guardRules)
    setRows(parsed)
    setNextId(parsed.length)
    setEnabled(props.statusEnabled)
    setGuardEnabled(props.guardEnabled)
  }, [
    props.value,
    props.statusRules,
    props.statusEnabled,
    props.guardRules,
    props.guardEnabled,
  ])

  const saveMutation = useMutation({
    mutationFn: (values: Record<string, string>) =>
      updateSystemOptionGroup({
        scope,
        module: 'group_retry_policy',
        values,
      }),
    onSuccess: (response) => {
      if (!response.success || !response.data?.applied) {
        toast.error(
          response.message ||
            t(
              'Could not save retry settings. Your changes are kept; please try again.'
            )
        )
        return
      }
      dirty.current = false
      setSubmitted(false)
      setRemoved(null)
      queryClient.invalidateQueries({ queryKey: ['system-options', scope] })
      queryClient.invalidateQueries({
        queryKey: ['group-retry-guard-status', scope],
      })
      toast.success(t('Retry settings saved'))
    },
    onError: handleServerError,
  })
  const busy = saveMutation.isPending
  const statusQuery = useQuery({
    queryKey: ['group-retry-guard-status', scope],
    queryFn: () =>
      getGroupRetryGuardStatus(scope ?? 'system-tuning.group-retry-times'),
    enabled: props.guardEnabled,
    refetchInterval: 5000,
  })

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
  const selectedGroups = useMemo(
    () => new Set(rows.map((row) => row.group).filter(Boolean)),
    [rows]
  )

  const rowError = (row: Row): string => {
    if (!row.group) return t('Select a group.')
    if (duplicates.has(row.group)) return t('This group is already configured.')
    if (!WHOLE_NUMBER.test(row.times)) return t('Enter a whole number.')
    const times = Number(row.times)
    if (times < DISABLED || times > MAX_GROUP_RETRY_TIMES) {
      return t('Must be -1, or between 0 and 10.')
    }
    return ''
  }

  const statusError = (row: Row): string => {
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

  const save = () => {
    setSubmitted(true)
    const error = rows
      .map((row) => rowError(row) || statusError(row))
      .find(Boolean)
    if (error) {
      const invalidRow = rows.find((row) => rowError(row) || statusError(row))
      if (invalidRow && !invalidRow.group) setOpenGroupId(invalidRow.id)
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
    const guardRules: Record<string, GuardRule> = Object.create(null)
    for (const row of rows) {
      if (!row.guard) continue
      const rule = parseGuardDraft(row.guard)
      if (!rule) {
        toast.error(
          t(
            'Check the protection limits. Maximum cooldown must be at least the initial cooldown.'
          )
        )
        return
      }
      guardRules[row.group] = rule
    }
    const values = {
      times: JSON.stringify(
        Object.fromEntries(rows.map((row) => [row.group, Number(row.times)]))
      ),
      status_enabled: String(enabled),
      status_rules: rules,
      guard_enabled: String(guardEnabled),
      guard_rules: JSON.stringify(guardRules),
    }
    if (
      rows.length > 256 ||
      Object.values(values).some(
        (value) => new TextEncoder().encode(value).length > 65536
      )
    ) {
      toast.error(t('Retry rules must fit within 64 KiB and 256 groups.'))
      return
    }
    saveMutation.mutate(values)
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
              dirty.current = true
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

        <div className='flex items-center gap-3'>
          <Switch
            id='group-retry-guard-enabled'
            checked={guardEnabled}
            disabled={busy}
            onCheckedChange={(checked) => {
              dirty.current = true
              setGuardEnabled(checked)
            }}
          />
          <Label htmlFor='group-retry-guard-enabled'>
            {t('Enable automatic retry protection')}
          </Label>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'When both the RPM and slow-response count reach their thresholds, retries pause and recover after cooldown. First attempts remain available.'
          )}
        </p>
        {guardEnabled && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Uses completed monitoring samples. Streams use first-response delay; other requests use total response time. Long streams are counted when they finish. Redis is required.'
            )}
          </p>
        )}

        {rows.length === 0 && (
          <p className='text-muted-foreground text-sm'>
            {t('No group overrides. Every group follows the system default.')}
          </p>
        )}

        <div className='flex flex-col gap-4'>
          {rows.map((row) => {
            const error = row.group || submitted ? rowError(row) : ''
            const hint = describeTimes(row.times, t)
            const availableGroups = groups.filter(
              (group) => group === row.group || !selectedGroups.has(group)
            )
            return (
              <section
                className='bg-card flex flex-col gap-3 rounded-lg border p-4'
                key={row.id}
                aria-label={row.group || t('Select a group')}
              >
                <div className='flex flex-wrap items-end gap-3'>
                  <div className='flex min-w-48 flex-1 flex-col gap-2'>
                    <Label htmlFor={`group-retry-name-${row.id}`}>
                      {t('Group')}
                    </Label>
                    <Select
                      items={availableGroups.map((group) => ({
                        value: group,
                        label: group,
                      }))}
                      value={row.group || null}
                      open={openGroupId === row.id}
                      onOpenChange={(open) =>
                        setOpenGroupId(open ? row.id : null)
                      }
                      disabled={busy}
                      onValueChange={(next) => {
                        if (next === null) return
                        dirty.current = true
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
                          {availableGroups.map((group) => (
                            <SelectItem key={group} value={group}>
                              {group}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className='flex w-full flex-col gap-2 sm:w-60'>
                    <Label htmlFor={`group-retry-times-${row.id}`}>
                      {t('Retry attempts')}
                    </Label>
                    <NumericPresetInput
                      id={`group-retry-times-${row.id}`}
                      presets={RETRY_TIMES_PRESETS}
                      min={1}
                      max={MAX_GROUP_RETRY_TIMES}
                      value={row.times}
                      disabled={busy}
                      onChange={(next) => {
                        dirty.current = true
                        setRows((current) =>
                          current.map((item) =>
                            item.id === row.id
                              ? { ...item, times: String(next) }
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
                        dirty.current = true
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
                          dirty.current = true
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
                      dirty.current = true
                      setRemoved({
                        row,
                        index: rows.findIndex((item) => item.id === row.id),
                      })
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
                {guardEnabled && (
                  <GroupRetryGuardFields
                    id={row.id}
                    value={row.guard ?? guardDraft()}
                    disabled={busy}
                    active={props.guardEnabled && !dirty.current}
                    available={
                      !statusQuery.isError &&
                      statusQuery.data?.success === true &&
                      statusQuery.data.data?.available === true
                    }
                    status={statusQuery.data?.data?.groups.find(
                      (item) => item.group === row.group
                    )}
                    onChange={(guard) => {
                      dirty.current = true
                      setRows((current) =>
                        current.map((item) =>
                          item.id === row.id ? { ...item, guard } : item
                        )
                      )
                    }}
                  />
                )}
              </section>
            )
          })}
        </div>

        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              disabled={busy}
              onClick={() => {
                dirty.current = true
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
                setOpenGroupId(nextId)
                setNextId((current) => current + 1)
              }}
            >
              <Plus className='mr-2 size-4' />
              {t('Add group')}
            </Button>
            {removed && (
              <Button
                variant='outline'
                disabled={busy}
                onClick={() => {
                  if (
                    removed.row.group &&
                    selectedGroups.has(removed.row.group)
                  ) {
                    toast.error(t('This group is already configured.'))
                    return
                  }
                  dirty.current = true
                  setRows((current) => {
                    const restored = [...current]
                    restored.splice(
                      Math.min(removed.index, restored.length),
                      0,
                      { ...removed.row, id: nextId }
                    )
                    return restored
                  })
                  setNextId((current) => current + 1)
                  setRemoved(null)
                }}
              >
                {t('Undo removal')}
              </Button>
            )}
          </div>
          <div className='flex flex-wrap items-center gap-3'>
            {dirty.current && (
              <span className='text-muted-foreground text-xs'>
                {t('Unsaved changes')}
              </span>
            )}
            <Button disabled={busy || !dirty.current} onClick={save}>
              {busy ? t('Saving...') : t('Save')}
            </Button>
          </div>
        </div>
      </div>
    </SettingsSection>
  )
}
