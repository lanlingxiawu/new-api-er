# 普通用户与员工审计日志字段收敛

日期：2026-11-21。状态：**用户已确认实施，字段投影、身份筛选、降权检查及员工客户入口已包含在 04:02 本地快照，并于 04:12 随 r5 部署香港；生产未部署**。本次同时检查并收敛筛选参数。打包与现场验收见[r5 记录](hk-local-repack-r5-20261121.md)。

## Goals and Scope

用户要求检查普通用户和员工的审计数据权限，并指出密钥操作详情展开整套配置过于详细。本方案把这两类入口收敛为可理解的操作记录：发生时间、操作对象、做了什么、结果、来源 IP 和简要客户端；修改只描述变更项，不展示配置快照或前后值。

覆盖审计日志的“仅自己”、安全与访问页面复用的本人令牌审计、员工“我的客户 → 审计与风险”、本人审计导出，以及客户风险结果中的内部标识。使用日志、任务日志、密钥管理表单的可见字段不在本次范围内。

管理端“全部审计”的字段仍按现有管理员/Root 权限提供；身份快照、可使用的凭证等继续禁止返回。管理员进入“仅自己”同样使用简化视图，不能通过客户端参数切换投影。

## Existing Logic and Related-Path Findings

以下事实来自当前工作区实现，而非截图或历史设计推断。

| 代码入口 | 已验证行为 |
|---|---|
| `router/api-router.go:475`、`controller/audit_filters.go:53`、`controller/access_token.go:376` | `/api/audit/self` 使用 `UserAuth`，服务端绑定当前 `id` 并清空客户端 username；自行传 user_id、username、self_view 不能扩大行范围。全局接口要求管理员和 `audit.read`。 |
| `model/audit_log.go:182` | 原先非 Root 均使用 `{1,10}`，追加检查发现普通账号仍可能读到自己的历史管理员记录；现已增加当前角色上限。审计归属按 `user_id`，不是按请求者传入的角色。管理操作通常归属实际操作者，目标客户只是参数，见 `controller/audit.go`。 |
| `model/audit_log.go:265`、`model/audit_changes.go` | self 投影移除 `admin_info/audit_info/root_info/op.identity`；`changes` 仍返回归属该账号且 visibility=self 的完整允许字段值；`op.params` 没有读取侧逐 action 白名单，`content` 也未重建。 |
| `controller/user.go:151`、`model/audit_security_capture.go:22` | 登录参数中的 `subject_ref` 是带用途前缀的 HMAC 关联值，不是可登录令牌，但没有用户阅读价值；目前会显示成“账户标识”。 |
| `model/audit_snapshots.go:45`、`controller/token.go:378`、`controller/access_token.go:190` | 密钥配置快照包含分组、额度、有效期、IP 白名单、模型限制等；个人访问令牌还记录 scopes 和前后权限。快照标为 self；日志不需要这些值才能表达“创建/修改密钥”。 |
| `model/employee_customer_audit.go:19` | 每次请求读取当前员工启用状态及 `users.inviter_id`。客户须是其当前普通客户，且不是有效员工；转移、删除、提升管理员/员工后拒绝访问。禁用客户仍可调查。 |
| `model/employee_customer_audit.go:95` | 客户日志排除管理员角色、已知员工角色和带 AdminInfo 的历史管理记录，再调用普通用户 self 投影。因此员工当前也能读取客户的完整 self 配置快照和操作参数。 |
| `controller/employee_customer_audit.go`、`service/auditsecurity/customer_access.go` | 客户入口另要求真实 dashboard 会话；风险读取核验账号、规则版本和可信历史证据。员工可看处置状态及员工排查备注；管理员姓名、内部备注、负责人和跨账号证据被剥离。客户处置摘要仍复用 `SecurityAccountResponse`，会带 `response_group_id/target_user_id/reason_code`。 |
| `controller/audit_self_security.go`、`service/auditsecurity/self_access.go` | “我的安全”已有专用白名单 DTO，只返回本人告警概要、事件时间/IP/结果和有限处置结果；无管理员内部备注或完整证据。 |
| `web/src/features/usage-logs/audit/components/audit-log-details-dialog.tsx`、`audit-changes-section.tsx`、`../lib/audit-details.ts` | 详情没有专门的用户简版；对后端传来的 params 逐项渲染，完整 changes 展示前后值。客户端把空值/零值放进“其他字段”，仍然能展开。 |
| `web/src/features/customer-console/customer-audit-dialog.tsx` | 员工列表只选时间/事件/IP/结果列，但详情复用完整审计详情；缩减列表列数没有缩减响应数据。403 会移除当前用户/客户查询缓存。 |
| `service/auditexport/source.go:175`、`model/log_export_management.go:148` | 本人导出复用同一 self 投影，CSV 仍含整个 other。导出要求登录会话和配置开启，只能下载自己的 self 任务；员工客户导出权限不适用于 audit 数据源，没有客户审计批量导出接口。 |
| `model/log_export_job.go`、`controller/log_export.go:1007` | 导出任务保存于 Redis，CSV 分片在文件系统。签发和使用下载链接时重新鉴权，但现有完成文件不会因展示代码变更自动重写。 |

