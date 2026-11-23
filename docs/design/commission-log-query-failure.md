# 提成记录空列表：查询故障排查与修复方案

日期：2026-11-24。状态：计数与明细解耦、游标分页、错误展示及 MySQL 服务端时限已实现，最终数据库功能回归通过；真实一亿行验证已完成，复杂筛选及部分读写并发仍超时，性能验收未全部通过。尚未部署。

## Goals and Scope

用户反馈生产环境“月度统计/员工页有金额，提成记录为空”。目标是让保留的明细可以独立于全量计数被读取，并将查询失败与真正没有匹配记录明确区分。管理员提成记录与员工自己的提成记录共用查询逻辑，需一起处理。

本次不重算提成、不恢复已清理的明细、不更改客户归属、计费、异步账务写入或月度汇总。

## Existing Logic and Related-Path Findings

以下是修复前的代码追踪结果；旧页码模式保留兼容，新网页使用后文的游标模式。

1. `web/src/features/employees/index.tsx:CommissionLogsTab` 初始 `filters={}`，时间选择器为空。`web/src/features/employees/api.ts:getCommissionLogs` 仅提交 `page=1&page_size=10`，不隐含日期。重置也恢复无时间范围。
2. `controller/employee.go:AdminListCommissionLogs` 将条件交给 `model.GetCommissionLogs`；该函数先执行 `countCommissionLogs`，计数成功后才执行明细 `Find`。计数错误直接返回 `nil,0,error`，没有尝试读取第一页。
3. `model/employee.go:countCommissionLogs` 创建三秒 context，先查 `FinancialCleanupVersion`。存在 `kind=commission AND before_time>start AND task_id IS NOT NULL` 的清理任务即切换到明细 `COUNT(*)`；不要求任务实际删过记录或成功完成。默认无时间和维度筛选时，实际 SQL 是 `SELECT count(*) FROM employee_commission_logs`。清理版本读取与计数共用三秒预算。
4. 未触发清理分支、没有客户/渠道/模型/盈亏条件时，计数可读取日汇总；有这些条件仍走明细计数。故障不限于清理后场景，但清理记录会使默认打开页面也依赖全表计数。
5. `AdminListCommissionLogs` 查询失败返回 HTTP 200 和 `{success:false,message:err.Error()}`。前端 Query 未调用已经引入的 `requireServerSuccess`，React Query 将它视为成功；表格取 `data?.data?.items ?? []`、总数取 `data?.data?.total ?? 0`，最终显示“暂无记录、总计 0”。网络层失败时也没有独立的表格错误状态。
6. `controller/employee.go:GetMyCommissionLogs`、`web/src/features/employee-console/index.tsx` 和 `api.ts` 存在相同的计数依赖及业务失败处理问题。员工身份与 `EmployeeUserId` 由服务端限制，修改查询方式必须保留该约束。
7. `model/employee.go:GetCommissionCalendarStats` 读取 `employee_commission_reset_period_daily_stats`；员工累计数据读取日汇总/扩展数据。它们与明细不是同一条查询，因此明细查询失败不妨碍统计显示金额。
8. 写入链为 `service/consume_settlement.go` 入队快照 → 日志流水线完成日志写入后的账务处理 → `service/employee_commission.go:RecordCostAndSettleEmployeeCommission` → `model/consumption_cost.go:CreateConsumptionCostAndCommissionLog` → `model/business_stats_buffer.go:flushCostAndCommissionLedger`。提成汇总在明细事务成功后进入聚合缓冲；本轮没有发现需要改变这条写入链的证据。
9. `model/data_cleanup.go` 清理会物理删除过期明细、保留汇总及去重凭据。因此历史统计有金额也可能与合法清理后的空明细同时存在，不能从截图反推数据丢失或自动回填。
10. 可复用 `model/consumption_cost_ledger.go` 已有的 `(created_at,id)` 游标查询模式，以及 `web/src/components/data-table/layout/data-table-page.tsx` 的 `cursorPagination`。后者已支持无总数的当前页/上一页/下一页、页大小与已访问页导航。
11. 管理员渠道名解析目前调用 `ResolveChannelDisplayNames`，该函数仍有使用日志回退。新查询分支改用现成的 `ResolveChannelDisplayNamesWithoutLogs`，按本页渠道读取当前名称与日汇总快照；解析不到时沿用界面 ID 展示。

