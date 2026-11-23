# 审计详情、导出权限与安全告警清理

## 已批准的追加范围：安全告警删除与清理

用户于 2026-11-17 明确要求实施并增加删除清理。新增独立 `audit_security.delete`，普通管理员默认不授予；与 `audit.read`、`audit_security.read` 及控制台会话同时校验。Root 可操作所有告警，管理员只能操作自身可见告警。

### Existing Logic and Related-Path Findings — 删除生命周期

- `model/audit_cleanup.go:CleanupAuditSecurityAlerts` 已有 `purge_state=deleting`、版本递增、每批最多 500 条子记录和持久扫描进度。现有检测合并、通知领取、IP 风险与关联查询已排除正在删除的告警。复用这个状态及子记录清理，不引入第二套软删除。
- `service/auditsecurity/worker.go:Start/cycle` 的原检测周期在监控关闭或暂停时不运行清理，故人工请求不能仅标记后依赖该检测周期。现已增加一个固定、可取消的清理循环，每 5 秒推进最多一个有界批次，使用已隔离的管理查询连接池，服务重启后自动继续。
- `delivery_lifecycle.go:claimDelivery` 不会领取已标记告警；已在发送的通知有 8 秒超时。清理等待当前发送，30 秒以上遗留 sending 记录可取消，避免停用通知后永久阻塞。已经发出的外部通知不能收回。
- `store.go:GetAlert` 原先可读到删除中的告警，现已补充排除条件。`SecurityEvent` 收据、检测基线以及原始 AuditLog 均保留，防止重扫产生重复历史告警；新的违规事件仍可生成新告警。

### API、交互和数据流

- `DELETE /api/audit/security/alerts/:id`，JSON `{version: number}`：对当前可见告警进行乐观版本检查，任何状态可由有删除权限者确认删除。返回 `{success:true,data:{queued:1}}` 表示已提交，不声称物理删除已经完成。
- `POST /api/audit/security/alerts/cleanup`，JSON `{closed_before: unix_seconds}`：只处理已解决/误报、关闭时间已知且不晚于指定日期的可见告警，每次最多 100 条；返回 `{queued:number,has_more:boolean}`。不接受角色、用户范围或任意筛选覆写。批量查询按状态、关闭时间、ID 排序，无 OFFSET；并发重新打开/更新造成版本不一致的行跳过，保留后续操作。
- 详情底部提供“删除告警”，使用既有 ConfirmDialog，说明将删除告警、快照、处理和投递记录，保留原始审计日志。列表工具栏提供“清理已关闭告警”，选择截止日期后确认；明确不跟随列表筛选、每次最多 100 条和可见范围，成功显示实际提交数及是否仍有更多。
- 标记与风险/活动 revision 在同一事务中提交；列表、详情、风险提示及已开始的风险导出能感知变化。操作另写现有管理审计 `audit_security.delete/cleanup`，此记录不会随告警删除。
- 异步清理依次删除关联索引、通知、规则快照、处理记录，最后删除父告警。子表按 alert_id 索引，单批最多 500；事务 3 秒超时；失败记录系统日志，保留状态供下次重试，不启动每请求 goroutine。
- 无新增可配置参数，复用告警保留期设置。人工清理独立于自动保留期及监控开关。

### Data Model、索引和共享资源

不新增表或字段。新增 `idx_sec_cleanup_closed(status,closed_at,id,visibility)` 支撑按关闭日期清理，新增 `idx_sec_purge_id(purge_state,id)` 支撑待删除队列；GORM AutoMigrate 在 SQLite/MySQL/PostgreSQL 创建索引。验收覆盖新库与旧结构升级、重复迁移不产生 DDL、真实数据库 EXPLAIN。

仅访问 audit_security_* 表、隔离的管理查询池；不读写 relay 的 tokens/channels/users、Redis 或热路径内存。固定一个后台循环，无新增数据库池，不占用 relay 连接；每 5 秒最多一批，单事务超时限制资源占用。删除权限检查沿用管理中间件。没有 AI 请求同步或异步钩子。

