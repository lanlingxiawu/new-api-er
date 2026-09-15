# 全渠道流式异常、部分结算及响应诊断

后续确认恢复原请求日志：私有流及 `/messages` 不再排除采集/投递，统一遵循原 RequestLog 开关、过滤、预算及文件存储逻辑，见 [恢复原请求日志](restore-original-request-logging.md)。本条覆盖下文关于普通请求日志排除的历史描述，私有诊断规则独立不变。

最新确认：统一大小上限已从 100 MiB 提升为 **200 MiB（209,715,200 字节）**；下文历史大小数值以 [最新实施记录](stream-size-limit-100mib.md) 为准，其余规则保持不变。

2026-09-15 零输出补估更新：有输出内容但用量为零时（包括最终显式零）补充本地输出估算，保留确认输入/缓存并标记 mixed。用户断开按接收内容，正常/上游异常按交付内容，原免收费资格不变；本条覆盖下文历史“显式零始终不估算”约定，见 [零输出补估](stream-zero-output-estimation.md)。

2026-09-15 大小上限更新：本文历史容量分析中的 8 MiB 已统一调整为 100 MiB，包含完整 JSON、事件及工具预算；诊断截取不变。详见 [统一大小上限](stream-size-limit-100mib.md)。

2026-09-15 用户确认调整断开结算，见 [用户断开估算](client-disconnect-estimation.md)：有确认用量优先使用；无确认但已收到业务响应时按输入请求和接收输出估算；零响应释放预扣。上游异常仍使用原有效交付规则。

2026-09-14 日志副作用修复见 [流式错误来源与日志解耦](stream-error-origin-and-logging.md)：上游 DTO 解析、本地转换与读取错误在来源处登记；正常完成后的读取错误不再被日志改判失败。计费矩阵、门控及渠道自动禁用逻辑不变。

2026-09-14 旧版工具交付修复见 [旧版 function_call 有效交付](stream-legacy-function-delivery.md)：按候选累计实际成功写出并刷新的旧版函数名称及参数，完整调用结束后参与有效内容判断和异常估算；不改原始帧、正常流计量及渠道自动禁用逻辑。

2026-09-14 两项后续修复见 [原生图片协议与渠道总用量归一化](stream-native-image-and-total-usage.md)：MiniMax/Ali 原生图片按渠道校验，Ali 异步任务中间态不伪装完成，轮询沿用同一会话；Baidu/xAI 异常结算保留同帧总量减输入的原适配器输出口径。

进一步复核修复见 [原生语音完成校验与图片出站张数](stream-native-audio-and-image-count.md)：MiniMax 语音完整 JSON 按渠道和模式限定识别，实际音频/跳转恢复对应响应类型；MiniMax/Ali 图片按最终原生出站计数字段校验，Ali 透传仅使用原生 parameters.n，不读取或记录额外请求正文。

2026-09-14 两项后续修复见 [Dify 确认用量与流式媒体类型识别](stream-dify-usage-and-media-type.md)：Dify message_end.metadata.usage 纳入确认用量，尾帧写入/刷新失败不再遗漏该用量；流式观察、下游交付和媒体终止统一使用基础 MIME 类型，忽略参数值和大小写，裸媒体不再误等 EOF 或触发文本帧限制。

2026-09-14 四项后续修复见 [流式限制终态、图片证据及 JSON 探测修复](stream-limits-evidence-and-json-probe.md)：Responses 明确上限/过滤原因的 incomplete 按正常终态，保留原事件与确认零用量；xAI 根据实际出站 n 同步完成约束；token 计价不把独立图片张数当作 token 确认用量；完整 JSON 首行探测改为增量扫描。

2026-09-13 三类后续修复见 [流式保活、Realtime 轮次及 SSE 分帧修复](stream-liveness-and-framing-fixes.md)：HTTP ping/终止错误续期；Realtime 合法取消及明确原因的 incomplete 按正常轮次结算，可恢复请求错误只透传；SSE 统一支持混合 LF/CRLF/CR 并保持增量交付。

2026-09-13 四项后续修复见 [流式顺序、完成判断与延迟验证](stream-four-regression-fixes.md)：SDK Claude 的真实 message_delta 单独放行；候选按索引与入口数量重新判定完成；受管状态在写入所有者消费帧时推进；完整 JSON 校验结构后才成功。OpenAI 受管路径逐帧写出，不再等下一帧才交付上一帧。原计费矩阵、诊断权限、非流式和失败握手边界保持。

用户于 2026-09-12 确认实施。沿用 `claude-stream-error-handling.md` 的六行异常计费约定，扩展到全部渠道；非流式请求保持原流程。

2026-09-13 边界收窄见 [成功响应门控](stream-success-response-gate.md)：HTTP 仅真实 200 后、WS 仅真实成功 101 后启用；非 200 / 未收到响应完全走原错误、重试和退款，不新增诊断。

