# 用户管理：一键清空用户 API Key

日期：2026-11-16。状态：用户已确认；已实现并完成本地自动化及浏览器联调，尚未部署。

## Goals and Scope

管理员在用户管理中处理模型 API Key 泄露，无需登录用户账号或删除用户。

交互为“用户行操作 → 清空 API Key → 确认及安全验证 → 完成”。按用户明确要求，只提供一键清空该用户全部 API Key，不提供 Key 列表、单条删除或预览面板。

对象是 `model.Token`，即调用模型接口的 `sk-` Key。个人访问令牌（PAT）、网页登录会话、渠道上游密钥属于不同凭据，本功能不处理它们。保留用户余额、订阅和历史消费记录；用户仍可自行创建新 Key。

删除成功后，后续鉴权应拒绝该 Key。已经通过鉴权的请求、流式连接和已提交任务不主动中断，继续沿用原来的结算与任务生命周期。

## Existing Logic and Related-Path Findings

以下均来自本次工作区代码阅读，不以历史文档代替实现：

| 代码位置 | 已核实的行为与设计影响 |
| --- | --- |
| `router/api-router.go` 的 `userRoute/adminRoute` | 用户管理已有 `AdminAuth()` 与 `RequirePermission(authz.AdminMenuUsersView)`，新增接口归此组。 |
| `controller/token.go` 的 `GetAllTokens`、`DeleteToken`、`DeleteTokenBatch` | 从认证上下文读取当前用户 ID，现有接口只能管理本人 Key，不能直接给管理员传另一个 `user_id`。 |
| `model/token.go` 的 `GetTokenByIds`、`DeleteTokenById` | 同时限定 token ID 与用户 ID，归属检查可复用。`Token.DeletedAt` 表明现有删除为软删除。 |
| `model/token.go` 的 `Token.Delete`、`BatchDeleteTokens` | 删除前调用缓存失效，但失效失败只记日志，仍可能返回删除成功；新应急删除路径不能直接继承这种成功语义。 |
| `model/token_cache.go` | 缓存为 `token:<HMAC>`，变更 fence 为 `token:fence:{token:<HMAC>}`。失效先设 fence 再删 hash；`cacheInitToken` 检查 fence 防止旧快照回填。fence 当前为 10 秒，缓存 TTL 来自 `RedisKeyCacheSeconds()`，无效值回退 60 秒。 |
| `model/token.go` 的 `GetTokenByKey`、`ValidateUserToken` | Redis 优先；未命中回主库，软删除记录不可见；禁用状态被拒绝。API Key 缓存读取不经过新的管理服务。 |
| `model/token.go` 的额度增减 | 配额通过现有异步缓存增量和批量数据库路径更新。缓存增量不会创建已不存在的完整凭据 hash；本功能不重写这些路径。 |
| `controller/user.go` 的 `DeleteUser` 与管理操作 | 管理敏感操作校验目标角色，再消费操作者的一次性验证 proof。本功能沿用严格角色层级，管理员不能通过该入口管理同级、上级或自己的 Key。 |
| `service/security_verification.go`、`controller/secure_verification.go` | 已有带 context 绑定的验证、过期检查、单次消费和 `requireAdminUserProof`。现有 admin-user 验证优先采用已绑定的 2FA/Passkey；没有独立因素时沿用现有密码/OAuth 策略。 |
| `middleware/access_token_routes.go`、`middleware/admin_access_token.go` | PAT 有明确路由许可与管理员模块限制。新接口显式声明为 session-only，不扩张既有管理 PAT 的许可。 |
| `controller/audit.go`、`middleware/audit.go` | `recordManageAuditForUser` 可同时记录操作者和目标用户；兜底审计需登记路由动作及目标用户参数。 |
| `web/src/features/users/components/data-table-row-actions.tsx`、`users-provider.tsx` | 用户行已有订阅/绑定管理入口，Provider 挂载一个共享验证弹窗，可复用其 `requestVerification`。 |
| `web/src/features/users/api.ts` | `securityProofConfig` 已通过 `X-Security-Proof` 和 `singleUseAuthorization: true` 防止一次性请求被认证重试自动重放。 |
| `web/src/features/users/components/users-delete-dialog.tsx` | 已有共享 `ConfirmDialog`、验证框让位、提交防重和结果提示，可复用为清空操作的交互模式。 |
| `i18n/i18n.go` | 实际后端资源为 `en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`，不是根规范概述中的 JSON 文件。按实际加载路径补齐语言。 |

