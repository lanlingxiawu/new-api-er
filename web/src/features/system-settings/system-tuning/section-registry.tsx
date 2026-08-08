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
import { BusinessStatsCircuitBreakerSection } from '../maintenance/business-stats-circuit-breaker-section'
import { ExportSettingsSection } from '../maintenance/export-settings-section'
import { FallbackBackfillSection } from '../maintenance/fallback-backfill-section'
import { LedgerDetailSection } from '../maintenance/ledger-detail-section'
import { LedgerPipelineSection } from '../maintenance/ledger-pipeline-section'
import { LogExportSection } from '../maintenance/log-export-section'
import { LogQuerySection } from '../maintenance/log-query-section'
import {
  RelayLogPipelineSection,
  type RelayLogPipelineFlatDefaults,
} from '../maintenance/relay-log-pipeline-section'
import type { SystemTuningSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { systemTuningFallbackSettings } from './defaults'
import {
  DBPoolHotConfigSection,
  RateLimitHotConfigSection,
  RelayTimeoutHotConfigSection,
  UserSessionHotConfigSection,
} from './hot-config-sections'

const SYSTEM_TUNING_SECTIONS = [
  {
    id: 'gateway-rate-limit',
    titleKey: 'Gateway Rate Limiting',
    build: (settings: SystemTuningSettings) => (
      <RateLimitHotConfigSection settings={settings} />
    ),
  },
  {
    id: 'database-pool',
    titleKey: 'Database Connection Pool',
    build: (settings: SystemTuningSettings) => (
      <DBPoolHotConfigSection settings={settings} />
    ),
  },
  {
    id: 'login-session-policy',
    titleKey: 'Login Session Policy',
    build: (settings: SystemTuningSettings) => (
      <UserSessionHotConfigSection settings={settings} />
    ),
  },
  {
    id: 'settlement-guard',
    titleKey: 'Settlement Guard',
    build: (settings: SystemTuningSettings) => (
      <BusinessStatsCircuitBreakerSection
        defaultValues={{
          'business_stats_circuit_breaker_setting.enabled':
            settings['business_stats_circuit_breaker_setting.enabled'] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.enabled'
            ],
          'business_stats_circuit_breaker_setting.manual_disabled':
            settings[
              'business_stats_circuit_breaker_setting.manual_disabled'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.manual_disabled'
            ],
          'business_stats_circuit_breaker_setting.failure_threshold':
            settings[
              'business_stats_circuit_breaker_setting.failure_threshold'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.failure_threshold'
            ],
          'business_stats_circuit_breaker_setting.initial_cooldown_seconds':
            settings[
              'business_stats_circuit_breaker_setting.initial_cooldown_seconds'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.initial_cooldown_seconds'
            ],
          'business_stats_circuit_breaker_setting.max_cooldown_seconds':
            settings[
              'business_stats_circuit_breaker_setting.max_cooldown_seconds'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.max_cooldown_seconds'
            ],
          'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms':
            settings[
              'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms'
            ],
        }}
      />
    ),
  },
  {
    id: 'ledger-pipeline',
    titleKey: 'Ledger Pipeline',
    build: (settings: SystemTuningSettings) => (
      <LedgerPipelineSection
        defaultValues={{
          'ledger_pipeline_setting.flush_interval_sec':
            settings['ledger_pipeline_setting.flush_interval_sec'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.flush_interval_sec'
            ],
          'ledger_pipeline_setting.outer_batch_size':
            settings['ledger_pipeline_setting.outer_batch_size'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.outer_batch_size'
            ],
          'ledger_pipeline_setting.cost_outer_batch_size':
            settings['ledger_pipeline_setting.cost_outer_batch_size'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.cost_outer_batch_size'
            ],
          'ledger_pipeline_setting.inner_batch_size':
            settings['ledger_pipeline_setting.inner_batch_size'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.inner_batch_size'
            ],
          'ledger_pipeline_setting.settlement_flush_max_per_cycle':
            settings[
              'ledger_pipeline_setting.settlement_flush_max_per_cycle'
            ] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.settlement_flush_max_per_cycle'
            ],
          'ledger_pipeline_setting.cost_flush_max_per_cycle':
            settings['ledger_pipeline_setting.cost_flush_max_per_cycle'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.cost_flush_max_per_cycle'
            ],
          'ledger_pipeline_setting.full_drain':
            settings['ledger_pipeline_setting.full_drain'] ??
            systemTuningFallbackSettings['ledger_pipeline_setting.full_drain'],
          'ledger_pipeline_setting.buf_max_entries':
            settings['ledger_pipeline_setting.buf_max_entries'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.buf_max_entries'
            ],
          'ledger_pipeline_setting.dedup_mem_max_entries':
            settings['ledger_pipeline_setting.dedup_mem_max_entries'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.dedup_mem_max_entries'
            ],
          'ledger_pipeline_setting.dedup_use_redis':
            settings['ledger_pipeline_setting.dedup_use_redis'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.dedup_use_redis'
            ],
          'ledger_pipeline_setting.dedup_redis_ttl_sec':
            settings['ledger_pipeline_setting.dedup_redis_ttl_sec'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.dedup_redis_ttl_sec'
            ],
          'ledger_pipeline_setting.flush_db_timeout_sec':
            settings['ledger_pipeline_setting.flush_db_timeout_sec'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.flush_db_timeout_sec'
            ],
          'ledger_pipeline_setting.fallback_queue_capacity':
            settings['ledger_pipeline_setting.fallback_queue_capacity'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.fallback_queue_capacity'
            ],
          'ledger_pipeline_setting.shutdown_timeout_sec':
            settings['ledger_pipeline_setting.shutdown_timeout_sec'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.shutdown_timeout_sec'
            ],
          'ledger_pipeline_setting.cache_ttl_secs':
            settings['ledger_pipeline_setting.cache_ttl_secs'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.cache_ttl_secs'
            ],
          'ledger_pipeline_setting.cache_ttl_jitter_percent':
            settings['ledger_pipeline_setting.cache_ttl_jitter_percent'] ??
            systemTuningFallbackSettings[
              'ledger_pipeline_setting.cache_ttl_jitter_percent'
            ],
          'ledger_retry_setting.retry_flush_interval_sec':
            settings['ledger_retry_setting.retry_flush_interval_sec'] ??
            systemTuningFallbackSettings[
              'ledger_retry_setting.retry_flush_interval_sec'
            ],
          'ledger_retry_setting.stat_upsert_max_retries':
            settings['ledger_retry_setting.stat_upsert_max_retries'] ??
            systemTuningFallbackSettings[
              'ledger_retry_setting.stat_upsert_max_retries'
            ],
          'ledger_retry_setting.retry_queue_max_entries':
            settings['ledger_retry_setting.retry_queue_max_entries'] ??
            systemTuningFallbackSettings[
              'ledger_retry_setting.retry_queue_max_entries'
            ],
          'ledger_retry_setting.allow_concurrent_flush':
            settings['ledger_retry_setting.allow_concurrent_flush'] ??
            systemTuningFallbackSettings[
              'ledger_retry_setting.allow_concurrent_flush'
            ],
        }}
      />
    ),
  },
  {
    id: 'relay-log-pipeline',
    titleKey: 'Relay Log Pipeline',
    build: (settings: SystemTuningSettings) => {
      const keys = [
        'relay_log_pipeline_setting.enabled',
        'relay_log_pipeline_setting.consume_buf_max_entries',
        'relay_log_pipeline_setting.error_buf_max_entries',
        'relay_log_pipeline_setting.continuation_buf_max_entries',
        'relay_log_pipeline_setting.outer_batch_size',
        'relay_log_pipeline_setting.inner_batch_size',
        'relay_log_pipeline_setting.flush_max_per_cycle',
        'relay_log_pipeline_setting.full_drain',
        'relay_log_pipeline_setting.flush_interval_ms',
        'relay_log_pipeline_setting.write_timeout_sec',
        'relay_log_pipeline_setting.fallback_queue_capacity',
        'relay_log_pipeline_setting.fallback_max_file_size_mb',
        'relay_log_pipeline_setting.fallback_max_files',
        'relay_log_pipeline_setting.shutdown_timeout_sec',
        'relay_log_retry_setting.retry_flush_interval_ms',
        'relay_log_retry_setting.max_retries',
        'relay_log_retry_setting.retry_buf_max_entries',
        'relay_log_retry_setting.allow_concurrent_flush',
        'relay_log_retry_setting.circuit_failure_threshold',
        'relay_log_retry_setting.circuit_open_sec',
      ] as const
      const defaultValues = {} as RelayLogPipelineFlatDefaults
      for (const key of keys) {
        ;(defaultValues as Record<string, boolean | number>)[key] =
          settings[key] ?? systemTuningFallbackSettings[key]
      }
      return <RelayLogPipelineSection defaultValues={defaultValues} />
    },
  },
  {
    id: 'export-settings',
    titleKey: 'Billing Export Settings',
    build: (settings: SystemTuningSettings) => (
      <ExportSettingsSection
        defaultValues={{
          'payment_setting.user_export_max_rows':
            settings['payment_setting.user_export_max_rows'] ??
            systemTuningFallbackSettings[
              'payment_setting.user_export_max_rows'
            ],
          'export_setting.user_export_enabled':
            settings['export_setting.user_export_enabled'] ??
            systemTuningFallbackSettings['export_setting.user_export_enabled'],
          'export_setting.rate_limit_cooldown_sec':
            settings['export_setting.rate_limit_cooldown_sec'] ??
            systemTuningFallbackSettings[
              'export_setting.rate_limit_cooldown_sec'
            ],
          'export_setting.hard_ceiling_rows':
            settings['export_setting.hard_ceiling_rows'] ??
            systemTuningFallbackSettings['export_setting.hard_ceiling_rows'],
        }}
      />
    ),
  },
  {
    id: 'log-query',
    titleKey: 'Usage Log Query Acceleration',
    build: (settings: SystemTuningSettings) => (
      <LogQuerySection
        defaultValues={{
          'log_query_setting.stat_cache_enabled':
            settings['log_query_setting.stat_cache_enabled'] ??
            systemTuningFallbackSettings[
              'log_query_setting.stat_cache_enabled'
            ],
          'log_query_setting.stat_cache_ttl_hours':
            settings['log_query_setting.stat_cache_ttl_hours'] ??
            systemTuningFallbackSettings[
              'log_query_setting.stat_cache_ttl_hours'
            ],
          'log_query_setting.current_hour_ttl_sec':
            settings['log_query_setting.current_hour_ttl_sec'] ??
            systemTuningFallbackSettings[
              'log_query_setting.current_hour_ttl_sec'
            ],
          'log_query_setting.warm_enabled':
            settings['log_query_setting.warm_enabled'] ??
            systemTuningFallbackSettings['log_query_setting.warm_enabled'],
          'log_query_setting.warm_hours':
            settings['log_query_setting.warm_hours'] ??
            systemTuningFallbackSettings['log_query_setting.warm_hours'],
          'log_query_setting.query_timeout_ms':
            settings['log_query_setting.query_timeout_ms'] ??
            systemTuningFallbackSettings['log_query_setting.query_timeout_ms'],
        }}
      />
    ),
  },
  {
    id: 'log-export',
    titleKey: 'Usage Log Export Settings',
    build: (settings: SystemTuningSettings) => (
      <LogExportSection
        defaultValues={{
          'log_export_setting.enabled':
            settings['log_export_setting.enabled'] ??
            systemTuningFallbackSettings['log_export_setting.enabled'],
          'log_export_setting.offpeak_only':
            settings['log_export_setting.offpeak_only'] ??
            systemTuningFallbackSettings['log_export_setting.offpeak_only'],
          'log_export_setting.user_cooldown_sec':
            settings['log_export_setting.user_cooldown_sec'] ??
            systemTuningFallbackSettings[
              'log_export_setting.user_cooldown_sec'
            ],
          'log_export_setting.max_concurrent_jobs':
            settings['log_export_setting.max_concurrent_jobs'] ??
            systemTuningFallbackSettings[
              'log_export_setting.max_concurrent_jobs'
            ],
          'log_export_setting.max_active_jobs_per_user':
            settings['log_export_setting.max_active_jobs_per_user'] ??
            systemTuningFallbackSettings[
              'log_export_setting.max_active_jobs_per_user'
            ],
          'log_export_setting.admin_max_range_sec':
            settings['log_export_setting.admin_max_range_sec'] ??
            systemTuningFallbackSettings[
              'log_export_setting.admin_max_range_sec'
            ],
          'log_export_setting.timeout_sec':
            settings['log_export_setting.timeout_sec'] ??
            systemTuningFallbackSettings['log_export_setting.timeout_sec'],
          'log_export_setting.job_ttl_hours':
            settings['log_export_setting.job_ttl_hours'] ??
            systemTuningFallbackSettings['log_export_setting.job_ttl_hours'],
          'log_export_setting.max_templates_per_user':
            settings['log_export_setting.max_templates_per_user'] ??
            systemTuningFallbackSettings[
              'log_export_setting.max_templates_per_user'
            ],
          'log_export_setting.batch_size':
            settings['log_export_setting.batch_size'] ??
            systemTuningFallbackSettings['log_export_setting.batch_size'],
          'log_export_setting.batch_sleep_ms':
            settings['log_export_setting.batch_sleep_ms'] ??
            systemTuningFallbackSettings['log_export_setting.batch_sleep_ms'],
          'log_export_setting.batch_query_timeout_sec':
            settings['log_export_setting.batch_query_timeout_sec'] ??
            systemTuningFallbackSettings[
              'log_export_setting.batch_query_timeout_sec'
            ],
          'log_export_setting.window_sec':
            settings['log_export_setting.window_sec'] ??
            systemTuningFallbackSettings['log_export_setting.window_sec'],
          'log_export_setting.max_rows_per_sec':
            settings['log_export_setting.max_rows_per_sec'] ??
            systemTuningFallbackSettings['log_export_setting.max_rows_per_sec'],
          'log_export_setting.cpu_soft_limit':
            settings['log_export_setting.cpu_soft_limit'] ??
            systemTuningFallbackSettings['log_export_setting.cpu_soft_limit'],
          'log_export_setting.cpu_hard_limit':
            settings['log_export_setting.cpu_hard_limit'] ??
            systemTuningFallbackSettings['log_export_setting.cpu_hard_limit'],
          'log_export_setting.cpu_check_interval_ms':
            settings['log_export_setting.cpu_check_interval_ms'] ??
            systemTuningFallbackSettings[
              'log_export_setting.cpu_check_interval_ms'
            ],
          'log_export_setting.gzip_level':
            settings['log_export_setting.gzip_level'] ??
            systemTuningFallbackSettings['log_export_setting.gzip_level'],
          'log_export_setting.rows_per_file':
            settings['log_export_setting.rows_per_file'] ??
            systemTuningFallbackSettings['log_export_setting.rows_per_file'],
          'log_export_setting.max_parts':
            settings['log_export_setting.max_parts'] ??
            systemTuningFallbackSettings['log_export_setting.max_parts'],
          'log_export_setting.xlsx_max_rows':
            settings['log_export_setting.xlsx_max_rows'] ??
            systemTuningFallbackSettings['log_export_setting.xlsx_max_rows'],
          'log_export_setting.min_free_disk_mb':
            settings['log_export_setting.min_free_disk_mb'] ??
            systemTuningFallbackSettings['log_export_setting.min_free_disk_mb'],
          'log_export_setting.download_token_ttl_sec':
            settings['log_export_setting.download_token_ttl_sec'] ??
            systemTuningFallbackSettings[
              'log_export_setting.download_token_ttl_sec'
            ],
          'log_export_setting.download_session_ttl_sec':
            settings['log_export_setting.download_session_ttl_sec'] ??
            systemTuningFallbackSettings[
              'log_export_setting.download_session_ttl_sec'
            ],
          'log_export_setting.max_concurrent_downloads_per_user':
            settings['log_export_setting.max_concurrent_downloads_per_user'] ??
            systemTuningFallbackSettings[
              'log_export_setting.max_concurrent_downloads_per_user'
            ],
          'log_export_setting.offpeak_window':
            settings['log_export_setting.offpeak_window'] ??
            systemTuningFallbackSettings['log_export_setting.offpeak_window'],
        }}
      />
    ),
  },
  {
    id: 'ledger-detail',
    titleKey: 'Ledger Detail Settings',
    build: (settings: SystemTuningSettings) => (
      <LedgerDetailSection
        defaultValues={{
          'ledger_detail_setting.export_user_cooldown_sec':
            settings['ledger_detail_setting.export_user_cooldown_sec'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_user_cooldown_sec'
            ],
          'ledger_detail_setting.export_batch_size':
            settings['ledger_detail_setting.export_batch_size'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_batch_size'
            ],
          'ledger_detail_setting.export_batch_sleep_ms':
            settings['ledger_detail_setting.export_batch_sleep_ms'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_batch_sleep_ms'
            ],
          'ledger_detail_setting.export_rows_per_file':
            settings['ledger_detail_setting.export_rows_per_file'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_rows_per_file'
            ],
          'ledger_detail_setting.export_max_range_sec':
            settings['ledger_detail_setting.export_max_range_sec'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_max_range_sec'
            ],
          'ledger_detail_setting.export_timeout_sec':
            settings['ledger_detail_setting.export_timeout_sec'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.export_timeout_sec'
            ],
          'ledger_detail_setting.list_max_range_sec':
            settings['ledger_detail_setting.list_max_range_sec'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_max_range_sec'
            ],
          'ledger_detail_setting.list_default_range_sec':
            settings['ledger_detail_setting.list_default_range_sec'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_default_range_sec'
            ],
          'ledger_detail_setting.list_default_limit':
            settings['ledger_detail_setting.list_default_limit'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_default_limit'
            ],
          'ledger_detail_setting.list_max_limit':
            settings['ledger_detail_setting.list_max_limit'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_max_limit'
            ],
          'ledger_detail_setting.list_scan_batch_size':
            settings['ledger_detail_setting.list_scan_batch_size'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_scan_batch_size'
            ],
          'ledger_detail_setting.list_scan_rows_per_req':
            settings['ledger_detail_setting.list_scan_rows_per_req'] ??
            systemTuningFallbackSettings[
              'ledger_detail_setting.list_scan_rows_per_req'
            ],
        }}
      />
    ),
  },
  {
    id: 'fallback-backfill',
    titleKey: 'Fallback Backfill',
    build: (settings: SystemTuningSettings) => (
      <FallbackBackfillSection
        defaultValues={{
          'business_stats_fallback_backfill_setting.enabled':
            settings['business_stats_fallback_backfill_setting.enabled'] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.enabled'
            ],
          'business_stats_fallback_backfill_setting.use_separate_fallback_dir':
            settings[
              'business_stats_fallback_backfill_setting.use_separate_fallback_dir'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.use_separate_fallback_dir'
            ],
          'business_stats_fallback_backfill_setting.status_cache_seconds':
            settings[
              'business_stats_fallback_backfill_setting.status_cache_seconds'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.status_cache_seconds'
            ],
          'business_stats_fallback_backfill_setting.max_read_line_bytes':
            settings[
              'business_stats_fallback_backfill_setting.max_read_line_bytes'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.max_read_line_bytes'
            ],
          'business_stats_fallback_backfill_setting.write_batch_size':
            settings[
              'business_stats_fallback_backfill_setting.write_batch_size'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.write_batch_size'
            ],
          'business_stats_fallback_backfill_setting.flush_interval_sec':
            settings[
              'business_stats_fallback_backfill_setting.flush_interval_sec'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.flush_interval_sec'
            ],
          'business_stats_fallback_backfill_setting.batch_sleep_ms':
            settings[
              'business_stats_fallback_backfill_setting.batch_sleep_ms'
            ] ??
            systemTuningFallbackSettings[
              'business_stats_fallback_backfill_setting.batch_sleep_ms'
            ],
        }}
      />
    ),
  },
  {
    id: 'relay-timeout',
    titleKey: 'AI Request Timeout',
    build: (settings: SystemTuningSettings) => (
      <RelayTimeoutHotConfigSection settings={settings} />
    ),
  },
] as const

export type SystemTuningSectionId =
  (typeof SYSTEM_TUNING_SECTIONS)[number]['id']

const systemTuningRegistry = createSectionRegistry<
  SystemTuningSectionId,
  SystemTuningSettings
>({
  sections: SYSTEM_TUNING_SECTIONS,
  defaultSection: 'settlement-guard',
  basePath: '/system-settings/system-tuning',
  urlStyle: 'path',
})

export const SYSTEM_TUNING_SECTION_IDS = systemTuningRegistry.sectionIds
export const SYSTEM_TUNING_DEFAULT_SECTION = systemTuningRegistry.defaultSection
export const getSystemTuningSectionNavItems =
  systemTuningRegistry.getSectionNavItems
export const getSystemTuningSectionContent =
  systemTuningRegistry.getSectionContent
export const getSystemTuningSectionMeta = systemTuningRegistry.getSectionMeta
