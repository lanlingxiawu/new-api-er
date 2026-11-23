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
import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  useComboboxAnchor,
} from '@/components/ui/combobox'
import { useAuthStore } from '@/stores/auth-store'

import { getAuditRoutes, type AuditFilters } from '../api'

export function AuditRouteSelect(props: {
  scope: 'all' | 'self'
  applied: AuditFilters
  filters: AuditFilters
  onChange: (patch: Partial<AuditFilters>) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [term, setTerm] = useState('')
  const anchor = useComboboxAnchor()
  useEffect(() => {
    const timer = setTimeout(() => setTerm(search), 250)
    return () => clearTimeout(timer)
  }, [search])
  const query = useInfiniteQuery({
    queryKey: [
      'audit',
      user?.id,
      props.scope,
      'routes',
      user?.role,
      user?.permissions,
      props.applied.start_timestamp,
      props.applied.end_timestamp,
      term,
    ],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) =>
      getAuditRoutes(
        props.scope,
        {
          start_timestamp: props.applied.start_timestamp,
          end_timestamp: props.applied.end_timestamp,
          q: term,
          after_route: pageParam,
          limit: 50,
        },
        signal
      ),
    getNextPageParam: (page) => page.next_cursor || undefined,
    enabled: open,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const value = props.filters.route_missing
    ? '__missing'
    : (props.filters.route ?? '__all')
  const options = [
    { value: '__all', label: t('All routes') },
    { value: '__missing', label: t('Route not recorded') },
    ...[...new Set(query.data?.pages.flatMap((page) => page.items) ?? [])].map(
      (route) => ({ value: route, label: route })
    ),
  ]
  if (
    props.filters.route &&
    !options.some((option) => option.value === props.filters.route)
  ) {
    options.push({ value: props.filters.route, label: props.filters.route })
  }
  const selected =
    options.find((option) => option.value === value) ?? options[0]
  return (
    <Combobox
      items={options}
      value={selected}
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setSearch('')
      }}
      inputValue={open ? search : selected.label}
      onInputValueChange={(next, details) => {
        if (details.reason === 'input-change') setSearch(next)
      }}
      onValueChange={(option) => {
        if (option) {
          props.onChange({
            route: option.value.startsWith('__') ? undefined : option.value,
            route_missing: option.value === '__missing' || undefined,
          })
        }
      }}
      isItemEqualToValue={(item, chosen) => item.value === chosen.value}
      filter={null}
    >
      <div ref={anchor}>
        <ComboboxInput
          aria-label={t('Route')}
          triggerAriaLabel={t('Route')}
          placeholder={t('Search routes')}
        />
      </div>
      <ComboboxContent anchor={anchor}>
        {query.isFetching && (
          <p role='status' className='text-muted-foreground p-2 text-xs'>
            {t('Loading...')}
          </p>
        )}
        {query.isError && (
          <div className='p-2 text-xs'>
            <p>{t('Failed to load routes')}</p>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void query.refetch()}
            >
              {t('Retry')}
            </Button>
          </div>
        )}
        <ComboboxEmpty>{t('No results found')}</ComboboxEmpty>
        <ComboboxList>
          {(option: { value: string; label: string }) => (
            <ComboboxItem key={option.value} value={option}>
              {option.label}
            </ComboboxItem>
          )}
        </ComboboxList>
        {query.hasNextPage && (
          <Button
            variant='ghost'
            size='sm'
            disabled={query.isFetchingNextPage}
            onClick={() => void query.fetchNextPage()}
          >
            {t('Load more')}
          </Button>
        )}
      </ComboboxContent>
    </Combobox>
  )
}
