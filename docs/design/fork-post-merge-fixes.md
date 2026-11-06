# 本分支与合并回退修复（2026-11-05）

## Goals and scope

用户已授权直接修复本分支缺陷和合并引入的问题。以合并前 `0f9cf5796`、上游 `706144769`、合并提交 `379c8f2c6` 和当前工作区作比较。本轮修复未完成输出被算作交付、xAI 输入约束和状态脱敏回退，以及 Realtime、strict Claude、补发错误的诊断缺口。修复合并后不匹配的测试夹具。Responses 限制终态缺失输入（B4）只有构造回包证据，后续按用户“未证实先证实”的要求撤回本轮提前增加的补估逻辑，保留原有计费行为。

上游独有及两个父分支共有的问题暂缓。用户已恢复 `/issues`，保留该路由及权限声明。现有未提交工作区修改保留。

## Data flow and API contracts

请求继续走现有路由、鉴权和受管流开关。Responses `output_item.done` 中明确未完成的工具或图片不会单独形成有效交付，保留未声明 status 的既有兼容行为；先前完整工具参数已交付时仍沿用原策略。xAI 请求构建恢复数量与时间戳检查，原生状态返回公开任务 ID 并移除上游成本；保留已计费失败的级联信号和上游新增的 202 pending 包装。

诊断只记录已经成功写出并刷新到下游的事件。补发错误后更新最终证据；strict Claude 从专用状态机生成同形证据；Realtime 汇总已结算轮次，连接结束时保留用量完整性和交付证据。诊断沿用现有日志队列与管理员展示，不增加接口或鉴权层级，不改变费用策略。

## Data model and configuration

无新表、字段、索引、迁移或配置。修复使用现有受管流和 Claude 兼容配置，不引入另一套开关。xAI 恢复原有插件契约。诊断 JSON 的字段已经存在，本轮补齐赋值与生命周期。

## Edge cases and errors

覆盖输入缺失与明确零、输出限制结束、完成与 incomplete 工具/图片、xAI 边界数量与无效时间戳、已计费失败状态、SSE/WS 错误写入成功及写入/刷新失败、strict Claude 正常/截断/客户端断开、Realtime 一轮/多轮/轮间断开。写失败不能声称错误已交付。只确认成功写入，保留原始失败原因与结算路径。

## Main Chain Impact

同步工作仅为现有解析结果上的分支、固定字段赋值和请求内计数；不增加网络、数据库、Redis 调用或等待。错误帧使用现有终止错误写入通道及其超时。日志继续走已有异步队列，不新增 goroutine。本轮补齐的 DeliveryEvidence 和 UsageFinal 不参与计费；原有 UsageEvidence 仍参与用量选择，不能把整个 Diagnostic 对象概括为纯展示。ErrorDelivered 另用于避免重复补发终止错误，不决定重试或费用。

## Shared Resource Audit

修改只访问请求私有 StreamSession、StreamOutcome、诊断对象以及插件请求/响应对象。StreamSession 使用已有短临界区，不跨 I/O 持锁。没有新增共享缓存、Redis namespace、DB 表访问、连接池或工作池；已有日志队列的写入频率不变。测试用独立数据库与 Redis 实例，夹具按行清理并等待异步日志完成后恢复全局状态。

## Concurrency Analysis

30k/100k RPM 下每请求新增 DB/Redis 调用为 0，新增 goroutine 为 0。Realtime 证据汇总为固定大小对象，空间不随轮数增长；工具和图片检查为常数时间，xAI 数组验证受恢复的数量上限约束。现有锁仅保护请求内状态，不引入跨请求争用。

## Validation

先增加失败回归，再实现修复。运行受影响包的全部测试及生产形态的主模块回归（独立 MySQL、PostgreSQL 日志库、Redis）；前端运行受影响测试、类型检查、lint。上游暂缓问题导致的既有失败独立列出，不用修改期望掩盖。

## Implementation

