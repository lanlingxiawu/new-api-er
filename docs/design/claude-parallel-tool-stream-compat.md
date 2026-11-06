# Claude 上游并行工具块兼容（OpenAI Chat 下游）

状态：前期备选，不采用；2026-11-04。本文件保留此前讨论，下面的渠道白名单、配置和测试清单均不代表实际实现。用户在上游实验完成后确认按上游方案处理，实际范围、配置及验证以 [实施设计](claude-upstream-stream-compat-implementation.md) 为准。

实验及真正上游 main 对照已完成，结论以 [复现报告](claude-stream-incident-reproduction-main.md) 为准：本轮 fetch 的 QuantumNous/new-api `ba5f73677` 能正确转换交错工具，也能解析缺最后空行的完整 message_stop；真正缺结束事件时会上报健康失败，但不恢复缺失内容，也不向客户端补发错误帧。此前误用旧本地 main 的对照已被替代。本备选不作为未经确认的新转换方案继续实施。

## 问题与证据

本次请求从 `/v1/chat/completions` 进入，请求转换为 Claude Messages；响应方向为上游 Claude SSE → 本地协议观察 → Claude-to-Chat 转换 → OpenAI SSE。

上游事件为 `start(0, Bash_5) → delta(0) → start(1, Bash_6)`，第二次 start 到达时 index 0 尚未关闭。`relay/common/stream_session.go` 的 `len(s.claudeBlocks) > 0` 判断立即产生 `invalid content block sequence`，记录 `upstream_protocol_error`。`relay/helper/managed_stream.go` 在转换回调前检查该错误，因此本地尚未转换第二个工具就结束了请求。

这证明本地校验与该上游事件顺序存在兼容冲突，不能证明上游本来会完整结束。上游 HTTP 200 或已计费均不等于完整交付。上游是否最终发送全部 stop、最终 usage 和 message_stop，需通过完整夹具及上线后的实际结果验证。

补充的 Opus 样本只有 `message_start`、index 0 的 thinking start/delta；后续截图确认底层错误为 `upstream ended without a complete message_stop`。该错误来自 `relay/channel/claude/strict_stream.go` 的 EOF / 不完整尾帧分支，分类为 `upstream_incomplete`，与前例的并行工具序列冲突不同。诊断正文中的“已省略 N 字节”是展示截取，不是线上 SSE 字节或上游截断证据。

第二例说明本地读取到的流未完成，尚不能定位是供应商、中间代理还是尾帧格式导致。thinking 已成功写出可进入现有有效交付计费，因此“消耗”日志与响应最终失败可以同时成立。起始 usage 不是最终用量；本方案不能修复此类缺尾流，也不得补造 message_stop 来掩盖它。

相关代码：`relay/common/stream_session.go`、`relay/helper/managed_stream.go`、`relay/channel/claude/relay-claude.go`、`relaykit/relayconvert/internal/claude_messages/to_oai_chat_resp.go`。

## 目标与范围

允许指定渠道返回多个相互交错的客户端 `tool_use` 内容块，并正确转换为 OpenAI Chat 工具调用。仅覆盖客户端流式 Chat Completions、上游 Claude 事件、工具起始 `input: {}` 且参数从 `input_json_delta` 传输的情况。

保留串行响应、身份/索引检查、真实完成事件、取消与超时、既有结算和诊断权限。首期不扩展原生 Claude 下游、Gemini、Responses、非流式、服务端工具或 text/thinking 与工具的交错。原生 Claude 客户端可能依赖按块顺序完成，不能直接将这种扩展流透传给它们。

当前 `ClaudeToChatStreamState` 已按内容块索引映射独立、连续的 OpenAI 工具索引，并按索引转发参数分片；预期不改转换算法。工具 start 的非空 `input` 是现有转换器的另一处兼容边界，不纳入本次交错放行条件，也不顺带扩展。

## 数据流与处理顺序