关联设计 `admin-token-management-permissions.md` 仅针对管理 PAT，并明确排除 AI API Key；本功能另立文档，不把两种令牌混为一体。

现有后端缺少管理员清空指定用户全部 Key 的接口和对应 proof scope，本次只补齐这两项能力，不增加管理员 Key 查询接口。

## Data Flow

1. 管理员在目标用户行菜单点击“清空 API Key”，直接打开确认框，不先加载 Key 列表或数量。
2. 确认框说明目标用户名、将删除其全部现有 API Key、使用这些 Key 的应用需要更换凭据，以及操作不可恢复。
3. 通过现有验证流程获取绑定目标用户及“全部清空”操作的一次性 proof。
4. 后端验证登录会话、用户管理菜单权限、操作者与目标角色，消费 proof，取得本次操作的服务端 ID 上界。
5. 在管理请求中按有限批次执行停用、缓存失效和软删除，记录实际结果。
6. 前端检查 `success` 与结果数据，成功后关闭确认框并提示清空数量；失败保留可重试状态，不把 HTTP 200 当作成功。

## API Contracts and Authorization

新增接口位于现有 `/api/user` 管理组，继承 `AdminAuth()` 和 `AdminMenuUsersView`，增加 `DisableCache()`；删除沿用管理操作限流。显式拒绝 PAT，包括 root PAT、历史管理令牌和 scoped PAT。模型 API Key 不能认证此管理接口。

目标条件：存在、未删除，且 `operator.role > target.role`。因此普通管理员可处理普通用户/员工，root 可处理管理员及更低角色；root 自己的 Key 使用原本人 Key 页面管理。已禁用但未删除的用户仍可清理 Key。前端按相同规则隐藏入口，最终判断始终在后端。

### 清空用户全部 Key

`DELETE /api/user/:id/tokens`

路径 `id` 为正整数；无需请求体、查询参数或提前获取 Key 信息。请求头为 `X-Security-Proof`。验证 scope 为 `admin.user.api_keys.delete_all`，context 严格为 `{user_id}`。scope 明确限定“清空全部”，不能复用删除账号、其他用户或其他操作的 proof。

服务端在执行开始时取得该用户当前未删除 Key 的最大 ID，记为本次内部上界。每批按用户和 `deleted_at IS NULL` 读取最早的最多 100 个 ID，在这批有序 ID 中去除超过上界的项，再按用户和这些主键读取 Key、停用及删除。不从客户端接受删除范围，也不无限追赶并发新建的 Key。开始执行后新建且 ID 超过上界的 Key 保留。没有 Key 时返回成功、`deleted_count:0`，界面提示“该用户没有需要清空的 API Key”。

按 ID 每批最多 100 条，逐批提交。清空服务的正常预算为 20 秒、单批 5 秒，到时停止启动新批次；删除提交结果不明时，额外用最多 1 秒回查该批。前置安全验证及既有审计管线不计入此预算。管理员可重新确认及验证后再次清空，重试按执行时的现有 Key 重新确定范围。既有每用户最大令牌数默认 1000 且可配置，不能据此假设存量总数必然不超过 1000，也不能一次性加载所有 Key。

