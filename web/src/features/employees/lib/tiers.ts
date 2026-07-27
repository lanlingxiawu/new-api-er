import type { EmployeeTier } from '../types'

const GROUP_BADGE_COLORS = [
  'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/50 dark:bg-sky-950/50 dark:text-sky-300',
  'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/50 dark:bg-emerald-950/50 dark:text-emerald-300',
  'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/50 dark:bg-amber-950/50 dark:text-amber-300',
  'border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/50 dark:bg-violet-950/50 dark:text-violet-300',
  'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/50 dark:text-rose-300',
  'border-cyan-200 bg-cyan-50 text-cyan-700 dark:border-cyan-900/50 dark:bg-cyan-950/50 dark:text-cyan-300',
  'border-lime-200 bg-lime-50 text-lime-700 dark:border-lime-900/50 dark:bg-lime-950/50 dark:text-lime-300',
  'border-fuchsia-200 bg-fuchsia-50 text-fuchsia-700 dark:border-fuchsia-900/50 dark:bg-fuchsia-950/50 dark:text-fuchsia-300',
]

const GROUP_DOT_COLORS = [
  'bg-sky-500',
  'bg-emerald-500',
  'bg-amber-500',
  'bg-violet-500',
  'bg-rose-500',
  'bg-cyan-500',
  'bg-lime-500',
  'bg-fuchsia-500',
]

const LEVEL_BADGE_COLORS = [
  'border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-950/50 dark:text-slate-300',
  'border-indigo-200 bg-indigo-50 text-indigo-700 dark:border-indigo-900/50 dark:bg-indigo-950/50 dark:text-indigo-300',
  'border-teal-200 bg-teal-50 text-teal-700 dark:border-teal-900/50 dark:bg-teal-950/50 dark:text-teal-300',
  'border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-900/50 dark:bg-orange-950/50 dark:text-orange-300',
  'border-pink-200 bg-pink-50 text-pink-700 dark:border-pink-900/50 dark:bg-pink-950/50 dark:text-pink-300',
  'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900/50 dark:bg-blue-950/50 dark:text-blue-300',
  'border-green-200 bg-green-50 text-green-700 dark:border-green-900/50 dark:bg-green-950/50 dark:text-green-300',
  'border-yellow-200 bg-yellow-50 text-yellow-700 dark:border-yellow-900/50 dark:bg-yellow-950/50 dark:text-yellow-300',
]

function getGroupColorIndex(group: string) {
  let hash = 0
  for (const char of group) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  }
  return hash % GROUP_BADGE_COLORS.length
}

function getLevelColorIndex(level: number | string | undefined) {
  const numericLevel = Number(level || 0)
  if (Number.isFinite(numericLevel) && numericLevel > 0) {
    return (Math.floor(numericLevel) - 1) % LEVEL_BADGE_COLORS.length
  }
  return 0
}

export function getEmployeeTierGroup(tier: EmployeeTier, defaultGroup: string) {
  return tier.group?.trim() || defaultGroup
}

export function getEmployeeTierGroupBadgeClass(group: string) {
  return GROUP_BADGE_COLORS[getGroupColorIndex(group)]
}

export function getEmployeeTierGroupDotClass(group: string) {
  return GROUP_DOT_COLORS[getGroupColorIndex(group)]
}

export function getEmployeeTierLevelBadgeClass(
  level: number | string | undefined
) {
  return LEVEL_BADGE_COLORS[getLevelColorIndex(level)]
}

export function compareEmployeeTiersByGroupLevel(
  a: EmployeeTier,
  b: EmployeeTier,
  defaultGroup: string
) {
  const groupCompare = getEmployeeTierGroup(a, defaultGroup).localeCompare(
    getEmployeeTierGroup(b, defaultGroup),
    undefined,
    { numeric: true, sensitivity: 'base' }
  )
  if (groupCompare !== 0) return groupCompare

  const levelCompare = Number(a.level || 0) - Number(b.level || 0)
  if (levelCompare !== 0) return levelCompare

  const thresholdCompare =
    Number(a.threshold_usd || 0) - Number(b.threshold_usd || 0)
  if (thresholdCompare !== 0) return thresholdCompare

  return Number(a.id || 0) - Number(b.id || 0)
}
