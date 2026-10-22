import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  observeExportProgress,
  type ExportProgressObservation,
} from '../remaining-time'
import type { ExportJob } from '../types'

export function ExportRemainingTime(props: { job: ExportJob }) {
  const { t } = useTranslation()
  const { status, progress, updated_at: updatedAt } = props.job
  const [observation, setObservation] = useState<ExportProgressObservation>()
  useEffect(() => {
    setObservation((previous) =>
      observeExportProgress(previous, status, progress, updatedAt * 1000)
    )
  }, [status, progress, updatedAt])

  if (status !== 'running') return null
  let message = t('Estimating remaining time...')
  if (observation?.seconds != null) {
    message =
      observation.seconds < 60
        ? t('Estimated remaining time: less than a minute')
        : t('Estimated remaining time: about {{minutes}} min', {
            minutes: Math.ceil(observation.seconds / 60),
          })
  }
  return <p className='text-muted-foreground text-xs'>{message}</p>
}
