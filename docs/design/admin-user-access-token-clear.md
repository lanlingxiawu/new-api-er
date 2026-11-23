# 用户管理：一键清空访问令牌

日期：2026-11-16。状态：用户已批准实现；已完成代码、三库验证和真实浏览器联调，尚未部署。

## Goals and Scope

用户已明确对象是“安全与访问”页面中用于脚本调用面板 API 的访问令牌。延续本轮用户管理需求，在目标用户的行操作中增加“清空访问令牌”，一次撤销全部现有访问令牌，无需查看列表或逐条选择。

范围包括 `user_access_tokens` 中的 scoped PAT（`nap_`，含已过期但尚未清理的记录），以及该用户仍保存的旧版 `users.access_token`。不改变已有“清空 API Key”的行为；本操作不撤销网页登录会话，不修改账号状态、余额、订阅或渠道。成功后用户仍可在原页面创建新访问令牌。

## Existing Logic and Related-Path Findings

以下结论来自本次实际实现和相关测试阅读：

| 代码位置 | 已核实行为及设计影响 |
| --- | --- |
| `web/src/features/security/components/access-tokens-card.tsx`、`hooks/use-access-tokens.ts` | 截图对应个人访问令牌卡片；列表、创建、单枚撤销、旧版撤销均走本人接口。现有 hook 在成功后失效个人列表查询，不存在管理员清空入口。 |
| `controller/access_token.go` | `ListAccessTokens`、`CreateAccessToken`、`DeleteAccessToken`、`RevokeLegacyAccessToken` 都要求真实浏览器会话。撤销单枚令牌使用绑定 token ID 的一次性 proof，不能直接用于清空另一个用户。 |
| `model/user_access_token.go` | PAT 保存 SHA-256 指纹；`CreateUserAccessToken` 先锁所属 `users` 行，再检查数量、插入。`service.AccessTokenMaxPerUser` 当前为 20。`DeleteUserAccessTokensWithTx` 已能在传入事务中按用户硬删除并返回行数，用户删除流程已复用它。 |
| `model/user.go` | `RevokeUserAccessToken` 锁用户行，清空旧版凭据及创建时间。它自持事务，不能与 PAT 删除分两次调用来宣称原子成功；新路径在同一事务中完成两项写入。 |
| `middleware/auth.go` 的 `lookupAccessToken` | scoped PAT 每个请求按指纹查主库，旧版令牌按 `users.access_token` 查主库；结果只在当前 Gin 请求上下文复用。不存在跨请求 PAT 凭据允许缓存。随后读取的 `UserBase` 缓存不包含访问令牌。 |
| `model/user_access_token.go` 的 `TouchUserAccessToken`、`UpdateUserAccessToken` | 只更新已有行，不执行 upsert；删除后迟到的最后使用时间、重命名、授权变更不会重新创建令牌。既有过期清理也是 DELETE。 |
| `model/auth_flow.go`、`service/security_verification.go` | 消费 proof 时调用 `ValidateAuthSessionWithTx`；`pat:<id>` 身份会重新检查对应令牌存在、归属、有效期。因此撤销后尚未消费的 PAT proof 不能通过身份校验；过期 flow 继续由原后台清理。 |
| `router/api-router.go`、`middleware/access_token_routes.go` | 用户管理组已有 `AdminAuth()` 和 `AdminMenuUsersView`；本人访问令牌管理路由均声明 session-only。新清空接口按同样边界禁止任何 PAT 调用。 |
| `service/user_api_keys.go`、`controller/user_api_keys.go` | 已有实时角色检查和管理员一次性验证模式；允许操作者角色严格高于目标，目标禁用仍可清理。复用授权语义，新增独立访问令牌 scope。 |
| `controller/audit.go`、`middleware/audit.go` | `recordManageAuditForUser` 记录操作者、目标用户及参数；路由兜底动作需要同步登记。沿用既有审计写入方式，不把跨日志库审计承诺为主库事务的一部分。 |
| `web/src/features/users/components/users-clear-api-keys-dialog.tsx`、`users-provider.tsx`、`web/src/components/confirm-dialog.tsx` | 已有行操作、共享安全验证、确认框、提交禁用和失败反馈的实现。新增业务组件组合这些封装，无需新通用 UI。`users/api.ts` 的 `securityProofConfig` 防止一次性授权被自动重放。 |
| `model/user_update_upstream_cases_test.go`、`middleware/auth_test.go` | 已覆盖 20 枚限制、归属、删除后指纹查找失败、失效 PAT 身份、session-only 路由、单请求只查询一次。新增测试重点是管理员清空、事务回滚、并发和真实鉴权结果。 |

