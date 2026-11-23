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
import type { Table } from '@tanstack/react-table'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Combobox } from '@/components/ui/combobox'
import type { SecurityView } from '@/lib/audit-navigation'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '../../components/logs-filter-toolbar'
import type { SecurityAlert } from './api'
import { alertStatuses } from './lib/alert-form'
import { resources, severities } from './lib/security-display'
import { securityRules } from './lib/security-rules'

export interface SecurityFilters {
  view: SecurityView
  resource: string
  status: string
  rule: string
  severity: string
  history_scope: string
  ip: string
  actor_id: string
  assignee_id: string
  start: number
  end: number
}

export type SecurityTextFilters = Pick<
  SecurityFilters,
  'ip' | 'actor_id' | 'assignee_id'
>

export function SecurityFilterBar(props: {
  table: Table<SecurityAlert>
  filters: SecurityFilters
  textFilters: SecurityTextFilters
  customRange: boolean
  isFetching: boolean
  invalidRange: boolean
  stats?: ReactNode
  actions?: ReactNode
  onChange: (patch: Partial<SecurityFilters>) => void
  onTextChange: (patch: Partial<SecurityTextFilters>) => void
  onDateChange: (patch: Pick<SecurityFilters, 'start' | 'end'>) => void
  onSearch: () => void
  onReset: () => void
}) {
  const { t } = useTranslation()
  const allTime =
    props.filters.view === 'unresolved' || props.filters.view === 'archived'
  const selects = [
    {
      key: 'view' as const,
      label: 'Security monitoring',
      options: [
        { value: 'unresolved', label: t('Unprocessed') },
        { value: 'handled', label: t('Processed') },
        { value: 'id', label: t('All') },
        { value: 'recent_hits', label: t('Recent matches (top 20)') },
        { value: 'activity', label: t('Recently updated') },
        { value: 'archived', label: t('Archived alerts') },
      ],
    },
    { key: 'resource' as const, label: 'Resource', values: resources },
    {
      key: 'status' as const,
      label: 'Handling status',
      values: Object.fromEntries(
        Object.entries(alertStatuses).filter(
          ([value]) =>
            (props.filters.view !== 'archived' &&
              props.filters.view !== 'handled') ||
            value === 'resolved' ||
            value === 'false_positive'
        )
      ),
    },
    { key: 'rule' as const, label: 'Rule', values: securityRules },
    { key: 'severity' as const, label: 'Severity', values: severities },
    {
      key: 'history_scope' as const,
      label: 'Evidence scope',
      values: { legacy: 'Legacy alerts to review' },
    },
  ].map(({ key, label, options, values }) => (
    <LogsFilterField key={key}>
      <Combobox
        id={`security-${key}`}
        aria-label={t(label)}
        value={props.filters[key]}
        options={
          options ?? [
            { value: '', label: `${t(label)}: ${t('All')}` },
            ...Object.entries(values ?? {}).map(([value, name]) => ({
              value,
              label: t(name),
            })),
          ]
        }
        className='w-full'
        disabled={key === 'status' && props.filters.view === 'unresolved'}
        onValueChange={(value) => {
          if (value === null) return
          props.onChange({
            [key]: value,
            ...(key === 'resource' && value ? { history_scope: '' } : {}),
            ...(key === 'history_scope' && value ? { resource: '' } : {}),
            ...(key === 'view' &&
            (value === 'unresolved' ||
              value === 'archived' ||
              value === 'handled')
              ? { status: '' }
              : {}),
          })
        }}
      />
    </LogsFilterField>
  ))
  const dateFilter = !allTime ? (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={
          props.filters.start ? new Date(props.filters.start * 1000) : undefined
        }
        end={props.filters.end ? new Date(props.filters.end * 1000) : undefined}
        onChange={({ start, end }) =>
          props.onDateChange({
            start: start ? Math.floor(start.getTime() / 1000) : 0,
            end: end ? Math.floor(end.getTime() / 1000) : 0,
          })
        }
      />
    </LogsFilterField>
  ) : null
  const advancedFilters = (
    <>
      {selects.slice(3)}
      {(
        [
          ['ip', 'IP'],
          ['actor_id', 'Operator ID'],
          ['assignee_id', 'Assignee ID'],
        ] as const
      ).map(([key, label]) => (
        <LogsFilterField key={key}>
          <LogsFilterInput
            id={`security-${key}`}
            aria-label={t(label)}
            placeholder={t(label)}
            value={props.textFilters[key]}
            type={key === 'ip' ? 'text' : 'number'}
            min={key === 'actor_id' ? 1 : 0}
            step={1}
            onChange={(event) =>
              props.onTextChange({ [key]: event.target.value })
            }
          />
        </LogsFilterField>
      ))}
    </>
  )
  const advancedCount = [
    props.filters.rule,
    props.filters.severity,
    props.filters.history_scope,
    ...Object.values(props.textFilters),
  ].filter(Boolean).length
  const filterCount =
    advancedCount +
    [
      props.filters.resource,
      props.filters.status,
      props.filters.view !== 'id',
    ].filter(Boolean).length
  return (
    <LogsFilterToolbar
      compactMobile
      table={props.table}
      primaryFilters={
        <>
          {dateFilter}
          {selects.slice(0, 3)}
        </>
      }
      advancedFilters={advancedFilters}
      mobilePinnedFilters={dateFilter ?? selects[0]}
      mobileFilters={
        <>
          {selects.slice(allTime ? 1 : 0, 3)}
          {advancedFilters}
        </>
      }
      mobileFilterCount={filterCount}
      advancedFilterCount={advancedCount}
      hasActiveFilters={filterCount > 0 || props.customRange}
      hasAdvancedActiveFilters={advancedCount > 0}
      searchLoading={props.isFetching}
      stats={props.stats}
      actionEnd={props.actions}
      onSearch={() => {
        if (!props.invalidRange) props.onSearch()
      }}
      onReset={props.onReset}
    />
  )
}
