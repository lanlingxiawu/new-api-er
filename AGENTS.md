# CLAUDE.md — Project Conventions for new-api

## Overview

This is an AI API gateway/proxy built with Go. It aggregates 40+ upstream AI providers (OpenAI, Claude, Gemini, Azure, AWS Bedrock, etc.) behind a unified API, with user management, billing, rate limiting, and an admin dashboard.

## Tech Stack

- **Backend**: Go 1.22+, Gin web framework, GORM v2 ORM
- **Frontend**: React 19, TypeScript, Rsbuild, Base UI, Tailwind CSS
- **Databases**: SQLite, MySQL, PostgreSQL (all three must be supported)
- **Cache**: Redis (go-redis) + in-memory cache
- **Auth**: JWT, WebAuthn/Passkeys, OAuth (GitHub, Discord, OIDC, etc.)
- **Frontend package manager**: Bun (preferred over npm/yarn/pnpm)

## Architecture

Layered architecture: Router -> Controller -> Service -> Model

```
router/        — HTTP routing (API, relay, dashboard, web)
controller/    — Request handlers
service/       — Business logic
model/         — Data models and DB access (GORM)
relay/         — AI API relay/proxy with provider adapters
  relay/channel/ — Provider-specific adapters (openai/, claude/, gemini/, aws/, etc.)
middleware/    — Auth, rate limiting, CORS, logging, distribution
setting/       — Configuration management (ratio, model, operation, system, performance)
common/        — Shared utilities (JSON, crypto, Redis, env, rate-limit, etc.)
dto/           — Data transfer objects (request/response structs)
constant/      — Constants (API types, channel types, context keys)
types/         — Type definitions (relay formats, file sources, errors)
i18n/          — Backend internationalization (go-i18n, en/zh)
oauth/         — OAuth provider implementations
pkg/           — Internal packages (cachex, ionet)
web/             — Frontend themes container
 web/default/   — Default frontend (React 19, TypeScript, Rsbuild, Base UI, Tailwind)
 web/classic/   — Classic frontend (React 19, JavaScript, Rsbuild, Semi Design)
 web/default/src/i18n/ — Default frontend i18n (i18next, en/zh/fr/ru/ja/vi)
 web/classic/src/i18n/ — Classic frontend i18n (i18next, en/zh-CN/zh-TW/fr/ru/ja/vi)
```

## Internationalization (i18n)

### Backend (`i18n/`)
- Library: `nicksnyder/go-i18n/v2`
- Languages: en, zh

### Default Frontend (`web/default/src/i18n/`)
- Library: `i18next` + `react-i18next` + `i18next-browser-languagedetector`
- Languages: en (base), zh (fallback), fr, ru, ja, vi
- Translation files: `web/default/src/i18n/locales/{lang}.json` — flat JSON, keys are English source strings
- Usage: `useTranslation()` hook, call `t('English key')` in components
- CLI tools: `bun run i18n:sync` (from `web/default/`)

### Classic Frontend (`web/classic/src/i18n/`)
- Library: `i18next` + `react-i18next` + `i18next-browser-languagedetector`
- Languages: en, zh-CN, zh-TW, fr, ru, ja, vi
- Translation files: `web/classic/src/i18n/locales/{lang}.json` — flat JSON, same key pattern
- Usage: `useTranslation()` hook, call `t('English key')` in components
- CLI tools: `bun run i18n:sync` (from `web/classic/`)

## Rules

### Rule 0: Main Relay Chain — Absolute Non-Interference

**Any code change — new feature or modification to existing functionality — MUST NOT affect the execution of the main AI relay chain.**

The main relay chain is the critical path: client request → middleware → relay handler → upstream AI provider → response streaming → client. It must remain correct, low-latency, and available at all times.

**Concrete requirements:**

