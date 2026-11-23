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
export const securityRules: Record<string, string> = {
  risky_ip_login: 'High-risk IP sign-in',
  risky_ip_sensitive_operation: 'High-risk IP sensitive action',
  new_source_sensitive_operation: 'Sensitive action from a new source',
  anonymous_network: 'Anonymous network activity',
  ip_abuse: 'IP abuse signal',
  impossible_travel: 'Unusual travel between sign-ins',
  sensitive_operation: 'Sensitive operation',
  bulk_sensitive_operations: 'Bulk sensitive operations',
  bulk_key_access: 'Bulk channel key access',
  login_failures: 'Repeated sign-in failures',
  password_spray: 'Password spraying',
  login_after_failures: 'Sign-in after repeated failures',
  recovery_requests: 'Repeated password recovery',
  permission_failures: 'Repeated permission failures',
  session_reuse: 'Reused sign-in credential',
  multi_account_key_access: 'Multiple accounts viewing keys',
  multi_source_key_access: 'Multiple IPs viewing keys',
  new_source_channel_rewrite: 'Bulk channel address changes from a new source',
  new_login_source: 'Sign-in from a new source',
}

export const singleActionRuleConditions: Record<string, string> = {
  risky_ip_login: 'Triggers on one successful sign-in from a high-risk IP.',
  risky_ip_sensitive_operation:
    'Triggers on one successful sensitive action from a high-risk IP.',
  new_source_sensitive_operation:
    'An account with established activity performs a sensitive action from a new source.',
}
