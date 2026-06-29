export const systemTuningDefaults = {
  'business_stats_circuit_breaker_setting.enabled': true,
  'business_stats_circuit_breaker_setting.manual_disabled': false,
  'business_stats_circuit_breaker_setting.failure_threshold': 3,
  'business_stats_circuit_breaker_setting.initial_cooldown_seconds': 60,
  'business_stats_circuit_breaker_setting.max_cooldown_seconds': 3600,
  'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms': 800,
  'ledger_pipeline_setting.flush_interval_sec': 8,
  'ledger_pipeline_setting.outer_batch_size': 2000,
  'ledger_pipeline_setting.cost_outer_batch_size': 0,
  'ledger_pipeline_setting.inner_batch_size': 500,
  'ledger_pipeline_setting.settlement_flush_max_per_cycle': 15000,
  'ledger_pipeline_setting.cost_flush_max_per_cycle': 0,
  'ledger_pipeline_setting.full_drain': false,
  'ledger_pipeline_setting.buf_max_entries': 100000,
  'ledger_pipeline_setting.dedup_mem_max_entries': 200000,
  'ledger_pipeline_setting.dedup_use_redis': false,
  'ledger_pipeline_setting.dedup_redis_ttl_sec': 21600,
  'ledger_pipeline_setting.flush_db_timeout_sec': 30,
  'ledger_retry_setting.retry_flush_interval_sec': 2,
  'ledger_retry_setting.stat_upsert_max_retries': 10,
  'ledger_retry_setting.retry_queue_max_entries': 50000,
  'ledger_retry_setting.allow_concurrent_flush': false,
  'ledger_pipeline_setting.fallback_queue_capacity': 10000,
  'ledger_pipeline_setting.shutdown_timeout_sec': 25,
  'payment_setting.user_export_max_rows': 10000,
  'export_setting.user_export_enabled': true,
  'export_setting.rate_limit_cooldown_sec': 600,
  'export_setting.hard_ceiling_rows': 1000000,
  'ledger_detail_setting.export_user_cooldown_sec': 300,
  'ledger_detail_setting.export_batch_size': 3000,
  'ledger_detail_setting.export_batch_sleep_ms': 100,
  'ledger_detail_setting.export_rows_per_file': 1000000,
  'ledger_detail_setting.export_max_range_sec': 86400,
  'ledger_detail_setting.export_timeout_sec': 7200,
  'ledger_detail_setting.list_max_range_sec': 86400,
  'ledger_detail_setting.list_default_range_sec': 86400,
  'ledger_detail_setting.list_default_limit': 100,
  'ledger_detail_setting.list_max_limit': 200,
  'ledger_detail_setting.list_scan_batch_size': 2000,
  'ledger_detail_setting.list_scan_rows_per_req': 20000,
  'business_stats_fallback_backfill_setting.enabled': true,
  'business_stats_fallback_backfill_setting.use_separate_fallback_dir': false,
  'business_stats_fallback_backfill_setting.status_cache_seconds': 15,
  'business_stats_fallback_backfill_setting.max_read_line_bytes': 1048576,
  'business_stats_fallback_backfill_setting.write_batch_size': 200,
  'business_stats_fallback_backfill_setting.flush_interval_sec': 5,
  'business_stats_fallback_backfill_setting.batch_sleep_ms': 200,
};

export const businessStatsGuardDefaults = {
  'business_stats_circuit_breaker_setting.enabled':
    systemTuningDefaults['business_stats_circuit_breaker_setting.enabled'],
  'business_stats_circuit_breaker_setting.manual_disabled':
    systemTuningDefaults['business_stats_circuit_breaker_setting.manual_disabled'],
  'business_stats_circuit_breaker_setting.failure_threshold':
    systemTuningDefaults['business_stats_circuit_breaker_setting.failure_threshold'],
  'business_stats_circuit_breaker_setting.initial_cooldown_seconds':
    systemTuningDefaults[
      'business_stats_circuit_breaker_setting.initial_cooldown_seconds'
    ],
  'business_stats_circuit_breaker_setting.max_cooldown_seconds':
    systemTuningDefaults['business_stats_circuit_breaker_setting.max_cooldown_seconds'],
  'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms':
    systemTuningDefaults[
      'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms'
    ],
};

