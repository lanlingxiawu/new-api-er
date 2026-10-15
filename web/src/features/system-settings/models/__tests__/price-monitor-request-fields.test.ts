/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  createRequestField,
  hydrateRequestContext,
  serializeRequestContext,
  requestContextFromRuleFields,
  addRequestParameterPath,
} from '../price-monitor-request-fields-draft'

describe('request context fields', () => {
  test('validates empty names, booleans, header types and unsupported hydrated values', () => {
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [createRequestField()],
          headers: [],
        }),
      { message: 'Enter a field name' }
    )
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [
            { ...createRequestField('boolean'), name: 'x', value: '' },
          ],
          headers: [],
        }),
      { message: 'Choose true or false' }
    )
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [],
          headers: [
            { ...createRequestField('number'), name: 'X-A', value: '1' },
          ],
        }),
      { message: 'Header values must be text' }
    )
    for (const value of [undefined, Infinity, () => 0]) {
      assert.throws(() => hydrateRequestContext({ value }, {}), {
        message: 'Use a supported request value',
      })
    }
    assert.throws(
      () => addRequestParameterPath(hydrateRequestContext({}, {}), []),
      { message: 'Enter a valid parameter path' }
    )
  })
  test('serializing limits apply to manual drafts and arrays retain order after removal', () => {
    const field = { ...createRequestField(), name: 'a' }
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: Array.from({ length: 4097 }, (_, index) => ({
            ...field,
            name: String(index),
          })),
          headers: [],
        }),
      { message: 'Too many request fields' }
    )
    let nested = field
    for (let i = 0; i < 66; i++) {
      nested = { ...createRequestField('array'), name: 'a', children: [nested] }
    }
    assert.throws(
      () => serializeRequestContext({ parameters: [nested], headers: [] }),
      { message: 'Request fields are too deeply nested' }
    )
    const draft = hydrateRequestContext({ list: [1, false, 'value'] }, {})
    draft.parameters[0]?.children.splice(1, 1)
    assert.deepEqual(serializeRequestContext(draft).request_body, {
      list: [1, 'value'],
    })
  })
  test('prefills basic and escaped paths, existing array items, and headers without overwriting', () => {
    const original = hydrateRequestContext(
      { rows: [{ value: 1 }] },
      { 'X-Test': 'kept' }
    )
    const { draft, skipped } = requestContextFromRuleFields(original, {
      params: [
        'options.quality',
        'literal\\.dot.flag',
        'rows.0.value',
        'rows.1.flag',
      ],
      headers: ['x-test', 'X-New'],
    })
    assert.deepEqual(skipped, [])
    assert.throws(() => serializeRequestContext(draft), {
      message: 'Enter a value for this suggested field',
    })
    const pending = [...draft.parameters, ...draft.headers]
    while (pending.length) {
      const field = pending.pop()
      if (field) {
        field.pendingValue = false
        pending.push(...field.children)
      }
    }
    assert.deepEqual(serializeRequestContext(draft), {
      request_body: {
        rows: [{ value: 1 }, { flag: '' }],
        options: { quality: '' },
        'literal.dot': { flag: '' },
      },
      headers: { 'X-Test': 'kept', 'X-New': '' },
    })
    assert.equal(original.parameters.length, 1)
  })
  test('prefill reports complex paths, sparse arrays and scalar conflicts without partial writes', () => {
    const original = hydrateRequestContext({ scalar: 1 }, {})
    const { draft, skipped } = requestContextFromRuleFields(original, {
      params: [
        'items.4.name',
        'items.#(a==1).name',
        'scalar.child',
        'a|@reverse',
        'x.*',
        'x.',
      ],
      headers: [],
    })
    assert.equal(skipped.length, 6)
    assert.deepEqual(draft, original)
  })
  test('preserves nested objects, arrays, empty containers, and scalar types', () => {
    const body = {
      zero: 0,
      flag: false,
      nil: null,
      blank: '',
      numberText: '02',
      nested: { 'literal.dot': [1, '1', null, false, {}, []] },
      empty: {},
    }
    const headers = { 'X-Test': '', 'x-other': 'a\tb' }
    assert.deepEqual(
      serializeRequestContext(hydrateRequestContext(body, headers)),
      { request_body: body, headers }
    )
  })
  test('preserves empty and prototype-like literal property names safely', () => {
    const body = JSON.parse(
      '{"":0,"__proto__":{"polluted":true},"constructor":false}'
    )
    assert.deepEqual(
      serializeRequestContext(hydrateRequestContext(body, {})).request_body,
      body
    )
    assert.equal(({} as Record<string, unknown>).polluted, undefined)
  })
  test('rejects blank, nonfinite, and incomplete numeric drafts without changing strings', () => {
    for (const value of ['', ' ', 'NaN', 'Infinity', '1e', '0x12']) {
      const field = { ...createRequestField('number'), name: 'amount', value }
      assert.throws(
        () => serializeRequestContext({ parameters: [field], headers: [] }),
        { message: 'Enter a valid number' }
      )
    }
    const field = {
      ...createRequestField('number'),
      name: 'amount',
      value: '-1.5e2',
    }
    assert.equal(
      serializeRequestContext({ parameters: [field], headers: [] }).request_body
        .amount,
      -150
    )
  })
  test('rejects duplicate properties and case-insensitive duplicate headers', () => {
    const field = { ...createRequestField(), name: 'a' }
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [field, { ...field, id: 'b' }],
          headers: [],
        }),
      { message: 'Use a unique field name' }
    )
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [],
          headers: [
            { ...field, name: 'X-A' },
            { ...field, name: 'x-a' },
          ],
        }),
      { message: 'Use a unique field name' }
    )
  })
  test('rejects invalid header names and line breaks but keeps empty values', () => {
    for (const name of ['', 'two words', 'a:b', '你好']) {
      assert.throws(() =>
        serializeRequestContext({
          parameters: [],
          headers: [{ ...createRequestField(), name }],
        })
      )
    }
    assert.throws(
      () =>
        serializeRequestContext({
          parameters: [],
          headers: [{ ...createRequestField(), name: 'X-A', value: 'a\r\nb' }],
        }),
      { message: 'Header values cannot contain line breaks' }
    )
  })
  test('adds nested parameter names without guessing values or overwriting existing paths', () => {
    const draft = hydrateRequestContext({ options: { existing: 1 } }, {})
    const next = addRequestParameterPath(draft, ['options', 'quality'])
    assert.equal(next.parameters[0]?.children[1]?.value, '')
    assert.deepEqual(serializeRequestContext(next).request_body, {
      options: { existing: 1, quality: '' },
    })
    assert.deepEqual(
      addRequestParameterPath(next, ['options', 'existing']),
      next
    )
    assert.throws(
      () => addRequestParameterPath(draft, ['options', 'existing', 'child']),
      { message: 'This path already contains a value' }
    )
    assert.equal(draft.parameters[0]?.children.length, 1)
  })
  test('bounds depth and field count before recursive rendering', () => {
    let body: Record<string, unknown> = {}
    for (let i = 0; i < 66; i++) body = { a: body }
    assert.throws(() => hydrateRequestContext(body, {}), {
      message: 'Request fields are too deeply nested',
    })
    assert.throws(
      () =>
        hydrateRequestContext(
          Object.fromEntries(
            Array.from({ length: 4097 }, (_, i) => [String(i), 1])
          ),
          {}
        ),
      { message: 'Too many request fields' }
    )
  })
})
