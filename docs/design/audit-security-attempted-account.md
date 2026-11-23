# 失败登录账号留存修复

2026-11-22 后续实现：[失败登录告警关联目标账户](audit-login-target-account.md) 已实现。服务端实际查到的目标 ID/角色现可独立关联本人及当前客户员工；尝试账号文本仍不是已认证操作者。以下“不关联 ID、不回填”是当时范围，可靠证据的维护修复以新文档为准；历史修复需在指定环境显式执行。

## Goals and scope

2026-11-18 用户确认修改：保存失败登录所尝试的账号，并明确区别于认证完成后的操作者。该确认承接对缺失字段和两种身份语义的说明。新界面标签仍遵守此前“新增文案先确认”的要求，译稿见 `audit-security-attempted-account-copy.json`。

只记录后续采集到的事件，不反解 HMAC、不回填历史、不根据输入用户名关联用户 ID 或执行账号处置。密码、验证码、令牌、Passkey 凭据正文均不新增留存。

2026-11-23：找回密码的提交邮箱改为和失败登录一样留存为 `attempted_account`。`auth.recovery_requested` 与 `auth.recovery_rejected` 都保存经过同样清理的地址，供管理端显示「找回账号」。它不写入操作者，不关联目标用户 ID，不改变公开响应。此前提到的旧告警仍不回填。

## Existing Logic and Related-Path Findings

以下为修改前核对的行为及本次复用的既有路径。

- `controller/user.go:Login` 在校验失败后仍持有输入 username；`model/audit_security_capture.go:CaptureSecurityEvent` 只保留 `subject_ref` HMAC，未保存供排查使用的文本。成功登录另有真实用户 ID 审计。
- `service/login_verification.go:VerifyLoginCode` 先验证服务端 AuthFlow、用户状态和 auth_version，然后验证验证码。其失败回到 `controller/secure_verification.go` 时只记录空 identifier。Passkey 的附加验证入口也已持有有效 LoginVerification；没有有效流程的失败不能推断账号。
- `model/login_verification.go` 的无凭据用户投影已经按主键读取用户；增加 username 投影无需增加查询或读取密码正文。失败账号仅传给审计，不能设置认证 context 的 id/role/identity。
- `service/auditsecurity/rules.go:Normalize` 仅接受 Facts 白名单，登录相关事件强制 Root 可见；subject_ref 用于相关性而非身份授权。
- `store.go:Apply` 在同一后台事务写告警、证据和不可变命中快照。列表不读 evidence，故给告警和命中快照各增加一个小型账号快照字段；详情、关联、JSON 导出沿用现有权限和分页。
- 普通审计已有 `AuditOther.Op.Params` JSON；风险证据已有 Facts JSON。个人安全和员工客户接口使用独立 DTO，不把匿名输入关联到客户。

## Data flow

密码失败输入 / 经服务端验证的二次验证流程账号 → 清理控制字符、格式控制字符及无效 UTF-8，最多 128 个 Unicode 字符 → `attempted_account` 与原 HMAC 并存 → 原 1024 容量非阻塞队列 → 来源审计 JSON → Normalize 再次校验动作、结果和长度 → Facts、告警及命中快照 → 已授权查询/详情/导出 → 单独的尝试账号标签。

HMAC 始终使用原始 identifier 保持原相关语义；清理和截断只作用于展示文本。多账号喷洒告警概要显示最新命中的尝试账号，各条证据和快照保留各自账号，不宣称所有事件只有该账号。更新告警时同步最新命中字段，不能遗留上次账号。

## API contracts and authorization

既有 `/api/audit/security/alerts`、详情、关联及导出响应在告警/快照/关联记录新增可选 `attempted_account`；事件保存在 `facts.attempted_account`。没有新端点、请求参数或查询条件。AdminAuth、审计权限、Root 可见性及个人/客户范围不变。登录响应、错误消息和认证决策不包含尝试账号，不新增可枚举账号存在性的信号。

验证码失败通过携带安全账号上下文、支持 `errors.Is/As` 的内部错误交给 controller；错误文本不包含账号。锁定事件也使用同一路径，保留锁定错误响应和单次失败计数。Passkey 只有成功验证流程后才传递账号上下文；缺失、过期、重放或伪造流程不猜测账号。

## Data model, migrations, and index design

- `audit_security_alerts.attempted_account`、`audit_security_findings.attempted_account`：可空 `varchar(128)`，不设默认值/唯一约束，旧行自然为空。
- MySQL 对这两个新增列明确使用 `utf8mb4`：匿名尝试可包含数据库现有账号之外的 Unicode 字符，不依赖 schema 的默认编码。迁移按 information_schema 精确检查两个列，已经是 utf8mb4 时不发 DDL；仅在需要时修改新增列，不转换整表或既有字段。SQLite/PostgreSQL 使用原生 Unicode 字符串。
- 来源审计 `other`、规范事件 `facts`、告警证据 JSON 沿用现有存储，不扩展来源日志 schema，兼容独立 PostgreSQL/ClickHouse 日志库。
- 仅展示字段，无新增 WHERE/JOIN/ORDER BY，不增加索引或用户名全表匹配；原时间、主键、游标和可见性索引继续使用。
- 新建/旧 schema 升级及第二次迁移在 SQLite、真实 MySQL 5.7、PostgreSQL 验证；确认第二次无 DDL。实际生产 schema 演练属于部署前步骤，本次不连接生产。

## Configuration, errors, and lifecycle

