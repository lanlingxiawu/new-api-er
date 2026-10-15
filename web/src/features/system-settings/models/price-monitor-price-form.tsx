/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type {
  PriceMonitorMatrixItem,
  PriceMonitorPriceCell,
  PriceMonitorPriceTier,
  PriceMonitorSourceHeader,
} from '../types'
import {
  applyMonitorPricing,
  convertMonitorPricing,
  describeMonitorPricing,
  evaluateMonitorPricing,
  getMonitorModelPricing,
  reviewMonitorPricing,
  type MonitorPricingConfig,
  type MonitorPricingEditorDescription,
  type MonitorPricingReview,
  type MonitorPricingScenario,
  type MonitorScenarioResults,
} from './price-monitor-pricing-api'
import {
  LEGACY_TOKEN_FIELDS,
  TOKEN_PRICE_VARIABLES,
  addPricingTier,
  applyBatchTokenPrice,
  boundaryPricingScenarios,
  buildPricingChange,
  expressionPriceFields,
  generateFlatPricing,
  legacyTokenPrice,
  parsePriceDraft,
  patchExpressionPrice,
  patchExpressionPrices,
  patchLegacyTokenPrice,
  patchPricingThreshold,
  pricingDraftFingerprint,
  removePricingTier,
  scalePricingDrafts,
  type MonitorPricingChange,
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
  requestContextFromRuleFields,
} from './price-monitor-request-fields-draft'
import { readRuleDocument, compileRuleDocument } from './price-monitor-rule-api'
import { PriceMonitorRuleEditor } from './price-monitor-rule-editor'
import { collectRuleRequestFields } from './price-monitor-rule-helpers'
import { ruleConditionExamples } from './price-monitor-rule-scenarios'
import type { RuleDocument } from './price-monitor-rule-types'