1. 分配渠道后，每次尝试初始化时读取当前渠道 ID 和兼容配置，保存请求局部布尔值。使用 Gin 上下文的 `ContextKeyChannelId`；此时 `info.ChannelMeta` 可能仍属于上次重试。
2. 成功响应门控激活后，原始 SSE 帧先进入既有观察器。仅命中兼容条件的 Claude 工具流使用下述索引规则。
3. 合法帧立即交给现有 `ClaudeToChatStreamState`，按其映射输出 tool ID、名称和对应参数片段，沿用 Write/Flush 和成功交付统计。
4. 所有块关闭后，接受真实 message_delta/stop_reason 和 message_stop；正常流程继续使用既有最终 usage 和结算。
5. 真正异常仍走原终止、上游关闭、有界诊断及异步日志路径；不额外读取、排空或重放上游。

不等待整个工具参数生成，不增加事件排序缓冲，不以 JSON 看起来完整为由提前关闭块，不补造成功事件。

## 核心校验与边界

兼容路径使用请求局部索引状态，而不是删除全局校验条件：

- `message_start` 必须先到且不能重复；message_delta 已确认 stop_reason 后不得再开新块。
- start/delta/stop 的 index 必须是可安全转换的非负整数；拒绝小数、缺失、负数和溢出。兼容路径统一限定到 int32 非负范围，避免跨架构转换差异。
- 新块 index 严格递增，允许间隔。使用 last-started-index 标量拒绝重复 start 和关闭后重用索引，无需保留无界历史集合。delta 和 stop 可按任意顺序引用各自尚未关闭的块。
- 每个打开块保存是否具有交错资格：必须为 tool_use、有非空 ID 和名称、input 为语义上的空对象。只有新块和所有未关闭块都具备资格才允许重叠；text、thinking、server_tool_use 或非空 input 混入仍拒绝交错。非空 input 等原有串行情况继续原处理，不因白名单开启而新增拒绝。用类型枚举和局部计数判断，不在每个 start 遍历所有块。
- 对获得交错资格的工具，参数 delta 必须是 `input_json_delta`，且 partial_json 为字符串。JSON 片段不要求单片完整，不复制或新增缓存完整参数。
- delta/stop 必须匹配未关闭索引；拒绝未知索引、关闭后 delta、重复 stop。各工具参数只进入其固定下游工具索引。
- 单条消息总 tool_use 数量最多 128，而不只是同时打开数最多 128。转换器关闭块后仍保留工具映射，且下游有效交付跟踪在 finish_reason 时才清理工具；总数限制同时约束这两处状态。
- 保留 message_delta/message_stop 前所有打开块均已关闭、stop_reason 非空、真实 message_stop 到达的完成要求。EOF、usage、`[DONE]` 均不能替代 Claude 完成事件。

未命中渠道、其他下游和非流式继续走原有路径。对兼容路径新增的边界校验不扩展到所有供应商。

正确转换示例：

```text
上游：start(0,A), start(1,B), delta(1,b1), delta(0,a1),
      delta(1,b2), delta(0,a2), stop(1), stop(0),
      message_delta(stop_reason=tool_use, usage=...), message_stop
下游：工具 0 始终对应 A，参数 a1+a2；工具 1 始终对应 B，参数 b1+b2；
      沿用一次正常 finish_reason=tool_calls 及现有 usage/结束输出。
```

## API contracts 与配置

新增独立配置 `claude_stream_compat_setting.parallel_tool_channel_ids`，类型为整数数组，默认 `[]`。命中白名单即允许本方案的有限兼容；空数组关闭。首批建议仅配置 `[469]`，不将该 ID 写死到业务代码。

配置放在 `setting/operation_setting/`，通过 ConfigManager 注册，采用已有 `RegisterSnapshot` / `config.Snapshot` 发布不可变白名单索引。请求开始时通过 getter 原子读取一次快照并保存布尔结果；不保留可变草稿指针，不在每帧读配置或查数据库。配置变化对下一次尝试生效，进行中的尝试保持既定策略；重试重新读取当前渠道和配置。

复用现有接口，不添加端点或前端控件：

```http
PUT /api/option/
Content-Type: application/json

{"scope":"","key":"claude_stream_compat_setting.parallel_tool_channel_ids","value":"[469]"}
```

路由维持 AdminAuth 和 RequireSystemSettingsScope；空 scope 的全局设置写入仅 Root 可用。返回现有 success/message 结构。首期只支持现有单键更新入口，不假设 `/api/option/group` 会自动接受新模块。

