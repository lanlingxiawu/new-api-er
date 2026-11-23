# 连续登录失败限制

状态：用户确认实施后已完成代码，验证结果见文末。日期：2026-11-16。

用户已确认默认连续失败 **5 次**、次数可配置，并随后指示「实现」。完整登录成功后清零，锁定后由管理员解除并提示联系销售。

## Goals and Scope

- 在系统设置的「认证」页增加「登录失败保护」开关，默认关闭，最大连续失败次数默认 5，允许整数 1–100。
- 开启后，同一账户的密码错误，以及已通过主认证的登录流程中的 TOTP/备用码错误，共用连续失败计数。达到阈值后，禁止该账户通过任何入口建立新的后台登录会话。
- 只在完整登录会话创建成功时清零；密码正确但仍在等待二次验证不算成功。计数不按 IP 分开，也不因等待一段时间自动清零。
- 锁定长期有效，管理员解除后计数归零。改密、找回密码、修改邮箱、切换登录方式均不解除此限制。
- 增加用户管理页的「登录受限」标记和「解除登录限制」操作，方便销售核实后交由有权限的管理员处理。不新增销售角色或自动联系销售的外部消息。
- 保护范围为所有非 Root 账户。Root 保留现有认证、MFA 和限流，但免于本功能自动锁定，作为管理员账户受限后的恢复入口。
- 本功能只限制新登录。已有会话及其刷新、API Key、PAT、余额和中继请求不受此限制；账户全面禁用继续使用原有管理操作。

## Existing Logic and Related-Path Findings

以下为本次读取当前工作区代码确认的事实，不能用旧设计文档替代：

| 路径 | 当前行为与方案衔接 |
| --- | --- |
| `router/api-router.go` 的 user 登录路由 | 密码、2FA、统一 verify、独立 Passkey 和登录二次验证 Passkey 分别有路由；保留现有 CriticalRateLimit、请求体限制、DisableCache 和密码入口的 Turnstile。 |
| `controller/user.go:Login` → `model/user.go:ValidateAndFill` | 输入可为用户名或邮箱，用户名经 TrimSpace，以 `username = ? OR email = ?` 查真实用户，然后比较密码和状态；当前不存在密码登录的账户级连续失败锁定。计数必须使用查询得到的用户 ID，沿用现有匹配语义，不另做邮箱大小写规则。 |
| `controller/user.go:setupLogin` → `service/login_verification.go:StartLoginVerification` | 主认证成功通常先生成二次验证流程，不能在这里清零。该流程保存用户 ID、认证版本和登录方式。 |
| `controller/login_verification.go`、`controller/twofa.go:Verify2FALogin` | 旧 2FA 路由委托 `VerifyLogin`；统一服务 `VerifyLoginCode` 调用 `VerifyTwoFactorCode` 后完成登录，可在登录专用调用处统计一次明确的验证码错误。 |
| `service/security_verification.go:VerifyTwoFactorCode`、`model/twofa.go` | 已有独立的 2FA 临时锁定；该校验同时用于登录和已登录敏感操作，不能在共享校验函数里追加本功能计数，否则敏感操作失败会误锁登录。 |
| `controller/passkey.go:PasskeyLoginFinish` | 验证 Passkey 后直接进入 `setupLoginAtAuthVersion`，绕过 `setupLogin`。必须覆盖最终会话签发。 |
| `controller/oauth.go:handleOAuthLogin`、`controller/wechat.go:WeChatAuth` | 第三方身份确认并映射本地用户后进入 `setupLogin`。第三方失败通常没有可信本地用户 ID，不能凭请求字段给某账户记失败。 |
| `service/auth_session.go:createLoginSession` → `model/user_session.go:CreateUserSession` | 直接登录路径检查用户缓存、认证版本、会话数量/签发频率后创建会话。新增登录专用受保护创建入口；原通用会话存储接口不增加隐式登录策略。 |
| `model/login_verification.go:CreateUserSessionFromLoginFlow` | 二次验证通过后，在消费 flow 的事务中检查用户/认证版本、会话限额，并调用 `createUserSessionWithTx`。这是另一个必须覆盖的最终提交入口。 |
| `controller/user.go:ManageUser`、`middleware/auth.go:TokenAuth` | 原 `disable` 写 `users.status`，且状态用于中继 token 鉴权。不能复用它实现仅限制登录；新增状态独立保存。 |
| `controller/user.go:GetAllUsers/SearchUsers/GetUser` | 用户列表与搜索有独立返回路径，管理 PAT 另走 `listAdminTokenUsers`。只在浏览器管理员视图批量补充当前页的登录限制信息，保持现有分页和 PAT 数据边界。 |
| `controller/user.go:ManageUser`、`service/security_verification.go` | 管理操作要求目标角色校验和绑定 `{user_id, action}` 的二次验证凭证；动作有显式白名单。解除限制复用这套机制，但独立分支返回，不能落入更新用户/失效令牌缓存的公共尾部。 |
| `model/config_group.go:SaveConfigGroup`、`model/option.go`、`service/settingsaccess` | 配置模块必须注册并加入组保存/字段/权限白名单，事务保存 `options` 后发布快照。节点通过 `SyncOptions` 周期加载，不存在跨节点瞬时生效保证。 |
| `web/src/features/system-settings/auth/` | 使用 `SettingsSection`、`SettingsForm`、`SettingsSwitchItem`、统一保存确认和页底按钮。新分区复用这些布局及现有整组保存 API。 |
| `web/src/features/auth/api.ts`、`web/src/lib/secure-verification.ts` | 登录业务失败及 `authResult` 能显示服务端已脱敏的 message。优先沿用现有展示链，不修改上游所有的 `server-error-message.ts`、会话或 HTTP 基础文件。 |
| `model/audit_security_capture.go` | `CaptureSecurityEvent` 使用有界、非阻塞队列；安全采集开关会影响是否记录事件，不能用审计事件或聚合结果决定是否锁定。 |

