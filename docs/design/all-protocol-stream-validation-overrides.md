# 全协议流式严格校验开关：代码复核后的方案

状态：2026-11-10 用户批准最终方案后已实施三级全协议配置、兼容处理路径及独立日志，并完成第二轮逻辑复核。验收记录见文末；此前 Claude 局部实现和旧渠道测试不构成本方案的验收，当前不得将异常矩阵的已知失败称为全部通过。

## 1. 目标、基准与边界

2026-11-16 前端规范修正：`StreamProtocolValidationField` 的选项置于项目 `SelectGroup` 内，恢复标准选项容器间距。继承、启用、禁用的值映射及表单禁用行为保持原契约，相关交互测试通过；未修改协议处理、配置存储或 relay 共享资源。

2026-11-16 文案修正：请求检查、用户与渠道设置直接说明严格校验的开关效果和用户、渠道、全局优先级，不再用“官方 main”或配置历史解释功能。兼容提示展示所在层级的 Claude 专用校验状态及其他协议的默认或继承规则；快捷按钮明确标注“全部启用”或“全部禁用”，仍须提交表单保存。请求文本过滤提示明确保存后生效及所需开关、关键词。七种语言同步更新；仅调整展示文案，不改变校验、保存、继承逻辑，不新增或修改 relay 共享资源访问。

文案验证：请求策略设置与校验字段两个 Vitest 文件共 30 项通过，Lint 无错误；七种语言各新增 8 个键，原有翻译未变，插值与 UTF-8 无 BOM 检查通过。`bun run i18n:sync` 完成。当前本机 Bun 1.3.13 无法运行项目的 `bun check -b`，改用 `bunx --package typescript tsc -b --pretty false`（TypeScript 7.0.2）完成类型检查，未修改依赖文件。

目标：覆盖本分支已经增加的流式协议校验、异常收尾及关联用量选择；支持全局、渠道、用户三级配置；关闭时对应处理遵循最新官方 main；日志独立保留，不为填写日志继续拦截响应。

本轮比较工作区实际代码，而非仅 HEAD。已重新查询和拉取官方 upstream/main，审计基准为 `0409deccfff2b0e8babc28ca552af44d526375bf`。本地 main 和之前部署的 `bd6bb53019fb7e21804b31ab7365c9b2097f4b6c` 均不能冒充当前最新基准。正式验收时再次获取最新提交，记录构建 SHA、二进制摘要、配置及 fixture 版本；一次对照固定版本，若 main 更新则重新构建并补跑受影响项目。

“与 main 相同”指相同实际上游输入下的解析、转发、转换、结束事件、错误传播、用量选取及缺失用量补算。不能只比较 HTTP 200 或最终 token 数。

认证、渠道权限、本分支独立重试/超时配置、价格表达式、额度防溢出和最终结算幂等继续生效。SDK 基础解码和必要 DTO 解析不是可关闭的额外严格校验。独立功能与 main 不同，须在对照中关闭或等值配置，并列出差异；不承诺整个分支变成 main。资源释放不能退回泄漏/竞态。按 2026-11-13 用户最终要求，Realtime 关闭态保留 main 逐轮实际扣款加最终结算的既有行为；不能用本分支认为更合理的单次收费替代 main 实际扣款。

## 2. 实际调用链审计

已核对入口分发、HTTP/SDK/WS、扫描器、协议转换、用量收口、资金、诊断采集、持久化及前端展示，并对相关处理器比较 upstream/main。以下是设计必须处理的差异，不是实施结果。

| 位置 | 当前行为 | 设计约束 |
| --- | --- | --- |
| controller/relay.go、service/stream_lifecycle.go | 每次尝试创建通用会话、语义 writer、采集器；当前只冻结 Claude 策略 | 在安装语义处理前决定模式，每次换渠道重新冻结 |
| controller/relay.go、service/relay_error.go | 已接受/已写出/已取消后的收口保护挂在 Active 上；DecideRelayRetry 不独立检查 Writer.Written | 关闭策略不能绕过已交付响应的禁止重试保护；须从语义校验中拆开 |
| relay/common/relay_info.go | UseStreamErrors 冻结客户端流式属性，采集也依赖它 | 不能拿它当新开关，否则日志与入口隔离一起关闭 |
| relay/common/stream_session.go、stream_diagnostic.go | Active 同时受响应门控和处理权移交影响；原生严格 Claude 也会 Disable 通用会话 | Active=false 不等于用户关闭；策略、所有权、门控分开 |
| 同文件 streamObservedBody、StreamWriter | 解析、拒绝、缓冲、过滤帧并产生交付证据 | 不是被动观察器，关闭后不能为日志继续运行 |
| relay/helper/stream_scanner.go、managed_stream.go | 部分入口看 Active，专用 event/line scanner 自身仍有错误过滤 | 逐调用点处理，不能统一 Disable 后就声称覆盖 |
| relay/channel/openai/relay-openai.go、helper.go | 旧路径仍有零响应补算差异；Chat 转 Claude 有额外补结束帧 | 关闭态连同这些行为恢复 main 语义 |
| relay/channel/gemini/relay-gemini.go、relay/channel/xai/text.go | usage 存在性、缓存/模态合并及补算有不受 Active 控制的改动 | 分离流式兼容行为，不能误回退同文件非流式功能 |
| service/usage_helpr.go、Cohere/Coze/Tencent/Dify 等 | ResponseText2UsageFromStream 无响应时返回零；不同于 main 的旧补算 | 逐调用点比较零响应、明确零与缺失字段 |
| relay/channel/aws/relay-aws.go | SDK 直接 ObserveEvent，不经过 Claude prepareMain；额外错误收尾 | 旧 Claude 开关没覆盖这里；HTTP 采集与 SDK 语义分开 |
| relay/channel/xunfei/relay-xunfei.go | 握手前按 StreamSession != nil 选受管路径 | 留 disabled session 仍可能走错处理器 |
| relay/channel/volcengine/tts.go | WS 载荷采集回调依赖 Active | 关闭后采集不能一起消失 |
| relay/channel/openai/relay_realtime.go、managed_realtime.go、service/quota.go | 旧路径逐轮实际扣款；受管路径逐轮预留、最终一次结算；当前外层还会总量结算 | 直接跳回旧函数有重复扣费风险 |
| relay/responses_websocket.go | 不进入 BeginStreamAttempt；核心终态检查和逐 create 处理已在 main 中 | 不是本分支新增严格状态机，保留 main 检查，补独立观察 |
| relay/upstream_stream_adapt*.go | 非流式客户端因独立超时功能改用上游 SSE 并组装响应 | 不能因上游 SSE 纳入严格开关；共用 StreamHandledKey 不代表同一功能 |
| service/log_info_generate.go | 资格与 StreamResult/会话快照耦合，关闭态可能投影默认 false | 日志脱离严格结算结果，未知不能显示为失败 |
| stream-diagnostic-panel.tsx | optional bool 用真假显示是/否，缺失估算显示 0 | 增加未校验/未知展示，缺失不等于否或零 |