已验证的是上述代码故障路径；本轮未连接生产数据库，不能据此断言生产本次确实遇到计数超时、具体清理状态或现存明细条数。用户要求直接查逻辑，后续验证以代码和本地测试为主。

## Local Reproduction

复现材料位于本机 `_local/commission-query-20261124/`，不写入生产数据。Go 使用 overlay 加入诊断测试，不修改现有源码；关系数据库使用独立临时数据库，测试结束已删除。

| 环境 | 结果 |
| --- | --- |
| SQLite 3.50.4 | 有明细时正常返回；增加零删除量的提成清理任务凭据后，捕获到无 WHERE 的全表计数 SQL |
| MySQL 协议实例 26.10.0 | 同上；仅对私有测试表施加锁以制造计数等待，三秒后返回 `context deadline exceeded`，结果为空且第一页 SELECT 未执行；解锁后相同请求立即读到原记录 |
| PostgreSQL 18.7 | 同样复现；错误为 `timeout: context deadline exceeded`；解除私有表锁后读取恢复 |
| Vitest / 实际 Employees 组件 | 两个用例通过：后端 `success:false` 与真正的成功空结果都呈现 `No records`；失败响应在 React Query 内仍为 success |

命令：

```powershell
python _local/commission-query-20261124/run_diagnostic.py
cd web
bun run test --config ../_local/commission-query-20261124/vitest.config.ts
```

这里使用表锁只是确定性地制造计数超时，证明失败传播顺序，不代表生产存在表锁，也不是生产数据规模的压测。

## Implemented Data Flow and API Contracts

在现有两个 GET 接口增加显式 `pagination=cursor` 模式，现有页码模式继续兼容既有调用方。网页切换到新模式，不再请求全量总数。

- 管理员：`GET /api/admin/employee/commission`，继续由 `AdminAuth` 和 `AdminMenuEmployeesView` 授权。
- 员工：`GET /api/user/employee/commission`，继续由 `UserAuth` 和启用员工检查授权；强制限定当前员工，不能用参数指定其他员工。
- 保留现有员工/客户/模型/渠道/盈亏筛选；`page_size` 为 1–100。新增成对参数 `cursor_created_at`、`cursor_id`，首屏不传；校验正数、成对出现、在请求时间范围内。
- 新模式默认最近 24 小时，界面展示实际时间范围；显式范围最多 31 天。边界仍使用现有闭区间 `created_at >= start AND created_at <= end`。同一次翻页固定时间窗口，修改筛选/页大小或重置时清除游标历史。
- 查询只选显示和游标所需字段，按 `created_at DESC,id DESC`，附加严格小于上页末尾的游标条件，取 `page_size+1` 条判断是否有下一页。翻页时将实际 SQL 时间上界收紧到游标的 `created_at`，避免 PostgreSQL 先扫描窗口内所有较新的记录再应用 OR 条件。
- 成功返回 `{success:true,data:{items,has_more,next_cursor}}`；`next_cursor` 为 `{created_at,id}` 或 null。不捏造总数，不用未删除的历史汇总推算剩余明细数量。
- 管理员仅对已取得的这一页解析渠道名称；员工继续不暴露内部渠道名称。
- 业务失败沿用 HTTP 200 / `success:false`，控制器记录底层错误并返回安全的 i18n 提示；不返回 SQL 或原始 Go 错误。明细查询使用请求 context 和 10 秒超时；MySQL 额外附加 `MAX_EXECUTION_TIME`，取不超过 10 秒的剩余请求预算，防止客户端断开后语句继续扫描。MySQL 错误 3024 保留底层错误并归类为超时。主动取消请求时客户端及时返回，服务端仍以该语句已设置的预算为上限，不能承诺立即停止。旧页码模式的查询错误也改为安全提示。

## Frontend Behavior and Error Handling

