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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'
import { AuthOperationError } from '@/lib/secure-verification'
import { createServerError } from '@/lib/server-error-message'

import { clearUserAPIKeys } from '../api'
import type { User } from '../types'
import { useUsers } from './users-provider'

export function UsersClearAPIKeysDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, requestVerification, verificationActive } =
    useUsers()
  const [progress, setProgress] = useState<{
    userId: number
    deletedCount: number
  } | null>(null)
  const mutation = useMutation({
    retry: false,
    mutationFn: async (target: User) => {
      const proof = await requestVerification({
        scope: 'admin.user.api_keys.delete_all',
        context: { user_id: target.id },
        title: t('Verify to clear API keys'),
        description: t(
          'Clear all existing API keys for {{username}}? Applications using these keys will need new keys. This cannot be undone.',
          { username: target.username }
        ),
      })
      if (!proof) return
      const result = await clearUserAPIKeys(target.id, proof.proof_token)
      if (!result.success || !result.data?.completed) {
        if (result.data) {
          setProgress({
            userId: target.id,
            deletedCount: result.data.deleted_count,
          })
        }
        throw createServerError(
          result,
          t('Unable to clear API keys. Please verify again and retry.')
        )
      }
      return result.data
    },
    onSuccess: (result) => {
      if (!result) return
      toast.success(
        result.deleted_count === 0
          ? t('This user has no API keys to clear.')
          : t('API keys cleared: {{count}}', {
              count: result.deleted_count,
            })
      )
      setOpen(null)
      setProgress(null)
      mutation.reset()
    },
    onError: (error) =>
      handleServerError(
        AuthOperationError.from(error),
        t('Unable to clear API keys. Please verify again and retry.')
      ),
  })
  const target = mutation.isPending ? mutation.variables : currentRow

  return (
    <ConfirmDialog
      open={open === 'clear-api-keys' && !verificationActive}
      onOpenChange={(isOpen) => {
        if (!isOpen && !mutation.isPending) {
          setOpen(null)
          setProgress(null)
          mutation.reset()
        }
      }}
      title={t('Clear API keys')}
      desc={t(
        'Clear all existing API keys for {{username}}? Applications using these keys will need new keys. This cannot be undone.',
        { username: target?.username }
      )}
      confirmText={mutation.isPending ? t('Clearing...') : t('Clear API keys')}
      destructive
      isLoading={mutation.isPending}
      handleConfirm={() => {
        if (currentRow && !mutation.isPending) {
          setProgress(null)
          mutation.mutate(currentRow)
        }
      }}
    >
      {progress && progress.userId === currentRow?.id && (
        <p role='status' className='text-destructive text-sm'>
          {t(
            'Clearing is incomplete ({{count}} keys deleted). Verify again and retry to ensure all keys are revoked.',
            { count: progress.deletedCount }
          )}
        </p>
      )}
    </ConfirmDialog>
  )
}
