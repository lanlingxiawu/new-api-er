/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useId, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import {
  createRequestField,
  RequestFieldError,
  serializeRequestContext,
  type RequestContextDraft,
  type RequestField,
  type RequestFieldType,
} from './price-monitor-request-fields-draft'

const TYPE_LABELS: Record<RequestFieldType, string> = {
  string: 'Text value',
  number: 'Number value',
  boolean: 'True or false',
  null: 'Empty value (null)',
  object: 'Nested fields',
  array: 'List of values',
}

export function PriceMonitorRequestFields({
  value,
  onChange,
  disabled = false,
}: {
  value: RequestContextDraft
  onChange: (value: RequestContextDraft) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const error = useMemo(() => {
    try {
      serializeRequestContext(value)
      return null
    } catch (error) {
      return error instanceof RequestFieldError
        ? error
        : new RequestFieldError('Check the request fields', '')
    }
  }, [value])
  return (
    <FieldGroup>
      <FieldSet>
        <FieldLegend>{t('Request parameters')}</FieldLegend>
        <FieldDescription>
          {t(
            'Add nested fields or lists to describe the request. Text, numbers, false and empty values stay distinct.'
          )}
        </FieldDescription>
        <RequestFieldRows
          fields={value.parameters}
          onChange={(parameters) => onChange({ ...value, parameters })}
          disabled={disabled}
          depth={0}
        />
      </FieldSet>
      <FieldSet>
        <FieldLegend>{t('Request headers')}</FieldLegend>
        <RequestFieldRows
          fields={value.headers}
          onChange={(headers) => onChange({ ...value, headers })}
          disabled={disabled}
          depth={0}
          headers
        />
      </FieldSet>
      {error && (
        <p role='alert' className='text-destructive text-xs break-all'>
          {error.path ? `${error.path}: ` : ''}
          {t(error.message)}
        </p>
      )}
    </FieldGroup>
  )
}

function RequestFieldRows({
  fields,
  onChange,
  disabled,
  depth,
  array = false,
  headers = false,
}: {
  fields: RequestField[]
  onChange: (fields: RequestField[]) => void
  disabled: boolean
  depth: number
  array?: boolean
  headers?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const types = Object.entries(TYPE_LABELS).map(([value, label]) => ({
    value,
    label: t(label),
  }))
  let addLabel = t('Add request field')
  if (headers) addLabel = t('Add request header')
  else if (array) addLabel = t('Add list item')
  return (
    <FieldGroup className='gap-3'>
      {fields.map((field, index) => {
        const prefix = `${id}-${field.id}`
        const update = (changes: Partial<RequestField>) =>
          onChange(
            fields.map((item) =>
              item.id === field.id ? { ...item, ...changes } : item
            )
          )
        return (
          <FieldGroup key={field.id} className='gap-3 rounded-md border p-3'>
            <div className='grid gap-3 sm:grid-cols-2'>
              {!array ? (
                <Field data-disabled={disabled}>
                  <FieldLabel htmlFor={`${prefix}-name`}>
                    {headers ? t('Header name') : t('Field name')}
                  </FieldLabel>
                  <Input
                    id={`${prefix}-name`}
                    value={field.name}
                    disabled={disabled}
                    onChange={(event) =>
                      update({
                        name: event.target.value,
                        allowEmptyName: false,
                      })
                    }
                  />
                </Field>
              ) : (
                <p className='self-center text-sm font-medium'>
                  {t('List item {{number}}', { number: index + 1 })}
                </p>
              )}
              {!headers && (
                <Field data-disabled={disabled}>
                  <FieldLabel htmlFor={`${prefix}-type`}>
                    {t('Value type')}
                  </FieldLabel>
                  <Select
                    items={types}
                    value={field.type}
                    disabled={disabled}
                    onValueChange={(type) => {
                      if (type) {
                        update({
                          type: type as RequestFieldType,
                          pendingValue:
                            field.pendingValue &&
                            ['string', 'number'].includes(type),
                          value:
                            type === 'boolean' &&
                            !['true', 'false'].includes(field.value)
                              ? 'false'
                              : field.value,
                        })
                      }
                    }}
                  >
                    <SelectTrigger id={`${prefix}-type`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {types.map((type) => (
                        <SelectItem key={type.value} value={type.value}>
                          {type.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              )}
            </div>
            {(field.type === 'object' || field.type === 'array') &&
              depth < 64 && (
                <RequestFieldRows
                  fields={field.children}
                  onChange={(children) => update({ children })}
                  disabled={disabled}
                  depth={depth + 1}
                  array={field.type === 'array'}
                />
              )}
            {(field.type === 'object' || field.type === 'array') &&
              depth >= 64 &&
              field.children.length > 0 && (
                <p role='alert' className='text-destructive text-xs'>
                  {t('Request fields are too deeply nested')}
                </p>
              )}
            {field.type === 'null' && (
              <p className='text-muted-foreground text-xs'>
                {t('This field is present with an empty value, not omitted.')}
              </p>
            )}
            {!['object', 'array', 'null'].includes(field.type) && (
              <Field data-disabled={disabled}>
                <FieldLabel htmlFor={`${prefix}-value`}>
                  {t('Value')}
                </FieldLabel>
                {field.type === 'boolean' ? (
                  <Select
                    items={[
                      { value: 'true', label: t('True') },
                      { value: 'false', label: t('False') },
                    ]}
                    value={field.value}
                    disabled={disabled}
                    onValueChange={(value) => {
                      if (value) update({ value, pendingValue: false })
                    }}
                  >
                    <SelectTrigger id={`${prefix}-value`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='true'>{t('True')}</SelectItem>
                      <SelectItem value='false'>{t('False')}</SelectItem>
                    </SelectContent>
                  </Select>
                ) : (
                  <Textarea
                    id={`${prefix}-value`}
                    className='min-h-16'
                    value={field.value}
                    disabled={disabled}
                    inputMode={field.type === 'number' ? 'decimal' : undefined}
                    onChange={(event) =>
                      update({ value: event.target.value, pendingValue: false })
                    }
                  />
                )}
              </Field>
            )}
            <div className='flex flex-wrap justify-end gap-2'>
              {field.pendingValue && field.type === 'string' && (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={disabled}
                  onClick={() => update({ value: '', pendingValue: false })}
                >
                  {t('Use empty text')}
                </Button>
              )}
              {array && (
                <>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={disabled || index === 0}
                    onClick={() => {
                      const previous = fields[index - 1]
                      if (!previous) return
                      const next = [...fields]
                      next[index - 1] = field
                      next[index] = previous
                      onChange(next)
                    }}
                  >
                    {t('Move up')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={disabled || index === fields.length - 1}
                    onClick={() => {
                      const following = fields[index + 1]
                      if (!following) return
                      const next = [...fields]
                      next[index] = following
                      next[index + 1] = field
                      onChange(next)
                    }}
                  >
                    {t('Move down')}
                  </Button>
                </>
              )}
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={disabled}
                onClick={() =>
                  onChange(fields.filter((item) => item.id !== field.id))
                }
              >
                {t('Remove')}
              </Button>
            </div>
          </FieldGroup>
        )
      })}
      <Button
        type='button'
        variant='outline'
        disabled={disabled || fields.length >= 4096}
        onClick={() => onChange([...fields, createRequestField()])}
      >
        {addLabel}
      </Button>
    </FieldGroup>
  )
}