- B4：已撤回本轮提前加入的限制终态输入补估和 Chat outcome 赋值，原生/转换 handler 恢复原有确认用量选择。相应的新增 64 用例保存在系统临时目录，既有审计探测恢复为条件性行为记录，不以未获证实的回包规定新收费策略。正常 completed 的共有零输入问题也未修改。
- B5：`response.output_item.done` 的工具和图片项只在完成状态（或兼容的未声明状态）下确认交付；该非完成项本身不产生有效交付，接收侧估算仍保留。这不会撤销先前通过 `function_call_arguments.done` 已确认的完整工具交付，后者是原设计有意保留的行为。
- P6/P7：xAI 恢复参考图 7、关键帧 4、参考音频 3 的上限以及正数时间戳验证。原生状态恢复公开任务 `request_id`，删除上游 `usage`，保留已计费失败的级联语义及 202 pending 包装。
- D1：Realtime 使用固定大小对象汇总已结算轮次的交付证据与用量完整性，连接结束时回填到最终诊断；轮间终止只合并状态，不再次累计最后一轮输出。
- D2：strict Claude 从专用状态机生成同形交付证据；已验证的 `message_stop` 独立记录协议完成，因此下游短写不能抹掉上游协议完整性。输出交付仍以写入和刷新成功为准。
- D3：终止错误完整写入且刷新成功后标记会话，刷新最终诊断中的交付证据。短写、写失败、刷新失败或 WS 写失败均不标记。SSE 沿已有包装链寻找 `FlushError`，避免无返回值的 `Flush` 隐藏错误。该遍历最多 16 层，仅发生在原有终止错误写入路径。
- 测试夹具：Doubao 优先复用集成测试主库和日志库；无配置时固定单连接 SQLite 并补齐结算依赖表，等待异步日志并按请求查询、按行清理。Suno 路由参数对齐生产的 `:s2`；渠道错误日志测试在切库前后排空队列、按请求过滤。改造后的上游插件集成测试重命名为 `plugin_native_upstream_cases_test.go`。前端 access/bot 夹具补齐现有组件必填参数。

## Evidence and purpose review

