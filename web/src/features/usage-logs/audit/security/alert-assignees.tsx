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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import type { SecurityAssignee } from './api'

export function AlertAssignees(props: {
  assignees?: SecurityAssignee[]
  legacyID?: number
}) {
  const { t } = useTranslation()
  if (!props.assignees?.length) {
    return props.legacyID ? `#${props.legacyID}` : t('Unassigned')
  }
  return (
    <div className='flex min-w-0 flex-col gap-1'>
      {props.assignees.map((person) => (
        <div
          key={`${person.source}-${person.id}`}
          className='flex flex-wrap items-center gap-1.5 break-words'
        >
          <span>
            {person.username
              ? `${person.username} #${person.id}`
              : `#${person.id}`}
          </span>
          {person.source === 'owner' && (
            <Badge variant='secondary'>
              {t('Automatically assigned employee')}
            </Badge>
          )}
        </div>
      ))}
    </div>
  )
}
