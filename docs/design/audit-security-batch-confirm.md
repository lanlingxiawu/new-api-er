# 安全告警批量确认

2026-11-23。用户反馈“一个个点确定太麻烦了”，方案给出后进一步指定复用截图中的现有分页栏样式。本轮按该交互方向继续完善本页批量确认及分页，实施结果在文末记录。

## Goals and Scope

在安全监控列表工具栏增加 **确认本页（N）**：点击一次确认当前页全部待处理告警，无额外弹窗、负责人、结论或备注。用户可先使用既有规则、等级、账户、状态筛选缩小范围。每页默认 20 条，可通过现有样式的下拉框选择 10/20/30/40/50/100 条，最近命中视图固定 20 条；按钮明确标识本页，处理数量只包含 `open` / `investigating`。

保留原有单条确认。此次仅扩展管理员安全监控列表，个人安全及员工客户入口保持原有授权和操作方式。批量确认更新处理状态，删除仍通过既有清理入口完成；不自动确认其他页或之后新出现的告警。

## Existing Logic and Related-Path Findings

已阅读以下实际调用链及相关测试：

- `web/src/features/usage-logs/audit/security/monitor.tsx`：实施前使用游标分页，普通视图每页 50 条，最近命中固定 20 条。列表每 15 秒刷新（activity 除外）；现有表格禁用行选择，操作按钮通过 `SecurityFilterBar.actions` 插槽组合。
- `security-alert-columns.tsx`：处理状态列使用 `AlertConfirmButton`；管理入口由 `audit_security.manage` 控制。
- `alert-confirm-button.tsx`：直接提交 `resolved`，成功后统一失效 `audit-security`、`audit`、当前用户的 `customer-audit` 缓存。普通告警提交当前 version；有效重复操作告警通过 `lib/repeat-operation.ts:canConfirmRepeatAlert` 附带已看到的 `observed_occurrences`。后者不能被批量流程省略。
- `api.ts:saveAlert`：调用已有 `POST /api/audit/security/alerts/:id/actions`，业务 `success:false` 抛出保留来源的服务端错误。该方法不会逐条弹 Toast，可以复用为批量请求。
- `router/api-router.go`：该入口要求 AdminAuth、AuditRead、AuditSecurityRead、AuditSecurityManage；`controller/audit_security.go:ActOnSecurityAlert` 还要求会话身份。逐条请求继续接受既有校验，不新增权限或接口。
- `service/auditsecurity/investigation.go:UpdateAlertInvestigation`：事务内查询角色可见性、拒绝清理中的记录；普通确认要求版本一致。重复操作确认只接受已观察计数，人工/响应修改仍造成冲突；更新 closed_at、version、处理记录及风险/活动 revision。状态确认不会执行封禁或凭据清理。
- `monitor.test.tsx` 已覆盖一次点击无弹窗、无管理权限、重复计数语义、旧响应不能确认新版本、失败后可重试。`service/auditsecurity/repeat_operation_test.go`、`store_test.go` 覆盖后端并发版本合同。
- 已检查共享 `DataTablePage`、`DataTableBulkActions`、`useDataTable`、移动卡片及用户/令牌表选择列。此次采用现有工具栏 actions 插槽加入本页按钮，不增加勾选步骤；共享分页只增加可选参数，默认行为保持兼容。
- 相关设计：[安全告警复核收尾](audit-security-review-completion-20261123.md)、[审计详情、导出权限与安全告警清理](audit-log-usability-and-export-access.md)。批量确认成功会通过既有后端写入真实关闭时间，后续可进入已关闭告警清理流程。
- 截图对应共享 `data-table/core/pagination.tsx` 的普通分页样式。该组件已有每页条数下拉框、数字页码及当前页强调；cursor 分支主动切换为固定条数和文本页码。`securityAlertFilter` 实际允许 1–100 的 limit，activity 游标绑定 limit，变更页大小必须重置游标。`AlertPage` 只返回 next_cursor；capacity 是未应用当前筛选的有界容量样本，不能充当当前结果总数。

## Data Flow and API Contracts

点击按钮 → 固定当时本页待处理告警的 ID、version、重复计数快照 → 最多 4 个并发请求分批调用既有 actions 接口 → 各自事务提交 → 汇总成功/失败/未处理数量 → 统一刷新列表及关联缓存。

沿用请求：`{ version, status: "resolved", note: "", observed_occurrences? }`。沿用响应：`{ success, message?, data? }`；无新增后端批量接口。每条独立成功或失败，不声明整个批次原子提交。

## Data Model, Index Design and Configuration

无新增表、字段、迁移、SQL、索引、环境变量或设置。所有读取和更新仍使用现有按主键定位的确认事务；当前页面数据已加载，不为批量操作遍历全库或额外查询所有筛选结果。

## Business Logic and Interaction