管理员和员工列表通过共享 `useCommissionLogPage` 调用 `requireServerSuccess`，复用项目最终失败提示。查询关闭自动重试，避免一次超时触发多轮慢查询；支持 AbortSignal 和用户主动重试。表格的错误标题、说明和重试按钮通过已有 `DataTablePage` 插槽提供；只有成功且 `items=[]` 才显示无记录。失败状态不展示虚假的总计 0，重试成功后恢复正常列表。

复用共享 `cursorPagination`：保留每页行数、上一页和下一页；显示本页记录数，移除无法兑现的精确总数与末页跳转。搜索条件仍可修改，重复搜索相同条件可以重新请求；翻页失败不能丢失上一个有效游标。按项目 i18n 技能维护涉及的七种前端语言与后端语言。

网页默认每页 10 条，接口省略参数时为 20 条；保留原有页大小选项。每页返回量与用于性能验证的表基数分别记录。

具体改动入口：`model/employee_commission_query.go`（复用 `employee.go` 的筛选条件）、`controller/employee{,_commission_query}.go`、`web/src/features/employees/{api.ts,types.ts,index.tsx,hooks/use-commission-log-page.ts,lib/commission-log-query.ts}`、`web/src/features/employee-console/{api.ts,index.tsx}` 及其回归测试。不修改受保护的认证、HTTP 客户端或 `server-error-message.ts`。

## Data Model, Indexes and Configuration

不新增业务表、字段、数据迁移、后台任务或可配置参数。默认 24 小时和最长 31 天采用项目已有审计明细查询的范围约定，仅作用于新模式。

优先复用现存 `idx_commission_cleanup_created_id(created_at,id)`、`idx_employee_commission_created_employee(created_at,employee_user_id)`、`idx_employee_commission_created_customer(created_at,employee_user_id,customer_user_id)`、`idx_employee_commission_created_channel(created_at,channel_id)` 及员工/客户单列索引。模型模糊搜索和盈亏筛选仍可能扫描较多候选，必须有时间范围与查询超时，不能承诺任意复杂过滤都恒定耗时。

已在真实 MySQL/PostgreSQL 的独立 20 万行合成表上检查无筛选、员工/客户/渠道/模型/盈亏筛选及深游标翻页的 EXPLAIN。所有查询使用已有索引，常规列表按时间范围扫描；员工/客户可能选择单列索引后排序。没有增加索引或改变生产表结构。若后续生产基数需要额外索引，仍应单独设计和演练迁移。

1 亿行补测发现 MySQL 为带 LIMIT 的渠道查询选择时间单列索引；不存在的渠道在 700 万行的一天窗口中耗时约 9.7 秒。对照实验使用现有 `(created_at,channel_id)` 索引后降至约 0.6–1 秒。因此新模式在 MySQL 的渠道筛选、且未指定员工/客户时明确使用 `idx_employee_commission_created_channel`，让渠道条件在索引内过滤。指定员工或客户时仍由优化器选择，避免覆盖稀疏员工/客户的高选择性单列索引；PostgreSQL 和 SQLite 保持原有规划。没有新增索引或迁移。

## Main Chain Impact and Shared Resource Audit

AI relay goroutine 没有新增工作，账务入队、结算和汇总流程保持原逻辑。本变更发生在管理/员工查询路径，不增加每次 AI 请求的数据库、Redis、锁或 goroutine 成本。

共享资源为主库连接池、`employee_commission_logs`（后台账务写入及清理使用）、`employee_profiles`（员工身份）、`channels` 和 `platform_channel_daily_stats`（渠道名），以及现有渠道缓存。列表只读，不加行锁、不创建新池或跨请求缓存；去掉网页全表计数减少共享数据库的读负担。MySQL 使用语句级执行时限限制超时后残留扫描，不修改连接池全局配置，也不增加取消用的额外连接。新分支不读取日志库大表、不写汇总/钱包/余额，也不修改任何 relay Redis 命名空间。

## Acceptance and Lifecycle

实施前先写回归：明细存在但计数不可用仍可通过新模式读取；清理后只返回保留明细；同秒多行翻页不重不漏；空页/末页/页大小/筛选重置；员工越权参数；超时/取消/数据库失败；HTTP 200 业务失败显示错误和重试；真正空结果保留空态。验证旧页码接口合同和月度汇总不受影响。

