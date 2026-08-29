import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { ArrowRight, Database, Eye, Loader2 } from 'lucide-react'
import {
  type SetStateAction,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  DataTableColumnHeader,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import dayjs from '@/lib/dayjs'
import { formatLogQuota, formatUseTime } from '@/lib/format'

import { getUpstreamLogChannels, queryUpstreamLog } from '../api'
import { CompactDateTimeRangePicker } from '../components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '../components/logs-filter-toolbar'
import { LOG_TYPE_ALL_VALUE, LOG_TYPE_FILTERS } from '../constants'
import { getDefaultTimeRange, getLogTypeConfig } from '../lib/utils'
import type {
  UpstreamLogChannelOption,
  UpstreamLogItem,
  UpstreamLogQueryRequest,
  UpstreamLogScope,
} from '../types'
import { UpstreamLogDetailsDialog } from './upstream-log-details-dialog'

const route = getRouteApi('/_authenticated/usage-logs/$section')

type UpstreamLogDraft = {
  localRequestId: string
  upstreamRequestId: string
  channelId: number
  keyIndex?: number
  type: string
  username: string
  tokenName: string
  modelName: string
  group: string
  startTime?: Date
  endTime?: Date
}

let upstreamDraftSnapshot: UpstreamLogDraft | null = null

type SubmittedQuery = Omit<UpstreamLogQueryRequest, 'page' | 'page_size'>

function createSubmittedQuery(
  draft: UpstreamLogDraft,
  includeKeyIndex: boolean
): SubmittedQuery | null {
  const localRequestId = draft.localRequestId.trim()
  if (localRequestId) return { local_request_id: localRequestId }
  if (!draft.channelId) return null

  return {
    channel_id: draft.channelId,
    key_index: includeKeyIndex ? draft.keyIndex : undefined,
    filters: {
      type: draft.type === LOG_TYPE_ALL_VALUE ? undefined : Number(draft.type),
      username: draft.username.trim() || undefined,
      token_name: draft.tokenName.trim() || undefined,
      model_name: draft.modelName.trim() || undefined,
      group: draft.group.trim() || undefined,
      request_id: draft.upstreamRequestId.trim() || undefined,
      start_timestamp: draft.startTime
        ? Math.floor(draft.startTime.getTime() / 1000)
        : undefined,
      end_timestamp: draft.endTime
        ? Math.floor(draft.endTime.getTime() / 1000)
        : undefined,
    },
  }
}

function scopeBadge(
  scope: UpstreamLogScope,
  t: (key: string) => string
): { label: string; variant: StatusBadgeProps['variant'] } {
  if (scope === 'exact') {
    return { label: t('Exact upstream query'), variant: 'green' }
  }
  if (scope === 'filtered') {
    return { label: t('Upstream filters applied'), variant: 'blue' }
  }
  return { label: t('Recent results only'), variant: 'amber' }
}

async function copyText(value: string, copiedMessage: string) {
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    toast.success(copiedMessage)
  } catch {
    // Clipboard access can be unavailable in non-secure browser contexts.
  }
}

