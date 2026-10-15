/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type {
  RuleDocument,
  RuleNode,
  RulePriceVariable,
} from './price-monitor-rule-types'

let nextRuleID = 0
export function ruleNode(
  kind: RuleNode['kind'],
  values: Omit<Partial<RuleNode>, 'id' | 'kind'> = {}
): RuleNode {
  nextRuleID++
  return { id: `edit:${Date.now()}:${nextRuleID}`, kind, ...values }
}

export function cloneRuleNode(node: RuleNode): RuleNode {
  return ruleNode(node.kind, {
    value: node.value,
    operator: node.operator,
    name: node.name,
    args: node.args?.map(cloneRuleNode),
  })
}

export function validateRuleDocument(document: RuleDocument): string[] {
  const errors: string[] = [],
    ids = new Set<string>()
  let count = 0
  const visit = (node: RuleNode, depth: number) => {
    count++
    if (depth > 64 || count > 4096) {
      errors.push(
        'This pricing rule is too complex. Remove a rule or simplify its conditions.'
      )
      return
    }
    if (!node.id || ids.has(node.id)) {
      errors.push(
        'The pricing rules could not be loaded. Reload the model and try again.'
      )
    }
    ids.add(node.id)
    if (
      node.kind === 'number' &&
      (!/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(
        node.value?.trim() ?? ''
      ) ||
        !Number.isFinite(Number(node.value)))
    ) {
      errors.push('Enter a valid amount')
    }
    node.args?.forEach((child) => visit(child, depth + 1))
  }
  visit(document.root, 0)
  return [...new Set(errors)]
}

export function replaceRuleNode(
  document: RuleDocument,
  id: string,
  replacement: RuleNode
): RuleDocument {
  const visit = (node: RuleNode): RuleNode => {
    if (node.id === id) return replacement
    const args = node.args?.map(visit)
    if (!args || args.every((child, index) => child === node.args?.[index])) {
      return node
    }
    return { ...node, args }
  }
  return { ...document, root: visit(document.root) }
}

export function moveRuleChild(
  node: RuleNode,
  from: number,
  to: number
): RuleNode {
  if (
    !node.args ||
    from < 0 ||
    to < 0 ||
    from >= node.args.length ||
    to >= node.args.length
  ) {
    throw new Error('Choose a valid rule position')
  }
  const args = [...node.args]
  const child = args.splice(from, 1)[0]
  if (!child) throw new Error('Choose a valid rule position')
  args.splice(to, 0, child)
  return { ...node, args }
}

