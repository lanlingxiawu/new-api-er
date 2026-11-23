# 上游耗时定位简化改进

日期：2026-11-21。状态：用户确认后已实施，包含后续要求的紧凑布局和精简文案；尚未部署。

## Goals and scope

改进日志详情现有的“上游交换”卡片，让管理员直接看出时间花在哪两个观测点之间，并保留判断所需的最少信息。界面保留阶段间隔、写入结果和已知请求体大小；沿用现有单栏、对比栏和移动端布局。

本次不增加页面、配置项、请求正文采集、外部查询或自动归责规则。历史数据中未记录的信息保持未知。

## Existing Logic and Related-Path Findings

以下为实施前从工作区代码核实的行为；本次变更在后文说明：

- `relay/channel/api_request.go::DoApiRequest`、`DoFormRequest`、`DoTaskApiRequest` 共用 `doRequest`。`ApplyUpstreamBodyMetadata` 已将可重放请求体的已知大小写入 `req.ContentLength`；无需再次读取正文。WebSocket 的发送路径独立，不在此次采集范围内。
- `doRequest` 在 `diagnosticClient.Do` 返回后，以 `RelayInfo.StartTime` 为起点保存一次 `UpstreamExchangeClock.Snapshot`。`relay/common/stream_response_capture.go::StreamDiagnosticHTTPClient.Do` 沿用原客户端并包装响应观察，不等待消费完整响应体。
- `relay/common/upstream_timing.go` 为每次应用层调用记录首次连接、首次写请求回调、首次响应字节和调用返回时间，单位是从整个请求开始累计的毫秒。`0` 有意义，缺失值用空指针表示。
- `service/upstream_exchange_trace.go` 当前忽略 `WroteRequestInfo.Err`，而界面把相应时间标为“请求体发送完成”，失败时会产生误导。Go 标准库 `net/http/httptrace/trace.go` 明确允许回调并发、调用返回后到达，以及传输重试时重复调用 `WroteRequest`。
- 超时和计费使用 `service/relay_timeout_input.go::relayUpstreamWroteRequestTrace` 的独立标记，并已检查 `Err == nil`。现有 `httptrace.WithClientTrace` 正确组合回调；本次不得改变这一行为。
- `service/log_info_generate.go::AppendRelayLogAdminInfo` 已将尝试列表放入 `other.admin_info.upstream_exchange`。`model/log.go` 的消费和错误日志通过现有 `enqueueAsyncRelayLog` 管线保存；关闭正文请求日志不影响已有这组元信息的采集路径。
- `common/utils.go::NewRequestId` 的当前格式是 23 位 UTC 时间（14 位年月日时分秒、9 位纳秒）加 16 位字母数字后缀。`middleware/request-id.go::RequestId` 在后续处理前生成 ID；它不是“请求体读取完成”的标记。后缀中的模块哈希也不是机器标识。
- `web/src/features/usage-logs/components/dialogs/log-detail-body.tsx::UpstreamExchangeValue` 原先仅展示累计时间。先前实现的 ID 差值由同一 `UsageLog` 的两个 ID 在前端计算，没有额外采集；本轮因无法可靠区分阶段而移除。
- 现有 `canViewUpstreamExchange ?? isAdmin` 控制展示；后端普通用户投影移除 `admin_info`，管理员和授权员工客户日志保留该数据。现有详情测试覆盖这些可见性差异。
- 相关设计为 `docs/design/relay-local-wait-optimization.md`。它描述的“body sent”缺少对失败回调的区分；实施后同步修正该说明。

未能从现有证据确认的事项是历史慢请求的实际网络协议、请求体字节数、双方时钟误差，以及上游何时收完正文。此次展示不会将这些未知事项推定为事实。

## 展示与业务逻辑

### 必要性复核（2026-11-21）

按用户“定位不出问题的没必要收集”的要求收窄后续改动，只补一个 `get_conn_ms` 分界点，不增加连接复用、DNS、TCP、TLS 明细。它与现有 `conn_ms` 的差值能区分取连接前等待和连接获取阶段，不承诺单凭阶段时间认定本地或上游故障。

