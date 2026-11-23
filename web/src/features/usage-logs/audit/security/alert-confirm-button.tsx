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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  isCustomerAccessDenied,
  saveCustomerAlert,
  type CustomerSecurityAlert,
} from '@/features/customer-console/audit-api'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import type { SecurityAlert } from './api'
import { alertStatuses } from './lib/alert-form'
import { confirmSecurityAlert } from './lib/confirm-alert'

type AlertConfirmation =
  | {
      alert: SecurityAlert
      customerID?: undefined
      onAccessDenied?: never
    }
  | {
      alert: CustomerSecurityAlert
      customerID: number
      onAccessDenied: () => void
    }

export function AlertConfirmButton(
  props: AlertConfirmation & {
    disabled?: boolean
    size?: 'default' | 'sm'
    onConfirmed?: () => void
    onViewRecord?: () => void
  }
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const confirm = useMutation({
    mutationFn: async (target: AlertConfirmation) => {
      if (target.customerID !== undefined) {
        await saveCustomerAlert(target.customerID, target.alert.id, {
          version: target.alert.version,
          status: 'resolved',
        })
        return
      }
      return confirmSecurityAlert(target.alert)
    },
    retry: false,
    onSuccess: () => {
      toast.success(t('Risk record processed'), {
        action: props.onViewRecord
          ? { label: t('View record'), onClick: props.onViewRecord }
          : undefined,
      })
      void queryClient.invalidateQueries({ queryKey: ['audit-security'] })
      void queryClient.invalidateQueries({ queryKey: ['audit'] })
      void queryClient.invalidateQueries({
        queryKey: ['customer-audit', userID],
      })
      props.onConfirmed?.()
    },
    onError: (error, target) => {
      if (target.customerID !== undefined && isCustomerAccessDenied(error)) {
        target.onAccessDenied()
        return
      }
      handleServerError(
        error,
        t('The alert may have changed. Refresh and try again.')
      )
      void queryClient.invalidateQueries({
        queryKey:
          target.customerID === undefined
            ? ['audit-security']
            : ['customer-audit', userID],
      })
    },
  })
  // A completed request only confirms the version the user actually saw.
  const confirmed =
    props.alert.status === 'resolved' ||
    (confirm.isSuccess &&
      confirm.variables.customerID === props.customerID &&
      confirm.variables.alert.id === props.alert.id &&
      confirm.variables.alert.version === props.alert.version)

  if (confirmed) {
    return (
      <StatusBadge
        copyable={false}
        variant='neutral'
        className={cn(props.size === 'default' && 'h-8')}
      >
        {t('Confirmed')}
      </StatusBadge>
    )
  }
  if (props.alert.status !== 'open' && props.alert.status !== 'investigating') {
    return (
      <StatusBadge
        copyable={false}
        variant='neutral'
        className={cn(props.size === 'default' && 'h-8')}
      >
        {t(
          alertStatuses[props.alert.status as keyof typeof alertStatuses] ??
            'Unknown'
        )}
      </StatusBadge>
    )
  }
  return (
    <Button
      type='button'
      variant='outline'
      size={props.size ?? 'sm'}
      disabled={props.disabled || confirm.isPending}
      onClick={() => confirm.mutate(props)}
    >
      {confirm.isPending ? t('Saving...') : t('Confirm')}
    </Button>
  )
}