结论：已检查路径未发现普通用户跨账号或员工跨客户读取；发现合法行范围内字段过宽、界面过细，以及追加检查定位的历史高级角色记录与降权后旧本人导出边界，均在本次收紧。账户关联值不是凭证泄露证据；原读取侧缺少参数白名单意味着新增参数可能被默认公开，已在响应边界收敛。

## 已实施字段范围

以下为本次实施范围，行级权限继续沿用原有本人/当前客户归属校验。

| 字段/内容 | 普通用户及员工查看本人 | 员工查看当前客户 |
|---|---|---|
| 时间、事件、成功/失败 | 显示 | 显示 |
| 操作对象名称、必要对象编号 | 仅来自已授权归属的变更凭据；批量操作不伪装成单个对象 | 同左 |
| 来源 IP、简要客户端，例如 Edge · Windows | 显示 | 显示 |
| 登录方式、网页登录/访问令牌等操作来源 | 显示 | 显示 |
| 修改了哪些业务项 | 简短摘要，最多列 3 项，其余显示数量 | 同左 |
| 配置快照及前后值：分组、额度、IP 白名单、模型限制、scopes、邮箱等 | 不返回 | 不返回 |
| 账户关联 HMAC、令牌指纹、会话标识、身份版本、响应组编号、内部错误码 | 不返回 | 不返回 |
| 完整 User-Agent、HTTP 方法/状态、路由、冗余角色及员工身份快照 | 简版不返回 | 简版不返回 |
| 请求 ID | 允许保留用于支持排查，详情底部提供复制，不展开请求技术区 | 同左，用于本客户内关联查询 |
| 告警类型、等级、状态、事件和处置结果 | 仅本人可信告警，沿用“我的安全” | 仅该客户可信告警 |
| 员工排查备注、处理进展和结论 | 不开放给客户本人 | 当前归属员工可看，沿用现有规则 |
| 管理员/Root 内部参数、备注、指派、跨账号证据、平台风控配置 | 不返回 | 不返回 |

`event_id`、告警 ID、客户 ID、分页游标和风险编辑 version 是必要的协议字段，不代表获得其他记录权限，不在详情中堆叠展示。密码、密钥原文、验证码、可用会话/刷新令牌任何审计视图都不得返回。

示例：截图中的创建记录显示“创建 API 密钥「111」 · 成功”，详情为时间、来源、IP、客户端。修改记录显示“修改 API 密钥「111」：名称、有效期等 4 项”；不再列出整套配置、默认值或“其他字段”。未知旧记录显示通用事件摘要，不回退到任意原始 JSON 或未经筛选的 content。

### 事件名称缺失修复（2026-11-21）

用户确认修复“仅自己”大量显示“操作审计”的问题，并要求同时检查普通用户。