- B4 的部分用量回包（有 output_tokens、无 input_tokens）未在既有真实试验记录中找到。官方 [Responses 契约](https://developers.openai.com/api/reference/typescript/resources/responses) 将 usage 内 input_tokens 列为必填；合法的 usage:null 是不同情形，原实现已经估算。仓库外 overlay 比较修改前后各 56 用例，固定价格 28 组合的用量和计价结果相同；只有按 token 计费的构造缺字段场景从输入 0 改为估算 99。因证据不足且影响收费，补估修改已撤回。撤回后用仅添加测试的 overlay 再跑 56 用例，与原行为的全部观察结果一致。证据在系统临时目录 `newapi-b4-fixedprice-proof/`。
- B5 补充 16 个组合场景，运行真实 SSE/本地 WebSocket handler 及最终用量选择。标准 Responses 的无 name 参数完成事件之后收到 incomplete 工具项，再遇上游失败，最终选择零用量，原始用量证据及下游内容帧保留。Realtime 含名称和合法参数的完成事件已经形成可用工具，后续 incomplete 不撤销这一事实，仍保留收费；修改该行为会扩大原策略，本轮没有改。缺参数的容错形态不符合官方必填契约，不能据此报告真实上游故障。图片 incomplete 不在官方图片状态枚举内，原图片探测仅为构造形态。证据在系统临时目录 `newapi-b5-sequence-review-20261105/`，未实际扣钱包或调用真实 AI。
- xAI 原有数量边界和公开任务 ID 契约由合并前提交直接证明。正常请求、编辑/续写、待处理 202、完成/审核拦截/已计费失败/未计费失败/过期及级联计价回归通过。恢复限制会拒绝合并后曾接受的超限输入；恢复脱敏会使客户端看不到供应商 usage，内部 task.data.usage 保留。不能声称所有外部行为逐字节不变，也未证实曾因此漏扣或产生实际损失。
- D1/D2/D3 通过缺失证据、短写、flush 失败、关闭 WebSocket 和重复最终处理等本地故障用例，证明本轮诊断修正不增加轮次结算、不重复退款、不将写失败判为交付成功。测试不证明客户端实际消费、真实供应商事故或线上吞吐。
- D3 的 Flush 解包会跳过部分中间 Flush 调用。当前链中 keepalive 停止、写权限检查及 Gin 写头已由前面的完整 Write 执行，插件门控使用同一 active 条件，capture/日志包装无额外 Flush 缓冲；relay 路由没有 gzip。结合既有刷新失败测试，未发现当前包装链的压缩或缓冲提交被丢失，结论限于已检查的实际链。

## Shutdown batch flush ordering

用户已确认修复关停时的排空预算，并要求补齐压力中停止及同数据重启场景。收到退出信号后立即进入 HTTP Shutdown，排空结束或超时后等待在途批量额度刷盘并执行最后一次刷盘，再依次关闭消费日志及其记账 continuation、成本与统计管线。移除排空前的同步额度刷盘，并将 HTTP 排空 context 的创建放到实际调用 `srv.Shutdown` 的回调内。

- 生命周期：退出信号 → 创建排空预算并停止接单、等待在途请求 → 等待在途批次并最终刷额度 → 消费日志及记账收尾 → 成本/统计刷盘 → 退出。排空期间现有周期批量更新继续运行。
- API、数据模型与配置：没有新增接口、表、迁移或配置；沿用 `SHUTDOWN_TIMEOUT_SECONDS` 和 `BATCH_UPDATE_INTERVAL`，数据库写入语句保持不变。
- 错误处理：HTTP 排空报错时仍记录系统错误并继续额度、日志和统计刷盘。此修复不为最终数据库刷盘增加超时，不改变写库失败批次的恢复机制。
- Main Chain Impact：调整收到退出信号后的 main goroutine 执行顺序，并串行化后台批量刷盘；正常 relay 请求不新增同步工作、异步任务或等待。
- Shared Resource Audit：额度刷盘继续使用既有批量 map、对应 store 互斥锁、主数据库连接池及 `users`、`tokens`、`channels` 表。新增的批量刷盘互斥锁仅由后台刷盘与退出刷盘获取，relay 仍通过原有按 store 短锁生成增量，不获取新增锁。不新增连接池或数据库调用，删除关停阶段的一次主动刷盘。
- 并发影响：30k/100k RPM 下每请求新增 DB/Redis 调用和 goroutine 均为 0。现有周期 worker 在排空期间保持运行。
- 验证结果：先运行 `go test . -run '^TestShutdownWithBatchFlush_Order$' -count=1 -timeout=90s`，正常排空与超时两组均在旧实现失败，实际顺序为 `batch → drain → batch → flush`。修改后 `go test . -count=1 -timeout=90s` 根包全部通过，覆盖两组的 `drain → batch → flush` 且各执行一次；`gofmt` 和限定本轮 Go 文件的 `git diff --check` 通过。
- 第一阶段验证边界：最初仅使用关停回调验证顺序；用户追加的真实压力、停止和重启测试及发现的修复见下节。所有测试均不访问业务数据库或真实上游，不改变 SQL 或数据库模型。

## Verification results

### Shutdown under load and restart validation

用户要求在持续压力中停止，并使用原持久化数据重启后验证服务继续处理请求。本轮增加独立、按需启用的验证夹具，不增加生产接口或关停钩子。

- 本地 HTTP 集成矩阵：SSE、非流式、混合请求；正常排空、客户端中途取消、排空期限到达、最终额度刷盘被阻塞、批量更新关闭和空闲关停。使用真实本地 socket 和同步屏障，确保触发关停时存在明确数量的在途请求，检查停止接单与刷盘顺序。
- 真实进程矩阵：Linux 容器中运行当前生产二进制和本地模拟上游，在持续流量中发送 SIGTERM 或 SIGKILL；区分自然完成、截断、客户端取消、关停后的连接拒绝和非预期失败。记录实际吞吐，不将目标 RPS 当作实测值。
- 重启矩阵：保留原数据库及缓存资源，停止后重新启动同一二进制，不重置余额或重新初始化测试数据；验证新的 SSE 与非流式请求，并核对重启前后的余额、用量及日志增量。
- 持久化：只使用任务专属数据库、容器和临时目录，核对用户、令牌、渠道与消费日志。成本/统计和 WebSocket 如存在既有关停限制，单独记录实际结果，不把 HTTP 排空通过扩展为所有后台任务安全退出。
- 资源与主链影响：测试使用有界客户端并发、本地监听器及任务专属数据库；不连接真实供应商，不读取业务凭据，不改变正常 relay 的 DB/Redis 调用、锁或 goroutine 成本。
- 已知边界：HTTP Shutdown 超时本身不会取消在途 handler，强制杀进程不会执行关停刷盘，hijack 后的 WebSocket 不由 HTTP Shutdown 等待。测试记录这些情况的截断和持久化差额，不以放宽正常关停的断言掩盖失败。

实际正常关停压力先发现并确认了成本收尾顺序缺陷：500.22 实测 RPS 下发送 SIGTERM 时仍有 252 个活跃客户端请求、251 个活跃上游，最终 2301 个 SSE 请求完整结束，用户、令牌、渠道及消费日志的额度均为 345150，但成本记录仅 2000 条，缺少 301 条。保留原库及 Redis 重启后，100 个混合请求均完成并正确扣费 15000，新增成本记录仍为 0。证据位于系统临时目录 `new-api-shutdown-5730973bde/normal500/`。根因是 main 先停止成本/统计消费者，再由消费日志收尾的 accounting continuation 继续写入成本缓冲。修复将收尾顺序改为额度 → 消费日志及其 continuation → 成本/统计，保持各管线独立预算；不增加正常 relay 的同步工作、锁或 DB 调用。此故障用真实生产二进制与本地模拟上游先复现，再重跑同场景及重启对账。

确定性 SQL 屏障还复现了已取走批次的额度丢失窗口：后台刷盘已换走全部 map、第一条 SQL 尚未执行时，关停的最终刷盘因 map 为空直接返回，用户、令牌和渠道的六项余额/次数断言均失败。修复仅在 `batchUpdate` 内增加后台刷盘互斥，使最终刷盘等待已取走的批次完成，再取剩余增量；`addNewRecord` 与 relay 请求不获取这把锁，既有按 store 的短锁不变，也不跨 I/O 延长请求侧锁。未新增连接或 SQL，不改变数据库写入形状；最终额度刷盘没有独立 deadline，仍依赖 driver/数据库结束等待，写库失败也沿用既有错误处理，不将 SQL 尝试结束宣称为一定已持久化。修复后的隔离 SQLite 子进程测试通过六项余额、消耗与次数断言，耗时约 5.33 秒；根包普通测试通过。

### Actual shutdown and restart results

环境为 Windows amd64 / Go 1.26.10，Docker Linux x86_64（16 CPU、约 23 GiB 内存）。生产 Linux 二进制 SHA256 为 `68ba57d878e34117c4288ffcd024fe7227c57986b1fb4a2669a3ef073d27b3be`。真实数据库为隔离 SQLite WAL、MySQL 8.0.46、PostgreSQL 17.11，配合任务专属 Redis 和本地模拟上游。所有请求均使用合成凭据。SQL 正常配置对照将应用连接池限制为 100 open / 20 idle，数据库服务端上限为 300。

负载在收到停止信号后仍继续发起请求，信号在有明确在途请求时发送。下表的速率是信号前实际发起速率；“通过”包含完整响应、用户/令牌/渠道额度与请求次数、消费日志的请求 ID 集合、逐日志成本关联、Redis/DB 一致，以及保留原库和缓存重启后的 100 个新混合请求。每个正常完整响应独立期望为 `(100 input + 50 output) × ratio 1 = 150 quota`。

| 数据库与场景 | 信号前实际发起 RPS | 完整响应数 | 停止耗时 | 对账及重启 |
| --- | ---: | ---: | ---: | --- |
| SQLite，正常 TERM | 500.11 | 2304 | 1.728 s | 通过 |
| SQLite，日志积压 60 s | 500.11 | 2318 | 1.670 s | 通过 |
| MySQL，正常 TERM | 500.22 | 2305 | 1.387 s | 通过 |
| MySQL，日志积压 60 s | 500.22 | 2308 | 1.551 s | 通过 |
| PostgreSQL，正常 TERM | 500.11 | 2313 | 1.292 s | 通过 |
| PostgreSQL，日志积压 60 s | 500.11 | 2302 | 1.321 s | 通过 |
| PostgreSQL，关闭批量更新、对齐连接池 | 500.11 | 1876 | 7.053 s | 通过 |
| PostgreSQL，禁用信任免预扣 | 500.11 | 2302 | 1.285 s | 通过；生产日志确认非零预扣 |
| PostgreSQL，1667 RPS 到达压力 | 1667.26 | 8105 | 4.860 s | 通过；原 HTTP endpoint 重启 |

最后一轮在 4.505 秒发送 TERM 时有 7511 个已发起请求、4268 个已完整结束、3243 个仍在途；最终无响应中断、HTTP 错误或丢弃到达，所有完整请求精确累计 1215750 quota，重启后的 100 个新请求精确新增 15000 quota。P50 为 1482 ms、P95 为 4197.6 ms，最大 6587 ms。信号前完成速率约 947.4 RPS、在途请求持续增长，因此本轮只证明短时 1667 RPS 到达压力下的停止与重启正确，不证明稳态 100k RPM 完成能力。早先 SQLite 的 1667 目标轮只达到 1075.33 RPS 并在信号前丢弃到达，原报告保留失败，不用后来的 PostgreSQL 成功覆盖它。

异常场景没有全部通过：

- PostgreSQL 强制预扣且 HTTP 排空期限 2 秒时，进程约 2.940 秒退出，仍留有 20754 quota 未完成终态结算的预扣：用户/令牌余额已减少、令牌 used 已增加，而用户 used、渠道 used、消费日志及成本均为 0。原库原缓存重启后可以精确处理 100 个新请求，但旧预扣并未自动完成结算或释放。部分 SSE 已交付 `hello`，不能据此断言 20754 应全部退款；最终应收/应退仍应按既有中断策略决定。当前缺口是超时后没有取消并等待剩余 handler 的终态路径收敛。
- SIGKILL 的退出码为 137，正常关闭钩子未执行。观察到批量额度、日志、成本的内存数据丢失及 Redis/DB 差额；重启后的 100 个新请求可以完成，但已有账务及缓存差额仍存在，场景保留失败。
- SQLite 关闭批量更新的 500 RPS 轮触发 8 秒 HTTP 排空期限，实际停止耗时 23.660 秒，887 个完整响应及 731 个中断响应，账务与缓存存在差额。这个结果保留失败；不能用 PostgreSQL 的配置对照替代它。
- PostgreSQL 早先使用应用默认 1000 连接池、数据库 300 上限的对照在信号前触发 `53300 too many clients`，原失败保留。对齐连接池后的 batch-off 场景才作为正常配置通过。
- WebSocket 握手和双向消息在 TERM 前、原库及 Redis 重启后均成功，前后 HTTP 请求也分别精确计费 150，并保留旧日志与成本。但正常关闭基线、持有连接时停止、重启后关闭均观察到 1006；这不是 WebSocket 优雅排空通过。

最后使用强化后的逐请求内容/终态验收再次验证 SQLite 的 2 秒排空期限，两个场景均保留失败：信任免预扣场景有 1153 个已收到内容的 SSE 请求没有终态日志，所有账务为 0，2.931 秒退出；强制预扣场景有 1154 个已收到内容的 SSE 请求没有终态日志，留下 20772 quota 未结算预扣，2.899 秒退出。两轮均零 HTTP 错误、零丢弃到达，实际约 500 RPS，并且在原 endpoint、原数据库及 Redis 重启后各 100 个新混合请求都精确新增 15000 quota。这确认“新请求可处理”与“旧请求已完成结算”必须分别验收，不能以重启可用掩盖旧终态缺失。原 PostgreSQL 20754 预扣的独立复现仍保留。

验证工具还增加 8 项对账门槛的反例测试，防止请求次数遗漏、成本缺失/重复关联、旧记录被修改、余额丢失或有效部分响应没有终态记录却被判为通过。最后一项曾先复现“零日志、零扣款却把有效部分响应判通过”的验收漏洞；修复后要求所有已交付内容的请求 ID 都有终态消费记录，8 项全部通过。异常响应的完整数为零，不代表应收必然为零。

重现命令与二进制构建步骤见 `bench/shutdown/README.md`。已执行根包普通测试、9 组实际 socket 矩阵（64/128 并发；共 10880 个 accepted，10864 完成及 16 个主动取消）、SQL 屏障持久化测试，以及低并发 `-race` 矩阵和持久化测试，均通过。CLI 与脚本不进入生产路径。

原始证据保存在系统临时目录：初始成本缺失 `new-api-shutdown-5730973bde`；最终 SQLite 正常对照 `new-api-shutdown-955de6d2b6`；SQLite 异常/积压与 WS `new-api-shutdown-f89c4b67d0`；MySQL `new-api-shutdown-mysql-1ec940552c`；PostgreSQL 核心/预扣 `new-api-shutdown-postgres-6f837157c3`；PG 对齐连接池 `new-api-shutdown-pg-pool-9f0a3920f5`；正式 PG 高到达压力 `new-api-shutdown-pg1667-1e8cedf523`。每轮包含逐请求 trace、进程日志及显式 pass/fail 的 JSON；有失败的旧轮次不覆盖或删除。

强化后的最终超时证据为 `new-api-shutdown-terminal-065751fc93/summary.json`。实际 SQLite 版本为 3.50.4、Redis 为 7.4.11，Docker Engine 为 28.1.1。工具开发阶段的端口绑定、合成 token 格式、Windows/Linux 活跃 WAL 锁、PostgreSQL JSON seed 类型问题均作为夹具失败排除；后续统一用 Linux helper 读写 SQLite，并在 Windows 临时源端口范围外发布监听端口。所有本任务创建的容器、网络与宿主负载子进程已清理，数据及失败证据留在临时目录；未改动其他项目的 Docker 资源。

最终静态及构建校验：`go test . ./bench/shutdown/... -count=1 -timeout=90s`、8 项 Python 对账反例、`gofmt`、UTF-8/AST 与限定本轮 Go 文件的 `git diff --check` 通过。压力场景的显式失败结论独立保留，不因单元测试通过而改称全部通过。未做生产部署、真实供应商调用或无界长时间稳态容量验证。

- B4 撤回后：`go test ./relay/channel/openai ./relay/common -count=1 -timeout=3m` 全包通过；另用独立 PostgreSQL 17 容器执行 service 的计费矩阵、错误交付、终止一次性、无交付退款、截断工具五组测试，通过后已停止并删除容器。首次未配置 SQL_DSN 的 service 命令在 TestMain 阶段停止，不计为通过。新增的补估策略测试随 B4 撤回，原审计用例仅记录条件回包，不用放宽业务断言掩盖有效修复失败。
- 定向回归通过：`go test ./relay/common ./relay/channel/openai`、`go test ./relay/channel/claude`、`go test ./plugins`；service 终止错误写入/短写/刷新失败回归通过。刷新失败和 Claude 已完成协议后短写的新增用例均先失败、修复后通过。
- controller 原三组失败在隔离 MySQL 8 / PostgreSQL 17 日志库上通过；Doubao 和渠道日志测试的 SQLite fallback 也通过。
- `relaykit` 的 `GOWORK=off go build ./...` 和独立全套测试通过；`tokenkit` 独立全套测试通过。受影响 service、relay/common、OpenAI、Claude、plugins、controller 包的 `go vet` 通过。
- 前端全套 246 文件、2833 项断言：2832 通过、1 失败，无加载失败。唯一失败是用户明确暂缓的共有 Flow tooltip XSS；access 116 项和 Bot Protection 1 项均通过。
- 本机 Bun 1.3.13 不支持项目要求的 `bun check -b`；现有 `tsgo 7.0.0-dev.20260707.2 -b` 通过，未改变依赖或脚本。前端 lint 和插件 lint 均通过（有既有 warnings）。xAI 格式检查通过；全插件格式检查仍在未改动的 hailuo、kling、vidu 三个文件失败。
- 主模块执行 `go test -p 2 ./... -count=1 -timeout=8m`：85 个有测试的包通过，包括 controller（235.233s）、model（98.594s）、service（69.205s）；仅 middleware 两组请求体预算测试失败。定位为先前审计探测未安装 `BodyStorageCleanup`，合计残留 1024 字节预算；只为该探测补生产同款清理及逐请求预算回收断言后，原失败组合和 `go test ./middleware -count=1 -timeout=3m` 全包通过（26.408s）。未放宽原断言，未修改中间件生产逻辑；主模块结果由该次全套及失败包修复后的完整复测组成。
- 测试基础设施：MySQL 8.0.46（127.0.0.1:23316，`newapi_fix`）、PostgreSQL 17.11 日志库（127.0.0.1:25436，`newapi_fix`）、Redis 7.4.11（127.0.0.1:26389）；全部为本任务独立临时容器。service 测试自行创建并清理独立数据库/schema，controller 夹具按行清理。日志存于系统临时目录 `newapi-fork-fixes-20261105-root-tests.log`、`newapi-fork-fixes-20261105-middleware-tests.log`、`newapi-fork-fixes-20261105-frontend-tests.json`。所有上游均为本地 fixture，无真实供应商调用；本轮无生产数据库行为修改。
