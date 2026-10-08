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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, ChevronUp, Edit, Plus, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { StatusBadge, StatusBadgeList } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'

import {
  getRelayErrorDisplayPresets,
  previewRelayErrorDisplay,
  updateSystemOptionGroup,
} from '../api'
import { useSettingsPageAccess } from '../components/settings-page-access-context'
import { SettingsSection } from '../components/settings-section'
import type {
  RelayErrorDisplayDraft,
  RelayErrorPreviewResponse,
  SystemTuningSettings,
} from '../types'
import {
  emptyRuleRow,
  charCount,
  firstRuleError,
  MAX_MESSAGE_LENGTH,
  MAX_RULES,
  parseRows,
  type RuleRow,
  sourceLabel,
  splitList,
  toRow,
  toRule,
  validateRuleRow,
} from './relay-error-display-rules'
import { RelayErrorRuleDialog } from './relay-error-rule-dialog'

function BadgeList(props: { items: string[] }) {
  if (props.items.length === 0) return null
  return (
    <StatusBadgeList
      items={props.items}
      max={3}
      getKey={(item) => item}
      renderItem={(item) => (
        <StatusBadge
          label={item}
          variant='neutral'
          size='sm'
          copyable={false}
        />
      )}
    />
  )
}

type RelayErrorDisplayStatusResponse = {
  success: boolean
  message: string
  data?: { skipped: { rule: number; reason: string }[] }
}

// Stored rules the server skips because they no longer validate (for example
// saved before a limit existed); the rest of the saved setting stays in effect.
async function getRelayErrorDisplayStatus(scope: string) {
  const res = await api.get<RelayErrorDisplayStatusResponse>(
    '/api/option/relay-error-display/status',
    { params: { scope } }
  )
  return res.data
}

const relayErrorDisplayStatusKey = 'relay-error-display-status'

function storedDraft(settings: SystemTuningSettings): RelayErrorDisplayDraft {
  return {
    enabled: Boolean(settings['relay_error_display_setting.enabled']),
    hide_upstream_errors: Boolean(
      settings['relay_error_display_setting.hide_upstream_errors']
    ),
    default_message: (
      settings['relay_error_display_setting.default_message'] ?? ''
    ).trim(),
    rules: JSON.stringify(
      parseRows(settings['relay_error_display_setting.rules'] ?? '').map(toRule)
    ),
  }
}

