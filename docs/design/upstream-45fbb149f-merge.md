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

## Initial validation before database provisioning

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

At the initial merge stage, the full root `go test -p 2 ./...` was attempted. `SQL_DSN` is absent; service's
TestMain requires it and some inherited controller/model integration cases
dereference the unavailable DB instead of skipping. These three complete suites
are therefore **not validated**. Available isolated SQLite fixtures ran in the
other packages; they do not substitute for MySQL/PostgreSQL migration coverage.
Docker CLI is installed but its daemon is unavailable. No real MySQL/PostgreSQL
version, fresh/upgrade migration rehearsal, second-start DDL check, server-schema
rehearsal or load test can be reported. Run that database matrix before deployment.

## 合并后的回归修复与验证

数据库回归测试发现部分测试夹具和断言未适配上游变更。现已补齐访问令牌、会话表，
按测试身份清理数据，并更新结构化消息、输入预扣费倍率和显式 XLSX 导出等断言。

本次修复：

- 将严格 Claude 流记录在请求上下文中的搜索次数纳入工具计费，并保留次数上限。
- 严格 HTTP 流结束时保留 Responses 协议结果，将最终状态发布到现有请求上下文，
  供仅统计成功请求的限流逻辑使用；Claude 严格解析器也发布失败状态。
- 所有个人日志导出格式统一拒绝缺失的用户身份。
- 语言查询兼容没有 HTTP 请求的上下文。
- 错误摘要按已配置的上游地址脱敏；被拦截的 Responses 错误帧保留健康评分所需业务错误码。

### Main Chain Impact

计费修复读取一个请求内整数，复用现有工具调用上限和价格计算逻辑。流结束时复用
请求内状态对象，更新已有上下文键。不增加数据库、Redis、网络调用、工作协程或全局锁。
Responses 失败结束时仅解析一次有大小限制的终止帧，正常流式数据块不增加解析。
现有请求内状态同步不跨越 I/O，计费和日志持久化继续沿用现有异步路径。
在每分钟 3 万至 10 万请求的负载下，新增工作量按每次流结束或结算保持常数，
不随数据库规模增长。严格流式行为继续由现有流错误配置控制。

### 共享资源检查

未引入新的共享资源。工具价格复用现有设置读取；流状态和上下文归属于单个请求。
日志导出身份校验在获取现有导出锁、查询日志前返回。
本次修复不增加用户、令牌、渠道数据行、Redis 命名空间或连接池的访问。

### 验证结果与限制

已完成真实 SQLite、MySQL 和 PostgreSQL 验证，以及服务器结构升级预演；
第二次启动无 DDL。真实上游普通、流式、缓存创建及缓存命中计费核对一致。
本机默认信任额度模式下 30 并发、约 60 秒 mock 压测完成 113,630 次请求，零失败。
原测试服务器 10 并发完成 529 次请求，零失败；100 并发触发 CPU 保护返回 503，
不能据此认定高并发容量通过。服务器测试账目合计 94,585 quota，与余额和令牌用量一致。

service 完整测试通过；model 在正确配置独立日志库后完整测试通过。
controller 的 Responses 健康分类失败已修复并定向复测通过，但没有最终单次全量根模块
测试全部通过的记录。上游 HTTP/2 GOAWAY 测试在 Windows 偶发连接重置，随后连续
五次独立复测通过。上述短时压测不代替长时间稳定性测试。

详细部署报告、压测结果及环境证据仅在本地留存，不纳入代码提交。

## 关联功能复查（2026-10-16）

复查覆盖账号表单、会话与权限、令牌限制、渠道状态、请求策略、模型价格、
用户分组倍率、员工与客户归属、计费日志和导出、任务插件及管理页面。
页面可打开、接口交互、自动化回归和真实上游验证分别记录，不能互相替代。

发现并修复：

- 用户新增、注册、修改及个人资料校验不再暴露 Go 校验器内部字段。
  后端按字段返回中文、繁体中文或英文说明；前端注册与管理表单共用
  用户名、显示名称、邮箱和备注长度边界，按 Unicode 字符计数。
- 普通用户没有覆盖权限时，管理员修改资料原先仍被判定为权限变化，
  导致登录失效。现在根据原有权限删除语句的实际影响行数判断是否改变权限。
  真正的权限变更、禁用和其他敏感操作仍保留原有认证与会话撤销机制。
- 渠道余额、编辑器展开按钮及价格同步阶梯条件复用现有完整翻译键；
  阶梯条件明确显示“完整输入长度”，避免与计价输入 token 混淆。
- 补全全新数据库中的测试夹具；诊断日志测试遵循独立日志库配置。
  API 密钥切换测试改为点击当前可访问的选项，避免误点已隐藏的弹层。

### Main Chain Impact

本轮生产代码变更仅在管理接口校验、权限清理结果判断和前端展示执行。
AI 转发链不新增同步或异步操作；每分钟 3 万至 10 万请求时，转发侧
数据库调用、Redis 调用、锁及协程成本均无增量。

### 共享资源检查

权限清理沿用原有管理事务和 Casbin 规则表上的用户、资源限定删除；
只读取已有执行结果的 RowsAffected，不增加查询、连接池、表或索引。
用户资料更新仍沿用原事务、资料版本及缓存刷新；未修改钱包扣费或额度缓存。
修复避免没有权限变更时无意义地更新认证版本，不取消真正的权限撤销。
没有新增 Redis 命名空间、共享内存结构、全局锁或后台任务。

### 本轮验证结果

前端全量 209 文件、2,445 项测试通过，类型检查、静态检查和生产构建通过。
根模块完整测试的 controller、model、service 等 83 个包通过；Windows 上两个
HTTP/2 GOAWAY 用例出现连接中止，Linux 完整渠道包及 HTTP/2 连续 20 轮复测通过。
补配独立数据库后，MySQL／PostgreSQL 关联矩阵 715 项、性能统计 10 项通过；
ClickHouse 未验证。relaykit 独立构建和测试通过。

本机最终程序完成 46 项接口关联检查，原测试服务器完成 9 项接口检查及用户编辑
浏览器回归；两端各检查 16 个管理页面，无运行异常或失败接口。
覆盖部署保持原目录和端口，临时测试账号已禁用、活动登录会话为零。
本轮不重复宣称首轮短时压测为长期容量验证，也不宣称已实际调用全部上游服务。
