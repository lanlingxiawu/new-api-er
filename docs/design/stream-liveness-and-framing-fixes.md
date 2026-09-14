# 流式保活、Realtime 轮次及 SSE 分帧修复

## 目标与已确认范围

用户确认按推荐修复本轮三类审计问题：HTTP 保活/终止错误续期遗漏、Realtime 合法取消及可恢复请求错误关闭整个连接、SSE 混合换行误判。沿用入口流式开关及真实 HTTP 200 / WS 101 门控；非流式、失败握手、价格、Root 诊断权限和异常六行计费合同保持。

## 数据流及业务边界

1. 通用扫描器的每次 ping 在原写入所有者内续期；HTTP 终止 error 在专用超时写入通道内获得新的有界写入期限。普通内容仍受原请求超时控制，错误不重新开放业务写入。统一 HTTP 流写入期限为原来的 30 秒，WS 仍为 15 秒。
2. Realtime `invalid_request_error` 属于可恢复的客户端事件错误：原字节转发，不更新终止原因、有效内容、确认用量或“终止错误已发送”标记，不追加轮次预留；已经开始的回答继续。保留 `server_error`、未知错误及失败响应原有异常策略。
3. Realtime `response.done.status=cancelled`，以及 `incomplete` 且原因明确为 `max_output_tokens` / `content_filter`，作为当前轮次的协议终态；保留原始状态，不伪造成 completed。确认用量按本轮结算，无确认时沿用正常轮次估算；不以无正文为由套用上游故障免收费。随后重建轮次状态，保留 WS。未知 incomplete、failed 及真正连接故障维持原异常结算。
4. SSE 按 LF、CRLF、CR 行边界判空行，多种形式可混用。增量扫描器只扫描新增字节，原始响应采集不规范化换行；payload 多 data 行仍以 LF 连接。CR 到达即能完成空行，不等待下一事件；跨 Read 的 CRLF 后续 LF 作为延续换行跳过语义处理，原字节留在随后读取的原始帧中。EOF 残余非空半帧仍异常。
5. 受管共享扫描器、独立响应观察器、下游 SSE 写入器和原生 Claude 严格分帧共用边界规则；SDK JSON、NDJSON 和裸媒体不改分帧语义。

协议依据：[OpenAI Realtime](https://developers.openai.com/api/reference/resources/realtime)、[SSE 标准](https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream)。

## API、数据模型、配置及错误处理

无新增 API、公开日志字段、表、索引、迁移、配置或依赖。复用 stream_error_setting.enabled/capture_response 和既有唯一资金会话、异步日志队列。原始错误仍原样转发；仅连接级/未豁免上游失败生成诊断，恢复事件本身不触发诊断。底层写失败仍归类客户端传输失败；超时后的终止错误仍最多一次。

## Main Chain Impact

同步：原所有者的 SetWriteDeadline、请求局部增量字节扫描、Realtime 事件分类及原逐轮预留。无新网络读取、事件等待或整段响应缓存；无需等待下一 SSE 事件才交付当前完整事件。异步：仅既有日志/统计队列，无新增后台任务。

## Shared Resource Audit

仅原请求的 HTTP writer、WS、StreamSession、固定上限帧缓冲及 BillingSession。新游标为读取器/写入器局部字段，不跨请求共享，不持有会话锁做网络 I/O。复用原逐轮预留及最终结算，不新增 DB/Redis key、共享缓存、连接池或工作池。

## Concurrency Analysis

30k / 100k RPM 下新增 DB/Redis 调用和工作协程均为 0；每次原 ping/终止写入增加一次已有连接 deadline 更新。SSE 扫描按字节线性推进，每个活跃读写器仅增加固定大小游标；8 MiB 帧预算不变。Realtime 每帧只有常量字段检查，轮次数量和预留调用不因恢复事件增加。真实 p95/p99 及慢客户端负载需部署环境验证。

## 测试与实施步骤

先写回归并在旧实现确认失败，再实施修复。边界/条件/路径覆盖：
- 模拟已过期 deadline 的保活和各 HTTP 下游格式的终止错误；受管超时不开放后续正文，真实写/刷新失败仍失败。
- WS 取消轮次无正文但有确认量、正常下一轮、重复 done、可恢复错误在轮间/轮内、后续真正故障仍补 error，费用/预留次数和诊断资格正确。
- 所有合法换行组合、多 data 行、逐字节短读、CRLF 跨块、半帧 EOF、超限、原错误字节和首帧不等下一事件。
- 重跑 relay 全包、结算及诊断选定测试、根构建与格式检查；使用本地夹具，不连接真实 AI 或操作真实余额。

## 实施结果

- HTTP deadline 常量集中在 `relay/common/stream_writer.go`；共享扫描器 ping 写前续期，终止 HTTP error 在原受管终止回调内续期。保留超时后普通正文禁写和错误最多一次的语义。
- Realtime 分类位于 `relay/common/stream_realtime.go`，观察及交付两处均排除恢复事件占用终止错误位。WS 所有者独立跟踪实际回答是否开始，避免被拒绝的 response.create 产生虚假轮次；确认过的正常取消轮次保留确认用量，后续轮次继续。
- 通用 `StreamFrameScanner` 用于受管读取、观察器、下游写入与 Claude 严格读取。分块后的可选 LF 独立转发为原始字节，没有事件语义；逐字节 CRLF 回归确认末尾 LF 不丢失。新增及修改的方法/状态字段已补中文用途注释并对照实现校核。
- 已执行 test-first：过期 deadline、Realtime 提前结束、混合换行等待/误判的旧实现均触发新增回归失败；修复后通过。额外覆盖终止标记误占位、CRLF 尾字节、受管超时后的终止通道。
- 已通过 `go test ./relay/... ./logger ./common ./router ./setting/operation_setting ./i18n -count=1`，以及根模块 `go build ./...`。Realtime 恢复/取消多轮夹具连续运行 20 次通过。
- service/model/controller/middleware 的选定流式、结算、诊断和超时测试通过；复用临时 Go overlay 跳过依赖真实数据库的 TestMain，不更改仓库测试入口、不连接真实 AI、不操作真实余额。本次未执行真实数据库集成、race 或生产负载测试。
- 心跳回归使用原 1 秒最小定时粒度与本地 pipe；其他新增流式夹具不等待真实上游或 30 秒 deadline。未增加生产首帧/逐帧等待；高并发及慢客户端 p95/p99 仍以部署环境压测为准。
