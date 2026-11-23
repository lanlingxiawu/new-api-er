# AI 请求固定记录来源 IP

状态：2026-11-16 已按用户确认完成仅后端修改；IP 专项通过，model 整包存在已复现的基线失败；未部署。

2026-11-18 用户再次确认“无论用户是否开启关闭”。已复核当前代码，消费/错误日志仍直接记录 `ClientIP()`；新增安全监控的 IP 风险分析开关位于日志保存成功之后，关闭它不影响 IP 记录，个人安全通知开关同样不参与记录判断。本次无需修改运行逻辑或前端。

本次定向回归：`go test ./model ./service/auditsecurity -run '^(TestRelayLogsAlwaysRecordClientIP|TestSecurityRelayIPOnlyAfterSuccessfulPersistence|TestRelayIPCaptureIsBoundedAndFailsClosed)$' -count=1 -v -timeout=3m` 通过，无跳过。包含 9 组消费/错误日志来源与用户开关组合、持久化成功/失败及风险分析开关边界；持久化专项使用隔离 SQLite，其余为内存队列测试，本次未重跑真实 MySQL/PostgreSQL 或完整 model 包。原始结果在 `_local/audit-ip-recording-correction-20261118/regression.log`。本次仅澄清既有行为，没有 schema、SQL、迁移或主链资源改动。

## 目标和范围

AI 请求生成消费日志或错误日志时，默认写入来源 IP，不再受用户 `record_ip_log` 的缺省值、`false` 或 `true` 控制。遵照用户最新要求，只修改后端，前端页面及多语言文件保持原样。

沿用现有日志产生条件、系统级开关、权限、异步队列和保留策略。此需求不等于为所有 HTTP 请求新建日志，不启用原本关闭的完整请求体日志，不补写无法恢复来源的历史日志。

## Existing Logic and Related-Path Findings

以下结论已核对当前工作区实现，不能从历史设计的“已实现”状态推断。

| 位置 | 已核实行为及拟调整方式 |
| --- | --- |
| `model/log.go:RecordErrorLog` | 调用 `contextWantsIPLog` 判断是否写入 `c.ClientIP()`，然后进入 `enqueueAsyncRelayLog`。移除用户设置判断，保留原入队路径。 |
| `model/log.go:buildConsumeLog` | 同样受用户开关控制；异步 `EnqueueConsumeLog` 和管理端同步 `RecordConsumeLog` 共用此构造函数。统一修改即可覆盖两个入口。 |
| `model/log.go:contextWantsIPLog` | 只读鉴权中间件放入上下文的 `dto.UserSetting`，缺少快照也返回 false；当前仅上述两处使用。修改后删除此函数及因此失去用途的 import。 |
| `service/consume_settlement.go:enqueueStreamConsumeLog` | 正常结算直接入队；WebSocket 延迟日志使用 `ctx.Copy()`，在终态回调中构造消费日志。保留该生命周期，不把活的 Gin 上下文交给数据库 worker。 |
| `service/relay_error.go:ProcessChannelError` | 先判断系统错误日志开关及错误是否允许记录，再调用 `RecordErrorLog`。记录 IP 不改变错误分类、重试和客户端返回。 |
| `service/task_billing.go` 的任务提交日志，以及 `relay/mjproxy_handler.go` | 提交消费日志通过 `EnqueueConsumeLogWithCost` 进入现有消费日志管线，持有客户端请求，因此一起获得 IP。 |
| `model/log.go:RecordTaskBillingLog`、`service/task_billing.go`、`service/midjourney.go` | 后台差额结算和退款直接生成账务日志，无原始 HTTP 上下文，参数也没有来源 IP。这类后台记录不是新的 AI 请求，本次保持其语义，不使用 worker/服务器 IP 填充。 |
| `middleware/request_logger.go` | 完整请求日志已直接写 `c.ClientIP()`，受系统请求日志开关和用户名过滤控制，不读取用户 IP 开关。无需修改。 |
| `controller/user.go:UpdateUserSetting` | `PUT /api/user/setting` 接收并保存 `record_ip_log`，与通知偏好共用设置接口。保留旧字段兼容性，但它不再决定 AI 请求日志是否记录 IP。 |
| `web/src/features/profile/api.ts:updateUserSettings` | 先获取最新个人资料，合并规范化设置再提交。通知设置等其他表单共用此函数；不为移除 IP 控件改写整个设置 API。 |
| `web/src/features/security/components/privacy-card.tsx` | 当前读取资料中的开关值，维护 state/effect，通过 mutation 保存并刷新。按用户明确要求保留；该开关的历史值不再决定后端 AI 日志 IP。 |
| `controller/log.go`、`model/log.go:formatUserLogs` | 日志查询按管理员/本人权限返回分页日志，现有投影不因 `record_ip_log` 隐藏 IP。无需新增查询或改分页。 |
| `common/trusted_proxies.go` | 统一配置可信代理；记录来源沿用 Gin `ClientIP()`，不直接相信任意转发头，不改代理配置。 |

