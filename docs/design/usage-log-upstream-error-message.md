# 使用日志保留上游明确错误

## 目标与确认范围

2026-09-18 用户确认：上游返回明确错误时，使用日志保留该消息；只有上游异常且缺少明确消息时才使用本地兜底。保留既有脱敏与当前请求 ID 规则。下游错误响应、状态码、重试、渠道禁用判定、计费、私有诊断权限与历史日志保持原行为。`<nil>` 错误类型修正另列范围。

## 当前问题与数据流

`ClaudeHelper` / `BeginStreamAttempt` 设置响应专用标记，`StreamPublicErrorSummary` 随后无条件替换错误文本。独立请求日志捕获的是控制器实际写出的响应，因而两处消息不同。

调整后：HTTP 错误解析 / 协议错误对象 / AWS SDK 明确 API 错误 → 请求局部错误对象保留明确消息 → 摘要出口选择该消息并脱敏 → 原 `ProcessChannelError` / 异步日志队列 → 使用日志。原始正文及头仍留在原有独立诊断路径。

## API contracts、数据模型与配置

无新增 HTTP 端点、权限、表、字段、索引、迁移或用户配置。`logs.content` 保存可读错误，现有日志查询与 Root 诊断接口合同不变。错误对象增加仅供日志使用的私有消息信息，既有错误序列化及对外 DTO 保持原样；relaykit 继续独立构建。

## 关键逻辑与边界

- 有非空明确消息时，即使 `code/type` 缺失也保留消息。
- 结构化 OpenAI / Claude 错误构造保留独立消息；本地 `NewOpenAIError` 包装默认没有上游消息，避免将连接错误或 body 预览当成上游消息。
- 通用 HTTP 解析保留 `error.message`、字符串 error、顶层 message 等既有受支持消息字段。空白、空 body、无消息对象、非法 JSON、纯 HTML 或只有数字/数组的 error 使用原兜底。
- AWS 仅提取 SDK 明确 API 错误的消息；网络/解码错误保持原兜底。
- 日志只取已确认的消息字段，排除附加的原始 body、metadata 和响应头；沿用脱敏、上游 request id 移除和当前 request id 附加。
- 明确隐藏错误的选项优先于日志保留；错误包装应保留来源。普通模式以及本地错误的既有处理保持原行为。
- 共用摘要出口的控制台日志和渠道禁用原因文本同步采用该选择规则；禁用条件仍读取原错误对象。

## Main Chain Impact

同步仅在错误构造时保留已有消息引用，以及响应专用错误日志出口选择/脱敏消息。正常响应路径没有新增操作。错误处理的返回、重试与结算控制流保持原样，因此本次为既有日志行为修正，不引入新的中转执行开关；沿用既有日志/响应专用模式开关。日志持久化仍由原异步批处理执行。

## Shared Resource Audit

只增加请求局部错误信息，无共享缓存或映射、Redis 命名空间、连接池、文件句柄、锁或工作池。沿用 logs 表与现有队列，记录次数及查询不变；users/tokens/channels 的读写路径保持原样。消息使用既有脱敏规则，不引入整段响应的额外复制。

## Concurrency Analysis

30k / 100k RPM 下，新增 DB、Redis、外部请求及 goroutine 数均为 0。错误消息保留为字符串引用；错误日志的脱敏成本随消息长度线性增长，与普通日志已有处理方式一致。具体消息比固定摘要更长，增加相应日志存储量，沿用现有队列容量及过载策略；本次不做生产压测。

## 实施步骤与验证

1. 先用本例与等价类/边界表测试复现明确错误被覆盖。
2. 增加错误来源消息，调整解析与摘要出口；覆盖 SDK 本地 HTTP fixture。
3. 验证日志落库的脱敏、request id、状态码，以及调用摘要前后原错误响应与重试标记一致。
4. 运行相关 Go 包测试、根模块构建、relaykit 的 `GOWORK=off go build ./...` 与独立测试。
5. 不访问真实 AI 服务；数据库测试使用现有隔离测试库入口，环境缺失时明确记录测试范围。

## 实施记录

### Coze / 原生 Claude 日志遗漏修复（2026-09-18 确认）

用户确认只修复本轮问题 1 和 2，问题 3（AWS SDK `UnknownError` 占位消息）保持现状。

