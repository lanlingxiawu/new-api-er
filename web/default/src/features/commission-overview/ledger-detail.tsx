import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type UIEvent,
} from 'react'
import axios from 'axios'
import { Download, List, Loader2, RefreshCw, Search, Undo2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'
import { useTableCompactMode } from '@/hooks/use-table-compact-mode'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { BusinessAmount } from '@/features/business/amount-display'
import { UsageLogIdHover } from '@/features/employees/components/usage-log-id-hover'
import {
  createLedgerExport,
  getConsumptionCostLedger,
  getConsumptionCostLedgerStats,
  getLedgerExportDownloadURL,
  getLedgerExportStatus,
  reverseLedgerRecord,
  type ConsumptionCostLedgerItem,
  type FallbackHint,
  type LedgerCursor,
  type LedgerExportJob,
  type LedgerListParams,
  type LedgerStats,
  type LedgerTag,
} from './api'
import { FallbackBackfillBanner } from './fallback-backfill-banner'

const LEDGER_PAGE_SIZE = 100
const LEDGER_STATS_AUTO_REFRESH_LIMIT = 100
const LEDGER_STATS_AUTO_REFRESH_DELAY_MS = 3000
const LEDGER_EXPORT_POLLING_DELAY_MS = 3000
const LEDGER_DATE_TIME_FORMAT = 'YYYY-MM-DDTHH:mm:ss'
const LEDGER_MAX_RANGE_SECONDS = 24 * 60 * 60 - 1
const LEDGER_EMPTY_PAGE_AUTO_ADVANCE_LIMIT = 5

function clampLedgerDateTimeToNow(value: dayjs.Dayjs): dayjs.Dayjs {
  const now = dayjs()
  return value.isSame(now, 'day') && value.isAfter(now) ? now : value
}

interface LedgerFilters {
  start_time: string
  end_time: string
  id: string
  log_id: string
  user_id: string
  channel_id: string
  model_name: string
  group_name: string
  tag: 'all' | LedgerTag
}

function defaultLedgerFilters(): LedgerFilters {
  const todayStart = dayjs().startOf('day')
  const todayEnd = clampLedgerDateTimeToNow(todayStart.endOf('day'))
  return {
    start_time: todayStart.format(LEDGER_DATE_TIME_FORMAT),
    end_time: todayEnd.format(LEDGER_DATE_TIME_FORMAT),
    id: '',
    log_id: '',
    user_id: '',
    channel_id: '',
    model_name: '',
    group_name: '',
    tag: 'all',
  }
}

function translateLedgerMessage(
  t: (key: string, options?: Record<string, unknown>) => string,
  message?: string,
  fallback = 'Request failed'
) {
  if (!message) return t(fallback)
  if (message === 'context deadline exceeded') {
    return t('Export timed out. Please narrow the time range and try again.')
  }
  const notReady = message.match(/^Export is not ready \(status: (.+)\)\.$/)
  if (notReady) {
    return t('Export is not ready (status: {{status}}).', {
      status: notReady[1],
    })
  }
  if (message.startsWith('invalid ')) {
    return t('Invalid {{param}}', { param: message.slice('invalid '.length) })
  }
  return t(message)
}

function parseDateTime(value: string): number | undefined {
  if (!value) return undefined
  const date = dayjs(value)
  if (!date.isValid()) return undefined
  return date.unix()
}

function parseEndDateTime(value: string): number | undefined {
  const timestamp = parseDateTime(value)
  return timestamp === undefined ? undefined : timestamp + 1
}

function parseNumber(value: string): number | undefined {
  if (value.trim() === '') return undefined
  const number = Number(value)
  return Number.isFinite(number) ? number : undefined
}

function buildLedgerParams(
  filters: LedgerFilters,
  cursor?: LedgerCursor | null
): LedgerListParams {
  const params: LedgerListParams = {
    start_time: parseDateTime(filters.start_time),
    end_time: parseEndDateTime(filters.end_time),
    limit: LEDGER_PAGE_SIZE,
  }
  const numericFields = ['id', 'log_id', 'user_id', 'channel_id'] as const
  numericFields.forEach((field) => {
    const value = parseNumber(filters[field])
    if (value !== undefined) {
      ;(params as Record<string, number>)[field] = value
    }
  })
  if (filters.model_name.trim()) params.model_name = filters.model_name.trim()
  if (filters.group_name.trim()) params.group_name = filters.group_name.trim()
  if (filters.tag !== 'all') params.tag = filters.tag
  if (cursor) {
    params.cursor_created_at = cursor.created_at
    params.cursor_id = cursor.id
  }
  return params
}

function formatTime(value: number) {
  if (!value) return '-'
  return dayjs.unix(value).format('YYYY-MM-DD HH:mm:ss')
}

function formatLoadedRowsTimeRange(rows: ConsumptionCostLedgerItem[]) {
  if (rows.length === 0) return '-'
  let start = rows[0]?.created_at ?? 0
  let end = start
  rows.forEach((row) => {
    if (row.created_at < start) start = row.created_at
    if (row.created_at > end) end = row.created_at
  })
  return `${formatTime(start)} - ${formatTime(end)}`
}

function formatPercent(value?: number | null) {
  if (value === null || value === undefined) return '-'
  return `${(value * 100).toFixed(1)}%`
}

function ratioText(value: number) {
  return Number(value || 0).toFixed(4)
}

function normalizeLedgerTag(tag: string) {
  return tag
    .trim()
    .toLowerCase()
    .replace(/[\s-]+/g, '_')
}

function tagBadgeClass(tag: string): string {
  switch (normalizeLedgerTag(tag)) {
    case 'profit':
      return 'border-green-300 bg-green-50 text-green-700 dark:border-green-700 dark:bg-green-950/40 dark:text-green-400'
    case 'loss':
      return 'border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-950/40 dark:text-red-400'
    case 'reversal':
      return 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-400'
    case 'zero_revenue':
      return 'border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-700 dark:bg-sky-950/40 dark:text-sky-400'
    default:
      return ''
  }
}

function getLedgerTags(tags: ConsumptionCostLedgerItem['tags']) {
  if (Array.isArray(tags)) return tags
  return []
}

function tagLabel(tag: string, t: (key: string) => string) {
  const map: Record<string, string> = {
    reversal: t('Reversal'),
    loss: t('Loss'),
    profit: t('Profit'),
    zero_revenue: t('Zero revenue'),
  }
  return map[normalizeLedgerTag(tag)] ?? tag
}

function tagFilterLabel(tag: LedgerFilters['tag'], t: (key: string) => string) {
  if (tag === 'all') return t('All')
  return tagLabel(tag, t)
}

function sortLedgerTags(tags: string[]) {
  const order: Record<string, number> = {
    loss: 0,
    profit: 1,
    reversal: 2,
    zero_revenue: 3,
  }
  return [...tags].sort(
    (a, b) =>
      (order[normalizeLedgerTag(a)] ?? 99) -
      (order[normalizeLedgerTag(b)] ?? 99)
  )
}

function visibleLedgerTags(row: ConsumptionCostLedgerItem) {
  return getLedgerTags(row.tags)
}

function computeLoadedSubtotal(rows: ConsumptionCostLedgerItem[]): LedgerStats {
  const stats: LedgerStats = {
    record_count: rows.length,
    total_revenue_quota: 0,
    total_cost_quota: 0,
    total_profit_quota: 0,
    gross_margin: null,
  }
  rows.forEach((row) => {
    stats.total_revenue_quota += row.revenue_quota
    stats.total_cost_quota += row.cost_quota
    stats.total_profit_quota += row.profit_quota
  })
  if (stats.total_revenue_quota !== 0) {
    stats.gross_margin = stats.total_profit_quota / stats.total_revenue_quota
  }
  return stats
}

function StatStrip(props: {
  title: string
  stats?: LedgerStats | null
  timeRange?: string
}) {
  const { t } = useTranslation()
  const stats = props.stats
  return (
    <div className='bg-muted/20 overflow-hidden rounded-lg border px-4 py-1.5'>
      <div className='flex flex-wrap items-center gap-x-5 gap-y-1'>
        {props.timeRange ? (
          <MiniStat label={t('Date Range')} value={props.timeRange} />
        ) : null}
        <MiniStat
          label={props.title}
          value={String(stats?.record_count ?? 0)}
        />
        <MiniAmount label={t('Revenue')} value={stats?.total_revenue_quota} />
        <MiniAmount label={t('Cost')} value={stats?.total_cost_quota} />
        <MiniAmount label={t('Profit')} value={stats?.total_profit_quota} />
        <MiniStat
          label={t('Gross margin')}
          value={formatPercent(stats?.gross_margin)}
        />
      </div>
    </div>
  )
}

function TotalStatsCards(props: { title: string; stats?: LedgerStats | null }) {
  const { t } = useTranslation()
  const stats = props.stats
  const items: Array<{
    label: string
    value: ReactNode
    tone?: 'amount'
  }> = [
    { label: props.title, value: String(stats?.record_count ?? 0) },
    {
      label: t('Revenue'),
      value: <BusinessAmount value={stats?.total_revenue_quota ?? 0} />,
      tone: 'amount',
    },
    {
      label: t('Cost'),
      value: <BusinessAmount value={stats?.total_cost_quota ?? 0} />,
      tone: 'amount',
    },
    {
      label: t('Profit'),
      value: <BusinessAmount value={stats?.total_profit_quota ?? 0} />,
      tone: 'amount',
    },
    {
      label: t('Gross margin'),
      value: formatPercent(stats?.gross_margin),
    },
  ].filter(Boolean) as Array<{
    label: string
    value: ReactNode
    tone?: 'amount'
  }>

  return (
    <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-5'>
      {items.map((item) => (
        <div
          key={item.label}
          className='bg-card text-card-foreground min-w-0 rounded-lg border px-3 py-2 shadow-sm'
        >
          <div className='text-muted-foreground truncate text-xs font-medium'>
            {item.label}
          </div>
          <div
            className={cn(
              'mt-1 min-w-0 truncate text-sm font-semibold tabular-nums',
              item.tone === 'amount' ? 'text-base' : 'font-mono'
            )}
            title={typeof item.value === 'string' ? item.value : undefined}
          >
            {item.value}
          </div>
        </div>
      ))}
    </div>
  )
}

function MiniStat(props: { label: string; value: ReactNode }) {
  return (
    <div className='flex min-w-0 items-center gap-1.5'>
      <span className='text-muted-foreground truncate text-xs'>
        {props.label}
      </span>
      <span className='font-mono text-xs font-semibold tabular-nums'>
        {props.value}
      </span>
    </div>
  )
}

function MiniAmount(props: { label: string; value?: number }) {
  return (
    <div className='flex min-w-0 items-center gap-1.5'>
      <span className='text-muted-foreground truncate text-xs'>
        {props.label}
      </span>
      <span className='text-xs font-semibold'>
        <BusinessAmount value={props.value ?? 0} />
      </span>
    </div>
  )
}

export function ConsumptionCostLedgerDetail() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<LedgerFilters>(() =>
    defaultLedgerFilters()
  )
  const [rows, setRows] = useState<ConsumptionCostLedgerItem[]>([])
  const [cursor, setCursor] = useState<LedgerCursor | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [filterStats, setFilterStats] = useState<LedgerStats | null>(null)
  const [statsStatus, setStatsStatus] = useState<
    'ready' | 'pending' | 'unsupported' | ''
  >('')
  const [statsRunningCount, setStatsRunningCount] = useState(0)
  const [statsRunningLimit, setStatsRunningLimit] = useState(2)
  const [searchKey, setSearchKey] = useState(0)
  const [compactMode, setCompactMode] = useTableCompactMode(
    'consumption-cost-ledger'
  )
  const [exportJob, setExportJob] = useState<LedgerExportJob | null>(null)
  const [exportLoading, setExportLoading] = useState(false)
  const exportPollingRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [fallbackHint, setFallbackHint] = useState<FallbackHint | null>(null)
  const [reversalTarget, setReversalTarget] =
    useState<ConsumptionCostLedgerItem | null>(null)
  const [reversing, setReversing] = useState(false)

  const statsRefreshCountRef = useRef(0)
  const statsLoadingRef = useRef(false)
  const loadingRef = useRef(false)
  const loadMoreArmedRef = useRef(true)
  const tableScrollRef = useRef<HTMLDivElement | null>(null)

  const loadedSubtotal = useMemo(() => computeLoadedSubtotal(rows), [rows])
  const loadedRowsTimeRange = useMemo(
    () => formatLoadedRowsTimeRange(rows),
    [rows]
  )
  const updateFilter = (key: keyof LedgerFilters, value: string) => {
    setFilters((previous) =>
      previous[key] === value ? previous : { ...previous, [key]: value }
    )
  }
  const updateTimeFilter = (key: 'start_time' | 'end_time', value: string) => {
    setFilters((previous) => {
      if (!value) return { ...previous, [key]: value }
      const selected = clampLedgerDateTimeToNow(dayjs(value))
      if (!selected.isValid()) return { ...previous, [key]: value }
      let start = key === 'start_time' ? selected : dayjs(previous.start_time)
      let end = key === 'end_time' ? selected : dayjs(previous.end_time)
      if (!start.isValid() || !end.isValid()) {
        return { ...previous, [key]: selected.format(LEDGER_DATE_TIME_FORMAT) }
      }
      start = clampLedgerDateTimeToNow(start)
      end = clampLedgerDateTimeToNow(end)
      if (key === 'start_time') {
        if (end.isBefore(start)) end = start
        if (end.diff(start, 's') > LEDGER_MAX_RANGE_SECONDS) {
          end = start.add(LEDGER_MAX_RANGE_SECONDS, 's')
        }
      } else {
        if (start.isAfter(end)) start = end
        if (end.diff(start, 's') > LEDGER_MAX_RANGE_SECONDS) {
          start = end.subtract(LEDGER_MAX_RANGE_SECONDS, 's')
        }
      }
      start = clampLedgerDateTimeToNow(start)
      end = clampLedgerDateTimeToNow(end)
      if (key === 'start_time' && end.diff(start, 's') > LEDGER_MAX_RANGE_SECONDS) {
        start = end.subtract(LEDGER_MAX_RANGE_SECONDS, 's')
      }
      const nextStart = start.format(LEDGER_DATE_TIME_FORMAT)
      const nextEnd = end.format(LEDGER_DATE_TIME_FORMAT)
      if (previous.start_time === nextStart && previous.end_time === nextEnd) {
        return previous
      }
      return {
        ...previous,
        start_time: nextStart,
        end_time: nextEnd,
      }
    })
  }

  const loadPage = useCallback(
    async (nextCursor?: LedgerCursor | null, append = false) => {
      if (loadingRef.current) return
      loadingRef.current = true
      setLoading(true)
      try {
        let currentCursor = nextCursor
        let res = await getConsumptionCostLedger(
          buildLedgerParams(filters, currentCursor)
        )
        let advanceCount = 0
        while (
          res.success &&
          (res.data?.items?.length ?? 0) === 0 &&
          res.data?.has_more &&
          res.data?.next_cursor &&
          advanceCount < LEDGER_EMPTY_PAGE_AUTO_ADVANCE_LIMIT - 1
        ) {
          currentCursor = res.data.next_cursor
          advanceCount += 1
          res = await getConsumptionCostLedger(
            buildLedgerParams(filters, currentCursor)
          )
        }
        if (!res.success) {
          toast.error(translateLedgerMessage(t, res.message))
          return
        }
        const nextRows = res.data?.items ?? []
        setRows((previous) => (append ? [...previous, ...nextRows] : nextRows))
        setCursor(res.data?.next_cursor ?? null)
        setHasMore(Boolean(res.data?.has_more))
        // fallback_hint is a top-level sibling of "data" in the JSON body
        const hint = (res as unknown as Record<string, unknown>).fallback_hint as
          | FallbackHint
          | null
          | undefined
        if (!append && hint !== undefined) setFallbackHint(hint ?? null)
      } catch (error) {
        if (axios.isAxiosError(error)) {
          if (append && error.response?.status === 429) {
            // Transient contention (e.g. another tab is querying the same account).
            // Re-arm the scroll trigger so the next scroll automatically retries
            // instead of leaving the user permanently stuck at the bottom.
            loadMoreArmedRef.current = true
          } else {
            toast.error(translateLedgerMessage(t, error.response?.data?.message))
          }
        } else {
          toast.error(t('Request failed'))
        }
      } finally {
        loadingRef.current = false
        setLoading(false)
      }
    },
    [filters, t]
  )

  const loadStats = useCallback(async (options?: { silent?: boolean }) => {
    if (statsLoadingRef.current) return
    statsLoadingRef.current = true
    try {
      const res = await getConsumptionCostLedgerStats(
        buildLedgerParams(filters)
      )
      if (!res.success) {
        if (!options?.silent) toast.error(translateLedgerMessage(t, res.message))
        return
      }
      setFilterStats(res.data?.stats ?? null)
      setStatsStatus(res.data?.stats_status ?? '')
      setStatsRunningCount(res.data?.stats_running_count ?? 0)
      setStatsRunningLimit(res.data?.stats_running_limit ?? 2)
      if (res.data?.stats_status !== 'pending') {
        statsRefreshCountRef.current = 0
      }
    } catch (error) {
      if (options?.silent) {
        return
      }
      if (axios.isAxiosError(error)) {
        toast.error(translateLedgerMessage(t, error.response?.data?.message))
      } else {
        toast.error(t('Request failed'))
      }
    } finally {
      statsLoadingRef.current = false
    }
  }, [filters, t])

  useEffect(() => {
    setRows([])
    setCursor(null)
    setHasMore(false)
    setFilterStats(null)
    setStatsStatus('')
    setStatsRunningCount(0)
    if (tableScrollRef.current) {
      tableScrollRef.current.scrollTop = 0
    }
    loadMoreArmedRef.current = true
    statsRefreshCountRef.current = 0
    loadPage(null, false)
    loadStats()
  }, [searchKey])

  // Keep a ref so the polling interval always calls the latest loadStats without
  // being a dep of the interval effect — prevents the interval from restarting
  // (and switching to uncommitted filter values) when the user edits filters
  // between clicking Search and the stats becoming ready.
  const loadStatsRef = useRef(loadStats)
  useEffect(() => {
    loadStatsRef.current = loadStats
  })

  useEffect(() => {
    if (statsStatus !== 'pending') return
    if (statsRefreshCountRef.current >= LEDGER_STATS_AUTO_REFRESH_LIMIT) return
    const timer = window.setInterval(() => {
      if (statsRefreshCountRef.current >= LEDGER_STATS_AUTO_REFRESH_LIMIT) {
        window.clearInterval(timer)
        return
      }
      statsRefreshCountRef.current += 1
      loadStatsRef.current({ silent: true })
    }, LEDGER_STATS_AUTO_REFRESH_DELAY_MS)
    return () => window.clearInterval(timer)
  }, [statsStatus])

  const applySearch = () => {
    setRows([])
    setCursor(null)
    setHasMore(false)
    setFilterStats(null)
    setStatsStatus('')
    setStatsRunningCount(0)
    setFallbackHint(null)
    statsRefreshCountRef.current = 0
    setSearchKey((value) => value + 1)
  }

  const confirmReverse = async () => {
    if (!reversalTarget) return
    setReversing(true)
    try {
      const res = await reverseLedgerRecord(reversalTarget.id)
      if (res?.success) {
        toast.success(t('Reversal completed'))
        setReversalTarget(null)
        applySearch()
      } else {
        toast.error(res?.message || t('Reversal failed'))
      }
    } catch (e) {
      const msg =
        (axios.isAxiosError(e) && e.response?.data?.message) ||
        (e instanceof Error ? e.message : '') ||
        t('Reversal failed')
      toast.error(msg)
    } finally {
      setReversing(false)
    }
  }

  const resetFilters = () => {
    const next = defaultLedgerFilters()
    setFilters(next)
    setRows([])
    setCursor(null)
    setHasMore(false)
    setFilterStats(null)
    setStatsStatus('')
    setStatsRunningCount(0)
    setFallbackHint(null)
    statsRefreshCountRef.current = 0
    setSearchKey((value) => value + 1)
  }

  const stopExportPolling = useCallback(() => {
    if (exportPollingRef.current !== null) {
      clearInterval(exportPollingRef.current)
      exportPollingRef.current = null
    }
  }, [])

  // Fetch a one-time signed URL (via axios with auth headers), then trigger
  // a native browser download — no blob buffering, no memory spike.
  // Throws on failure so the caller can decide whether to clear job state.
  const triggerDownload = useCallback(
    async (jobId: string) => {
      const res = await getLedgerExportDownloadURL(jobId)
      if (!res.success || !res.data?.url) {
        throw new Error(translateLedgerMessage(t, res.message, 'Export failed'))
      }
      const a = document.createElement('a')
      a.href = res.data.url
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
    },
    [t]
  )

  const startExport = useCallback(async () => {
    if (exportLoading || exportJob?.status === 'pending' || exportJob?.status === 'running') return
    if (exportJob?.status === 'ready') {
      try {
        await triggerDownload(exportJob.job_id)
        toast.success(t('Download started'))
        setExportJob(null)
      } catch (err) {
        toast.error(err instanceof Error ? err.message : t('Export failed'))
        setExportJob(null)
      }
      return
    }
    setExportLoading(true)
    try {
      const params = buildLedgerParams(filters)
      delete (params as Record<string, unknown>).limit
      delete (params as Record<string, unknown>).cursor_created_at
      delete (params as Record<string, unknown>).cursor_id
      const res = await createLedgerExport(params)
      if (!res.success) {
        toast.error(translateLedgerMessage(t, res.message, 'Export failed'))
        return
      }
      const jobId = res.data!.job_id
      setExportJob({ job_id: jobId, status: 'pending', progress: 0, row_count: 0 })
      stopExportPolling()
      exportPollingRef.current = setInterval(async () => {
        try {
          const statusRes = await getLedgerExportStatus(jobId)
          if (!statusRes.success || !statusRes.data) return
          const job = statusRes.data
          setExportJob(job)
          if (job.status === 'ready') {
            stopExportPolling()
            try {
              await triggerDownload(jobId)
              toast.success(
                t('Export ready. Download started for {{count}} rows.', {
                  count: job.row_count.toLocaleString(),
                })
              )
              setExportJob(null)
            } catch (err) {
              toast.error(err instanceof Error ? err.message : t('Export failed'))
              setExportJob(null)
            }
          } else if (job.status === 'failed') {
            stopExportPolling()
            toast.error(translateLedgerMessage(t, job.error, 'Export failed'))
            setTimeout(() => setExportJob(null), 4000)
          }
        } catch {
          // network/parse errors during polling are silent
        }
      }, LEDGER_EXPORT_POLLING_DELAY_MS)
    } catch (error) {
      if (axios.isAxiosError(error)) {
        toast.error(translateLedgerMessage(t, error.response?.data?.message, 'Export failed'))
      } else {
        toast.error(t('Export failed'))
      }
    } finally {
      setExportLoading(false)
    }
  }, [exportJob, exportLoading, filters, stopExportPolling, t, triggerDownload])

  useEffect(() => stopExportPolling, [stopExportPolling])

  const handleTableScroll = useCallback(
    (event: UIEvent<HTMLDivElement>) => {
      const target = event.currentTarget
      const distanceToBottom =
        target.scrollHeight - target.scrollTop - target.clientHeight
      if (distanceToBottom > 96) {
        loadMoreArmedRef.current = true
      }
      if (
        distanceToBottom > 48 ||
        !loadMoreArmedRef.current ||
        !hasMore ||
        loading ||
        !cursor
      )
        return
      loadMoreArmedRef.current = false
      loadPage(cursor, true)
    },
    [cursor, hasMore, loadPage, loading]
  )

  return (
    <div className='flex h-full min-h-0 flex-1 flex-col gap-4 overflow-hidden'>
      <div className='shrink-0 rounded-lg border p-3'>
        <div className='grid gap-2 md:grid-cols-4 xl:grid-cols-8'>
          <FilterInput
            label={t('Start time')}
            type='datetime-local'
            value={filters.start_time}
            onChange={(value) => updateTimeFilter('start_time', value)}
          />
          <FilterInput
            label={t('End time')}
            type='datetime-local'
            value={filters.end_time}
            onChange={(value) => updateTimeFilter('end_time', value)}
          />
          <FilterInput
            label={t('Log ID')}
            value={filters.log_id}
            onChange={(value) => updateFilter('log_id', value)}
          />
          <FilterInput
            label={t('Ledger ID')}
            value={filters.id}
            onChange={(value) => updateFilter('id', value)}
          />
          <FilterInput
            label={t('User ID')}
            value={filters.user_id}
            onChange={(value) => updateFilter('user_id', value)}
          />
          <FilterInput
            label={t('Channel ID')}
            value={filters.channel_id}
            onChange={(value) => updateFilter('channel_id', value)}
          />
          <FilterInput
            label={t('Model')}
            value={filters.model_name}
            onChange={(value) => updateFilter('model_name', value)}
          />
          <FilterInput
            label={t('Group')}
            value={filters.group_name}
            onChange={(value) => updateFilter('group_name', value)}
          />
          <div className='flex flex-col gap-1'>
            <span className='text-muted-foreground text-xs'>{t('Tag')}</span>
            <Select
              value={filters.tag}
              onValueChange={(value) => updateFilter('tag', value ?? 'all')}
            >
              <SelectTrigger size='sm'>
                <SelectValue>{tagFilterLabel(filters.tag, t)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='all'>{t('All')}</SelectItem>
                <SelectItem value='reversal'>{t('Reversal')}</SelectItem>
                <SelectItem value='loss'>{t('Loss')}</SelectItem>
                <SelectItem value='profit'>{t('Profit')}</SelectItem>
                <SelectItem value='zero_revenue'>
                  {t('Zero revenue')}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className='mt-3 flex flex-wrap justify-end gap-2'>
          <Button
            type='button'
            size='sm'
            variant={compactMode ? 'default' : 'outline'}
            onClick={() => setCompactMode(!compactMode)}
          >
            <List data-icon='inline-start' />
            {compactMode ? t('Adaptive list') : t('Compact list')}
          </Button>
          <Button size='sm' variant='outline' onClick={resetFilters}>
            <RefreshCw data-icon='inline-start' />
            {t('Reset')}
          </Button>
          <Button size='sm' onClick={applySearch} disabled={loading}>
            <Search data-icon='inline-start' />
            {t('Search')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={startExport}
            disabled={
              exportLoading ||
              exportJob?.status === 'pending' ||
              exportJob?.status === 'running'
            }
          >
            {exportJob?.status === 'pending' || exportJob?.status === 'running' ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Download data-icon='inline-start' />
            )}
            {exportJob?.status === 'pending' || exportJob?.status === 'running'
              ? t('Exporting ({{progress}}%)', { progress: exportJob.progress })
              : exportJob?.status === 'ready'
              ? t('Download')
              : t('Export')}
          </Button>
        </div>
      </div>

      <FallbackBackfillBanner
        hint={fallbackHint}
        onBackfillComplete={applySearch}
      />

      <div className='flex flex-col gap-1.5'>
        {filterStats ? (
          <TotalStatsCards title={t('Total rows')} stats={filterStats} />
        ) : null}
        {statsStatus === 'pending' ? (
          <div className='text-muted-foreground flex gap-2 rounded-lg border px-3 py-2 text-xs'>
            <Loader2 className='mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin' />
            <div className='flex flex-col gap-0.5'>
              <span>
                {t(
                  'Preparing statistics. They will update automatically.'
                )}
              </span>
              <span>
                {t('Statistics tasks running: {{count}}/{{limit}}', {
                  count: statsRunningCount,
                  limit: statsRunningLimit,
                })}
              </span>
              <span>
                {t(
                  'Tag filters check each row and may take longer; historical stats are cached for 5 minutes and can be viewed later with the same filters.'
                )}
              </span>
            </div>
          </div>
        ) : null}
        <StatStrip
          title={t('Loaded rows')}
          stats={loadedSubtotal}
          timeRange={loadedRowsTimeRange}
        />
      </div>

      <div
        ref={tableScrollRef}
        className='min-h-0 flex-1 basis-0 overflow-auto rounded-lg border'
        onScroll={handleTableScroll}
      >
        <Table
          containerClassName={cn(
            'overflow-visible',
            compactMode ? 'overflow-x-visible' : 'overflow-x-auto'
          )}
          className={cn(
            compactMode &&
              'table-auto text-xs [&_td]:h-8 [&_td]:px-1.5 [&_td]:py-1 [&_td]:text-xs [&_td_*]:text-xs [&_th]:h-8 [&_th]:px-1.5 [&_th]:py-1 [&_th]:text-xs [&_th_*]:text-xs'
          )}
        >
          <TableHeader className='bg-background sticky top-0 z-10'>
            <TableRow>
              <TableHead>{t('Time')}</TableHead>
              <TableHead>{t('Tags')}</TableHead>
              <TableHead>{t('Log ID')}</TableHead>
              <TableHead>{t('User ID')}</TableHead>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Group')}</TableHead>
              <TableHead>{t('Revenue')}</TableHead>
              <TableHead>{t('Cost')}</TableHead>
              <TableHead>{t('Profit')}</TableHead>
              <TableHead>{t('Gross margin')}</TableHead>
              <TableHead>{t('Group ratio')}</TableHead>
              <TableHead>{t('Cost ratio')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 && !loading ? (
              <TableRow>
                <TableCell
                  colSpan={14}
                  className='text-muted-foreground text-center'
                >
                  {t('No records')}
                </TableCell>
              </TableRow>
            ) : null}
            {rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell className='font-mono text-xs whitespace-nowrap'>
                  {formatTime(row.created_at)}
                </TableCell>
                <TableCell>
                  <div className='flex flex-wrap gap-1'>
                    {sortLedgerTags(visibleLedgerTags(row)).map((tag) => (
                      <Badge key={tag} variant='outline' className={tagBadgeClass(tag)}>
                        {tagLabel(tag, t)}
                      </Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <UsageLogIdHover logId={row.log_id} />
                </TableCell>
                <TableCell>#{row.user_id}</TableCell>
                <TableCell>
                  {row.channel_name || `#${row.channel_id || '-'} `}
                </TableCell>
                <TableCell>{row.model_name || '-'}</TableCell>
                <TableCell>{row.group_name || '-'}</TableCell>
                <TableCell>
                  <BusinessAmount value={row.revenue_quota} />
                </TableCell>
                <TableCell>
                  <BusinessAmount value={row.cost_quota} />
                </TableCell>
                <TableCell>
                  <BusinessAmount value={row.profit_quota} />
                </TableCell>
                <TableCell>{formatPercent(row.gross_margin)}</TableCell>
                <TableCell>{ratioText(row.group_ratio)}</TableCell>
                <TableCell>{ratioText(row.cost_ratio)}</TableCell>
                <TableCell className='text-right whitespace-nowrap'>
                  {row.log_id != null &&
                  !visibleLedgerTags(row).includes('reversal') ? (
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      onClick={() => setReversalTarget(row)}
                    >
                      <Undo2 data-icon='inline-start' />
                      {t('Reverse')}
                    </Button>
                  ) : (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <div className='text-muted-foreground flex h-10 items-center justify-center gap-2 text-xs'>
          {loading && rows.length > 0 ? (
            <>
              <Loader2 className='h-4 w-4 animate-spin' />
              {t('Loading...')}
            </>
          ) : rows.length > 0 && !hasMore ? (
            t('No more records')
          ) : null}
        </div>
      </div>
      <AlertDialog
        open={reversalTarget != null}
        onOpenChange={(open) => {
          if (!open && !reversing) setReversalTarget(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Reverse this record?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This inserts a mirror reversal record and re-runs settlement, correcting the balance, ledger and commission. This cannot be undone.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {reversalTarget ? (
            <div className='text-muted-foreground space-y-1 text-sm'>
              <div>
                {t('Log ID')}: #{reversalTarget.log_id} · {t('User ID')}: #
                {reversalTarget.user_id}
              </div>
              <div className='flex items-center gap-1'>
                {t('Revenue')}:{' '}
                <BusinessAmount value={reversalTarget.revenue_quota} />
              </div>
            </div>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={reversing}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault()
                confirmReverse()
              }}
              disabled={reversing}
            >
              {reversing ? (
                <Loader2 className='mr-1 h-4 w-4 animate-spin' />
              ) : null}
              {t('Confirm reversal')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function FilterInput(props: {
  label: string
  value: string
  type?: string
  onChange: (value: string) => void
}) {
  return (
    <label className='flex min-w-0 flex-col gap-1'>
      <span className='text-muted-foreground truncate text-xs'>
        {props.label}
      </span>
      <Input
        type={props.type ?? 'text'}
        value={props.value}
        onChange={(event) => props.onChange(event.target.value)}
        step={props.type === 'datetime-local' ? 1 : undefined}
        className='h-8'
      />
    </label>
  )
}