### 边界与验证

验证越权/Root 可见性、旧版本、重复删除、未关闭/关闭时间未知保留、批量 100 条边界、关联清理有界、发送中通知等待与遗留记录恢复、原始审计/收据不受影响、停用监控仍推进删除、风险和活动 revision 更新。业务失败沿用 HTTP 200 + success=false 本地化信息；无会话/权限不足为 403；底层错误记录日志，不返回原始 DB 错误。

日期：2026-11-17。状态：**用户已确认实施，并追加安全告警删除清理功能；本地实现完成，验证记录见文末，未部署。**

## Goals and Scope

针对用户反馈的详情查看不便、权限边界不清和找不到导出按钮，调整安全告警详情及审计导出入口，并补充管理员普通审计导出的独立授权。

具体结果：安全告警使用宽屏详情和分区导航，证据先看摘要再展开；普通审计列表常驻“导出审计日志”“导出任务”；管理员可以被授权查看审计记录而不能批量导出。导出继续使用现有使用日志共用引擎、后台配置和资源额度。

员工客户审计、多人负责人、自动账户处置等保留在 `audit-security-customer-response.md` 的独立范围内。本方案不把这些尚未实现的能力视为既有功能。

## Existing Logic and Related-Path Findings

下表记录实施前的实际代码核查及选定改造点，不是对截图所在部署版本的推断。实施后的界面、权限及删除生命周期合同见本文件对应章节。

| 调用链或组件 | 已验证行为及本次处理 |
| --- | --- |
| `web/src/features/usage-logs/audit/security/security-alert-dialog.tsx:59` | `sm:max-w-3xl` 弹窗依次渲染概览、规则、全部快照、全部事件、关联告警、处理历史和投递记录。刷新按钮处于正文内；截图中的窄列和长滚动符合此实现。调整呈现结构，保留已加载快照及“有更新，刷新查看”的合同。 |
| `service/auditsecurity/store.go:GetAlert` | 先按 `SecurityVisibleQuery` 查告警，再返回证据及最多各 100 条 findings/actions/deliveries；普通管理员过滤 Root 操作历史，投递不返回 destination。相关事件最多保留 20 条是已有采集合同，不代表全部规则命中。前端可以本地分页，但不能声称拥有完整历史。 |
| `security/security-event-card.tsx`、`security-rule-summary.tsx` | 已有操作名翻译、字段解释、复制 IP/请求 ID、查看关联审计和历史阈值缺失提示。保留这些展示逻辑，不用当前阈值补造历史结果。 |
| `security/security-relations.tsx` → `GetSecurityAlertRelations` | 关联查询按账号/IP/请求 ID、时间范围、游标和权限限定，选择关联类型后才请求。详情跳至关联告警有返回原告警入口。继续按需查询并保留返回路径。 |
| `components/dialog.tsx`、`components/dialogs/log-detail-layout.tsx` | 已有标题、正文滚动、footer、尺寸及内容高度配置；使用日志详情已有宽屏布局。复用业务 Dialog 和 DetailRow/DetailSection，不另建全局弹层。 |
| `components/data-table/README.md`、`static/static-data-table.tsx` | 现有 StaticDataTable 适合已加载的本地数组；后台列表仍用 DataTablePage。证据摘要表只展示当前响应，使用现有表格与分页控件。 |
| `router/api-router.go:470` → `controller/access_token.go:GetAuditLogs` → `controller/audit_filters.go:parseAuditFilter` | 全量列表要求 AdminAuth + audit.read；self 要求 UserAuth，并在服务端固定当前 user_id、清除 username。员工身份本身不授予查看客户或全量审计的权限。 |
| `model/audit_log.go:AuditLogQuery/ProjectAuditLogs` | 非 Root 排除 Root/未知操作者角色；本人视图清除 AdminInfo/AuditInfo，非 Root 清除 RootInfo；字段变更还有独立投影。不能靠前端隐藏敏感字段替代这些后端检查。 |
| `router/api-router.go:486`、`service/authz/resources_audit.go` | 安全监控要求 audit.read + audit_security.read；处理/归档另要求 manage，告警导出另要求 export。健康接口仅 Root；处理和导出还要求控制台会话。普通管理员的这些权限均没有默认授予。 |
| `model/audit_security.go:SecurityVisibleQuery` | 管理员只能查 visibility=管理员的告警；Root 可查所有。详情、关联查询和导出沿用这个边界。分配负责人不改变读取权限。 |
| `audit/components/audit-log-viewer.tsx:197` → `audit-log-filter-bar.tsx` → `logs-filter-toolbar.tsx` | 普通审计列表已经通过 actionEnd 渲染 AuditExportButton；桌面和手机分支均传递这个插槽。按钮目前只写“导出”，任务入口藏在面板的第二个标签页。 |
| `audit/export/audit-export-panel.tsx`、`api.ts` | 导出使用已生效筛选的白名单快照，包含令牌模式隐含条件，移除分页；成功后显示审计专属任务列表。账号/角色/权限变化重挂载；现有导出 403 会直接调用列表撤权回调，新增独立权限后必须区分“导出撤权”和“读取撤权”。 |
| `security/monitor.tsx` → `security/api.ts:exportAlerts` → `controller/audit_security_triage.go:ExportSecurityAlerts` | 安全列表已有“最多 100 条告警”JSON 导出，需要 audit_security.export；activity/recent_hits 视图不支持。它是有上限的告警证据导出，与原始审计 CSV.GZ 导出不同。告警详情内目前没有导出按钮。 |
| `controller/audit_export.go` → `service/auditexport/source.go` → `model/log_export_management.go` | 普通管理员审计导出目前只需 audit.read；尚不存在 audit.export。创建、任务查询、删除/取消、下载授权、worker 验权和签名下载已共用当前权限能力检查。风险筛选再要求安全 read/export。缺少的是普通导出的独立授权粒度，不是完全没有后端鉴权。 |
| `controller/log_export.go:DownloadLogExport` | 签名下载再次查当前下载者状态、权限、数据源及任务范围；Root 创建的管理导出不能由普通管理员取得。每次 Range 请求重验，但不能承诺撤权会追回已下载字节或中断已开始的同一次响应。 |
| `service/authz/current_permissions.go`、`web/src/features/users/components/admin-permissions-editor.tsx` | 导出读当前数据库规则，不只信任异步 enforcer；权限编辑器从后端 catalog 动态渲染。可增加一个 audit action，无需另建权限页面或存储体系。 |

