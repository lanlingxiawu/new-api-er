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
import type { TFunction } from 'i18next'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import type { PriceMonitorChannelCost, PriceMonitorRatioReason } from '../types'
import {
  formatCostFactor,
  isCostRatioMismatch,
} from './price-monitor-channel-cost-utils'

// 与后端 maxPriceMonitorCostRatio 一致：只拦明显的误输入。
const MAX_COST_RATIO = 100

function ratioReasonText(reason: PriceMonitorRatioReason, t: TFunction) {
  const texts: Record<PriceMonitorRatioReason, string> = {
    no_source: t(
      'No upstream account token is configured and the upstream log has no usable record'
    ),
    key_not_in_account: t(
      'The channel key does not belong to the configured upstream account'
    ),
    auto_group: t('The key uses the auto group, so the ratio is not fixed'),
    group_missing: t(
      "The upstream account's usable groups do not include this group"
    ),
    credential_rejected: t('The upstream rejected the credential'),
    rate_limited: t('The upstream is rate limiting; retried automatically'),
    unavailable: t('The upstream could not be reached'),
    not_supported: t(
      'The upstream offers neither the new-api nor the sub2api interface, so its group ratio is unavailable'
    ),
    no_consume_log: t('The upstream log has no billed request yet'),
    waiting: t('Queued for the next check'),
    unsupported_channel: t('Not supported for this channel type'),
    sub2api_unsupported: t(
      'This sub2api has no billing API (older version or simple mode)'
    ),
    sub2api_no_group: t(
      'The key has no group on sub2api, or its group is unavailable'
    ),
  }
  return texts[reason] ?? reason
}

function ratioSourceText(cost: PriceMonitorChannelCost, t: TFunction) {
  const sources = {
    official: t('Upstream account API'),
    sub2api: t('sub2api billing API'),
    log: t('Upstream log'),
  }
  const source = sources[cost.ratio_source ?? 'log']
  if (!cost.observed_at) return source
  return `${source} · ${new Date(cost.observed_at * 1000).toLocaleString()}`
}

function CostStatusBadge({
  cost,
  t,
}: {
  cost: PriceMonitorChannelCost
  t: TFunction
}) {
  const percent =
    cost.deviation === undefined
      ? ''
      : `${Math.abs(cost.deviation * 100).toFixed(1)}%`
  switch (cost.status) {
    case 'match':
      return <Badge variant='outline'>{t('Matches')}</Badge>
    case 'cost_low':
      return (
        <Badge variant='destructive'>
          {t('Too low by {{percent}}', { percent })}
        </Badge>
      )
    case 'cost_high':
      return (
        <Badge variant='warning'>
          {t('Too high by {{percent}}', { percent })}
        </Badge>
      )
    case 'free':
      return <Badge variant='outline'>{t('Free upstream group')}</Badge>
    default:
      return <Badge variant='secondary'>{t('Cannot check')}</Badge>
  }
}

type PriceMonitorChannelCostsProps = {
  items: PriceMonitorChannelCost[]
  isLoading: boolean
  canEdit: boolean
  saving: boolean
  highlightChannelId?: number
  onSave: (channelId: number, costRatio: number) => Promise<boolean>
}

/**
 * 成本系数核对：每个渠道的上游分组倍率与本站成本系数对比，并可就地修改成本系数
 * （设计 docs/design/price-monitor-channel-cost-check.md §4）。
 */
