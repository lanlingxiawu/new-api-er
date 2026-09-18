import { describe, expect, it } from 'vitest'

import type { AuthUser } from '@/stores/auth-store'

import {
  SYSTEM_SETTINGS_GROUPS,
  SYSTEM_SETTINGS_SECTIONS,
  canViewSettingsSection,
  firstVisibleSettingsSection,
} from '../access'

// 这张分区表是「系统设置」入口与索引路由的唯一依据：后端有 scope、页面有分区，但这里漏登记，
// 只被授予该 scope 的管理员看不到入口，索引路由还会把他重定向到 /403。

function userWithScopes(...scopes: string[]): AuthUser {
  const admin_permissions: Record<string, Record<string, boolean>> = {}
  for (const scope of scopes) {
    admin_permissions[`system_settings.${scope}`] = { view: true }
  }
  return {
    id: 1,
    username: 'section-admin',
    role: 10,
    permissions: { admin_permissions },
  } as AuthUser
}

describe('system settings section registry', () => {
  it('exposes every group exactly once with a non-empty section list', () => {
    const groups = Object.keys(SYSTEM_SETTINGS_SECTIONS)
    expect(new Set(groups).size).toBe(groups.length)
    expect(groups.sort()).toEqual([...SYSTEM_SETTINGS_GROUPS].sort())
    for (const group of SYSTEM_SETTINGS_GROUPS) {
      const sections = SYSTEM_SETTINGS_SECTIONS[group]
      expect(sections.length).toBeGreaterThan(0)
      expect(new Set(sections).size).toBe(sections.length)
    }
  })

  it('registers the relay timeout section under system tuning', () => {
    expect(SYSTEM_SETTINGS_SECTIONS['system-tuning']).toContain('relay-timeout')
  })

  it.each(
    SYSTEM_SETTINGS_GROUPS.flatMap((group) =>
      SYSTEM_SETTINGS_SECTIONS[group].map(
        (section) => [group, section] as const
      )
    )
  )('opens %s/%s for an admin holding only that scope', (group, section) => {
    const user = userWithScopes(`${group}.${section}`)
    expect(canViewSettingsSection(user, group, section)).toBe(true)
    expect(firstVisibleSettingsSection(user)).toEqual({ group, section })
  })

  it('returns null when the admin holds no settings scope at all', () => {
    expect(firstVisibleSettingsSection(userWithScopes())).toBeNull()
    expect(firstVisibleSettingsSection(null)).toBeNull()
  })
})
