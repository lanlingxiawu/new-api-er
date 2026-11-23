# 我的客户审计日志默认展示

状态：已在本地实现，尚未部署。

“我的客户”默认展示当前员工名下全部可访问客户的记录，客户选择器作为可选筛选。

## Goals and Scope

- 进入“我的客户”立即查询并展示本地时区昨天 `00:00:00` 至今天 `23:59:59` 的操作日志；客户筛选默认显示“全部客户”，清除选择恢复全部客户。默认日期边界于 2026-11-23 按用户反馈修正，见[完整自然日修复说明](audit-self-security.md#frontend-lifecycle-and-copy)。
- 每行及详情显示该记录对应的客户账号和编号；保留现有时间、IP、请求 ID、刷新和游标分页。
- 保留已有“操作日志 / 风险记录”两个分区。切到风险记录时，同样按当前全部客户或单客户范围加载；风险详情和处理仍针对单个客户。
- 当前员工的全部可访问客户构成汇总范围，分页展示，不自动选中第一位客户，不依赖客户候选下拉已加载的 20 位客户。
- 权限范围仍为当前名下普通、非有效员工客户的原有安全投影。本人日志、管理员全局审计、客户弹窗和风险写入的权限合同继续适用。

## Existing Logic and Related-Path Findings

以下为实施前直接读取实现与测试后确认的事实，未以历史设计代替代码检查；实际改动和验收见文末。

| 位置 | 已验证逻辑与改动落点 |
| --- | --- |
| `web/src/features/usage-logs/audit/index.tsx` / `AuditLogs` | “我的客户”只向 `is_employee` 账号展示；身份变化通过组件 key 清空旧视图。本人入口调用独立 self API，不能扩大其范围。 |
| `components/employee-customer-audit.tsx` / `EmployeeCustomerAudit` | `customer` 初始为空，只渲染选择器和空态，未挂载 `CustomerAuditView`，因此没有查询日志。候选下拉按需分页、搜索并传递 AbortSignal，可继续复用。 |
| `web/src/features/customer-console/customer-audit-dialog.tsx` | 主页面和客户弹窗共享 `CustomerAuditView`。内容要求固定 `customer_user_id`；列表默认 24 小时、每页 20 条、下一页游标，查询失败隐藏旧数据，403 清缓存。需要增加明确的全部客户范围。 |
| `audit-api.ts` / `customerAuditGet` | URL 固定为 `/api/user/employee/customers/:id/...`，不存在全部客户审计接口。不能只去掉前端空态就调用现有 API。 |
| `audit-activity-columns.tsx` / `useAuditActivityColumns` | 客户名称是整张表共享的字符串，详情沿用同一字符串。汇总列表必须支持按行解析客户，避免把不同客户都标成同一个账号。 |
| `controller/employee_customer_audit.go` | 控制器检查 dashboard session，路径 `:id` 必须为正数；复用 `auditCursorTimeRange`，控制器总截止时间为 3 秒。日志接口最终返回 `ProjectAuditActivities` 的公开 DTO。 |
| `model/employee_customer_audit.go` | `RequireEmployeeCustomerAccess` 实时核验启用员工和 `users.inviter_id`，不依赖员工缓存或 `CustomerProfile`；客户当前须为普通角色且非启用员工，禁用客户仍可调查。`CustomerAuditFilter.DecodeCursor` 拒绝 `CustomerID <= 0`。 |
| `QueryCustomerAuditLogs` / `AuditLogQuery` | 按客户、时间、普通角色、非员工身份读取；有 `AdminInfo` 的历史管理记录在计入分页前排除。过滤后收集第 21 条才生成下一页游标，最后执行普通用户安全投影。 |
| `model/audit_activity.go` | `AuditActivity` 是白名单 DTO，不包含客户身份、原始路由、参数、旧值/新值。汇总接口需单独附带获授权的客户身份，不能改成返回原始 `AuditLog`。 |
| `service/auditsecurity/customer_access.go` | 风险列表还核验规则版本、触发角色和历史快照；可信 session reuse 有特定例外。详情只返回同客户证据和允许的处理历史；状态写入使用当前归属和版本检查。 |
| `service/auditsecurity/account_response.go` / `loadAlertResponses` | 处置结果按告警 ID 批量读取，并按 `TargetUserID == ActorID` 过滤。汇总列表必须保留各行真实 ActorID，不能传入同一个选中客户 ID。 |
| `customer-risk-detail.tsx`、`security/alert-status-cell.tsx` | 详情及写入按 `customerID` 路由；`AlertStatusCell` 缺少 `customerID` 会使用管理 API。汇总行必须始终传递有效客户 ID；保存后的缓存失效还须覆盖全部客户列表。 |
| `router/api-router.go`、`middleware/access_token_routes.go` | 客户路由位于已有 `UserAuth` 组，四个客户审计接口另列 session-only 规则。新增汇总 GET 也必须完整登记，不能通过 PAT/API Key 调用。 |
| `model/audit_query.go` | 审计主库、日志库、员工身份采集分别有独立池，每池最多 2 个连接。汇总查询复用前两个池，不能假设主库和日志库同库或跨库 JOIN。 |
| `model/user.go`、`model/customer.go` | 当前有单列 `inviter_id` 索引；客户候选接口采用 COUNT/OFFSET 且返回较多字段，不适合作为全体客户聚合的后端数据源。新增内部只读客户 ID 批次查询。 |

已读取现有 HTTP 权限、归属变更、隐藏记录分页、双库读取、前端客户切换/撤权测试。既有设计 `employee-customer-audit-access.md` 和 `audit-user-employee-field-visibility.md` 中“未选择客户不查询”是本次修改前的行为；本方案替代该交互，两份文档已同步更新。

## Data Flow

浏览器进入客户页签 → 汇总 GET → `UserAuth` 和 dashboard session 检查 → 独立审计主库池重新核验员工 → 主库按 ID 游标分批取得当前可访问客户 → 独立日志库池读取这些客户的活动，或主库池读取这些客户的风险 → 应用既有身份/隐私投影 → 合并全局最近记录 → 返回客户身份和 20 条记录、下一页游标。

选择具体客户后继续使用现有单客户接口；清除筛选切回汇总接口并重置分页。候选搜索只负责查找筛选项，不阻塞默认日志请求。操作日志与风险页签按需加载，不预取所有客户详情。

所有汇总请求为只读。打开风险详情、保存状态/结论/备注时，仍使用记录所属客户的原有详情和 actions API，重新核验归属，复用版本冲突和处理历史生命周期。

## API Contracts

在现有 `/api/user/employee/customers` 路由组增加两条静态 GET；继续使用组内 `UserAuth()`，在 `access_token_routes.go` 登记 `accessTokenSessionRule`，控制器再次检查 `GetSessionAuthIdentity`。该客户路由组在认证前应用既有 `DisableCache()`，包括 401 在内的响应均禁止缓存。

| 新接口 | 查询参数 | `data.items` |
| --- | --- | --- |
| GET `/api/user/employee/customers/audit` | `start,end,cursor`，可选 `request_id,ip` | `EmployeeCustomerAuditActivity[]` |
| GET `/api/user/employee/customers/security/alerts` | `start,end,cursor` | `EmployeeCustomerSecurityAlert[]` |

正常响应沿用 `{success:true,data:{items:[],next_cursor:""}}`。

- `EmployeeCustomerAuditActivity`：已有 `AuditActivity` 公开字段，额外含 `customer: {id, username, display_name}`。
- `EmployeeCustomerSecurityAlert`：已有 `CustomerSecurityAlert` 公开字段，额外含相同 `customer` 对象。
- `customer.id` 是客户用户 ID，不是客户资料副表 ID；账号名来自本次获授权客户的主库记录，不从令牌名、日志目标或前端选项推断。
- 既有 `/:id/audit`、`/:id/security/alerts` 及详情/actions 的请求和响应合同保持兼容；本人 DTO 不增加其他账号身份字段。
- 汇总接口不接受由客户端指定员工、角色或客户集合。管理专用筛选沿用现有拒绝规则；风险汇总不接受 IP/请求 ID 条件。
- 前端默认昨天至今天的完整自然日；接口省略时间参数仍回退最近 24 小时，最长 31 天；每页固定 20 条；请求 ID、IP、游标沿用现有长度和格式上限。无 COUNT/OFFSET。
- 汇总游标绑定员工 ID、汇总范围、记录类型、时间窗、IP/请求 ID、投影版本及最后返回位置。使用独立的汇总游标类型，不把旧 `CustomerAuditFilter` 的正数客户校验放宽为“0 代表任意范围”。单客户和汇总、不同员工或不同筛选的游标互不通用。
- 未认证交由中间件返回 401；无有效员工/dashboard session 为 403；参数错误沿用 HTTP 200 的 `success:false` 业务错误格式。

## Data Model and Index Design

不增加业务表、列或数据回填。增加主库非唯一索引 `idx_users_customer_audit(inviter_id, role, id, deleted_at)`，由 `model/user.go` 的 GORM tags 和现有迁移流程创建。

| 资源与索引 | 访问方式 |
| --- | --- |
| `users: idx_users_customer_audit(inviter_id,role,id,deleted_at)`，新增 | 固定当前邀请人和普通角色，排除软删除，`id > last_id ORDER BY id LIMIT 200`；只读取客户 ID，无 OFFSET。ID 在 deleted_at 之前，保证 PostgreSQL 的 ID 顺序扫描。 |
| `users` 主键，已有 | 授权员工及最终最多 21 个客户的账号名投影，显式选择所需列。 |
| `employee_profiles(user_id)` 唯一索引，已有 | 按具体用户关联核验 `status=1`；无全量员工 ID 列表。 |
| `audit_logs: idx_audit_customer_cursor(user_id,created_at,event_id)`，已有 | 每批至多 200 个客户 ID、固定时间窗和全局游标，取可见活动；IP/请求 ID 继续由既有筛选实现。 |
| `audit_security_alerts: idx_sec_customer_time(actor_id,last_seen,id)`，已有 | 同样的客户 ID 批次、时间窗及风险游标。 |
| 安全快照、处置关联和动作表的既有索引 | 复用客户风险身份验证与 `loadAlertResponses`，最后只加载实际返回告警的处置结果。 |

分页汇总算法：

1. 每个 HTTP 请求先重新验证员工；当前客户集合按 ID 分批查询，每批最多 200 个，排除员工本人、软删除、非普通角色和有效员工客户。
2. 每批仅查询该批客户在时间窗/全局游标之后的记录，复用既有“过滤隐藏记录后补齐 21 条”的语义。日志按 `(created_at,event_id)` 倒序，风险按 `(last_seen,id)` 倒序。
3. 每批候选与当前全局候选合并，只保留最近的 21 条。活动的 EventId 来自 `common.NewRequestId()`，包含大小写随机字符：两个非空候选集合合并时，必须让原日志数据库按 `(created_at,event_id)` 排序最多 42 个已知事件 ID，再保留 21 个。该查询仅投影 `event_id`，按唯一事件索引和原时间窗读取，不能用 Go 字节序替代数据库 collation。风险 ID 为整数，仍在内存按 `(last_seen,id)` 排序。全部客户批次处理完毕后，输出前 20 条；存在第 21 条时，游标指向最后一条实际返回记录。
4. 不逐客户发 HTTP 请求，不为每位客户常驻查询缓存。客户批次、记录批次和全局候选均有界；不把全部日志或全部客户装入内存。
5. 最终再次核验员工，然后按候选记录中的最多 21 个客户主键查询名称，只投影 `id,username,display_name`。这次读取仍应用当前归属条件；若期间归属发生变化使候选不再完整，返回可重试业务错误，不以部分结果伪装完整页。客户归属始终以请求执行时的实时数据库检查为界，不承诺撤回已发送数据。
6. 无客户直接返回空页；无可见记录或纯隐藏尾页返回空游标。总工作受原 3 秒 context 限制，超时不返回部分成功结果，也不扩大查询范围降级。

索引只涉及归属、角色、主键、软删除字段；额度扣减不修改这些索引键。已有大表建立索引仍可能产生 DDL 锁及 I/O，发布前须对目标 schema 副本演练，按既有发布流程安排预建索引；不得在繁忙生产流量下把自动启动建索引当作验收。

## Frontend Lifecycle and Reuse

- `EmployeeCustomerAudit` 始终渲染共享客户审计视图；空选择代表汇总，界面显示“全部客户”。选择器复用现有 `ComboboxInput` 的清除能力并正确处理 null，允许从具体客户返回汇总。
- `CustomerAuditView` / `CustomerRecordList` 显式区分全部客户和具体客户，查询键包含登录账号、角色/员工状态、范围、投影版本、条件及游标；切换范围重新挂载内容，关闭原详情、取消请求并清理旧缓存。
- `useAuditActivityColumns` 在兼容现有固定名称参数的基础上支持按行客户标签；表格和 `AuditActivityDetailsDialog` 取同一行的身份，不新增一套重复表格或详情。
- 风险列表新增客户列；打开的风险状态由 `{customerID,alertID}` 标识。每行的确认组件均传明确正数 `customerID`，绝不因“全部客户”范围而遗漏它。
- 风险详情的“查看关联审计”切到该行客户、事件时间附近的原有单客户查询，保留请求 ID/IP 条件；不把某客户的关联查询扩展为汇总。
- 风险保存成功后同时失效当前员工的单客户与汇总查询。撤权时清除相应列表和已打开详情；被转移客户详情返回 403 时，保留回到全部客户/重新选择入口。
- 空数据使用现有“该时间范围内暂无记录”，加载、错误、重试复用 `DataTablePage`、`ErrorState`；客户候选加载失败与日志查询失败分别显示，不把错误当作无数据。
- 复用 `LogsFilterToolbar`、`LogsFilterField`、`CompactDateTimeRangePicker`。列表布局和分页按下述统一布局修正；新增文案按 i18n 技能覆盖七个前端语言，不修改受保护的认证/HTTP 文件。

### 统一列表布局与分页（2026-11-22 修正）

调整前，`CustomerRecordList` 已按 `next_cursor` 请求下一页，服务端单客户与汇总均固定每页 20 条，但前端设置 `showPagination={false}`，在表格外手写 `Previous` / `Next` 按钮；`EmployeeCustomerAudit` 的整页滚动容器及内容高度未接入公共表格的固定高度布局。

本次是既有页面的布局与分页展示修正：使用 `useDataTable`、`DataTablePage` 和公共 `DataTablePagination`；为公共分页兼容增加游标模式，显示本页条数、每页 20 条、当前页、首页和前后页按钮。接口未返回总数或随机页游标，因此不显示虚构总数、总页数和末页跳转，也不添加不可执行的每页条数选择。普通总数分页的既有行为保持兼容。

主页面使用现有 `PageFooterPortal` 固定页脚、公共表格内部滚动与固定表头；客户弹窗使用同一分页组件并留在弹窗内。查询中禁止重复翻页，末页禁用下一页；查询失败保留回上一页/首页和重试入口。客户切换、搜索、重置、刷新和关联调查继续清空原分页位置。操作日志与风险记录共用该逻辑。

改动只涉及浏览器布局、表格状态和文案，不改变 API、每页条数、权限、数据库查询、缓存键或后端共享资源，也不影响 relay。验收覆盖三页前后导航和首页、末页/加载/错误状态、筛选重置、弹窗内分页，以及桌面和移动端的滚动区域与页脚可见性。

新增的“本页条数”和“第几页”文案覆盖七个前端语言；原有总数分页仍保留每页条数选择和末页跳转。组件回归同时覆盖两种分页模式，具体执行结果与发布状态单独记录。

### 客户弹窗布局与稳定性（2026-11-23 修正）

核实当前实现：`customer-audit-dialog.tsx:CustomerAuditDialog` 原使用 `sm:max-w-6xl` 和 `min(72vh, 760px)` 固定内容高度；`useAuditActivityColumns` 的八列合计最小宽度为 1172px，超过弹窗扣除内边距后的可用宽度。共享 `DataTablePage` 使用独立表格滚动区和弹窗内分页，`CustomerAuditView` 同时供审计主页使用。单条记录的大面积空白及默认桌面横向滚动来自上述布局约束。

本次仅修正展示：桌面弹窗扩大到 `sm:max-w-7xl`，内容区采用 `min(72dvh, 32rem)` 的稳定高度，关闭区复用审计导出弹窗的紧凑按钮排布。按用户补充要求，容器高度不随请求加载状态、记录数量或页签变化。补齐页签内容和表格容器的 flex 收缩链，使长列表在表格内滚动，保留固定表头、弹窗内分页及关闭按钮；为表格滚动区预留滚动条位置，避免滚动条出现时列宽跳动。移动端继续使用现有卡片列表与响应式分页，较窄桌面允许表格内部横向滚动。

同时核实 `LogsFilterToolbar` 的搜索按钮：原先仅在请求中插入加载图标，按钮宽度增加 22px，并挤动同排动作。现在常态搜索图标与加载图标占用相同空间，保留搜索文字、禁用状态和 `aria-busy`；桌面、移动端工具栏和移动筛选抽屉采用同一处理。复用现有翻译，不增加文案或语言包改动。

API、游标、客户授权、查询键及缓存清理语义均未调整。共享资源核对：改动只涉及浏览器布局和既有搜索按钮渲染，没有新增或修改后端 DB、Redis、连接池、文件句柄、worker 或 relay 资源访问。审计主页继续使用原固定页高与页面页脚，客户弹窗分页留在弹窗内。

本地验证使用真实前端组件和隔离的合成响应，覆盖 1440/1366px 桌面、1024/768px 窄屏、390/320px 手机，单条/满页/空列表/失败/加载、风险页签、深色和长字段。1440×900 下弹窗由 1152×829 调整为 1280×677；初始加载、搜索后 1/20/0 条记录及页签切换时，弹窗、标题、标签、分页和关闭区的矩形变化为 0px。桌面搜索动作的宽度与位置变化也为 0px；移动端同一页签加载切换不改变按钮尺寸，风险页签按既有逻辑少显示请求 ID/IP 两个筛选字段。滚动时表头固定，分页可点击，移动端不产生页面横向溢出。

组件回归：`customer-audit-dialog.test.tsx`、`audit/__tests__/employee-customer-audit.test.tsx` 和 `components/__tests__/mobile-filter.test.tsx` 共 36 项通过；类型检查、格式检查、生产构建及上述浏览器验证通过。lint 通过，仅有仓库原有的非本次文件警告。本次未部署。

## Config Parameters

无新增用户配置或环境变量。每页 20 条、每批 200 个客户是实现边界，时间窗及超时沿用现有客户审计约束。

## Error Handling

- 复用 `employeeCustomerAuditResponse`、`ApiErrorI18n` 和已有参数/不可用文案；真实数据库、投影或超时错误先 `logger.LogError(c, ...)`，不返回 SQL 或 Go 错误。
- 客户集合在请求过程中变化导致最终归属检查失败时使用可重试的客户范围变更错误，新增后端文案同步当前实际的 en、zh-CN、zh-TW YAML；不伪造“没有记录”。
- 既有风险版本冲突、记录被清理、客户转移/提权、员工停用全部保留现有拒绝路径；汇总列表不提供批量处置或管理权限。

## Main Chain Impact

本功能仅新增 dashboard 只读查询；AI relay goroutine 不执行本方案中的任何步骤。30k/100k RPM 下每个 AI 请求新增 DB 调用、Redis 调用、锁、goroutine 均为 **0**。日志采集、计费、令牌校验和响应流没有改动。

## Shared Resource Audit

| 资源 | 是否与 relay 有交集及处理 |
| --- | --- |
| `users` | 共享业务表；仅独立审计池的普通索引读，无行锁/写入。新增索引不包含额度字段，DDL 必须通过独立升级演练控制。 |
| `employee_profiles` | 员工资格/提成相关共享表；按用户唯一键只读，不修改员工缓存，不在 relay 中增加检查。 |
| `audit_logs` | 与既有异步审计写入共享存储；只读指定客户和时间范围，使用原独立日志读取池，3 秒截止时间；不改采集队列。 |
| 风险告警、快照、处置关联/动作表 | 与安全后台共享；只读现有索引范围，风险写入仍走原客户 actions API，无新增 worker。 |
| 连接池 | 复用审计 main/log 各最多 2 个连接，客户批次顺序处理，不占用 relay 或身份快照专用池，不新建池。 |
| Redis、全局内存、文件、goroutine 池 | 无新键、缓存、文件句柄或后台协程。请求内临时候选有界；浏览器 Query 缓存与后端 relay 无关。 |

## Concurrency Analysis

汇总为非 relay 交互查询，不承接 AI 请求 RPM。设当前客户数 N、批次数 B = ceil(N/200)、补扫隐藏批次数 H：每个列表请求约 1 次员工授权 + B 次客户批次读（可能额外 1 次耗尽检查）+ B+H 次记录读；非空候选额外进行 1 次最终员工授权 + 1 次客户身份读。活动还会有至多 B−1 次有界事件 ID 归并排序查询；风险历史身份核验及最终处置结果加载沿用现有有界逻辑，历史快照每次最多 101 行。

新增 Redis 调用、分布式锁、`FOR UPDATE`、后台 goroutine 均为 0。请求最多保留 200 个客户 ID、两个至多 21 条的候选集合及既有单条历史核验数据。大量客户或隐藏记录可能触及 3 秒截止时间，届时返回失败供缩小时间范围/选择客户后重试，不在后台继续工作。

## Concrete Changes and Validation

先补回归用例再实施，实际落点如下：

1. `model/employee_customer_audit.go`、`model/employee_audit_list.go`：拆出可复用的实时员工资格校验，新增客户 ID 批次查询、汇总游标及活动汇总；单客户正数 ID 约束不改变。
2. `service/auditsecurity/customer_access.go`、`employee_customer_list.go`：复用规则/历史身份核验和安全 DTO，新增客户风险汇总；最终处置加载保留每条告警的 ActorID。
3. `controller/employee_customer_audit.go`、`router/api-router.go`、`middleware/access_token_routes.go`：两个汇总只读端点及 session-only 规则；沿用原错误和超时策略。
4. `model/user.go`：新增上述非唯一索引标签；在已有 `model/user_assignee_index_test.go` 的新库/上一版 schema 升级测试中验证，使用生产 `chooseDB` 迁移驱动，确保第二次迁移无 DDL。
5. 前端上述共享页面、API、活动列、风险详情及状态保存的失效逻辑：默认汇总、可清除筛选、行身份和缓存隔离；新增词条前加载 i18n 技能。
6. 更新两份既有客户审计设计中“未选择不查询”的说明，并将本设计状态改为实际实施/验证结果。

必须覆盖的可观察行为：

- 进入“我的客户”无需点击选择器即可看到多个客户记录；列表和详情标识正确；员工自己的日志仍只在本人入口显示。
- 多于 200 个客户和跨批次同时间戳的混合记录，20/21/40/41 条可见边界、连续隐藏批次、纯隐藏尾页、无客户、无记录；全局顺序正确且无重复/遗漏。
- 候选搜索分页不限制日志全集；选择客户、清除选择、刷新、时间/IP/请求 ID 筛选、前后分页均重置正确；迟到响应不覆盖新范围。
- 本人/其他员工客户隔离；伪造员工/客户 ID、跨客户/汇总/员工游标；PAT/API Key、无会话、停用员工；客户转移、删除、提权、成为员工及禁用客户。
- 风险行、详情、关联审计、状态保存使用该行客户；汇总和单客户列表同步刷新；版本冲突/403 后不残留数据或回退到管理 API。
- 保留原脱敏哨兵：原始管理信息、匿名/其他账号证据、内部备注、令牌/参数和旧值新值均不泄露。

实施后的验证门槛：相关 controller/model/auditsecurity/router/middleware 回归；实际 SQLite、MySQL、PostgreSQL 查询与迁移，主/日志双库组合；新增索引的新库与旧版升级和连续两次启动无重复 DDL；捕获实际 GORM SQL 的查询计划。按发布清单补较新 MySQL及目标 schema 演练，ClickHouse 若启用则单独覆盖，不能用 SQL 探针代替。

前端运行受影响 Vitest、`bun run typecheck`、`bun run lint` 和生产构建，浏览器验证默认汇总、筛选清除、详情与风险关联。若当前 Bun 不支持脚本中的 `bun check -b`，明确报告并使用仓库已安装类型检查器完成实际检查，不把脚本失败记为通过。

## 发布边界

新增索引的测试覆盖新库、上一版 schema 升级和再次迁移无 DDL；汇总查询测试捕获真实 GORM SQL，并覆盖独立主库/日志库、跨客户批次及两页结果。查询计划允许优化器选择等价的有界索引路径，不以某一个固定索引名称代替访问范围验证。

本地查询计划和组件验证不保证生产基数下的时延。发布时仍须完成目标 schema 副本演练、完整应用启动，以及已启用 ClickHouse 的适用验证；生产大表索引创建按既有发布流程安排。