export const ledgerPipelineDefaults = {
  'ledger_pipeline_setting.flush_interval_sec':
    systemTuningDefaults['ledger_pipeline_setting.flush_interval_sec'],
  'ledger_pipeline_setting.outer_batch_size':
    systemTuningDefaults['ledger_pipeline_setting.outer_batch_size'],
  'ledger_pipeline_setting.cost_outer_batch_size':
    systemTuningDefaults['ledger_pipeline_setting.cost_outer_batch_size'],
  'ledger_pipeline_setting.inner_batch_size':
    systemTuningDefaults['ledger_pipeline_setting.inner_batch_size'],
  'ledger_pipeline_setting.settlement_flush_max_per_cycle':
    systemTuningDefaults['ledger_pipeline_setting.settlement_flush_max_per_cycle'],
  'ledger_pipeline_setting.cost_flush_max_per_cycle':
    systemTuningDefaults['ledger_pipeline_setting.cost_flush_max_per_cycle'],
  'ledger_pipeline_setting.full_drain':
    systemTuningDefaults['ledger_pipeline_setting.full_drain'],
  'ledger_pipeline_setting.buf_max_entries':
    systemTuningDefaults['ledger_pipeline_setting.buf_max_entries'],
  'ledger_pipeline_setting.dedup_mem_max_entries':
    systemTuningDefaults['ledger_pipeline_setting.dedup_mem_max_entries'],
  'ledger_pipeline_setting.dedup_use_redis':
    systemTuningDefaults['ledger_pipeline_setting.dedup_use_redis'],
  'ledger_pipeline_setting.dedup_redis_ttl_sec':
    systemTuningDefaults['ledger_pipeline_setting.dedup_redis_ttl_sec'],
  'ledger_pipeline_setting.flush_db_timeout_sec':
    systemTuningDefaults['ledger_pipeline_setting.flush_db_timeout_sec'],
  'ledger_retry_setting.allow_concurrent_flush':
    systemTuningDefaults['ledger_retry_setting.allow_concurrent_flush'],
  'ledger_retry_setting.retry_queue_max_entries':
    systemTuningDefaults['ledger_retry_setting.retry_queue_max_entries'],
  'ledger_retry_setting.retry_flush_interval_sec':
    systemTuningDefaults['ledger_retry_setting.retry_flush_interval_sec'],
  'ledger_retry_setting.stat_upsert_max_retries':
    systemTuningDefaults['ledger_retry_setting.stat_upsert_max_retries'],
  'ledger_pipeline_setting.fallback_queue_capacity':
    systemTuningDefaults['ledger_pipeline_setting.fallback_queue_capacity'],
  'ledger_pipeline_setting.shutdown_timeout_sec':
    systemTuningDefaults['ledger_pipeline_setting.shutdown_timeout_sec'],
};

export const exportSettingsDefaults = {
  'payment_setting.user_export_max_rows':
    systemTuningDefaults['payment_setting.user_export_max_rows'],
  'export_setting.user_export_enabled':
    systemTuningDefaults['export_setting.user_export_enabled'],
  'export_setting.rate_limit_cooldown_sec':
    systemTuningDefaults['export_setting.rate_limit_cooldown_sec'],
  'export_setting.hard_ceiling_rows':
    systemTuningDefaults['export_setting.hard_ceiling_rows'],
};

export const ledgerDetailDefaults = {
  'ledger_detail_setting.export_user_cooldown_sec':
    systemTuningDefaults['ledger_detail_setting.export_user_cooldown_sec'],
  'ledger_detail_setting.export_batch_size':
    systemTuningDefaults['ledger_detail_setting.export_batch_size'],
  'ledger_detail_setting.export_batch_sleep_ms':
    systemTuningDefaults['ledger_detail_setting.export_batch_sleep_ms'],
  'ledger_detail_setting.export_rows_per_file':
    systemTuningDefaults['ledger_detail_setting.export_rows_per_file'],
  'ledger_detail_setting.export_max_range_sec':
    systemTuningDefaults['ledger_detail_setting.export_max_range_sec'],
  'ledger_detail_setting.export_timeout_sec':
    systemTuningDefaults['ledger_detail_setting.export_timeout_sec'],
  'ledger_detail_setting.list_max_range_sec':
    systemTuningDefaults['ledger_detail_setting.list_max_range_sec'],
  'ledger_detail_setting.list_default_range_sec':
    systemTuningDefaults['ledger_detail_setting.list_default_range_sec'],
  'ledger_detail_setting.list_default_limit':
    systemTuningDefaults['ledger_detail_setting.list_default_limit'],
  'ledger_detail_setting.list_max_limit':
    systemTuningDefaults['ledger_detail_setting.list_max_limit'],
  'ledger_detail_setting.list_scan_batch_size':
    systemTuningDefaults['ledger_detail_setting.list_scan_batch_size'],
  'ledger_detail_setting.list_scan_rows_per_req':
    systemTuningDefaults['ledger_detail_setting.list_scan_rows_per_req'],
};

export const fallbackBackfillDefaults = {
  'business_stats_fallback_backfill_setting.enabled':
    systemTuningDefaults['business_stats_fallback_backfill_setting.enabled'],
  'business_stats_fallback_backfill_setting.use_separate_fallback_dir':
    systemTuningDefaults['business_stats_fallback_backfill_setting.use_separate_fallback_dir'],
  'business_stats_fallback_backfill_setting.status_cache_seconds':
    systemTuningDefaults[
      'business_stats_fallback_backfill_setting.status_cache_seconds'
    ],
  'business_stats_fallback_backfill_setting.max_read_line_bytes':
    systemTuningDefaults[
      'business_stats_fallback_backfill_setting.max_read_line_bytes'
    ],
  'business_stats_fallback_backfill_setting.write_batch_size':
    systemTuningDefaults[
      'business_stats_fallback_backfill_setting.write_batch_size'
    ],
  'business_stats_fallback_backfill_setting.flush_interval_sec':
    systemTuningDefaults[
      'business_stats_fallback_backfill_setting.flush_interval_sec'
    ],
  'business_stats_fallback_backfill_setting.batch_sleep_ms':
    systemTuningDefaults['business_stats_fallback_backfill_setting.batch_sleep_ms'],
};