相关设计：[模型 API Key 清空](admin-user-api-key-deletion.md)、[管理 PAT 权限](admin-token-management-permissions.md)。后端目前缺少管理员清空访问令牌的接口及 proof scope，需要本次补齐；不使用前端循环单删来模拟清空。

## Data Flow and Interaction

1. 管理员进入用户管理，在目标行选择“清空访问令牌”。不请求目标的令牌列表或数量。
2. 共享确认框显示用户名，并说明现有访问令牌将全部撤销，依赖它们的脚本需要新令牌，操作不能恢复。
3. 确认后使用现有安全验证，获取绑定目标用户及本操作的 proof；取消时不发送清空请求。
4. 后端检查会话、菜单权限、实时角色层级，消费 proof，进入有超时的清空事务。
5. 事务锁定并复核操作者与目标，删除 PAT，清空旧版凭据，提交后返回实际撤销总数并记录管理审计。
6. 前端只在 `success:true` 时关闭弹窗、提示撤销数量；零条提示“该用户没有需要清空的访问令牌”。提交中固定目标、禁止重复提交和关闭。失败保留重试入口，再次操作必须重新验证。

入口继续位于用户管理；截图用于确认令牌类型，本次不额外给个人卡片添加另一套清空入口。用户管理列表与分页不因清空改变，不需要重新加载整张用户表；同一 QueryClient 中若存在目标个人令牌列表缓存，则精确失效该用户的查询键。其他浏览器中的列表在原有重新获取时更新，服务端鉴权不依赖列表刷新。

## API Contracts and Authorization

新增 `DELETE /api/user/:id/access_tokens`，放在已有用户管理组，使用 `DisableCache()`、既有 `UserCriticalRateLimit()`。无需请求体或查询参数。

- 仅已登录的浏览器管理会话可调用，普通用户、员工、全部 scoped/legacy/root PAT 均不能调用。
- 当前操作者必须启用且角色至少为 admin；当前目标必须存在且未删除，并满足 `operator.role > target.role`。管理员不能清理自己、同级或上级；root 可清理普通管理员。禁用目标允许清理。
- `X-Security-Proof` 必填；新增 `admin.user.access_tokens.delete_all` scope，context 严格为 `{ "user_id": 正整数 }`。
- 在安全验证 scope 的常量、context 规范化、方法策略、密码禁用分支、前端类型及 PAT scope 映射中完整登记。PAT 映射显式为空，禁止申请该操作的 proof。
- 成功：`{ "success": true, "message": "", "data": { "revoked_count": N } }`。N 是本次实际删除的 PAT 数加实际撤销的非空旧版凭据数量；已过期 PAT 的存储记录也计入。
- 无令牌时成功且 N=0。每个请求使用新的 proof；重复使用 proof 必须失败。

## Transaction, Concurrency and Lifecycle

新增管理专用 model/service/controller 路径。服务层把现有 `AuthorizeUserAPIKeyDeletion` 提炼为两种凭据清空共用的 `AuthorizeUserCredentialDeletion`，保持原 API Key 权限和删除行为，先完成前置检查再消费 proof。

清空数据库操作使用请求 context 派生的 5 秒预算。在 model 中开启事务，按用户 ID 升序分别通过 `lockForUpdate(tx)` 锁定操作者和目标的主键行，读取明确列并重新校验角色、状态、目标存在性。先锁 users，再操作 user_access_tokens，与创建 PAT 及 token-bound proof 身份校验的锁顺序一致。事务内：

1. 调用现有 `DeleteUserAccessTokensWithTx(tx, targetID)`，按 `user_id` 硬删除。
2. 清空目标 `access_token`、`access_token_created_at`，只修改这两列，记录是否实际撤销非空旧凭据。
3. 两项同时提交；只在提交成功后返回数量。不会先提交 PAT 删除再另开事务撤销旧版令牌。