- 已核实 `controller/access_token.go:GetAuditLogs` 在 self 视图调用 `ProjectAuditActivities`，管理员也走该投影。`model/audit_activity.go:ProjectAuditActivity` 原先只允许个人账户动作，导致已通过行权限校验的渠道、用户管理和系统设置动作变成 `operation`。`activityLabel` 另缺少日志导出、会话退出等已允许动作的名称，因此普通用户同样受影响。
- 修复范围是既有事件名称合同：依据 `controller/audit.go`、`middleware/audit.go:auditRouteActions` 及现有前端审计映射补齐固定动作白名单；复用 `auditActionLabel` 和既有七语文案，参数化模板补充不含对象或配置值的简短名称。明确未知的动作、`generic` 和伪造的已知动作前缀仍使用通用摘要，不按前缀放行，也不回退到路由、content 或 params。
- 数据流仍是授权查询 → 原有行/字段投影 → `AuditActivity` → 列表/详情或本人 CSV。普通用户、员工客户、管理员本人共用安全名称；查询范围、当前角色上限、对象归属与变更字段限制不变。全部审计不切换到简版。
- 无 API、数据表、索引、SQL、配置或导出版本变化；原始审计不重写。新读取和新生成导出保留已知动作标识，已经生成的 CSV 不自动改写；接口失败与未知元数据继续走现有处理。
- 共享资源审计：仅对查询所得内存记录执行固定 switch 和本地化映射；无新增数据库/Redis 调用、连接、锁、后台任务或共享可变缓存，不进入 relay 主链。
- 回归先覆盖管理动作保留、普通用户导出名称、未知动作降级、敏感参数/目标不泄露；再检查每个后端允许动作均有七语名称，并复跑现有本人/客户列表、详情及导出用例。修改只涉及纯投影和显示映射，不改变数据库行为。

本项实施与验证：

- 后端固定白名单覆盖 144 个已知动作；现有 `auditContentTemplates` 和 `auditRouteActions` 登记的动作无遗漏。前端复用共享名称并补齐个人导出、会话、设置等映射，新增七条无参数名称通过指定脚本写入七语并执行 `bun run i18n:sync`；七语均无缺失键，临时脚本已删除。
- 先运行新增回归，确认渠道/用户管理等动作被改为 `operation`，并复现普通用户创建导出及系统设置详情的通用文案；修复后通过同一组断言。未知动作及已知前缀下的伪造动作仍降级，管理对象/参数不泄露且原始记录不被修改。
- `go test ./model ./controller ./service/auditexport -run '^(TestAuditActivity.*|TestAuditSelfExportProjectionVersion|TestAuditRoleVisibilityAndPermissions|TestAuditIsolationVisibilityAndFailureContracts|TestEmployeeCustomerAuditHTTPBoundary|TestAuditExportReaderLifecycle)$' -count=1 -timeout=120s` 通过。纯投影用例无需数据库；API/导出夹具使用隔离 SQLite，本轮未配置真实 MySQL/PostgreSQL 验收实例；本次没有 SQL、Schema 或数据库行为修改。
- 前端审计列表/详情/客户视图、七语映射、导出及个人访问令牌卡片合计 10 文件 **155 项通过**。名称合同逐个检查后端允许动作在全部七语中存在，且不包含未填充模板或原始参数。
- `bun run lint` 通过，只有其他文件的既有警告；修改文件 oxfmt、UTF-8 无 BOM 及空白检查通过。仓库 `bun run typecheck` 仍因本机 Bun 不支持 `bun check -b` 失败，使用已安装的 `bun x --no-install tsgo -b` 检查通过，未修改依赖或脚本。
- `bun run build` 通过（8.74 秒），根模块 `go build ./...` 通过。受保护认证/HTTP 文件相对 HEAD 无差异。本轮未执行部署或生产浏览器验收，线上需同时更新前后端后生效。

### 追加排查：事件与查询定位（2026-11-21）

用户要求继续修复遗漏，并确认操作对象/改动与筛选定位两方面都不清楚。本轮修复既有投影和查询交互，不增加搜索接口或放宽字段权限。

已核实的相关路径：

- `middleware/audit.go:beginAccessTokenAudit/finishAccessTokenAudit` 写入的访问令牌请求没有顶层 action；`model/log.go:RecordLogWithAdminInfo` 的订阅重置记录只在 `other.op.action` 保存动作。读取投影仅检查顶层 action，因此已补过名称仍会降级。读取时仅在顶层为空时采用固定白名单内的 op.action；动作均为空且分类是 access_token 时显示访问令牌请求。非空未知动作不猜测，原始证据不改写。
- `controller/channel_veridrop.go:startVeridropSystemTask` 的三个调用传入单渠道、选定渠道、全部启用渠道检测动作，之前只检查静态映射表遗漏了这些动态传参入口。补齐三个动作及可区分的名称，并用实际记录形状测试。
- `audit-activity-columns.tsx` 丢弃了 DTO 已有的对象编号及变更摘要；详情也会同时出现“已修改字段”和“未保存”。列表/详情复用允许字段摘要，展示对象名称与编号，未提交/未改变/未记录分别明确表达，避免将失败写成已修改。
- `audit-log-filter-bar.tsx` 在 accessOnly 下隐藏请求 ID；`AuditLogViewer.filterIP` 分别合并 draft 和已提交 filters，导致屏幕条件与真实查询不一致；搜索不清理复制输入前后空格，且默认时间窗口停留在首次打开时。请求 ID 输入恢复到所有审计视图；IP 快捷筛选统一回到已提交条件；搜索修剪输入并重置分页；默认最近一天更新到搜索时刻，明确选择的历史区间保持不变。复用客户审计已有的 customRange 行为，客户输入也统一修剪。

