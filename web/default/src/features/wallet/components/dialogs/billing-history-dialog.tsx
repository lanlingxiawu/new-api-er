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
import { useState } from 'react'
import {
  Search,
  Filter,
  Copy,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  RotateCcw,
  Loader2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatNumber, formatQuota } from '@/lib/format'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
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
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/status-badge'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { useBillingHistory } from '../../hooks/use-billing-history'
import type { TopupStatus } from '../../types'
import {
  STATUS_CONFIG,
  getStatusConfig,
  getPaymentMethodName,
  formatTimestamp,
  formatPaymentAmount,
  PAYMENT_METHOD_FILTER_OPTIONS,
} from '../../lib/billing'

interface BillingHistoryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userExportEnabled?: boolean
  /**
   * Payment methods currently enabled by the admin. When provided, only these
   * are selectable in the payment-method filter. Falls back to all known
   * methods when omitted.
   */
  enabledPaymentMethods?: string[]
}

const ALL_VALUE = 'all'
// Status filter options are derived from the canonical status config so the
// dropdown always reflects the actually-designed top-up statuses.
const STATUS_OPTIONS = Object.keys(STATUS_CONFIG) as TopupStatus[]

// Convert a Unix-seconds value to a Date for the range picker (and back).
const secondsToDate = (seconds?: number) =>
  seconds ? new Date(seconds * 1000) : undefined
const dateToSeconds = (date?: Date) =>
  date ? Math.floor(date.getTime() / 1000) : undefined