- Coze：在流式错误日志的既有字段列表中补充 `last_error.msg`，沿用字符串校验、空白回退、脱敏和 request id 清理；不调整 HTTP 非 200 的旧错误解析。
- 原生 Claude：完整 DTO 解码失败时，仅为合法 JSON 且 `type=error` 的帧保留独立日志来源。既有 `upstreamError`、透传帧、终止分类、下游补发和用量选择保持原样；终止响应写出后再选择日志消息。
- 涉及 `service/log_info_generate.go`、`relay/channel/claude/strict_stream.go`，以及 service、Claude、Coze 的既有相邻测试。无 API、配置、数据模型、权限或翻译变更。
- 测试先行：覆盖 Coze 真实失败事件、明确/空白/非字符串消息与字段优先级；Claude 覆盖省略事件名的错误帧、无关字段类型冲突、非法 JSON、非错误事件、无输出/已输出两条路径。断言下游正文、HTTP 状态、异常分类、用量与私有诊断均沿用原处理。

#### Main Chain Impact

新增处理仅发生在既有失败分支：Coze 增加一个日志字段候选；Claude 借用当前失败帧的字节引用，不复制完整响应，不在成功帧循环增加 JSON 检查。日志提取继续在终止写出后执行，持久化沿用异步队列；继承既有受管流开关，不新增错误返回或计费分支。

#### Shared Resource Audit / Concurrency Analysis

只访问已有请求局部帧和日志结果，复用原 logs 表与日志队列；不新增 DB/Redis 调用、共享缓存、连接池、锁或 goroutine。30k / 100k RPM 下新增外部调用数为 0，JSON 校验仅在 DTO 解码失败时执行，成本随失败帧大小线性增长。风险是错误字段选择范围扩大，因此只接纳明确字符串字段，并用非法 JSON/非错误事件用例防止公开普通正文。非 200、非流式及 AWS SDK 占位逻辑保持现状。

#### 实现与验证结果

- 先补测试并复现两个遗漏：Coze 真实 `conversation.chat.failed` 帧及字段边界测试未取得明确消息；Claude 在 `usage`、`index`、`message` 字段冲突时仍输出通用日志提示。修改前，下游正文、状态、分类、诊断和用量断言已通过。
- Coze 新增 `last_error.msg` 字符串候选，保留已有候选的优先级。Claude 只在 DTO 错误分支借用合法 `type=error` 帧作为日志来源，与透传帧分离；既有通用终止响应及私有解码错误保持原样。
- 新增测试修复后通过；`go test ./relay/... -count=1` 全包通过。service 流式日志、HTTP 错误、违规归一化、日志落库及诊断专项使用已有 SQLite overlay 连续三次通过；请求日志/终止写出文件级测试通过。
- 根模块 `go build ./...`、相关包 `go vet`、`git diff --check`、UTF-8 无 BOM 检查通过；relaykit 的 types/dto 测试及 `GOWORK=off go build ./...` 通过。
- 标准 service 测试入口再次因本机 MySQL `127.0.0.1:3306` 未启动而退出；专项使用仓库外原 SQLite overlay。未执行 service 全包、真实 MySQL/PostgreSQL 集成、生产压测或部署；没有数据库结构或查询变更，没有真实 AI 调用。问题 3 按用户要求保持现状。

### 第二轮审计修复（2026-09-18 确认）

用户确认修复本轮三个遗漏，范围仍仅限日志消息选择，凭证脱敏扩展继续单列：

1. AWS HTTP 200 EventStream 的异常经 SDK 解码后从 `stream.Err()` 原样进入 `EndRead`，日志出口识别 `smithy.APIError.ErrorMessage()`；普通网络/解码错误仍无公开消息。已有 `NewAPIError` 包装的来源优先（包括显式隐藏后的空值），不穿透隐藏包装。SDK 错误对象不重建，保留私有原因中的原 request id、失败分类、终止响应、用量和结算路径。
2. 流式错误先沿用原消息解析；整体 DTO 因无关字段类型冲突解码失败时，在合法 JSON 中独立按原优先级读取字符串消息字段，兼容原生协议字段。数组、数字、对象和空白字段继续向后回退，非法 JSON 不提取，metadata 不作为消息。
3. 违规错误归一化的两个重建分支显式传递原 `UpstreamErrorMessage`（包括空值），避免把响应附加的 metadata 或本地兜底重新登记为上游消息。原响应、状态码、错误码与重试语义保持。

测试先行：真实 AWS SDK EventStream 异常帧覆盖明确/空消息、未知异常、传输失败以及已交付内容；流式 SSE/SDK 表测试覆盖类型冲突、字段优先级和非法 JSON；归一化覆盖两个分支的 metadata、空来源、字段回退以及原响应合同。

涉及 AWS 相邻 fixture、`service/log_info_generate.go`、`service/violation_fee.go` 及相邻现有测试；AWS 流读取入口保持原样，无 API、数据库、配置、翻译或 relaykit 公共 API 变更。

#### Main Chain Impact

同步操作仅在原有失败分支登记消息，以及失败终止后的日志字段选择；无成功事件循环内新增工作。沿用现有受管流门控与异步日志写入，不新增日志记录，不修改重试/扣费判定。

