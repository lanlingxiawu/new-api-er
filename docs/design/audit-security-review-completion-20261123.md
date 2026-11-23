# 安全告警复核收尾

日期：2026-11-23。依据：用户在复核结果后要求“进行完善，不可见无所谓，但是要能清理”。本轮按该授权补齐既有流程；保留旧规则告警在个人/客户视图中的访问限制，保证管理员清理可用。实现及验证结果在文末更新。

## Goals and Scope

- 个人、客户和管理员详情统一消除历史重复事件，并在去重后应用展示上限。
- 找回密码、登录和无法确定操作类型的账号标签使用准确或中性文案。
- 新登录来源、新管理来源敏感操作保存命中时的实际来源时间证据；历史记录不回填。
- 在既有关联告警页面内按同一事件合并展示规则入口；独立告警的处置、计数与游标保持原契约，明确分组范围为当前页。
- 回归管理员删除、按关闭时间批量清理和后台实际删除，覆盖个人/客户不可见的旧规则告警。
- 修正现有类型检查脚本对已安装 `tsgo` 的调用。

## Existing Logic and Related-Path Findings

- `store.go:appendTriggerEvidence/GetAlert` 已修复新写入及管理员详情去重；`self_access.go:GetSelfSecurityAlert`、`customer_access.go:GetCustomerAlert` 仍直接遍历原始证据。复核已在真实 MySQL/PostgreSQL 复现管理员 1 条、另外两个入口各 2 条。
- `customer_access.go:customerAccountRuleVersion/accountAlertAccessible` 把旧规则转入完整快照验证，超过 100 次或快照缺失时拒绝个人/客户访问。用户接受此边界，本轮不放宽权限。
- `deletion.go:DeleteAlert/CleanupClosedAlerts` 仅使用管理员可见性、版本 CAS、关闭时间及活跃处置约束，不调用个人/客户身份验证。`model/audit_cleanup.go` 分批清除关联证据、finding、通知、调查及指派；原始审计保留。缺失关闭时间的记录仍可逐条显式删除；不推测关闭时间。独立删除循环在监控关闭时也运行。
- `observeManagementSource` 已读取新来源首次时间及成熟来源，但返回的只有布尔值；登录来源成熟查询只有计数。`Facts` 在 IP 补查时从原始事件回执恢复，适合保留第一次判定的时间证据，不能用补查时的现状重新计算。
- `SecurityFindingSnapshot.Dimensions` 已持久化为 JSON 文本，可容纳时间戳而无须 schema 变化。仅由后台生成的来源时间放入内部 Facts，`Normalize` 清除伪造字段，公开事件移除内部字段，界面从 finding 快照读取。
- `auditFieldLabel` 默认把所有 `attempted_account` 当作登录；列表及关联页只有规则名，`ip_abuse` 可来自登录或找回密码。未知上下文必须使用“提交的账号”。
- `SecurityRelations` 使用已有分页关系接口，每条 finding 重复展示事件。按非空 event_id 及相同事件身份/时间/IP/目标分组，空 ID 或上下文不同的行分开，保留每个告警的入口与分页。

## Data Flow and API Contracts

后台审计事件 → 原有来源基线查询 → 保存新来源首次时间、成熟来源首次/最近时间 → 原有 finding Dimensions → 管理员详情/导出 → 来源证据摘要。管理来源证据在回执内保留，IP 补查沿用原值。

沿用 `/api/audit/security/alerts`、`alerts/:id`、`alerts/:id/relations`、个人及员工客户详情；请求及权限保持现状。管理员删除 `DELETE alerts/:id` 仍要求当前版本、会话身份及 `AuditSecurityDelete` 权限；批量清理 `POST alerts/cleanup` 仍接收 `closed_before`，每批最多 100 条，返回 `queued/has_more`。不对生产数据执行删除。

新增 finding dimensions：`source_first_seen`、`baseline_first_seen`、`baseline_last_seen`（Unix 秒）。旧记录缺少这些字段时展示“未记录”，不把窗口参数转换成真实时间。

## Data Model, Index Design and Configuration

没有新增表、列、迁移或配置。沿用来源唯一索引 `idx_sec_baseline(account_id,source_ref)` 和成熟基线索引 `idx_sec_baseline_maturity(account_id,kind,first_seen)`。将现有成熟基线查询改为有界 `LIMIT 1`，选择 `id,first_seen,last_seen`；不增加每事件查询次数。清理使用既有关闭时间、purge 状态及关联表索引。

## Business Logic, Edge Cases and Error Handling

- 去重只认非空 event_id，保留最后一份；空 ID 不视为相同事件，权限过滤继续执行。
- 旧规则不可见不等于不可删除；管理员角色限制、版本冲突、活跃处置保护、原始审计保留必须继续通过测试。
- 新来源证据只采自后台基线查询，覆盖成熟条件边界、无成熟来源、已有来源、延迟补查。旧快照缺字段时不补造时间。
- 当前页的相同事件可包含多个规则，未知 ID、不同账号/目标/时间/IP 不合并。分组不改变后端 finding 数量、告警状态或自动处置。
- 数据库错误沿用事务回滚；前端沿用 `ErrorState`、`handleServerError` 及现有加载/分页行为，不增加吞错路径。

