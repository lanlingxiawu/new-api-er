import { createFileRoute, redirect } from '@tanstack/react-router'

import { EmployeeConsole } from '@/features/employee-console'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/commission/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      throw redirect({ to: '/sign-in' })
    }
    if (!auth.user.is_employee) {
      throw redirect({ to: '/403' })
    }
  },
  component: EmployeeConsole,
})