已阅读相关设计 `docs/design/relay-log-async-batch.md`。当前代码与该文档中“请求只入队、后台批量写日志、关闭管线不回退同步写”的原则一致。

现有测试中 `model/log_test.go:TestRecordErrorLog` 明确期望默认 IP 为空，需要按新需求更新；`model/relay_log_pipeline_test.go:TestEnqueueConsumeLogPreservesCacheUsage` 使用没有 `Request` 的合成上下文。因此不能简单无条件调用 `c.ClientIP()` 而引入 nil Request panic。

## 数据流

1. 客户端请求经过现有鉴权、代理解析和 relay 处理。
2. 原有逻辑决定生成消费/错误日志时，从当前请求解析来源 IP；存在 Request 时调用 `c.ClientIP()`，缺失 Request 时保留空值。
3. 将 IP 字符串写入现有 `Log.Ip`；不读取用户设置，也不查询用户表或 IP 情报服务。
4. 主 relay 路径沿用有界异步队列；固定 worker 批量写入 `LOG_DB.logs`，重试/兜底文件沿用同一个日志对象。
5. 日志列表、详情和已有导出按当前权限读取 `ip`。旧日志不回填。

管理端渠道测试使用的同步 `RecordConsumeLog` 同样复用构造函数；仍只影响已有日志的 IP 内容，不改变其执行方式。

## API 合同和权限

无新 endpoint，无鉴权变化。

- `PUT /api/user/setting`：现有 `UserAuth()`。继续接受现有完整设置请求及兼容字段 `record_ip_log: boolean`，成功/失败响应仍使用现有 `success`、`message` 合同。即使旧客户端提交 `false`，AI 日志仍记录来源 IP。保存历史字段不代表能够切换服务端记录策略。
- `GET /api/log`：现有 `AdminAuth()`；`GET /api/log/self`：现有 `UserAuth()`，仅本人日志。响应仍为 `{"success":true,"data":{"items":[{"ip":"203.0.113.7", "...":"原有字段"}],"...":"原有分页字段"}}`（省略号仅示意）。
- 员工查询、token 查询及日志导出沿用现有权限和投影；不扩大日志查看范围。

`relaykit/dto/user_settings.go` 和个人设置类型中的旧字段保留，不改变 relaykit 公共 API。

## 数据模型和配置

- 使用已有 `logs.ip` 字符串列和现有索引；无新表、字段、GORM tag、SQL、迁移、索引或扫描查询。
- 不批量修改 `users.setting`，不需要用户保存一次设置才能生效。
- 不新增用户或管理员 IP 开关。现有 `LogConsumeEnabled`、`ErrorLogEnabled` 和 `relay_log_pipeline_setting.enabled` 继续决定是否记录相应日志；全局管线关闭时仍不回退为同步写。
- 不改完整请求体日志的系统开关、磁盘保留和用户名筛选。

## 界面和多语言

用户于设计确认时明确要求“前端不动，后端逻辑默认实现”。因此 `PrivacyCard`、页面调用、设置保存逻辑、前端测试和所有 locale 文件都保持原样。现有开关仍能保存历史偏好值，但无法关闭后端 AI 请求日志的 IP 记录。不新增文案或翻译键，不改上游保护的鉴权/HTTP 模块。

## 关键业务逻辑和边界

- 无用户设置、空设置、显式 false、显式 true：有来源地址的请求均记录相同 IP。
- IPv4、IPv6、可信代理：使用现有 Gin 解析结果，保留已有信任边界。
- 无 Request 的合成上下文、无法解析地址：IP 为空，日志构造仍可正常完成，不阻断其他字段和记账。
- 流式、非流式和延迟 WebSocket 日志：在原来的日志构造时点保存 IP 字符串；不更改终态或额度结算时机。
- 系统关闭日志、队列拥塞、数据库不可用：沿用原有丢弃/重试/兜底语义，不因为要求记录 IP 阻塞响应。
- 无原始请求的后台账务、历史记录和其他日志类别不合成来源地址。

