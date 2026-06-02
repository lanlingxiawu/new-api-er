import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { CustomerConsole } from '@/features/customer-console'

export const Route = createFileRoute('/_authenticated/customer-console/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user?.is_employee) {
      throw redirect({ to: '/403' })
    }
  },
  component: CustomerConsole,
})
