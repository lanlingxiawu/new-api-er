# 审计日志复用使用日志导出

状态：用户已于 2026-11-16 确认设计，前后端实现已落地；验证记录及发布前缺口见文末。

2026-11-17 已按用户确认的[详情与导出权限方案](audit-log-usability-and-export-access.md)增加独立 `audit.export`，并将“导出审计日志”“导出任务”作为常驻入口。以下 API 和权限合同已同步；普通管理员的新增导出权限默认关闭，Root 可在管理员权限中明确授予。

用户已明确：审计日志需要导出，复用使用日志的导出能力；“可以配置”指后台的时间范围、条数、并发、限速等设置。本设计替换原先独立同步下载、固定 10,000 条上限的方案。

## 目标与范围

审计日志接入现有异步导出任务、资源控制、CSV.GZ 分片、进度、取消、下载和过期清理。后台共用现有设置入口和配置键，两类日志合计占用并发名额、用户任务额度和提交冷却。

覆盖审计日志“全部”“仅本人”、复用查看器的令牌访问历史，以及有权限访问的异常 IP 筛选。导出所有匹配已应用条件的记录，不受当前页、每页条数或表格隐藏列影响。审计保持自身的权限、字段投影和筛选语义。

本次审计导出使用固定安全字段集，复用表头、BOM、时区选项；不扩展审计模板、计费重算或汇总维度。使用日志已有模板、汇总和员工导出能力继续保留。安全监控告警详情的既有 JSON 导出保持原合同。

共用指导出引擎、Redis 任务存储和配置，不等于把两类数据放进同一个管理入口。现有 `/api/log/...` 只管理 usage，新 `/api/audit/...` 只管理 audit；不允许通过参数切换数据源。这样避免现有管理员在没有 AuditRead 时看到审计任务，也避免混源列表绕过逐任务权限。

## Existing Logic and Related-Path Findings

以下为实施前读取代码、调用链和相关测试后确认的事实；表中“拟”描述当时选定的接入点，实际实现见文末。

| 代码位置 | 已验证行为及接入点 |
| --- | --- |
| `controller/log_export.go:createLogExportJob`、`controller/employee_export.go` | 管理员/self 创建共用一条准入流程，员工创建另有入口。拟提取 usage 管理员/self 与 audit 共用的准入，员工路径本次不重写。先获取名额再消耗冷却；self 创建还挂载 `LogExportRateLimit` |
| `model/log_export_job.go:ExecuteLogExportJob`、`model/log_export_scanner.go` | 执行器先进行使用日志重算、self 归属、汇总和列处理，扫描器把 `len(logs)` 同时用作扫描量、游标、进度、结束和限速依据。审计必须在上述 usage 前置逻辑之前分派，不能只替换最后一条 SQL |
| `model/log_export_job.go`、`model/log_export_writer.go` | 已有 Redis 元数据、CPU/速率控制、取消、分片和清理；新任务统一生成 CSV.GZ，历史 XLSX 仅保留下载兼容。当前进度公式只支持正序，审计倒序需显式扩展 |
| `model/log_export_management.go:LogExportAccess.Permissions` | 当前管理员可管理全部使用日志任务，员工可按客户关系访问自助任务。权限必须先按数据源分派；不能靠 URL 前缀判断，也不能把审计任务交给员工客户关系规则 |
| `controller/log_export_self.go:isSelfLogExportRoute`、`controller/log_export.go:logExportRequestAccess` | 现有 scope 和 enabled 只识别 `/api/log/self/export/jobs`。拟由路由显式构造 `usage_admin/usage_self/audit_admin/audit_self` 能力，杜绝 audit self 被误判为 admin |
| 下载处理及 `CurrentLogExportAccess` | 授权绑定实际访问者，兑换和续传复核当前账号/角色；尚不包含审计 Casbin 权限。授权只有 job/user/part/scope，没有节点，文件直接从本机路径打开，不能宣称已有节点定位或跨节点交付 |
| `setting/operation_setting/log_export_setting.go`、`log_setting.go` | 管理范围取 `admin_max_range_sec`；自助范围 getter 实际取 `log_setting.max_range_days`，旧 `user_max_range_sec` 仅兼容。自助开关为 `log_setting.user_export_enabled`；管理员即使走 self 也使用 `admin_cooldown_sec` |
| `web/src/features/system-settings/maintenance/log-export-section.tsx`、`service/settingsaccess/scopes.go` | 已有完整配置表单，按 `system-tuning.log-export` 授权，通过既有设置 API 保存；拟调整标题和说明为两类日志共用 |
| `controller/audit_filters.go:parseAuditFilter` | self 绑定当前用户；`bounded=true` 有固定 31 天限制，不能直接用于可配置范围的导出。拟抽出结构化字段校验，并显式传入导出范围上限，保留列表合同 |
| `model/audit_log.go:AuditLogQuery/ProjectAuditLogs` | 非 Root 排除 Root/未知角色；self 即使由 Root 发起也按用户投影。导出须先投影后渲染，禁止输出原始 other；任务需固定 self/admin/root 投影级别 |
| `model/audit_query.go`、`model/audit_security.go:OpenAuditSecurityDB` | 已有显式列清单、独立读池和连接构造器。现有 MySQL/ClickHouse 驱动还有固定 5 秒读超时，会覆盖更长的导出批次超时，必须显式处理 |
| `service/auditsecurity/ip_risk.go:QueryRiskRecords` | 先读活动告警候选，再读审计事件和标记；每次内部查询最多 100 候选，外层最多处理 1000；空结果页可能仍有下一游标。修订号变化会令整页失败，正常检测、调查状态改变和清理都可能推进修订号 |
| `router/api-router.go`、`middleware/access_token_routes.go` | 全量审计查询需要 AdminAuth + AuditRead；self 查询的 PAT 范围为 usage:read。现有使用日志导出全部要求控制台会话；审计导出拟沿用这一更严格边界，风险还要求安全权限 |
| `web/src/features/usage-logs/audit/components/audit-log-viewer.tsx` | 草稿与生效条件分离；令牌历史合成 category/token_ref/exclude_token_ref。导出必须使用最终 params 的白名单快照，不使用草稿、分页或当前风险游标 |
| `web/src/features/usage-logs/export/` | 任务中心绑定 usage API、`/usage-logs/$section` 路由、模板和新建表单；查询键只有 admin/self、用户和筛选。拟抽无路由/数据源耦合的任务组件，并把数据源和完整 scope 加入缓存键 |
| `controller/log_export_filename.go` | 下载显示文件名目前固定“使用日志”，需按数据源提供审计标题和安全摘要 |