正常每用户最多 20 枚 PAT；SQL 不加载整份令牌列表，也不加载 PAT 指纹。即便异常存量超过当前创建限制，按用户删除仍覆盖全部匹配行，并受请求预算约束，不依赖“最多 20”截断删除。

并发创建由同一个目标 users 行串行化：先取得用户锁并提交的新建会被此次清空覆盖；清空提交后取得锁的新建属于新令牌，保留。该操作不禁止以后创建，也不无限追赶新建。并发清空只有实际删除者计数；后一个请求通常成功返回零。并发单枚撤销、过期清理按实际 DELETE 行数计数；任何数据库冲突或死锁返回失败并允许重新验证重试。

成功提交后开始的新鉴权无法查询到被撤销的凭据；跨请求没有凭据缓存需要失效。已完成鉴权的在途请求不保证终止，已消费且开始执行的操作沿用原生命周期。删除后旧的 PAT-bound proof 在后续消费阶段不能通过身份验证；不主动批量扫描 auth_flows。

不递增用户 auth_version，不调用撤销全部网页登录会话或用户缓存广播，否则会扩大本次撤销的范围。

## Data Model, Index Design and Config

不新增表、列、索引、配置、环境变量或依赖。复用 `user_access_tokens.user_id` 普通索引、PAT 指纹唯一索引及 users 主键；PAT 是硬删除，查询没有 soft-delete 条件。所有查询和写入通过 GORM，保留用户归属条件；users 主键读取显式选列。支持 SQLite、MySQL >=5.7.8、PostgreSQL >=9.6。

设计阶段已在本地 MySQL 26.10.0 和 PostgreSQL 18.7 用连接级临时表验证相同 `id` 主键、`user_id` 索引访问模式：20,000 行、每用户 20 行。MySQL 按用户 SELECT 为 covering index lookup，`EXPLAIN FORMAT=TRADITIONAL DELETE ... WHERE user_id=500` 为 `range`、命中 user_id 索引、估算 20 行；PostgreSQL SELECT 和 DELETE 均为 user_id index scan、估算 20 行。临时表随连接自动释放，未改实际用户表。实现后另在 GORM 创建的完整 `user_access_tokens` 表插入 10,000 条测试记录，SQLite、MySQL、PostgreSQL 的实际 DELETE 查询均命中 `idx_user_access_tokens_user_id`；MySQL 为 range，PostgreSQL 为 index scan，均估算目标用户 20 行。

无 schema 变更，不需要新增迁移或生产 schema 演练；若实现过程中发现必须改 schema，应先修订设计并补充相应迁移矩阵。

## Error Handling and Audit

使用 `common.ApiErrorI18n` / `common.ApiErrorStatus`，保留统一 success/message 外壳。非法目标和业务失败使用 HTTP 200；权限拒绝 403，未认证交由认证中间件。proof 错误复用既有安全验证合同。底层数据库错误带请求 context 写 `logger.LogError`，客户端只收到可重试的翻译消息，不返回 SQL、Go 错误或凭据。

事务明确失败时回滚两项写入；proof 在此前已消费，不复用。提交响应丢失、超时等结果不确定时不宣称“未撤销”，不返回未经确认的数量；提示“未能确认清空结果，请重新验证后重试”。再次调用针对当前存储状态执行，不能自动重放旧授权。

登记审计动作 `user.access_tokens.delete_all`，记录操作者、目标、实际撤销数量、是否撤销旧版、成功状态、验证方式。成功后才提供已确认的变更数量；失败记录失败状态，不把回滚前 RowsAffected 写成已完成。凭据明文、完整 proof、PAT 指纹及敏感日志内容均不进入此批量操作的参数。日志沿用现有主库/独立日志库写入机制，日志失败不能恢复已撤销凭据。

前端文案覆盖七种语言，遵循 i18n 技能的临时补键脚本及同步流程。后端使用实际加载的 `en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`，同步新增错误键。审计动作显示名称同步本地化。

## Main Chain Impact

