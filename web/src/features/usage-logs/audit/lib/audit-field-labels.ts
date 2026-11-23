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

export function auditFieldLabel(
  key: string,
  t: TFunction,
  action?: string
): string {
  switch (key) {
    case 'attempted_account':
      if (
        action === 'auth.recovery_requested' ||
        action === 'auth.recovery_rejected' ||
        action === 'recovery_requests'
      ) {
        return t('Password recovery account')
      }
      if (
        action === 'auth.login_failed' ||
        action === 'auth.login_locked' ||
        action === 'login_failures' ||
        action === 'login_after_failures' ||
        action === 'password_spray'
      ) {
        return t('Attempted sign-in account')
      }
      return t('Submitted account')
    case 'key_accounts':
      return t('Accounts viewing keys')
    case 'login_ips':
      return t('Sign-in IPs')
    case 'key_ips':
      return t('IPs viewing keys')
    case 'outcome':
      return t('Response result')
    case 'client_ref':
      return t('Client')
    case 'login_window_seconds':
      return t('Sign-in window (seconds)')
    case 'key_account_threshold':
      return t('Minimum accounts viewing keys')
    case 'login_ip_threshold':
      return t('Minimum sign-in IPs')
    case 'key_ip_threshold':
      return t('Minimum IPs viewing keys')
    case 'channel_exclusions':
      return t('Excluded channels')
    case 'non_stream_response_timeout':
      return t('Non-streaming response timeout')
    case 'non_stream_total_timeout':
      return t('Non-streaming total timeout')
    case 'non_stream_timeout_billing':
      return t('Timeout billing')
    case 'archived':
      return t('Archived')
    case 'assignee_id':
      return t('Assignee ID')
    case 'conclusion':
      return t('Conclusion')
    case 'conclusion_note':
      return t('Investigation note')
    case 'ip_intelligence':
      return t('IP intelligence')
    case 'providers':
    case 'sources':
      return t('IP data sources')
    case 'asn':
    case 'asn_number':
      return t('ASN')
    case 'asn_organization':
      return t('ASN organization')
    case 'evidence':
      return t('IP match evidence')
    case 'source':
      return t('Source')
    case 'capability':
      return t('Capability')
    case 'matched_network':
      return t('Matched network')
    case 'reference':
      return t('Source reference')
    case 'data_updated_at':
      return t('Data updated at')
    case 'field_providers':
      return t('Source per capability')
    case 'observed_at':
      return t('Observed at')
    case 'country_code':
      return t('Country code')
    case 'country':
      return t('Country')
    case 'state':
      return t('Region')
    case 'city':
      return t('City')
    case 'latitude':
      return t('Latitude')
    case 'longitude':
      return t('Longitude')
    case 'accuracy':
      return t('Location accuracy')
    case 'anonymous':
      return t('Anonymous lookup')
    case 'vpn':
    case 'is_vpn':
      return t('VPN')
    case 'is_proxy':
      return t('Proxy')
    case 'tor':
    case 'is_tor':
      return t('Tor')
    case 'abuse':
    case 'is_abuser':
      return t('Abuse intelligence')
    case 'geo':
      return t('Geolocation')
    case 'is_anycast':
      return t('Anycast IP')
    case 'is_satellite':
      return t('Satellite network')
    case 'account_threshold':
      return action === 'multi_account_key_access'
        ? t('Minimum signed-in accounts')
        : t('Account count threshold')
    case 'events':
      return t('Event count')
    case 'reuse':
      return t('Session reuse')
    case 'new_source':
      return t('New login source')
    case 'baseline':
      return t('Established baseline')
    case 'source_first_seen':
      return t('New source first observed')
    case 'baseline_first_seen':
      return t('Known source first observed')
    case 'baseline_last_seen':
      return t('Known source last observed')
    case 'new_window_seconds':
      return t('New source window (seconds)')
    case 'baseline_window_seconds':
      return t('Baseline window (seconds)')
    case 'detected':
      return t('Detected signal')
    case 'min_distance_km':
      return t('Minimum travel distance (km)')
    case 'distance_km':
      return t('Distance (km)')
    case 'speed_kmh':
      return t('Speed (km/h)')
    case 'failures':
      return t('Sign-in failures')
    case 'accounts':
      return action === 'multi_account_key_access'
        ? t('Signed-in accounts')
        : t('Account identifiers')
    case 'distinct_channels':
      return t('Distinct channels')
    case 'status':
      return t('Status')
    case 'role':
      return t('Role')
    case 'changed_fields':
      return t('Changed Fields')
    case 'changed':
      return t('State changed')
    case 'count':
      return t('Count')
    case 'total':
      return t('Total')
    case 'requested_redemption_ids':
      return t('Requested redemption code IDs')
    case 'requested_ids':
      return t('Requested token IDs')
    case 'returned_ids':
      return t('Returned token IDs')
    case 'requested_ids_truncated':
      return t('Requested token IDs truncated')
    case 'expired_time':
      return t('Expiration Time')
    case 'remain_quota':
      return t('Remaining quota')
    case 'unlimited_quota':
      return t('Unlimited Quota')
    case 'model_limits_enabled':
      return t('Model limits enabled')
    case 'model_limits':
      return t('Model Limits')
    case 'allow_ips':
      return t('IP Whitelist (supports CIDR)')
    case 'allow_user_quota_query':
      return t('Allow querying user quota')
    case 'auto_groups':
      return t('Auto Group Chain')
    case 'cross_group_retry':
      return t('Cross-group retry')
    case 'sourceId':
      return t('Source ID')
    case 'name':
      return t('Name')
    case 'username':
      return t('Username')
    case 'target_user_id':
      return t('User ID')
    case 'plan_id':
      return t('Plan ID')
    case 'plan_title':
      return t('Plan Title')
    case 'reset_count':
      return t('Reset Count')
    case 'user_count':
      return t('User Count')
    case 'advance_reset_time':
      return t('Advance next reset time')
    case 'bindingType':
      return t('Binding Type')
    case 'from':
      return t('Previous value')
    case 'to':
      return t('New value')
    case 'action':
      return t('Operation')
    case 'method':
      return t('Authentication Method')
    case 'key':
      return t('Key')
    case 'tag':
      return t('Tag')
    case 'group':
      return t('Group')
    case 'models':
      return t('Models')
    case 'type':
      return t('Type')
    case 'base_url':
      return t('Base URL')
    case 'quota':
      return t('Quota')
    case 'admin_info':
      return t('Operator Admin')
    case 'audit_info':
      return t('Request')
    case 'token_id':
      return t('Token ID')
    case 'token_ref':
      return t('Token identifier')
    case 'scopes':
      return t('Permissions')
    case 'previous_scopes':
      return t('Previous permissions')
    case 'permission_increase':
      return t('Changed access token permissions')
    case 'expires_at':
      return t('Expiration')
    case 'required_scope':
      return t('Required permission')
    case 'failure_reason':
      return t('Failure reason')
    case 'legacy':
      return t('Legacy token')
    case 'legacy_revoked':
      return t('Legacy token revoked')
    case 'access_tokens':
      return t('Access tokens')
    case 'revoked_count':
    case 'revoked_access_tokens':
      return t('Revoked access tokens')
    case 'verification_method':
      return t('Verification method')
    case 'password_reset':
      return t('Reset password')
    case 'admin_permissions_updated':
      return t('Admin permissions updated')
    case 'provider_id':
      return t('Provider ID')
    case 'id':
      return t('ID')
    case 'submitted_fields':
      return t('Submitted fields')
    case 'target_role':
      return t('Target role')
    case 'followup_failed':
      return t('Follow-up failed')
    case 'evidence_incomplete':
      return t('Evidence incomplete')
    case 'ids':
      return t('Target IDs')
    case 'reason':
      return t('Reason')
    case 'priority':
      return t('Priority')
    case 'weight':
      return t('Weight')
    case 'model_mapping':
      return t('Model Mapping')
    case 'status_code_mapping':
      return t('Status Code Mapping')
    case 'param_override':
      return t('Parameter Override')
    case 'header_override':
      return t('Header Override')
    case 'setting':
      return t('Settings')
    case 'settings':
      return t('Settings')
    case 'auto_ban':
      return t('Auto Ban')
    case 'proxy':
      return t('Proxy URL')
    case 'daily_limit':
      return t('Daily Limit')
    case 'other':
      return t('Other')
    case 'other_display_field':
      return t('Other')
    case 'password':
      return t('Password')
    case 'email':
      return t('Email')
    case 'permissions':
      return t('Permissions')
    case 'group_ratios':
      return t('Group ratios')
    case 'acknowledge_unknown':
      return t('Unknown costs acknowledged')
    case 'alert_id':
      return t('Alert ID')
    case 'applied':
      return t('Applied settings')
    case 'base_url_source':
      return t('Base URL source')
    case 'channel_id':
      return t('Channel ID')
    case 'channel_ids':
      return t('Channel IDs')
    case 'code':
      return t('Error code')
    case 'commission_quota':
      return t('Commission quota')
    case 'commission_rate':
      return t('Commission rate')
    case 'commission_ref':
      return t('Commission record ID')
    case 'committed':
      return t('Committed')
    case 'compensation_ref':
      return t('Compensation record ID')
    case 'created':
      return t('Created')
    case 'created_models':
      return t('Created models')
    case 'created_vendors':
      return t('Created vendors')
    case 'customer_user_id':
      return t('Customer user ID')
    case 'deleted_vendor_ids':
      return t('Deleted vendor IDs')
    case 'detection_id':
      return t('Detection ID')
    case 'domains':
      return t('Domains')
    case 'effective_rp_id':
      return t('Effective relying party ID')
    case 'employee_id':
      return t('Employee ID')
    case 'employee_user_id':
      return t('Employee user ID')
    case 'entry':
      return t('Entry point')
    case 'excluded_channel_ids_changed':
      return t('Excluded channels changed')
    case 'filters':
      return t('Filters')
    case 'force_below_floor':
      return t('Allow prices below cost')
    case 'is_historical':
      return t('Historical record')
    case 'known':
      return t('Known credentials')
    case 'legacy_id':
      return t('Legacy ID')
    case 'model_ids':
      return t('Model IDs')
    case 'module':
      return t('Module')
    case 'notification_failed':
      return t('Notification failed')
    case 'option_keys':
      return t('Setting keys')
    case 'previous_rp_id':
      return t('Previous relying party ID')
    case 'profit_quota':
      return t('Profit quota')
    case 'protocol':
      return t('Protocol')
    case 'provider':
      return t('Provider')
    case 'provider_user_id':
      return t('Provider user ID')
    case 'remove_from_channels':
      return t('Remove from channels')
    case 'remove_pricing':
      return t('Remove pricing')
    case 'removed_group_ratios':
      return t('Removed group ratios')
    case 'removed_rp_ids':
      return t('Removed relying party IDs')
    case 'reset_started_at':
      return t('Reset start time')
    case 'retention_days':
      return t('Retention days')
    case 'route':
      return t('Route')
    case 'scope':
      return t('Scope')
    case 'source_vendor_ids':
      return t('Source vendor IDs')
    case 'subject_ref':
      return t('Account identifier')
    case 'success':
      return t('Success')
    case 'target_username':
      return t('Target username')
    case 'target_vendor_id':
      return t('Target vendor ID')
    case 'task_id':
      return t('Task ID')
    case 'template_id':
      return t('Template ID')
    case 'trade_no':
      return t('Order number')
    case 'unknown':
      return t('Unknown credentials')
    case 'updated_channels':
      return t('Updated channels')
    case 'updated_model_ids':
      return t('Updated model IDs')
    case 'updated_models':
      return t('Updated models')
    case 'vendor_id':
      return t('Vendor ID')
    case 'verified_email_matched':
      return t('Verified email matched')
    case 'version':
      return t('Version')
    case 'job_id':
      return t('Job ID')
    case 'start':
      return t('Start Time')
    case 'end':
      return t('End Time')
    case 'columns':
      return t('Columns')
    case 'format':
      return t('Format')
    case 'mode':
      return t('Adjustment mode')
    case 'requested_quota':
      return t('Requested quota')
    case 'params':
      return t('Parameters')
    case 'binding_type':
      return t('Binding Type')
    case 'user_id':
      return t('User ID')
    case 'confirmed':
      return t('Confirmed')
    case 'admin_id':
      return t('Operator ID')
    case 'admin_username':
      return t('Operator username')
    case 'admin_role':
      return t('Role')
    case 'auth_method':
      return t('Authentication Method')
    case 'target_id':
      return t('Target ID')
    case 'actor_id':
      return t('Operator ID')
    case 'actor_role':
      return t('Role')
    case 'resource_type':
      return t('Resource')
    case 'affected_count':
      return t('Count')
    case 'request_id':
      return t('Request ID')
    case 'event_id':
      return t('Event ID')
    case 'ip':
      return t('IP')
    case 'at':
      return t('Time')
    case 'created_at':
      return t('Created At')
    case 'provider_name':
      return t('Provider')
    case 'daily_quota_limit':
      return t('Daily Limit')
    case 'daily_quota_reset_time':
      return t('Daily reset time')
    case 'auto_reenable':
      return t('Auto re-enable')
    case 'display_name':
      return t('Display Name')
    case 'remark':
      return t('Remark')
    case 'deleted_at':
      return t('Deleted At')
    case 'before':
      return t('Previous value')
    case 'after':
      return t('New value')
    case 'employee_user_ids':
      return t('Employee user IDs')
    case 'cutoff_time':
      return t('Cutoff time')
    case 'batch_size':
      return t('Batch Size')
    case 'log_type':
      return t('Log Type')
    case 'log_category':
      return t('Log category')
    case 'group_ratio_overrides':
      return t('Group ratio overrides')
    case 'force':
      return t('Force')
    case 'summary_dims':
      return t('Summary dimensions')
    case 'part_no':
      return t('Part number')
    case 'row_count':
      return t('Row count')
    case 'is_employee':
      return t('Employee')
    case 'token_name':
      return t('Token Name')
    case 'channel_name':
      return t('Channel Name')
    case 'model_name':
      return t('Model Name')
    case 'start_timestamp':
      return t('Start Time')
    case 'end_timestamp':
      return t('End Time')
    case 'use_time':
      return t('Duration')
    case 'stream':
      return t('Stream')
    case 'group_ratio':
      return t('Group Ratio')
    case 'quota_min':
      return t('Minimum quota')
    case 'quota_max':
      return t('Maximum quota')
    case 'template_key':
      return t('Template key')
    case 'template_version':
      return t('Template version')
    case 'customer_count':
      return t('Customer count')
    default:
      return key
  }
}
