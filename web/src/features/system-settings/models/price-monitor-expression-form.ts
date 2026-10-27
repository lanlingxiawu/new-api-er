/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

const VARIABLES = new Set([
  'p',
  'c',
  'cr',
  'cc',
  'cc1h',
  'cc12h',
  'img',
  'img_cr',
  'img_o',
  'ai',
  'ao',
])

type Token = { text: string; start: number; end: number }
type Node = Token & { kind: string; children: Node[] }

export type PricingExpressionField = {
  key: string
  label: string
  start: number
  end: number
  min: number
  max?: number
  reachable: boolean
  wrapped: boolean
  unit?: 'request' | 'image'
  price?: string
  quantity?: 'image_count'
  prices: Partial<
    Record<
      | 'p'
      | 'c'
      | 'cr'
      | 'cc'
      | 'cc1h'
      | 'cc12h'
      | 'img'
      | 'img_cr'
      | 'img_o'
      | 'ai'
      | 'ao',
      string
    >
  >
  coefficients: Record<string, { start: number; end: number }[]>
}

// This bounded grammar is for lossless local edits. The server's complete AST
// analysis and pricing validation remain authoritative before a draft is saved.
class PricingExpressionParser {
  tokens: Token[] = []
  index = 0
  depth = 0

  constructor(source: string) {
    if (source.length > 32768) throw new Error('expression limit')
    const version = source.match(/^\s*v(\d+)\s*:/)
    if (version && version[1] !== '1') throw new Error('version')
    let offset = version?.[0].length ?? 0
    const pattern =
      /\s+|\d+(?:\.\d*)?(?:[eE][+-]?\d+)?|\.\d+(?:[eE][+-]?\d+)?|[a-zA-Z_][a-zA-Z_0-9]*|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|<=|>=|[()+*?:,<>]/y
    while (offset < source.length) {
      pattern.lastIndex = offset
      const token = pattern.exec(source)
      if (!token) throw new Error('unsupported token')
      offset = pattern.lastIndex
      if (!token[0].trim()) continue
      this.tokens.push({ text: token[0], start: token.index, end: offset })
      if (this.tokens.length > 4096) throw new Error('expression limit')
    }
  }

  take(expected?: string): Token {
    const token = this.tokens[this.index++]
    if (!token || (expected !== undefined && token.text !== expected)) {
      throw new Error('invalid syntax')
    }
    return token
  }

  conditional(): Node {
    if (++this.depth > 64) throw new Error('expression depth')
    let node = this.binary(0)
    if (this.tokens[this.index]?.text === '?') {
      this.take('?')
      const yes = this.conditional()
      this.take(':')
      const no = this.conditional()
      node = {
        kind: 'conditional',
        text: '?',
        start: node.start,
        end: no.end,
        children: [node, yes, no],
      }
    }
    this.depth--
    return node
  }

  binary(minimum: number): Node {
    let node = this.primary()
    const precedence: Record<string, number> = {
      '<': 1,
      '<=': 1,
      '>': 1,
      '>=': 1,
      '+': 2,
      '*': 3,
    }
    while (this.index < this.tokens.length) {
      const token = this.tokens[this.index]
      const level = token ? precedence[token.text] : undefined
      if (!token || level === undefined || level < minimum) break
      this.take()
      const right = this.binary(level + 1)
      node = {
        kind: 'binary',
        text: token.text,
        start: node.start,
        end: right.end,
        children: [node, right],
      }
    }
    return node
  }

  primary(): Node {
    const token = this.take()
    if (token.text === '(') {
      const node = this.conditional()
      const close = this.take(')')
      return { ...node, start: token.start, end: close.end }
    }
    if (token.text === 'tier') {
      this.take('(')
      const label = this.take()
      if (!['"', "'"].includes(label.text[0] ?? '')) {
        throw new Error('tier label')
      }
      let encoded = label.text
      if (encoded.startsWith("'")) {
        let decoded = ''
        for (let index = 1; index < encoded.length - 1; index++) {
          const char = encoded.charAt(index)
          if (char !== '\\') {
            decoded += char
            continue
          }
          const escaped = encoded.charAt(++index)
          if (escaped === "'") decoded += "'"
          else {
            const suffix =
              escaped === 'u' ? encoded.slice(index + 1, index + 5) : ''
            decoded += JSON.parse(`"\\${escaped}${suffix}"`) as string
            index += suffix.length
          }
        }
        encoded = JSON.stringify(decoded)
      }
      const name: unknown = JSON.parse(encoded)
      if (typeof name !== 'string') throw new Error('tier label')
      this.take(',')
      const body = this.conditional()
      const close = this.take(')')
      return {
        kind: 'tier',
        text: name,
        start: token.start,
        end: close.end,
        children: [body],
      }
    }
    if (/^(?:\d|\.)/.test(token.text)) {
      if (!Number.isFinite(Number(token.text))) {
        throw new Error('invalid number')
      }
      return { ...token, kind: 'number', children: [] }
    }
    if (token.text === 'fixed') {
      this.take('(')
      const amount = this.take()
      if (
        !/^(?:\d|\.)/.test(amount.text) ||
        !Number.isFinite(Number(amount.text))
      ) {
        throw new Error('invalid fixed price')
      }
      const close = this.take(')')
      return {
        kind: 'fixed',
        text: 'fixed',
        start: token.start,
        end: close.end,
        children: [{ ...amount, kind: 'number', children: [] }],
      }
    }
    if (/^[a-zA-Z_]\w*$/.test(token.text)) {
      return { ...token, kind: 'variable', children: [] }
    }
    throw new Error('unsupported expression')
  }
}

