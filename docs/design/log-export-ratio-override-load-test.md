# 分组倍率覆盖：大量日志导出实测

日期：2026-11-07。真实 MySQL 5.7.44、PostgreSQL 9.6.24 各使用 100 万条合成日志，七个场景全部通过。测试代码：`model/log_export_reprice_load_test.go`。

## 数据与执行方式

- Windows amd64 / AMD Ryzen 7 8700G；数据库分别运行于 Docker，每个容器限制 2 CPU、2 GiB 内存。
- 使用专用、可丢弃的 `reprice_export_load` 数据库和隔离 miniRedis fixture，不访问项目业务数据。
- 每库 100 万条消费日志，31 个模型分组；每秒 16 条，覆盖多个小时窗口。
- 原倍率为 2、4、0、缺失、1.5、0.8、0.333333、2.5，覆盖为 1.5；其中 25% 为零或缺失，必须保留原费用。部分记录含专属分组倍率。
- Other 的诊断填充 80% 为 192 B、20% 为 4096 B；平均 Other 约 1 KiB。数据为合成数据，其可压缩性不能代表全部生产日志。
- 调用真实 `runLogExport`：获取任务名额、批量游标查询、倍率计算、CSV/GZIP 写入、分片、任务完成。每批 3000 条、gzip level 1、4 MiB 写缓冲，强制每 20 万条分片以验证跨文件边界。
- 百万条明细生成 5 个文件，明细加汇总生成 6 个文件。覆盖明细压缩后总计 15,716,824 B（约 15 MiB），两库大小一致。
- 导出完成后逐行解压读取所有文件，校验 gzip 完整性、连续请求 ID、无遗漏/重复、倍率、额度、美元费用及 token 数。汇总按独立整数有理数算法逐条算后求和，核对 31 个分组。

每库累计扫描 610 万条，核对 410 万条明细、93 条汇总、24 个文件；两库合计扫描 1220 万条，核对 820 万条明细、186 条汇总、48 个文件。

## 默认节流验收

保持默认批间休眠 100 ms、最大 20,000 行/秒。以下时间只包含导出任务，不包含造数、解压核对和数据清理。

| 数据库 / 明细覆盖规模 | 总耗时 | 任务记录节流时间 | 进程 CPU 时间 | Go 堆峰值 | 进程 RSS 峰值 | SQL 查询 |
|---|---:|---:|---:|---:|---:|---:|
| MySQL / 10 万条 | 17.2 秒 | 15.1 秒 | 1.02 秒 | 32.9 MiB | 62.0 MiB | 35 |
| MySQL / 100 万条 | 317.7 秒 | 304.3 秒 | 18.23 秒 | 33.8 MiB | 71.0 MiB | 347 |
| PostgreSQL / 10 万条 | 9.6 秒 | 5.0 秒 | 0.88 秒 | 34.5 MiB | 68.8 MiB | 35 |
| PostgreSQL / 100 万条 | 270.7 秒 | 252.7 秒 | 11.75 秒 | 35.3 MiB | 73.6 MiB | 347 |

百万条实际平均约 3148 / 3694 行每秒，低于速率上限：现有节流还包括数据库耗时反馈和批间等待，20,000 行/秒是上限，不是保证吞吐。耗时主要为已有节流等待。本轮不修改生产节流配置。

从 10 万增加至 100 万条，Go 堆峰值基本稳定，没有出现随总行数成比例的常驻内存增长。这里的堆/RSS仅为 Go 测试进程，不包含数据库、Docker 或文件系统缓存。

## 减少固定节流后的对照

仅测试时设置批间休眠为 0、最大行速为现有配置上限 500,000；资源反馈控制仍保留，百万条任务仍记录约 2.0 秒节流。本表用于观察处理开销，不作为生产配置建议。

