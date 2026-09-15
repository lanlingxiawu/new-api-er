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
import { useQueryClient } from '@tanstack/react-query'
import type { Table } from '@tanstack/react-table'
import { Power, PowerOff, Tag, Trash2, Wallet } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { getCurrencyLabel } from '@/lib/currency'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { batchSetChannelDailyLimit } from '../api'
import { DAILY_LIMIT_RECOVER_MODE_OPTIONS } from '../constants'
import {
  DAILY_LIMIT_AMOUNT_TOO_SMALL_MESSAGE,
  buildDailyLimitRecoverFields,
  channelsQueryKeys,
  dailyLimitAmountToQuota,
  handleBatchDelete,
  handleBatchDisable,
  handleBatchEnable,
  handleBatchSetTag,
  isValidDailyLimitRecoverMinutes,
  normalizeLeadingZeros,
} from '../lib'
import {
  DAILY_LIMIT_RECOVER_MINUTES_DEFAULT,
  DAILY_LIMIT_RECOVER_MINUTES_MAX,
  DAILY_LIMIT_RECOVER_MINUTES_MIN,
  type Channel,
} from '../types'
import {
  DailyLimitRecoverSelect,
  type DailyLimitRecoverSelectValue,
} from './daily-limit-recover-select'

interface DataTableBulkActionsProps<TData> {
  table: Table<TData>
}