完全成功返回 `{success:true,message:"",data:{deleted_count:N,completed:true}}`。中途失败或超时返回标准 `success:false` 外壳及 `{deleted_count:N,completed:false}`，说明已经删除的数量并提示重试；已提交批次不回滚，不重复计算为本次新增删除。普通错误用 `common.ApiErrorI18n`；需要携带进度时，用已存在的 `common.ApiErrorStatus`、HTTP 200、经 `i18n.T` 翻译的安全消息和 `data` 参数，保留相同错误外壳，不直接拼写 JSON 错误响应。

## Key Business Logic and Failure Recovery

在管理专用 model/service 中实现，不改本人 `/api/token` 的返回语义：

1. 每批先用覆盖索引读取指定用户最多 100 个 ID、裁去超过操作上界的项，再按主键读取对应 Key。明文 Key 仅在服务器内用于计算原缓存位置。
2. 先按 ID 与用户条件将该批尚未删除的记录设为禁用；这一步失败则不继续删除。禁用记录保留在数据库中，供失败后再次执行清空。
3. Redis 启用时，每个 Key 用 Lua 原子设置 fence 并删除 hash，该批最多 100 个脚本通过 pipeline 执行，检查整体及逐条错误；沿用已有缓存键和 fence 机制，不扩大到其他用户。
4. 只有该批缓存清理全部成功才执行按相同 ID、用户条件的软删除。缓存清理失败则保留尚未软删除的禁用记录并报错，不能返回“已删除”。Redis 未配置时，按主库停用/软删除完成。
5. 缓存清理与软删除使用同一批次时间预算；数据库写入必须在现有 10 秒 fence 窗口内完成，单批预算不超过 5 秒。剩余预算不足时不开始写入，返回失败供重试。不得为清空上千 Key 持有一个长事务，也不在数据库事务内等待 Redis。
6. 软删除失败时 Key 保持禁用，返回失败供重试；不会补偿为重新启用。数据库提交结果不确定时回查记录，不把超时直接解释为“未改动”。
7. 删除前按真实当前目标角色再次检查，全部删除在每个批次前复核；进行中的单批与其他管理操作并发时沿用现有短操作授权边界，不引入用户表全局锁。

失败提示区分“未完成删除”和“已删除部分”。禁用成功但缓存清理失败时，必须说明仍需重试确认生效，不把数据库状态当作缓存已经同步的证据。用户主动取消验证时不发出删除请求。

现有缓存 fence 是有时间界限的旧快照保护，不是无限期撤销登记。必须用并发回归确认普通在途回填、更新及配额增量不会重新生成已删除的有效 Key；不能宣称能覆盖进程暂停超过 fence、Redis 恢复旧快照等任意故障。若验证发现本功能在正常并发下不能满足删除后的鉴权拒绝，需先修订缓存设计，不能缩弱断言交付。

## Data Model, Migration and Index Design

不新增业务表或配置字段。继续使用 `tokens.status` 和 `tokens.deleted_at`，保留原 Key 唯一索引、历史 token ID 与计费记录关联。

已增加 `idx_tokens_user_deleted_id (user_id, deleted_at, id)`，保留已有独立索引。取得上界使用 `ORDER BY deleted_at DESC, id DESC`，取批次 ID 使用 `ORDER BY deleted_at, id`；因为查询已限定未删除，业务排序仍等同于 ID 排序。显式包含 `deleted_at` 使 PostgreSQL 能匹配索引排序。MySQL 实测在范围扫描中同时使用 `id <= 上界` 容易选择 index merge，因此在已限制为 100 个的有序 ID 中裁去越界项。每批写入按主键定位，并在 SQL 保留用户归属条件。大字段和凭据不放进索引，Key 通过第二次有限主键查询取得。

通过 GORM 跨库索引声明/既有迁移入口实现，支持 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6；不使用特定数据库的 partial index 或在线 DDL 作为唯一实现。