## 目标与范围

按入口请求的流式属性冻结开关，不依赖后续被上游 Content-Type 改写的 IsStream。覆盖 HTTP SSE、NDJSON、SDK EventStream、协议转换、请求体透传，以及现有流式媒体和 Realtime。不是给原本没有流式能力的渠道新增生成接口。

## 数据流

入口冻结模式 → 每次尝试初始化请求局部会话 → HTTP 客户端边界采集原始响应 → 协议观察（SSE/NDJSON/SDK 解码事件）→ 原渠道转换 → 下游完整写入/刷新后统计有效交付 → 统一终止 → 唯一结算 → 既有异步日志队列。

通用会话不替换各渠道转换器。原生 Claude 严格解析继续作为协议专用实现，和通用实现每次只由一个处理器拥有终止权。保留历史 Claude 类型及日志键作为兼容别名，避免改写历史记录及现有 API 使用方。

## 协议与错误

正常结束按上游协议判断，支持 Claude message_stop、OpenAI 结束帧/finish_reason、Responses 终态、Gemini finishReason、Ollama done 及供应商终止事件。未知扩展事件不自动视作完成。非空数据解析失败、未正常完成的 EOF、读错误、超时为异常；用户取消和写/刷新失败独立归类。已取得上游错误时不再重复补发。同协议保留原帧，跨协议输出下游兼容错误；绝不补造成功结束。裸二进制媒体异常时停止转发，不插入 SSE，也不改变已经发出的 HTTP 状态码。

2026-09-13 经确认修复多候选结束帧过滤：`choices.finish_reason` / `candidates.finishReason` 属于候选级结束，在未记录终止原因时原样转发整帧；响应级成功终止仍须全流 Complete。已记录异常后两类成功尾帧继续过滤，不更改原错误补发及计费矩阵，见 [多候选尾帧修复](stream-candidate-end-forwarding.md)。

2026-09-13 经确认修复 Realtime 轮间异常漏补错误：已有完成轮次且当前无活动轮次时，上游异常仅补发一次终止 JSON，不再调用轮次计量/预留；轮间受管超时按 timeout 处理，正常关闭、用户取消及已发送错误不重复补发。原完成轮次用量保持，见 [Realtime 轮间终止修复](realtime-interround-terminal-error.md)。

连接失败 / HTTP 非 200 沿用既有错误响应、状态码映射、重试与退款，不进入新异常结算或保存新诊断。HTTP 200 后的流异常继续走单次结算，已开始的流保留已发送的状态，开始下游流后不再重试。每次渠道尝试重建证据，SDK 最多保留四份成功 HTTP 交换的响应。Realtime 对最近 128 个 response ID 去重并累计已完成轮次；协议内 cancelled 以及明确为 max_output_tokens/content_filter 的 incomplete 按正常轮次处理，真正断连/连接取消时的未完成轮次按异常矩阵选择用量。逐轮仅补充同一个 BillingSession 的累计预留，最终结算一次，替代旧逐轮直接扣款。长连接禁用一次性请求的信任免预扣旁路，预留失败结束连接。

## 计费合同

| 结束原因 | 确认用量 | 有效交付 | 收费 |
|---|---|---|---|
| 用户断开 | 有 | 任意 | 上游已确认部分 |
| 用户断开 | 无 | 任意 | 已收到业务响应则本地估算；零响应退预扣 |
| 上游异常 | 有 | 有 | 上游已确认部分 |
| 上游异常 | 有 | 无 | 0，退预扣 |
| 上游异常 | 无 | 有 | 输入及成功写出内容的估算 |
| 上游异常 | 无 | 无 | 0，退预扣 |

缺失与显式零分开，累计报告不相加。估算不冒充确认用量，不估造缓存。正常完成保留原协议计价，异常无收费来源覆盖按次、工具等附加费用。资金/令牌部分提交不自动重做运算，订阅快照在结算后生成。文本、音频、图片保留既有价格表达式和 token 归一化，不改变 pricing 合同。有效内容是成功写出/刷新的非空正文、思考、完整工具调用或可用媒体；ping、signature、usage 和终止标记不计入。

用量语义从原始事件识别，透传未执行请求转换时也能识别 Claude 缓存口径、Gemini 思考 token。图片完成事件的张数保留为完成证据，仅按张计价时可独立构成确认计费单位；token 计价只有张数时按无确认用量处理，不把 base64 字节当作 token，显式零 token 仍属于确认用量。多张请求单张完成不当作整体完成；异常按次计价使用已完成张数，只有预览且无完成证据时按一张估算。裸媒体成功字节用于判断有效交付，不把压缩字节冒充输出 token；缺少可验证的输出音频计量时仅采用已知输入估算。SDK/渠道正常分支已有的 BillingUsage 继续优先交给原价格归一化逻辑。

