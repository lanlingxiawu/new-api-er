# Claude stream termination, partial billing and private diagnostics

Approved for implementation in the current task on 2026-09-10.

## 中文注释补充（2026-09-10，已确认）

范围为本轮 Claude 流式解析、用量确认、结算退款、原始响应诊断、权限控制与对应前端及测试的新增或已修改方法。逐项说明方法功能、参数含义、返回值、字段单位、零值语义和关键状态转换；既有上游代码中与本轮无关的声明保持原样。JSON 语言文件不插入注释，编译指令、序列化标签与接口签名保持原样。

仅修改注释和必要的格式。修改前保存当前工作区快照，以 Go 词法标记及前端 TypeScript AST 对照确认可执行语义一致；再执行相关测试、构建、类型检查和编码检查。原始 body 调试输出保持现状。Main Chain Impact：没有新增同步或异步执行、分配、锁、数据库/Redis 调用或共享资源；原有主链路、计费、日志队列及权限行为全部保持一致。

完成与验证：

- 已补充 121 个本轮新增/修改的 Go 方法（含测试及夹具）的中文功能、接收者、参数和返回值说明，并补齐响应采集、协议状态、结算参数和配置字段注释；前端补充诊断接口、组件参数、解码、查询缓存和权限回调说明。共涉及 42 个源文件/设计文件，既有许可证头和运行时文案保持原样。
- 以修改前快照验证：所有 Go 非注释词法标记完全一致；四个前端 TS/TSX 文件去除注释/位置元数据后的 AST 完全一致。方法/接收者/参数注释覆盖检查通过，UTF-8 无 BOM、Go 格式和差异空白检查通过。
- relay/common、Claude、AWS、共享 channel、relay/helper、operation_setting、i18n、router 测试通过，根模块构建通过；前端类型检查和本轮文件的 oxlint 通过。
- service/model/controller/middleware 的相关回归测试通过。标准入口仍因本地 MySQL 127.0.0.1:3306 未启动而在 TestMain 初始化阶段停止；仅在临时 Go overlay 中绕开环境初始化运行本地夹具，需要数据库的权限/投影用例使用真实 SQLite/GORM。仓库测试入口、生产数据库和真实上游均保持原样，未执行部署或历史日志回填。

## Approved subscription log snapshot repair (2026-09-10)

The usage/error log `other` map is constructed before terminal settlement. A successful subscription refund updates `RelayInfo.SubscriptionPostDelta`, but the previously built map still contains the reserved consumption and pre-refund balance. Refresh the subscription projection after the one terminal settlement attempt and before either error or consume logging. Reset the zero-omitting consumption/delta fields before reusing the existing billing serializer, so a full refund displays final consumption zero. Keep the historical pre-consumed amount as evidence, not as the final charge. Retain the existing generic serializer's omitted-zero contract for other paths.

Use only the subscription delta actually recorded by settlement, including a committed funding adjustment followed by a token-adjustment failure. A funding failure must not be presented as a successful refund. Keep the existing settlement state, intended quota, one-attempt guard and accounting operations unchanged. Wallet logs, legacy/non-strict settlement, pricing, API contracts, configuration and historical rows are outside this repair.

Tests precede implementation: full/partial refund, supplementary charge, equal reservation/charge, zero reservation, funding failure, committed funding with token failure, stale zero fields, remaining-balance floor, duplicate settlement, nil log metadata and wallet isolation. Exercise the real BillingSession with an in-memory funding fixture; no upstream request or production balance mutation is needed.

### Main Chain Impact — subscription log snapshot repair

Synchronous work adds a bounded update of the existing request-local log map after settlement. No additional balance operation, DB/Redis call, network/file I/O, lock, pool or goroutine is introduced. The existing strict-stream feature flag gates this path. Logs still use the existing asynchronous pipeline. Shared Resource Audit: the same log map is serialized only after refresh; no shared cache or cross-request state is added, and existing subscription/token tables are accessed only by the unchanged settlement operation. At 30k/100k RPM, incremental shared-resource calls remain zero and per-request work is constant-sized; no new buffer accumulates stream data.

### Pending legacy non-streaming debug logging investigation

