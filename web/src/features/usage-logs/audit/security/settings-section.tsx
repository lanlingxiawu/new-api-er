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
import { ChevronDown } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

export function SettingsSection({
  children,
  open,
  onOpenChange,
}: {
  children: ReactNode
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useTranslation()
  return (
    <Collapsible open={open} onOpenChange={onOpenChange} className='space-y-4'>
      <CollapsibleTrigger
        render={
          <Button type='button' variant='ghost' size='sm' className='group' />
        }
      >
        {t('More settings')}
        <ChevronDown
          className='size-3.5 group-data-[panel-open]:rotate-180'
          aria-hidden
        />
      </CollapsibleTrigger>
      <CollapsibleContent className='space-y-4'>{children}</CollapsibleContent>
    </Collapsible>
  )
}
