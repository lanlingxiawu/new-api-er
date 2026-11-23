# 个人安全事件只读视图

日期：2026-11-18。用户已在权限核查方案后确认“可以，就是按照这个来”。本文记录该已确认范围的实现合同：本人安全事件和处理结果只读展示；账户安全操作沿用已有入口。员工客户权限、管理员权限和监控配置不扩大。

2026-11-22 后续实现：[目标账户关联](audit-login-target-account.md) 增加具有服务端目标 ID/角色证明的失败登录、IP 滥用及匿名网络单账户告警。列表分别查询 actor 和 target 索引后归并，详情逐事件核验目标；个人 DTO 和只读权限不变。以下“匿名失败不归属”仍适用于只有尝试文本、没有可信目标快照的记录；跨账户喷洒继续不可见。

## Goals and Scope

审计日志页新增“我的安全”页签，普通用户、员工、管理员及 Root 均可查看属于自己账号的安全摘要。全局审计和管理安全监控继续使用原权限。个人视图不提供标记误报、关闭、指派、删除、导出或后台设置。

提供到现有“安全与访问”和“API 密钥”页面的入口，前者已有密码、登录会话与访问令牌管理。只复用既有交互，不新增安全操作或降低二次验证要求。

## Existing Logic and Related-Path Findings

- `web/src/features/usage-logs/audit/index.tsx` 将无 `audit.read` 的账号固定为本人日志；管理安全页还要求 `audit_security.read`。新增独立个人 scope，不能通过给普通用户授予管理员权限实现。
- `router/api-router.go` 的 `/audit/security` 组由 `AdminAuth + audit.read + audit_security.read` 保护，管理权限独立。个人接口放入独立 `UserAuth` 组，并在 PAT 路由表与控制器要求 dashboard 会话。
- `service/auditsecurity/customer_access.go` 已有可信账号规则版本、旧快照完整性核验和分页后身份过滤。将账号归属判断复用给个人读取；员工分支仍只认可普通客户角色。
- `rules.go/Normalize` 将登录、匿名相关认证证据设为 Root 可见；`store.go` 对可信账号规则按 actor ID 和角色隔离 episode。不能直接按可见级别或 IP/用户名推断本人归属。个人读取只认可记录中的本人账号和已知角色；已匹配旧刷新凭据的 `session_reuse` 保留角色未知的特定例外。
- `GetCustomerAlert` 会返回员工备注和部分管理状态。个人 DTO 独立投影，不能直接转发客户或管理员 DTO。只返回本账号事件和本账号实际处置回执，不返回调查备注、其他账号证据、通知地址、负责人或规则阈值。
- `model/audit_query.go` 已有独立审计连接池；`audit_security_alerts` 已有 `(actor_id,last_seen,id)` 索引，快照有 `(alert_id,id)`，动作有 `(alert_id,created_at,id)`。
- 前端复用 `DataTablePage`、`LogsFilterToolbar`、日期选择器、`Dialog`、`DetailSection`、`StatusBadge`、已有规则/状态翻译。管理详情与员工详情绑定可写 API，不能整体复用；新增个人数据组件承载只读合同。

## Data Flow and API Contracts

Dashboard Bearer 会话 → `UserAuth` → dashboard 会话检查 → 3 秒请求 context → 独立审计主库池 → 固定当前用户 ID 的只读查询 → 账号归属核验 → 白名单 DTO → 浏览器。

| 方法与接口 | 输入 | 输出 |
| --- | --- | --- |
| GET `/api/audit/self/security/alerts` | `start,end,cursor`；默认近 24 小时，最大 31 天 | `{items: SelfSecurityAlert[], next_cursor}` |
| GET `/api/audit/self/security/alerts/:id` | 正数告警 ID | `{alert,events,responses}` |

全部沿用 `{success,data,message}`；必须登录，拒绝 PAT/API key。忽略客户端 `user_id/actor_id/role` 等提权参数。普通用户、员工、管理员和 Root 的个人接口均固定本人，不能利用后两种角色读取别人的详情。无写接口。

列表固定每页 20 条，以 `last_seen DESC,id DESC` 游标分页。游标绑定当前用户及时间窗；身份过滤后收集 21 条可见记录才能返回下一页。隐藏记录不能造成提前结束，查询受总超时约束。摘要包括 ID、规则、级别、状态、首次/最近时间；不返回跨账号命中数量、阈值、内部版本或结论备注。

