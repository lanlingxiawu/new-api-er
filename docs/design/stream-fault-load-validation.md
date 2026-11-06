# Local fault-injection load validation

The user explicitly requested load tests with simulated upstream faults and no real upstream calls on 2026-11-04. This is an opt-in validation harness for the existing implementation, not a production feature or deployment.

## Scope and execution

Use loopback `httptest` HTTP/TCP/WebSocket origins and vendor-shaped fixtures. Exercise the actual protocol handlers, converters, stream lifecycle, bounded diagnostics and usage selection. A separate service component load verifies pricing normalization and exactly-once terminal settlement using per-request in-memory funding fixtures. Do not load `.env`, connect to a provider account, or write a shared database. Explicit custom transports have no environment proxy; URLs come only from local test servers.

Run a low-volume correctness matrix before load. Then run each high-volume family sequentially so simultaneous load generators do not distort measurements. Use fixed worker limits, bounded sample arrays, finite request deadlines and shutdown/drain checks. Record actual completed throughput and latency rather than treating configured arrival rate as achieved throughput. Closed-loop throughput and scheduled-arrival tests are reported separately. Expected injected provider failures are successful test observations; unexpected outcomes are failures.

## Scenario matrix

- Normal vendor text, reasoning/thinking, tool calls, complete usage, cache read/write, explicit zero output, missing usage, large frames and fragmented frames.
- No response/slow headers, no output after headers, mid-stream pauses, response/total/idle timeout ownership, upstream EOF before completion, truncated chunked body, partial SSE/JSON, reset and read failures.
- Provider error events/statuses, invalid JSON, invalid block/event order, invalid usage shapes, missing terminal events, legitimate non-error terminal reasons and unknown events.
- Native and converted Claude, OpenAI Chat/Responses, Gemini; AWS SDK eventstream success/error/CRC/truncation. Realtime bidirectional messages, multiple rounds, duplicate terminal messages and connection errors.
- Downstream cancellation, slow/stopped reading, write/flush failures, local cleanup following another cause, compatibility flag on/off and SDK/channel response isolation.
- Billing: successful and failed requests, explicit zero versus missing usage, cache semantics, group multipliers including free groups, tier boundaries, interrupted-input estimate, zero-charge release, funding failure and post-commit failure, duplicate-finalization guard.

This matrix is finite and documents supported protocol equivalence classes. It does not claim every possible byte sequence, provider, operating system or race schedule has been exhausted.

## Assertions and measurements

Check returned frames, status/outcome, terminal-event counts, normalized usage, first observation side/operation, HTTP response indices, cleanup ordering, bounded private diagnostics and public-log stripping where applicable. Billing assertions use independently calculated expected quotas, not a result compared to itself. Assert no duplicate settlement or unintended refund and preserve upstream-confirmed versus estimated provenance.

Collect total operations, per-scenario counts, expected fault counts, unexpected failures, elapsed time, actual RPS/RPM, P50/P95/P99, allocations/GC and goroutine counts before/after cleanup. Capture peak memory/concurrency where the harness supports it. Run selected concurrent scenarios with the Go race detector separately; race-instrumented timing is not production throughput.

## Main Chain Impact

The load fixtures are opt-in `_test.go` code and add no runtime hooks. The confirmed production repairs are the logger counter/rotation atomics, narrow Gemini usage-presence/copy fixes, and AWS request-body interface wrapper described below. APIs, settings, schemas, pricing expressions and deployment are unchanged. Tests deliberately execute existing synchronous relay code against local mock upstreams. No new synchronous I/O, database query, Redis call or worker is added to relay execution.

## Shared Resource Audit

No new Redis keys, database tables, remote pools or production processes. Local listeners, client transports, worker pools and funding fixtures are owned and closed by each test. Global test configuration is changed only outside concurrent phases and restored afterward. Billing load stops at existing settlement/log serialization boundaries without invoking database/counter/log-worker side effects. Parser/expression caches remain shared as in the real process and are warmed before measurement.

## Concurrency Analysis and limits