同时核对 `audit-log-export.md`、`audit-security-ui-alignment.md`、`audit-security-customer-response.md` 及相关测试。导出文档记录本地实现完成但尚未部署；本轮没有核对截图对应服务器的运行二进制/前端包，**按钮在线上缺失的具体原因仍待目标版本核验**，不能断言是浏览器缓存或权限导致。

## 界面与交互

### 安全告警详情

1. 复用 Dialog，桌面宽度上限采用项目已有 `sm:max-w-4xl lg:max-w-6xl` 组合，受视口宽度约束；手机接近全屏。标题和底部关闭/刷新操作固定在共享 Dialog 的 header/footer，正文只有一条纵向滚动路径。
2. 四个标签：**概览、证据、处理记录、通知**。默认概览同时显示风险、状态、操作者、资源、负责人、首次/最近发生时间及规则说明；桌面双列，手机单列。次要归档/关闭时间放在概览下方，字段完整保留。
3. 证据标签包含规则快照、相关事件、关联告警。事件先以紧凑表列出时间、动作、操作者、目标、结果，选中后复用 SecurityEventCard 展示完整字段；手机用可展开摘要。快照和历史每页 10 条，仅对已返回数组本地分页，页码标明“已加载记录”，不改变后端上限。
4. 复用 DetailRow/DetailSection；如正文需要从 text-xs 调整至 text-sm，在共享组件增加可选显示尺寸，默认值保持原样，只由本详情选择较大字号，不全局覆写既有日志样式。
5. 切换标签不重新发详情请求；关联查询仍按需执行。15 秒轮询只提示变化，点击刷新才替换正在阅读的快照。切换 activeId 时重置本地分页和证据选择；返回原告警可用。账号/权限作用域变化清除旧快照和关联结果，403 时不显示旧数据。
6. “查看关联审计”继续传递请求 ID，或 IP 与事件前后 15 分钟，并切至全量审计列表；不把筛选命中等同于全部证据。保留旧告警无快照、资源归类不完整、保留条数等提示。