export function BillingHistoryDialog({
  open,
  onOpenChange,
  userExportEnabled = true,
  enabledPaymentMethods,
}: BillingHistoryDialogProps) {
  const { t } = useTranslation()
  // Only enabled payment methods are selectable; fall back to all when the
  // caller doesn't supply the enabled set.
  const paymentMethodOptions = enabledPaymentMethods ?? [
    ...PAYMENT_METHOD_FILTER_OPTIONS,
  ]
  const {
    records,
    total,
    page,
    pageSize,
    keyword,
    filters,
    loading,
    completing,
    exporting,
    isAdmin,
    hasActiveFilters,
    hasAppliedFilters,
    hasPendingFilterChanges,
    handlePageChange,
    handlePageSizeChange,
    handleSearch,
    handleFilterChange,
    handleApplyFilters,
    handleResetFilters,
    handleExport,
    handleCompleteOrder,
  } = useBillingHistory()

  const [confirmTradeNo, setConfirmTradeNo] = useState<string | null>(null)
  const { copyToClipboard, copiedText } = useCopyToClipboard({ notify: false })

  const totalPages = Math.ceil(total / pageSize)
  const selectedStatusLabel = filters.status
    ? t(getStatusConfig(filters.status as TopupStatus).label)
    : t('All status')
  const selectedPaymentMethodLabel = filters.paymentMethod
    ? getPaymentMethodName(filters.paymentMethod, t)
    : t('All methods')

  const canShowExport = isAdmin || userExportEnabled
  const canExport = total > 0

  const handleConfirmComplete = async () => {
    if (confirmTradeNo) {
      const success = await handleCompleteOrder(confirmTradeNo)
      if (success) {
        setConfirmTradeNo(null)
      }
    }
  }

  const isRawQuotaTopup = (record: {
    payment_method?: string
    payment_provider?: string
  }) =>
    record.payment_provider === 'infini' || record.payment_method === 'infini'

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none max-sm:p-4 sm:h-[min(820px,calc(100dvh-2rem))] sm:w-[92vw] sm:max-w-5xl'>
          <DialogHeader>
            <DialogTitle>{t('Billing History')}</DialogTitle>
            <DialogDescription>
              {t('View your topup transaction records and payment history')}
            </DialogDescription>
          </DialogHeader>

          <div className='flex min-h-0 flex-1 flex-col gap-3 sm:gap-4'>
            {/* Search + page size */}
            <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
              <div className='relative flex-1'>
                <Search className='text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2' />
                <Input
                  placeholder={t('Search by order number...')}
                  value={keyword}
                  onChange={(e) => handleSearch(e.target.value)}
                  className='h-9 pl-10'
                />
              </div>
              <div className='flex items-center gap-2'>
                <Select
                  value={pageSize.toString()}
                  onValueChange={(value) =>
                    value !== null && handlePageSizeChange(parseInt(value))
                  }
                >
                  <SelectTrigger className='h-9 w-[92px] sm:w-32'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='10'>{t('10 / page')}</SelectItem>
                      <SelectItem value='20'>{t('20 / page')}</SelectItem>
                      <SelectItem value='50'>{t('50 / page')}</SelectItem>
                      <SelectItem value='100'>{t('100 / page')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            </div>

            {/* Filter bar */}
            <div className='flex flex-wrap items-center gap-2'>
              <CompactDateTimeRangePicker
                start={secondsToDate(filters.startTime)}
                end={secondsToDate(filters.endTime)}
                onChange={({ start, end }) =>
                  handleFilterChange({
                    startTime: dateToSeconds(start),
                    endTime: dateToSeconds(end),
                  })
                }
              />

              <Select
                value={filters.status || ALL_VALUE}
                onValueChange={(value) =>
                  handleFilterChange({
                    status:
                      value === ALL_VALUE
                        ? ''
                        : (value as TopupStatus),
                  })
                }
              >
                <SelectTrigger className='h-9 w-[120px]'>
                  <SelectValue>{selectedStatusLabel}</SelectValue>
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectItem value={ALL_VALUE}>{t('All status')}</SelectItem>
                  {STATUS_OPTIONS.map((status) => (
                    <SelectItem key={status} value={status}>
                      {t(getStatusConfig(status).label)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              <Select
                value={filters.paymentMethod || ALL_VALUE}
                onValueChange={(value) =>
                  handleFilterChange({
                    paymentMethod: value && value !== ALL_VALUE ? value : '',
                  })
                }
              >
                <SelectTrigger className='h-9 w-[140px]'>
                  <SelectValue>{selectedPaymentMethodLabel}</SelectValue>
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectItem value={ALL_VALUE}>{t('All methods')}</SelectItem>
                  {paymentMethodOptions.map((method) => (
                    <SelectItem key={method} value={method}>
                      {getPaymentMethodName(method, t)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              {isAdmin && (
                <Input
                  type='number'
                  min={1}
                  inputMode='numeric'
                  placeholder={t('User ID')}
                  value={filters.userId ? String(filters.userId) : ''}
                  onChange={(e) => {
                    const v = parseInt(e.target.value, 10)
                    handleFilterChange({
                      userId: Number.isNaN(v) || v <= 0 ? undefined : v,
                    })
                  }}
                  className='h-9 w-[110px]'
                />
              )}

              <Button
                variant='outline'
                size='sm'
                className='h-9'
                onClick={handleApplyFilters}
                disabled={!hasPendingFilterChanges}
              >
                <Filter className='mr-1.5 h-3.5 w-3.5' />
                {t('Filter')}
              </Button>

              <Button
                variant='ghost'
                size='sm'
                className='h-9'
                onClick={handleResetFilters}
                disabled={!hasActiveFilters && !hasAppliedFilters}
              >
                <RotateCcw className='mr-1.5 h-3.5 w-3.5' />
                {t('Reset')}
              </Button>

              {canShowExport && (
                <Button
                  variant='outline'
                  size='sm'
                  className='h-9'
                  disabled={exporting || !canExport}
                  onClick={() => handleExport()}
                >
                  {exporting ? (
                    <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                  ) : (
                    <Download className='mr-2 h-4 w-4' />
                  )}
                  {t('Export')}
                </Button>
              )}
            </div>

            {/* Records List */}
            <ScrollArea className='min-h-0 flex-1 pr-3 sm:pr-4'>
              {loading ? (
                <div className='space-y-3'>
                  {Array.from({ length: 5 }).map((_, i) => (
                    <div key={i} className='rounded-lg border p-3 sm:p-4'>
                      <div className='flex items-start justify-between'>
                        <div className='flex-1 space-y-2'>
                          <Skeleton className='h-4 w-48' />
                          <Skeleton className='h-3 w-32' />
                        </div>
                        <Skeleton className='h-5 w-16' />
                      </div>
                      <div className='mt-3 grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4'>
                        <Skeleton className='h-3 w-full' />
                        <Skeleton className='h-3 w-full' />
                        <Skeleton className='h-3 w-full' />
                      </div>
                    </div>
                  ))}
                </div>
              ) : records.length === 0 ? (
                <div className='text-muted-foreground flex h-[320px] flex-col items-center justify-center text-center sm:h-[400px]'>
                  <p className='text-sm font-medium'>
                    {t('No billing records found')}
                  </p>
                  <p className='mt-1 text-xs'>
                    {hasActiveFilters || hasAppliedFilters
                      ? t('Try adjusting your search')
                      : t('Your transaction history will appear here')}
                  </p>
                </div>
              ) : (
                <div className='space-y-3'>
                  {records.map((record) => {
                    const statusConfig = getStatusConfig(record.status)
                    return (
                      <div
                        key={record.id}
                        className='hover:bg-muted/50 rounded-lg border p-3 transition-colors sm:p-4'
                      >
                        {/* Header Row */}
                        <div className='flex items-start justify-between gap-2'>
                          <div className='flex-1 space-y-1'>
                            <div className='flex min-w-0 items-center gap-2'>
                              <code className='text-foreground truncate font-mono text-sm'>
                                {record.trade_no}
                              </code>
                              <Button
                                variant='ghost'
                                size='sm'
                                className='h-5 w-5 p-0'
                                onClick={() => copyToClipboard(record.trade_no)}
                              >
                                {copiedText === record.trade_no ? (
                                  <Check className='h-3 w-3' />
                                ) : (
                                  <Copy className='h-3 w-3' />
                                )}
                              </Button>
                              {isAdmin && record.user_id != null && (
                                <StatusBadge
                                  label={`${t('User ID')}: ${record.user_id}`}
                                  variant='neutral'
                                  size='sm'
                                  copyText={String(record.user_id)}
                                />
                              )}
                            </div>
                            <div className='text-muted-foreground text-xs'>
                              {formatTimestamp(record.create_time)}
                            </div>
                          </div>
                          <StatusBadge
                            label={statusConfig.label}
                            variant={statusConfig.variant}
                            showDot
                            copyable={false}
                          />
                        </div>

                        {/* Details Grid */}
                        <div className='mt-3 grid grid-cols-2 gap-3 sm:mt-4 sm:grid-cols-3 sm:gap-4'>
                          <div className='space-y-1'>
                            <Label className='text-muted-foreground text-xs'>
                              {t('Payment Method')}
                            </Label>
                            <div className='text-sm font-medium'>
                              {getPaymentMethodName(record.payment_method, t)}
                            </div>
                          </div>
                          <div className='space-y-1'>
                            <Label className='text-muted-foreground text-xs'>
                              {t('Amount')}
                            </Label>
                            <div className='text-sm font-semibold'>
                              {isRawQuotaTopup(record)
                                ? formatQuota(record.amount)
                                : formatCurrencyFromUSD(record.amount, {
                                    digitsLarge: 2,
                                    digitsSmall: 2,
                                    abbreviate: false,
                                  })}
                            </div>
                          </div>
                          <div className='space-y-1'>
                            <Label className='text-muted-foreground text-xs'>
                              {t('Payment')}
                            </Label>
                            <div className='text-sm font-semibold text-red-600'>
                              {isRawQuotaTopup(record)
                                ? formatPaymentAmount(
                                    record.money,
                                    record.payment_currency
                                  )
                                : formatNumber(record.money)}
                            </div>
                          </div>
                        </div>

                        {/* Admin Actions */}
                        {isAdmin && record.status === 'pending' && (
                          <div className='mt-4 flex justify-end'>
                            <Button
                              size='sm'
                              variant='outline'
                              onClick={() => setConfirmTradeNo(record.trade_no)}
                              disabled={completing}
                            >
                              {t('Complete Order')}
                            </Button>
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </ScrollArea>

            {/* Pagination */}
            {!loading && records.length > 0 && (
              <div className='flex flex-col items-center gap-3 border-t pt-4 sm:flex-row sm:items-center sm:justify-between'>
                <div className='text-muted-foreground text-xs sm:text-sm'>
                  {t('Showing')} {(page - 1) * pageSize + 1}-
                  {Math.min(page * pageSize, total)} {t('of')} {total}
                </div>
                <div className='flex items-center gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => handlePageChange(page - 1)}
                    disabled={page <= 1}
                    className='h-8 w-8 p-0'
                  >
                    <ChevronLeft className='h-4 w-4' />
                  </Button>
                  <div className='text-muted-foreground flex items-center gap-1 text-sm'>
                    <span className='font-medium'>{page}</span>
                    <span>/</span>
                    <span>{totalPages}</span>
                  </div>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => handlePageChange(page + 1)}
                    disabled={page >= totalPages}
                    className='h-8 w-8 p-0'
                  >
                    <ChevronRight className='h-4 w-4' />
                  </Button>
                </div>
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      {/* Confirm Complete Order Dialog */}
      <AlertDialog
        open={!!confirmTradeNo}
        onOpenChange={(open) => !open && setConfirmTradeNo(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Complete Order')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'Are you sure you want to manually complete this order? The user will be credited with the corresponding quota.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={completing}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleConfirmComplete}
              disabled={completing}
            >
              {completing ? t('Processing...') : t('Confirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