Use bounded concurrency stages and existing scheduled-arrival component experiments at 500 and 1,667 requests/second (30k and about 100k RPM). If the local machine cannot sustain a target, record the achieved rate and queueing rather than claim capacity. This does not benchmark a deployed full router/authentication/database/Redis/ledger stack or geographically distributed networks. Local simulated timers reproduce timeout state transitions; the real stopped-reader fixture additionally exercises actual socket write deadlines. Environment and exact commands/results are recorded below after execution.

## Results

Test host: Windows amd64, AMD Ryzen 7 8700G, 16 logical processors, approximately 47.1 GiB physical RAM, Go 1.26.10. All traffic for this experiment stays on loopback; no provider credentials are used. Log files below live in `F:/code/new-api-er-experiments/`.

### Completed bounded-worker stages

| Suite | Workers | Operations | Expected injected faults | Unexpected outcomes | Actual RPS | P99 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Claude, 52 protocol/fault combinations | 32 | 5,200 | 4,000 | 0 | 699.91 | 250.88 |
| Claude, same matrix | 128 | 26,000 | 20,000 | 0 | 2,215.74 | 280.83 |
| OpenAI HTTP, 102 protocol/fault combinations | 16 | 10,200 | 7,800 | 0 | 6,817.30 | 12.10 |
| OpenAI HTTP, same matrix | 128 | 102,000 | 78,000 | 0 | 6,401.89 | 65.67 |
| Realtime, 15 fault/round combinations | 16 | 1,500 | 1,000 | 0 | 4,775.26 | 13.09 |
| Realtime, isolated batch after port recovery | 128 | 1,500 | 1,000 | 0 | 4,638.14 | 37.92 |
| Gemini, compatibility off / on | 16 | 1,200 / 1,200 | 560 / 560 | 0 / 0 | 1,419.20 / 1,462.27 | 150.52 / 150.38 |
| Gemini, compatibility off / on | 128 | 6,000 / 6,000 | 2,800 / 2,800 | 0 / 0 | 8,531.07 / 8,035.36 | 152.09 / 153.23 |
| Bedrock SDK, compatibility off / on | 16 | 400 / 400 | 320 / 320 | 0 / 0 | 6,548.13 / 7,556.95 | 9.63 / 6.28 |
| Bedrock SDK, repaired compatibility on | 128 | 4,000 | 3,200 | 0 | 7,317.94 | 88.16 |
| Pricing/terminal settlement, 108 combinations | 32 | 108,000 | funding outcomes included | 0 | 246,484.85 | 1.79 |
| Pricing/terminal settlement, same matrix | 128 | 108,000 | funding outcomes included | 0 | 196,340.40 | 7.27 |

Claude includes deliberate 250 ms stalls, so its latency percentiles include injected waiting. Claude and OpenAI drain to two goroutines after closing local resources. At 128 workers Claude heap peaks at 80.2 MB and is 11.7 MB after GC; OpenAI HTTP is 16.7 MB after GC. These are observed bounded runs, not proof of indefinite leak freedom. The billing stage is a sub-second CPU/component stress burst, not a steady-state wallet/DB throughput measurement; Windows clock granularity makes its P50 appear as zero, so do not interpret that as zero execution time.

Source logs: `fault-load-claude-w{32,128}.jsonl`, `fault-load-openai-w{16,128}.jsonl`, `fault-load-billing-w{32,128}.jsonl`.

The isolated Realtime recovery stage successfully established all 3,000 TCP connections, with zero transport-setup, invariant or panic failures. The harness now records those failure categories for all requests instead of relying solely on the first 20 examples. Gemini includes deliberate 150 ms total-timeout fixtures. Its 128-worker stage returns to two goroutines and 9.8 MB heap after shutdown and GC. Recovery logs: `fault-load-ws-w128-recovery.jsonl`, `fault-load-vendor-w{16,128}-recovery.jsonl`.

### Sustained scheduled arrivals

Each stage runs for 30 seconds with 64 bounded workers, alternating native Claude and converted Chat. All six pass: 195,030 requests, zero errors, actual rates within the required 95% target. Refusals are legitimate completed vendor messages, not transport failures. Source: `fault-load-scheduled.jsonl`.