| 信息 | 能指导的排查 | 决定 |
| --- | --- | --- |
| 首次开始取连接 `get_conn_ms` | 1.6 秒主要发生在开始取连接前，还是连接获取过程中 | 唯一新增采集点 |
| 已有连接、写入、首字节、调用返回时间 | 区分连接后写入等待、响应等待；调用返回时间还覆盖没有首字节的失败和响应头接收阶段 | 保留，不另存派生间隔 |
| 首次写入结果 | 避免把失败的写入回调当作上传完成 | 保留 |
| 声明请求体大小 | 写入阶段慢时核对是否与请求体体积相关；不以体积直接判责 | 保留已有标量，不读正文 |
| 实际响应协议 | 可辅助协议对照，但不能说明当前请求慢在哪个阶段 | 按进一步精简要求移除本功能的采集和展示；渠道协议配置及传输行为保持原样 |
| ID 时间差 | 受跨机时钟和 ID 生成位置影响，不能确定正文收完时间或故障归属 | 移除推算、展示、仅有 ID 时的诊断分区入口；原始两个 ID 及上游查询保持原样 |
| 连接复用、DNS/TCP/TLS 全量明细 | 对当前“前置等待还是获取连接慢”的分阶段问题非必需 | 不采集 |

`get_conn_ms` 在既有 `httptrace.GetConn` 回调第一次到达时记录，起点仍是整条请求的 `StartTime`，不读取或保存回调中的目标地址。界面复用原阶段间隔区，增加“首次取连接前”和“获取连接”；前者仅在第一个记录的尝试展示，表示请求计时开始至首次观测到 GetConn 的累计等待（包含此前前置处理，不等于本地 CPU 时间）。后续尝试不将累计时间当作本次准备耗时。获取连接区间按 `conn_ms - get_conn_ms` 计算；旧日志缺失、非法或乱序时不推算。缺少 GotConn 也不臆断网络故障。

所有新增数据仍受原关闭开关控制。每次尝试最多增加一个请求私有原子时间记录与一个可选整数 JSON 字段，不新增 I/O、跨请求状态、工作协程或查询。测试覆盖回调组合、首次记录、晚到回调、零值、关闭开关和旧日志/重试显示。

用户随后明确允许去除不必要项。本轮移除协议字段和 ID 时间推算，清理对应解析、显示、测试及未引用翻译；缺失请求体大小时省略整行，不再显示占位文案。仍保留 `end_ms`：失败可能没有首字节，且 HTTP 返回与首次收到字节并不总是同一时刻，不能因成功样例相近就删除。

### 1. 保留累计时间，直接展示阶段间隔

继续显示四个时间点，并明确这些值是“自请求开始”。在同一卡片内补充：

| 展示 | 计算 | 含义 |
| --- | --- | --- |
| 首次取连接前 | 首个记录尝试的 `get_conn_ms` | 从请求计时开始到首次开始取连接；不作为后续重试的本次准备耗时 |
| 获取连接 | `conn_ms - get_conn_ms` | 获取可用连接的观测间隔，可能包含池等待或建连，不直接判责 |
| 连接后至写入结束 | `wrote_ms - conn_ms` | 两次首次观测之间的间隔，可能包含读取请求体、网络等待及传输重试 |
| 写入结束至首字节 | `first_byte_ms - wrote_ms` | 两次首次观测之间的间隔，不等同于模型计算时间 |

只有两个端点均为有效非负数、顺序正确时才显示间隔。保留合法的零；缺失或异常值不补零。首字节早于写入结束时说明这一事实，不输出负的“耗时”。不计算四个累计时间之和，也不将 `end_ms` 标作响应接收完成。

“请求体发送完成”改为“首次写入结束”：新数据同时显示“已完成”或“失败”；旧数据说明“结果未记录”。“失败”只说明首次写入回调结果，不覆盖整条请求最终状态。底部短句说明累计观测、可能含重试及 HTTP 返回后仍可能接收正文；不输出故障归属结论。

### 2. 已知请求体大小

每次尝试的标题右侧显示已知的声明请求体大小，例如“请求体声明大小：327,680 B”。按当前语言分组显示精确字节数，窄屏自动换行。