后端在真实 MySQL、PostgreSQL 及 SQLite 回归，记录版本、命令、查询计划和结果；前端执行针对性 Vitest、typecheck、lint。本轮按用户要求不以打开浏览器代替代码检查。完成后更新本文实际实现和验证结果，再按单独的发布授权处理部署。

### 2026-11-24 Verification Results

| 环境 | 验证及结果 |
| --- | --- |
| SQLite 3.50.4 | 新明细查询与参数校验通过；清理凭据表不可用、旧计数失败时，新模式仍读取已有明细；包含同秒边界、删除游标行、筛选、请求取消和过期 context |
| MySQL 5.7.44 | model/controller 回归通过，包括旧页码接口、提成汇总与日历查询；20 万行 EXPLAIN 使用已有索引 |
| MySQL 26.10.0 | 相同回归与查询计划检查通过；测试实例 `SELECT VERSION()` 返回此版本 |
| PostgreSQL 18.7 | 相同回归与查询计划检查通过；深游标的 Index Cond 时间上界已收紧到游标时间 |
| Vitest 5.0.4 | 实际管理员与员工页面共 10 项回归通过：业务/网络错误、重试恢复、时间与游标参数、末页、筛选/页大小重置、重复搜索、真空结果、失败翻页后重试与返回上一页 |
| 前端静态检查 | `bun run typecheck`、`bun run lint` 通过；lint 仅剩其他文件的既有 warning |
| 国际化 | `go test ./i18n -count=1` 通过；两条新增前端文案在七种语言齐全，`bun run i18n:sync` 完成 |

本地验收命令（脚本只在本机现有容器内新建独立临时数据库，运行后删除，凭据不会输出）：

```powershell
python _local/commission-query-20261124/run_acceptance.py
go test ./i18n -count=1
cd web
bun run test src/features/employees/commission-logs.test.tsx
bun run typecheck
bun run lint
```

脚本的 model 选择器为 `^Test(CommissionLogPage|GetCommission(Logs|Totals|Calendar|Reset|Stats)|CreateCommissionLog)`；controller 选择器为 `^Test(CommissionLogCursor|AdminListCommissionLogs|GetMyCommissionLogs|AdminCommissionSummary|GetMyCommissionSummary)`。计划测试通过 Go overlay 调用真实 `ListCommissionLogs`，断言每页只执行一个 SELECT，且无 COUNT/OFFSET。SQLite 仅运行新 model 查询测试，controller 的数据库验收在 MySQL/PostgreSQL 完成。

20 万行样本包含约 28 天数据、200 名员工、2000 名客户和同秒多行；普通条件查询最近 24 小时，深游标定位到接近表头的历史记录。最终单次取页观测范围：MySQL 5.7 约 2.6–12.7ms、MySQL 26.10 约 1.6–269ms、PostgreSQL 约 1–4ms。PostgreSQL 深翻页在收紧上界前约 29.7ms，收紧后约 2.1ms；EXPLAIN 确认索引上界改变。这些是本地合成数据的单次观测，不代表生产时延承诺。明细稀疏、模糊搜索等仍受 31 天窗口和 10 秒查询超时限制。

本次没有模型、索引或迁移改动，因此未执行生产 schema 导出/升级演练或重复启动无 DDL 检查。没有访问生产数据、运行真实上游 AI 请求或发布版本。发布需同时包含后端新模式与前端调用；旧网页可继续使用旧页码接口。生产本次是否还存在合法明细清理或其他存储故障，仍需上线后的实际结果确认。

### Large-Scale Validation — Method and Limits

生产量级背景来自本地 `docs/production-environment.md` 中用户于 2026-11-22 提供的记录：每天约 700 万条使用日志、超过 60% 为错误、保留约 70 天。使用日志在独立 PostgreSQL 日志库，提成明细在 MySQL 主库；只有满足员工归属及结算条件的消费才生成提成。不能将所有使用日志或错误日志的数量直接当作提成表的实际行数。目前没有生产提成表基数、实际数据分布、数据库版本和硬件的现场测量。

20 万行旧样本的最近一天仅约 7200 行，既缺少存量基数，也缺少生产量级的时间窗口密度。旧结果仅用来确认基本逻辑和查询计划，不作为生产性能验收。

