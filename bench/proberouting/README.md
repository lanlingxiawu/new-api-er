# 渠道探针范围压测

使用明确指定的 `.env` 文件中的 `SQL_DSN` 连接 MySQL/PostgreSQL，压测真实 `middleware.Distribute()` 的探针分类和选路。拒绝空连接配置及 SQLite，不运行数据库迁移，不改变既有渠道。数据库必须已具备当前版本的 `channels`、`abilities` 表。

从仓库根目录运行：

```powershell
go build -o tmp/probe-routing-load.exe ./bench/proberouting
./tmp/probe-routing-load.exe -env-file .env.example -duration 30s -report tmp/probe-load-report.json
```

Linux 可直接使用 `go run ./bench/proberouting -env-file /path/to/.env -duration 30s -report /path/to/report.json`。

## 范围与资源

- 在指定数据库创建随机独立分组中的两个模拟渠道，一高一低优先级；仅修改这两个测试渠道，结束按明确 ID 清理渠道和能力行并核对剩余数。
- 连接池最多 4 条连接，启用本进程渠道内存缓存。无 Redis、真实 AI、鉴权、账单或用户余额写入。不要把结果称为完整网关端到端容量。
- HTTP 请求实际经过本地 TCP、三个入口的请求解析、探针分类、渠道分发和本地 HTTP 模拟上游。流式响应消费至 DONE；每次请求核对所选渠道，选路错误立即计入失败。
- 三入口均衡混合、流式/非流式各 50%、短探针 75%、超过阈值的普通输入 25%。比较关闭、全部、非流、流四种设置。
- 先用 64 个有界 worker 校准模拟上游吞吐，需高于最高目标 RPS 的三倍且无错误；随后按 500 / 1667 RPS 开环定速发压，对应约 3 万 / 10 万 RPM。每种设置每档默认 30 秒。
- 每 10 秒后台刷新一次策略（正式默认 60 秒），覆盖刷新与请求并发。分别记录全部 DB 查询、后台刷新次数、扣除刷新后的请求路径查询，以及连接池等待。
- 压测器队列有界；队列溢出记为丢弃，不能把降速后的成功请求数当作目标吞吐。延迟同时报告实际 HTTP 耗时和从计划发出时刻计算的 P99，以暴露调度/排队延迟。
- Windows 用 QueryPerformanceCounter 测量亚毫秒延迟，避免低分辨率时钟把请求计成 0ms。GC 累计暂停数据仍来自 runtime，仅作辅助参考。

输出 JSON 含各轮吞吐、P50/P95/P99/max、错误/错路/丢弃数、分配、堆大小、GC、goroutine 与数据库调用统计。内存和 goroutine 包含同进程的压测器、分发服务和模拟上游；不能直接视为生产网关自身资源占用。

工具使用 `SQL_DSN` 指定连接，不把连接字符串写入日志或报告；其他全局业务设置保持测试进程默认值，测试渠道策略真实保存并从数据库刷新。未覆盖大规模渠道、关闭缓存、Redis、真实上游延迟、长时间稳定性或多机部署。

异常退出会尝试行级清理；若进程被强制杀死，使用开始时输出的唯一分组和两个 ID 定位残留，仅清理本次测试行。