已阅读 `docs/production-environment.md` 与 `docs/release-checklist.md`：记录的生产主库为 MySQL、日志库为 PostgreSQL。本地验收通过 Docker 的真实 MySQL/PostgreSQL 和专用 Redis 完成，环境变量只传给测试子进程，未写入工作区 `.env`。结果见下方验收记录。

实现验收须对真实 MySQL/PostgreSQL 验证 ID 上界、首批和后续清空批次查询的 `EXPLAIN`，确认索引使用和扫描范围；同时检查新库、旧版结构升级及二次启动无重复 DDL。索引上线前按目标库 schema 在隔离库演练并评估建索引的锁/I/O；不得直接对生产运行实验。

## Configuration

不增加用户设置。复用管理员鉴权、限流、验证有效期和缓存 TTL。批次上限用于约束管理操作资源；若后续需要可调参数，必须走 `setting/`，不临时增加环境变量或全局开关。

## Frontend Interaction

- 在 `data-table-row-actions.tsx` 的现有用户行菜单中增加“清空 API Key”，采用相邻危险操作的文字、图标和样式结构，不增加管理面板。
- 通过 `users-provider.tsx`/`index.tsx` 挂载一个确认框，复用 `UsersDeleteDialog` 的布局与提交模式；确认统一使用 `ConfirmDialog`，验证统一使用 UsersProvider 的流程。
- 确认框明确显示目标用户名，提醒全部 Key 将失效、需要为相关应用更换凭据；不请求或展示 Key 列表，也不要求逐个选择。
- 开始确认后固定目标用户，操作期间禁用切换目标及重复提交；取消后清除本次确认状态，避免误操作另一用户。
- 删除 Mutation 禁止自动重试，复用 `securityProofConfig`，操作期间禁用重复提交；确认框让位给验证框。
- 成功后关闭确认框，提示实际清空数量；0 条显示无需清空。部分失败保留确认框及已删除数量，管理员再次提交时重新获取 proof。
- 使用 `requireServerSuccess` / `handleServerError` 处理错误；提交中显示“正在清空…”，失败给出重试说明。
- 所有新增界面/审计文案按 `i18n-translate` 工作流覆盖 en、zh、zh-TW、fr、ja、ru、vi；后端同步实际 YAML 三语言资源。既有 upstream-owned 五个 plumbing 文件不编辑。

## Error Handling and Audit

参数非法、目标不存在、业务失败使用现有标准响应和 i18n 辅助函数；未认证/无权限由相应鉴权层处理，基础设施异常沿用项目的服务器错误处理。客户端不接收原始数据库/Redis错误。

审计动作统一为 `user.api_keys.delete_all`。复用 `recordManageAuditForUser`，记录操作者、目标用户、服务端清空 ID 上界、实际删除数量、验证方式、success/completed；在 `middleware/audit.go` 登记失败路径的动作与目标参数，避免漏记或重复记录。前端审计动作标签补齐翻译。

不在日志、审计、URL或返回值中写完整 Key、Authorization、proof、验证码或完整 Redis 命令参数。日志包含请求上下文和安全的错误类别，相关后台错误沿用系统日志。

## Main Chain Impact

AI relay goroutine 不执行本功能的新代码，不新增中间件、权限查询、锁、数据库访问或 Redis 查询；不改变流式输出及计费流程。因此不需要向每次模型请求加入新的功能开关判断。

授权、停用、缓存失效和删除均同步发生在管理员 HTTP 请求中，成功响应必须等待该批完成。审计沿用项目现有记录管线，不新增 per-relay-request goroutine。失效后目标 Key 的缓存未命中/拒绝走现有鉴权逻辑；其他用户缓存保持原样。

已删除 Key 的重复攻击请求仍可能触发现有缓存未命中查库，这是当前鉴权路径的行为，不能把“新增热路径调用为零”解释为所有场景数据库负载完全不变。

## Shared Resource Audit

