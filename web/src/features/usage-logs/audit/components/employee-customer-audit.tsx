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
import type {
  CustomerAuditAccount,
  CustomerSecurityEvent,
} from '@/features/customer-console/audit-api'
import { CustomerAuditView } from '@/features/customer-console/customer-audit-dialog'
import { getMyCustomers } from '@/features/customers/api'
import type { CustomerProfile } from '@/features/customers/types'
import type { AuditRiskView } from '@/lib/audit-navigation'
import { createServerError } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

export function EmployeeCustomerAudit(
  props: {
    tab?: 'audit' | 'risk'
    riskView?: AuditRiskView
    onTabChange?: (tab: 'audit' | 'risk') => void
    onRiskViewChange?: (view: AuditRiskView) => void
  } = {}
) {
  const [customer, setCustomer] = useState<CustomerAuditAccount>()
  const [investigation, setInvestigation] = useState<CustomerSecurityEvent>()
  const customerFilter = (
    <CustomerSelect
      value={customer}
      onChange={(next) => {
        setCustomer(next)
        setInvestigation(undefined)
      }}
    />
  )
  return (
    <div className='flex h-full min-h-0 min-w-0 flex-col'>
      <CustomerAuditView
        {...props}
        paginationInFooter
        customer={customer}
        customerFilter={customerFilter}
        initialInvestigation={investigation}
        onInvestigateCustomer={(account, event) => {
          setCustomer({
            customer_user_id: account.id,
            username: account.username,
            display_name: account.display_name,
          })
          setInvestigation(event)
          props.onTabChange?.('audit')
        }}
      />
    </div>
  )
}

function CustomerSelect(props: {
  value?: CustomerAuditAccount
  onChange: (customer: CustomerAuditAccount | undefined) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [term, setTerm] = useState('')
  const anchor = useComboboxAnchor()
  useEffect(() => {
    const timer = setTimeout(() => setTerm(search.trim()), 250)
    return () => clearTimeout(timer)
  }, [search])
  const query = useInfiniteQuery({
    queryKey: [
      'audit-customers',
      user?.id,
      user?.role,
      user?.is_employee,
      term,
    ],
    initialPageParam: 1,
    queryFn: async ({ pageParam, signal }) => {
      const response = await getMyCustomers(
        { page: pageParam, page_size: 20, keyword: term },
        signal
      )
      if (!response.success || !response.data) {
        throw createServerError(response, t('Failed to load customers'))
      }
      return response.data
    },
    getNextPageParam: (page) =>
      page.items.length > 0 && page.page * page.page_size < page.total
        ? page.page + 1
        : undefined,
    enabled: open && !!user?.is_employee,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const searching = search.trim() !== term
  const options =
    query.isError || searching
      ? []
      : (query.data?.pages.flatMap((page) => page.items) ?? [])
  const selectedLabel = props.value
    ? `${props.value.username || props.value.display_name || t('Customer')} #${props.value.customer_user_id}`
    : t('All customers')
  return (
    <Combobox
      items={options}
      value={props.value ?? null}
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setSearch('')
      }}
      inputValue={open ? search : selectedLabel}
      onInputValueChange={(next, details) => {
        if (details.reason === 'input-change') setSearch(next)
      }}
      onValueChange={(customer, details) => {
        if (customer) props.onChange(customer)
        else if (details.reason === 'clear-press') props.onChange(undefined)
      }}
      isItemEqualToValue={(item, chosen) =>
        item.customer_user_id === chosen.customer_user_id
      }
      itemToStringLabel={(customer) =>
        `${customer.username || customer.display_name || t('Customer')} #${customer.customer_user_id}`
      }
      filter={null}
    >
      <div ref={anchor}>
        <ComboboxInput
          aria-label={t('Customer')}
          triggerAriaLabel={t('Customer')}
          showClear={!!props.value}
          clearAriaLabel={t('Clear selection')}
          placeholder={t('Customer ID / Account name / Remark')}
        />
      </div>
      <ComboboxContent anchor={anchor}>
        {(query.isFetching || searching) && (
          <p role='status' className='text-muted-foreground p-2 text-xs'>
            {t('Loading...')}
          </p>
        )}
        {query.isError && !query.isFetching && !searching && (
          <div className='p-2 text-xs'>
            <p role='alert'>{t('Failed to load customers')}</p>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void query.refetch()}
            >
              {t('Retry')}
            </Button>
          </div>
        )}
        {!query.isFetching && !query.isError && !searching && (
          <ComboboxEmpty>{t('No results found')}</ComboboxEmpty>
        )}
        <ComboboxList>
          {(customer: CustomerProfile) => (
            <ComboboxItem key={customer.customer_user_id} value={customer}>
              {`${customer.username || customer.display_name || t('Customer')} #${customer.customer_user_id}`}
            </ComboboxItem>
          )}
        </ComboboxList>
        {query.hasNextPage && !query.isError && !searching && (
          <Button
            variant='ghost'
            size='sm'
            disabled={query.isFetching}
            onClick={() => void query.fetchNextPage()}
          >
            {t('Load more')}
          </Button>
        )}
      </ComboboxContent>
    </Combobox>
  )
}
