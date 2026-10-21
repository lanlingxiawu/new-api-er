# 流式诊断与 Tokens 修复：回归和压力测试

测试日期：2026-10-21。对象为当前工作区中“诊断固定开启、拒答留存诊断、仅缓存用量显示”修改，基于提交 `84611fbca`。未部署，未请求真实 AI 上游，未操作线上数据库。

## 回归范围与结果

| 检查 | 结果 |
| --- | --- |
| `go test -json -count=1 ./relay/... ./setting/config ./setting/operation_setting` | 52 个包通过；`relay` 包的既有资源计数测试失败，详见下文 |
| Claude、Gemini、MiniMax、relay/common、setting/config、setting/operation_setting 连续重复 10 次 | 全部通过 |
| service 的 stream_diagnostic、stream_log_info、stream_response_gate 测试 | 文件列表模式通过 |
| model/controller 的 stream_diagnostic 测试 | 文件列表模式通过，使用现有 SQLite 回退 |
| `TestProviderBodyGovernanceParity` 独立运行 | 通过 |
| `go build ./...` | 通过 |
| 前端 `bun run typecheck` | 通过 |
| `bun run scripts/verify-stream-tokens-render.tsx` | 通过：四类仅缓存用量、普通用量、全零，以及 Root/管理员/用户入口权限 |
| 变更前端文件定向 oxlint | 通过 |
| 全量 `bun run lint` | 12 个既有 curly 错误及 1 个 warning，未通过 |
| `go test -race ./relay/channel/claude ./relay/common` | 无法启动：当前 `CGO_ENABLED=0`，缺少 C 编译工具链 |

回归覆盖正常结束、拒答、协议错误、取消、重试隔离、采集边界、成功响应门控、用量及诊断权限。本次修改没有把拒答改判为传输错误，也没有取消 Root 权限限制。

`TestProviderBodyGovernanceParity` 在整包运行时有 6 个子用例报告全局 `BodyMemoryReserved=16832`，要求值为零；独立运行通过。前一轮已用 HEAD overlay 恢复本次 Go 修改并复现相同问题。此处记录为既有整包测试状态污染，未修改全局计数或跳过失败测试来宣称全绿。JSON 日志中共有 3665 个通过、7 个失败事件（失败数包含父测试）、2 个跳过事件，不等同于独立顶层测试数。

全量前端 lint 的错误位于未修改的 `web/src/features/system-settings/models/price-monitor-rule-scenarios.ts`，warning 位于既有 footer。当前环境没有 SQL_DSN，service 的标准 TestMain 无法初始化数据库；文件列表模式只运行指定单元测试，不代表 service 全包、MySQL/PostgreSQL 或真实日志入库验证。Docker 引擎也未运行。

文件列表模式使用 `go list -json ./<package>` 的全部 `GoFiles` 加指定测试文件传给 `go test -count=1`，没有更改 TestMain，也没有替换 GORM。model/controller 的测试使用其已有真实 SQLite 数据库回退实现。

## 压测方法

- 环境：Windows 11 专业版 10.0.26200，AMD Ryzen 7 8700G，8 核/16 线程，约 47.1 GiB 可见内存；Go 1.25.1 windows/amd64。
- 新增可重复运行的 `TestClaudeDiagnosticLoad`，默认跳过，仅显式设置环境变量时启动。本次每个场景运行 30 秒，另有每场景 20 次预热。
- 正常完成、零输出拒答、带 64 KiB 附加字段的拒答，分别按 500 和 1667 请求/秒调度；相当于 30,000 和 100,020 RPM 的目标速率。
- 每个场景均一半原生 Claude、一半转换为 OpenAI。真实 loopback HTTP 响应经过现有解析器、流式终止、用量归一化、动态计费计算及诊断序列化。
- 每请求断言：正常/拒答分类正确、缓存写入 1200、普通输入/输出准确、计费 quota 准确、拒答类别和下游证据存在、保留正文不超过上限、正常响应不持久化原始正文、不向客户端注入错误事件。
- 固定 64 个工作者、64 容量队列、最多 64 个 HTTP 连接。按目标到达时间调度；队列满时的等待会纳入计划延迟，避免只统计处理时间而忽略排队。
- Windows 使用 QueryPerformanceCounter 采样，避免本机 `time.Since` 对亚毫秒样本出现零值；其他系统使用 Go 单调时钟。
- 处理延迟包含本地 HTTP、解析、计费计算、诊断序列化和测试断言。计划延迟额外包含调度与排队。每 100 ms 采样堆和 goroutine，峰值为采样峰值，可能漏掉瞬时尖峰。
- 测试期间关闭逐请求控制台日志输出；不运行其他编译或回归任务争抢 CPU。结束后关闭连接，验证工作者/连接 goroutine 在 3 秒内回落，再采样 GC 后堆。