const PRICE_LABELS: Record<TokenPriceVariable, string> = {
  p: 'Input price',
  c: 'Completion price',
  cr: 'Cache read price',
  cc: 'Cache write price',
  cc1h: 'Cache Creation (1h)',
  img: 'Image input price',
  img_o: 'Image output price',
  ai: 'Audio input price',
  ao: 'Audio output price',
}
const LANE_VARIABLES: Record<string, TokenPriceVariable> = {
  cache_read: 'cr',
  cache_write: 'cc',
  cache_write_1h: 'cc1h',
  image_input: 'img',
  image_output: 'img_o',
  audio_input: 'ai',
  audio_output: 'ao',
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
type Range = { min: number; max?: number }

function sourceTierRange(tier: PriceMonitorPriceTier): Range | null {
  if (tier.min_context !== undefined) {
    return { min: tier.min_context, max: tier.max_context }
  }
  if (!tier.condition_variable) return { min: 0 }
  if (tier.condition_variable !== 'len' || tier.condition_value === undefined) {
    return null
  }
  const value = tier.condition_value
  if (tier.condition_operator === '<=') {
    return { min: 0, max: Math.floor(value) }
  }
  if (tier.condition_operator === '<') {
    return { min: 0, max: Math.ceil(value) - 1 }
  }
  if (tier.condition_operator === '>') return { min: Math.floor(value) + 1 }
  if (tier.condition_operator === '>=') return { min: Math.ceil(value) }
  return null
}

function referencePrice(
  price: PriceMonitorPriceCell | undefined,
  range: Range,
  variable: TokenPriceVariable
): number | undefined {
  if (
    !price ||
    price.dynamic ||
    price.unavailable_reason ||
    price.mode === 'per_request'
  ) {
    return undefined
  }
  let item: {
    input?: number
    output?: number
    lanes?: { key: string; price?: number }[]
  } = price
  if (price.tiers?.length) {
    const tier = price.tiers.find((candidate) => {
      const bounds = sourceTierRange(candidate)
      return (
        bounds &&
        bounds.min <= range.min &&
        (bounds.max ?? Infinity) >= (range.max ?? Infinity)
      )
    })
    if (!tier) return undefined
    item = tier
  }
  if (variable === 'p') return item.input
  if (variable === 'c') return item.output
  return item.lanes?.find((lane) => LANE_VARIABLES[lane.key] === variable)
    ?.price
}

export function PriceMonitorPriceFormDialog(props: Props) {
  const { t } = useTranslation()
  const [dirty, setDirty] = useState(false)
  const [working, setWorking] = useState(false)
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
    if (!pinned && query.data && !query.isFetching && !query.isError) {
      setPinned(query.data)
    }
  }, [pinned, query.data, query.isFetching, query.isError])
  return (
    <Dialog
      open={props.open}
      title={t('Change selected prices')}
      description={names.join(', ')}
      contentClassName='sm:max-w-5xl'
      contentHeight='70vh'
      onOpenChange={(open) => {
        if (!open && working) return
        if (
          !open &&
          dirty &&
          !window.confirm(t('Discard unsaved pricing changes?'))
        ) {
          return
        }
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
          onDirty={() => setDirty(true)}
          onWorking={setWorking}
          reload={async () => {
            const result = await query.refetch()
            if (result.error) throw result.error
            if (!result.data) throw new Error('Failed to load model pricing')
            setPinned(result.data)
            setDirty(false)
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
    onDirty: () => void
    onWorking: (value: boolean) => void
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
  const [buffers, setBuffers] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [added, setAdded] = useState<Record<string, TokenPriceVariable[]>>({})
  const [descriptions, setDescriptions] = useState<
    Record<string, MonitorPricingEditorDescription>
  >({})
  const [resetModels, setResetModels] = useState<Set<string>>(new Set())
  const [pricingMethods, setPricingMethods] = useState<Record<string, string>>(
    {}
  )
  const [methodDrafts, setMethodDrafts] = useState<
    Record<
      string,
      {
        pricing: PricingValues
        buffers: Record<string, string>
        errors: Record<string, string>
        method: string
        added: TokenPriceVariable[]
      }
    >
  >({})
  const [working, setWorking] = useState(false)
  const [analysisPending, setAnalysisPending] = useState(false)
  const [sourceKey, setSourceKey] = useState(
    props.sources.find((source) => source.type === 'official')?.key ??
      props.sources.find((source) => source.type !== 'platform')?.key ??
      ''
  )
  const [newItem, setNewItem] = useState<TokenPriceVariable>('cr')
  const [threshold, setThreshold] = useState('')
  const [batchKind, setBatchKind] = useState('set')
  const [batchVariable, setBatchVariable] = useState<TokenPriceVariable>('p')
  const [batchAmount, setBatchAmount] = useState('')
  const [batchProblems, setBatchProblems] = useState<
    { model: string; message: string }[]
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
  const [skippedFields, setSkippedFields] = useState<string[]>([])
  const [skippedConditions, setSkippedConditions] = useState(0)
  const [ruleSessions, setRuleSessions] = useState<
    Record<
      string,
      { document: RuleDocument; pricing: PricingValues; dirty: boolean }
    >
  >({})
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
  const operationRef = useRef(false)
  const revisionRef = useRef(0)
  const analysisRef = useRef(0)
  const entry = entries.find((candidate) => candidate.model_name === active)
  const draft = drafts[active] ?? {}
  const expression = String(
    draft['billing_setting.billing_expr'] ??
      entry?.effective['billing_setting.billing_expr'] ??
      ''
  )
  const modeValue =
    draft['billing_setting.billing_mode'] ??
    entry?.effective['billing_setting.billing_mode']
  let mode = 'token'
  if (
    draft.ModelPrice !== undefined ||
    (modeValue === undefined && entry?.effective.ModelPrice !== undefined)
  ) {
    mode = 'request'
  }
  if (modeValue === 'tiered_expr') mode = 'expression'
  if (
    !modeValue &&
    entry?.effective.ModelRatio === undefined &&
    entry?.effective.ModelPrice === undefined
  ) {
    mode = 'expression'
  }
  if (pricingMethods[active]) mode = pricingMethods[active]
  const description = descriptions[active]
  const canUseForm =
    description?.source === expression && description.kind !== 'custom'
  const parsed =
    mode === 'expression' && canUseForm
      ? expressionPriceFields(expression)
      : null
  const beforeParsed = expressionPriceFields(
    String(entry?.effective['billing_setting.billing_expr'] ?? '')
  )
  const changes = useMemo(
    () =>
      entries.map((model) => ({
        ...buildPricingChange(model, drafts[model.model_name] ?? {}),
        ...(resetModels.has(model.model_name) ? { reset: true } : {}),
      })),
    [entries, drafts, resetModels]
  )
  const fingerprint = pricingDraftFingerprint(changes)
  const scenarioFingerprint = JSON.stringify([
    active,
    fingerprint,
    scenarioForm,
    requestContext,
    examples,
    ruleSessions,
    sourceKey,
  ])
  const source = props.sources.find((candidate) => candidate.key === sourceKey)
  const sourcePrice = props.items.find(
    (candidate) => candidate.model === active
  )?.prices[sourceKey]
  const invalid =
    Object.keys(errors).length > 0 ||
    Object.values(buffers).some((value) => !value.trim()) ||
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
  const visibleVariables = TOKEN_PRICE_VARIABLES.filter(
    (variable) =>
      variable === 'p' ||
      variable === 'c' ||
      added[active]?.includes(variable) ||
      parsed?.some((branch) => branch.prices[variable] !== undefined) ||
      (mode === 'token' &&
        LEGACY_TOKEN_FIELDS.some(
          ([key, item]) => item === variable && draft[key] !== undefined
        ))
  )
  const riskFlags = risk
    ? pricingRiskRequired(risk.data)
    : { loss: false, unknown: false }
  const restoringDefaults = resetModels.has(active)
  const ruleSession = ruleSessions[active]

  const errorMessage = (error: unknown, fallback: string) => {
    let message = t(fallback)
    if (error instanceof Error && error.message) message = t(error.message)
    if (error instanceof RequestFieldError) message += ` · ${error.path}`
    toast.error(message)
  }
  const markDirty = () => {
    revisionRef.current++
    props.onDirty()
    setRisk(null)
    setAcceptLoss(false)
    setAcceptUnknown(false)
  }
  const update = (
    name: string,
    values: PricingValues,
    nextExpression?: string
  ) => {
    markDirty()
    setDrafts((current) => ({ ...current, [name]: values }))
    setPricingMethods((current) => {
      const next = { ...current }
      delete next[name]
      if (values['billing_setting.billing_mode'] === 'tiered_expr') {
        next[name] = 'expression'
      } else if (values.ModelPrice !== undefined) next[name] = 'request'
      else if (values['billing_setting.billing_mode'] === 'ratio') {
        next[name] = 'token'
      }
      return next
    })
    setResetModels((current) => {
      const next = new Set(current)
      next.delete(name)
      return next
    })
    if (nextExpression !== undefined) {
      setDescriptions((current) => {
        const previous = current[name]
        if (!previous || previous.kind === 'custom') return current
        return { ...current, [name]: { ...previous, source: nextExpression } }
      })
    }
  }
  const setFieldError = (key: string, message?: string) =>
    setErrors((current) => {
      const next = { ...current }
      if (message) next[key] = message
      else delete next[key]
      return next
    })
  const clearActiveBuffers = () => {
    setBuffers((current) =>
      Object.fromEntries(
        Object.entries(current).filter(([key]) => !key.startsWith(`${active}:`))
      )
    )
    setErrors((current) =>
      Object.fromEntries(
        Object.entries(current).filter(([key]) => !key.startsWith(`${active}:`))
      )
    )
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
  const analyze = async (name: string, values: PricingValues) => {
    const serial = ++analysisRef.current
    const revision = revisionRef.current
    setAnalysisPending(true)
    try {
      const result = await describeMonitorPricing(name, values)
      if (serial !== analysisRef.current || revision !== revisionRef.current) {
        return
      }
      if (result.editor) {
        setDescriptions((current) => ({
          ...current,
          [name]: result.editor as MonitorPricingEditorDescription,
        }))
      }
      if (result.editor?.kind === 'custom' && result.rule_document) {
        const document = result.rule_document
        setRuleSessions((current) =>
          current[name]?.dirty
            ? current
            : {
                ...current,
                [name]: {
                  document,
                  pricing: {
                    ...values,
                    'billing_setting.billing_mode': 'tiered_expr',
                    'billing_setting.billing_expr':
                      result.effective['billing_setting.billing_expr'],
                  },
                  dirty: false,
                },
              }
        )
      }
    } catch {
      toast.error(t('Could not prepare pricing rules. Retry to keep editing.'))
    } finally {
      if (serial === analysisRef.current) setAnalysisPending(false)
    }
  }
  // Analyze only on initial model selection or an explicit advanced-editor action.
  useEffect(() => {
    analysisRef.current++
    setAnalysisPending(false)
    const model = entries.find((candidate) => candidate.model_name === active)
    const values = drafts[active] ?? {}
    const selectedMode =
      values['billing_setting.billing_mode'] ??
      model?.effective['billing_setting.billing_mode']
    if (selectedMode === 'tiered_expr' && !descriptions[active]) {
      void analyze(active, values)
    }
    // The expression deliberately is not a dependency: typing never sends a request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  const setPrice = (
    tier: number,
    variable: TokenPriceVariable,
    text: string
  ) => {
    if (!entry) return
    const fieldId = `${active}:price:${tier}:${variable}`
    markDirty()
    setBuffers((current) => ({ ...current, [fieldId]: text }))
    try {
      if (parsePriceDraft(text) === undefined) {
        throw new Error('Enter a valid price')
      }
      if (mode === 'token') {
        const legacy = LEGACY_TOKEN_FIELDS.find(([, item]) => item === variable)
        if (!legacy) {
          throw new Error('Open pricing rules to add this price item')
        }
        update(
          active,
          patchLegacyTokenPrice(
            entry,
            draft,
            legacy[0],
            text,
            config.token_price_factor
          )
        )
      } else if (parsed) {
        let next = expression
        if (
          parsed.length > 1 &&
          !parsed.some((branch) => branch.prices[variable] !== undefined)
        ) {
          const values = parsed.map((_, index) =>
            index === tier
              ? text
              : (buffers[`${active}:price:${index}:${variable}`] ?? '')
          )
          setBuffers((current) => ({
            ...current,
            ...Object.fromEntries(
              values.map((value, index) => [
                `${active}:price:${index}:${variable}`,
                value,
              ])
            ),
          }))
          if (values.some((value) => !value.trim())) {
            throw new Error('Enter the new price item for every tier')
          }
          next = patchExpressionPrices(expression, variable, values)
          setErrors((current) =>
            Object.fromEntries(
              Object.entries(current).filter(
                ([key]) =>
                  !(
                    key.startsWith(`${active}:price:`) &&
                    key.endsWith(`:${variable}`)
                  )
              )
            )
          )
        } else next = patchExpressionPrice(expression, tier, variable, text)
        update(
          active,
          {
            ...draft,
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': next,
          },
          next
        )
      } else if (!expression.trim() && mode === 'expression') {
        const values: Partial<Record<TokenPriceVariable, string>> = {}
        for (const item of visibleVariables) {
          values[item] =
            item === variable ? text : buffers[`${active}:price:0:${item}`]
        }
        if (!values.p?.trim() || !values.c?.trim()) {
          throw new Error('Enter input and output prices')
        }
        if (visibleVariables.some((item) => !values[item]?.trim())) {
          throw new Error('Enter a valid price')
        }
        const next = generateFlatPricing(values)
        const nextDraft = {
          ...draft,
          'billing_setting.billing_mode': 'tiered_expr',
          'billing_setting.billing_expr': next,
        }
        update(active, nextDraft)
        clearActiveBuffers()
        void analyze(active, nextDraft)
      } else throw new Error('Reload pricing rules before editing prices')
      setFieldError(fieldId)
    } catch (error) {
      setFieldError(
        fieldId,
        error instanceof Error ? error.message : 'Enter a valid price'
      )
    }
  }
  const setFixedPrice = (text: string) => {
    markDirty()
    setBuffers((current) => ({ ...current, [`${active}:fixed`]: text }))
    try {
      const value = parsePriceDraft(text)
      if (value === undefined) throw new Error('Enter a valid price')
      update(active, { ...draft, ModelPrice: value })
      setFieldError(`${active}:fixed`)
    } catch {
      setFieldError(`${active}:fixed`, 'Enter a valid price')
    }
  }

  const compileChanges = async () => {
    const captured = structuredClone(changes)
    for (const change of captured) {
      if (change.reset) continue
      const session = ruleSessions[change.model_name]
      if (!session?.dirty) continue
      const result = await compileRuleDocument(
        change.model_name,
        session.pricing,
        session.document
      )
      change.pricing = result.pricing as PricingValues
    }
    return captured
  }
  const openRules = async () => {
    if (!begin()) return
    try {
      let values = { ...draft }
      if (mode !== 'expression') {
        if (mode === 'request') {
          const amount = parsePriceDraft(
            String(draft.ModelPrice ?? entry?.effective.ModelPrice ?? '')
          )
          if (amount === undefined) throw new Error('Enter a valid price')
          values = {
            ...values,
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': String(amount * 1_000_000),
          }
          delete values.ModelPrice
        } else {
          const converted = await convertMonitorPricing(active, values)
          if (!converted.expression) {
            throw new Error(
              'These prices need review before adding rules. Check the current prices.'
            )
          }
          values = {
            ...values,
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': converted.expression,
          }
        }
      }
      const result = await readRuleDocument(active, values)
      setRuleSessions((current) => ({
        ...current,
        [active]: {
          document: result.rule_document,
          pricing: {
            ...values,
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': String(
              result.effective['billing_setting.billing_expr']
            ),
          },
          dirty: false,
        },
      }))
    } catch (error) {
      errorMessage(
        error,
        'Could not prepare pricing rules. Retry to keep editing.'
      )
    } finally {
      end()
    }
  }
  const finishRules = async () => {
    if (!ruleSession || !begin()) return
    try {
      if (ruleSession.dirty) {
        const result = await compileRuleDocument(
          active,
          ruleSession.pricing,
          ruleSession.document
        )
        update(active, result.pricing as PricingValues)
        clearActiveBuffers()
        await analyze(active, result.pricing as PricingValues)
      } else {
        await analyze(active, draft)
      }
      setRuleSessions((current) => {
        const next = { ...current }
        delete next[active]
        return next
      })
    } catch (error) {
      errorMessage(error, 'Check the rule fields and try again')
    } finally {
      end()
    }
  }

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
    if (invalid || !begin()) return
    const revision = revisionRef.current
    try {
      const capturedChanges = await compileChanges()
      const capturedFingerprint = pricingDraftFingerprint(capturedChanges)
      setDrafts(
        Object.fromEntries(
          capturedChanges.map((change) => [change.model_name, change.pricing])
        )
      )
      setPricingMethods({})
      setRuleSessions((current) =>
        Object.fromEntries(
          Object.entries(current).map(([name, session]) => [
            name,
            { ...session, dirty: false },
          ])
        )
      )
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
      const candidateChanges = await compileChanges()
      const candidate = candidateChanges.find(
        (change) => change.model_name === active
      )
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
  const allPriceItems = TOKEN_PRICE_VARIABLES.map((variable) => ({
    value: variable,
    label: t(PRICE_LABELS[variable]),
  }))
  const formatAmount = (value: number | string | undefined) =>
    value === undefined || !Number.isFinite(Number(value))
      ? t('Not separately priced')
      : `$${Number(value).toLocaleString(undefined, { maximumFractionDigits: 8 })}`
  const rangeLabel = (range: Range) => {
    if (range.min === 0 && range.max === undefined) {
      return t('All input lengths')
    }
    if (range.min === 0) {
      return t('Input length up to {{count}} tokens', {
        count: range.max?.toLocaleString(),
      })
    }
    if (range.max === undefined) {
      return t('Input length above {{count}} tokens', {
        count: (range.min - 1).toLocaleString(),
      })
    }
    return t('Input length from {{min}} to {{max}} tokens', {
      min: range.min.toLocaleString(),
      max: range.max.toLocaleString(),
    })
  }
  const riskPriceLabel = (field: string) => {
    if (field === 'model_price') return t('Fixed price')
    const variable = field.split(':').at(-1) as TokenPriceVariable
    const priceLabel = t(PRICE_LABELS[variable] ?? 'Price')
    const start = field.split(':')[0]
    return start && /^\d+$/.test(start) && Number(start) > 0
      ? `${priceLabel} · ${t('Input length from {{count}} tokens', { count: Number(start).toLocaleString() })}`
      : priceLabel
  }
  const priceTables: {
    key: string
    label: string
    min: number
    max?: number
    prices: Partial<Record<TokenPriceVariable, string>>
    reachable: boolean
  }[] =
    mode === 'expression'
      ? (parsed ??
        (!expression.trim()
          ? [{ key: 'new', label: '', min: 0, prices: {}, reachable: true }]
          : []))
      : [{ key: 'legacy', label: '', min: 0, prices: {}, reachable: true }]
  return (
    <div className='space-y-5'>
      {entries.length > 1 && (
        <div className='flex flex-wrap gap-2'>
          {entries.map((model) => (
            <Button
              key={model.model_name}
              size='sm'
              variant={active === model.model_name ? 'default' : 'outline'}
              disabled={working}
              onClick={() => {
                setActive(model.model_name)
                setThreshold('')
              }}
            >
              {model.model_name}
              {Object.keys(errors).some((key) =>
                key.startsWith(`${model.model_name}:`)
              )
                ? ' •'
                : ''}
            </Button>
          ))}
        </div>
      )}
      <Field>
        <FieldLabel>{t('Pricing method')}</FieldLabel>
        <ToggleGroup
          value={[mode === 'request' ? 'request' : 'usage']}
          disabled={working || !!ruleSession}
          onValueChange={(values) => {
            const selected = values[0]
            if (
              !selected ||
              (selected === 'request') === (mode === 'request')
            ) {
              return
            }
            setMethodDrafts((current) => ({
              ...current,
              [`${active}:${mode === 'request' ? 'request' : 'usage'}`]: {
                pricing: structuredClone(draft),
                buffers: Object.fromEntries(
                  Object.entries(buffers).filter(([key]) =>
                    key.startsWith(`${active}:`)
                  )
                ),
                errors: Object.fromEntries(
                  Object.entries(errors).filter(([key]) =>
                    key.startsWith(`${active}:`)
                  )
                ),
                method: mode,
                added: added[active] ?? [],
              },
            }))
            const previous = methodDrafts[`${active}:${selected}`]
            if (previous) {
              update(active, structuredClone(previous.pricing))
              clearActiveBuffers()
              setBuffers((current) => ({ ...current, ...previous.buffers }))
              setErrors((current) => ({ ...current, ...previous.errors }))
              setAdded((current) => ({ ...current, [active]: previous.added }))
              setPricingMethods((current) => ({
                ...current,
                [active]: previous.method,
              }))
              if (
                previous.method === 'expression' &&
                !Object.keys(previous.errors).length
              ) {
                void analyze(active, previous.pricing)
              }
              return
            }
            const next: PricingValues = { ...draft }
            delete next.ModelPrice
            delete next['billing_setting.billing_expr']
            for (const [key] of LEGACY_TOKEN_FIELDS) delete next[key]
            next['billing_setting.billing_mode'] =
              selected === 'request' ? 'ratio' : 'tiered_expr'
            if (selected !== 'request') {
              next['billing_setting.billing_expr'] = ''
            }
            update(active, next)
            clearActiveBuffers()
            setDescriptions((current) => {
              const result = { ...current }
              delete result[active]
              return result
            })
            setPricingMethods((current) => ({
              ...current,
              [active]: selected === 'request' ? 'request' : 'expression',
            }))
            if (selected === 'request') {
              setBuffers((current) => ({ ...current, [`${active}:fixed`]: '' }))
              setFieldError(`${active}:fixed`, 'Enter a valid price')
            }
          }}
        >
          <ToggleGroupItem value='usage'>{t('Per token')}</ToggleGroupItem>
          <ToggleGroupItem value='request'>{t('Per-call')}</ToggleGroupItem>
        </ToggleGroup>
      </Field>
      <FieldGroup className='gap-3'>
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
          <FieldDescription>
            {t(
              'Reference prices never prevent you from entering your own price.'
            )}
            {source && ` ${source.name}`}
          </FieldDescription>
        </Field>
      </FieldGroup>
      {restoringDefaults && (
        <div className='rounded-md border p-3 text-sm'>
          <p>
            {t(
              'Default pricing will replace these prices when saved. The fields below show the previous configuration.'
            )}
          </p>
          <Button
            variant='outline'
            className='mt-3'
            disabled={working}
            onClick={() => {
              update(active, structuredClone(entry.configured))
              clearActiveBuffers()
            }}
          >
            {t('Keep current pricing')}
          </Button>
        </div>
      )}
      {!ruleSession &&
        (mode === 'request' ? (
          <div className='rounded-md border p-3'>
            <p className='mb-2 text-sm font-medium'>{t('USD per request')}</p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Price item')}</TableHead>
                  <TableHead>{t('Current price')}</TableHead>
                  <TableHead>{t('New price')}</TableHead>
                  <TableHead>{t('Reference price')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell>{t('Fixed price')}</TableCell>
                  <TableCell>
                    {formatAmount(
                      entry.effective.ModelPrice === undefined
                        ? undefined
                        : Number(entry.effective.ModelPrice)
                    )}
                  </TableCell>
                  <TableCell>
                    <Input
                      aria-label={t('Fixed price')}
                      disabled={working || restoringDefaults}
                      value={
                        buffers[`${active}:fixed`] ??
                        String(
                          draft.ModelPrice ?? entry.effective.ModelPrice ?? ''
                        )
                      }
                      onChange={(event) => {
                        setFixedPrice(event.target.value)
                      }}
                    />
                  </TableCell>
                  <TableCell>
                    {sourcePrice?.mode === 'per_request' &&
                    sourcePrice.price !== undefined &&
                    !sourcePrice.unavailable_reason &&
                    !sourcePrice.dynamic ? (
                      <Button
                        variant='link'
                        disabled={working || restoringDefaults}
                        onClick={() => setFixedPrice(String(sourcePrice.price))}
                      >
                        {formatAmount(sourcePrice.price)}
                      </Button>
                    ) : (
                      t('No reference price')
                    )}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        ) : (
          <>
            {analysisPending && (
              <p role='status'>{t('Preparing price fields...')}</p>
            )}
            {mode === 'expression' &&
              expression.trim() &&
              !parsed &&
              !analysisPending && (
                <div className='rounded-md border p-3 text-sm'>
                  <p className='font-medium'>{t('Custom pricing rules')}</p>
                  <p className='text-muted-foreground mt-1'>
                    {t(
                      'Use pricing rule cards to change conditions, prices and fee adjustments.'
                    )}
                  </p>
                  <Button
                    variant='outline'
                    className='mt-3'
                    disabled={working}
                    onClick={() => void openRules()}
                  >
                    {t('Edit pricing rules')}
                  </Button>
                </div>
              )}
            {priceTables.map((tier, tierIndex) => {
              const range: Range = {
                min: tier.min,
                max: 'max' in tier ? tier.max : undefined,
              }
              const previous = beforeParsed?.find(
                (candidate) =>
                  candidate.min <= range.min &&
                  (candidate.max ?? Infinity) >= (range.max ?? Infinity)
              )
              return (
                <div key={tier.key} className='rounded-md border p-3'>
                  <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
                    <div>
                      <p className='text-sm font-medium'>
                        {tier.reachable
                          ? rangeLabel(range)
                          : t('This pricing tier is not currently reachable')}
                      </p>
                      {tier.label && (
                        <p className='text-muted-foreground text-xs'>
                          {tier.label}
                        </p>
                      )}
                    </div>
                    <span className='text-muted-foreground text-xs'>
                      {t('USD per million tokens')}
                    </span>
                  </div>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('Price item')}</TableHead>
                        <TableHead>{t('Current price')}</TableHead>
                        <TableHead>{t('New price')}</TableHead>
                        <TableHead>{t('Reference price')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visibleVariables.map((variable) => {
                        const fieldId = `${active}:price:${tierIndex}:${variable}`
                        const legacy = LEGACY_TOKEN_FIELDS.find(
                          ([, item]) => item === variable
                        )
                        const current =
                          mode === 'token' && legacy
                            ? legacyTokenPrice(
                                entry,
                                entry.configured,
                                legacy[0],
                                config.token_price_factor
                              )
                            : previous?.prices[variable]
                        const actual =
                          mode === 'token' && legacy
                            ? legacyTokenPrice(
                                entry,
                                draft,
                                legacy[0],
                                config.token_price_factor
                              )
                            : tier.prices[variable]
                        const price = referencePrice(
                          sourcePrice,
                          range,
                          variable
                        )
                        const locked =
                          mode === 'token' &&
                          variable === 'c' &&
                          entry.completion_locked !== false
                        const enabled =
                          !working &&
                          !restoringDefaults &&
                          !locked &&
                          (mode !== 'token' ||
                            (Boolean(legacy) && config.token_price_factor > 0))
                        return (
                          <TableRow key={variable}>
                            <TableCell className='min-w-28 text-xs'>
                              {t(PRICE_LABELS[variable])}
                              {added[active]?.includes(variable) &&
                                tier.prices[variable] === undefined && (
                                  <Button
                                    variant='link'
                                    size='sm'
                                    className='mt-1 h-auto p-0 text-xs'
                                    disabled={working}
                                    onClick={() => {
                                      markDirty()
                                      setAdded((current) => ({
                                        ...current,
                                        [active]: (
                                          current[active] ?? []
                                        ).filter((item) => item !== variable),
                                      }))
                                      setBuffers((current) =>
                                        Object.fromEntries(
                                          Object.entries(current).filter(
                                            ([key]) =>
                                              !(
                                                key.startsWith(
                                                  `${active}:price:`
                                                ) &&
                                                key.endsWith(`:${variable}`)
                                              )
                                          )
                                        )
                                      )
                                      setErrors((current) =>
                                        Object.fromEntries(
                                          Object.entries(current).filter(
                                            ([key]) =>
                                              !(
                                                key.startsWith(
                                                  `${active}:price:`
                                                ) &&
                                                key.endsWith(`:${variable}`)
                                              )
                                          )
                                        )
                                      )
                                    }}
                                  >
                                    {t('Cancel adding this item')}
                                  </Button>
                                )}
                            </TableCell>
                            <TableCell className='text-xs whitespace-nowrap'>
                              {formatAmount(current)}
                            </TableCell>
                            <TableCell className='min-w-32'>
                              <Input
                                aria-label={`${rangeLabel(range)} ${t(PRICE_LABELS[variable])}`}
                                value={
                                  buffers[fieldId] ??
                                  (actual === undefined ? '' : String(actual))
                                }
                                disabled={!enabled}
                                placeholder={t('Enter price')}
                                aria-invalid={Boolean(errors[fieldId])}
                                onChange={(event) =>
                                  setPrice(
                                    tierIndex,
                                    variable,
                                    event.target.value
                                  )
                                }
                              />
                              {errors[fieldId] && (
                                <p className='text-destructive mt-1 text-xs'>
                                  {t(errors[fieldId])}
                                </p>
                              )}
                              {locked && (
                                <p className='text-muted-foreground mt-1 text-xs'>
                                  {t(
                                    'Completion price follows the input price for this model'
                                  )}
                                </p>
                              )}
                            </TableCell>
                            <TableCell className='text-xs'>
                              {price === undefined ? (
                                t('No compatible reference price')
                              ) : (
                                <Button
                                  variant='link'
                                  className='h-auto p-0 text-xs'
                                  disabled={!enabled}
                                  onClick={() =>
                                    setPrice(tierIndex, variable, String(price))
                                  }
                                >
                                  {formatAmount(price)} · {t('Use this price')}
                                </Button>
                              )}
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                  {parsed && parsed.length > 1 && tier.reachable && (
                    <div className='mt-3 flex flex-wrap items-end gap-2'>
                      {tier.max !== undefined && (
                        <Field className='max-w-48'>
                          <FieldLabel htmlFor={`${id}-threshold-${tierIndex}`}>
                            {t('Maximum input length')}
                          </FieldLabel>
                          <Input
                            id={`${id}-threshold-${tierIndex}`}
                            value={
                              buffers[`${active}:threshold:${tierIndex}`] ??
                              String(tier.max)
                            }
                            disabled={working}
                            aria-invalid={Boolean(
                              errors[`${active}:threshold:${tierIndex}`]
                            )}
                            onChange={(event) => {
                              markDirty()
                              setBuffers((current) => ({
                                ...current,
                                [`${active}:threshold:${tierIndex}`]:
                                  event.target.value,
                              }))
                              setFieldError(
                                `${active}:threshold:${tierIndex}`,
                                'Apply a valid input-length boundary before saving'
                              )
                            }}
                            onBlur={(event) => {
                              try {
                                const next = patchPricingThreshold(
                                  expression,
                                  tierIndex,
                                  event.target.value
                                )
                                setFieldError(
                                  `${active}:threshold:${tierIndex}`
                                )
                                setBuffers((current) => {
                                  const values = { ...current }
                                  delete values[
                                    `${active}:threshold:${tierIndex}`
                                  ]
                                  return values
                                })
                                if (next !== expression) {
                                  update(active, {
                                    ...draft,
                                    'billing_setting.billing_expr': next,
                                  })
                                  void analyze(active, {
                                    ...draft,
                                    'billing_setting.billing_expr': next,
                                  })
                                }
                              } catch {
                                setFieldError(
                                  `${active}:threshold:${tierIndex}`,
                                  'Enter a valid tier boundary'
                                )
                              }
                            }}
                          />
                          {errors[`${active}:threshold:${tierIndex}`] && (
                            <p className='text-destructive text-xs'>
                              {t(errors[`${active}:threshold:${tierIndex}`])}
                            </p>
                          )}
                        </Field>
                      )}
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={working || invalid}
                        onClick={() => {
                          if (
                            !window.confirm(
                              t(
                                'Remove this tier and extend the adjacent tier prices over its range?'
                              )
                            )
                          ) {
                            return
                          }
                          try {
                            const next = removePricingTier(
                              expression,
                              tierIndex
                            )
                            clearActiveBuffers()
                            update(active, {
                              ...draft,
                              'billing_setting.billing_expr': next,
                            })
                            void analyze(active, {
                              ...draft,
                              'billing_setting.billing_expr': next,
                            })
                          } catch (error) {
                            errorMessage(
                              error,
                              'Could not change pricing tiers'
                            )
                          }
                        }}
                      >
                        {t('Remove tier')}
                      </Button>
                    </div>
                  )}
                </div>
              )
            })}
            <div className='flex flex-wrap items-end gap-2'>
              <Field className='min-w-40 flex-1'>
                <FieldLabel htmlFor={`${id}-new-item`}>
                  {t('Add price item')}
                </FieldLabel>
                <Select
                  items={allPriceItems}
                  value={newItem}
                  onValueChange={(value) => {
                    if (value) setNewItem(value as TokenPriceVariable)
                  }}
                  disabled={working}
                >
                  <SelectTrigger id={`${id}-new-item`}>
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
              </Field>
              <Button
                variant='outline'
                disabled={
                  working ||
                  visibleVariables.includes(newItem) ||
                  (mode === 'expression' &&
                    Boolean(expression.trim()) &&
                    !parsed)
                }
                onClick={() => {
                  setAdded((current) => ({
                    ...current,
                    [active]: [...(current[active] ?? []), newItem],
                  }))
                  markDirty()
                  const ranges = priceTables.length || 1
                  setBuffers((current) => ({
                    ...current,
                    ...Object.fromEntries(
                      Array.from({ length: ranges }, (_, index) => [
                        `${active}:price:${index}:${newItem}`,
                        '',
                      ])
                    ),
                  }))
                }}
              >
                {t('Add')}
              </Button>
            </div>
            {added[active]?.length ? (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Fill the new price item for every tier. Cache and media pricing may change how tokens are counted; compare both usage formats.'
                )}
              </p>
            ) : null}
            {mode === 'token' && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Other token prices follow the input price unless changed separately. The full changes are shown below.'
                )}
              </p>
            )}
            {(parsed || mode === 'token') && (
              <FieldGroup className='rounded-md border p-3'>
                <Field>
                  <FieldLabel htmlFor={`${id}-new-tier`}>
                    {t('Add pricing tier')}
                  </FieldLabel>
                  <FieldDescription>
                    {t(
                      'The new tier starts after this input length and initially uses the current prices.'
                    )}
                  </FieldDescription>
                  <div className='flex gap-2'>
                    <Input
                      id={`${id}-new-tier`}
                      value={threshold}
                      disabled={working}
                      placeholder={t('Input length')}
                      onChange={(event) => setThreshold(event.target.value)}
                    />
                    <Button
                      variant='outline'
                      disabled={working || invalid}
                      onClick={async () => {
                        if (!begin()) return
                        try {
                          let original = expression
                          if (mode === 'token') {
                            const result = await convertMonitorPricing(
                              active,
                              draft
                            )
                            if (!result.expression) {
                              throw new Error(
                                'These prices need review before adding rules. Check the current prices.'
                              )
                            }
                            original = result.expression
                          }
                          const next = addPricingTier(original, threshold)
                          clearActiveBuffers()
                          update(active, {
                            ...draft,
                            'billing_setting.billing_mode': 'tiered_expr',
                            'billing_setting.billing_expr': next,
                          })
                          setThreshold('')
                          void analyze(active, {
                            ...draft,
                            'billing_setting.billing_mode': 'tiered_expr',
                            'billing_setting.billing_expr': next,
                          })
                        } catch (error) {
                          errorMessage(error, 'Could not change pricing tiers')
                        } finally {
                          end()
                        }
                      }}
                    >
                      {t('Add tier')}
                    </Button>
                  </div>
                </Field>
              </FieldGroup>
            )}
          </>
        ))}
      {ruleSession ? (
        <FieldGroup className='rounded-md border p-3'>
          <PriceMonitorRuleEditor
            key={active}
            document={ruleSession.document}
            disabled={working}
            onValidityChange={(valid) =>
              setFieldError(
                `${active}:rules`,
                valid ? undefined : 'Check the rule fields and try again'
              )
            }
            onChange={(document) => {
              markDirty()
              setRuleSessions((current) => ({
                ...current,
                [active]: { ...ruleSession, document, dirty: true },
              }))
            }}
          />
          <Button
            variant='outline'
            disabled={working}
            onClick={() => void finishRules()}
          >
            {t('Done editing rules')}
          </Button>
        </FieldGroup>
      ) : (
        <Button
          variant='outline'
          disabled={working || invalid || restoringDefaults}
          onClick={() => void openRules()}
        >
          {t('Add or edit special pricing rules')}
        </Button>
      )}
      {(entries.length > 1 || (mode === 'expression' && !parsed)) && (
        <FieldGroup className='rounded-md border p-3'>
          <Field>
            <FieldLabel>
              {entries.length > 1
                ? t('Batch price adjustment')
                : t('Adjust total calculated price')}
            </FieldLabel>
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
                  if (value) setBatchVariable(value as TokenPriceVariable)
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
              placeholder={
                batchKind === 'set' ? t('USD per million tokens') : '%'
              }
              onChange={(event) => setBatchAmount(event.target.value)}
            />
            <Button
              variant='outline'
              disabled={working || invalid}
              onClick={async () => {
                if (!begin()) return
                try {
                  const compiledChanges = await compileChanges()
                  const amount = parsePriceDraft(batchAmount)
                  if (amount === undefined) {
                    throw new Error('Enter a valid price')
                  }
                  const problems: { model: string; message: string }[] = []
                  const next: MonitorPricingChange[] = []
                  for (const change of compiledChanges) {
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
                  setBatchProblems(problems)
                  if (problems.length) return
                  markDirty()
                  setDrafts(
                    Object.fromEntries(
                      next.map((change) => [change.model_name, change.pricing])
                    )
                  )
                  setBuffers({})
                  setErrors({})
                  setResetModels(new Set())
                  setPricingMethods({})
                  setDescriptions({})
                  const activeDraft = next.find(
                    (change) => change.model_name === active
                  )?.pricing
                  if (activeDraft) void analyze(active, activeDraft)
                  setRuleSessions({})
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
            {batchProblems.map((problem) => (
              <p key={problem.model} className='text-destructive text-xs'>
                {problem.model} ·{' '}
                {batchKind === 'set'
                  ? t(PRICE_LABELS[batchVariable])
                  : t('All existing prices')}
                : {t(problem.message)}
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
      <details className='rounded-md border p-3'>
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
          <Button
            variant='outline'
            disabled={working || mode !== 'expression'}
            onClick={async () => {
              if (!begin()) return
              try {
                const document =
                  ruleSession?.document ??
                  (await readRuleDocument(active, draft)).rule_document
                const result = requestContextFromRuleFields(
                  requestContext,
                  collectRuleRequestFields(document)
                )
                setRequestContext(result.draft)
                setSkippedFields(result.skipped)
              } catch (error) {
                errorMessage(error, 'Could not prepare request fields')
              } finally {
                end()
              }
            }}
          >
            {t('Add fields used by these rules')}
          </Button>
          {skippedFields.length > 0 && (
            <p role='status' className='text-muted-foreground text-xs'>
              {t('Add these request fields manually')}:{' '}
              {skippedFields.join(', ')}
            </p>
          )}
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
              disabled={working || !parsed || !!ruleSession}
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
            <Button
              variant='outline'
              disabled={working || mode !== 'expression'}
              onClick={async () => {
                if (!begin()) return
                try {
                  const document =
                    ruleSession?.document ??
                    (await readRuleDocument(active, draft)).rule_document
                  const result = ruleConditionExamples(document, {
                    ...basicPricingScenario(scenarioForm),
                    ...serializeRequestContext(requestContext),
                  })
                  setExamples(result.scenarios)
                  setSkippedConditions(result.skipped)
                } catch (error) {
                  errorMessage(error, 'Check the example fields and try again')
                } finally {
                  end()
                }
              }}
            >
              {t('Generate examples for these conditions')}
            </Button>
          </div>
          {skippedConditions > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Some conditions need a manually filled example. Generated examples do not cover every combination.'
              )}
            </p>
          )}
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
        changes={changes.filter(
          (change) => !ruleSessions[change.model_name]?.dirty
        )}
        factor={config.token_price_factor}
      />
      {Object.entries(ruleSessions)
        .filter(([, session]) => session.dirty)
        .map(([name]) => (
          <p key={name} className='text-muted-foreground text-sm'>
            {name} ·{' '}
            {t(
              'Rule changes are shown above and will be checked before saving.'
            )}
          </p>
        ))}
      {Object.entries(errors).length > 0 && (
        <div role='alert' className='text-destructive space-y-1 text-xs'>
          {Object.entries(errors).map(([key, error]) => {
            const model = [...entries]
              .sort(
                (left, right) =>
                  right.model_name.length - left.model_name.length
              )
              .find((candidate) => key.startsWith(`${candidate.model_name}:`))
            const field = key
              .slice((model?.model_name.length ?? 0) + 1)
              .split(':')
            let label = t('Fixed price')
            if (field[0] === 'threshold') label = t('Maximum input length')
            if (field[0] === 'rules') label = t('Pricing rules')
            if (field[0] === 'price') {
              label = `${t('Tier {{number}}', { number: Number(field[1]) + 1 })} · ${t(PRICE_LABELS[field[2] as TokenPriceVariable] ?? 'Price')}`
            }
            return (
              <p key={key}>
                {model?.model_name ?? active} · {label}: {t(error)}
              </p>
            )
          })}
        </div>
      )}
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
              if (
                !window.confirm(t('Discard unsaved pricing changes?')) ||
                !begin()
              ) {
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
      <div className='flex flex-wrap justify-end gap-2 border-t pt-4'>
        <Button
          variant='outline'
          disabled={working}
          onClick={() => {
            if (!window.confirm(t('Restore default pricing for this model?'))) {
              return
            }
            update(active, {})
            clearActiveBuffers()
            setMethodDrafts((current) =>
              Object.fromEntries(
                Object.entries(current).filter(
                  ([key]) =>
                    key !== `${active}:request` && key !== `${active}:usage`
                )
              )
            )
            setRuleSessions((current) => {
              const next = { ...current }
              delete next[active]
              return next
            })
            setResetModels((current) => new Set([...current, active]))
          }}
        >
          {t('Restore default pricing')}
        </Button>
        <Button
          variant='outline'
          disabled={working}
          onClick={() => {
            if (
              revisionRef.current > 0 &&
              !window.confirm(t('Discard unsaved pricing changes?'))
            ) {
              return
            }
            props.onOpenChange(false)
          }}
        >
          {t('Cancel')}
        </Button>
        <Button disabled={working || invalid} onClick={() => void save()}>
          {working ? t('Saving...') : t('Save changes')}
        </Button>
      </div>
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
  return (
    <details className='rounded-md border p-3' open>
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
            for (const tier of after) {
              for (const variable of TOKEN_PRICE_VARIABLES) {
                const old = before?.find(
                  (candidate) =>
                    candidate.min === tier.min && candidate.max === tier.max
                )?.prices[variable]
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
            return (
              <p key={change.model_name}>
                {change.model_name} ·{' '}
                {t(
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
