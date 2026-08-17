import type { VeridropDetectionResult } from './types'

export type VeridropReportCheck = {
  name: string
  displayName: string
  status: string
  score: number
  weight: number
  contribution: number | null
  durationMs: number | null
  error: string
  skipReason: string
  details: Record<string, unknown>
}

export type VeridropReportFilter = {
  label: string
  value: string
}

export type VeridropScoreReport = {
  totalScore: number
  verdict: string
  summary: string
  effectiveWeight: number
  hasCriticalIssues: boolean
  requestCount: number | null
  totalLatencyMs: number | null
  checks: VeridropReportCheck[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function finiteNumber(value: unknown, fallback = 0) {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function scoreNumber(value: unknown) {
  return Math.min(100, Math.max(0, finiteNumber(value)))
}

function optionalFiniteNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function stringValue(value: unknown) {
  return typeof value === 'string' ? value : ''
}

function containsCriticalIssue(details: Record<string, unknown>) {
  if (finiteNumber(details.critical_issue_count) > 0) return true
  if (!Array.isArray(details.issues)) return false
  return details.issues.some(
    (issue) => isRecord(issue) && issue.severity === 'critical'
  )
}

export function hasVeridropScoreReport(source?: string) {
  return parseVeridropScoreReport(source ?? '') != null
}

function normalizeCheckStatus(status: string) {
  const normalized = status.trim().toLowerCase()
  if (['pass', 'passed', 'success'].includes(normalized)) return 'pass'
  if (['fail', 'failed'].includes(normalized)) return 'fail'
  if (['skip', 'skipped'].includes(normalized)) return 'skip'
  if (normalized === 'error') return 'error'
  return normalized
}

export function parseVeridropScoreReport(
  source: string
): VeridropScoreReport | null {
  if (source.trim() === '') return null

  let raw: unknown
  try {
    raw = JSON.parse(source)
  } catch {
    return null
  }
  if (!isRecord(raw) || !Array.isArray(raw.results)) return null

  const parsedChecks = raw.results.flatMap((entry) => {
    if (!isRecord(entry)) return []
    const name = stringValue(entry.name)
    const displayName = stringValue(entry.display_name) || name
    const status = normalizeCheckStatus(stringValue(entry.status))
    if (name === '' || status === '') return []

    const details = isRecord(entry.details) ? entry.details : {}
    const durationMs = optionalFiniteNumber(entry.duration_ms)
    return [
      {
        name,
        displayName,
        status,
        score: scoreNumber(entry.score),
        weight: Math.max(0, finiteNumber(entry.weight)),
        durationMs: durationMs == null ? null : Math.max(0, durationMs),
        error: stringValue(entry.error),
        details,
      },
    ]
  })
  if (parsedChecks.length === 0) return null

  const effectiveWeight = parsedChecks.reduce(
    (sum, check) =>
      check.status === 'skip' ? sum : sum + Math.max(0, check.weight),
    0
  )
  const checks = parsedChecks.map((check) => ({
    name: check.name,
    displayName: check.displayName,
    status: check.status,
    score: check.score,
    weight: check.weight,
    contribution:
      check.status === 'skip' || effectiveWeight <= 0
        ? null
        : (check.score * check.weight) / effectiveWeight,
    durationMs: check.durationMs,
    error: check.error,
    skipReason: stringValue(check.details.skip_reason),
    details: check.details,
  }))
  const performance = isRecord(raw.performance) ? raw.performance : {}

  return {
    totalScore: scoreNumber(raw.total_score),
    verdict: stringValue(raw.verdict),
    summary: stringValue(raw.summary),
    effectiveWeight,
    hasCriticalIssues: parsedChecks.some((check) =>
      containsCriticalIssue(check.details)
    ),
    requestCount: optionalFiniteNumber(performance.request_count),
    totalLatencyMs: optionalFiniteNumber(performance.total_latency_ms),
    checks,
  }
}

export function getVeridropResultDisplayMessage(
  result: VeridropDetectionResult
) {
  const raw = result.error || result.run_error || result.summary
  if (raw === '') {
    const verdict = result.verdict.trim()
    return ['passed', 'pass', 'success', 'failed', 'fail', 'error'].includes(
      verdict.toLowerCase()
    )
      ? ''
      : verdict
  }

  try {
    const parsed: unknown = JSON.parse(raw)
    if (isRecord(parsed)) {
      const message = stringValue(parsed.message)
      if (message !== '') return message
      const detail = stringValue(parsed.detail)
      if (detail !== '') return detail
    }
  } catch {
    // Non-JSON upstream errors are already suitable for direct display.
  }
  return raw
}

function escapeHtml(value: unknown) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}

function resultOutcome(result: VeridropDetectionResult) {
  if (result.outcome === 'in_progress') return 'pending'
  if (result.outcome === 'cancelled') return 'skipped'
  if (
    result.outcome === 'completed' &&
    result.verdict.trim().toLowerCase() === 'marginal'
  ) {
    return 'marginal'
  }
  return result.outcome
}

function outcomeLabel(outcome: string, translate: (key: string) => string) {
  if (outcome === 'passed') return translate('Passed')
  if (outcome === 'marginal') return translate('Marginal')
  if (outcome === 'low_score') return translate('Below threshold')
  if (outcome === 'failed') return translate('Failed')
  if (outcome === 'skipped') return translate('Skipped')
  if (outcome === 'pending') return translate('In Progress')
  return translate('Completed')
}

function checkStatusLabel(status: string, translate: (key: string) => string) {
  const normalized = status.trim().toLowerCase()
  if (['pass', 'passed', 'success'].includes(normalized)) {
    return translate('Passed')
  }
  if (['fail', 'failed', 'error'].includes(normalized)) {
    return translate('Failed')
  }
  if (['skip', 'skipped'].includes(normalized)) {
    return translate('Skipped')
  }
  return status || '—'
}

function displayNumber(value: number | null) {
  if (value == null || !Number.isFinite(value)) return '—'
  return Number.isInteger(value) ? String(value) : value.toFixed(1)
}

function serializeForInlineScript(value: unknown) {
  return JSON.stringify(value)
    .replaceAll('<', '\\u003c')
    .replaceAll('>', '\\u003e')
    .replaceAll('&', '\\u0026')
    .replaceAll('\u2028', '\\u2028')
    .replaceAll('\u2029', '\\u2029')
}

export function buildDetailedDetectionReportHtml(
  results: VeridropDetectionResult[],
  translate: (key: string) => string,
  formatUpdatedAt: (timestamp: number) => string,
  generatedAt: string,
  filters: VeridropReportFilter[],
  locale: string
) {
  const parsedResults = results.map((result) => ({
    result,
    report: parseVeridropScoreReport(result.result_json ?? ''),
  }))
  const counts = {
    scored: 0,
    passed: 0,
    marginal: 0,
    low_score: 0,
    failed: 0,
    skipped: 0,
    pending: 0,
    completed: 0,
  }
  for (const item of parsedResults) {
    if (item.report != null) counts.scored += 1
    counts[resultOutcome(item.result)] += 1
  }

  const filterMarkup = filters
    .map(
      (filter) =>
        `<div class="filter"><span>${escapeHtml(filter.label)}</span><strong>${escapeHtml(filter.value)}</strong></div>`
    )
    .join('')
  const summaryItems = [
    [translate('Total Records'), results.length],
    [translate('Scored Records'), counts.scored],
    [translate('Passed'), counts.passed],
    [translate('Marginal'), counts.marginal],
    [translate('Below threshold'), counts.low_score],
    [translate('Failed'), counts.failed],
    [translate('Skipped'), counts.skipped],
  ]
  const summaryMarkup = summaryItems
    .map(
      ([label, value]) =>
        `<div class="metric"><span>${escapeHtml(label)}</span><strong>${escapeHtml(value)}</strong></div>`
    )
    .join('')

  const lazyEvidence: Record<string, unknown>[] = []
  const recordsMarkup = parsedResults
    .map(({ result, report }, recordIndex) => {
      const outcome = resultOutcome(result)
      const score =
        report?.totalScore ?? (result.status === 'done' ? result.score : null)
      const checksMarkup =
        report == null || report.checks.length === 0
          ? `<p class="empty-block">${escapeHtml(translate('No scoring details'))}</p>`
          : report.checks
              .map((check, checkIndex) => {
                const evidenceIndex =
                  Object.keys(check.details).length === 0
                    ? null
                    : lazyEvidence.push(check.details) - 1
                const evidenceMarkup =
                  evidenceIndex == null
                    ? `<p class="empty-block">${escapeHtml(translate('No evidence'))}</p>`
                    : `<details class="evidence" data-evidence-id="${evidenceIndex}"><summary>${escapeHtml(translate('Complete Evidence'))}</summary></details>`
                const checkError = check.error
                  ? `<div><span>${escapeHtml(translate('Check Error'))}</span><strong>${escapeHtml(check.error)}</strong></div>`
                  : ''
                const skipReason = check.skipReason
                  ? `<div><span>${escapeHtml(translate('Skip Reason'))}</span><strong>${escapeHtml(check.skipReason)}</strong></div>`
                  : ''
                return `<details class="check-card">
                  <summary class="check-summary"><div class="check-header">
                    <div><span class="eyebrow">${escapeHtml(translate('Detection Check'))} ${checkIndex + 1}</span><h3>${escapeHtml(check.displayName)}</h3><code>${escapeHtml(check.name)}</code></div>
                    <div class="check-result"><span class="status">${escapeHtml(checkStatusLabel(check.status, translate))}</span><strong>${escapeHtml(displayNumber(check.score))} ${escapeHtml(translate('points'))}</strong></div>
                  </div></summary>
                  <div class="check-content"><div class="check-meta">
                    <div><span>${escapeHtml(translate('Weight'))}</span><strong>${escapeHtml(displayNumber(check.weight))}</strong></div>
                    <div><span>${escapeHtml(translate('Contribution'))}</span><strong>${escapeHtml(displayNumber(check.contribution))}</strong></div>
                    <div><span>${escapeHtml(translate('Duration (ms)'))}</span><strong>${escapeHtml(displayNumber(check.durationMs))}</strong></div>
                    ${checkError}${skipReason}
                  </div>
                  ${evidenceMarkup}
                  ${evidenceIndex == null ? '' : `<details class="raw-evidence" data-raw-evidence-id="${evidenceIndex}"><summary>${escapeHtml(translate('Raw Evidence JSON'))}</summary></details>`}</div>
                </details>`
              })
              .join('')
      const summary = report?.summary || result.summary
      const error = result.error || result.run_error
      return `<details class="record">
        <summary class="record-summary"><div class="record-header">
          <div><span class="eyebrow">${escapeHtml(translate('Record'))} ${recordIndex + 1}</span><h2>${escapeHtml(result.channel_name || `#${result.channel_id}`)} · ${escapeHtml(result.model || '—')}</h2><p>${escapeHtml(formatUpdatedAt(result.updated_at))}</p><div class="record-quick-meta"><span>${escapeHtml(translate('Protocol'))}: <strong>${escapeHtml(result.protocol || '—')}</strong></span><span>${escapeHtml(translate('Mode'))}: <strong>${escapeHtml(result.mode || '—')}</strong></span></div></div>
          <div class="outcome outcome-${escapeHtml(outcome)}"><strong>${escapeHtml(outcomeLabel(outcome, translate))}</strong>${score == null ? '' : `<span>${escapeHtml(displayNumber(score))} ${escapeHtml(translate('points'))}</span>`}</div>
        </div></summary>
        <div class="record-content">
        <div class="record-meta">
          <div><span>${escapeHtml(translate('Record ID'))}</span><strong>${escapeHtml(result.id)}</strong></div>
          <div><span>${escapeHtml(translate('Channel ID'))}</span><strong>${escapeHtml(result.channel_id)}</strong></div>
          <div><span>${escapeHtml(translate('Protocol'))}</span><strong>${escapeHtml(result.protocol || '—')}</strong></div>
          <div><span>${escapeHtml(translate('Mode'))}</span><strong>${escapeHtml(result.mode || '—')}</strong></div>
        </div>
        ${summary ? `<div class="message"><span>${escapeHtml(translate('Summary'))}</span><p>${escapeHtml(summary)}</p></div>` : ''}
        ${error ? `<div class="message error"><span>${escapeHtml(translate('Error'))}</span><p>${escapeHtml(error)}</p></div>` : ''}
        <h3 class="checks-title">${escapeHtml(translate('Detection Checks'))}</h3>
        <div class="checks">${checksMarkup}</div>
        </div>
      </details>`
    })
    .join('')

  return `<!doctype html>
<html lang="${escapeHtml(locale || 'en')}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHtml(translate('Detailed Detection Report'))}</title>
  <style>
    :root{color-scheme:light;--ink:#172033;--muted:#60708a;--line:#dbe4f0;--soft:#f4f7fb;--blue:#2563eb;--green:#087a55;--amber:#a45b00;--red:#c81e3a}*{box-sizing:border-box}body{margin:0;background:#eef3f9;color:var(--ink);font:14px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif}main{max-width:1180px;margin:0 auto;padding:36px 24px 72px}.report-header,.record{background:#fff;border:1px solid var(--line);border-radius:18px;box-shadow:0 10px 30px rgba(31,50,80,.06)}.report-header{padding:28px;margin-bottom:24px}.eyebrow{display:block;color:var(--blue);font-size:11px;font-weight:700;letter-spacing:.08em;text-transform:uppercase}h1,h2,h3,h4,p{margin-top:0}h1{font-size:28px;margin-bottom:4px}.generated{color:var(--muted);margin-bottom:22px}.filters,.metrics,.record-meta,.check-meta{display:grid;gap:10px}.filters{grid-template-columns:repeat(auto-fit,minmax(150px,1fr));margin-bottom:20px}.filter,.metric,.record-meta>div,.check-meta>div{background:var(--soft);border-radius:10px;padding:10px 12px}.filter span,.metric span,.record-meta span,.check-meta span,.message span{display:block;color:var(--muted);font-size:11px;margin-bottom:2px}.metrics{grid-template-columns:repeat(auto-fit,minmax(110px,1fr))}.metric strong{font-size:20px}.record{padding:26px;margin-top:18px;break-before:page}.record:first-of-type{break-before:auto}.record-summary,.check-summary{position:relative;cursor:pointer;padding-right:28px;list-style:none}.record-summary::-webkit-details-marker,.check-summary::-webkit-details-marker{display:none}.record-summary::after,.check-summary::after{content:'›';position:absolute;top:50%;right:2px;color:var(--blue);font-size:28px;font-weight:400;line-height:1;transform:translateY(-50%);transition:transform .15s ease}.record[open]>.record-summary::after,.check-card[open]>.check-summary::after{transform:translateY(-50%) rotate(90deg)}.record-header,.check-header{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.record-header h2{font-size:20px;margin:4px 0}.record-header p{color:var(--muted);margin:0}.record-quick-meta{display:flex;flex-wrap:wrap;gap:6px;margin-top:10px}.record-quick-meta span{border:1px solid var(--line);border-radius:999px;color:var(--muted);font-size:12px;padding:2px 8px}.record-quick-meta strong{color:var(--ink);font-weight:600}.record-content{border-top:1px solid var(--line);margin-top:18px;padding-top:2px}.outcome{min-width:110px;border-radius:12px;background:var(--soft);padding:10px 14px;text-align:right}.outcome strong,.outcome span{display:block}.outcome-passed{color:var(--green);background:#eaf8f2}.outcome-marginal{color:var(--amber);background:#fff5df}.outcome-low_score,.outcome-failed{color:var(--red);background:#fff0f2}.record-meta{grid-template-columns:repeat(4,1fr);margin:18px 0}.message{border-left:3px solid var(--line);padding:8px 12px;margin:10px 0}.message p{margin:0;white-space:pre-wrap}.message.error{border-color:var(--red);background:#fff7f8}.checks-title{margin:24px 0 12px}.checks{display:grid;gap:10px}.check-card{border:1px solid var(--line);border-radius:14px;padding:14px 18px;break-inside:avoid-page}.check-card[open]{border-color:#b9cae6;background:#fbfdff}.check-header h3{font-size:16px;margin:3px 0 0}.check-header code{color:var(--muted);font-size:12px}.check-result{text-align:right}.check-result span,.check-result strong{display:block}.check-meta{grid-template-columns:repeat(auto-fit,minmax(120px,1fr));margin:14px 0}.evidence{border-top:1px solid var(--line);padding-top:14px}.evidence-content{padding-top:10px}.evidence-object{display:grid;gap:8px;margin:0}.evidence-field{border-left:2px solid var(--line);padding-left:12px}.evidence-field dt{color:var(--muted);font-family:ui-monospace,monospace;font-size:12px}.evidence-field dd{margin:5px 0 0}.evidence-list{display:grid;gap:8px;list-style:none;margin:8px 0 0;padding:0}.evidence-list>li{display:grid;grid-template-columns:28px minmax(0,1fr);gap:8px;align-items:start}.item-index{display:flex;width:24px;height:24px;align-items:center;justify-content:center;border-radius:999px;background:#e8eef8;color:#405574;font-size:11px;font-weight:700}.scalar{white-space:pre-wrap;overflow-wrap:anywhere}.empty,.empty-block{color:var(--muted)}details{margin-top:12px}summary{cursor:pointer;color:var(--blue);font-weight:600}pre{max-height:360px;overflow:auto;background:#172033;color:#edf4ff;border-radius:10px;padding:14px;white-space:pre-wrap;overflow-wrap:anywhere}@media(max-width:720px){main{padding:18px 10px 40px}.report-header,.record{border-radius:12px;padding:18px}.record-header,.check-header{display:block}.outcome,.check-result{margin-top:10px;text-align:left}.record-meta{grid-template-columns:repeat(2,1fr)}}@media print{body{background:#fff}main{max-width:none;padding:0}.report-header,.record{box-shadow:none}.record{border-radius:0;border-width:1px 0 0;margin-top:16px;padding:20px 0}.check-card{break-inside:avoid-page}details:not([open])>:not(summary){display:block}.raw-evidence{display:none!important}}
  </style>
</head>
<body><main>
  <section class="report-header">
    <span class="eyebrow">Veridrop</span><h1>${escapeHtml(translate('Detailed Detection Report'))}</h1>
    <p class="generated">${escapeHtml(translate('Generated At'))}: ${escapeHtml(generatedAt)}</p>
    <h2>${escapeHtml(translate('Report Overview'))}</h2><div class="filters">${filterMarkup}</div><div class="metrics">${summaryMarkup}</div>
  </section>
  ${recordsMarkup}
</main><script>
(()=>{const evidenceData=${serializeForInlineScript(lazyEvidence)};const empty='—';const make=(tag,className,text)=>{const node=document.createElement(tag);if(className)node.className=className;if(text!==undefined)node.textContent=text;return node};const appendValue=(parent,value)=>{if(Array.isArray(value)){if(value.length===0){parent.append(make('span','empty',empty));return}const list=make('ol','evidence-list');value.forEach((item,index)=>{const itemNode=document.createElement('li');itemNode.append(make('span','item-index',String(index+1)));const itemValue=make('div','item-value');appendValue(itemValue,item);itemNode.append(itemValue);list.append(itemNode)});parent.append(list);return}if(value&&typeof value==='object'){const entries=Object.entries(value);if(entries.length===0){parent.append(make('span','empty',empty));return}const object=make('dl','evidence-object');entries.forEach(([key,item])=>{const field=make('div','evidence-field');field.append(make('dt','',key));const content=document.createElement('dd');appendValue(content,item);field.append(content);object.append(field)});parent.append(object);return}parent.append(make('span',value==null||value===''?'empty':'scalar',value==null||value===''?empty:String(value)))};const render=(details)=>{if(details.dataset.rendered==='true')return;const index=Number(details.dataset.evidenceId??details.dataset.rawEvidenceId);const value=evidenceData[index];if(value===undefined)return;if(details.dataset.evidenceId!==undefined){const content=make('div','evidence-content');appendValue(content,value);details.append(content)}else{details.append(make('pre','',JSON.stringify(value,null,2)))}details.dataset.rendered='true';delete details.dataset.evidenceId;delete details.dataset.rawEvidenceId};const deferred=()=>document.querySelectorAll('details[data-evidence-id],details[data-raw-evidence-id]').forEach(render);document.querySelectorAll('details[data-evidence-id],details[data-raw-evidence-id]').forEach(details=>details.addEventListener('toggle',()=>{if(details.open)render(details)}));window.addEventListener('beforeprint',deferred)})();
</script></body></html>`
}