已检查相关测试：`controller/auth_flow_upstream_cases_test.go` 的二次验证、重放和默认关闭时登录查询次数约束，`controller/auth_session_upstream_cases_test.go` 的会话限额拒绝不记录登录成功，以及 `model/user_test.go` 的凭据契约。保留上游测试原文，新增 fork 功能用例。

相关文档：`user-session-hot-config.md` 提供快照模式，但其中仅 Root 保存的历史描述与当前 scoped Admin 路由不同，本设计以当前路由为准。`password-reset-security-audit-20261113.md` 记录独立的恢复流程问题，本功能不宣称修复这些问题；恢复流程不能清除此处新增的锁定记录。

## Data Flow

### 密码与二次验证失败

1. 先经过现有限流、验证码和参数校验，解密密码失败/参数错误不计入账户次数。
2. 密码校验区分「真实、启用、非 Root、有密码账户的密码不匹配」与不存在、已停用、无密码、数据库错误。使用明确内部结果，不用 `ErrInvalidCredentials` 的所有情况笼统计数；保持外部通用错误。
3. 开关开启时，只对上一步确认的账户，在专用表的短事务内初始化行、锁定该行、递增计数并判定阈值。已锁定行保持原锁定时间，停止递增。
4. 2FA 仅在有效登录 flow 下，`VerifyLoginCode` 得到 `ErrVerificationFailed` 时累计一次。无效/过期/重放 flow、已有 2FA 临时锁定、数据库故障不累计；TOTP 与备用码同一次提交不会重复计数。
5. 事务提交后返回失败，首次进入锁定状态时提交脱敏安全事件。计数不依赖日志写入成功。

独立 Passkey 的失败往往发生在身份确认之前，OAuth/微信的超时或拒绝也不是已确认账户的错误密码，因此不新增账户失败计数。登录二次验证的 Passkey 失败保留现有 challenge 单次消费及错误处理。本功能的计数来源明确限定为密码和登录 TOTP/备用码；**所有成功认证入口仍须接受最终锁定检查**。

