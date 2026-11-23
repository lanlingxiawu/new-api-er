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

import type { AuditActivity } from '../api'
import { auditActionLabel } from './audit-action-labels'
import { auditFieldLabel } from './audit-field-labels'

export function activityLabel(
  entry: Pick<AuditActivity, 'action'>,
  t: TFunction
): string {
  switch (entry.action) {
    case 'token.create':
      return t('Create API token')
    case 'token.update':
    case 'token.status_update':
      return t('Update API token')
    case 'token.delete':
      return t('Delete API token')
    case 'token.delete_batch':
      return t('Batch delete API tokens')
    case 'token.key_view':
      return t('View API token key')
    case 'token.key_view_batch':
      return t('View API token keys in batch')
    case 'access_token.revoke':
      return t('Revoked an access token')
    case 'access_token.rename':
      return t('Renamed an access token')
    case 'user.2fa_setup':
      return t('Started two-factor authentication setup')
    case 'user.2fa_enable':
      return t('Enabled two-factor authentication')
    case 'user.binding_start':
      return t('Account binding request')
    case 'user.email_binding_resend':
      return t('Email confirmation code resend')
    case 'user.security_verify':
      return t('Completed security verification')
    case 'user.account_delete':
      return t('Account deletion')
    case 'access_token.request':
      return t('Request with access token')
    case 'access_token.denied':
      return t('Access denied.')
    case 'logout':
      return t('Sign out')
    case 'user.session_revoke':
      return t('Session signed out')
    case 'user.sessions_revoke':
      return t('Sign out other sessions')
    case 'user.topup_complete':
      return t('Completed top-up order for the user')
    case 'option.update':
      return t('Update system settings')
    case 'option.passkey_domains':
      return t('Update Passkey domains')
    case 'option.passkey_domains_confirmed':
      return t('Confirm Passkey domain removal')
    case 'option.passkey_domains_blocked':
      return t('Passkey domain change blocked')
    case 'option.passkey_domains_failed':
      return t('Passkey domain update failed')
    case 'option.payment_compliance':
      return t('Confirmed payment compliance')
    case 'option.reset_ratio':
      return t('Reset model ratios')
    case 'option.clear_affinity_cache':
      return t('Cleared channel affinity cache')
    case 'custom_oauth.create':
      return t('Created a custom OAuth provider')
    case 'custom_oauth.update':
      return t('Updated a custom OAuth provider')
    case 'custom_oauth.delete':
      return t('Deleted a custom OAuth provider')
    case 'performance.clear_disk_cache':
      return t('Cleared disk cache')
    case 'performance.gc':
      return t('Triggered garbage collection')
    case 'performance.clear_logs':
      return t('Cleared log files')
    case 'redemption.create':
      return t('Create Redemption Code')
    case 'redemption.update':
      return t('Updated a redemption code')
    case 'redemption.delete':
      return t('Deleted a redemption code')
    case 'redemption.delete_batch':
      return t('Batch delete redemption codes')
    case 'redemption.delete_invalid':
      return t('Deleted invalid redemption codes')
    case 'prefill_group.create':
      return t('Created a prefill group')
    case 'prefill_group.update':
      return t('Updated a prefill group')
    case 'prefill_group.delete':
      return t('Deleted a prefill group')
    case 'vendor.create':
      return t('Created a vendor')
    case 'vendor.update':
      return t('Updated a vendor')
    case 'model.create':
      return t('Created a model')
    case 'model.update':
      return t('Updated a model')
    case 'model.delete':
      return t('Deleted a model')
    case 'model.sync_upstream':
      return t('Synced upstream models')
    case 'deployment.create':
      return t('Created a deployment')
    case 'deployment.update':
      return t('Updated a deployment')
    case 'deployment.delete':
      return t('Deleted a deployment')
    case 'subscription.plan_create':
      return t('Created a subscription plan')
    case 'subscription.plan_update':
      return t('Updated a subscription plan')
    case 'subscription.bind':
      return t('Bound a subscription')
    case 'subscription.plan_reset':
    case 'subscription.user_plan_reset':
    case 'subscription.user_quota_reset':
      return t('Reset subscription quota')
    case 'employee.performance_adjust':
      return t('Adjust Performance')
    case 'employee.performance_revert':
      return t('Revert performance adjustment')
    case 'customer.bind_blocked':
      return t('Customer assignment blocked')
    case 'log.clear':
      return t('Cleared historical logs')
    case 'log.cleanup_start':
      return t('Log cleanup task started.')
    case 'log_export.job_create':
      return t('Start export')
    case 'log_export.job_delete':
    case 'log_export.job_cancel':
      return t('Canceled or deleted a log export job')
    case 'log_export.template_create':
      return t('Created a log export template')
    case 'log_export.template_update':
      return t('Updated a log export template')
    case 'log_export.template_delete':
      return t('Deleted a log export template')
    case 'employee_export.template_save':
      return t('Saved an employee export template')
    case 'employee_export.template_delete':
      return t('Deleted an employee export template')
    default:
      return auditActionLabel(entry.action, t) ?? t('Operation audit')
  }
}

export function activitySource(source: string, t: TFunction): string {
  if (source === 'session') return t('Web session')
  if (source === 'access_token') return t('Access Token')
  return t('Unknown source')
}

const publicFields = new Set(
  'name status expired_time remain_quota unlimited_quota model_limits_enabled model_limits allow_ips allow_user_quota_query group cross_group_retry auto_groups scopes expires_at revoked username display_name email language sidebar_modules setting password bound deleted'.split(
    ' '
  )
)

export function activityChanges(entry: AuditActivity, t: TFunction): string {
  const summary = entry.change_summary
  if (!summary) return ''
  if (summary.outcome === 'not_committed') return t('No changes were saved')
  if (summary.outcome === 'unchanged') return t('No changes')
  if (summary.outcome !== 'committed' && summary.outcome !== 'partial') {
    return t('Field change details were not recorded')
  }
  const fields = summary.fields
    .filter((field) => publicFields.has(field))
    .slice(0, 3)
    .map((field) => {
      switch (field) {
        case 'revoked':
          return t('Status')
        case 'language':
          return t('Language')
        case 'sidebar_modules':
          return t('Sidebar')
        case 'bound':
          return t('Account binding')
        case 'deleted':
          return t('Deleted')
        default:
          return auditFieldLabel(field, t)
      }
    })
    .join(', ')
  let changes = t('Field change details were not recorded')
  if (fields) {
    changes =
      summary.total > 3
        ? t('Changed {{count}} fields: {{fields}}…', {
            count: summary.total,
            fields,
          })
        : t('Changed fields: {{fields}}', { fields })
  }
  return summary.outcome === 'partial'
    ? `${changes} · ${t('Some changes were saved before the operation failed.')}`
    : changes
}
