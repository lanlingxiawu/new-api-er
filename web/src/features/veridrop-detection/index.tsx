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
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  CheckCircle2,
  Download,
  FlaskConical,
  ListChecks,
  Loader2,
  Play,
  RefreshCw,
  Save,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type {
  SystemTask,
  SystemTaskStatus,
} from '@/features/system-settings/types'
import { useDebounce } from '@/hooks'
import { toIntlLocale } from '@/i18n/languages'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  fetchManualUpstreamModels,
  cleanupVeridropDetectionResults,
  getVeridropOptions,
  listVeridropDetectionTargets,
  listVeridropDetectionResults,
  listVeridropSystemTasks,
  startChannelVeridropDetection,
  startChannelsVeridropDetection,
  startManualVeridropDetection,
  updateVeridropOptions,
  VERIDROP_DEFAULT_OPTIONS,
} from './api'
import {
  buildDetailedDetectionReportHtml,
  getVeridropResultDisplayMessage,
  hasVeridropScoreReport,
  parseVeridropScoreReport,
  type VeridropReportCheck,
  type VeridropScoreReport,
} from './report'
import type {
  VeridropDetectionOptions,
  VeridropDetectionResult,
  VeridropDetectionSortBy,
  VeridropDetectionSortOrder,
  VeridropDetectionStatus,
  VeridropDetectionSummary,
  VeridropDetectionTarget,
  VeridropManualDetectionRequest,
} from './types'

const ACTIVE_POLL_INTERVAL_MS = 8000
const RESULT_LIMIT = 100
const RESULT_OUTCOMES = [
  'in_progress',
  'passed',
  'completed',
  'low_score',
  'failed',
  'skipped',
  'cancelled',
] as const
const RESULT_MODES = ['all', 'quick', 'standard', 'full'] as const
const RESULT_BATCHES = ['all', 'latest'] as const
const EMPTY_DETECTION_RESULTS: VeridropDetectionResult[] = []
const EMPTY_DETECTION_TARGETS: VeridropDetectionTarget[] = []
const DEFAULT_MANUAL_FORM: VeridropManualDetectionRequest = {
  protocol: 'openai',
  base_url: 'https://',
  api_key: '',
  model: '',
  mode: 'quick',
  include_long_context: false,
  include_long_context_extreme: false,
  openai_wire_api: 'chat_completions',
  force: false,
}

type Translator = (key: string) => string
type ResultOutcomeFilter = (typeof RESULT_OUTCOMES)[number]
type ResultModeFilter = (typeof RESULT_MODES)[number]
type ResultBatchFilter = (typeof RESULT_BATCHES)[number]
type ResultTimeFilter = 'all' | '24h' | '7d' | '30d'
type DetectionWorkspaceTab = 'results' | 'batch' | 'manual'
type DetectionResultSort = {
  by: VeridropDetectionSortBy
  order: VeridropDetectionSortOrder
}

const TASK_STATUS_CLASS_NAME: Record<SystemTaskStatus, string> = {
  pending:
    'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  running: 'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  succeeded:
    'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  failed: '',
}

const TASK_STATUS_DOT_CLASS_NAME: Record<SystemTaskStatus, string> = {
  pending: 'bg-amber-500',
  running: 'bg-sky-500',
  succeeded: 'bg-emerald-500',
  failed: 'bg-destructive',
}

const RESULT_STATUS_CLASS_NAME: Record<VeridropDetectionStatus, string> = {
  queued: 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  running: 'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  done: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  error: '',
  timeout: '',
  cancelled: 'bg-muted text-muted-foreground',
  skipped: 'bg-muted text-muted-foreground',
}

const RESULT_STATUS_DOT_CLASS_NAME: Record<VeridropDetectionStatus, string> = {
  queued: 'bg-amber-500',
  running: 'bg-sky-500',
  done: 'bg-emerald-500',
  error: 'bg-destructive',
  timeout: 'bg-destructive',
  cancelled: 'bg-muted-foreground/50',
  skipped: 'bg-muted-foreground/50',
}

type LiveRefreshIndicatorProps = {
  active: boolean
}

function LiveRefreshIndicator({ active }: LiveRefreshIndicatorProps) {
  const { t } = useTranslation()
  return (
    <span
      className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'
      aria-live='polite'
    >
      <span
        className={cn(
          'size-1.5 rounded-full',
          active ? 'bg-emerald-500' : 'bg-muted-foreground/40'
        )}
        aria-hidden='true'
      />
      {active
        ? t('Auto-refreshing every {{seconds}}s', {
            seconds: ACTIVE_POLL_INTERVAL_MS / 1000,
          })
        : t('Live refresh pauses when no task is running')}
    </span>
  )
}

type PanelEmptyStateProps = {
  icon: ReactNode
  message: string
}

function PanelEmptyState({ icon, message }: PanelEmptyStateProps) {
  return (
    <div className='px-4 py-10 text-center'>
      <div className='bg-muted mx-auto mb-3 flex size-10 items-center justify-center rounded-lg'>
        {icon}
      </div>
      <p className='text-muted-foreground text-sm'>{message}</p>
    </div>
  )
}

function TaskStatusBadge({ status }: { status: SystemTaskStatus }) {
  const { t } = useTranslation()
  return (
    <Badge
      variant={status === 'failed' ? 'destructive' : 'secondary'}
      className={cn('gap-1.5', TASK_STATUS_CLASS_NAME[status])}
    >
      <span
        className={cn(
          'size-1.5 rounded-full',
          TASK_STATUS_DOT_CLASS_NAME[status]
        )}
        aria-hidden='true'
      />
      {t(status)}
    </Badge>
  )
}

function getResultStatusLabel(result: VeridropDetectionResult, t: Translator) {
  if (result.status === 'done') {
    if (isPassedDetectionResult(result)) {
      return t('Passed')
    }
    if (isFailedDetectionResult(result)) {
      return t('Failed')
    }
    if (isBelowThresholdDetectionResult(result)) {
      return t('Below threshold')
    }
    if (isMarginalDetectionResult(result)) {
      return t('Marginal')
    }
    return t('Completed')
  }
  if (result.status === 'queued') return t('Waiting')
  if (result.status === 'running') return t('Detecting')
  if (result.status === 'error') return t('Failed')
  if (result.status === 'timeout') return t('Timed out')
  if (result.status === 'cancelled') return t('Cancelled')
  return t('Skipped')
}

function getResultOutcomeFilterLabel(
  outcome: ResultOutcomeFilter,
  t: Translator
) {
  if (outcome === 'in_progress') return t('In Progress')
  if (outcome === 'passed') return t('Passed')
  if (outcome === 'completed') return t('Marginal')
  if (outcome === 'low_score') return t('Below threshold')
  if (outcome === 'failed') return t('Failed')
  if (outcome === 'skipped') return t('Skipped')
  return t('Cancelled')
}

function getResultModeFilterLabel(mode: ResultModeFilter, t: Translator) {
  if (mode === 'all') return t('All modes')
  return getVeridropModeLabel(mode, t)
}

function getResultBatchFilterLabel(batch: ResultBatchFilter, t: Translator) {
  return batch === 'latest' ? t('Latest batch') : t('All batches')
}

function isPassedDetectionResult(result: VeridropDetectionResult) {
  return result.outcome === 'passed'
}

function isFailedDetectionResult(result: VeridropDetectionResult) {
  return result.outcome === 'failed'
}

function isMarginalDetectionResult(result: VeridropDetectionResult) {
  return (
    result.outcome === 'completed' &&
    result.verdict.trim().toLowerCase() === 'marginal'
  )
}

function isBelowThresholdDetectionResult(result: VeridropDetectionResult) {
  return result.outcome === 'low_score'
}

function ResultStatusBadge({
  result,
  score,
}: {
  result: VeridropDetectionResult
  score?: string | null
}) {
  const { t } = useTranslation()
  const failedVerdict =
    result.status === 'done' && isFailedDetectionResult(result)
  const belowThreshold = isBelowThresholdDetectionResult(result)
  const marginal = isMarginalDetectionResult(result) && !belowThreshold
  const destructive = isFailedDetectionResult(result) || belowThreshold
  let badgeVariant: 'destructive' | 'secondary' | 'warning' = 'secondary'
  if (destructive) badgeVariant = 'destructive'
  else if (marginal) badgeVariant = 'warning'

  let dotClassName = RESULT_STATUS_DOT_CLASS_NAME[result.status]
  if (failedVerdict || belowThreshold) {
    dotClassName = RESULT_STATUS_DOT_CLASS_NAME.error
  } else if (marginal) {
    dotClassName = 'bg-warning'
  }

  return (
    <Badge
      variant={badgeVariant}
      className={cn(
        'gap-1.5',
        failedVerdict || belowThreshold || marginal
          ? ''
          : RESULT_STATUS_CLASS_NAME[result.status]
      )}
    >
      <span
        className={cn('size-1.5 rounded-full', dotClassName)}
        aria-hidden='true'
      />
      {getResultStatusLabel(result, t)}
      {score != null && result.status === 'done' ? (
        <span className='tabular-nums'>{t('{{score}} points', { score })}</span>
      ) : null}
    </Badge>
  )
}

function getVeridropModeLabel(mode: string | undefined, t: Translator) {
  switch (mode) {
    case 'standard':
      return t('Standard')
    case 'full':
    case 'strict':
      return t('Full')
    case 'quick':
    default:
      return t('Quick')
  }
}

function getVeridropProtocolLabel(protocol: string | undefined, t: Translator) {
  switch (protocol) {
    case 'anthropic':
      return t('Anthropic')
    case 'gemini':
      return t('Gemini')
    case 'openai':
    default:
      return t('OpenAI Compatible')
  }
}

function getResultTimeLabel(timeFilter: ResultTimeFilter, t: Translator) {
  switch (timeFilter) {
    case '24h':
      return t('Last 24 hours')
    case '7d':
      return t('Last 7 days')
    case '30d':
      return t('Last 30 days')
    case 'all':
    default:
      return t('Any time')
  }
}

