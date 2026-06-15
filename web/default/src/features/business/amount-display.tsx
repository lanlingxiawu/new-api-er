import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { formatBusinessAmount } from './format'

export function BusinessAmount({
  value,
  digits,
  className,
  positiveClassName,
  negativeClassName = 'text-destructive',
  neutralClassName,
  markNegative = true,
  showPositiveSign = false,
  isReversal = false,
}: {
  value: number | null | undefined
  digits?: number
  className?: string
  positiveClassName?: string
  negativeClassName?: string
  neutralClassName?: string
  markNegative?: boolean
  showPositiveSign?: boolean
  isReversal?: boolean
}) {
  const { t } = useTranslation()
  const amount = Number(value || 0)
  const isPositive = amount > 0
  const isNegative = amount < 0

  return (
    <span
      className={cn(
        'inline-flex min-w-0 items-center gap-1.5',
        isPositive && positiveClassName,
        isNegative && negativeClassName,
        !isPositive && !isNegative && neutralClassName,
        className
      )}
    >
      <span className='min-w-0 truncate tabular-nums'>
        {showPositiveSign && isPositive ? '+' : ''}
        {formatBusinessAmount(amount, digits)}
      </span>
      {markNegative && isNegative ? (
        isReversal ? (
          <Badge
            variant='outline'
            className='border-muted-foreground/30 bg-muted text-muted-foreground h-5 shrink-0 px-1.5 text-[10px] font-medium'
          >
            {t('Refund/Reversal')}
          </Badge>
        ) : (
          <Badge
            variant='outline'
            className='border-destructive/30 bg-destructive/5 text-destructive h-5 shrink-0 px-1.5 text-[10px] font-medium'
          >
            {t('Loss')}
          </Badge>
        )
      ) : null}
    </span>
  )
}
