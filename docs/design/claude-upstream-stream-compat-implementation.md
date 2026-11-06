# Claude 流处理对齐上游 main

日期：2026-11-04。用户已在上游对照报告后确认“先按上游的方案进行处理”。本文落实已确认的两个兼容点；此前渠道白名单备选不采用。依据为 QuantumNous/new-api `ba5f7367797566175a6cd97a748d430af3602a01`，实验见 [对照报告](claude-stream-incident-reproduction-main.md)。

## 目标与范围

1. Claude 上游转换为 OpenAI Chat 时，允许客户端 tool_use 内容块交错，保留现有按索引转换器。
2. 原生 Claude 流在真实 EOF 前已经收到完整 message_stop JSON、仅缺最后 SSE 空行时，接受这个真实终态。
3. 真正缺少结束事件、读取错误、取消和超时继续失败，原生 Claude 的工具 JSON 校验保持不变。不移植整份上游扫描器，不补造成功事件，不增加重试；原生 Claude 不扩展交错内容块支持。

上游使用按行扫描兼容末尾；本地已有原始帧透传和多行 data 支持，因此只借用 EOF 尾行兼容语义，保留现有分帧实现。严格协议校验不会整体关闭。

## 数据流与业务边界

通用路径：创建尝试会话 → 读取一次兼容配置快照 → 既有观察器按内容块 index 跟踪 → 既有 ClaudeToChatStreamState 输出工具分片 → 原完成和结算流程。

仅 OpenAI Chat 下游启用工具交错。块状态沿用现有 map，并在首个块打开时保存 claudeToolsOnly 布尔值。后续只有同为 tool_use 的块才能重叠，因而无需遍历或额外计数即可 O(1) 判断全部打开块类型。活动块最多 128 个，沿用已有工具跟踪容量；不缓存或重排参数。兼容路径校验非负整数索引、递增且不重用的 start 索引（允许间隔），拒绝未知/已关闭索引的 delta/stop，消息终态前仍须所有块关闭。text、thinking、server_tool_use 混合重叠保持拒绝。

OpenAI 转换路径沿用既有参数转发和有效交付判断；该路径原本不会仅因累计工具参数 JSON 残缺而将整个流判失败，本轮未增加这项校验。工具序列校验与参数语法完整性不能混为一谈。

原生路径：开始处理响应时读取一次配置 → 原扫描器继续处理完整帧 → 真实 EOF 时检查残留帧 → 仅完整合法的 message_stop JSON 可继续交给原协议状态机 → 已开始、全部块关闭、已收到 stop_reason 才允许成功。Scanner 的 atEOF 也可能来自非 EOF 读取错误，必须记录底层真实 io.EOF，不能将传输错误当成正常 EOF。

接受的末尾事件只补 SSE 分隔换行，使客户端能够派发实际收到的 message_stop；不新增 JSON 事件、不补 usage 或 stop_reason。普通短读继续等待；损坏 JSON、其他残留事件、非 EOF 读取错误不通过兼容。

## API contracts 与配置

新增 `setting/operation_setting` 模块 `claude_stream_compat_setting`，单个布尔配置 `enabled`，默认 true。本轮用户已授权采用上游兼容行为，默认启用；false 回退这两个兼容点，流式诊断和真实失败检查始终开启。

通过 ConfigManager.RegisterSnapshot 发布不可变快照；getter 为 ClaudeStreamUpstreamCompatibilityEnabled。会话/响应只取一次值，处理中不会因热更新改变协议策略。

复用既有 `PUT /api/option/` 管理接口，不新增接口或 UI。使用原 AdminAuth、RequireSystemSettingsScope 及全局设置的 Root 权限约束，返回原 success/message 格式：

```json
{"scope":"","key":"claude_stream_compat_setting.enabled","value":"false"}
```

沿用已有布尔配置解析和选项同步机制。回滚对配置同步后的新会话/响应生效，不中途取消已有流。本轮不修改线上配置。

## 数据模型、存储与子系统

无表、列、索引、迁移或 SQL 变化。新键通过既有 options 配置持久化机制管理；主链不查询 options。没有新大表访问模式，不需要新索引。

不修改计费公式、用量归一化或资金结算。完整流恢复到既有成功路径，残缺流继续原失败结算。诊断保存和公开错误文案复用原机制，无新用户文案或 i18n 键。

## 错误处理

保留首个错误、读取错误、协议错误、未完成、取消、超时和下游写失败的分类及错误帧去重。禁用开关恢复此前工具交错拒绝和 EOF 残帧拒绝。不能把上游健康统计 failure 与内容恢复混为一谈。

## Main Chain Impact