## 错误处理策略

IP 提取不增加外部调用或新的用户可见错误。缺失地址保留空串；现有日志写入错误仍由日志管线记录、重试和降级，不传播到 relay 响应。前端加载/保存行为不变。

## Main Chain Impact

同步执行：在已有日志构造函数内判断 Request 是否存在并调用 Gin `ClientIP()`，把结果赋给已有字符串字段，然后沿用原来的非阻塞入队。原有用户设置读取判断被移除。

异步执行：日志数据库写入、重试、fallback 文件和成本/提成 continuation 保持原实现。新增同步 DB 调用 0，Redis 调用 0，外部 HTTP/DNS 调用 0，goroutine 0，锁 0。既有管线 `enabled` 仍可立即停止接收新日志。

不改模型请求 DTO、上游发送、流式转发、请求鉴权、余额计算或结算策略。

## Shared Resource Audit

| 资源 | 当前 relay 使用情况 | 本次增量及隔离结论 |
| --- | --- | --- |
| `LOG_DB.logs` / 既有 IP 索引 | 已由日志 worker 写入 | 行数、INSERT 次数和连接数不变；原先空的 IP 会填入地址，行和索引有效载荷增加。没有新增查询/锁/表扫描。 |
| `LOG_DB` 连接池 | 日志查询和原有批量 writer 共用 | 不打开额外连接，不新增 writer 或同步查询。 |
| 消费/错误日志 buffer 与 mutex | 已有独立有界管线 | 不改变容量、临界区或锁顺序；IP 在入队前保存。 |
| 重试队列、fallback 文件和固定 worker | 日志管线现有旁路 | 已序列化 `Log.Ip`，仅字段内容改变；不新建文件通道或 worker。 |
| `users` / 用户设置缓存 | 鉴权已有使用 | 日志构造不再读取 IP 开关，不新增 DB/缓存访问或批量用户更新。 |
| Redis / 共享 goroutine pool / IP 情报服务 | 本次不访问 | 无新 key、连接、池占用或远程 IP 查询。 |

没有引入跨子系统资源争用，也不通过主链协调锁解决日志问题。

## Concurrency Analysis

30,000 RPM 约为每秒 500 请求，100,000 RPM 约为每秒 1,667 请求。每条原本关闭 IP 记录的日志增加一次本地 IP 解析，移除用户设置分支；启用用户原有路径已执行相同解析。每请求新增 DB/Redis 调用、锁和 goroutine 均为 0，批量写入及队列上限不变。

IPv4/IPv6 地址本身最长约 45 字节，以每请求一条日志估算，100k RPM 下新增 IP 文本上限约 4.5 MB/分钟，未计现有数据库索引和行开销；存在重试错误日志时按实际日志条数增加。现有有界 buffer 限制内存增长。这里是增量分析，不声称已完成压力测试。

## 验证计划

先更新/编写回归用例，再修改实现。测试集中在现有 model 日志测试，避免为简单字段逻辑扩散测试文件。

1. 消费和错误日志分别验证无快照、空设置、false、true，精确断言记录的 IPv4/IPv6。
2. 覆盖可信代理与非可信代理来源；合成上下文没有 Request 不 panic、不伪造 IP。
3. 经真实消费/错误日志入口入队，核验事件里的 IP，在修改请求或释放上下文后仍保持快照值；同步消费日志使用同样策略。
4. 覆盖旧客户端保存 false 后的日志记录；关闭系统消费日志/异步管线仍保持原有行为，cache usage、request_id、quota、token 和 accounting 字段不变。
5. 更新旧的默认 IP 为空断言；保留后台退款/差额账务相关回归，确认未引入来源伪造。
6. 确认本次未修改任何前端文件；没有前端变更，不新增或执行前端专项测试。
7. 执行 `go test ./model ./service ./controller` 的相关包/定向回归，记录通过、失败和跳过情况。已有数据库环境测试用例只清理自身数据。
8. 本次没有 schema、SQL、ORM/driver 或事务修改；若验证日志落库，按 `docs/production-environment.md` 和 `docs/release-checklist.md` 使用真实 MySQL/PostgreSQL，报告实际版本和命令，不用 mock 代替落库证据。部署验收另按现有发布流程执行，本设计不授权部署。

