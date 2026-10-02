/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import i18next from 'i18next'
import * as z from 'zod'

type Issue = z.core.$ZodRawIssue

const NUMERIC_ORIGINS = new Set(['number', 'int', 'bigint'])
const COLLECTION_ORIGINS = new Set(['array', 'set'])

const t = (key: string, options?: Record<string, unknown>): string =>
  i18next.t(key, options)

function isBlank(input: unknown): boolean {
  return input === undefined || input === null || input === ''
}

function invalidTypeMessage(issue: Issue & { code: 'invalid_type' }): string {
  const expected = String(issue.expected)
  // `int()` reports a fractional number as an invalid type.
  if (
    expected === 'int' &&
    typeof issue.input === 'number' &&
    Number.isFinite(issue.input)
  ) {
    return t('Enter a whole number')
  }
  if (expected === 'number' || expected === 'int' || expected === 'bigint') {
    return t('Please enter a valid number')
  }
  if (isBlank(issue.input)) {
    return t('This field is required')
  }
  return t('Enter a valid value')
}

function tooSmallMessage(issue: Issue & { code: 'too_small' }): string {
  const count = Number(issue.minimum)
  if (NUMERIC_ORIGINS.has(issue.origin)) {
    const min = String(issue.minimum)
    return issue.inclusive === false
      ? t('Must be greater than {{min}}', { min })
      : t('Must be at least {{min}}', { min })
  }
  if (issue.origin === 'string') {
    if (issue.exact) {
      return t('Must be exactly {{count}} characters', { count })
    }
    return count <= 1
      ? t('This field is required')
      : t('Must be at least {{count}} characters', { count })
  }
  if (COLLECTION_ORIGINS.has(issue.origin)) {
    if (issue.exact) {
      return t('Must contain exactly {{count}} items', { count })
    }
    return count <= 1
      ? t('Select at least one item')
      : t('Must contain at least {{count}} items', { count })
  }
  return t('Enter a valid value')
}

function tooBigMessage(issue: Issue & { code: 'too_big' }): string {
  const count = Number(issue.maximum)
  if (NUMERIC_ORIGINS.has(issue.origin)) {
    const max = String(issue.maximum)
    return issue.inclusive === false
      ? t('Must be less than {{max}}', { max })
      : t('Must be at most {{max}}', { max })
  }
  if (issue.origin === 'string') {
    return issue.exact
      ? t('Must be exactly {{count}} characters', { count })
      : t('Must be at most {{count}} characters', { count })
  }
  if (COLLECTION_ORIGINS.has(issue.origin)) {
    return issue.exact
      ? t('Must contain exactly {{count}} items', { count })
      : t('Must contain at most {{count}} items', { count })
  }
  return t('Enter a valid value')
}

function invalidFormatMessage(format: string): string {
  if (format === 'email') {
    return t('Please enter a valid email address')
  }
  if (format === 'url') {
    return t('Please enter a valid URL')
  }
  return t('The format is not valid')
}

/**
 * Localized fallback for every zod issue that has no message of its own.
 * Messages written in a schema (`z.string().min(1, t('…'))`) still win.
 */
export function zodErrorMessage(issue: Issue): string {
  switch (issue.code) {
    case 'invalid_type':
      return invalidTypeMessage(issue)
    case 'too_small':
      return tooSmallMessage(issue)
    case 'too_big':
      return tooBigMessage(issue)
    case 'not_multiple_of':
      return t('Must be a multiple of {{divisor}}', {
        divisor: String(issue.divisor),
      })
    case 'invalid_format':
      return invalidFormatMessage(issue.format)
    case 'invalid_value':
      return t('Choose one of the available options')
    default:
      return t('Enter a valid value')
  }
}

/**
 * zod registers its English messages through a module side effect that the
 * production bundle tree-shakes away (zod declares `sideEffects: false`), which
 * left every form showing a bare "Invalid input". Registering our own messages
 * through an explicit call keeps them in the bundle and translates them.
 * Called once from `main.tsx`.
 */
export function installZodErrorMessages(): void {
  z.config({ localeError: zodErrorMessage })
}
