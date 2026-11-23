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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { hasPermission } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorSources,
  getServerErrorStatus,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import type { SecurityAlert } from './api'
import { confirmSecurityAlert } from './lib/confirm-alert'

export function useAlertBatchConfirmation(onProcessed?: () => void) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const mounted = useRef(true)
  const running = useRef(false)
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const mutation = useMutation({
    mutationFn: async (request: {
      alerts: SecurityAlert[]
      userID: number
      isActive: () => boolean
    }) => {
      let confirmed = 0
      let processed = 0
      let firstError: unknown
      for (let index = 0; index < request.alerts.length; index += 4) {
        if (!request.isActive()) break
        const results = await Promise.allSettled(
          request.alerts.slice(index, index + 4).map(confirmSecurityAlert)
        )
        let stop = false
        for (const result of results) {
          processed++
          if (result.status === 'fulfilled') {
            confirmed++
            continue
          }
          firstError ??= result.reason
          const status = getServerErrorStatus(result.reason)
          const businessFailure = getServerErrorSources(result.reason).some(
            (source) => source.success === false
          )
          if (status === 401 || status === 403 || !businessFailure) stop = true
        }
        if (request.isActive()) {
          setProgress({ done: processed, total: request.alerts.length })
        }
        if (stop) break
      }
      return {
        confirmed,
        failed: processed - confirmed,
        remaining: request.alerts.length - processed,
        firstError,
      }
    },
    retry: false,
    onSuccess: (result, request) => {
      if (!request.isActive()) return
      if (result.confirmed > 0) onProcessed?.()
      if (result.failed || result.remaining) {
        handleServerError(
          result.firstError,
          t('The alert may have changed. Refresh and try again.'),
          {
            title: t(
              'Confirmed {{confirmed}}; failed {{failed}}; not processed {{remaining}}.',
              result
            ),
            description: t('Refresh the list before trying again.'),
          }
        )
      } else {
        toast.success(
          t('Confirmed {{count}} alerts.', { count: result.confirmed })
        )
      }
    },
    onError: (error, request) => {
      if (request.isActive()) handleServerError(error, t('Save failed'))
    },
    onSettled: async (_result, _error, request) => {
      try {
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: ['audit-security'] }),
          queryClient.invalidateQueries({ queryKey: ['audit'] }),
          queryClient.invalidateQueries({
            queryKey: ['customer-audit', request.userID],
          }),
        ])
      } finally {
        running.current = false
      }
    },
  })

  function confirm(alerts: SecurityAlert[]) {
    const auth = useAuthStore.getState().auth
    if (
      running.current ||
      !auth.user ||
      !hasPermission(auth.user, 'audit_security', 'manage')
    ) {
      return
    }
    const targets = alerts
      .filter(
        (alert) => alert.status === 'open' || alert.status === 'investigating'
      )
      .slice(0, 100)
      .map((alert) => ({ ...alert }))
    if (!targets.length) return
    const scope = JSON.stringify([
      auth.user.id,
      auth.user.role,
      auth.user.permissions,
      auth.session?.sid,
    ])
    running.current = true
    setProgress({ done: 0, total: targets.length })
    mutation.mutate({
      alerts: targets,
      userID: auth.user.id,
      isActive: () => {
        const current = useAuthStore.getState().auth
        return (
          mounted.current &&
          scope ===
            JSON.stringify([
              current.user?.id,
              current.user?.role,
              current.user?.permissions,
              current.session?.sid,
            ])
        )
      },
    })
  }
  return { confirm, isPending: mutation.isPending, progress }
}