### 登录成功与锁定拦截

1. 主认证通过后，`StartLoginVerification` 在开启时检查限制，避免给已锁账户继续发二次验证 flow；验证 flow 的读取入口也检查当前限制。
2. 独立 Passkey 直接登录、密码/第三方登录，以及二次验证完成，在最终创建会话的事务内重新检查保护行。
3. 在同一事务中执行「限制检查 → 创建会话 → 清零未锁定账户的失败计数」。锁定行绝不因凭据正确而自动清零。现有会话限额/flow 校验失败时事务回滚，计数不清零。
4. 事务提交后继续现有 session cache 发布、token/cookie 返回和成功审计。若后续签名失败，沿用会话撤销逻辑并返回系统错误；它不是凭据失败。计数的成功边界是认证已完成且会话已提交，不能以客户端是否收到 HTTP 响应判定。
5. 锁定期间不能利用已创建的二次验证 flow、其他登录方式或找回密码签发新会话。管理员解除后仍需完成正常认证，不签发任何绕过凭据。

### 管理员解除

1. 当前页用户列表一次 `WHERE user_id IN (...)` 读取保护状态；详情读取单条。缺行代表未锁定。
2. 行菜单显示「解除登录限制」，复用现有 `requestVerification`，凭证绑定 `admin.user.manage` / `{user_id, action: "unlock_login"}`。
3. `ManageUser` 的专用分支验证浏览器会话、用户管理权限、目标角色和 proof，再以一条原子 UPDATE 清零计数及锁定时间。拒绝 PAT 调用该新动作，不扩张管理员令牌权限。目标查询显式使用 `id = ?`，并处理不存在和数据库错误，避免零 ID 命中首个用户或查询失败后沿用请求 ID。
4. 返回成功后刷新当前页，保留分页/筛选状态，记录操作者、目标、是否发生变更和二次验证方式。重复解除幂等。目标已删除则拒绝。
5. 解除只修改保护行，不写 `users.status`、`auth_version`、密码、额度，不撤销 API Key/PAT 或改动其缓存。原账户已禁用时仍然禁用，原 2FA 临时锁定仍按已有机制恢复。

## API Contracts and Authorization

不新增公开 endpoint。现有登录接口保持公开，因为它们就是取得登录身份的入口；既有限流、no-store、flow 验证继续生效。

| 接口 | 合同变化 | 权限 |
| --- | --- | --- |
| `GET /api/option/?scope=auth.login-protection` | 返回本分区设置 | 既有 AdminAuth + 对应设置查看权限 |
| `PUT /api/option/group` | `{scope:"auth.login-protection", module:"login_protection_setting", values:{enabled:"true", max_failures:"5"}}` | AdminAuth + 对应设置编辑权限；Root 沿用既有授权 |
| `GET /api/user/`、`GET /api/user/search`、`GET /api/user/:id` | 浏览器管理员视图附加 `login_protection:{locked:boolean, failed_attempts:number, locked_at:number}`；已有分页结构不变 | 既有用户管理权限及详情目标角色限制 |
| `POST /api/user/manage` | 新动作 `{id:123, action:"unlock_login"}`，凭证使用 `X-Security-Proof` 请求头；成功沿用 `{success:true,message:""}` | 用户管理 AdminAuth + 浏览器会话 + 目标角色限制 + 绑定该目标/动作的一次性 proof |
| 现有登录及验证接口 | 锁定失败沿用 HTTP 200 / `{success:false,message:"…"}`，不发 refresh cookie 或 access token | 公开认证接口内服务端强制检查 |

普通管理员只能解除低于自身角色的用户；Root 可解除管理员。销售通过已有流程协调管理员，不通过员工/客户接口获取新权限。前端 action 类型、后端 proof 白名单和授权分派须同步扩展，并回归未知动作/PAT 默认拒绝。

## Data Model, Migration and Index Design

新增主库表 `user_login_protections`，不增加 `users` 列，也不写日志库 schema。