## Main Chain Impact and Shared Resource Audit

仅修改后台安全检测、管理/个人/员工详情投影及前端。AI relay 同步路径新增 SQL、Redis、锁和 goroutine 均为 0。

访问的表为既有 `audit_security_baselines/events/findings/alerts` 及清理关联表，relay 不直接访问这些监控表。沿用监控隔离连接池和固定 worker；不新增共享用户/令牌表查询、缓存、Redis 命名空间或连接池。基线选择使用既有查询和索引，30k/100k RPM 下每次 AI 请求新增资源成本均为 0；这是静态影响分析，不代表负载实测。

## Verification

先写回归用例，再修改实现。使用隔离真实 MySQL/PostgreSQL，覆盖跨入口去重、旧规则清理、来源时间快照、补查保持、原有处置与权限约束；新增查询检查实际 EXPLAIN。前端覆盖标签上下文、事件分组/空 ID/分页、真实及缺失来源时间，并执行类型、lint、作用域格式与七语言验证。

2026-11-23 验收结果：

- 真实隔离 MySQL **26.10.0**、PostgreSQL **18.7**：各 18 项回归通过。覆盖新旧事件去重、权限投影、旧规则不可见但可删除、关闭时间清理边界、发送中的通知/共享自动处置保护、监控关闭后继续删除、来源快照和 IP 补查。命令见下方，结果位于本地 `_local/audit-security-completion-20261123/mysql_after.log`、`postgres_after.log`；测试数据库独立创建并回收。
- `go test ./service/auditsecurity -run '^TestSourceBaselineEvidenceQueryPlans$' -count=1 -timeout=5m -v` 在上述两种数据库各通过。向基线表写入 10,050 条隔离样本，捕获 `Apply` 的真实登录/管理来源查询；MySQL 分别选择 `idx_sec_baseline`、`idx_sec_baseline_maturity`，PostgreSQL 同样使用这两个索引，无全表顺序扫描。原有清理查询 EXPLAIN 也通过。
- SQLite **3.50.4**：`go test ./service/auditsecurity -run '^Test(HistoricalEvidenceDeduplicatesBeforeAccountViewLimits|LegacyAccountAlertsRemainDeletableWithoutAccountVisibility|SourceFindingsRetainObservedBaselineTimes|NewSourceSingleKeyCreationAndIPRetryKeepOriginalIdentity)$' -count=1 -timeout=2m`，4 项通过。使用临时文件数据库。
- `bun run test src/features/usage-logs/audit src/features/customer-console/customer-audit-dialog.test.tsx`：23 个文件的 307 项通过；另一个文件的一处旧标签断言与新的中性标签不符，改成登录/找回密码/IP 风险三种上下文后，`bun run test src/features/usage-logs/audit/security/monitor.test.tsx` 的 41 项全部通过。合计 24 个文件、348 项通过。
- `bun run typecheck`、`bun run lint`、本轮 9 个前端文件的 `oxfmt --check`、`git diff --check` 通过。Lint 仍报告既有其他模块的 7 条 warning，无 error。
- 经临时翻译脚本和 `bun run i18n:sync` 补齐 6 条文案的全部 7 种语言；校验 JSON 有效、无 BOM、非英语内容均已翻译。临时脚本已删除，恢复同步工具产生的无关翻译报告变化。
- 实际组件的隔离 Rsbuild/Playwright 检查：1440/390 像素、深浅色、完整/缺失来源证据共 8 组通过；相同事件的两条规则合并，空 event_id 分开，弹窗/内容/卡片无水平溢出，无浏览器运行错误。截图与结果保留于本地 `web/_local/security-completion-review/`，预览服务已停止。

本轮没有 schema 或迁移修改，因此没有增加迁移或生产 schema 演练；未执行全项目测试、生产构建或真实流量压测。无部署、无生产清理。

后端回归命令（由本地 `run_tests.py` 为两种数据库分别注入隔离测试 DSN；正则中的 `CustomerSecurityProjectionAndActions` 未匹配测试，实际执行 18 项）：

```powershell
go test ./service/auditsecurity -run '^Test(HistoricalEvidenceDeduplicatesBeforeAccountViewLimits|LegacyAccountAlertsRemainDeletableWithoutAccountVisibility|SourceFindingsRetainObservedBaselineTimes|NewSourceSingleKeyCreationAndIPRetryKeepOriginalIdentity|PublicSecurityFactsExcludeExecutionProvenance|NormalizationNeverCopiesCredentials|DuplicateTriggerEvidenceCollapses|AlertDeletionVisibilityVersionAndEvidence|AlertCleanupBoundsAndClosure|PendingDeletionWaitsForSendingAndRecoversStale|DisabledMonitoringStillDrainsDeletion|DeletingOrReviewingAnAlertCannotStopSharedAutomaticWork|DeletionQueryPlans|NewManagementSourceRequiresActualDistinctChannelChanges|SelfSecurityIsolationAndProjection|SelfSecurityIdentityAndLegacyEvidence|CustomerSecurityProjectionAndActions|ChannelRoutineMaintenanceAlertPolicy|PermissionDirectionGradesPromotionAboveRevocation)$' -count=1 -timeout=5m -v
```