export function DataTableBulkActions<TData>({
  table,
}: DataTableBulkActionsProps<TData>) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [showTagDialog, setShowTagDialog] = useState(false)
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [tagValue, setTagValue] = useState('')
  const [showDailyLimitDialog, setShowDailyLimitDialog] = useState(false)
  const [dailyLimitValue, setDailyLimitValue] = useState('')
  // 默认「保持不变」：批量设置只改动管理员明确选择的项。
  const [dailyLimitRecoverMode, setDailyLimitRecoverMode] =
    useState<DailyLimitRecoverSelectValue>('keep')
  const [dailyLimitRecoverMinutes, setDailyLimitRecoverMinutes] = useState(
    String(DAILY_LIMIT_RECOVER_MINUTES_DEFAULT)
  )
  const [dailyLimitSubmitting, setDailyLimitSubmitting] = useState(false)
  const dailyLimitRecoverDescription = DAILY_LIMIT_RECOVER_MODE_OPTIONS.find(
    (option) => option.value === dailyLimitRecoverMode
  )?.description
  const dailyLimitNothingToChange =
    dailyLimitValue.trim() === '' && dailyLimitRecoverMode === 'keep'
  const currentUser = useAuthStore((s) => s.auth.user)
  const canEditSensitive = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
  )

  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedIds = selectedRows.reduce<number[]>((ids, row) => {
    const id = (row.original as Channel).id

    if (typeof id === 'number') {
      ids.push(id)
    }

    return ids
  }, [])

  const handleClearSelection = () => {
    table.resetRowSelection()
  }

  const handleEnableAll = () => {
    handleBatchEnable(selectedIds, queryClient, handleClearSelection)
  }

  const handleDisableAll = () => {
    handleBatchDisable(selectedIds, queryClient, handleClearSelection)
  }

  const handleDeleteAll = () => {
    if (!canEditSensitive) return
    handleBatchDelete(selectedIds, queryClient, () => {
      setShowDeleteConfirm(false)
      handleClearSelection()
    })
  }

  const handleSetTag = () => {
    handleBatchSetTag(selectedIds, tagValue || null, queryClient, () => {
      setShowTagDialog(false)
      setTagValue('')
      handleClearSelection()
    })
  }

  // 无论取消、点遮罩关闭还是提交成功，关闭时都回到默认值，下次打开不会带着上次的输入。
  const handleDailyLimitDialogOpenChange = (open: boolean) => {
    setShowDailyLimitDialog(open)
    if (!open) {
      setDailyLimitValue('')
      setDailyLimitRecoverMode('keep')
      setDailyLimitRecoverMinutes(String(DAILY_LIMIT_RECOVER_MINUTES_DEFAULT))
    }
  }

  const handleSetDailyLimit = async () => {
    if (
      !canEditSensitive ||
      dailyLimitSubmitting ||
      dailyLimitNothingToChange
    ) {
      return
    }
    const trimmed = dailyLimitValue.trim()
    // 留空 = 本次不修改上限；要清除上限必须显式填 0。
    const amount = trimmed === '' ? undefined : Number(trimmed)
    if (amount !== undefined && (!Number.isFinite(amount) || amount < 0)) {
      toast.error(t('Please enter an amount greater than or equal to 0'))
      return
    }
    let quotaLimit: number | undefined
    if (amount !== undefined) {
      const quota = dailyLimitAmountToQuota(amount)
      if (quota === null) {
        toast.error(t(DAILY_LIMIT_AMOUNT_TOO_SMALL_MESSAGE))
        return
      }
      quotaLimit = quota
    }
    let recoverMinutes: number | undefined
    if (dailyLimitRecoverMode === 'after_minutes') {
      const trimmedMinutes = dailyLimitRecoverMinutes.trim()
      if (trimmedMinutes === '') {
        toast.error(t('Recovery interval is required'))
        return
      }
      recoverMinutes = Number(trimmedMinutes)
      if (!isValidDailyLimitRecoverMinutes(recoverMinutes)) {
        toast.error(t('Enter a whole number of minutes between 1 and 10080'))
        return
      }
    }
    setDailyLimitSubmitting(true)
    try {
      const res = await batchSetChannelDailyLimit({
        ids: selectedIds,
        daily_quota_limit: quotaLimit,
        // 「保持不变」时两列都不发送，后端据此不改动各渠道的恢复方式。
        ...(dailyLimitRecoverMode === 'keep'
          ? {}
          : buildDailyLimitRecoverFields(
              dailyLimitRecoverMode,
              recoverMinutes
            )),
      })
      if (!res.success) {
        toast.error(res.message || t('Failed to update daily limit'))
        return
      }
      toast.success(
        t('Daily limit updated for {{count}} channel(s)', {
          count: res.data ?? selectedIds.length,
        })
      )
      await queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      handleDailyLimitDialogOpenChange(false)
      handleClearSelection()
    } catch {
      toast.error(t('Failed to update daily limit'))
    } finally {
      setDailyLimitSubmitting(false)
    }
  }

  return (
    <>
      <BulkActionsToolbar table={table} entityName={t('channel')}>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                onClick={handleEnableAll}
                className='size-8'
                aria-label={t('Enable selected channels')}
                title={t('Enable selected channels')}
              />
            }
          >
            <Power />
            <span className='sr-only'>{t('Enable selected channels')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Enable selected channels')}</p>
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                onClick={handleDisableAll}
                className='size-8'
                aria-label={t('Disable selected channels')}
                title={t('Disable selected channels')}
              />
            }
          >
            <PowerOff />
            <span className='sr-only'>{t('Disable selected channels')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Disable selected channels')}</p>
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                onClick={() => setShowTagDialog(true)}
                className='size-8'
                aria-label={t('Set tag for selected channels')}
                title={t('Set tag for selected channels')}
              />
            }
          >
            <Tag />
            <span className='sr-only'>
              {t('Set tag for selected channels')}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Set tag for selected channels')}</p>
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                onClick={() => {
                  if (!canEditSensitive) return
                  setShowDailyLimitDialog(true)
                }}
                aria-disabled={!canEditSensitive}
                className={cn(
                  'size-8',
                  !canEditSensitive && 'cursor-not-allowed opacity-50'
                )}
                aria-label={t('Set daily amount limit')}
                title={
                  canEditSensitive
                    ? t('Set daily amount limit')
                    : t('No permission to perform this action')
                }
              />
            }
          >
            <Wallet />
            <span className='sr-only'>{t('Set daily amount limit')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {canEditSensitive
                ? t('Set daily amount limit')
                : t('No permission to perform this action')}
            </p>
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                onClick={() => {
                  if (!canEditSensitive) return
                  setShowDeleteConfirm(true)
                }}
                aria-disabled={!canEditSensitive}
                className={cn(
                  'size-8',
                  !canEditSensitive && 'cursor-not-allowed opacity-50'
                )}
                aria-label={t('Delete selected channels')}
                title={
                  canEditSensitive
                    ? t('Delete selected channels')
                    : t('No permission to perform this action')
                }
              />
            }
          >
            <Trash2 />
            <span className='sr-only'>{t('Delete selected channels')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {canEditSensitive
                ? t('Delete selected channels')
                : t('No permission to perform this action')}
            </p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      {/* Set Tag Dialog */}
      <Dialog
        open={showTagDialog}
        onOpenChange={setShowTagDialog}
        title={t('Set Tag')}
        description={
          <>
            {t('Set a tag for')}
            {selectedIds.length}{' '}
            {t('selected channel(s). Leave empty to remove tag.')}
          </>
        }
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => {
                setShowTagDialog(false)
                setTagValue('')
              }}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={handleSetTag}>{t('Set Tag')}</Button>
          </>
        }
      >
        <div className='grid gap-4 py-4'>
          <div className='grid gap-2'>
            <Label htmlFor='tag'>{t('Tag')}</Label>
            <Input
              id='tag'
              placeholder={t('Enter tag name (optional)')}
              value={tagValue}
              onChange={(e) => setTagValue(e.target.value)}
            />
          </div>
        </div>
      </Dialog>

      {/* Set Daily Amount Limit Dialog */}
      <Dialog
        open={showDailyLimitDialog}
        onOpenChange={handleDailyLimitDialogOpenChange}
        title={t('Set daily amount limit')}
        description={t(
          'Applies to {{count}} channel(s). Leave blank to keep; 0 removes the limit.',
          { count: selectedIds.length }
        )}
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => handleDailyLimitDialogOpenChange(false)}
              disabled={dailyLimitSubmitting}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={handleSetDailyLimit}
              disabled={dailyLimitSubmitting || dailyLimitNothingToChange}
            >
              {dailyLimitSubmitting ? t('Saving...') : t('Save')}
            </Button>
          </>
        }
      >
        <div className='grid gap-4 py-4'>
          <Alert>
            <AlertDescription>
              {t('Raising the limit does not re-enable disabled channels.')}
            </AlertDescription>
          </Alert>
          <div className='grid gap-2'>
            <Label htmlFor='daily-limit-amount'>
              {t('Daily Amount Limit')} ({getCurrencyLabel()})
            </Label>
            <Input
              id='daily-limit-amount'
              type='number'
              min={0}
              step='any'
              placeholder={t('Leave empty to keep unchanged')}
              value={dailyLimitValue}
              onChange={(e) =>
                setDailyLimitValue(normalizeLeadingZeros(e.target.value))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Counts upstream cost: model price × cost ratio (1.0 if unset), excluding group ratios.'
              )}
            </p>
          </div>
          <div className='grid gap-2'>
            <Label htmlFor='daily-limit-recover-mode'>
              {t('Recovery mode')}
            </Label>
            <DailyLimitRecoverSelect
              id='daily-limit-recover-mode'
              allowKeepUnchanged
              value={dailyLimitRecoverMode}
              onValueChange={setDailyLimitRecoverMode}
            />
            {dailyLimitRecoverDescription && (
              <p className='text-muted-foreground text-xs'>
                {t(dailyLimitRecoverDescription)}
              </p>
            )}
          </div>
          {dailyLimitRecoverMode === 'after_minutes' && (
            <div className='grid gap-2'>
              <Label htmlFor='daily-limit-recover-minutes'>
                {t('Recovery interval (minutes)')}
              </Label>
              <Input
                id='daily-limit-recover-minutes'
                type='number'
                min={DAILY_LIMIT_RECOVER_MINUTES_MIN}
                max={DAILY_LIMIT_RECOVER_MINUTES_MAX}
                step={1}
                placeholder={String(DAILY_LIMIT_RECOVER_MINUTES_DEFAULT)}
                value={dailyLimitRecoverMinutes}
                onChange={(e) =>
                  setDailyLimitRecoverMinutes(
                    normalizeLeadingZeros(e.target.value)
                  )
                }
              />
              <p className='text-muted-foreground text-xs'>
                {t('1–10080 minutes (up to 7 days)')}
              </p>
            </div>
          )}
        </div>
      </Dialog>

      {/* Delete Confirmation Dialog */}
      <Dialog
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        title={t('Delete Channels?')}
        description={
          <>
            {t('Are you sure you want to delete')}
            {selectedIds.length}{' '}
            {t('channel(s)? This action cannot be undone.')}
          </>
        }
        contentHeight='auto'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => setShowDeleteConfirm(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              onClick={handleDeleteAll}
              disabled={!canEditSensitive}
            >
              {t('Delete')}
            </Button>
          </>
        }
      >
        {' '}
      </Dialog>
    </>
  )
}
