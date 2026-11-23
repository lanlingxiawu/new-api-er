# 请求采集必要性与开销复核

日期：2026-11-21。范围：请求进入网关后的耗时、策略、流式证据、请求/应用日志和性能统计采集。用户要求检查所有这些采集，删除没有排查价值的内容和无效开销，保持实现简单。

## Existing Logic and Related-Path Findings

本次按生产者、持久化路径及实际读取方检查代码，没有仅凭字段名称判断用途。

| 采集 | 已核实的代码与用途 | 处理 |
| --- | --- | --- |
| 上游交换时间、写入结果、声明大小 | `service/upstream_exchange_trace.go`、`relay/channel/api_request.go` → `AppendRelayLogAdminInfo` → 日志详情阶段差值；不读取正文 | 保留现有七个字段。可缩小到准备、获取连接、写入或等待响应阶段，但不能单独认定责任方 |
| 请求 ID、上游 ID、渠道重试链 | `model/log.go`、`service/log_info_generate.go` 与上游查询/日志关联 | 保留关联标识；前轮已删除 ID 时间戳差值推断及协议采集 |
| 首响应、状态、总耗时 | `RelayInfo`、`middleware/logger.go` → 用量日志/性能统计/访问日志 | 保留，支持查找失败请求及慢请求；与传输首字节不是同一口径 |
| 策略与转换信息 | `service/request_policy.go`、`relay/common/conversion_diagnostics.go` → 日志策略流程/转换损失提示 | 保留；分别有 512 条事件和 32 条去重项上限，记录实际路由/重试/字段丢失 |
| 流式响应首尾、头部、终止证据 | `relay/common/stream_response_capture.go`、`stream_termination.go` → Root 诊断接口与面板 | 保留有界证据。正文每个响应首尾各 1 KiB、头部最多 16 KiB、至多四个响应；终止仅固定槽位，不增加 watcher |
| 流式用量、交付、终止状态 | `stream_session.go`、`service/stream_lifecycle.go`、严格 Claude/Realtime → 结算与诊断 | 保留。它们参与实际计费与错误处理，不是可直接删除的旁路统计 |
| 结算后的重复响应快照 | `appendStreamLogInfo` 先克隆完整捕获数据，随后被 `StreamResult.Diagnostic` 覆盖 | 删除被覆盖的复制；无已结算快照时保留原分支。晚到终止、下游补发帧仍单独刷新 |
| 错误日志快照 | `AppendStreamErrorDiagnostic` 还为没有明确错误的捕获器补写后备错误 | 保留此分支，避免为了省一次复制改变后续错误优先级；错误流量不是本轮正常路径优化对象 |
| 请求/响应原文日志 | `middleware/request_logger.go` → 有界队列/固定 worker → `model/request_log*` → Root 详情 | 沿用默认关闭、内容上限、用户名过滤。写入队列未启动/已停止时跳过无法落盘的采集；请求结束时再次检查，避免无意义序列化 |
| 正常预扣、结算、免费模型、流结束提示 | `service/billing*.go`、`quota.go`、`relay/request_billing.go`、`relay/helper/stream_scanner.go` → 同步应用日志 | 降为已有 DEBUG 下输出，关闭时不提前格式化。保留失败、退款失败和异常终止日志、结构化用量/资金记录及每请求访问日志 |
| Realtime 逐帧 token、累计用量 | `relay/channel/openai/relay_realtime.go` → 同步应用日志 | 改为调试输出，删除完全重复的一条；计数和计费本身保留 |
| 签名请求全文、消费日志完整参数的二次打印 | `relay/channel/jimeng/sign.go::SetPayloadHash`、`model/log.go::RecordConsumeLog` | 删除应用日志副本。签名仍使用同一序列化结果；消费记录仍原样持久化，错误日志保留 |
| 活跃请求、模型性能汇总 | `middleware/stats.go` → 管理端；`pkg/perf_metrics` → 趋势和重试保护 | 保留功能性统计。热桶命中时直接复用现有桶，避免每请求构造马上丢弃的桶；原子计数和后台刷盘不变 |

