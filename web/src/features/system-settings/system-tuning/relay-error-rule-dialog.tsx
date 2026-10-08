/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'

import type { RelayErrorRuleAction, RelayErrorRuleSource } from '../types'
import {
  type EditRow,
  emptyEditRow,
  MAX_EDIT_LENGTH,
  MAX_EDITS,
  MAX_MESSAGE_LENGTH,
  MAX_NAME_LENGTH,
  RULE_SOURCES,
  type RuleFieldErrors,
  type RuleRow,
  sourceLabel,
  validateRuleRow,
} from './relay-error-display-rules'

function FieldError(props: { message?: string }) {
  if (!props.message) return null
  return <p className='text-destructive text-xs'>{props.message}</p>
}

// RelayErrorRuleDialog edits one rule of the page draft. Confirming only
// updates the draft; the section's Save button stores it. The section mounts
// it per edit, so state starts from props.rule. An existing rule opens with its
// problems already shown (Save sends the user here for exactly that).
export function RelayErrorRuleDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  rule: RuleRow
  isNew: boolean
  onConfirm: (rule: RuleRow) => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<RuleRow>(props.rule)
  const [errors, setErrors] = useState<RuleFieldErrors>(() =>
    props.isNew ? {} : validateRuleRow(props.rule, t)
  )

  const update = (patch: Partial<RuleRow>) =>
    setDraft((current) => ({ ...current, ...patch }))
  const updateEdit = (id: number, patch: Partial<EditRow>) =>
    setDraft((current) => ({
      ...current,
      edits: current.edits.map((item) =>
        item.id === id ? { ...item, ...patch } : item
      ),
    }))

  const statusCodeField = (
    <div className='grid gap-1.5'>
      <Label htmlFor='red-rule-status'>{t('Return status code')}</Label>
      <Input
        id='red-rule-status'
        value={draft.statusCode}
        placeholder={t('Unchanged')}
        onChange={(event) => update({ statusCode: event.target.value })}
      />
      <FieldError message={errors.statusCode} />
    </div>
  )

  const confirm = () => {
    const next = validateRuleRow(draft, t)
    setErrors(next)
    if (Object.keys(next).length > 0) return
    props.onConfirm(draft)
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.isNew ? t('Add Rule') : t('Edit Rule')}
      contentClassName='max-w-2xl'
      contentHeight='auto'
      bodyClassName='pr-2'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='button' onClick={confirm}>
            {t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='min-w-0 space-y-4'>
        <div className='space-y-3'>
          <h4 className='text-sm font-medium'>{t('Match when')}</h4>
          <div className='grid gap-1.5'>
            <Label htmlFor='red-rule-source'>{t('Applies to')}</Label>
            <Select
              items={RULE_SOURCES.map((value) => ({
                value,
                label: sourceLabel(value, t),
              }))}
              value={draft.source}
              onValueChange={(next) => {
                if (next) update({ source: next as RelayErrorRuleSource })
              }}
            >
              <SelectTrigger id='red-rule-source' className='w-full sm:w-60'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {RULE_SOURCES.map((value) => (
                    <SelectItem key={value} value={value}>
                      {sourceLabel(value, t)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className='grid gap-3 sm:grid-cols-3'>
            <div className='grid gap-1.5'>
              <Label htmlFor='red-rule-status-codes'>{t('Status codes')}</Label>
              <Input
                id='red-rule-status-codes'
                value={draft.statusCodes}
                placeholder={t('e.g. 429, 503')}
                onChange={(event) =>
                  update({ statusCodes: event.target.value })
                }
              />
              <FieldError message={errors.statusCodes} />
            </div>
            <div className='grid gap-1.5'>
              <Label htmlFor='red-rule-error-codes'>{t('Error codes')}</Label>
              <Input
                id='red-rule-error-codes'
                value={draft.errorCodes}
                placeholder={t('e.g. model_not_found')}
                onChange={(event) => update({ errorCodes: event.target.value })}
              />
            </div>
            <div className='grid gap-1.5'>
              <Label htmlFor='red-rule-keywords'>{t('Keywords')}</Label>
              <Input
                id='red-rule-keywords'
                value={draft.keywords}
                placeholder={t('e.g. quota, balance')}
                onChange={(event) => update({ keywords: event.target.value })}
              />
            </div>
          </div>
          <FieldError message={errors.lists} />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Separate multiple values with commas; any one of them may match. Every filled-in field must match, and empty fields are ignored. Keywords ignore case.'
            )}
          </p>
        </div>

        <Separator />

        <div className='space-y-3'>
          <h4 className='text-sm font-medium'>{t('Then')}</h4>
          <RadioGroup
            value={draft.action}
            onValueChange={(next) =>
              update({ action: next as RelayErrorRuleAction })
            }
            className='flex flex-wrap gap-4'
          >
            <div className='flex items-center gap-2'>
              <RadioGroupItem value='replace' id='red-rule-action-replace' />
              <Label
                htmlFor='red-rule-action-replace'
                className='cursor-pointer'
              >
                {t('Replace the message')}
              </Label>
            </div>
            <div className='flex items-center gap-2'>
              <RadioGroupItem value='edit' id='red-rule-action-edit' />
              <Label htmlFor='red-rule-action-edit' className='cursor-pointer'>
                {t('Edit parts of the message')}
              </Label>
            </div>
            <div className='flex items-center gap-2'>
              <RadioGroupItem value='keep' id='red-rule-action-keep' />
              <Label htmlFor='red-rule-action-keep' className='cursor-pointer'>
                {t('Keep the original')}
              </Label>
            </div>
          </RadioGroup>
          {draft.action === 'edit' && (
            <div className='space-y-3'>
              <div className='space-y-2'>
                <div className='text-muted-foreground hidden grid-cols-[1fr_1fr_auto_auto] gap-2 text-xs sm:grid'>
                  <span>{t('Find')}</span>
                  <span>{t('Replace with (empty deletes it)')}</span>
                  <span>{t('Regex')}</span>
                  <span className='w-7' />
                </div>
                {draft.edits.map((item, index) => (
                  <div
                    key={item.id}
                    className='grid grid-cols-1 items-center gap-2 sm:grid-cols-[1fr_1fr_auto_auto]'
                  >
                    <Input
                      aria-label={t('Find')}
                      value={item.find}
                      maxLength={MAX_EDIT_LENGTH}
                      placeholder={t('Text to find')}
                      onChange={(event) =>
                        updateEdit(item.id, { find: event.target.value })
                      }
                    />
                    <Input
                      aria-label={t('Replace with (empty deletes it)')}
                      value={item.replace}
                      maxLength={MAX_EDIT_LENGTH}
                      placeholder={t('Leave empty to delete')}
                      onChange={(event) =>
                        updateEdit(item.id, { replace: event.target.value })
                      }
                    />
                    <label className='flex items-center gap-2 text-sm sm:justify-center'>
                      <Checkbox
                        checked={item.regex}
                        onCheckedChange={(checked) =>
                          updateEdit(item.id, { regex: checked === true })
                        }
                      />
                      <span className='sm:hidden'>{t('Regex')}</span>
                    </label>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      className='h-7 w-7'
                      aria-label={t('Remove')}
                      title={t('Remove')}
                      disabled={draft.edits.length === 1}
                      onClick={() =>
                        update({
                          edits: draft.edits.filter(
                            (_, position) => position !== index
                          ),
                        })
                      }
                    >
                      <Trash2 className='h-3 w-3' />
                    </Button>
                  </div>
                ))}
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={draft.edits.length >= MAX_EDITS}
                  onClick={() =>
                    update({ edits: [...draft.edits, emptyEditRow()] })
                  }
                >
                  <Plus className='h-3 w-3' />
                  {t('Add a step')}
                </Button>
                <FieldError message={errors.edits} />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Steps run in order on the original error, each on the result of the previous one. Matching ignores case. With Regex checked, use $1 to reuse a group and $$ for a literal $. If nothing is left, the default message is shown.'
                  )}
                </p>
                {!draft.statusCodes.trim() &&
                  !draft.errorCodes.trim() &&
                  !draft.keywords.trim() && (
                    <p className='text-warning text-xs'>
                      {t(
                        'This rule has no conditions, so it catches every error, upstream errors included. When none of its steps match, users see the original error instead of the default message.'
                      )}
                    </p>
                  )}
              </div>
              <div className='grid gap-3 sm:grid-cols-3'>{statusCodeField}</div>
            </div>
          )}
          {draft.action === 'replace' && (
            <div className='grid gap-3 sm:grid-cols-3'>
              <div className='grid gap-1.5 sm:col-span-2'>
                <Label htmlFor='red-rule-message'>
                  {t('Message users will see')}
                </Label>
                <Input
                  id='red-rule-message'
                  value={draft.message}
                  maxLength={MAX_MESSAGE_LENGTH}
                  onChange={(event) => update({ message: event.target.value })}
                />
                <FieldError message={errors.message} />
              </div>
              {statusCodeField}
            </div>
          )}
          {draft.action === 'keep' && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Users see the original error. Use this for errors they can fix themselves, such as a prompt that is too long.'
              )}
            </p>
          )}
        </div>

        <Separator />

        <div className='grid gap-1.5'>
          <Label htmlFor='red-rule-name'>{t('Note')}</Label>
          <Input
            id='red-rule-name'
            value={draft.name}
            maxLength={MAX_NAME_LENGTH}
            placeholder={t('Optional, for your own reference')}
            onChange={(event) => update({ name: event.target.value })}
          />
          <FieldError message={errors.name} />
        </div>
      </div>
    </Dialog>
  )
}
