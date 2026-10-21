# 流式异常处理与诊断采集固定启用

状态：用户已确认实施，并授权一并修复缓存 Tokens 展示及流式拒答诊断。

## 目标与范围

移除通用和 Claude 的四个后端开关，采用当前四项均为 true 时的行为。现有前端没有这些设置的操作控件，固定启用后无需通过隐藏配置维护诊断功能。

移除的配置为 `stream_error_setting.enabled`、`stream_error_setting.capture_response`、`claude_stream_setting.enabled`、`claude_stream_setting.capture_response`。

移除开关并保留入口流式判定、真实 HTTP 200 / WebSocket 101 门控、按尝试隔离、原有结算规则和 Root 查询权限。正常结束但带有明确上游拒绝标记的流式请求也取得诊断资格，保存既有有界响应和用量证据；不把拒答改判为传输失败，不改变计费。历史费用核对不在本次范围。

本方案按本次用户明确提出的去除开关要求，替代 AGENTS.md Rule 0 对本功能保留运行时开关的通常要求。上线后不能再通过这四个设置关闭处理或采集，需要回退代码版本；不另设环境变量或隐藏替代开关。

## 当前实现与修改位置

- `setting/operation_setting/stream_error_setting.go`、`claude_stream_setting.go`：删除配置结构、注册和 getter。
- `relay/common/relay_info.go`：移除四个冻结配置字段，保留 `clientStreamMode` 的入口模式冻结。`UseStreamErrors` 和 `CaptureStreamResponse` 只取决于入口模式；严格 Claude 入口仍遵守原有协议与入口模式条件。
- `relay/claude_handler.go`、`relay/channel/claude/relay-claude.go`、`strict_stream.go`：清理仅用于配置分支的调用，保留通用会话与专用 Claude 解析器互斥、采集器复用和重试隔离。
- `service/stream_diagnostic_test.go`、`relay/common/stream_setting_test.go`、Claude 和 MiniMax 相关测试：将开关矩阵替换为固定启用与入口隔离的回归用例，不删除仍适用的正常/异常、计费和采集边界覆盖。

特别注意：`CaptureClaudeResponse` 还用于原生 Claude 的既有非流式采集路径。不能简单将其替换为 `IsStream`，否则会意外关闭默认已启用的旧路径；也不能将全部流程方法直接替换为 true，使非流式请求被纳入新流式状态机。实现以四项均开启的现状为对照。

## 数据流

请求进入 → 冻结客户端流式模式 → 对符合入口条件的请求准备独立会话与有界响应采集 → 真实成功响应激活协议观察 → 转发及原有终止/结算 → 依据既有上游异常资格过滤原始响应 → 通过既有日志队列异步持久化 → Root 按需读取诊断。

去除开关不会把所有响应存入日志。正常完成且没有拒绝标记、仅下游取消及本地错误继续遵守现有资格与过滤规则；已收到上游拒绝标记时保留诊断。响应头前失败和非成功 HTTP 响应沿用成功响应门控设计。

## API contracts

不新增 API。诊断继续使用 `GET /api/log/claude-diagnostic?request_id=...&created_at=...&attempt=...`，要求 RootAuth，沿用 `success/data` 响应和既有错误格式、`Cache-Control: no-store`。

普通日志查询继续剥离私有诊断内容，前端仍凭 `stream_diagnostic_available === true` 和 Root 身份展示入口。设置 API 中不再注册这两个设置模块；历史配置不能改变处理行为。实现时验证配置管理器加载未知旧键不报错，不为兼容旧键改动通用配置管理机制。

## 数据模型、配置与迁移

无新表、列、索引或数据库迁移。继续使用 `logs.other` 和既有 request_id / created_at 查询条件。

停止注册和读取四个旧配置。数据库中已有的旧键保留为无效历史数据，不执行删除或全表更新；无需访问线上数据库即可实施。若设置通用接口仍投影历史 options，不能误称这些旧键仍可控制功能，交付时说明其已失效。

不新增用户配置、环境配置或前端控件。复用现有诊断按钮与翻译文案。

## 本次追加范围

