# Stream termination observations

Status: implementation authorized by the user's instruction to distinguish disconnect sources, following the reviewed proposal. This extends existing private stream diagnostics and does not change relay or billing decisions.

## Goals and scope

Record where the gateway first observes a termination signal: downstream request cancellation or read/write/flush failure, an upstream read/write/protocol failure, or a gateway timeout/local failure. Record subsequent upstream cleanup separately. These are observations at the gateway, not proof that an end user, SDK, CDN or provider deliberately disconnected. Concurrent events have only a local observation order.

Cover managed HTTP streams, native strict Claude and managed Realtime. Keep ordinary non-stream/legacy paths and pre-success HTTP errors outside the existing successful-response diagnostic gate. Channel retries create independent records; Realtime rounds on one connection retain the connection's observation record. No production deployment or proxy configuration changes are included.

## Data flow and API contract

BeginStreamAttempt freezes the existing upstream usage compatibility flag and creates one request-local observer shared by the session and bounded capture. Existing read/write/cancel/timeout/error/close boundaries record fixed-size observations. Logging copies the observer into the private diagnostic envelope and uses the existing asynchronous log pipeline. Root reads the existing GET /api/log/claude-diagnostic endpoint; no new endpoint or auth changes.

The optional diagnostic `termination` object contains:

- `first_observed`: the first termination observation (absent for normal completion/cleanup alone).
- `upstream_read`: the first terminal transport read result, including ordinary EOF; this alone does not mean protocol failure.
- `upstream_read_after_close`: whether that read result was observed after the gateway began closing upstream.
- `upstream_close`: the gateway's first upstream close action, separate from the triggering observation.
- `current_response`: the sequence number of the current captured HTTP response, omitted for a recorder without a captured HTTP exchange. Each observation can include its own `response_index`.

Each observation contains `side` (`downstream`, `upstream`, `gateway`, `unknown`), `operation`, `error_class`, and `after_ms` relative to the attempt's observer creation. Optional `timeout_kind`, `timeout_seconds` and `ws_close_code` add typed evidence. Operations include `request_cancel`, `read`, `write`, `flush`, `ping`, `protocol`, `panic`, `timeout`, and `close`. Error classes include `canceled`, `deadline`, `eof`, `unexpected_eof`, `reset`, `broken_pipe`, `short_write`, `closed`, `protocol`, `provider_error`, `panic`, `other`, and `cleanup`. Arbitrary error/context-cause strings, credentials, request bodies and WebSocket close text are not added to this object. Existing bounded private error text remains unchanged.

The frontend uses the existing Root-only diagnostic panel, labels these as gateway observations, and handles absent/unknown fields without claiming an initiating actor. All new text is translated in the project's seven locales.

SDK retries within an attempt can create several captured HTTP exchanges. The attempt's first observation remains immutable and carries its response index; it can belong to an earlier recovered exchange and is not a claim about the final failure. The read/close slots refer to the current exchange. Each captured body binds its generation, so a late read/close on an old body cannot suppress or contaminate the current exchange. The UI displays the current and individual response indices. Re-registering an already wrapped body does not advance the generation. Channel retries still allocate a new recorder, while Realtime rounds retain their connection recorder.

## Business logic and error handling

The observer never changes StreamOutcome, protocol completion, quota, retry, cancellation or response bytes. Existing typed request-local timeout ownership identifies gateway response/total timeouts; legacy idle timeouts are explicitly observed where their timer fires. A canceled request is checked before recording read errors caused by cancellation. Raw EOF is recorded as transport evidence but is promoted to a first termination only when the owning protocol reader determines it was incomplete. Active cleanup records are never promoted into a new upstream-failure first cause. Writer panic is a gateway failure, not evidence of a client disconnect. Downstream protocol violations and upstream WebSocket send failures retain their actual direction/operation.

The same observer remains usable when strict Claude disables the generic session. Only fixed slots are retained; duplicates do not append unbounded events or overwrite the first observation. Close is observed before invoking existing cleanup. No additional close, drain, retry or timer is introduced. Error classification uses typed errors where possible, not arbitrary string matching to infer a remote actor.

Timeout kinds are `response_timeout`, `total_timeout`, and `idle_timeout`. The actual middleware timeout controller is captured at attempt initialization; its current typed ownership is checked when an existing execution boundary observes cancellation. Write/flush deadline errors retain the downstream operation and typed `deadline` error class; they do not establish whether an SDK or proxy disconnected. Transparent captured-body wrappers identify that they already observe close, so an owner does not associate a previously detached closer with a newer exchange.

The existing frozen `upstream_usage_compat_setting.enabled` gates this extension. No removed diagnostic switches are restored. Existing private-data stripping and root authorization continue unchanged; historical unrecorded observations cannot be reconstructed.

## Data model and configuration

Only an optional object in existing logs.other.stream_diagnostic is added. No tables, columns, indexes, queries, migrations or billing expressions change. The existing compatibility setting provides rollback. Original diagnostic retention eligibility is preserved, including the newly authorized zero-output retention.

