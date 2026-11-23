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
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'

import {
  DetailRow,
  DetailSection,
} from '../../components/dialogs/log-detail-layout'
import { AlertTriggerCount } from './alert-trigger-count'
import type { SecurityAlert, SecurityFinding } from './api'
import { securityTime } from './lib/security-display'
import { securityRules, singleActionRuleConditions } from './lib/security-rules'

// Describe stored rule results; never re-evaluate partial evidence or substitute
// today's settings for the threshold used when a historical alert was raised.
function describeSecurityRule(
  rule: string,
  t: TFunction,
  snapshot?: SecurityFinding
) {
  if (singleActionRuleConditions[rule]) {
    return { condition: t(singleActionRuleConditions[rule]) }
  }
  switch (rule) {
    case 'multi_account_key_access':
      return {
        condition: snapshot
          ? t(
              'The same IP and client sign in to at least {{loginAccounts}} accounts within {{loginSeconds}} seconds, and at least {{keyAccounts}} of those accounts view keys after sign-in within {{windowSeconds}} seconds.',
              {
                loginAccounts: snapshot.dimensions.account_threshold,
                loginSeconds: snapshot.dimensions.login_window_seconds,
                keyAccounts: snapshot.threshold,
                windowSeconds: snapshot.window_seconds,
              }
            )
          : t(
              'The threshold at the time of this alert was not retained, so its exact value cannot be recovered.'
            ),
      }
    case 'multi_source_key_access':
      return {
        condition: snapshot
          ? t(
              'One account signs in from at least {{loginIPs}} IPs within {{windowSeconds}} seconds and views keys after sign-in from at least {{keyIPs}} of those IPs.',
              {
                loginIPs: snapshot.dimensions.login_ip_threshold,
                keyIPs: snapshot.threshold,
                windowSeconds: snapshot.window_seconds,
              }
            )
          : t(
              'The threshold at the time of this alert was not retained, so its exact value cannot be recovered.'
            ),
      }
    case 'new_source_channel_rewrite':
      return {
        condition: snapshot
          ? t(
              'One operator changes at least {{channels}} channel addresses from a new source within {{windowSeconds}} seconds.',
              {
                channels: snapshot.threshold,
                windowSeconds: snapshot.window_seconds,
              }
            )
          : t(
              'The threshold at the time of this alert was not retained, so its exact value cannot be recovered.'
            ),
      }
    case 'anonymous_network':
      return {
        condition:
          snapshot?.dimensions.tor === 1
            ? t('The event IP was identified as a Tor exit.')
            : t('The event IP was identified as VPN, proxy or Tor.'),
      }
    case 'ip_abuse':
      return {
        condition: t('The event IP has explicit abuse or compromise evidence.'),
      }
    case 'impossible_travel':
      return {
        condition: t(
          'Sign-in distance and travel speed exceeded the recorded thresholds. IP location is approximate.'
        ),
        configuredThreshold: true,
      }
    case 'sensitive_operation':
      return {
        condition: t(
          'A successful operation matched the sensitive-operation policy, covering channel configuration, destructive actions, account security and permissions.'
        ),
        metric: t('Maximum impact per operation'),
      }
    case 'bulk_sensitive_operations':
      return {
        condition: t(
          'The same operator’s cumulative impact on the same resource type within 5 minutes reached the configured threshold. Requests with the same request ID are counted once.'
        ),
        metric: t('Peak cumulative impact in 5 minutes'),
        configuredThreshold: true,
      }
    case 'bulk_key_access':
      return {
        condition: t(
          'The same operator viewed secrets for enough distinct channels within 5 minutes to reach the configured threshold.'
        ),
        metric: t('Peak distinct channels in 5 minutes'),
        configuredThreshold: true,
      }
    case 'login_failures':
      return {
        condition: t(
          'Wrong credentials or verification codes for the same account or unidentified source within 5 minutes reached the configured threshold. Service errors and expired requests are excluded.'
        ),
        metric: t('Peak sign-in failures in 5 minutes'),
        configuredThreshold: true,
      }
    case 'password_spray':
      return {
        condition: t(
          'The same IP caused at least 20 sign-in failures across at least 5 account identifiers within 5 minutes, using wrong credentials or verification codes.'
        ),
        metric: t('Peak sign-in failures in 5 minutes'),
      }
    case 'login_after_failures':
      return {
        condition: t(
          'A successful sign-in followed enough wrong-credential or verification-code failures for the same account in the preceding 15 minutes to reach the configured threshold.'
        ),
        metric: t('Peak failures before sign-in in 15 minutes'),
        configuredThreshold: true,
      }
    case 'recovery_requests':
      return {
        condition: t(
          'The same account identifier received at least 3 password recovery requests within 10 minutes.'
        ),
        metric: t('Peak recovery requests in 10 minutes'),
      }
    case 'permission_failures':
      return {
        condition: t(
          'The same access token received at least 10 permission denials within 5 minutes.'
        ),
        metric: t('Peak permission denials in 5 minutes'),
      }
    case 'session_reuse':
      return {
        condition: t(
          'An invalidated sign-in credential was used again and confirmed by the system.'
        ),
      }
    case 'new_login_source':
      return {
        condition: t(
          'A successful sign-in used a new IP source for an account with a known source established for at least one day and active within the last 30 days.'
        ),
      }
    default:
      return {
        condition: t(
          'This rule is not recognized. Review the retained records.'
        ),
      }
  }
}