### 2.1 覆盖目录：入口格式、上游协议、处理器分别登记

以 relaykit/types/relay_format.go 的 14 个 RelayFormat 为入口清单，再展开供应商、传输和转换路径。不能用“测过 OpenAI”代替测过所有 OpenAI 格式渠道。

| 入口/传输 | 本次覆盖 |
| --- | --- |
| OpenAI Chat / Completions SSE | 通用层、原生转发、透传、function_call、多工具/候选及转 Claude/Gemini/Responses |
| Responses SSE、Chat 经 Responses 转换 | 受管层及 incomplete/completed、错误和用量收尾 |
| Claude 原生及转 Chat/Responses/Gemini | 专用状态机、转换器、通用层互斥、最终 usage；Vertex 复用也验证 |
| Gemini 原生及转换 | 候选、finishReason、安全拦截、思考/缓存/媒体及转换尾帧 |
| Realtime WS | 新增语义处理；单独保持唯一资金会话与连接所有权 |
| Responses 原生 WS | 两种配置均保持 main 原处理；本分支额外严格策略标不适用；每个 create 独立观察 |
| Images | 真正图片流及客户端流式入口返回完整 JSON 的受管路径；MiniMax/Ali 原生结构、Ali 任务中间态和最终张数 |
| Audio | SSE、二进制、完整 JSON 转媒体/URL、Volcengine TTS WS；媒体中不能插 SSE 错误 |
| Embedding、Rerank、ResponsesCompaction、AlphaSearch、DeepSeekTools | main 正常/异常合同回归；普通 JSON 不新造状态机；实际复用受管处理器则按该路径归类 |
| Task、MjProxy | 提交、查询、回调、终态按独立合同；Task 借 Responses 呈现单列，不混入文本流结算 |

原生适配器另列：Ollama NDJSON、Cohere 行事件、旧智谱 add/finish SSE、Baidu/Coze/Tencent/Dify、PaLM 完整 JSON 转 SSE、xAI、Xunfei WS、AWS SDK EventStream、MiniMax、Volcengine。其余渠道按实际复用处理器分组，登记渠道类型与调用路径，新增/未分类路径不得默认已覆盖。

AWS 当前 Nova 是 InvokeModel 完整 JSON 后合成输出，不得标成已测 Nova EventStream。Ollama 自身文件与 main 相同，但外层 body observer 仍能改变结果，不能仅看适配器 diff 排除。

## 3. 运行时设计

### 3.1 三项状态独立

2026-11-15 审查清理：通用策略和旧 Claude 策略共用一个冻结标记，Claude enabled 直接读取其策略值；删除重复布尔字段和 MainStreamMode 中与所有权重复的条件。实际 Claude 生命周期标记、处理所有权、日志来源及观察状态继续保留。每次渠道重试仍重新冻结；详情见 [审查问题的局部修复](review-simplification-20261115.md)。

1. 策略：每次尝试冻结 enabled/source/config_origin 的值，不持有共享缓存指针。source 为 user/channel/global，config_origin 区分规范键、旧 Claude 键或默认值。
2. 处理所有权：通用严格、Claude 专用严格、Realtime 专用严格、main 兼容、不适用。不能用 session 是否 nil 或 Active 猜测。
3. 观察状态：实际响应/握手状态、有界样本、实际读写字节/错误和清理时间。独立于严格开关；保留原有流式错误记录链路，以及私有响应诊断的成功响应门控和留存条件，不能把两者混为同一个资格判断。

数据流：认证缓存 → 渠道分配 → 冻结策略 → 选择实际处理路径 → 安装被动采集 → 仅开启态安装语义处理 → 处理器消费响应和选择 usage → 唯一资金收口 → 冻结日志快照 → 现有队列异步落库。

非 200、握手失败、无响应继续现有错误记录、重试与退款，不因此新增一套流式私有诊断；映射后 200 不算真实门控证据。不为采集额外读取或排空 body，没有响应体就不补造正文。

### 3.2 开启与关闭合同

- 开启：以当前工作区严格行为为基准，保留既有异常矩阵及专用 Claude 所有权；重构不能无意改变已确认计费。
- 关闭：不执行本分支额外状态机、成功尾帧过滤、缺结束补错、按有效交付重选用量、零输出二次补估等严格处理。main 的解析/SDK/转换错误和超时仍可能报错。
- 恢复具体处理器的 main 语义，不复制整个适配器形成两套长期分叉；在必要差异处分支，复杂收口保留可测试边界。
- 不在生产运行影子严格校验来填日志或比较，不预读完整流。main 本来补的帧仍补，本分支额外补的帧在关闭态不继续补。
- 不为日志创建 StreamResult：它影响纯缓存计费资格、无用量处理及最终结算。日志结果和计费状态分开。
- 客户端入口模式只冻结一次；每次渠道重试重新冻结策略并重建门控、样本和错误。SDK 内部交换沿用该尝试策略，交换编号及证据独立。
- 当前请求不被后台切到一半；下一次尝试/请求生效。Realtime 整条会话固定策略；Responses WS 每次 create 独立记录，不能串用上轮证据。

### 3.3 Realtime 专门方案

保留单一状态所有者、有限读取工作者和统一关闭/等待框架，将协议判断、轮次用量选择分为严格/兼容行为。兼容态采用 main 的事件、用量及实际扣款口径，调用既有逐轮实际扣款，并在退出时执行一次最终结算。此处覆盖早期仅对齐日志用量、改用累计预留的设计。

严格模式使用一个 BillingSession：开始预留，轮次按累计额度补预留，结束唯一结算或释放。兼容态累计 usage 来自兼容处理器，不调用严格 FinalizeStreamUsage 重选；每轮调用 main 对应的 PreWssConsumeQuota，最终复用既有资金会话完成结算。最终结算标记仍只允许一次最终调用，不伪造 StreamOutcome，不新建第二本账。