- **No blocking calls injected into relay.** New features that need to observe relay events (logging, stats, billing hooks) MUST operate asynchronously — fire-and-forget via goroutine/channel, never inline on the relay goroutine.
- **New middleware must be non-blocking.** Any middleware added to the relay route must complete in sub-millisecond time for the happy path. Heavy logic (DB queries, external calls) belongs in a goroutine or background worker, not in middleware.
- **Failures in new features must be silent.** If a new subsystem errors, panics, or times out, it must recover internally and log the error without propagating to the relay response. Use `recover()` in goroutines, timeouts on all external calls.
- **No new synchronous DB reads added to the relay hot path.** If relay needs data from a new feature's table, it must be served from cache; the DB is only a cache-miss fallback with a strict timeout.
- **Feature flags for relay-adjacent changes.** Any change that touches relay execution flow must be gated behind a config/feature flag so it can be disabled instantly if it causes issues.
- **Verify with load context.** Before merging any change that touches `relay/` or its middleware, reason explicitly about its behavior at 30k+ RPM. Note this reasoning in the PR or design document.

**Shared resource conflict check — mandatory for ALL code changes (new features and modifications):**

Whenever code introduces or modifies usage of any shared resource — Redis keys/namespaces, DB tables/rows, in-memory caches, connection pools, goroutine pools, file handles — you MUST check whether the main relay chain accesses the same resource:

- **Redis**: Search for any relay-path code that reads/writes the same key prefix or namespace. Conflicts can cause cache poisoning, unexpected eviction, or key collisions. Code must use a distinct key namespace from relay.
- **DB tables**: If the changed code reads or writes a table also accessed on the relay path (e.g., `tokens`, `channels`, `users`), verify the queries cannot cause lock contention, slow the relay's reads, or bloat the table scan. Add indexes proactively.
- **In-memory caches / maps**: Shared in-memory structures must be protected by appropriate synchronization and must not cause GC pressure that affects relay latency.
- **Connection pools**: Code that opens additional DB or Redis connections must account for pool exhaustion under relay load. Do not assume connections are free.
- **Goroutine / worker pools**: Shared pools must have capacity reserved for relay traffic. Changes must not starve relay workers.

When a conflict is found, resolve it by isolation (separate key namespace, separate table, separate pool) — not by coordination locks on the hot path.

**Design document requirement (Rule 8):** For any feature that touches or hooks into the relay chain, the design document must include a section titled **"Main Chain Impact"** that explicitly states:
1. What executes synchronously on the relay goroutine vs. what is deferred/async.
2. Every shared resource (Redis key, DB table, memory structure, pool) the feature accesses, and whether the relay chain touches the same resource.

### Rule 1: JSON Package — Use `common/json.go`

All JSON marshal/unmarshal operations MUST use the wrapper functions in `common/json.go`:

- `common.Marshal(v any) ([]byte, error)`
- `common.Unmarshal(data []byte, v any) error`
- `common.UnmarshalJsonStr(data string, v any) error`
- `common.DecodeJson(reader io.Reader, v any) error`
- `common.GetJsonType(data json.RawMessage) string`

Do NOT directly import or call `encoding/json` in business code. These wrappers exist for consistency and future extensibility (e.g., swapping to a faster JSON library).

Note: `json.RawMessage`, `json.Number`, and other type definitions from `encoding/json` may still be referenced as types, but actual marshal/unmarshal calls must go through `common.*`.

### Rule 2: Database Compatibility — SQLite, MySQL >= 5.7.8, PostgreSQL >= 9.6

All database code MUST be fully compatible with all three databases simultaneously.

**Use GORM abstractions:**
- Prefer GORM methods (`Create`, `Find`, `Where`, `Updates`, etc.) over raw SQL.
- Let GORM handle primary key generation — do not use `AUTO_INCREMENT` or `SERIAL` directly.

**When raw SQL is unavoidable:**
- Column quoting differs: PostgreSQL uses `"column"`, MySQL/SQLite uses `` `column` ``.
- Use `commonGroupCol`, `commonKeyCol` variables from `model/main.go` for reserved-word columns like `group` and `key`.
- Boolean values differ: PostgreSQL uses `true`/`false`, MySQL/SQLite uses `1`/`0`. Use `commonTrueVal`/`commonFalseVal`.
- Use `common.UsingPostgreSQL`, `common.UsingSQLite`, `common.UsingMySQL` flags to branch DB-specific logic.

**Forbidden without cross-DB fallback:**
- MySQL-only functions (e.g., `GROUP_CONCAT` without PostgreSQL `STRING_AGG` equivalent)
- PostgreSQL-only operators (e.g., `@>`, `?`, `JSONB` operators)
- `ALTER COLUMN` in SQLite (unsupported — use column-add workaround)
- Database-specific column types without fallback — use `TEXT` instead of `JSONB` for JSON storage

