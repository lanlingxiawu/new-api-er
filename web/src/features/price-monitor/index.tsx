import { SettingsSaveConfirmationProvider } from '@/features/system-settings/components/settings-save-confirmation'
import { PriceMonitorPanel } from '@/features/system-settings/models/price-monitor-panel'
import { ADMIN_MENU_IDS } from '@/lib/admin-menu-access'
import {
  ADMIN_PERMISSION_ACTIONS,
  adminMenuResource,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

export function PriceMonitor() {
  const user = useAuthStore((state) => state.auth.user)
  const canEdit = hasPermission(
    user,
    adminMenuResource(ADMIN_MENU_IDS.PRICE_MONITOR),
    ADMIN_PERMISSION_ACTIONS.EDIT
  )

  return (
    <SettingsSaveConfirmationProvider>
      <PriceMonitorPanel canEdit={canEdit} />
    </SettingsSaveConfirmationProvider>
  )
}