比较 main 所选累计用量、钱包实际减少、令牌实际增加、消费日志和成本收入；钱包与日志不相等的 main 既有缺陷须保留并披露。重复 response.done、未知/缺失 ID、取消、结束后读错、预留失败、钱包扣成但令牌更新失败均需分别验证，不能把未覆盖情况声明通过。兼容态重复 response.done 按 main 累加，不自行去重；严格态保留原去重规则。用量累加与唯一最终结算分别验证。

### 3.3.1 2026-11-13 原生 WebSocket 兼容边界补查

补查确认讯飞关闭态仍施加 main 没有的空闲读截止，晚到的最终用量由 21 降到 9。Realtime 和讯飞共用受管读取上限也在关闭态拒绝 200 MiB+1 的合法 JSON 消息，阻断本帧或后续用量。修复范围是按冻结的 main/严格策略选择这些附加限制；严格态 200 MiB 上限、空闲截止和写截止保持原值，独立请求总时限继续生效。讯飞初始写入的 15 秒截止也须对照慢接收反例。

Main Chain Impact：只改变连接初始化及既有截止分支，不增加同步 DB、Redis、网络请求或读取工作者。兼容态保留 main 无额外 WS 消息大小上限的读取行为；缓冲仍按实际消息增长，不能宣称其内存上界为 200 MiB。严格态、诊断首尾截取和资金防溢出不变。

Shared Resource Audit：使用已有每连接 websocket.Conn、局部读队列和既有工作者池；无新增共享 map、连接池、表、Redis namespace 或热路径锁。上限选择在启动读取前完成，冻结策略不跨 goroutine 修改。

Concurrency Analysis：30k/100k RPM 每请求新增 DB/Redis 调用、锁和 goroutine 均为零；关闭态的大消息内存风险与 main 相同，不能以功能回归替代并发大消息负载验证。大帧测试串行执行。必须区分原生适配器测试与 OpenAI 渠道经 main 的矩阵，两者不能互相冒充。

### 3.4 响应提交与重试保护独立于严格模式

controller/relay.go 当前在 StreamSession.Active() 内检查 state.Accepted、c.Writer.Written() 和请求取消，调用 FinalizeStreamFailure 后返回。若只是 Disable，会绕过这一层。后续 DecideRelayRetry 只按错误、重试策略等判断，没有独立的响应已写出检查。因此“已写出部分响应后返回可重试错误”可能重新分配渠道，并非关闭后所有请求必然重试。

实施时把响应是否已经提交、是否由专用处理器收口、请求是否取消作为独立生命周期事实；不依赖 Active、也不能把收到上游 200 直接等同于已经交付客户端。已向下游提交响应不能重试拼接另一个模型响应，取消后不能继续发起新尝试；响应前失败仍按现有重试合同处理。

关闭态停止重试不等于调用严格 FinalizeStreamFailure：只执行对应兼容路径的错误记录及唯一资金收口，不能借保护重试重新套用严格异常用量矩阵。回归必须覆盖已写正文、仅提交响应头、写入失败、取消，以及未提交响应的可重试失败，验证尝试数、下游字节和资金操作次数。

### 3.5 本轮复核的表述校正

- prepareClaudeMainStream 是旧 Claude 策略的处理器入口，但不是唯一调用 Disable 的地方；strict_stream.go 的 strictClaudeStream 在严格开启时也 Disable 通用会话，用于移交所有权。因此 Disable 本身不能说明严格开关关闭。
- openai/helper.go 的 handleClaudeFormat 有 Active 条件分支；但 HandleFinalResponse 中“已发送 message_start 且未 Done 时合成 stop”的分支没有 Active 条件。两者不能混称为“额外结束帧都已由 Active 保护”。
- AppendStreamLogInfo 和 AppendStreamErrorDiagnostic 都是日志字段附加函数，后者不是创建错误日志的入口。实际渠道错误记录由 ProcessChannelError → RecordErrorLog 完成，并保留 ErrorLogEnabled、IsRecordErrorLog 等既有条件；该路径调用 AppendStreamErrorDiagnostic 附加符合条件的私有证据。维持原记录流程不能只改诊断函数就认为错误已落库。

## 4. 配置、旧值兼容、API 与权限

优先级：**用户显式设置 > 渠道显式设置 > 全局默认**。全局是默认值，不是压过用户覆盖的总闸。

| 层级 | 规范字段 | 值 |
| --- | --- | --- |
| 全局 | stream_protocol_validation_setting.enabled | bool，未配置默认 true |
| 渠道 setting JSON | stream_protocol_validation | true/false/null（继承） |
| 用户 setting JSON及管理 DTO | stream_protocol_validation | true/false/null（继承），普通用户不可自行修改 |

更新时字段缺失保留原值，显式 null 清除覆盖，false 不得被 omitempty 丢失。保存规范字段时清理同层旧 Claude 覆盖，避免选择继承后旧 false 复活。用户局部更新保留其他偏好、profile_version 和缓存失效；渠道保留未提交的其他设置。

### 4.1 旧 Claude 值不能静默扩成全协议

旧字段只控制过部分 Claude 处理器，不能把旧 false 直接迁为全协议 false。

按层兼容读取：该层显式新字段优先；没有新字段时，旧字段仅对以前真实受它控制的 Claude 路径生效，其他协议跳过旧字段。之后按用户、渠道、全局取值。旧值不是额外否决开关，新全局也不能越过更高层有效覆盖。

例如仅旧全局 Claude=false：升级后原受控 Claude 仍关闭，其他协议默认开启；管理员明确保存新全局 false 后，才全协议默认关闭；用户显式 true 仍优先。AWS SDK Claude 原先未受旧键控制，不能仅因模型名字叫 Claude 就套用旧值。

全局新键的显式存在性取已加载的持久化 options 集合；model/option.go 先向 OptionMap 填注册默认值，不能用该 Map 的键存在性判断管理员是否设置过。启动、周期同步和管理保存都计算完整相关配置后一次发布快照，不受 map 遍历顺序影响，不在 relay 查数据库。

旧 API 只更新原 Claude 范围。同层已有规范覆盖时拒绝冲突旧写入，给出可操作的翻译提示，避免显示保存成功却不生效。新旧字段同次提交时规范键优先并清理旧键。加载顺序、缺失/null/false、失败回滚均测试。

界面显示旧配置兼容提示，不能把旧 Claude=false 展示成全协议关闭；不批量改写 users/channels，不隐式扩大旧用户覆盖。

### 4.2 API contracts