补充测试材料：`_local/commission-query-20261124/scale/`。使用独立、带专属标识的本地 MySQL 26.10.0 容器，3 CPU、3 GiB 内存上限、768 MiB InnoDB buffer pool。数据库表由当前 `EmployeeCommissionLog` 的 GORM 定义创建，保留完整 17 个字段、主键和九个现有二级索引。装载期间延后构建二级索引并关闭 redo 仅用于私有 fixture 准备；计时查询前恢复所有索引与 redo，再核对表结构、精确行数和最近一天行数。装载速度不作为生产写入能力。

已于 19:27 精确核对 100,000,000 行、70 天历史，其中最近一天 7,000,000 行，其他 9300 万行分布于此前 69 天。数据占 17,943,232,512 字节、二级索引占 23,415,619,584 字节，总计约 38.52 GiB，显著超过 768 MiB buffer pool。该密度是压力假设，不声称等于生产提成量。包含占比约 80% 的热点员工、稀疏员工、只存在于历史范围的员工、热点/稀疏客户、模型与渠道筛选、同秒多条、无匹配及组合条件。按现有 API 允许的 24 小时/31 天窗口，直接调用实际 `ListCommissionLogs`，每种条件重复三次，并记录 SQL、EXPLAIN、返回条数、超时和耗时。覆盖深游标与连续 100 页无重复/遗漏；额外检查重启后空数据库缓冲池及四个并发读请求伴随每秒 100 行的批量写入。操作系统缓存及其他本地容器的 I/O 竞争不可排除。

另调用实际旧 `GetCommissionLogs`：创建零删除量的清理凭据，分别测试原默认无时间范围和相同 24 小时范围，记录计数失败是否阻止页查询。这个对照不施加表锁。

装载过程发生了两次索引准备失败：第一次 DDL 会话未继承调高的全局 buffer，主动中止私有表的索引构建并确认回滚；第二次同时构建九个索引超过 3 GiB 限额，容器被 OOM 终止。redo 在此之前已经恢复，重启后数据完整；随后仅在准备期间提高到 6 GiB、启用容器内存感知、改为每批三个索引，完整构建并核对九个索引。正式计时前已重启并恢复 3 CPU / 3 GiB / 768 MiB，redo、doublewrite 均开启，`innodb_flush_log_at_trx_commit=1`、`O_DIRECT`；binlog 关闭。准备阶段数据及部分索引预检不计入最终性能结果。

### One-Hundred-Million-Row Results

旧路径未加锁对照：管理员默认无时间范围时，实际 `GetCommissionLogs` 在 3000.442ms 因计数超时失败；热点员工无时间范围为 3000.545ms。两者均未执行明细 SELECT。管理员旧 COUNT 返回后数据库查询仍在运行，观察一秒后由测试工具终止该私有连接以隔离后续测量；这不是生产代码中的自动终止逻辑。增加 24 小时时间范围后，旧管理员计数加取页为 820.564ms、热点员工为 1158.455ms。

服务端时限修正前的新路径首次冷数据库缓冲池读取：最近一天首屏 47.245ms，31 天窗口深游标（ID 54000000）30.163ms。这里冷指 MySQL 重启后未加载 buffer pool，不保证宿主操作系统/磁盘缓存为空。最终代码额外含语句级时限提示，最终复测值见下表，不用这一轮的冷读结果替代最终代码测量。

渠道修正前的 22 类条件、每类三次实测中，11 次复杂筛选超时：稀疏模型三次、24 小时不存在模型两次、31 天不存在模型三次、热点员工加无匹配渠道三次。模型条件缩小至一小时时，稀疏匹配/无匹配均成功，约 0.7–1.0 秒。渠道不存在条件原为 9.685–9.880 秒，索引对照揭示了上文的优化机会。原始记录保留为 `bench-before-channel-index.jsonl`，未用修正后的测量覆盖失败证据。