## Main Chain Impact

Synchronous work is a request-local pointer check, typed classification and fixed-slot update at termination/error/close boundaries, plus existing snapshot creation. There are no per-chunk timestamps or event lists, no new I/O, background watcher, goroutine, timer, worker, provider call or database/Redis access. Persistence stays asynchronous in the existing log pipeline. UI interpretation runs outside the relay chain.

## Shared Resource Audit

The observer uses only request-local memory and a short mutex, never held across I/O. It shares the existing capture/session pointers and existing logs.other queue, without new keys, tables, connection pools or shared caches. Existing users/tokens/channels billing access is unchanged. Raw capture size and retention limits do not grow; the additional observation object has a fixed number of small fields.

## Concurrency Analysis

At 30k/100k RPM, added DB/Redis/network operations, timers and goroutines per request are zero. Each enabled attempt allocates one bounded observer; normal streams have no error-event updates and only existing cleanup adds a fixed observation. There is no cross-request synchronization or unbounded memory growth. Existing log writes carry a small additional object only when diagnostics are retained. Static analysis is not a claim of measured production throughput.

## Test-first validation

Before implementation, add deterministic tests for downstream context cancellation, owned response/total and legacy idle timeouts, upstream EOF before/after terminal, unexpected EOF/reset, short write, write/flush timeout, writer panic, explicit protocol errors, upstream WebSocket write versus downstream read, close-induced late read errors, simultaneous-signal local ordering, SDK/channel retries, Realtime rounds, disabled compatibility and snapshot isolation. Assert unchanged returned bytes/errors, status, usage, settlement and close counts. Frontend tests cover Root access, old/unknown data and clear actor uncertainty; run translations, typecheck and lint.

Use local fixtures only; no real provider, account or production data access. Run relevant Go package regressions and root build. No database behavior changes are introduced, so this extension does not require new dialect/migration experiments.

## Implementation and verification

The observer is integrated at attempt initialization, captured transport reads/closes, generic stream session/writer boundaries, the managed scanner, strict Claude, Realtime and private log snapshotting. The log boundary refreshes only termination metadata after deferred cleanup; finalized usage, estimation, settlement and error evidence remain intact. The existing Root panel uses the existing diagnostic API and has no new network request or auth path.

Test-first evidence under `F:/code/new-api-er-experiments/`: service wiring had 5 failing/1 passing leaf cases before integration; strict Claude had 6 failing/6 passing cases after correcting the fixture's expected existing message_delta usage patch; scanner conversion-panic classification had 1 failing/3 passing cases. Common boundary tests found 8 actual behavior failures after the initial missing-API compilation failure. SDK retry tests reproduced three failures for a previous accepted response's close/EOF contaminating the next accepted response; three rejected-response gate controls passed. The fixes retain the original return values, bytes, usage, close counts and settlement behavior.

The service file-list regression excludes the repository's database-opening TestMain and runs all service production files plus the existing pure stream diagnostic, settlement, deadline, usage, gate and zero-output fixtures. Provider/helper regressions run their ordinary package tests. Database schemas/queries and relaykit APIs are unchanged. Production deployment, real provider calls and production load testing are outside this follow-up.

Final checks (2026-11-04):

- Common full package: 1,001 leaf tests passed, zero failures/skips. The 53 new common cases also passed `-race` using process-local `CGO_ENABLED=1` and `C:/msys64/mingw64/bin/gcc.exe`; no machine-wide toolchain changes were made. This supersedes an earlier attempt with the default disabled CGO.
- Service pure regression: 206 leaf tests passed, zero failures/skips. Dedicated wiring, timeout ownership, retry reset, snapshot refresh and private-log projection tests are included.
- Claude, OpenAI and helper full package regression: 1,824 leaf tests passed, zero failures; three pre-existing opt-in/environment-gated tests skipped (two Claude load experiments, one helper admin-price test requiring SQL_DSN). New cases include 16 strict Claude, 5 scanner and 28 loopback Realtime cases. No production throughput claim is made.
- Frontend: 66 related tests passed, including 17 diagnostic panel tests. `tsgo -b`, changed-file lint and `bun run build` passed. This machine's Bun 1.3.13 does not implement the repository's `bun check` typecheck script, so the installed TypeScript Go checker was invoked directly. Seven locale files each gained 27 keys; existing values, reports and auth plumbing were not changed.
- Root `go build ./...`, formatting and `git diff --check` passed. No binary was deployed, no shared database/provider account was accessed, and no test background service remains.

Final evidence files: `stream-termination-common-regression.jsonl`, `stream-termination-common-race.jsonl`, `termination-service-final.jsonl`, `termination-providers-final.jsonl`, and `termination-scanner-final.jsonl` under the experiment directory above. The code and UI report locally observed evidence; confirming the initiating client/SDK/CDN/proxy still requires correlated logs from those layers.
