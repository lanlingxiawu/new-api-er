import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { useDebounce } from '@/hooks/use-debounce'

import { getChannelExclusionOptions } from '../api'

type Props = {
  id?: string
  value: number[]
  onChange: (ids: number[]) => void
  disabled?: boolean
}

// The shared multi-select owns searching, chips and keyboard interaction.
// This wrapper only supplies bounded, credential-free channel metadata.
export function UserChannelExclusionsField(props: Props) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const keyword = useDebounce(search.trim(), 300)
  const searchPending = search.trim() !== keyword
  const selectedKey = props.value.join(',')
  const pages = useInfiniteQuery({
    queryKey: ['channel-exclusion-options', 'list', keyword],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      getChannelExclusionOptions({ after_id: pageParam, keyword }, signal),
    getNextPageParam: (last) =>
      last.has_more ? last.next_after_id : undefined,
    staleTime: 30_000,
    retry: false,
  })
  const selected = useQuery({
    queryKey: ['channel-exclusion-options', 'selected', selectedKey],
    queryFn: ({ signal }) =>
      getChannelExclusionOptions({ ids: selectedKey }, signal),
    enabled: props.value.length > 0,
    staleTime: 30_000,
  })
  const options = useMemo(() => {
    const channels = new Map(
      pages.data?.pages
        .flatMap((page) => page.items)
        .map((item) => [item.id, item])
    )
    for (const item of selected.data?.items ?? []) channels.set(item.id, item)
    const result = Array.from(channels.values(), (item) => ({
      value: String(item.id),
      label: `#${item.id} ${item.name}`,
    }))
    for (const id of props.value) {
      if (!channels.has(id)) result.push({ value: String(id), label: `#${id}` })
    }
    return result
  }, [pages.data, selected.data, props.value])

  return (
    <div className='space-y-2'>
      <MultiSelect
        id={props.id}
        options={options}
        selected={props.value.map(String)}
        onChange={(ids) => props.onChange(ids.map(Number))}
        disabled={props.disabled}
        maxVisibleChips={8}
        searchByLabel
        onSearchChange={setSearch}
        onListScroll={(event) => {
          const list = event.currentTarget
          if (
            !searchPending &&
            pages.hasNextPage &&
            !pages.isFetching &&
            !pages.isError &&
            list.scrollHeight - list.scrollTop - list.clientHeight < 48
          ) {
            void pages.fetchNextPage({ cancelRefetch: false })
          }
        }}
        emptyText={
          searchPending || pages.isPending ? t('Loading...') : undefined
        }
        dropdownFooter={
          <>
            {(pages.isFetching || searchPending) && (
              <p
                role='status'
                className='text-muted-foreground px-3 py-2 text-xs'
              >
                {t('Loading...')}
              </p>
            )}
            {!pages.isFetching &&
              !searchPending &&
              (pages.isError || selected.isError) && (
                <div className='border-t p-2'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={() => {
                      if (pages.isFetchNextPageError) {
                        void pages.fetchNextPage({ cancelRefetch: false })
                      } else if (pages.isError) {
                        void pages.refetch()
                      }
                      if (selected.isError) void selected.refetch()
                    }}
                  >
                    {t('Retry')}
                  </Button>
                </div>
              )}
          </>
        }
        placeholder={t('Select channels to exclude')}
      />
      <p className='text-muted-foreground text-xs'>
        {t('Search channels by name or ID. Maximum 256 selections.')}
      </p>
    </div>
  )
}