| 资源 | 主链是否访问 | 本功能访问及约束 |
| --- | --- | --- |
| `users` | 是，用户信息/认证缓存来源 | 管理侧按主键读取角色和状态；不修改余额，不为此功能锁目标用户行。proof 消费沿用原操作者认证短事务。 |
| `tokens` | 是，凭据及配额 | 有意撤销目标凭据，无法另建独立表替代。仅用户范围内最多 100 个主键的短写入，不全表扫描，不覆盖配额字段；冲突只限本次撤销的 Key。 |
| `token:<HMAC>`、对应 fence | 是 | 必须使原鉴权缓存失效，不能改用独立前缀伪装完成撤销。不触碰无关 Key，不使用 SCAN/KEYS/全库清空。 |
| `auth_flows`、`user_sessions`、Casbin 权限 | 管理鉴权路径使用；主链不消费此次 proof | 复用验证与权限机制，不发明另一套身份缓存。 |
| 审计日志/现有队列 | 既有公共设施 | 每次管理操作记录有限元数据，沿用既有故障处理；不把批量 Key 明文放入队列。 |
| 主库/Redis连接池 | 是 | 复用现有池、有限串行批次和超时，不新增并行删除 worker，不在持有 DB 事务时等待 Redis。 |
| 前端确认状态 | 否 | 仅保留目标用户及操作进度，不查询或缓存 Key。 |

## Concurrency Analysis

30k RPM 约为 500 次/秒，100k RPM 约为 1667 次/秒。本功能为每次模型请求增加的数据库调用、Redis调用、锁和 goroutine 数均为 0。

每次管理操作包括目标用户主键检查、既有 proof 消费和审计，以及一次索引查询取得 Key ID 上界。清空每批最多 100 条，包括操作者与目标各一次主键权限读取、一次 ID 查询、一次 Key 查询、一次停用、一次软删除，以及一次 Redis pipeline（最多 100 个 fence/删除脚本）；发生不确定提交时最多增加一次短预算回查。

单批最长 5 秒、整体最长 20 秒、顺序执行；发生故障停止，不靠扩大池或无界 goroutine 提高删除吞吐。负载验证关注目标 Key 的缓存回源，以及删除期间其他用户的 relay 尾延迟和连接池等待。

## Implementation Files

| 范围 | 计划修改/新增 |
| --- | --- |
| 路由/凭据许可 | `router/api-router.go`、`middleware/access_token_routes.go` |
| 管理接口与业务 | 新增 `controller/user_api_keys.go`、`service/user_api_keys.go`、`model/user_api_keys.go`；controller 不直接承载批次数据库流程 |
| 索引/缓存变更入口 | `model/token.go` 的索引声明；`model/token_cache.go` 仅增加管理侧可用的有超时失效能力，保留原调用兼容性 |
| 验证 | `service/security_verification.go` 与前端 `secure-verification/types.ts` 增加全部清空 scope，复用 `AdminUserContext` 与原 proof 消费 |
| 审计/i18n | 既有审计动作/目标表、动作显示、后端 YAML 和前端七语言资源 |
| 用户界面 | `users/api.ts`、`types.ts`、页面入口、行菜单及复用 Provider 的清空确认框 |

不修改 `relaykit/` 或 upstream-owned 前端鉴权文件。若实现阶段确需扩张主链范围，先更新设计，不能悄悄扩大此次功能。

## Verification Plan

先写失败测试再实现；以下为覆盖目标，实测范围和未演练项以末尾验收记录为准：