export function SecurityRuleSummary({
  alert,
  snapshot,
  compact = false,
}: {
  alert: SecurityAlert
  snapshot?: SecurityFinding
  compact?: boolean
}) {
  const { t } = useTranslation()
  const rule = describeSecurityRule(alert.rule, t, snapshot)
  const sourceRule = [
    'new_login_source',
    'new_source_sensitive_operation',
    'new_source_channel_rewrite',
  ].includes(alert.rule)
  const sourceTimes = [
    {
      label: t('New source first observed'),
      at: snapshot?.dimensions.source_first_seen,
    },
    {
      label: t('Known source first observed'),
      at: snapshot?.dimensions.baseline_first_seen,
    },
    {
      label: t('Known source last observed'),
      at: snapshot?.dimensions.baseline_last_seen,
    },
  ]
  let sourceEvidence = null
  if (sourceRule) {
    sourceEvidence = sourceTimes.every(
      (item) =>
        typeof item.at === 'number' && Number.isFinite(item.at) && item.at > 0
    ) ? (
      <div className='space-y-1'>
        {sourceTimes.map((item) => (
          <DetailRow
            key={item.label}
            label={item.label}
            value={securityTime(item.at)}
          />
        ))}
      </div>
    ) : (
      <p className='text-muted-foreground text-xs'>
        {t('Source timing was not recorded for this alert.')}
      </p>
    )
  }
  if (compact) {
    return (
      <DetailSection label={t('Trigger condition')}>
        <p className='text-sm'>{rule.condition}</p>
        {sourceEvidence}
      </DetailSection>
    )
  }
  return (
    <DetailSection label={t('Matched rule')}>
      <DetailRow
        label={t('Rule')}
        value={
          securityRules[alert.rule]
            ? t(securityRules[alert.rule])
            : alert.rule || t('Not recorded')
        }
      />
      <DetailRow label={t('Trigger condition')} value={rule.condition} />
      {sourceEvidence}
      <DetailRow
        label={rule.metric ?? t('Peak matching count')}
        value={alert.matched_count}
      />
      <DetailRow
        label={t('Alert trigger count')}
        value={<AlertTriggerCount alert={alert} />}
      />
      {rule.configuredThreshold && !snapshot && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'The threshold at the time of this alert was not retained, so its exact value cannot be recovered.'
          )}
        </p>
      )}
    </DetailSection>
  )
}
