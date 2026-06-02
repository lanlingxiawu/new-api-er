import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { EmployeeConsole } from '@/features/employee-console'

export const Route = createFileRoute('/_authenticated/commission/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      throw redirect({ to: '/sign-in' })
    }
  },
  component: EmployeeConsole,
})