纠正初版取消检测：最初仅统计 `PROCESSLIST_COMMAND='Query'`，漏掉 Go 驱动预处理语句的 `Execute`，当时“服务端已停止”的结论无效。并发检查发现四条超时扫描叠加；其中两条在客户端返回后仍继续到约 179 秒，被私有测试工具终止。故增加 MySQL 服务端时限，所有后续检测同时统计 Query/Execute；新增真实 MySQL 回归用私有表慢查询验证语句停止，不只检查 Go context 返回。修正前的读写结果可能受残留查询影响，完整保留但不能用于给最终代码下性能结论。

最终代码（渠道索引修正及 MySQL 服务端时限）直接调用实际 `ListCommissionLogs` 的结果如下。每类三次，表内为最小/中位/最大毫秒；53/66 次成功，13 次超时，失败证据完整保留。复测时其他本地容器仍有 I/O 负载，兼容性测试也在另一实例准备 schema；这组时延不是隔离环境的生产 SLA。中间版本 54/66 次成功的记录保留为 `bench-before-server-timeout.jsonl`；两轮不能视为严格同负载下的性能比较。

| 条件 | 成功次数 | 最小 / 中位 / 最大 ms |
| --- | --- | --- |
| 最近 24 小时，10 行 | 3/3 | 2.094 / 2.610 / 3.282 |
| 最近 24 小时，100 行 | 3/3 | 1.550 / 2.096 / 2.614 |
| 最近 31 天 | 3/3 | 1.564 / 2.091 / 8.697 |
| 热点员工 | 3/3 | 2.080 / 2.089 / 2.093 |
| 稀疏员工 | 3/3 | 2.074 / 2.674 / 378.753 |
| 不存在员工 | 3/3 | 1.034 / 1.563 / 1.681 |
| 仅在历史窗口存在的员工 | 3/3 | 7.990 / 8.620 / 26.294 |
| 热点客户 | 3/3 | 1.053 / 1.571 / 2.096 |
| 稀疏客户 | 3/3 | 4.665 / 4.686 / 274.489 |
| 稀疏渠道 | 3/3 | 1.043 / 2.082 / 2.717 |
| 不存在渠道 | 3/3 | 644.230 / 678.077 / 706.621 |
| 常见模型模糊匹配 | 3/3 | 1.556 / 1.563 / 1.643 |
| 稀疏模型，24 小时 | 0/3 | 三次约 10000，超时 |
| 不存在模型，24 小时 | 0/3 | 三次约 10000，超时 |
| 不存在模型，31 天 | 0/3 | 三次约 10000，超时 |
| 稀疏模型，1 小时 | 2/3 | 354.048 / 371.276 / 10000.696（一次超时） |
| 不存在模型，1 小时 | 3/3 | 345.309 / 347.944 / 366.971 |
| 亏损记录 | 3/3 | 2.085 / 2.094 / 2.133 |
| 热点员工加无匹配渠道 | 0/3 | 三次约 10000，超时 |
| 31 天窗口深游标 | 3/3 | 1.565 / 1.575 / 21.508 |
| 31 天窗口员工深游标 | 3/3 | 1.568 / 1.572 / 11.034 |
| 同秒游标边界 | 3/3 | 1.572 / 1.615 / 2.633 |

所有成功结果检查筛选条件、时间/ID 排序及游标边界；所有查询断言仅一条 SELECT，无 COUNT/OFFSET。正式九索引/3 GiB 环境连续翻页 100 页、10,000 行，逐 ID 核对没有重复或缺失，合计 384.261ms。修正服务端时限后，父请求 100ms 截止的实际模型扫描于 100.400ms 返回取消；首次观察 Execute 仍在执行，一秒后已停止，无需工具终止。最终 13 次慢查询超时均在返回后 0–2 秒的观察中停止，工具终止次数为零；执行时限是服务端协作式中断，不保证中断正在等待的 I/O 的精确墙钟时间。

并发只读对照（加服务端时限前）：四个 worker，各次查询之间间隔 100ms，运行 20 秒，混合普通列表、UID、渠道、模型常见值、亏损及深游标，770 次，零错误，p50 2.520ms、p95 5.883ms、最大 65.398ms。

