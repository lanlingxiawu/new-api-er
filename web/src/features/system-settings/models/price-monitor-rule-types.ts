/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
export type RuleKind =
  | 'number'
  | 'string'
  | 'boolean'
  | 'null'
  | 'usage'
  | 'unary'
  | 'binary'
  | 'conditional'
  | 'call'

export type RuleNode = {
  id: string
  kind: RuleKind
  value?: string
  operator?: string
  name?: string
  args?: RuleNode[]
}

export type RuleDocument = {
  schema_version: 1
  expression_version: 1
  source_hash: string
  root: RuleNode
}

export const RULE_USAGE_VARIABLES = [
  'p',
  'c',
  'len',
  'cr',
  'cc',
  'cc1h',
  'img',
  'img_o',
  'ai',
  'ao',
] as const
export type RuleUsageVariable = (typeof RULE_USAGE_VARIABLES)[number]
export type RulePriceVariable = Exclude<RuleUsageVariable, 'len'>

export const RULE_USAGE_LABELS: Record<RuleUsageVariable, string> = {
  p: 'Billable input tokens',
  c: 'Billable output tokens',
  len: 'Total input context length',
  cr: 'Cache read tokens',
  cc: 'Cache creation tokens',
  cc1h: 'One-hour cache creation tokens',
  img: 'Image input tokens',
  img_o: 'Image output tokens',
  ai: 'Audio input tokens',
  ao: 'Audio output tokens',
}
export const RULE_PRICE_LABELS: Record<RulePriceVariable, string> = {
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

export const RULE_BINARY_ACTIONS = [
  ['+', 'Add amounts'],
  ['-', 'Subtract amounts'],
  ['*', 'Multiply amounts'],
  ['/', 'Divide amounts'],
  ['%', 'Remainder after division'],
  ['**', 'Raise to a power'],
  ['^', 'Raise to a power'],
  ['==', 'Equals'],
  ['!=', 'Does not equal'],
  ['<', 'Less than'],
  ['<=', 'At most'],
  ['>', 'Greater than'],
  ['>=', 'At least'],
  ['and', 'All conditions must match'],
  ['&&', 'All conditions must match'],
  ['or', 'Any condition may match'],
  ['||', 'Any condition may match'],
  ['contains', 'Contains text'],
  ['startsWith', 'Starts with text'],
  ['endsWith', 'Ends with text'],
] as const

export const RULE_CALL_ACTIONS = [
  ['tier', 'Pricing branch'],
  ['param', 'Request parameter'],
  ['header', 'Request header'],
  ['has', 'Contains text'],
  ['hour', 'Hour of day'],
  ['minute', 'Minute of hour'],
  ['weekday', 'Day of week'],
  ['month', 'Month'],
  ['day', 'Day of month'],
  ['max', 'Choose the larger amount'],
  ['min', 'Choose the smaller amount'],
  ['abs', 'Absolute amount'],
  ['ceil', 'Round amount up'],
  ['floor', 'Round amount down'],
] as const

export const RULE_TIME_ZONES = [
  ['Asia/Shanghai', 'China Standard Time (UTC+8)'],
  ['UTC', 'UTC'],
  ['America/New_York', 'New York time'],
  ['America/Los_Angeles', 'Los Angeles time'],
  ['Europe/London', 'London time'],
  ['custom', 'Custom time zone'],
] as const