已核对 `usage-log-export.md`、`usage-log-export-unification.md`、`export-list-management-visibility.md`、`audit-log-retention-and-role-projection.md`、`audit-log-filter-enhancements.md` 和导出、权限、风险分页测试。旧文档独立用户范围与当前代码不符，以统一 `log_setting.max_range_days` 为准。

## 后台配置：共用现有值

不新增 `audit_export_setting`、环境变量或单独保存接口。配置继续由 `setting/` 注册及现有设置 API 持久化，审计执行端调用相同 getter。

| 配置 | 两类日志共同遵循的行为 |
| --- | --- |
| `log_export_setting.enabled` | 总开关，关闭后拒绝新任务；已运行任务沿用现有取消和超时规则 |
| `log_setting.user_export_enabled` | “仅本人”导出开关，包括管理员使用 self 入口 |
| `admin_max_range_sec` / `log_setting.max_range_days` | 分别限制管理/仅本人范围，不另加审计固定 31 天总范围限制 |
| `rows_per_file`、`max_parts` | 每个分片条数和分片数；达到上限按现有失败逻辑处理，不静默截断。现无独立总条数配置，本次不伪造该配置或增加固定 10,000 条限制 |
| `batch_size`、`window_sec`、`batch_query_timeout_sec` | 每批扫描数量、时间窗口及查询超时；窗口自适应上限沿用现有 1 天，完整导出可跨窗口。审计可以设置更小的内部安全批上限，但不另加用户配置 |
| `max_concurrent_jobs`、`max_active_jobs_per_user` | 同一组 Redis 全局名额和用户任务额度；切换数据源不能绕过限制。并发为 0 停止接收新任务 |
| `user_cooldown_sec`、`admin_cooldown_sec` | 共用提交冷却，沿用现有按当前角色选择getter的规则：管理员 self 任务也用 admin 冷却。用户冷却 <=0 回退默认值；admin 冷却为负回退用户冷却，0 才关闭管理员冷却 |
| `max_rows_per_sec`、`batch_sleep_ms`、CPU 阈值及采样间隔 | 共用节流器；行速率是每任务速率。风险导出按实际扫描候选计费，不能只按命中行数限速 |
| 低峰时段、`timeout_sec` | 共用等待和总超时。实际外层超时包含限速、CPU 和低峰等待；同步纠正界面中“等待不计入超时”的旧说明 |
| `gzip_level`、`min_free_disk_mb`、`job_ttl_hours` | 共用压缩、磁盘保护及元数据/文件保留期；批次间也检查磁盘，不能只在创建和分片切换时检查 |
| 下载授权有效期、下载会话时长、每用户下载并发 | 共用短期授权、Range 续传与下载控制；现有下载并发为进程内计数，不宣称跨节点全局限制 |

任务开始时固定范围、输出选项与总超时；批大小、窗口、单批超时和节流按现有批次节奏读取，行数/分片/压缩/磁盘配置按写入节奏读取，TTL 按保存任务时读取，不跨任务缓存设置指针。前端打开面板时刷新能力和上限，提交时服务端重新校验。模板数量、汇总分组等配置仍仅作用于其对应使用日志能力。

## 数据流与共用实现

提交 → 审计认证/筛选校验 → 共用任务准入 → Redis 保存任务 → 执行器先按数据源分派 → 审计分批读取 → 字段投影 → 共用 CSV.GZ 写入/分片 → ready → 授权下载 → 过期清理。