写入前校验合法 JSON 数组、渠道 ID 为正整数且不超过 int32 范围、列表最多 1024 项；重复 ID 在发布时去重。不查询 channels 表验证存在性，删除渠道后的残留 ID 不影响其他渠道。错误使用现有参数错误翻译和控制器错误助手；底层原因记录现有内部日志。校验失败不得修改已生效快照或持久化配置。

历史 `stream_error_setting` 和 `claude_stream_setting` 已退役，本方案不恢复它们，也不关闭流式诊断。相关依据见 `stream-diagnostics-always-enabled.md`；本次新配置只控制兼容范围。

## 数据模型、存储与索引

没有新表、列、迁移或大表查询。只通过现有设置持久化流程写入 options 中的新配置键；请求处理不读写 options。现有日志和诊断存储、保留周期、访问权限保持原样。

不修改 DB 驱动、SQL、事务、表标签或索引，不需要新增索引。若实现过程中扩大到数据库行为变化，须追加 SQLite/MySQL/PostgreSQL 验证，不能用本方案的内存夹具替代。

## 错误、计费及既有子系统

复用现有协议错误、截断、超时、取消和本地转换错误分类；底层详细错误仅进入既有私有诊断，客户端继续收到既有安全错误文案。继续确保首个错误不被覆盖、错误终态最多一次、异常后没有伪造成功尾帧。

计费公式、usage 归一化、缓存用量、预扣及退款规则均不修改。修复使真正完整的工具流进入原成功结算；失败仍按既有有效交付和用量证据矩阵处理。上游已收费不作为本地成功或收费证据，不补造最终 usage，不追补历史费用。

OpenAI 工具分片成功写出并不等于完整工具交付；沿用最终 finish_reason 与完整合法参数对象的现有确认方式。部分输出、无有效交付、客户端取消分别保留现有规则。兼容策略不得触发重放或额外上游调用。

## Main Chain Impact

同步执行：每次尝试一次不可变配置快照读取和渠道 membership 判断；每帧在现有观察器中执行有界索引/类型检查；保留现有转换、写出和用量证据更新。新增逻辑属于必要协议解析，不引入观察性外部 I/O。

异步执行：诊断与日志继续复用既有队列。本功能不增加任务、goroutine、定时器或后台工作者。不增加网络等待、数据库/Redis 调用或跨帧等待。开关关闭的路径不得增加工具集合分配或参数缓存。

共享资源为配置快照、现有日志与资金设施；新增可变状态仅属于单次请求尝试，详细审计如下。

## Shared Resource Audit

| 资源 | 本次变化 | 与主链关系及隔离 |
| --- | --- | --- |
| Redis | 无新 key、读写或连接 | 无新增冲突 |
| options | 管理端写一个独立配置键 | 主链只读原子快照，无同步 DB 查询、锁或新增连接 |
| users/tokens/channels、账务表 | 无新访问 | 保留既有结算，不修改资金路径 |
| logs/诊断 | 沿用现有异步保存 | 不扩大采集字节数，不增加每帧日志 |
| 配置内存 | 发布时构建不可变白名单 map，最多 1024 项 | 请求只读；无跨请求可变 map，无热路径草稿锁 |
| 协议/转换状态 | 每尝试索引、类型、计数及最多 128 个工具映射 | 与其他请求隔离；复用现有短会话锁，锁内无 I/O |
| 参数缓存 | 不新增 | 原工具跟踪共享总预算，不改成每个工具独立预算 |
| DB/Redis/HTTP 连接池、goroutine 池、文件 | 无新增使用方式或容量需求 | 不挤占额外主链工作者或连接 |

当前代码的 `MaxStreamFrameBytes` 为 200 MiB，旧文档中的 8 MiB 已不代表当前实现。本方案不提高该限制，不预分配该大小，不额外保存整条流；新状态不持有指向完整 JSON 帧的 gjson 字符串切片。200 MiB 上限不是高并发内存预算保证，压测需分别观察真实参数大小和极限输入下的现有内存开销。

## Concurrency Analysis

30k RPM 约 500 次请求/秒；100k RPM 约 1667 次/秒。新增每请求 DB 调用 0、Redis 调用 0、网络调用 0、goroutine 0。每次尝试配置判断为平均 O(1)；新增每帧索引检查为平均 O(1)，不逐帧扫描白名单、全部工具或已拼接参数。

