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
import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { useAuthStore } from '@/stores/auth-store'

import type { AuditLog } from '../api'
import { getIPLocation, type IPLocation } from '../lib/ip-location'
import { IPRiskReasons } from '../security/ip-risk-reasons'
import { securityRules } from '../security/lib/security-rules'

const SecurityAlertDialog = lazy(async () => ({
  default: (await import('../security/monitor')).SecurityAlertDialog,
}))

function auditLocationLabel(
  location: IPLocation | undefined,
  t: (key: string) => string,
  locale: string | undefined
): string {
  if (!location) return t('Loading...')
  if (location.network_type === 'loopback') return t('Loopback address')
  if (location.network_type === 'link_local') return t('Link-local address')
  if (location.network_type === 'reserved') return t('Reserved address')
  if (location.status === 'private') return t('Private network')
  const parts = [
    location.country_code && /^[A-Z]{2}$/.test(location.country_code)
      ? new Intl.DisplayNames(locale, { type: 'region' }).of(
          location.country_code
        )
      : location.country,
    location.region,
    location.city,
  ].filter(Boolean)
  return [...new Set(parts)].join(' · ') || t('Unknown location')
}

export function AuditIPAddress(props: { ip: string }) {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const [open, setOpen] = useState(false)
  const locationId = useId()
  const location = useQuery({
    queryKey: [
      'audit-ip-location',
      user?.id,
      user?.role,
      user?.permissions,
      props.ip,
    ],
    queryFn: ({ signal }) => getIPLocation(props.ip, signal),
    enabled: open && !!props.ip,
    staleTime: 24 * 60 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  return (
    <div className='flex items-center gap-1'>
      {props.ip ? (
        <Tooltip open={open} onOpenChange={setOpen}>
          <TooltipTrigger
            delay={300}
            closeOnClick={false}
            aria-describedby={open ? locationId : undefined}
            render={
              <Button
                variant='ghost'
                size='sm'
                className='h-auto p-0 font-mono break-all whitespace-normal'
                onClick={() => setOpen(true)}
              />
            }
          >
            {props.ip}
          </TooltipTrigger>
          <TooltipContent id={locationId} role='tooltip'>
            {location.isError
              ? t('IP locations unavailable')
              : auditLocationLabel(
                  location.data,
                  t,
                  toIntlLocale(i18n.resolvedLanguage || i18n.language)
                )}
          </TooltipContent>
        </Tooltip>
      ) : (
        <span>—</span>
      )}
      {props.ip && <CopyButton value={props.ip} className='size-6' />}
    </div>
  )
}

export function AuditIPCell(props: {
  entry: Pick<AuditLog, 'event_id' | 'ip' | 'ip_marker'>
  onFilter?: (ip: string) => void
  onSelectAlert?: (id: number) => void
}) {
  const { t } = useTranslation()
  const [showReasons, setShowReasons] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const candidate = props.entry.ip_marker
  const marker =
    !!props.entry.ip &&
    candidate?.event_id === props.entry.event_id &&
    candidate?.ip === props.entry.ip
      ? candidate
      : undefined
  const selectAlert = props.onSelectAlert ?? setSelected
  const high = marker?.level === 'high'
  let variant: 'danger' | 'warning' | 'neutral' = high ? 'danger' : 'warning'
  if (marker?.state === 'resolved') variant = 'neutral'
  let label = t('Needs attention')
  if (high) label = t('High risk')
  if (marker?.state === 'resolved') label = t('Resolved')
  const visible = marker?.state === 'active' || marker?.state === 'resolved'
  const reasons: Record<string, string> = {
    new_login_source: t('New login source'),
    login_failures: t('Repeated login failures'),
    password_spray: t('Password spraying'),
    login_after_failures: t('Login after failures'),
    session_reuse: t('Session reuse'),
  }
  return (
    <div className='min-w-0 space-y-1'>
      <AuditIPAddress ip={props.entry.ip} />
      {visible && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='sm'
                className='h-auto p-0'
                aria-expanded={showReasons}
                onClick={() => setShowReasons((value) => !value)}
              />
            }
          >
            <StatusBadge label={label} variant={variant} copyable={false} />
          </TooltipTrigger>
          <TooltipContent>
            {marker.reasons
              .map(
                (reason) =>
                  reasons[reason.rule] ??
                  t(securityRules[reason.rule] ?? 'Security alert')
              )
              .join(' · ')}
          </TooltipContent>
        </Tooltip>
      )}
      {visible &&
        showReasons &&
        !marker.more &&
        marker.reasons.map((reason) => (
          <Button
            key={`${reason.rule}:${reason.alert_id}`}
            variant='link'
            size='sm'
            className='h-auto p-0 text-xs whitespace-normal'
            onClick={() => selectAlert(reason.alert_id)}
          >
            {reasons[reason.rule] ??
              t(securityRules[reason.rule] ?? 'Security alert')}{' '}
            #{reason.alert_id}
          </Button>
        ))}
      {visible && showReasons && marker.more && (
        <IPRiskReasons
          key={props.entry.event_id}
          eventId={props.entry.event_id}
          onSelect={selectAlert}
        />
      )}
      {props.onFilter && props.entry.ip && (
        <Button
          variant='link'
          size='sm'
          className='h-auto p-0 text-xs'
          onClick={() => props.onFilter?.(props.entry.ip)}
        >
          {t('Filter by this IP')}
        </Button>
      )}
      {selected !== null && (
        <Suspense fallback={<span>{t('Loading...')}</span>}>
          <SecurityAlertDialog
            id={selected}
            onClose={() => setSelected(null)}
          />
        </Suspense>
      )}
    </div>
  )
}
