# Claude 流式严格校验三级配置

2026-11-11 更新：已按用户确认的 [全协议方案](all-protocol-stream-validation-overrides.md) 扩展规范配置；本文件保留旧 Claude 局部实现的历史记录。旧键仅兼容原 Claude 范围，不静默扩大作用域；当前实现和验证状态以全协议文档及其验收记录为准。

历史状态（Claude 局部阶段，2026-11-10）：用户已确认实施，代码与本地验证已完成；当时未部署、未修改线上配置或历史账单。

## 目标与范围

用户要求重新提供严格校验开关，支持全局、渠道、用户三个级别。本方案针对本次讨论的 Claude 上游流式协议校验，覆盖原生 Messages 和 Claude 上游转换为其他协议的受管路径；其他供应商协议、非流式请求和请求 JSON 校验不受影响。

当前 `relay/channel/claude/strict_stream.go` 在内容块未关闭时拒绝 message_delta。相同模拟事件在官方 upstream/main `bd6bb5301` 可继续转发最终 usage 和 message_stop。本轮检查已覆盖文本/工具块、关闭/未关闭、普通读取/CRLF 逐字节读取，两边各 8 个用例；这不是本次线上完整响应的重放。

**核心验收要求：有效开关为 false 时，Claude 流式响应按官方 QuantumNous/new-api 的 main 逻辑处理。不是在本分支严格状态机上选择性放宽几条规则，也不是仅修复本次 message_delta 报错。** 开启时继续本分支严格处理。

关闭时需要对齐官方的扫描、事件处理、协议转换、错误传播、结束处理、usage 合并与缺失用量估算；本分支附加的协议观察不能再拦截事件、补发 main 不会发送的错误，或覆盖 main 选定的用量。保留日志采集作为旁路观察，不重新引入历史 capture_response 开关，不修改历史账单。账号权限、管理员配置的计费价格/倍率、钱包安全和一次结算等网关业务保持现有合同，不属于把整个项目切回上游的范围。

官方基准是本次已拉取的 upstream/main `bd6bb5301`，不是本地 main `7aaa53326`。实施前重新核对远端 main；如有相关变化，先更新固定对照提交及夹具预期。代码不在运行时获取 GitHub；后续升级官方版本须重跑差异测试。

## 配置与优先级

采用已确认的 **用户显式设置 > 渠道显式设置 > 全局默认**，三层均由有相应管理权限的管理员配置，普通用户不能自行修改。

| 层级 | 字段 / 持久化位置 | 默认和取值 |
| --- | --- | --- |
| 全局 | `claude_stream_validation_setting.enabled`，setting/operation_setting 注册快照，现有 options 表 | true；开启 / 关闭 |
| 渠道 | ChannelSettings 的 `claude_stream_validation`，channels.setting JSON | nil / 缺省为继承；true 开启；false 关闭 |
| 用户 | UserSetting 的 `claude_stream_validation`，users.setting JSON | nil / 缺省为继承；true 开启；false 关闭 |

DTO 使用 `*bool`，不把 false 当作未设置。管理员 API 的局部更新另外区分字段缺失与显式 null：缺失保留旧值，null 清除覆盖并恢复继承，true/false 写入覆盖。

全局开关是默认值，不是强制总闸；例如用户显式开启时，即使渠道或全局关闭，该用户仍执行严格校验。需要全部恢复严格模式时，应清除局部覆盖并开启全局，界面文案必须明确这一点。

默认全局开启、用户和渠道继承，升级不改变现有行为。使用新配置名，历史已退役的 `claude_stream_setting.enabled` / `stream_error_setting.enabled` 及 capture 键仍不生效，避免旧数据库中的 false 意外改变升级后的策略。

已有 `claude_stream_compat_setting.enabled` 仅在严格模式内继续控制本分支的工具交错和 EOF 分隔符兼容。新开关关闭时按官方 main 行为，不应再受该本地兼容开关二次限制；测试覆盖两种旧开关值下关闭模式都与官方一致。切换官方处理路径仍须保留旁路诊断和计费证据。

## 数据流

管理员保存 → 参数和权限校验 → 更新原有 options / channel setting / user setting → 现有配置快照或缓存发布 → 请求认证读取已有用户缓存 → 渠道选定 → 每次尝试解析并冻结最终校验值及来源 → 严格处理或官方 main 处理 → 选定用量交给既有资金结算、异步日志。

解析时从当前尝试的渠道上下文读取 ChannelSettings，不能使用上一次尝试遗留的 ChannelMeta。重试换渠道必须重新解析；全局配置每次尝试读取一次，用户设置使用请求已取得的认证快照，不增加数据库或 Redis 查询。进行中的单次尝试不因配置修改而切换状态机。

