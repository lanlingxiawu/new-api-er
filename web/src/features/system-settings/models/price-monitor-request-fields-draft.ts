/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
export type RequestFieldType =
  | 'string'
  | 'number'
  | 'boolean'
  | 'null'
  | 'object'
  | 'array'
export type RequestField = {
  id: string
  name: string
  type: RequestFieldType
  value: string
  children: RequestField[]
  allowEmptyName?: boolean
  pendingValue?: boolean
}
export type RequestContextDraft = {
  parameters: RequestField[]
  headers: RequestField[]
}
export class RequestFieldError extends Error {
  constructor(
    message: string,
    public path: string
  ) {
    super(message)
    this.name = 'RequestFieldError'
  }
}
export function createRequestField(
  type: RequestFieldType = 'string'
): RequestField {
  return {
    id: crypto.randomUUID(),
    name: '',
    type,
    value: type === 'boolean' ? 'false' : '',
    children: [],
  }
}

export function hydrateRequestContext(
  body: Record<string, unknown>,
  headers: Record<string, string>
): RequestContextDraft {
  let count = 0
  const hydrate = (
    name: string,
    value: unknown,
    depth: number
  ): RequestField => {
    if (depth > 64) {
      throw new RequestFieldError('Request fields are too deeply nested', name)
    }
    if (++count > 4096) {
      throw new RequestFieldError('Too many request fields', name)
    }
    let type: string = typeof value
    if (value === null) type = 'null'
    else if (Array.isArray(value)) type = 'array'
    if (
      !['string', 'number', 'boolean', 'null', 'array', 'object'].includes(
        type
      ) ||
      (type === 'number' && !Number.isFinite(value))
    ) {
      throw new RequestFieldError('Use a supported request value', name)
    }
    const node: RequestField = {
      id: `request-field-${count}`,
      name,
      type: type as RequestFieldType,
      value: value === null || typeof value === 'object' ? '' : String(value),
      children: [],
      allowEmptyName: name === '',
    }
    if (type === 'object' || type === 'array') {
      node.children = Object.entries(value as object).map(([key, child]) =>
        hydrate(key, child, depth + 1)
      )
    }
    return node
  }
  return {
    parameters: Object.entries(body).map(([name, value]) =>
      hydrate(name, value, 0)
    ),
    headers: Object.entries(headers).map(([name, value]) =>
      hydrate(name, value, 0)
    ),
  }
}

export function serializeRequestContext(draft: RequestContextDraft): {
  request_body: Record<string, unknown>
  headers: Record<string, string>
} {
  let count = 0
  const serializeFields = (
    fields: RequestField[],
    path: string,
    depth: number,
    array = false
  ): unknown => {
    if (depth > 64 && fields.length > 0) {
      throw new RequestFieldError('Request fields are too deeply nested', path)
    }
    const names = new Set<string>()
    const values = fields.map((field, index) => {
      const fieldPath = array
        ? `${path}[${index}]`
        : `${path}[${JSON.stringify(field.name)}]`
      if (++count > 4096) {
        throw new RequestFieldError('Too many request fields', fieldPath)
      }
      if (!array && !field.name && !field.allowEmptyName) {
        throw new RequestFieldError('Enter a field name', fieldPath)
      }
      if (!array && names.has(field.name)) {
        throw new RequestFieldError('Use a unique field name', fieldPath)
      }
      names.add(field.name)
      if (field.pendingValue) {
        throw new RequestFieldError(
          'Enter a value for this suggested field',
          fieldPath
        )
      }
      let value: unknown
      switch (field.type) {
        case 'string':
          value = field.value
          break
        case 'number':
          if (
            !/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(
              field.value
            ) ||
            !Number.isFinite(Number(field.value))
          ) {
            throw new RequestFieldError('Enter a valid number', fieldPath)
          }
          value = Number(field.value)
          break
        case 'boolean':
          if (field.value !== 'true' && field.value !== 'false') {
            throw new RequestFieldError('Choose true or false', fieldPath)
          }
          value = field.value === 'true'
          break
        case 'null':
          value = null
          break
        case 'object':
          value = serializeFields(field.children, fieldPath, depth + 1)
          break
        case 'array':
          value = serializeFields(field.children, fieldPath, depth + 1, true)
          break
        default:
          throw new RequestFieldError(
            'Use a supported request value',
            fieldPath
          )
      }
      return [field.name, value] as const
    })
    return array ? values.map(([, value]) => value) : Object.fromEntries(values)
  }
  const request_body = serializeFields(draft.parameters, '', 0) as Record<
    string,
    unknown
  >
  const headerNames = new Set<string>()
  const headers = Object.fromEntries(
    draft.headers.map((field) => {
      if (++count > 4096) {
        throw new RequestFieldError('Too many request fields', field.name)
      }
      if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(field.name)) {
        throw new RequestFieldError('Enter a valid header name', field.name)
      }
      if (headerNames.has(field.name.toLowerCase())) {
        throw new RequestFieldError('Use a unique field name', field.name)
      }
      headerNames.add(field.name.toLowerCase())
      if (field.pendingValue) {
        throw new RequestFieldError(
          'Enter a value for this suggested field',
          field.name
        )
      }
      if (field.type !== 'string') {
        throw new RequestFieldError('Header values must be text', field.name)
      }
      if (/[\r\n]/.test(field.value)) {
        throw new RequestFieldError(
          'Header values cannot contain line breaks',
          field.name
        )
      }
      return [field.name, field.value]
    })
  )
  return { request_body, headers }
}