| Profile | Target RPS | Requests | Actual RPM | Service P99 ms | Scheduled P99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Normal | 500 | 15,000 | 30,001.20 | 7.29 | 417.19 |
| Normal | 1,667 | 50,010 | 100,020.15 | 0.68 | 1.20 |
| Refusal | 500 | 15,000 | 30,000.87 | 0.68 | 1.18 |
| Refusal | 1,667 | 50,010 | 100,019.74 | 0.59 | 1.12 |
| 64 KiB refusal frame | 500 | 15,000 | 30,000.68 | 2.59 | 2.97 |
| 64 KiB refusal frame | 1,667 | 50,010 | 100,014.52 | 5.28 | 6.40 |

Scheduled latency includes generator delay; service latency does not. The first stage has a real high scheduling tail and is reported unchanged. This run started during recovery from the short-connection experiment, with some concurrent correctness compilation early in the run; these timings are validation measurements on a shared workstation, not an isolated hardware capacity certification. Large frames allocate approximately 0.96 MB/request and trigger 6,392 GC cycles in the high-rate stage; this cost must be included in production sizing. Heap after GC is below 8.5 MB in every stage, and goroutines return to three including the test sampler.

### Real slow/stopped downstream sockets

All seven profiles pass, totaling 416 actual downstream connections. Each attempts a 2 MiB response; frames are 16 KiB, except the separate 256 KiB-frame profile. Source: `fault-load-slow-client.jsonl`.

- Fast 64, slow 16/64/128 and large-frame slow 16 complete without truncation or false upstream errors. The 128-client slow profile peaks at 355.9 MB heap and returns to 9.7 MB after GC.
- All 64 stopped readers hit a real socket write timeout under the existing 30-second renewed write deadline, with handler durations up to 38.19 seconds including earlier successful writes. They are classified as downstream loss, and backpressure stops draining upstream bodies.
- All 64 clients that disconnect after reading 64 KiB are classified as downstream loss, without a false timeout; maximum handler duration is 0.31 seconds.
- Every profile drains relay/upstream/transport workers and returns to three goroutines including the sampler. Upstream capture remains at most 2,048 bytes; base64 downstream capture at most 2,732 bytes.

This does not exercise a 200 MiB Claude SSE frame at high concurrency. That boundary is kept separate from the bounded small-frame load model.

### Resource-limit experiment retained as a failure

The subsequent 15,000-operation Realtime stage at 128 workers failed with 7,244 unexpected results. The first 20 retained examples are fixture WebSocket dial failures (`connectex: Only one usage of each socket address ...`), before entering the relay handler. Because the initial harness retained only 20 examples, the remaining failures cannot all be attributed to the same cause from this log alone. No business pass is claimed for that stage.

The host reported 15,336 TIME_WAIT sockets against a dynamic TCP port range starting at 49,152 with 16,384 ports. A Gemini/Bedrock run immediately afterward also suffered local dial failures and is excluded from throughput conclusions; it additionally ran both packages concurrently and is retained only as exploratory evidence. Do not change host TCP settings or hide failures with retries. Rerun bounded batches after port recovery, serializing vendor packages with `-p 1`. The failing logs are retained as `fault-load-openai-w128.jsonl` and `fault-load-vendor-w16.jsonl`.

### Correctness gates discovered during preparation

The initial Gemini matrix exposed two defects before high load: an explicit final cache count of zero retained an earlier positive count, and Gemini-to-Responses replaced explicit zero input with an estimate during converter finalization. Source comparison against the real upstream-main worktree at `ba5f7367797566175a6cd97a748d430af3602a01` found no ready-made repair: upstream `relay-gemini.go` still uses the nonzero metadata merge, and `relay_responses.go` passes the same usage pointer into the converter's in-place final merge. This comparison is source evidence, not a new dual-worktree runtime experiment.

The authorized narrow repair stays behind the existing frozen `upstream_usage_compat_setting.enabled` flag. Gemini reads cache-field presence from the original validated metadata frame, so explicit zero replaces an earlier count while omission preserves it. Responses finalization receives an independent usage copy, including cloned token details and billing evidence, so intermediate conversion estimates cannot mutate the confirmed adapter usage. An additional review regression found that a first frame containing only `cachedContentTokenCount:0` produced input fallback 999 with total zero and no estimate provenance. The missing-input supplement now creates an estimated Gemini billing snapshot when the all-zero provider metadata has no snapshot; total becomes 999, `Estimated` and `LocalCountTokens` are true, and diagnostics still contain cache zero without a fabricated confirmed input count.

