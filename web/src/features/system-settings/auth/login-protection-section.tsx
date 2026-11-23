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
import { ConfigGroupSection } from '../system-tuning/hot-config-sections'

type LoginProtectionSectionProps = {
  enabled: boolean
  maxFailures: number
}

export function LoginProtectionSection(props: LoginProtectionSectionProps) {
  return (
    <ConfigGroupSection
      title='Login failure protection'
      description='Repeated password or login verification code failures restrict new sign-ins until an administrator unlocks the account. Users are asked to contact sales. Root accounts, existing sessions and API calls remain available.'
      module='login_protection_setting'
      defaults={{ enabled: props.enabled, max_failures: props.maxFailures }}
      fields={[
        {
          key: 'enabled',
          label: 'Enable login failure protection',
          type: 'switch',
          description:
            'Turning this off pauses counting and enforcement. Existing restrictions return when it is enabled again.',
        },
        {
          key: 'max_failures',
          label: 'Maximum consecutive login failures',
          min: 1,
          max: 100,
          description:
            'A completed sign-in resets the count. Restricted accounts require an administrator to unlock them.',
        },
      ]}
    />
  )
}
