# 日志导出分组倍率覆盖

日期：2026-11-07

状态：用户已确认方案，功能已实现，验证结果见文末。

## 目标与范围

仅管理员（含超级管理员）可在创建日志导出任务时覆盖有效分组倍率。复用现有批量导出，不增加脚本、进程、数据库关联、查询轮次或并行计算。

开启后直接替换表格原有分组倍率、额度和费用字段，不增加原值、新值、差额或状态列。模型、输出、缓存倍率、原计费表达式保持不变。数据库日志、用户余额、订阅和佣金不变。

旧倍率为零或缺失时，该记录不替换，保留原倍率及费用继续导出；汇总照常计入该记录。原始 `other_raw` 列在覆盖模式下不输出。

## 实际代码与数据流

`logs` 没有独立的 `group_ratio` 列，历史倍率位于 `logs.other.group_ratio`。独立导出列由 `model/log_export_columns.go` 提取。成本台账虽有同名列，本功能不查询或关联台账。

现有 `logExportScanner.runCustomer` 已批量读取，默认每批 3000 行；明细、汇总和 both 共用扫描器。both 已经只扫描一次。writer 已有 CSV、gzip 和 4 MiB 写缓冲。

接入顺序：

```
现有查询前资源闸门
  → 读取当前批次
  → 原始行级筛选
  → prepareReprice 计算当前行一次
  → Render 输出覆盖值
  → 原 writer 写文件
  → AddQuota 累加同一额度
  → 原有批次限流
```

纯汇总在自身已有批回调的 AddQuota 前调用同一计算器，不新增批循环或结果数组。

计算器位于 `model/log_export_reprice.go`；任务开始时初始化，精确解析新倍率一次。行上下文保存当前计算结果及日志指针，避免跨行串值，不修改 Log 对象、Other 字符串或原始解析 map。旧倍率只缓存最近一次解析结果，内存有界。

## 计算规则与边界

对消费日志，旧倍率 G 大于零时：

```
新额度 = Round(原额度 × 新倍率 ÷ 旧倍率)
美元金额 = 新额度 ÷ 任务固定的 QuotaPerUnit
```

例如原额度 1000、旧倍率 2、新倍率 1.5，导出额度为 750。按历史已结算额度换算，无法消除历史逐笔舍入或最低收费造成的误差，不宣称等价于原始用量完整重放。

- G 为零、缺失、null，或 Other 为空：不替换该行，即使新倍率为零也保留原值。
- 非消费日志：不替换。
- G 合法且新旧倍率相等：复用原额度。
- G 合法且新倍率或原额度为零：新额度为零。
- 负旧倍率、非法倍率类型、损坏 JSON、异常负费用或换算溢出：任务失败，使用可理解的错误提示；旧倍率零/缺失不属于此类错误。
- 新倍率输入为十进制字符串，范围 0～10000，最多 6 位小数；显式零与未开启分开。
- 使用 decimal 乘法和 DivRound 在整数额度处舍入一次，再经 common.QuotaFromDecimalChecked 检查，不缓存截断的除法系数，不使用浮点直接强转额度。
- 汇总逐笔计算后再求和，检测整数累计溢出，禁止先汇总原费用再换算。

只替换导出字段，不执行上游请求、计费表达式、扣费、退款或实时配置读取。

## API 合同与权限

沿用管理员 `POST /api/log/export/jobs`，在原有请求体增加可选字段：

```json
{
  "group_ratio_override": "1.5"
}
```

省略或 null 表示不开启，字符串 "0" 表示覆盖为零。成功响应维持 `{success, message, data: {job_id}}`。

请求沿用 AdminAuth 路由；创建逻辑再次检查实际角色。自助和员工入口保持现有字段白名单，拒绝覆盖参数。worker 也校验 CreatorRole、SelfExport 和 EmployeeScope，避免特权参数进入受限任务。共享模板不保存覆盖倍率。

非法输入使用 ApiErrorI18n 和 HTTP 200 业务失败响应；角色不足使用原有 403 格式。后台计算失败映射为 `reprice_failed`，前端提示关闭覆盖或调整筛选重试，不输出原始 Go 错误。

## 导出字段和交互

管理员新建导出表单增加默认关闭的开关及倍率输入框，校验失败时禁用提交并显示原因。修改倍率不会触发行数估算请求。任务列表显示使用的覆盖倍率，执行状态、取消和下载保持原有交互。

开启覆盖仅隐藏 `other_raw`，操作可见列及保存模板时保留该列原有选择和位置，关闭覆盖后恢复显示；隐藏列仍计入模板列数上限。创建请求继续剔除该列。纯汇总导出不要求选择明细列，明细及混合导出仍至少需要一个有效明细列。

- `group_ratio`：成功换算的行输出新倍率。
- `user_group_ratio`：原来存在有效专属倍率时同步替换；未配置时继续为空，不重复乘算。
- `quota`、`cost_usd`：输出本次额度和费用。
- 旧倍率为零/缺失的行：原有字段取值保留。
- `other_raw`：前端选择器隐藏，创建时及 worker 中均排除；不输出原始 Other。
- 原有日期、用户、模型和其他列顺序保留，不添加对照列。