新代码只在管理请求执行；AI relay goroutine 新增同步操作为零，新增后台工作也为零，不修改 relay、中间件 TokenAuth、模型 API Key 缓存或结算流程。正常 relay 每请求新增 DB/Redis 调用、锁、goroutine 均为 0；30k/100k RPM 下亦相同，因此不新增 relay feature flag。

### Shared Resource Audit

| 资源 | 共享情况与约束 |
| --- | --- |
| `user_access_tokens` | 面板 PAT 管理/鉴权使用，模型 relay 不访问；按已有 user_id 索引删除。 |
| `users` | 与 relay 共享。仅按主键访问两行、短事务锁定，旧凭据更新最多一行；不扫描表，不改角色、额度或 auth_version，不在事务内等待 Redis、审计或网络。可能与同用户既有写操作短暂竞争，5 秒 context 与管理限流约束持续时间，验收检查超时和锁释放。 |
| 主数据库连接池 | 复用现有池；一次清空至多一个事务连接，无新池、无并发扇出。清空核心约 2 次主键读取、1 次按用户 DELETE、1 次 legacy UPDATE，加事务开销；前置授权另有 2 次主键读取。 |
| `auth_flows`、`user_sessions` | 只通过已有管理员安全验证路径访问；不新增全量查询或后台任务。 |
| Redis 与用户缓存 | 清空本身不读写任何 Redis key；登录、限流、安全验证仍用原命名空间。无新缓存、无 token/user hash 删除。 |
| 审计日志库/既有日志管线 | 每次操作沿用一条管理审计；不查询历史日志，不新开池，不持有用户事务锁写日志。 |
| 内存、goroutine、文件 | 固定大小的用户与计数对象；无全量令牌切片、无自建 goroutine 或文件句柄。 |

## Implementation and Verification

已新增 `controller/user_access_tokens.go`、`service/user_access_tokens.go`、`model/user_access_tokens_clear.go`，主要集成测试位于 `controller/user_access_tokens_test.go`，路由合同扩展已有 `router/user_api_keys_test.go`。已修改上述路由、验证 scope、审计登记和语言资源；用户管理增加 `users-clear-access-tokens-dialog.tsx`，接入 `data-table-row-actions.tsx`、`users-provider` 使用的 dialog 类型、`users/index.tsx`、`users/api.ts`。保护的五个 upstream 前端认证/HTTP 文件保持字节不变，已与当前 HEAD 逐字节核对；本地 main 尚无这些路径，无法声称与该分支比较通过。

实现前已补充行为测试，确认新增后端符号和前端组件尚不存在时测试失败，再完成实现。验证范围：

- SQLite、真实 MySQL、真实 PostgreSQL：零/单枚/20 枚/异常超过 20 的存量，旧版单独及与 PAT 共存、过期令牌、禁用目标、其他用户隔离；PAT 删除或旧版更新失败整笔回滚。
- 权限与 proof：普通角色、自己、同级、上级、已删除目标、停用操作者；缺失、过期、错误 scope、其他用户 context、重放；所有 PAT 被 session-only 拦截，角色在前置检查后变化时事务复核拒绝。
- 并发与实际鉴权：清空/创建按锁顺序验证边界，两个清空不重复计数；撤销后旧 scoped/legacy 请求拒绝，迟到 touch/update 不复活，未消费 PAT proof 失效；其他用户和新建令牌继续有效。
- 影响隔离：目标浏览器会话、模型 API Key、额度和渠道仍正常。浏览器真实用户行操作完成后，从独立 HTTP 客户端使用旧访问令牌请求面板 API 并核验拒绝；确认过程中不请求令牌列表。
- 前端：权限入口、确认与取消、安全验证让位、pending 防重、目标冻结、空结果、成功计数、失败与重新验证；桌面/窄屏/暗色浏览器检查。扩展现有 `step-up-verification.test.tsx`，同步审计显示回归。
- 使用生产文档所述 MySQL 主库、PostgreSQL 日志库组合验证审计；测试采用隔离 fixture，不 truncate 共享库。记录版本、命令、结果；执行类型检查、相关 lint、生产构建及 Go 构建。尚未执行的实现测试不得标为通过。

