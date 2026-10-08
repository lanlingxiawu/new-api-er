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
import type {
  RelayErrorRule,
  RelayErrorRuleAction,
  RelayErrorRuleSource,
} from '../types'

// Mirrors setting/operation_setting/relay_error_display_setting.go.
export const MAX_RULES = 100
export const MAX_MESSAGE_LENGTH = 500
export const MAX_NAME_LENGTH = 100
const MAX_LIST_ITEMS = 20
const MAX_KEYWORD_LENGTH = 100
export const MAX_EDITS = 20
export const MAX_EDIT_LENGTH = 200
const WHOLE_NUMBER = /^\d+$/

export const RULE_SOURCES = ['upstream', 'local', 'any'] as const

// One find-and-replace step of an edit rule, as edited in the dialog.
export type EditRow = {
  id: number
  find: string
  replace: string
  regex: boolean
}

let nextEditId = 1

export function emptyEditRow(): EditRow {
  return { id: nextEditId++, find: '', replace: '', regex: false }
}

export type RuleRow = {
  // Stable key so edits and reordering do not remount rows.
  id: number
  name: string
  source: RelayErrorRuleSource
  // Comma-separated as typed; parsed on save so partial input stays editable.
  statusCodes: string
  errorCodes: string
  keywords: string
  action: RelayErrorRuleAction
  message: string
  edits: EditRow[]
  statusCode: string
}

export type RuleFieldErrors = Partial<
  Record<
    'name' | 'statusCodes' | 'lists' | 'message' | 'edits' | 'statusCode',
    string
  >
>

type Translate = (key: string) => string

