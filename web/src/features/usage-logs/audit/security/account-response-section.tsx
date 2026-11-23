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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { hasPermission } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorSources,
  getServerErrorStatus,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  DetailRow,
  DetailSection,
} from '../../components/dialogs/log-detail-layout'
import { auditFieldLabel } from '../lib/audit-field-labels'
import { AccountResponses } from './account-response-status'
import {
  banSecurityAccount,
  stopSecurityResponse,
  type SecurityDetail,
} from './api'
import { securityRules } from './lib/security-rules'

export function AccountResponseSection({
  data,
  stale,
  onRefresh,
}: {
  data: SecurityDetail
  stale: boolean
  onRefresh: () => Promise<void>
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const [confirm, setConfirm] = useState(false)
  const [responseAction, setResponseAction] = useState<'ban' | 'stop'>('ban')
  const [uncertain, setUncertain] = useState(false)
  const [submittedVersion, setSubmittedVersion] = useState(0)
  const requestID = useRef('')
  const requestSubmitted = useRef(false)
  const verification = useSecureVerification()
  const { alert } = data
  const response = alert.response
  const finding = data.findings?.[0]
  let triggerFields: string[] = []
  if (finding?.rule === 'multi_account_key_access') {
    triggerFields = ['accounts', 'key_accounts']
  } else if (finding?.rule === 'multi_source_key_access') {
    triggerFields = ['login_ips', 'key_ips']
  }
  const triggerCounts = triggerFields.filter(
    (key) => typeof finding?.dimensions?.[key] === 'number'
  )
  const canBan =
    !!user &&
    hasPermission(user, 'audit_security', 'manage') &&
    hasPermission(user, 'admin_menu.users', 'view') &&
    alert.actor_id > 0 &&
    alert.actor_id !== user.id &&
    (alert.actor_role ?? 0) < user.role &&
    (alert.actor_role ?? 0) < 100
  const canStop =
    user?.role === 100 && alert.can_stop === true && !!alert.response_group_id
  const pending =
    response &&
    [
      'pending',
      'committed',
      'sync_pending',
      'partial',
      'unknown',
      'succeeded',
    ].includes(response.outcome)
  const mutation = useMutation({
    mutationFn: async () => {
      requestSubmitted.current = false
      setConfirm(false)
      const responseGroupID = alert.response_group_id ?? 0
      if (responseAction === 'stop' && responseGroupID <= 0) {
        throw new Error(t('The result is not confirmed. Refresh first.'))
      }
      const proof = await verification.requestVerification(
        responseAction === 'stop'
          ? {
              scope: 'audit.security.response.stop',
              context: {
                alert_id: alert.id,
                version: alert.version,
                response_group_id: responseGroupID,
                action: 'stop_automatic_response',
              },
            }
          : {
              scope: 'admin.user.manage',
              context: { user_id: alert.actor_id, action: 'disable' },
            }
      )
      if (!proof) return undefined
      setSubmittedVersion(alert.version)
      requestSubmitted.current = true
      if (responseAction === 'stop') {
        return stopSecurityResponse(
          alert.id,
          alert.version,
          responseGroupID,
          requestID.current,
          proof.proof_token
        )
      }
      return banSecurityAccount(
        alert.id,
        alert.version,
        requestID.current,
        proof.proof_token
      )
    },
    retry: false,
    onSuccess: async (result) => {
      if (!result) return
      if (result.outcome === 'succeeded') {
        toast.success(
          t(
            responseAction === 'stop'
              ? 'Further automatic actions stopped'
              : 'Account banned.'
          )
        )
      }
      await queryClient.invalidateQueries({ queryKey: ['audit-security'] })
      await onRefresh()
    },
    onError: async (error) => {
      const status = getServerErrorStatus(error)
      const rejected =
        !requestSubmitted.current ||
        status === 401 ||
        status === 403 ||
        getServerErrorSources(error).some(
          (source) =>
            source.code === 'AUDIT_SECURITY_RESPONSE_REJECTED' ||
            (typeof source.code === 'string' &&
              source.code.startsWith('SECURITY_PROOF_'))
        )
      if (rejected) {
        setSubmittedVersion(0)
        setUncertain(false)
        handleServerError(
          error,
          t(responseAction === 'stop' ? 'Save failed' : 'Ban failed')
        )
      } else {
        setUncertain(true)
        handleServerError(error, undefined, {
          title: t('The result is not confirmed. Refresh first.'),
        })
      }
      await onRefresh()
    },
  })
  return (
    <DetailSection
      size='sm'
      label={t('Response result')}
      contentClassName='space-y-3'
    >
      <div className='grid gap-x-6 gap-y-3 md:grid-cols-2'>
        <DetailRow
          size='sm'
          label={t('Matched rule')}
          value={
            <span className='break-normal wrap-break-word'>
              {t(securityRules[alert.rule] ?? 'Security alert')}
            </span>
          }
        />
        <DetailRow
          size='sm'
          label={<span className='wrap-anywhere'>{t('Trigger details')}</span>}
          value={
            triggerCounts.length ? (
              <span className='block space-y-1 break-normal wrap-break-word'>
                {triggerCounts.map((key) => (
                  <span key={key} className='block'>
                    {t('{{label}}: {{value}}', {
                      label: auditFieldLabel(key, t, finding?.rule),
                      value: finding?.dimensions?.[key],
                    })}
                  </span>
                ))}
              </span>
            ) : (
              t('{{label}}: {{value}}', {
                label: t('Matching count'),
                value: finding?.matched_count ?? alert.matched_count,
              })
            )
          }
        />
        <DetailRow
          size='sm'
          label={t('Target account')}
          value={alert.actor_id || t('Unidentified account')}
        />
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('Review conclusions do not stop automatic actions.')}
      </p>
      <AccountResponses
        response={response}
        responses={alert.responses}
        showReason
      />
      {uncertain && !response && (
        <p role='alert' className='text-muted-foreground text-sm'>
          {t('The result is not confirmed. Refresh first.')}
        </p>
      )}
      {(canBan || canStop) && (
        <div className='flex flex-wrap justify-end gap-2'>
          {canStop && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={
                mutation.isPending ||
                stale ||
                submittedVersion === alert.version ||
                uncertain
              }
              onClick={() => {
                requestID.current = crypto.randomUUID()
                setResponseAction('stop')
                setConfirm(true)
              }}
            >
              {t('Stop this response')}
            </Button>
          )}
          {canBan && (
            <Button
              type='button'
              variant='destructive'
              size='sm'
              disabled={
                mutation.isPending ||
                stale ||
                !!pending ||
                submittedVersion === alert.version ||
                uncertain
              }
              onClick={() => {
                requestID.current = crypto.randomUUID()
                setResponseAction('ban')
                setConfirm(true)
              }}
            >
              {mutation.isPending && responseAction === 'ban'
                ? t('Banning...')
                : t('Ban account')}
            </Button>
          )}
        </div>
      )}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        destructive={responseAction === 'ban'}
        title={t(
          responseAction === 'stop' ? 'Stop this response' : 'Ban this account?'
        )}
        desc={
          responseAction === 'stop'
            ? t(
                'Stop further actions. Work already started will finish, and completed bans or cleanup will not be undone.'
              )
            : t(
                'Banning "{{account}}" (ID: {{id}}) prevents sign-in and new API requests. You can unban it in user management.',
                { account: String(alert.actor_id), id: alert.actor_id }
              )
        }
        confirmText={t(
          responseAction === 'stop' ? 'Stop this response' : 'Confirm ban'
        )}
        handleConfirm={() => mutation.mutate()}
        isLoading={mutation.isPending}
      />
      <SecureVerificationDialog {...verification.dialogProps} />
    </DetailSection>
  )
}