The existing `ClaudeHandler` debug call logs the complete successfully read upstream response body before JSON parsing when `DEBUG=true`. It predates this feature (introduced in `b869cec78`, 2025-03-14; converted to `logger.LogDebug` in `0936e2504`, 2026-05-19). It writes to application debug output rather than the Root-only diagnostic endpoint, and is not governed by response-capture limits or flags. The raw-payload exclusions below describe the new diagnostic code, not removal of this legacy call. This call remains unchanged pending separate user confirmation.

## Approved final-usage and message-start repair (2026-09-10)

This repair is limited to two audited defects. A validated `message_delta` output report can precede the stop reason. It is authoritative on a valid `message_stop` if no subsequent content-block event occurred. A later content block invalidates that candidate until a newer output report arrives. Pings, metadata and stop-only deltas do not invalidate it. Explicit zero is preserved; message-start usage alone is never final. The abnormal/client-disconnect billing matrix is unchanged.

Validate message-start core structure before forwarding or adopting usage: nonblank string ID/model, type `message`, role `assistant`, and an empty content array. Streamed content is owned by the subsequent indexed content-block events, not an untracked initial content payload. Usage remains optional, unknown extension fields remain accepted, and absent usage retains existing estimation. A malformed start yields one error event with no adopted usage or effective content, selecting zero charge/release of reservation.

Regression cases precede implementation: output before/with/after stop, explicit zero, later content with/without a fresh report, initial-only usage, missing message stop; malformed/missing core fields and valid starts without usage. Exercise normal, global request-body passthrough and channel passthrough paths through the native response dispatcher; verify raw passthrough frames, usage evidence and both billing representations.

### Main Chain Impact — final-usage and message-start repair

Synchronous work is a few bounded schema checks per message start and one request-local boolean update per content/usage event. No new I/O, DB/Redis calls, locks, pools, goroutines or shared mutable resources. Existing strict-processing feature flag, reader, capture and asynchronous settlement/logging paths remain unchanged. At 30k/100k RPM, incremental shared-resource operations remain zero; no accumulated content or per-event memory structure is added. Live load and billing-backend verification remain rollout checks, not part of the local fixture tests.

## Approved follow-up repair (2026-09-10)

The follow-up approval covers four regressions: required schema validation for known SSE events; field/phase-aware output estimation on normal completion only; response-only diagnostics across native non-streaming/non-200/legacy and Bedrock SDK HTTP paths; and restoration of the policy-stop marker as Root-only evidence. No historical rebilling or pricing changes.

Known business events with empty data or missing required fields fail before usage adoption or forwarding. Comment/keepalive frames and unknown extensions remain compatible. Upstream error frames remain byte-exact. Normal completion honors explicit terminal output zero; missing terminal output uses the greater of confirmed cumulative output and the estimate of delivered content. Preserve confirmed input/cache fields and record mixed provenance without modifying raw evidence. Abnormal/client-disconnect settlement keeps the existing six-row contract unchanged.

Capture is independent of strict parsing (`claude_stream_setting.capture_response`, default true). Each relay attempt owns a bounded capture group and a public numeric attempt selector; SDK HTTP retries retain at most four response snapshots, with an omission count. Each response has at most 2 KiB body and 16 KiB headers. Wrappers observe reads, never drain for diagnostics, and never observe request headers/body. Snapshotting uses request-local short locks, never locks across network reads or body close. Root lookup uses request ID, timestamp and attempt selector to avoid selecting a different retry. Standard log reads remove both the private envelope and legacy policy reasons.

### Main Chain Impact — follow-up

Synchronous: bounded copies, protocol checks, usage provenance and snapshots. No new DB/Redis calls or per-request logging goroutines. Existing async log/accounting queues and HTTP pools are reused; HTTP clients are wrapped, not reconfigured globally. Capture state is isolated per attempt, with no cross-request locks. Normal-output estimation uses the existing bounded content counter and does not reread the prompt when input is confirmed. At 100k RPM the incremental database/Redis call count remains zero. SDK retry diagnostics have a four-response bound (at most ~72 KiB plus metadata); ordinary native calls retain one response. Validate both 30k and 100k RPM error storms and queue pressure before rollout.

