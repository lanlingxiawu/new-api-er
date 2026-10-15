# Upstream main integration: 45fbb149f

## Scope

Merge QuantumNous/new-api `main` at `45fbb149f` into `zhuzhan`, starting at
`716c9f270`. This is an upstream synchronization, retaining the fork's employee
accounting, scoped administration, asynchronous log pipeline, stream settlement,
quota saturation protection, probe routing and per-group retry overrides.

The five upstream-owned frontend authentication modules are taken verbatim from
the upstream commit. Ali image generation now uses the upstream Alibaba task
plugin; the retired Go image adaptor and its direct-adaptor tests are removed.
Old utility tests are migrated to their replacement interfaces or removed when
the utility itself no longer exists. Upstream tests that globally truncate shared
tables are excluded; the fork's row-scoped database harness remains authoritative.

## Main Chain Impact

Channel constraints and request policy decisions execute synchronously from the
request context and the published policy snapshot. Probe routing still skips
affinity and uses the existing bounded selection path. Group retry overrides
remain effective. No new database lookup is introduced by these merge adapters.

The existing stream settlement coordinator remains the owner of terminal billing,
including timeout input-only billing and exactly-once finalization. Structured
upstream log messages are passed alongside fork settlement notes. Consumption
logs and cost/commission records continue through the existing asynchronous
pipeline. Performance collection follows upstream's terminal-result API.

The extracted `tokenkit` keeps upstream model classification while retaining
bounded BPE segments and the fork's 8 MiB exact-count ceiling. Its 1024-byte run
bound matches upstream accuracy tests; oversized input is extrapolated after the
bounded prefix. This avoids an unbounded tokenizer invocation on hostile text.

## Shared Resource Audit

* User/token/channel quota rows remain shared with relay billing. Merge adapters
  retain existing settlement ownership and asynchronous accounting; database
  compatibility requires the integration tests described below.
* Request policy snapshots are read in memory and replaced on administrative
  writes. Option writes are serialized outside the relay path. No distributed
  coordination lock is added to request processing.
* Affinity caches retain their existing namespaces and capacity controls. Probe
  requests do not acquire affinity entries.
* Static asset throttling uses the separate `GS` namespace; dashboard throttling
  continues to use the fork's runtime settings and Redis-degradation behavior.
* Log exports use the upstream export lock plus the fork's existing export paths.
  These are administrative/user management operations, not relay middleware.
* Existing database/Redis pools and bounded accounting queues are retained. The
  merge does not provision another pool or reserve a connection per AI stream.

## Concurrency Analysis

At 30,000 RPM (500 requests/s) and 100,000 RPM (about 1,667 requests/s), merge
compatibility logic adds no per-request DB operation, distributed lock or worker
creation. Policy decisions add bounded per-attempt bookkeeping; existing retry
budgets still bound attempts. Settlement and accounting retain their original
queue/backpressure behavior. Tokenization remains proportional to a bounded
prefix rather than arbitrarily large request bodies. These are code-level
observations, not a claim that a 100,000 RPM load test was performed.

## Administrative contracts and authentication

`GET/PATCH /api/option/request_policy` remain under dashboard authentication and
system settings authorization. New routing, health and filtering sections have
separate resources. Handlers use the scope recorded by middleware and enforce
the option allowlist; a body cannot widen it. Reads filter the returned snapshot.
The affinity cache controls use the routing section's permission.

Both XLSX quick export and upstream CSV streaming export are available. Their
toolbar labels distinguish the formats. User exports remain user-scoped.

Fork-only dashboard endpoints are explicitly session-only in the personal access
token route table until dedicated PAT scopes are defined. Existing role and
Casbin checks still apply. Public payment callbacks and the public exchange-rate
endpoint remain explicit authentication exemptions. The routing table tests
cover every dashboard route and reject missing or duplicate declarations.

Upstream input-only pre-consumption and its configurable multiplier are retained;
output limits no longer inflate the initial reservation. Final settlement keeps
the fork's overflow checks and asynchronous accounting. xAI cascades continue
to expose gateway content URLs and authenticate downloads to the gateway origin;
provider CDN downloads remain credentialless. Channel IDs 58–64 retain the
fork's persisted numbering.

Authentication review references:

* [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
* [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
* [OWASP ASVS](https://owasp.org/projects/asvs)

Server-side scope checks, bearer authentication, refresh rotation and sensitive
action verification must remain covered by the authentication regression suites.
This merge does not claim a complete ASVS assessment.

## Validation

Validated with Go 1.26.0 and Bun 1.3.13 on Windows:

* `go build -p 2 ./...`: passed.
* Root Go packages excluding the three database-harness packages
  (`controller`, `model`, `service`), using `go test -p 2`: passed. This includes
  relay/provider fixtures, middleware, plugins, router, authorization and settings.
* `cd relaykit; GOWORK=off go test -p 2 ./...`: passed; independent build passed too.
* `cd tokenkit; go test ./...`: passed, including bounded-tokenizer regression cases.
* Frontend full run (`bun run test -- --maxWorkers=2`): 196 files / 2393 tests
  initially passed. All 12 failing files were rechecked after adapting the
  fixtures and fixing regressions; the remaining focused rechecks passed. These
  cover channel configuration/Vertex, policy/settings saves, exports, permission
  verification, menu authorization and wallet translations.
* `bun run typecheck`, `bun run lint`, `bun run lint:plugins`, `bun run build`:
  passed. Existing non-fatal lint warnings remain.
* Five authentication plumbing files are byte-identical to upstream `45fbb149f`.
  Adapted Go upstream test copies use `_upstream_cases_test.go` names.

The full root `go test -p 2 ./...` was attempted. `SQL_DSN` is absent; service's
TestMain requires it and some inherited controller/model integration cases
dereference the unavailable DB instead of skipping. These three complete suites
are therefore **not validated**. Available isolated SQLite fixtures ran in the
other packages; they do not substitute for MySQL/PostgreSQL migration coverage.
Docker CLI is installed but its daemon is unavailable. No real MySQL/PostgreSQL
version, fresh/upgrade migration rehearsal, second-start DDL check, server-schema
rehearsal or load test can be reported. Run that database matrix before deployment.