`TestForkGeminiUsagePresenceRegression` has 40 always-on cases: four output protocols, compatibility on/off, and cache zero, absent cache, all-zero usage, missing usage, and cache-only zero. The first two defects produced five independent red cases; the follow-up boundary produced four more. All 40 now pass, including the final Responses wire usage and its agreement with settlement. Gemini and AWS full package tests pass with the opt-in load disabled. The local HTTP/SDK workloads cover 120 Gemini and 40 Bedrock requests per repetition; high-load measurements are recorded separately above. No claim of capacity is based on these correctness passes.

The isolated Gemini 40-case regression and both vendor workloads also pass `-race` with four workers and one repetition (120 Gemini plus 40 Bedrock local HTTP requests). The workload post-shutdown samples return from two baseline goroutines to two in each package. Race-build timing is used only for correctness, not throughput conclusions. Commands use `go test -race -p 2`, `FORK_VENDOR_LOAD=1`, `FORK_VENDOR_LOAD_WORKERS=4`, `FORK_VENDOR_LOAD_REPEATS=1`, with each vendor package run sequentially; Windows CGO uses the local MinGW GCC toolchain.

Main-chain impact of the Gemini repair is limited to per-request metadata-presence checks and one independent final conversion usage copy. The metadata object is located once per frame, then its two scalar fields are inspected. The missing-input repair runs at finalization only. It adds no database or Redis operation, global map, shared lock, goroutine, connection, setting, or pricing expression. State and copied evidence stay request-owned; compatibility off preserves the prior merge/fallback behavior. Both opt-in workloads suppress log output with the existing `LogWriterMu` guard and restore it afterward. Their phase metrics explicitly label live heap/goroutines, and a separate sample follows worker completion, local transport/server shutdown and GC; neither sample proves indefinite leak freedom.

Concurrent settlement/error paths exposed a real data race in `logger.logHelper` (`logCount++`). A focused race test uses a race-safe sink to distinguish application races from test-buffer races. The counter and rotation flag now use atomics without adding a lock, I/O, worker or new setting. Logging text, rotation threshold and existing background rotation remain unchanged. The logger package and concurrent settlement harness pass the race detector after the repair. Shared-resource impact is limited to the existing process-wide logger counters; no database/cache/connection namespace is introduced.

An apparent OpenAI Chat-to-Responses cache loss was a fixture error, not a product defect: Responses stores the value in `InputTokensDetails`, while the initial assertion read the Chat-only flat field. Corrected assertions independently verify Responses cache count, canonical billing usage and diagnostic evidence. All three preserve the reported count, matching the real upstream-main worktree. No OpenAI production change was needed.