不增加公开接口，复用既有 envelope：

- PUT /api/option/：`{"key":"stream_protocol_validation_setting.enabled","value":"false"}`；PUT /api/option/group 沿用原 scope/module/values 合同，例如 `{"scope":"request-policies.filtering","module":"stream_protocol_validation_setting","values":{"enabled":"false"}}`。两者均要求 AdminAuth + 系统设置编辑 scope，登记 request-policies.filtering。
- 现有渠道管理新增/更新接口：原 setting JSON 内增加规范键，保留原字段类型和管理权限，不能擅自把序列化字符串换成对象。
- 现有管理员用户编辑接口：可空布尔 DTO，落已有 setting JSON；自助资料更新必须保留管理员覆盖，不能通过整个 setting 覆盖它。
- GET /api/log/claude-diagnostic?request_id=...&created_at=...&attempt=...：沿用 RootAuth、no-store、三秒查询超时及历史路径，支持所有实际诊断协议，无需新同义接口。

管理参数错误遵循 success:false 和后端双语规范；底层错误先日志再脱敏。相关键保存必须检查事务错误、提交后发布快照，不能沿用忽略 Save.Error 的路径声称原子成功。

## 5. 日志设计：没有异常中止原因也是有效结果

用户于 2026-11-10 进一步明确：**维持原来的流式错误记录；有异常默认记录，无论严格校验开启或关闭。** 本次沿用既有错误记录入口、日志类型、字段含义、请求/尝试关联、异步落库和展示方式，不另建错误日志体系。严格开关不能让原本应记录的实际错误被跳过。

这里区分“错误记录”和“私有响应诊断”：实际错误继续进入原有错误记录链路；原始响应头/正文等私有诊断仍遵守既有门控、预算、留存和权限。收回上一版扩大所有异常私有诊断资格、取消成功响应门控的提议；不能借本次开关改写原来的日志范围。

关闭模式接回同一记录链路，不能只因 StreamSession 不活动或没有 StreamResult 就漏记实际错误。未执行的严格检查不能伪造为已发现错误，不为了填日志重新启用拦截。正常消费日志、请求日志及拒绝/零输出诊断保留原有规则；同一次错误不得因新旧入口同时调用而重复记账或重复记录。

### 5.1 字段和展示

维持现有字段及其含义，仅在已有 other JSON/私有诊断中兼容追加必要的策略/未知状态信息，不替换原日志格式、不新增列：

- validation：enabled、source、config_origin、适用处理器、检查状态。区分未校验、不适用、通过、失败、未完成；配置开着但尚未进入处理器不算通过。
- protocol_complete：有实际判断才填布尔；未校验省略/null，前端显示未校验；不适用显示不适用。main 自己报告的完成可标来源 main，不能冒充本分支严格校验通过。
- 用量：处理器选中的实际结算 usage，来源为已知的上游确认/估算/混合/未知。没有最终性证据不能默认 usage_final:false；兼容读取历史字段。
- 交付：实际读写字节可记录；业务响应、有效内容、token 估算只有测过才填。缺失不能在 UI 补成 0/false，写出也不等于用户已阅读。
- 模型 stop_reason、网关处理结果、异常终止信号分开；max_tokens 不自动生成连接断开原因。

旧记录没有新字段就显示已有证据或未记录，不反推开关、不回填失败、不重算账单。普通日志仅公开必要策略摘要，原始头/正文/底层错误仍限 Root。

### 5.2 终止与采样

| 实际观察 | 日志行为 |
| --- | --- |
| 正常 EOF / 正常 WS close，无其他异常 | 异常首因留空，可记正常结束事实，不判断流 |
| 未见完整结束帧，严格关闭 | 未知/未校验，不额外补查或报协议错误 |
| 真实读取/写入失败、短写、取消、服务端超时 | 记录实际阶段、时间和错误，不推断远端发起主体 |
| main 解析器或 SDK 报错 | 按实际处理错误记录，不能因关闭开关吞掉 |
| 非成功 HTTP/握手、拨号失败、尚无响应 | 沿用原错误记录路径，不新增流式私有诊断资格，无正文不补造 |
| 某次尝试/交换异常，后续重试成功 | 按原尝试记录规则保留已有错误关联，最终请求状态按实际结果记录 |
| 出错后网关关闭上游 | 单列 cleanup，不覆盖首个异常 |
| 只有零 usage 或 max_tokens | 可按原规则留诊断，不能仅凭此制造异常中止原因 |

采样维持：单响应头 16 KiB，上游正文首尾共 2 KiB，下游共 2 KiB，每尝试最多四份 HTTP 交换。只复制实际读到/写出的部分，不新增 Read/Write/Flush/Close 次数，返回原 n/err。

流式错误沿用现有默认记录行为，不以严格开关作为过滤条件。私有诊断保留原有成功响应门控及上游异常/拒绝/最终零输出等留存规则，原始头和正文继续脱敏并限 Root；没有取得的内容明确标未采集。关闭态使用处理器已有错误/拒绝信息，不另跑语义解析找拒绝，不用未初始化的 ResponseOutcome!=completed 判断异常。

核查 FlushError、ResponseController、Hijack/WS、ReaderFrom 等接口转发，不能因 wrapper 隐藏能力；无法观察 flush 时标未知，不能额外 flush 求证。采集失败隔离降级，不改变响应与收费。

### 5.3 各路径接入和存储时序

- HTTP/NDJSON 关闭态只装被动原始采集；streamObservedBody 有语义拦截能力，不能继续拿它充当纯日志层。
- AWS 原始 HTTP 与 SDK 解码事件分层；内部重试独立编号，不累计重复 usage。
- Xunfei/Volcengine/Realtime 原始 WS 采集脱离 Active；二进制记录字节，不误作 SSE。
- Responses WS 每 create 建观察上下文。当前终态实际下发可能晚于用量收口，诊断在真正写出结果后冻结；调整原消费日志快照时序，不能另扣一次或写第二条消费记录。
- 入队前复制必要字段，后台不能再读已经复用的 Gin context、轮次 map 或可变采集器。

复用 stream-termination-evidence.tsx 已有的“未记录异常信号”和 cleanup 展示；修正面板 optional bool/number 默认值。保留 Root 主动展开才查询、无持久缓存、普通列表/导出过滤私有内容。

## 6. 数据模型与子系统

不加表、列、索引。新增 options 行及 users.setting/channels.setting/logs.other JSON 属性，仍是持久化行为变化，需要真实 SQLite/MySQL/PostgreSQL 验证；日志存储按部署情况补测 ClickHouse。

