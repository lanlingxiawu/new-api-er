# Stream refunds and delivery evidence

## Goals and scope

User instruction, 2026-11-04: remove the newly introduced upstream usage compatibility switch. Usage/protocol corrections always apply. Restore the established policy from `ddb988d77`: an upstream failure with no effective downstream delivery costs the user zero, even when upstream usage was reported; settle zero to release the reservation. This supersedes the undelivered-usage charging proposal in `upstream-usage-billing-compat-fixes.md`.

Preserve existing client-disconnect and gateway-owned timeout billing policies. Preserve the Gemini explicit-zero and xAI omitted-detail fixes. Do not equate upstream usage with an upstream invoice, upstream bytes with delivery, or successful server writes with application consumption.

Production incidents are not available locally. Local regression fixtures verify behavior, but cannot establish what happened or what the provider charged for those requests.

## Data flow and API contracts

Request -> per-attempt upstream response/protocol observation -> downstream write and flush -> immutable usage/delivery snapshot -> billing policy -> existing settlement -> existing private diagnostic log. No new endpoint, database query, or provider request. Diagnostic evidence remains accessible through the existing privileged diagnostic API; raw response data must not become public log fields.

## Data model and configuration

No schema, migration, index, Redis namespace, or billing ledger format changes. Remove `upstream_usage_compat_setting.enabled` registration, request snapshots and disabled branches. An old persisted key is inert and needs no destructive database cleanup. The separate Claude protocol option and existing user timeout options retain their current scope.

## Business logic and edge cases

- Upstream error, malformed/incomplete stream or transport failure + no effective delivery: `usage_source=none`, zero final usage/quota, release reservation even when confirmed usage is nonzero.
- Existing delivered text/thinking, complete tool calls and completed media remain effective only after successful full writes and flush. Role, usage, keepalive, signatures and unfinished tools alone are not delivery.
- Delivered content followed by failure retains the established confirmed/estimated billing rules. Local timeout and client disconnect remain distinct from upstream failure; cleanup errors must not overwrite an already established upstream cause.
- Preserve field presence and explicit zero in usage evidence. Raw upstream evidence must survive zeroing the billable usage.
- Capture the delivery decision and protocol state alongside upstream usage, bounded upstream/downstream bodies, termination observations, intended charge, reserved charge and actual settlement state. A settlement failure must remain `failed/partial`, never be presented as a successful refund.
- Diagnostic collection must not decide billing, fabricate protocol completion, drain upstream after failure, or retry financial settlement.

## Error handling and existing subsystems

Keep existing relay error ownership and exactly-once settlement. Evidence is observational, bounded, and uses existing privacy/redaction and asynchronous log persistence. No full prompts, credentials, unbounded stream buffers or new external calls are introduced.

## Main Chain Impact

Protocol parsing, field-presence merges, delivery commits and a final fixed-size evidence snapshot execute synchronously using existing request-local objects. Existing billing/log queues retain persistence. No new blocking I/O, timer, worker, goroutine, or network request is introduced.

## Shared Resource Audit

Only per-attempt sessions, captures and immutable log payloads are changed. Existing short session/capture locks remain request-local and do not span I/O. No new access to shared maps, Redis, database tables, connection pools or worker pools. Settlement uses the existing billing session and log dispatch; no extra reads/writes are added.

## Concurrency Analysis

At 30k and 100k RPM, removing configuration snapshots reduces per-request work. Added evidence consists of fixed counters/booleans and bounded existing captures, with constant per-finalization cost. Incremental DB calls: 0; Redis calls: 0; goroutines: 0. Regression and local fixture tests cannot establish production throughput.

## Test design and validation

Test first: failure/delivery/confirmed-usage cross product; metadata-only and partial-tool failures; valid text/thinking/tool/media delivery; write/flush failure; upstream error before downstream cancellation; explicit zero and omitted usage; repeated finalization; reservation release and settlement failure; raw usage/termination evidence survives refund and is private. Remove obsolete off-switch expectations while retaining meaningful always-on protocol tests. Run affected provider/common tests, isolated pure service tests and root build without live provider or production database access.

Verified locally on 2026-11-04 without a provider or production database. Upstream failure with confirmed usage and no effective delivery settles zero and releases the reservation. A tool name followed by `finish_reason=length` is not effective delivery; the same request still keeps upstream token evidence, a positive received-output estimate, and a zero written-output estimate. A flushed complete tool call remains billable after a later upstream read error. Root diagnostics show the delivery decision separately from upstream usage. These fixtures do not identify the production requests or prove what the provider invoiced.
