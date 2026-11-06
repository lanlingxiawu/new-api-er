# Real process shutdown and restart verification

This opt-in fixture runs the production Linux gateway binary in Docker, sends
synthetic OpenAI SSE and JSON requests, signals the gateway while requests are
active, and starts a new process against exactly the same database and Redis DB.
It never mounts the repository, `.env`, or an existing database into the gateway.
The only upstream is the local fixture. All Docker resources use a unique task
label; cleanup only removes resources created by this invocation.

Run from the repository root. Docker Desktop must use Linux containers. Go,
Python 3, `alpine:3.22`, and `redis:7-alpine` are required. MySQL and PostgreSQL
verification additionally use `mysql:8.0` and `postgres:17-alpine`.

PowerShell build commands (artifacts stay outside the repository):

```powershell
$taskOut = Join-Path $env:TEMP ('new-api-shutdown-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskOut | Out-Null
go build -o "$taskOut/harness.exe" ./bench/shutdown/harness.go
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -o "$taskOut/harness" ./bench/shutdown/harness.go
go build -o "$taskOut/new-api" .
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
python bench/shutdown/run.py --output "$taskOut"
```

Use a fresh output directory containing the three binaries for each invocation.
For a targeted database run:

```powershell
python bench/shutdown/run.py --output "$taskOut" --database mysql --cases normal500,queued-logs
python bench/shutdown/run.py --output "$taskOut" --database postgres --cases normal500,queued-logs
```

Cases:

| Case | Offered rate | Signal and load |
| --- | ---: | --- |
| normal500 | 500 requests/s | TERM during 500 ms SSE/JSON requests; batch enabled |
| normal1667 | 1667 requests/s | TERM during 500 ms SSE/JSON requests; batch enabled |
| timeout | 500 requests/s | TERM with a 2 s drain budget and 30 s upstream responses |
| kill | 500 requests/s | KILL while requests and unflushed buffers exist |
| batch-off | 500 requests/s | TERM with synchronous quota persistence |
| queued-logs | 500 requests/s | TERM with the log flush interval raised to 60 s |
| preconsume | 500 requests/s | TERM with `quota_setting.trust_quota_usd=0`, forcing real wallet/token pre-consumption |
| preconsume-timeout | 500 requests/s | Forced pre-consumption plus the 2 s drain budget and 30 s upstream responses |

The generator is bounded to 4096 active requests, records dropped arrivals and
arrival lag, and keeps offering traffic after the signal to verify that new
connections are refused. A normal response must have the exact fixture usage
(100 prompt and 50 completion tokens), a normal finish reason, and, for SSE, a
complete `[DONE]` with no stream/read error. The offered-rate gate fails if the
started rate before the signal differs by more than 10% or any arrival is dropped
before the signal. Drops after the signal are reported separately. A passing
arrival-rate test with a growing in-flight backlog does not establish sustained
completion throughput.

After stopping, the verifier checks user/token/channel counters, request counts,
request IDs, consumption logs, each linked cost row, duplicate/missing cost rows,
and Redis/DB counters. A successful normal request costs exactly 150 quota units.
After restart, no seed/reset runs: the verifier checks that startup has not
changed accounting, sends 100 new mixed SSE/JSON requests, stops again, and checks
their exact incremental accounting and the unchanged old records.
The current driver reuses the original HTTP endpoint on restart. SQL fixtures
limit the gateway pool to 100 open / 20 idle connections against a server limit
of 300. Earlier reports using the default 1000-connection gateway pool retain
their connection-exhaustion failures; they are not normal configured runs.

`summary.json` and each `result.json` contain explicit pass/fail decisions. A
failed invariant makes the driver exit nonzero after preserving evidence. The
KILL case can expose memory-only batch/log loss and Redis/DB drift; that outcome
is retained as a failure rather than accepted as a passing crash-recovery test.
Timeout cases use persisted log quota for internal consistency, since interrupted
responses need not have the normal fixture's final usage.
The forced-preconsumption timeout case also checks for preconsumed quota that has
no matching terminal settlement. Partial SSE content can be billable, so a
missing complete response does not imply that the whole reservation must be
refunded. The report retains unresolved reservations as a failure.

Reports include full request traces, gateway logs, the production binary SHA256,
and Go build metadata. SQLite reads/writes execute in a Linux helper container;
the Windows host never opens a live Docker WAL. Only a stopped bootstrap database
is copied with SQLite's backup API.

## WebSocket restart probe

Build the additional mock/probe binaries into the same output directory:

```powershell
go build -o "$taskOut/wsprobe.exe" ./bench/shutdown/wsprobe
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -o "$taskOut/wsprobe" ./bench/shutdown/wsprobe
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
python bench/shutdown/run_ws.py --output "$taskOut"
```

This probe uses a separate synthetic realtime upstream and a fresh isolated
database. It checks `session.created` and a bidirectional `session.update` /
`session.updated` exchange before shutdown and after starting the same container
against the same database and Redis DB. An HTTP completion before stopping and
another after restarting must both finish, have distinct persisted request IDs,
and preserve the old log and cost records. All waits are finite, and fixture
containers and the network are removed at the end. The JSON report and logs stay
under `websocket-<id>/`.

`passed_restart` reports restored request handling and persisted old records.
WebSocket closure is reported separately, including the actual close code. In
the current observed implementation, an upstream normal close (1000) reaches
the client as abnormal close 1006; stopping while a WebSocket is held also yields
1006 (`unexpected EOF`). The baseline and restarted normal-close probes retain
their nonzero probe exit codes and `normal_close: false`. A successful restart
does **not** mean that WebSockets drained gracefully; inspect
`held_ws_gracefully_closed` and each close result in `report.json`.

## Socket concurrency and race checks

The root-package opt-in matrix tests the production shutdown coordinator with
real local JSON/SSE sockets. It covers 64/128 accepted concurrent requests,
partial client cancellation, 500 ms and 1 s drain deadlines, a blocked batch
callback, batch disabled, and an idle server. Every worker completes 16 warmup
requests before a barrier holds its final request during shutdown. It checks
rejection of new connections, exact callback order, cancellation and completion
counts, and the number of active handlers when flushing begins.

```powershell
$env:FORK_SHUTDOWN_LOAD = '1'
go test . -run '^TestShutdownWithBatchFlush_(Load|WaitsForBatchAlreadyInFlight)$' -count=1 -timeout=120s -v
$env:FORK_SHUTDOWN_WORKERS = '8'
$env:CGO_ENABLED = '1'
# Put a compatible GCC on PATH first; this host uses C:\msys64\mingw64\bin.
go test -race . -run '^TestShutdownWithBatchFlush_(Load|WaitsForBatchAlreadyInFlight)$' -count=1 -timeout=120s -v
Remove-Item Env:FORK_SHUTDOWN_LOAD, Env:FORK_SHUTDOWN_WORKERS, Env:CGO_ENABLED
```

The short warmup rate is a local socket burst measurement, not a gateway capacity
claim. Deadline cases explicitly observe that `http.Server.Shutdown` returning
does not cancel active handlers; those fixtures release the handlers afterward
to join every worker. They do not label timed-out requests as gracefully drained.
The separate persistence regression holds an existing batch database write open
during shutdown and verifies that the final flush waits for it before the child
process exits. It uses an isolated SQLite database and subprocess.