不新增配置，沿用 CaptureEnabled/Suspended 的审计采集控制。AI 请求 IP 的无条件留存不受影响。队列满仍按现有丢弃计数降级，审计故障不改变登录结果；数据库后台事务失败沿用现有重试与错误记录。清理、归档和导出复用告警/快照原生命周期，删除记录同时删除新增字段；无独立缓存或补写任务。

## Main Chain Impact

AI relay goroutine 无新增执行。dashboard 登录仅增加有界字符串清理和已有队列投递；二次验证在原有 users 主键查询中多取 username。告警持久化在原后台事务，不新增每请求 DB 写入。

## Shared Resource Audit / Concurrency Analysis

`users` 表与 relay 共用，但只扩展既有按主键读取的投影，无新增查询、行锁或连接。审计源表、规范事件、告警和快照写入使用原后台路径；新增两个小型字段不增加锁范围。沿用审计队列和专用有限连接池，没有新 Redis key、缓存、goroutine 或共享 worker。30k/100k RPM 下 AI 请求新增 DB/Redis 次数、锁、goroutine 均为 0；登录审计每条最多新增 128 字符的副本。

## Verification plan

先编写回归用例：存在/不存在账号的密码失败、Unicode/控制符/超长输入、采集关闭、恢复邮箱不留存、非失败登录字段丢弃、验证码错误/锁定/无效或过期流程、Passkey 已绑定流程失败；断言 actor_id/identity 不从输入构造且密码/验证码/令牌不入审计。覆盖实际队列→Normalize→Apply→列表/详情/关联/导出和 Root/普通管理员/个人边界。覆盖新建及升级迁移、第二次无 DDL、前端列表与详情及恶意文本普通渲染。

依据项目 Rule 16，核对 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)：保留通用认证错误、流程绑定/过期/单次使用控制；仅允许账号标识字段，清理不可信输入，不留存可用凭据，限制日志读取权限。

## Implementation and results

2026-11-18 用户已确认采用七语言译稿。已实现捕获、规范化、持久化和完整的管理端显示路径：

- 密码失败（含用户名不存在、邮箱作为登录名和加密密码解析失败）保存输入账号；验证码/附加 Passkey 验证失败只使用已验证的服务端流程账号。验证码达到锁定阈值时由 controller 记录失败和锁定事件，仍只计一次失败，不建立登录身份。
- `SecurityAttemptedAccount` 将控制/格式字符、行段分隔符和无效字符替换为空格，截断为最多 128 字符后去除首尾空白；HMAC 使用原始账号。Normalize 再次限制字段只能用于失败的 `auth.login_failed` / `auth.login_locked`。
- 告警概要记录最新命中账号，命中快照和事件证据分别保存各自账号。列表在操作者下单列“尝试登录账号”，详情/命中/关联及来源审计参数使用相同标签。使用已有 DetailRow、AuditDetailFields、列表单元格布局，无新增基础 UI 组件或手写弹层。
- 三库持久化及新建、升级、第二次迁移验证均通过。MySQL 特别覆盖继承 latin1 的隔离库，证明两个新增字段可以保存 128 字符的中文/表情组合。PostgreSQL 的升级测试使用明确列投影，避免测试连接在删除再重建列后复用 `SELECT *` 的旧结果结构；业务连接仍沿用既有配置。

验证环境与命令记录位于 `_local/audit-attempted-account-20261118/`：

| 检查 | 结果与证据 |
| --- | --- |
| MySQL 5.7.44、PostgreSQL 18.7 | `python .../run_matrix.py mysql/postgres`：各 34 项认证、权限、查询定向回归通过，0 跳过；`*-persistence` 追加各 4 项捕获/来源库 JSON/后台扫描/列表详情导出及迁移生命周期检查通过。所有库均由测试创建并清理。 |
| SQLite 3.50.4 | 覆盖实际非阻塞队列→来源审计 JSON→扫描→规范事实→告警/命中快照→已授权读取，及旧行空值、新建/升级和重启无 DDL。最终 `go test ./service/auditsecurity -count=1 -v -timeout=8m`：127 项通过，9 项依赖外部环境的顶层测试跳过，0 失败；详见 `backend.log` 和 `verification-summary.json`。 |
| 前端 | `bun run test src/features/usage-logs/audit src/features/customer-console/customer-audit-dialog.test.tsx`：19 文件、240 项通过；`bun run test src/i18n/locale-contract.test.ts`：7 项通过。 |
| 静态和构建 | Bun 1.4.4 `bun run typecheck`、涉及 7 个 TS/TSX 文件的 oxlint、前端生产构建和随后根 Go 构建通过。七个语言仅增加已确认的一条标签；UTF-8 无 BOM，五个受保护的认证/请求模块与修改前逐字节一致。 |
| 浏览器 | Chrome 390×844 和 1840×1000，使用本次构建及本地模拟 API；列表、详情、证据文本、浅/深主题及手机前后翻页归顶通过，无 JS 错误。恶意 HTML 按文本展示，不创建元素。截图及 `mobile-scroll.json` 已保存。 |

完整服务回归首次暴露旧 `TestMonitoringDatabaseLifecycle` 未创建读取用户名必需的 users 表，已补齐该测试库结构。MySQL 补测暴露继承列编码问题，已通过仅转换两个新增字段解决；再次迁移确认没有重复 DDL。

本次未连接生产、未部署，未运行生产 schema 演练、真实登录后的浏览器全栈流程或 ClickHouse/Redis 集成。本轮服务套件中依赖未配置外部服务的跳过项须按日志记录，不能视为通过；实际部署前仍需执行对应发布验收。旧摘要不能还原，未回填历史用户名。
