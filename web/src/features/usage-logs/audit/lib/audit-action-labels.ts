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
type Translate = (key: string) => string

// Neutral labels work even when retained security evidence omits names or counts.
export function auditActionLabel(
  action: string,
  t: Translate
): string | undefined {
  switch (action) {
    case 'relay.ip_observed':
      return t('AI request IP observed')
    case 'audit_security.disable_account':
      return t('Ban account')
    case 'user.profile_update':
      return t('Update profile')
    case 'user.settings_update':
      return t('Update user settings')
    case 'channel.priority_reorder':
      return t('Reorder channel priorities')
    case 'user.api_keys.delete_all':
      return t('Clear user API keys')
    case 'user.access_tokens.delete_all':
      return t('Clear user access tokens')
    case 'channel.create':
      return t('Create Channel')
    case 'channel.update':
      return t('Update Channel')
    case 'channel.token_update':
      return t('Batch update channels')
    case 'channel.delete':
      return t('Delete Channel')
    case 'channel.copy':
      return t('Copy Channel')
    case 'channel.key_view':
      return t('View channel secrets')
    case 'channel.multi_key_manage':
      return t('Multi-Key Management')
    case 'channel.batch_daily_limit':
      return t('Update channel daily limits')
    case 'user.create':
      return t('Create User')
    case 'user.update':
      return t('Update User')
    case 'login':
      return t('Login')
    case 'auth.login_locked':
      return t('Sign-in restricted')
    case 'auth.login_failed':
      return t('Login failed')
    case 'auth.access_denied':
      return t('Access denied.')
    case 'auth.recovery_requested':
      return t('Password recovery request')
    case 'auth.session_refresh_reuse':
      return t('Session reuse')
    case 'auth.password_reset_committed':
      return t('Password reset completed')
    case 'auth.password_reset_cleanup_failed':
      return t('Password reset cleanup failed')
    case 'channel.status_update':
      return t('Update channel status')
    case 'channel.status_update_batch':
      return t('Batch update channel status')
    case 'channel.delete_batch':
      return t('Batch delete channels')
    case 'channel.delete_disabled':
      return t('Delete disabled channels')
    case 'channel.tag_disable':
      return t('Disable channels by tag')
    case 'channel.tag_enable':
      return t('Enable channels by tag')
    case 'channel.tag_edit':
      return t('Batch Edit by Tag')
    case 'channel.tag_batch_set':
      return t('Batch set channel tags')
    case 'channel.upstream_apply':
      return t('Apply upstream model changes')
    case 'channel.upstream_apply_all':
      return t('Batch apply upstream model changes')
    case 'channel.upstream_detect_all':
      return t('Batch upstream model update')
    case 'channel.veridrop_cleanup':
      return t('Clean up detection records')
    case 'channel.veridrop_detect_manual':
      return t('Channel detection')
    case 'channel.veridrop_detect':
      return t('Start channel detection')
    case 'channel.veridrop_detect_batch':
      return t('Start detection for selected channels')
    case 'channel.veridrop_detect_enabled':
      return t('Start detection for enabled channels')
    case 'user.delete':
      return t('Delete user')
    case 'user.manage':
      return t('Manage user')
    case 'user.quota_add':
      return t('Increase user quota')
    case 'user.quota_subtract':
      return t('Decrease user quota')
    case 'user.quota_override':
      return t('Override user quota')
    case 'user.binding_clear':
      return t('Clear user binding')
    case 'user.2fa_disable':
      return t('Disable user two-factor authentication')
    case 'user.reset_passkey':
      return t('Reset Passkey')
    case 'user.oauth_unbind':
      return t('Remove user OAuth binding')
    case 'user.password_change':
      return t('Account password change')
    case 'user.2fa_disable_self':
      return t('Disabled two-factor authentication')
    case 'user.passkey_delete':
      return t('Deleted a passkey')
    case 'user.binding_unbind':
      return t('Account unlinking')
    case 'user.2fa_backup_codes':
      return t('Regenerated two-factor backup codes')
    case 'user.passkey_register':
      return t('Registered a passkey')
    case 'user.binding_bind':
      return t('Account binding')
    case 'access_token.generate':
      return t('Created an access token')
    case 'access_token.update':
      return t('Changed access token permissions')
    case 'auth.recovery_rejected':
      return t('Password recovery rejected')
    case 'audit_security.status':
      return t('Update security alert status')
    case 'audit_security.archive':
      return t('Change alert archive state')
    case 'audit_security.export':
      return t('Export security alerts')
    case 'audit_security.delete':
      return t('Delete security alerts')
    case 'audit_security.cleanup':
      return t('Clean up closed alerts')
    case 'model.delete_batch':
      return t('Batch delete models')
    case 'model.metadata.sync':
      return t('Sync model metadata')
    case 'model.pricing.update':
      return t('Update model pricing')
    case 'option.group.update':
      return t('Update grouped settings')
    case 'price_monitor.cost_ratio.update':
      return t('Update channel cost ratio')
    case 'price_monitor.price.apply':
      return t('Apply model pricing')
    case 'price_monitor.settings.update':
      return t('Update price monitoring settings')
    case 'vendor.metadata.delete':
      return t('Delete vendor metadata')
    case 'vendor.metadata.save':
      return t('Save vendor metadata')
    case 'vendor.merge':
      return t('Merge vendors')
    case 'vendor.assign':
      return t('Assign vendor')
    case 'vendor.delete':
      return t('Deleted a vendor')
    default:
      return undefined
  }
}