### 导出入口

1. 普通审计列表工具栏常驻两个明确按钮：**导出审计日志**、**导出任务**。复用 LogsFilterToolbar 的操作插槽；手机使用现有 compactMobile 换行布局，按钮不放进高级筛选抽屉。保留令牌安全页复用 AuditLogViewer 的入口。
2. 两个按钮打开同一个 AuditExportPanel，增加明确的初始标签参数。导出任务按钮不因列表草稿时间不合法或当前令牌为空而禁用；新建导出仍按最终 params 和后端能力验证。
3. 有 audit.read 但无 audit.export 的管理员看见禁用的导出入口及“未获授予导出权限”说明；本人视图沿用导出总开关及本人导出开关。服务关闭/不可用在面板内显示真实原因，不把按钮消失当作反馈。
4. 普通导出保持后台任务、压缩 CSV、进度、取消、删除、分片下载、时区和 BOM 选项。关闭面板不取消任务，重新打开“导出任务”可以继续查看。
5. 安全监控列表明确标为**导出告警（最多 100 条）**，继续输出 JSON，显示提交中状态；不支持的视图给出切换视图的说明。它使用既有安全导出权限，不改成普通审计导出，不在详情伪造“导出本条”接口。

## 权限方案与兼容性

新增 `audit.export`，在“用户 → 管理员权限 → 审计日志”中与读取权限并列，由已有 catalog 渲染。

| 身份/能力 | 本人审计 | 全量审计 | 普通管理审计导出 | 安全告警 |
| --- | --- | --- | --- | --- |
| 普通用户/员工身份 | 本人查询；本人导出受配置开关约束 | 不允许 | 不允许 | 不允许 |
| 管理员，audit.read=false | 本人范围 | 不允许 | 不允许 | 不允许 |
| 管理员，audit.read=true、audit.export=false | 本人范围 | 普通用户及管理员记录 | 不允许创建、枚举管理导出或下载文件 | 仍分别由安全 read/manage/export 控制 |
| 管理员，audit.read=true、audit.export=true | 本人范围 | 同上，排除 Root | 允许可见范围，风险筛选还需安全 read/export | 同上 |
| Root | 本人视图仍按本人投影 | 全部 | 全部管理导出 | 全部；设置仍按现有二次验证 |

`audit.export` 不隐式授予 `audit.read`；两者必须同时满足。权限描述说明这个依赖，避免改变其他资源的 view/edit 归一化规则。

**已确认的兼容性变化：**普通管理员的新 export 权限默认关闭，不从已有 read 自动推导；Root 不受影响。升级后原来仅有 read 的管理员仍可看记录，但其管理导出任务和下载被拒绝，运行中任务会在下一次后台权限复核停止。确有导出职责的管理员由 Root 明确授权。本人导出仍按本人合同，不以新管理权限限制本人数据。

落点：

- `service/authz/resources_audit.go` 注册 AuditExport；增加 catalog 描述及对应前端七语言翻译。
- `router/api-router.go` 的管理审计导出组同时要求 AuditRead/AuditExport，保留 AdminAuth、DisableCache 和 AuditExportScope 会话绑定；self 路由保持独立。
- `service/auditexport/source.go:currentAccess` 在同一次当前规则查询中读取新权限；`model.LogExportAccess` 增加仅请求内的 AuditExport 能力，`Permissions` 必须同时验证 read/export。
- worker 的 `Reader.Validate`、签名下载和 Range 续传继续通过上述共用判定，不只在 POST 或按钮上加判断。风险权限及 Root 文件边界继续叠加。
- 导出前端 403 先关闭敏感任务内容、取消/删除对应缓存并刷新本人 profile。只有确认 audit.read 也撤销后才调用列表的 onAccessDenied；单独撤销 export 不应把全量审计错误降为本人。profile 刷新失败则关闭导出，维持后端拒绝，不放宽风险筛选。