function DetectionResultStatistics({
  summary,
  isLoading,
  selectedOutcomes,
  onSelectedOutcomesChange,
}: {
  summary?: VeridropDetectionSummary
  isLoading: boolean
  selectedOutcomes: ResultOutcomeFilter[]
  onSelectedOutcomesChange: (outcomes: ResultOutcomeFilter[]) => void
}) {
  const { t } = useTranslation()

  if (summary == null) {
    if (!isLoading) return null
    return (
      <div className='mt-3 flex min-h-11 flex-wrap items-center gap-2 border-y py-2'>
        {Array.from({ length: 8 }, (_, index) => (
          <Skeleton key={index} className='h-7 w-24 rounded-full' />
        ))}
      </div>
    )
  }

  const statistics = [
    {
      key: 'total',
      label: t('Total'),
      count: summary.total,
      variant: 'outline',
    },
    {
      key: 'in_progress',
      label: t('In Progress'),
      count: summary.in_progress,
      variant: 'outline',
    },
    {
      key: 'passed',
      label: t('Passed'),
      count: summary.passed,
      variant: 'secondary',
    },
    {
      key: 'completed',
      label: t('Marginal'),
      count: summary.completed,
      variant: 'warning',
    },
    {
      key: 'low_score',
      label: t('Below threshold'),
      count: summary.low_score,
      variant: 'destructive',
    },
    {
      key: 'failed',
      label: t('Failed'),
      count: summary.failed,
      variant: 'destructive',
    },
    {
      key: 'skipped',
      label: t('Skipped'),
      count: summary.skipped,
      variant: 'secondary',
    },
    {
      key: 'cancelled',
      label: t('Cancelled'),
      count: summary.cancelled,
      variant: 'outline',
    },
  ] as const

  return (
    <div className='mt-3 flex min-h-11 flex-wrap items-center gap-2 border-y py-2'>
      {statistics.map((statistic) => {
        const isTotal = statistic.key === 'total'
        const isSelected = isTotal
          ? selectedOutcomes.length === 0
          : selectedOutcomes.includes(statistic.key)

        return (
          <Badge
            key={statistic.key}
            render={<button type='button' />}
            variant={statistic.variant}
            aria-pressed={isSelected}
            className={cn(
              'h-7 cursor-pointer gap-1.5 px-2.5 select-none hover:shadow-sm',
              isSelected &&
                'ring-primary/50 ring-2 ring-offset-1 ring-offset-background shadow-sm'
            )}
            onClick={() => {
              if (isTotal) {
                onSelectedOutcomesChange([])
                return
              }
              if (isSelected) {
                onSelectedOutcomesChange(
                  selectedOutcomes.filter(
                    (outcome) => outcome !== statistic.key
                  )
                )
                return
              }
              onSelectedOutcomesChange(
                RESULT_OUTCOMES.filter(
                  (outcome) =>
                    outcome === statistic.key ||
                    selectedOutcomes.includes(outcome)
                )
              )
            }}
          >
            <span>{statistic.label}</span>
            <span className='font-semibold tabular-nums'>
              {statistic.count}
            </span>
          </Badge>
        )
      })}
    </div>
  )
}

function getResultSinceSeconds(
  timeFilter: ResultTimeFilter,
  nowSeconds: number
) {
  if (timeFilter === '24h') return nowSeconds - 24 * 60 * 60
  if (timeFilter === '7d') return nowSeconds - 7 * 24 * 60 * 60
  if (timeFilter === '30d') return nowSeconds - 30 * 24 * 60 * 60
  return 0
}