1. 任务模型新增 `data_source`，白名单 `usage|audit`，缺省按 usage 兼容；与现有 `source=admin|employee|self` 分开。增加 `audit_filters` 保存 `AuditLogFilter` 快照；起止时间仍统一存于 `filters.start_timestamp/end_timestamp`。self 任务由服务端同时写入 `Filters.UserId` 和 `audit_filters.user_id`，以保留现有 self 归属不变式，查询只采用审计快照。
2. 从创建控制器提取共用准入流程，保留开关、范围、活动任务、磁盘、名额、冷却和 self 频率限制的现有语义。审计控制器负责自身凭证、权限和参数校验。usage 入口服务端固定 usage 并拒绝审计字段；audit 入口固定 audit；未知数据源在读取和执行时都拒绝，不能回退 usage。
3. 在 usage 重算、汇总、列解析和员工逻辑之前分派数据源。`model` 定义小型接口，使用日志为默认实现；`service/auditexport` 提供审计实现，在初始化时注册到固定白名单。`model` 不反向导入 `service`。数据源负责读取、投影、渲染和权限复核；任务准入、节流、窗口、取消及文件生命周期只有一套，员工创建流程不在本次重构范围。
4. 批次合同必须分别返回：可渲染行（允许为空）、实际扫描数、独立游标/扫描位置、明确 `done`、窗口密度和进度位置。**空行集合不等于扫描结束**。普通数据源按事件计扫描数；风险按候选计扫描数、按命中行计输出数。`exportGate.Before/Observe/Charge` 仍由共用扫描器调用一次，数据源不得重复计费；风险 `Observe` 覆盖候选、事件、标记和修订检查的整批耗时。
5. 从写入器抽出共用分片输出，接收表头、字符串行和事件时间。使用日志保留原列系统、重算和汇总；审计不伪装为 `model.Log`。复用公式防护、CSV 转义、BOM、gzip、磁盘检查和未完成文件清理。普通审计固定字段但 `content/other` 可能很宽，因此内部单批最多 1000 行（配置更小时取更小值）；共用写入器每批及分片切换检查磁盘，不宣称只靠行数即可保证内存/磁盘有界。
6. 列表在每个 API 入口服务端固定数据源与访问 scope，二者都纳入游标指纹；先按数据源和当前权限过滤，再生成页和游标。公开元数据不返回内部路径、授权串或他人的受限筛选值。下载文件名按数据源生成审计标题和已授权摘要，不再固定为“使用日志”。
7. Redis 不可用时提示已有错误，不降级成同步导出。可捕获的错误、取消、超时和 panic 必须清理已登记分片及当前未完成分片并释放名额，失败不能交付为 ready。进程崩溃的恢复保持现有限制：只扫描最近任务且按同名 NodeName 回收，不是通用节点故障租约；孤儿文件依赖现有启动/创建时被动清理和 TTL，不承诺即时或跨节点清理。下载 Range 续传不等于任务断点续跑。

### 普通审计数据源

复用 `AuditLogQuery`、`AuditLogColumns`，按窗口查询，不执行 COUNT/OFFSET。按 `(created_at DESC,event_id DESC)` 排序，游标为 `created_at < last_at OR (created_at = last_at AND event_id < last_event)`，不用 ClickHouse 中不能作为事件标识的 id。

共用窗口驱动支持数据源声明的方向：使用日志保持原方向，审计从新到旧，下一窗口结束于上一窗口起点减 1 秒。普通/风险审计保持一致顺序；进度按已完成时间范围计算，不能复用仅支持正序的现有公式；分片起止时间取实际最小/最大值。窗口密度和自适应均按实际扫描数，而非渲染后的行数。每批查询完成并释放连接后再投影、压缩和等待。

不持有长事务，不承诺跨库快照。迟到写入和保留期清理可能使结果与先前列表不同，不扩大范围补足行数。空结果沿用合法空 CSV.GZ 行为。

### 异常 IP 数据源

复用 `QueryRiskRecords` 的候选、过滤、投影及修订校验逻辑，抽出内部候选批次读取供页面和导出共用，不复制风险算法。页面仍保持单请求 31 天、最多 1000 候选及页大小合同；导出逐窗口调用内部批次，不受页面 31 天总范围限制，单窗口不超过共用配置的 1 天上限。

修订号在整个任务固定，每批和收尾复核；变化时失败并提示重新导出，窗口切换不能重置此约束。正常检测、调查状态变化和清理均可能推进修订号，因此活跃系统上的大风险任务可能反复失败；本方案不伪造快照或静默输出不一致结果。若未来要求风险长导出高成功率，需另做不可变快照设计。空命中批有下一游标须继续，不以 `len(rows)==0` 或 `rows<batchSize` 判完成。

在候选批次边界接入共用超时、取消及节流。实际候选数量回报 `scanned_rows`，命中输出数量回报 `row_count`；导出每个候选批最多 100 条（配置更小时取更小值），与事件查询及标记查询的现有上限一致。不得沿用页面外层一次处理 1000 候选后才计费的做法，也不增加固定页数预算。

仅导出已有风险标记、覆盖信息摘要；more 表示仍有未展开原因，不宣称是完整调查证据。普通审计不追加安全告警详情或外部 IP 查询。无安全导出权限时拒绝风险创建并显示原因，不能删除 `ip_risk` 后退化成范围更大的普通审计导出。

## API 合同与授权

以下入口已实现，共用底层任务服务和公开任务结构，不另建存储；每个入口固定数据源，不能通过请求参数切换。