未把计费、成本流水、权限审计、限流状态当作无用诊断删除。请求原文日志与有界流式异常证据用途不同：前者按开关排查输入输出，后者必须在流进行时保留少量字节，才能在之后发生异常或零输出时提供证据。

## 目标、范围与数据流

只删确定无效的复制/重复输出，复用已有开关和快照；不增加诊断字段、界面、探测请求、采样系统或新的配置。

请求 → 原业务处理及已有有界观察 → 已冻结的结算快照/必要元数据 → 原日志异步队列 → 原存储 → 按需诊断详情。成功提示只在 DEBUG 开启且非私有流时生成。原文请求日志在写入侧不可用时直接放行。

## API、数据模型与配置

不新增或修改端点、响应字段、权限、数据库表/索引/迁移或保留期。Root 私有诊断仍经 `controller/stream_diagnostic.go` 查询，限制请求标识/时间/尝试号、三秒查询超时并 `no-store`；普通日志读取继续剥离私有内容。前端仍为点击后查询，无轮询。

沿用 `DEBUG`、请求日志设置、性能统计设置、现有耗时诊断开关。这里只减少旁路工作，不增加新的 relay 执行分支或开关。

## 业务边界与错误处理

- 不更改计费计算、资金调用、token 计数、协议解析、请求/响应字节、连接池、超时或重试策略。
- 有冻结结果时以其用量、估算和第一错误为准；后续关闭证据和已写出的下游错误帧仍刷新。没有结果时沿用原捕获器及 `StreamStatus.EndError`。
- 正常请求的原始响应仍不持久化；既有异常、拒绝和最终零输出资格保留。不能等异常发生后才开始抓取之前已经消费的响应字节。
- 请求日志队列在请求期间启动时，不追溯采集这个已放行请求；期间停止则不再组装无法投递的记录。队列过载时原来的非阻塞丢弃策略不变，不因开始时队列满而预先丢弃可能最终能投递的请求。
- 性能桶首次并发创建仍通过 `LoadOrStore` 竞争；命中路径不换桶。后台 drain、失败回填和旧桶清理规则不变。
- 异常日志及原日志写入失败处理保留；不新增影响主链的错误。

## Main Chain Impact

同步部分减少一次完整响应快照、成功提示的格式化/文件写入、不可写请求日志的正文预读/复制，以及已存在性能桶的分配。保留原请求私有有界内存观察及计数，不添加磁盘、网络、数据库调用或后台任务。

异步部分仍使用现有日志队列、固定请求日志 worker、性能 Redis 合并及后台 DB 刷新。无新增 goroutine、timer 或探测。

## Shared Resource Audit

| 资源 | 与主链的共享情况及影响 |
| --- | --- |
| 应用日志 writer、文件句柄及 `LogWriterMu` | 主链原本同步访问；成功提示减少访问，不换 writer、不加锁、不增加异步输出队列 |
| `StreamResponseCapture` 及冻结结果 | 请求私有；少做响应锁与克隆；终止状态按既有锁读取，结果不被修改 |
| 请求日志原子队列指针、worker、磁盘索引 | 沿用现有资源，只在采集前和组装记录前判断可写；不增加 worker 或文件 |
| 性能 `hotBuckets`、原子桶、Redis pending map | 与统计写入/后台 drain 共享；热命中复用同一桶，未改命名空间、容量、原子操作或 drain 生命周期 |
| DB/Redis/HTTP 连接池、用户/令牌/渠道数据 | 没有新增调用或变更查询；资金和日志的原持久化路径不变 |

## Concurrency Analysis

30k RPM 约 500 请求/秒，100k RPM 约 1,667 请求/秒。新增 DB、Redis、网络调用以及 goroutine 数均为零。冻结结果存在时，每次日志组装避免克隆至多四份响应头及首尾缓冲；普通流一次响应就少一次最多 2 KiB 正文复制和相应头部 map/slice 分配。成功路径少写预扣/结算/流结束等重复应用日志，Realtime 不再默认逐帧写盘。

