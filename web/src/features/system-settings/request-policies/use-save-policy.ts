import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { useSettingsPageAccess } from '../components/settings-page-access-context'
import { savePolicyConfig } from './api'

export function useSavePolicy() {
  const { scope } = useSettingsPageAccess()
  const client = useQueryClient()
  const { t } = useTranslation()
  return useMutation({
    mutationFn: (options: Record<string, string>) =>
      savePolicyConfig(options, scope),
    onSuccess: (data) => {
      if (scope === 'request-policies.routing') {
        client.setQueryData(['request-policy'], data)
      } else {
        void client.invalidateQueries({ queryKey: ['request-policy'] })
      }
      void client.invalidateQueries({ queryKey: ['system-options'] })
      void client.invalidateQueries({ queryKey: ['channel-ops'] })
      toast.success(t('Saved successfully'))
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })
}
