import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { getApiKeys, searchApiKeys } from '@/features/keys/api'
import { getUserGroups, getUserModels } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

// Suggestions come from the current user's existing APIs. Historical names
// remain valid even when the model, token or group is no longer available.
export function SelfExportFilterSelect(props: {
  id: string
  field: 'model_name' | 'token_name' | 'group'
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setKeyword(search.trim()), 300)
    return () => clearTimeout(timer)
  }, [search])
  const query = useInfiniteQuery({
    queryKey: ['self-export-options', userId, props.field, keyword],
    initialPageParam: 1,
    queryFn: async ({ pageParam }) => {
      if (props.field === 'model_name') {
        const response = requireServerSuccess(await getUserModels())
        return { values: response.data ?? [], nextPage: undefined }
      }
      if (props.field === 'group') {
        const response = requireServerSuccess(await getUserGroups())
        return {
          values: Object.keys(response.data ?? {}).filter(
            (group) => group !== 'auto'
          ),
          nextPage: undefined,
        }
      }
      const response = requireServerSuccess(
        await (keyword
          ? searchApiKeys({ keyword, p: pageParam, size: 50 })
          : getApiKeys({ p: pageParam, size: 50 }))
      )
      return {
        values: (response.data?.items ?? []).map((token) => token.name),
        nextPage:
          pageParam * 50 < (response.data?.total ?? 0)
            ? pageParam + 1
            : undefined,
      }
    },
    getNextPageParam: (page) => page.nextPage,
    staleTime: 30_000,
  })
  const options = [
    ...new Set(query.data?.pages.flatMap((page) => page.values) ?? []),
  ]
    .filter(Boolean)
    .map((value) => ({ label: value, value }))
  return (
    <div className='space-y-1'>
      <ComboboxInput
        id={props.id}
        options={options}
        value={props.value}
        onValueChange={props.onChange}
        onSearchValueChange={
          props.field === 'token_name' ? setSearch : undefined
        }
        disabled={props.disabled}
        allowCustomValue
        placeholder={t('Select a suggestion or enter a historical value')}
        emptyText={query.isFetching ? t('Loading...') : t('No results found')}
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
                {query.isFetchingNextPage ? t('Loading...') : t('Load more')}
              </Button>
            </div>
          ) : undefined
        }
      />
      {query.isError && (
        <div className='flex flex-wrap items-center gap-1'>
          <p role='alert' className='text-destructive text-xs'>
            {t('Failed to load')}
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