export function splitList(text: string): string[] {
  return text
    .split(/[,，\n]/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
}

export function emptyRuleRow(id: number): RuleRow {
  return {
    id,
    name: '',
    source: 'upstream',
    statusCodes: '',
    errorCodes: '',
    keywords: '',
    action: 'replace',
    message: '',
    edits: [emptyEditRow()],
    statusCode: '',
  }
}

export function toRow(rule: RelayErrorRule, id: number): RuleRow {
  return {
    id,
    name: rule.name ?? '',
    source: rule.source,
    statusCodes: (rule.status_codes ?? []).join(', '),
    errorCodes: (rule.error_codes ?? []).join(', '),
    keywords: (rule.keywords ?? []).join(', '),
    action: rule.action,
    message: rule.message ?? '',
    edits: rule.edits?.length
      ? rule.edits.map((item) => ({
          id: nextEditId++,
          find: item.find,
          replace: item.replace ?? '',
          regex: item.regex ?? false,
        }))
      : [emptyEditRow()],
    statusCode: rule.status_code ? String(rule.status_code) : '',
  }
}

export function toRule(row: RuleRow): RelayErrorRule {
  const rule: RelayErrorRule = { source: row.source, action: row.action }
  if (row.name.trim()) rule.name = row.name.trim()
  const statusCodes = splitList(row.statusCodes).map(Number)
  if (statusCodes.length) rule.status_codes = statusCodes
  const errorCodes = splitList(row.errorCodes)
  if (errorCodes.length) rule.error_codes = errorCodes
  const keywords = splitList(row.keywords)
  if (keywords.length) rule.keywords = keywords
  if (row.action === 'replace') {
    rule.message = row.message.trim()
  }
  if (row.action === 'edit') {
    // Find and replace are kept as typed: spaces can be part of what to match.
    rule.edits = row.edits.map((item) => ({
      find: item.find,
      ...(item.replace ? { replace: item.replace } : {}),
      ...(item.regex ? { regex: true } : {}),
    }))
  }
  if (row.action !== 'keep' && row.statusCode.trim()) {
    rule.status_code = Number(row.statusCode)
  }
  return rule
}

export function parseRows(raw: string): RuleRow[] {
  if (!raw.trim()) return []
  try {
    const parsed = JSON.parse(raw) as RelayErrorRule[]
    return Array.isArray(parsed) ? parsed.map(toRow) : []
  } catch {
    return []
  }
}

export function sourceLabel(source: RelayErrorRuleSource, t: Translate) {
  if (source === 'upstream') return t('Upstream errors')
  if (source === 'local') return t('Errors from this site')
  return t('All errors')
}

// Characters as the backend counts them (utf8.RuneCountInString), not UTF-16
// units: an emoji is one character, not two.
export function charCount(text: string): number {
  return [...text].length
}

// Same limits the backend enforces on save, reported per field so the rule
// dialog can show each message under the input it belongs to.
export function validateRuleRow(row: RuleRow, t: Translate): RuleFieldErrors {
  const errors: RuleFieldErrors = {}
  if (charCount(row.name.trim()) > MAX_NAME_LENGTH) {
    errors.name = t('The name is too long.')
  }
  const statusCodes = splitList(row.statusCodes)
  if (
    statusCodes.some(
      (code) =>
        !WHOLE_NUMBER.test(code) || Number(code) < 100 || Number(code) > 599
    )
  ) {
    errors.statusCodes = t(
      'Status codes must be whole numbers from 100 to 599.'
    )
  }
  const errorCodes = splitList(row.errorCodes)
  const keywords = splitList(row.keywords)
  if (errorCodes.length > MAX_LIST_ITEMS || keywords.length > MAX_LIST_ITEMS) {
    errors.lists = t('Use at most 20 error codes and 20 keywords per rule.')
  } else if (
    [...errorCodes, ...keywords].some(
      (item) => charCount(item) > MAX_KEYWORD_LENGTH
    )
  ) {
    errors.lists = t(
      'Each error code or keyword can be at most 100 characters.'
    )
  }
  if (row.action === 'replace') {
    if (!row.message.trim()) {
      errors.message = t('Enter the message users will see.')
    } else if (charCount(row.message.trim()) > MAX_MESSAGE_LENGTH) {
      errors.message = t('The message can be at most 500 characters.')
    }
  }
  if (row.action === 'edit') {
    const editError = validateEdits(row.edits, t)
    if (editError) errors.edits = editError
  }
  if (row.action !== 'keep') {
    const status = row.statusCode.trim()
    if (
      status &&
      (!WHOLE_NUMBER.test(status) ||
        Number(status) < 400 ||
        Number(status) > 599)
    ) {
      errors.statusCode = t('The returned status code must be from 400 to 599.')
    }
  }
  return errors
}

// Regular expressions run on the backend as Go RE2, which has no lookaround
// or backreferences; the browser would accept those, so reject them here.
const RE2_UNSUPPORTED = /\(\?<?[=!]|\\[1-9]/

// compileLikeRE2 compiles an RE2 pattern with the browser's engine to catch
// typos early. RE2-only syntax the browser does not know is rewritten first:
// inline flags such as (?-i) or (?s:...) and (?P<name>...). Unicode classes
// like \p{Han} need the 'u' flag in the browser, so both modes are tried. The
// backend check on save stays authoritative.
function compileLikeRE2(pattern: string): RegExp | undefined {
  const rewritten = pattern
    .replaceAll(/\(\?[imsU-]+\)/g, '')
    .replaceAll(/\(\?[imsU-]+:/g, '(?:')
    .replaceAll('(?P<', '(?<')
  for (const flags of ['i', 'iu']) {
    try {
      return new RegExp(rewritten, flags)
    } catch {
      // try the next mode
    }
  }
  return undefined
}

// replacementRefsExist mirrors the backend: every $name or ${name} in a regex
// replacement must name a group of the pattern ($$ is a literal $). Go turns a
// reference to a missing group into nothing, so "$5 credit" would lose "$5".
function replacementRefsExist(replace: string, pattern: RegExp): boolean {
  const match = new RegExp(`${pattern.source}|`, pattern.flags).exec('')
  const groupCount = match ? match.length - 1 : 0
  const names = new Set(Object.keys(match?.groups ?? {}))
  const reference = /\$(\$|\{([A-Za-z0-9_]+)\}|([A-Za-z0-9_]+))/g
  for (const found of replace.matchAll(reference)) {
    if (found[1] === '$') continue
    const name = found[2] ?? found[3] ?? ''
    if (/^\d+$/.test(name)) {
      if (Number(name) > groupCount) return false
    } else if (!names.has(name)) {
      return false
    }
  }
  return true
}

function validateEdits(edits: EditRow[], t: Translate): string | undefined {
  if (edits.length === 0) {
    return t('Add at least one find-and-replace step.')
  }
  if (edits.length > MAX_EDITS) {
    return t('Use at most 20 find-and-replace steps.')
  }
  for (const item of edits) {
    if (!item.find.trim()) {
      return t('Every step needs the text to find.')
    }
    if (
      charCount(item.find) > MAX_EDIT_LENGTH ||
      charCount(item.replace) > MAX_EDIT_LENGTH
    ) {
      return t('Find and replace can be at most 200 characters each.')
    }
    if (item.regex) {
      if (RE2_UNSUPPORTED.test(item.find)) {
        return t(
          'Lookahead, lookbehind and backreferences are not supported in regular expressions.'
        )
      }
      const pattern = compileLikeRE2(item.find)
      if (!pattern) {
        return t('A regular expression is not valid.')
      }
      // It would insert the replacement between every character.
      if (pattern.test('')) {
        return t('A regular expression must not match empty text.')
      }
      if (!replacementRefsExist(item.replace, pattern)) {
        return t(
          'The replacement refers to a group that does not exist. Write $$ for a literal $.'
        )
      }
    }
  }
  return undefined
}

export function firstRuleError(errors: RuleFieldErrors): string {
  return (
    errors.name ??
    errors.statusCodes ??
    errors.lists ??
    errors.message ??
    errors.edits ??
    errors.statusCode ??
    ''
  )
}