详情事件允许同账号可信操作者角色（1/10/100），或同账号服务端登录目标快照；操作者角色 0 的旧例外仍限已核实的 `auth.session_refresh_reuse`，新失败登录按独立目标角色核验。事件返回动作、时间、IP、成功状态。处置回执仅允许 `target_user_id=当前用户` 的账号禁用、访问令牌清理、API Key 清理，最多最近 100 条；只返回 ID、动作、结果、时间及已确认数量，不返回操作者、内部原因或恢复元数据。

## Business Logic and Edge Cases

复用现有账号规则白名单及版本；资源管理规则、匿名失败、密码喷洒和恢复请求不推断归属。当前版本须匹配账号与可信角色；旧版本必须有完整同账号快照、数量匹配且最新快照一致，最多 100 条。未来未知版本、缺失快照、混合身份、清理中的记录默认不可见。证据不是对事件的最终定性。

个人接口开放所有登录角色的本账号事件，员工客户接口仍执行当前归属、有效员工资格、客户普通角色和非员工条件。管理员完整视图的 Root 隔离继续保留。被禁用账号仍不能登录，不新增用于绕过封禁的读取接口。

## Frontend Lifecycle and Copy

现有审计页保持默认日志视图；新增“我的安全”页签。管理员可继续切换“全部”“仅自己”“安全监控”。手机上复用 `stackActionsOnMobile`，将页签放到标题下方并允许换行，避免挤压标题。列表支持时间查询、刷新、上一页/下一页和只读详情；分页位于滚动区外。账号切换时重置本地选择与分页，查询 key 包含账号 ID，使用 abort signal 和 `gcTime:0`，错误时清除可见旧数据与详情。

2026-11-23 默认时间范围修复：用户指出截止时间默认到当前时分秒会遗漏信息。前端统一使用浏览器本地时区的昨天 `00:00:00` 至今天 `23:59:59`，通过 `audit/lib/time-range.ts:defaultAuditTimeRange` 复用 `lib/time.ts:getNormalizedDateRange`，覆盖“我的安全”、本人/管理审计、管理安全监控和员工客户审计（含客户弹窗）。日期边界按日历计算，兼容跨月、跨年和夏令时。

初始加载、默认范围的搜索/刷新和重置使用上述完整自然日；同一天重新搜索/刷新仍须发出查询，跨日操作重新计算日期并回到第一页。分页固定已应用时间窗，手动选择和事件调查的精确起止时间保持不变。前端显式提交 Unix 秒，接口省略参数时的滚动 24 小时回退、30/31 天上限和权限校验不变。

本次仅调整前端参数生成和管理监控同范围重查条件；没有增加请求频率、数据库查询类型、Redis 键、共享缓存、连接池或后台任务，不触及 AI relay 主链。四组相关组件测试共 119 项通过，覆盖完整日边界、打开页面后新增记录、同日重查、跨日查询/重置、游标及自定义范围保留。生产构建和 lint 通过（lint 保留既有无关警告）；本地 Bun 1.3.13 不支持脚本中的 `bun check -b`，改用已安装的 `tsgo -b` 通过类型检查。本轮没有部署。

按用户对截图的反馈，详情采用平铺布局：概览、事件和处置结果去掉内外嵌套边框；记录之间仅保留细分隔线与留白。仍复用 `DetailSection/DetailRow`，刷新、关闭按钮固定在弹窗底部。

标题使用已确认的“我的安全”（英文 key `My security`），七种语言齐全。其余标签、空态、错误、规则和状态沿用现有文案。处置结果使用普通结果标签，不显示“到用户管理检查”等只适合管理员的操作提示。列表/详情均不包含可写按钮。

## Data Model, Configuration and Errors

无新表、列、索引、迁移、配置、通知任务或历史回填。参数错误复用现有国际化错误；他人、缺失、不可信或删除中的详情统一返回 `audit_security.not_found`；存储失败记录内部日志并返回 `audit_security.unavailable`。不存在记录与不属于本人不可被区分。

## Main Chain Impact and Shared Resource Audit

所有新增工作仅发生于 dashboard GET，不修改 relay、检测器、自动处置或计费。中继每请求新增 DB/Redis/锁/协程为 0。查询复用审计独立池，只读 `audit_security_alerts/findings/actions`，这些表不在 relay 热路径访问；不锁定 `users/tokens/channels`，不增加连接池、Redis key 或全局缓存。

查询计划应确认个人列表使用现有 actor/time 索引，详情按主键后核验本人，动作按告警/时间索引。对 SQLite、真实 MySQL/PostgreSQL 进行权限及分页回归；不涉及日志库读取或 schema 变化。