- 按钮只对具有管理权限的用户显示，标明可处理数量；空列表、无待处理项、无有效时间范围、列表加载失败或批量执行中不可点击。
- 以点击瞬间的列表快照固定处理对象。轮询、换页、筛选或新增事件不会加入已开始的批次；请求始终携带快照版本，不自动重读版本覆盖冲突。
- 将管理员单条确认的提交逻辑提取为稳定可复用的 `confirmSecurityAlert`，单条与批量共用，保留重复告警 `observed_occurrences` 的特殊合同。员工客户分支继续调用客户专用接口。
- 批量执行期间禁用本页确认与逐条确认，显示已处理/总数；同一批次不能重复提交。只在完成时汇总提示，不生成几十条成功通知。
- 普通单项业务失败保留其他项的处理结果；网络/认证失败、页面卸载或登录账号/权限范围变化后停止发出剩余请求。已发送请求的结果可能已经提交，重新进入或重试前刷新列表；不声称停止能撤销已发送请求。
- 结束后统一失效既有相关查询。失败项通过刷新后的待处理状态继续处理，不自动重试，不把过期版本的成功结果应用到更新版本。
- 本页按钮沿用相邻操作按钮的样式、加载反馈和响应式换行；所有新增文案补齐 7 种语言。
- 共享分页新增可选能力，由安全监控启用：每页条数选择、数字导航到已访问页及可确认存在的下一页。复用原有 Select/Button 样式；旧 cursor 调用方保持原行为。保持“本页条数”文案，不伪造总计、未加载的任意页或末页入口。每页条数改变时回到第一页并保留筛选时间范围。

## Error Handling

请求复用 `saveAlert` 的业务失败检查。批量流程记录每条结果，汇总一次成功/失败/未处理数量，并通过既有 `handleServerError` 提示刷新列表后重试；首个错误用于既有错误去重，不直接向用户展示内部错误。不会把部分成功显示为全部成功。保留单条入口的既有错误反馈。

## Main Chain Impact and Shared Resource Audit

此次仅前端编排既有管理请求，无新增 AI relay 同步调用、查询、锁、goroutine 或缓存。批量请求上限由本页大小限定，并发最多 4。

沿用监控隔离数据库池及 `audit_security_alerts`、`audit_security_actions`、指派关联读取、风险/活动 revision 表；未新增用户/令牌/渠道写入或 Redis 操作。原有管理审计记录沿用既有审计写入队列。批量可能与后台检测竞争同一告警版本，由既有 CAS 冲突机制处理，不添加主链协调锁。

## Concrete Files and Verification

- `security/monitor.tsx`：本页待处理数量、按钮、页大小状态、游标重置。
- `security/use-alert-batch-confirmation.ts`、`security/lib/confirm-alert.ts`：批量 mutation、快照/进度、并发上限、停止条件及统一刷新。
- `components/data-table/core/pagination.tsx`：兼容扩展 cursor 的页大小选择和已知页码，复用普通分页样式；README 记录选项。
- `security/security-alert-columns.tsx`：接受批量忙碌状态并禁用单条确认。
- `security/alert-confirm-button.tsx`、新增 `security/lib/confirm-alert.ts`：复用管理员提交语义，保留客户专用分支。
- `security/monitor.test.tsx` 及必要的提交逻辑测试：多条一键确认、已关闭项跳过、权限、固定页面快照、并发上限、部分失败、重复点击、账号切换/卸载后不继续、重复操作计数、版本冲突后不静默重试。
- 七语言通过 `add-missing-keys.mjs` 和 `bun run i18n:sync` 写入；完成后删除临时脚本。
- 验证受影响前端测试、`bun run typecheck`、作用域 lint/格式；浏览器检查桌面/手机、深浅色及处理进度。无后端或数据库行为变更，既有版本校验测试作为合同参考。

## Implementation and Verification Results

已完成上述实现。默认每页 20 条，页面大小上限 100；导出仍单独固定上限 100。最近命中视图保持 20 条，不提供分页。共享分页的新选项用于默认布局；compact 布局维持原有紧凑控件。

批量任务执行到相关列表刷新结束之前持续禁用重复提交；普通业务冲突保留其他项的结果，权限、账号或会话变化以及网络失败会阻止后续批次。已有请求可能仍然提交，前端不会将其描述为撤销。

2026-11-23 本地验证：

- `bun run test src/components/data-table src/features/usage-logs/audit/security/monitor.test.tsx src/features/customer-console/customer-audit-dialog.test.tsx`：4 个测试文件、74 项通过，覆盖分页边界、条数切换与游标重置、导出上限、批量筛选、重复计数、部分失败、4 并发限制、固定快照、重复点击、账号/权限变化及卸载后停止。
- `bun run typecheck` 通过；8 个源码文件的 oxlint、保留版权头的格式检查及 UTF-8 无 BOM 检查通过；5 个新增文案在 7 种语言中均存在、占位符一致。i18n 同步后已移除临时脚本和无关的品牌未翻译报告变动，`git diff --check` 通过。
- 本地浏览器使用真实组件及隔离的模拟接口，1440×960 / 390×844、浅色 / 深色四种组合均通过：第一页 20 条、第二页 19 条，切换 100 条后回到第一页，一次确认全部 39 条且最大并发 4；进度期间操作禁用，完成后刷新为 0 条待处理，只显示一次汇总通知。无浏览器运行错误和页面/分页栏横向溢出。
- 视觉证据及脚本保存在本地忽略目录 `web/_local/security-batch-pagination-review/`；未向真实服务发送确认或清理请求。
- 无后端和数据库代码修改，无迁移、依赖变更或部署；未重跑数据库兼容性矩阵。