| 路径 | 权限和操作 |
| --- | --- |
| `/api/audit/export/jobs` | AdminAuth + AuditRead + AuditExport + 控制台会话；POST 创建、GET 列表 |
| `/api/audit/self/export/jobs` | UserAuth + 控制台会话；POST 创建（保留 `LogExportRateLimit`）、GET 列表，服务端固定当前用户/self 投影 |
| 上述路径的 `/:job_id` | 同一 scope 与控制台会话；GET 详情、DELETE 取消/删除 |
| 上述路径的 `/:job_id/download-url` | 同一 scope 与控制台会话；GET 授权下载地址，保留 `part=all` 或正整数分片合同 |
| 现有 `/dl/log-export/:token` | 文件交付和 Range 续传；按授权的数据源及任务复核当前账号和权限，不新增公开下载路由 |

创建请求为 `{filters:{start_timestamp,end_timestamp,category,success,username,request_id,token_ref,exclude_token_ref,ip,actor_type,auth_method,route,route_missing,ip_risk},options:{header,csv_bom,timezone}}`。`success` 为可选 boolean，不是列表使用的字符串；类型/枚举沿用现有筛选，未知字段拒绝。固定 detail、csv_gz，不接收任意列、模板、路径或 SQL。self 不接收 `user_id/username/ip_risk`，用户归属由服务端设置。页码、page_size 及当前风险游标不进入创建请求。

响应沿用现有真实合同，不笼统返回完整任务：POST 为 `{success:true,data:{job_id}}`；GET 列表为 `jobs/next_cursor/has_more/enabled/max_range_sec`；GET 详情为公开任务；DELETE 为 `canceled` 或 `deleted`；download-url 为 `url/expires_in`。列表按当前 scope 返回正确的 enabled 和 admin/self 范围，审计固定字段不依赖 usage columns catalog。失败任务没有现成 retry API，本次不伪造；用户从原面板重新创建，若提供“重新导出”按钮也必须再次 POST、产生新 job ID 并重新校验权限、冷却和风险修订号。

所有新审计任务接口与现有使用日志导出一样，仅允许控制台会话，普通审计查询已有的 PAT 权限不自动扩展到导出。管理路由继续用 `handlePermissionRoute` 声明 AuditRead，但还需显式检查 `GetSessionAuthIdentity`，防止该 helper 的 PAT 声明使导出绕过会话边界；self 和任务操作在凭证路由表逐项声明 session，不能重复注册。风险创建、列表中风险项、详情、取消/删除和 download-url 额外要求 AuditSecurityRead + AuditSecurityExport；self 明确拒绝风险导出。

### 全生命周期权限

- 审计 self 任务仅本人访问；员工客户管理配置不能授权读取客户审计。管理任务不能因创建者降权变成 self。
- 管理审计要求当前有效账号、管理员角色、AuditRead 和 AuditExport。新导出权限不会从读取权限推导；创建、任务管理、worker、签名下载和 Range 请求均复核两者。创建时固定 `CreatorRole` 与 self 投影：Root 创建的非 self 管理任务按 Root 投影且始终只允许当前 Root 访问，即使实际结果恰好没有 Root 行；其他授权管理员只能管理非 Root 管理任务。升权不扩大旧任务投影，降权/撤权使管理任务失败。
- 风险审计在创建、后台复核、列表/详情、操作、download-url、签名下载及续传均复核安全权限；混在 audit 列表中的风险任务对无安全权限者逐条隐藏。`/api/log/...` 不返回或操作 audit 任务，签名下载则同时核对 grant、实际任务数据源和 scope，不能只信任 token 中的声明。
- 服务层构建请求内授权能力并交给任务权限判断；同一审计授权器在执行批次间复核创建者，最多每秒一次。复用 `ReadCurrentPermissions` 的当前数据库权限语义，不只看异步 Casbin 缓存；数据库访问使用有界审计读池。后台任务不因关闭页面或退出登录隐式取消，但账号禁用、管理权限撤销或安全权限撤销必须停止后续交付。
- 下载复核实际下载者当前状态、数据源权限和 Root 限制；已生成文件不能再做降级投影，权限不足即拒绝。签名下载路由本身没有控制台会话，短期 capability 只能由上述会话接口签发，兑换和续传仍须执行当前账号/权限检查；不宣称退出登录会立即撤销已签授权。参考 [OWASP 每次请求验证授权原则](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)。
- 审计下载使用新授权版本，显式绑定 `data_source=audit` 与 `audit_admin|audit_self` 范围；旧授权只接受 usage，新授权还须匹配实际任务和分片。授权没有节点字段，现有实现也没有跨节点路由：部署必须用任务 origin sticky 或新增 origin 重定向，非 sticky 多节点下载验收不得视为通过。本期不增加跨节点文件复制，日志不记录授权串。

## 字段、交互与国际化

### 2026-11-20 导出弹窗展示修整

本次为既有导出界面的局部排版修整，不增加功能或改变业务合同。已读取 `audit-export-panel.tsx`、`export-task-list.tsx`、共用 `Dialog`/`Table`、同模块员工导出记录和查看器测试。当前面板固定 `min(52dvh, 34rem)` 正文高度；七列任务表置于 `sm:max-w-3xl` 弹窗，单条任务也保留大片空白，日期和元数据不换行时产生横向滚动。

