import { createFileRoute } from '@tanstack/react-router'

import { PriceMonitor } from '@/features/price-monitor'
import { ADMIN_MENU_IDS, requireAdminMenu } from '@/lib/admin-menu-access'

export const Route = createFileRoute('/_authenticated/price-monitor/')({
  beforeLoad: () => {
    requireAdminMenu(ADMIN_MENU_IDS.PRICE_MONITOR)
  },
  component: PriceMonitor,
})
