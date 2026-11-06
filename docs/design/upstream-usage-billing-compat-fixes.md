# 上游用量兼容与异常流计费修复

日期：2026-11-04。基于实际 `upstream/main` 的 `ba5f7367797566175a6cd97a748d430af3602a01`；不是本地 main。用户已明确要求对审计发现进行修复并测试，本设计记录本次获准实施的范围。

## Goals and scope

### 2026-11-06 Responses 自定义工具修复

已先合并真实上游 main `2de51a19e`，合并提交 `5f28e1404`。本次修复已有 Responses 功能的两个兼容缺陷：缺少终态 usage 时遗漏 custom_tool_call_input.delta 的输出估算，以及已完成 custom_tool_call 未被交付观察器识别。普通 function_call 继续要求 JSON 对象；custom_tool_call 的 input 是原始字符串，允许代码、补丁及空字符串，不能套用 JSON 校验。只补齐计量和观察，不改变客户端取消、超时、退款策略或历史账单，不猜测缓存命中量、不修改模型定价。

数据流：现有 SSE 解析 -> 自定义工具 delta 加入原输出估算；接收侧逐片估算 -> 完整写出并刷新 input.done/output_item.done 后确认有效交付 -> 原用量选择与动态表达式。两类完成事件按既有 item_id/call_id 有界身份表去重；未完成工具只计接收估算，不计有效交付。确认的最终 usage 优先，缓存值仍只来自真实 usage。

API、鉴权、配置、数据表、迁移、索引均不新增。用户明确要求这是缺陷修复，不增加回退开关；修复直接生效。

Main Chain Impact：同步只扩展既有事件分支、请求局部字符串和有界工具去重；无新网络/数据库/Redis 调用，无新 goroutine、等待或跨请求锁。接收与交付沿用原会话锁，锁不跨 I/O；普通文本和普通工具不增加正文解析次数。Shared Resource Audit：复用请求局部会话及原结算路径；不增加 relay 共享键、资金表访问、连接或 worker 池。Concurrency Analysis：30k/100k RPM 下每请求额外 DB/Redis/网络/协程均为 0；工具状态沿用最多 128 个、共享 MaxStreamFrameBytes 预算，完成即释放；没有生产吞吐实测，不能据静态分析宣称通过 100k RPM。

测试先行：将双分支事件夹具改成自定义输出必须大于 0、完整自定义调用交付为 true；覆盖原始非 JSON 输入、空输入、未完成调用、重复完成事件、缺名/非法类型、最终 usage/缓存不变。所有异常沿用现有错误处理；不新增对外文案或错误响应。