不回填历史日志或扫描全表，诊断沿用 request_id + created_at + attempt 和最多 128 条候选读取。管理端只更新目标配置行及已有缓存版本，不更新余额字段，不新增请求内查询。

价格表达式、缓存扣除、工具/媒体价格不改；变动的是进入结算的 usage 及来源。验证须同时看原始 usage、转换后 usage、最终计价参数和真实余额。退款、渠道失败和自动禁用仅由实际处理结果驱动，日志观察不能反向改变路由或触发重试。

## 7. Main Chain Impact

同步：每尝试 O(1) 快照读取和路径选择；既有有界采样、字节计数及请求局部短锁；所选处理器解析与既有资金操作。关闭态无第二套解析或额外逐帧 token 估算；采样锁不跨 I/O。

异步：现有日志队列落库，复用既有资金持久化机制；不新增逐请求写库或工作者。日志失败不能传回 relay，不为策略/采集新建 goroutine、连接、外部调用或无界缓冲。

每连接只有一个上游读所有者和一个下游写所有者，关闭与等待保持可控。恢复协议兼容语义，不恢复旧资源/并发缺陷。

## 8. Shared Resource Audit

| 资源 | 主链现有使用 | 本方案 |
| --- | --- | --- |
| options/配置快照 | 主链读快照，管理端/同步任务写 | 无请求内新查询，完整策略一次发布，写失败不发布 |
| users.setting/profile_version/用户缓存 | 认证与计费共享 users | 管理端目标行局部更新，缓存一致，不改钱包列 |
| channels.setting/缓存 | 分配主链使用 | 既有失效流程，不读上次尝试的 ChannelMeta |
| 钱包/令牌/订阅/BillingSession | 预留和结算使用 | 不新建账本或协调锁，Realtime 一套资金所有权 |
| logs.other/队列/池 | 既有消费和错误日志 | 不新建池/逐请求写库，同一消费日志带最终诊断快照 |
| HTTP/SDK/WS 池 | 转发使用 | 不为采集另拨连接或 drain body，SDK 重试隔离 |
| capture/session/map | 请求/轮次局部 | 无新全局 map，采样和轮次状态有界 |
| Redis | 缓存/额度已有使用 | 无新 key/namespace、无新增 relay 调用 |

管理端短事务不跨上游调用持锁；设置并发修改与扣款在真实 DB 验证无余额覆盖及额外锁等待。

## 9. Concurrency Analysis

30k RPM 约 500 RPS，100k RPM 约 1,667 RPS。相对现状，策略/诊断新增 DB/Redis/外部调用均 0，新 goroutine 0、共享协调锁 0；不代表整个 relay 没有既有调用/锁。

每在途请求原始样本最多约 `4 × (16 KiB 头 + 2 KiB body) + 2 KiB 下游`，另有编码/对象开销及开启态原有协议状态。不能只凭 RPM 估算内存。关闭态不保留严格帧缓冲/工具累计，WS 只保留有界样本和统计。

同负载、响应大小、时长与并发比较开启/关闭/main 的首字延迟、吞吐、CPU、分配、GC、goroutine、连接释放、日志积压。异常风暴单列；队列满按原降级机制，不为承诺“日志必有”阻塞主链。容量推演不是 100k RPM 实测结论。

## 10. 测试设计与验收门槛

实现前先写失败用例。每项登记入口、渠道、实际处理器、传输/转换、策略来源、mock 输入、main 实际输出、候选版本实际输入/输出、日志和资金预期。用处理器分支清单衡量覆盖，不能用请求总数替代。

### 10.1 配置、权限、生命周期

- 用户/渠道三态 × 全局二态共 18 组；缺失与 null 单列，错类型/字符串/数字拒绝，false 保留。
- 新旧键、旧值限原 Claude 范围、优先级、冲突写入、加载顺序、重启/同步、事务失败、缓存失效。
- 自助用户整个 setting 覆盖攻击、管理员边界、设置 scope、Root 诊断及普通查询/导出隐私。
- 进行中改配置、重试换渠道、SDK 重试、头前失败、非 200/101、失败后成功、非流式上游 SSE、WS 多 create/连接复用。

### 10.2 数据和异常分区

| 类别 | 必测项 |
| --- | --- |
| 分帧 | LF/CRLF/CR、任意字节分片、半个 UTF-8、多事件合读、注释/ping、空/多行 data、无末尾换行 |
| 结构 | 空 body/仅 ping、合法/畸形 JSON、工具 JSON 截断、缺失/null/错类型、未知扩展、重复/乱序开始结束、终态后数据 |
| 终态 | 正常、max_tokens/length、拒绝/content_filter、安全拦截、cancelled、各种 incomplete、缺终态 EOF、明确 error |
| 传输 | 拨号/握手失败、4xx/5xx/200 内错误、中断/RST、正常/异常 WS close、各类超时、取消、短写、flush 失败 |
| 用量 | 缺失/明确零/负数/小数/字符串/极大值、字段乱序、累计重复/减少、仅输入/输出/缓存、5m/1h、思考/音频/图片、total 不一致 |
| 工具/候选 | 纯工具、function_call、并行工具、名称参数分片、不完整工具、多候选不同顺序、重复/非法索引 |
| 媒体/任务 | 二进制空/完整/截断、MIME 参数、预览/最终图、图片 JSON 错误、Ali 中间态后成功/失败、SDK 解码错误、任务终态 |
| 上限 | 当前帧/JSON/工具/采样限制边界及越界；严格关闭不绕过基础请求或资金上限 |

每类同时测开启/关闭，开启保持当前严格合同，关闭与等深 main 比较。关闭不保证成功。main 自身负 usage 等异常单列，保留防负收费/溢出，不能为一致性放宽资金安全。

原始 Claude 故障必须列为回归：出现 invalid message_delta sequence，末尾有 message_delta/max_tokens 与 message_stop，上游报告 output_tokens=32000，而当时最终按输出估算 3736 结算。用户粘贴的中间正文已省略，不能仅凭首尾断言具体缺了哪一个 content_block_stop。应使用完整授权样本，或明确标为合成的内容块未闭合/乱序 fixture 重现校验拒绝；关闭后检查 main 实际是否采纳最终 usage，并比较候选结果，不能预先把 32000 或 3736 硬编码成所有情况下的正确收费值。

### 10.3 日志与资金

