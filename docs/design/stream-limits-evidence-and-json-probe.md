# 流式限制终态、图片证据及 JSON 探测修复

## 已确认目标与范围

用户确认按推荐修复本轮四项审计结论：Responses 正常限制终态误报、xAI 图片 n 覆盖遗漏、token 计价误用图片完成数作为用量，以及完整单行 JSON 的重复前缀扫描。仅更改新增受管流式流程，沿用入口开关和成功 HTTP 200 / WS 101 门控；非流式、开关关闭、连接失败及非 200 保持原路径。

## 数据流与业务边界

1. Responses 的 response.incomplete（以及兼容 response.done）必须具有 incomplete 状态、明确 max_output_tokens/content_filter 原因且没有显式 error，才作为正常限制终态。完整 Responses JSON 使用相同原因识别并保留 output 结构检查。正常终态交给原转换器生成 length/content_filter 或对应下游结束；未知原因、真实错误、非法用量继续异常。原生 Responses 在新路径采用该终态的实际 usage，原事件不改写；非受管分支不变。原生和转换 SSE 在此终态有确认字段时采用会话证据构建用量，保留显式零及已确认部分；没有确认字段时保留原估算逻辑，随后真实错误仍由统一异常流程覆盖选定用量。
2. 标准图片 DTO 与已明确使用同一 n 协议的 xAI 出站 JSON，在参数覆盖后同步完成数量；缺失/null 按协议默认 1。厂商自有计数字段不重解释，透传沿用入口原文解析值，multipart 不增加覆盖行为。
3. 图片完成数仍保存为诊断证据；只有按张计价时才能单独构成可结算用量。token 计价仅有 image_count、上游异常且已交付内容时，改走现有估算；用户断开但无可用计费用量时零结算。实际 token 字段即使明确为 0 仍是确认用量，不因值为 0 而估算。正常渠道计量、价格表达式、唯一结算及退款逻辑保持原合同。
4. 完整 JSON/NDJSON 格式探测保存首行搜索位置，只搜索新读入部分；第一行完整或 EOF 后只探测一次。保留原帧大小限制、JSON 校验、读取错误优先级和 NDJSON 兼容，不额外读取或缓存整段请求。

## API、存储、配置与错误处理

不新增 API、表、字段、索引、依赖或配置。继续通过现有 RootAuth 端点查询私有诊断；原始响应只在已确认上游异常时持久化。不采集请求，不增加请求日志。异常复用现有 error 补发和结算分支，真实原生 error 继续原样返回。

## Main Chain Impact

同步：每事件少量原因字段判断、出站 n 查询、按当前计价方式筛选确认资格，以及 JSON 首行增量扫描。适配器仅对 incomplete/done 终态增加确认用量快照与识别，正文 delta 不增加这些操作。异步：继续既有日志和统计队列。不新增网络访问、下一帧等待、定时器或重试；完整 JSON 本来就等待 EOF，修复减少等待期间的 CPU 开销。

## Shared Resource Audit

仅访问原请求会话、已有短锁、读缓冲和价格快照。新增 DB/Redis 访问、共享缓存、连接池、文件句柄及工作池为 0。无跨请求锁；不持锁进行网络 I/O。费用仍复用原 BillingSession 与异步日志，不增加第二次资金调整。

## Concurrency Analysis

30k/100k RPM 下新增 DB、Redis、工作者数量均为 0。Responses 和用量资格判断为每帧/每次结算常量成本；JSON 首行累计扫描从平方级降为线性，仍受 8 MiB 帧上限约束。以本地 64 KiB/1 MiB/4 MiB 微基准比较修复前后，不把本机时间当作生产 p95/p99 保证。

## 实施与验证步骤

先写回归并记录旧行为，再修改实现、补中文注释并核对：
- Responses SSE/完整 JSON：两种合法限制、缺失/未知原因、显式错误、非法 usage、真实失败；实际原生/Chat/Claude 转换和诊断资格。
- xAI：本地服务观察实际 n，覆盖增减、删除、null、透传及重试；HTTP 503 避开真实资金，后续使用内存成功响应校验完成约束。
- 图片：按次/token 计价 × 上游异常/客户端取消 × 有/无有效交付 × 无 token/显式零/正用量，验证选定用量和来源；无数据库资金操作。
- 大 JSON：长首行、排版 JSON、NDJSON、分片和 EOF/读取异常边界；记录修复前后相同输入的微基准。
- relay 相关测试及根构建；service 选定 DB 无关用例使用既有仓库外 overlay，保留仓库数据库测试入口。格式、UTF-8 及代码复核。

## 风险与可选项

仅对白名单限制原因放宽；真实 error 优先于限制终态。xAI 依据已实现的出站协议补接，不扩大到未知厂商字段。图片 token 缺失时不根据 base64 字节估造图像 token，使用已有输入/已交付内容估算。诊断保留原始 image_count，公开 confirmed_usage 表示当前计价方式可采用的证据。

## 实施结果

四项已实现，新增/修改的业务方法和参数说明已补中文注释并与实际分支核对。

涉及实现：relay/common/stream_responses.go、stream_session.go、stream_outcome.go，relay/channel/openai/relay_responses.go、chat_via_responses.go，relay/image_handler.go，service/stream_lifecycle.go。没有修改非流式处理函数、relaykit、前端、配置或数据库结构。

验证记录（2026-09-14）：
- 先运行回归，复现合法限制终态误报、xAI 覆盖后数量不同步、图片数单独被误认作 token 用量。修复分类后又用回归确认并消除显式零被正常分支旧估算覆盖的问题。
- Responses 原生/Chat/Claude 实际转换覆盖两种限制原因、正常尾帧、无诊断及真实用量；显式 0/0、0/2、5/0 均保持。额外验证非法 usage，以及限制终态之前/之后到达的真实错误继续优先。
- xAI 本地 HTTP 夹具验证 n 增减、null/删除、透传和重复尝试；保留标准图片与 multipart 回归。
- 图片计费资格覆盖按张/token、客户端取消/上游异常、交付与否和缺失/零/正 token；只调用内存策略与额度计算，没有实际资金操作。
- JSON 游标有确定性进度断言；长首行、4096 字节边界、排版 JSON、NDJSON 和尾部换行兼容通过。
- relay 全包、common、logger、router、operation_setting、i18n 测试通过；选定 service/model/controller/middleware 的流式、诊断、日志及退款测试通过，后者使用既有仓库外 overlay 跳过数据库 TestMain，未改动仓库测试入口。新增核心回归连续 20 次通过。
- 根模块 go build ./...、git diff --check、gofmt 及本轮文件 UTF-8 无 BOM 检查通过。

### 本地 JSON 微基准

相同命令运行三次，表中取 ns/op 中位数折算为毫秒；Windows amd64 / i9-14900HX，包含完整观察包装、校验和复制，不代表网络或客户端延迟。

| 单行 JSON 内容规模 | 修复前 | 修复后 |
|---|---:|---:|
| 64 KiB | 0.544 ms | 0.555 ms |
| 1 MiB | 10.256 ms | 9.153 ms |
| 4 MiB | 61.137 ms | 36.441 ms |

命令：`go test ./relay/common -run '^TestStreamJSONProbeBoundaries$' -bench '^BenchmarkStreamWholeJSONProbe$' -benchtime=300ms -count=3`。4 MiB 中位耗时约下降 40%；内存分配量基本不变，不宣称内存优化。其他修复不增加下一帧等待，生产首字/尾字延迟及 30k/100k RPM 容量仍需环境压测。

未进行真实数据库联调或供应商调用；未提交 Git、未部署。