Regression tests precede code changes: malformed known events, missing delta subtype, normal missing output vs terminal zero and start-only zero, mixed usage/caches, all existing abnormal billing cases, response read/EOF/header/body limits, retry isolation, feature switches, SDK pre-decoding capture, Root access and list/export filtering. Frontend changes need seven-language translations and type/lint checks.

## Goals and scope

Native Claude Messages SSE must never manufacture a successful `end_turn`, block stop or message stop after a truncated response. Upstream EOF without a complete message, invalid event JSON and read failures produce one Anthropic `error` event. Native upstream error frames retain their original bytes. Client cancellation stops upstream reading immediately. Transport failure before receiving an HTTP response uses the same terminal path, with an empty response snapshot. Non-200 HTTP responses, converted OpenAI streams and Bedrock decoding retain their existing paths; shared finalization no longer fabricates Claude success events.

## Data flow and lifecycle

Response headers and body are observed at the HTTP client response boundary, before SSE extraction, JSON decoding and usage patching. A single response-processing owner validates events, updates cumulative usage evidence and tracks successful downstream writes. A pooled reader has a bounded handoff and is joined before request context reuse. The terminal owner closes upstream, chooses usage once, settles once, and enqueues one usage/error record through the existing log pipeline. No retry or controller JSON append follows a handled SSE response.

Effective content means non-whitespace text/thinking or a complete tool invocation with valid arguments. Pings, signatures, empty blocks, metadata, usage and stop/error events do not qualify. This measures a completed server write/flush, not application consumption. Unknown extension events are tolerated. Tool argument fragments are not parsed as complete JSON until block completion. Multiple message deltas are accepted, including cumulative usage before or after a stop reason; conflicting stop reasons fail. `usage_final` requires a validated message-delta output report, no later content-block events, and a valid upstream message stop. Message-start usage alone does not qualify.

## Billing contract

| Termination | Upstream evidence | Effective downstream content | Settlement |
|---|---|---|---|
| Client cancellation | Present | Any | Reported portion |
| Client cancellation | Absent | Any | Zero; release reservation |
| Upstream failure | Present | Yes | Reported portion |
| Upstream failure | Present | No | Zero; release reservation |
| Upstream failure | Absent | Yes | Estimated input and successfully written output |
| Upstream failure | Absent | No | Zero; release reservation |

Normal completion honors final reported output including explicit zero. When some upstream evidence exists but final output is missing, output is the greater of confirmed cumulative output and estimated delivered output, with `usage_source=mixed`. Confirmed input/cache fields remain unchanged; cache-only evidence does not trigger a full prompt estimate that could double-count cached input. Completely absent usage retains the existing local estimate policy. Presence is distinct from explicit zero and message-start zero is not a final report. Cumulative reports replace supplied fields rather than being summed; absent fields retain earlier reports. Estimates never invent cache usage and must update both legacy Usage and BillingUsage. Prices remain the configured model/group/tier expressions. No historical rebilling. Settlement failure is distinct from stream failure and from a completed charge. Non-idempotent balance operations are never automatically repeated after an uncertain outcome.

Normal completed empty messages retain input estimation when usage is absent; signature-only normal completion still honors reported usage. The no-effective-content waiver applies to upstream failure, not normal completion or confirmed usage after client cancellation. Estimates read the existing outbound BodyStorage after upstream close (no diagnostic request copy), include existing fixed media estimates without fetching URLs, and count successfully written content in bounded 8 KiB batches. If outbound storage is absent or unreadable, the existing request estimate is the fallback. This remains an estimate, not a reconstruction of unreported thinking/signature tokens. Cache-only usage is billable. Funding settlement and token adjustment are distinguished as settled/released/failed/partial; ambiguous failures require operator review, not automatic repeated arithmetic.

## Diagnostic payload and API contracts

Only upstream response headers/body are captured; no request headers/body or downstream request headers. Native `/messages` bypasses the old downstream request/response capture middleware. Diagnostic body retains all bytes through 2048 bytes, otherwise first/last 1024 bytes; base64 preserves byte boundaries. Observed length, omission count and EOF observation distinguish a read prefix from a fully read body. Headers preserve multi-values, subject to a separate 16 KiB bound. Raw means HTTP-client entity bytes, not TCP/TLS wire representation.

