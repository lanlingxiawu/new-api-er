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
import i18next from 'i18next'
import { useState, useEffect, useCallback } from 'react'
import { toast } from 'sonner'

import { useIsAdmin } from '@/hooks/use-admin'

import {
  getUserBillingHistory,
  getAllBillingHistory,
  exportUserBillingHistory,
  exportAllBillingHistory,
  completeOrder,
  queryTopUpPlatformStatus,
  isApiSuccess,
} from '../api'
import type { TopupRecord, BillingHistoryFilters } from '../types'

const EMPTY_FILTERS: BillingHistoryFilters = {}

function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.URL.revokeObjectURL(url)
}

// ============================================================================
// Billing History Hook
// ============================================================================

interface UseBillingHistoryOptions {
  /** Initial page number */
  initialPage?: number
  /** Initial page size */
  initialPageSize?: number
}

function normalizeFilters(
  filters: BillingHistoryFilters
): BillingHistoryFilters {
  const keyword = filters.keyword?.trim()
  const userId =
    typeof filters.userId === 'number' && filters.userId > 0
      ? filters.userId
      : undefined

  return {
    keyword: keyword || undefined,
    startTime: filters.startTime || undefined,
    endTime: filters.endTime || undefined,
    status: filters.status || undefined,
    paymentMethod: filters.paymentMethod || undefined,
    userId,
  }
}

function hasFilters(filters: BillingHistoryFilters): boolean {
  const normalized = normalizeFilters(filters)
  return Boolean(
    normalized.keyword ||
    normalized.startTime ||
    normalized.endTime ||
    normalized.status ||
    normalized.paymentMethod ||
    normalized.userId
  )
}

function isSameFilters(
  left: BillingHistoryFilters,
  right: BillingHistoryFilters
): boolean {
  return (
    JSON.stringify(normalizeFilters(left)) ===
    JSON.stringify(normalizeFilters(right))
  )
}

