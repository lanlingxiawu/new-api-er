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
import { ROLE } from '@/lib/roles'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '../../components/logs-filter-toolbar'
import type { AuditFilters, AuditRecord } from '../api'
import { AuditRouteSelect } from './audit-route-select'

function AuditFilterSelect(props: {
  label: string
  value: string
  options: { value: string; label: string; disabled?: boolean }[]
  onChange: (value: string) => void
}) {
  return (
    <LogsFilterField>
      <Combobox
        options={props.options}
        value={props.value}
        onValueChange={(value) => {
          if (value !== null) props.onChange(value)
        }}
        aria-label={props.label}
        className='w-full'
      />
    </LogsFilterField>
  )
}

export function AuditLogFilterBar(props: {
  actionEnd?: ReactNode
  table: Table<AuditRecord>
  filters: AuditFilters
  baseline: AuditFilters
  applied: AuditFilters
  canReadSecurity?: boolean
  onChange: (patch: Partial<AuditFilters>) => void
  scope: 'all' | 'self'
  viewerRole: number
  accessOnly?: boolean
  tokenScope: string
  defaultTokenScope: string
  currentTokenRef?: string
  onTokenScopeChange: (value: string) => void
  isFetching: boolean
  onSearch: () => void
  onReset: () => void
}) {
  const { t } = useTranslation()
  const dateFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        maxRangeSec={30 * 86400}
        start={
          props.filters.start_timestamp === undefined
            ? undefined
            : new Date(props.filters.start_timestamp * 1000)
        }
        end={
          props.filters.end_timestamp === undefined
            ? undefined
            : new Date(props.filters.end_timestamp * 1000)
        }
        onChange={({ start, end }) =>
          props.onChange({
            start_timestamp: start
              ? Math.floor(start.getTime() / 1000)
              : undefined,
            end_timestamp: end ? Math.floor(end.getTime() / 1000) : undefined,
          })
        }
      />
    </LogsFilterField>
  )
  const mainFilters = (
    <>
      <AuditFilterSelect
        label={t('Result')}
        value={props.filters.success ?? 'all'}
        options={[
          { value: 'all', label: t('All results') },
          { value: 'true', label: t('Success') },
          { value: 'false', label: t('Failed') },
        ]}
        onChange={(value) =>
          props.onChange({ success: value === 'all' ? undefined : value })
        }
      />
      {props.accessOnly ? (
        <AuditFilterSelect
          label={t('Token scope')}
          value={props.tokenScope}
          options={[
            { value: 'all', label: t('All tokens') },
            {
              value: 'current',
              label: t('Current token'),
              disabled: !props.currentTokenRef,
            },
            {
              value: 'historical',
              label: t('Historical tokens'),
              disabled: props.currentTokenRef === undefined,
            },
          ]}
          onChange={props.onTokenScopeChange}
        />
      ) : (
        <AuditFilterSelect
          label={t('Category')}
          value={props.filters.category ?? 'all'}
          options={[
            { value: 'all', label: t('All categories') },
            { value: 'login', label: t('Login') },
            { value: 'security', label: t('Account security') },
            { value: 'operation', label: t('Operation audit') },
            { value: 'access_token', label: t('Access Token') },
          ]}
          onChange={(value) =>
            props.onChange({ category: value === 'all' ? undefined : value })
          }
        />
      )}
    </>
  )
  const advancedFilters = (
    <>
      <LogsFilterField>
        <LogsFilterInput
          aria-label='IP'
          placeholder='IP'
          value={props.filters.ip ?? ''}
          onChange={(event) =>
            props.onChange({ ip: event.target.value || undefined })
          }
        />
      </LogsFilterField>
      {props.scope === 'all' && props.viewerRole >= ROLE.ADMIN && (
        <AuditFilterSelect
          label={t('Actor identity')}
          value={props.filters.actor_type ?? 'all'}
          options={[
            { value: 'all', label: t('All identities') },
            ...(props.viewerRole >= ROLE.SUPER_ADMIN
              ? [{ value: 'root', label: t('Super administrator') }]
              : []),
            { value: 'admin', label: t('Administrator') },
            { value: 'employee', label: t('Employee') },
            { value: 'user', label: t('User') },
            {
              value: 'unclassified_user',
              label: t('User (employee status not recorded)'),
            },
            ...(props.viewerRole >= ROLE.SUPER_ADMIN
              ? [{ value: 'unknown', label: t('Unknown identity') }]
              : []),
          ]}
          onChange={(value) =>
            props.onChange({ actor_type: value === 'all' ? undefined : value })
          }
        />
      )}
      <AuditFilterSelect
        label={t('Operation source')}
        value={props.filters.auth_method ?? 'all'}
        options={[
          { value: 'all', label: t('All sources') },
          { value: 'session', label: t('Web session') },
          { value: 'access_token', label: t('Access Token') },
          { value: 'unknown', label: t('Unknown source') },
        ]}
        onChange={(value) =>
          props.onChange({ auth_method: value === 'all' ? undefined : value })
        }
      />
      {props.scope === 'all' && (
        <LogsFilterField>
          <AuditRouteSelect
            scope={props.scope}
            applied={props.applied}
            filters={props.filters}
            onChange={props.onChange}
          />
        </LogsFilterField>
      )}
      {props.canReadSecurity && (
        <AuditFilterSelect
          label={t('IP risk')}
          value={props.filters.ip_risk ?? 'all'}
          options={[
            { value: 'all', label: t('All IPs') },
            { value: 'abnormal', label: t('Abnormal IPs only') },
            { value: 'attention', label: t('Needs attention') },
            { value: 'high', label: t('High risk') },
          ]}
          onChange={(value) =>
            props.onChange({ ip_risk: value === 'all' ? undefined : value })
          }
        />
      )}
      {!props.accessOnly && props.scope === 'all' && (
        <LogsFilterField>
          <LogsFilterInput
            aria-label={t('Token identifier')}
            placeholder={t('Token identifier')}
            value={props.filters.token_ref ?? ''}
            onChange={(event) =>
              props.onChange({ token_ref: event.target.value || undefined })
            }
          />
        </LogsFilterField>
      )}
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Request ID')}
          placeholder={t('Request ID')}
          value={props.filters.request_id ?? ''}
          onChange={(event) =>
            props.onChange({ request_id: event.target.value || undefined })
          }
        />
      </LogsFilterField>
      {props.scope === 'all' && (
        <LogsFilterField>
          <LogsFilterInput
            aria-label={t('Username')}
            placeholder={t('Username')}
            value={props.filters.username ?? ''}
            onChange={(event) =>
              props.onChange({ username: event.target.value || undefined })
            }
          />
        </LogsFilterField>
      )}
    </>
  )
  const advancedCount = [
    props.scope === 'all' && !props.accessOnly && props.filters.token_ref,
    props.filters.request_id,
    props.scope === 'all' && props.filters.username,
    props.filters.ip,
    props.scope === 'all' && props.filters.actor_type,
    props.filters.auth_method,
    props.scope === 'all' && props.filters.route,
    props.scope === 'all' && props.filters.route_missing,
    props.canReadSecurity && props.filters.ip_risk,
  ].filter(Boolean).length
  const filterCount =
    advancedCount +
    [
      props.filters.success,
      props.filters.category,
      props.tokenScope !== props.defaultTokenScope,
    ].filter(Boolean).length
  const hasFilters =
    filterCount > 0 ||
    props.filters.start_timestamp !== props.baseline.start_timestamp ||
    props.filters.end_timestamp !== props.baseline.end_timestamp
  return (
    <LogsFilterToolbar
      compactMobile
      actionEnd={props.actionEnd}
      table={props.table}
      primaryFilters={
        <>
          {dateFilter}
          {mainFilters}
        </>
      }
      advancedFilters={advancedFilters}
      mobilePinnedFilters={dateFilter}
      mobileFilters={
        <>
          {mainFilters}
          {advancedFilters}
        </>
      }
      mobileFilterCount={filterCount}
      advancedFilterCount={advancedCount}
      hasActiveFilters={hasFilters}
      hasAdvancedActiveFilters={advancedCount > 0}
      searchLoading={props.isFetching}
      onSearch={props.onSearch}
      onReset={props.onReset}
    />
  )
}