// Segments are literal object names, so a property containing a dot stays intact.
export function addRequestParameterPath(
  draft: RequestContextDraft,
  path: string[]
): RequestContextDraft {
  if (!path.length || path.length > 64) {
    throw new RequestFieldError('Enter a valid parameter path', path.join('.'))
  }
  const next = structuredClone(draft)
  let children = next.parameters
  for (let index = 0; index < path.length; index++) {
    const name = path[index] ?? ''
    let field = children.find((candidate) => candidate.name === name)
    if (!field) {
      field = {
        ...createRequestField(index === path.length - 1 ? 'string' : 'object'),
        name,
        allowEmptyName: name === '',
      }
      children.push(field)
    }
    if (index < path.length - 1 && field.type !== 'object') {
      throw new RequestFieldError(
        'This path already contains a value',
        path.slice(0, index + 1).join('.')
      )
    }
    children = field.children
  }
  return next
}

export function requestContextFromRuleFields(
  current: RequestContextDraft,
  fields: { params: string[]; headers: string[] }
): { draft: RequestContextDraft; skipped: string[] } {
  let draft = structuredClone(current)
  const skipped: string[] = []
  const pending = [...current.parameters, ...current.headers]
  let fieldCount = 0
  while (pending.length && fieldCount <= 4096) {
    const field = pending.pop()
    if (field) {
      fieldCount++
      pending.push(...field.children)
    }
  }
  for (const path of fields.params) {
    const segments: string[] = []
    let segment = ''
    let escaped = false
    let supported = Boolean(path)
    for (const character of path) {
      if (escaped) {
        segment += character
        escaped = false
        continue
      }
      if (character === '\\') {
        escaped = true
        continue
      }
      if (character === '.') {
        segments.push(segment)
        segment = ''
        continue
      }
      if ('#@*?|![]{}()'.includes(character)) supported = false
      segment += character
    }
    segments.push(segment)
    if (
      !supported ||
      escaped ||
      segments.some((item) => !item) ||
      segments.length > 64
    ) {
      skipped.push(path)
      continue
    }
    const candidate = structuredClone(draft)
    let newFields = 0
    let children = candidate.parameters
    let isArray = false
    for (let index = 0; index < segments.length; index++) {
      const name = segments[index] ?? ''
      const arrayIndex = Number(name)
      if (
        isArray &&
        (!/^(0|[1-9]\d*)$/.test(name) ||
          !Number.isSafeInteger(arrayIndex) ||
          arrayIndex > children.length)
      ) {
        supported = false
        break
      }
      let field: RequestField | undefined = isArray
        ? children[arrayIndex]
        : children.find((item) => item.name === name)
      if (!field) {
        if (fieldCount + ++newFields > 4096) {
          supported = false
          break
        }
        let type: RequestFieldType = 'string'
        if (index < segments.length - 1) {
          type = /^(0|[1-9]\d*)$/.test(segments[index + 1] ?? '')
            ? 'array'
            : 'object'
        }
        field = {
          ...createRequestField(type),
          name,
          pendingValue: index === segments.length - 1,
        }
        children.push(field)
      }
      if (
        index < segments.length - 1 &&
        field.type !== 'object' &&
        field.type !== 'array'
      ) {
        supported = false
        break
      }
      children = field.children
      isArray = field.type === 'array'
    }
    if (supported) {
      draft = candidate
      fieldCount += newFields
    } else skipped.push(path)
  }
  for (const name of fields.headers) {
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) {
      skipped.push(name)
      continue
    }
    if (
      !draft.headers.some(
        (field) => field.name.toLowerCase() === name.toLowerCase()
      )
    ) {
      if (fieldCount >= 4096) {
        skipped.push(name)
        continue
      }
      draft.headers.push({ ...createRequestField(), name, pendingValue: true })
      fieldCount++
    }
  }
  return { draft, skipped }
}
