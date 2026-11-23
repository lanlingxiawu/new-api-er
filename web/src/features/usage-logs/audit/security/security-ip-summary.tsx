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

import { StatusBadge } from '@/components/status-badge'

import {
  DetailRow,
  DetailSection,
} from '../../components/dialogs/log-detail-layout'
import { isAuditDetailObject } from '../lib/audit-details'
import { auditParameterValue } from '../lib/audit-parameter-value'
import { securityTime } from './lib/security-display'

export function SecurityIPSummary(props: {
  snapshot: Record<string, unknown>
}) {
  const { t } = useTranslation()
  const snapshot = props.snapshot
  const location = [
    ...new Set(
      [snapshot.country || snapshot.country_code, snapshot.state, snapshot.city]
        .filter(
          (value): value is string =>
            typeof value === 'string' && !!value.trim()
        )
        .map((value) => value.trim())
    ),
  ].join(' · ')
  const network = [
    typeof snapshot.asn_number === 'number' && snapshot.asn_number > 0
      ? `AS${snapshot.asn_number}`
      : '',
    typeof snapshot.asn_organization === 'string'
      ? snapshot.asn_organization
      : '',
  ]
    .filter(Boolean)
    .join(' · ')
  const signals = [
    { key: 'is_vpn', label: t('VPN'), variant: 'info' },
    { key: 'is_proxy', label: t('Proxy'), variant: 'info' },
    { key: 'is_tor', label: t('Tor'), variant: 'warning' },
    { key: 'is_abuser', label: t('Abuse intelligence'), variant: 'danger' },
  ] as const
  const detected = signals.filter((signal) => snapshot[signal.key] === true)
  const unknown = signals.filter(
    (signal) => typeof snapshot[signal.key] !== 'boolean'
  )
  const sources = Array.isArray(snapshot.sources)
    ? snapshot.sources.filter(isAuditDetailObject)
    : []
  const evidence = Array.isArray(snapshot.evidence)
    ? snapshot.evidence
        .filter(isAuditDetailObject)
        .filter((item) => item.matched_network || item.reference)
        .slice(0, 3)
    : []
  return (
    <DetailSection label={t('IP intelligence')}>
      {!!location && <DetailRow label={t('Geolocation')} value={location} />}
      {!!network && <DetailRow label={t('ASN organization')} value={network} />}
      <DetailRow
        label={t('Detected signal')}
        value={
          <span className='flex flex-wrap items-center gap-1.5'>
            {detected.map((signal) => (
              <StatusBadge
                key={signal.key}
                copyable={false}
                variant={signal.variant}
              >
                {signal.label}
              </StatusBadge>
            ))}
            {detected.length === 0 && unknown.length === 0 && t('None')}
            {unknown.length > 0 && (
              <span className='text-muted-foreground'>
                {t('Unknown')}:{' '}
                {unknown.map((signal) => signal.label).join(', ')}
              </span>
            )}
          </span>
        }
      />
      {evidence.length > 0 && (
        <DetailRow
          label={t('Evidence')}
          value={
            <span className='flex flex-col gap-1'>
              {evidence.map((item) => {
                const capability =
                  typeof item.capability === 'string' ? item.capability : ''
                const sourceRecord = sources.find(
                  (source) =>
                    source.source === item.source &&
                    (!capability || source.capability === capability)
                )
                const recordedName =
                  typeof sourceRecord?.name === 'string'
                    ? sourceRecord.name.trim()
                    : ''
                let expires = 0
                if (typeof item.expires_at === 'number') {
                  expires = item.expires_at
                } else if (typeof sourceRecord?.expires_at === 'number') {
                  expires = sourceRecord.expires_at
                }
                return (
                  <span key={JSON.stringify(item)}>
                    {[
                      recordedName ||
                        auditParameterValue('source', item.source, '', t),
                      capability
                        ? auditParameterValue('capability', capability, '', t)
                        : '',
                      item.matched_network,
                      item.reference,
                      expires > 0
                        ? `${t('Expires')} ${securityTime(expires)}`
                        : '',
                    ]
                      .filter((value) => typeof value === 'string' && value)
                      .join(' · ')}
                  </span>
                )
              })}
              <span className='text-muted-foreground'>
                {t('Stored intelligence snapshot')}
              </span>
            </span>
          }
        />
      )}
    </DetailSection>
  )
}