function downloadTextFile(filename: string, content: string, type: string) {
  const blob = new Blob([content], { type })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function formatResultScore(score: number) {
  if (!Number.isFinite(score) || score < 0) return null
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format(score)
}

function getReportCheckStatusLabel(status: string, t: Translator) {
  if (status === 'pass') return t('Passed')
  if (status === 'fail') return t('Failed')
  if (status === 'skip') return t('Skipped')
  if (status === 'error') return t('Error')
  return status
}

function getReportVerdictLabel(verdict: string, t: Translator) {
  if (verdict === 'passed') return t('Passed')
  if (verdict === 'marginal') return t('Marginal')
  if (verdict === 'failed') return t('Failed')
  return verdict
}

function getReportCheckStatusVariant(
  status: string
): 'destructive' | 'outline' | 'secondary' {
  if (status === 'fail' || status === 'error') return 'destructive'
  if (status === 'skip') return 'outline'
  return 'secondary'
}

function ReportCheckStatusBadge({ check }: { check: VeridropReportCheck }) {
  const { t } = useTranslation()
  return (
    <Badge variant={getReportCheckStatusVariant(check.status)}>
      {getReportCheckStatusLabel(check.status, t)}
    </Badge>
  )
}

function getSkippedCheckExplanation(
  check: VeridropReportCheck,
  mode: string,
  t: ReturnType<typeof useTranslation>['t']
) {
  if (check.skipReason === 'mode-excluded') {
    return t('Not called in {{mode}} mode.', {
      mode: getVeridropModeLabel(mode, t),
    })
  }
  if (
    [
      'model-excluded',
      'unknown model',
      'model lacks thinking support',
    ].includes(check.skipReason)
  ) {
    return t('Not called because this check does not apply to the model.')
  }
  if (
    check.skipReason.toLowerCase().includes('long context') ||
    check.skipReason.includes('长上下文')
  ) {
    return t('Not called because the optional probe is disabled.')
  }
  if (['missing-usage', 'no observations'].includes(check.skipReason)) {
    return t(
      'The upstream was called, but the response did not contain enough evidence to score this check.'
    )
  }
  return t('Skipped: {{reason}}', {
    reason: check.skipReason || t('No reason provided'),
  })
}

function ReportCheckEvidence({
  check,
  mode,
}: {
  check: VeridropReportCheck
  mode: string
}) {
  const { t } = useTranslation()
  if (check.error !== '') {
    return <span className='text-destructive text-xs'>{check.error}</span>
  }
  const skippedExplanation =
    check.status === 'skip' ? getSkippedCheckExplanation(check, mode, t) : null
  if (Object.keys(check.details).length === 0) {
    return skippedExplanation == null ? (
      <span className='text-muted-foreground'>—</span>
    ) : (
      <span className='text-muted-foreground text-xs'>
        {skippedExplanation}
      </span>
    )
  }
  return (
    <div className='flex flex-col gap-1.5'>
      {skippedExplanation != null ? (
        <span className='text-muted-foreground text-xs'>
          {skippedExplanation}
        </span>
      ) : null}
      <details>
        <summary className='text-primary cursor-pointer text-xs'>
          {t('View evidence')}
        </summary>
        <pre className='bg-muted mt-2 max-h-64 w-full max-w-[420px] overflow-auto rounded-md p-3 text-[11px] whitespace-pre-wrap'>
          {JSON.stringify(check.details, null, 2)}
        </pre>
      </details>
    </div>
  )
}

function ScoreDetailsDialog({
  result,
  report,
  onOpenChange,
}: {
  result: VeridropDetectionResult | null
  report: VeridropScoreReport | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const belowThreshold = result?.outcome === 'low_score'
  const marginal =
    result?.outcome === 'completed' &&
    report?.verdict.trim().toLowerCase() === 'marginal'
  let scorePanelClassName = 'bg-muted/40'
  if (belowThreshold) scorePanelClassName = 'bg-destructive/5'
  else if (marginal) scorePanelClassName = 'bg-warning/5'

  return (
    <Dialog
      open={result != null && report != null}
      onOpenChange={onOpenChange}
      title={t('Score Details')}
      description={
        result == null
          ? undefined
          : `${result.channel_name || t('Manual Test')} · ${result.model}`
      }
      contentClassName='sm:max-w-6xl'
      contentHeight='min(72vh, 760px)'
      showCloseButton
    >
      {report != null ? (
        <div className='space-y-4'>
          <div
            className={cn(
              'grid gap-3 rounded-md px-4 py-3 sm:grid-cols-[auto_1fr] sm:items-center',
              scorePanelClassName
            )}
          >
            <div className='min-w-24'>
              <div className='text-muted-foreground text-xs'>
                {t('Total Score')}
              </div>
              <div
                className={cn(
                  'text-2xl font-semibold tabular-nums',
                  belowThreshold && 'text-destructive'
                )}
              >
                {formatResultScore(report.totalScore) ?? '0'}
              </div>
            </div>
            <div className='min-w-0 text-sm'>
              <div className='flex flex-wrap items-center gap-2'>
                {belowThreshold ? (
                  <Badge variant='destructive'>{t('Below threshold')}</Badge>
                ) : null}
                {marginal && !belowThreshold ? (
                  <Badge variant='warning'>{t('Marginal')}</Badge>
                ) : null}
                <span className='font-medium'>
                  {getReportVerdictLabel(report.verdict, t) || report.summary}
                </span>
              </div>
              <div className='text-muted-foreground mt-1 text-xs'>
                {t('{{count}} checks · {{weight}} total weight', {
                  count: report.checks.length,
                  weight: formatResultScore(report.effectiveWeight) ?? '0',
                })}
              </div>
            </div>
          </div>

          <div className='border-border space-y-1 border-b pb-3 text-xs'>
            <div className='font-medium'>{t('Scoring Rules')}</div>
            <div className='text-muted-foreground'>
              {t('Weighted average of all non-skipped checks.')}
            </div>
            <div className='text-muted-foreground'>
              {t('Error checks remain weighted; skipped checks are excluded.')}
            </div>
            <div className='text-muted-foreground'>
              {t('70+ passed · 50–69.9 marginal · below 50 failed')}
            </div>
            {report.hasCriticalIssues ? (
              <div className='text-amber-700 dark:text-amber-300'>
                {t('Critical findings can lower a passing result to marginal.')}
              </div>
            ) : null}
          </div>

          <div className='overflow-x-auto'>
            <Table className='min-w-[900px]'>
              <TableHeader>
                <TableRow className='bg-muted/40 hover:bg-muted/40'>
                  <TableHead className='min-w-[190px]'>{t('Check')}</TableHead>
                  <TableHead className='min-w-[150px]'>{t('Result')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Contribution')}
                  </TableHead>
                  <TableHead className='min-w-[220px]'>
                    {t('Evidence')}
                  </TableHead>
                  <TableHead className='text-right'>{t('Weight')}</TableHead>
                  <TableHead className='text-right'>{t('Duration')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {report.checks.map((check) => {
                  const checkFailed = ['fail', 'error'].includes(check.status)
                  return (
                    <TableRow
                      key={`${check.name}-${check.displayName}`}
                      className={cn(checkFailed && 'bg-destructive/5')}
                    >
                      <TableCell className='align-top'>
                        <div className='font-medium'>{check.displayName}</div>
                        <div className='text-muted-foreground font-mono text-[11px]'>
                          {check.name}
                        </div>
                      </TableCell>
                      <TableCell className='align-top'>
                        <div className='flex flex-wrap items-center gap-2'>
                          <ReportCheckStatusBadge check={check} />
                          <span
                            className={cn(
                              'font-semibold tabular-nums',
                              checkFailed && 'text-destructive'
                            )}
                          >
                            {t('{{score}} points', {
                              score: formatResultScore(check.score) ?? '0',
                            })}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className='text-right align-top tabular-nums'>
                        {check.contribution == null
                          ? '—'
                          : (formatResultScore(check.contribution) ?? '0')}
                      </TableCell>
                      <TableCell className='align-top'>
                        <ReportCheckEvidence
                          check={check}
                          mode={result?.mode ?? ''}
                        />
                      </TableCell>
                      <TableCell className='text-right align-top tabular-nums'>
                        {formatResultScore(check.weight) ?? '0'}
                      </TableCell>
                      <TableCell className='text-right align-top tabular-nums'>
                        {check.durationMs == null
                          ? '—'
                          : `${check.durationMs} ms`}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        </div>
      ) : null}
    </Dialog>
  )
}

function isActiveTask(task: SystemTask) {
  return task.status === 'pending' || task.status === 'running'
}

function isActiveResult(result: VeridropDetectionResult) {
  return result.status === 'queued' || result.status === 'running'
}

function numberInputValue(value: number) {
  return Number.isFinite(value) ? String(value) : ''
}

function clampInteger(
  value: string,
  fallback: number,
  min: number,
  max: number
) {
  const parsed = Number.parseInt(value, 10)
  if (!Number.isFinite(parsed)) return fallback
  return Math.min(max, Math.max(min, parsed))
}

function hasUnsavedVeridropChanges(
  options: VeridropDetectionOptions,
  savedOptions: VeridropDetectionOptions
) {
  return (
    JSON.stringify({ ...options, admin_api_key: '' }) !==
      JSON.stringify({ ...savedOptions, admin_api_key: '' }) ||
    options.admin_api_key.trim() !== ''
  )
}

type SettingFieldProps = {
  label: string
  description?: string
  children: ReactNode
}

function SettingField({ label, description, children }: SettingFieldProps) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {children}
      {description != null && (
        <FieldDescription>{description}</FieldDescription>
      )}
    </Field>
  )
}

function CompactField({
  label,
  children,
}: Pick<SettingFieldProps, 'label' | 'children'>) {
  return (
    <div className='grid min-w-0 gap-1'>
      <Label className='text-muted-foreground text-xs'>{label}</Label>
      {children}
    </div>
  )
}

type SwitchRowProps = {
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}

function SwitchRow({
  label,
  description,
  checked,
  onCheckedChange,
}: SwitchRowProps) {
  return (
    <Field orientation='horizontal' className='rounded-md border px-3 py-3'>
      <div className='min-w-0'>
        <FieldLabel>{label}</FieldLabel>
        <FieldDescription className='mt-0.5'>{description}</FieldDescription>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </Field>
  )
}

type DetectionSettingsDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  options: VeridropDetectionOptions
  savedOptions: VeridropDetectionOptions
  onChange: (options: VeridropDetectionOptions) => void
  onSave: () => void
  isSaving: boolean
}

function DetectionSettingsDialog({
  open,
  onOpenChange,
  options,
  savedOptions,
  onChange,
  onSave,
  isSaving,
}: DetectionSettingsDialogProps) {
  const { t } = useTranslation()
  const dirty = hasUnsavedVeridropChanges(options, savedOptions)

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Detection Settings')}
      description={t(
        'Configure the Veridrop monitor backend used by this page.'
      )}
      contentClassName='sm:max-w-4xl'
      contentHeight='min(70vh, 620px)'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='button' onClick={onSave} disabled={!dirty || isSaving}>
            <Save data-icon='inline-start' className='size-4' />
            {isSaving ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <FieldGroup className='grid gap-4 lg:grid-cols-2'>
        <SwitchRow
          label={t('Enable Veridrop Detection')}
          description={t(
            'Allow detection tasks to call the configured Veridrop backend.'
          )}
          checked={options.enabled}
          onCheckedChange={(enabled) => onChange({ ...options, enabled })}
        />
        <SwitchRow
          label={t('Scheduled Detection')}
          description={t(
            'Automatically run batch detection for enabled channels at the configured interval.'
          )}
          checked={options.auto_detection_enabled}
          onCheckedChange={(auto_detection_enabled) =>
            onChange({ ...options, auto_detection_enabled })
          }
        />
        <SettingField
          label={t('Detection Interval (minutes)')}
          description={t(
            'Time between scheduled batch detections. This is separate from task status polling.'
          )}
        >
          <Input
            type='number'
            min={15}
            max={43200}
            disabled={!options.auto_detection_enabled}
            value={numberInputValue(options.detection_interval_minutes)}
            onChange={(event) =>
              onChange({
                ...options,
                detection_interval_minutes: clampInteger(
                  event.target.value,
                  VERIDROP_DEFAULT_OPTIONS.detection_interval_minutes,
                  15,
                  43200
                ),
              })
            }
          />
        </SettingField>
        <SettingField
          label={t('Veridrop Base URL')}
          description={t('The backend address of veridrop-monitor.')}
        >
          <Input
            value={options.base_url}
            placeholder='https://'
            onChange={(event) =>
              onChange({ ...options, base_url: event.target.value })
            }
          />
        </SettingField>
        <SettingField
          label={t('Admin API Key')}
          description={t('Leave blank to keep the existing secret unchanged.')}
        >
          <Input
            type='password'
            value={options.admin_api_key}
            placeholder='sk-...'
            autoComplete='new-password'
            onChange={(event) =>
              onChange({ ...options, admin_api_key: event.target.value })
            }
          />
        </SettingField>
        <SettingField label={t('Detection Mode')}>
          <Select
            value={options.default_mode}
            onValueChange={(default_mode) =>
              onChange({ ...options, default_mode: default_mode ?? 'quick' })
            }
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropModeLabel(options.default_mode, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value='quick'>{t('Quick')}</SelectItem>
                <SelectItem value='standard'>{t('Standard')}</SelectItem>
                <SelectItem value='full'>{t('Full')}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField
          label={t('OpenAI Wire API')}
          description={t(
            'Optional wire protocol passed to OpenAI-compatible checks.'
          )}
        >
          <Select
            value={options.default_openai_wire_api}
            onValueChange={(default_openai_wire_api) =>
              onChange({
                ...options,
                default_openai_wire_api:
                  default_openai_wire_api ?? 'chat_completions',
              })
            }
          >
            <SelectTrigger className='w-full'>
              <SelectValue>{options.default_openai_wire_api}</SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value='chat_completions'>
                  chat_completions
                </SelectItem>
                <SelectItem value='responses'>responses</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField label={t('Max Concurrent Detections')}>
          <Input
            type='number'
            min={1}
            max={20}
            value={numberInputValue(options.max_concurrent)}
            onChange={(event) =>
              onChange({
                ...options,
                max_concurrent: clampInteger(
                  event.target.value,
                  VERIDROP_DEFAULT_OPTIONS.max_concurrent,
                  1,
                  20
                ),
              })
            }
          />
        </SettingField>
        <SwitchRow
          label={t('Long-context Probe')}
          description={t(
            'Run the longer probe when the monitor backend supports it.'
          )}
          checked={options.include_long_context}
          onCheckedChange={(include_long_context) =>
            onChange({ ...options, include_long_context })
          }
        />
        <SwitchRow
          label={t('Extreme Long-context Probe')}
          description={t(
            'Use only when the upstream and monitor backend can tolerate the cost.'
          )}
          checked={options.include_long_context_extreme}
          onCheckedChange={(include_long_context_extreme) =>
            onChange({ ...options, include_long_context_extreme })
          }
        />
      </FieldGroup>
    </Dialog>
  )
}

type DetectionRunToolbarProps = {
  activeTask: SystemTask | null
  totalChannelCount: number
  targetModelCount: number
  skippedChannelCount: number
  onRefresh: () => void
  isRefreshing: boolean
}

function DetectionRunToolbar({
  activeTask,
  totalChannelCount,
  targetModelCount,
  skippedChannelCount,
  onRefresh,
  isRefreshing,
}: DetectionRunToolbarProps) {
  const { t, i18n } = useTranslation()
  const taskProgress = (activeTask?.state as { progress?: unknown } | undefined)
    ?.progress

  return (
    <div className='flex flex-col gap-3 border-b py-2.5 lg:flex-row lg:items-center lg:justify-between'>
      <div className='flex min-w-0 items-center gap-2'>
        <span className='bg-muted text-muted-foreground inline-flex size-7 shrink-0 items-center justify-center rounded-md'>
          <ListChecks className='size-4' aria-hidden='true' />
        </span>
        {activeTask != null ? (
          <div className='flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1'>
            <TaskStatusBadge status={activeTask.status} />
            {typeof taskProgress === 'number' && (
              <span className='text-sm font-medium tabular-nums'>
                {Math.min(100, Math.max(0, taskProgress))}%
              </span>
            )}
            <span
              className='text-muted-foreground truncate text-xs'
              title={formatTimestampToDate(activeTask.updated_at)}
            >
              {t('Updated {{time}}', {
                time: formatTimestampRelative(
                  activeTask.updated_at,
                  'seconds',
                  toIntlLocale(i18n.language)
                ),
              })}
            </span>
          </div>
        ) : (
          <span className='text-muted-foreground text-sm'>
            {t('No task is running')}
          </span>
        )}
      </div>
      <div className='flex flex-wrap items-center gap-2 lg:justify-end'>
        <Badge variant='outline'>
          {t('{{channels}} channels · {{models}} models', {
            channels: totalChannelCount,
            models: targetModelCount,
          })}
        </Badge>
        {skippedChannelCount > 0 && (
          <Badge variant='secondary'>
            {t('{{count}} skipped', { count: skippedChannelCount })}
          </Badge>
        )}
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={onRefresh}
          disabled={isRefreshing}
        >
          <RefreshCw
            data-icon='inline-start'
            className={cn('size-4', isRefreshing && 'animate-spin')}
          />
          {isRefreshing ? t('Refreshing...') : t('Refresh')}
        </Button>
      </div>
    </div>
  )
}

type ManualDetectionCardProps = {
  form: VeridropManualDetectionRequest
  options: VeridropDetectionOptions
  settingsDirty: boolean
  onChange: (form: VeridropManualDetectionRequest) => void
  onSubmit: () => void
  isSubmitting: boolean
}

function ManualDetectionCard({
  form,
  options,
  settingsDirty,
  onChange,
  onSubmit,
  isSubmitting,
}: ManualDetectionCardProps) {
  const { t } = useTranslation()
  const [fetchedModels, setFetchedModels] = useState<string[]>([])
  const modelSourceRevision = useRef(0)
  const modelOptions = useMemo(
    () => fetchedModels.map((model) => ({ label: model, value: model })),
    [fetchedModels]
  )
  const canFetchModels =
    form.base_url.trim() !== '' &&
    form.base_url.trim() !== 'https://' &&
    form.api_key.trim() !== '' &&
    form.protocol.trim() !== ''
  const supportsLongContext = form.protocol !== 'gemini'
  const fetchModelsMutation = useMutation({
    mutationFn: ({
      request,
    }: {
      request: Pick<
        VeridropManualDetectionRequest,
        'base_url' | 'api_key' | 'protocol'
      >
      revision: number
    }) => fetchManualUpstreamModels(request),
    onSuccess: (models, variables) => {
      if (variables.revision !== modelSourceRevision.current) return
      setFetchedModels(models)
      if (models.length === 0) {
        toast.info(
          t('No models were returned. You can enter a model manually.')
        )
        return
      }
      toast.success(t('Fetched {{count}} models', { count: models.length }))
    },
    onError: (_error, variables) => {
      if (variables.revision !== modelSourceRevision.current) return
      toast.error(
        t(
          'Could not fetch models. Check the Base URL, API Key and protocol, then try again.'
        )
      )
    },
  })
  const clearFetchedModels = () => {
    modelSourceRevision.current += 1
    setFetchedModels([])
  }
  const canSubmit =
    !settingsDirty &&
    options.enabled &&
    options.base_url.trim() !== '' &&
    form.base_url.trim() !== '' &&
    form.base_url.trim() !== 'https://' &&
    form.api_key.trim() !== '' &&
    form.model.trim() !== '' &&
    form.protocol.trim() !== ''
  let disabledReason: string | null = null
  if (settingsDirty) {
    disabledReason = t('Save detection settings before starting.')
  } else if (!options.enabled || options.base_url.trim() === '') {
    disabledReason = t(
      'Complete and enable detection settings before starting.'
    )
  } else if (!canSubmit) {
    disabledReason = t(
      'Fill in Base URL, API Key, model and protocol before testing.'
    )
  }

  return (
    <section className='rounded-lg border px-3'>
      <div className='flex flex-col gap-3 border-b py-3 sm:flex-row sm:items-center sm:justify-between'>
        <div className='text-muted-foreground flex min-w-0 items-center gap-2 text-xs'>
          <span className='bg-muted inline-flex size-7 shrink-0 items-center justify-center rounded-md'>
            <FlaskConical className='size-4' aria-hidden='true' />
          </span>
          <span>
            {t('Test a temporary upstream without adding it as a channel.')}
          </span>
        </div>
        <Button
          type='button'
          size='sm'
          onClick={onSubmit}
          disabled={!canSubmit || isSubmitting}
          className='w-full sm:w-auto'
        >
          <Play data-icon='inline-start' className='size-4' />
          {isSubmitting ? t('Starting...') : t('Start Detection')}
        </Button>
      </div>
      <div className='grid gap-4 py-4 lg:grid-cols-2'>
        <SettingField label={t('Protocol')}>
          <Select
            value={form.protocol}
            onValueChange={(protocol) => {
              clearFetchedModels()
              onChange({ ...form, protocol: protocol ?? '' })
            }}
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropProtocolLabel(form.protocol, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value='openai'>{t('OpenAI Compatible')}</SelectItem>
                <SelectItem value='anthropic'>{t('Anthropic')}</SelectItem>
                <SelectItem value='gemini'>{t('Gemini')}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField label={t('Detection Mode')}>
          <Select
            value={form.mode || options.default_mode}
            onValueChange={(mode) => onChange({ ...form, mode: mode ?? '' })}
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropModeLabel(form.mode || options.default_mode, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value='quick'>{t('Quick')}</SelectItem>
                <SelectItem value='standard'>{t('Standard')}</SelectItem>
                <SelectItem value='full'>{t('Full')}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField
          label={t('Upstream Base URL')}
          description={t('The temporary upstream address to test.')}
        >
          <Input
            value={form.base_url}
            placeholder='https://api.example.com/v1'
            onChange={(event) => {
              clearFetchedModels()
              onChange({ ...form, base_url: event.target.value })
            }}
          />
        </SettingField>
        <SettingField
          label={t('API Key')}
          description={t(
            'Only used for this test and never stored in system tasks.'
          )}
        >
          <Input
            type='password'
            value={form.api_key}
            placeholder='sk-...'
            autoComplete='new-password'
            onChange={(event) => {
              clearFetchedModels()
              onChange({ ...form, api_key: event.target.value })
            }}
          />
        </SettingField>
        <SettingField
          label={t('Model')}
          description={
            canFetchModels
              ? t('Fetch available models from upstream')
              : t(
                  'Enter the upstream Base URL and API Key before fetching models.'
                )
          }
        >
          <div className='flex flex-col gap-2 sm:flex-row'>
            <div className='min-w-0 flex-1'>
              <Combobox
                options={modelOptions}
                value={form.model}
                onValueChange={(model) =>
                  onChange({ ...form, model: model ?? '' })
                }
                placeholder={t('Select or enter a model')}
                emptyText='No models fetched from upstream'
                allowCustomValue
              />
            </div>
            <Button
              type='button'
              variant='outline'
              onClick={() =>
                fetchModelsMutation.mutate({
                  request: {
                    base_url: form.base_url,
                    api_key: form.api_key,
                    protocol: form.protocol,
                  },
                  revision: modelSourceRevision.current,
                })
              }
              disabled={!canFetchModels || fetchModelsMutation.isPending}
              aria-busy={fetchModelsMutation.isPending}
              className='w-full shrink-0 sm:w-auto'
            >
              <RefreshCw
                data-icon='inline-start'
                className={cn(
                  'size-4',
                  fetchModelsMutation.isPending && 'animate-spin'
                )}
              />
              {t('Fetch Models')}
            </Button>
          </div>
        </SettingField>
        {form.protocol === 'openai' ? (
          <SettingField
            label={t('OpenAI Wire API')}
            description={t('Only used when protocol is openai.')}
          >
            <Select
              value={form.openai_wire_api || 'chat_completions'}
              onValueChange={(openai_wire_api) =>
                onChange({
                  ...form,
                  openai_wire_api: openai_wire_api ?? 'chat_completions',
                })
              }
            >
              <SelectTrigger className='w-full'>
                <SelectValue>
                  {form.openai_wire_api || 'chat_completions'}
                </SelectValue>
              </SelectTrigger>
              <SelectContent align='start' alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='chat_completions'>
                    chat_completions
                  </SelectItem>
                  <SelectItem value='responses'>responses</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </SettingField>
        ) : null}
        {supportsLongContext ? (
          <>
            <SwitchRow
              label={t('Long-context Probe')}
              description={t(
                'Run the longer probe when the monitor backend supports it.'
              )}
              checked={Boolean(form.include_long_context)}
              onCheckedChange={(include_long_context) =>
                onChange({ ...form, include_long_context })
              }
            />
            <SwitchRow
              label={t('Extreme Long-context Probe')}
              description={t(
                'Use only when the upstream and monitor backend can tolerate the cost.'
              )}
              checked={Boolean(form.include_long_context_extreme)}
              onCheckedChange={(include_long_context_extreme) =>
                onChange({ ...form, include_long_context_extreme })
              }
            />
          </>
        ) : null}
        <SwitchRow
          label={t('Skip availability pre-check')}
          description={t(
            'Start detection even if the preliminary model check fails.'
          )}
          checked={Boolean(form.force)}
          onCheckedChange={(force) => onChange({ ...form, force })}
        />
      </div>
      {disabledReason != null && (
        <div className='text-muted-foreground flex items-start gap-2 border-t py-2 text-xs'>
          <AlertTriangle className='mt-0.5 size-4 shrink-0' />
          <span>{disabledReason}</span>
        </div>
      )}
    </section>
  )
}

type DetectionTargetsContentProps = {
  targets: VeridropDetectionTarget[]
  loading: boolean
  isError: boolean
  onRetry: () => void
  disabledReason: string | null
  onDetectBatch: (channelIds: number[]) => void
  onDetectOne: (channelId: number) => void
  pendingBatch: boolean
  pendingChannelId: number | null
}

function getTargetSkippedLabel(
  reason: string | undefined,
  t: (key: string) => string
) {
  if (reason === 'unsupported channel protocol') {
    return t('Unsupported protocol')
  }
  if (reason === 'channel has no enabled models') {
    return t('No enabled models')
  }
  return reason != null && reason !== '' ? reason : t('Not included')
}

function DetectionTargetsContent({
  targets,
  loading,
  isError,
  onRetry,
  disabledReason,
  onDetectBatch,
  onDetectOne,
  pendingBatch,
  pendingChannelId,
}: DetectionTargetsContentProps) {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const debouncedKeyword = useDebounce(keyword, 300).trim().toLowerCase()
  const [statusFilter, setStatusFilter] = useState('all')
  const [protocolFilter, setProtocolFilter] = useState('all')
  const [availabilityFilter, setAvailabilityFilter] = useState('all')
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const filteredTargets = useMemo(
    () =>
      targets.filter((target) => {
        const models = Array.isArray(target.models) ? target.models : []
        const ready = !target.skipped_reason && models.length > 0
        if (statusFilter === 'enabled' && target.status !== 1) return false
        if (statusFilter === 'disabled' && target.status === 1) return false
        if (protocolFilter !== 'all' && target.protocol !== protocolFilter) {
          return false
        }
        if (availabilityFilter === 'ready' && !ready) return false
        if (availabilityFilter === 'unavailable' && ready) return false
        if (debouncedKeyword === '') return true
        return [
          target.channel_name,
          String(target.channel_id),
          target.base_url,
          target.protocol,
          ...models,
        ].some((value) => value.toLowerCase().includes(debouncedKeyword))
      }),
    [
      availabilityFilter,
      debouncedKeyword,
      protocolFilter,
      statusFilter,
      targets,
    ]
  )
  const readyFilteredIds = useMemo(
    () =>
      filteredTargets
        .filter(
          (target) =>
            !target.skipped_reason &&
            Array.isArray(target.models) &&
            target.models.length > 0
        )
        .map((target) => target.channel_id),
    [filteredTargets]
  )
  const selectedIdSet = useMemo(
    () => new Set(selectedChannelIds),
    [selectedChannelIds]
  )
  const allReadySelected =
    readyFilteredIds.length > 0 &&
    readyFilteredIds.every((channelId) => selectedIdSet.has(channelId))
  let statusFilterLabel = t('All channel statuses')
  if (statusFilter === 'enabled') statusFilterLabel = t('Enabled')
  if (statusFilter === 'disabled') statusFilterLabel = t('Disabled')
  let availabilityFilterLabel = t('All channels')
  if (availabilityFilter === 'ready') {
    availabilityFilterLabel = t('Ready for detection')
  }
  if (availabilityFilter === 'unavailable') {
    availabilityFilterLabel = t('Unavailable for detection')
  }

  useEffect(() => {
    const visibleReadyIds = new Set(readyFilteredIds)
    setSelectedChannelIds((current) =>
      current.filter((channelId) => visibleReadyIds.has(channelId))
    )
  }, [readyFilteredIds])

  let content: ReactNode

  if (loading) {
    content = (
      <div className='flex flex-col gap-2'>
        {['target-skeleton-1', 'target-skeleton-2', 'target-skeleton-3'].map(
          (key) => (
            <Skeleton key={key} className='h-9 w-full rounded-md' />
          )
        )}
      </div>
    )
  } else if (isError) {
    content = (
      <ErrorState
        title={t('We could not load detection coverage.')}
        description={t('Refresh the page or try again later.')}
        onRetry={onRetry}
        className='min-h-[180px]'
      />
    )
  } else if (targets.length === 0) {
    content = (
      <PanelEmptyState
        icon={
          <ListChecks
            className='text-muted-foreground size-5'
            aria-hidden='true'
          />
        }
        message={t('No channels are available for detection.')}
      />
    )
  } else if (filteredTargets.length === 0) {
    content = (
      <PanelEmptyState
        icon={<ListChecks className='text-muted-foreground size-5' />}
        message={t('No channels match the current filters.')}
      />
    )
  } else {
    content = (
      <div className='overflow-x-auto'>
        <Table className='min-w-[1020px]'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead className='h-9 w-10 px-3'>
                <Checkbox
                  checked={allReadySelected}
                  onCheckedChange={(checked) =>
                    setSelectedChannelIds(checked ? readyFilteredIds : [])
                  }
                  disabled={readyFilteredIds.length === 0 || pendingBatch}
                  aria-label={t('Select all filtered channels')}
                />
              </TableHead>
              <TableHead className='h-9 min-w-[220px] text-xs'>
                {t('Channel')}
              </TableHead>
              <TableHead className='h-9 min-w-[220px] text-xs'>
                {t('Base URL')}
              </TableHead>
              <TableHead className='h-9 w-[120px] text-xs'>
                {t('Protocol')}
              </TableHead>
              <TableHead className='h-9 min-w-[260px] text-xs'>
                {t('Models')}
              </TableHead>
              <TableHead className='h-9 w-[150px] pr-4 text-right text-xs'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredTargets.map((target) => {
              const models = Array.isArray(target.models) ? target.models : []
              const skipped =
                target.skipped_reason != null && target.skipped_reason !== ''
              return (
                <TableRow
                  key={target.channel_id}
                  className={cn(
                    'hover:bg-muted/30 transition-colors [contain-intrinsic-size:auto_52px] [content-visibility:auto]',
                    selectedIdSet.has(target.channel_id) &&
                      'bg-primary/5 hover:bg-primary/10'
                  )}
                >
                  <TableCell className='px-3 py-2.5 align-middle'>
                    <Checkbox
                      checked={selectedIdSet.has(target.channel_id)}
                      onCheckedChange={(checked) => {
                        setSelectedChannelIds((current) => {
                          if (!checked) {
                            return current.filter(
                              (channelId) => channelId !== target.channel_id
                            )
                          }
                          return current.includes(target.channel_id)
                            ? current
                            : [...current, target.channel_id]
                        })
                      }}
                      disabled={skipped || pendingBatch}
                      aria-label={t('Select channel {{name}}', {
                        name: target.channel_name,
                      })}
                    />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <div className='flex min-w-0 items-center gap-2'>
                      {skipped ? (
                        <AlertTriangle className='text-muted-foreground size-4 shrink-0' />
                      ) : (
                        <CheckCircle2 className='size-4 shrink-0 text-emerald-600 dark:text-emerald-400' />
                      )}
                      <div className='min-w-0'>
                        <div className='flex items-center gap-2'>
                          <span className='truncate text-sm font-medium'>
                            {target.channel_name || `#${target.channel_id}`}
                          </span>
                          <Badge
                            variant={
                              target.status === 1 ? 'secondary' : 'outline'
                            }
                            className='text-[10px]'
                          >
                            {target.status === 1 ? t('Enabled') : t('Disabled')}
                          </Badge>
                        </div>
                        <span className='text-muted-foreground font-mono text-[10px]'>
                          #{target.channel_id}
                        </span>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell
                    className='text-muted-foreground max-w-[260px] truncate py-2.5 align-middle font-mono text-[11px]'
                    title={target.base_url}
                  >
                    {target.base_url || '-'}
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <Badge variant='outline' className='text-[11px]'>
                      {target.protocol || target.channel_type_name}
                    </Badge>
                  </TableCell>
                  <TableCell className='max-w-[320px] py-2.5 align-middle whitespace-normal'>
                    {skipped ? (
                      <span className='text-muted-foreground text-xs'>
                        {getTargetSkippedLabel(target.skipped_reason, t)}
                      </span>
                    ) : (
                      <div className='flex flex-wrap gap-1.5'>
                        {models.slice(0, 3).map((model) => (
                          <Badge
                            key={`${target.channel_id}-${model}`}
                            variant='secondary'
                            className='max-w-[220px] truncate font-mono text-[11px]'
                          >
                            {model}
                          </Badge>
                        ))}
                        {models.length > 3 && (
                          <Badge variant='outline' className='text-[11px]'>
                            {t('+{{count}} more', {
                              count: models.length - 3,
                            })}
                          </Badge>
                        )}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className='py-2.5 pr-4 text-right align-middle'>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      disabled={
                        skipped ||
                        disabledReason != null ||
                        pendingBatch ||
                        pendingChannelId != null
                      }
                      onClick={() => onDetectOne(target.channel_id)}
                    >
                      {pendingChannelId === target.channel_id ? (
                        <Loader2
                          data-icon='inline-start'
                          className='size-4 animate-spin'
                        />
                      ) : (
                        <Play data-icon='inline-start' className='size-4' />
                      )}
                      {pendingChannelId === target.channel_id
                        ? t('Detecting...')
                        : t('Detect')}
                    </Button>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <div aria-busy={loading} className='pt-3'>
      <div className='border-b pb-3'>
        <div className='grid gap-2 md:grid-cols-2 2xl:grid-cols-[minmax(320px,1fr)_180px_160px_180px] 2xl:items-end'>
          <div className='md:col-span-2 2xl:col-span-1'>
            <CompactField label={t('Search')}>
              <Input
                value={keyword}
                onChange={(event) => setKeyword(event.target.value)}
                placeholder={t('Search channels, IDs, API URLs or models...')}
              />
            </CompactField>
          </div>
          <CompactField label={t('Status')}>
            <Select
              value={statusFilter}
              onValueChange={(value) => setStatusFilter(value ?? 'all')}
            >
              <SelectTrigger className='w-full'>
                <SelectValue>{statusFilterLabel}</SelectValue>
              </SelectTrigger>
              <SelectContent align='start' alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>
                    {t('All channel statuses')}
                  </SelectItem>
                  <SelectItem value='enabled'>{t('Enabled')}</SelectItem>
                  <SelectItem value='disabled'>{t('Disabled')}</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </CompactField>
          <CompactField label={t('Protocol')}>
            <Select
              value={protocolFilter}
              onValueChange={(value) => setProtocolFilter(value ?? 'all')}
            >
              <SelectTrigger className='w-full'>
                <SelectValue>
                  {protocolFilter === 'all'
                    ? t('All protocols')
                    : getVeridropProtocolLabel(protocolFilter, t)}
                </SelectValue>
              </SelectTrigger>
              <SelectContent align='start' alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All protocols')}</SelectItem>
                  <SelectItem value='openai'>OpenAI</SelectItem>
                  <SelectItem value='anthropic'>Anthropic</SelectItem>
                  <SelectItem value='gemini'>Gemini</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </CompactField>
          <CompactField label={t('Availability')}>
            <Select
              value={availabilityFilter}
              onValueChange={(value) => setAvailabilityFilter(value ?? 'all')}
            >
              <SelectTrigger className='w-full'>
                <SelectValue>{availabilityFilterLabel}</SelectValue>
              </SelectTrigger>
              <SelectContent align='start' alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All channels')}</SelectItem>
                  <SelectItem value='ready'>
                    {t('Ready for detection')}
                  </SelectItem>
                  <SelectItem value='unavailable'>
                    {t('Unavailable for detection')}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </CompactField>
        </div>
        <div className='mt-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
          <span className='text-muted-foreground text-xs'>
            {t('{{visible}} of {{total}} channels · {{selected}} selected', {
              visible: filteredTargets.length,
              total: targets.length,
              selected: selectedChannelIds.length,
            })}
          </span>
          <div className='flex flex-wrap gap-2 sm:justify-end'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={
                disabledReason != null ||
                pendingBatch ||
                readyFilteredIds.length === 0
              }
              onClick={() => onDetectBatch(readyFilteredIds)}
            >
              <Play data-icon='inline-start' className='size-4' />
              {t('Detect current results ({{count}})', {
                count: readyFilteredIds.length,
              })}
            </Button>
            <Button
              type='button'
              size='sm'
              disabled={
                disabledReason != null ||
                pendingBatch ||
                selectedChannelIds.length === 0
              }
              onClick={() => onDetectBatch(selectedChannelIds)}
            >
              {pendingBatch ? (
                <Loader2
                  data-icon='inline-start'
                  className='size-4 animate-spin'
                />
              ) : (
                <Play data-icon='inline-start' className='size-4' />
              )}
              {t('Detect selected ({{count}})', {
                count: selectedChannelIds.length,
              })}
            </Button>
          </div>
        </div>
      </div>
      {disabledReason != null && (
        <div className='text-muted-foreground flex items-start gap-2 border-b py-2 text-xs'>
          <AlertTriangle className='mt-0.5 size-4 shrink-0' />
          <span>{disabledReason}</span>
        </div>
      )}
      {content}
    </div>
  )
}

type DetectionResultsTableProps = {
  results: VeridropDetectionResult[]
  loading: boolean
  emptyMessage: string
  sort: DetectionResultSort
  onSort: (sortBy: VeridropDetectionSortBy) => void
}

function DetectionResultSortHead({
  label,
  sortBy,
  sort,
  onSort,
  className,
}: {
  label: string
  sortBy: VeridropDetectionSortBy
  sort: DetectionResultSort
  onSort: (sortBy: VeridropDetectionSortBy) => void
  className?: string
}) {
  const { t } = useTranslation()
  const active = sort.by === sortBy
  const defaultOrder: VeridropDetectionSortOrder =
    sortBy === 'updated_at' ? 'desc' : 'asc'
  let nextOrder = defaultOrder
  let ariaSort: 'ascending' | 'descending' | 'none' = 'none'
  let sortIcon: ReactNode = (
    <ArrowUpDown
      className='text-muted-foreground size-3.5'
      aria-hidden='true'
    />
  )
  if (active) {
    nextOrder = sort.order === 'asc' ? 'desc' : 'asc'
    ariaSort = sort.order === 'asc' ? 'ascending' : 'descending'
    sortIcon =
      sort.order === 'asc' ? (
        <ArrowUp className='size-3.5' aria-hidden='true' />
      ) : (
        <ArrowDown className='size-3.5' aria-hidden='true' />
      )
  }

  return (
    <TableHead aria-sort={ariaSort} className={cn('h-9 text-xs', className)}>
      <Button
        type='button'
        variant='ghost'
        size='sm'
        className='-ml-3 h-8 gap-1 px-3 text-xs'
        aria-label={`${label}: ${t(nextOrder === 'asc' ? 'Asc' : 'Desc')}`}
        onClick={() => onSort(sortBy)}
      >
        {label}
        {sortIcon}
      </Button>
    </TableHead>
  )
}

function DetectionResultsTable({
  results,
  loading,
  emptyMessage,
  sort,
  onSort,
}: DetectionResultsTableProps) {
  const { t, i18n } = useTranslation()
  const [detailsSelection, setDetailsSelection] = useState<{
    result: VeridropDetectionResult
    report: VeridropScoreReport
  } | null>(null)

  if (loading) {
    const skeletonRows = [
      'detection-result-skeleton-1',
      'detection-result-skeleton-2',
      'detection-result-skeleton-3',
      'detection-result-skeleton-4',
      'detection-result-skeleton-5',
      'detection-result-skeleton-6',
    ]
    return (
      <div className='flex flex-col gap-2'>
        {skeletonRows.map((key) => (
          <Skeleton key={key} className='h-10 w-full rounded-md' />
        ))}
      </div>
    )
  }

  if (results.length === 0) {
    return (
      <PanelEmptyState
        icon={
          <Activity
            className='text-muted-foreground size-5'
            aria-hidden='true'
          />
        }
        message={emptyMessage}
      />
    )
  }

  return (
    <>
      <div className='overflow-x-auto'>
        <Table className='min-w-[1000px]'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <DetectionResultSortHead
                label={t('Result')}
                sortBy='score'
                sort={sort}
                onSort={onSort}
                className='min-w-[210px] pl-4'
              />
              <DetectionResultSortHead
                label={t('Channel')}
                sortBy='channel_name'
                sort={sort}
                onSort={onSort}
                className='min-w-[220px]'
              />
              <DetectionResultSortHead
                label={t('Model')}
                sortBy='model'
                sort={sort}
                onSort={onSort}
                className='min-w-[160px]'
              />
              <TableHead className='h-9 min-w-[260px] text-xs'>
                {t('Details')}
              </TableHead>
              <DetectionResultSortHead
                label={t('Updated')}
                sortBy='updated_at'
                sort={sort}
                onSort={onSort}
                className='w-[140px] pr-4'
              />
            </TableRow>
          </TableHeader>
          <TableBody>
            {results.map((result) => {
              const message = getVeridropResultDisplayMessage(result)
              const score =
                result.status === 'done'
                  ? formatResultScore(result.score)
                  : null
              const manual = result.channel_id === 0
              const hasScoreReport = hasVeridropScoreReport(result.result_json)
              const belowThreshold = isBelowThresholdDetectionResult(result)
              const statusLabel = getResultStatusLabel(result, t)
                .trim()
                .toLocaleLowerCase()
              const verdict = result.verdict.trim().toLocaleLowerCase()
              const normalizedMessage = message.trim().toLocaleLowerCase()
              const detailMessage =
                normalizedMessage !== '' &&
                normalizedMessage !== statusLabel &&
                normalizedMessage !== verdict
                  ? message
                  : ''
              return (
                <TableRow
                  key={result.id}
                  className={cn(
                    'hover:bg-muted/30 [contain-intrinsic-size:auto_64px] [content-visibility:auto]',
                    belowThreshold && 'bg-destructive/5 hover:bg-destructive/10'
                  )}
                >
                  <TableCell className='px-4 py-3 align-middle'>
                    <div className='flex flex-wrap items-center gap-1.5'>
                      <ResultStatusBadge result={result} score={score} />
                      {hasScoreReport ? (
                        <Button
                          type='button'
                          variant='link'
                          size='sm'
                          className='h-auto gap-1 px-1 text-xs'
                          onClick={() => {
                            const report = parseVeridropScoreReport(
                              result.result_json ?? ''
                            )
                            if (report == null) {
                              toast.error(
                                t(
                                  'No scoring details are available for this result.'
                                )
                              )
                              return
                            }
                            setDetailsSelection({ result, report })
                          }}
                        >
                          <ListChecks className='size-3.5' />
                          {t('View details')}
                        </Button>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell className='py-3 align-middle'>
                    <div className='min-w-0'>
                      <div className='truncate text-sm font-medium'>
                        {manual
                          ? t('Manual Test')
                          : result.channel_name || `#${result.channel_id}`}
                      </div>
                      <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                        {!manual && (
                          <span className='text-muted-foreground font-mono text-[11px]'>
                            #{result.channel_id}
                          </span>
                        )}
                        {result.protocol !== '' && (
                          <Badge variant='outline' className='text-[11px]'>
                            {result.protocol}
                          </Badge>
                        )}
                        {result.mode !== '' && (
                          <Badge variant='secondary' className='text-[11px]'>
                            {getVeridropModeLabel(result.mode, t)}
                          </Badge>
                        )}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className='max-w-[200px] truncate py-3 align-middle font-mono text-xs'>
                    {result.model || '-'}
                  </TableCell>
                  <TableCell
                    className='text-muted-foreground line-clamp-2 max-w-[420px] py-3 align-middle text-sm whitespace-normal'
                    title={detailMessage}
                  >
                    {detailMessage || '—'}
                  </TableCell>
                  <TableCell
                    className='text-muted-foreground py-3 pr-4 align-middle text-xs whitespace-nowrap'
                    title={formatTimestampToDate(result.updated_at)}
                  >
                    {formatTimestampRelative(
                      result.updated_at,
                      'seconds',
                      toIntlLocale(i18n.language)
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
      <ScoreDetailsDialog
        result={detailsSelection?.result ?? null}
        report={detailsSelection?.report ?? null}
        onOpenChange={(open) => {
          if (!open) setDetailsSelection(null)
        }}
      />
    </>
  )
}

export function VeridropDetection() {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((state) => state.auth.user)
  const canEditVeridrop = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.VERIDROP_DETECTION,
    ADMIN_PERMISSION_ACTIONS.EDIT
  )
  const [draftOptions, setDraftOptions] = useState<VeridropDetectionOptions>(
    VERIDROP_DEFAULT_OPTIONS
  )
  const [runConfirmOpen, setRunConfirmOpen] = useState(false)
  const [pendingBatchChannelIds, setPendingBatchChannelIds] = useState<
    number[]
  >([])
  const [pendingChannelId, setPendingChannelId] = useState<number | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [cleanupOpen, setCleanupOpen] = useState(false)
  const [cleanupRetentionDays, setCleanupRetentionDays] = useState('30')
  const [activeTab, setActiveTab] = useState<DetectionWorkspaceTab>('results')
  const visibleActiveTab = canEditVeridrop ? activeTab : 'results'
  const [manualForm, setManualForm] =
    useState<VeridropManualDetectionRequest>(DEFAULT_MANUAL_FORM)
  const [resultOutcomeFilters, setResultOutcomeFilters] = useState<
    ResultOutcomeFilter[]
  >([])
  const [resultChannelFilter, setResultChannelFilter] = useState('')
  const debouncedResultChannelFilter = useDebounce(resultChannelFilter, 400)
  const [resultModelFilter, setResultModelFilter] = useState('')
  const debouncedResultModelFilter = useDebounce(resultModelFilter, 400)
  const [resultModeFilter, setResultModeFilter] =
    useState<ResultModeFilter>('all')
  const [resultBatchFilter, setResultBatchFilter] =
    useState<ResultBatchFilter>('all')
  const [resultTimeFilter, setResultTimeFilter] =
    useState<ResultTimeFilter>('all')
  const [resultSort, setResultSort] = useState<DetectionResultSort>({
    by: 'updated_at',
    order: 'desc',
  })

  const optionsQuery = useQuery({
    queryKey: ['veridrop-detection', 'options'],
    queryFn: getVeridropOptions,
    staleTime: 60 * 1000,
    enabled: canEditVeridrop,
  })

  const resultFilters = useMemo(() => {
    return {
      channel: debouncedResultChannelFilter.trim(),
      model: debouncedResultModelFilter.trim(),
      outcomes: resultOutcomeFilters,
      mode: resultModeFilter,
      batch: resultBatchFilter,
      time: resultTimeFilter,
      sortBy: resultSort.by,
      sortOrder: resultSort.order,
    }
  }, [
    debouncedResultChannelFilter,
    debouncedResultModelFilter,
    resultModeFilter,
    resultBatchFilter,
    resultOutcomeFilters,
    resultSort.by,
    resultSort.order,
    resultTimeFilter,
  ])

  const resultsQuery = useQuery({
    queryKey: ['veridrop-detection', 'results', resultFilters],
    queryFn: async () => {
      const request: Parameters<typeof listVeridropDetectionResults>[0] = {
        limit: RESULT_LIMIT,
        sort_by: resultFilters.sortBy,
        sort_order: resultFilters.sortOrder,
      }
      const updatedAfter = getResultSinceSeconds(
        resultFilters.time,
        Math.floor(Date.now() / 1000)
      )
      if (resultFilters.outcomes.length > 0) {
        request.outcomes = resultFilters.outcomes
      }
      if (resultFilters.channel !== '') {
        request.channel_name = resultFilters.channel
      }
      if (resultFilters.model !== '') request.model = resultFilters.model
      if (resultFilters.mode !== 'all') request.mode = resultFilters.mode
      if (resultFilters.batch === 'latest') request.batch = 'latest'
      if (updatedAfter > 0) request.updated_after = updatedAfter

      const res = await listVeridropDetectionResults(request)
      if (!res.success || !Array.isArray(res.data?.items)) {
        throw new Error(
          res.message || t('We could not load detection results.')
        )
      }
      return res.data
    },
    retry: false,
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      resultFilters.batch === 'latest' ||
      query.state.data?.items.some((result) => isActiveResult(result))
        ? ACTIVE_POLL_INTERVAL_MS
        : false,
  })

  const tasksQuery = useQuery({
    queryKey: ['veridrop-detection', 'tasks'],
    queryFn: async () => {
      const res = await listVeridropSystemTasks(30)
      if (!res.success || !Array.isArray(res.data)) {
        throw new Error(res.message || t('We could not load system tasks.'))
      }
      return res.data.filter(
        (task) =>
          task.type === 'veridrop_detection' ||
          task.type === 'veridrop_detection_single' ||
          task.type === 'veridrop_detection_cleanup'
      )
    },
    retry: false,
    enabled: visibleActiveTab === 'batch' || visibleActiveTab === 'results',
    refetchInterval: (query) =>
      query.state.data?.some((task) => isActiveTask(task))
        ? ACTIVE_POLL_INTERVAL_MS
        : false,
  })

  const targetRequest = useMemo(
    () => ({
      scope: 'all' as const,
      mode: draftOptions.default_mode,
      include_long_context: draftOptions.include_long_context,
      include_long_context_extreme: draftOptions.include_long_context_extreme,
      openai_wire_api: draftOptions.default_openai_wire_api,
    }),
    [
      draftOptions.default_mode,
      draftOptions.default_openai_wire_api,
      draftOptions.include_long_context,
      draftOptions.include_long_context_extreme,
    ]
  )

  const targetsQuery = useQuery({
    queryKey: ['veridrop-detection', 'targets', targetRequest],
    queryFn: async () => {
      const res = await listVeridropDetectionTargets(targetRequest)
      if (!res.success || res.data == null) {
        throw new Error(
          res.message || t('We could not load detection coverage.')
        )
      }
      return res.data
    },
    retry: false,
    enabled:
      visibleActiveTab === 'results' ||
      (canEditVeridrop && visibleActiveTab === 'batch'),
    staleTime: 30 * 1000,
  })

  useEffect(() => {
    if (optionsQuery.data != null) {
      setDraftOptions({ ...optionsQuery.data, admin_api_key: '' })
    }
  }, [optionsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () =>
      updateVeridropOptions(
        draftOptions,
        optionsQuery.data ?? VERIDROP_DEFAULT_OPTIONS
      ),
    onSuccess: (count) => {
      toast.success(
        count === 0
          ? t('No changes to save')
          : t('Setting updated successfully')
      )
      setSettingsOpen(false)
      queryClient.invalidateQueries({
        queryKey: ['veridrop-detection', 'options'],
      })
    },
    onError: () => {
      toast.error(t('Settings were not saved. Check the values and try again.'))
    },
  })

  const runMutation = useMutation({
    mutationFn: (channelIds: number[]) =>
      startChannelsVeridropDetection({
        channel_ids: channelIds,
        mode: draftOptions.default_mode,
        include_long_context: draftOptions.include_long_context,
        include_long_context_extreme: draftOptions.include_long_context_extreme,
        openai_wire_api: draftOptions.default_openai_wire_api,
      }),
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message ||
            t('Detection was not started. Check the settings and try again.')
        )
        return
      }
      toast.success(
        res.data.created
          ? t('Detection task started')
          : t('Detection task is already running')
      )
      queryClient.invalidateQueries({ queryKey: ['veridrop-detection'] })
    },
    onError: () => {
      toast.error(
        t('Detection was not started. Check the settings and try again.')
      )
    },
  })

  const singleRunMutation = useMutation({
    mutationFn: (channelId: number) => {
      setPendingChannelId(channelId)
      return startChannelVeridropDetection(channelId)
    },
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message ||
            t('Detection was not started. Check the settings and try again.')
        )
        return
      }
      toast.success(
        res.data.created
          ? t('Detection task started')
          : t('Detection task is already running')
      )
      queryClient.invalidateQueries({ queryKey: ['veridrop-detection'] })
    },
    onError: () => {
      toast.error(
        t('Detection was not started. Check the settings and try again.')
      )
    },
    onSettled: () => setPendingChannelId(null),
  })

  const manualMutation = useMutation({
    mutationFn: () =>
      startManualVeridropDetection({
        ...manualForm,
        mode: manualForm.mode || draftOptions.default_mode,
        include_long_context:
          manualForm.protocol === 'gemini'
            ? false
            : manualForm.include_long_context,
        include_long_context_extreme:
          manualForm.protocol === 'gemini'
            ? false
            : manualForm.include_long_context_extreme,
        openai_wire_api:
          manualForm.protocol === 'openai'
            ? manualForm.openai_wire_api || draftOptions.default_openai_wire_api
            : undefined,
      }),
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message ||
            t(
              'Manual detection was not started. Check the values and try again.'
            )
        )
        return
      }
      toast.success(t('Manual detection started'))
      setManualForm((current) => ({ ...current, api_key: '' }))
      queryClient.invalidateQueries({ queryKey: ['veridrop-detection'] })
    },
    onError: () => {
      toast.error(
        t('Manual detection was not started. Check the values and try again.')
      )
    },
  })

  const cleanupMutation = useMutation({
    mutationFn: () =>
      cleanupVeridropDetectionResults({
        retention_days: Number(cleanupRetentionDays),
      }),
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message || t('Record cleanup was not started. Try again later.')
        )
        return
      }
      toast.success(
        res.data.created
          ? t('Record cleanup started')
          : t('Record cleanup is already running')
      )
      setCleanupOpen(false)
      queryClient.invalidateQueries({
        queryKey: ['veridrop-detection', 'tasks'],
      })
      queryClient.invalidateQueries({
        queryKey: ['veridrop-detection', 'results'],
      })
    },
    onError: () => {
      toast.error(t('Record cleanup was not started. Try again later.'))
    },
  })

  const savedOptions = optionsQuery.data ?? VERIDROP_DEFAULT_OPTIONS
  const settingsDirty = hasUnsavedVeridropChanges(draftOptions, savedOptions)
  const handleSettingsOpenChange = (open: boolean) => {
    if (!open) {
      setDraftOptions({ ...savedOptions, admin_api_key: '' })
    }
    setSettingsOpen(open)
  }
  const tasks = tasksQuery.data ?? []
  const activeBatchTask =
    tasks.find(
      (task) => task.type === 'veridrop_detection' && isActiveTask(task)
    ) ?? null
  const activeSingleTask =
    tasks.find(
      (task) => task.type === 'veridrop_detection_single' && isActiveTask(task)
    ) ?? null
  const activeTask = activeBatchTask ?? activeSingleTask
  const activeCleanupTask =
    tasks.find(
      (task) => task.type === 'veridrop_detection_cleanup' && isActiveTask(task)
    ) ?? null
  const cleanupWasActiveRef = useRef(false)

  useEffect(() => {
    if (cleanupWasActiveRef.current && activeCleanupTask == null) {
      queryClient.invalidateQueries({
        queryKey: ['veridrop-detection', 'results'],
      })
    }
    cleanupWasActiveRef.current = activeCleanupTask != null
  }, [activeCleanupTask, queryClient])
  const results = resultsQuery.data?.items ?? EMPTY_DETECTION_RESULTS
  const resultSummary = resultsQuery.data?.summary
  const matchingResultCount = resultsQuery.data?.matching_count ?? 0
  let resultOutcomeTriggerLabel = t('All')
  if (resultOutcomeFilters.length === 1) {
    resultOutcomeTriggerLabel = getResultOutcomeFilterLabel(
      resultOutcomeFilters[0],
      t
    )
  } else if (resultOutcomeFilters.length > 1) {
    resultOutcomeTriggerLabel = t('Selected {{count}}', {
      count: resultOutcomeFilters.length,
    })
  }
  const resultOutcomeReportLabel =
    resultOutcomeFilters.length === 0
      ? t('All')
      : resultOutcomeFilters
          .map((outcome) => getResultOutcomeFilterLabel(outcome, t))
          .join(', ')
  const hasActiveResultFilters =
    resultOutcomeFilters.length > 0 ||
    resultChannelFilter.trim() !== '' ||
    resultModelFilter.trim() !== '' ||
    resultModeFilter !== 'all' ||
    resultBatchFilter !== 'all' ||
    resultTimeFilter !== 'all'
  const targets = targetsQuery.data?.items ?? EMPTY_DETECTION_TARGETS
  const resultChannelOptions = useMemo(() => {
    const channels = new Map<string, number>()
    for (const target of targets) {
      const name = target.channel_name.trim()
      if (name !== '' && !channels.has(name)) {
        channels.set(name, target.channel_id)
      }
    }
    return [...channels.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, channelId]) => ({
        value: name,
        label: `${name} · #${channelId}`,
      }))
  }, [targets])
  const resultModelOptions = useMemo(() => {
    const models = new Set<string>()
    for (const target of targets) {
      for (const model of target.models ?? []) {
        const name = model.trim()
        if (name !== '') models.add(name)
      }
    }
    return [...models]
      .sort((left, right) => left.localeCompare(right))
      .map((model) => ({ value: model, label: model }))
  }, [targets])
  const targetChannelCount = targets.length
  const targetModelCount = targetsQuery.data?.model_count ?? 0
  const skippedTargetCount = targetsQuery.data?.skipped_channel_count ?? 0
  const activeResultCount = results.filter((result) =>
    isActiveResult(result)
  ).length
  let resultEmptyMessage = t('No detection results yet.')
  if (resultBatchFilter === 'latest') {
    resultEmptyMessage = t(
      'No results match the latest batch and current filters.'
    )
  } else if (hasActiveResultFilters) {
    resultEmptyMessage = t(
      'No results match these filters. Clear filters or broaden the criteria.'
    )
  }
  let batchDisabledReason: string | null = null
  if (settingsDirty) {
    batchDisabledReason = t('Save detection settings before starting.')
  } else if (!draftOptions.enabled || draftOptions.base_url.trim() === '') {
    batchDisabledReason = t(
      'Complete and enable detection settings before starting.'
    )
  } else if (targetsQuery.isLoading) {
    batchDisabledReason = t('Loading detection coverage...')
  } else if (targetsQuery.isError) {
    batchDisabledReason = t(
      'Detection coverage is unavailable. Refresh before starting detection.'
    )
  }
  const selectedBatchModelCount = targets
    .filter((target) => pendingBatchChannelIds.includes(target.channel_id))
    .reduce((sum, target) => sum + target.model_count, 0)
  const runConfirmDescription = t(
    'This will test {{channels}} channels and {{models}} models.',
    {
      channels: pendingBatchChannelIds.length,
      models: selectedBatchModelCount,
    }
  )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Authenticity Detection')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {canEditVeridrop ? (
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => {
                if (optionsQuery.isError) void optionsQuery.refetch()
                setDraftOptions({ ...savedOptions, admin_api_key: '' })
                setSettingsOpen(true)
              }}
              disabled={optionsQuery.isLoading}
            >
              <Settings2 data-icon='inline-start' className='size-4' />
              {t('Detection Settings')}
            </Button>
          ) : null}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Tabs
            value={visibleActiveTab}
            onValueChange={(value) =>
              setActiveTab((value ?? 'results') as DetectionWorkspaceTab)
            }
            className='flex flex-col gap-3'
          >
            <TabsList className='w-full justify-start sm:w-fit'>
              <TabsTrigger value='results' className='px-4'>
                {t('Detection Results')}
              </TabsTrigger>
              {canEditVeridrop ? (
                <>
                  <TabsTrigger value='batch' className='px-4'>
                    {t('Batch Detection')}
                  </TabsTrigger>
                  <TabsTrigger value='manual' className='px-4'>
                    {t('Manual Test')}
                  </TabsTrigger>
                </>
              ) : null}
            </TabsList>
            <TabsContent value='batch' className='mt-0'>
              {optionsQuery.isError ? (
                <ErrorState
                  title={t('We could not load detection settings.')}
                  description={t(
                    'Refresh the page or check your administrator permissions.'
                  )}
                  onRetry={() => {
                    void optionsQuery.refetch()
                  }}
                  className='min-h-[240px]'
                />
              ) : null}
              {!optionsQuery.isError && optionsQuery.isLoading ? (
                <div className='flex flex-col gap-4'>
                  <Skeleton className='h-16 rounded-lg' />
                  <Skeleton className='h-[320px] rounded-lg' />
                </div>
              ) : null}
              {!optionsQuery.isError && !optionsQuery.isLoading ? (
                <section className='rounded-lg border px-3'>
                  <DetectionRunToolbar
                    activeTask={activeTask}
                    totalChannelCount={targetChannelCount}
                    targetModelCount={targetModelCount}
                    skippedChannelCount={skippedTargetCount}
                    onRefresh={() => {
                      void tasksQuery.refetch()
                      void resultsQuery.refetch()
                      void targetsQuery.refetch()
                    }}
                    isRefreshing={
                      (tasksQuery.isFetching ||
                        resultsQuery.isFetching ||
                        targetsQuery.isFetching) &&
                      !tasksQuery.isLoading &&
                      !resultsQuery.isLoading &&
                      !targetsQuery.isLoading
                    }
                  />
                  <DetectionTargetsContent
                    targets={targets}
                    loading={targetsQuery.isLoading}
                    isError={targetsQuery.isError}
                    onRetry={() => {
                      void targetsQuery.refetch()
                    }}
                    disabledReason={batchDisabledReason}
                    onDetectBatch={(channelIds) => {
                      setPendingBatchChannelIds(channelIds)
                      setRunConfirmOpen(true)
                    }}
                    onDetectOne={(channelId) =>
                      singleRunMutation.mutate(channelId)
                    }
                    pendingBatch={runMutation.isPending}
                    pendingChannelId={pendingChannelId}
                  />
                </section>
              ) : null}
            </TabsContent>
            <TabsContent value='manual' className='mt-0'>
              {optionsQuery.isError ? (
                <ErrorState
                  title={t('We could not load detection settings.')}
                  description={t(
                    'Refresh the page or check your administrator permissions.'
                  )}
                  onRetry={() => {
                    void optionsQuery.refetch()
                  }}
                  className='min-h-[240px]'
                />
              ) : null}
              {!optionsQuery.isError && optionsQuery.isLoading ? (
                <Skeleton className='h-[420px] rounded-lg' />
              ) : null}
              {!optionsQuery.isError && !optionsQuery.isLoading ? (
                <ManualDetectionCard
                  form={manualForm}
                  options={draftOptions}
                  settingsDirty={settingsDirty}
                  onChange={setManualForm}
                  onSubmit={() => manualMutation.mutate()}
                  isSubmitting={manualMutation.isPending}
                />
              ) : null}
            </TabsContent>
            <TabsContent value='results' className='mt-0'>
              <section
                aria-busy={
                  resultsQuery.isFetching ||
                  debouncedResultChannelFilter !== resultChannelFilter ||
                  debouncedResultModelFilter !== resultModelFilter
                }
              >
                {resultsQuery.isError ? (
                  <ErrorState
                    title={t('We could not load detection results.')}
                    description={t('Refresh the page or try again later.')}
                    onRetry={() => {
                      void resultsQuery.refetch()
                    }}
                    className='min-h-[240px]'
                  />
                ) : (
                  <div>
                    <div className='rounded-lg border p-3'>
                      <div className='grid gap-2 md:grid-cols-2 md:items-end xl:grid-cols-[minmax(160px,1fr)_minmax(160px,1fr)_140px_150px_130px_140px]'>
                        <CompactField label={t('Channel')}>
                          <Combobox
                            options={resultChannelOptions}
                            value={resultChannelFilter}
                            placeholder={t('Filter by channel name...')}
                            emptyText='No channels found.'
                            allowCustomValue
                            openOnFocus
                            onValueChange={(channel) =>
                              setResultChannelFilter(channel ?? '')
                            }
                          />
                        </CompactField>
                        <CompactField label={t('Model')}>
                          <Combobox
                            options={resultModelOptions}
                            value={resultModelFilter}
                            placeholder={t('Filter by model name...')}
                            emptyText='No models found.'
                            allowCustomValue
                            openOnFocus
                            onValueChange={(model) =>
                              setResultModelFilter(model ?? '')
                            }
                          />
                        </CompactField>
                        <CompactField label={t('Batch')}>
                          <Select
                            value={resultBatchFilter}
                            onValueChange={(batch) =>
                              setResultBatchFilter(
                                (batch ?? 'all') as ResultBatchFilter
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {getResultBatchFilterLabel(
                                  resultBatchFilter,
                                  t
                                )}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectGroup>
                                {RESULT_BATCHES.map((batch) => (
                                  <SelectItem key={batch} value={batch}>
                                    {getResultBatchFilterLabel(batch, t)}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </CompactField>
                        <CompactField label={t('Result')}>
                          <Select
                            multiple
                            value={resultOutcomeFilters}
                            onValueChange={(outcomes) =>
                              setResultOutcomeFilters(
                                outcomes as ResultOutcomeFilter[]
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {resultOutcomeTriggerLabel}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectGroup>
                                {RESULT_OUTCOMES.map((outcome) => (
                                  <SelectItem
                                    key={outcome}
                                    value={outcome}
                                    className='data-selected:border-primary/20 data-selected:bg-primary/5 data-selected:ring-primary/20 focus:bg-muted/60 border border-transparent data-selected:ring-1 data-selected:ring-inset'
                                  >
                                    {getResultOutcomeFilterLabel(outcome, t)}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </CompactField>
                        <CompactField label={t('Mode')}>
                          <Select
                            value={resultModeFilter}
                            onValueChange={(mode) =>
                              setResultModeFilter(
                                (mode ?? 'all') as ResultModeFilter
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {getResultModeFilterLabel(resultModeFilter, t)}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectGroup>
                                {RESULT_MODES.map((mode) => (
                                  <SelectItem key={mode} value={mode}>
                                    {getResultModeFilterLabel(mode, t)}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </CompactField>
                        <CompactField label={t('Updated')}>
                          <Select
                            value={resultTimeFilter}
                            onValueChange={(timeFilter) =>
                              setResultTimeFilter(
                                (timeFilter ?? 'all') as ResultTimeFilter
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {getResultTimeLabel(resultTimeFilter, t)}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectGroup>
                                <SelectItem value='all'>
                                  {t('Any time')}
                                </SelectItem>
                                <SelectItem value='24h'>
                                  {t('Last 24 hours')}
                                </SelectItem>
                                <SelectItem value='7d'>
                                  {t('Last 7 days')}
                                </SelectItem>
                                <SelectItem value='30d'>
                                  {t('Last 30 days')}
                                </SelectItem>
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </CompactField>
                      </div>
                      <DetectionResultStatistics
                        summary={resultSummary}
                        isLoading={resultsQuery.isLoading}
                        selectedOutcomes={resultOutcomeFilters}
                        onSelectedOutcomesChange={setResultOutcomeFilters}
                      />
                      <div className='mt-2 flex flex-wrap items-center justify-end gap-2'>
                        <span className='text-muted-foreground text-xs whitespace-nowrap'>
                          {t(
                            '{{total}} matching results · showing {{visible}}',
                            {
                              total: matchingResultCount,
                              visible: results.length,
                            }
                          )}
                        </span>
                        {activeResultCount > 0 && (
                          <LiveRefreshIndicator active />
                        )}
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          disabled={!hasActiveResultFilters}
                          onClick={() => {
                            setResultChannelFilter('')
                            setResultModelFilter('')
                            setResultOutcomeFilters([])
                            setResultModeFilter('all')
                            setResultBatchFilter('all')
                            setResultTimeFilter('all')
                          }}
                        >
                          {t('Reset Filters')}
                        </Button>
                        {canEditVeridrop && (
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            disabled={
                              cleanupMutation.isPending ||
                              activeCleanupTask != null ||
                              activeTask != null
                            }
                            onClick={() => setCleanupOpen(true)}
                          >
                            <Trash2
                              data-icon='inline-start'
                              className='size-4'
                            />
                            {activeCleanupTask != null
                              ? t('Cleaning records...')
                              : t('Clean Records')}
                          </Button>
                        )}
                        <Button
                          type='button'
                          size='icon-sm'
                          variant='outline'
                          aria-label={t('Refresh')}
                          title={t('Refresh')}
                          disabled={resultsQuery.isFetching}
                          onClick={() => {
                            void resultsQuery.refetch()
                          }}
                        >
                          <RefreshCw
                            className={cn(
                              'size-4',
                              resultsQuery.isFetching && 'animate-spin'
                            )}
                          />
                        </Button>
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          disabled={results.length === 0}
                          onClick={() => {
                            downloadTextFile(
                              `veridrop-detailed-report-${Date.now()}.html`,
                              buildDetailedDetectionReportHtml(
                                results,
                                t,
                                formatTimestampToDate,
                                formatTimestampToDate(
                                  Math.floor(Date.now() / 1000)
                                ),
                                [
                                  {
                                    label: t('Channel'),
                                    value:
                                      debouncedResultChannelFilter ||
                                      t('All channels'),
                                  },
                                  {
                                    label: t('Model'),
                                    value:
                                      debouncedResultModelFilter ||
                                      t('All models'),
                                  },
                                  {
                                    label: t('Batch'),
                                    value: getResultBatchFilterLabel(
                                      resultBatchFilter,
                                      t
                                    ),
                                  },
                                  {
                                    label: t('Result'),
                                    value: resultOutcomeReportLabel,
                                  },
                                  {
                                    label: t('Mode'),
                                    value: getResultModeFilterLabel(
                                      resultModeFilter,
                                      t
                                    ),
                                  },
                                  {
                                    label: t('Updated'),
                                    value: getResultTimeLabel(
                                      resultTimeFilter,
                                      t
                                    ),
                                  },
                                ],
                                i18n.language
                              ),
                              'text/html;charset=utf-8'
                            )
                          }}
                        >
                          <Download
                            data-icon='inline-start'
                            className='size-4'
                          />
                          {t('Download Detailed Report')}
                        </Button>
                      </div>
                    </div>
                    <div className='pt-2'>
                      <DetectionResultsTable
                        results={results}
                        loading={resultsQuery.isLoading}
                        sort={resultSort}
                        onSort={(sortBy) => {
                          setResultSort((current) => {
                            if (current.by === sortBy) {
                              return {
                                by: sortBy,
                                order: current.order === 'asc' ? 'desc' : 'asc',
                              }
                            }
                            return {
                              by: sortBy,
                              order: sortBy === 'updated_at' ? 'desc' : 'asc',
                            }
                          })
                        }}
                        emptyMessage={resultEmptyMessage}
                      />
                    </div>
                  </div>
                )}
              </section>
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {canEditVeridrop ? (
        <>
          <DetectionSettingsDialog
            open={settingsOpen}
            onOpenChange={handleSettingsOpenChange}
            options={draftOptions}
            savedOptions={savedOptions}
            onChange={setDraftOptions}
            onSave={() => saveMutation.mutate()}
            isSaving={saveMutation.isPending}
          />
          <ConfirmDialog
            open={runConfirmOpen}
            onOpenChange={setRunConfirmOpen}
            title={t('Start detection?')}
            desc={runConfirmDescription}
            confirmText={runMutation.isPending ? t('Starting...') : t('Start')}
            isLoading={runMutation.isPending}
            handleConfirm={() => {
              runMutation.mutate(pendingBatchChannelIds)
              setRunConfirmOpen(false)
            }}
          />
          <ConfirmDialog
            open={cleanupOpen}
            onOpenChange={setCleanupOpen}
            title={t('Clean detection records?')}
            desc={t(
              'This permanently deletes completed records in the selected range. Queued and running detections are kept.'
            )}
            confirmText={
              cleanupMutation.isPending ? t('Cleaning...') : t('Clean Records')
            }
            destructive
            isLoading={cleanupMutation.isPending}
            handleConfirm={() => cleanupMutation.mutate()}
          >
            <div className='grid gap-2'>
              <Label>{t('Records to clean')}</Label>
              <Select
                value={cleanupRetentionDays}
                onValueChange={(value) =>
                  setCleanupRetentionDays(value ?? '30')
                }
              >
                <SelectTrigger className='w-full'>
                  <SelectValue>
                    {cleanupRetentionDays === '0'
                      ? t('All completed records')
                      : t('Completed records older than {{days}} days', {
                          days: cleanupRetentionDays,
                        })}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent align='start' alignItemWithTrigger={false}>
                  <SelectGroup>
                    {[7, 30, 90].map((days) => (
                      <SelectItem key={days} value={String(days)}>
                        {t('Completed records older than {{days}} days', {
                          days,
                        })}
                      </SelectItem>
                    ))}
                    <SelectItem value='0'>
                      {t('All completed records')}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          </ConfirmDialog>
        </>
      ) : null}
    </>
  )
}
