export const TROUBLESHOOTING_FILTERS = [
  { value: 'anomaly_only', label: 'Any anomaly' },
  { value: 'anomaly_kinds', label: 'Anomalies' },
  { value: 'charged', label: 'Charged' },
  { value: 'quota_min', label: 'Quota' },
  { value: 'completion_tokens_min', label: 'Min output tokens' },
  { value: 'completion_tokens_max', label: 'Max output tokens' },
  { value: 'use_time_min', label: 'Min duration (s)' },
  { value: 'min_retry_count', label: 'Min retries' },
] as const
