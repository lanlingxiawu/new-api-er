import { redirect } from '@tanstack/react-router'

import { useAuthStore } from '@/stores/auth-store'

import {
  canViewSettingsSection,
  firstVisibleSettingsSection,
  type SystemSettingsGroup,
} from './access'

export function requireSystemSettingsSection(
  group: SystemSettingsGroup,
  section: string,
  validSections: readonly string[]
) {
  const user = useAuthStore.getState().auth.user
  if (
    !validSections.includes(section) ||
    !canViewSettingsSection(user, group, section)
  ) {
    throw redirect({ to: '/403' })
  }
}

export function redirectToFirstVisibleSystemSettings(
  group?: SystemSettingsGroup
) {
  const user = useAuthStore.getState().auth.user
  const target = firstVisibleSettingsSection(user, group)
  if (!target) throw redirect({ to: '/403' })
  throw redirect({
    href: `/system-settings/${target.group}/${target.section}`,
    replace: true,
  })
}