断言采集不改变读写次数/n/err/正文/flush 时序；无异常可留空首因；unknown 不等于 false/0；采集失败不改变响应；不同尝试/SDK 交换不污染；零输出留诊断不等于断流。

以现有流式错误记录为回归基准：相同实际错误在严格开启/关闭时均进入原记录链路，日志类型、字段、尝试关联、权限及展示保持兼容；没有 StreamResult 或 Active=false 不能成为漏记原因。非 200/101、拨号失败、下游/本地错误、重试成功等按原各自路径验证，私有诊断资格不被意外扩大。未执行的严格检查不伪造错误；已有样本按预算保留，缺失样本不补造，无重复错误记录或重复消费，记录不改变响应/重试/用量/结算。

Realtime 零轮/多轮、重复终态、轮间断开、可恢复错误、预留失败、最终写失败、资金部分提交都核对余额、令牌、订阅、渠道统计、日志、退款和预留释放。Responses WS 最后写失败不能记成成功交付。

### 10.4 最新 main 渠道集成

沿用香港隔离栈及 [main 渠道测试流程](main-upstream-channel-testing.md)。必须是本分支真实配置渠道 → 最新 main 真实配置渠道 → 鉴权 mock；禁止落到付费模型，不改现有生产服务与数据。

等深对照：main 外层 → 同版本 main 内层 → 同 fixture。请求、模型映射、配置/定价一致，仅归一化事先声明的动态 ID/时间，不能删除错误、终态或 usage 制造一致。

main 会归一化、吞掉或拒绝某些 mock 异常，须保存逐跳证据。mock 发过但候选分支没收到，不算候选异常分支已测。必须在 main 出站边界注入的异常使用明确标记的测试故障代理，不冒充未经修改的 main 原始输出。

AWS EventStream、Xunfei/Volcengine 原生 WS 是上游适配协议，未必是 main 对外入口。普通 OpenAI 渠道接 main 不能覆盖候选版本这些原生解析器；需同栈独立原生线协议 fixture/故障边界，证明实际适配器执行。这是补充适配器验证，不能替代 main 渠道验收。无法经 main 原生入口验证的项标“不适用/补充验证”并解释，不能伪造全协议都走过 main。

14 个 main RelayFormat 都有正常/异常回归，各新增处理器补原生协议用例；SSE/NDJSON/WS/SDK/媒体不能相互替代。每次发布用最新已验证部署，记录精确基准，不能用旧二进制冒充最新。

### 10.5 工程及发布检查

- 相关 Go 单元/集成测试及所有权/并发 race 检查；relaykit 修改须 `GOWORK=off go build ./...` 独立验证。
- 真实 SQLite/MySQL/PostgreSQL 测配置、JSON 局部更新、并发、升级/重启。无新 DDL 也检查第二次启动无意外 DDL；记录版本/命令；日志按部署方言补测。
- 三处设置与日志 UI 测继承/保存/失败/加载/旧值/未知/权限；七语言、typecheck、相关交互和构建。复用 Base UI/Tailwind，不改五个 upstream-owned 认证/HTTP 文件。
- 最新 main 对照、异常矩阵和资金核对未完成前，不宣称全协议兼容。旧 2,180 次测试与 Claude 164 组对照只作历史参考。

## 11. 实施顺序与回滚

1. 建立暴露当前差异的测试及处理器清单，冻结 main 和开启态基准。
2. 分离策略、观察、处理所有权，补 unknown 日志合同。
3. 按 HTTP/SSE/NDJSON、Claude/转换、SDK/媒体、Realtime 分批实现，逐批验证收尾和资金。
4. 接入三级设置、限定范围旧键兼容、缓存/API、三个设置入口与日志翻译。
5. 最新 main 隔离渠道、真实三库、前端及容量验证，报告未测项和有意保留的差异。

默认开启，保留旧 Claude 原范围。先指定测试用户/渠道关闭并核对命中来源；全局关闭仍会被更高层覆盖，不是紧急总停用。

回滚不只是替换二进制：旧版本不认识新全协议字段，无法表达非 Claude 关闭。发布前保存非敏感配置清单，回滚旧版恢复对应配置并明确能力丢失，不能承诺无损自动回滚。不删日志、不重算历史账单。

历史验收保持原日期/版本。本轮实现与复核记录如下。

## 12. 实际实现与复核

### 12.1 配置与存储

- 规范键为 `stream_protocol_validation_setting.enabled`，用户和渠道 JSON 字段为 `stream_protocol_validation`。优先级为用户 > 渠道 > 全局，用户/渠道 `null` 代表继承。
- 未保存的全局规范配置用 `null` 区分，不能用启动时默认填充的 OptionMap 判断。一次尝试读取不可变组合快照；管理员更新新旧全局键在同一事务中处理，随后发布快照。
- 旧 Claude 字段在原范围内继续生效；显式迁移才扩展到全协议。新键写入清除同层旧键，旧键覆盖已保存规范键会被拒绝。全局策略组独立保存，不接受和其他模块键混合提交。
- 没有新表、数据库列或迁移。用户管理 DTO 字段使用 `gorm:"-:all"`，持久化到已有 setting JSON；渠道使用已有 settings JSON，全局使用 options。用户自行修改偏好不能覆盖管理员策略。

### 12.2 运行与资金

- `FreezeStreamValidation` 冻结策略，`StreamProcessingOwner` 表达处理所有权，`StreamResponseGate` 独立控制实际成功响应门控。兼容态不安装语义检查，不生成 `StreamResult`，仍保留有界被动采集。
- Claude、AWS、Gemini、xAI、Xunfei、OpenAI 转换及共用用量回退分别处理 main 差异；Volcengine 载荷采集不再依赖 `Active()`。非流式入口不会因上游 SSE 自动成为严格流式请求。
- 已提交响应或请求取消后，`DecideRelayRetry` 独立阻止重试。该保护不依赖会话是否活动。
- Cohere/PaLM 兼容路径保留 main 的解析、软错误和结束帧，但读取任务使用可取消发送并在返回前关闭响应、等待退出；PaLM 返回文本在读取任务退出后取值，避免取消后的协程滞留和数据竞争。取消用例先复现失败，修正后两个包的 race 测试通过。
- Realtime 兼容态使用 main DTO/计数规则，共用受管连接所有权；按第 13 节 2026-11-13 的用户要求恢复逐轮实扣及最终结算。兼容态转发并累计重复事件，严格态才使用有界 128 项去重窗口及累计预留。负数和溢出拒绝更新累计用量，保留资金安全。
- Responses 原生 WS 标为额外严格策略不适用，每个 create 重新建立观察器；日志在终态实际写出/失败后冻结，资金结算不因此重做。