- 继续使用 Public Sans、既有背景/边框/次要文字主题 tokens 和原按钮 variants；不增加颜色或装饰性卡片。
- 两个标签统一 `sm:max-w-5xl` 宽度；桌面正文高度为 `min(44dvh, 20rem)`，窄屏为 `min(60dvh, 26rem)`，让切换前后的弹窗位置和底栏保持不变。长列表仅正文纵向滚动，滚动条预留空间固定。
- 标签栏持续挂载在同一位置，不随任务列表重新挂载。共用任务列表增加可选响应式呈现，审计入口启用，使用日志入口保持现状。
- 桌面保留七列表格，长筛选、错误与元数据允许换行，任务 ID 次级显示；窄屏同一条记录重排为信息块，保留字段标签、状态及下载/取消/删除操作，不重复挂载任务组件或请求。
- 复用现有翻译键，不新增文案、API、存储或配置。创建、游标分页、轮询、权限撤销、分片下载与删除确认沿用既有逻辑。

共享资源检查：仅改变浏览器 DOM/CSS 排布，沿用原查询缓存和请求频率，不访问新的 Redis 键、数据库表、服务端缓存、连接或工作池；主中继链新增成本为零。

验证场景：桌面/手机、单条/多条/空记录、长筛选与任务 ID、运行/失败/已完成状态、明暗主题；检查横向溢出、底栏可达性、标签切换及已有导出回归，并运行 typecheck、lint、构建。

本轮实际验证：

- 用户指出切换抖动后，已移除按标签改变宽度/自然高度的实现，改为上述统一尺寸和持续挂载的标签栏。
- production preview + 本地模拟接口：1440×960 桌面连续切换 6 次，390×844 手机连续切换 4 次，弹窗、标签栏及底栏位置均不随标签变化。桌面在 0/1/24 条记录间刷新时，弹窗保持约 1024×485，底栏位置不变；手机约 358×573。表格横向 `scrollWidth - clientWidth` 均为 0，长筛选与失败说明换行。截图保存在 `_local/audit-export-layout-20261120/`。手机最终布局以 DOM 尺寸验收，后台浏览器截图超时；不把计划中的明暗主题、运行态和真实下载验收写成已完成。
- `bun run test src/features/usage-logs/audit src/features/usage-logs/export`：最终 26 文件、268 项通过。`bun x tsgo -b`、两处改动文件的 oxlint 和 oxfmt 检查、`bun run build` 通过。
- `bun run typecheck` 仍因既有 `bun check -b` 脚本不存在而失败，使用已安装的 `tsgo` 完成类型检查。全局 `bun run lint` 被本轮未修改的 `log-detail-body.tsx:1098` 的 `react/no-array-index-key` 阻挡；未改写其他工作区变更。
- 本次未修改后台、语言包或五个上游拥有的认证/HTTP 文件，未部署。

固定字段：事件 ID、时间、用户 ID/用户名、角色、员工身份快照、类别、action、认证来源、IP、原始 UA、HTTP 方法、路由、HTTP 状态、业务结果、request_id、令牌指纹、content、投影后的 other JSON；风险附已有标记/覆盖摘要。员工身份 NULL 与 false 区分。JSON 经 `common.Marshal`，保留 AuditOther 大整数精度，不查询 users/tokens/channels 补充凭证或资料。

“导出审计日志”“导出任务”按钮复用当前模块 outline 与 actionEnd 结构，手机通过 compactMobile 保持在筛选抽屉外。只读管理员看到禁用按钮和缺少导出授权的说明。面板以最终 `params` 的不可变白名单快照初始化，去除 p、page_size、cursor，保留 accessOnly 的 category 和令牌当前/历史模式的隐含条件；`canQuery=false` 时禁止新建，但仍可进入导出任务。success 字符串在 API 层转 boolean。可修改导出时间范围并按后台上限校验，普通列表原有 31 天交互不限制独立导出范围；修改不反向改变列表草稿或生效条件。

提交后展示任务状态并进入审计任务列表；保留进度、取消、删除和分片下载。关闭弹窗不取消已提交任务。抽取不依赖 usage 路由、模板和新建表单的任务组件，通过 props 注入 dataSource、完整 scope、API base、能力/上限、筛选摘要和 onAccessDenied；审计入口位于 AuditLogs 自身授权流程内，不要求 usage 菜单权限。

React Query 键必须包含 dataSource、完整 scope、userId 和规范化筛选，创建/取消/删除按相同层级失效，不能复用 usage 与 audit 的列表和游标缓存。账号切换沿用全局 SID 清理，同账号 scope/权限切换也清理对应缓存。列表、创建、详情、删除和 download-url 的 403 都进入导出撤权流程，停止轮询、移除受限缓存并刷新用户权限；仅当确认 audit.read 也被撤销时才通知外层列表降级。没有 audit.export 的管理员不能导出；缺少安全导出权限时拒绝风险导出，不得静默放宽数据范围。

设置页改为“日志导出设置”，明确两类日志共用；self 范围仍指向既有“最长日志时间范围（天）”，不重复配置。新文案补齐前端七种语言、后端中英文，实施先读 i18n 技能；不修改受保护的认证/HTTP 基础文件。

## 数据模型与 Index Design