- 大小取构建好的出站请求 `ContentLength`，表示声明的正文大小，不是实际写入字节数，也不是 Nginx 的响应大小。
- 正值直接记录；`Body == nil` 或 `Body == http.NoBody` 时记录 0；其余长度为 0 或负值时保持未知。禁止为了统计大小再次读、复制或包装正文。
- 缺失或无效大小省略展示；合法的 0 仍显示。旧数据不会因为缺少字段而被当作 0 字节。
- 不再在本功能复制、存储或显示 `resp.Proto`，旧日志已有的 `protocol` 字段直接忽略，无需清理历史数据。

### 3. 布局

沿用一个 `DetailSection` 卡片，不增加折叠层级或设置项。每次尝试由标题与已知大小、四个累计时间、阶段间隔组成；间隔在宽屏并排、窄屏纵向，数值右对齐。新增分界点沿用同一区域的两行，旧数据不显示无法计算的行。四个时间点沿用宽屏四列、窄屏两列。异常说明只在对应状态出现。仅有 ID 的历史日志不再为时间推算创建诊断分区。

## Data flow and API contracts

现有出站请求 → 原 HTTP trace 记录时间及首次写入结果 → `doRequest` 复制已知大小到尝试快照 → `AppendRelayLogAdminInfo` → 现有异步日志管线 → 现有日志列表 API → 现有详情卡片计算阶段间隔。

沿用 `GET /api/log`（`AdminAuth`）以及 `GET /api/log/employee`（`UserAuth` 加现有员工客户范围校验）的查询参数、分页和响应外层结构。普通用户的 `GET /api/log/self` 投影不扩大。无需改变路由、授权、请求参数、缓存或上游日志查询接口。

## Data model and configuration

只向既有 JSON 的 `admin_info.upstream_exchange[]` 追加三个可选字段：

| 字段 | Go / JSON | 约定 |
| --- | --- | --- |
| `get_conn_ms` | `*int64` / number | 首次开始获取连接，距整条请求开始的毫秒数；未观测则缺失 |
| `wrote_ok` | `*bool` / boolean | 对应现有首次 `wrote_ms`；false 不能被省略；未观测时缺失 |
| `request_body_bytes` | `*int64` / number | 已知正文长度；0 与未知明确区分 |

保留原有四个字段及其起点、单位。没有表、列、迁移、索引、查询条件或数据库驱动修改；SQLite、MySQL、PostgreSQL 和现有日志数据库仅保存同一 JSON 字段的扩展内容。无需历史回填。

沿用 `UPSTREAM_EXCHANGE_TRACE_DISABLED=true`：同时停止本次新增的逐尝试元信息采集，保留原超时计费 trace。无新环境变量或后台配置项。

## 实现位置与错误处理

- `relay/common/upstream_timing.go`：扩展快照。用一个原子指针发布不可变的首次写入时间/结果二元组，使并发快照不会将一次时间配到另一次结果；其余时间点沿用已有逻辑。没有观察到完整二元组时省略两项，不等回调补齐。
- `service/upstream_exchange_trace.go`：将首次 `GetConn` 时间和 `WroteRequestInfo.Err == nil` 交给时钟；不记录目标地址，保留一次性组合现有回调。
- `relay/channel/api_request.go::doRequest`：在现有采集开关内复制已知请求大小，不修改请求及原错误处理；不再复制响应协议。
- `web/src/features/usage-logs/types.ts`：扩展可选字段。`lib/upstream-timing.ts` 只保留阶段间隔和有效时间校验，删除 ID 解析与差值计算。
- `components/dialogs/log-detail-body.tsx`：复用 `DetailSection`、原四列和 `CompareCell`；派生数据在渲染时计算，沿用现有 i18n 和七语言翻译流程。保留工作区正在修改的详情分区、对比和组件样式。
- `components/dialogs/log-detail-sections.ts`：沿用原日志类型和权限校验，删除仅凭两个 ID 开放诊断分区的分支，无新请求。

未知、乱序、解析失败均局部隐藏派生值或显示未记录，不抛出到详情页面，不触发查询或后台重试。诊断代码不保存请求正文、凭据或原始传输错误文本，不返回新的 relay 错误。晚到回调只操作请求私有时钟，不访问可复用 Gin 上下文，也不回写已提交快照。

## Main Chain Impact

同步部分仅为既有 trace 的请求私有原子记录、读取 `ContentLength` 和构造小型快照。新增数据随既有日志事件异步持久化；前端阶段计算只发生在查看详情时。进一步精简只删除协议复制及一个 JSON 字段，没有新增主链行为。