数据流、授权、API 参数及 DTO 字段不变；原查询仍在服务端执行，禁止仅过滤当前页伪装为完整搜索。没有新增 SQL、索引、迁移、配置、网络调用或共享资源；处理仅发生在读取后的内存投影及前端，不进入 AI relay。历史记录没有授权归属的对象凭据时仍不能从任意参数重建对象；已经生成的导出文件不会重写。

实施与验证：

- 固定动作现为 **147** 个。新增回归先复现空 action、嵌套动作及三个检测入口降级；非空未知动作仍不从嵌套名称覆盖。七语合同加入实际检测控制器入口检查，避免只验证已有白名单自洽。没有改动审计写入中间件或订阅业务逻辑。
- 列表显示已授权名称、对象编号及最多三项变更摘要；详情复用同一摘要。失败未保存、未改变、未记录、未知结果、部分保存分别测试，不显示配置值或内部字段。个人资料、个人设置、渠道优先级使用明确动作名称；操作来源统一显示“网页会话”，避免所有操作都像一次网页登录。八个新增名称已通过指定脚本同步七语，缺失键为零，UTF-8 无 BOM，临时脚本已删除。
- 主审计与令牌历史均可按请求 ID 查询，复制输入去除首尾空格；员工客户查询也修剪 IP/请求 ID 并重置游标。IP 快捷筛选将页面条件同步到已提交条件，不混用未提交的结果、令牌范围和时间。默认查询时间随搜索更新，手动时间和调查链接固定区间不移动。风险导出保留原始请求条件和权限拒绝，不静默扩大导出范围。
- 先复现八项前端失败；修复后相关十文件中的其余七文件 **96 项通过**，最终重跑有改动的 activity、viewer、customer-audit-dialog 三文件 **75 项通过**，合计 **171 项**。覆盖权限、导出、七语、请求定位和显示行为。
- `go test ./model ./controller ./service/auditexport -run '^(TestAuditActivity.*|TestAuditSelfExportProjectionVersion|TestAuditRoleVisibilityAndPermissions|TestAuditIsolationVisibilityAndFailureContracts|TestEmployeeCustomerAuditHTTPBoundary|TestAuditExportReaderLifecycle)$' -count=1 -timeout=120s` 三包通过。数据库相关夹具仍使用隔离 SQLite；本轮不改变数据库行为，未重新执行真实 MySQL/PostgreSQL/ClickHouse 矩阵。
- `bun run lint` 通过（仅其他文件既有警告），13 个文件 oxfmt 通过；`bun run typecheck` 因本机 `bun check -b` 不可用失败，等价的 `bun x --no-install tsgo -b` 通过。最终前端构建通过（10.7 秒），根模块 `go build ./...` 通过，受保护认证/HTTP 文件相对 HEAD 无差异。
- 本地生产构建配合合成 API 响应，在独立 Chromium 验证中文 1440×900 列表、详情、请求 ID 搜索/重置和 390×844 手机布局。桌面验收发现详情列被挤出，已沿用现有列布局调整宽度，最终无需横向滚动即可看到详情；手机文档宽度与视口均为 390。没有浏览器运行错误。此验收不连接生产，也不替代后端权限测试；本轮尚未部署。

## Data Flow