- 列表和详情的 Tokens 显示条件增加已有 `hasAnyCacheTokens` 判断。普通输入和输出为零、缓存读取或任一写入字段非零时，显示 0 / 0 和实际缓存用量。全零保持既有空态。
- 通用及 Claude 专用流式终止器将已确认的上游拒绝标记纳入诊断资格；错误日志入口采用同样条件，但仍要求已激活成功响应门控，旧非流式路径不扩展。现有 Claude refusal、OpenAI content_filter、Gemini block 等标记均复用，不解析回复文本来猜测拒答。
- 保留 `Failed`、终止原因与计费用量语义，只扩大诊断留存资格。复用有界响应中的 `stop_details` 等原始字段和结构化 usage evidence，权限仍为 Root，历史没有采集的数据不补造。
- 前端不新增测试框架，复用现有 SSR 检查脚本模式验证仅缓存读取、5m/1h 写入、普通用量及全零状态。
- 新增诊断内容量与拒答率成正比，持久化沿用原异步日志队列且单条限额不增加；100k RPM 全拒答时最多约 1667 条/秒的既有日志携带有界诊断证据。

## 关键逻辑与边界

1. 入口流式状态首次确定后，在渠道重试和上游响应格式变化时保持一致。非流式请求不能因上游 SSE Content-Type 被提升为新流程。
2. 实际 HTTP 200 / WS 101 的门控不变；诊断开关移除不能替代这个条件。
3. 通用流程和原生 Claude 专用解析器保持单一终止、单次结算、单份响应采集，不叠加 wrapper。
4. 渠道重试重新创建尝试状态；SDK 响应历史仍有上限，前次失败不污染后续正常结果。
5. 响应头最多 16 KiB，单次上游 body 保留首尾各 1 KiB，下游正文超过 2 KiB 保留首尾，SDK 最多四份响应；不额外读取或排空上游 body。
6. 历史未保存的正文无法补回；原来的诊断资格标记不回填，不重算历史账单。

## 错误处理策略

复用现有诊断采集、异常分类、日志和 Root 查询错误处理。诊断不增加外部调用，不向客户暴露私有错误或凭证，不将采集失败升级为中转错误。实现审查保持原有采集异常隔离及有界资源处理，不改动响应协议或新增业务错误文案。

## 与既有子系统的交互

计费、预扣、退款、订阅、工具附加费以及缓存计费均复用现有开启态路径。默认配置原本为全开，因此未改过配置的实例应行为等价；曾持久化 false 的实例将切换到现有开启态异常处理及计费规则，这一点需在发布说明中明确。

请求日志隔离、原始响应访问权限、日志保留周期不变。完成实现后同步更新 `claude-stream-error-handling.md`、`unified-stream-error-handling.md`、`stream-diagnostic-visibility.md`、`stream-success-response-gate.md`、`stream-diagnostic-body.md` 中仍描述可配置开关的当前合同，历史验证记录保留其时间语境。

## Main Chain Impact

同步：入口模式判定、已有有界字节复制、协议观察和终止快照继续在原执行位置运行；删除配置快照读取和相关分支，不新增同步 DB、Redis、网络调用或等待。已有采集器的请求局部短锁不跨网络读写持有。

异步：诊断持久化继续进入既有日志队列，沿用后台工作者；不为诊断增加每请求 goroutine、worker 或独立写库。

本次取消运行时关闭能力属于用户要求的明确范围。保持主链正确性与容量限制，实施验证以现有全开启行为为基线。

## Shared Resource Audit

| 资源 | 主链是否使用 | 本次影响 |
| --- | --- | --- |
| Redis | 主链使用，但诊断本身不新增 Redis 访问 | 无新 key、命名空间或连接 |
| options 与配置快照 | 主链原先读取这四项的内存快照 | 删除读取及注册，不在请求中查表，不清理旧行 |
| logs.other 与日志队列 | 主链通过现有队列记录日志 | 复用现有异步队列；诊断随原消费日志写入，不增加每请求写库次数，拒答记录内容增加 |
| users / tokens / channels 等表 | 计费及路由原有使用 | 本次不新增查询、写入或锁 |
| 请求局部会话与响应缓冲 | 主链协议观察使用 | 保持容量和锁范围，移除配置指针字段 |
| HTTP / SDK / WS 连接及连接池 | 主链使用 | 复用，不新增连接，不为诊断 drain body |
| goroutine / worker 池 | 原有读取及异步日志使用 | 不增加 worker，不新增诊断 goroutine |

未发现新增共享命名空间、表或池冲突。曾关闭采集的实例会恢复现有有界缓冲与异常日志量，不能按关闭态资源消耗评估上线容量。

## Concurrency Analysis

30k RPM 约 500 请求/秒，100k RPM 约 1667 请求/秒。相对当前全开启实现，每请求新增 DB 调用 0、Redis 调用 0、网络调用 0、goroutine 0；减少最多四个配置值的冻结与分支，不新增共享锁。