export function PriceMonitorChannelCosts({
  items,
  isLoading,
  canEdit,
  saving,
  highlightChannelId,
  onSave,
}: PriceMonitorChannelCostsProps) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<PriceMonitorChannelCost | null>(null)
  const [draft, setDraft] = useState('')
  const [adopting, setAdopting] = useState<PriceMonitorChannelCost | null>(null)

  const draftValue = Number(draft)
  const draftError =
    draft.trim() === '' ||
    !Number.isFinite(draftValue) ||
    draftValue < 0 ||
    draftValue > MAX_COST_RATIO
      ? t('Enter a cost ratio from 0 to {{max}}', { max: MAX_COST_RATIO })
      : undefined

  const openEditor = (cost: PriceMonitorChannelCost) => {
    setEditing(cost)
    setDraft(formatCostFactor(cost.cost_ratio))
  }

  const editDisabledTitle = canEdit
    ? undefined
    : t('Requires permission to edit channels')

  return (
    <>
      <div className='flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b p-3 text-sm'>
        <span className='text-muted-foreground'>
          {t(
            'The cost ratio should equal the ratio the upstream bills our key at. Any difference is reported.'
          )}
        </span>
      </div>
      <Table containerClassName='min-h-0 flex-1 overflow-auto'>
        <TableHeader>
          <TableRow>
            <TableHead className='bg-muted sticky top-0'>
              {t('Channel')}
            </TableHead>
            <TableHead className='bg-muted sticky top-0'>
              {t('Upstream group')}
            </TableHead>
            <TableHead className='bg-muted sticky top-0'>
              {t('Upstream ratio')}
            </TableHead>
            <TableHead className='bg-muted sticky top-0'>
              {t('Cost Ratio')}
            </TableHead>
            <TableHead className='bg-muted sticky top-0'>
              {t('Check result')}
            </TableHead>
            <TableHead className='bg-muted sticky top-0 text-end'>
              {t('Action')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((cost) => {
            const mismatch = isCostRatioMismatch(cost)
            return (
              <TableRow
                key={cost.channel_id}
                className={cn(
                  highlightChannelId === cost.channel_id && 'bg-primary/5'
                )}
              >
                <TableCell className='align-top'>
                  <span className='block font-medium'>{cost.channel_name}</span>
                  <span className='text-muted-foreground text-xs'>
                    #{cost.channel_id}
                  </span>
                </TableCell>
                <TableCell className='align-top'>
                  {cost.upstream_groups?.length
                    ? cost.upstream_groups.join(', ')
                    : cost.upstream_group || '—'}
                </TableCell>
                <TableCell className='align-top'>
                  {cost.upstream_ratio !== undefined ? (
                    <>
                      <span className='block font-medium tabular-nums'>
                        {formatCostFactor(cost.upstream_ratio)}
                      </span>
                      <span className='text-muted-foreground block text-xs'>
                        {ratioSourceText(cost, t)}
                      </span>
                      {cost.peak_multiplier !== undefined && (
                        <span className='text-muted-foreground block text-xs'>
                          {t('Peak {{window}} ×{{multiplier}}', {
                            window: cost.peak_window ?? '',
                            multiplier: formatCostFactor(cost.peak_multiplier),
                          })}
                        </span>
                      )}
                      {cost.reason && (
                        <span className='text-muted-foreground block text-xs'>
                          {t('Last refresh failed: {{reason}}', {
                            reason: ratioReasonText(cost.reason, t),
                          })}
                        </span>
                      )}
                    </>
                  ) : (
                    <span className='text-muted-foreground text-xs'>
                      {cost.reason ? ratioReasonText(cost.reason, t) : '—'}
                    </span>
                  )}
                </TableCell>
                <TableCell className='align-top'>
                  <span className='block font-medium tabular-nums'>
                    {formatCostFactor(cost.cost_ratio)}
                  </span>
                  {!cost.cost_ratio_configured && (
                    <span className='text-muted-foreground text-xs'>
                      {t('Not configured, counted as 1')}
                    </span>
                  )}
                </TableCell>
                <TableCell className='align-top'>
                  <CostStatusBadge cost={cost} t={t} />
                </TableCell>
                <TableCell className='align-top'>
                  <div className='flex justify-end gap-2'>
                    {mismatch && cost.upstream_ratio !== undefined && (
                      <Button
                        size='sm'
                        disabled={!canEdit || saving}
                        title={editDisabledTitle}
                        onClick={() => setAdopting(cost)}
                      >
                        {t('Use upstream ratio')}
                      </Button>
                    )}
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={!canEdit || saving}
                      title={editDisabledTitle}
                      onClick={() => openEditor(cost)}
                    >
                      {t('Edit cost ratio')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
          {!isLoading && items.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={6}
                className='text-muted-foreground text-center'
              >
                {t('No channel has been checked yet. Run a price check first.')}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      <ConfirmDialog
        open={adopting !== null}
        onOpenChange={(open) => !open && setAdopting(null)}
        title={t('Use the upstream ratio as the cost ratio?')}
        desc={
          adopting
            ? t(
                'The cost ratio of {{channel}} changes from {{from}} to {{to}}. Profit, commission and the channel daily limit use it from now on.',
                {
                  channel: adopting.channel_name,
                  from: formatCostFactor(adopting.cost_ratio),
                  to: formatCostFactor(adopting.upstream_ratio),
                }
              )
            : ''
        }
        confirmText={t('Update cost ratio')}
        isLoading={saving}
        handleConfirm={async () => {
          if (!adopting || adopting.upstream_ratio === undefined) return
          if (await onSave(adopting.channel_id, adopting.upstream_ratio)) {
            setAdopting(null)
          }
        }}
      />

      <Dialog
        open={editing !== null}
        onOpenChange={(open) => !open && setEditing(null)}
        title={t('Edit cost ratio')}
        description={editing?.channel_name}
        contentClassName='sm:max-w-md'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setEditing(null)}
              disabled={saving}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={saving || Boolean(draftError)}
              onClick={async () => {
                if (!editing || draftError) return
                if (await onSave(editing.channel_id, draftValue)) {
                  setEditing(null)
                }
              }}
            >
              {saving ? t('Saving...') : t('Save')}
            </Button>
          </>
        }
      >
        <Field data-invalid={Boolean(draftError)}>
          <FieldLabel htmlFor='price-monitor-cost-ratio'>
            {t('Cost Ratio')}
          </FieldLabel>
          <Input
            id='price-monitor-cost-ratio'
            inputMode='decimal'
            value={draft}
            aria-invalid={Boolean(draftError)}
            onChange={(event) => setDraft(event.target.value)}
          />
          {draftError ? (
            <FieldError>{draftError}</FieldError>
          ) : (
            <FieldDescription>
              {editing?.upstream_ratio !== undefined
                ? t('Upstream ratio: {{ratio}}', {
                    ratio: formatCostFactor(editing.upstream_ratio),
                  })
                : t('The upstream ratio is not available for this channel.')}
            </FieldDescription>
          )}
        </Field>
      </Dialog>
    </>
  )
}
