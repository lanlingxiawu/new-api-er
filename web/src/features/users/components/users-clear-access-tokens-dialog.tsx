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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { AuthOperationError } from '@/lib/secure-verification'
import { createServerError } from '@/lib/server-error-message'

import { clearUserAccessTokens } from '../api'
import type { User } from '../types'
import { useUsers } from './users-provider'

export function UsersClearAccessTokensDialog() {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const { open, setOpen, currentRow, requestVerification, verificationActive } =
    useUsers()
  const mutation = useMutation({
    retry: false,
    mutationFn: async (target: User) => {
      const proof = await requestVerification({
        scope: 'admin.user.access_tokens.delete_all',
        context: { user_id: target.id },
        title: t('Verify to clear access tokens'),
        description: t(
          'Clear all access tokens for {{username}}, including legacy tokens? Scripts using them will need new tokens. This cannot be undone.',
          { username: target.username }
        ),
      })
      if (!proof) return
      const result = await clearUserAccessTokens(target.id, proof.proof_token)
      if (!result.success || !result.data) {
        throw createServerError(
          result,
          t(
            'Unable to confirm that access tokens were cleared. Please verify again and retry.'
          )
        )
      }
      return result.data
    },
    onSuccess: (result, target) => {
      if (!result) return
      void client.invalidateQueries({
        queryKey: ['security', 'access-tokens', 'list', target.id],
      })
      toast.success(
        result.revoked_count === 0
          ? t('This user has no access tokens to clear.')
          : t('Access tokens cleared: {{count}}', {
              count: formatNumber(
                result.revoked_count,
                toIntlLocale(i18n.resolvedLanguage || i18n.language)
              ),
            })
      )
      setOpen(null)
      mutation.reset()
    },
    onError: (error) =>
      handleServerError(
        AuthOperationError.from(error),
        t(
          'Unable to confirm that access tokens were cleared. Please verify again and retry.'
        )
      ),
  })
  const target = mutation.isPending ? mutation.variables : currentRow

  return (
    <ConfirmDialog
      open={open === 'clear-access-tokens' && !verificationActive}
      onOpenChange={(isOpen) => {
        if (!isOpen && !mutation.isPending) {
          setOpen(null)
          mutation.reset()
        }
      }}
      title={t('Clear access tokens')}
      desc={t(
        'Clear all access tokens for {{username}}, including legacy tokens? Scripts using them will need new tokens. This cannot be undone.',
        { username: target?.username }
      )}
      confirmText={
        mutation.isPending ? t('Clearing...') : t('Clear access tokens')
      }
      destructive
      isLoading={mutation.isPending}
      handleConfirm={() => {
        if (currentRow && !mutation.isPending) mutation.mutate(currentRow)
      }}
    />
  )
}