### 12.3 日志及复核修正

- 原流式错误记录继续保存实际读写、解析、上游错误和超时。关闭态未知结束保持未知，不根据零输出或空 outcome 制造中止原因。
- 私有正文保持原成功响应门控与资格；下游/本地错误且非零输出不会单独扩大正文留存。上下游正文、响应头和 SDK 响应份数沿用原预算。
- 复核修正了 Gin 包装器吞掉底层 FlushError 的问题；保持写出及 flush 合同，并记录实际失败。
- 复核修正了兼容扫描器替换 StreamStatus 导致被动错误丢失的问题，新增测试先失败后通过；兼容态复用本次尝试已创建的状态。
- 兼容扫描器的软解析/转换错误现在显式登记被动诊断，仍按 main 继续或停止，不由 logger 回调驱动业务状态。Cohere、PaLM、Ollama 和 AWS SDK 的独立解析/读取错误在原出错点登记；保留原错误返回和结束帧，补测缺终态、损坏 JSON 后继续输出及正常/异常 WS 关闭。
- Realtime 可恢复 provider error 只转发并记录，不制造严格终态；结算选定用量复制给日志仅用于既有零输出留存资格，不重新选择计费。
- Volcengine 实际异常断连不再因关闭策略而漏记；正常 WS close 在兼容态不生成协议不完整错误。Responses WS 本次请求的 provider/control error 和上游写失败进入观察记录，其他响应的错误不污染当前状态。
- 前端三处设置及日志详情已接入；未校验/不适用/未知分别显示，旧 Claude 配置迁移按钮保留原开关值。七种语言均已更新。

### 12.4 验证边界

单元、数据库、前端和香港渠道证据记录在 [全协议实现验收记录](all-protocol-stream-validation-results-20261110.md)。关闭策略不撤销本分支原有错误提示规范化：当前请求 ID 归一、空错误类型映射等仍与官方 main 有差异，逐事件对照保留这些差异，不删除 error/usage/终态来制造一致。

本次未做 100k RPM 实际压测。策略快照不增加热路径数据库/Redis 请求或 goroutine；每请求仍使用原有有界样本，日志继续走现有队列。Realtime 复用原有两个 reader，未增加第三个读取者。上述是资源与并发代码审计结论，不是容量实测结论。

## 13. 2026-11-12 审计修复范围

用户要求：官方 main 本身存在的问题保留原样，避免改变计费。对照基准为官方 `a9932d1f98276d93ff4f61e04cddfbd04de01467`。本节覆盖此前允许兼容态重复终态去重的表述；历史验收数据不改写。

修复目标与数据流：

- xAI 兼容态逐帧直接复制 main 的输入/输出用量明细，包括后帧省略字段时重置；严格态仍按已报告字段合并。
- Realtime 兼容态逐个累加 response.done，包括重复 ID。11 月 12 日采用累计预留；下文 11 月 13 日的实际扣款要求覆盖该实现。严格态去重不变。
- Realtime 观察记录按实际读取、处理、取消、超时原因分类，不把所有错误写成 scanner_error；不影响转发和计费。
- 错误日志封装保留已经采集的原始错误，仅在缺失时补外层错误；严格已冻结结果继续优先。
- Realtime 兼容态 total_tokens=0 时仍按 main 记录最终消费日志 quota=0。11 月 13 日恢复逐轮实扣后，这不代表实际钱包扣款为零。本分支收入/佣金流水保持最终消费日志口径，不推断真实供应商成本或宣称它等于钱包总扣减。
- 验收分类区分 main 自身失败与候选同现失败；关闭态所有 SSE/WS 用例比较同深度参考的实际账单，包括异常、缺账单、重复账单，不只比较正常用例。

不修复：Nova 空数组访问、main 既有结算失败处理、WS 写截止缺失、讯飞未完成业务后正常关闭的错误处理、Responses WS 既有错误持久化/策略成功时序/跨 create 终态隔离问题，以及 total_tokens 与分项矛盾本身。不扩大原始正文留存资格。