相同响应同时携带多种用量别名时，按实际协议选择固定优先级：Chat 的 prompt/completion 优先，Responses/Claude 的 input/output 优先，显式零也保留；不依赖 map 遍历顺序。异常确认用量保留音频、图片、缓存写入及思考细分，OpenAI reasoning 已在输出总量内，区别于 Gemini 的额外思考量。

已交付文本采用尝试局部增量估算，跨刷新保留词类和未取整权重，不按每个分片独立取整；Realtime 每轮重置，估算结算分别合并输入、已交付文本和音频，不再用旧 local 总量覆盖。兼容缓存证据按当前渠道识别 DeepSeek prompt_cache_hit_tokens、Moonshot choices[].usage.cached_tokens、Moonshot/智谱 v4 usage.cached_tokens 与 prompt_cache_hit_tokens，以及 OpenAI/llama.cpp timings.cache_n，标准字段优先且保留零。详见 `stream-estimation-and-cache-evidence-fixes.md`。

原生 Claude 的独立状态机也使用同一增量估算器，移除 8 KiB 分批独立取整。Responses/Realtime 同一工具的参数完成和输出项完成事件按 item/call 身份只计量一次，原始帧仍全部转发；仅保留最近 128 个已交付工具，SDK 新成功响应及新轮次重置。纯缓存用量的新增收费资格仅限已有 StreamResult 的受管终态，非流式、关闭开关、HTTP 非 200 与未收到响应维持原资格，非零普通用量的缓存折扣不变。详见 `stream-native-estimate-tool-dedup-and-billing-scope.md`。

## API、存储与权限

复用 logs.other 私有 `stream_diagnostic` 与公开 `stream_result` 结算摘要；`stream_diagnostic_attempt` 定位尝试，旧 Claude 字段仅读取兼容。RootAuth GET `/api/log/claude-diagnostic` 的 request_id/created_at/attempt 查询合同不变，仍返回 success/data，普通读接口继续剥离私有响应证据。2026-09-13 经确认改用公开 `stream_diagnostic_available`：只有新流式流程明确上游异常才为 true，Root 按钮仅依据此标记。`reject_reason` 恢复 bb6317462 的顶层字段和管理员直接展示，普通用户接口也保留此字段但前端隐藏。详情见 `stream-diagnostic-visibility.md`。

持久化上游原始响应头和 body；新流程上游异常时另保存单字段 `downstream_body_base64`，记录实际写出正文（含补发 error），具体见 `stream-diagnostic-body.md`。响应体最多前后各 1024 字节，头最多 16 KiB，SDK 重试最多四份。WS 保存握手响应头及按接收顺序拼接的原始消息载荷，不含 WS 网络帧头或请求消息。受管流式不投递请求日志；仅 Root 诊断保留有界下游正文，不缓存完整下游副本，原日志中间件在识别流式入口前临时读取的请求快照不会落盘；非流式日志采集不变。应用调试日志跳过受管请求，底层错误仅保留公开定位提示，原始原因进入 Root 诊断。采集开关仅控制响应头/body，错误与用量证据仍保留。无新表、列、索引或历史数据迁移；既有精确日志查询索引继续适用。

## 配置

新增 setting 注册的 stream_error_setting.enabled/capture_response，默认开启；请求内冻结。enabled 是流式处理总开关，Claude 原有 enabled/capture_response 同时作为该协议的兼容开关，不使已关闭的旧配置意外生效。关闭总开关回到旧转换/结算路径；已开始请求维持冻结配置。原生 Claude 严格处理与通用会话互斥，不重复运行。

## Main Chain Impact

同步：有界事件观察、计数、短写/刷新检查和既有一次终止结算。异步：既有日志/成本/佣金流水队列。共享 SSE/NDJSON 使用一个既有中转池读取者及单个转换/写入所有者，Realtime 使用两个池读取者及一个所有者；退出时关闭连接并等待工作者，避免归还 Gin 后继续访问。PaLM/讯飞不再使用旧的无界发送协程。无新增诊断后台 goroutine、文件日志、外部请求或独立资金操作；Realtime 的逐轮 Reserve 替换原逐轮扣款，不与原扣款叠加。诊断只旁路读取已读字节，不为日志额外读空上游；协议错误属于本次明确要求的终止策略。

## Shared Resource Audit

复用既有 users/tokens/channels/订阅资金资源，通过同一个 BillingSession 一次结算，不引入第二组余额锁或资金操作。复用日志队列和 HTTP/SDK 池，不创建新连接池。会话/采集器只在单个请求尝试内共享，短锁不跨网络读写，帧/工具缓冲有上限，禁止保存整段生成文本或请求诊断。配置使用 snapshot；无新 Redis 命名空间或共享可变缓存。

## Concurrency Analysis