初始读写脚本使用 `INSERT ... SELECT` 从同一张表复制 100 行：两轮分别为 32 次读中 4 次超时、16 次读中 8 次超时，单批写入约 20/31 秒。仅执行同形式写入时也有 1.18–8.87 秒的批次。对照实际 `model/business_stats_buffer.go:flushCostAndCommissionLedger` 后，确认生产使用 `CreateInBatches` 生成批量 VALUES；正式并发读写改用预先读入内存的相同 100 行模板执行 `INSERT ... VALUES`，保留原失败记录，不以复制表的写入表现推断生产账务性能。当前测试仍未覆盖成本表/日汇总同事务、去重、后台队列等完整结算过程。

最终读写测试在确认私有实例没有残留查询后顺序执行，写入为 100 行一批、完成后间隔一秒，新数据时间在固定查询窗口之外；它不是保证每秒提交 100 行的恒定速率发生器：

| 最终场景 | 读取结果 | 写入结果 |
| --- | --- | --- |
| 仅批量写入对照 | 无读请求 | 20.02 秒提交 1400 行；单批 10.45–4097.42ms |
| 四路普通读加写入 | 416 次，4 次不存在员工查询超时；p50 2.802ms，p95 3.616ms，最大 10000.524ms | 52.45 秒提交 300 行；单批最大 41744.82ms；写入无错误 |
| 两路普通读、两路 31 天无匹配模型扫描加写入 | 普通读 364 次零错误，p95 3.411ms，最大 1151.415ms；重扫描 4/4 次超时 | 20.27 秒提交 1000 行；单批最大 5687.58ms；写入无错误 |

结束后两次检查 Query/Execute 均无残留。普通读写仍有超时及长写入延迟，因此该项不判为通过；仅写入对照本身也有明显延迟波动，不能从本地共享存储下的单次测试确定全部因果，更不能据此宣称生产结算吞吐已验收。主 relay 和账务写入代码未修改。

大表验证命令：

```powershell
go build -o _local/commission-query-20261124/scale/runner.exe ./_local/commission-query-20261124/scale
python _local/commission-query-20261124/scale/lab.py bench
python _local/commission-query-20261124/scale/lab.py pages
python _local/commission-query-20261124/scale/lab.py cancel-scan
python _local/commission-query-20261124/scale/lab.py writer-baseline
python _local/commission-query-20261124/scale/lab.py load
python _local/commission-query-20261124/scale/lab.py mixed-load
python _local/commission-query-20261124/scale/summarize.py
```

完整 SQL、EXPLAIN、阶段失败、最终源码 SHA256 和汇总保留在该本地目录。最终时间窗口仍是原 700 万条，额外并发写入 5300 行在窗口之外。20:18 已校验专属标签后移除私有大表容器及其卷，删除临时凭据文件；其他容器与生产环境未操作。重新运行大表命令需先用同目录脚本重新准备独立 fixture。

最终兼容性补跑命令为 `python _local/commission-query-20261124/run_acceptance.py --functional-only`。早期一轮 MySQL 5.7 因共享本地环境下建表异常缓慢，在 TestMain 准备阶段被 Go 的六分钟外层时限终止，未进入用例断言；日志保留为 `acceptance-mysql57-model-setup-timeout.log`。脚本测试预算改为十分钟后重跑，不移除断言。新增服务端时限回归使用真实 MySQL 私有表、慢查询和 Query/Execute 进程检查；MySQL 5.7.44、26.10.0 均已通过，其他方言跳过该专属用例并保留通用查询回归。

最终脚本退出码为 0：MySQL 5.7.44 和 26.10.0 分别通过 13 个 model、8 个 controller 顶层用例；PostgreSQL 18.7 通过 12 个 model、8 个 controller 用例，跳过一个 MySQL 专属用例；SQLite 通过两个 model 用例并跳过 MySQL 专属用例。包括新增的渠道同秒游标和服务端超时停止回归。临时兼容性数据库已由脚本清理。前端和 i18n 自上一轮通过后未再修改。`git diff --check` 及相关前端 UTF-8 无 BOM 检查通过。

任何稀疏或无匹配条件出现超时，均记为该规模下未通过，不以错误提示已修复代替查询性能通过。本次无生产 schema 副本，无主库完整业务负载，不将单表合成测试称作生产全栈验收。
