/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'

import type {
  PriceMonitorMatrixItem,
  PriceMonitorPriceCell,
  PriceMonitorSourceHeader,
} from '../types'
import {
  applyMonitorPricing,
  convertMonitorPricing,
  evaluateMonitorPricing,
  getMonitorModelPricing,
  reviewMonitorPricing,
  type MonitorPricingConfig,
  type MonitorPricingReview,
  type MonitorPricingScenario,
  type MonitorScenarioResults,
} from './price-monitor-pricing-api'
import {
  LEGACY_TOKEN_FIELDS,
  TOKEN_PRICE_VARIABLES,
  applyBatchTokenPrice,
  boundaryPricingScenarios,
  buildPricingChange,
  expressionPriceFields,
  legacyTokenPrice,
  parsePriceDraft,
  pricingDraftFingerprint,
  scalePricingDrafts,
  type MonitorPricingChange,
  type MonitorPricingEntry,
  type PricingValues,
  type TokenPriceVariable,
} from './price-monitor-pricing-draft'
import {
  basicPricingScenario,
  pricingRiskRequired,
  pricingScenarioForm,
  type BasicScenarioForm,
} from './price-monitor-pricing-workflow'
import { PriceMonitorRequestFields } from './price-monitor-request-fields'
import {
  hydrateRequestContext,
  serializeRequestContext,
  RequestFieldError,
} from './price-monitor-request-fields-draft'
import { TieredPricingEditor } from './tiered-pricing-editor'

const PRICE_LABELS: Record<TokenPriceVariable, string> = {
  p: 'Input price',
  c: 'Completion price',
  cr: 'Cache read price',
  cc: 'Cache write price',
  cc1h: 'Cache Creation (1h)',
  img: 'Image input price',
  img_cr: 'Image cache read price',
  img_o: 'Image output price',
  ai: 'Audio input price',
  ao: 'Audio output price',
}
type Props = {
  open: boolean
  models: string[]
  checkedAt: number
  items: PriceMonitorMatrixItem[]
  sources: PriceMonitorSourceHeader[]
  onOpenChange: (open: boolean) => void
  onSaved: (models: string[], refreshed: boolean) => void
}

function needsConversion(
  entry: MonitorPricingEntry,
  draft: PricingValues | undefined
): boolean {
  return (
    entry.effective['billing_setting.billing_mode'] !== 'tiered_expr' &&
    draft?.['billing_setting.billing_mode'] !== 'tiered_expr' &&
    (entry.effective.ModelRatio !== undefined ||
      entry.effective.ModelPrice !== undefined)
  )
}

type SummaryPrices = {
  input?: number
  output?: number
  fixed?: number
  fixedUnit?: 'request' | 'image'
}

function effectiveSummaryPrices(
  entry: MonitorPricingEntry,
  factor: number
): SummaryPrices {
  const effective = entry.effective
  if (effective['billing_setting.billing_mode'] === 'tiered_expr') {
    const branch = expressionPriceFields(
      String(effective['billing_setting.billing_expr'] ?? '')
    )?.[0]
    if (branch?.unit !== undefined && branch.price !== undefined) {
      return { fixed: Number(branch.price), fixedUnit: branch.unit }
    }
    if (branch) {
      return {
        input:
          branch.prices.p === undefined ? undefined : Number(branch.prices.p),
        output:
          branch.prices.c === undefined ? undefined : Number(branch.prices.c),
      }
    }
    return {}
  }
  if (effective.ModelPrice !== undefined) {
    return { fixed: Number(effective.ModelPrice), fixedUnit: 'request' }
  }
  if (effective.ModelRatio !== undefined) {
    return {
      input: legacyTokenPrice(entry, effective, 'ModelRatio', factor),
      output: legacyTokenPrice(entry, effective, 'CompletionRatio', factor),
    }
  }
  return {}
}

function sourceSummaryPrices(
  price: PriceMonitorPriceCell | undefined
): SummaryPrices {
  if (!price || price.dynamic || price.unavailable_reason) return {}
  if (price.mode === 'per_request') {
    return price.price === undefined
      ? {}
      : { fixed: price.price, fixedUnit: 'request' }
  }
  const tier = price.tiers?.[0]
  return {
    input: price.input ?? tier?.input,
    output: price.output ?? tier?.output,
  }
}

export function PriceMonitorPriceFormDialog(props: Props) {
  const { t } = useTranslation()
  const [working, setWorking] = useState(false)
  const [footerTarget, setFooterTarget] = useState<HTMLDivElement | null>(null)
  const [session, setSession] = useState(0)
  const [pinned, setPinned] = useState<MonitorPricingConfig | null>(null)
  const names = useMemo(() => [...props.models].sort(), [props.models])
  const query = useQuery({
    queryKey: ['price-monitor-model-pricing', ...names],
    queryFn: () => getMonitorModelPricing(names),
    enabled: props.open && names.length > 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  })
  useEffect(() => {
    if (!props.open) {
      setPinned(null)
      return
    }
    if (!pinned && query.data && !query.isFetching && !query.isError) {
      setPinned(query.data)
    }
  }, [props.open, pinned, query.data, query.isFetching, query.isError])
  return (
    <Dialog
      open={props.open}
      title={t('Change selected prices')}
      description={names.join(', ')}
      contentClassName='sm:max-w-5xl'
      contentHeight='70vh'
      footer={<div ref={setFooterTarget} className='w-full' />}
      footerClassName='border-t'
      onOpenChange={(open) => {
        if (!open && working) return
        props.onOpenChange(open)
      }}
    >
      {!pinned && !query.isError && <p role='status'>{t('Loading...')}</p>}
      {!pinned && query.isError && (
        <FieldGroup>
          <p role='alert'>{t('Failed to load model pricing')}</p>
          <Button variant='outline' onClick={() => void query.refetch()}>
            {t('Retry')}
          </Button>
        </FieldGroup>
      )}
      {pinned && (
        <PriceFormSession
          key={`${names.join(',')}:${session}`}
          {...props}
          initial={pinned}
          onWorking={setWorking}
          footerTarget={footerTarget}
          reload={async () => {
            const result = await query.refetch()
            if (result.error) throw result.error
            if (!result.data) throw new Error('Failed to load model pricing')
            setPinned(result.data)
            setSession((current) => current + 1)
          }}
        />
      )}
    </Dialog>
  )
}