The retained response bytes, low-level error, policy-stop reason and usage evidence are private to Root. General `other` includes only safe terminal/billing summaries and `claude_diagnostic_attempt`. A dedicated RootAuth GET `/api/log/claude-diagnostic?request_id=...&created_at=...&attempt=...` selects a bounded set of at most 128 same-request/time rows and matches the attempt (zero for historical records). It returns `success/data`, uses `Cache-Control: no-store`, and audits access without payload. List, token, employee and export queries strip the private envelope and historical `reject_reason` via the Log read hook. Root can retrieve historical policy-only records through the same endpoint. Existing request-log raw-detail access is restricted to Root. UI loads diagnostics on demand with an account/attempt-scoped query key, zero cache retention after unmount, and no persisted diagnostic cache.

Capture begins in the shared HTTP client response wrapper, before HTTP status handling and SDK decoding. It is active for `/messages` native, converted and Bedrock HTTP paths without enrolling those legacy paths in strict billing. SDK binary EventStream bytes are retained as bytes/base64, not reserialized Claude JSON. SDK retries retain up to four individual responses; earlier omitted exchanges are counted. Each response has its own body/header/EOF/read-error state, while the enclosing relay attempt owns terminal errors and usage provenance. A failed connection is a zero-status, empty-body exchange; a later successful SDK retry does not inherit that failure as its terminal error. Low-level cause strings have an 8 KiB head/tail limit, in addition to the response body/header limits. Response read errors and final parser errors are separate private fields. Capture never drains an unread body for logging.

## Data model and indexes

Reuse `logs.other` and existing bounded asynchronous consume/error log pipeline. No new table, migration, full scan or historical rewrite. Root lookup selects `other` only, with exact request_id and created_at, using existing request_id index (SQL) and created_at/request_id ordering (ClickHouse). Retention follows existing logs; raw diagnostic data must not be included in ordinary exports or console logging.

## Configuration

`claude_stream_setting.enabled` and `claude_stream_setting.capture_response` default true, registered through setting/config immutable snapshots and managed by the existing settings API. The switches are independent. Disabling strict processing restores the legacy parser but never synthetic success closers; response capture remains available. Disabling response capture removes raw response collection, not strict error/usage evidence. Configuration is frozen per request, including retries. Existing ping/timeout settings remain honored. No new settings page is introduced.

## Main Chain Impact

Synchronous: bounded capture, SSE framing, event validation, write-result tracking and terminal billing/estimation. The no-usage estimate path rereads the existing outbound storage after response termination; disk-backed BodyStorage adds local read work on this terminal path, not per-event I/O or diagnostic persistence. No new DB/Redis lookup or per-event settlement. Persistence/accounting use existing asynchronous queues. Non-critical diagnostics do not issue external calls. Raw payloads are excluded from ordinary logger calls.

## Shared Resource Audit

Existing balances/tokens/subscriptions are accessed only by the existing settlement operation, not a second billing pass. Existing log DB/pipeline is reused; each captured response is bounded by 2 KiB body plus 16 KiB headers and 8 KiB read-error text. Native requests normally have one response; SDK retries retain at most four, with an additional 8 KiB terminal cause and small usage evidence. No new Redis keys or cross-request mutable map. Capture snapshot locks are request-local, never held across network reads or close. Reader uses the existing relay pool with a bounded handoff; writes remain single-owner. All request state is released on termination.

## Concurrency Analysis

At 100k RPM (~1667 requests/s): zero added synchronous DB/Redis queries; one pooled reader per active strict stream replacing the legacy scanner/data/ping workers. No cross-request locks. At 50k streams the body capture budget alone is ~98 MiB; header snapshots and event buffers add overhead (the 16 KiB header cap could add ~781 MiB at this concurrency). Raw bodies are not accumulated. Individual SSE frames and assembled tool arguments are capped at 8 MiB; usage fields at 1 billion tokens. The frame handoff has capacity one; incremental splitter offsets avoid quadratic scanning on short reads. These are per-stream bounds, not an aggregate memory guarantee. Existing settlement DB/Redis work is unchanged; load and error-storm validation in staging remains necessary before broad deployment.

## Tests, audit and rollout