## Data Flow

详情：用户选择告警 → 有权限的详情 API → 当前可见告警及保留数组 → 分区/本地分页 → 按需关联查询或回查原始审计。展示状态仅浏览器组件内存；关闭即卸载，查询沿用 gcTime=0 与 AbortSignal。

导出：已生效筛选快照 → 控制台会话、当前权限和范围验证 → 现有共用准入 → Redis 任务 → 已获并发名额的后台执行器 → 分批查询/角色投影/权限复核 → 共用 CSV.GZ 分片 → ready → 短期下载授权 → 下载及续传再次验证 → 原有 TTL 清理。创建失败不生成成功任务；权限变化失败不交付未完成文件。

## API Contracts

普通审计导出沿用既有 endpoint；catalog 新增 `audit.export`、`audit_security.delete` action，profile 权限矩阵相应增加字段。删除清理接口见文首补充。

| 入口 | 请求/响应及授权 |
| --- | --- |
| `GET /api/audit`、`GET /api/audit/self` | 沿用 AuditFilters 和 `{success,data:{items,total,...}}`；前者 AdminAuth + AuditRead，后者 UserAuth 固定本人。 |
| `GET /api/audit/security/alerts/:id` | `{success,data:{alert,events,findings,actions,deliveries}}`；AdminAuth + AuditRead + AuditSecurityRead，加告警 visibility。 |
| `GET /api/audit/security/alerts/:id/relations` | 原 relation/start/end/cursor/limit 与 `{items,next_cursor,coverage}`；读取权限及可见性不变。 |
| `POST /api/audit/export/jobs` | 原 `{filters,options}`，返回 `{success:true,data:{job_id}}`；管理会话 + audit.read + audit.export，风险再加安全 read/export。 |
| `GET /api/audit/export/jobs`、`GET .../:job_id` | 原任务分页/能力及公开任务形状；管理会话 + 两项审计权限，逐任务检查范围。 |
| `DELETE .../:job_id`、`GET .../:job_id/download-url` | 原 canceled/deleted 或 url/expires_in 响应；相同管理权限。 |
| `/api/audit/self/export/jobs` 及子路径 | 同类合同，UserAuth + 会话，绑定本人；不接受客户端 user_id、自选 scope 或风险筛选。 |
| `GET /dl/log-export/:token` | 既有签名 capability；绑定数据源/范围/任务/分片，兑换与每次续传校验当前权限。 |
| `GET /api/audit/security/alerts/export` | 原筛选、limit<=100、JSON `{success,data:{exported_at,filters,truncated,items}}`；管理会话 + AuditRead + AuditSecurityRead + AuditSecurityExport。 |

401/403 仍由既有认证/授权路径处理，业务错误沿用现有 `{success:false,message}`；不引入前端自造的成功状态。

## Data Model Changes / Index Design

不增加表或列。安全告警清理新增两个索引，定义及迁移验收见文首；详情呈现与导出权限不另增索引。新增权限使用既有 `casbin_rules` 策略记录；`LogExportAccess.AuditExport` 是请求内能力，不是持久化任务字段。老任务下载受当前授权约束，无需重写 Redis 任务。

审计日志及告警读取的时间范围、游标和分页上限保持既有合同。权限查询保持同一条按主体/资源筛选的 SELECT，不增加逐行权限查库。新增清理按状态及关闭时间取最多 101 个候选，待删除队列按 ID 取最多 20 个父记录；子表沿用 alert_id 索引并限定每批 500 条。

## Config Parameters

无新增配置。继续共用 `log_export_setting` 的开关、范围、并发、冷却、扫描/速率、磁盘、分片和过期控制，以及 `log_setting.user_export_enabled`。不创建审计专用导出配置或另一个 worker。

## Error Handling and Edge Cases