不新增同步 DB/Redis/网络/文件调用，不等待回调，不创建定时器或 goroutine，不读取正文，不改超时、计费、重试、连接策略和响应流。失败回调的诊断结果与现有计费标记保持独立。

## Shared Resource Audit

| 资源 | 本次使用 | 对主链的影响 |
| --- | --- | --- |
| 请求私有时钟/尝试切片 | 增加首次写入结果和少量标量 | 无跨请求共享状态或协调锁 |
| HTTP client / transport / 连接池 | 只沿用现有请求与响应 | 无新连接、池修改或额外调用 |
| 现有日志事件队列、`logs.other`、日志 DB 池 | 每次尝试增加最多三个小字段 | 事件数量、批量写入、索引及查询不变；只有有限的元数据大小增长 |
| Redis、全局缓存、文件句柄、worker 池 | 无新增使用 | 无新 key、缓存、句柄或 worker 争用 |

## Concurrency Analysis

按每请求一次尝试估算，30k RPM 为 500 次/秒，100k RPM 为约 1,667 次/秒。本次每次尝试增加常数次请求私有原子操作、至多一个成功发布的写入记录，以及三个小字段；必要性复核只再增加一项 GetConn 首次时间记录。DB/Redis/外部网络调用增量为 0，新锁及 goroutine 为 0。传输回调重复到达时保留首次记录，不积累回调数组；应用层重试继续沿用既有尝试列表。

这是代码成本分析，不是吞吐压测结论。日志序列化尺寸会小幅增加，但不会为字段增加日志事件或独立持久化任务。

## 验证与交付

本轮先调整现有回归，确认原实现仍会记录协议、显示无数据大小及按 ID 创建诊断页，再删除对应行为。最终验证：

- `go test ./relay/channel ./relay/common -count=1`：通过。覆盖已知/空/未知正文长度、关闭开关、请求正文与响应内容不变，以及快照不再序列化协议。只调用本地 `httptest`。
- `relay/channel/api_request_redirect_test.go::TestUpstreamExchangeMetadataInLog` 调用实际 `service.AppendRelayLogAdminInfo` 和日志 JSON 编码，确认 `get_conn_ms:0`、`wrote_ok:false` 与零字节保留，协议字段省略。
- `bun run test src/features/usage-logs/lib/__tests__/upstream-timing.test.ts src/features/usage-logs/components/__tests__/detail-preview.test.tsx src/features/usage-logs/components/__tests__/log-comparison.test.tsx`：3 个文件、83 项通过。覆盖阶段差值、缺失/乱序时间、旧日志、权限、对比、100ms 前置等待与 1.5s 连接获取拆分；验证旧协议字段被忽略、缺失大小不显示占位、原始两个 ID 仍可见且不产生时间诊断。已删除被移除解析功能的专用用例。
- 修改文件的定向 `oxlint`、`oxfmt`、`gofmt` 和 `git diff --check` 通过。全局 `bun run lint` 仍被其他临时文件阻塞：`.tmp/monitor-parameter-review/parameters.test.tsx:100` 的 `curly`，以及 `.tmp/monitor-ui-review/entry.tsx` 的嵌套三元、非空断言和组件导出错误。本次未修改这些文件。
- `bun run typecheck` 仍因脚本调用不存在的 `bun check` 失败；实际编译器 `bunx --no-install tsgo -b` 通过。
- 通过 `add-missing-keys.mjs` 从七语言删除 5 个无引用文案键，运行 `bun run i18n:sync`，校验删除完成、无 BOM；当前保留 9 个本功能新增文案键。临时脚本已删除，其他工作新增文案保留。
- 本轮未修改 trace 回调和原子同步。前轮独立 trace 文件的常规及 race 测试已通过；完整 service 包的 DB 集成测试因未配置 `SQL_DSN` 未运行，本轮不重复该集成测试。
- 初始布局曾做桌面、移动端和对比视图检查；后来 GetConn 布局的浏览器复核工具未连接成功。本轮只删除展示内容，依靠组件回归和类型检查验证，未新增浏览器截图，不将早期截图计为最终布局验收。

新增开始取连接时间、写入结果和请求体声明大小只对实施后的请求生效；旧日志仅能使用原有时间。未执行生产部署或吞吐压测。