请求日志关闭时仍立即返回，不读队列；开启时多一次原子指针读取，未启动/已停止直接不采集。性能热桶的 `Load` 命中只读现有对象，未命中才构建并竞争入表。无跨请求新锁；已有性能计数与 Redis 批量锁保持原样。微基准只衡量本机路径，不据此声称生产吞吐提升。

## 验证计划与结果

先补充行为用例与基准，再修改生产代码：

- 请求日志写入器不存在时，业务处理开始前不得预读正文，业务仍能获得原正文/错误并返回原响应；原开启、关闭、截断、panic、过滤测试继续通过。
- 签名摘要与签名行为不变，应用日志不包含请求正文；DEBUG 开启也不恢复全文二次打印。
- 冻结诊断优先级、缺失结果回退、晚到终止及补发下游证据、历史响应/零输出资格不变；对同一 fixture 比较优化前后分配量。
- 性能桶并发首次创建及复用的计数不丢失；比较热桶基准的分配量；保留现有关闭、Redis 合并和重试保护测试。
- 运行受影响 Go 包与独立 service 纯诊断回归。service 的完整 TestMain 依赖 SQL_DSN；若本地缺少数据库，明确不将纯回归宣称为 DB 集成验收。

已实施上表五类优化：冻结快照复用、正常明细降为调试输出、删除两处全文二次打印、无写入器不采集、性能热桶复用。没有新增采集、端点或界面。

验证记录：

- 修改前，`TestRequestResponseLogger_NoWritersDoesNotPrefetchBody` 发现未启动写入器仍把 29 字节请求全部预读；`TestPayloadHashDoesNotLogRequestBody` 发现签名函数把请求全文写入 INFO。修改后通过。原 Claude 请求日志测试补齐真实写入器初始化，仍检查 Messages 请求进入采集，没有降低断言。
- `go test ./relay ./middleware -count=1` 通过（middleware 包数据库用例按既有 TestMain 在 SQL_DSN 缺失时跳过）。`./relay/helper`、`./relay/channel`、`./relay/channel/openai`、`./relay/channel/jimeng`、`./relay/common`、`./logger`、`./pkg/perf_metrics` 全包通过。
- service 以全部生产 `.go` 文件加 `stream_log_info_test.go`、`stream_lifecycle_test.go`、`stream_settlement_test.go`、`stream_diagnostic_test.go`、`downstream_diagnostic_test.go`、`fork_zero_output_diagnostic_test.go`、`fork_stream_termination_test.go`、`stream_deadline_test.go`、`stream_response_gate_test.go` 运行，全部通过；其中资金接口为纯内存 fixture，覆盖结算次数、差额、失败及零输出诊断资格。未运行要求 SQL_DSN 的 service 全包数据库 TestMain。
- 新增并发桶用例检查 16 个 goroutine 各记录 100 次，总请求数、成功数和累计时延不丢失。其他请求日志开启/关闭、错误透传、大小上限、panic、过滤、worker 关停及流式隔离用例随 middleware 包通过。
- `go test -race ./pkg/perf_metrics -run '^TestRecordConcurrentBucketReuse$' -count=1` 通过。首次因本机 cgo 编译环境失败；在命令进程内设置 `CGO_ENABLED=1`、`CC=C:/msys64/mingw64/bin/gcc.exe` 并补入 MinGW bin 的 PATH 后通过，没有更改系统环境。`go build ./...` 通过。
- 修改文件的 `gofmt`、`git diff --check` 通过；没有修改前端或数据库行为，不需要新增翻译/迁移验收。

Windows amd64 / Ryzen 7 8700G / Go 1.26.11 的本地微基准，`-benchmem -benchtime=300ms -count=3`；表内分配量在三次运行一致：