只扩展 Redis 任务元数据和下载授权，不新增 SQL 表、配置键或模板迁移。兼容承诺仅为**新代码读取旧 usage 任务**：旧任务缺省 usage，旧筛选、模板及完成文件保持兼容；未知数据源拒绝，不能回退为 usage。

旧二进制会忽略新 JSON 字段并可能把 audit 任务按 usage 执行，因此不允许新旧节点共享任务队列混跑。发布前先暂停新任务并排空所有共享 Redis 的 worker，再升级 API、worker、恢复和下载节点，完成后才开放审计创建。回滚也必须在启动任何旧二进制前暂停、排空并清理 audit 任务、文件及授权；仅增加版本字段不能防止旧节点误取任务。

审计查询有显式列和强制窗口，复用 `idx_audit_time_event(created_at,event_id)` 及用户、令牌、IP、身份、来源、路由索引；风险复用告警状态/事件关联索引。过滤条件在有界窗口内求交，不把 content/other 放进宽索引。索引名称存在不等于每种组合过滤都有覆盖计划，不能预先承诺所有筛选均为有界索引扫描。

2026-11-16 在隔离容器执行只读 SELECT VERSION()、EXPLAIN：显式 AuditLogColumns、时间闭区间、actor_role、双字段倒序游标、LIMIT 3000，另查 user_id 条件。`newapi-audit-triage-mysql-20261116` 返回版本 26.10.0，选择时间索引 + Sort，self 选择用户前缀索引 + Sort；`newapi-audit-triage-pg-20261116` 为 PostgreSQL 18.7，小表选择 Seq Scan + Sort。仅证明 SQL 可解释，不能证明大表计划或最低版本兼容。

实现验收需用代表性数据复核普通/风险计划，覆盖宽范围、单用户、令牌及稀疏条件，确认有界索引扫描；若现有索引不足，先补查询对应的索引和迁移设计，不上线无界扫描。当前不预设新增 DDL。MySQL 5.7.8、PostgreSQL 9.6 最低版本和 ClickHouse 尚未完成验证。

## Main Chain Impact

不修改中继链、计费、日志写入或检测入口；仅扩展 dashboard 导出凭证声明，不增加中继中间件。中继 goroutine 新增同步工作为零。新增扫描、生成及授权检查仅在管理请求或已获名额的后台任务中执行；复用导出总开关。

### Shared Resource Audit

| 资源 | 共享关系与保护 |
| --- | --- |
| audit_logs、安全告警/关联/修订表 | 与异步写入、页面和检测共享数据库；只读、窗口、游标、超时，无长事务或行锁，不占页面读池 |
| 审计导出连接 | 复用 OpenAuditSecurityDB 的 DSN/方言构造逻辑，节点创建主库/日志库专用池，各最多 1 连接，所有审计任务共享，关闭时释放，不触发迁移、不按任务开池。现有固定 5 秒驱动读超时不能覆盖可配置批次超时：导出构造器取消该读超时，以 context 的批次/总超时约束查询和连接等待，页面池保持原值 |
| 用户状态/Casbin 权限 | 后台低频检查；用户按主键、权限按当前数据库规则走上述主库池。必要缓存只读使用，不写中继缓存，不每行查权限 |
| Redis logexport:* | 共用任务、索引、slots、cooldown、cancel 和下载授权，这是导出间有意共享配额。已检索的中继业务不使用这些键，不增加中继调用；新旧节点混跑限制见上 |
| CPU、内存、磁盘、文件句柄 | 共用有界名额、CPU/速率、分片、磁盘和保留期治理；普通审计内部批次最多 1000 行，逐批渲染不累计完整文件，每批检查磁盘。超大 other 仍需实测峰值，不能只凭行数宣称恒定内存或绝不打满磁盘 |
| goroutine、下载计数 | 共用准入和取消监视，未获名额不启动 worker，不加入中继工作池；下载仍沿用进程内计数 |
| 本地文件与节点 | 文件由原节点提供，授权不含节点。多节点必须验证 origin sticky/重定向，不增加跨节点复制；同名节点恢复不是通用故障租约 |

数据库服务器、SQLite 文件锁、CPU 和磁盘仍可能物理竞争；独立连接不代表零负载，需共同配额和节流。每个主库/日志库池只有 1 连接意味着同一存储的审计 SQL 实质串行；全局任务仍可并发执行其他阶段，但连接排队计入批次和总超时。该容量取舍必须有测试，不能按全局 16 个任务推算数据库吞吐。

### Concurrency Analysis

30k/100k RPM 中继负载下，每个中继请求新增 SQL、Redis、锁、goroutine 均为 0。后台任务合计受同一 max_concurrent_jobs 限制；普通审计每批约 1 次日志 SQL，风险每候选批最多候选/事件/标记三类 SQL，另有修订复核。权限每活动审计任务最多每秒一次；进度/取消沿用原节奏。同一存储的查询还受单连接池串行约束，等待与限速均消耗实际总超时。实际吞吐取决于配置和连接容量，不声称已完成中继高负载压测。

## 错误处理、兼容与验收