Test-first fixtures cover complete/incomplete streams, ping before start, signature-only blocks, cumulative and zero usage, raw upstream errors, read/JSON errors, client cancellation, failed writes, six billing rows, cache-only usage, duplicate settlement prevention, capture boundaries and Root isolation. Controller retry/JSON/refund guards were audited along the call chain, rather than exercising the complete relay hot path end-to-end. Run affected Go tests, frontend typecheck/lint and independent relaykit build if affected. Audit controller defers, settlement side effects, exports and request logging after implementation. No real AI calls. Record actual verification and remaining operational limits below when complete.

## Implementation audit and verification (2026-09-10)

Subscription log snapshot repair completed:

- New test-first cases reproduced the stale pre-settlement subscription fields. The strict terminal helper now refreshes the existing log map after settlement, before the error/consume logging branches; no second balance operation occurs. The generic serializer and other relay paths remain unchanged.
- A reservation of 100 followed by a full refund now retains pre-consumed 100, records post-delta -100 and final consumed 0, and updates used/remaining from 300/700 to 200/800 for a total of 1000. Partial refunds, supplementary charges, zero deltas/reservations, exhausted balances and failed/partial commits use the recorded funding result. Existing unrelated log metadata and wallet paths are preserved. Historical stored rows are not rewritten.
- New subscription cases plus existing terminal-settlement, cache-only and generic subscription-log tests passed 20 consecutive runs. All service Claude fixture tests passed. The standard service test command stops during bootstrap because configured MySQL at 127.0.0.1:3306 is offline; fixture runs used a temporary Go overlay to bypass only that bootstrap, without modifying the repository harness or simulating GORM. Real BillingSession funding arithmetic was exercised against an in-memory FundingSource; token-adjustment failures were modeled at the billing-session interface. No production subscription/token records were touched.
- Full affected relay/common, Claude, AWS, shared channel and relay/helper suites, root `go build ./...`, formatting and `git diff --check` passed. Re-audit verified refresh precedes both log destinations, duplicate settlement still returns before any second update, and no new shared resource or I/O was introduced. Production billing-backend and load verification remain rollout checks.
- The legacy non-streaming complete-body debug log was investigated through current call paths and git history only. Its code and runtime debug configuration remain unchanged pending user confirmation.

Final-usage and message-start repair completed:

- Test-first regressions reproduced the premature estimation of an output report preceding stop, and acceptance of malformed message starts. Final usage now follows content/report ordering rather than the presence of a stop reason in the same or earlier delta. New content invalidates an older report; a fresh report restores finality. Usage and BillingUsage agree, including explicit zero.
- Core start checks execute before protocol advancement, usage adoption or downstream writes. Missing/invalid identity, envelope or initial content produces one error and selects no charge. Valid starts without usage and with future extension fields remain supported. An initial content array must be empty; content is accounted through indexed block events.
- 104 new table/HTTP fixture cases cover normal/global/channel passthrough and both defects; the three new test groups passed 20 consecutive runs. Eight affected package suites passed again (relay/common, Claude, AWS, shared channel, relay/helper, operation settings, i18n, router), along with `go build ./...`, formatting and `git diff --check`.
- Local HTTP adapter fixtures verify byte-exact outbound passthrough including unknown fields/whitespace, corrected final output, malformed-start no-charge selection, byte-exact native upstream errors, and response-only diagnostics. The actual request-body passthrough branch was rechecked to reach the same native response dispatcher. No production billing backend, full relay end-to-end load test, deployment or historical charge adjustment was performed. Existing abnormal/client-loss billing fixtures still pass; no settlement, storage, configuration or frontend code changed in this repair.

Follow-up fixes completed and re-audited:

