import { createFileRoute } from '@tanstack/react-router'

import { CommissionOverview } from '@/features/commission-overview'
import { ADMIN_MENU_IDS, requireAdminMenu } from '@/lib/admin-menu-access'

export const Route = createFileRoute('/_authenticated/commission-overview/')({
  beforeLoad: () => {
    requireAdminMenu(ADMIN_MENU_IDS.BUSINESS_OVERVIEW)
  },
  component: CommissionOverview,
})