| 列 | 类型/约束 | 含义 |
| --- | --- | --- |
| `user_id` | 与 users.id 一致的整数，主键，`autoIncrement:false` | 唯一账户；最多一行 |
| `failed_attempts` | 非空整数，默认 0 | 已确认失败次数，递增上限为配置上限 100 |
| `locked_at` | bigint，默认 0 | 0 为未锁定，否则为锁定 Unix 秒 |
| `last_failed_at` | bigint，默认 0 | 最近一次已计数失败时间 |
| `updated_at` | bigint | 最近一次修改时间 |

- 以主键覆盖单账户检查/更新、当前页批量查询和删除；不提供全表统计或按锁定状态扫描的大列表。本表按账户数有界，不按请求数增长。
- 使用 GORM `OnConflict DoNothing` 初始化行及 `lockForUpdate(tx)` 锁定行；SQLite 路径依赖数据库事务串行写入并对忙/冲突做有限重试，超过预算返回可重试错误，不静默漏计。
- MySQL/PostgreSQL 的锁竞争只发生在同一保护行，不新增 users 行锁。流程已有的 user/auth-flow 锁先于保护行；保护逻辑禁止再反向锁 users/auth_flows，避免锁序倒置。
- 新表加入 `model/main.go:migrateDB`。无历史失败数据回填，旧用户从 0 开始；非 Root 用户首次计数/成功登录需要行时按需建立。
- 硬删除用户时接入 `deleteUserAuthenticationData` 删除该行；软删除保留，恢复账户后原限制仍在。不存在的账号不建行。与删除并发时复核目标仍存在，不能绕过既有删除/认证版本检查。
- 迁移仅建表/主键，SQLite、MySQL >=5.7.8、PostgreSQL >=9.6 均支持。实施验收要检查新建和旧版本升级、第二次启动无 DDL，以及主键访问的 EXPLAIN。

## Configuration and Lifecycle

新增 `setting/operation_setting/login_protection_setting.go`，注册 `login_protection_setting`。复用 `UserSessionSetting` 的草稿锁与原子快照模式，每次登录/验证操作读取一份快照。

| 参数 | 默认 | 校验 |
| --- | --- | --- |
| `enabled` | false | 布尔值 |
| `max_failures` | 5 | 整数 1–100；拒绝 0、负数、小数、溢出和未知字段 |

- `SaveConfigGroup` 校验并事务保存两项配置，提交后发布。`model/option.go` 的加载/发布及单项更新路径也必须采用相同校验，防止绕过。无新增环境变量。
- 新增 `auth.login-protection` 设置权限范围和前端分区登记。保存采用整组请求，遵循现有 `data.applied` 反馈，刷新设置查询。
- 默认关闭：登录路径不访问新表，不统计新失败，不影响既有登录查询次数约束。
- 关闭表示暂停统计和拦截，保留已存计数/锁定；重新开启后恢复。关闭期间的成功登录也不访问/修改该表。设置页明确写出这项行为，管理员可随时单独解除锁定。
- 修改阈值不扫描全表，不自动解除已锁账户；未锁账户在下一次失败时按新阈值判断，下一次完整成功登录仍清零。
- 节点重启不丢失状态。计数/锁定使用共享主库，跨节点一致；设置发布按已有 SyncOptions 周期收敛。部署/开关验收必须等所有节点加载一致后再验证，不能把本节点保存成功称为全节点即时生效。

## Error Handling and User Interaction