1. 授权：普通用户、员工、PAT、无菜单权限、同级/上级、root 本人拒绝；管理员对普通用户、root 对管理员允许；禁用目标可清理；只能删除指定用户的 Key。
2. Proof：缺失、过期、重放、错误 scope、替换目标用户拒绝；中途业务失败不得复用已消费 proof；身份/权限变化后重新检查。
3. 删除效果：冷热缓存和 Redis 未启用；成功后全部目标 Key 的 `ValidateUserToken` 校验拒绝，其他用户 Key 仍可用；已经停用/过期/耗尽的 Key 也可删除；历史日志与余额不变。
4. 故障：停用失败、Redis SET/DEL 部分失败/超时、软删除失败、数据库提交结果不确定；结果不能伪报成功，禁用但未删除记录可以再次处理；全部删除的已提交数量准确。
5. 并发：旧快照回填、配额增减、本人更新、两名管理员同时删除；清空时新建 Key 不被越界删除。覆盖现有 fence 窗口和超时边界，确定性控制时序，不用短 sleep 碰运气。
6. 数据量：空用户、1/100/101 条、多批次、超过当前创建上限的历史数据；服务端上界和分批范围正确，整体超时后可再次验证继续处理。
7. 前端：真实业务组件验证用户行入口、目标用户名、确认取消、验证取消、重复点击、0 条成功、正常成功及部分失败重试；断言没有 Key 列表或明文读取请求。
8. 审计与语言：成功/失败目标及计数准确，无凭据；所有语言 key 存在，浅/深主题、窄屏与键盘操作可用。
9. 验收命令：相关 Go 包测试（model/service/controller/middleware/router）及现有 Key 回归，前端相关 Vitest、`bun run typecheck`、`bun run lint`、生产构建；真实 SQLite/MySQL/PostgreSQL 行为、迁移及 `EXPLAIN` 按上述矩阵记录版本/命令/结果。测试数据隔离，禁止清空共享开发表。
10. 使用隔离用户完成浏览器一键清空 → 外部客户端用该用户旧 Key 请求被拒绝 → 其他用户 Key 仍成功 → 审计回查。只使用受控 mock 上游，按项目要求补足适用链路验收，不调用付费模型。

## Security References and Known Limits