## API contracts 与权限

复用现有路由，不新增公开接口：

- `GET /api/option/`、`PUT /api/option/group`：沿用 AdminAuth、RequireSystemSettingsScope 和页面权限。页面使用 `request-policies.filtering`，保存体为 `{"scope":"request-policies.filtering","module":"claude_stream_validation_setting","values":{"enabled":"false"}}`。单键配置接口也使用同一 option scope；不通过空 scope 规避权限。
- 渠道创建、详情、更新接口：复用现有 `/api/channel/` 管理权限和 setting JSON。JSON 内新增 `claude_stream_validation`，创建缺省为继承；更新 setting 时合并该字段，不能因旧客户端不认识它而丢失配置。null 表示恢复继承。
- `GET /api/user/:id`、`POST/PUT /api/user/`：复用用户管理权限及目标账号角色限制。管理员 DTO 增加顶层 `claude_stream_validation` 三态字段；后端将它合并到目标用户的 setting，详情只投影该字段，不额外暴露通知密钥等完整设置。缺省保留、null 继承、true/false 覆盖。
- `/api/user/self`、`/api/user/self/setting` 不提供修改入口；不能经原始 setting 字符串绕过管理权限。个人偏好保存也不能清除管理员配置。

沿用 `{"success":true,...}` 和 `{"success":false,"message":"..."}` 返回约定。非法值使用现有参数错误翻译；底层失败先记录日志，再返回已有可读错误。新用户文案若需要新增，后端 en/zh 一并覆盖。

## 数据模型、写入与缓存一致性

不新增表、列或索引；两个 DTO 位于 relaykit/dto，只增加可选字段，不引入根模块依赖。存储仍为现有 TEXT JSON，禁止添加数据库 JSON 专用操作符。

管理员修改用户配置在现有用户更新事务内，通过 `lockForUpdate(tx)` 读取最新 setting，合并单个字段并递增 profile_version；不将请求提供的整个 setting 写回。个人偏好 `UpdateUserSetting` 和通用 `UpdateWithTx` 同样在写事务内读取最新策略并保留，避免陈旧表单覆盖管理员设置。实际采用项目已有的行锁抽象而非 CAS；SQLite 按既有无 FOR UPDATE 分支处理。锁只出现在管理/个人资料写入，不新增 relay 路径行锁。数据库冲突返回原有保存失败响应，不静默重试写入。

提交后使用既有 PublishUserAuthCache 发布完整用户快照；渠道更新复用原渠道缓存刷新，多实例行为保持现有同步约定。缓存发布失败不能假报完全生效。配置读取或历史值损坏时回落到上层默认，不因配置解析错误阻断主请求；管理端拒绝新写入非法值。

变更涉及既有 JSON 字段持久化和缓存，仍需真实 SQLite、MySQL、PostgreSQL 验证，不能用无 schema 变化替代数据库行为验证。无需新索引：读取/写入均沿用主键定位，没有新过滤、排序或聚合查询。

## 严格模式与官方 main 模式

严格开启：保持现有块序列、索引、工具输入校验、终态要求及错误反馈。

严格关闭：进入与固定官方 main 对等的响应处理路径，包括扫描器、HandleStreamResponseData、HandleStreamFinalResponse 及对应转换器。优先复用已经对齐上游的代码；本分支中同名函数不代表行为相同，必须核对其依赖链和出口。不能只让 UseStrictClaudeStream 返回 false：通用 StreamSession、managed_stream、透明 Body 包装和最终结算适配也不得继续施加本分支额外的协议拦截。

实现与验收边界：

- 本次未关闭块后 message_delta(max_tokens) + message_stop 的样本应与 main 一致，转发真实终态并采纳 output_tokens=32000。
- EOF、缺少 message_stop、上游 error、解析失败、取消、超时与写出失败逐项以官方真实行为为准；不能沿用上一版设计中“仍按本分支既有失败路径补发错误”的要求。例如 main 原生路径缺少终态时可能只记录未完成状态而不向客户端补发错误，这一差异不能被本地 finalizer 覆盖。
- 下游内容、事件顺序、是否发送错误或结束事件、错误返回和最终 usage 均进入对照；不只比较最终 success 状态。main 的转码/usage 补全可能改写原始 JSON，不以本分支字节透传习惯替代 main 行为。
- 缺失用量估算、起始/最终 usage 的覆盖和缓存计数规则采用 main 的对应逻辑，不再由本分支严格交付判定把已采纳的上游用量改回估算或清零；实际资金仍由现有唯一结算入口按管理员价格计算。
- 原生和 Claude 转 Chat/Responses/Gemini 等已有路径都使用该策略。转换器的错误与收尾也以对应 main 路径为准，不以开关承诺恢复上游未生成的内容或工具参数。
- 日志观察只能记录事实，不能通过 ProtocolError、关闭 Body、改写终态或重选用量反向控制 main 模式。观察状态与主响应处理状态分离，失败不能影响转发。
- 保留已有额度防溢出、资源预算及部署超时等基础保护，但不得借此保留普通事件上的本分支严格策略。若与 main 在这些保护边界存在差异，必须在设计和验收中逐项列明，不能把有差异的边界宣称为完全相同；需要扩大保护行为变更时先说明。

