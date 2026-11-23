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
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

import type { SecurityHealth } from './api'
import { securityTime, automaticBanMode } from './lib/security-display'
import { ProviderHealth } from './provider-health'

export function SecurityMonitorHealth(props: { health: SecurityHealth }) {
  const { t } = useTranslation()
  const health = props.health
  let label = 'Monitoring active'
  let variant: 'success' | 'warning' | 'neutral' = 'success'
  if (health.suspended) {
    label = 'Monitoring suspended'
    variant = 'neutral'
  } else if (!health.enabled) {
    label = 'Monitoring disabled'
    variant = 'neutral'
  } else if (
    !health.ready ||
    health.error_code ||
    health.history_truncated ||
    health.dropped ||
    health.relay_ip_dropped ||
    !health.last_scan ||
    Date.now() / 1000 - health.last_scan > 120
  ) {
    label = 'Monitoring needs attention'
    variant = 'warning'
  }
  const providerIssue = health.providers?.some(
    (provider) =>
      provider.enabled &&
      (provider.last_error ||
        provider.circuit_until > Date.now() / 1000 ||
        (provider.daily_limit > 0 && provider.used >= provider.daily_limit))
  )
  return (
    <Collapsible className='min-w-0'>
      <div className='flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs'>
        <span role='status'>
          <StatusBadge variant={variant} copyable={false} showDot>
            {t(label)}
          </StatusBadge>
        </span>
        <StatusBadge copyable={false} variant='neutral'>
          {t(
            automaticBanMode(
              health.enabled,
              health.suspended,
              !!health.auto_ban_enabled
            )
          )}
        </StatusBadge>
        <span className='text-muted-foreground'>
          {t('Last scan')}: {securityTime(health.last_scan)}
        </span>
        {health.storage_ready === false && (
          <StatusBadge variant='warning' copyable={false}>
            {t('Historical alert storage')}: {t('Unavailable')}
          </StatusBadge>
        )}
        {providerIssue && (
          <StatusBadge variant='warning' copyable={false}>
            {t('Provider unavailable. Check configuration and retry.')}
          </StatusBadge>
        )}
        <CollapsibleTrigger
          render={
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='group ms-auto'
            />
          }
        >
          {t('Details')}
          <ChevronDown className='size-3.5 transition-transform group-data-[panel-open]:rotate-180' />
        </CollapsibleTrigger>
      </div>
      <CollapsibleContent>
        <div className='bg-card/50 mt-2 flex min-w-0 flex-col gap-3 rounded-lg border p-3'>
          <dl className='grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4'>
            {[
              [
                'Sign-in event capture',
                t(
                  health.capture_enabled && !health.suspended
                    ? 'Enabled'
                    : 'Disabled'
                ),
              ],
              ['Processed event time', securityTime(health.cursor_at)],
              ['Dropped events', health.dropped ?? 0],
              [
                'AI request IP monitoring',
                t(health.relay_ip_enabled ? 'Enabled' : 'Disabled'),
              ],
              [
                'Historical evidence',
                t(
                  health.history_truncated
                    ? 'Incomplete'
                    : 'Coverage is not guaranteed'
                ),
              ],
              [
                'Historical alert storage',
                t(health.storage_ready === false ? 'Unavailable' : 'Available'),
              ],
            ].map(([name, value]) => (
              <div key={name} className='flex min-w-0 flex-col gap-1'>
                <dt className='text-muted-foreground'>{t(String(name))}</dt>
                <dd className='break-words'>{value}</dd>
              </div>
            ))}
          </dl>
          {health.relay_ip_enabled !== undefined && (
            <section className='space-y-2 text-xs'>
              <h4 className='font-medium'>{t('AI request IP observations')}</h4>
              <dl className='grid grid-cols-2 gap-3 lg:grid-cols-4'>
                {[
                  ['Queued', health.relay_ip_queued ?? 0],
                  ['Dropped events', health.relay_ip_dropped ?? 0],
                  ['Not recorded', health.relay_ip_missing ?? 0],
                  [
                    'Last updated',
                    securityTime(health.relay_ip_last_observed ?? 0),
                  ],
                ].map(([label, value]) => (
                  <div key={label} className='min-w-0 space-y-1'>
                    <dt className='text-muted-foreground'>
                      {t(String(label))}
                    </dt>
                    <dd className='break-words'>{value}</dd>
                  </div>
                ))}
              </dl>
            </section>
          )}
          {!!health.providers?.length && (
            <ProviderHealth providers={health.providers} />
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
