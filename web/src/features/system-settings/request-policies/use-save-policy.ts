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

import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { updateSystemOptionGroup } from '../api'
import { useSettingsPageAccess } from '../components/settings-page-access-context'
import { savePolicyConfig } from './api'

export function useSavePolicy(
  onPartSaved?: (options: Record<string, string>) => void
) {
  const { scope } = useSettingsPageAccess()
  const client = useQueryClient()
  const { t } = useTranslation()
  return useMutation({
    mutationFn: async (options: Record<string, string>) => {
      const policy = { ...options }
      const groups = new Map<
        | 'probe_routing_setting'
        | 'channel_daily_limit_setting'
        | 'stream_protocol_validation_setting',
        Record<string, string>
      >()
      for (const [key, value] of Object.entries(options)) {
        const [module, field] = key.split('.')
        if (
          module !== 'probe_routing_setting' &&
          module !== 'channel_daily_limit_setting' &&
          module !== 'stream_protocol_validation_setting'
        ) {
          continue
        }
        const values = groups.get(module) ?? {}
        values[field] = value
        groups.set(module, values)
        delete policy[key]
      }
      let savedOptions: Record<string, string> = {}
      if (Object.keys(policy).length > 0) {
        const data = await savePolicyConfig(policy, scope)
        savedOptions = { ...data.options }
        onPartSaved?.(policy)
        if (scope === 'request-policies.routing') {
          client.setQueryData(['request-policy'], data)
        }
      }
      for (const [module, values] of groups) {
        requireServerSuccess(
          await updateSystemOptionGroup({ scope, module, values })
        )
        const saved = Object.fromEntries(
          Object.entries(values).map(([key, value]) => [
            `${module}.${key}`,
            value,
          ])
        )
        Object.assign(savedOptions, saved)
        onPartSaved?.(saved)
      }
      return { options: savedOptions }
    },
    onSuccess: () => {
      toast.success(t('Saved successfully'))
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['request-policy'] })
      void client.invalidateQueries({ queryKey: ['system-options'] })
      void client.invalidateQueries({ queryKey: ['channel-ops'] })
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })
}
