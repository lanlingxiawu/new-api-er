/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  cloneRuleNode,
  collectRuleRequestFields,
  createConditionalAdjustment,
  createFlatPriceNode,
  createTimeCondition,
  extractPriceItems,
  moveRuleChild,
  replaceRuleNode,
  updatePriceItem,
  rulePriceLeaves,
  ruleUsedVariables,
  reorderPricingBranches,
  validateRuleDocument,
} from '../price-monitor-rule-helpers'
import type { RuleDocument, RuleNode } from '../price-monitor-rule-types'

test('rule changes and copies preserve untouched nodes and source binding', () => {
  const root = createFlatPriceNode({ p: '2', c: '8' })
  const document: RuleDocument = {
    schema_version: 1,
    expression_version: 1,
    source_hash: 'bound',
    root,
  }
  const leaf = root.args?.[0]
  assert.ok(leaf)
  const next = replaceRuleNode(document, leaf.id, { ...leaf, operator: '-' })
  assert.equal(document.root.args?.[0]?.operator, '*')
  assert.equal(next.source_hash, 'bound')
  const copy = cloneRuleNode(root)
  assert.notEqual(copy.id, root.id)
  assert.notEqual(copy.args?.[0]?.id, root.args?.[0]?.id)
})

test('price cards retain explicit zero and omitted items without mutating others', () => {
  const root = createFlatPriceNode({ p: '2', c: '0' })
  assert.deepEqual(extractPriceItems(root), { p: '2', c: '0' })
  const next = updatePriceItem(root, 'cr', '0.25')
  assert.deepEqual(extractPriceItems(next), { p: '2', c: '0', cr: '0.25' })
  assert.deepEqual(extractPriceItems(root), { p: '2', c: '0' })
  assert.throws(() => createFlatPriceNode({ p: '', c: '2' }))
})

test('cross-midnight weekdays shift the early part to the following calendar day', () => {
  const node = createTimeCondition({
    timezone: 'Asia/Shanghai',
    start: '22:00',
    end: '02:00',
    weekdays: [1, 5],
  })
  assert.equal(node.operator, 'or')
  const text = JSON.stringify(node)
  assert.ok(text.includes('1320'))
  assert.ok(text.includes('120'))
  assert.ok(text.includes('Asia/Shanghai'))
  assert.throws(() =>
    createTimeCondition({
      timezone: '',
      start: '09:00',
      end: '18:00',
      weekdays: [1],
    })
  )
  assert.throws(() =>
    createTimeCondition({
      timezone: 'invalid/zone',
      start: '09:00',
      end: '18:00',
      weekdays: [1],
    })
  )
  assert.throws(() =>
    createTimeCondition({
      timezone: 'UTC',
      start: '24:00',
      end: '18:00',
      weekdays: [1],
    })
  )
  assert.throws(() =>
    createTimeCondition({
      timezone: 'UTC',
      start: '09:60',
      end: '18:00',
      weekdays: [1],
    })
  )
  const allDay = createTimeCondition({
    timezone: 'UTC',
    start: '09:00',
    end: '09:00',
    weekdays: [1],
  })
  assert.ok(!JSON.stringify(allDay).includes('minute'))
})

test('priority changes keep the complete fallback and original condition identities', () => {
  const base = createFlatPriceNode({ p: '2', c: '8' })
  const later = createConditionalAdjustment(
    base,
    { id: 'later', kind: 'boolean', value: 'false' },
    '10',
    'increase'
  )
  const earlier = createConditionalAdjustment(
    later,
    { id: 'earlier', kind: 'boolean', value: 'true' },
    '20',
    'decrease'
  )
  const next = reorderPricingBranches(earlier, 0, 1)
  assert.equal(next.args?.[0]?.id, 'later')
  assert.equal(next.args?.[2]?.args?.[0]?.id, 'earlier')
  assert.equal(next.args?.[2]?.args?.[2]?.id, base.id)
  assert.deepEqual(
    [...ruleUsedVariables(next)].sort(),
    [...ruleUsedVariables(earlier)].sort()
  )
})

test('price leaves exclude condition arithmetic and price edits preserve billing variable usage', () => {
  const cost = createFlatPriceNode({ p: '2', c: '8', cr: '0.2' })
  const quantity = createFlatPriceNode({ p: '10' })
  const doc: RuleDocument = {
    schema_version: 1,
    expression_version: 1,
    source_hash: 'original',
    root: {
      id: 'conditional',
      kind: 'conditional',
      args: [
        {
          id: 'compare',
          kind: 'binary',
          operator: '>',
          args: [quantity, { id: 'limit', kind: 'number', value: '100' }],
        },
        cost,
        { id: 'fallback', kind: 'number', value: '0' },
      ],
    },
  }
  const leaves = rulePriceLeaves(doc.root)
  assert.equal(leaves.length, 1)
  assert.equal(leaves[0]?.node.id, cost.id)
  const next = replaceRuleNode(doc, cost.id, updatePriceItem(cost, 'c', '10'))
  assert.equal(next.root.args?.[0], doc.root.args?.[0])
  assert.deepEqual(
    [...ruleUsedVariables(next.root)].sort(),
    [...ruleUsedVariables(doc.root)].sort()
  )
  assert.deepEqual(validateRuleDocument(next), [])
  assert.throws(() => createFlatPriceNode({ p: '0x10' }))
  assert.deepEqual(
    validateRuleDocument({
      ...doc,
      root: { id: 'n', kind: 'number', value: '-2.5e+1' },
    }),
    []
  )
})

test('request field names are collected without exposing or inventing values', () => {
  const header: RuleNode = {
    id: 'h',
    kind: 'call',
    name: 'header',
    args: [{ id: 'k', kind: 'string', value: 'x-fast' }],
  }
  const param: RuleNode = {
    id: 'p',
    kind: 'call',
    name: 'param',
    args: [{ id: 'pk', kind: 'string', value: 'options.quality' }],
  }
  const document: RuleDocument = {
    schema_version: 1,
    expression_version: 1,
    source_hash: 's',
    root: { id: 'r', kind: 'binary', operator: '==', args: [header, param] },
  }
  assert.deepEqual(collectRuleRequestFields(document), {
    params: ['options.quality'],
    headers: ['x-fast'],
  })
})

test('conditional percentage adjustment preserves original rule as fallback and changes priority explicitly', () => {
  const old = createFlatPriceNode({ p: '2', c: '8' })
  const condition: RuleNode = {
    id: 'condition',
    kind: 'boolean',
    value: 'true',
  }
  const next = createConditionalAdjustment(old, condition, '10', 'decrease')
  assert.equal(next.kind, 'conditional')
  assert.equal(next.args?.[2]?.id, old.id)
  assert.equal(next.args?.[1]?.args?.[1]?.value, '0.9')
  const moved = moveRuleChild(next, 1, 2)
  assert.equal(moved.args?.[1]?.id, old.id)
  assert.equal(next.args?.[2]?.id, old.id)
})