function collectLinearPrices(
  node: Node,
  field: PricingExpressionField,
  depth: number
): void {
  if (depth > 64) throw new Error('expression depth')
  if (node.kind === 'binary' && node.text === '+') {
    for (const child of node.children) {
      collectLinearPrices(child, field, depth + 1)
    }
    return
  }
  if (node.kind === 'number' && Number(node.text) === 0) return
  if (node.kind !== 'binary' || node.text !== '*') {
    throw new Error('not a linear price')
  }
  let [variable, coefficient] = node.children
  if (variable?.kind === 'number') {
    ;[variable, coefficient] = [coefficient, variable]
  }
  if (
    variable?.kind !== 'variable' ||
    !VARIABLES.has(variable.text) ||
    coefficient?.kind !== 'number'
  ) {
    throw new Error('not a price coefficient')
  }
  const key = variable.text as keyof PricingExpressionField['prices']
  const value = Number(coefficient.text) + Number(field.prices[key] ?? 0)
  if (!Number.isFinite(value) || value < 0) throw new Error('invalid price')
  field.prices[key] = String(value)
  ;(field.coefficients[key] ??= []).push({
    start: coefficient.start,
    end: coefficient.end,
  })
}

function collectPricingBranches(
  node: Node,
  min: number,
  max: number,
  fields: PricingExpressionField[],
  depth: number
): void {
  if (depth > 64 || fields.length >= 128) throw new Error('expression limit')
  if (node.kind === 'conditional') {
    const [condition, yes, no] = node.children
    const [variable, value] = condition?.children ?? []
    if (
      condition?.kind !== 'binary' ||
      !['<', '<=', '>', '>='].includes(condition.text) ||
      variable?.kind !== 'variable' ||
      variable.text !== 'len' ||
      value?.kind !== 'number' ||
      !yes ||
      !no
    ) {
      throw new Error('unsupported condition')
    }
    const threshold = Number(value.text)
    if (threshold < 0 || threshold > 1e15) throw new Error('invalid threshold')
    const boundary = ['<', '>='].includes(condition.text)
      ? Math.ceil(threshold) - 1
      : Math.floor(threshold)
    const lowFirst = ['<', '<='].includes(condition.text)
    collectPricingBranches(
      yes,
      lowFirst ? min : Math.max(min, boundary + 1),
      lowFirst ? Math.min(max, boundary) : max,
      fields,
      depth + 1
    )
    collectPricingBranches(
      no,
      lowFirst ? Math.max(min, boundary + 1) : min,
      lowFirst ? max : Math.min(max, boundary),
      fields,
      depth + 1
    )
    return
  }
  // Fixed leaves are tier(name, fixed(amount)), optionally scaled by
  // image_count on either side; the editable span then covers the multiplier
  // too, so structural rebuilds keep it.
  let tier = node.kind === 'tier' ? node : undefined
  let quantity: 'image_count' | undefined
  if (!tier && node.kind === 'binary' && node.text === '*') {
    const [left, right] = node.children
    let candidate: Node | undefined
    if (left?.kind === 'variable' && left.text === 'image_count') {
      candidate = right
    } else if (right?.kind === 'variable' && right.text === 'image_count') {
      candidate = left
    }
    if (candidate?.kind === 'tier') {
      tier = candidate
      quantity = 'image_count'
    }
  }
  const body = tier ? tier.children[0] : node
  if (!body) throw new Error('missing tier body')
  const field: PricingExpressionField = {
    key: `tier:${fields.length}`,
    label: tier?.text ?? '',
    wrapped: tier !== undefined,
    start: quantity ? node.start : body.start,
    end: quantity ? node.end : body.end,
    min,
    max: max === Number.MAX_SAFE_INTEGER ? undefined : max,
    reachable: min <= max,
    prices: {},
    coefficients: {},
  }
  if (body.kind === 'fixed') {
    if (!tier) throw new Error('bare fixed price')
    const amount = body.children[0]
    if (!amount) throw new Error('missing fixed amount')
    field.unit = quantity ? 'image' : 'request'
    if (quantity) field.quantity = quantity
    field.price = String(Number(amount.text))
    field.coefficients['fixed'] = [{ start: amount.start, end: amount.end }]
    fields.push(field)
    return
  }
  collectLinearPrices(body, field, 0)
  fields.push(field)
}

export function parsePricingExpression(
  source: string
): PricingExpressionField[] | null {
  try {
    const parser = new PricingExpressionParser(source)
    const node = parser.conditional()
    if (parser.index !== parser.tokens.length) return null
    const fields: PricingExpressionField[] = []
    collectPricingBranches(node, 0, Number.MAX_SAFE_INTEGER, fields, 0)
    // The relay rejects trees mixing fixed and token-linear leaves.
    if (
      fields.some((field) => field.unit !== undefined) &&
      fields.some((field) => field.unit === undefined)
    ) {
      return null
    }
    return fields
  } catch {
    return null
  }
}