#### Shared Resource Audit / Concurrency Analysis

继续复用请求局部错误对象、logs 表及既有日志队列，不新增 Redis、DB 调用、锁、goroutine、连接池或共享可变结构。30k / 100k RPM 下新增外部调用和后台任务均为 0；额外 JSON 字段检查只发生在失败载荷，成本随该载荷长度增长。无 schema、查询或迁移变化；以本地 fixture 和隔离日志库验证，不执行生产压测。

#### 实现与验证结果

- 三个遗漏均先通过新增测试复现失败，再修复。SDK 消息选择放在日志出口，保持 `stream.Err()` 原对象及私有原因文本不变；真实 SDK fixture 验证错误帧原字节、用量、失败分类及下游终止响应不变。已有隐藏包装优先于 SDK 解包，避免重新公开隐藏消息。
- 流式提取保留原 DTO 成功路径及大小写兼容，增加合法 JSON 的独立字段回退（含 Tencent 原生字段）；覆盖无关字段的数组/对象/数字/布尔类型、非字符串消息向后回退、空白/request id 清理、优先级、非法 JSON 和原生协议消息。
- 违规归一化两个分支均传递原日志来源，测试覆盖 metadata、空来源、隐藏消息与独立回退字段；原 OpenAI/Claude 响应、HTTP 状态及 skip-retry 合同保持。
- `go test ./relay/channel/aws ./relay/channel/claude ./relay/common -count=1` 通过；新增 AWS 异常回归连续三次通过。service 日志、流式、违规归一化、结算及诊断专项使用既有隔离 SQLite overlay 连续三次通过。
- 根模块 `go build ./...`、`go vet ./service ./relay/channel/aws ./relay/channel/claude ./relay/common`、`git diff --check` 通过。relaykit 在 `GOWORK=off` 下全包测试及独立构建通过；格式与 UTF-8 无 BOM 检查通过。
- 本轮再次尝试标准 service 测试入口，本机 MySQL `127.0.0.1:3306` 仍未启动，专项使用仓库外原 SQLite overlay；未运行 service 全包、真实 MySQL/PostgreSQL 集成或生产压测，未部署。无真实 AI 调用、数据库结构或查询变更。

### 审计问题 2 / 3 修复（2026-09-18 确认）

用户指定仅修复流式日志出口遗漏和空白字段遮挡明确消息，问题 1 的脱敏规则扩展保持待处理。

历史核查以 9 月 10 日引入严格流式的 `ccb9e01b` 的父提交 `bb6317462`（本分支 8 月 29 日最后版本）为准：原 Claude 流解析器把明确 error 转为 `WithClaudeError`，返回控制器 `processChannelError`，由 `MaskSensitiveErrorWithStatusCode` 加当前 request id 写入错误日志；普通流状态还曾记录 `end_error/errors`。严格流式及之后统一结算改为处理器内部结束并直接写消费/错误日志，跳过该控制器错误出口。恢复消息可见性，而非回退旧的重试、退款、补成功结束帧或公开底层错误行为。

修复方案：

1. HTTP 日志消息独立选择：按既有字段优先级逐项移除 request id、裁剪空白，只有清理后非空才采用。保留 `TryToOpenAIError` / `ToMessage` 的下游响应及重试合同。
2. 通用流与原生 Claude 严格流在终止阶段，从已保留的原错误帧/SDK 载荷提取明确字符串字段；不从私有诊断或原始 body 预览反推。缺消息使用现有流式失败提示。结果保存仅供日志使用的消息，既有脱敏规则不扩展。
3. 在唯一结算后的共同日志出口追加消息与当前 request id，保留原计费说明，兼顾零收费错误日志与部分收费消费日志；正常结束和纯客户端断开保持原内容。
4. 先补 HTTP 字段回退、通用流/原生 Claude、落库及单次结算测试，再实现。检查原错误帧字节、费用、诊断权限和请求 ID 不变。

涉及 `service/error.go`、`service/log_info_generate.go`、`service/stream_lifecycle.go`、`service/consume_settlement.go`、`relay/common/stream_outcome.go`、`relay/channel/claude/strict_stream.go` 及相邻测试。

Main Chain Impact：仅失败终止时、原终止响应写出逻辑之后提取一次已收到的错误载荷并生成日志字符串；不修改响应、协议状态、重试、禁用、计费或开关。既有受管流开关仍决定是否进入此终止流程。日志仍通过原异步队列写入。

Shared Resource Audit / Concurrency Analysis：30k / 100k RPM 下新增 DB/Redis 调用、锁、goroutine 和连接均为 0；新增结果字段属于单请求，原 logs 表及队列的写入次数不变。提取和脱敏成本随失败消息长度增长，不在成功帧循环增加解析。没有 schema/索引/查询变动。风险为错误日志内容增长；沿用现有队列策略，历史日志不回填。