| 路径（每次调用） | 修改前 | 修改后 |
| --- | --- | --- |
| 已冻结流式日志，1 次响应 | 4,088 B，21 次分配 | 1,312 B，11 次分配 |
| 已冻结流式日志，4 次响应 | 12,688 B，40 次分配 | 1,664 B，12 次分配 |
| 性能统计热桶命中、Redis 关闭 | 112 B，2 次分配 | 0 B，0 次分配 |

基准只覆盖所列函数与 fixture，不包含整个请求，也不能代表生产吞吐。正常成功提示不再默认写盘的收益未单独做磁盘压测。未访问真实上游或生产环境，未部署。

## 计费确认与全量自动化回归（2026-11-21 补充）

按用户要求继续验证计费，并扩展到仓库全量自动化测试。`service/fork_normal_billing_test.go` 增加 DEBUG 开/关矩阵；另修正两处测试夹具缺少生产已有批次去重表的问题，并隔离恢复文件。没有修改金额预期、扣费公式或生产计费逻辑。

已核对 `preConsume` 的信任判断、令牌预扣、资金预扣、`SettleBilling` 的差额提交及失败返回、`BillingSession` 的重复结算/退款保护、Realtime 逐轮扣费与最终结算、消费日志写入路径。日志开关只包围提示文本，实际资金调用在开关之外；保留 token/用量计数及异步消费日志、成本与佣金记录。

本轮启动独立的本地 MySQL 5.7.44、MySQL 8.0.46、PostgreSQL 18.7、ClickHouse 25.8.33.6 和 Redis 7.4.11 容器，数据库与现有服务隔离。测试不使用真实上游。首次新建 MySQL 默认 latin1，导致中文默认值和字符集检查失败；改为 utf8mb4 并新建测试库后复测。故障注入与审计测试的专用库名保护保持不变，按测试要求另建专用库，没有放宽断言。

`go test ./service -run '^Test(ForkNormalBillingDatabaseMatrix|FixedPriceBillingDatabaseMatrix)$'` 的 1,051 个测试项（含父级/子测试）全部通过；SQLite/MySQL/PostgreSQL 均实际执行，MySQL/PostgreSQL 的消费日志使用单独测试数据库。DEBUG 开/关各覆盖流式/非流式、预扣少于/等于/大于实际费用、缓存读写及 TTL、推理 token 不重复收费、工具/音频/图片价格、阶梯边界、显式零与缺失用量、按次收费及免费模型。断言实际钱包/令牌余额、用户/令牌/渠道累计用量、消费日志金额与归属，以及已结算请求不重复收费/退款。

另行启用 `TestForkStreamFaultBillingLoad`，默认 32 个 worker、3,000 次混合故障定价与终止结算用例通过；该组件用例不替代数据库矩阵或生产压测。

### 失败项处理与资金验证

- `TestBatchFailureDiagnosticRetainsUnwrittenDeltas` 的旧夹具缺少 `BusinessStatsAppliedBatch`，数据库恢复写入后仍无法提交。生产 `model/main.go` 已迁移此表；本轮只补齐测试表，隔离 `quotaRecoveryDir`/未保存批次，增加拒绝时未扣款、恢复文件保留/确认、累计用量断言。该故障仅作用于私有 SQLite 连接，取消不必要的环境开关，让它进入默认套件。专项及完整 model 包均通过。
- `TestUserChannelExclusionLoad` 同样缺少批次去重表，首次压测的最终入账断言失败。本轮只在该独立压力用例中补表，并以 `t.Chdir(t.TempDir())` 隔离恢复文件；原来的金额、条数和缓存断言保持不变。复测 71,680 次本地 HTTP 请求全部符合预期，其中 55,010 次成功、16,670 次预期拒绝；SQL/Redis 钱包、令牌余额、消费/错误日志条数一致。128 个 worker、半数流式，五阶段各 10 秒；100k RPM 阶段实际约 1,667 RPS，最长阶段 P99 为 10.009 ms。这是合成上游与本机数据库下的短测，不代表生产持续容量。
- `TestQuotaFailureRecoveryMySQL` 对真实 MySQL 注入只读拒绝，批量开/关两条路径的补扣、退款与恢复回放通过；已提交批次不会重复扣款。完整 service 套件也覆盖订阅、余额不足、部分退款、重复结算、配额溢出及 Realtime 与官方实现的扣款一致性。
- Claude 的 `large_refusal/1667_rps` 首次与其他重负载并行运行时未达吞吐阈值，50,010 请求无业务错误但仅约 80,798 RPM。结束竞争任务后，按原断言单独复跑得到 50,010 请求、零错误、100,016 RPM，服务 P99 6.75 ms、含调度等待 P99 7.44 ms；没有降低阈值或改生产代码。

