import { SettingsSaveConfirmationProvider } from '@/features/system-settings/components/settings-save-confirmation'
import { PriceMonitorPanel } from '@/features/system-settings/models/price-monitor-panel'
import { ADMIN_MENU_IDS } from '@/lib/admin-menu-access'
import {
  ADMIN_PERMISSION_ACTIONS,
  adminMenuResource,
  canEditSystemSettingsScope,
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

  // 改价走的是 POST /api/price_monitor/apply_price，后端要求
  // SystemSettingsEdit("billing.model-pricing")，与巡检页自身的编辑权不是一回事。
  // 用巡检页权限去开改价按钮，会让有定价权的人点不到、没定价权的人点了必然失败。
  const canRepairPricing = canEditSystemSettingsScope(
    user,
    'billing.model-pricing'
  )

  return (
    <SettingsSaveConfirmationProvider>
      <PriceMonitorPanel
        canEdit={canEdit}
        canRepairPricing={canRepairPricing}
      />
    </SettingsSaveConfirmationProvider>
  )
}