function UpstreamLogsMobileList({
  items,
  isLoading,
  emptyTitle,
  emptyDescription,
  onView,
}: {
  items: UpstreamLogItem[]
  isLoading: boolean
  emptyTitle: string
  emptyDescription: string
  onView: (item: UpstreamLogItem) => void
}) {
  const { t } = useTranslation()

  if (isLoading) {
    return (
      <div className='space-y-2'>
        {[0, 1, 2].map((key) => (
          <Skeleton key={key} className='h-32 w-full rounded-lg' />
        ))}
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className='rounded-lg border p-6'>
        <Empty className='border-none p-0'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Database className='size-5' />
            </EmptyMedia>
            <EmptyTitle>{emptyTitle}</EmptyTitle>
            <EmptyDescription>{emptyDescription}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  return (
    <div className='border-border/50 bg-card overflow-hidden rounded-lg border'>
      {items.map((item) => {
        const typeConfig = getLogTypeConfig(item.type)
        return (
          <article
            key={`${item.id}-${item.request_id}`}
            className='border-border/40 space-y-2 border-b p-3 last:border-b-0'
          >
            <div className='flex items-start justify-between gap-2'>
              <div className='min-w-0'>
                <p className='truncate text-sm font-medium'>
                  {item.model_name || '-'}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {dayjs.unix(item.created_at).format('MM-DD HH:mm:ss')}
                </p>
              </div>
              <StatusBadge
                label={t(typeConfig.label)}
                variant={typeConfig.color as StatusBadgeProps['variant']}
                size='sm'
              />
            </div>
            <button
              type='button'
              className='hover:text-primary w-full text-left font-mono text-xs break-all'
              onClick={() => void copyText(item.request_id, t('Copied'))}
            >
              {item.request_id || '-'}
            </button>
            <div className='text-muted-foreground grid grid-cols-3 gap-2 text-xs'>
              <span>{`${item.prompt_tokens}/${item.completion_tokens}`}</span>
              <span>{`${t('Quota')}: ${formatLogQuota(item.quota)}`}</span>
              <span>{formatUseTime(item.use_time)}</span>
            </div>
            {item.content && (
              <p className='text-destructive line-clamp-2 text-xs break-all'>
                {item.content}
              </p>
            )}
            <div className='flex justify-end'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={() => onView(item)}
              >
                <Eye />
                {t('Details')}
              </Button>
            </div>
          </article>
        )
      })}
    </div>
  )
}

interface UpstreamLogsPageProps {
  isWorkspaceActive?: boolean
}

export function UpstreamLogsPage({
  isWorkspaceActive = true,
}: UpstreamLogsPageProps) {
  const search = route.useSearch()
  const workspaceKey = JSON.stringify([
    search.upstreamLocalRequestId,
    search.upstreamFilterRequestId,
    search.upstreamChannel,
    search.upstreamFilterKeyIndex,
    search.upstreamType,
    search.upstreamUsername,
    search.upstreamToken,
    search.upstreamModel,
    search.upstreamGroup,
    search.upstreamStartTime,
    search.upstreamEndTime,
  ])

  return (
    <UpstreamLogsWorkspace
      key={workspaceKey}
      isWorkspaceActive={isWorkspaceActive}
    />
  )
}

function UpstreamLogsWorkspace({
  isWorkspaceActive,
}: Required<UpstreamLogsPageProps>) {
  const { t } = useTranslation()
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const [draft, setDraftState] = useState<UpstreamLogDraft>(() => {
    const range = getDefaultTimeRange()
    const hasCommittedFilters = Boolean(
      search.upstreamLocalRequestId ||
        search.upstreamFilterRequestId ||
        search.upstreamChannel ||
        search.upstreamFilterKeyIndex != null ||
        search.upstreamType?.length ||
        search.upstreamUsername ||
        search.upstreamToken ||
        search.upstreamModel ||
        search.upstreamGroup ||
        search.upstreamStartTime ||
        search.upstreamEndTime
    )
    if (!hasCommittedFilters && upstreamDraftSnapshot) {
      return upstreamDraftSnapshot
    }
    const initialDraft = {
      localRequestId: search.upstreamLocalRequestId || '',
      upstreamRequestId: search.upstreamFilterRequestId || '',
      channelId: Number(search.upstreamChannel) || 0,
      keyIndex: search.upstreamFilterKeyIndex,
      type: search.upstreamType?.[0] ?? LOG_TYPE_ALL_VALUE,
      username: search.upstreamUsername || '',
      tokenName: search.upstreamToken || '',
      modelName: search.upstreamModel || '',
      group: search.upstreamGroup || '',
      startTime: search.upstreamStartTime
        ? new Date(search.upstreamStartTime)
        : range.start,
      endTime: search.upstreamEndTime
        ? new Date(search.upstreamEndTime)
        : range.end,
    }
    upstreamDraftSnapshot = initialDraft
    return initialDraft
  })
  const setDraft = useCallback(
    (action: SetStateAction<UpstreamLogDraft>) => {
      setDraftState((current) => {
        const next =
          typeof action === 'function' ? action(current) : action
        upstreamDraftSnapshot = next
        return next
      })
    },
    []
  )
  const [submitted, setSubmitted] = useState<SubmittedQuery | null>(() =>
    createSubmittedQuery(draft, draft.keyIndex != null)
  )
  const pagination = useMemo<PaginationState>(
    () => ({
      pageIndex: Math.max((search.upstreamPage ?? 1) - 1, 0),
      pageSize: search.upstreamPageSize ?? 20,
    }),
    [search.upstreamPage, search.upstreamPageSize]
  )
  const [selectedItem, setSelectedItem] = useState<UpstreamLogItem | null>(null)
  const [channelSearchInput, setChannelSearchInput] = useState(
    draft.channelId ? String(draft.channelId) : ''
  )
  const [channelKeyword, setChannelKeyword] = useState(channelSearchInput)
  const [selectedChannel, setSelectedChannel] =
    useState<UpstreamLogChannelOption | null>(null)
  const errorShownRef = useRef<unknown>(null)
  const channelErrorShownRef = useRef<unknown>(null)

  useEffect(() => {
    const timer = window.setTimeout(
      () => setChannelKeyword(channelSearchInput.trim()),
      250
    )
    return () => window.clearTimeout(timer)
  }, [channelSearchInput])

  const channelsQuery = useQuery({
    queryKey: ['upstream-log-channels', channelKeyword],
    queryFn: async ({ signal }) => {
      const response = await getUpstreamLogChannels(channelKeyword, signal)
      if (!response.success) {
        throw new Error(response.message || t('Failed to load channels'))
      }
      return response.data ?? []
    },
    staleTime: 30_000,
  })

  useEffect(() => {
    if (
      !channelsQuery.error ||
      channelErrorShownRef.current === channelsQuery.error
    ) {
      return
    }
    channelErrorShownRef.current = channelsQuery.error
    toast.error(channelsQuery.error.message || t('Failed to load channels'))
  }, [channelsQuery.error, t])

  const channelFromResults = channelsQuery.data?.find(
    (channel) => channel.id === draft.channelId
  )
  const resolvedChannel = channelFromResults ?? selectedChannel

  useEffect(() => {
    if (channelFromResults) setSelectedChannel(channelFromResults)
  }, [channelFromResults])

  const channelOptions = useMemo(() => {
    const channels = channelsQuery.data ?? []
    const merged =
      resolvedChannel &&
      !channels.some((item) => item.id === resolvedChannel.id)
        ? [resolvedChannel, ...channels]
        : channels
    return merged.map((channel) => ({
      value: String(channel.id),
      label: `${channel.name} (#${channel.id})`,
    }))
  }, [channelsQuery.data, resolvedChannel])

  const logTypeItems = useMemo(
    () =>
      LOG_TYPE_FILTERS.map((item) => ({
        value: item.value,
        label: t(item.label),
      })),
    [t]
  )
  const isMultiKey = resolvedChannel?.is_multi_key ?? false
  const keyCount = Math.max(resolvedChannel?.key_count ?? 0, 1)
  // 填了本站请求 ID 就走反查：渠道、令牌序号、上游请求 ID 全部由后端按日志判定，
  // 其余条件不会生效，所以直接禁用它们，而不是让用户填完才发现被忽略。
  const traceMode = draft.localRequestId.trim().length > 0

  const buildSubmittedQuery = useCallback((): SubmittedQuery | null => {
    const localRequestId = draft.localRequestId.trim()
    if (localRequestId) return { local_request_id: localRequestId }
    if (!draft.channelId) {
      toast.error(t('Select a channel to query upstream logs'))
      return null
    }
    if (isMultiKey && draft.keyIndex == null) {
      toast.error(t('Select a channel key'))
      return null
    }
    if (
      draft.startTime &&
      draft.endTime &&
      draft.startTime.getTime() > draft.endTime.getTime()
    ) {
      toast.error(t('The start time must be earlier than the end time.'))
      return null
    }

    return createSubmittedQuery(draft, isMultiKey)
  }, [draft, isMultiKey, t])

  const logsQuery = useQuery({
    queryKey: [
      'upstream-logs',
      submitted,
      pagination.pageIndex,
      pagination.pageSize,
    ],
    queryFn: async ({ signal }) => {
      if (!submitted) throw new Error(t('Query parameters are incomplete.'))
      const response = await queryUpstreamLog(
        {
          ...submitted,
          page: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        },
        signal
      )
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Request failed'))
      }
      return response.data
    },
    enabled: submitted != null,
    retry: false,
  })

  const handleSearch = useCallback(() => {
    const next = buildSubmittedQuery()
    if (!next) return
    const sameQuery = JSON.stringify(next) === JSON.stringify(submitted)
    setSubmitted(next)
    void navigate({
      to: '/usage-logs/$section',
      params: { section: 'upstream' },
      replace: true,
      search: {
        ...search,
        upstreamPage: 1,
        upstreamPageSize: pagination.pageSize,
        upstreamType:
          draft.type === LOG_TYPE_ALL_VALUE ? undefined : [draft.type],
        upstreamModel: draft.modelName || undefined,
        upstreamToken: draft.tokenName || undefined,
        upstreamChannel: draft.channelId ? String(draft.channelId) : undefined,
        upstreamGroup: draft.group || undefined,
        upstreamUsername: draft.username || undefined,
        upstreamLocalRequestId: draft.localRequestId || undefined,
        upstreamFilterRequestId: draft.upstreamRequestId || undefined,
        upstreamStartTime: draft.startTime?.getTime(),
        upstreamEndTime: draft.endTime?.getTime(),
        upstreamFilterKeyIndex: isMultiKey ? draft.keyIndex : undefined,
      },
    })
    if (sameQuery) void logsQuery.refetch()
  }, [
    buildSubmittedQuery,
    draft,
    isMultiKey,
    logsQuery,
    navigate,
    pagination.pageSize,
    search,
    submitted,
  ])

  useEffect(() => {
    if (!logsQuery.error || errorShownRef.current === logsQuery.error) return
    errorShownRef.current = logsQuery.error
    toast.error(logsQuery.error.message || t('Request failed'))
  }, [logsQuery.error, t])

  const openDetails = useCallback((item: UpstreamLogItem) => {
    setSelectedItem(item)
  }, [])

  const columns = useMemo<ColumnDef<UpstreamLogItem>[]>(
    () => [
      {
        accessorKey: 'created_at',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Created')} />
        ),
        cell: ({ row }) => (
          <span className='whitespace-nowrap'>
            {dayjs.unix(row.original.created_at).format('MM-DD HH:mm:ss')}
          </span>
        ),
        size: 130,
      },
      {
        accessorKey: 'type',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Type')} />
        ),
        cell: ({ row }) => {
          const config = getLogTypeConfig(row.original.type)
          return (
            <StatusBadge
              label={t(config.label)}
              variant={config.color as StatusBadgeProps['variant']}
              size='sm'
            />
          )
        },
        size: 90,
      },
      {
        accessorKey: 'model_name',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Model')} />
        ),
        cell: ({ row }) => (
          <span className='block max-w-48 truncate font-medium'>
            {row.original.model_name || '-'}
          </span>
        ),
      },
      {
        accessorKey: 'request_id',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Request ID')} />
        ),
        cell: ({ row }) => (
          <button
            type='button'
            className='hover:text-primary block max-w-64 truncate text-left font-mono text-xs'
            title={row.original.request_id}
            onClick={() => void copyText(row.original.request_id, t('Copied'))}
          >
            {row.original.request_id || '-'}
          </button>
        ),
      },
      {
        accessorKey: 'token_name',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Token')} />
        ),
        cell: ({ row }) => row.original.token_name || '-',
      },
      {
        id: 'tokens',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Tokens')} />
        ),
        cell: ({ row }) =>
          `${row.original.prompt_tokens}/${row.original.completion_tokens}`,
        size: 100,
      },
      {
        accessorKey: 'quota',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Quota')} />
        ),
        cell: ({ row }) => (
          <span className='whitespace-nowrap tabular-nums'>
            {formatLogQuota(row.original.quota)}
          </span>
        ),
        size: 100,
      },
      {
        accessorKey: 'use_time',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Duration')} />
        ),
        cell: ({ row }) => (
          <span className='whitespace-nowrap tabular-nums'>
            {formatUseTime(row.original.use_time)}
          </span>
        ),
        size: 90,
      },
      {
        id: 'actions',
        header: () => <span className='sr-only'>{t('Actions')}</span>,
        cell: ({ row }) => (
          <Button
            type='button'
            variant='ghost'
            size='icon'
            aria-label={t('Details')}
            onClick={() => openDetails(row.original)}
          >
            <Eye />
          </Button>
        ),
        size: 52,
      },
    ],
    [openDetails, t]
  )

  const items = logsQuery.data?.items ?? []
  const handlePaginationChange = useCallback(
    (
      updater: PaginationState | ((current: PaginationState) => PaginationState)
    ) => {
      const next = typeof updater === 'function' ? updater(pagination) : updater
      void navigate({
        to: '/usage-logs/$section',
        params: { section: 'upstream' },
        replace: true,
        search: {
          ...search,
          upstreamPage: next.pageIndex + 1,
          upstreamPageSize: next.pageSize,
        },
      })
    },
    [navigate, pagination, search]
  )
  const { table } = useDataTable({
    data: items,
    columns,
    pagination,
    onPaginationChange: handlePaginationChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: logsQuery.data?.total ?? 0,
    enableRowSelection: false,
    columnVisibilityStorageKey: 'usage-logs:upstream:column-visibility',
  })

  const handleChannelChange = useCallback(
    (value: string) => {
      const channelId = Number(value) || 0
      const channel = channelsQuery.data?.find((item) => item.id === channelId)
      setSelectedChannel(channel ?? null)
      setDraft((current) => ({
        ...current,
        channelId,
        keyIndex: undefined,
      }))
      setChannelSearchInput('')
    },
    [channelsQuery.data, setDraft]
  )

  const handleReset = useCallback(() => {
    const range = getDefaultTimeRange()
    setDraft({
      localRequestId: '',
      upstreamRequestId: '',
      channelId: 0,
      keyIndex: undefined,
      type: LOG_TYPE_ALL_VALUE,
      username: '',
      tokenName: '',
      modelName: '',
      group: '',
      startTime: range.start,
      endTime: range.end,
    })
    setSelectedChannel(null)
    setSubmitted(null)
    setChannelSearchInput('')
    void navigate({
      to: '/usage-logs/$section',
      params: { section: 'upstream' },
      replace: true,
      search: {
        ...search,
        upstreamPage: 1,
        upstreamPageSize: undefined,
        upstreamType: undefined,
        upstreamModel: undefined,
        upstreamToken: undefined,
        upstreamChannel: undefined,
        upstreamGroup: undefined,
        upstreamUsername: undefined,
        upstreamLocalRequestId: undefined,
        upstreamFilterRequestId: undefined,
        upstreamStartTime: undefined,
        upstreamEndTime: undefined,
        upstreamFilterKeyIndex: undefined,
      },
    })
  }, [navigate, search, setDraft])

  const requestField = (
    <LogsFilterField wide>
      <LogsFilterInput
        value={draft.localRequestId}
        placeholder={t('Local request ID')}
        aria-label={t('Local request ID')}
        onChange={(event) =>
          setDraft((current) => ({
            ...current,
            localRequestId: event.target.value,
          }))
        }
        onKeyDown={(event) => {
          if (event.key === 'Enter') handleSearch()
        }}
      />
    </LogsFilterField>
  )
  const upstreamRequestField = (
    <LogsFilterField wide>
      <LogsFilterInput
        value={draft.upstreamRequestId}
        placeholder={t('Upstream request ID')}
        aria-label={t('Upstream Request ID')}
        disabled={traceMode}
        title={
          traceMode
            ? t('Resolved automatically from the local request ID.')
            : undefined
        }
        onChange={(event) =>
          setDraft((current) => ({
            ...current,
            upstreamRequestId: event.target.value,
          }))
        }
        onKeyDown={(event) => {
          if (event.key === 'Enter') handleSearch()
        }}
      />
    </LogsFilterField>
  )
  const channelField = (
    <LogsFilterField wide>
      <ComboboxInput
        options={channelOptions}
        value={draft.channelId ? String(draft.channelId) : ''}
        onValueChange={handleChannelChange}
        onSearchValueChange={setChannelSearchInput}
        placeholder={
          traceMode
            ? t('Resolved from the local request ID')
            : t('Select a New API channel')
        }
        emptyText={t('No matching channel')}
        disabled={traceMode}
        className='h-8'
      />
    </LogsFilterField>
  )
  const dateField = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        disabled={traceMode}
        start={draft.startTime}
        end={draft.endTime}
        onChange={({ start, end }) =>
          setDraft((current) => ({
            ...current,
            startTime: start,
            endTime: end,
          }))
        }
        className='w-full'
      />
    </LogsFilterField>
  )
  // 多令牌渠道必须选序号才能查询，放进折叠的高级筛选里会让用户对着看不见的字段报错。
  const keyField =
    isMultiKey && !traceMode ? (
      <LogsFilterField>
        <Select
          value={draft.keyIndex == null ? '' : String(draft.keyIndex)}
          onValueChange={(value) => {
            setDraft((current) => ({ ...current, keyIndex: Number(value) }))
          }}
        >
          <SelectTrigger className='h-8'>
            <SelectValue placeholder={t('Select a channel key')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {Array.from({ length: keyCount }, (_, index) => (
                <SelectItem key={index} value={String(index)}>
                  {t('Channel key #{{index}}', { index })}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </LogsFilterField>
    ) : null
  const advancedFilters = (
    <>
      {dateField}
      <LogsFilterField>
        <Select
          items={logTypeItems}
          value={draft.type}
          disabled={traceMode}
          onValueChange={(value) =>
            setDraft((current) => ({
              ...current,
              type: value ?? LOG_TYPE_ALL_VALUE,
            }))
          }
        >
          <SelectTrigger className='h-8'>
            <SelectValue placeholder={t('Type')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {logTypeItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          value={draft.username}
          disabled={traceMode}
          placeholder={t('Username')}
          onChange={(event) =>
            setDraft((current) => ({
              ...current,
              username: event.target.value,
            }))
          }
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          value={draft.tokenName}
          disabled={traceMode}
          placeholder={t('Token Name')}
          onChange={(event) =>
            setDraft((current) => ({
              ...current,
              tokenName: event.target.value,
            }))
          }
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          value={draft.modelName}
          disabled={traceMode}
          placeholder={t('Model')}
          onChange={(event) =>
            setDraft((current) => ({
              ...current,
              modelName: event.target.value,
            }))
          }
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          value={draft.group}
          disabled={traceMode}
          placeholder={t('Group')}
          onChange={(event) =>
            setDraft((current) => ({ ...current, group: event.target.value }))
          }
        />
      </LogsFilterField>
    </>
  )
  const activeAdvancedCount = [
    draft.type !== LOG_TYPE_ALL_VALUE,
    draft.username,
    draft.tokenName,
    draft.modelName,
    draft.group,
  ].filter(Boolean).length
  const hasActiveFilters = Boolean(
    draft.localRequestId ||
    draft.upstreamRequestId ||
    draft.channelId ||
    draft.keyIndex != null ||
    activeAdvancedCount
  )
  const isRecentFallback = logsQuery.data?.query.scope === 'recent_fallback'
  // 近期降级只能拿到上游最近一页日志，用户名/分组这类字段不在返回体里，无法本地过滤。
  const droppedFallbackFilters = isRecentFallback
    ? [
        submitted?.filters?.username ? t('Username') : '',
        submitted?.filters?.group ? t('Group') : '',
      ].filter(Boolean)
    : []
  const queryBadge = logsQuery.data
    ? scopeBadge(logsQuery.data.query.scope, t)
    : null
  const queryStats = logsQuery.data && queryBadge && (
    <div className='flex flex-wrap items-center gap-2 text-xs'>
      <StatusBadge
        label={queryBadge.label}
        variant={queryBadge.variant}
        size='sm'
      />
      <span className='text-muted-foreground'>
        {logsQuery.data.channel.name} (#{logsQuery.data.channel.id})
      </span>
      {logsQuery.data.source && (
        <span className='text-muted-foreground flex min-w-0 items-center gap-1.5'>
          <span
            className='max-w-48 truncate font-mono'
            title={logsQuery.data.source.request_id}
          >
            {logsQuery.data.source.request_id}
          </span>
          <ArrowRight className='size-3.5 shrink-0' />
          <span
            className='max-w-48 truncate font-mono'
            title={logsQuery.data.source.upstream_request_id}
          >
            {logsQuery.data.source.upstream_request_id}
          </span>
        </span>
      )}
      <span className='text-muted-foreground'>
        {t('{{count}} results · {{elapsed}} ms', {
          count: logsQuery.data.total,
          elapsed: logsQuery.data.elapsed_ms,
        })}
      </span>
      {logsQuery.data.source?.key_index_from_log === false && (
        <StatusBadge
          label={t('Channel key not recorded in the local log')}
          variant='amber'
          size='sm'
        />
      )}
    </div>
  )
  let hintText = t(
    'Select a channel to browse its upstream logs, or enter a local request ID to trace one request.'
  )
  if (traceMode) {
    hintText = t(
      'Tracing by local request ID: the channel, channel key and upstream request ID are resolved from the local log, so the other filters are ignored.'
    )
  } else if (draft.upstreamRequestId.trim()) {
    hintText = t(
      'Looking up this upstream request ID inside the selected channel.'
    )
  }
  const toolbarStats = queryStats ?? (
    <span className='text-muted-foreground text-xs'>{hintText}</span>
  )

  const primaryFilters = (
    <>
      {channelField}
      {keyField}
      {requestField}
      {upstreamRequestField}
    </>
  )

  const toolbar = (
    <>
      <LogsFilterToolbar
        table={table}
        primaryFilters={primaryFilters}
        advancedFilters={advancedFilters}
        mobilePinnedFilters={primaryFilters}
        mobileFilters={advancedFilters}
        mobileFilterCount={activeAdvancedCount}
        stats={toolbarStats}
        hasActiveFilters={hasActiveFilters}
        hasAdvancedActiveFilters={activeAdvancedCount > 0}
        advancedFilterCount={activeAdvancedCount}
        searchLoading={logsQuery.isFetching}
        onReset={handleReset}
        onSearch={handleSearch}
      />
      {channelsQuery.isLoading && draft.channelId > 0 && (
        <div className='text-muted-foreground flex items-center gap-2 text-xs'>
          <Loader2 className='size-3.5 animate-spin' />
          {t('Loading channel information...')}
        </div>
      )}
      {logsQuery.data?.query.scope === 'recent_fallback' && (
        <Alert>
          <AlertTitle>{t('Recent upstream results')}</AlertTitle>
          <AlertDescription>
            {t(
              'The upstream instance does not support exact filters yet, so only its most recent logs were matched locally and there are no further pages.'
            )}
            {droppedFallbackFilters.length > 0 &&
              ` ${t('These filters could not be applied: {{filters}}.', {
                filters: droppedFallbackFilters.join(', '),
              })}`}
          </AlertDescription>
        </Alert>
      )}
    </>
  )

  const emptyTitle = submitted
    ? t('No upstream logs found')
    : t('Search upstream logs')
  let emptyDescription = t(
    'Enter a local request ID, or select a New API channel to browse upstream logs.'
  )
  if (submitted?.local_request_id) {
    emptyDescription = t(
      'The upstream instance has no log for this request. It may have been trimmed by the upstream retention policy.'
    )
  } else if (submitted) {
    emptyDescription = t(
      'Check the request ID or adjust the channel filters, then search again.'
    )
  }

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={logsQuery.isLoading}
        isFetching={logsQuery.isFetching}
        emptyTitle={emptyTitle}
        emptyDescription={emptyDescription}
        toolbar={toolbar}
        mobile={
          <UpstreamLogsMobileList
            items={items}
            isLoading={logsQuery.isLoading}
            emptyTitle={emptyTitle}
            emptyDescription={emptyDescription}
            onView={openDetails}
          />
        }
        applyHeaderSize
        showPagination={Boolean(
          isWorkspaceActive &&
            submitted &&
            logsQuery.data?.total &&
            !isRecentFallback
        )}
        tableClassName='[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:py-2'
      />

      <UpstreamLogDetailsDialog
        item={selectedItem}
        open={selectedItem != null}
        onOpenChange={(open) => {
          if (!open) setSelectedItem(null)
        }}
      />
    </>
  )
}