安全控制参考当前稳定版 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)、[Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#require-re-authentication-for-sensitive-features) 的敏感操作再认证，以及 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html#session-expiration) 的服务端失效原则。具体采用目标绑定、短期单次 proof、服务端授权与持久化撤销；已鉴权在途操作和提交结果不确定的边界如上，不宣称能终止一切进行中操作。

### 实测结果（2026-11-16）

| 检查 | 结果 |
| --- | --- |
| SQLite 3.50.4 | 清空计数、过期/旧版、归属、proof 缺失/错 scope/错目标/过期/重放、数据库回滚、权限、取消、并发创建/清空、角色变化复核、真实 HTTP 鉴权、索引均通过。 |
| MySQL 26.10.0、PostgreSQL 18.7 | 上述清空专项及已有 API Key 清空回归通过。PostgreSQL CHAR(32) 自动补空格已在实现计数和测试比较中正确处理。 |
| MySQL 主库 + PostgreSQL 日志库 | 独立日志数据库的管理审计写入通过；此组合用例只在 MySQL 轮次运行，PostgreSQL 主库轮次标记为不适用跳过。 |
| 前端 | 用户管理与审计本地化共 19 项测试通过；包含安全验证、取消、失败后新 proof、提交防重、角色入口、零/非零结果及七语言审计标签。七语言新增文案、变量占位符和 UTF-8 无 BOM 检查通过。 |
| 类型、lint、构建 | `bunx --no-install tsgo -b`、`bun run lint`、`bun run build`、`go build` 通过。lint 有 8 条现有无关警告。本仓库 `bun run typecheck` 仍引用不存在的 `bun check -b`，本次直接运行已有 tsgo，不修改无关脚本。 |
| 浏览器与独立 HTTP | Chrome 157.0.8103.51，真实后端、SQLite、Redis 7.4.11、受控模型上游。真实行菜单操作撤销 2 枚 PAT + 1 枚旧版令牌；三个旧凭据随后 401，其他用户 PAT 200，两位用户的模型 API Key 均正常访问上游；管理会话刷新页面后仍有效。一条管理审计，无凭据/指纹泄漏，无令牌列表请求。中英文桌面亮色及 430px 暗色确认框已截图检查，无横向溢出。 |

复现命令（环境变量仅在进程内注入，不记录密码）：

```text
go test ./controller ./router -run 'Test(AdminClearUserAccessTokens|UserAccessTokensClear|AdminClearUserAPIKeys)' -count=1
go test ./controller -run 'TestUserAccessTokensClear(LegacyOnly|StorageIndex|MySQLWithPostgresAudit)' -count=1
go test ./controller ./router ./middleware -run 'Test(AdminClearUserAccessTokens|UserAccessTokensClear|UserAPIKeysRoute|AccessTokenIdentity|UserAuthAppliesAccessToken|AdminUserRisk)' -count=1
cd web
bun run test src/features/users/components/__tests__/step-up-verification.test.tsx src/features/usage-logs/audit/lib/audit-localization.test.tsx
bunx --no-install tsgo -b
bun run lint
bun run build
```

MySQL/PG 轮次设置 `TEST_SECURITY_DIALECT`、对应 `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`、`SQL_DSN` 和 PostgreSQL `LOG_SQL_DSN`，均使用本地隔离测试库。证据位于 `%TEMP%/user-api-key-tests/*-controller-regression.log`、`*-controller-pat-extra.log`；浏览器脚本、结果和截图位于 `_local/user-access-tokens-e2e/`。浏览器 fixture 数据库已删除，测试服务已停止。

边界：未部署；未对生产数据或旧版 MySQL 5.7 / PostgreSQL 9.6 实例实测；没有新增 schema。事务失败用确定性 DELETE/UPDATE 故障注入验证，未模拟真实网络断开导致的提交结果不确定；超时策略使用 context，未做各驱动锁等待的故障压力测试。目标冻结由 mutation 绑定目标实现，本轮没有单列跨账号切换 E2E。PAT 后续 proof 消费复用的身份检查已验证拒绝被删除 token，本轮没有另走完整 PAT 验证仪式后再消费的 E2E。上述边界不计作已通过项目。