- 保留加载/失败/重试/空状态的区分；详情失败隐藏旧证据，关闭仍可用。
- 本地页码不能声称代表未加载的全部历史；无旧阈值/快照时展示缺失事实。
- 导出使用已生效条件，不把用户尚未提交的草稿或当前分页当作导出范围；任务页独立可打开。
- 仅撤销导出权限不抹掉读取权限。撤销安全权限时风险条件不可悄悄移除后转成更宽范围导出。
- 防重复提交；取消/删除沿用服务端状态机。列表刷新或关闭面板不隐式取消后台任务。
- 禁止跨账号、scope、usage/audit 数据源串用缓存、任务或下载授权；只读管理权限不能借 self 接口下载管理导出。
- 不复制服务端内部错误；复用 createServerError/handleServerError，后端记录原始失败并返回现有安全消息。新增 UI 文案使用七语言 i18n；涉及新增后端用户文案时同步实际项目的语言文件。

## Main Chain Impact

本次没有 relay goroutine 上的新增同步工作，也不挂接日志写入、计费或检测入口。变更为管理页面呈现、已有管理导出的授权条件，以及安全告警管理事务和独立清理循环。

### Shared Resource Audit

| 资源 | 与主链关系及约束 |
| --- | --- |
| `users` | 中继也访问；本次仍只由已有导出独立读池按主键读状态/角色，不更新用户余额或扩大查询，不加行锁。 |
| `casbin_rules` / enforcer | 管理权限现有存储；新 action 随现有查询批量读取，不增加 relay 鉴权调用，不新增全局互斥。权限保存复用既有事务及发布机制。 |
| `audit_logs`、`audit_security_*` | 原有审计读/导出查询；清理仅删除安全告警父子记录，不删除原始审计日志。不新增中继读取或采集，不扩大采样上限。清理与既有保留期任务按相同顺序锁定 cleanup state，再锁父告警，子记录每批最多 500。 |
| 审计读池、审计导出池 | `AuditReadDB` 各池 2 连接；`AuditExportDB` 主/日志池各 1 连接，均为已存在独立池。本次不新开池；管理读取仍有超时。独立池不等于与同一数据库实例隔绝 IO，导出继续受共用节流限制。 |
| Redis `logexport:*` | 既有导出任务/并发/冷却/授权命名空间；已检索 relay/middleware/service 的直接引用，无新增中继键冲突。不增加键命名空间或轮询频率。 |
| 导出文件、worker、下载并发 | 全部复用现有有界任务和 TTL；不从浏览器逐页全量抓取，不增每条日志 goroutine。安全告警清理仅增加一个固定后台循环，每 5 秒一批，事务超时 3 秒，与服务一同停止。 |
| 浏览器查询缓存/本地分页 | 按用户、权限、告警/导出 scope 隔离，不持久保存审计证据；与服务端 relay 内存不共享。 |

### Concurrency Analysis

在 30k/100k RPM 中继流量下，本次每个中继请求增加的 DB 调用、Redis 调用、锁和 goroutine 均为 0。管理导出仍在请求/后台批次边界复核，普通管理员一次复核为既有用户主键查询 + 一次权限规则查询；只增加后者判断的 action，不增加 SQL 次数。详情切换标签/证据页不产生新请求，关联查询继续显式触发。不以此静态分析宣称已完成混合负载验收。

## Implementation and Validation — 2026-11-17

实现已落地：宽屏四分区详情、折叠规则快照、事件摘要/选中详情、已加载记录每页 10 条；独立导出和任务按钮；`audit.export` 与 `audit_security.delete`；单条删除及关闭日期清理。删除成功后刷新列表并重置分页，标记后即从详情/风险视图隐藏；物理删除由固定循环继续推进。两个新权限均不默认授予普通管理员，Root 保持完整权限。