请求层用 `common.ApiErrorI18n` 等标准辅助函数，业务失败 HTTP 200 + success=false，认证/授权沿用 401/403；不返回原始 Go/SQL 错误。请求日志带上下文，后台失败用 `SysError` 并保存可翻译错误码，同时把新增稳定错误码加入公开任务白名单。范围超限提示调整、资源忙提示稍后重试、风险版本改变提示重新导出、撤权提示权限已变化。失败不能交付部分结果为完整成功。

实施前先写会失败的回归测试，再按以下顺序实现和验证：

1. 抽共用提交、数据源及分片写入，回归使用日志明细/汇总/模板、用户/员工权限、管理员/self 冷却、节流/超时、取消、分片、当前未完成文件清理和旧格式。
2. 接审计普通/风险源，覆盖允许的大于 31 天范围、同秒事件、窗口边界、空命中批续游标、显式 done、修订变化、候选限速、倒序进度、宽行内存/磁盘、投影和大整数。风险修订变化测试必须模拟正常检测/调查更新，不只调用测试 helper。
3. 覆盖混合数据源任务共用额度/冷却、关闭/零并发、CPU/低峰等待超时、单连接排队、磁盘不足、Redis 故障、取消、panic 和同名节点恢复；不得把现有限制写成通用故障恢复已通过。
4. 权限覆盖两个数据源的入口隔离、任务枚举、Root 文件、降权/撤权、员工客户、全部新路由 PAT 拒绝、会话拒绝、安全权限、旧授权访问审计任务及 Range 续传。验证 usage 管理员无 AuditRead 时看不到 audit，audit self 不被识别成 admin。
5. 前端验证生效/令牌条件、success 类型、csv_bom、真实响应形状、配置范围、无 usage 菜单的审计入口、缓存隔离、账号/scope 切换、403、防重提交、关闭/取消和风险无 export 权限；运行 Bun 类型检查、测试、lint 和构建。
6. 按 `docs/production-environment.md`、`docs/release-checklist.md` 在独立 SQLite、真实 MySQL、PostgreSQL 及 ClickHouse 日志库验证，记录版本/命令/计划/未覆盖项；若增索引，还验证新库、升级和第二次启动无 DDL。另验证多节点 origin 下载或明确记录该部署能力缺失，不能用单节点下载替代。
7. 最后运行根模块完整 `go test ./...`、相关构建和前端标准检查。任何未运行的数据库、多节点或负载项都明确记录，不宣称通过。

## 实际实现与验证记录（2026-11-16）

### 实现落点

- `model/log_export_source.go` 定义来源、批次合同和审计读取器注册；`service/auditexport/source.go` 实现普通/风险审计读取、固定投影和当前权限复核。`model/log_export_scanner.go` 共用窗口、限速、扫描量和进度，审计倒序；`model/log_export_output.go` 共用分片生命周期，取消、异常和 panic 时清理当前未登记文件及已完成分片。
- `controller/audit_export.go` 严格解析审计请求并绑定会话与审计 scope；`UsageExportScope` 为 usage 路由显式绑定 scope，旧 URL 判断只保留为已有直接调用和员工入口的兼容后备。`submitLogExportJob` 共用准入和启动；员工创建路径保持原有入口。
- `model/log_export_management.go` 按数据源及完整 scope 隔离列表、操作、游标和下载；audit 下载授权版本为 3，旧版本只可访问 usage。列表另返回 `risk_export_enabled`，表示当前安全 read 与 export 权限共同允许风险导出。
- `model/audit_export_pool.go` 启动时建立主库/日志库各一条连接的专用池，所有审计导出及授权请求共用。查询、连接排队和首尾权限复核受 context 超时控制；首尾复核也使用批次查询超时，控制器授权上限为 3 秒。进程关闭时释放池，不新增表、索引或迁移。
- `service/auditsecurity/ip_risk.go:QueryRiskBatch` 为页面与导出复用的候选批次接口；页面仍保留原有 31 天和单页 1000 候选预算。风险修订号在任务内固定，正常 `UpdateAlertInvestigation` 操作引起的修订变化已纳入测试。
- `audit/export/audit-export-panel.tsx` 从已应用筛选拍快照；`export/components/export-task-list.tsx` 共用任务操作和下载。账号、角色、scope、权限变化会重置面板；403 清缓存并停止轮询。风险条件在失去安全权限时仍保留并阻止提交。后台设置标题改为两类日志共用，补齐七种前端语言及后端 en/zh-CN/zh-TW YAML 和 `i18n/keys.go`。
- 路由覆盖检查同时发现既有安全管理路由缺少凭据声明，补齐告警导出、归档、关联、IP 原因及 assignees 五项 session 声明；没有放宽 PAT 权限。

`AuditLogFilter` 新增 JSON 字段标签，以持久化 API 使用的 snake_case 筛选快照。这会改变风险列表游标的过滤指纹；升级前已经打开的风险分页需要重新查询，不承诺跨版本复用该短期游标。混合版本 worker 的发布/回滚限制仍按上文执行。

### 已执行验证

使用本次创建的隔离容器，不连接生产数据库：MySQL 5.7.44（utf8mb4）、PostgreSQL 17.11、Redis 7；主库为 MySQL，日志库为 PostgreSQL，另对读取器分别验证两种实际方言及 SQLite 回退。工具为 Go 1.26.11 windows/amd64、Bun 1.3.13、Node 22.20.0。临时凭据不写入报告。