内存取决于同时在途请求数及已读响应规模，不能仅用 RPM 推算。每响应保持原有 16 KiB 头和 2 KiB body 限额，下游保留 2 KiB，SDK 响应历史最多四份，另有协议状态及元数据。异常风暴下仍使用既有日志队列容量与背压规则，需结合已有容量基线验证；不以单元测试替代 30k/100k RPM 压测结论。

## 测试设计与实施顺序

先更新测试用例，再修改实现。使用 testify 和本地 httptest fixture，不请求真实 AI 上游。

- 等价类与分支：普通流式、原生 Claude、透传、Realtime 固定启用；非流式保持原模式；上游转流不改变入口模式。
- 历史配置兼容：载入四个旧 false 值不影响新行为，未知旧键加载不报错，注册配置输出不再包含两个模块。
- 状态路径：成功/协议不完整/JSON 错误/上游 error/超时/仅下游取消/正常 refusal；断言诊断资格和结算结果沿用原开启态合同。
- 重试路径：同请求模式冻结、渠道尝试编号隔离、SDK 失败后成功、Claude 解析器不重复采集和结算。
- 边界值：继续执行 0/1/1024/2048/2049 字节采集测试，以及 header 和 SDK 历史上限测试。
- 权限：普通用户与管理员不能读取 Root 私有诊断；Root 可按请求、时间、尝试查询；普通日志仍剥离正文。

运行相关包 Go 测试，至少覆盖 `relay/common`、`relay/channel/claude`、`relay/channel/minimax`、`service` 及受影响的配置/日志/控制器包；执行根模块构建和 `git diff --check`。数据库测试按项目环境运行，缺少实际数据库时遵循已有 SQLite 回退并如实说明验证范围，不规避失败。若实施涉及 relaykit，则额外执行 `cd relaykit && GOWORK=off go build ./...`。

## 实施结果

- 已移除两个配置模块及四个请求内冻结配置字段；旧配置不再注册或读取，保留模式冻结和原有成功响应门控。
- 通用终止、Claude 专用终止及错误日志入口均将明确拒绝标记纳入诊断资格；保持失败分类、资金结算和 Root 权限。
- 两处 Tokens 显示条件复用 `hasAnyCacheTokens`；未增加 UI 文案或修改 locale 文件。
- 新增 Claude 原生/转换路径拒答夹具，覆盖缓存写入 1200、输出 0、诊断正文中的拒绝类别、下游原始正文和重试不继承拒绝；原有正常完成作为对照。
- 前端验证脚本 `web/scripts/verify-stream-tokens-render.tsx` 使用已有 React SSR/i18next，无新增测试框架或依赖，覆盖四种仅缓存用量、全零及普通用量，以及 Root/管理员/用户的诊断按钮资格。

### 验证结果

- 核心 relay/common、Claude、Gemini、MiniMax、setting/config、setting/operation_setting 测试与根模块构建通过；拒答诊断覆盖原生与协议转换路径，Gemini 用量与正常终止断言保持不变。
- `go test ./relay/...` 中其余渠道及 helper 测试通过；relay 包的 `TestProviderBodyGovernanceParity` 在整包运行时因已有全局资源计数残留 16832 而失败。用 HEAD 文件 overlay 恢复全部本次 Go 修改后复现相同失败，确认是既有问题；该测试独立运行通过。本次未改写无关资源计数或测试以掩盖失败。
- service 标准入口因缺少 SQL_DSN 在 TestMain 退出，尚未完成真实数据库集成验证。使用 Go 原生文件列表模式编译 service 全部生产文件与 stream_diagnostic、stream_log_info、stream_response_gate 三份测试，这些无 DB 单元回归通过。没有更改测试入口或伪造数据库环境。
- model/controller 全部生产文件与各自 stream_diagnostic 测试同样用文件列表模式运行，使用测试自带的 SQLite 回退，诊断字段过滤、查询和 Root 权限测试通过；这不代表 MySQL/PostgreSQL 集成验证。
- 前端 typecheck、变更文件定向 oxlint、Tokens 与 Root 权限的实际 SSR 检查通过。全量 lint 被未修改的 price-monitor-rule-scenarios.ts 中 12 个既有 curly 错误阻断，另有 footer 的既有 warning。
- 没有改动 relaykit、受保护前端认证文件、数据库内容或历史账单；没有部署。后续回归和 30k/100k RPM 组件压测记录见 [2026-10-21 验证报告](stream-diagnostics-validation-20261021.md)；组件结果不替代包含数据库、日志队列和长流的整链路容量验证。