合并适配：上游定价测试的 940/950 固定 ID 已映射到本分支保留测试区间，避免重复执行残留和共享开发库冲突；新增前端 PriceChangesFixture 补齐本分支 SettingsPageProvider 的 scope/canEdit。共享资源审计发现本分支额外的 profile 版本键也参与用户缓存 Lua，必须与上游新 fence/version 键一样使用 `{getUserCacheKey(id)}` 哈希标签；已先用同槽断言复现失败再修复。没有新增 Redis 操作、跨请求锁或权限判断；旧版本键只按既有 TTL 自然过期。鉴权缓存回归检查缓存签名、版本、权限及会话归属，范围为合并适配，不构成完整安全审计；依据 [OWASP ASVS 5.0](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 和 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 的服务端可信校验要求。

本轮验证：`go build ./...`、`cd relaykit; GOWORK=off go build ./...`；`go test ./relay/common ./relay/helper ./relay/channel/openai -count=1 -timeout=180s` 全包通过，自定义工具事件回归另重复 20 次通过。真实隔离 MySQL 8.0.46 / PostgreSQL 17.11：鉴权缓存矩阵和上游两项定价测试各启动两次通过；SQLite 3.50.4：鉴权缓存矩阵及相同定价测试（只读 Go overlay 注入 SQLite 夹具）通过；service ResponsesUsageAccumulator 回归使用独立 MySQL 主库及 PG 日志 schema 通过。本轮没有 schema/model tag/migration 修改，没有进行生产 schema 升级演练或 DDL 审计。

前端：4 文件 42 项 Vitest 通过；lint 无 error、仅原有 warning；生产构建通过；类型检查以 `bun run tsgo -b` 通过。本机 Bun 1.3.13 不支持 package.json 中的 `bun check -b`，原 `bun run typecheck` 命令失败原因是工具版本，不修改项目脚本规避；使用已安装 TypeScript 原生编译器执行等价检查。五个受保护鉴权基础文件与真实上游完全一致；无冲突标记或 locale BOM。完整项目全测试、真实供应商、真实断开来源和生产负载未验证，不宣称已解决 16 秒取消或缓存缺失后的定价策略。

修复已经在当前分支和上游 main 分别复现的八项差异：xAI 缓存与推理详情、Gemini 三种 embedding 路由的上游用量、图片正常完成的实际张数、图片中断不能降低请求张数、Claude fine-grained 工具参数被 max_tokens 截断、Claude 原始 BillingUsage 快照、Gemini 异常流的工具输入/模态计数、OpenAI 异常流的缓存创建/TTL/模态字段。

后续按用户要求增加厂商协议故障交叉实验：真实本地 HTTP 分块/截断/阻塞、SDK eventstream、正常文本/思考/工具/图片对照、异常 JSON/结构/序列、确认用量与缺失/显式零、交付状态、兼容开关及 timeout refund/input/charge 的组合。仅使用本地构造数据，不调用供应商、不写业务库。测试断言正文和终态、确认用量、结算策略及重复终止幂等，不能只断言没有 panic。

实验已复现 Responses 在缺少真实协议终态时被 `[DONE]` 判成功；实际 upstream/main 的 `OaiResponsesStreamHandler` 使用 `RequireTerminal()` 并在真实 completed/done 响应中标记结束。当前通用会话补上 Responses 协议识别与 DONE 防护，沿用兼容开关并在新交换时清除状态，不补造成功事件。另复现 Gemini 正常流的显式零输入被本地估算替换，修复须保留字段缺失的既有 fallback。

Responses 完成事件还须包含对象型 response 和非空字符串 status；Realtime 的 response.done 使用其独立规则。Gemini 在客户端流的原生/转换入口按原始字段存在性保留 promptTokenCount，包括后续显式零覆盖和缺省不清空；上游 main 也未区分此边界，不改非流入口的旧 fallback。

交叉实验进一步确认，已识别 Responses 方言也须隔离 done/is_end/choices/candidates/promptFeedback 等其他协议的结束字段，避免先误判 complete 再绕过 DONE 防护。协议识别根据真实上游事件，不根据下游目标格式，因此保留 Chat 转 Responses 的合法结束方式。

AWS SDK 事件流实验另外复现：通用 Claude 路径接受内容块与 delta 类型冲突、非法工具 JSON、冲突 stop_reason，以及用其他协议的 done 字段替代 message_stop。上游 main 同样没有完整校验。沿用兼容开关，在已识别 Claude 后限定终态为 Claude 自身协议；记录块类型与严格递增整数索引、拒绝已知类型冲突和终止原因冲突；工具参数在 block_stop 校验对象，客户端工具截断仅允许随后真实 max_tokens/message_stop。保留已有 OpenAI 并行工具兼容与最多 128 个活动块。工具 JSON 按需累积，全部未关闭工具共享既有 200 MiB 上限，关闭即释放；每块只做最终校验，不引入额外 I/O、goroutine 或共享资源。SDK 在正常传输 EOF 后才发现包截断的细分错误归因另行记录，不改变失败与收费判定。

同时修复异常流已经收到确认 token 用量，但因尚未交付有效内容而全部退款的问题。计费资格和有效内容是两个独立判断：空 thinking 块不是有效交付，但真实 message_start 中的确认输入、缓存和输出计数仍是用量证据。

不补造 message_stop，不把真实断流改成成功，不追补历史账单，不改用户指定的超时 refund/input/charge 策略。无交付的新收费分支只使用确认字段；缺字段不补估算，不把初始输出计数误称最终计数。按次/固定请求价格在没有交付的失败场景继续退款，避免用局部 token 证据收取整次费用。

## Data flow and API contracts

客户端请求 → 每次尝试冻结兼容设置 → 原适配器/受管流采集与验证上游用量 → 协议终态与交付状态独立判断 → 选择确认用量/原有估算策略 → 既有计价与单次结算 → 既有日志。

不增加 API 或认证方式。新增配置复用现有管理员设置 API；诊断仍仅超级管理员可见。真实流错误仍通过既有 relay 错误帧返回。客户端工具调用若因 max_tokens 收到不完整 JSON，只允许真实 max_tokens/message_stop 结束，不将该工具算成可执行的有效工具调用。

## Configuration and data model

新增 `upstream_usage_compat_setting.enabled`，默认 true，以 ConfigManager 原子快照发布，每次尝试冻结；false 回退本次修复的计量选择/协议兼容。既有 `claude_stream_compat_setting.enabled` 保持独立。新增 DTO metadata 对齐上游，关闭开关回退用量选择；错误类型的 metadata 仍按上游 DTO 解码规则拒绝。

无新表、迁移、索引或日志列。用量证据继续存于现有诊断 JSON。增加请求内 ConfirmedOnly 标志禁止此分支的估算输出、缺失输入补齐及模型名推断的工具附加费；上游明确报告的工具调用仍按现有规则处理。

强制搜索等请求推断的工具计数增加请求内来源标记：无交付确认用量分支扣除推断次数，真实协议调用或上游计数字段仍可计费；上游显式覆盖（包括零）清除推断标记。普通请求维持原规则。

## Business logic and edge cases

- 正常用量仍由适配器确定；异常用量从经验证的字段重建，保持来源协议的 token 语义、显式零和字段缺失的区别。
- Gemini 输入包括 toolUsePromptTokenCount；输出包含 thoughtsTokenCount，音频和缓存模态不丢失。OpenAI 缓存创建及 12h、缓存图片字段保留其存在性，不能重复计数。
- 图片有实际有效完成图及真实 DONE，即使少于请求 n 也按实际完成张数更新两个计价口径；中断不降低 n。只有预览/无图 DONE 不能算成功。
- Claude 工具 JSON 不完整只在后续确认为 max_tokens 且真实终态到达时兼容；其他终态、缺终态、非法完整 JSON 类型仍失败。原始 BillingUsage 保留前须验证来源及非负有界计数，不能以估算冒充确认值。
- 无交付收费须上游成功响应已被接受、真实上游失败、存在确认 token 计数、兼容开关开启、非按次/固定费用。HTTP 拒绝、无证据、仅张数、客户端断开、我方超时走各自原规则。显式全零不产生估算费用。
- 每次尝试清除前次证据与冻结设置；Finalize 和资金结算沿用幂等保护。账单仍保留 error 和 usage_final=false。
- 动态表达式按实际计价分支再检查：无交付确认用量若转入 fixed()，或计算失败无法取得实际价格，释放预扣，不能按预估单位/预扣金额收费。文本及音频共用此保护，已交付请求保持原有策略。

## Error handling and subsystem interaction

不新增重试/资金通道，继续使用原有 quota 安全计算、预扣/差额结算/退款及失败诊断。兼容修复本身不增加 DB/网络调用。工具、缓存、动态表达式都在现有 token 归一化入口处理，沿用管理员价格和倍率。

## Main Chain Impact

同步操作为每次尝试一次配置原子读取、有限用量字段校验/复制、请求内协议状态判断，以及工具参数追加和关闭块时的一次 JSON 校验。没有增加网络/DB/Redis 调用、睡眠、异步任务或同步日志写入。配置持久化仍走原管理员通道；原计费/日志异步机制不变。

## Shared Resource Audit

- Redis：无新键或操作，无 namespace 冲突。
- DB：不改 SQL、表、索引、锁或连接池；沿用现有结算，修正提交的用量和额度。
- 内存：配置只读原子快照；证据 map、BillingUsage、协议状态限于单个请求，流采集使用已有锁。不给全局 map 增加请求数据。
- 文件/连接/协程池：无新增资源，不新增 goroutine 或抢占 relay worker。

## Concurrency Analysis

在 30k RPM（约 500 RPS）及 100k RPM（约 1667 RPS）下，每请求额外 DB/Redis/网络/协程均为零。用量处理仅发生在 usage 帧，字段集合有界；普通文本帧只维护块类型，不新增正文副本。无新增跨请求锁。

工具参数校验与下游转换的交付状态不能直接共享，额外保留一份未关闭工具参数；增量追加总耗时与参数字节数成正比，每块关闭做一次完整 JSON 校验。所有活动块共享 200 MiB 逻辑字节上限（包括类型/身份/起始 input），关闭时立即释放引用；strings.Builder 容量余量、扩容临时副本及既有接收/交付缓冲使物理峰值可能更高。这是巨大并发工具请求下的额外内存/GC 成本，不能据静态分析宣称已通过 100k RPM 验证。本次不增加无界状态，也不做生产压测；可用原兼容开关回退。

## Test plan and results

先将 26 项双分支审计夹具改为修复后预期并运行失败验证，再实施。增加开关关闭/单次尝试冻结、缺字段/显式零、无交付确认用量、超时策略、工具 JSON 终态、原始快照及重复结算覆盖。

必须运行相关适配器、relay/common、operation_setting 回归；service 流生命周期及计费测试需隔离数据库或纯 fixture；涉及 relaykit DTO 必须独立 `GOWORK=off go build ./...`；最后根模块构建。测试结果及环境限制在完成后补记。

### 实施结果

以上八项差异和无交付确认用量结算均已实现。复查追加并验证：

- 无载荷、空白载荷、null 载荷的图片 completed 事件不能计为完成图。
- 可空缓存模态字段的 null 视为缺失，显式零保留。
- Claude 原始快照只接受单一受支持协议、非估算快照；计数非负且不超过既有 10 亿上限，遍历深度 8、单数组 128、总节点 512。后续确认字段变化会使旧快照失效，非法新快照不能覆盖仍匹配的有效旧快照。
- 混合计价按实际分支处理，预估 fixed → 实际 token 仍按确认 token 收费；反向切换和计算失败不收整次/预扣费用。
- 无交付场景扣除模型名或强制搜索请求推断的工具次数，保留上游明确的零/正数及实际协议计数。

### 最终验证（2026-11-04）

环境：Windows amd64，Go 1.26.10；未调用真实 AI 供应商。数据库均为本次创建的临时 Docker 容器/独立测试库，未连接现有业务数据库。

| 检查 | 结果 |
| --- | --- |
| `go test ./relay/... ./setting/operation_setting -count=1` | 45 个有测试的包通过；4827 个顶层/子测试 pass 事件，0 失败 |
| `SQL_DSN=<临时 MySQL> go test ./service -count=1` | 全包通过；2748 个顶层/子测试 pass 事件，含三数据库计费矩阵 |
| `SQL_DSN=<临时 PostgreSQL> go test ./service -count=1` | 全包通过；2678 个顶层/子测试 pass 事件 |
| `go test ./service -run '^(TestFixedPriceBillingDatabaseMatrix\|TestUndelivered.*\|TestForkConfirmedOnlyPricingReview)$' -count=1` | SQLite 3.50.4、MySQL 5.7.44、PostgreSQL 9.6.24 真实计费矩阵通过；随后新增反向分支随 MySQL 全包再次通过三库矩阵 |
| `cd relaykit; GOWORK=off go test ./dto -count=1` | 通过 |
| `cd relaykit; GOWORK=off go build ./...` | 通过，未依赖宿主 workspace |
| `go build ./...` | 通过 |
| `git diff --check`、修改 Go 文件 gofmt | 通过 |

数据库矩阵覆盖真实预扣、确认 token/纯缓存收费、零用量释放、实际 fixed 分支释放、计算失败释放、文本/音频入口、钱包与令牌余额、日志费用及重复结算。MySQL 测试服务使用 utf8mb4，DSN 包含 `charset=utf8mb4`。不改 SQL、表或迁移，因此没有执行版本升级迁移演练。

service 两个进程曾并行触碰既有 backfill 测试的同一日志文件，出现 Windows 文件占用；改为串行运行后全包通过。该测试执行隔离问题未引入生产代码修改。

验证日志位于 `F:/code/new-api-er-experiments/usage-fix-relay-final.jsonl`、`usage-fix-service-mysql-final.jsonl`、`usage-fix-service-pg-final.jsonl` 和 `usage-fix-billing-matrix.jsonl`。未执行生产部署、真实供应商调用、100k RPM 压测或 race 检测；主链并发影响为上述静态资源分析。修复默认开启，可通过 `upstream_usage_compat_setting.enabled=false` 回退本次兼容行为；先前的工具块/EOF 兼容仍由独立 Claude 开关控制。

### 厂商故障交叉实验（后续请求）

正常计费追加验证计划：使用各厂商完整成功响应经过真实适配器，分别断言原始/归一化用量与独立手算表达式费用；服务层复用真实 SQLite/MySQL/PostgreSQL 隔离数据库夹具，覆盖流式/非流式、兼容开关、传统倍率/动态 token/按次计价、缓存读写与 TTL、预扣小于/等于/大于实际、工具附加费及图片数量。核对钱包、令牌、用户/渠道用量、消费日志和结算幂等。测试使用明确的测试价格，不依赖实时厂商价目表；不向真实模型发请求，不进行整条 relay 路由端到端测试。测试代码不引入生产共享资源；若发现业务缺陷，先保留失败断言并核对真正 upstream/main 后最小修复。

使用合成的厂商协议帧与真实适配器；正常样本包含完整的文本、thinking/signature、工具 JSON 分片及完成事件，图片采用真实 1px PNG 的 base64。异常样本对正常帧做单点或组合破坏。没有使用线上客户正文，也没有请求真实模型。

协议依据：[Claude Streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)、[AWS InvokeModelWithResponseStream](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModelWithResponseStream.html)、[OpenAI Chat Streaming](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)、[OpenAI Responses Streaming](https://developers.openai.com/api/reference/resources/responses/streaming-events)、[Gemini generateContent](https://aistudio.google.com/api-reference/generate-content)、[xAI Streaming](https://docs.x.ai/developers/model-capabilities/text/streaming)。

实验采用等价类、边界值、条件组合及分块不变性验证：

- 正常文本/思考/工具/图片、拒绝与长度限制终态、缓存及模态用量；原生与转换路径。
- JSON 损坏、顶层类型/字段类型错误、负数/显式零/缺失用量、块索引/类型/结束原因冲突、非法工具参数。
- 单字节 UTF-8 和 7 字节 HTTP 分块、干净 EOF、Content-Length 不足导致的真实 unexpected EOF、AWS SDK 真实 eventstream 编解码及 CRC/载荷截断、5 类 SDK 异常。
- 故障类型 × 是否交付 × 确认用量/无用量 × 兼容开关；超时另交叉 refund/input/charge。检查正文不丢失或重复、错误终态仅一次、不能补造成功、用量来源及 Finalize 幂等。
- 本地 HTTP 无数据/仅心跳/仅用量/零用量/正文后阻塞。120 行主矩阵使用真实控制器、网络请求取消与确定性触发过期，不实际等待一小时；少量真实定时器对照将长等待缩短为毫秒，避免测试本身耗时数分钟。

新增文件：五个厂商目录的 `fork_vendor_fault_matrix_test.go`，`middleware/fork_vendor_timeout_matrix_test.go`，以及 `relay/common/fork_claude_protocol_guard_test.go`、`fork_responses_terminal_test.go`、`fork_responses_cross_protocol_test.go`。

本轮先复现后修复三类问题：Responses 缺失/损坏终态及跨协议字段冒充完成；通用 Claude（含 AWS SDK）块类型、工具参数、结束原因和跨协议终态校验缺口；Gemini 正常流显式零输入被估算替代，以及修正输入后旧 total 被误推为输出。关闭兼容开关保留旧路径，合法 max_tokens 部分工具参数仍要求真实 message_stop，且不算有效可执行工具。

已知限制：AWS SDK 二进制载荷中途截断、底层却返回干净 EOF 时，最先记录的错误仍可能是 upstream_incomplete；后续 SDK 的更精确截断诊断不会覆盖首因。它仍失败且不生成成功终态，计费测试验证确认用量规则；本轮没有修改这项诊断粒度。模拟实验能验证本地处理，不能确定历史渠道 460 的真实断流来源，也不能证明线上网络故障已经消除。

#### 统一实验结果

新增 **625 个叶子场景，全部通过，0 失败、0 跳过**；按 package + 完整测试名去除父测试后统计，不把父节点重复当成案例。

| 测试范围 | 新增叶子场景 |
| --- | ---: |
| Claude 原生 | 61 |
| AWS Bedrock SDK | 76 |
| OpenAI Chat / Responses / Images | 167 |
| Gemini 原生及转 OpenAI | 72 |
| xAI | 31 |
| 通用 Claude / Responses 协议边界与状态生命周期 | 84 |
| HTTP 超时、正常结束、客户端取消及计费策略 | 134 |

超时 134 项含原 120 行交叉、8 个真实定时器、4 个正常完成与已关闭定时器回调对照、2 个客户端取消。补查每次异常仅一个错误、无伪成功终态、正文一次、Finalize 不重复写、Write/WriteString/Flush 迟到写被拒绝、handler 关闭 detached 上游请求。无响应头时流门控不激活，保留外层 HTTP 504 处理职责，不伪造 SSE 响应。

| 最终验证命令/范围 | 结果 |
| --- | --- |
| 上述 9 个新增文件的 Test 名称组成精确 `-run` 表达式，运行 7 个 package | 625 个叶子全部通过 |
| `go test ./relay/... ./setting/operation_setting -json -count=1` | 45 个有测试的包通过；5348 个顶层/子测试 pass 事件，0 失败 |
| `go test ./middleware -run '^(TestRelayTimeout\|TestManaged.*\|TestForkVendor)' -json -count=1` | 通过，覆盖既有超时控制器回归及新增矩阵 |
| 临时 MySQL 主库 + PostgreSQL 日志库：`go test ./service -json -count=1` | 全包通过，2748 个 pass 事件，0 失败/跳过 |
| 协议生产代码冻结后重跑 service 的 Stream/Claude/Responses/Gemini/Undelivered/Fork/FixedPriceBillingDatabaseMatrix/Timeout 范围 | 986 个 pass 事件，0 失败/跳过 |
| `go build ./...` | 通过 |
| `cd relaykit; GOWORK=off go build ./...` | 独立构建通过 |
| `git diff --check`、修改/新增 Go 文件的 `gofmt -l` | 无错误/无待格式化文件 |

service 的两个运行均包含真实 SQLite 3.50.4、MySQL 5.7.44、PostgreSQL 9.6.24 计费矩阵。只使用本次创建的临时容器与独立库；验证结束已移除这两个容器及其匿名卷，未操作现有业务容器。relay 全量中 3 个显式启用的压测和 1 个无 SQL_DSN 的管理员错误测试保持既有跳过；另 3 个包无测试文件。新增矩阵没有跳过。未执行真实供应商调用、生产部署、100k RPM 压测或 race 检测。

本轮日志：`F:/code/new-api-er-experiments/fault-matrix-new-cases-final.jsonl`、`fault-matrix-relay-final.jsonl`、`fault-matrix-middleware-final.jsonl`、`fault-matrix-service-final.jsonl`、`fault-matrix-service-regression.jsonl`。复现红测试日志另见 `vendor-claude-crossovers-red.txt`、`vendor-claude-protocol-red.txt`；Responses 与 Gemini 的红断言也在修复前实际运行确认。

### 正常请求计费追加验证（2026-11-04）

测试分为两个相互衔接的边界：真实适配器解析完整厂商响应后核对 canonical 用量及独立手算费用；真实数据库的服务结算核对预扣、实际扣费、钱包/令牌余额、用户/渠道统计和消费日志。服务层测试手填成功 StreamResult，不宣称跑了整条 relay 路由端到端测试。全部金额使用显式测试价格。

新增 **1312 个叶子场景通过，0 失败、0 跳过**：Claude 64、AWS 64、OpenAI Chat/Responses/图片 156、Gemini 76、xAI 16、三数据库资金矩阵 936。一个适配器场景可校验多个价格表达式，未将同场景的金额断言重复计为叶子案例。

- 适配器：正常文本、thinking、工具、图片、缓存读写及 5m/1h/30m/12h、推理与音频包含关系、显式零与字段缺失、原生/转换、流式/非流式、开关两侧、token/按次/免费/阶梯边界。
- 资金矩阵：26 种正常计费输入 × 流式/非流式 × 开关两侧 × 预扣少于/等于/多于实际 × SQLite/MySQL/PostgreSQL；覆盖额外扣款、退还差额、零收费、无 usage 的估算、免费模型的有价工具、图片实际张数、缓存 TTL、舍入。
- 首次结算成功和余额先单独断言，再用不同金额重复 Settle、调用 Refund，确保不会补做首次失败而假绿、不会改变已结算金额；受管流重复 PostText 还要求消费日志和统计只有一次。
- 复用数据库夹具时暂时隔离员工成本/提成异步回调，结束后恢复原回调；钱包、令牌、用户/渠道用量和消费日志均走真实数据库。没有验证员工成本/提成台账，也没有让其缓冲引用跨临时数据库泄漏。

#### 历史策略核对与实际修复

Claude 转换流及 AWS 共用 finalizer 对输入与缓存总量均为零时估算输入，是上游提交 `1b63c28dcc999c4445c70f21ec90103ab49e8be5`（#7816）有意的防漏收策略。复核完整提交说明后，撤回了本轮尝试改成“已报告零就永不估算”的修改，保持实际 upstream/main 行为；测试明确记录与严格原生 Claude、非流式路径的既有差异。`relay/channel/claude/relay-claude.go` 本轮没有改动。不能将此策略差异描述成已修复的上游 bug。

正常 Gemini 响应确实复现了漏收：只有 candidatesTokenCount、缺少 promptTokenCount 时，转换后的平面 Usage 有请求输入估算，但原始 BillingUsage 快照仍为零；服务优先使用快照，从而丢失已有的估算输入。真实 upstream/main `ba5f7367797566175a6cd97a748d430af3602a01` 同样存在该路径。测试价格下输入估算 999、输出 20 的应收 1059 quota 曾实际只得到 60；修复后恢复 1059。

最小修复仅在 Gemini 适配器：已有兼容开关开启、原始输入字段确实缺失、没有上游自带原始 BillingUsage 时，克隆并补齐计费快照；明确标记 Estimated 和 LocalCountTokens。工具输入只加一次，缓存/音频/推理保持原值，不添加虚假的上游确认 evidence；显式零、上游原始快照和关闭开关保持原策略。原生响应字节不改写；Chat/Responses 缺输入修复场景另验证下游 JSON/SSE 最终用量与计费用量一致。既有显式零场景的平面用量/客户端显示与 canonical 计费用量差异未在本轮改变。

#### Main Chain Impact

本轮生产增量是 Gemini 缺输入判断与一次请求内计费快照克隆，文本流在最终用量汇总后执行，非流式在响应解析后执行；没有新增 DB/Redis/网络访问、goroutine、后台任务或跨请求锁。共享资源和连接池均无新增。30k/100k RPM 下不会增加外部调用次数；缺字段请求增加原有 metadata 大小的一次内存复制，正常完整 metadata 不复制。未实测这两个负载，因此不宣称吞吐达标。沿用 `upstream_usage_compat_setting.enabled` 回退。

#### 最终验证

- 临时 MySQL 主库 + PostgreSQL 日志库运行 `go test ./service -count=1 -json`：全包通过，3689 个 pass 事件、0 失败/跳过；其中两个数据库矩阵均实际运行 SQLite 3.50.4、MySQL 5.7.44、PostgreSQL 9.6.24。
- `go test ./relay/... ./setting/operation_setting -count=1 -json`：45 个有测试的包通过，5776 个 pass 事件、0 失败。保留既有 3 个需显式开启的负载测试与 1 个无 SQL_DSN 的管理员错误测试跳过；另 3 个包没有测试文件。
- 追加 Gemini 客户端用量断言后重新运行该包通过；生产代码未再变更。
- 根模块 `go build ./...`、relaykit 的 `GOWORK=off go build ./...`、gofmt 和 `git diff --check` 均通过。

最终日志：`F:/code/new-api-er-experiments/normal-billing-service-final.jsonl`、`normal-billing-relay-final.jsonl`、`normal-billing-gemini-wire-final.jsonl`；Claude/AWS 保留上游策略后的专门日志为 `normal-claude-aws-preserved-upstream.jsonl`，不使用已撤回策略的中间测试结果作为最终结论。本轮无生产部署、真实 AI 请求、生产压测或 race 检测。数据库仅本次临时容器及独立库；验证结束已删除这两个临时容器及其匿名卷，未操作已有业务数据库。
### Managed conversion finalization after upstream failure (2026-11-04)

The managed scanner observes Claude errors, malformed frames and missing terminals before the adapter callback. Consequently the Responses adapter's local `streamFailed` flag can remain false even though the managed session has already failed. Calling the normal Responses finalizer then creates `function_call_arguments.done` and a completed `output_item.done` for an unfinished tool. The writer suppresses the response-level success terminal, but these item events can still be delivered and counted as executable tools. Actual upstream/main delivers the error to the adapter callback and enters `FailResponsesStream` instead; it does not mark that tool completed.

For an active managed session with the existing frozen `upstream_usage_compat_setting.enabled` flag, a missing successful protocol terminal prevents normal conversion finalization. The guard belongs in the shared `service.FinalizeStreamResponse` boundary, covering all six adapter callers (Claude, OpenAI and Gemini conversions) while retaining their existing usage aggregation. Gemini's separate hosted-tool bridge also checks the same condition before its own finalization, since it bypasses that service boundary. The managed service remains the single owner of the terminal error and confirmed/estimated usage selection. No additional failed/incomplete event, synthetic tool completion, or tool-token estimate is introduced. Already delivered text/reasoning and confirmed upstream usage are preserved. Complete streams, unmanaged streams and the disabled compatibility path retain their existing behavior. No relaykit behavior is changed.

Regression fixtures use Anthropic's documented event schema: message_start, thinking/text/tool blocks, message_delta and message_stop, with synthetic contents only. Tests cover normal tool completion, provider error, EOF, malformed JSON, invalid block sequence, explicit zero usage, delivered partial content, compatibility rollback and idempotent finalization. The public protocol source is https://platform.claude.com/docs/en/build-with-claude/streaming .

#### Main Chain Impact

The change performs only existing request-local session/flag checks at the finalization boundary, synchronously on the relay goroutine. It adds no buffer, database/Redis access, network request, worker, goroutine or cross-request lock. The session's existing short mutex protects the completion check; no lock spans conversion or writing. Shared Resource Audit: only the already allocated per-attempt session and frozen flag are read; no shared namespace, table or connection pool changes. At 30k/100k RPM the extra work is constant per request, with zero external operations; no throughput claim is made without a load test. Turning off the existing compatibility setting restores the previous finalizer behavior.

#### Final verification

- Test-first reproduction failed in 14 leaf cases before the production guard: Claude-to-Responses created completed tools on four interruption types and changed an explicit-zero request into effective output; OpenAI Chat-to-Responses and Gemini-to-Responses created completed tools after error/EOF/malformed JSON; Responses-to-Claude emitted a synthetic content-block stop after the same three failures.
- Final focused run: `go test ./relay/channel/claude ./relay/channel/openai ./relay/channel/gemini -run '^(TestForkManaged(Claude|OpenAI|Gemini)(Hosted)?Finalizer|TestForkClaudeResponsesUnmanagedFailure)' -count=1 -json`. **86 leaf cases passed, zero failed/skipped**: Claude 38, OpenAI 32, Gemini 16. Includes both compatibility settings, unmanaged Claude error handling, normal complete tools and Gemini hosted grounding. Failed Gemini streams do not synthesize hosted search completion; actual reported search counts remain intact.
- `go test ./relay/channel/claude ./relay/channel/aws ./relay/channel/openai ./relay/channel/gemini -count=1 -json`: all four packages passed, 1714 leaf cases / 1845 pass events, zero failures. Two existing opt-in Claude load tests skipped. The later rollback/hosted additions were then included in the final 86-case focused run above.
- Pure service regression used all non-test `service/*.go` files plus `convert_test.go`, `responses_usage_test.go`, `stream_lifecycle_test.go`, `stream_disconnect_test.go`, `stream_undelivered_usage_test.go`, and `stream_settlement_test.go` as an explicit `go test ... -count=1 -json` file list. **93 leaf cases / 105 pass events passed, zero failed/skipped**. This excludes the package's database-opening TestMain; no business database or new database instance was used.
- Independent review found no blocking issue in the two guards. Root `go build ./...`, the isolated fixed gateway build, formatting and `git diff --check` passed. No relaykit production files were modified by this finalizer fix.
- The parent agent's eight authorized live follow-ups with the fixed isolated gateway confirmed: Kimi still returned genuine upstream errors on all four protocols, but the Responses synthetic tool-done count fell from two to zero; Haiku completed two valid tools on all four protocols (four legitimate Responses tool-done events). This fixes local conversion finalization, not the underlying Kimi upstream failure. It does not change Gemini's inherited thinking conversion limitation or claim exact historical remote token-estimation equivalence.

Local evidence: `F:/code/new-api-er-experiments/managed-claude-finalizer-red.txt`, `managed-finalizer-final.jsonl`, `managed-finalizer-provider-regression.jsonl`, and `managed-finalizer-service-regression.jsonl`. The shared service guard and Gemini hosted guard are the only production changes for this follow-up; both Claude handler files remain unchanged. Actual upstream/main's explicit-error callback behavior is the reference, not a claim that every upstream EOF conversion path is already correct.

### Cumulative Gemini and xAI usage presence (2026-11-04)

The user explicitly authorized fixing the first two follow-up audit findings. Local fixtures reproduced two successful-stream accounting inconsistencies: Gemini's nonzero-only merge retains tool, candidate and modality counts after explicit zero corrections, while xAI's whole-detail replacement clears previously confirmed cache/reasoning counts when a later usage frame omits those fields. Failed streams already preserve field presence in their session evidence. This follow-up fixes the adapter accounting selected for successful streams; embedding's existing nonpositive-input estimation policy is outside the authorized change.

Data flow remains upstream JSON/SSE -> adapter usage accumulation -> existing stream finalization -> canonical billing usage -> existing settlement and logging. With the existing attempt-frozen `upstream_usage_compat_setting.enabled` flag, merge reported numeric fields individually: omission keeps the previous count, explicit zero replaces it, and partial modality/detail updates preserve other fields. Null/empty detail containers and nullable cached-modality fields do not clear previous counts; existing managed-stream validation still rejects invalid scalar counters or malformed modality entries. Gemini must not derive stale output from an earlier total after a component is corrected to zero; retain authoritative provider billing sidecars and the existing estimate for zero output when actual content was received. xAI retains its existing `total_tokens - prompt_tokens` completion calculation. Disabling compatibility restores the previous adapter behavior.

There are no new APIs, auth levels, tables, migrations, indexes, configuration parameters or user-facing errors. Existing parsing failures and stream termination policies remain in place. Tests use synthetic responses and the real handlers/finalizer, checking canonical usage and pricing inputs without settling balances or accessing a database/provider. Regression cases written before implementation cover positive-to-zero, missing/null/empty/partial details, retained unrelated modalities, successful versus incomplete streams, supported downstream conversions, and compatibility rollback.

Implementation is confined to `relay/channel/gemini/relay-gemini.go` and `relay/channel/xai/text.go`; the shared relaykit merge APIs remain unchanged. Gemini merges six scalar counters and the three supported modality lists using raw field presence. Duplicate entries within a frame retain their summed billing meaning. A previously reported candidate zero cannot be revived by a stale/later total; thoughts-only zero does not imply that unreported candidates are zero. When no new total is reported, reductions remove stale components while newly reported components are not added twice to an existing total. Original provider billing sidecars retain their existing merge authority.

xAI merges all eleven scalar detail fields plus three nullable cached-modality counts and two cache-write TTL counts. The converted downstream usage receives a cloned input-detail snapshot and a copied output-detail snapshot, so omitted upstream details cannot be serialized as false zero corrections to the client. Its total-minus-prompt output calculation is unchanged.

#### Main Chain Impact

Presence checks and merges execute synchronously only while processing usage frames, using request-local accumulated metadata. No new asynchronous work, network/DB/Redis operation, goroutine, pool, cross-request lock or error propagation is introduced. Existing streaming and terminal handling are unchanged.

#### Shared Resource Audit

The changed adapters only read the existing attempt-frozen compatibility setting and update their request-local usage objects. They do not add or modify Redis namespaces, DB queries/tables, shared caches, connection pools or worker pools. Downstream billing and its existing asynchronous accounting/logging pipeline consume corrected counts without new writes or access patterns. Provider-native billing snapshots remain request-local.

#### Concurrency Analysis

At 30k/100k RPM this adds zero external calls and zero goroutines per request. Scalar/detail checks are bounded by the known usage schema; modality merges process only the usage arrays already decoded by the adapter. Do not add another full-response decode, recursive generic merge or growing per-frame history. This is a code-path resource analysis, not a measured throughput claim.

#### Final verification

- Added **94 regression cases** in the existing fork test files: Gemini 19 scenarios across four downstream formats (76), xAI nine scenarios with both compatibility settings (18). The original merge failures and xAI downstream inconsistency were observed as failing assertions before their fixes. Review also caught and reproduced the thoughts-only-zero and total-double-counting boundaries before correcting them.
- `go test ./relay/channel/gemini ./relay/channel/xai -count=1`: both full packages passed after the final production changes. Focused Gemini presence/audit tests passed 118 subcases; xAI partial-detail/audit/switch tests passed 22 subcases, including the new cases above.
- `go test ./relay/common ./setting/operation_setting -count=1`: both packages passed. The pure service file-list regression also passed using all non-test service files with `fork_zero_output_diagnostic_test.go`, `stream_settlement_test.go`, `stream_deadline_test.go`, `stream_log_info_test.go`, `stream_usage_compat_test.go`, `stream_original_error_test.go`, `stream_diagnostic_test.go`, `stream_response_gate_test.go`, `stream_zero_output_test.go`, and `downstream_diagnostic_test.go`. Excluding the database-opening TestMain avoids accessing a business database.
- `go build ./...`, `git diff --check`, and gofmt checks of the four changed Go files passed. Independent final review found no remaining blocking issue.
- No schema/query, relaykit code/public API, embedding policy, frontend, deployment or upstream-provider call changed in this follow-up. No database settlement, race run or production load test was performed; tests validate selected billing usage, pricing inputs and converted usage output.

### Zero-output stream diagnostic retention (2026-11-04)

The user explicitly requested diagnostic records for zero-output requests. The paired screenshots and follow-up identify the local request as `client_gone`, no effective content, locally estimated input 375867/output 0, multiplier 3.3 and charge $9.9229 after about 80 seconds; the upstream log with the same request ID completed after about 95 seconds and reports input 389301/output 1542/cache read 389298. The reproduced client-disconnect path is consistent with these facts. The available evidence cannot distinguish a client cancel, client deadline or intermediary disconnect. Upstream commit `55a6cd2a44e` (#7427) explains the existing input-estimation policy for interrupted requests; this change does not alter that policy, recover unknown final usage, or invent cache counts.

Currently only upstream failures/refusals receive `DiagnosticAvailable`; the log filter removes bounded response headers/body and downstream bytes for ordinary client disconnects and successful zero-output streams. Numeric usage evidence, estimates and error metadata may already remain in the private log envelope even when the UI has no diagnostic entry. Actual upstream/main has public stream status logging but does not have this fork's private capture/Root retrieval subsystem.

Implementation is limited to the final settlement/log boundary and error-log attachment. `FinalizeConsumptionSettlement` already receives final normalized log token counts: add diagnostic eligibility immediately before its existing stream-log refresh when the emitted row has zero completion tokens (including a refund/error row whose token columns are fixed to zero). `AppendStreamErrorDiagnostic` likewise retains accepted stream captures for actual errors, since its error-log rows have zero token columns. Both additions require the existing frozen compatibility flag, the successful response gate and an existing capture; absent captures do not create new raw data. Existing upstream-failure eligibility remains unchanged. Later error attachment must preserve an already finalized diagnostic's usage evidence, estimates, terminal reason and eligibility.

No API, schema, quota calculation, retry behavior, terminal frame or capture capacity changes. Root authorization and ordinary-log stripping remain the read boundary. The new eligibility is diagnostic-only: normal zero output stays successful, client disconnect stays `client_gone`, and usage field absence remains distinct from explicit zero. Existing raw-body omission and empty captured-body distinctions remain observable.

#### Main Chain Impact

The final log counts are already normalized, so the new decision adds constant-time scalar checks rather than another canonical-usage clone or content parse. It retains existing per-attempt samples (upstream head/tail 1 KiB each; downstream 2 KiB; existing header/history bounds) and adds no new body buffer, read/drain, network/DB/Redis call, lock, connection, worker or goroutine. Shared Resource Audit: only the existing request-local outcome/capture and existing `logs.other` asynchronous queue are used; no changes to users/tokens/channels, indexes or connection pools. At 30k/100k RPM the number of log writes is unchanged, but each newly eligible zero-output row carries the existing bounded diagnostic payload; storage/queue bandwidth can increase with that request fraction. No load-test claim is made.

Test-first local fixtures cover client disconnect with estimated 375867/0 and missing terminal usage, timeout, upstream error, successful explicit zero, missing usage versus explicit zero, delivered content with logged zero, normal nonzero filtering, final refund/error rows, direct error logs, compatibility rollback, absent capture, pre-response/non-200 exclusion, finalized error-append preservation, idempotence and ordinary-log privacy. Mock settlement and an unstarted asynchronous log pipeline avoid database and provider calls.

#### Final verification

- The meaningful pre-fix run contained 30 leaf cases: 10 failed and 20 passed. Eight failures reproduced missing zero-output diagnostic eligibility; two showed later error attachment losing existing usage evidence for an already eligible timeout/upstream error. An initial empty-string privacy assertion was corrected before this recorded red run.
- The final dedicated fixture has **41 leaf cases, all passed**, including attempt-frozen rollback, failed funding with positive candidate output but a zero-output error row, committed partial settlement with positive output remaining filtered, and rejecting a previous attempt's finalized evidence. No quota, usage value, terminal classification or settlement-call count changed.
- Pure service regression compiled all non-test `service/*.go` plus `fork_zero_output_diagnostic_test.go`, `stream_settlement_test.go`, `stream_deadline_test.go`, `stream_log_info_test.go`, `stream_usage_compat_test.go`, `stream_original_error_test.go`, `stream_diagnostic_test.go`, `stream_response_gate_test.go`, `stream_zero_output_test.go` and `downstream_diagnostic_test.go` using an explicit `go test ... -count=1 -json` file list: **200 leaf cases / 214 pass events, zero failures/skips**. Excluding TestMain avoids opening a database. The legacy dual-log test keeps separate expectations: actual error rows gain the new eligibility, while consumption metadata before final settlement still follows the original filter.
- Parent verification: `go test ./relay/channel/openai -count=1 -json` passed **687 leaf cases**, including 24 late-usage reproduction cases; targeted `relay/common` diagnostic/capture/gate regression passed **25 leaf cases**. Both runs had zero failures/skips. Root `go build ./...`, formatting and `git diff --check` passed.
- Two independent read-only reviews found no blocking issue. Production changes are confined to `service/consume_settlement.go` and `service/log_info_generate.go`. No new database schema/query, provider request, credential access, background service or long-running test resource was introduced; test accounting callbacks were drained and restored. This follow-up did not deploy a binary or alter the running gateway.

Evidence files under `F:/code/new-api-er-experiments/`: `zero-output-diagnostic-red.jsonl`, `zero-output-diagnostic-green.jsonl`, `zero-output-diagnostic-service-regression.jsonl`, and the parent's `zero-output-capture-regression.jsonl`. Historical omitted response samples remain unrecoverable; newly retained evidence describes only bytes and usage observed locally before termination.