| 验证 | 实际结果 |
| --- | --- |
| `cd web; bun run test src/features/usage-logs/audit` | 14 个文件、184 项通过。覆盖导出入口、筛选快照、403 读取/导出撤权区别、已有详情和关联路径。最后调整折叠快照与删除后刷新后，再跑 `.../security/monitor.test.tsx`，15 项通过，包含确认删除/版本、清理日期/更多提示及本地分页保留。 |
| `cd web; bun x tsgo -b`、`bun run lint`、`bun run build` | 全部退出码 0。现有 `bun run typecheck` 脚本实际调用不存在的 `bun check -b`，因此改用项目已安装的 TypeScript 编译器验证，未修改该脚本。lint 有既有 warning，构建有既有体积提示，无 error。 |
| `go test -p 1 ./service/auditsecurity ./service/auditexport ./service/authz -count=1 -timeout=6m` | SQLite 服务回归通过；清理用例同时确认原始 AuditLog/SecurityEvent 保留、Root 隔离、版本冲突、100/500 条边界、sending 等待/超时恢复、停用监控仍清理。 |
| 真实数据库服务矩阵 | MySQL 5.7.44、MySQL 服务报告版本 26.10.0、PostgreSQL 18.7（Debian）全部通过 `go test -p 1 ./service/auditsecurity ./service/auditexport -count=1 -timeout=12m -v`。使用本地既有实例、每次新建独立 `audit_test_usability_*` 库，完成后只删除本次创建的库；未对共享开发库或线上数据执行清理。 |
| 迁移与索引 | 上述每个数据库覆盖新库、原结构升级、第二次迁移无 DDL、旧版安全告警结构升级；10,050 条告警的 EXPLAIN 验证关闭日期查询使用 `idx_sec_cleanup_closed`。待删队列使用 purge_state 索引；PostgreSQL 可选择既有单列索引，再对少量候选排序，测试接受该有界计划。 |
| MySQL 主库 + PostgreSQL 日志库 | 完整 model、router、middleware、authz、auditexport 包通过。controller 全量未通过，具体差异见下段。本次接口定向回归 `go test -p 1 ./controller -run '^Test(AuditExport\|SecurityDeletion\|SecurityAlertQuery\|SecurityAssignee\|RejectNegativeSecurityAssignee\|SecurityProviderSecrets)' -count=1 -timeout=6m -v` 通过。 |
| 浏览器 | 本地 production preview + 明确的模拟接口，1440×1000、390×844 实测；浅/深色、四区切换、折叠证据、删除/清理确认、日期未选禁用、导出任务直接进入、Enter 打开/Escape 关闭及返回原按钮焦点通过。手机页面宽度 390、scrollWidth 390，footer 操作在可视区域；控制台无 error。未向真实服务执行删除或导出。 |
| 翻译与受保护文件 | 7 个 locale 各补 18 个键，法语/日语另修正 Evidence 译文；JSON 均为 UTF-8 无 BOM。已删除临时翻译脚本。5 个受保护认证/HTTP 文件相对当前工作树基线无改动。 |

全量 controller 回归的两个失败项是 `TestAIAuditSQLiteFixtureRestoresTokenLookup` 和 `TestAPITokenAuditDatabaseMatrix` 的 `configuration_values_stay_private` 断言：它们要求整条事件不包含模型限制/IP 配置，工作区已有 `controller/audit_field_changes_test.go:TestAuditTokenConfigurationValuesUseProjectedChanges` 则明确要求这些值保存在投影后的字段变更中。本轮没有修改令牌更新实现或这些测试；这是当前工作区的测试合同冲突，不能将全量回归表述为全部通过。本次清理测试曾出现配置快照取值编译错误，已修正并在上述全部数据库重跑通过。

运行记录、数据库脚本及截图位于本地忽略目录 `_local/audit-usability/`。未部署，未对生产 schema dump 做升级演练，未执行混合中继负载测试；线上按钮及实际包版本仍需发布时核验。发布前仍须按 `docs/production-environment.md`、`docs/release-checklist.md` 对目标 schema 演练索引迁移，并由 Root 给确需导出/删除的管理员授予新权限。

授权设计遵循 [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) 的最小授权、默认拒绝、每次请求校验及对象范围检查。上述代码核查和有限回归不能等同于整个系统已通过安全审计。
