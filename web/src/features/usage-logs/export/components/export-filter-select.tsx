import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Label } from '@/components/ui/label'
import { useAuthStore } from '@/stores/auth-store'

import { exportRequest } from '../employee-api'

interface Props {
  base?: string
  field: string
  label: string
  value: string
  onChange: (value: string) => void
  onSelect?: (option: { value: string; label: string }) => void
  start?: number
  end?: number
  customerIds?: number[]
  templateId?: string
  disabled?: boolean
}

export function ExportFilterSelect(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [selected, setSelected] = useState<{ value: string; label: string }>()
  useEffect(() => {
    const timer = setTimeout(() => setKeyword(search), 300)
    return () => clearTimeout(timer)
  }, [search])
  const query = useInfiniteQuery({
    queryKey: [
      'export-filter-options',
      userId,
      props.base,
      props.field,
      props.start,
      props.end,
      props.customerIds,
      props.templateId,
      keyword,
    ],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({
        field: props.field,
        keyword,
        cursor: pageParam,
      })
      if (props.start) params.set('start_timestamp', String(props.start))
      if (props.end) params.set('end_timestamp', String(props.end))
      if (props.templateId) params.set('template_id', props.templateId)
      for (const id of props.customerIds ?? []) {
        params.append('customer_ids', String(id))
      }
      return exportRequest<{
        items: { value: string; label: string }[]
        next_cursor: string
      }>(
        'get',
        `${props.base ?? '/api/log/export'}/options?${params}`,
        undefined,
        {
          // Shown inline below; several dropdowns failing together must not stack toasts.
          quiet: true,
        }
      )
    },
    enabled:
      !props.disabled &&
      (['customer', 'username'].includes(props.field) ||
        Boolean(props.start && props.end && props.end >= props.start)),
    getNextPageParam: (page) => page.next_cursor || undefined,
    staleTime: 30_000,
  })
  const options = [
    ...new Map(
      query.data?.pages
        .flatMap((page) => page.items)
        .map((item) => [item.value, item]) ?? []
    ).values(),
  ]
  if (
    selected?.value === props.value &&
    !options.some((item) => item.value === props.value)
  ) {
    options.unshift(selected)
  }
  return (
    <div className='space-y-1'>
      <div className='flex items-center gap-1'>
        <div className='min-w-0 flex-1'>
          <Label htmlFor={id} className='sr-only'>
            {props.label}
          </Label>
          <ComboboxInput
            id={id}
            disabled={props.disabled}
            options={options}
            value={props.value}
            placeholder={props.label}
            onSearchValueChange={setSearch}
            emptyText={
              query.isFetching ? t('Loading...') : t('No results found')
            }
            onValueChange={(value) => {
              const option = options.find((item) => item.value === value)
              setSelected(option)
              if (option) props.onSelect?.(option)
              props.onChange(value)
            }}
            dropdownFooter={
              query.hasNextPage ? (
                <div className='border-t p-1'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='w-full'
                    disabled={query.isFetchingNextPage}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => void query.fetchNextPage()}
                  >
                    {query.isFetchingNextPage
                      ? t('Loading...')
                      : t('Load more')}
                  </Button>
                </div>
              ) : undefined
            }
          />
        </div>
        {props.value && (
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.disabled}
            onClick={() => props.onChange('')}
          >
            {t('Clear')}
          </Button>
        )}
      </div>
      {query.isError && (
        <div className='flex flex-wrap items-center gap-1'>
          <p className='text-destructive text-xs' role='alert'>
            {query.error.message}
          </p>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => void query.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
    </div>
  )
}