1. 保留现有认证、角色和账号/客户归属检查及时间范围限制。
2. 从现有独立审计连接池读取记录，先执行既有行权限和 changes 归属投影。
3. 在读取出口构造独立的 `AuditActivity` DTO；从 action、允许的对象名/编号和已授权 changes 的字段名生成概要，绝不复制原始 other 或任意 content。批量事件保留批量动作名称，不展开成员清单。
4. 浏览器使用简版列表/详情；本人导出 worker 用相同 DTO 构造有限 CSV 列。
5. 审计原始记录、写入快照、后台规则计算及管理员审阅继续读取完整存储数据；简化只发生在对外读取边界，不影响安全告警判定。

## API Contracts

不新增路由、角色或可配置权限；现有成功包裹和分页合同保留。

- `GET /api/audit/self`：仍要求 UserAuth（个人访问令牌沿用既有 usage:read 授权），items 改为简版 DTO。
- `GET /api/user/employee/customers/:id/audit`：仍要求 dashboard 会话、当前有效员工及实时客户归属，items 改为同类简版 DTO，游标继续绑定员工、客户和筛选。
- DTO 固定字段：`event_id, created_at, category, action, success, ip, client, source, login_method, request_id, target, change_summary`。`target` 只含允许的资源类别、对象编号、名称；`change_summary` 只含业务字段枚举、总数、提交结果，不含原始路径或值。身份与客户名称由当前页面已授权的账号信息显示。
- 用有限客户端规则在服务端生成 browser/OS 或 SDK 名称；复用前端 `identifyAuditClient` 的既有识别口径并以样例锁定行为，未知值返回 Unknown，不把原始 UA 当兜底。
- 本人列表保留时间、结果、类别、IP、操作来源、请求 ID 筛选；移除身份、路由和任意令牌指纹输入。安全与访问页面的“当前/历史令牌”筛选继续由既有受控参数完成，不展示指纹。只有 access_token 类别允许 token_ref / exclude_token_ref，二者不能同时提交；本人范围始终在服务端固定。
- 普通审计日期选择器与后端统一为最多 30 天；员工客户游标接口保留既有 31 天上限。员工客户操作列表只接受时间、IP、请求 ID；客户告警列表只使用时间，不再默默忽略 IP / 请求 ID 或管理筛选。IPv6 zone 参数被拒绝。
- `/api/audit/self/routes` 在简版中不再调用，接口返回空路由候选；不得绕过简版枚举内部路由。管理端 `/api/audit/routes` 按原权限返回。
- `POST/GET/DELETE /api/audit/self/export/jobs...` 保持会话及本人任务鉴权，CSV 只含简版字段；移除原始 other 列。`audit_admin` 仍须管理员、audit.read、audit.export；员工客户审计仍不提供导出入口。
- 客户安全告警维持既有业务字段，处置结果改用专用 DTO，保留动作、结果、时间、影响数量及对用户可理解的原因枚举，移除内部响应组/目标账号重复编号。员工维护状态、结论、备注的接口仍按已有 version 冲突控制处理。
- “我的安全”维持现有最小 DTO；不把管理 DTO 或员工备注引入本人接口。

## Key Business Logic and Edge Cases

### 角色筛选与查询上限（2026-11-21 补充）

用户追加要求：筛选不显示高于当前账号的角色，手动构造查询也不能越权。本项在已确认的字段及筛选收敛范围内继续实施。

- 本人和员工客户入口继续移除身份筛选；管理员管理入口仅显示管理员、员工、用户及员工身份未记录的普通账号；Root 才能选择超级管理员、未知身份。
- `parseAuditFilter` 和导出请求解析按服务端身份拒绝越级筛选；导出使用当前权限复核得到的角色，优先于认证上下文中的旧快照。`AuditLogQuery` 的非 Root 固定 `{1,10}` 范围改为同时受当前角色上限约束，覆盖列表、总数、路由候选、批量事件、风险关联及 CSV 查询。普通账号不能读取自己过去以管理员或 Root 身份产生的记录，客户端 role/user_id 不参与授权。旧访问令牌的最后使用摘要同步应用上限。
- 已核实本人导出只检查归属，未比较任务创建角色与当前角色。新增读取数据权限检查：降权后不能继续生成或下载包含旧高级角色数据的文件，旧任务仍可查看状态及取消/删除。下载链接及 Range 请求重新检查；权限升级不会扩大旧任务的导出范围。
- 前端角色变化重新挂载审计查询视图，清除旧身份选项、分页和已打开的详情；查询缓存已按当前角色隔离。
- 无新表、索引、配置、Redis 命名空间或连接池；仅收紧既有独立审计读取连接池上的角色谓词。无 relay 调用或共享资源新增；SQL 使用可移植的 IN 和数值比较。