export function useBillingHistory(options: UseBillingHistoryOptions = {}) {
  const { initialPage = 1, initialPageSize = 10 } = options
  const isAdmin = useIsAdmin()

  const [records, setRecords] = useState<TopupRecord[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(initialPage)
  const [pageSize, setPageSize] = useState(initialPageSize)
  const [filters, setFilters] = useState<BillingHistoryFilters>(EMPTY_FILTERS)
  const [draftFilters, setDraftFilters] =
    useState<BillingHistoryFilters>(EMPTY_FILTERS)
  const [loading, setLoading] = useState(false)
  const [completing, setCompleting] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [queryingPlatformTradeNos, setQueryingPlatformTradeNos] = useState<
    Set<string>
  >(new Set())
  // Trade numbers whose most recent platform-status query failed (query_failed
  // / persist_failed). Surfaced inline so the admin can see which order to retry.
  const [failedPlatformTradeNos, setFailedPlatformTradeNos] = useState<
    Set<string>
  >(new Set())

  const keyword = draftFilters.keyword ?? ''
  const hasPendingFilterChanges = !isSameFilters(draftFilters, filters)
  const hasActiveFilters = hasFilters(draftFilters)
  const hasAppliedFilters = hasFilters(filters)

  const fetchHistoryPage = useCallback(
    async (
      currentPage: number,
      currentPageSize: number,
      currentFilters: BillingHistoryFilters
    ) =>
      isAdmin
        ? getAllBillingHistory({
            pageSize: currentPageSize,
            filters: currentFilters,
            page: currentPage,
          })
        : getUserBillingHistory({
            pageSize: currentPageSize,
            filters: currentFilters,
            page: currentPage,
          }),
    [isAdmin]
  )

  /**
   * Fetch billing history. Offset pagination: jumping to any page is a single
   * request (no sequential cursor prefetch of every preceding page).
   */
  const fetchBillingHistory = useCallback(async () => {
    setLoading(true)
    try {
      const response = await fetchHistoryPage(page, pageSize, filters)

      if (isApiSuccess(response) && response.data) {
        setRecords(response.data.items || [])
        setTotal(response.data.total || 0)
      } else {
        toast.error(
          response.message || i18next.t('Failed to load billing history')
        )
        setRecords([])
        setTotal(0)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to fetch billing history:', error)
      toast.error(i18next.t('Failed to load billing history'))
      setRecords([])
      setTotal(0)
    } finally {
      setLoading(false)
    }
  }, [fetchHistoryPage, filters, page, pageSize])

  /**
   * Export the currently applied billing-history view as CSV.
   */
  const handleExport = useCallback(async () => {
    if (exporting) return
    if (total <= 0) return
    setExporting(true)
    try {
      const result = isAdmin
        ? await exportAllBillingHistory(filters)
        : await exportUserBillingHistory(filters)

      triggerBlobDownload(result.blob, result.filename)

      if (result.truncated) {
        toast.warning(
          i18next.t(
            'Export reached the {{count}}-row limit. Some records were not exported. Narrow the time range to export the rest.',
            { count: result.maxRows ?? 0 }
          )
        )
      } else {
        toast.success(i18next.t('Export started'))
      }
    } catch (error) {
      let message = i18next.t('Failed to export billing history')
      if (error instanceof Error && error.message) {
        message =
          error.message === 'TOPUP_EXPORT_RATE_LIMITED'
            ? i18next.t(
                'Export requests are too frequent, please try again later.'
              )
            : error.message
      }
      toast.error(message)
    } finally {
      setExporting(false)
    }
  }, [exporting, filters, isAdmin, total])

  /**
   * Complete a pending order (admin only)
   */
  const handleCompleteOrder = useCallback(
    async (tradeNo: string) => {
      if (!isAdmin) {
        toast.error(i18next.t('Admin access required'))
        return false
      }

      setCompleting(true)
      try {
        const response = await completeOrder({ trade_no: tradeNo })
        if (isApiSuccess(response)) {
          toast.success(i18next.t('Order completed successfully'))
          // Refresh the list
          await fetchBillingHistory()
          return true
        } else {
          toast.error(response.message || i18next.t('Failed to complete order'))
          return false
        }
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to complete order:', error)
        toast.error(i18next.t('Failed to complete order'))
        return false
      } finally {
        setCompleting(false)
      }
    },
    [isAdmin, fetchBillingHistory]
  )

  const handleQueryPlatformStatus = useCallback(
    async (tradeNos: string[]) => {
      if (!isAdmin) {
        toast.error(i18next.t('Admin access required'))
        return
      }

      const uniqueTradeNos = [
        ...new Set(tradeNos.map((value) => value.trim())),
      ].filter(Boolean)
      if (uniqueTradeNos.length === 0) return

      setQueryingPlatformTradeNos((current) => {
        const next = new Set(current)
        uniqueTradeNos.forEach((tradeNo) => next.add(tradeNo))
        return next
      })

      try {
        const response = await queryTopUpPlatformStatus(uniqueTradeNos)
        if (!isApiSuccess(response) || !response.data) {
          toast.error(
            response.message || i18next.t('Failed to query platform status')
          )
          return
        }

        const updatedItems = new Map(
          response.data.items
            .filter((item) => item.result === 'updated')
            .map((item) => [item.trade_no, item])
        )
        if (updatedItems.size > 0) {
          setRecords((current) =>
            current.map((record) => {
              const item = updatedItems.get(record.trade_no)
              if (!item) return record
              return {
                ...record,
                platform_payment_status: item.platform_payment_status,
                platform_payment_status_raw: item.platform_payment_status_raw,
                platform_payment_status_checked_at:
                  item.platform_payment_status_checked_at,
              }
            })
          )
        }

        // Track which orders failed this round so the row can show a retry hint;
        // clear the flag for any order that succeeded this time.
        const failedTradeNos = new Set(
          response.data.items
            .filter(
              (item) =>
                item.result === 'query_failed' ||
                item.result === 'persist_failed'
            )
            .map((item) => item.trade_no)
        )
        setFailedPlatformTradeNos((current) => {
          const next = new Set(current)
          uniqueTradeNos.forEach((tradeNo) => next.delete(tradeNo))
          failedTradeNos.forEach((tradeNo) => next.add(tradeNo))
          return next
        })

        const { updated, failed, skipped } = response.data.summary
        if (failed > 0) {
          toast.warning(
            i18next.t(
              'Updated {{success}} platform statuses; {{failed}} failed, please retry',
              { success: updated, failed }
            )
          )
        } else if (updated === 0 && skipped > 0) {
          toast.info(i18next.t('No orders eligible for platform status query'))
        } else {
          toast.success(
            i18next.t('Updated {{count}} platform statuses', {
              count: updated,
            })
          )
        }
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to query platform status:', error)
        toast.error(i18next.t('Failed to query platform status'))
      } finally {
        setQueryingPlatformTradeNos((current) => {
          const next = new Set(current)
          uniqueTradeNos.forEach((tradeNo) => next.delete(tradeNo))
          return next
        })
      }
    },
    [isAdmin]
  )

  /**
   * Change page
   */
  const handlePageChange = useCallback((newPage: number) => {
    setPage(newPage)
  }, [])

  /**
   * Change page size
   */
  const handlePageSizeChange = useCallback((newPageSize: number) => {
    setPageSize(newPageSize)
    setPage(1) // Reset to first page when changing page size
  }, [])

  /**
   * Search by keyword
   */
  const handleSearch = useCallback((newKeyword: string) => {
    setDraftFilters((prev) => ({ ...prev, keyword: newKeyword }))
  }, [])

  /**
   * Update one or more draft filter fields without fetching immediately.
   */
  const handleFilterChange = useCallback(
    (patch: Partial<BillingHistoryFilters>) => {
      setDraftFilters((prev) => ({ ...prev, ...patch }))
    },
    []
  )

  /**
   * Apply the current draft filters and fetch the first page.
   */
  const handleApplyFilters = useCallback(() => {
    const normalized = normalizeFilters(draftFilters)
    setFilters(normalized)
    setDraftFilters(normalized)
    setPage(1)
  }, [draftFilters])

  /**
   * Clear all filters.
   */
  const handleResetFilters = useCallback(() => {
    setDraftFilters(EMPTY_FILTERS)
    setFilters(EMPTY_FILTERS)
    setPage(1)
  }, [])

  // Fetch data when dependencies change
  useEffect(() => {
    fetchBillingHistory()
  }, [fetchBillingHistory])

  return {
    records,
    total,
    page,
    pageSize,
    keyword,
    filters: draftFilters,
    appliedFilters: filters,
    loading,
    completing,
    exporting,
    queryingPlatformTradeNos,
    failedPlatformTradeNos,
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
    handleQueryPlatformStatus,
    refresh: fetchBillingHistory,
  }
}