**Migrations:**
- Ensure all migrations work on all three databases.
- For SQLite, use `ALTER TABLE ... ADD COLUMN` instead of `ALTER COLUMN` (see `model/main.go` for patterns).

**Billing safety invariants:** Quota/billing code MUST never produce a negative charge (a credit) from arithmetic overflow or unvalidated input. Apply defense in depth:

- Every user-controlled quantity that becomes a billing multiplier (image `n`, video `seconds`/`duration`, resolution/quality ratios, batch counts) MUST be bounded before it reaches quota calculation. Reject out-of-range values at request validation with a 400. Existing bounds: `dto.MaxImageN` for image generation count, `relaycommon.MaxTaskDurationSeconds` for task video duration. Reuse these constants instead of introducing new ad hoc limits for the same concepts.
- Watch for validation bypass paths: passthrough fields (e.g. `Extra["parameters"]`), task `metadata` maps, and multipart form fields can carry the same quantities around the standard DTO validation. Any adaptor that reads a multiplier from such a path must enforce the same bound (or clamp) locally.
- Never convert a computed quota to `int` with a bare cast like `int(float64(quota) * ratio)` or `int(decimal.IntPart())`. Use the saturating converters: `common.QuotaFromFloat` for float products, `decimalToQuota` in `service` for decimal products. Saturation bounds are int32 because quota columns (user/token/log) are 32-bit integers in the database.
- Multiplier maps go through `types.PriceData.AddOtherRatio`, which rejects non-positive, NaN, and +Inf ratios. Do not write to `PriceData.OtherRatios` directly, and do not weaken these guards.
- Pre-consume (预扣费) and settle (结算/差额) must both be safe: a saturated oversized quota must fail pre-consume with insufficient-quota, never silently wrap. When adding a new billing path (new relay format, new task platform, new adjustment hook), trace the full chain — validation → EstimateBilling/OtherRatios → quota conversion → pre-consume → settle/refund — and confirm each step preserves these invariants.
- Fields parsed into unsigned types (`*uint`) accept huge positive JSON numbers (e.g. `18446744073686646784`, a wrapped negative); a `>= 0` check is not sufficient, an upper bound is mandatory.
- Regression tests for these invariants belong with the boundary they protect (request validators, converter helpers). See `relay/helper/openai_image_request_test.go`, `relay/common/relay_utils_test.go`, and `common/quota_math_test.go` for the expected style.

### Rule 3: Frontend — Prefer Bun

Use `bun` as the preferred package manager and script runner for **both** frontend directories (`web/default/` and `web/classic/`):
- `bun install` for dependency installation
- `bun run dev` for development server
- `bun run build` for production build
- `bun run i18n:*` for i18n tooling

### Rule 4: New Channel StreamOptions Support

When implementing a new channel:
- Confirm whether the provider supports `StreamOptions`.
- If supported, add the channel to `streamSupportedChannels`.

### Rule 5: Upstream Relay Request DTOs — Preserve Explicit Zero Values

For request structs that are parsed from client JSON and then re-marshaled to upstream providers (especially relay/convert paths):

- Optional scalar fields MUST use pointer types with `omitempty` (e.g. `*int`, `*uint`, `*float64`, `*bool`), not non-pointer scalars.
- Semantics MUST be:
  - field absent in client JSON => `nil` => omitted on marshal;
  - field explicitly set to zero/false => non-`nil` pointer => must still be sent upstream.
- Avoid using non-pointer scalars with `omitempty` for optional request parameters, because zero values (`0`, `0.0`, `false`) will be silently dropped during marshal.

### Rule 6: Frontend Changes — Both UIs Must Be Updated

Any change to a frontend page or component MUST be applied to **both** UIs:

| UI | Directory | Stack | Port |
|---|---|---|---|
| Default | `web/default/` | React 19, TypeScript, Base UI, Tailwind CSS | 3002 |
| Classic | `web/classic/` | React 19, JavaScript (JSX), Semi Design (`@douyinfe/semi-ui`) | 5173 |