### 全量套件与补测范围

主命令及结果记录在 `_local/collection-billing-full-20261121/`；连接参数只由临时环境注入，不写入报告。

```text
go test -json -p 1 -count=1 -timeout=15m ./...
cd relaykit && GOWORK=off go test -json -count=1 ./...
cd tokenkit && GOWORK=off go test -json -count=1 ./...
go vet ./... / go build ./...（根模块、relaykit、tokenkit 分别执行）
cd web && bun run test --maxWorkers=4
cd web && bun run build
```

根模块第一次全量命令因测试库编码与专用库配置退出 1，不能算作一次全绿。修正隔离环境后，完整 controller/middleware/model/service、审计导出/安全包和失败专项重新执行；最终按测试名合并结果，保留首次失败记录。独立 relaykit、tokenkit 的全量测试、三个模块的 vet/build 均通过。

已另外启用的专项包括：Redis 恢复和去重、审计启动/迁移及 ClickHouse 新旧表、MySQL 8 不可见索引、MySQL/PostgreSQL 查询计划、PG 锁超时恢复、真实库渠道排除及 session 迁移、混合主/日志数据库管理操作、完整扫描回归、内存与请求体压力、厂商流异常/慢客户端、正常关停刷盘、Midjourney 本地并发。Windows 不支持的两个文件语义用例在隔离 Linux 容器内补跑通过。

前端 290 个测试文件、3,249 个用例通过；生产构建通过。当前 Bun 1.3.13 不支持项目 `typecheck` 脚本使用的 `bun check`，改用已安装的 `tsgo -b` 完成类型检查并通过。第一次 lint 只在未跟踪的 `web/.tmp` 临时文件报错；明确排除此目录后源码 lint 通过，已有 warning 保留。插件 lint/格式检查通过。Python 三组脚本测试共 23 项通过。

百万条日志导出补测通过：真实生成 1,000,000 条记录，七个场景覆盖原价/改倍率、明细/汇总/两者、默认节流；逐行核对导出金额与汇总，未修改源消费记录。总运行 268 秒，默认节流的百万条明细场景约 125 秒。

最终按“包名 + 测试名”去重，并用复测结果替换对应首次失败：Go 共 **25,144 个通过项（包含父测试和子测试）**，2 项条件跳过，未解决测试失败与包失败均为 0。独立模块已计入；这不是 25,144 个独立业务场景。`gofmt` 与本轮修改的 `git diff --check` 通过。

测试结束后已移除本轮创建的五个隔离容器及临时凭据文件；保留本地测试结果和首次失败证据，未改动既有数据库服务。

### 计费结论的边界

现有 `TestKill9DropsUnflushedBatchAndLogs` 通过的含义是**验证了强杀进程会丢失尚未刷盘的内存批次和消费日志**，并非验证强杀后资金不丢失。正常关停排空及已落盘恢复批次的重放通过；崩溃发生在持久化前的窗口仍是既有限制，本轮没有更改其可靠性设计。不能把这轮结果表述为所有进程崩溃场景下计费绝对正确。

未配置的真实 vLLM 集成及专用大规模清理 fixture 保持跳过。没有执行浏览器端到端发布验收、生产 schema 演练或真实付费上游测试，也没有部署；上述本地自动化通过不能替代这些验收。