- 按 action 建立有限的事件/登录方式映射；对象必须来自匹配 owner 的变更凭据。任何未知 params 键默认不返回，未知 action 使用通用安全概要。旧记录缺少归属凭据时不从 params 回填目标，防止已被权限投影移除的对象重新出现。
- 变更项先经过 self/owner 权限检查，再转换为允许的业务名称。创建、删除只显示对象及动作，不生成几十个字段的初始化清单；修改区分已保存、部分保存、未保存、没有变更、无法确认。
- 对象名仅使用已有受限文本，前端按文本转义；导出沿用 CSV 公式防护。未知字段名不能把内部路径伪装成“修改项”。
- 管理员使用全局入口保持原始授权后的详细视图；仅客户端修改 scope、role、user_id 或 cursor 不能选用管理投影。
- 员工资格停用、客户转移/删除/提权后，新请求按现有规则拒绝；已成功发送的数据无法收回。客户禁用不阻止所属员工调查。
- 前端查询 key 增加公开投影版本；身份或客户变化后取消旧查询并清理旧缓存。详情不复用管理员已缓存记录，接口失败不保留上一客户结果。
- 安全与访问中的 `AuditLogViewer accessOnly` 同步改用简版，当前/历史令牌过滤及本人身份边界须保持。客户“查看关联审计”仍只使用本客户 IP/请求 ID。

## 导出生命周期与历史文件

只修改新 CSV 生成器不足以收回旧 CSV 的字段。

- 为 `LogExportJob` 增加仅服务端设置的 `audit_projection_version` JSON 元数据，当前简版为 1；现有 Redis JSON 可兼容，无 SQL 迁移。请求体不接受该字段。
- 新本人任务创建、执行、恢复均校验版本。旧版 pending/running 任务按既有失败/取消流程终止并清理自己的临时分片，不继续混合写入两种格式。
- 旧版 ready 任务可在本人任务列表看见基本状态并删除，但禁止签发新下载链接；已签发链接及 Range 请求在实际下载入口同样检查版本。`can_download` 独立于查看/删除权限计算，不能顺带禁止用户删除旧任务。
- 旧文件按原有过期和删除流程清理，不全量改写文件、不新增扫描数据库作业。已经下载到用户设备的文件无法撤回。
- 多节点部署先升级并确认所有导出 worker、下载入口均理解版本，再开放新任务，避免旧节点续写或下载旧产物；发布步骤单独验收。

## Data Model and Config

### 员工客户标识与筛选补充（2026-11-21）

用户追加要求：审计记录需要明确显示哪个客户，并可筛选客户。本项沿用已授权的员工视角和筛选修改，不扩大员工的数据权限。

核实当前入口：`AuditLogs` 的“仅自己”调用 `/api/audit/self`，`parseAuditFilter` 强制使用登录账号 ID，截图事件下方的名称来自令牌对象，不能作为客户身份。现有客户审计在“我的客户 → 审计与风险”，调用 `/api/user/employee/customers/:id/audit`。客户候选列表 `EmployeeListCustomers` / `GetInvitedCustomersByEmployeeWithFilter` 已限定当前邀请人、普通角色及非有效员工；实际日志每次还通过 `RequireEmployeeCustomerAccess` 复核当前员工资格和客户归属。

实现：审计主页根据当前 `is_employee` 显示“我的客户”页签，接口继续实时核验有效员工资格，复用现有客户审计视图，客户筛选放在时间/IP/请求 ID 的同一工具栏中。下拉使用既有客户列表接口按关键词搜索、分页加载。2026-11-22 按用户确认的[默认展示设计](employee-customer-audit-default-list.md)调整为未选择时直接查询全部可访问客户，选择后查询单客户，清除后恢复汇总。列表和详情明确显示每行所属客户账号及编号，本人列表显示当前账号，避免把令牌名当成客户。原客户弹窗复用同一内容组件，保留风险记录和关联审计能力。客户表格关闭未实现的客户端排序，保持后端时间倒序和游标顺序。