export function createFlatPriceNode(
  prices: Partial<Record<RulePriceVariable, string>>
): RuleNode {
  const terms: RuleNode[] = []
  for (const [variable, value] of Object.entries(prices)) {
    if (
      !/^(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(value.trim()) ||
      !Number.isFinite(Number(value)) ||
      Number(value) < 0
    ) {
      throw new Error('Enter a valid price')
    }
    terms.push(
      ruleNode('binary', {
        operator: '*',
        args: [
          ruleNode('usage', { name: variable }),
          ruleNode('number', { value }),
        ],
      })
    )
  }
  if (!terms.length) throw new Error('Add at least one price item')
  return terms
    .slice(1)
    .reduce(
      (left, right) =>
        ruleNode('binary', { operator: '+', args: [left, right] }),
      terms[0] as RuleNode
    )
}

export function ruleUsedVariables(node: RuleNode): Set<string> {
  const variables = new Set<string>()
  const visit = (item: RuleNode) => {
    if (item.kind === 'usage' && item.name) variables.add(item.name)
    item.args?.forEach(visit)
  }
  visit(node)
  return variables
}

export function rulePriceLeaves(node: RuleNode): {
  node: RuleNode
  label: string
  prices: Partial<Record<RulePriceVariable, string>>
}[] {
  const leaves: {
    node: RuleNode
    label: string
    prices: Partial<Record<RulePriceVariable, string>>
  }[] = []
  const visit = (item: RuleNode, label: string) => {
    const prices = extractPriceItems(item)
    if (prices) {
      leaves.push({ node: item, label, prices })
      return
    }
    if (
      item.kind === 'call' &&
      item.name === 'tier' &&
      item.args?.[0]?.kind === 'string'
    ) {
      if (item.args[1]) visit(item.args[1], item.args[0].value ?? '')
      return
    }
    if (item.kind === 'conditional') {
      if (item.args?.[1]) visit(item.args[1], label)
      if (item.args?.[2]) visit(item.args[2], label)
      return
    }
    if (
      item.kind === 'binary' &&
      [
        '==',
        '!=',
        '<',
        '<=',
        '>',
        '>=',
        'and',
        'or',
        '&&',
        '||',
        'contains',
        'startsWith',
        'endsWith',
      ].includes(item.operator ?? '')
    ) {
      return
    }
    item.args?.forEach((child) => visit(child, label))
  }
  visit(node, '')
  return leaves
}

export function reorderPricingBranches(
  root: RuleNode,
  from: number,
  to: number
): RuleNode {
  const branches: RuleNode[] = []
  let fallback = root
  while (fallback.kind === 'conditional' && fallback.args?.length === 3) {
    branches.push(fallback)
    const next = fallback.args[2]
    if (!next) break
    fallback = next
  }
  if (from < 0 || to < 0 || from >= branches.length || to >= branches.length) {
    throw new Error('Choose a valid rule position')
  }
  const moved = branches.splice(from, 1)[0]
  if (!moved) throw new Error('Choose a valid rule position')
  branches.splice(to, 0, moved)
  return branches.reduceRight(
    (tail, branch) => ({
      ...branch,
      args: [branch.args?.[0] as RuleNode, branch.args?.[1] as RuleNode, tail],
    }),
    fallback
  )
}

export function extractPriceItems(
  node: RuleNode
): Partial<Record<RulePriceVariable, string>> | null {
  const prices: Partial<Record<RulePriceVariable, string>> = {}
  const visit = (item: RuleNode): boolean => {
    if (
      item.kind === 'binary' &&
      item.operator === '+' &&
      item.args?.length === 2
    ) {
      return item.args.every(visit)
    }
    if (
      item.kind !== 'binary' ||
      item.operator !== '*' ||
      item.args?.length !== 2
    ) {
      return false
    }
    const variable = item.args.find((child) => child.kind === 'usage')
    const amount = item.args.find((child) => child.kind === 'number')
    if (
      !variable?.name ||
      variable.name === 'len' ||
      amount?.value === undefined ||
      prices[variable.name as RulePriceVariable] !== undefined
    ) {
      return false
    }
    prices[variable.name as RulePriceVariable] = amount.value
    return true
  }
  return visit(node) ? prices : null
}

export function updatePriceItem(
  node: RuleNode,
  variable: RulePriceVariable,
  value: string
): RuleNode {
  const prices = extractPriceItems(node)
  if (!prices) {
    throw new Error('Choose a price calculation before editing this item')
  }
  let updated = false
  const visit = (item: RuleNode): RuleNode => {
    if (
      item.kind === 'binary' &&
      item.operator === '*' &&
      item.args?.some(
        (child) => child.kind === 'usage' && child.name === variable
      )
    ) {
      updated = true
      return {
        ...item,
        args: item.args.map((child) =>
          child.kind === 'number' ? { ...child, value } : child
        ),
      }
    }
    return item.args ? { ...item, args: item.args.map(visit) } : item
  }
  const next = visit(node)
  if (updated) return next
  return ruleNode('binary', {
    operator: '+',
    args: [
      node,
      ruleNode('binary', {
        operator: '*',
        args: [
          ruleNode('usage', { name: variable }),
          ruleNode('number', { value }),
        ],
      }),
    ],
  })
}

export function collectRuleRequestFields(document: RuleDocument): {
  params: string[]
  headers: string[]
} {
  const params = new Set<string>(),
    headers = new Set<string>()
  const visit = (node: RuleNode) => {
    if (node.kind === 'call' && node.args?.[0]?.kind === 'string') {
      const value = node.args[0].value
      if (value && node.name === 'param') params.add(value)
      if (value && node.name === 'header') headers.add(value)
    }
    node.args?.forEach(visit)
  }
  visit(document.root)
  return { params: [...params], headers: [...headers] }
}

export function createConditionalAdjustment(
  original: RuleNode,
  condition: RuleNode,
  percent: string,
  direction: 'increase' | 'decrease'
): RuleNode {
  const value = Number(percent)
  if (
    !percent.trim() ||
    !Number.isFinite(value) ||
    value < 0 ||
    (direction === 'decrease' && value > 100)
  ) {
    throw new Error('Enter a valid percentage')
  }
  const factor = Number(
    (1 + (direction === 'decrease' ? -value : value) / 100).toPrecision(15)
  )
  return ruleNode('conditional', {
    args: [
      condition,
      ruleNode('binary', {
        operator: '*',
        args: [
          cloneRuleNode(original),
          ruleNode('number', { value: String(factor) }),
        ],
      }),
      original,
    ],
  })
}

export function createTimeCondition(values: {
  timezone: string
  start: string
  end: string
  weekdays: number[]
}): RuleNode {
  if (
    !values.timezone.trim() ||
    !values.weekdays.length ||
    values.weekdays.some((day) => !Number.isInteger(day) || day < 0 || day > 6)
  ) {
    throw new Error('Choose a time zone and valid weekdays')
  }
  try {
    new Intl.DateTimeFormat('en', { timeZone: values.timezone }).format()
  } catch {
    throw new Error('Choose a valid time zone')
  }
  const minute = (value: string) => {
    const match = value.match(/^(\d{2}):(\d{2})$/)
    if (!match || Number(match[1]) > 23 || Number(match[2]) > 59) {
      throw new Error('Enter a valid start and end time')
    }
    return Number(match[1]) * 60 + Number(match[2])
  }
  const start = minute(values.start),
    end = minute(values.end)
  const timeValue = () =>
    ruleNode('binary', {
      operator: '+',
      args: [
        ruleNode('binary', {
          operator: '*',
          args: [
            ruleNode('call', {
              name: 'hour',
              args: [ruleNode('string', { value: values.timezone })],
            }),
            ruleNode('number', { value: '60' }),
          ],
        }),
        ruleNode('call', {
          name: 'minute',
          args: [ruleNode('string', { value: values.timezone })],
        }),
      ],
    })
  const weekday = (days: number[]) =>
    days
      .map((day) =>
        ruleNode('binary', {
          operator: '==',
          args: [
            ruleNode('call', {
              name: 'weekday',
              args: [ruleNode('string', { value: values.timezone })],
            }),
            ruleNode('number', { value: String(day) }),
          ],
        })
      )
      .reduce((left, right) =>
        ruleNode('binary', { operator: 'or', args: [left, right] })
      )
  const after = ruleNode('binary', {
    operator: '>=',
    args: [timeValue(), ruleNode('number', { value: String(start) })],
  })
  const before = ruleNode('binary', {
    operator: '<',
    args: [timeValue(), ruleNode('number', { value: String(end) })],
  })
  if (start === end) return weekday(values.weekdays)
  if (start < end) {
    return ruleNode('binary', {
      operator: 'and',
      args: [
        weekday(values.weekdays),
        ruleNode('binary', { operator: 'and', args: [after, before] }),
      ],
    })
  }
  return ruleNode('binary', {
    operator: 'or',
    args: [
      ruleNode('binary', {
        operator: 'and',
        args: [weekday(values.weekdays), after],
      }),
      ruleNode('binary', {
        operator: 'and',
        args: [weekday(values.weekdays.map((day) => (day + 1) % 7)), before],
      }),
    ],
  })
}