费用筛选和异常筛选仍使用原始日志，之后才计算覆盖值。明细与汇总使用同一最终额度，汇总美元格式化只在输出分组时进行。新任务继续生成 CSV.GZ，解压后可用 Excel/WPS 打开，不恢复 XLSX 生成。

## 数据模型与配置

不新增 SQL 表、列、索引或迁移。Redis 任务增加可选 GroupRatioOverride 和 RepriceQuotaPerUnit，客户端不能指定兑换率。创建时固定兑换率，文件生成后重复下载不查询日志、不重新计算。

旧任务无覆盖字段时沿用原行为。运行中取消、失败、节点重启、分片清理、下载权限与 TTL 继续使用既有机制。没有新增设置项；沿用 log_export_setting 的并发、批大小、CPU 水位、按行速率、超时、压缩、磁盘和低峰配置。

后端使用实际存在的 en/zh-CN/zh-TW YAML 文案，前端补齐七语言。未修改上游拥有的认证和 API 基础模块。

## Main Chain Impact

relay goroutine 不增加任何执行代码、DB/Redis 操作、锁或 goroutine。计算只发生在受名额约束的导出任务中。共享节点仍存在 CPU、GC 和 I/O 竞争，因此依赖现有限流，不把函数计算快等同于主链负载下零影响。

覆盖关闭时不增加 SELECT 字段或 JSON 计算。开启时为明细、纯汇总和 both 补齐 type、quota、other，避免列未选中导致假零值。纯汇总原本不读取 Other，这部分传输和扫描成本会增加。

## Shared Resource Audit

| 资源 | 约束 |
|---|---|
| LOG_DB.logs | 与异步日志写入同表，只读原时间窗口和游标查询，不加行锁或长事务 |
| DB/Redis 连接池 | 不增加逐行查询、连接或任务进度写入频率；复用已有任务控制操作 |
| Redis logexport:* | 只扩展原任务元数据，不使用 relay 命名空间 |
| 内存与 GC | 复用 rowCtx 和 rowBuf；定向读取旧倍率和专属价标识，不构建额外 map 或整批结果 |
| 导出 worker | 默认名额 1，不提高并发，不创建每行 goroutine |
| 文件与压缩 | 使用已有 CSV/GZIP 缓冲、分片和磁盘限制，不增加对照文件 |
| 表达式缓存及钱包 | 不访问 |

沿用 SQL 的 (created_at,id) 游标和 ClickHouse 的 (created_at,request_id) 游标。没有新增 WHERE/ORDER BY/JOIN，不对 JSON 增加 SQL 过滤或新索引。

## Concurrency Analysis 与性能约束

在 100k RPM 主链容量设计下，本功能每个 relay 请求新增 DB 0 次、Redis 0 次、锁 0 次、goroutine 0 个；导出总成本仍由扫描量及原任务名额约束。

一批 B 行增加 O(B) 字段读取和运算，当前行额外状态 O(1)，最近倍率缓存 O(1)。不提高批大小，不全量收集日志，不预扫描，不重算每个显示列。每 128 条检查任务上下文，取消不必等待整批完成。

不改 exportGate 的 Before/Observe/Charge 顺序。Observe 仍只观察数据库查询，不能混入计算耗时；CPU 水位、固定休眠和按行限速继续使用原实现。无 CPU 读数时仍保留原固定节流。

本地微基准（Windows amd64，AMD Ryzen 7 8700G；仅字段解析与计算，不含数据库/格式化/压缩）：

| 路径 | 耗时 | 分配 |
|---|---:|---:|
| 常规覆盖 | 约 922 ns/条 | 576 B，25 次 |
| 新旧倍率相同 | 约 152 ns/条 | 0 B |
| 旧倍率缺失 | 约 34 ns/条 | 0 B |
| 含约 4 KiB Other 的覆盖 | 约 4701 ns/条 | 576 B，25 次 |

已完成真实 MySQL 5.7.44、PostgreSQL 9.6.24 各 100 万条日志的明细、汇总和混合导出，全部核对通过。默认节流下百万条覆盖明细分别耗时约 318 秒、271 秒，Go 堆峰值约 34 MiB、35 MiB，SQL 均为 347 次，与关闭覆盖相同。测试配置、逐行校验和测量边界见[大量数据实测报告](log-export-ratio-override-load-test.md)。

未进行 30k+ RPM 背景负载或 100k RPM 容量压测，不据此承诺 relay P95/P99。若实际瓶颈为宽 Other、I/O 或压缩，先降低后台导出速率；首版不引入自定义整数快速路径或大缓存。

## 测试设计与验证

先编写计算、权限和输出用例，再实现。已覆盖输入边界、显式零、旧零/缺失/null、专属价、舍入、溢出、非消费、行状态重置、日志不可变、固定兑换率、任务序列化、细列依赖、三种导出模式、明细/汇总一致、自助和员工拒绝参数、管理员开关及不增加估算请求。

已完成：