客户搜索缓存按登录账号及角色、员工身份隔离，传递 AbortSignal；切换客户、全部客户或身份重新挂载视图，清空旧列表、详情及游标；失败不回显上一范围数据，403 清除客户缓存并保留重新选择入口。汇总由服务端分批查询实时获授权客户并归并分页，不逐客户发浏览器请求，也不授予全局审计权限。单客户接口每次按路径中的客户 ID 复核归属；汇总游标另行绑定当前员工、记录类型与筛选，返回前再次核验客户归属。

最初单客户入口复用既有接口；默认汇总现已增加两个只读 API、行级客户身份 DTO 和 users 客户查询索引，详见默认展示设计。继续复用独立审计读取连接池，不新增配置或后台任务。候选列表仅在员工主动展开搜索时查询，250ms 防抖、每页 20 条，不限制汇总查询的客户范围；relay 路径不变。回归覆盖默认汇总、搜索分页、客户切换、403 和身份撤销。

不修改 SQL 表、索引、日志保留期或审计记录；只增加内存响应类型及上述 Redis 任务 JSON 版本字段。没有新增用户配置，无需新增 setting 模块。现有导出开启开关、用户导出限制、资源配额和 TTL 保留。

## Error Handling

查询失败沿用现有 controller 的用户可读错误与日志记录；不向前端返回 Go 错误。旧任务下载提供“请重新导出”的可理解提示；新增后台提示同时登记中英文，前端新文案覆盖七语。未知元数据按安全概要降级，不把解析失败变成原始数据透传。前端沿用共享 Dialog、DetailRow、ErrorState、LoadingState 和 CopyButton；详情由内容决定高度，避免少量内容仍占满大弹窗。

## Main Chain Impact

本次修改位于管理/自助审计读取、展示和后台导出，不在 AI relay goroutine 执行。转发请求的同步步骤、计费、日志捕获、身份快照写入和安全规则计算不增加调用。每个 AI 请求新增 DB/Redis 调用、锁和 goroutine 均为 0；30k/100k RPM 转发负载不增加本方案的每请求开销。

## Shared Resource Audit

- `audit_logs`、安全告警/处置表：使用已有独立审计连接池和已有时间/游标范围，增加的 DTO 投影仅在查询结果内执行，不新增查询或锁。
- `users`、`employee_profiles`：授权读取与 relay 所用用户表有资源交集，但沿用已有主键查询，不新增查询、连接或行锁。
- `logexport:*`：已检索 relay/middleware/common，没有转发链对该命名空间的读写；任务版本使用已有键，不创建旁路缓存。导出仍共享 Redis 服务及既有任务资源限额。
- 导出分片、worker、连接池：沿用既有容量和过期清理；不新建池、不增加无界 goroutine、不修改共享额度/令牌缓存。
- 浏览器查询缓存：以用户、视图、客户和投影版本隔离；不把客户端缓存作为后端授权依据。

## Concrete Changes and Validation

实现落点：新增 `model/audit_activity.go` 承载可复用公开 DTO/白名单构造；在 `controller/access_token.go`、`controller/employee_customer_audit.go` 的响应边界调用，并在 `service/auditexport/source.go` 复用。`controller/audit_filters.go` 收敛 self 路由候选。客户风险 DTO 位于 `service/auditsecurity/customer_access.go`。导出版本及下载状态检查落在 `model/log_export_job.go`、`model/log_export_management.go`、`controller/audit_export.go`、`controller/log_export.go`。

前端在 `usage-logs/audit/api.ts` 区分公开 DTO 与管理 DTO，复用已有业务 Dialog/表格建立简版渲染，调整 self viewer/filter/详情及客户审计使用点；管理员详情的完整 Changes 区保留。新文案依项目 i18n 技能执行；不修改上游拥有的认证/HTTP 文件。

实施前先补行为测试：响应中不存在旧值/新值及任意 params/content 哨兵；本人/客户/管理员三种边界；当前/历史令牌入口；未知及历史记录；客户转移和员工停用；风险证据及备注隔离；简版 CSV、旧文件下载/续传拒绝和任务删除；分页/错误/身份切换不残留数据。保留现有路由鉴权测试。字段投影不涉及 SQL 变化；追加角色上限调整查询谓词后，已按项目要求补做真实 MySQL/PostgreSQL 验证。

实施前基线：

