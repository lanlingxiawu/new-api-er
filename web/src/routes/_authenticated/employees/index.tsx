import { createFileRoute } from '@tanstack/react-router'

import { Employees } from '@/features/employees'
import { ADMIN_MENU_IDS, requireAdminMenu } from '@/lib/admin-menu-access'

export const Route = createFileRoute('/_authenticated/employees/')({
  beforeLoad: () => {
    requireAdminMenu(ADMIN_MENU_IDS.EMPLOYEES)
  },
  component: Employees,
})