同步：每个会话/原生响应一次原子配置读取；原有锁内 O(1) 的块索引/类型检查；仅 EOF 残片进行一次 JSON 验证及最多两个换行的规范化。无外部 I/O、数据库或 Redis 调用。保留现有短锁，锁内不读写网络。

异步：继续使用已有扫描工作者和日志队列；无新增工作者、任务或 goroutine。不会等待后续工具或收齐整个响应再转发。

## Shared Resource Audit

| 资源 | 变化及与主链的关系 |
| --- | --- |
| Redis/DB 连接池 | 无新访问或连接 |
| options | 管理流程复用独立配置键；主链只读原子快照 |
| users/tokens/channels/账务表 | 无新访问，原结算不变 |
| 配置内存 | 单个不可变布尔快照，管理写入按既有机制发布；读取不取草稿锁 |
| 请求块状态 | 复用请求局部 map，最多 128 活动块；计数/索引为局部标量，不共享参数 |
| 扫描缓冲 | 保留现有 MaxStreamFrameBytes，不预分配上限、不复制整条流；末尾只处理一帧 |
| 日志/工作者/HTTP 池 | 不增加调用、容量需求、文件或新锁 |

## Concurrency Analysis

30k RPM 为约 500 次/秒，100k RPM 为约 1667 次/秒。新增每请求 DB/Redis/网络调用均为 0，goroutine 为 0。每次开始一次原子读，每个相关帧平均 O(1) map 检查，不扫描所有活动块。配置只在低频管理写入时分配快照。

EOF 尾帧检查为 O(末帧字节数)，仅缺分隔符的残帧执行；不增大现有 200 MiB 帧上限。内存按活跃请求而非 RPM 评估：流长 T 秒时约 1667×T 活跃请求，新增每请求为少量标量/读取状态；活动 map 上限不能替代整体生产内存压测。验证使用真实本地夹具和相关测试，不声称已验证生产 100k RPM。

## 测试与验收

先改复现断言并确认旧实现失败，再实现。覆盖：串行/交错/逆序关闭参数正确；开关关闭恢复旧行为；配置变更不影响已开始响应；非法/重复/未知/重用索引及混合块仍拒绝；128/129 活动块；未关闭块的终态仍拒绝。

EOF 覆盖：0/1/2 换行、CRLF、data+EOF 同次读、逐字节、真实 loopback HTTP；合法末尾只有一个 message_stop，输出能被 SSE 客户端分帧；非 EOF 读取错误、损坏 JSON、非 stop 残片、未关闭块、缺 stop_reason 和真正 thinking 截断仍失败。

运行相关 relay/common、relay/helper、relay/channel/claude、setting/operation_setting 包测试及根构建。支持时运行相关 race 测试。无 relaykit 生产或公开 API 变化，不搬动转换器。不调用真实 AI、账务数据库，不修改线上账单。

## 实施结果

已完成本地实现，未部署，未更改线上配置或历史账单。

- `relay/common/stream_session.go`：按尝试冻结开关，复用打开块 map、工具类型布尔值及最后开始索引；仅 OpenAI Chat 放行交错客户端工具，转换器未修改。
- `relay/channel/claude/strict_stream.go`：按响应冻结开关，读取器记录真实 EOF；只放行完整 message_stop 尾帧并补分隔换行，继续原协议校验。非 EOF 残帧保留原读取错误，原始诊断捕获不加入补齐换行。
- `setting/operation_setting/claude_stream_compat_setting.go`：注册默认开启的原子配置快照；既有配置导入/导出及关闭回退经测试验证。
- 复现实验改为修复后的回归断言；两个交错工具场景和两个末尾换行场景均先验证旧实现失败，再验证新实现通过。关闭开关恢复原拒绝行为。原修复前实验结论仍保存在对照报告。

已执行验证（Windows amd64 / Go 1.26.10）：

| 命令 | 结果 |
| --- | --- |
| `go test ./relay/common -count=1` | 通过，12.869s |
| `go test ./relay/channel/claude -count=1` | 通过，1.472s |
| `go test ./relay/helper -count=1` | 通过，6.020s |
| `go test ./setting/operation_setting -count=1` | 通过，0.315s |
| `go build ./...` | 通过 |
| `git diff --check`，修改 Go 文件 `gofmt -l` | 通过，无格式输出 |

独立审查检查了 EOF/读取错误区分、原始诊断字节、配置快照、块索引和活动状态上限，未发现新增阻断缺陷。race 未运行：本机 CGO_ENABLED=0，PATH 中没有 gcc/clang。没有修改 relaykit 或其公开 API；未开展生产压测、真实供应商调用或账务数据库集成验证。参数语法校验范围限制见上文，不能用本轮测试声称修复了渠道 460 的真实传输中断。