诊断继续保留处理器选定用量、终止来源和有界正文采样。日志 admin_info 附加 `claude_stream_validation` 及 `claude_stream_validation_source`（user/channel/global）。main 模式不创建严格 `StreamResult`，采集器不可重选 usage、发送错误或控制连接。缺少严格 usage_phases / delivery_evidence 时不伪造；业务日志仍记录处理器最终计数。正常非零响应沿用现有正文不持久化规则；异常、拒绝、零输出保留有界采样。

## 前端交互

全局在管理端请求策略页面添加“Claude 流式严格校验”开关，默认开启。渠道编辑和用户编辑的请求设置区域添加三态选择：“继承 / 开启 / 关闭”。用户继承说明为“跟随渠道设置，渠道未设置时跟随全局”。关闭说明明确为“按官方 main 的流式处理逻辑转发”，不能只写“放宽校验”。

复用 SettingsForm、SettingsSwitchField、已有三态 Select 和编辑表单保存区，不新增弹窗或特殊保存动作。保留现有保存中禁用、错误提示、dirty 状态和权限展示规则。渠道和用户表单的加载、重置、复制及保存序列化须保留 false 与继承的区别。

表单类型、schema、API payload、默认值和回填逻辑一并修改；不会修改 upstream-owned 的 auth-session、http-client、api 等文件。遵循 shadcn-ui、vercel-react-best-practices、i18n-translate 技能；新增文案覆盖 en/zh/zh-TW/fr/ru/ja/vi，翻译变更保持本功能范围。

## Main Chain Impact

同步部分仅为每次尝试读取一次配置快照、两个已有设置字段和固定次数的优先级判断，随后选择严格或官方响应处理路径。新增外部 I/O、DB/Redis 调用均为 0；策略选择不等待完整响应、不额外读取或排空上游、不加入重试。

异步部分沿用已有日志和诊断队列。开关及旁路观察不新增 goroutine、定时器、worker 或连接；官方处理器与严格处理器原有的工作者和缓冲数量可能不同，实施时记录实际差异并做并发验证，不能同时启动两套读取器。取消、清理和连接回收按选中的路径执行。

## Shared Resource Audit

| 资源 | 与主链关系及处理 |
| --- | --- |
| options | 管理端写独立配置键；主链只读不可变原子快照 |
| users.setting / profile_version | 与认证缓存共用；仅管理操作按主键更新，不改余额字段，不在主链查库或加锁；避免全行覆盖 |
| channels.setting | 与渠道分配缓存共用；沿用管理更新和缓存失效，不增加 relay 读写 |
| 用户 Redis 缓存 | 复用现有字段序列化与版本化发布；无新 key、扫描或逐请求读写 |
| 用户/渠道内存缓存 | 复用已有不可变快照/同步机制；不就地修改共享 DTO 指针 |
| 请求块 map / 参数缓冲 | 仅属于当前尝试，保留现有容量和字节上限，终态释放 |
| 日志、账务、HTTP/DB/Redis 池、worker | 沿用现有调用，无新增连接或任务，不挤占额外容量 |

## Concurrency Analysis

30k RPM 约 500 次/秒，100k RPM 约 1667 次/秒。策略解析为每尝试 O(1)，该解析新增 DB 0、Redis 0、goroutine 0、网络 0、跨请求锁 0；请求中不扫描用户列表或渠道集合。关闭路径的队列、工作者和内容累计开销按官方实现单独测量，不能将两种处理器误称为只差一次每帧布尔判断。

不以 RPM 替代并发流数评估：平均流长 T 秒时约 1667×T 活跃流。开关本身只增加请求局部布尔值和来源，诊断不额外缓存完整流。解析基准约 4.9–5.1 ns/op、0 B/op、0 allocs/op（Windows amd64，Ryzen 7 8700G）。严格原生路径使用一个读取任务；main 扫描器沿用官方的读取任务、转换任务、容量 10 的 dataChan，以及按需 ping 任务，绝不同时启动两套扫描器。关闭路径的 ResponseText 仍按 main 累计，不宣称它拥有严格解析器相同的累计上限。既有单帧扫描上限、写超时和有界诊断保留。完整 relay/helper/common/claude/service 回归通过；未进行生产 100k RPM 容量压测。