兼容路径额外内存为有界块状态，转换映射和现有工具跟踪受每消息 128 工具上限约束。必须按并发活跃请求数评估内存，而不是按 RPM 直接估算：平均流长为 T 秒时，100k RPM 约有 1667×T 个活跃流。基准记录每事件 ns/op、allocs/op、每请求附加内存，并比较开关关闭、串行和双/多工具交错场景；不把本机基准当成生产吞吐保证。

## 测试设计与实施顺序

确认方案后先写回归夹具并验证现状失败，再写实现。使用 testify；上游使用内存 SSE / httptest，不调用真实 AI 服务。

| 用例 | 预期 |
| --- | --- |
| 用户样本前缀，追加两个 stop、最终 usage 和 message_stop | 命中白名单后输出两个独立工具并正常完成；原行为能复现协议错误 |
| 两工具真正交错参数、逆序关闭 | 按下游工具索引重组后 ID、名称和参数逐个正确，无串参或重复 |
| 前置已关闭 text/thinking、工具内容索引有间隔 | 下游工具索引仍连续 0、1 |
| 上游发送当前完整参数片段后暂停 | 下游立即收到该片段，不等待下一帧或完整消息；用通道握手而非短 sleep |
| 白名单为空、未命中、原生 Claude、其他下游 | 保持各自原行为；不扩展兼容范围 |
| text/thinking/server_tool_use 混入未关闭工具、非空 start input | 不因兼容标记而放行交错 |
| 负数、小数、缺失、溢出、重复及关闭后重用索引 | 兼容路径拒绝，不能把第二个工具并入旧工具 |
| 未开始/已关闭块的 delta、重复 stop、错误工具 delta 类型 | 拒绝并只发一次错误 |
| 未关闭块时 message_delta/stop，EOF、[DONE] 或 usage 代替结束 | 继续报真实协议/截断错误，不补成功 |
| 仅 thinking 增量的完整流与提前 EOF | 完整流正常，EOF 缺结束仍报错；诊断展示省略不影响协议输入 |
| 1、2、127、128、129 个 tool_use | 128 及以内正确；129 超限失败且状态有界 |
| 渠道重试 469→其他渠道；处理中清空配置 | 重试不继承前次渠道策略；更新只影响新尝试 |
| usage 在起始/结束出现，含显式零与缓存字段 | 不重复累计，最终用量沿用现有选择，成功/失败结算各一次 |
| 下游写失败、取消、真实上游 error/超时 | 原终止优先级、交付和结算语义不变 |
| 配置空值、非法 JSON、非正整数、1024/1025 项、热更新 | 非法值不持久化、不改变快照；并发读取无数据竞争 |

重点在现有 `relay/common`、Claude 渠道转换测试和 relaykit 的 Claude-to-Chat 测试扩展；配置验证与重试策略在其现有测试文件中补充。生产修改预计集中于新 setting、现有选项校验入口、BeginStreamAttempt 和 StreamSession。原生 strict_stream 与 OpenAI-to-Claude 响应转换器不属于此次修复点。

验证命令按最终修改包调整，至少包括相关 relay/common、relay/helper、relay/channel/claude、setting 与 service 选择性测试，根模块构建；relaykit 测试和 `GOWORK=off go build ./...` 必须在 relaykit 目录独立执行。支持 race 的环境运行配置热更新和并发流测试。涉及 DB 的测试只运行必要已有夹具并遵守隔离规则，不操作真实用户余额；不可运行项需在交付中明确列出。

## 发布、回滚与验收

默认空白名单发布；确认所有回归后由 Root 将目标环境配置为 `[469]`。观察真实请求的双工具参数、完整结束、最终用量、协议错误率、首片延迟和内存分配。只有完整结束证据才能证明此次样本已修复，不能用上游收费替代验收。

回滚将白名单改为 `[]`，按既有配置同步周期使各实例的新尝试恢复原策略；不承诺立刻改变所有实例，需核对配置已同步。进行中的尝试继续原策略，不在流中途切换状态机。正常校验及诊断始终启用。

若真实上游随后确实缺少 stop/message_stop，本方案仍应报错，应另外追查上游或超时；不继续放宽终态要求。原生 Claude 下游若也需要支持此类事件，需单独设计有界串行化与延迟语义。

实施后更新本文的实际改动、验证结果、性能数据及残留限制。当前仅完成代码核对和测试设计，未执行修复测试或修改线上配置。
