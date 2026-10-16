import { useMutation, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import { getDefaultTimeRange } from '../../lib/utils'
import { createSelfExportJob } from '../api'

const route = getRouteApi('/_authenticated/usage-logs/$section')

export function SelfExportSheet(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  maxRangeSec?: number
}) {
  const { t } = useTranslation()
  const search = route.useSearch()
  const queryClient = useQueryClient()
  const [range, setRange] = useState<{ start?: Date; end?: Date }>(() => {
    const defaults = getDefaultTimeRange()
    return {
      start: search.startTime ? new Date(search.startTime) : defaults.start,
      end: search.endTime ? new Date(search.endTime) : defaults.end,
    }
  })
  const start = range.start?.getTime()
  const end = range.end?.getTime()
  const maxRange = props.maxRangeSec ?? 30 * 86400
  const valid =
    start !== undefined &&
    end !== undefined &&
    start > 0 &&
    end >= start &&
    end - start <= maxRange * 1000
  const create = useMutation({
    mutationFn: createSelfExportJob,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['log-export-jobs', 'self'],
      })
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error, t('Export failed')),
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('New Export')}
      description={t(
        'Export runs in the background. Download the compressed CSV when it is ready.'
      )}
      contentClassName='sm:max-w-md'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={!valid || create.isPending}
            aria-busy={create.isPending}
            onClick={() => {
              if (!valid || start === undefined || end === undefined) return
              create.mutate({
                filters: {
                  start_timestamp: Math.floor(start / 1000),
                  end_timestamp: Math.floor(end / 1000),
                  type:
                    Number(
                      Array.isArray(search.type) ? search.type[0] : search.type
                    ) || undefined,
                  model_name: search.model || undefined,
                  token_name: search.token || undefined,
                  group: search.group || undefined,
                },
                options: {
                  timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
                  header: true,
                  csv_bom: true,
                },
              })
            }}
          >
            {t('Export')}
          </Button>
        </>
      }
    >
      <CompactDateTimeRangePicker
        start={range.start}
        end={range.end}
        onChange={setRange}
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Choose a time range of up to {{days}} days. The other filters on this page also apply.',
          { days: maxRange / 86400 }
        )}
      </p>
      {!valid && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Please select a valid time range')}
        </p>
      )}
    </Dialog>
  )
}