**Styling — follow the theme of each UI:**
- **Default**: use Tailwind utility classes and Base UI design tokens. Never hardcode colors — use `bg-*`, `text-*`, `border-*` classes that respond to dark/light mode.
- **Classic**: use Semi Design component props and Semi CSS variables (`var(--semi-color-*)`) for all colors. Never hardcode hex values outside of Semi's token system.
- **Reuse existing styles first**: before writing new styles for a feature, inspect the existing components in the same module or page. Reuse the same class combinations, component variants, and layout patterns already in use. Do not invent parallel styling solutions for the same UI pattern.

**i18n — add translations to both locale sets:**
- Default: `web/default/src/i18n/locales/{lang}.json` (key = English source string, `t('English key')`)
- Classic: `web/classic/src/i18n/locales/{lang}.json` (zh-CN / zh-TW are separate files; `t('English key')` same pattern)
- Sync command: `bun run i18n:sync` from within each UI directory.

**Encoding — Windows / UTF-8:**
- All source files (`.tsx`, `.jsx`, `.ts`, `.js`, `.json`) MUST be saved as **UTF-8 without BOM**.
- Never write Chinese or non-ASCII text via PowerShell `Out-File`, `Set-Content`, or `echo >` — these default to UTF-16 on Windows and will produce garbled output.
- Use the `Write` or `Edit` tools directly for any file containing Chinese text; they emit UTF-8.
- After writing locale JSON files, verify the file does not start with a BOM (`EF BB BF`) if encoding issues are suspected.

**Classic UI — reuse existing layout patterns, including button areas:**
- "Reuse existing styles first" applies equally to button areas: before writing any button layout, find the nearest similar button group in the same module and copy its structure exactly.
- For destructive actions that are minor/secondary, a labelled button is sufficient — do not add a bold section title or description paragraph above it.
- Confirmation dialogs for destructive actions use `Modal.confirm` from `@douyinfe/semi-ui` with `okType: 'danger'`.

**Interaction design — write from the user's perspective:**
- All UI interactions (loading states, error messages, empty states, confirmations, feedback) must be designed from the end user's point of view, not the implementation's point of view.
- Error messages must describe what the user can do next, not expose internal error codes or Go error strings.
- Loading and disabled states must be handled so users are never left confused about whether an action is in progress.

**When backend implementation is missing:**
- Do not silently stub or fake the backend behavior in the frontend.
- Record the gap as a comment or TODO in the design document (`docs/design/`) and confirm with the user before proceeding — the missing backend may be an intentional trade-off, a deferred scope item, or a design decision that has not been made yet.