- Regression fixtures first reproduced empty known-event data, missing delta subtype, missing normal output usage and the missing policy marker. Known block text/thinking/signature field types are now checked before accepting content. Unknown extensions/comments remain compatible.
- Normal partial usage records `usage_phases` and `estimated_usage` separately from untouched upstream evidence. Explicit terminal zero remains zero; initial zero can be supplemented only on normal completion. Existing client-loss and abnormal billing fixtures remain unchanged and pass.
- Shared response capture now covers HTTP 200/non-200/non-streaming and strict-off paths; AWS fixtures verify exact binary EventStream bytes before SDK decoding. Reader close is idempotent, snapshots are detached, limits and concurrent reads/snapshots are tested. Follow-up tests caught and corrected SDK transport-retry failure leaking into the successful response's terminal cause, and excluded the internal transport-error reader from upstream response history.
- Root selection includes the attempt, including same-request/same-second retries. Root/user/admin endpoint fixtures, invalid selectors and historical policy filtering pass. Private policy reasons no longer live in ordinary `other.reject_reason`. Legacy Claude stream error details are kept out of public stream-status fields. Console/error-log summaries do not include raw upstream causes on the native Messages path.
- Affected package suites passed again: relay/common, Claude, AWS, shared channel, relay/helper, operation settings, i18n, router. New service/model/controller/middleware regression tests and existing text quota, cache, BillingUsage, tool surcharge and tier token normalization tests passed using temporary Go overlays for the unavailable DB bootstrap, with actual SQLite/GORM fixtures where a database was needed. Production MySQL at localhost:3306 remained offline; the standard DB-backed test invocation fails during bootstrap, not in the new test assertions. No repository test harness was changed.
- Frontend typecheck, scoped lint, production build and root Go build passed. All new mixed-usage/retry labels are translated in seven locales. CGO is disabled, so race-enabled testing remains a separate verification requirement. No production load run, service restart or deployment was performed.

Audited and corrected:

- Removed synthetic native success closure; one stream owner now selects the terminal cause before cleanup cancellation.
- Controller retry, generic JSON and refund defers exit after a handled strict stream. Settlement has a one-attempt guard and records incomplete funding/token commits separately.
- Upstream reader is closed/joined before terminal error writes; downstream slowness does not keep upstream generation alive. Managed relay timeout permits only the terminal error write.
- Flush failures are detected underneath Gin wrappers; a buffered/failed write is not effective delivered content.
- Explicit zero, cumulative replacement, cache-only billing, and synchronized Usage/BillingUsage avoid stale zero output or accidental cache waivers.
- No-charge policy overrides flat/tier/tool fees before constructing displayed charges. Strict zero-charge logs use the policy summary instead of falsely claiming upstream usage was absent.
- Repeated valid message deltas and normal empty-message estimation have regression fixtures.
- Root backend authorization, list/export filtering and old raw request-log detail authorization were checked. Same-second retry rows do not hide the diagnostic. All new UI keys exist in seven locales.

Verification results:

- Passed affected package suites: Claude, AWS, shared channel, relay/common, relay/helper, operation settings, i18n and router.
- Passed new service/model/controller/middleware fixtures and existing text/cache/tool/tier billing and settlement-snapshot unit tests. The configured local MySQL service was offline; these were run with a temporary Go test overlay that bypasses only TestMain environment bootstrap, using real SQLite/GORM for database/auth fixtures. No repository harness changes or real upstream calls.
- Passed `go build ./...`, frontend typecheck, production build, scoped frontend lint, locale integrity and `git diff --check`.
- Full DB-backed suite remains unverified against MySQL/PostgreSQL/ClickHouse. Race testing requires a CGO-capable C compiler; this host has CGO disabled and no C compiler. Full frontend lint has existing findings outside the changed files; scoped lint is clean. No relaykit source changed.

Operational limits and rollout:

- Keep the existing asynchronous relay-log pipeline enabled; charged request diagnostics also follow the consume-log switch. Queue saturation, shutdown or log backend failures retain the existing non-blocking/durable-spool behavior, not a synchronous persistence guarantee.
- Raw response headers/body are sensitive Root data and intentionally unredacted. Storage/backups still require restricted access; Root-only UI access is not a substitute for storage access control. No historical request logs are deleted.
- Error events can only be attempted on a writable downstream connection; client loss may prevent delivery. Successful writes do not prove the application consumed the content.
- Existing channel health/auto-disable processing is skipped for handled strict stream terminals, avoiding retries and duplicate terminal side effects; failed stream performance samples still report failure.
- Perform staging validation for real client SDKs, proxy timeouts, HTTP/2 disconnects, billing backend failures, and 30k/100k RPM memory/error-storm behavior before deployment. Changes are local only; no service restart or deployment was performed.