这是一项组件持续负载测试，不经过网关鉴权、渠道分配、配额数据库、异步日志队列和日志落库，也不模拟多秒长流或慢客户端。目标速率达到不表示网关整链路的最大容量已经验证。

复现命令（PowerShell）：

```powershell
$env:STREAM_DIAGNOSTIC_LOAD='1'
$env:STREAM_DIAGNOSTIC_LOAD_SECONDS='30'
go test ./relay/channel/claude -run '^TestClaudeDiagnosticLoad$' -v -count=1 -timeout=8m
```

原始输出、逐场景 JSON 和回归日志保存在本地 `_local/stream-diagnostics-validation-20261021/`（不纳入版本控制）。

## 持续压测结果

六个场景全部通过，合计 **195,030 次计量请求，零错误**，不含 120 次预热。每个场景持续约 30 秒，总计约 180 秒。两档目标到达速率均达到，以下 RPM 是实际完成请求数除以实际时长计算，并非最大吞吐量。

| 场景 | 请求数 | 实际 RPM | 处理 P99（ms） | 含调度排队 P99（ms） | 错误 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 正常完成，500 RPS | 15,000 | 30,001 | 0.653 | 1.112 | 0 |
| 正常完成，1667 RPS | 50,010 | 100,019 | 0.581 | 0.978 | 0 |
| 零输出拒答，500 RPS | 15,000 | 30,001 | 0.613 | 1.071 | 0 |
| 零输出拒答，1667 RPS | 50,010 | 100,020 | 0.535 | 0.957 | 0 |
| 64 KiB 大响应拒答，500 RPS | 15,000 | 30,000 | 2.186 | 2.584 | 0 |
| 64 KiB 大响应拒答，1667 RPS | 50,010 | 100,017 | 5.521 | 5.815 | 0 |

所有场景结束后的 goroutine 均回落至 3；每场景预热后基线为 7，包含尚未关闭的测试 HTTP 连接。没有观察到测试结束后的工作者或连接 goroutine 残留。整个测试进程的采样 Go 堆峰值最大约 26.1 MiB；各场景结束且 GC 后堆约 7.1–8.0 MiB。这里不是进程 RSS，也不是生产网关内存占用。

大响应拒答在 1667 RPS 时，最多观察到 28 个同时处理的请求，计划延迟最大 17.99 ms。平均每请求总分配约 957 KB，30 秒内 GC 6532 次；该指标包含 HTTP、完整上游 JSON 解析、响应写入和测试断言等开销，不能单独归因于新增诊断逻辑。普通/短拒答场景约 62–68 KB/请求。大响应带来的分配成本仍需要在真实长流和日志落库环境结合 CPU/heap profile 评估。

测试序列化的 `other` 字段平均为正常 755 字节、短拒答 2405 字节、大响应拒答 5392 字节。按 1667 RPS 全拒答估算，后两者分别约 4.0 MB/s 和 9.0 MB/s，尚未计入完整日志行、数据库索引、WAL 或复制。这是日志载荷估算，**未测得日志队列和数据库能够持续承受该写入量**。

## 结论与未完成的验证

本次修改相关回归、重复执行及本地 30k/100k RPM 组件负载均通过，没有发现诊断缺失、缓存用量丢失、计费断言错误或结束后的工作者残留。不能据此宣称全量检查全绿或生产网关已具备 100k RPM 容量。

仍需具备隔离测试数据库和运行中的网关后，验证鉴权、分发、配额结算、日志队列及落库的整链路压力。初次压测没有覆盖慢客户端；用户追加后的真实 TCP 补测见[慢客户端补测报告](stream-slow-client-validation-20261021.md)。长时间稳定性、竞争检测及 MySQL/PostgreSQL 集成仍未覆盖。另有上述既有整包资源计数失败和全量 lint 错误待处理。本轮仅增加测试夹具、测试计时支持和验证记录，没有为获得通过结果修改业务逻辑。

## 慢客户端补测计划

用户追加要求后，增加真实 TCP 的本地 mock 上游 → Claude 流处理器 → HTTP 客户端测试。保持生产 30 秒写入期限，测试监听器缩小发送缓冲、客户端缩小接收缓冲，以确保大回复能产生真实背压。测试不使用内存 ResponseRecorder 代替下游连接。

预设用例：64 个快速读取作为对照；16/64/128 个限速读取；64 个读完响应头后停止读取；64 个读取部分正文后断开；16 个慢读且单帧文本增大至 256 KiB。每组一半 Claude 原生、一半 OpenAI 转换。完整读取应正常返回拒答诊断；停止读取应触发生产写入期限；中途关闭应终止上游并释放工作者；所有用例检查采集边界、活动请求归零及 GC 后堆，并记录负载期间堆和 goroutine 时间序列。此测试仍不包含数据库或整套网关中间件。
