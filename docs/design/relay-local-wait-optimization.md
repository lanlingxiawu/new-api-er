# Local wait optimization

Confirmed implementation for the 2026-11-19 memory diagnosis. This does not shorten upstream waits, change billing, or resize the Redis pool.

## Existing logic

- `PostTextConsumeQuota` called `observeChannelAffinityUsageCache` before settlement. That function held one of 64 locks across a Redis get and set on the shared `common.RDB` client. The counters are admin hit-rate display data.
- `perfmetrics.Record` already updated the local retry guard and memory buckets, then started one goroutine per sample to write Redis.
- `RelayResponseTraceContext` already traces a completed body write and, for managed non-stream calls, the first response byte. Those hooks decide timeout billing and must keep running.
- The performance page showed Go `Alloc`, cumulative `TotalAlloc`, and `Sys` beside the body-cache size without stating that they measure different things. A memory suggestion below the current reservation could still be filled into the form.

## Behavior

- Affinity display stats snapshot the counters on the request goroutine and enqueue them. Four workers apply the existing locked update. A full queue of 4096 drops the sample. `CHANNEL_AFFINITY_USAGE_STATS_SYNC=true` restores the inline update.
- Performance Redis writes merge by model, group, and hour, then flush about once per second on one pipeline. `PERF_METRICS_REDIS_IMMEDIATE=true` restores one write per sample. The in-process retry guard is unchanged.
- Each `doRequest` records the first connection-acquisition start, first obtained connection, first request-write callback (including its success/failure result), first response byte, and HTTP call return relative to request start, under `admin_info.upstream_exchange`. HTTP return does not imply that the response body has finished. Missing callbacks stay absent; old logs without a write result remain unknown. The 2026-11-21 [timing diagnostics improvement](upstream-timing-diagnostics.md) also copies the known declared body size without reading the body; protocol metadata and request-ID timestamp inference were removed during the necessity review. `httptrace.WithClientTrace` composes the diagnostic and existing timeout hooks exactly once; the diagnostic timestamps run before the existing hooks, following the standard library's ordering. `UPSTREAM_EXCHANGE_TRACE_DISABLED=true` disables these diagnostic hooks and log fields without disabling timeout or billing traces.
- The performance page separates live heap, cumulative allocation, Go-reserved memory, the part of the heap outside the body cache, host memory percent, and this process's Redis pool. Pool size remains `REDIS_POOL_SIZE`. A memory suggestion below the current reservation cannot be filled.

## Main Chain Impact

Request goroutines no longer wait on the affinity display Redis update. They record a few timestamps while the existing HTTP trace runs. No billing formula, timeout, retry, or request-body budget changes. The shared Redis client and `perf:` keys stay the same; the affinity path uses fewer concurrent commands and the metrics path sends one pipeline per second instead of one per request. Failures in the workers are recovered and do not affect the relay response.

## Shared Resource Audit

- Affinity observations use the existing `new-api:channel_affinity_usage_cache_stats:v1` namespace for display counters, separate from affinity routing keys. Four workers use the existing 64 counter locks and shared Redis pool. The request goroutine only copies counters and attempts a non-blocking queue send; a full queue drops display statistics.
- Performance counters continue to use `perf:<model>:<group>:<hour>` and the same Redis pool. Requests merge a small delta under the pending-map mutex; one worker swaps the map and writes a pipeline with a two-second timeout. Existing local retry-guard buckets remain immediately available and do not depend on the flush succeeding.
- Trace clocks and attempt marks belong to one request. Transport callbacks use atomic timestamp stores; there is no DB/Redis access or additional goroutine per attempt. The existing log path stores admin-only timings; no new table, index, pool or synchronous log write is introduced.
- Redis pool statistics are read locally by the existing performance management API. They do not create connections or issue Redis commands. The new rollback environment switches are deployment diagnostics, not new dashboard settings.

## Concurrency Analysis

At 30k and 100k RPM, affinity submissions are approximately 500 and 1,667 per second. They share four workers and a 4,096-entry queue; overflow drops display observations instead of waiting on the relay goroutine. Metrics add no per-request network calls and retain one flush worker; Redis operations scale with distinct model/group/hour buckets per flush. Short pending-map locking and three atomic trace marks are the added synchronous work. Shared Redis capacity still requires observation under deployment load; these bounds and code-path checks are not a throughput benchmark.

## Regression Coverage

- HTTP fixture tests assert that pre-existing connection, request-write, and first-byte hooks each run exactly once, that missing timestamps stay absent, and that disabling exchange diagnostics preserves the upstream response.
- Affinity tests cover asynchronous counter updates, the synchronous fallback, and dropping observations when all workers and queue slots are occupied. The worker test hook is loaded and replaced atomically.
- Performance delta tests verify that merging successful and failed samples preserves request, latency, first-token, output-token, and generation-time counters.

## Not in this change

Redis pool size is displayed, not edited. Billing body copies, response-header timeout, idle timeout, and HTTP/2 sharding are unchanged.
