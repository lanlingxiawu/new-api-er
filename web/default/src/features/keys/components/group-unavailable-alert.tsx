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
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getGroupStatuses } from '@/features/monitoring/api'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { getApiKeys } from '../api'

/**
 * Shows a warning banner for each API-key group that is currently unavailable
 * (status = 4). Only appears when the signed-in user actually has keys in that
 * group, so it stays silent for unaffected users.
 */
export function GroupUnavailableAlert() {
  const { t } = useTranslation()

  const statusQuery = useQuery({
    queryKey: ['group-statuses'],
    queryFn: getGroupStatuses,
    staleTime: 3 * 60 * 1000,
    retry: false,
  })

  const keysQuery = useQuery({
    queryKey: ['api-keys-all-for-alert'],
    queryFn: () => getApiKeys({ p: 1, size: 500 }),
    staleTime: 60 * 1000,
    retry: false,
  })

  // Groups the user actually has keys in
  const usedGroups = new Set<string>()
  for (const key of keysQuery.data?.data?.items ?? []) {
    if (key.group && key.group !== 'auto') usedGroups.add(key.group)
  }

  // Groups that are currently unavailable AND used by this user
  const affected = (statusQuery.data?.data ?? [])
    .filter((g) => g.status === 4 && usedGroups.has(g.user_group))
    .map((g) => g.user_group)

  if (affected.length === 0) return null

  return (
    <div className='mb-4 space-y-2'>
      {affected.map((group) => (
        <Alert key={group} variant='destructive'>
          <AlertTriangle className='size-4' />
          <AlertTitle>
            {t('Group {{group}} is currently unavailable', { group })}
          </AlertTitle>
          <AlertDescription>
            {t(
              'This group is experiencing issues. Please edit your API key and switch to a different group to restore service.'
            )}
          </AlertDescription>
        </Alert>
      ))}
    </div>
  )
}
