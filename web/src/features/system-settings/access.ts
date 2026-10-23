import {
  canEditSystemSettingsScope,
  canViewAnySystemSettings,
  canViewSystemSettingsScope,
} from '@/lib/admin-permissions'
import type { AuthUser } from '@/stores/auth-store'

export const SYSTEM_SETTINGS_GROUPS = [
  'site',
  'auth',
  'billing',
  'models',
  'security',
  'content',
  'operations',
  'system-tuning',
  'request-policies',
] as const

export type SystemSettingsGroup = (typeof SYSTEM_SETTINGS_GROUPS)[number]

export const SYSTEM_SETTINGS_SECTIONS: Record<SystemSettingsGroup, string[]> = {
  'request-policies': ['routing', 'health', 'filtering'],
  site: ['system-info', 'notice', 'header-navigation', 'sidebar-modules'],
  auth: ['basic-auth', 'oauth', 'passkey', 'bot-protection', 'custom-oauth'],
  billing: [
    'quota',
    'currency',
    'model-pricing',
    'group-pricing',
    'payment',
    'checkin',
  ],
  models: ['global', 'gemini', 'claude', 'grok', 'model-deployment'],
  security: ['rate-limit', 'ssrf', 'token-limits'],
  content: [
    'dashboard',
    'announcements',
    'api-info',
    'faq',
    'uptime-kuma',
    'chat',
    'drawing',
  ],
  operations: [
    'node-control',
    'behavior',
    'alerts',
    'email',
    'worker',
    'logs',
    'request-log',
    'performance',
    'update-checker',
  ],
  'system-tuning': [
    'gateway-rate-limit',
    'database-pool',
    'login-session-policy',
    'settlement-guard',
    'ledger-pipeline',
    'relay-log-pipeline',
    'export-settings',
    'log-query',
    'log-export',
    'ledger-detail',
    'fallback-backfill',
    'relay-timeout',
    'group-retry-times',
    'relay-error-display',
  ],
}

export function systemSettingsScope(group: string, section: string): string {
  return `${group}.${section}`
}

export function canViewSettingsSection(
  user: AuthUser | null | undefined,
  group: string,
  section: string
): boolean {
  return canViewSystemSettingsScope(user, systemSettingsScope(group, section))
}

export function canEditSettingsSection(
  user: AuthUser | null | undefined,
  group: string,
  section: string
): boolean {
  return canEditSystemSettingsScope(user, systemSettingsScope(group, section))
}

export { canViewAnySystemSettings }

export function scopeFromSystemSettingsUrl(url: string): string | null {
  const match = url.match(/^\/system-settings\/([^/?]+)\/([^/?]+)/)
  if (!match) return null
  return systemSettingsScope(match[1], match[2])
}

export function firstVisibleSettingsSection(
  user: AuthUser | null | undefined,
  group?: SystemSettingsGroup
): { group: SystemSettingsGroup; section: string } | null {
  const groups = group ? [group] : SYSTEM_SETTINGS_GROUPS
  for (const candidateGroup of groups) {
    for (const section of SYSTEM_SETTINGS_SECTIONS[candidateGroup]) {
      if (canViewSettingsSection(user, candidateGroup, section)) {
        return { group: candidateGroup, section }
      }
    }
  }
  return null
}
