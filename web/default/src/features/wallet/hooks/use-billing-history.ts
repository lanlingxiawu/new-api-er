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
import { useState, useEffect, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import { useIsAdmin } from '@/hooks/use-admin'
import {
  getUserBillingHistory,
  getAllBillingHistory,
  exportUserBillingHistory,
  exportAllBillingHistory,
  completeOrder,
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

export function useBillingHistory(options: UseBillingHistoryOptions = {}) {
  const { initialPage = 1, initialPageSize = 10 } = options
  const isAdmin = useIsAdmin()

  const [records, setRecords] = useState<TopupRecord[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(initialPage)
  const [pageSize, setPageSize] = useState(initialPageSize)
  const [filters, setFilters] = useState<BillingHistoryFilters>(EMPTY_FILTERS)
  const [loading, setLoading] = useState(false)
  const [completing, setCompleting] = useState(false)
  const [exporting, setExporting] = useState(false)

  const keyword = filters.keyword ?? ''

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
   * Export billing history as CSV.
   * @param useFilters true → export the current filtered view (incl. time range);
   *                   false → export everything (ignore filters / "export all").
   */
  const handleExport = useCallback(
    async (useFilters: boolean) => {
      if (exporting) return
      setExporting(true)
      try {
        const exportFilters = useFilters ? filters : EMPTY_FILTERS
        const result = isAdmin
          ? await exportAllBillingHistory(exportFilters)
          : await exportUserBillingHistory(exportFilters)

        triggerBlobDownload(result.blob, result.filename)

        if (result.truncated) {
          toast.warning(
            i18next.t(
              'Export reached the {{count}}-row limit. Some records were not exported — narrow the time range to export the rest.',
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
    },
    [exporting, filters, isAdmin]
  )

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
    setFilters((prev) => ({ ...prev, keyword: newKeyword }))
    setPage(1) // Reset to first page when searching
  }, [])

  /**
   * Update one or more filter fields. Resets to the first page.
   */
  const handleFilterChange = useCallback(
    (patch: Partial<BillingHistoryFilters>) => {
        setFilters((prev) => ({ ...prev, ...patch }))
      setPage(1)
    },
    []
  )

  /**
   * Clear all filters.
   */
  const handleResetFilters = useCallback(() => {
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
    filters,
    loading,
    completing,
    exporting,
    isAdmin,
    handlePageChange,
    handlePageSizeChange,
    handleSearch,
    handleFilterChange,
    handleResetFilters,
    handleExport,
    handleCompleteOrder,
    refresh: fetchBillingHistory,
  }
}