- `go test ./model ./controller ./service/auditsecurity ./service/auditexport -run '^(TestAuditChangesPreserveValuesAndHideCredentials|TestAuditChangeProjectionDoesNotLeakManagementValues|TestAuditChangesRejectCredentialsInMalformedSafeFields|TestAuditIsolationVisibilityAndFailureContracts|TestAuditRoleVisibilityAndPermissions|TestAuditBatchVisibilityAndBounds|TestAuditFiltersKeepLegacyTimeContract|TestEmployeeCustomerAuditHTTPBoundary|TestSelfSecurity.*|TestCustomerAuditCurrentOwnershipAndProjection|TestCustomerRiskProjectionAndInvestigation|TestCustomerLegacyRiskIdentityAndCursor|TestCustomerVerifiedSessionReuseKeepsUnknownActorPrivate|TestAuditExportReaderLifecycle)$' -count=1 -timeout=180s`：四个包通过。
- 从 web 执行 `bun run test src/features/usage-logs/audit/__tests__/details.test.tsx src/features/usage-logs/audit/__tests__/changes-section.test.tsx src/features/usage-logs/audit/__tests__/viewer.test.tsx src/features/customer-console/customer-audit-dialog.test.tsx src/features/usage-logs/audit/export/api.test.ts`：5 个文件、114 项通过。
- 字段收敛首轮未配置 SQL_DSN/LOG_SQL_DSN 或审计测试 DSN，使用隔离 SQLite fixture；该阶段没有修改 SQL 查询。追加角色上限后修改了既有查询谓词，并补做下方真实 MySQL/PostgreSQL 验证；始终没有修改 Schema、索引或持久化审计数据。

权限依据：遵循 [OWASP 最小权限、默认拒绝和每次请求授权](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)；凭证及日志敏感值处理参照 [OWASP Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)。同时核对了 [ASVS 5.0.0](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 和 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 的相关边界。本轮不是完整认证系统合规评估。

## Implementation and Verification

- 新增 `model/audit_activity.go` 的独立白名单 DTO，由本人 API、客户 API、本人 CSV 共用；管理员接口和原始审计凭据保持原有合同。
- 创建/删除不输出初始化配置；修改最多返回三个已授权业务字段名及已记录字段总数。无变更、未保存、部分保存等状态保留；未知参数、字段路径和内容没有原文兜底。
- 本人 CSV 为固定 14 列。旧任务仍可查看/删除；新链接签发、旧链接、Range 下载及 worker 执行均校验版本。本人任务元数据不再返回原始令牌指纹、路由、身份筛选，旧格式任务在界面提示重新导出。
- 客户风险处置使用 `CustomerSecurityResponse`；移除 response_group_id、target_user_id、reason_code，替换为有限公开 reason。员工备注和结论规则保持原样。
- 增加/更新投影泄露哨兵、本人/Root/员工范围、隐藏筛选拒绝、旧导出及 Range 拦截测试。保留当前/历史令牌、分页、客户归属变化和管理员详情回归；原有完整详情断言转用于管理员入口。本次调整的 `access_token_audit_upstream_cases_test.go` 是本分支审计功能用例，main 中没有此文件；没有修改上游会话测试或认证/HTTP 前端文件。

### 角色筛选补充验证

- 管理员身份选项移除 Root 和未知身份；Root 保留全部。本人及员工客户入口无管理身份筛选。账号角色或员工身份改变时，重新挂载列表以清除旧筛选、分页及详情。
- 列表、总数、路由候选、批量事件共用角色上限；伪造 role/viewer_role 不改变权限。导出任务执行及最终完成前沿用当前权限复核，下载链接签发和使用（含 Range）独立复核；降权后旧任务仍能取消或删除。
- 新增/更新行为测试覆盖各角色上限、未知及非法身份、跨账号混入、员工客户边界、最后使用摘要、普通账号新导出、降权后的旧链接和 Range 拦截。旧数据库矩阵用例中“普通用户可读管理员事件”的预期与夹具已改为当前合同。
### 验证与发布边界

前后端行为测试随实现保留，覆盖角色筛选、公开 DTO、导出撤权与 Range 下载、员工客户默认汇总及单客户切换、403 清缓存和迟到响应。前端文案覆盖七语，后端同步实际 YAML 资源和消息常量。

上线须同时更新前后端；多节点必须先升级导出 worker 和下载入口，再开放新任务。数据库、浏览器与目标环境验证分别执行，不以合成 UI 夹具代替服务端授权或生产 schema 演练。