The Claude-to-Chat zero-input expectation deliberately preserves a different existing upstream policy. Upstream commit `1b63c28dcc` (#7816) treats input as uncounted when ordinary input, cache reads and cache writes are all zero, and applies the request estimate. A four-case external overlay produced identical current/real-upstream-main flat and canonical results: empty all-zero -> 50/0, text all-zero -> 50/2, zero input with output 12 -> 50/12, cache-only 200 -> 0/0 plus cache 200. Existing normal billing tests also check canonical usage and independent expected quota. Native strict Claude preserves explicit zero. This policy difference was not silently changed by load-test expectations; the original commit explains the output-present motivation, but not the empty-success business rationale. Existing `LocalCountTokens=true` with `BillingUsage.Estimated=false` on a completed converted stream remains an upstream provenance limitation, outside this repair. Evidence: `claude-zero-current-comparison.txt`, `claude-zero-upstream-comparison.txt`, `claude-zero-normal-canonical.txt`.

## Reproduction and final checks

### AWS request-body close ordering repair

The 128-worker SDK run exposed two unexpected failures in 4,000 requests. Extra evidence then reproduced failures with zero decoded events and only 17/102/493 raw bytes: a local request-write error closed the socket before a complete SDK event arrived. The first `net.Conn.Close` stack belongs to `net/http.persistConn.writeLoop`; `httptrace.WroteRequest` reports EOF. Requiring the mock server to read and validate the entire invocation JSON before returning HTTP 200 does not eliminate the failure, so early mock response alone is not the cause. Existing decoded-prefix preservation is verified separately by 12 deterministic SDK cases, including an error pending during downstream delivery.

A deterministic test using the real SDK-built request body confirms the lifecycle race: after writing the declared ContentLength, a remainder probe before SDK Close succeeds; the same probe after Close returns EOF. Smithy 1.24.2 closes the request body when HTTP `Do` returns. Its closed `safeWriteToReadCloser.WriteTo` returns EOF. Go 1.26.10's HTTP writer probes the remainder through `io.Copy`, which takes that WriterTo shortcut and propagates EOF as a write error, closing an already-started response. The current branch and actual upstream-main have the same Go, Bedrock SDK 1.50.4 and Smithy 1.24.2 versions and no existing repair for this ordering.

The authorized narrow fix is implemented in `relay/channel/aws/request_body.go`, composed with the existing diagnostic client in both authentication branches of `newAwsClient`. With the request's upstream-usage compatibility setting enabled, a body implementing `io.WriterTo` is presented to HTTP as `io.ReadCloser` only. A shallow request copy retains headers, context, ContentLength, GetBody and cancellation. No request payload is buffered or changed. The ordinary Reader EOF path ends the remainder probe successfully; non-EOF errors and genuinely short or overlong bodies remain errors. Disabled compatibility retains the original executor/body behavior. Tests cover both flags, the actual SDK body, bytes/length/GetBody preservation, non-EOF errors, short/overlong bodies and nil/non-WriterTo bodies. The deterministic compatibility test first failed with the enabled branch still returning EOF and passes with the wrapper; the disabled branch explicitly retains that original EOF as a rollback control.

Main Chain Impact: one interface check and a small request/body wrapper for eligible AWS calls; no new network/DB/Redis operation, timer, goroutine, lock or pool. Existing transport and response ownership remain intact. No module-cache or dependency edits, automatic retries, fabricated success or billing-policy changes. The wrapper is request-owned, does not modify the shared HTTP client/transport, and is reversible with the existing frozen compatibility setting. At 100k RPM the added work is bounded per eligible request; final measured SDK component results are recorded separately, not extrapolated to whole-gateway capacity.

The focused repair has 38 deterministic leaf cases across `request_body_test.go` and `fork_sdk_order_regression_test.go`; all pass with the race detector. Close errors are forwarded unchanged, the SDK-owned request object is retained, and a runtime setting change does not alter an already-created SDK client's frozen decision. The repaired production wrapper is also used by the local vendor workload. A four-worker race pass covers 40 SDK requests with zero unexpected outcomes and returns from two baseline goroutines to two after shutdown. The failed request-write investigations remain in `fault-load-aws-write-trace-before.txt` and `fault-load-aws-write-trace-after.txt`; the latter is the control that reads and validates the request but still fails before the production repair. High-load rollback runs may still reproduce the original EOF and must not be described as green.

The final repaired, enabled 128-worker run passes all 4,000 requests: 800 normal completions and 3,200 injected failures, zero unexpected results, 7,317.94 RPS and 88.16 ms P99. Heap returns to 9.7 MB after shutdown/GC and goroutines return from two to two. Source: `fault-load-aws-w128-fixed.jsonl`. This is a bounded component run; it does not prove indefinite absence of races. The deterministic disabled-flag test remains the regression's failure control.

Length-error coverage applies while the reader remains readable. After Smithy has closed and discarded its internal reader, no wrapper can retrospectively detect additional unread bytes. The SDK builds both the invocation body and ContentLength from the same byte reader; this repair changes neither. Independent review found no blocking issue with this scope.

Run each block in a fresh PowerShell process from the repository root. Do not run high-load families concurrently. Counts in the tables above identify the worker/repetition changes between stages. These commands do not load a provider account or `.env`.

```powershell
$env:FORK_CLAUDE_FAULT_LOAD='1'
$env:FORK_CLAUDE_FAULT_WORKERS='128'
$env:FORK_CLAUDE_FAULT_REQUESTS='26000'
$env:FORK_CLAUDE_FAULT_STALL_MS='250'
go test -p 2 ./relay/channel/claude -run '^TestForkClaudeMixedFaultLoad$' -count=1 -json
```

```powershell
$env:FORK_LOOPBACK_LOAD='1'
$env:FORK_LOOPBACK_REPEAT='100'
$env:FORK_LOOPBACK_WORKERS='128'
go test -p 2 ./relay/channel/openai -run '^TestForkLoopbackLoadRealtime$' -count=1 -json
# HTTP-only high stage used REPEAT=1000, WORKERS=128, TestForkLoopbackLoadHTTP.
```

```powershell
$env:FORK_VENDOR_LOAD='1'
$env:FORK_VENDOR_LOAD_WORKERS='128'
$env:FORK_VENDOR_LOAD_REPEATS='100'
go test -p 1 ./relay/channel/gemini -run '^TestForkGeminiVendorConcurrentLoad$' -count=1 -json
# The AWS disabled flag deliberately retains the original request-close defect.
# The final repaired high-load run selects the enabled branch only:
$env:FORK_VENDOR_LOAD_REPEATS='200'
go test -p 1 ./relay/channel/aws -run '^TestForkAWSVendorConcurrentLoad$/compat_true$' -count=1 -json
```

```powershell
$env:STREAM_DIAGNOSTIC_LOAD='1'
$env:STREAM_DIAGNOSTIC_LOAD_SECONDS='30'
go test -p 2 ./relay/channel/claude -run '^TestClaudeDiagnosticLoad$' -count=1 -timeout=6m -json
```

```powershell
$env:STREAM_SLOW_CLIENT_LOAD='1'
go test -p 2 ./relay/channel/claude -run '^TestClaudeSlowClientLoad$' -count=1 -timeout=8m -json
```

For the pure service and middleware experiments, explicitly exclude the package TestMain, which otherwise loads database configuration:

```powershell
$env:STREAM_FAULT_BILLING_LOAD='1'
$env:STREAM_FAULT_BILLING_WORKERS='128'
$env:STREAM_FAULT_BILLING_REQUESTS='108000'
$sourceFiles = @(Get-ChildItem service -Filter *.go | Where-Object { $_.Name -notlike '*_test.go' } | ForEach-Object { 'service/' + $_.Name })
go test -p 2 @sourceFiles service/fork_stream_fault_billing_load_test.go service/stream_settlement_test.go -run '^TestForkStreamFaultBillingLoad$' -count=1 -json
```

```powershell
$sourceFiles = @(Get-ChildItem middleware -Filter *.go | Where-Object { $_.Name -notlike '*_test.go' } | ForEach-Object { 'middleware/' + $_.Name })
go test -p 2 @sourceFiles middleware/fork_vendor_timeout_matrix_test.go -run '^TestForkVendor' -count=1 -json
```

The middleware matrix passes 138 named top-level/subtests (139 pass events including the package), exercising the real timeout controller, timeout billing modes and terminal suppression without database bootstrap. The final seven-package regression after the AWS repair passes 3,697 named top-level/subtests with zero failures; seven opt-in load tests are skipped in that ordinary run, and one existing DB-dependent admin test skips. Those opt-in workloads are exercised separately above. Source: `fault-load-final-regression-fixed.jsonl`; the earlier pre-AWS regression is retained as `fault-load-final-regression.jsonl`.

```powershell
go test -p 2 ./logger ./relay/common ./relay/helper ./relay/channel/claude ./relay/channel/openai ./relay/channel/gemini ./relay/channel/aws -count=1 -timeout=6m -json
go build -p 2 ./...
# Also passed separately from relaykit/ with GOWORK=off:
# go build -p 2 ./...
```

Race checks use process-local `CGO_ENABLED=1`, `CC=C:/msys64/mingw64/bin/gcc.exe` and that compiler directory prepended to PATH. Logger race testing fails before the atomic repair and passes afterward. After repair, the pure billing workload (216 operations/8 workers), Claude/OpenAI concurrent matrices (104 + 234 operations/8 workers), common termination/response-isolation tests, and Gemini/AWS matrices (120 + 40 operations/4 workers) pass with `-race`. Race timings are excluded from throughput tables. No host-wide toolchain, TCP or production configuration was changed. Both root build and independently built relaykit pass; `git diff --check` is clean.
