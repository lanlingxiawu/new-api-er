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
import { AlertTriangle } from 'lucide-react'
import type { ReactNode } from 'react'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

export function DetailRow(props: {
  label: ReactNode
  value: ReactNode
  mono?: boolean
  muted?: boolean
  size?: 'xs' | 'sm'
  warning?: string
  className?: string
}) {
  return (
    <div
      className={cn(
        'grid min-w-0 grid-cols-[5.25rem_minmax(0,1fr)] gap-2 text-sm sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-3',
        props.className
      )}
    >
      <span
        className={cn(
          'text-muted-foreground min-w-0',
          props.size === 'sm' ? 'text-sm' : 'text-xs'
        )}
      >
        {props.label}
      </span>
      <span
        className={cn(
          'max-w-full min-w-0 break-all sm:wrap-break-word',
          props.size === 'sm' ? 'text-sm' : 'text-xs',
          props.mono && 'font-mono',
          props.muted && 'text-muted-foreground',
          props.warning && 'font-medium text-warning'
        )}
        title={props.warning}
      >
        {props.warning ? (
          <span className='inline-flex items-center gap-1'>
            <AlertTriangle className='size-3 shrink-0' aria-hidden='true' />
            {props.value}
            <span className='sr-only'>{props.warning}</span>
          </span>
        ) : (
          props.value
        )}
      </span>
    </div>
  )
}

export function DetailSection(props: {
  icon?: ReactNode
  iconTone?: IconBadgeTone
  label: string
  variant?: 'default' | 'danger'
  children: ReactNode
  contentClassName?: string
  size?: 'xs' | 'sm'
}) {
  const isDanger = props.variant === 'danger'
  const iconTone = isDanger ? 'destructive' : props.iconTone
  return (
    <div className='min-w-0 space-y-1.5'>
      <Label
        className={cn(
          'flex items-center gap-1.5 text-xs font-semibold',
          props.size === 'sm' && 'text-sm',
          isDanger && 'text-destructive'
        )}
      >
        {props.icon && (
          <IconBadge tone={iconTone} size='xs'>
            {props.icon}
          </IconBadge>
        )}
        {props.label}
      </Label>
      <div
        className={cn(
          'min-w-0 space-y-1 overflow-hidden rounded-md border p-2.5 max-sm:p-2',
          isDanger ? 'border-destructive/30 bg-destructive/5' : 'bg-muted/30',
          props.contentClassName
        )}
      >
        {props.children}
      </div>
    </div>
  )
}