## Verification

覆盖普通用户、员工、无/有权限管理员及 Root 的本人/他人访问；匿名/PAT 拒绝；伪造 ID/参数/游标；历史角色及混合快照；匿名认证例外；跨账号证据和内部字段不泄漏；所有个人写方法不存在；原管理和员工权限回归。

前端验证入口、账号切换不串数据、查询/翻页/刷新/错误重试、详情及安全链接、无管理按钮；桌面/手机截图与实际 API 联调，类型检查、lint、构建及七语言 key 覆盖。以 [OWASP ASVS 5.0](https://asvs.owasp.org/) 和 [Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) 的服务端授权、最小权限和对象范围为依据；密码/会话操作保持既有 Authentication/Session Management 控制。

### 实现与验证结果（2026-11-18）

- 后端落点：`controller/audit_self_security.go`、`service/auditsecurity/self_access.go`，并注册两个 GET 路由和 session-only 凭据规则。`customer_access.go` 仅抽出可复用的账号归属核验，员工分支的普通客户角色边界保持原合同。
- 前端落点：`web/src/features/usage-logs/audit/self/` 与审计页入口。只有 `My security` 是新增翻译 key，七个语言文件的本轮语义增量均仅此一项，UTF-8 无 BOM。五个受保护的认证/HTTP 文件相对 HEAD 无改动；本地 `main` 不包含这些路径，不能用该旧分支作逐字节比较依据。
- SQLite 隔离夹具通过本人/他人归属、版本与历史证据、字段投影、游标及路由/session 检查。真实 MySQL **26.10.0**、PostgreSQL **18.7** 各通过 12 个顶层用例（含员工原有范围、既有索引升级/重启、个人与员工查询计划），没有跳过。每例使用独立的 `audit_test_worker_*` 临时库并自动清理，未连接生产或共享开发数据库。
- 10,050 条记录的 EXPLAIN 验证：个人列表初始页与续页保留账号/时间索引访问，处置结果使用告警范围索引；PostgreSQL 使用 `EXPLAIN (ANALYZE, BUFFERS)` 检查无全表顺序扫描。未增加 schema、索引或日志库查询。
- 相关 controller/router 检查通过，`service/authz` 全包通过。一次扩展的 middleware 全包运行曾失败，首次输出未完整保留，未据此声称整体发布验收通过；显式清空数据库环境变量后完整重跑通过，完整结果保存在 `middleware-tests.log`，其中依赖外部 DB 的既有用例按测试框架跳过。当前功能的真实 HTTP 边界另有浏览器实测覆盖。
- 前端 6 个相关测试文件 **100/100** 通过；最后的布局与边框调整后，再跑受影响的 2 个文件 **53/53** 通过。`bun x tsgo -b`、定向 oxlint、`bun run build` 通过，后端实际可执行程序构建通过。仓库 `typecheck` 脚本仍为已有的 `bun check -b`，本轮使用实际 TypeScript 检查器 `tsgo`，未改包脚本。
- 真实 Chrome + 实际后端：普通用户、员工、有审计权限管理员、无审计权限管理员及 Root 均完成登录、个人页面读取和跨账号拒绝。管理员/Root 在个人接口同样不能读取他人；匿名返回 401，个人写路径不存在；管理入口依原权限返回 200/403。核对响应无其他账号 IP、内部备注、角色、负责人、阈值和原始证据字段。
- 浏览器交互通过列表翻页、刷新/查询、7 天日期预设、账号安全及 API Key 页面跳转、详情分页、Escape 关闭与焦点返回。手机列表滚动不带动页面，分页和弹窗按钮保持可见；390×844 窄屏、844×390 横屏及七种语言的深色视图无横向溢出，标题不被页签挤压；1440×900 浅色桌面也已检查。运行期间没有页面脚本异常。

证据目录：`_local/audit-self-security-20261118/`，包含 `matrix.json`、两种真实数据库测试日志、`http-authz-tests.log`、`browser-results.json`、`visual-results.json`、检查日志和截图。最终简化样式截图为 `zh-user-detail-mobile-dark.png`、`zh-user-detail-bottom-dark.png`。测试后关闭本轮临时服务。

这些结果覆盖本功能及其关联路径，不代表整个脏工作区已完成发布验收；未部署生产，也没有进行本轮不涉及的 AI 中继压测、生产 schema 演练或全应用数据库升级验收。