本次查阅的稳定标准为 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)。设计采用 [V8 Authorization](https://raw.githubusercontent.com/OWASP/ASVS/v5.0.0/5.0/en/0x17-V8-Authorization.md) 的服务端功能/对象/字段权限边界，以及 [V7 Session Management](https://raw.githubusercontent.com/OWASP/ASVS/v5.0.0/5.0/en/0x16-V7-Session-Management.md) 的敏感操作再次验证原则；API Key 与 dashboard session 分别管理，不将两者等同。

验证交互参考 [Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#require-re-authentication-for-sensitive-features)，审计脱敏参考 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html#logging-sessions-life-cycle-monitoring-creation-usage-and-destruction-of-session-ids)。这些是设计依据，不代表整个现有认证系统已完成 ASVS 认证。

已知边界：不终止在途请求；不撤销 PAT/网页登录会话；不防止仍能登录账号的人重新创建 Key；现有有限期 fence 的极端故障边界如上。未对真实生产 schema 进行索引上线演练，未部署。

## Implementation and Acceptance Results (2026-11-16)

已实现用户行“清空 API Key”入口、共享确认框和安全验证、仅会话可用的管理员接口、严格角色层级、分批停用/缓存失效/软删除、部分完成进度及重试、审计动作，以及前端七语言和后端三语言文案。失败 Mutation 抛出标准服务端错误，保留已删除数量；下一次点击重新获取 proof。前端五个 upstream-owned 鉴权文件、relay 和 relaykit 本次均未修改。

测试子进程使用隔离数据库，未截断共享表。真实库版本为 MySQL **26.10.0**、PostgreSQL **18.7**、Redis **7.4.11**；SQLite 用例报告 **3.50.4**。使用项目 Go/Bun 工具链执行。

| 验收 | 命令及结果 |
| --- | --- |
| MySQL/PostgreSQL 核心功能 | 分别设置隔离 `SQL_DSN`、`LOG_SQL_DSN`、`TEST_SECURITY_DIALECT`、`TEST_<DIALECT>_DSN` 和专用 `REDIS_CONN_STRING`，运行 `go test ./model ./controller -run 'Test(UserAPIKeys\|AdminClearUserAPIKeys)' -count=1`，通过。覆盖 0/1/100/101、上界外新 Key、其他用户、Redis 不可用后重试、DB 删除失败及取消、角色复核、部分完成与新 proof 重试、旧快照/额度增量/旧结构更新不可复活 Key。 |
| SQLite | 无外部 DSN，运行 `go test ./controller -run 'Test(UserAPIKeys\|AdminClearUserAPIKeys)' -count=1`，通过。 |
| 迁移及 EXPLAIN | 两种真实数据库分别运行 `TestUserAPIKeysMigrationAndQueryPlans`，新建结构、去除新索引的旧结构升级，以及之后连续两次 AutoMigrate 均通过，重复迁移无 DDL。10000 条、其中 9000 条软删除的样本，上界与批次 ID 查询均使用新复合索引。MySQL 为 covering index lookup；PostgreSQL 上界为 backward index-only scan，批次为该索引的 bitmap scan 加有限排序。全新未删除样本的 PostgreSQL 也可能选择已有 user_id 索引，不强制 hint。 |
| 既有管理验证回归 | 两库运行 `go test ./model ./controller -run 'Test.*(AdminUser\|Verification\|Token_Cache\|Token_Delete\|BatchDeleteTokens\|AuthBearer)' -count=1`，通过。service 包同一筛选无匹配用例，不计作覆盖。 |
| 路由及 PAT | `go test ./router ./middleware -run 'Test.*(AccessToken\|UserAPIKeys\|SecurityProof)' -count=1`，通过；新路由及 proof scope 均明确 session-only。 |
| React 真实组件 | `bun run test src/features/users/components/__tests__/step-up-verification.test.tsx`：10 项通过；包括无列表请求、目标绑定、取消、重复点击禁用、角色入口、部分失败重新验证。审计本地化现有测试另有 3 项通过。 |
| 类型、lint、构建 | `bun run typecheck` 被仓库现有 `bun check -b` 脚本错误阻挡；直接运行 `bunx --no-install tsgo -b` 通过。`bun run lint` 无错误，有既有警告。`bun run build`、`go build -o <临时目录>/new-api.exe .` 通过。未修改 package.json 修复无关脚本。 |
| 语言 | 使用临时 `add-missing-keys.mjs` 写入七语言并运行 `bun run i18n:sync`；新文案全部覆盖，临时写入脚本已清理。 |
| 真实浏览器端到端 | Chrome **157.0.8103.51**，当前生产构建 + 当前 Go 后端 + 隔离 SQLite + 专用 Redis + 本地 OpenAI mock。先用目标/其他用户 Key 各完成一次模型请求预热缓存，再网页登录 → 用户行 → 确认 → 密码验证 → 清空。返回删除 1 条；旧 Key 的 `/v1/chat/completions` 返回 **401** 且未到达 mock；其他用户仍 **200**；有 1 条成功审计且不含 Key；没有 `/api/token` 列表请求。桌面浅色及 430px 深色确认框已查看，无横向溢出。 |

本地复现脚本和证据位于 `_local/user-api-keys-e2e/`，真实数据库测试日志位于系统临时目录 `user-api-key-tests/`；不含可复用生产凭据。浏览器使用随机隔离测试凭据，未调用付费上游。

另在两种真实数据库上运行 `TestUserAPIKeysConcurrentDeletion` 和 `TestAdminClearUserAPIKeysProofAndIsolation`，验证并发清空只累计删除一次，以及新 scope 的过期 proof 被拒绝，均通过。

未演练：Redis 恢复旧快照、进程暂停超过 fence、真实网络断连恰逢数据库提交的结果不明、生产规模建索引的锁/I/O。回归采用确定性故障注入验证删除失败保留禁用记录及重试，未把这些极端故障宣称为已覆盖。上线前仍需按生产 schema 和版本进行迁移演练。