function PriceFormSession(
  props: Props & {
    initial: MonitorPricingConfig
    onWorking: (value: boolean) => void
    footerTarget: HTMLDivElement | null
    reload: () => Promise<void>
  }
) {
  const { t } = useTranslation()
  const id = useId()
  // A background query refresh never replaces an open editable session.
  const [config] = useState(() => structuredClone(props.initial))
  const entries = config.entries
  const [active, setActive] = useState(entries[0]?.model_name ?? '')
  const [drafts, setDrafts] = useState<Record<string, PricingValues>>(() =>
    Object.fromEntries(
      entries.map((entry) => [
        entry.model_name,
        structuredClone(entry.configured),
      ])
    )
  )
  const [resetModels, setResetModels] = useState<Set<string>>(new Set())
  const [working, setWorking] = useState(false)
  const [sourceKey, setSourceKey] = useState(
    props.sources.find((source) => source.type === 'official')?.key ??
      props.sources.find((source) => source.type !== 'platform')?.key ??
      ''
  )
  const [batchKind, setBatchKind] = useState('set')
  const [batchVariable, setBatchVariable] = useState<
    TokenPriceVariable | 'fixed'
  >('p')
  const [batchAmount, setBatchAmount] = useState('')
  const [batchProblems, setBatchProblems] = useState<
    { model: string; message: string; source?: 'conversion' }[]
  >([])
  const [scenarioForm, setScenarioForm] = useState<BasicScenarioForm>(() => ({
    input: '1000',
    output: '100',
    cache: '0',
    semantics: 'openai',
    at: new Date().toISOString(),
  }))
  const [requestContext, setRequestContext] = useState(() =>
    hydrateRequestContext({}, {})
  )
  const [examples, setExamples] = useState<MonitorPricingScenario[]>([])
  const [moreUsage, setMoreUsage] = useState(false)
  const [scenarioResult, setScenarioResult] = useState<{
    fingerprint: string
    data: MonitorScenarioResults
  } | null>(null)
  const [risk, setRisk] = useState<{
    data: MonitorPricingReview
    changes: MonitorPricingChange[]
    fingerprint: string
    revision: number
  } | null>(null)
  const [acceptLoss, setAcceptLoss] = useState(false)
  const [acceptUnknown, setAcceptUnknown] = useState(false)
  const [saveFailed, setSaveFailed] = useState(false)
  const [editorRevision, setEditorRevision] = useState(0)
  const [editorInvalid, setEditorInvalid] = useState(false)
  const [editorUnfinished, setEditorUnfinished] = useState(false)
  const [conversionPending, setConversionPending] = useState<
    Record<string, true>
  >({})
  const operationRef = useRef(false)
  const revisionRef = useRef(0)
  const activeRef = useRef(active)
  activeRef.current = active
  const draftsRef = useRef(drafts)
  draftsRef.current = drafts
  const entry = entries.find((candidate) => candidate.model_name === active)
  const draft = drafts[active] ?? {}
  const expression = String(
    draft['billing_setting.billing_expr'] ??
      entry?.effective['billing_setting.billing_expr'] ??
      ''
  )
  const expressionFields = expressionPriceFields(expression)
  const allChanges = useMemo(
    () =>
      entries.map((model) => ({
        ...buildPricingChange(model, drafts[model.model_name] ?? {}),
        ...(resetModels.has(model.model_name) ? { reset: true } : {}),
      })),
    [entries, drafts, resetModels]
  )
  const changes = useMemo(
    () =>
      // Unchanged drafts are never submitted: saving is a full-model replace,
      // so only actual edits and resets go in. Batch adjustments still operate
      // on every selected model (allChanges).
      allChanges.filter((change) => {
        if (change.reset) return true
        const model = entries.find(
          (candidate) => candidate.model_name === change.model_name
        )
        if (!model) return false
        return (
          pricingDraftFingerprint([change]) !==
          pricingDraftFingerprint([buildPricingChange(model, model.configured)])
        )
      }),
    [allChanges, entries]
  )
  const fingerprint = pricingDraftFingerprint(changes)
  const scenarioFingerprint = JSON.stringify([
    active,
    fingerprint,
    scenarioForm,
    requestContext,
    examples,
    sourceKey,
  ])
  const source = props.sources.find((candidate) => candidate.key === sourceKey)
  const sourcePrice = props.items.find(
    (candidate) => candidate.model === active
  )?.prices[sourceKey]
  const invalid =
    editorInvalid ||
    entries.some((model) => {
      const values = drafts[model.model_name] ?? {}
      return (
        (values['billing_setting.billing_mode'] ??
          model.effective['billing_setting.billing_mode']) === 'tiered_expr' &&
        !String(
          values['billing_setting.billing_expr'] ??
            model.effective['billing_setting.billing_expr'] ??
            ''
        ).trim()
      )
    })
  const conversionProblems = batchProblems.filter(
    (problem) => problem.source === 'conversion'
  )
  const activeProblem = conversionProblems.find(
    (problem) => problem.model === active
  )
  const riskFlags = risk
    ? pricingRiskRequired(risk.data)
    : { loss: false, unknown: false }
  const restoringDefaults = resetModels.has(active)

  const errorMessage = (error: unknown, fallback: string) => {
    let message = t(fallback)
    if (error instanceof Error && error.message) message = t(error.message)
    if (error instanceof RequestFieldError) message += ` · ${error.path}`
    toast.error(message)
  }
  const markDirty = () => {
    revisionRef.current++
    setRisk(null)
    setAcceptLoss(false)
    setAcceptUnknown(false)
  }
  const update = (name: string, values: PricingValues) => {
    markDirty()
    setDrafts((current) => ({ ...current, [name]: values }))
    setResetModels((current) => {
      const next = new Set(current)
      next.delete(name)
      return next
    })
  }
  const begin = () => {
    if (operationRef.current) return false
    operationRef.current = true
    setWorking(true)
    props.onWorking(true)
    return true
  }
  const end = () => {
    operationRef.current = false
    setWorking(false)
    props.onWorking(false)
  }
  const draftFingerprint = (name: string, pricing: PricingValues) =>
    pricingDraftFingerprint([
      { model_name: name, expected_version: '', pricing },
    ])
  // Legacy entries convert to expression drafts silently; failures and
  // unsupported models are recorded as problems instead.
  const convertEntrySilently = async (model: MonitorPricingEntry) => {
    const requestDraft = drafts[model.model_name] ?? {}
    const recordProblem = (message: string) =>
      setBatchProblems((current) => [
        ...current.filter(
          (problem) =>
            !(
              problem.model === model.model_name &&
              problem.source === 'conversion'
            )
        ),
        { model: model.model_name, message, source: 'conversion' as const },
      ])
    setConversionPending((current) => ({
      ...current,
      [model.model_name]: true,
    }))
    try {
      const result = await convertMonitorPricing(model.model_name, requestDraft)
      if (result.unsupported_reason) {
        recordProblem(result.unsupported_reason)
        return
      }
      if (!result.expression) {
        throw new Error('Failed to prepare pricing conversion')
      }
      const converted = result.expression
      const requestFingerprint = draftFingerprint(
        model.model_name,
        requestDraft
      )
      const currentDraft = draftsRef.current[model.model_name] ?? {}
      if (
        draftFingerprint(model.model_name, currentDraft) !== requestFingerprint
      ) {
        recordProblem(
          'Prices changed while preparing the conversion. Try again.'
        )
        return
      }
      markDirty()
      setDrafts((current) => ({
        ...current,
        [model.model_name]: {
          ...currentDraft,
          'billing_setting.billing_mode': 'tiered_expr',
          'billing_setting.billing_expr': converted,
        },
      }))
      setBatchProblems((current) =>
        current.filter(
          (problem) =>
            !(
              problem.model === model.model_name &&
              problem.source === 'conversion'
            )
        )
      )
      if (activeRef.current === model.model_name) {
        setEditorRevision((current) => current + 1)
      }
    } catch (error) {
      recordProblem(
        error instanceof Error && error.message
          ? error.message
          : 'Failed to prepare pricing conversion'
      )
    } finally {
      setConversionPending((current) => {
        const next = { ...current }
        delete next[model.model_name]
        return next
      })
    }
  }
  // Convert every legacy entry once when the session opens, five at a time.
  useEffect(() => {
    const legacy = entries.filter((model) =>
      needsConversion(model, drafts[model.model_name])
    )
    if (!legacy.length) return
    let cancelled = false
    const run = async () => {
      for (let index = 0; index < legacy.length; index += 5) {
        if (cancelled) return
        await Promise.all(
          legacy
            .slice(index, index + 5)
            .map((model) => convertEntrySilently(model))
        )
      }
    }
    void run()
    return () => {
      cancelled = true
    }
    // Session init only; drafts deliberately are not a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const persist = async (
    capture: {
      changes: MonitorPricingChange[]
      data: MonitorPricingReview
      revision: number
    },
    loss: boolean,
    unknown: boolean
  ) => {
    if (capture.revision !== revisionRef.current) {
      setRisk(null)
      return
    }
    let response: { updated_models: string[]; refreshed: boolean }
    try {
      response = await applyMonitorPricing({
        checked_at: props.checkedAt,
        context_version: capture.data.context_version,
        changes: capture.changes,
        force_below_floor: loss,
        acknowledge_unknown: unknown,
      })
    } catch (error) {
      setSaveFailed(true)
      setRisk(null)
      errorMessage(error, 'Failed to save model pricing')
      return
    }
    toast.success(
      response.refreshed
        ? t('Model pricing saved')
        : t('Pricing saved; monitor results are waiting to refresh')
    )
    props.onOpenChange(false)
    try {
      await props.onSaved(response.updated_models, response.refreshed)
    } catch {
      toast.info(t('Pricing saved; monitor results are waiting to refresh'))
    }
  }
  const save = async () => {
    if (
      invalid ||
      conversionProblems.length > 0 ||
      !changes.length ||
      !begin()
    ) {
      return
    }
    const revision = revisionRef.current
    try {
      const capturedChanges = structuredClone(changes)
      const capturedFingerprint = pricingDraftFingerprint(capturedChanges)
      const data = await reviewMonitorPricing(props.checkedAt, capturedChanges)
      if (revision !== revisionRef.current) return
      const flags = pricingRiskRequired(data)
      if (flags.loss || flags.unknown) {
        setRisk({
          data,
          changes: capturedChanges,
          fingerprint: capturedFingerprint,
          revision,
        })
        setAcceptLoss(false)
        setAcceptUnknown(false)
      } else {
        await persist(
          { data, changes: capturedChanges, revision },
          false,
          false
        )
      }
    } catch (error) {
      errorMessage(error, 'Failed to prepare pricing changes')
    } finally {
      end()
    }
  }
  const runScenario = async () => {
    if (!entry || !begin()) return
    const capture = scenarioFingerprint
    const revision = revisionRef.current
    try {
      const candidate = changes.find((change) => change.model_name === active)
      const scenario = {
        ...basicPricingScenario(scenarioForm),
        ...serializeRequestContext(requestContext),
      }
      const scenarios = [scenario, ...examples]
      if (scenarios.length > 32) {
        throw new Error('Enter between 1 and 32 pricing scenarios')
      }
      const result = await evaluateMonitorPricing({
        model_name: active,
        expected_version: entry.version,
        pricing: candidate?.pricing ?? draft,
        source_keys: sourceKey ? [sourceKey] : [],
        scenarios,
      })
      if (revision === revisionRef.current) {
        setScenarioResult({ fingerprint: capture, data: result })
      }
    } catch (error) {
      errorMessage(error, 'Failed to evaluate pricing scenarios')
    } finally {
      end()
    }
  }

  if (!entry) return <p role='alert'>{t('Failed to load model pricing')}</p>
  const allPriceItems: {
    value: TokenPriceVariable | 'fixed'
    label: string
  }[] = [
    { value: 'fixed', label: t('Price per request') },
    ...TOKEN_PRICE_VARIABLES.map((variable) => ({
      value: variable as TokenPriceVariable | 'fixed',
      label: t(PRICE_LABELS[variable]),
    })),
  ]
  const formatAmount = (value: number | string | undefined) =>
    value === undefined || !Number.isFinite(Number(value))
      ? t('Not separately priced')
      : `$${Number(value).toLocaleString(undefined, { maximumFractionDigits: 8 })}`
  const riskPriceLabel = (field: string) => {
    if (field === 'model_price') return t('Fixed price')
    const variable = field.split(':').at(-1) as TokenPriceVariable
    const priceLabel = t(PRICE_LABELS[variable] ?? 'Price')
    const start = field.split(':')[0]
    return start && /^\d+$/.test(start) && Number(start) > 0
      ? `${priceLabel} · ${t('Input length from {{count}} tokens', { count: Number(start).toLocaleString() })}`
      : priceLabel
  }
  const summaryText = (prices: SummaryPrices) => {
    if (prices.fixed !== undefined) {
      return `${t(prices.fixedUnit === 'image' ? 'Price per image' : 'Price per request')} ${formatAmount(prices.fixed)}`
    }
    return [
      prices.input === undefined
        ? undefined
        : `${t('Input price')} ${formatAmount(prices.input)}`,
      prices.output === undefined
        ? undefined
        : `${t('Completion price')} ${formatAmount(prices.output)}`,
    ]
      .filter((part): part is string => Boolean(part))
      .join(' · ')
  }
  const currentSummary = summaryText(
    effectiveSummaryPrices(entry, config.token_price_factor)
  )
  const referenceSummary = summaryText(sourceSummaryPrices(sourcePrice))
  const editorSplit = splitBillingExprAndRequestRules(expression)
  const writeEditorExpression = (next: string) => {
    update(active, {
      ...draft,
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr': next,
    })
  }
  let editorArea: React.JSX.Element
  if (activeProblem) {
    editorArea = (
      <p role='alert' className='text-destructive text-sm'>
        {t(activeProblem.message)}
      </p>
    )
  } else if (conversionPending[active]) {
    editorArea = (
      <p role='status' className='text-muted-foreground text-sm'>
        {t('Preparing conversion...')}
      </p>
    )
  } else {
    editorArea = (
      <fieldset disabled={working} className='min-w-0'>
        <TieredPricingEditor
          key={`${active}:${editorRevision}`}
          modelName={active}
          billingExpr={editorSplit.billingExpr}
          requestRuleExpr={editorSplit.requestRuleExpr}
          hidePresets
          hideEstimator
          pricingOnly
          onInvalidChange={setEditorInvalid}
          onUnfinishedChange={setEditorUnfinished}
          onBillingExprChange={(next) =>
            writeEditorExpression(
              combineBillingExpr(next, editorSplit.requestRuleExpr)
            )
          }
          onRequestRuleExprChange={(next) =>
            writeEditorExpression(
              combineBillingExpr(editorSplit.billingExpr, next)
            )
          }
        />
      </fieldset>
    )
  }
  let batchAmountPlaceholder = '%'
  if (batchKind === 'set') {
    batchAmountPlaceholder =
      batchVariable === 'fixed'
        ? t('USD per request')
        : t('USD per million tokens')
  }
  let batchProblemLabel = t('All existing prices')
  if (batchKind === 'set') {
    batchProblemLabel =
      batchVariable === 'fixed'
        ? t('Price per request')
        : t(PRICE_LABELS[batchVariable])
  }
  return (
    <div className='space-y-5'>
      {entries.length > 1 && (
        <div className='flex flex-wrap gap-2'>
          {entries.map((model) => (
            <Button
              key={model.model_name}
              size='sm'
              variant={active === model.model_name ? 'default' : 'outline'}
              disabled={working || editorUnfinished}
              onClick={() => setActive(model.model_name)}
            >
              {model.model_name}
              {conversionProblems.some(
                (problem) => problem.model === model.model_name
              )
                ? ' •'
                : ''}
            </Button>
          ))}
        </div>
      )}
      {conversionProblems.length > 0 && (
        <Alert variant='destructive'>
          <AlertTriangle data-icon='inline-start' />
          <AlertDescription>
            <div className='flex flex-col gap-1'>
              {conversionProblems.map((problem) => (
                <span key={problem.model}>
                  {problem.model}: {t(problem.message)}
                </span>
              ))}
            </div>
          </AlertDescription>
        </Alert>
      )}
      <details>
        <summary className='text-muted-foreground cursor-pointer text-sm'>
          {t('Reference price')}
          {referenceSummary && `: ${referenceSummary}`}
        </summary>
        <FieldGroup className='mt-3 gap-3'>
          <Field>
            <FieldLabel htmlFor={`${id}-source`}>
              {t('Reference source')}
            </FieldLabel>
            <Select
              items={[
                { value: '', label: t('No reference source') },
                ...props.sources
                  .filter((candidate) => candidate.type !== 'platform')
                  .map((candidate) => ({
                    value: candidate.key,
                    label: candidate.name,
                  })),
              ]}
              value={sourceKey}
              onValueChange={(value) => setSourceKey(value ?? '')}
              disabled={working}
            >
              <SelectTrigger id={`${id}-source`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value=''>{t('No reference source')}</SelectItem>
                {props.sources
                  .filter((candidate) => candidate.type !== 'platform')
                  .map((candidate) => (
                    <SelectItem key={candidate.key} value={candidate.key}>
                      {candidate.name}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
            {(currentSummary || referenceSummary) && (
              <p className='text-muted-foreground text-xs'>
                {currentSummary && `${t('Current price')}: ${currentSummary}`}
                {currentSummary && referenceSummary && ' ｜ '}
                {referenceSummary &&
                  `${t('Reference price')}: ${referenceSummary}`}
              </p>
            )}
          </Field>
        </FieldGroup>
      </details>
      {restoringDefaults && (
        <div className='space-y-2'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Default pricing will replace these prices when saved. The fields below show the previous configuration.'
            )}
          </p>
          <Button
            variant='outline'
            size='sm'
            disabled={working}
            onClick={() => update(active, structuredClone(entry.configured))}
          >
            {t('Keep current pricing')}
          </Button>
        </div>
      )}
      {editorArea}
      {entries.length > 1 && (
        <FieldGroup>
          <Field>
            <FieldLabel>{t('Batch price adjustment')}</FieldLabel>
            <ToggleGroup
              value={[batchKind]}
              disabled={working}
              onValueChange={(values) => {
                if (values[0]) setBatchKind(values[0])
              }}
            >
              <ToggleGroupItem value='set'>
                {t('Set one price')}
              </ToggleGroupItem>
              <ToggleGroupItem value='increase'>
                {t('Increase by percent')}
              </ToggleGroupItem>
              <ToggleGroupItem value='decrease'>
                {t('Decrease by percent')}
              </ToggleGroupItem>
            </ToggleGroup>
            {batchKind === 'set' && (
              <Select
                items={allPriceItems}
                value={batchVariable}
                onValueChange={(value) => {
                  if (value) {
                    setBatchVariable(value as TokenPriceVariable | 'fixed')
                  }
                }}
                disabled={working}
              >
                <SelectTrigger aria-label={t('Price item')}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {allPriceItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <Input
              aria-label={
                batchKind === 'set' ? t('New price') : t('Percentage')
              }
              value={batchAmount}
              disabled={working}
              placeholder={batchAmountPlaceholder}
              onChange={(event) => setBatchAmount(event.target.value)}
            />
            <Button
              variant='outline'
              disabled={working || invalid}
              onClick={async () => {
                if (!begin()) return
                try {
                  const amount = parsePriceDraft(batchAmount)
                  if (amount === undefined) {
                    throw new Error('Enter a valid price')
                  }
                  const problems: { model: string; message: string }[] = []
                  const next: MonitorPricingChange[] = []
                  for (const change of allChanges) {
                    try {
                      const adjusted =
                        batchKind === 'set'
                          ? applyBatchTokenPrice(
                              [change],
                              entries,
                              batchVariable,
                              batchAmount,
                              config.token_price_factor
                            )
                          : scalePricingDrafts(
                              [change],
                              entries,
                              String(
                                batchKind === 'decrease' ? -amount : amount
                              )
                            )
                      const candidate = adjusted[0]
                      if (candidate) next.push(candidate)
                    } catch (error) {
                      problems.push({
                        model: change.model_name,
                        message:
                          error instanceof Error
                            ? error.message
                            : 'Enter a valid price',
                      })
                    }
                  }
                  setBatchProblems((current) => [
                    ...current.filter(
                      (problem) => problem.source === 'conversion'
                    ),
                    ...problems,
                  ])
                  if (problems.length) return
                  markDirty()
                  setDrafts(
                    Object.fromEntries(
                      next.map((change) => [change.model_name, change.pricing])
                    )
                  )
                  setResetModels(new Set())
                  setEditorRevision((current) => current + 1)
                } catch (error) {
                  errorMessage(
                    error,
                    'Could not adjust the selected prices. Check each model and price item.'
                  )
                } finally {
                  end()
                }
              }}
            >
              {t('Apply adjustment')}
            </Button>
            {batchProblems
              .filter((problem) => problem.source !== 'conversion')
              .map((problem) => (
                <p key={problem.model} className='text-destructive text-xs'>
                  {problem.model} · {batchProblemLabel}: {t(problem.message)}
                </p>
              ))}
            <FieldDescription>
              {t(
                'This changes every selected model and applicable tier. Check the complete changes before saving.'
              )}
            </FieldDescription>
          </Field>
        </FieldGroup>
      )}
      <details>
        <summary className='cursor-pointer text-sm font-medium'>
          {t('Try a usage example')}
        </summary>
        <FieldGroup className='mt-4'>
          <div className='grid gap-3 sm:grid-cols-3'>
            {(
              [
                'input',
                'output',
                'cache',
                'cacheWrite',
                'cacheWrite1h',
                'imageInput',
                'imageOutput',
                'audioInput',
                'audioOutput',
              ] as const
            )
              .filter(
                (field) =>
                  moreUsage || ['input', 'output', 'cache'].includes(field)
              )
              .map((field) => (
                <Field key={field}>
                  <FieldLabel htmlFor={`${id}-scenario-${field}`}>
                    {t(
                      {
                        input: 'Input tokens',
                        output: 'Output tokens',
                        cache: 'Cached tokens',
                        cacheWrite: 'Cache creation tokens',
                        cacheWrite1h: 'One-hour cache creation tokens',
                        imageInput: 'Image input tokens',
                        imageOutput: 'Image output tokens',
                        audioInput: 'Audio input tokens',
                        audioOutput: 'Audio output tokens',
                      }[field]
                    )}
                  </FieldLabel>
                  <Input
                    id={`${id}-scenario-${field}`}
                    inputMode='numeric'
                    value={scenarioForm[field] ?? '0'}
                    disabled={working}
                    onChange={(event) =>
                      setScenarioForm((current) => ({
                        ...current,
                        [field]: event.target.value,
                      }))
                    }
                  />
                </Field>
              ))}
          </div>
          <Button
            variant='outline'
            disabled={working}
            onClick={() => setMoreUsage((value) => !value)}
          >
            {moreUsage
              ? t('Hide additional usage fields')
              : t('Add cache creation, image or audio usage')}
          </Button>
          <FieldDescription>
            {t(
              'Input tokens include cache creation and cache reads. The selected usage format is converted automatically.'
            )}
          </FieldDescription>
          <Field>
            <FieldLabel>{t('Usage format')}</FieldLabel>
            <ToggleGroup
              value={[scenarioForm.semantics]}
              disabled={working}
              onValueChange={(values) => {
                const value = values[0]
                if (value === 'openai' || value === 'claude') {
                  setScenarioForm((current) => ({
                    ...current,
                    semantics: value,
                  }))
                }
              }}
            >
              <ToggleGroupItem value='openai'>OpenAI</ToggleGroupItem>
              <ToggleGroupItem value='claude'>Claude</ToggleGroupItem>
            </ToggleGroup>
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-at`}>{t('Simulation time')}</FieldLabel>
            <Input
              id={`${id}-at`}
              type='datetime-local'
              value={
                Number.isFinite(Date.parse(scenarioForm.at))
                  ? new Date(
                      Date.parse(scenarioForm.at) -
                        new Date(scenarioForm.at).getTimezoneOffset() * 60000
                    )
                      .toISOString()
                      .slice(0, 16)
                  : ''
              }
              disabled={working}
              onChange={(event) =>
                setScenarioForm((current) => ({
                  ...current,
                  at:
                    event.target.value &&
                    Number.isFinite(Date.parse(event.target.value))
                      ? new Date(event.target.value).toISOString()
                      : '',
                }))
              }
            />
            <FieldDescription>
              {t('Time zone')}:{' '}
              {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </FieldDescription>
          </Field>
          <PriceMonitorRequestFields
            value={requestContext}
            onChange={setRequestContext}
            disabled={working}
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              disabled={working || examples.length >= 31}
              onClick={() => {
                try {
                  const scenario = {
                    ...basicPricingScenario(scenarioForm),
                    ...serializeRequestContext(requestContext),
                    id: crypto.randomUUID(),
                  }
                  setExamples((current) => [...current, scenario])
                } catch (error) {
                  errorMessage(error, 'Check the example fields and try again')
                }
              }}
            >
              {t('Keep this example')}
            </Button>
            <Button
              variant='outline'
              disabled={working || !expressionFields}
              onClick={() => {
                try {
                  setExamples(
                    boundaryPricingScenarios(expression, scenarioForm.at).slice(
                      0,
                      31
                    )
                  )
                } catch (error) {
                  errorMessage(error, 'Failed to evaluate pricing scenarios')
                }
              }}
            >
              {t('Generate boundary and cache scenarios')}
            </Button>
          </div>
          {examples.map((example, index) => (
            <div
              key={example.id}
              className='flex items-center justify-between gap-2 text-sm'
            >
              <span>
                {t('Example {{number}}', { number: index + 2 })} ·{' '}
                {t('Input tokens')}: {pricingScenarioForm(example).input} ·{' '}
                {t('Output tokens')}: {String(example.usage.completion_tokens)}
              </span>
              <Button
                size='sm'
                variant='outline'
                disabled={working}
                onClick={() => {
                  setScenarioForm(pricingScenarioForm(example))
                  setRequestContext(
                    hydrateRequestContext(example.request_body, example.headers)
                  )
                  setMoreUsage(true)
                  setExamples((current) =>
                    current.filter((_, i) => i !== index)
                  )
                }}
              >
                {t('Edit')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={working}
                onClick={() =>
                  setExamples((current) =>
                    current.filter((_, i) => i !== index)
                  )
                }
              >
                {t('Remove')}
              </Button>
            </div>
          ))}
          <Button
            variant='outline'
            disabled={working || invalid || restoringDefaults}
            onClick={() => void runScenario()}
          >
            {t('Calculate example cost')}
          </Button>
          <FieldDescription>
            {t('Sample results do not prove every request is profitable.')}
          </FieldDescription>
          {scenarioResult?.fingerprint === scenarioFingerprint && (
            <div className='space-y-3 rounded-md border p-3'>
              {scenarioResult.data.results.map((result, index) => {
                const before = result.sides.find(
                  (side) => side.source_key === 'platform'
                )?.amount_usd
                const after = result.sides.find(
                  (side) => side.source_key === 'draft'
                )?.amount_usd
                return (
                  <div key={result.scenario_id}>
                    <p className='font-medium'>
                      {t('Example {{number}}', { number: index + 1 })}
                    </p>
                    <p>
                      {t('Current cost')}{' '}
                      {before === undefined
                        ? t('Not evaluated')
                        : formatAmount(before)}{' '}
                      → {t('New cost')}{' '}
                      {after === undefined
                        ? t('Not evaluated')
                        : formatAmount(after)}
                    </p>
                    {(before === undefined || after === undefined) && (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Check the source price and request context for this scenario'
                        )}
                      </p>
                    )}
                    {before !== undefined && after !== undefined && (
                      <p className='text-muted-foreground text-xs'>
                        {t('Cost change')}: {formatAmount(after - before)}
                        {before > 0
                          ? ` (${((after / before - 1) * 100).toFixed(2)}%)`
                          : ''}
                      </p>
                    )}
                    {result.sides
                      .filter(
                        (side) =>
                          side.source_key !== 'platform' &&
                          side.source_key !== 'draft'
                      )
                      .map((side) => (
                        <p key={side.source_key}>
                          {source?.name ?? t('Reference price')}:{' '}
                          {side.amount_usd === undefined
                            ? t('Not evaluated')
                            : formatAmount(side.amount_usd)}
                          {side.reason
                            ? ` · ${t('Check the source price and request context for this scenario')}`
                            : ''}
                        </p>
                      ))}
                  </div>
                )
              })}
            </div>
          )}
        </FieldGroup>
      </details>
      <ChangeSummary
        entries={entries}
        changes={changes}
        factor={config.token_price_factor}
      />
      {saveFailed && (
        <FieldGroup>
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Save failed. Your changes are kept. Reload current prices if another administrator changed them.'
            )}
          </p>
          <Button
            variant='outline'
            disabled={working}
            onClick={async () => {
              if (!begin()) {
                return
              }
              try {
                await props.reload()
              } catch {
                toast.error(t('Failed to load model pricing'))
              } finally {
                end()
              }
            }}
          >
            {t('Reload current prices')}
          </Button>
        </FieldGroup>
      )}
      {props.footerTarget &&
        createPortal(
          <div className='flex flex-col gap-2'>
            {editorUnfinished && (
              <p className='text-muted-foreground text-xs' role='status'>
                {t('Complete this tier before switching models or saving.')}
              </p>
            )}
            <div className='flex flex-wrap justify-end gap-2'>
              <Button
                variant='outline'
                disabled={working}
                onClick={() => {
                  if (
                    !window.confirm(
                      t('Restore default pricing for this model?')
                    )
                  ) {
                    return
                  }
                  update(active, {})
                  setResetModels((current) => new Set([...current, active]))
                  setBatchProblems((current) =>
                    current.filter(
                      (problem) =>
                        !(
                          problem.model === active &&
                          problem.source === 'conversion'
                        )
                    )
                  )
                  setEditorRevision((current) => current + 1)
                }}
              >
                {t('Restore default pricing')}
              </Button>
              <Button
                variant='outline'
                disabled={working}
                onClick={() => {
                  props.onOpenChange(false)
                }}
              >
                {t('Cancel')}
              </Button>
              <Button
                disabled={
                  working ||
                  invalid ||
                  conversionProblems.length > 0 ||
                  !changes.length
                }
                onClick={() => void save()}
              >
                {working ? t('Saving...') : t('Save changes')}
              </Button>
            </div>
          </div>,
          props.footerTarget
        )}
      <Dialog
        open={Boolean(risk)}
        title={t('Confirm pricing risks')}
        onOpenChange={(open) => {
          if (!open && !working) setRisk(null)
        }}
        footer={
          <>
            <Button
              variant='outline'
              disabled={working}
              onClick={() => setRisk(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                working ||
                (riskFlags.loss && !acceptLoss) ||
                (riskFlags.unknown && !acceptUnknown)
              }
              onClick={async () => {
                if (!risk || risk.fingerprint !== fingerprint || !begin()) {
                  return
                }
                try {
                  await persist(risk, acceptLoss, acceptUnknown)
                } finally {
                  end()
                }
              }}
            >
              {working ? t('Saving...') : t('Confirm and save')}
            </Button>
          </>
        }
      >
        <FieldGroup>
          {risk?.data.models.map((model) => (
            <div
              key={model.model_name}
              className='rounded-md border p-3 text-sm'
            >
              <p className='font-medium'>{model.model_name}</p>
              {model.violations.map((violation) => (
                <p
                  key={`${violation.field}:${violation.binding ?? ''}`}
                  className='text-destructive'
                >
                  {riskPriceLabel(violation.field)}:{' '}
                  {formatAmount(violation.value)} &lt;{' '}
                  {formatAmount(violation.floor)}{' '}
                  {
                    props.sources.find(
                      (candidate) => candidate.key === violation.binding
                    )?.name
                  }
                </p>
              ))}
              {model.coverage.unknown > 0 && (
                <p className='text-muted-foreground'>
                  {t(
                    '{{count}} pricing scopes could not be verified. Sources, token counting or custom rules may differ.',
                    { count: model.coverage.unknown }
                  )}
                </p>
              )}
            </div>
          ))}
          {riskFlags.loss && (
            <Field orientation='horizontal'>
              <Checkbox
                id={`${id}-accept-loss`}
                checked={acceptLoss}
                disabled={working}
                onCheckedChange={(checked) => setAcceptLoss(checked === true)}
              />
              <FieldLabel htmlFor={`${id}-accept-loss`}>
                {t('I accept the prices below the known cost floor')}
              </FieldLabel>
            </Field>
          )}
          {riskFlags.unknown && (
            <Field orientation='horizontal'>
              <Checkbox
                id={`${id}-accept-unknown`}
                checked={acceptUnknown}
                disabled={working}
                onCheckedChange={(checked) =>
                  setAcceptUnknown(checked === true)
                }
              />
              <FieldLabel htmlFor={`${id}-accept-unknown`}>
                {t('I understand that uncovered pricing may still lose money')}
              </FieldLabel>
            </Field>
          )}
        </FieldGroup>
      </Dialog>
    </div>
  )
}

function ChangeSummary(props: {
  entries: MonitorPricingConfig['entries']
  changes: MonitorPricingChange[]
  factor: number
}) {
  const { t } = useTranslation()
  if (!props.changes.length) return null
  return (
    <details open>
      <summary className='cursor-pointer text-sm font-medium'>
        {t('Price changes')}
      </summary>
      <div className='mt-3 space-y-3'>
        {props.changes.map((change) => {
          const entry = props.entries.find(
            (candidate) => candidate.model_name === change.model_name
          )
          if (!entry) return null
          if (change.reset) {
            return (
              <p key={change.model_name}>
                {change.model_name} · {t('Restore default pricing')}
              </p>
            )
          }
          const before = expressionPriceFields(
            String(entry.effective['billing_setting.billing_expr'] ?? '')
          )
          const after = expressionPriceFields(
            String(
              change.pricing['billing_setting.billing_expr'] ??
                entry.effective['billing_setting.billing_expr'] ??
                ''
            )
          )
          const rows: {
            key: string
            label: string
            before?: number
            after?: number
          }[] = []
          if (
            after &&
            (change.pricing['billing_setting.billing_mode'] ??
              entry.effective['billing_setting.billing_mode']) === 'tiered_expr'
          ) {
            // A converted legacy model has no effective expression: its flat
            // legacy prices are the "before" side of the catch-all tier. Only
            // configured fields count — effective fills inherited defaults
            // that the expression inherits the same way, so they are not changes.
            const legacyFlat = before === null
            const legacyVariablePrice = (variable: TokenPriceVariable) => {
              const field = LEGACY_TOKEN_FIELDS.find(
                ([, item]) => item === variable
              )?.[0]
              if (!field || entry.configured[field] === undefined) {
                return undefined
              }
              return legacyTokenPrice(
                entry,
                entry.configured,
                field,
                props.factor
              )
            }
            for (const tier of after) {
              const previousTier = before?.find(
                (candidate) =>
                  candidate.min <= tier.min &&
                  (candidate.max ?? Infinity) >= (tier.max ?? Infinity) &&
                  candidate.unit === tier.unit
              )
              // Splitting a range keeps its previous price. Merging ranges or
              // changing units needs scenario comparison, not a made-up old price.
              if (before && !previousTier) {
                rows.length = 0
                break
              }
              const catchAll = tier.min === 0 && tier.max === undefined
              if (tier.unit !== undefined) {
                const matched = previousTier?.price
                const legacyFixed =
                  legacyFlat &&
                  catchAll &&
                  entry.configured.ModelPrice !== undefined
                    ? String(Number(entry.configured.ModelPrice))
                    : undefined
                const old = matched ?? legacyFixed
                if (String(old) !== String(tier.price)) {
                  rows.push({
                    key: `${tier.key}:fixed`,
                    label: `${tier.min}–${tier.max ?? '∞'} · ${t(tier.unit === 'image' ? 'Price per image' : 'Price per request')}`,
                    before: old === undefined ? undefined : Number(old),
                    after:
                      tier.price === undefined ? undefined : Number(tier.price),
                  })
                }
                continue
              }
              for (const variable of TOKEN_PRICE_VARIABLES) {
                const matched = previousTier?.prices[variable]
                const legacyValue =
                  legacyFlat && catchAll
                    ? legacyVariablePrice(variable)
                    : undefined
                const old =
                  matched ??
                  (legacyValue === undefined ? undefined : String(legacyValue))
                const value = tier.prices[variable]
                if (String(old) !== String(value)) {
                  rows.push({
                    key: `${tier.key}:${variable}`,
                    label: `${tier.min}–${tier.max ?? '∞'} · ${t(PRICE_LABELS[variable])}`,
                    before: old === undefined ? undefined : Number(old),
                    after: value === undefined ? undefined : Number(value),
                  })
                }
              }
            }
          } else if (change.pricing.ModelPrice !== undefined) {
            if (
              Number(change.pricing.ModelPrice) !==
              Number(entry.effective.ModelPrice)
            ) {
              rows.push({
                key: 'fixed',
                label: t('Fixed price'),
                before: Number(entry.effective.ModelPrice),
                after: Number(change.pricing.ModelPrice),
              })
            }
          } else {
            for (const [field, variable] of LEGACY_TOKEN_FIELDS) {
              const old = legacyTokenPrice(
                entry,
                entry.configured,
                field,
                props.factor
              )
              const value = legacyTokenPrice(
                entry,
                change.pricing,
                field,
                props.factor
              )
              if (old !== value) {
                rows.push({
                  key: field,
                  label: t(PRICE_LABELS[variable]),
                  before: old,
                  after: value,
                })
              }
            }
          }
          if (
            !rows.length &&
            pricingDraftFingerprint([change]) !==
              pricingDraftFingerprint([
                buildPricingChange(entry, entry.configured),
              ])
          ) {
            const converted =
              (change.pricing['billing_setting.billing_mode'] ??
                entry.effective['billing_setting.billing_mode']) ===
                'tiered_expr' &&
              entry.effective['billing_setting.billing_mode'] !== 'tiered_expr'
            return (
              <p key={change.model_name}>
                {change.model_name} ·{' '}
                {converted
                  ? t(
                      'Converted to dynamic pricing; compare an example before saving.'
                    )
                  : t(
                      'Custom pricing rules changed; compare an example before saving.'
                    )}
              </p>
            )
          }
          if (!rows.length) return null
          return (
            <div key={change.model_name}>
              <p className='text-sm font-medium'>{change.model_name}</p>
              {rows.map((row) => (
                <p key={row.key} className='text-muted-foreground text-xs'>
                  {row.label}:{' '}
                  {row.before === undefined || !Number.isFinite(row.before)
                    ? t('Not separately priced')
                    : `$${row.before}`}{' '}
                  →{' '}
                  {row.after === undefined || !Number.isFinite(row.after)
                    ? t('Not separately priced')
                    : `$${row.after}`}
                </p>
              ))}
            </div>
          )
        })}
      </div>
    </details>
  )
}
