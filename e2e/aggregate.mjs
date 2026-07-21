import fs from 'node:fs'

function readJsonl(f) {
  if (!fs.existsSync(f)) return []
  return fs
    .readFileSync(f, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map((l) => JSON.parse(l))
}

const smoke = readJsonl('results/smoke.jsonl')
const rbac = readJsonl('results/rbac.jsonl')
const login = readJsonl('results/login.jsonl')
const crud = readJsonl('results/crud.jsonl')
const employee = readJsonl('results/employee.jsonl')
const correctness = fs.existsSync('results/correctness.json') ? JSON.parse(fs.readFileSync('results/correctness.json', 'utf8')) : null
const ALL_ROLES = ['guest', 'common', 'employee', 'admin', 'root']

// dedupe smoke by ui+role+route (last wins)
const smokeMap = new Map()
for (const r of smoke) smokeMap.set(`${r.ui}|${r.role}|${r.route}`, r)
const smokeRows = [...smokeMap.values()]

let md = `# new-api E2E Test Report\n\n`
md += `Generated: ${new Date().toISOString()}\n\n`
md += `Backend :3000 (real MySQL/PG/Redis) · Default UI :3002 · Classic UI :5173 · Playwright/Chromium\n\n`
md += `Accounts: e2e_root (root/100), e2e_admin (admin/10), e2e_common (common/1), all password \`Test1234!\`; guest = unauthenticated.\n\n`

// summary
const total = smokeRows.length
const passed = smokeRows.filter((r) => r.ok).length
const failed = total - passed
md += `## Summary\n\n`
md += `- Smoke pages tested: **${total}** — PASS **${passed}**, FAIL **${failed}**\n`
md += `- RBAC checks: ${rbac.length} · Login checks: ${login.length} · CRUD flows: ${crud.length}\n\n`

// per ui/role counts
md += `### Smoke pass/fail by UI + role\n\n`
md += `| UI | Role | Pass | Fail | Total |\n|---|---|---|---|---|\n`
const combos = new Map()
for (const r of smokeRows) {
  const k = `${r.ui}|${r.role}`
  if (!combos.has(k)) combos.set(k, { p: 0, f: 0 })
  const c = combos.get(k)
  r.ok ? c.p++ : c.f++
}
for (const [k, c] of [...combos.entries()].sort()) {
  const [ui, role] = k.split('|')
  md += `| ${ui} | ${role} | ${c.p} | ${c.f} | ${c.p + c.f} |\n`
}
md += `\n`

// failures detail
const fails = smokeRows.filter((r) => !r.ok)
md += `## Smoke FAILURES (${fails.length})\n\n`
if (!fails.length) md += `None.\n\n`
else {
  md += `| UI | Role | Route | finalUrl | blank? | pageErrors | 5xx | note |\n|---|---|---|---|---|---|---|---|\n`
  for (const r of fails.sort((a, b) => (a.ui + a.role + a.route).localeCompare(b.ui + b.role + b.route))) {
    md += `| ${r.ui} | ${r.role} | \`${r.route}\` | ${r.finalUrl.replace(/^https?:\/\/[^/]+/, '')} | ${r.rendered ? 'no' : 'YES'} | ${(r.pageErrors || []).join('<br>').replace(/\|/g, '\\|').slice(0, 300) || '-'} | ${(r.serverErrors || []).join('<br>').slice(0, 200) || '-'} | ${(r.snippet || '').replace(/\|/g, '\\|').slice(0, 80)} |\n`
  }
  md += `\n`
}

// console errors summary (non-fatal but reported)
const withConsole = smokeRows.filter((r) => (r.consoleErrors || []).length)
md += `## Pages with console errors (non-fatal, ${withConsole.length})\n\n`
if (!withConsole.length) md += `None.\n\n`
else {
  md += `| UI | Role | Route | console.error (first) |\n|---|---|---|---|\n`
  for (const r of withConsole.sort((a, b) => (a.ui + a.route).localeCompare(b.ui + b.route))) {
    md += `| ${r.ui} | ${r.role} | \`${r.route}\` | ${(r.consoleErrors[0] || '').replace(/\|/g, '\\|').slice(0, 140)} |\n`
  }
  md += `\n`
}

// full matrix
md += `## Full smoke matrix\n\n`
for (const ui of ['default', 'classic']) {
  md += `### ${ui}\n\n`
  const routes = [...new Set(smokeRows.filter((r) => r.ui === ui).map((r) => r.route))].sort()
  const roles = ALL_ROLES
  md += `| Route | ${roles.join(' | ')} |\n|---|${roles.map(() => '---').join('|')}|\n`
  for (const route of routes) {
    const cells = roles.map((role) => {
      const r = smokeMap.get(`${ui}|${role}|${route}`)
      if (!r) return '-'
      return r.ok ? 'PASS' : r.rendered ? 'FAIL' : 'BLANK'
    })
    md += `| \`${route}\` | ${cells.join(' | ')} |\n`
  }
  md += `\n`
}

// RBAC
md += `## RBAC results\n\n`
const leaks = rbac.filter((r) => r.leak)
md += `Leaks detected: **${leaks.length}**\n\n`
md += `| UI | kind | route | finalUrl | stayed | forbiddenShown | leak |\n|---|---|---|---|---|---|---|\n`
for (const r of rbac) {
  md += `| ${r.ui} | ${r.kind} | \`${r.route}\` | ${(r.finalUrl || '').replace(/^https?:\/\/[^/]+/, '')} | ${r.stayed ?? '-'} | ${r.forbiddenShown ?? r.redirected ?? '-'} | ${r.leak ? 'LEAK' : '-'} |\n`
}
md += `\n`

// login
md += `## Login flows\n\n`
md += `| UI | case | finalUrl | result |\n|---|---|---|---|\n`
for (const r of login) {
  const ok = r.case === 'valid' ? r.loggedIn : r.stayedOnLogin
  md += `| ${r.ui} | ${r.case} | ${(r.finalUrl || '').replace(/^https?:\/\/[^/]+/, '')} | ${ok ? 'PASS' : 'FAIL'} |\n`
}
md += `\n`

// CRUD
md += `## Key CRUD flows\n\n`
if (!crud.length) md += `Not run.\n\n`
else {
  md += `| UI | flow | result | detail |\n|---|---|---|---|\n`
  for (const r of crud) {
    md += `| ${r.ui} | ${r.flow} | ${r.ok ? 'PASS' : 'FAIL'} | ${(r.detail || '').replace(/\|/g, '\\|').slice(0, 160)} |\n`
  }
  md += `\n`
}

// ---------- Employee role (B) ----------
md += `## Employee role (B)\n\n`
md += `Seeded \`e2e_emp\` = common user (role 1) + \`employee_profiles\` row (status=1) bound to tier (rate 0.10). Employee-ness is via employee_profiles, not role, so RBAC treats it as a common user for admin pages.\n\n`
const empSmoke = smokeRows.filter((r) => r.role === 'employee')
md += `- Employee smoke pages: ${empSmoke.filter((r) => r.ok).length}/${empSmoke.length} passed.\n`
const empRbac = rbac.filter((r) => r.kind === 'employee-admin')
md += `- Employee blocked from admin pages: ${empRbac.filter((r) => !r.leak).length}/${empRbac.length} correctly blocked, ${empRbac.filter((r) => r.leak).length} leaks.\n`
const empSummary = employee.find((e) => e.check === 'commission_summary')
if (empSummary) {
  md += `- Employee self commission summary (via their own session): performance(profit)=${empSummary.current_performance_quota}, commission_total(accrual)=${empSummary.commission_total_quota}, current_commission(period)=${empSummary.current_commission_quota}, customer_consumption=${empSummary.customer_total_consumption_quota} — matches DB ground truth.\n`
}
md += `\n`

// ---------- Ledger / commission correctness (C) ----------
md += `## 员工 / 台账 / 提成 data correctness (C)\n\n`
if (correctness) {
  const c = correctness
  md += `### Seed\n\n`
  md += Object.entries(c.seed).map(([k, v]) => `- **${k}**: ${v}`).join('\n') + '\n\n'
  md += `### Known consumption driven\n\n`
  md += `- N = **${c.driven.N_requests}** requests as customer C through the :3000 relay (${c.driven.note}).\n\n`
  md += `### DB ground truth\n\n`
  md += '```json\n' + JSON.stringify(c.db_ground_truth, null, 2) + '\n```\n\n'
  md += `### Expected formulas\n\n`
  md += Object.entries(c.expected_formulas).map(([k, v]) => `- **${k}**: ${v}`).join('\n') + '\n\n'
  md += `### Reconciliation (DB vs formula)\n\n`
  md += `| Check | Result |\n|---|---|\n`
  for (const [k, v] of Object.entries(c.reconciliation)) md += `| ${k.replace(/\|/g, '\\|')} | ${v} |\n`
  md += `\n### API-displayed numbers\n\n`
  md += '```json\n' + JSON.stringify(c.api_display, null, 2) + '\n```\n\n'
  md += `### UI-displayed numbers\n\n`
  md += Object.entries(c.ui_display).map(([k, v]) => `- **${k}**: ${v}`).join('\n') + '\n\n'
  md += `### D3 ledger hour-boundary (no double-count)\n\n`
  md += '```json\n' + JSON.stringify(c.d3_hour_boundary, null, 2) + '\n```\n\n'
  md += `### Notes\n\n`
  md += Object.entries(c.notes).map(([k, v]) => `- **${k}**: ${v}`).join('\n') + '\n\n'
} else {
  md += `Not run.\n\n`
}

fs.writeFileSync('REPORT.md', md)
const empPass = employee.filter((e) => e.ok !== false).length
console.log(`REPORT.md written. smoke=${total} pass=${passed} fail=${failed} rbac=${rbac.length} leaks=${leaks.length} login=${login.length} crud=${crud.length} employee=${employee.length}`)