| 数据库 / 场景（均扫描 100 万条） | 耗时 | 进程 CPU 时间 | Go 堆峰值 | RSS 峰值 | SQL 查询 |
|---|---:|---:|---:|---:|---:|
| MySQL / 明细不覆盖 | 19.4 秒 | 12.81 秒 | 33.7 MiB | 67.5 MiB | 347 |
| MySQL / 明细覆盖 | 18.0 秒 | 10.25 秒 | 34.1 MiB | 68.9 MiB | 347 |
| MySQL / 汇总不覆盖 | 6.8 秒 | 1.72 秒 | 16.7 MiB | 49.2 MiB | 347 |
| MySQL / 汇总覆盖 | 12.9 秒 | 5.95 秒 | 24.0 MiB | 57.0 MiB | 347 |
| MySQL / 明细加汇总覆盖 | 16.5 秒 | 9.25 秒 | 34.1 MiB | 71.1 MiB | 347 |
| PostgreSQL / 明细不覆盖 | 46.2 秒 | 12.69 秒 | 34.9 MiB | 72.2 MiB | 347 |
| PostgreSQL / 明细覆盖 | 21.0 秒 | 11.08 秒 | 35.0 MiB | 73.1 MiB | 347 |
| PostgreSQL / 汇总不覆盖 | 6.4 秒 | 1.50 秒 | 17.2 MiB | 53.3 MiB | 347 |
| PostgreSQL / 汇总覆盖 | 18.3 秒 | 7.80 秒 | 24.8 MiB | 60.8 MiB | 347 |
| PostgreSQL / 明细加汇总覆盖 | 20.3 秒 | 10.02 秒 | 35.5 MiB | 74.5 MiB | 347 |

所有场景采样到的 SQL 同时使用连接峰值为 1，连接等待次数为 0。覆盖与不覆盖查询次数一致，未引入逐行 SQL；both 仍只扫描一次。

纯汇总覆盖需要额外读取、解析 Other 并逐条重算，因此耗时和分配增加。其总分配约 2.0 GiB，经过 GC 回收后的堆峰值约 24–25 MiB；总分配量不能理解为同时占用的内存。明细原来就读取 Other，本轮覆盖明细总分配约 2.7 GiB，峰值约 34–35 MiB。

各场景顺序执行且缓存未清空；PostgreSQL 前几个场景还受到前一个 MySQL 测试清理的宿主磁盘竞争。故不能据墙钟时间得出“覆盖反而加速”或数据库性能排名。本次是单轮实测，不提供统计置信区间。

## 查询与共享资源

- MySQL 代表性窗口查询的 EXPLAIN 使用 `idx_created_at_id` 范围扫描，包含 index condition / where，无 filesort。
- PostgreSQL 窗口内带游标的查询使用 `idx_created_at_id` Index Scan，时间范围为 Index Cond，用户名及游标条件为 Filter，LIMIT 3000，未采用全表顺序扫描。
- 不改变 SQL 字段以外的筛选或游标，不增加索引、事务、锁或连接池；数据库日志表与异步日志写入共享，本测试没有模拟并行写入竞争。
- 导出保持单任务名额，没有增加 goroutine 并行计算，也没有调用外部计算脚本。

## 测量范围与复测

每 25 ms 采样 Go 堆、RSS 和连接池；短暂峰值可能漏采。CPU 为整个测试进程的 user+system 时间，包含采样等开销。每场景测量前执行 `debug.FreeOSMemory`，测量结束后才解压核对。

测试未启动生产 CPU 采样器，因此没有验证 CPU 软/硬水位自动暂停。没有进行 30k+ RPM relay 混合负载、并发日志写入、慢 HTTP 下载或浏览器下载测试，也未做 SQLite/ClickHouse 百万条测试；不能据此承诺主链 P95/P99。数据库结构未变更。

先准备专用数据库，再从项目根目录运行（PowerShell，DSN 示例不含密码）：

```powershell
Remove-Item Env:SQL_DSN -ErrorAction SilentlyContinue
Remove-Item Env:LOG_SQL_DSN -ErrorAction SilentlyContinue
$env:EXPORT_REPRICE_LOAD = '1'
# MySQL 5.7.44
$env:LOG_EXPORT_TEST_DSN = 'root@tcp(127.0.0.1:23326)/reprice_export_load?charset=utf8mb4&parseTime=true'
# 或 PostgreSQL 9.6.24
# $env:LOG_EXPORT_TEST_DSN = 'postgres://postgres@127.0.0.1:23327/reprice_export_load?sslmode=disable'
$env:EXPORT_REPRICE_LOAD_REPORT = Join-Path $PWD 'reprice-load-result.json'
go test ./model -run '^TestLogExportRepriceLargeData$' -v -count=1 -timeout=30m
```

测试拒绝非指定名称的数据库，结束时只删除本次合成用户名对应的数据，清理任务元数据与导出文件。普通测试不设置 `EXPORT_REPRICE_LOAD=1` 时跳过百万条场景。复测必须使用专用数据库实例；大批量造数和删除本身也会消耗 I/O。

本机原始结果位于 `_local/log-export-reprice-load-{mysql,postgres}.json`，执行日志位于 `tmp/log-export-reprice-load-{mysql,postgres}.log`，这些为本地忽略的测试产物。最终两库均 PASS；包含清理的完整测试时间分别约 682 秒和 450 秒。测试容器使用完毕后移除，未部署功能。