API、认证、配置、模型及迁移均无新增或变更。测试先固定反例，再实施局部修正；验证正常/省略/显式零明细、重复终态、读取/解析/预留错误、原始诊断保留、零收费与普通收费账本，并运行相关现有套件。2026-11-12 已完成本地隔离实际渠道矩阵及专项，见 [验收记录的修复后对比](all-protocol-stream-validation-results-20261110.md#2026-11-12-修复后渠道对比)；香港远端仍待凭据，不以本地执行冒充远端验收。

2026-11-12 的对照更新到官方 `7b857f5a1e91a1109189d06f1fc1abca50d53f96`，候选业务代码及二进制未变。完整矩阵 4,376 请求，关闭态 484 组日志账单一致；独立超时补测另有 4/12 组实际差异，不能据主矩阵宣称全部兼容。钱包专项确认候选自身 9 场景账实一致，但官方 main 的流内扣款与最终结算使正常 Realtime 每层实际扣款 236、日志 118；重复终态为 472/236，零总量正分项为 118/0。此前仅对齐日志，没有达到真实扣款等价。

### 2026-11-13 用户明确的扣款兼容范围

用户要求先确认是否少计费；不能保证所有情况均不少计费时，按官方 main。有限用例不能证明所有输入的全称保证，因此兼容态恢复官方逐轮 `PreWssConsumeQuota` 实扣以及结束后的 `PostWssConsumeQuota`，不以本分支累计预留抵销官方流内扣款。每轮传本轮用量，包含重复终态、零总量正分项和缺失 usage 的本地回退；中途失败后的收尾也按官方累计/重试顺序。最终消费日志仍仅写一次，不能用日志额度替代钱包/令牌真实扣减的验收。

严格态继续使用累计预留和唯一结算。零总量最终日志/收入仍为零，与官方日志口径一致；逐轮实扣是官方既有差额，不另造成功日志去掩盖。测试分别报告官方兼容性与钱包/日志自洽性，后者的既有失败不改判。无需新增配置、API、表列或迁移。

长停顿反例进一步确认：兼容态新增的 `StreamingTimeout` 空闲计时器会在 3 秒提前关闭 WebSocket，4 秒后到达的用量无法被处理，实际扣款从官方每层 236 变成 0。移除兼容态的额外空闲计时器，继续服从实际连接关闭、读写失败及请求 context 取消/截止；严格态的空闲超时保持原样。该反例通过真实渠道 `realtime_delayed_round` 保留修复前后证据。

此变更重新使用已有 `PreWssConsumeQuota` 路径：每个终态同步读取用户及令牌余额，调用原钱包/订阅及令牌结算函数；缓存、异步批量落库、原连接池均复用原实现。没有增加 goroutine/锁或新共享资源。相对前一兼容实现，每轮增加两次已有缓存/余额读取，并将累计预留替换为逐轮实扣；在未命中缓存或禁用批量写时可能进入既有数据库操作。这是按用户要求恢复官方资金路径的明确取舍，不能继续声称该调整新增 DB 调用恒为零。30k/100k RPM 下额外工作随 Realtime 终态数量增长，未做容量实测；开关仅影响兼容态。

最终对照基准为官方 `0bda086fb4c2f83ac0397e4a60d90686578d67ad`。实际扣款专项 18/18 配对一致；完整矩阵 4,376 次请求中，关闭态用量和账单 483/484 配对一致，剩余一组为同截止竞争，已用 main 对 main 和控制先后顺序的真实逐帧对照复现。原有错误提示差异、main 同现异常及原始失败均保留，详见 [最终完整对比](all-protocol-stream-validation-results-20261110.md#最终完整对比与交付边界)。该结论只覆盖本地隔离夹具及已测配置，不代表全输入、全部插件配置或容量保证。

### Main Chain Impact

用量赋值、累计、错误原因及日志快照在既有调用点同步执行。Realtime 兼容态每个终态恢复 main 的余额检查和逐轮实扣；可能访问现有数据库/缓存，具体取舍见 11 月 13 日范围。严格态不改。异步消费日志及成本/佣金仍走原队列。

### Shared Resource Audit

不新增 Redis 命名空间、共享缓存、连接池或 goroutine。沿用请求局部 usage、StreamStatus、采集器短锁及原日志队列；不跨网络操作持锁。用户/令牌/订阅资金走已有 `PreWssConsumeQuota`/`PostConsumeQuota`/最终结算路径，额外余额读取与现有资金链共用缓存和连接池；无新 SQL、索引、事务或协调锁。收入记录依旧关联最终消费日志，保留官方日志与实际扣减的差额。

### Concurrency Analysis

明细复制和分类为常量工作，错误封装复用原有有界快照。Realtime 保留两个有界 reader，无新增 goroutine；30k/100k RPM 下恢复的两次余额读取和逐轮实扣次数随终态数量增长，不能宣称新增 DB/Redis 调用为零。单轮使用现有缓存/批量结算机制，没有新增全局锁或无限队列。此为代码资源分析，非容量压测结论。

### 本轮验证

- 新增反例先在修复前失败：xAI 明细、Realtime 重复终态/错误类别、诊断错误覆盖、零收费正收入、分类器 main 自匹配及异常账单遗漏；修复后对应回归通过。
- `go test ./relay/channel/xai ./relay/channel/openai -run 'TestXAIProtocolDisabledUsesMainUsage|TestRealtimeMain' -count=1` 通过。新增 context 取消/截止测试通过；严格 Realtime 本地 WS 15 场景（含重复终态、异常关闭、写失败、取消、超时）通过，unexpected=0，日志 `_local/stream-policy-fix-20261112/strict-realtime.log`。
- `SQL_DSN=<isolated PostgreSQL> LOG_SQL_DSN=<same server> go test ./service -run 'TestStreamPolicy|TestRealtimeMain' -count=1` 通过。
- `go test ./relay/... ./service/... ./controller ./setting/operation_setting ./i18n -p=1 -count=1` 全部通过，service 使用隔离 PostgreSQL 主库/日志库；日志 `_local/stream-policy-fix-20261112/backend-postgres.log`。控制器套件耗时约 229 秒，最终正常完成。
- 零总量保留 main 的 0 收费、正常收费以及成本/收入一致性在真实 SQLite 3.50.4、MySQL 5.7.44、PostgreSQL 17.11 上通过。PostgreSQL 运行正式测试；SQLite/MySQL 的验证使用 `_local/stream-policy-fix-20261112/overlay-db.json` 扩展测试入口，SQLite 使用独立内存库，MySQL 使用独立 utf8mb4 数据库。原 MySQL 临时库的默认 latin1 字符集无法建含中文默认值的既有表，仅修正临时夹具，不修改业务迁移。无新增 schema/索引/查询或 ORM 依赖，因此不涉及升级迁移验收。
- 三库集中命令：`go test -overlay=_local/stream-policy-fix-20261112/overlay-db.json ./service -run '^TestRealtimeMainRevenueMatchesFinalCharge$|^TestStreamPolicyDatabaseVersion$' -count=1 -v`，分别配置隔离 DSN。版本与结果位于同目录 `sqlite.log`、`mysql.log`、`postgres-version.log`；PostgreSQL 账本测试也包含在正式 service 套件中。
- `python -m unittest discover -s scripts/testing/main-channel -p test_acceptance_audit.py`：5 项测试通过，含 SSE/WS 正常与异常用例、漏行/重复行和严格态排除。
- 未修改 relaykit、前端或生产部署；未重跑完整远端渠道矩阵，不能宣称全协议验收通过。

### 2026-11-15 全工作区验收补充

本次对最新官方 `b388140acfd80369ec6243b16869daff1b4051f4` 重新构建并运行本地实际两跳：完整矩阵 4,376 请求、18 个策略优先级场景以及 38 次钱包专项。钱包专项官方兼容性通过，账实一致性保留 39 条失败断言，正常 Realtime 单层仍为钱包/令牌扣减 236、最终消费日志 118。这确认了 11 月 13 日明确保留的官方行为，不是新引入的候选专属回归；也没有授权改变既定资金行为。

完整协议矩阵仍有 138 条失败断言，已分类官方自身、候选同现以及既有错误表示差异，未改判原结果。30k RPM 目标的短时本地 JSON/SSE 探测未达到计划负载，不能认证容量。详细构建身份、失败、三库测试和范围限制见 [上线审计](release-audit-20261115.md)。

2026-11-15 用户进一步明确：“最新官方 main 同样复现的先不处理”。因此 Realtime 兼容态账实差异及协议矩阵中已确认的官方同现问题暂缓修复，维持当前兼容行为，保留原始失败证据；不把候选与官方之间的其他差异自动归入暂缓范围。该决定不改变严格态，也不代表全协议或生产容量验收通过。
