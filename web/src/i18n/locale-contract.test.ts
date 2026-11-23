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
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { expect, test } from 'vitest'

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'] as const

test.each(locales)(
  '%s preserves translation keys and interpolation variables',
  (locale) => {
    const read = (name: string): Record<string, string> =>
      JSON.parse(
        readFileSync(resolve('src/i18n/locales', `${name}.json`), 'utf8')
      ).translation
    const english = read('en')
    const translated = read(locale)
    expect(Object.keys(translated).sort()).toEqual(Object.keys(english).sort())
    const mismatches: string[] = []
    for (const [key, value] of Object.entries(english)) {
      const variables = (text: string) =>
        [...text.matchAll(/\{\{\s*-?\s*([^},\s]+)(?:,[^}]*)?\s*\}\}/g)]
          .map((match) => match[1])
          .sort()
      if (
        JSON.stringify(variables(value)) !==
        JSON.stringify(variables(translated[key]))
      ) {
        mismatches.push(key)
      }
    }
    expect(mismatches).toEqual([])
    expect(translated['View related audit']).toBeTruthy()
    if (locale !== 'en') {
      expect(translated['View related audit']).not.toBe('View related audit')
    }
  }
)