- 新功能端到端导出分别使用真实 SQLite 3.50.4、MySQL 5.7.44、PostgreSQL 9.6.24，通过；新测试自带隔离 Redis fixture，不因缺少外部 Redis 而跳过。
- 使用隔离 MySQL 主库、PostgreSQL 日志库和 Redis 7 执行 model 的 LogExport/LogSummary/SummaryScan 回归及 controller 的 LogExport/ExportGroupRatio 回归，通过。两个包顺序执行，避免旧孤儿文件清理用例干扰另一进程的临时导出文件。
- controller 完整包测试在独立 PostgreSQL 测试库通过（242 秒）。
- 前端导出目录 8 个测试文件、24 项测试通过。
- i18n 包测试、前端七语言同步、tsgo -b、lint、格式检查、生产构建通过；lint 仍有原有文件警告。
- 原 bun run typecheck 脚本调用不存在的 bun check，使用已安装的 bun x tsgo -b 完成类型检查，未修改项目脚本。
- 本轮补齐三条已存在支付 locale 的 Msg 常量，使原有 i18n 一致性测试能够通过。
- model 完整包在隔离 MySQL 主库和 PostgreSQL 日志库通过（74 秒）；未改动 upstream 用户会话测试或认证实现。

完整包运行时，依赖外部 Redis 的旧测试依照原守卫跳过；上述导出专项随后配置 Redis 单独运行并通过。新增覆盖测试自带 Redis fixture，普通测试环境也会执行。

主要验证命令：

```text
go test ./model -run '^TestWriteLogExportGroupRatioOverride$' -v -count=1
  # LOG_EXPORT_TEST_DSN 依次留空/指向隔离 MySQL/指向隔离 PostgreSQL
go test ./model -run 'LogExport|LogSummary|SummaryScan' -count=1
go test ./controller -run 'LogExport|ExportGroupRatio' -count=1
  # 上述两项配置 SQL_DSN、LOG_SQL_DSN、REDIS_CONN_STRING，顺序执行
go test ./model -count=1
go test ./controller -count=1
go test ./i18n -count=1
go build ./...
go test ./model -run '^$' -bench '^BenchmarkLogExportReprice$' -benchmem -benchtime=1s
cd web
bun run test src/features/usage-logs/export
bun x tsgo -b
bun run lint
bun run i18n:sync
bun run build
```

此次无数据库结构变更，未做迁移上线演练。ClickHouse 游标代码未修改，但本轮没有 ClickHouse 实例验证。

## 香港测试服务器部署（2026-11-07）

已更新 `43.251.102.17` 的原 `/root/new-api` 实例，版本为 `v1.0.0-rc.48-342-g60d67d78d-hk-20261107-reprice`（当前 HEAD 加本功能工作区修改）。Linux amd64 构建包含新前端；公网前端资源校验与本地构建一致。

旧程序、配置及主库备份保留于服务器 `/root/hk-update-20261107-reprice/`，日志库保留结构快照。正常 SIGTERM 退出后替换程序，约 5.77 秒恢复健康，保持单实例。配置哈希、主库结构、日志库结构均未变化，批量更新继续启用。

服务器实际 API 验收通过：管理员创建 both 导出并下载两个文件；原额度 1000、旧倍率 2、新倍率 1.5 输出 750；旧零及缺失两行保留 1000；汇总额度 2750，数据库原日志不变，other_raw 未输出。匿名管理入口、普通用户管理入口及自助倍率覆盖均被拒绝。临时账号、日志、会话和导出任务已清理。未在服务器追加百万条数据或发起高负载测试。

部署程序 SHA-256：`07a677b9a9d00ee2e554b7d8521ab4f6ea98674d3e1054453174777299b69db1`。本机部署、验证记录位于 `_local/hk-deploy-20261107/`，不包含 SSH 密码。

### 部署后的界面审核修复

已修复开启覆盖后编辑列导致隐藏选择丢失、纯汇总被明细列校验拦截的问题，并修正任务字段注释。新增六项回归测试，覆盖保存模板、新增/删除/排序/清空列、隐藏列数上限及汇总提交，导出目录共 30 项测试通过。类型检查使用 `bun x tsgo -b`（原 typecheck 脚本问题同前），格式检查、lint 和生产构建通过，lint 仅保留原有警告。本轮仅修改界面状态及注释，不增加数据库/Redis 操作，不改变后台计算。

2026-11-07 已再次部署香港测试服务器，版本 `v1.0.0-rc.48-342-g60d67d78d-hk-20261107-reprice-ui`，包含上述界面审核修复。正常 SIGTERM 切换约 6.71 秒恢复健康，保持单实例，配置及两库结构不变；公网状态接口、新界面资源 SHA-256、服务器实际明细/汇总下载、费用替换及权限验证均通过，临时测试数据已清理。程序 SHA-256 为 `f2ce63dd983f11db9859200ec4984aa6890afd6dbc6a6827a0a9419566fe2cce`；本次回滚程序、配置、主库备份保留于服务器 `/root/hk-update-20261107-reprice-ui/`，本机记录位于 `_local/hk-deploy-20261107-ui/`。
