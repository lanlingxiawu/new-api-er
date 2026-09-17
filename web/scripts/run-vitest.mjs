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
// Entry point for `bun run test` / `bun run test:watch`.
//
// `bun run` substitutes its own runtime for `node` whenever its PATH lookup
// finds no Node binary. That happens on Windows even with Node installed when
// Git Bash hands child processes a truncated PATH: MSYS stops converting the
// PATH list at the first entry that goes *through a file* (for example
// `...\app.asar\node_modules\...` added by Electron apps), so every entry
// after it, including the Node directory, is dropped. Under Bun, vitest's
// externalized zod import loses its `z` export and every schema-backed test
// fails with `undefined is not an object (evaluating 'z.object')`.
//
// Resolution order:
// 1. Running under Node: load the vitest CLI in-process.
// 2. Running under Bun with a real Node on PATH: re-run vitest with that Node.
// 3. Running under Bun with no Node: run vitest under Bun with
//    vitest-bun.config.mjs, which inlines zod so vite transforms it.
import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url))
const VITEST_CLI = path.resolve(SCRIPT_DIR, '../node_modules/vitest/vitest.mjs')
const BUN_CONFIG = path.resolve(SCRIPT_DIR, 'vitest-bun.config.mjs')

function findRealNode() {
  const name = process.platform === 'win32' ? 'node.exe' : 'node'
  for (const dir of (process.env.PATH ?? '').split(path.delimiter)) {
    // Bun's own `node` shim lives in a temporary `bun-node-*` directory.
    if (!dir || /bun-node-[^\\/]*[\\/]?$/.test(dir)) continue
    const candidate = path.join(dir, name)
    try {
      if (fs.statSync(candidate).isFile()) return candidate
    } catch {
      // Missing or unreadable PATH entry: keep scanning.
    }
  }
  return null
}

const args = process.argv.slice(2)

if (process.versions.bun) {
  const realNode = findRealNode()
  if (realNode) {
    const result = spawnSync(realNode, [VITEST_CLI, ...args], {
      stdio: 'inherit',
    })
    if (result.error) throw result.error
    process.exit(result.status ?? 1)
  }
  const hasConfig = args.some(
    (arg) => arg === '--config' || arg === '-c' || arg.startsWith('--config=')
  )
  if (!hasConfig) process.argv.push('--config', BUN_CONFIG)
}

await import(pathToFileURL(VITEST_CLI).href)