| 验证 | 命令或方法 | 结果 |
| --- | --- | --- |
| 后端导出回归 | `go test -p 1 ./model ./controller ./service/auditexport ./service/auditsecurity ./router ./middleware -run 'Export\|AuditFilterEnhancements\|AccessToken\|Audit.*Route' -count=1 -timeout=12m`，使用隔离 DB/Redis 环境 | 通过；覆盖使用日志旧行为及新增审计读取、40 天范围、同秒/窗口边界、CSV.GZ 分片、公式保护、大整数、Range、数据源隔离、旧授权拒绝和降权 |
| SQLite/授权/i18n 定向回归 | `go test ./model ./controller ./service/auditexport ./service/auditsecurity ./i18n ./router ./middleware -run 'TestAuditExport\|TestExportUnknownSource\|TestRiskExport\|TestAuditFilterEnhancements\|TestLocales\|TestConstants\|TestAccessToken.*Route' -count=1` | 通过；无 DB 环境时需真实主库的控制器端到端用例跳过，其实际执行结果见上一行 |
| 前端全量 | `bun run test` | 262 个文件、2,986 项测试通过 |
| 前端最后改动复核 | `bun run test src/features/usage-logs/audit src/features/usage-logs/export` | 20 个文件、196 项通过，包含账号切换清缓存与安全 read 撤权后不放宽风险筛选 |
| 类型检查 | `node_modules/.bin/tsgo.exe -b` | 通过；环境中的 `bun run typecheck` 调用 `bun check -b` 后提示找不到 check，因此使用已安装的 TypeScript 检查器直接执行，未修改 package 脚本 |
| lint / 生产构建 | `bun run lint` / `bun run build` | 通过；lint 的既有 warning 和构建体积提示不影响退出状态 |

根模块 `go test -p 1 ./... -count=1 -timeout=15m` 已带隔离数据库完整执行：controller 等其余包通过，model 的既有工作时间预算用例及 service 初始化失败。前者已恢复扫描器扣除节流时间的原合同，任务外层 context 仍限制包含等待的总时间；后者是测试新建 schema 继承了隔离 MySQL 服务的 latin1 默认字符集，已把该测试实例的默认值改为 utf8mb4。随后完整 model 包通过（85.279s）；service 包出现一次既有 `TestUserChannelExclusionCleanupWorker/100` 的数据库 bad connection/超时，单独重新执行完整 `go test ./service -count=1 -timeout=15m` 通过（66.794s），未修改该清理功能或其测试。最终重新执行上述六个包的导出/权限回归全部通过，包含混源活动额度、列表/游标隔离及未完成文件清理。

此前未设置 SQL_DSN 的全量试跑曾因现有员工/会话测试空 DB 指针及 service TestMain 要求配置 DB 而失败；另发现本次新增翻译键未登记，已修正并通过 i18n 全套相关测试。全量命令的原始失败记录保留，以上按修复后的包级复跑结果记录，不将首次全量运行写成一次通过。

### 查询计划与剩余验收边界

在上述 MySQL/PostgreSQL 的独立 scratch 数据库/命名空间中复制现有表结构、写入 100,000 条合成审计记录（每条 content 500 字节），执行显式列、角色限制、闭区间、双字段倒序游标的 EXPLAIN，PostgreSQL 同时执行 ANALYZE/BUFFERS。普通宽范围走时间索引；self 和 token 走对应前缀索引并可能排序；稀疏用户名加排除 token 条件走用户名索引再过滤时间。此次 PostgreSQL 普通查询约 1.5–4.0 ms，不作为生产延迟承诺。用户名索引仍可能读取该用户窗口外历史，不能据此宣称所有筛选组合都仅扫描窗口内数据。

另以 100 个告警、100,000 条事件关联验证原风险候选 SQL。MySQL 在 ANALYZE 后仍使用告警表加关联索引、临时表和排序；PostgreSQL 刚装载数据、尚未 ANALYZE 时约 328 ms 且发生磁盘排序，更新统计信息后改用事件时间索引倒序扫描和主键关联，约 1.1 ms（还过滤了游标之前的 6,401 条记录）。**返回最多 100 个候选不等于底层 SQL 最多扫描 100 行**；计划受统计信息和数据分布影响，GROUP BY/HAVING 仍可能处理窗口内大量关联。已有查询没有新增到 relay goroutine，仍通过独立池、窗口、批次超时和共用节流约束，但生产大告警集需要单独核对计划与资源峰值。

未完成的发布验收：ClickHouse 实例、PostgreSQL 9.6 最低版本、真实浏览器/反向代理下载、多节点 origin sticky/重定向、极宽 other 的峰值内存/磁盘、节点崩溃恢复和 30k/100k RPM 中继混合负载。没有新增数据库 DDL，因此本次没有新增迁移的升级/二次启动验证；不能把上述查询测试解释为完整生产 schema 演练。未部署或发布。

授权实现参考 [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)、[Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [ASVS 5.0.0](https://owasp.org/www-project-application-security-verification-standard/)。服务端在请求、后台复核及下载/续传验证当前账号权限；前端检查仅用于反馈。已签发短期下载 capability 的登出撤销和节点路由限制仍按上文合同，不扩大保证范围。
