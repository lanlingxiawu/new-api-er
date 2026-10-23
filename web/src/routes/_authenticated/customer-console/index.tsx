import { createFileRoute, redirect } from '@tanstack/react-router'

import { CustomerConsole } from '@/features/customer-console'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/customer-console/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      throw redirect({ to: '/sign-in' })
    }
    if (!auth.user.is_employee) {
      throw redirect({ to: '/403' })
    }
  },
  component: CustomerConsole,
})