30k/100k RPM 下协议/诊断增加的 DB/Redis 调用为零；一次终止复用既有资金操作。Realtime 保留逐轮额度保护的资金访问，改为同一会话累计预留，最终结算一次，需按响应轮次单独核算。100k RPM 约 1667 请求/秒，诊断常规上限约 18 KiB/响应，四份 SDK 响应约 72 KiB；事件与写出批次各限 8 MiB，所有未完成工具参数/名称共享 8 MiB，候选/工具/近期 WS 去重各限 128 项。观察器不保存整段生成正文，原渠道正常计量仍沿用自己的内容累计器。按活跃长连接数而非 RPM 单独核算内存。部署前应压测错误风暴、慢客户端和队列饱和，单元测试不代替容量验证。

## 测试与实施顺序

先写公共协议、写入确认、六行计费、开关及非流式隔离测试，再实现会话及逐入口接入。覆盖共享 scanner、独立 scanner、SDK、WS、转换和透传；包括尾帧、短写、刷新失败、显式零、缓存、重复终止、重试隔离、原错误透传、诊断截断/权限和订阅退款。仅本地 fixture，不请求真实 AI。运行相关 go test、根 build；如涉及 relaykit 源码，另执行 GOWORK=off go build ./...。全部新增/修改业务方法、字段带中文功能和参数注释。

## 实施状态

代码已实现并完成本地复核，覆盖入口、共享/独立扫描、SDK、WS、转换和请求体透传；未部署、未提交 Git。

### 接入与复核清单

- controller/relay：每次尝试独立会话，已开始流不重试，最后失败与原退款 defer 互斥；非流式无会话。上游退化为完整 JSON 时，终止日志仍保留入口的流式标记。
- 共享 HTTP 客户端、Coze 自有客户端、AWS SDK：原始响应先采集后解析，保留显式零与已确认部分。
- 共享 SSE/NDJSON、旧智谱、Cohere、PaLM、讯飞：正常结束按真实协议确认；原生 Claude 保留原严格状态机。
- OpenAI 音频、火山 WS 音频：裸媒体原字节转发，异常停止转发，不插入 SSE；Realtime 多轮连接不会在第一轮正常结束时提前关闭。
- 转换写出：短写、刷新错误、写入 panic 不算有效交付；异常不补成功尾帧；SDK/同协议上游错误保留原载荷，跨协议使用下游错误形状。
- 原错误与内存保留复核：SDK/Realtime 在校验 usage 前保存原终止错误，非法用量不覆盖既有证据；WS 原载荷逐字节发送，SDK 多行 JSON 用固定缓冲添加 SSE 外壳。Realtime 身份只保存定长摘要，未完成工具名独立复制，详见 `stream-retention-and-native-error-preservation.md`。
- 协议边界复核：缺失 Content-Type 的排版 JSON 不误按 NDJSON 解析；单行 done:false 仍为不完整流；原生供应商错误需匹配下游错误形状再透传，防止旧适配器未登记转换链而把 header.code 当成 OpenAI error。
- 图片与首轮复核：标准图片完整 JSON 恢复转 SSE 并登记实际图片数；DONE 不替代足量完成图，参数覆盖后的 n 同步完成约束。Realtime 首轮请求级可恢复错误后的正常空闲关闭不虚构轮次或追加 error，详见 `stream-image-and-first-round-fixes.md`。
- 计费：文本/音频/Realtime 共用一次结算；用户断开优先确认用量，否则按接收响应估算；上游异常仍要求有效交付。零响应退预扣；订阅退款复用结算后日志快照。
- 日志：私有响应与底层原因继续经现有 Root 接口；公开诊断资格标记按 `stream-diagnostic-visibility.md` 更名和收窄，未增加 API 或日志请求内容。

### 验证记录

- 已通过根模块 `go build ./...`，以及 relay 全包、common、logger、operation_setting、router、i18n 的测试，包括已有非流式回归。
- 新增本地夹具覆盖 OpenAI SSE、Responses 失败用量、AWS EventStream 原始采集、WS 两轮/失败轮、讯飞、旧智谱、Cohere、裸音频、HTTP 透传、混合换行、解析/读取失败、显式零、短写/刷新失败、重试隔离及多张图片结束判断。
- 追加复核夹具覆盖 PaLM 完整 JSON 转 SSE、WS 空连接关闭、用量别名冲突/多模态细分、缺失或错误响应头以及原生错误与下游协议匹配。
- service/middleware 标准 TestMain 在本地 MySQL `127.0.0.1:3306` 未启动时终止；临时 Go overlay 仅跳过环境初始化，运行 DB 无关的新增策略/日志隔离夹具和既有订阅结算回归，仓库 TestMain、数据库与余额均未改动。
- 数据库联调与 30k/100k RPM 压测尚待环境就绪；不把本地内存/WS 夹具视为全量供应商端到端认证。