## 测试设计与验收

实现前先写用例；使用 testify、httptest 和内存 SSE，不调用真实 AI 服务。

1. 优先级：全局 2 × 渠道 3 × 用户 3 共 18 种组合；显式 false、继承、缺失、null、非法类型；修改其他字段不得清除覆盖。
2. 生命周期：换渠道重试、流中修改配置、新尝试读取新配置、入口模式冻结、缓存冷/热读一致；普通用户不能写入管理员字段。
3. 本次复现：文本/工具块未关闭，后接 max_tokens + 32000 usage + message_stop；开启仍报同一错误，关闭转发真实终态并采纳 32000，确认输入 2 和缓存 77864 不变。
4. 官方差异验收：将同一组夹具分别送入独立官方 main checkout 和关闭模式，比较下游事件/内容、错误返回、终态、usage 及交给资金结算的计数。覆盖正常流、缺 stop、错误 JSON、缺失/显式零 usage、上游 error、超时、客户端断开、读写失败、工具参数截断。仅规范化请求 ID、时间戳等非确定字段，不过滤业务差异；严格开启另与修改前基线对照。边界保护的例外须单列。
5. 分帧与资源：LF/CRLF/逐字节、末尾缺空行、交错工具、重复索引、活动块与字节边界；关闭严格校验不突破内存上限。原生 Claude 与 Claude 转 Chat/Responses 等现有适配路径分别覆盖。
6. DB：真实 SQLite/MySQL/PostgreSQL 验证新旧 JSON、true/false/null、管理更新与个人偏好并发、缓存版本刷新；新库/旧库连续启动两次确认无额外 DDL。记录版本、命令和结果；日志库未改 schema/SQL，验证原有诊断路径即可。测试数据隔离，不修改真实用户或渠道。
7. 前端：三态回填、保存、继承恢复、false 不丢失、权限和失败保留表单、七语言键检查。运行相关 Vitest、`bun run typecheck`、`bun run lint` 和构建。
8. Go：相关 setting/relay/common/relay/helper/relay/channel/claude/service/controller/model 测试和根构建；relaykit DTO 改动后必须独立执行 `GOWORK=off go build ./...` 及相关测试。支持环境执行 race。

## 发布与回滚

默认发布保持严格开启，先由管理员为目标渠道或用户关闭后验证真实响应的结束事件和用量。若兼容模式出现问题，将该局部覆盖设为开启或继承严格的上层配置；设置对缓存传播后的新尝试生效，不承诺中途改变现有流。全局关闭不会覆盖显式开启的用户/渠道。

本方案替代 `stream-diagnostics-always-enabled.md` 中 Claude 严格校验不可配置的部分，诊断固定采集保持不变。

## 实施与验证记录

### 异常矩阵发现的协议串线

2026-11-10 扩展异常夹具时，严格开启的 Claude 转 Chat、Responses、Gemini 分支发现 18 项失败：实际 Claude 上游错返回其他供应商协议时，通用观察器自动识别其终态，转换器忽略不认识的数据后仍可能补发成功结束并估算输入。原生 Claude 严格解析器已能拒绝这些数据。

修复方案：只有实际 Claude 流处理器且严格开关有效开启时，在请求私有的 StreamSession 标记 RequireClaudeProtocol。通用观察器在记录用量、终态之前拒绝缺少 Claude type 的普通数据，以及 Chat choices、Gemini candidates/usageMetadata、Responses response.* 结构；供应商显式 error 仍沿用原错误路径，未知且有字符串 type 的未来 Claude 事件保持可兼容。关闭路径不设置此约束，继续使用固定 main 基准。添加四种输出协议、两种读取分片的先失败回归，再修复。

Main Chain Impact：标记在开始读取前同步写入一次，校验复用已有 JSON 解析结果及会话短锁，不新增 JSON 解码、网络、数据库、Redis、goroutine 或公共缓存。字段只属于单次请求会话，不访问跨请求共享资源；其他供应商未设置标记。100k RPM 下每事件仅固定字段判断，没有新增排队和锁竞争源。

实现入口为 `FreezeClaudeStreamValidation`、`BeginStreamAttempt` 和两个 Claude stream handler。全局原子快照每次尝试读取一次；渠道取当前上下文，用户取已有认证快照。只有实际 Claude 响应处理器会停用语义观察和受管写入器，其他供应商保持原有策略。关闭时跳过原生严格解析、通用协议拦截和严格最终用量重选，复用与固定 main 对齐的扫描、转换、usage 收尾。