- 已确认主认证/有效登录 flow 的锁定提示：**「该账户因连续登录失败已被限制登录，请联系销售处理。」**
- 尚未确认身份的密码错误，包括不存在账户、错误密码、已锁账户的错误密码，开关开启时统一提示：**「账号或密码错误，或登录已被限制。如持续无法登录，请联系销售处理。」** 达到阈值的那次错误也使用该提示，不向匿名请求泄露账号是否存在或剩余次数。关闭时保持原行为。
- 不以不同的 HTTP 状态、响应字段或公开查询接口向匿名用户暴露锁定状态。正确主凭据确认后允许显示明确的联系销售提示。
- 数据库/事务异常先 `logger.LogError(c, ...)`，再通过既有 `common.ApiErrorI18n` 等错误帮助函数输出脱敏提示；保护开启时无法确定状态则拒绝本次新登录。不能把系统错误累加为密码错误，也不能失败放行新会话。
- 新的锁定业务错误在 `writeSecurityOperationError`、`writeAuthSessionError` 分派前统一转换为本功能 i18n 提示，不被未知错误映射成泛化 500。
- 后端实际使用 `i18n/locales/{en,zh-CN,zh-TW}.yaml`，三个语言均补齐消息；设置页、用户列表、解除操作覆盖全部七个前端语言。沿用当前登录的服务端本地化 message 展示，不编辑上游保护文件。
- 设置保存和解除时显示 loading、防止重复提交；失败保留原状态，成功刷新数据。用户列表使用现有 Badge 与行菜单，解除复用安全验证弹窗，不另造危险操作区域。

## Main Chain Impact

此功能只接入后台登录及管理接口，不接入 AI relay handler/middleware。Relay goroutine 新增同步工作为 **0**，新增 DB/Redis 调用为 **0**，不检查本表、不失效用户/令牌中继缓存。

登录接口中状态读写同步完成，因为会话签发必须服从已经提交的锁定决定；只有审计沿用现有队列异步写入，不把安全判定延迟到后台。

## Shared Resource Audit and Concurrency Analysis

| 资源 | 与中继关系 | 处理 |
| --- | --- | --- |
| `user_login_protections` | 独立新表，中继不访问 | 主键点查、单账户短事务；bcrypt、Passkey、OAuth 外部请求均在锁外完成 |
| `users` / 用户缓存 | 既有登录和中继都读取 | 不为本功能写 status/auth_version，不增加行锁或缓存字段；保护事务内按主键只读身份状态和认证版本，避免处理已删除/停用/过期身份 |
| `user_sessions` / `auth_flows` | 现有后台认证资源 | 在既有最终创建/消费事务增加保护检查，保留单次消费与会话限额 |
| `options` / 配置草稿锁 | 全项目配置资源 | 仅管理保存/周期加载访问；登录只读本模块原子快照，relay 不读它 |
| 主库连接池 | 与中继共用 | 不额外建连接池；沿用匿名限流与请求超时，新保护操作使用短期限上下文（上限 2 秒）；禁止无限重试、批量扫描和持锁外部 I/O |
| Redis | 无新 key/namespace | 不采用 Redis-only 锁定，避免缓存清理/重启意外解锁；既有登录会话缓存行为保留 |
| 安全审计队列/日志库 | 复用登录审计设施 | 有界非阻塞入队，失败只影响可观察性，不影响已持久化限制 |

新增成本按**后台登录**计算：明确失败包含初始化尝试、主键锁定读取、用户状态复核、状态更新各一次；最终登录提交增加同类保护行操作。管理员列表每页一次批量状态查询，不做 N+1；解除为一条原子 UPDATE。功能关闭时登录新增 SQL 为 0。SQLite 独立保护事务在 2 秒总预算内最多尝试 5 次，仅重试 busy/locked，其他错误直接返回；登录 flow 的既有消费事务保留原重试规则，失败时整体回滚。

在 30k–100k RPM 的 AI 请求负载下，中继每请求新增 DB/Redis/锁/协程成本均为 0。仍需在验收中同时施加受既有限流约束的登录失败负载，观察共享数据库池等待和 relay 延迟；不能因不改 relay 文件就忽略共享连接池影响。保护只使用现有请求 goroutine，不创建 per-request 后台协程。

## Security References and Residual Risks

- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)：账户维度计数、统一失败响应及锁定导致拒绝服务的风险。
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)：服务端会话控制、新认证会话及日志凭据脱敏。
- [OWASP ASVS](https://asvs.owasp.org/)：本次查阅的稳定版本为 5.0.0，作为认证与会话相关验收依据，不声称本次改动覆盖全标准。

永久人工解除的策略可能被恶意错误密码用于锁定已知账号。沿用现有限流/Turnstile，保留 Root 恢复和管理员解除入口；不假定 Turnstile 在所有部署已开启。明确提示只在主认证后出现，匿名统一提示仍提供联系销售的操作建议。已有会话继续可用是本功能的产品范围，不等同于对已泄露会话的封禁。

密码锁定使用现有 CaptureSecurityEvent，登录验证码锁定使用同一有界队列的 QueueSecurityAudit，管理员解除使用用户管理审计；记录身份引用、触发原因、动作和结果，不写密码、验证码、恢复码、flow/proof token、cookie 或 bearer token。安全采集关闭或队列满时可能缺失锁定审计，锁定状态和后台标记仍准确；不另建可靠消息投递子系统。

## Implementation Files

- 新增：`setting/operation_setting/login_protection_setting.go`、`model/login_protection.go`，以及前端 `auth/login-protection-section.tsx`。服务层直接组合模型能力，没有另建仅转发的 service 文件。
- 配置接线：`model/config_group.go`、`model/option.go`、`controller/option.go`、`service/settingsaccess/scopes.go`、前端设置 types/defaults/section registry 与整组保存的模块类型。
- 登录接线：`model/user.go` 的凭据失败分类，`controller/user.go:Login`，`service/auth_session.go` 的直接会话创建，`service/login_verification.go` 和 `model/login_verification.go` 的 flow 路径，控制器两种错误输出入口。保留通用 `ValidateAndFill` 对外原有错误契约，新增内部密码不匹配信息不暴露给客户端。
- 生命周期：`model/main.go` 迁移、`model/user.go:deleteUserAuthenticationData` 硬删除清理。
- 管理：用户列表/详情响应组合及 `ManageUser` 独立解除分支、`service/security_verification.go` 动作绑定/授权、前端 users 类型/API/状态列/行菜单与 secure-verification 动作类型。
- i18n：后端 en/zh-CN/zh-TW YAML、新增消息常量、前端七个 locale 与静态键；通过翻译脚本及 `bun run i18n:sync` 维护前端文案，保留当前工作区已有修改。

## Validation Plan

实施前先写行为测试，复用现有夹具，不修改上游所有的测试断言。

1. 默认关闭与 Root 豁免；设置非法边界 0/101/负数/小数/未知字段，合法 1/5/100；保存权限、事务失败不发布、重启加载及多节点配置收敛。
2. 前 4 次失败不锁，第 5 次锁，第 6 次保持；成功清零；用户名/邮箱混合、不同 IP 和不同节点共用计数；用户间隔离。
3. 仅主认证成功、会话数量超限、flow 过期/重放/版本变化、数据库错误不清零；无效账号/停用账号/加密参数错误不建保护记录。
4. 登录 TOTP/备用码每次只累计一次；已登录敏感操作验证不计数；2FA 临时锁定与新的人工解除状态互不替代。
5. 已锁账户通过密码、独立 Passkey、二次验证 Passkey/TOTP、OAuth/微信全部不能得到新会话/cookie；重置密码不解除；浏览器取消及供应商失败不累计。
6. 并发失败不丢失更新；失败达到阈值与成功签发竞争，按保护行事务顺序决定，锁定提交之后不再签发新会话；解除与失败竞争可解释且无死锁。用并发屏障，不用短 sleep 断言。
7. 管理员只解除允许目标，普通用户/PAT/越权动作被拒绝；缺失/过期/重用/错目标 proof 均失败；重复解除幂等；未顺带启用已禁用用户、修改令牌或清除 MFA。
8. 用户列表、搜索、详情状态一致，当前页批量查询且分页总数不变；UI 保存/解除的 loading、错误和刷新行为及七语文案；匿名响应不泄露账户存在/次数。
9. SQLite、真实 MySQL、真实 PostgreSQL：新库迁移、旧版本升级、第二次启动无 DDL、并发计数/解锁、硬删除清理和 EXPLAIN。已查阅 `docs/production-environment.md`（主库 MySQL、日志库 PostgreSQL，确切生产版本未记录）与 `docs/release-checklist.md`；实施时记录实际测试版本与命令，清理仅限测试行。
10. 运行受影响 Go 包测试、前端相关 Vitest、`bun run typecheck` 和 `bun run lint`。验证保护前后有效模型 token 鉴权及 mock 中继不变；不访问付费上游。对共享池执行隔离的并发登录/relay 验证。

## Implementation Verification — 2026-11-16

使用本次任务创建的独立 Docker 数据库和每个用例独立的数据库名，没有连接生产库或修改真实账户。测试版本为 SQLite 3.50.4、MySQL 9.7.4、PostgreSQL 18.7；MySQL/PostgreSQL DSN 只通过环境变量传入，没有写入本文或提交文件。

- `TEST_SECURITY_DIALECT=mysql/postgres go test ./controller -run '^TestLoginProtection' -count=1 -v`：验证同一账户用户名/邮箱共享计数、第五次锁定、成功清零、关闭/重开保留状态、Root 豁免、现有会话保留、TOTP 与第三方登录入口、过期认证版本、失败事务不清零、并发失败和成功竞争、管理权限和绑定凭证。独立 SQLite 用例运行同一套行为断言。
- 迁移用例先使用既有用户/会话表结构，再加入新表；二次 AutoMigrate 捕获 SQL，确认本表没有 CREATE/ALTER/CREATE INDEX。三种数据库对 `user_id` 点查执行 EXPLAIN，验证主键访问。没有改变 `users` 或日志表的结构。
- 受影响的 model、service、service/settingsaccess、service/authz、setting/operation_setting、i18n 包完整回归通过。controller 的登录保护及相关管理员/认证用例单独回归；完整 controller 回归结果和对照见后续记录。
- 前端设置及用户管理、登录验证码、审计本地化共 8 个测试文件、66 个用例通过；`bun run typecheck`、`bun run lint` 通过，lint 保留工作区既有警告。使用本机已安装 Bun 1.4.4；系统默认旧版 Bun 不支持本项目的 `bun check`。
- 新表只在登录/管理路径访问，未修改 relay handler、relay 鉴权、用户缓存状态或 API Token 缓存字段。

完整 controller 回归有两项既有失败：`TestAIAuditSQLiteFixtureRestoresTokenLookup` 和 `TestAPITokenAuditDatabaseMatrix`，均为 token 审计的 `configuration_values_stay_private` 断言。通过 Go `-overlay` 将本功能改动替换为修改前内容，在同样独立 MySQL 主库/PostgreSQL 日志库配置下重跑这两个用例，两项失败均复现。对照保留了同时发生的访问令牌功能改动及其消息常量，以保证工作区可编译；没有修改审计实现或弱化断言。因此本功能专项验证通过，不能声称工作区完整回归全绿。详细命令和结果保存在本机 `_local/login-protection/`，其中测试连接凭据文件仅本机保留，不应提交。

最终专项命令：`go test ./controller -run '^(TestLoginProtection|TestAdminUser|TestAuth|TestLogin|TestPasskey)' -count=1`。前端命令：`bun run test src/features/system-settings/auth/__tests__/login-protection.test.tsx src/features/users/components/__tests__ src/features/auth/otp/__tests__/login-verification.test.tsx src/features/usage-logs/audit/lib/audit-localization.test.tsx`；随后执行 `bun run typecheck`、`bun run lint`。MySQL EXPLAIN 使用 `PRIMARY`/`const`，PostgreSQL 使用 `user_login_protections_pkey`；二次迁移未产生本表 DDL。

尚未执行部署、生产 schema 演练、实际旧版本二进制升级/重启、多节点配置传播或 30k–100k RPM 混合负载测试；不能据本地数据库测试宣称这些验收已完成。生产数据库确切版本仍以 `docs/production-environment.md` 中待核实事项为准。
