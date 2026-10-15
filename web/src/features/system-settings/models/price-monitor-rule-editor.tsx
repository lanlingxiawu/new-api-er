/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
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
import { Textarea } from '@/components/ui/textarea'

import {
  cloneRuleNode,
  createConditionalAdjustment,
  createFlatPriceNode,
  createTimeCondition,
  extractPriceItems,
  moveRuleChild,
  replaceRuleNode,
  reorderPricingBranches,
  ruleNode,
  rulePriceLeaves,
  ruleUsedVariables,
  updatePriceItem,
  validateRuleDocument,
} from './price-monitor-rule-helpers'
import {
  RULE_BINARY_ACTIONS,
  RULE_CALL_ACTIONS,
  RULE_PRICE_LABELS,
  RULE_USAGE_LABELS,
  RULE_USAGE_VARIABLES,
  RULE_TIME_ZONES,
  type RuleDocument,
  type RuleNode,
  type RulePriceVariable,
} from './price-monitor-rule-types'

type Choice = { value: string; label: string }
function RuleChoice(props: {
  label: string
  value: string
  options: Choice[]
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const id = useId()
  return (
    <Field>
      <FieldLabel htmlFor={id}>{props.label}</FieldLabel>
      <Select
        items={props.options}
        value={props.value}
        disabled={props.disabled}
        onValueChange={(value) => {
          if (value) props.onChange(value)
        }}
      >
        <SelectTrigger id={id}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {props.options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

function blankNode(type: string): RuleNode {
  if (type === 'usage') return ruleNode('usage', { name: 'len' })
  if (type === 'string') return ruleNode('string', { value: '' })
  if (type === 'boolean') return ruleNode('boolean', { value: 'true' })
  if (type === 'null') return ruleNode('null')
  if (type === 'conditional') {
    return ruleNode('conditional', {
      args: [
        ruleNode('boolean', { value: 'true' }),
        ruleNode('number', { value: '' }),
        ruleNode('number', { value: '' }),
      ],
    })
  }
  if (type.startsWith('call:')) {
    const name = type.slice(5)
    if (
      ['param', 'header', 'hour', 'minute', 'weekday', 'month', 'day'].includes(
        name
      )
    ) {
      return ruleNode('call', {
        name,
        args: [
          ruleNode('string', {
            value: name === 'param' || name === 'header' ? '' : 'Asia/Shanghai',
          }),
        ],
      })
    }
    if (name === 'tier') {
      return ruleNode('call', {
        name,
        args: [
          ruleNode('string', { value: '' }),
          ruleNode('number', { value: '' }),
        ],
      })
    }
    const args =
      name === 'has'
        ? [ruleNode('string', { value: '' }), ruleNode('string', { value: '' })]
        : [ruleNode('number', { value: '' })]
    if (name === 'max' || name === 'min') {
      args.push(ruleNode('number', { value: '' }))
    }
    return ruleNode('call', { name, args })
  }
  if (type.startsWith('unary:')) {
    return ruleNode('unary', {
      operator: type.slice(6),
      args: [ruleNode('number', { value: '' })],
    })
  }
  if (type.startsWith('binary:')) {
    return ruleNode('binary', {
      operator: type.slice(7),
      args: [
        ruleNode('number', { value: '' }),
        ruleNode('number', { value: '' }),
      ],
    })
  }
  return ruleNode('number', { value: '' })
}

export function PriceMonitorRuleEditor(props: {
  document: RuleDocument
  onChange: (document: RuleDocument) => void
  disabled?: boolean
  onValidityChange?: (valid: boolean) => void
}) {
  const { t } = useTranslation()
  const [adding, setAdding] = useState(false)
  const [impact, setImpact] = useState<{
    document: RuleDocument
    added: RulePriceVariable[]
    removed: string[]
  } | null>(null)
  const [impactPrices, setImpactPrices] = useState<Record<string, string>>({})
  const problems = useMemo(
    () => validateRuleDocument(props.document),
    [props.document]
  )
  const commit = (document: RuleDocument) => {
    props.onChange(document)
    props.onValidityChange?.(validateRuleDocument(document).length === 0)
  }
  const change = (document: RuleDocument) => {
    const before = ruleUsedVariables(props.document.root),
      after = ruleUsedVariables(document.root)
    const categories = ['cr', 'cc', 'cc1h', 'img', 'img_o', 'ai', 'ao']
    const added = categories.filter(
      (name) => after.has(name) && !before.has(name)
    ) as RulePriceVariable[]
    const removed = categories.filter(
      (name) => before.has(name) && !after.has(name)
    )
    if (!added.length && !removed.length) {
      commit(document)
      return
    }
    const prices: Record<string, string> = {}
    for (const leaf of rulePriceLeaves(document.root)) {
      for (const variable of added) {
        prices[`${leaf.node.id}:${variable}`] = leaf.prices[variable] ?? ''
      }
    }
    setImpact({ document, added, removed })
    setImpactPrices(prices)
  }
  const replace = (id: string, node: RuleNode) =>
    change(replaceRuleNode(props.document, id, node))
  const branches: RuleNode[] = []
  let fallback = props.document.root
  while (fallback.kind === 'conditional' && fallback.args?.length === 3) {
    branches.push(fallback)
    const next = fallback.args[2]
    if (!next) break
    fallback = next
  }
  const impactLeaves = impact ? rulePriceLeaves(impact.document.root) : []
  if (
    props.document.schema_version !== 1 ||
    props.document.expression_version !== 1
  ) {
    return (
      <p role='alert'>
        {t(
          'This pricing rule version is not supported. Reload before editing.'
        )}
      </p>
    )
  }
  return (
    <FieldGroup>
      <FieldDescription>
        {t(
          'Rules are checked in order. The first matching price applies; unmatched requests use the base pricing.'
        )}
      </FieldDescription>
      {branches.map((branch, index) => (
        <div key={branch.id} className='rounded-md border p-3'>
          <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
            <p className='text-sm font-medium'>
              {t('Special pricing rule {{number}}', { number: index + 1 })}
            </p>
            <div className='flex flex-wrap gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={props.disabled || index === 0}
                onClick={() => {
                  if (
                    window.confirm(
                      t(
                        'Change rule priority? Earlier matching rules can change the price.'
                      )
                    )
                  ) {
                    change({
                      ...props.document,
                      root: reorderPricingBranches(
                        props.document.root,
                        index,
                        index - 1
                      ),
                    })
                  }
                }}
              >
                {t('Move up')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={props.disabled || index === branches.length - 1}
                onClick={() => {
                  if (
                    window.confirm(
                      t(
                        'Change rule priority? Earlier matching rules can change the price.'
                      )
                    )
                  ) {
                    change({
                      ...props.document,
                      root: reorderPricingBranches(
                        props.document.root,
                        index,
                        index + 1
                      ),
                    })
                  }
                }}
              >
                {t('Move down')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={props.disabled}
                onClick={() => {
                  const condition = branch.args?.[0],
                    price = branch.args?.[1]
                  if (condition && price) {
                    replace(
                      branch.id,
                      ruleNode('conditional', {
                        args: [
                          cloneRuleNode(condition),
                          cloneRuleNode(price),
                          branch,
                        ],
                      })
                    )
                  }
                }}
              >
                {t('Duplicate rule')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={props.disabled}
                onClick={() => {
                  if (
                    window.confirm(
                      t(
                        'Remove this rule? Requests will use the remaining rules or base price.'
                      )
                    ) &&
                    branch.args?.[2]
                  ) {
                    replace(branch.id, branch.args[2])
                  }
                }}
              >
                {t('Remove rule')}
              </Button>
            </div>
          </div>
          <FieldGroup>
            {branch.args?.[0] && (
              <RuleValueEditor
                node={branch.args[0]}
                title={t('When these conditions match')}
                disabled={props.disabled}
                onChange={(node) =>
                  replace(branch.id, {
                    ...branch,
                    args: [
                      node,
                      branch.args?.[1] as RuleNode,
                      branch.args?.[2] as RuleNode,
                    ],
                  })
                }
              />
            )}
            {branch.args?.[1] && (
              <RuleValueEditor
                node={branch.args[1]}
                monetary
                title={t('Use these prices or fee adjustments')}
                disabled={props.disabled}
                onChange={(node) =>
                  replace(branch.id, {
                    ...branch,
                    args: [
                      branch.args?.[0] as RuleNode,
                      node,
                      branch.args?.[2] as RuleNode,
                    ],
                  })
                }
              />
            )}
          </FieldGroup>
        </div>
      ))}
      <RuleValueEditor
        node={fallback}
        monetary
        title={t('Base pricing and fee calculation')}
        disabled={props.disabled}
        onChange={(node) => replace(fallback.id, node)}
      />
      <Button
        variant='outline'
        disabled={props.disabled}
        onClick={() => setAdding(true)}
      >
        {t('Add special pricing rule')}
      </Button>
      {problems.map((problem) => (
        <p key={problem} role='alert' className='text-destructive text-xs'>
          {t(problem)}
        </p>
      ))}
      <Dialog
        open={adding}
        onOpenChange={setAdding}
        title={t('Add special pricing rule')}
        contentClassName='sm:max-w-3xl'
      >
        <NewRuleForm
          original={props.document.root}
          disabled={props.disabled}
          onCreate={(root) => {
            change({ ...props.document, root })
            setAdding(false)
          }}
        />
      </Dialog>
      <Dialog
        open={Boolean(impact)}
        onOpenChange={(open) => {
          if (!open) setImpact(null)
        }}
        title={t('Review token counting changes')}
        footer={
          <>
            <Button variant='outline' onClick={() => setImpact(null)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                props.disabled ||
                Object.values(impactPrices).some(
                  (value) =>
                    !value.trim() ||
                    !Number.isFinite(Number(value)) ||
                    Number(value) < 0
                )
              }
              onClick={() => {
                if (!impact) return
                let document = impact.document
                for (const leaf of impactLeaves) {
                  let node = leaf.node
                  for (const variable of impact.added) {
                    node = updatePriceItem(
                      node,
                      variable,
                      impactPrices[`${leaf.node.id}:${variable}`] ?? ''
                    )
                  }
                  document = replaceRuleNode(document, leaf.node.id, node)
                }
                commit(document)
                setImpact(null)
              }}
            >
              {t('Apply reviewed pricing')}
            </Button>
          </>
        }
      >
        <FieldGroup>
          <p className='text-sm'>
            {t(
              'Adding or removing cache or media pricing changes token counting across the entire rule, including conditions. Compare OpenAI and Claude examples before saving.'
            )}
          </p>
          {impact?.removed.map((variable) => (
            <p key={variable} className='text-sm'>
              {t('No longer counted separately')}:{' '}
              {t(RULE_USAGE_LABELS[variable as keyof typeof RULE_USAGE_LABELS])}
            </p>
          ))}
          {impactLeaves.map((leaf, index) => (
            <div key={leaf.node.id} className='rounded-md border p-3'>
              <p className='mb-2 text-sm font-medium'>
                {leaf.label ||
                  t('Price calculation {{number}}', { number: index + 1 })}
              </p>
              {impact?.added.map((variable) => (
                <Field key={variable}>
                  <FieldLabel>
                    {t(RULE_PRICE_LABELS[variable])} ·{' '}
                    {t('USD per million tokens')}
                  </FieldLabel>
                  <Input
                    aria-label={`${leaf.label || t('Price calculation {{number}}', { number: index + 1 })} ${t(RULE_PRICE_LABELS[variable])}`}
                    value={impactPrices[`${leaf.node.id}:${variable}`] ?? ''}
                    onChange={(event) =>
                      setImpactPrices((current) => ({
                        ...current,
                        [`${leaf.node.id}:${variable}`]: event.target.value,
                      }))
                    }
                  />
                </Field>
              ))}
            </div>
          ))}
        </FieldGroup>
      </Dialog>
    </FieldGroup>
  )
}

type RuleValueProps = {
  node: RuleNode
  title?: string
  disabled?: boolean
  monetary?: boolean
  onChange: (node: RuleNode) => void
}

export function RuleValueEditor(props: RuleValueProps) {
  const { t } = useTranslation()
  const node = props.node
  const choices: Choice[] = [
    { value: 'number', label: t('Numeric value') },
    { value: 'string', label: t('Text value') },
    { value: 'boolean', label: t('True or false') },
    { value: 'null', label: t('Empty or missing value') },
    { value: 'usage', label: t('Usage quantity') },
    { value: 'conditional', label: t('Choose an amount based on conditions') },
    ...RULE_BINARY_ACTIONS.map(([value, label]) => ({
      value: `binary:${value}`,
      label: t(label),
    })),
    ...RULE_CALL_ACTIONS.map(([value, label]) => ({
      value: `call:${value}`,
      label: t(label),
    })),
    { value: 'unary:-', label: t('Negate this amount') },
    { value: 'unary:+', label: t('Keep this amount unchanged') },
    { value: 'unary:not', label: t('Condition must not match') },
    { value: 'unary:!', label: t('Condition must not match') },
  ]
  let selection: string = node.kind
  if (node.kind === 'call') selection = `call:${node.name}`
  if (node.kind === 'binary') selection = `binary:${node.operator}`
  if (node.kind === 'unary') selection = `unary:${node.operator}`
  return (
    <div className='rounded-md border p-3'>
      {props.title && <p className='mb-3 text-sm font-medium'>{props.title}</p>}
      <RuleNodeFields {...props} />
      <details className='mt-3 rounded-md border p-3'>
        <summary className='cursor-pointer text-xs'>
          {t('More calculation options')}
        </summary>
        <FieldGroup className='mt-3'>
          <RuleChoice
            label={t('Choose a value or calculation action')}
            value={selection}
            options={choices}
            disabled={props.disabled}
            onChange={(type) => {
              if (type === selection) return
              if (
                window.confirm(
                  t(
                    'Replace this calculation? The existing values in this step will be replaced.'
                  )
                )
              ) {
                props.onChange(blankNode(type))
              }
            }}
          />
          <Button
            size='sm'
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              props.onChange(
                ruleNode('binary', {
                  operator: '+',
                  args: [node, cloneRuleNode(node)],
                })
              )
            }
          >
            {t('Duplicate and add this amount')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={props.disabled || !node.args?.length}
            onClick={() => {
              if (
                node.args?.[0] &&
                window.confirm(
                  t('Remove this operation and keep its first value?')
                )
              ) {
                props.onChange(node.args[0])
              }
            }}
          >
            {t('Remove operation')}
          </Button>
        </FieldGroup>
      </details>
    </div>
  )
}

function RuleNodeFields(props: RuleValueProps) {
  const { t } = useTranslation()
  const id = useId()
  const node = props.node
  const prices = extractPriceItems(node)
  const [priceItem, setPriceItem] = useState<RulePriceVariable>('cr')
  const [moneyBuffer, setMoneyBuffer] = useState<string | undefined>()
  const lastMoneyValue = useRef(node.value)
  useEffect(() => {
    if (node.value !== lastMoneyValue.current) setMoneyBuffer(undefined)
    lastMoneyValue.current = node.value
  }, [node.value])
  const updateArgument = (index: number, child: RuleNode) =>
    props.onChange({
      ...node,
      args: node.args?.map((argument, position) =>
        position === index ? child : argument
      ),
    })
  if (prices) {
    return (
      <FieldGroup>
        <p className='text-muted-foreground text-xs'>
          {t('USD per million tokens')}
        </p>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Price item')}</TableHead>
              <TableHead>{t('Price')}</TableHead>
              <TableHead>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {Object.entries(prices).map(([variable, value]) => (
              <TableRow key={variable}>
                <TableCell>
                  {t(RULE_PRICE_LABELS[variable as RulePriceVariable])}
                </TableCell>
                <TableCell>
                  <Input
                    aria-label={t(
                      RULE_PRICE_LABELS[variable as RulePriceVariable]
                    )}
                    value={value}
                    disabled={props.disabled}
                    onChange={(event) =>
                      props.onChange(
                        updatePriceItem(
                          node,
                          variable as RulePriceVariable,
                          event.target.value
                        )
                      )
                    }
                  />
                </TableCell>
                <TableCell>
                  <Button
                    variant='link'
                    size='sm'
                    disabled={props.disabled || Object.keys(prices).length <= 1}
                    onClick={() => {
                      if (
                        !window.confirm(
                          t(
                            'Remove this price item? Token counting and the remaining prices may change.'
                          )
                        )
                      ) {
                        return
                      }
                      const remaining = { ...prices }
                      delete remaining[variable as RulePriceVariable]
                      try {
                        props.onChange(createFlatPriceNode(remaining))
                      } catch (error) {
                        toast.error(
                          error instanceof Error
                            ? t(error.message)
                            : t('Enter a valid price')
                        )
                      }
                    }}
                  >
                    {t('Remove price item')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <div className='flex flex-wrap items-end gap-2'>
          <RuleChoice
            label={t('Add price item')}
            value={priceItem}
            options={Object.entries(RULE_PRICE_LABELS).map(
              ([value, label]) => ({ value, label: t(label) })
            )}
            disabled={props.disabled}
            onChange={(value) => setPriceItem(value as RulePriceVariable)}
          />
          <Button
            variant='outline'
            disabled={props.disabled || prices[priceItem] !== undefined}
            onClick={() => props.onChange(updatePriceItem(node, priceItem, ''))}
          >
            {t('Add')}
          </Button>
        </div>
      </FieldGroup>
    )
  }
  if (node.kind === 'number' || node.kind === 'string') {
    let label = node.kind === 'number' ? t('Numeric value') : t('Text value')
    let value = node.value ?? ''
    if (node.kind === 'number' && props.monetary) {
      label = t('USD per request')
      value =
        value.trim() && Number.isFinite(Number(value))
          ? String(Number((Number(value) / 1_000_000).toPrecision(15)))
          : value
      value = moneyBuffer ?? value
    }
    if (node.kind === 'string') {
      return (
        <Field>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          <Textarea
            id={id}
            value={value}
            disabled={props.disabled}
            onChange={(event) =>
              props.onChange({ ...node, value: event.target.value })
            }
          />
        </Field>
      )
    }
    return (
      <Field>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <Input
          id={id}
          value={value}
          disabled={props.disabled}
          inputMode={node.kind === 'number' ? 'decimal' : undefined}
          onChange={(event) => {
            let next = event.target.value
            if (props.monetary && node.kind === 'number') setMoneyBuffer(next)
            if (
              props.monetary &&
              node.kind === 'number' &&
              next.trim() &&
              Number.isFinite(Number(next))
            ) {
              next = String(Number((Number(next) * 1_000_000).toPrecision(15)))
            }
            lastMoneyValue.current = next
            props.onChange({ ...node, value: next })
          }}
        />
        {node.kind === 'number' &&
          (!node.value?.trim() || !Number.isFinite(Number(node.value))) && (
            <p className='text-destructive text-xs'>
              {t('Enter a valid amount')}
            </p>
          )}
      </Field>
    )
  }
  if (node.kind === 'boolean') {
    return (
      <RuleChoice
        label={t('True or false')}
        value={node.value ?? 'true'}
        options={[
          { value: 'true', label: t('True') },
          { value: 'false', label: t('False') },
        ]}
        disabled={props.disabled}
        onChange={(value) => props.onChange({ ...node, value })}
      />
    )
  }
  if (node.kind === 'null') {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('Empty or missing value')}
      </p>
    )
  }
  if (node.kind === 'usage') {
    return (
      <RuleChoice
        label={t('Usage quantity')}
        value={node.name ?? 'len'}
        options={RULE_USAGE_VARIABLES.map((value) => ({
          value,
          label: t(RULE_USAGE_LABELS[value]),
        }))}
        disabled={props.disabled}
        onChange={(name) => props.onChange({ ...node, name })}
      />
    )
  }
  if (node.kind === 'conditional') {
    return (
      <FieldGroup>
        <p className='text-sm font-medium'>
          {t('Choose pricing based on conditions')}
        </p>
        {node.args?.map((child, index) => (
          <RuleValueEditor
            key={child.id}
            node={child}
            monetary={index > 0}
            title={t(
              [
                'When these conditions match',
                'When matched, calculate this amount',
                'Otherwise, calculate this amount',
              ][index] ?? 'Calculation value'
            )}
            disabled={props.disabled}
            onChange={(value) => updateArgument(index, value)}
          />
        ))}
        <Button
          variant='outline'
          disabled={props.disabled}
          onClick={() => {
            if (
              window.confirm(
                t(
                  'Swap matched and unmatched prices? This changes which requests use each price.'
                )
              )
            ) {
              props.onChange(moveRuleChild(node, 1, 2))
            }
          }}
        >
          {t('Swap matched and unmatched prices')}
        </Button>
      </FieldGroup>
    )
  }
  if (node.kind === 'call' && node.name === 'tier') {
    return (
      <FieldGroup>
        <p className='text-sm font-medium'>{t('Pricing branch')}</p>
        {node.args?.[0] && (
          <RuleValueEditor
            node={node.args[0]}
            title={t('Rule name shown in usage records')}
            disabled={props.disabled}
            onChange={(child) => updateArgument(0, child)}
          />
        )}
        {node.args?.[1] && (
          <RuleValueEditor
            node={node.args[1]}
            monetary
            title={t('Prices and fee calculation')}
            disabled={props.disabled}
            onChange={(child) => updateArgument(1, child)}
          />
        )}
      </FieldGroup>
    )
  }
  if (
    node.kind === 'binary' &&
    ['and', '&&', 'or', '||'].includes(node.operator ?? '')
  ) {
    const all = node.operator === 'and' || node.operator === '&&'
    const newCondition = () =>
      ruleNode('binary', {
        operator: '>=',
        args: [
          ruleNode('usage', { name: 'len' }),
          ruleNode('number', { value: '' }),
        ],
      })
    return (
      <FieldGroup>
        <RuleChoice
          label={t('Condition group')}
          value={all ? 'and' : 'or'}
          options={[
            { value: 'and', label: t('All conditions must match') },
            { value: 'or', label: t('Any condition may match') },
          ]}
          disabled={props.disabled}
          onChange={(operator) => {
            if (operator !== (all ? 'and' : 'or')) {
              props.onChange({ ...node, operator })
            }
          }}
        />
        {node.args?.map((child, index) => (
          <div key={child.id}>
            <RuleValueEditor
              node={child}
              title={t('Condition {{number}}', { number: index + 1 })}
              disabled={props.disabled}
              onChange={(next) => updateArgument(index, next)}
            />
            <Button
              variant='link'
              size='sm'
              disabled={props.disabled}
              onClick={() => {
                const remaining = node.args?.[1 - index]
                if (
                  remaining &&
                  window.confirm(
                    t('Remove this condition and keep the other condition?')
                  )
                ) {
                  props.onChange(remaining)
                }
              }}
            >
              {t('Remove condition')}
            </Button>
          </div>
        ))}
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              props.onChange(
                ruleNode('binary', {
                  operator: node.operator,
                  args: [node, newCondition()],
                })
              )
            }
          >
            {t('Add condition')}
          </Button>
          <Button
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              props.onChange(
                ruleNode('binary', {
                  operator: node.operator,
                  args: [
                    node,
                    ruleNode('binary', {
                      operator: 'and',
                      args: [newCondition(), newCondition()],
                    }),
                  ],
                })
              )
            }
          >
            {t('Add nested condition group')}
          </Button>
          <Button
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              props.onChange(
                ruleNode('unary', { operator: 'not', args: [node] })
              )
            }
          >
            {t('Invert this condition group')}
          </Button>
        </div>
      </FieldGroup>
    )
  }
  let operation = 'Calculation step'
  if (node.kind === 'binary') {
    operation =
      RULE_BINARY_ACTIONS.find(([value]) => value === node.operator)?.[1] ??
      operation
  }
  if (node.kind === 'call') {
    operation =
      RULE_CALL_ACTIONS.find(([value]) => value === node.name)?.[1] ?? operation
  }
  if (node.kind === 'unary') {
    operation = 'Condition must not match'
    if (node.operator === '-') operation = 'Negate this amount'
    if (node.operator === '+') operation = 'Keep this amount unchanged'
  }
  if (
    node.kind === 'call' &&
    ['param', 'header'].includes(node.name ?? '') &&
    node.args?.[0]?.kind === 'string'
  ) {
    return (
      <Field>
        <FieldLabel htmlFor={id}>
          {node.name === 'param'
            ? t('Parameter name or nested path')
            : t('Request header name')}
        </FieldLabel>
        <Input
          id={id}
          value={node.args[0].value ?? ''}
          disabled={props.disabled}
          onChange={(event) =>
            updateArgument(0, {
              ...(node.args?.[0] as RuleNode),
              value: event.target.value,
            })
          }
        />
      </Field>
    )
  }
  const bound =
    props.monetary &&
    node.kind === 'call' &&
    ['max', 'min'].includes(node.name ?? '') &&
    node.args?.some(
      (child) =>
        child.kind === 'number' ||
        (child.kind === 'unary' &&
          ['+', '-'].includes(child.operator ?? '') &&
          child.args?.[0]?.kind === 'number')
    )
  const amountArguments =
    props.monetary &&
    ((node.kind === 'binary' && ['+', '-'].includes(node.operator ?? '')) ||
      (node.kind === 'call' &&
        ['max', 'min', 'abs', 'ceil', 'floor'].includes(node.name ?? '')) ||
      node.kind === 'unary')
  if (bound) {
    operation =
      node.name === 'max'
        ? 'Minimum charge per request'
        : 'Maximum charge per request'
  }
  return (
    <FieldGroup>
      <p className='text-sm font-medium'>{t(operation)}</p>
      {props.monetary &&
        node.kind === 'call' &&
        ['ceil', 'floor'].includes(node.name ?? '') && (
          <FieldDescription>
            {t(
              'This step rounds in increments of one-millionth of a dollar per request.'
            )}
          </FieldDescription>
        )}
      {node.args?.map((child, index) => (
        <div key={child.id}>
          <RuleValueEditor
            node={child}
            monetary={
              amountArguments ||
              (props.monetary &&
                node.kind === 'binary' &&
                ['*', '/'].includes(node.operator ?? '') &&
                index === 0)
            }
            title={
              node.kind === 'call' &&
              ['hour', 'minute', 'weekday', 'month', 'day'].includes(
                node.name ?? ''
              )
                ? t('Time zone')
                : t('Calculation value {{number}}', { number: index + 1 })
            }
            disabled={props.disabled}
            onChange={(value) => updateArgument(index, value)}
          />
          {node.args?.length === 2 && (
            <Button
              size='sm'
              variant='link'
              disabled={props.disabled}
              onClick={() => {
                if (
                  window.confirm(
                    t(
                      'Swap calculation order? This can change the result or which conditions run first.'
                    )
                  )
                ) {
                  props.onChange(moveRuleChild(node, index, 1 - index))
                }
              }}
            >
              {t('Swap calculation order')}
            </Button>
          )}
        </div>
      ))}
    </FieldGroup>
  )
}

function NewRuleForm(props: {
  original: RuleNode
  disabled?: boolean
  onCreate: (root: RuleNode) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [kind, setKind] = useState('adjust')
  const [source, setSource] = useState('length')
  const [name, setName] = useState('')
  const [operator, setOperator] = useState('==')
  const [valueType, setValueType] = useState('string')
  const [value, setValue] = useState('')
  const [amount, setAmount] = useState('')
  const [direction, setDirection] = useState('decrease')
  const [timezone, setTimezone] = useState('Asia/Shanghai')
  const [customTimezone, setCustomTimezone] = useState(false)
  const [start, setStart] = useState('09:00')
  const [end, setEnd] = useState('18:00')
  const [weekdays, setWeekdays] = useState([1, 2, 3, 4, 5])
  const [inputPrice, setInputPrice] = useState('')
  const [outputPrice, setOutputPrice] = useState('')
  const weekdayLabels = [
    'Sunday',
    'Monday',
    'Tuesday',
    'Wednesday',
    'Thursday',
    'Friday',
    'Saturday',
  ]
  const numeric = (text: string): RuleNode => {
    if (
      !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(text.trim()) ||
      !Number.isFinite(Number(text))
    ) {
      throw new Error('Enter a valid amount')
    }
    return ruleNode('number', { value: text.trim() })
  }
  const condition = (): RuleNode => {
    if (source === 'time') {
      return createTimeCondition({ timezone, start, end, weekdays })
    }
    let left: RuleNode
    if (source === 'length') left = ruleNode('usage', { name: 'len' })
    else {
      if (!name.trim()) throw new Error('Enter a parameter or header name')
      left = ruleNode('call', {
        name: source === 'header' ? 'header' : 'param',
        args: [ruleNode('string', { value: name.trim() })],
      })
    }
    if (operator === 'exists') {
      return ruleNode('binary', {
        operator: '!=',
        args: [
          left,
          source === 'header'
            ? ruleNode('string', { value: '' })
            : ruleNode('null'),
        ],
      })
    }
    let right: RuleNode = ruleNode('string', { value })
    if (valueType === 'null') right = ruleNode('null')
    if (source === 'length' || valueType === 'number') right = numeric(value)
    if (valueType === 'boolean') {
      if (value !== 'true' && value !== 'false') {
        throw new Error('Choose true or false')
      }
      right = ruleNode('boolean', { value })
    }
    if (source === 'param' && operator === 'contains') {
      return ruleNode('call', { name: 'has', args: [left, right] })
    }
    const comparison = ruleNode('binary', { operator, args: [left, right] })
    if (
      source === 'param' &&
      ['>', '>=', '<', '<=', 'startsWith', 'endsWith'].includes(operator)
    ) {
      return ruleNode('binary', {
        operator: 'and',
        args: [
          ruleNode('binary', {
            operator: '!=',
            args: [cloneRuleNode(left), ruleNode('null')],
          }),
          comparison,
        ],
      })
    }
    return comparison
  }
  let amountLabel = t('USD per request')
  if (kind === 'adjust') amountLabel = t('Percentage')
  if (kind === 'calculation') amountLabel = t('Numeric value')
  return (
    <FieldGroup>
      <RuleChoice
        label={t('What should this rule do?')}
        value={kind}
        disabled={props.disabled}
        onChange={setKind}
        options={[
          { value: 'adjust', label: t('Adjust fees when conditions match') },
          {
            value: 'prices',
            label: t('Use different prices when conditions match'),
          },
          { value: 'minimum', label: t('Set a minimum charge per request') },
          { value: 'maximum', label: t('Cap the charge per request') },
          { value: 'calculation', label: t('Add another calculation step') },
        ]}
      />
      {(kind === 'adjust' || kind === 'prices') && (
        <>
          <RuleChoice
            label={t('Match requests by')}
            value={source}
            disabled={props.disabled}
            onChange={(selected) => {
              setSource(selected)
              if (selected === 'length') {
                setValueType('number')
                setOperator('>')
              } else {
                setValueType('string')
                setOperator('==')
              }
            }}
            options={[
              { value: 'length', label: t('Total input context length') },
              { value: 'time', label: t('Time period and weekdays') },
              { value: 'param', label: t('Request parameter') },
              { value: 'header', label: t('Request header') },
            ]}
          />
          {source === 'time' ? (
            <>
              <RuleChoice
                label={t('Time zone')}
                value={customTimezone ? 'custom' : timezone}
                disabled={props.disabled}
                options={RULE_TIME_ZONES.map(([value, label]) => ({
                  value,
                  label: t(label),
                }))}
                onChange={(next) => {
                  setCustomTimezone(next === 'custom')
                  if (next !== 'custom') setTimezone(next)
                }}
              />
              {customTimezone && (
                <Field>
                  <FieldLabel htmlFor={`${id}-timezone`}>
                    {t('Custom time zone')}
                  </FieldLabel>
                  <Input
                    id={`${id}-timezone`}
                    value={timezone}
                    disabled={props.disabled}
                    onChange={(event) => setTimezone(event.target.value)}
                  />
                </Field>
              )}
              <div className='grid gap-3 sm:grid-cols-2'>
                <Field>
                  <FieldLabel htmlFor={`${id}-start`}>
                    {t('Start time')}
                  </FieldLabel>
                  <Input
                    id={`${id}-start`}
                    type='time'
                    value={start}
                    disabled={props.disabled}
                    onChange={(event) => setStart(event.target.value)}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-end`}>{t('End time')}</FieldLabel>
                  <Input
                    id={`${id}-end`}
                    type='time'
                    value={end}
                    disabled={props.disabled}
                    onChange={(event) => setEnd(event.target.value)}
                  />
                </Field>
              </div>
              <div className='flex flex-wrap gap-3'>
                {weekdayLabels.map((label, day) => (
                  <Field
                    key={label}
                    orientation='horizontal'
                    className='w-auto'
                  >
                    <Checkbox
                      id={`${id}-day-${day}`}
                      checked={weekdays.includes(day)}
                      disabled={props.disabled}
                      onCheckedChange={(checked) =>
                        setWeekdays((current) =>
                          checked === true
                            ? [...current, day]
                            : current.filter((item) => item !== day)
                        )
                      }
                    />
                    <FieldLabel htmlFor={`${id}-day-${day}`}>
                      {t(label)}
                    </FieldLabel>
                  </Field>
                ))}
              </div>
              <FieldDescription>
                {t(
                  'Weekdays refer to the start of the period. Overnight periods continue into the following day; equal start and end times cover the whole day.'
                )}
              </FieldDescription>
            </>
          ) : (
            <>
              {(source === 'param' || source === 'header') && (
                <Field>
                  <FieldLabel htmlFor={`${id}-field-name`}>
                    {source === 'param'
                      ? t('Parameter name or nested path')
                      : t('Request header name')}
                  </FieldLabel>
                  <Input
                    id={`${id}-field-name`}
                    value={name}
                    disabled={props.disabled}
                    onChange={(event) => setName(event.target.value)}
                  />
                </Field>
              )}
              <RuleChoice
                label={t('How should it match?')}
                value={operator}
                disabled={props.disabled}
                onChange={(next) => {
                  setOperator(next)
                  if (['contains', 'startsWith', 'endsWith'].includes(next)) {
                    setValueType('string')
                  }
                }}
                options={[
                  { value: '==', label: t('Equals') },
                  { value: '!=', label: t('Does not equal') },
                  { value: '>', label: t('Greater than') },
                  { value: '>=', label: t('At least') },
                  { value: '<', label: t('Less than') },
                  { value: '<=', label: t('At most') },
                  { value: 'contains', label: t('Contains text') },
                  { value: 'startsWith', label: t('Starts with text') },
                  { value: 'endsWith', label: t('Ends with text') },
                  {
                    value: 'exists',
                    label:
                      source === 'header' ? t('Is not empty') : t('Exists'),
                  },
                ].filter(
                  (choice) =>
                    source !== 'length' ||
                    ['==', '!=', '>', '>=', '<', '<='].includes(choice.value)
                )}
              />
              {source === 'param' && operator !== 'exists' && (
                <RuleChoice
                  label={t('Value type')}
                  value={valueType}
                  disabled={props.disabled}
                  onChange={setValueType}
                  options={[
                    { value: 'string', label: t('Text value') },
                    { value: 'number', label: t('Numeric value') },
                    { value: 'boolean', label: t('True or false') },
                    { value: 'null', label: t('Empty or missing value') },
                  ].filter(
                    (choice) =>
                      !['contains', 'startsWith', 'endsWith'].includes(
                        operator
                      ) || choice.value === 'string'
                  )}
                />
              )}
              {operator !== 'exists' && valueType === 'boolean' && (
                <RuleChoice
                  label={t('Match value')}
                  value={value || 'unset'}
                  disabled={props.disabled}
                  onChange={(next) => setValue(next === 'unset' ? '' : next)}
                  options={[
                    { value: 'unset', label: t('Choose true or false') },
                    { value: 'true', label: t('True') },
                    { value: 'false', label: t('False') },
                  ]}
                />
              )}
              {operator !== 'exists' &&
                valueType !== 'null' &&
                valueType !== 'boolean' && (
                  <Field>
                    <FieldLabel htmlFor={`${id}-match-value`}>
                      {t('Match value')}
                    </FieldLabel>
                    <Input
                      id={`${id}-match-value`}
                      value={value}
                      disabled={props.disabled}
                      onChange={(event) => setValue(event.target.value)}
                    />
                  </Field>
                )}
              {source === 'header' && (
                <FieldDescription>
                  {t(
                    'Headers are text values. An empty header is treated like a missing header; use request parameters for numeric or boolean comparisons.'
                  )}
                </FieldDescription>
              )}
            </>
          )}
        </>
      )}
      {kind === 'prices' && (
        <div className='grid gap-3 sm:grid-cols-2'>
          <Field>
            <FieldLabel htmlFor={`${id}-input-price`}>
              {t('Input price')} · {t('USD per million tokens')}
            </FieldLabel>
            <Input
              id={`${id}-input-price`}
              value={inputPrice}
              disabled={props.disabled}
              onChange={(event) => setInputPrice(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-output-price`}>
              {t('Completion price')} · {t('USD per million tokens')}
            </FieldLabel>
            <Input
              id={`${id}-output-price`}
              value={outputPrice}
              disabled={props.disabled}
              onChange={(event) => setOutputPrice(event.target.value)}
            />
          </Field>
        </div>
      )}
      {kind === 'adjust' && (
        <RuleChoice
          label={t('Fee adjustment')}
          value={direction}
          disabled={props.disabled}
          onChange={setDirection}
          options={[
            { value: 'increase', label: t('Increase by percent') },
            { value: 'decrease', label: t('Decrease by percent') },
          ]}
        />
      )}
      {kind !== 'prices' && (
        <Field>
          <FieldLabel htmlFor={`${id}-amount`}>{amountLabel}</FieldLabel>
          <Input
            id={`${id}-amount`}
            value={amount}
            disabled={props.disabled}
            onChange={(event) => setAmount(event.target.value)}
          />
        </Field>
      )}
      <Button
        disabled={props.disabled}
        onClick={() => {
          try {
            let root: RuleNode
            if (kind === 'adjust') {
              root = createConditionalAdjustment(
                props.original,
                condition(),
                amount,
                direction as 'increase' | 'decrease'
              )
            } else if (kind === 'prices') {
              root = ruleNode('conditional', {
                args: [
                  condition(),
                  createFlatPriceNode({ p: inputPrice, c: outputPrice }),
                  props.original,
                ],
              })
            } else if (kind === 'minimum' || kind === 'maximum') {
              const price = numeric(amount)
              if (
                Number(price.value) < 0 ||
                !Number.isFinite(Number(price.value) * 1_000_000)
              ) {
                throw new Error('Enter a valid price')
              }
              root = ruleNode('call', {
                name: kind === 'minimum' ? 'max' : 'min',
                args: [
                  props.original,
                  ruleNode('number', {
                    value: String(
                      Number((Number(price.value) * 1_000_000).toPrecision(15))
                    ),
                  }),
                ],
              })
            } else {
              root = ruleNode('binary', {
                operator: '*',
                args: [props.original, numeric(amount)],
              })
            }
            props.onCreate(root)
          } catch (error) {
            toast.error(
              error instanceof Error
                ? t(error.message)
                : t('Check the rule values and try again')
            )
          }
        }}
      >
        {t('Add rule')}
      </Button>
    </FieldGroup>
  )
}