官方对照提交：`bd6bb53019fb7e21804b31ab7365c9b2097f4b6c`。`stream_policy_test.go` 的 440 个用例比较下游内容 SHA-256、错误返回和完整 usage，仅规范化时间戳。覆盖 55 个场景 × 4 种输出格式 × 普通读取/CRLF 逐字节读取，包括未闭合文本/工具块、缺 stop、错误 JSON、上游 error、缺失/零/负数/错误类型/溢出 usage、空流、ping、末尾缺换行、重复和非法索引、跨协议数据、SSE 变体、读失败、写失败、取消、idle timeout。通过真实 `BeginStreamAttempt` 包装和 `FinalizeStreamUsage` 后仍与官方一致。重录命令：`python scripts/testing/record-claude-main-parity.py <固定官方 checkout>`；脚本检查提交并使用只读 Go overlay，随后运行 `go test ./relay/channel/claude -run TestClaudeMainStreamParity -count=1`。

严格开启另有 `TestClaudeValidationEnabledFaultProtocols`：33 个场景 × 4 种输出格式 × 逐字节/4 KiB 读取，共 264 组，检查资源关闭、只发一次错误、不伪造成功终态、重复收尾不改用量、零交付归零。`TestClaudeRequiredProtocolBoundaries` 检查新约束的字段边界和其他供应商不受影响；`TestClaudePolicyKeepsOtherProtocolSessions` 对全部 14 个 RelayFormat、开关两态检查入口不误停用其他协议会话。

明确边界：部署侧分用户超时与当前请求超时所有权仍由本分支控制；main 模式不恢复官方统一 timeout 配置。写超时、扫描上限、管理员价格/倍率、额度安全、日志权限和账务入口保持本项目实现。空流和缺失 usage 的补全也遵循 main：例如空流可能返回估算输入量，不能继续宣称关闭模式仍执行严格路径的零响应归零。main 无严格块/累计参数检查，无法补回上游未生成的工具参数。

验证结果：

- 18 种优先级组合、尝试快照/换渠道、旧兼容开关两态、诊断不改 usage、普通用户不能改管理策略、缺省保留/null 清除/false 保存均通过。
- `go test ./relay/common ./relay/helper ./relay/channel/claude ./service/settingsaccess ./setting/operation_setting ./service -count=1` 通过；新增管理/持久化测试在隔离 MySQL、PostgreSQL 实例通过。SQLite 真实引擎单独覆盖用户/渠道保存、配置保存、并发偏好保护。数据库：SQLite 3.50.4、MySQL 8.0.46、PostgreSQL 17.11。个人偏好与管理员策略并发写入后，管理员 false 不会被陈旧 true 覆盖。
- 无新增持久化列；`User.ClaudeStreamValidation` 标记 `gorm:"-:all"`，策略存于现有 JSON。用变更前 HEAD 的 User 模型在隔离表创建旧 schema，再以当前模型连续 AutoMigrate 两次：SQLite/MySQL 均 0 DDL；PostgreSQL 每次对既有 `access_token char(32)` 重发 ALTER，变更前模型复测同样出现，属于已有迁移行为，本次未修改。日志数据库没有新 schema/SQL。未对生产 schema 做演练、未部署迁移。
- 根 `go build ./...`、relaykit 中 `GOWORK=off go build ./...` 和 `go test ./dto` 通过。
- 前端 36 个相关 Vitest 通过，七语言新增 4 个键并完成 i18n sync；`bunx tsc -b`、`bun run lint`、`bun run build` 通过。现有 `bun run typecheck` 脚本调用不存在的 `bun check -b`，使用等价 TypeScript 构建检查；lint 仅有 7 条其他文件的既有 warning。
- 首次 Windows 验证因 CGO 未启用且没有 C 编译器，未运行 race；后续上线前验证已在 Linux CGO 环境通过相关策略和 main 对照 race。未进行完整线上流量重放；用户贴出的响应中段被省略，本次为同类合成夹具。

共享资源复核：无新 Redis key、连接池、表、索引或逐请求外部 I/O。用户写事务沿用 users 主键；发布完整用户缓存仍由既有 post-commit 路径执行。新增前端字段未触碰受保护的认证与 HTTP 客户端文件。工作区原有超时改动不属于本次功能。

上线前全量回归、Linux 产物实例、三态页面保存、并发结算及 race 的单次验证记录仅在本地保留；可重复执行的测试和协议样本随实现提交。