实现：`upstreamErrorMessage` 按旧字段次序逐个归一化，仅供日志；`StreamFailureLogMessage` 从实际错误帧/载荷或明确来源的 API 错误生成消息，兼容 MiniMax/Ali/Realtime 特有消息字段，排除纯正文、metadata 和传输原因。消息经原脱敏后保存在 `StreamOutcome.ErrorMessage`（`json:"-"`），不增加 `other` 或诊断公开字段。统一结算出口保留原 Content，并以分号追加错误及当前 request id，零收费/部分收费共享该出口；正常完成和客户端断开不追加。

验证补充：新增 HTTP 清理后逐字段回退回归在实现前复现失败；修复后 HTTP 响应、状态、重试保持原值。通用 SSE/SDK、原生 Claude、空消息/非法 JSON、本地异常/客户端断开、原帧透传、零收费/部分收费落库和单次结算回归通过。落库测试使用真实日志队列与隔离 SQLite，仅隔离无关的异步成本回调；未访问真实 AI 或共享业务库。

环境与遗留测试：标准 service 入口仍因本机 MySQL 3306 未启动而失败，使用上述仓库外 SQLite overlay 验证相关用例。relay 全包首次运行遇到既有 HTTP/2 GOAWAY 用例偶发 Windows 连接重置；以全部修改前的 HEAD 生产源码构造仓库外 overlay，单独运行该用例 30 次也出现相同失败，确认并非本次消息日志逻辑引入。本次未扩展修改该传输测试。

最终验证：`go test ./relay/... -count=1` 全包重跑通过；终止日志生成移到原响应写出之后后，再次运行 Claude/common 全包通过。service 专项（含 UnifiedStream、HTTP 字段回退、流式错误落库、既有日志/诊断/结算用例）连续三次通过。根模块 `go build ./...`、`go vet ./service ./relay/channel/claude ./relay/common`、`git diff --check` 通过；relaykit 的 `GOWORK=off go test ./...` 和独立构建通过。未进行真实 MySQL/PostgreSQL 集成、生产压测或部署。

- 已在 `relaykit/types/error.go` 保留日志专用 `upstreamMessage`，结构化协议错误记录原消息（追加 metadata 前），本地包装默认清空；显式 SDK/HTTP 来源通过选项登记。隐藏错误选项清空该消息，包装与追加当前 request id 保留原来源。
- `RelayErrorHandler` 保持原响应文本和状态码，只为日志记录明确字段；`error` 为数字、数组、布尔、null 或消息清理后为空时继续查找其他受支持字段。`showBodyWhenFail` 附加的 body 不进入该字段。
- `StreamPublicErrorSummary` 在响应专用模式优先输出脱敏消息；其他模式及缺少消息的原摘要保持。AWS 使用 SDK `APIError.ErrorMessage()` 登记消息，连接/超时等错误没有该来源。
- 未新增前端、数据库行为、配置或翻译文案；既有 `<nil>` 类型行为保持本次范围之外。

### 验证记录（2026-09-18）

- 修改前，service 回归复现了本例 `unknown_error` 覆盖明确 Bedrock 错误及同一问题的日志落库结果；随后完成修复与空值/消息来源回归。
- `go test ./relay/... -count=1` 通过，含 AWS 本地 HTTP fixture、原流式/计费测试；未调用真实 AI。
- `cd relaykit; GOWORK=off go test ./...` 与 `GOWORK=off go build ./...` 通过；最终再次运行 types 全包及独立构建通过。
- 根模块 `go build ./...`、`go vet ./service ./relay/channel/aws`、`git diff --check` 通过。
- service 专项回归连续三次通过：`Test(StreamPublicErrorSummary|ProcessChannelError|MessageWithCurrentRequestId|RelayErrorHandler|ResetStatusCode|ClaudeDiagnosticLegacyAndErrorLogPaths|StreamDiagnosticResponsePersistence|StreamResponseGate)`。
- service 标准测试入口连接本机 MySQL `127.0.0.1:3306` 失败（服务未启动）。专项验证使用仓库外临时 Go overlay，仅为 TestMain 增加独立 SQLite 内存库及原日志队列初始化；仓库测试入口保持原样，没有连接或改动共享业务库。运行参数为 `CODEX_USAGE_LOG_TEST_SQLITE=1 go test -overlay <临时 overlay.json> ./service -run '<上述专项表达式>' -count=3`。
- 未运行 service 全包及真实 MySQL/PostgreSQL 集成、生产压测、历史数据回填或部署；本次没有数据库查询/迁移变更。