**Dev commands (from each UI's directory):**
- `bun run dev` — start dev server
- `bun run build` — production build
- `bun run i18n:sync` — sync translations

### Rule 7: New Features — Design Document First (docs/design/)

Before writing any code for a **new feature or significant modification to existing functionality**, you MUST:

1. **Write a design document** in `docs/design/` (filename: `<feature-name>.md`).
2. **Wait for user confirmation** before writing any implementation code. Do not proceed until the user explicitly approves the design.
3. The design document MUST cover the **complete lifecycle** of the feature. Required sections:

   **All features:**
   - Goals and scope
   - Data flow (request → processing → storage → response)
   - API contracts (endpoints, request/response shapes, auth level per Rule 13)
   - Data model changes (new tables, columns, migrations; index design for any large table per Rule 9)
   - Config parameters added (if any, per Rule 14)
   - Key business logic and edge cases
   - Error handling strategy (per Rule 11)
   - Interaction with existing subsystems (billing, relay, cache, etc.)

   **If the feature touches or hooks into the AI relay chain — also required:**
   - **Main Chain Impact**: what executes synchronously on the relay goroutine vs. what is deferred/async
   - **Shared Resource Audit**: every Redis key/namespace, DB table, in-memory structure, or pool the feature accesses — and whether the relay chain touches the same resource (per Rule 0)
   - **Concurrency Analysis**: estimated DB calls, Redis calls, lock usage, and goroutine cost per request at 100k RPM (per Rule 9)

4. **After implementation**, update the design document to reflect the actual code logic (remove divergences, note implementation decisions).

**When modifying existing code:**
- Check `docs/design/` for a related design document and use it as context.
- **Code logic takes priority over documentation** — if the doc and the code conflict, trust the code; the doc may be stale.
- Update the relevant doc if you discover a material discrepancy.

### Rule 8: High-Concurrency and Large-Data Design Constraints

#### 8.1 Scope — Which paths require high-concurrency design

**Only the AI relay chain requires strict high-concurrency design.** This includes:
- `relay/` — request routing, upstream dispatch, response streaming
- Billing / quota pre-consume and settlement on the relay path
- Token/rate-limit checks on the relay path
- Any middleware that executes on every AI request

Admin dashboards, settings pages, management APIs, reporting, and other non-relay paths do **not** need to meet the same bar — use straightforward DB queries and normal synchronous logic there.

The system currently handles **30,000 RPM** on the AI relay chain; design must sustain **100,000+ RPM** on that chain without architectural changes.

#### 8.2 AI relay chain — mandatory principles

- **No per-request DB writes on the hot path.** Writes (billing, logging, stats) must be batched, queued asynchronously, or written to Redis first and flushed to DB in background workers.
- **Read from cache first.** Any data read on every relay request must be served from Redis or in-memory cache (`common/` cache helpers, `pkg/cachex`). Cache TTL must be explicit and justified.
- **No distributed locks on the hot path.** If a lock is unavoidable, scope it narrowly with a short timeout. Never hold a lock across an upstream AI call.
- **Goroutine pools, not unbounded goroutines.** Do not spawn a `go func()` per request; use a worker pool or channel-based dispatcher to bound concurrency.
- **Stateless request handling.** Relay handlers must not accumulate per-request state in memory. All shared state belongs in Redis.
- **Graceful degradation.** If a non-critical subsystem (stats, ledger, notification) fails or is slow, it must not block or error the primary relay response.

#### 8.3 Large-data considerations — applies system-wide

The system accumulates large volumes of data (request logs, billing records, usage stats, etc.). For any table that grows continuously:

- **Indexes before queries.** Every `WHERE`, `ORDER BY`, and `JOIN` column on a large table must have a covering index. Propose indexes in the design document and add them in the migration.
- **Avoid `SELECT *` and full-table scans.** Use explicit column lists and always verify `EXPLAIN` output for large-table queries during design.
- **Pagination over `OFFSET`.** For list APIs on large tables, prefer cursor/keyset pagination (`WHERE id < last_id`) over `LIMIT n OFFSET m` — offset scans become expensive as data grows.
- **Time-range partitioning awareness.** Queries on log/stat tables must always filter by a time range so the query hits only recent data. Do not query unbounded time ranges.
- **Aggregation in background.** Pre-aggregate stats into summary tables via scheduled jobs; do not run heavy `GROUP BY` aggregations in real-time API responses.
- **Soft-delete with index.** If using soft delete (`deleted_at`), include `deleted_at` in composite indexes to avoid scanning deleted rows.

**Design document requirement (Rule 8):** For features touching the AI relay chain, include a **concurrency analysis** (DB/Redis calls per request, lock usage, goroutine cost at 100k RPM). For features adding new large tables or queries, include an **index design** section listing all proposed indexes and access patterns.

### Rule 9: Error Handling Conventions

**Controller layer** — always use the helpers in `common/gin.go`, never write raw JSON error responses:

- `ApiError(c, err)` — business error, wraps `err.Error()` as message
- `ApiErrorMsg(c, msg)` — business error with a plain string message
- `ApiErrorI18n(c, msgKey)` — business error with an i18n message key (preferred for user-facing errors)

All three return **HTTP 200** with body `{"success": false, "message": "..."}`. Do not invent alternative response shapes.

**HTTP status codes that differ from 200:**
- `401` — unauthenticated (handled by auth middleware; do not return manually unless in auth middleware)
- `403` — forbidden (role insufficient)
- `500` — unrecoverable server error (DB failure, panic recovery)

**Rules:**
- Never return raw Go error strings to the client — they may expose internals. Wrap with a user-readable message.
- Relay chain errors do NOT use `ApiError`; they follow the relay error path in `controller/relay.go`. Do not mix the two.
- Always log the underlying error before returning a sanitized message to the client.
- Do not swallow errors silently in non-relay paths — at minimum call `logger.LogError`.

### Rule 10: Logging Conventions

Use the `logger` package (`logger/logger.go`). All log calls MUST pass the request context as the first argument so the requestId is captured:

```go
logger.LogInfo(c, "message")
logger.LogWarn(c, "message")
logger.LogError(c, "message")
logger.LogDebug(c, "message", args...)  // only emits when common.DebugEnabled = true
```

**Level guidelines:**

| Level | Use for |
|---|---|
| `LogDebug` | Development/diagnostic detail; never enabled in production by default |
| `LogInfo` | Key business flow events (model selected, quota skipped, cache hit) |
| `LogWarn` | Recoverable anomalies (rate limit approaching, retry triggered, fallback used) |
| `LogError` | Failures requiring attention (relay error, DB failure, external call failure) |

**System-level (non-request-scoped) events** — startup, config load, background worker errors:
Use `common.SysLog(msg)` / `common.SysError(msg)` instead of `logger.Log*`.

**Forbidden:**
- Do NOT log API keys, bearer tokens, user passwords, or any credential material.
- Do NOT use `fmt.Println` or `log.Printf` directly in business code — use the logger package.

### Rule 11: New API Endpoint — Auth and Routing

Every new endpoint MUST be placed in the correct router group in `router/api-router.go` with the appropriate middleware. Never add an endpoint to the wrong group or omit authentication.

**Auth middleware reference (`middleware/auth.go`):**

| Middleware | Required role | Use for |
|---|---|---|
| `middleware.AdminAuth()` | `>= RoleAdminUser` | Admin management APIs |
| `middleware.UserAuth()` | `>= RoleCommonUser` | Authenticated user APIs |
| `middleware.RootAuth()` | Root only | System-level operations |
| `middleware.TryUserAuth()` | None (optional) | Endpoints that work with or without auth |

**Rules:**
- Public endpoints (no auth) must be explicitly justified — document why in the code and in the design doc.
- Admin endpoints go under groups that already use `AdminAuth()`; do not re-apply the middleware individually.
- Do not bypass middleware by placing privileged logic in a public route group.
- New relay-adjacent endpoints that process AI requests follow the relay router, not the API router.

### Rule 12: Configuration — Use the `setting/` Package

All user-configurable settings MUST go through the `setting/` package. Do not introduce new global variables, environment-only config, or ad-hoc DB queries for settings.

**Adding a new config — four steps:**

1. **Define the struct** in `setting/{module}_setting/{module}_setting.go`:
   ```go
   type MyFeatureSetting struct {
       Enabled  bool `json:"enabled"`
       MaxItems int  `json:"max_items"`
   }
   var myFeatureSetting = MyFeatureSetting{Enabled: false, MaxItems: 100}
   ```

2. **Register in `init()`** so ConfigManager persists it to DB:
   ```go
   func init() {
       config.GlobalConfig.Register("my_feature_setting", &myFeatureSetting)
   }
   ```

3. **Expose a getter** (callers must not cache the returned pointer across requests):
   ```go
   func GetMyFeatureSetting() *MyFeatureSetting { return &myFeatureSetting }
   ```

4. **DB persistence is automatic** — `ConfigManager.LoadFromDB()` and `SaveToDB()` handle it. Keys are stored as `{module}.{field}` (e.g., `my_feature_setting.enabled`).

**Rules:**
- Do not read settings directly from `os.Getenv` for user-configurable values — env vars are for deployment/infra config only.
- Thread safety is handled by ConfigManager; always call the getter, never hold a reference to the struct directly across goroutines.
- Expose config changes via the existing settings API (admin settings controller) — do not add custom save endpoints.

### Rule 13: Backend i18n — Sync Both Languages

When new code produces **any user-facing string** (error messages, notification text, log messages shown in UI), add translations to both language files in `i18n/`:

- `i18n/locales/en.json` — English (source of truth)
- `i18n/locales/zh.json` — Chinese

Use `ApiErrorI18n(c, msgKey)` (Rule 11) for controller errors so the message key is looked up at runtime. Do not hardcode Chinese or English strings directly in controller/service code — put the string in the i18n files and reference the key.

For backend strings not shown in the UI (internal log messages), i18n is not required — write in English.

### Rule 14: Billing Expression System — Read `pkg/billingexpr/expr.md`

When working on tiered/dynamic billing (expression-based pricing), you MUST read `pkg/billingexpr/expr.md` first. It documents the design philosophy, expression language (variables, functions, examples), full system architecture (editor → storage → pre-consume → settlement → log display), token normalization rules (`p`/`c` auto-exclusion), quota conversion, and expression versioning. All code changes to the billing expression system must follow the patterns described in that document.

### Rule 15: Testing Conventions

#### 15.1 Go backend — test file placement and framework

- Test files live **alongside the source file** they test (`foo.go` → `foo_test.go`), in the same package or the `foo_test` black-box package.
- Use **`testify`** (`github.com/stretchr/testify/require` / `assert`) for all assertions. Do not use bare `t.Fatal` / `t.Error` for assertion logic.
- Use **`net/http/httptest`** + **`gin.CreateTestContext`** for controller and middleware tests.

#### 15.2 Test-first workflow — write test cases before implementation

**Before writing any implementation code**, design the test cases first:

1. **Identify test techniques** applicable to the logic under test:
   - **Boundary value analysis** — test at and just inside/outside every boundary (e.g. `limit=0`, `limit=1`, `limit=200`, `limit=201`).
   - **Equivalence partitioning** — group inputs into valid/invalid classes and test one representative from each.
   - **Statement coverage** — ensure every line of code is executed by at least one test.
   - **Decision coverage** — ensure every branch (if/else, switch case) takes both true and false paths.
   - **Condition coverage** — for compound conditions (`a && b`), exercise each sub-condition independently.
   - **Path coverage** — cover distinct execution paths through the function, especially loops and nested branches.

2. **Write the test cases** (inputs, expected outputs, error expectations) in the test file before touching the implementation.
3. **Run the implementation** against the tests — all tests must pass before the feature is considered done.

#### 15.3 What to test

| Layer | Test focus |
|---|---|
| `dto/` | JSON round-trip, zero-value preservation, pointer semantics (Rule 5) |
| `model/` | Query correctness, migration compatibility |
| `service/` | Business logic, edge cases, error paths |
| `relay/channel/` | Provider request/response transforms; mock upstream with `httptest.NewServer` |
| `relay/helper/` | Billing expressions, price calculation, stream parsing |
| `middleware/` | Header parsing, auth logic (use `gin.CreateTestContext`) |
| `common/`, `pkg/` | Utility correctness |

#### 15.4 Relay chain — never call real upstream AI providers in tests

- **Never** make real network calls to OpenAI, Claude, Gemini, or any upstream AI API in tests.
- Mock upstream responses with `httptest.NewServer` returning fixture JSON — already the pattern used in `relay/channel/` tests.
- Relay tests must be fast (< 1s) and deterministic — no external I/O.

#### 15.5 Database tests — use real project env; SQLite as fallback only

- Prefer the **real project database** configured via the project's `.env` / environment config for integration tests. This catches SQL dialect issues that SQLite silently accepts.
- Use SQLite in-memory (`:memory:`) only as a **fallback** when no real DB env is available (e.g. CI without a DB service), not as the default.
- **Never mock GORM** — mocks have caused prod/test divergence in this project (missed migration failures).
- When using SQLite as fallback, explicitly document any SQL that behaves differently on MySQL/PostgreSQL.

#### 15.6 What not to test

- Do not reproduce the relay hot path end-to-end — that belongs to staging/load testing.
- Do not test framework behaviour (Gin routing, GORM auto-migration) — only test project logic built on top of them.
- Do not write tests purely for coverage metrics; write tests for logic that can actually break.

#### 15.7 Frontend — no test framework currently

- The frontends have no test runner configured. TypeScript type checking (`bun run typecheck`) and ESLint (`bun run lint`) are the primary correctness gates.
- Do not add a test runner without user approval. If a bug warrants a regression test, document it in the design document instead.

#### 15.8 Mandatory test run after implementation

- **All tests must be run and pass** before reporting implementation as complete. Run: `go test ./...` from the repo root (or the relevant package).
- Fix all test failures before marking the task done — do not ship with known failing tests.
- New business logic in `service/` or `model/` with branching or edge-case behaviour MUST have tests covering all branches.
- Bug fixes MUST add a test that would have caught the original bug.
- Relay channel adapter changes MUST include a fixture-based test for the changed transform.
