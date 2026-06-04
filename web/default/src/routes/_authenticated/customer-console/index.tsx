import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { CustomerConsole } from '@/features/customer-console'

export const Route = createFileRoute('/_authenticated/customer-console/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      throw redirect({ to: '/sign-in' })
    }
  },
  component: CustomerConsole,
})
