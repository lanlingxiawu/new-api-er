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
import i18next from 'i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { updateSystemOption } from '../api'
import { useSettingsPageAccess } from '../components/settings-page-access-context'
import type { UpdateOptionRequest } from '../types'

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'theme.frontend',
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'LogConsumeEnabled',
  'QuotaPerUnit',
  'USDExchangeRate',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
  'oidc.display_name',
])

export function useUpdateOption(explicitScope?: string) {
  const queryClient = useQueryClient()
  const { scope: contextScope } = useSettingsPageAccess()
  const scope = explicitScope || contextScope

  return useMutation({
    mutationFn: (request: UpdateOptionRequest) =>
      updateSystemOption(scope ? { ...request, scope } : request),
    onSuccess: (data, variables) => {
      // 业务失败已由 http-client 的全局拦截器提示（updateSystemOption 没有设 skipBusinessError），
      // 这里再提示一次就会叠出两个弱提示。
      if (!data.success) return

      // Always refresh system-options
      queryClient.invalidateQueries({
        queryKey: scope ? ['system-options', scope] : ['system-options'],
      })

      // If updating frontend-display-related config, also refresh status
      if (STATUS_RELATED_KEYS.has(variables.key)) {
        queryClient.invalidateQueries({ queryKey: ['status'] })
        try {
          window.localStorage.removeItem('status')
        } catch {
          /* empty */
        }
      }

      // 逐键保存时每个键都会走到这里，相同文案由 toast 去重合并为一个（见 lib/toast-dedupe）。
      toast.success(i18next.t('Setting updated successfully'))
    },
    onError: (error: Error) => handleServerError(error),
  })
}