## 实施文件与交付状态

- `model/log.go`：两个日志构造点在 `c.Request != nil` 时取 `c.ClientIP()`，删除 `contextWantsIPLog` 及不再使用的 constant/dto import；没有修改其他日志和结算路径。
- `model/log_test.go`：增加 9 组消费/错误日志入队及 IP 快照回归，更新默认错误日志和同步消费日志的实际落库断言。该测试文件不属于 main 中要求原样保留的上游测试；`model/relay_log_pipeline_test.go` 未修改。
- 本文档及关联异步日志设计：实施后补充实际逻辑和验证结果。

### 实际验证结果

环境：Go 1.26.11 / Windows amd64；本机隔离 MySQL 26.10.0、PostgreSQL 18.7。通过独立 scratch 数据库运行测试，测试结束删除的仅是本轮创建的数据库；没有使用生产库或真实 AI 上游。

证据目录：`_local/ai-request-ip-20261116/`，`verify.py` 记录数据库隔离和执行命令；计数按叶子用例统计，不重复计算父级测试。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 修改前先运行新增 IP 表测试 | 缺省/关闭开关时 IP 为空导致失败；无 Request 且开关为 true 时出现 nil Request panic | `red.txt` |
| 修改后相同测试 | 9 组场景通过，每组检查消费及错误日志 | `green.txt` |
| MySQL 日志落库及 IP/队列专项 | 17 通过，0 失败，0 跳过 | `0-model-mysql.jsonl` |
| PostgreSQL 相同专项 | 17 通过，0 失败，0 跳过 | `focused-postgres-0-model-postgres.jsonl` |
| model 整包（主库 MySQL，日志库 PostgreSQL） | 2,197 通过，5 失败，149 跳过；不宣称整包通过 | `1-model-postgres.jsonl` |
| service 消费/结算/错误/任务/流式相关回归 | 777 通过，0 失败，1 跳过；跳过的是需显式启用的 `TestForkStreamFaultBillingLoad` 压测 | `2-service-postgres.jsonl` |
| controller 日志/用户设置/渠道测试相关回归 | 35 通过，0 失败，0 跳过 | `3-controller-postgres.jsonl` |
| 修改前源码 overlay 复测整包失败项 | 相同 5 个叶子用例全部复现；不修改工作区或放宽断言 | `baseline-0-model-postgres.jsonl` |

实际命令：

```text
go test ./model -run ^TestRelayLogsAlwaysRecordClientIP$ -count=1 -timeout=3m -v
go test ./model -count=1 -json -timeout=10m -run "TestRelayLogsAlwaysRecordClientIP|TestRecord(Error|Consume|TaskBilling)Log|TestEnqueueConsumeLog"
go test ./model -count=1 -json -timeout=10m
go test ./service -count=1 -json -timeout=10m -run "Test.*(Consume|Settlement|RelayError|ProcessChannelError|TaskBilling|Stream).*"
go test ./controller -count=1 -json -timeout=10m -run "Test.*(Logs|LogQuery|UpdateUserSetting|ChannelTest).*"
go test -overlay _local/ai-request-ip-20261116/before-overlay.json ./model -count=1 -json -timeout=10m -run "^Test(HardDeleteRemovesAuthorizationAndChannelOwnership|HardDeleteUserPublishesTombstoneAndPurgesAuthenticationData|User_HardDelete|DisablingUserKeepsAccessTokensAndDeletingRemovesThem)$"
```

整包失败范围：用户硬删除路径的四个顶层测试（其中一个包含两个失败子用例）。删除逻辑访问 `user_login_protections`，而既有 SQLite/主库测试夹具没有创建该表；SQLite 清理还报告缺少 `channel_cost_configs`。用本次修改前保存的 `model/log.go`、`model/log_test.go` 通过 Go overlay 复测，得到相同失败集合。没有把这项基线问题改成跳过或混入 IP 修改中修复。

`gofmt` 与本次文件的 `git diff --check` 通过。安全页面及卡片与修改前字节比对一致，本次未写入前端或 locale 文件。没有数据库 schema/SQL/driver 改动，未做升级迁移演练、真实上游验收、负载测试或部署；这些结果不能从上述回归推断。