export function RelayErrorDisplaySection({
  settings,
}: {
  settings: SystemTuningSettings
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const { scope } = useSettingsPageAccess()

  const storedRules = settings['relay_error_display_setting.rules'] ?? ''
  const [enabled, setEnabled] = useState(
    Boolean(settings['relay_error_display_setting.enabled'])
  )
  const [hideUpstream, setHideUpstream] = useState(
    Boolean(settings['relay_error_display_setting.hide_upstream_errors'])
  )
  const [defaultMessage, setDefaultMessage] = useState(
    settings['relay_error_display_setting.default_message'] ?? ''
  )
  const [rows, setRows] = useState<RuleRow[]>(() => parseRows(storedRules))
  const [nextId, setNextId] = useState(() => parseRows(storedRules).length)

  const statusQuery = useQuery({
    queryKey: [relayErrorDisplayStatusKey, scope, i18n.language, storedRules],
    queryFn: () => getRelayErrorDisplayStatus(scope),
  })
  // Each reason says what to fix; identical reasons are listed once.
  const skippedReasons = [
    ...new Set(
      statusQuery.data?.success && statusQuery.data.data
        ? statusQuery.data.data.skipped.map((item) => item.reason)
        : []
    ),
  ]

  useEffect(() => {
    setEnabled(Boolean(settings['relay_error_display_setting.enabled']))
    setHideUpstream(
      Boolean(settings['relay_error_display_setting.hide_upstream_errors'])
    )
    setDefaultMessage(
      settings['relay_error_display_setting.default_message'] ?? ''
    )
    const parsed = parseRows(
      settings['relay_error_display_setting.rules'] ?? ''
    )
    setRows(parsed)
    // Never lower it: a dialog may still hold a rule with an id handed out
    // before this reset, and reusing that id would make two rules one.
    setNextId((current) => Math.max(current, parsed.length))
  }, [settings])

  const draft = useMemo<RelayErrorDisplayDraft>(
    () => ({
      enabled,
      hide_upstream_errors: hideUpstream,
      default_message: defaultMessage.trim(),
      rules: JSON.stringify(rows.map(toRule)),
    }),
    [enabled, hideUpstream, defaultMessage, rows]
  )
  const dirty = useMemo(() => {
    const stored = storedDraft(settings)
    return (
      stored.enabled !== draft.enabled ||
      stored.hide_upstream_errors !== draft.hide_upstream_errors ||
      stored.default_message !== draft.default_message ||
      stored.rules !== draft.rules
    )
  }, [settings, draft])

  // ---- rule editing
  const [editing, setEditing] = useState<{
    rule: RuleRow
    isNew: boolean
  } | null>(null)

  const openNewRule = () => {
    setEditing({ rule: emptyRuleRow(nextId), isNew: true })
    setNextId((current) => current + 1)
  }

  const confirmRule = (rule: RuleRow) => {
    setRows((current) =>
      current.some((row) => row.id === rule.id)
        ? current.map((row) => (row.id === rule.id ? rule : row))
        : [...current, rule]
    )
  }

  const moveRow = (index: number, delta: number) =>
    setRows((current) => {
      const target = index + delta
      if (target < 0 || target >= current.length) return current
      const next = [...current]
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })

  // ---- save
  const saveMutation = useMutation({
    mutationFn: (value: RelayErrorDisplayDraft) =>
      updateSystemOptionGroup({
        scope,
        module: 'relay_error_display_setting',
        values: {
          enabled: String(value.enabled),
          hide_upstream_errors: String(value.hide_upstream_errors),
          default_message: value.default_message,
          rules: value.rules,
        },
      }),
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message)
        return
      }
      queryClient.invalidateQueries({ queryKey: ['system-options', scope] })
      queryClient.invalidateQueries({
        queryKey: [relayErrorDisplayStatusKey],
      })
      toast.success(t('Setting updated successfully'))
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const save = async () => {
    if (charCount(defaultMessage.trim()) > MAX_MESSAGE_LENGTH) {
      toast.error(t('The message can be at most 500 characters.'))
      return
    }
    if (rows.length > MAX_RULES) {
      toast.error(t('Use at most 100 rules.'))
      return
    }
    // Rules are validated in the dialog; presets and stored rules are checked
    // here so nothing invalid reaches the server unnoticed.
    const invalid = rows.findIndex(
      (row) => firstRuleError(validateRuleRow(row, t)) !== ''
    )
    if (invalid >= 0) {
      toast.error(
        `${t('Rule')} ${invalid + 1}: ${firstRuleError(validateRuleRow(rows[invalid], t))}`
      )
      setEditing({ rule: rows[invalid], isNew: false })
      return
    }
    await saveMutation.mutateAsync(draft)
  }

  const presetMutation = useMutation({
    mutationFn: () => getRelayErrorDisplayPresets(scope, i18n.language),
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Could not load the preset rules.'))
        return
      }
      const presets = response.data
      setRows((current) => [
        ...current,
        ...presets.map((rule, index) => toRow(rule, nextId + index)),
      ])
      setNextId((current) => current + presets.length)
      toast.success(t('Preset rules added. Review them, then save to apply.'))
    },
    onError: (error: Error) => toast.error(error.message),
  })

  // ---- test
  const [testOpen, setTestOpen] = useState(false)
  const [sampleSource, setSampleSource] = useState<'upstream' | 'local'>(
    'upstream'
  )
  const [sampleStatus, setSampleStatus] = useState('503')
  const [sampleCode, setSampleCode] = useState('')
  const [sampleMessage, setSampleMessage] = useState('')
  const [preview, setPreview] = useState<RelayErrorPreviewResponse['data']>()

  // A result describes the draft it was computed for; drop it once the draft
  // changes, and ignore a result that arrives for an older draft, so a rule
  // number never points at a different rule.
  const latestDraft = useRef(draft)
  useEffect(() => {
    latestDraft.current = draft
    setPreview(undefined)
  }, [draft])

  const previewMutation = useMutation({
    mutationFn: (sent: RelayErrorDisplayDraft) =>
      previewRelayErrorDisplay({
        scope,
        setting: sent,
        sample: {
          source: sampleSource,
          status_code: Number(sampleStatus) || 0,
          error_code: sampleCode.trim(),
          message: sampleMessage,
        },
      }),
    onSuccess: (response, sent) => {
      if (sent !== latestDraft.current) return
      if (!response.success || !response.data) {
        setPreview(undefined)
        toast.error(response.message)
        return
      }
      setPreview(response.data)
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const matchedRule =
    preview && preview.rule_index >= 0 ? rows[preview.rule_index] : undefined

  return (
    <SettingsSection title={t('Error messages shown to users')}>
      <div className='space-y-6'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Replace the error messages API users receive, so they cannot learn about your upstream providers. Admin logs always keep the original text, and retries and automatic channel disabling are not affected.'
          )}
        </p>

        {skippedReasons.length > 0 && (
          <Alert variant='destructive'>
            <AlertTitle>
              {t('Part of the saved settings is invalid and is being skipped')}
            </AlertTitle>
            <AlertDescription className='space-y-1 text-sm'>
              <p>
                {t(
                  'Everything else in the saved settings still applies. Fix the items below, then save.'
                )}
              </p>
              <ul className='list-disc space-y-0.5 pl-4'>
                {skippedReasons.map((reason) => (
                  <li key={reason}>{reason}</li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        )}

        <div className='flex items-center justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-1'>
            <Label htmlFor='relay-error-display-enabled'>
              {t('Replace error messages')}
            </Label>
            <p className='text-muted-foreground text-xs'>
              {t('When off, users see error messages exactly as before.')}
            </p>
          </div>
          <Switch
            id='relay-error-display-enabled'
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </div>

        <div
          className={
            enabled ? 'space-y-6' : 'space-y-6 opacity-60 transition-opacity'
          }
        >
          <div className='grid gap-4 md:grid-cols-2'>
            <div className='grid gap-1.5'>
              <Label htmlFor='relay-error-display-unmatched'>
                {t('Upstream errors no rule matches')}
              </Label>
              <Select
                items={[
                  { value: 'hide', label: t('Show the default message') },
                  { value: 'keep', label: t('Show the original message') },
                ]}
                value={hideUpstream ? 'hide' : 'keep'}
                onValueChange={(next) => {
                  if (next) setHideUpstream(next === 'hide')
                }}
              >
                <SelectTrigger
                  id='relay-error-display-unmatched'
                  className='w-full'
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='hide'>
                      {t('Show the default message')}
                    </SelectItem>
                    <SelectItem value='keep'>
                      {t('Show the original message')}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            {hideUpstream && (
              <div className='grid gap-1.5'>
                <Label htmlFor='relay-error-display-default-message'>
                  {t('Default message for upstream errors')}
                </Label>
                <Input
                  id='relay-error-display-default-message'
                  value={defaultMessage}
                  maxLength={MAX_MESSAGE_LENGTH}
                  placeholder={t(
                    'The service is temporarily unavailable. Please try again later.'
                  )}
                  onChange={(event) => setDefaultMessage(event.target.value)}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Leave empty to use the built-in message, shown in each user’s language.'
                  )}
                </p>
              </div>
            )}
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Your request ID is always appended, so users can still report problems and you can find the original error in the logs.'
            )}
          </p>

          <div className='space-y-3'>
            <div className='flex flex-wrap items-end justify-between gap-2'>
              <div className='space-y-1'>
                <h4 className='text-sm font-medium'>{t('Special rules')}</h4>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Checked from top to bottom; the first match wins. Use them to give certain errors their own message, or to let errors users can act on through unchanged.'
                  )}
                </p>
              </div>
              <div className='flex flex-wrap gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={presetMutation.isPending}
                  onClick={() => presetMutation.mutate()}
                >
                  {presetMutation.isPending
                    ? t('Loading...')
                    : t('Add preset rules')}
                </Button>
                <Button variant='outline' size='sm' onClick={openNewRule}>
                  <Plus className='mr-1 h-3 w-3' />
                  {t('Add rule')}
                </Button>
              </div>
            </div>

            <StaticDataTable
              data={rows}
              getRowKey={(row) => row.id}
              emptyClassName='text-muted-foreground py-8'
              emptyContent={
                hideUpstream
                  ? t(
                      'No rules yet. Every upstream error shows the default message above.'
                    )
                  : t('No rules yet. Every error is shown unchanged.')
              }
              columns={[
                {
                  id: 'index',
                  header: '#',
                  className: 'w-10',
                  cellClassName: 'text-muted-foreground align-top',
                  cell: (_row, index) => index + 1,
                },
                {
                  id: 'conditions',
                  header: t('Conditions'),
                  cellClassName: 'align-top',
                  cell: (row) => (
                    <div className='space-y-1'>
                      <div className='flex flex-wrap items-center gap-1'>
                        <StatusBadge
                          label={sourceLabel(row.source, t)}
                          variant={
                            row.source === 'upstream' ? 'info' : 'neutral'
                          }
                          size='sm'
                          copyable={false}
                        />
                        <BadgeList items={splitList(row.statusCodes)} />
                        <BadgeList items={splitList(row.errorCodes)} />
                        <BadgeList items={splitList(row.keywords)} />
                      </div>
                      {row.name.trim() && (
                        <p className='text-muted-foreground text-xs'>
                          {row.name.trim()}
                        </p>
                      )}
                    </div>
                  ),
                },
                {
                  id: 'action',
                  header: t('Then'),
                  cellClassName: 'align-top',
                  cell: (row) =>
                    row.action === 'keep' ? (
                      <span className='text-muted-foreground text-sm'>
                        {t('Keep the original')}
                      </span>
                    ) : (
                      <div className='max-w-xs space-y-0.5 text-sm'>
                        <p className='break-words'>
                          {row.action === 'edit'
                            ? t('Edit {{count}} part(s)', {
                                count: row.edits.length,
                              })
                            : row.message.trim() || '-'}
                        </p>
                        {row.statusCode.trim() && (
                          <p className='text-muted-foreground text-xs'>
                            {t('HTTP status')} {row.statusCode.trim()}
                          </p>
                        )}
                      </div>
                    ),
                },
                {
                  id: 'actions',
                  header: t('Actions'),
                  className: 'text-right',
                  cellClassName: 'text-right align-top',
                  cell: (row, index) => (
                    <div className='flex justify-end gap-1'>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        aria-label={t('Move up')}
                        title={t('Move up')}
                        disabled={index === 0}
                        onClick={() => moveRow(index, -1)}
                      >
                        <ChevronUp className='h-3 w-3' />
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        aria-label={t('Move down')}
                        title={t('Move down')}
                        disabled={index === rows.length - 1}
                        onClick={() => moveRow(index, 1)}
                      >
                        <ChevronDown className='h-3 w-3' />
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        aria-label={t('Edit')}
                        title={t('Edit')}
                        onClick={() => setEditing({ rule: row, isNew: false })}
                      >
                        <Edit className='h-3 w-3' />
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        aria-label={t('Remove')}
                        title={t('Remove')}
                        onClick={() =>
                          setRows((current) =>
                            current.filter((item) => item.id !== row.id)
                          )
                        }
                      >
                        <Trash2 className='h-3 w-3' />
                      </Button>
                    </div>
                  ),
                },
              ]}
            />
          </div>

          <Collapsible open={testOpen} onOpenChange={setTestOpen}>
            <CollapsibleTrigger
              render={
                <Button
                  type='button'
                  variant='ghost'
                  className='w-full justify-start'
                />
              }
            >
              {testOpen ? '▼' : '▶'} {t('Test an error')}
            </CollapsibleTrigger>
            <CollapsibleContent className='space-y-3 pt-2'>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Paste an error to see what users would receive with the settings above, including unsaved changes.'
                )}
              </p>
              <div className='grid gap-1.5'>
                <Label htmlFor='red-sample-message'>{t('Error message')}</Label>
                <Textarea
                  id='red-sample-message'
                  rows={3}
                  value={sampleMessage}
                  placeholder={t('Paste the error message here')}
                  onChange={(event) => setSampleMessage(event.target.value)}
                />
              </div>
              <div className='grid gap-3 sm:grid-cols-3'>
                <div className='grid gap-1.5'>
                  <Label htmlFor='red-sample-source'>
                    {t('Error comes from')}
                  </Label>
                  <Select
                    items={[
                      { value: 'upstream', label: t('Upstream errors') },
                      { value: 'local', label: t('Errors from this site') },
                    ]}
                    value={sampleSource}
                    onValueChange={(next) => {
                      if (next) setSampleSource(next as 'upstream' | 'local')
                    }}
                  >
                    <SelectTrigger id='red-sample-source' className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='upstream'>
                          {t('Upstream errors')}
                        </SelectItem>
                        <SelectItem value='local'>
                          {t('Errors from this site')}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </div>
                <div className='grid gap-1.5'>
                  <Label htmlFor='red-sample-status'>{t('Status code')}</Label>
                  <Input
                    id='red-sample-status'
                    value={sampleStatus}
                    onChange={(event) => setSampleStatus(event.target.value)}
                  />
                </div>
                <div className='grid gap-1.5'>
                  <Label htmlFor='red-sample-code'>{t('Error code')}</Label>
                  <Input
                    id='red-sample-code'
                    value={sampleCode}
                    placeholder={t('Optional')}
                    onChange={(event) => setSampleCode(event.target.value)}
                  />
                </div>
              </div>
              <div>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={previewMutation.isPending || !sampleMessage.trim()}
                  onClick={() => previewMutation.mutate(draft)}
                >
                  {previewMutation.isPending ? t('Checking...') : t('Test')}
                </Button>
              </div>
              {preview && (
                <div className='bg-muted/40 space-y-1 rounded-lg border p-3 text-sm'>
                  <p>
                    <span className='text-muted-foreground'>
                      {preview.replace
                        ? t('Users will see:')
                        : t('Unchanged. Users will see:')}
                    </span>{' '}
                    <span className='font-medium break-all'>
                      {preview.message}
                    </span>
                  </p>
                  <p className='text-muted-foreground flex flex-wrap items-center gap-x-1 text-xs'>
                    <span>
                      {t('HTTP status')} {preview.status_code} ·
                    </span>
                    {matchedRule ? (
                      <Button
                        variant='link'
                        size='sm'
                        className='h-auto p-0 text-xs'
                        onClick={() =>
                          setEditing({ rule: matchedRule, isNew: false })
                        }
                      >
                        {t('Matched rule')} {preview.rule_index + 1}
                        {preview.rule_name ? ` · ${preview.rule_name}` : ''}
                      </Button>
                    ) : (
                      <span>
                        {preview.replace
                          ? t(
                              'No rule matched; the default message for upstream errors was used.'
                            )
                          : t('No rule matched.')}
                      </span>
                    )}
                  </p>
                  {!enabled && (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Replacement is currently off; this is what users will see once you turn it on and save.'
                      )}
                    </p>
                  )}
                </div>
              )}
            </CollapsibleContent>
          </Collapsible>
        </div>

        <div className='flex items-center justify-end gap-3'>
          {dirty && (
            <span className='text-muted-foreground text-xs'>
              {t('Unsaved changes')}
            </span>
          )}
          <Button disabled={saveMutation.isPending} onClick={save}>
            {saveMutation.isPending ? t('Saving...') : t('Save')}
          </Button>
        </div>
      </div>

      {editing && (
        // Mounted per edit, so every open starts from the rule it was given.
        <RelayErrorRuleDialog
          key={editing.rule.id}
          open
          onOpenChange={(open) => {
            if (!open) setEditing(null)
          }}
          rule={editing.rule}
          isNew={editing.isNew}
          onConfirm={confirmRule}
        />
      )}
    </SettingsSection>
  )
}
