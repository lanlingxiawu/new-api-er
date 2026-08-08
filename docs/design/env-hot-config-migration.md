# 环境变量迁移到后台热更新配置

日期：2026-07-31

## 状态

- 状态：第一批已实现
- 范围：限流热配置、数据库连接池热配置及整组保存 API
- 实现日期：2026-07-31

> 实现说明：第一批使用 `model.configGroupMu` 串行化新模块的整组保存与
> `loadOptionsFromDatabase` 的 DB 读取，避免旧同步结果覆盖新保存结果；请求路径通过
> `atomic.Pointer` 读取不可变快照。文档中面向所有既有 setting 模块的通用
> `configUpdateMutex` 迁移尚未实施，保留为后续批次，避免在本批扩大回归面。
> 管理端已增加“网关限流”和“数据库连接池”两个系统调优 section，整组保存走
> `PUT /api/option/group`。

**第二版**修掉的三处（读侧）：

1. **§5.2**：请求路径不再热读注册进 `config.GlobalConfig` 的裸结构体
   （那与反射写并发时是 data race），改为"草稿结构体 + `atomic.Pointer` 不可变快照"。
2. **§5.3 / §6.1**：新增整组原子保存端点。"一字段一行 option"
   只解决了字段互不覆盖，解决不了一组配置原子生效。
3. **§10**：把"严格校验"与"静默钳制"拆成三层职责，消除
   "展示值 / 存储值 / 生效值三者不一致"。

**第三版**修掉的四处（写侧与授权边界）：

4. **§6.1.1**：整组端点改为**模块 + 字段双白名单**。
   原来的"已注册模块即可"会让它变成通用配置写入口，
   绕过 `controller.UpdateOption` 里逐条积累的业务不变量
   （`last_reset_at` 只读、支付合规、OAuth 前置检查、gemini/claude 专用校验）。
5. **§5.2.4**：引入 `configUpdateMutex` 作为草稿的**唯一**同步边界。
   原来说的"由 `OptionMapRWMutex` 保护"是错的——
   `SaveToDB` / `ExportAllConfigs` 走的是 `ConfigManager.mutex`，
   与 `handleConfigUpdate` 的写互不排斥，这是**本次改动之前就存在**的竞争。
6. **§5.3.2**：把"读基准 → 合并 → 校验 → 事务 → 灌草稿 → 发布 → apply"
   整体纳入同一临界区。只锁最后两步挡不住两个管理员各自基于旧基准校验，
   合并出非法组合甚至互相覆盖。
7. **§5.3.4**：后台同步的 DB 读移进锁内。
   `loadOptionsFromDatabase` 现在在锁外 `AllOption()`，
   会用旧查询结果覆盖管理员刚发布的新配置——
   `atomic.Pointer` 保证不了"哪份更新"。

---

## 1. 背景与问题

`.env.example` 目前列了 90 多个后端变量，绝大多数在 [common/init.go](../../common/init.go)
里一次性读进包级全局。改任何一个都要重启进程。

这在两类场景下是实打实的可用性成本：

1. **限流参数**。2026-07-30 的 429 事故（见
   [dashboard-api-rate-limit-429.md](dashboard-api-rate-limit-429.md)）里，
   排查和处置全程要改 `.env` + 重启。而重启本身会打断在途请求，正是限流最需要
   即时调整的时刻反而最不能动。
2. **数据库连接池**。`SQL_MAX_OPEN_CONNS` 打满时唯一的手段是重启，
   而重启会让连接池从零开始重建，短时间内 relay 反而更容易超时。

同时，仓库里已经有成熟的热更新配置机制（`setting/` + `config.GlobalConfig`），
迁移不需要新造轮子。

### 1.1 判据：能不能热更新，看"消费点"不看"读取点"

一个常见误解是"只要值在全局变量里就能改"。实际判据是这个全局**怎么被用掉**：

| 消费方式 | 能否热更新 | 例子 |
|---|---|---|
| 每次请求/每次调用现读现用 | **能**，换成 setting getter 即可 | `constant.StreamingTimeout`、`common.UserSessionActiveLimit` |
| 启动时喂给长生命周期对象，之后对象自持 | **要额外写"应用"钩子** | `http.Transport`、`sql.DB` 连接池、后台 goroutine 的 ticker |
| 闭包在**路由注册时**捕获了数值 | **要先改闭包**，否则搬进 setting 也不生效 | `rateLimitFactory` |
| 引导阶段决定进程形态 / 身份 / 密钥 | **不能，也不该** | `SQL_DSN`、`SESSION_SECRET`、`TRUSTED_PROXIES` |

第三类是本次最容易踩的坑，§5.5 单独展开。

---

## 2. 目标和范围

### 2.1 目标

- 给出全部 `.env.example` 变量的三级分类（可热更新 / 需应用钩子 / 必须留 env），
  作为后续分批迁移的依据。
- 确立一条统一的迁移范式：**DB > env > 内置默认**，存量部署的 `.env` 不失效。
- 第一批落地两组：**限流参数**与**数据库连接池**。

### 2.2 非目标

- 不一次性迁移全部变量。本文档的分类是长期依据，实施按批次走。
- 不改变任何限流算法、分桶维度或连接池语义。本次只改"值从哪来、什么时候生效"。
- 不引入新的配置存储。继续用 `options` 表 + `config.GlobalConfig`。
- 不做配置变更审计日志（可作为后续独立需求）。

---

## 3. 迁移范式

### 3.1 优先级范式：`pprof_setting`

[setting/pprof_setting/config.go](../../setting/pprof_setting/config.go) 是本仓库里
唯一完整实现了"env 只作启动默认值"的模块。**它只是优先级的范式，不是并发实现的模板**——
它直接热读裸结构体字段，与 `handleConfigUpdate` 的反射写并发时是数据竞争，
只不过 pprof 开关的读取频率极低、至今没暴露。本次迁移的两个模块都在请求路径上，
必须叠加 §5.2 的快照机制。优先级部分照此办理：

```go
var pprofSetting = PprofSetting{Enabled: false}   // ① 内置默认

func init() {
    config.GlobalConfig.Register("pprof_setting", &pprofSetting)  // ② 注册
}

// ③ env 覆盖内置默认。必须在 model.InitOptionMap() 之前调用。
func ApplyEnvDefaults() {
    if strings.TrimSpace(os.Getenv("ENABLE_PPROF")) == "true" {
        pprofSetting.Enabled = true
    }
}

func IsEnabled() bool { return pprofSetting.Enabled }   // ④ 每次现读
```

调用顺序在 [main.go:358](../../main.go#L358)：`ApplyEnvDefaults()` → `InitOptionMap()`，
于是优先级天然是 **DB > env > 内置默认**。管理员在后台存过一次，之后 env 就不再干预；
从未在后台改过的存量部署，行为与升级前完全一致。

### 3.2 反面范式：`GetMonitorSetting`

[setting/operation_setting/monitor_setting.go:33](../../setting/operation_setting/monitor_setting.go#L33)
在 **每次 getter 调用**里读 `os.Getenv` 并覆盖内存值：

```go
func GetMonitorSetting() *MonitorSetting {
    if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" { ... 覆盖 ... }
```

后果是 env > DB：只要 `.env` 里写了值，后台怎么改都会被下一次读取覆盖回去，
而界面上还显示着管理员刚保存的值。这是本次要避免的具体错误，**不要复制这个写法**。
迁移过程中如果顺手，可以把 `monitor_setting` 一并纠正为 §3.1 的形态。

### 3.3 每个迁移项的固定步骤

1. 在 `setting/{module}_setting/` 定义**草稿结构体**，字段扁平、带 `json` tag（§5.1）。
2. `init()` 里 `config.GlobalConfig.Register("{name}", &s)`。
3. 定义**不可变快照类型**与 `atomic.Pointer`，写 `Publish{Module}Setting()`（§5.2）。
4. 写 `ApplyEnvDefaults()`，在 `InitOptionMap()` 之前调用，紧跟一次 `Publish`。
5. 写入校验函数：越界与跨字段非法组合**直接拒绝**，不靠钳制掩盖（§10）。
6. 把原消费点从 `common.X` 改成 `Get{Module}Snapshot()`，
   **一次 `Load` 用到底**，不要每个字段各 `Load` 一次。
7. `.env.example` 对应条目补一句"仅作启动默认值；后台改过后以数据库为准"。

---

## 4. 全量分类清单

> **这是初步分类，不是终审结论。** 分类依据是对每个变量消费点的一轮通读，
> 但"读取点"与"消费点"可能不止一处——`DEBUG` 就是例子：它既被
> `common/redis.go` 等处每次现读（看起来是 A 类），又在
> [main.go:67](../../main.go#L67) 被 `kitutil.Debug.Store(common.DebugEnabled)`
> 复制进另一个原子变量（实际是 B 类，热更新必须同时回写 `kitutil.Debug`）。
> **每批实施前必须对该批变量重新做一次全仓 grep 核对**，
> 确认没有第二个复制点或长生命周期持有者。

### 4.1 A 类 —— 可直接热更新（消费点在请求路径上）

改造 = 建 setting 模块 + 替换消费点，不需要重建任何对象。
**但仍需 §5.2 的快照机制**——"消费点在请求路径上"意味着并发读，
换成 setting getter 后就与配置写入构成竞争。

| 分组 | 变量 | 当前消费点 |
|---|---|---|
| **限流** | `GLOBAL_API_RATE_LIMIT[_ENABLE/_DURATION]`、`GLOBAL_API_USER_RATE_LIMIT[_ENABLE/_DURATION]`、`GLOBAL_WEB_RATE_LIMIT[_ENABLE/_DURATION]`、`CRITICAL_RATE_LIMIT[_ENABLE/_DURATION]`、`AUTH_REFRESH_RATE_LIMIT[_ENABLE/_IP/_DURATION]`、`SEARCH_RATE_LIMIT[_ENABLE/_DURATION]`、`LOG_EXPORT_RATE_LIMIT[_ENABLE/_DURATION]` | [middleware/rate-limit.go](../../middleware/rate-limit.go)，**部分闭包捕获，见 §5.5** |
| **relay 行为** | `GEMINI_SAFETY_SETTING`、`COHERE_SAFETY_SETTING`、`FORCE_STREAM_OPTION`、`CountToken`、`AZURE_DEFAULT_API_VERSION`、`GET_MEDIA_TOKEN`、`GET_MEDIA_TOKEN_NOT_STREAM`、`DIFY_DEBUG`、`ALI_ANTHROPIC_MESSAGES_MODELS` | `constant.*` / `common.*`，每请求读 |
| **请求体与流限制** | `MAX_REQUEST_BODY_MB`、`ANONYMOUS_REQUEST_BODY_LIMIT_KB`、`MAX_FILE_DOWNLOAD_MB`、`STREAM_SCANNER_MAX_BUFFER_MB`、`STREAMING_TIMEOUT` | 中间件 / stream scanner，每请求读 |
| **会话策略** | `USER_SESSION_ACTIVE_LIMIT`、`USER_SESSION_ISSUANCE_LIMIT`、`USER_SESSION_ISSUANCE_WINDOW_SECONDS`、`USER_SESSION_REVOKED_RETENTION_DAYS`、`USER_SESSION_HOURLY_ALERT_THRESHOLD` | [auth_session.go:72](../../service/auth_session.go#L72)、[auth_cleanup.go:34](../../service/auth_cleanup.go#L34)、[user_session.go:780](../../model/user_session.go#L780) |
| **通知与异步任务** | `NOTIFY_LIMIT_COUNT`、`NOTIFICATION_LIMIT_DURATION_MINUTE`、`TASK_QUERY_LIMIT`、`TASK_TIMEOUT_MINUTES`、`TASK_PRICE_PATCH`、`POLLING_INTERVAL` | 每次通知 / 每轮轮询读 |
| **外部端点与同步** | `LINUX_DO_TOKEN_ENDPOINT`、`LINUX_DO_USER_ENDPOINT`、`TRUSTED_REDIRECT_DOMAINS`、`BINANCE_PROXY_URL`、`SYNC_UPSTREAM_BASE`、`SYNC_HTTP_TIMEOUT_SECONDS`、`SYNC_HTTP_RETRY`、`SYNC_HTTP_MAX_MB` | 每次调用读 |
| **SMTP 传输开关** | `SMTP_STARTTLS_ENABLE`、`SMTP_INSECURE_SKIP_VERIFY`（含 `_ENABLED` / `SMTP_TLS_` 旧别名） | 每次发信读。**SMTP 服务器和账号早已在后台设置页**，只有这两个开关卡在 env，属于明显的不一致 |
| **缓存 TTL** | `SUBSCRIPTION_PLAN_CACHE_TTL`、`SUBSCRIPTION_PLAN_INFO_CACHE_TTL` | [subscription.go:55](../../model/subscription.go#L55)，每次取缓存读。对应的 `_CAP` 是建缓存时用的容量，属 B 类 |
| **其他** | `ERROR_LOG_ENABLED`、`GENERATE_DEFAULT_TOKEN`（[user.go:292](../../controller/user.go#L292)，每次注册现读）、`SHUTDOWN_TIMEOUT_SECONDS`（[main.go:232](../../main.go#L232) 只在收到退出信号那一刻读，天然是热的） | |

### 4.2 B 类 —— 可热更新，但需要额外的"应用"钩子

| 变量 | 需要做什么 | 评价 |
|---|---|---|
| `SQL_MAX_IDLE_CONNS`、`SQL_MAX_OPEN_CONNS`、`SQL_MAX_LIFETIME`（含 `LOG_SQL_*` 同名项） | 保存后调一次 `sqlDB.SetMaxIdleConns` 等（[model/main.go:197-199](../../model/main.go#L197-L199)）。**连接池对象不用重建** | 性价比最高，第一批做 |
| `SQL_SLOW_THRESHOLD_MS` | 阈值被 GORM 自己的 `Trace` 消费，改造要么包 `logger.Interface`（破坏 `FileWithLineNum` 归因），要么并发替换 `DB.Config.Logger`（数据竞争） | **剔出第一批，保持 env-only**，详见 §5.7 |
| `SYNC_FREQUENCY`、`CHANNEL_UPDATE_FREQUENCY`、`BATCH_UPDATE_INTERVAL`、`CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES`、`CHANNEL_UPSTREAM_MODEL_UPDATE_MIN_CHECK_INTERVAL_SECONDS` | 把后台 goroutine 里的固定 sleep 改成每轮循环现读配置，不必重启协程 | 第二批 |
| `RELAY_TIMEOUT`、`RELAY_IDLE_CONN_TIMEOUT`、`RELAY_MAX_IDLE_CONNS`、`RELAY_MAX_IDLE_CONNS_PER_HOST`、`TLS_INSECURE_SKIP_VERIFY` | [http_client.go:91-107](../../service/http_client.go#L91-L107) 在 `InitHttpClient()` 里构造 transport；热更新要重建 client 并对旧 client 调 `CloseIdleConnections()` | **`RELAY_TIMEOUT` 语义是混的**：既进 transport，又被 [relay-aws.go:44](../../relay/channel/aws/relay-aws.go#L44) 和 [http.go:61](../../service/http.go#L61) 每请求读。要做先理顺语义 |
| `SUBSCRIPTION_PLAN_CACHE_CAP`、`SUBSCRIPTION_PLAN_INFO_CACHE_CAP` | 容量在建缓存时定死，改容量要重建缓存实例 | 收益低，可不做 |
| `REDIS_POOL_SIZE` | go-redis 池大小不支持运行时调整，只能重建客户端 | **不建议做**，风险不抵收益 |
| `DEBUG` | 除 `common.DebugEnabled` 的每处现读外，[main.go:67](../../main.go#L67) 还把它复制进 `kitutil.Debug`（`atomic.Bool`）。热更新必须同时回写该原子变量，否则 relaykit 内部的调试判断永远停在启动值 | 从 A 类下调至此，作为"一个变量两个消费点"的样板 |

`BATCH_UPDATE_ENABLED`、`MEMORY_CACHE_ENABLED` 属于"后台协程启不启"的开关：
关掉容易、热开起来要处理已积压状态。**建议只热更新间隔，开关仍留 env**。

### 4.3 C 类 —— 必须继续留在 env

| 分组 | 变量 | 理由 |
|---|---|---|
| 引导与身份 | `PORT`、`GIN_MODE`、`VERSION`、`NODE_TYPE`、`NODE_NAME`、`HOSTNAME` | 决定进程形态，启动后没有"改"的语义 |
| 数据源 | `SQL_DSN`、`LOG_SQL_DSN`、`SQLITE_PATH`、`REDIS_CONN_STRING`、`REQUEST_LOG_REDIS_DB`、`LOG_SQL_CLICKHOUSE_TTL_DAYS` | 配置本身就存在这些库里，鸡生蛋 |
| 安全边界 | `SESSION_SECRET`、`CRYPTO_SECRET`、`SESSION_COOKIE_SECURE`、`SESSION_COOKIE_TRUSTED_URL` | 搬进后台等于开出一条"从后台自我降级安全策略"的路径 |
| **`TRUSTED_PROXIES`** | 单独点名 | 在 gin engine 启动时设定，且决定 `c.ClientIP()`，是**所有按 IP 限流的地基**。一次误改就能把全站折叠进同一个桶——正是 [dashboard-api-rate-limit-429.md](dashboard-api-rate-limit-429.md) §8.4 要求上线前核对的那条。**绝不能进后台** |
| 证书与代理 | `NODE_POOL_CLIENT_CERT` / `_KEY` / `_CA_CERT`、`HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | 文件路径与 Go 标准库启动读 |
| 可观测性引导 | `PYROSCOPE_*` | profiler 启动即固定。注意 `ENABLE_PPROF` 已经是热更新的了 |
| 前端注入 | `FRONTEND_BASE_URL`、`UMAMI_WEBSITE_ID`、`UMAMI_SCRIPT_URL`、`GOOGLE_ANALYTICS_ID` | [router/main.go:20](../../router/main.go#L20) 决定路由怎么注册；[main.go:278](../../main.go#L278) 在启动时把脚本标签替换进内嵌的 `indexPage` 字节流 |

### 4.4 文档本身的两处问题（顺带修）

- **`GEMINI_VISION_MAX_IMAGE_NUM` 在 Go 代码里零引用**，`.env.example:104` 是死条目，
  建议直接删除，避免误导运维。
- **`LOG_EXPORT_RATE_LIMIT[_ENABLE/_DURATION]` 有 env 实现但 `.env.example` 未收录**
  （[common/init.go:145-147](../../common/init.go#L145-L147)），补进文档。
  `UPLOAD_RATE_LIMIT` / `DOWNLOAD_RATE_LIMIT` 在 [constants.go:240-244](../../common/constants.go#L240-L244)
  是写死常量、根本没有 env，别被命名骗到。

---

## 5. 第一批方案设计

只做两组：**限流参数**（事故处置时最需要）与**数据库连接池**（改造成本最低、收益最直接）。

### 5.0 模块落位与依赖方向

两个新模块都放进 `setting/operation_setting/`：

| 文件 | 模块名（option 键前缀） |
|---|---|
| `setting/operation_setting/rate_limit_setting.go` | `rate_limit_setting` |
| `setting/operation_setting/db_pool_setting.go` | `db_pool_setting` |

选 `operation_setting` 而不是新建包，理由有二：系统调优页现有的
`ledger-pipeline` / `relay-log-pipeline` / `log-query` / `log-export` 等 section
全部来自这个包，前端注册方式可以直接照抄；依赖方向也安全——
`operation_setting` 只依赖 `setting/config` 与 `common`。

**依赖环检查**：

- `middleware` → `operation_setting`：新增依赖。`operation_setting` 不 import
  `middleware` / `service` / `model`，无环。
- `model` → `operation_setting`：已存在（`relay_log_setting`），无新增风险。

**`common` 里的限流全局变量全部删除，不做双写。** 保留 `common.GlobalApiRateLimitNum`
之类的旧变量并在两处同步，正是 §3.2 那种优先级混乱的温床。已核对消费点：
这批变量只有 `middleware/rate-limit.go` 读，删除后无悬挂引用。
`common/init.go:121-147` 的对应赋值一并移除，改由 `ApplyEnvDefaults` 承担。

### 5.1 配置结构必须是扁平标量（硬约束）

`config.configToMap` 对 struct / map / slice 类型的字段一律
`json.Marshal` 成**一整行 option**（[config.go:143](../../setting/config/config.go#L143)）。
如果按"每个桶一个嵌套结构体"来建模，会有两个后果：

1. 前端要编辑一个 JSON 字符串，而不是一组输入框；
2. `PUT /api/option/` 一次只存一个 key，整桶存成一行意味着**两个管理员同时改不同字段会互相覆盖**。

因此照 [`RelayLogPipelineSetting`](../../setting/operation_setting/relay_log_setting.go)
的既有做法，用扁平标量字段，一个字段对应一行 option：

```go
// setting/operation_setting/rate_limit_setting.go
type RateLimitSetting struct {
    GlobalAPIEnabled      bool `json:"global_api_enabled"`
    GlobalAPINum          int  `json:"global_api_num"`
    GlobalAPIDurationSec  int  `json:"global_api_duration_sec"`

    GlobalAPIUserEnabled     bool `json:"global_api_user_enabled"`
    GlobalAPIUserNum         int  `json:"global_api_user_num"`
    GlobalAPIUserDurationSec int  `json:"global_api_user_duration_sec"`

    GlobalWebEnabled     bool `json:"global_web_enabled"`
    GlobalWebNum         int  `json:"global_web_num"`
    GlobalWebDurationSec int  `json:"global_web_duration_sec"`

    CriticalEnabled     bool `json:"critical_enabled"`
    CriticalNum         int  `json:"critical_num"`
    CriticalDurationSec int  `json:"critical_duration_sec"`

    AuthRefreshEnabled     bool `json:"auth_refresh_enabled"`
    AuthRefreshNum         int  `json:"auth_refresh_num"`
    AuthRefreshIPNum       int  `json:"auth_refresh_ip_num"`
    AuthRefreshDurationSec int  `json:"auth_refresh_duration_sec"`

    SearchEnabled     bool `json:"search_enabled"`
    SearchNum         int  `json:"search_num"`
    SearchDurationSec int  `json:"search_duration_sec"`

    LogExportEnabled     bool `json:"log_export_enabled"`
    LogExportNum         int  `json:"log_export_num"`
    LogExportDurationSec int  `json:"log_export_duration_sec"`
}
```

字段名带 `Sec` 后缀是为了让"这是秒不是毫秒"在调用点自解释——
`relay_log_setting` 用 `FlushIntervalMs` 也是同一考量。

### 5.2 并发模型：草稿结构体 + 不可变快照

**这是本次设计的核心，不是可选优化。**

`config.GlobalConfig` 的现有机制是：`Get(name)` 在 map 锁保护下返回结构体指针，
**锁随即释放**，然后 `UpdateConfigFromMap` 用反射**原地写**这个结构体的字段
（[config.go:160](../../setting/config/config.go#L160)）。
写入侧确实被 `common.OptionMapRWMutex` 串行化了
（[option.go:320](../../model/option.go#L320) 在 `handleConfigUpdate` 外层持写锁），
但**读取侧完全不持任何锁**。

于是"每请求读 `s.GlobalAPINum`"与"后台保存写 `s.GlobalAPINum`"就是一对
无同步的并发读写——按 Go 内存模型这是 data race，`go test -race` 会直接报，
而且不是理论风险：`int` 字段的撕裂在 64 位平台上虽不发生，
但编译器有权把循环内的字段读提升到循环外、或与相邻字段读合并重排，
`bool` 与 `int` 相邻字段的组合读可能拿到"新 Enabled + 旧 Num"。

> 顺带说明：**这个竞争今天就存在**于每一个已注册的 setting 模块
> （`relay_log_setting`、`log_query_setting`、`ledger_pipeline_setting`…），
> 只是它们的读取频率远低于限流。本次不修全仓（见 §16.6），
> 但**新迁移的两个模块必须一次做对**，不能再复制这个形态。

#### 5.2.1 两层结构

| 层 | 是什么 | 谁读写 | 同步方式 |
|---|---|---|---|
| **草稿**（draft） | 注册进 `config.GlobalConfig` 的那个结构体 | 只被配置读写路径碰：`LoadFromDB` / `SaveToDB` / `handleConfigUpdate` / `GET /api/option` | 沿用既有的 `OptionMapRWMutex` 写锁，读取只发生在管理接口 |
| **快照**（snapshot） | 校验+钳制后的不可变值，存在 `atomic.Pointer` 里 | 请求路径只读快照 | `atomic.Pointer.Load/Store`，无锁 |

草稿保留下来是必要的：`SaveToDB` / `LoadFromDB` / `GET /api/option` 全靠反射遍历
这个结构体，去掉它就要重写整套配置持久化。快照则是请求路径唯一能看到的东西。

```go
// 不可变快照。构造后任何字段都不再被修改，因此可以按值随意拷贝。
type RateLimitSnapshot struct {
    GlobalAPI     RateLimitBucket
    GlobalAPIUser RateLimitBucket
    GlobalWeb     RateLimitBucket
    Critical      RateLimitBucket
    AuthRefresh   AuthRefreshBucket   // 比普通桶多一个 IPNum
    Search        RateLimitBucket
    LogExport     RateLimitBucket
}

type RateLimitBucket struct {
    Enabled  bool
    Num      int
    Duration int64
}

var rateLimitSnapshot atomic.Pointer[RateLimitSnapshot]

// PublishRateLimitSetting 用当前草稿重建快照并整体发布。
// 调用方必须已持有配置写锁（即从 handleConfigUpdate / LoadFromDB 进来）。
func PublishRateLimitSetting() {
    snap := buildRateLimitSnapshot(&rateLimitSetting)  // 读草稿 + 钳制
    rateLimitSnapshot.Store(&snap)                     // 一次原子发布
}

// GetRateLimitSnapshot 供请求路径调用。返回的是指向不可变值的指针，
// 调用方可以安全地一路读下去——它永远不会被就地改写，
// 后续发布只会换掉 atomic.Pointer 里的地址。
func GetRateLimitSnapshot() *RateLimitSnapshot {
    if snap := rateLimitSnapshot.Load(); snap != nil {
        return snap
    }
    return &defaultRateLimitSnapshot   // 启动早期兜底，永不返回 nil
}
```

关键性质：**一次请求内看到的一定是同一份完整配置**。中间件在闭包开头
`Load()` 一次，之后所有字段都读这一份，不可能出现"新配额 + 旧窗口"。

`DBPoolSetting` 用同一套形态（`atomic.Pointer[DBPoolSnapshot]` +
`PublishDBPoolSetting()`），只是它的消费者不是请求路径而是 apply 钩子。

#### 5.2.2 钳制发生在发布时，不是读取时

上一版把 `bounded` 放在 getter 里，等于每请求算一遍，还让"存储值"与"生效值"
长期不一致。改成在 `buildRateLimitSnapshot` 里做一次：

```go
func buildRateLimitSnapshot(s *RateLimitSetting) RateLimitSnapshot {
    return RateLimitSnapshot{
        GlobalAPI: RateLimitBucket{
            Enabled:  s.GlobalAPIEnabled,
            Num:      bounded(s.GlobalAPINum, 2000, maxRateLimitNum),
            Duration: int64(boundedWindow(s.GlobalAPIDurationSec, 180)),
        },
        // 其余六个桶同构
    }
}

// 窗口上界与内存兜底的 GC 周期绑定，理由见 §8.1。
func boundedWindow(value, fallback int) int {
    return bounded(value, fallback, int(common.RateLimitKeyExpirationDuration.Seconds()))
}
```

请求路径于是退化为一次 `atomic.Load` + 字段读，`takeRateLimit` 里
`maxRequestNum <= 0` / `duration <= 0` 两个错误分支永远走不到。

**但钳制不再是主要防线**——它只是兜底，正门是 §10 的写入校验。
钳制真正要覆盖的场景只有一个：**升级前数据库里已经存着越界值**
（例如手工改过 `options` 表）。这种情况钳制的同时必须 `common.SysError`
把"存储值 X 被钳制为 Y"打出来，否则就成了静默篡改。

#### 5.2.3 发布时机

| 触发点 | 动作 |
|---|---|
| `ApplyEnvDefaults()` 之后、`InitOptionMap()` 之前 | 发布一次 |
| `InitOptionMap()` / `LoadFromDB()` 加载完**全部** option 之后 | 发布一次（不是每个 key 一次，见 §5.3） |
| 管理员保存（单项或整组） | 在同一临界区内发布一次，见 §5.3.2 |
| 多节点配置同步 | 在同一临界区内发布一次，见 §5.3.4 |

第一条的理由要说准：`InitOptionMap()` 在 `InitResources()` 里执行
（[main.go:366](../../main.go#L366)），而 HTTP 服务要到
[main.go:218](../../main.go#L218) 的 `ListenAndServe` 才起，
**此时根本不会有请求到达**。提前发布不是为了服务早到的请求，而是为了
①`GetSnapshot()` 永不返回 nil，②让"进程内任何时刻都存在一份有效快照"
成为可测试的初始化不变量，测试不必关心是否已经走过加载流程。

#### 5.2.4 草稿的唯一同步边界：`configUpdateMutex`

上一版说草稿"由 `OptionMapRWMutex` 保护"，这句话是错的，而且掩盖了一个
**本次改动之前就存在**的竞争。实际锁的分布是：

| 路径 | 对草稿做什么 | 持有的锁 |
|---|---|---|
| `handleConfigUpdate` | 反射**写**字段 | `common.OptionMapRWMutex`（在 [option.go:320](../../model/option.go#L320) 外层） |
| `ConfigManager.LoadFromDB` | 反射**写**字段 | `ConfigManager.mutex`（写锁） |
| `ConfigManager.SaveToDB` | 反射**读**字段 | `ConfigManager.mutex`（读锁） |
| `ConfigManager.ExportAllConfigs` | 反射**读**字段 | `ConfigManager.mutex`（读锁） |

`ConfigManager.mutex` 保护的是 **map 本身**，`OptionMapRWMutex` 保护的是
**`common.OptionMap`**。两者互不相干，却各自被用来"顺带"保护同一批结构体字段。
于是 `SaveToDB`（反射读）与 `handleConfigUpdate`（反射写）**互不排斥**——
这是今天就存在的 data race，与快照方案无关，但只要我们把
`go test -race` 列为验收条件，它就会立刻暴露出来。

因此不再借用任何一把现有锁，给配置草稿建立**单一同步边界**：

```go
// setting/config/config.go
// configUpdateMutex 是配置草稿结构体的唯一同步边界。
// 所有对草稿的反射读写、钳制回写、快照构造，都必须在它内部完成。
// 它只出现在低频的管理与同步路径上，绝不出现在任何请求路径。
var configUpdateMutex sync.Mutex

// WithConfigUpdate 在临界区内执行 fn。嵌套调用是编程错误（会自死锁），
// 由 go vet / 约定保证：fn 内部不得再调用任何自身加锁的配置 API。
func WithConfigUpdate(fn func() error) error {
    configUpdateMutex.Lock()
    defer configUpdateMutex.Unlock()
    return fn()
}
```

必须迁移到这把锁下的调用点：

- `LoadFromDB` / `SaveToDB` / `ExportAllConfigs` / `UpdateConfigFromMap`；
- `handleConfigUpdate`（拆分后的 `applyConfigDraft`）；
- 单项保存与整组保存的**完整流程**（§5.3.2）；
- 加载钳制的回写（§10.2）；
- 快照构造与 `atomic.Store`。

三条不变式：

1. **请求路径永远不碰这把锁**——它只读 `atomic.Pointer`。
   所以锁的持有时长不影响 relay 与 `/api` 的延迟。
2. **`ConfigManager.mutex` 保留**，但职责收窄为"只保护 configs map 的增删查"，
   不再兼职保护结构体字段。`Register` / `Get` 继续用它。
3. 快照的 `Store` 一定发生在这把锁内，所以**不存在两个发布互相覆盖**——
   这一点上一版说对了结论，但当时给的理由（"在配置写锁内"）指的是一把
   并不覆盖全流程的锁。

##### 实现现状（与本节的差异）

代码里这把锁叫 `configDraftMutex`，入口是 `config.WithConfigDraft(fn func())`
（无返回值，因为现有调用点都不需要在临界区内返回 error）。已经迁移到它下面的是：

- `ConfigManager.LoadFromDB` / `SaveToDB` / `ExportAllConfigs`；
- 新增的 `ConfigManager.UpdateFromMap`，`handleConfigUpdate` 改调它，
  不再直接调包级 `UpdateConfigFromMap`；
- `Get/ReplaceRateLimitSetting`、`Get/ReplaceDBPoolSetting` 的整体读写。

尚未迁移的是 §5.3.2 的整组保存全流程与 §10.2 的加载钳制回写——
`SaveConfigGroup` 目前仍是"取草稿 → 校验 → 落库 → 整体替换"的分段加锁，
两个管理员并发保存同一模块时仍可能各自基于旧基准校验。这不会撕裂结构体
（每段都在锁内），但会丢失一次校验的原子性，属于本节尚未兑现的部分。

锁序固定为 `common.OptionMapRWMutex` → `cm.mutex` → `configDraftMutex`；
`updateOptionMap` 持 `OptionMapRWMutex` 调 `handleConfigUpdate`，
`InitOptionMap` 持 `OptionMapRWMutex` 调 `ExportAllConfigs`，两条路径同序，
且没有任何路径持 `configDraftMutex` 去取前两把锁。

回归用例 `TestRateLimitDraftAccessIsSerialized`：多写多读并发跑，
每个 writer 把同一身份值写进 5 个字段，reader 只要观察到字段间身份不一致
就判定撕裂读；同时有一条协程持续调 `ExportAllConfigs` 触发反射读。
注：本仓库的 Windows 开发环境没有 C 工具链，`go test -race` 跑不起来
（`-race` 需要 cgo），所以该用例靠不变式而不是 race detector 定位问题。

### 5.3 整组原子保存与一次性发布

§5.1 的"一个字段一行 option"只解决了**不同字段互不覆盖**，没有解决**一组配置原子生效**。
这是两个独立的问题，上一版把它们混为一谈了。

#### 5.3.1 逐字段保存会暴露中间组合

当前前端与 `PUT /api/option/` 都是单项写。管理员把连接池从
`open=1000, idle=100, lifetime=60` 调成 `open=200, idle=20, lifetime=3600`，
实际发生的是三次独立保存 + 三次 apply：

```
open: 1000 → 200   apply   此刻 idle=100 > open=200，连接池处于非法组合
idle: 100  → 20    apply
life: 60   → 3600  apply
```

中间那一步 `idle > open` 会被 `database/sql` 静默按 `open` 截断，
虽然不至于崩，但**如果第三次请求失败（网络中断、浏览器关闭），
前两次已经永久落库**，配置就停在一个管理员没打算要的组合上。
限流同理：会短暂出现"新配额 + 旧窗口"。

`UpdateOptionsBulk`（[option.go:280](../../model/option.go#L280)）虽然有 DB 事务，
但提交后仍是**逐 key 调 `updateOptionMap`**，每个 key 触发一次
`handleConfigUpdate` → 一次 apply。所以它解决了"存"的原子性，
没解决"生效"的原子性。

#### 5.3.2 整个流程必须在同一个临界区内

上一版把校验放在临界区之外、只锁最后的"更新草稿 + 发布"，这挡不住两个并发写入。
具体失效场景：

```
草稿：open=1000, idle=100

管理员 A 提交 open=200   ── 读基准 open=1000,idle=100 → 校验 200≥100 通过
管理员 B 提交 idle=500   ── 读基准 open=1000,idle=100 → 校验 500≤1000 通过
                            两人各自基于旧基准校验，都过

A 写库 → 取锁 → 灌草稿 → 发布   草稿变成 open=200, idle=100
B 写库 → 取锁 → 灌草稿 → 发布   草稿变成 open=200, idle=500   ← idle > open
```

两笔各自合法的更新合并出一个非法组合。若 B 的草稿是"读基准时的整份拷贝"而非增量，
还会直接把 A 的 `open=200` 覆盖回 1000，DB 与内存从此不一致。

所以边界要放到**整个流程**外面：

```go
// controller 侧只做参数解析与响应组装，全部状态变更收敛在这里。
func SaveConfigGroup(module string, values map[string]string) (applied bool, err error) {
    err = config.WithConfigUpdate(func() error {
        spec, ok := configGroupRegistry[module]        // ① 白名单，见 §6.1
        if !ok {
            return errUnknownConfigModule
        }
        if err := spec.CheckFields(values); err != nil { // ② 字段白名单
            return err
        }
        merged := spec.MergeWithDraft(values)           // ③ 读基准 + 合并部分字段
        if err := spec.Validate(merged); err != nil {   // ④ 对合并结果做完整校验
            return err
        }
        if err := model.PersistOptionsTx(prefixed(module, values)); err != nil { // ⑤ 事务
            return err
        }
        spec.WriteDraft(merged)                         // ⑥ 灌草稿
        spec.Publish()                                  // ⑦ 构造快照 + atomic.Store
        return spec.Apply()                             // ⑧ apply，仅 db_pool 需要
    })
    ...
}
```

要点：

- **③④ 与 ⑤⑥⑦ 在同一把锁内**，所以"基于什么基准校验的"与"最终写进去的"
  一定是同一份状态。上面 A/B 的场景变成串行：B 的基准是 A 提交后的
  `open=200`，`idle=500` 会被拒绝。
- **⑤ 的 DB 事务在锁内**。这意味着一次配置保存会持锁跨一次数据库往返。
  可以接受，理由是：这把锁**不在任何请求路径上**（请求只读 `atomic.Pointer`），
  而配置写入是 root 手工触发的低频操作，量级是"每天个位数"。
  代价是 DB 卡住时配置接口会一起卡住——但那种情况下配置也确实不该继续写。
  这是明确的取舍，不是疏忽；替代方案（乐观并发 + revision 重试）留在 §16.7。
- **失败即返回，锁内不留半成品**：④ 之前失败一个字节都不写；⑤ 失败草稿不动；
  ⑥⑦ 不会失败（纯内存）；只有 ⑧ 可能失败，那时配置已生效于 DB 与内存，
  只是外部对象没跟上，按 §5.6.3 表达。

`handleConfigUpdate` 相应拆成两段，两段都必须在 `WithConfigUpdate` 内被调用：

```go
func applyConfigDraft(configName string, configMap map[string]string) bool  // 只写草稿
func publishConfig(configName string) error                                  // 发布 + apply
```

#### 5.3.3 单项 PUT 也要走"合并 + 完整校验"

"单项 PUT 等价于只有一个 key 的整组保存"这句话，实现上必须落成同一段代码，
否则跨字段校验会被绕开：当前 `idle=100`，单项把 `open` 改成 50，
如果只校验 `open ∈ [1, 100000]` 就会放过一个 `idle > open` 的非法组合。

因此：**本次两个新模块的单项 PUT，内部直接转调 `SaveConfigGroup(module, {单个字段})`**。
其余既有模块的单项 PUT 保持原样，不受影响（它们没有跨字段约束，
也不在整组白名单里）。

`values` 允许只提交部分字段，语义固定为：

| 环节 | 行为 |
|---|---|
| 合并 | 未提交的字段取**当前草稿的最新值**，不是前端传来的、也不是默认值 |
| 校验 | 基于合并后的**完整**配置，含跨字段规则 |
| 写库 | 只写本次提交的字段，未提交字段不产生 option 写入 |
| 快照 | 基于合并后的完整结果构造 |
| 位置 | 合并与校验必须在 §5.3.2 的锁内，不能在 controller 里先算好再进锁 |

#### 5.3.4 后台同步不得用锁外的旧查询结果覆盖新配置

这是最隐蔽的一条。当前同步链路是
`SyncOptions` → `loadOptionsFromDatabase`（[option.go:231](../../model/option.go#L231)）：

```go
func loadOptionsFromDatabase() {
    options, _ := AllOption()          // ← DB 读，不持任何锁
    for _, option := range options {   // ← 逐 key 更新
        updateOptionMap(option.Key, option.Value)
    }
}
```

DB 读发生在锁外，于是即使只有一个管理员也有这个时序：

```
t0  同步任务执行 AllOption()，拿到旧 options（open=1000）
t1  管理员整组保存 open=200，写库 + 发布新快照
t2  同步任务开始逐 key 灌草稿，把 t0 读到的 open=1000 又发布了一遍
```

`atomic.Pointer` 只保证每份快照内部自洽，**判断不了哪份更新**。
结果是管理员刚改的值在一个同步周期内被悄悄回滚，而 DB 里明明是新值——
这类"改了又变回去"的现象在生产上极难定位。

修法（第一批采用第一种）：

```go
func loadOptionsFromDatabase() {
    _ = config.WithConfigUpdate(func() error {
        options, err := AllOption()        // ← DB 读移进锁内
        if err != nil {
            return err
        }
        applyAllConfigDrafts(options)      // 一次灌完全部草稿
        publishAllConfigs()                // 一次发布 + 一次 apply
        return nil
    })
}
```

关键是 **DB 读必须在锁内**，而不是"取到锁后再用锁外读的结果"。
备选方案是给配置加单调 revision、发布时拒绝旧 revision，
但那要在 `options` 表上加列并改写全部读写路径，第一批不做。

顺带解决了另外两件事：

- 一次同步 = **一次**发布 + **一次** apply，不再随变更字段数抖动；
- 修正了 §9.9 的表述——同步节点是"晚一点整体切换"，而不是"逐字段抖动"。

启动加载（`InitOptionMap` → `LoadFromDB`）走同一条路径，
`LoadFromDB` 本来就是拿到全部 options 之后一次性遍历
（[config.go:42](../../setting/config/config.go#L42)），天然适合整组语义。

### 5.4 `ApplyEnvDefaults`

照 §3.1 的 `pprof_setting` 形态，在 `InitOptionMap()` 之前调用一次：

```go
func ApplyEnvDefaults() {
    s := &rateLimitSetting
    s.GlobalAPIEnabled = common.GetEnvOrDefaultBool("GLOBAL_API_RATE_LIMIT_ENABLE", s.GlobalAPIEnabled)
    s.GlobalAPINum = common.GetEnvOrDefault("GLOBAL_API_RATE_LIMIT", s.GlobalAPINum)
    s.GlobalAPIDurationSec = common.GetEnvOrDefault("GLOBAL_API_RATE_LIMIT_DURATION", s.GlobalAPIDurationSec)
    // ... 其余 19 项同构
}
```

注意 `GetEnvOrDefault` 的第二参数传的是**当前内置默认值**而非字面量，
这样默认值只在结构体初始化处写一遍，不会出现两处默认值漂移。

[main.go:358](../../main.go#L358) 现在是：

```go
pprof_setting.ApplyEnvDefaults()
// 新增：env 覆盖内置默认后立刻发布一次。
// 此时 HTTP 服务尚未启动（ListenAndServe 在 main.go:218），不存在早到的请求；
// 发布是为了让"进程内任何时刻都有一份有效快照"成为初始化不变量，
// 使 GetSnapshot() 永不返回 nil，测试也不必先跑一遍加载流程。
operation_setting.ApplyRateLimitEnvDefaults()
operation_setting.PublishRateLimitSetting()
operation_setting.ApplyDBPoolEnvDefaults()
operation_setting.PublishDBPoolSetting()
...
model.InitOptionMap()      // 内部加载完全部 option 后再统一发布一轮（§5.3.4）
```

顺序不能反：`InitOptionMap` 会先把内存里的默认值导出成 option，再用 DB 里存过的值覆盖。
env 若在其后应用，就变成 env > DB，正是 §3.2 的错误。

`ApplyEnvDefaults` 阶段是单 goroutine 的，草稿结构体此时没有并发读者，
可以直接写字段；一旦 HTTP 服务起来，草稿就只能在配置写锁内改。

### 5.5 middleware 改造

当前实现分成两种，**混在同一个文件里**：

**已经是懒读的**（只把 `Enable` 捕获在注册期，数值每请求现读）：

| 函数 | 现读的字段 |
|---|---|
| `applyAPIUserRateLimit` [rate-limit.go:228](../../middleware/rate-limit.go#L228) | 连 `Enable` 都是每请求读，最干净 |
| `UserCriticalRateLimit` [:282](../../middleware/rate-limit.go#L282) | `CriticalRateLimitNum` / `Duration` |
| `SessionCriticalRateLimit` [:310](../../middleware/rate-limit.go#L310) | `AuthRefreshRateLimitNum` / `AuthRefreshIpRateLimitNum` / `Duration` |

**在路由注册时把数值捕获进闭包的**（搬进 setting 也不会生效）：

| 函数 | 问题 |
|---|---|
| `rateLimitFactory` [:235](../../middleware/rate-limit.go#L235) | `maxRequestNum` / `duration` 是形参，闭包捕获的是**注册那一刻的值** |
| `GlobalWebRateLimit` [:244](../../middleware/rate-limit.go#L244)、`GlobalAPIRateLimit` [:251](../../middleware/rate-limit.go#L251)、`CriticalRateLimit` [:262](../../middleware/rate-limit.go#L262)、`PublicQueryRateLimit` [:344](../../middleware/rate-limit.go#L344) | 除数值外，`Enable` 也在注册期判定：关着启动就返回 `defNext`，后台再打开也没有限流器可用 |
| `userRateLimitFactory` [:362](../../middleware/rate-limit.go#L362)、`SearchRateLimit` [:378](../../middleware/rate-limit.go#L378)、`LogExportRateLimit` [:389](../../middleware/rate-limit.go#L389) | 同上 |

**改造方案**：两个 factory 从"收数值"改成"收取值函数"，`Enable` 判定挪进闭包。
取值函数从 §5.2 的**不可变快照**取桶，不是从草稿结构体读字段。

```go
// bucketProvider 每请求调用一次。它从 atomic.Pointer 取一份不可变快照，
// 再返回其中一个桶的值拷贝——因此单次请求内看到的配置一定自洽。
type bucketProvider func() operation_setting.RateLimitBucket

// 每个限流器一个 provider，闭包里只做一次 Load。
func globalAPIBucket() operation_setting.RateLimitBucket {
    return operation_setting.GetRateLimitSnapshot().GlobalAPI
}

func rateLimitFactory(provider bucketProvider, mark string) gin.HandlerFunc {
    // 保持原样：兜底限流器必须在请求到达前就绪，否则 Redis 故障会与首次初始化竞争。
    inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
    return func(c *gin.Context) {
        bucket := provider()
        if !bucket.Enabled {
            return
        }
        takeRateLimit(c, bucket.Num, bucket.Duration, redisIPRateLimitKey(mark, c.ClientIP()))
    }
}

func userRateLimitFactory(provider bucketProvider, mark string) gin.HandlerFunc {
    inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
    return func(c *gin.Context) {
        bucket := provider()
        if !bucket.Enabled {
            return
        }
        userID := c.GetInt("id")
        if userID == 0 {
            c.Status(http.StatusUnauthorized)
            c.Abort()
            return
        }
        takeRateLimit(c, bucket.Num, bucket.Duration, redisUserRateLimitKey(mark, userID))
    }
}
```

逐个调用点的改法：

| 函数 | 改成 |
|---|---|
| `GlobalAPIRateLimit` | `rateLimitFactory(globalAPIBucket, globalAPIRateLimitMark)` |
| `GlobalWebRateLimit` | `rateLimitFactory(globalWebBucket, globalWebRateLimitMark)` |
| `CriticalRateLimit` | `rateLimitFactory(criticalBucket, criticalRateLimitMark)` |
| `PublicQueryRateLimit` | `rateLimitFactory(criticalBucket, publicQueryRateLimitMark)` —— 与 `CriticalRateLimit` 共用配额值、**不共用 mark**，与现状一致 |
| `SearchRateLimit` | `userRateLimitFactory(searchBucket, searchRateLimitMark)` |
| `LogExportRateLimit` | `userRateLimitFactory(logExportBucket, logExportRateLimitMark)` |
| `UserCriticalRateLimit` | 保留自有闭包（key 要按"有无 userID"二选一），闭包开头 `snap := GetRateLimitSnapshot()`，用 `snap.Critical`，`Enabled` 判定移进闭包 |
| `SessionCriticalRateLimit` | 同上，用 `snap.AuthRefresh`（含 `IPNum`）。**两个分支必须读同一次 `Load` 的结果**，否则会出现"会话配额来自新快照、IP 配额来自旧快照" |
| `applyAPIUserRateLimit` | 已是全懒读，仅换取值来源为 `snap.GlobalAPIUser` |
| `DownloadRateLimit` / `UploadRateLimit` | **不进配置模块**：它们在 [constants.go:240-244](../../common/constants.go#L240-L244) 是写死常量、本来就没有 env。用一个 `staticBucket(num, duration)` 返回固定 provider 即可，行为零变化 |

provider 一律写成独立的具名函数（`globalAPIBucket` 等），**不要传方法值**。
上一版曾论证"绑定 receiver 指针是安全的，因为全局结构体地址不变"——
在快照方案下这个论证连同它依赖的前提一起作废：现在每次都必须重新
`Load()`，绑定任何指针都会把请求钉死在某一版快照上。

**`defNext` 的语义变化**：改造后所有限流中间件都会被注册，不再有"注册期返回 `defNext`"
的分支，关闭状态由闭包内的 `bucket.Enabled` 表达。这正是"关着启动、后台打开"能生效的原因。
代价是即使全部关闭，每个 `/api` 请求也会多一次 `atomic.Pointer.Load` + 三个字段读，
无锁无分配、无 CAS，与现在读 `common.GlobalApiRateLimitNum` 同量级。
改造覆盖了 `defNext` 现有的全部 8 个返回点，该变量随之删除。

**关闭 ≠ 计数不拒绝**：`Enabled=false` 时必须在 `takeRateLimit` **之前**返回，
不能进去计数再放行——否则 Redis 里会堆积一批永不被读的计数器 key。
`TestDisabledRateLimiterStillPassesRequestsThrough` 锁定这一点。

**不变的东西**（改造不得触碰，已有回归测试锁定）：

- 所有 mark 常量与 Redis key 前缀 `rateLimit:v2` 一字不改；
- 分桶维度不变：`CT` 按 IP、`CTU` 按用户、`CTS` 按会话且**有效会话绝不计入 IP 桶**；
- `takeRateLimit` 的 Redis 故障降级到内存计数器的行为不变；
- `TestEveryRateLimiterUsesItsOwnCounter` 与
  `TestSessionCriticalRateLimitNeverChargesValidSessionsToTheIPBucket` 必须继续通过。

### 5.6 数据库连接池：apply 钩子与失败可见性

[model/main.go:197-199](../../model/main.go#L197-L199) 现在是：

```go
sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))
```

`database/sql` 的这三个 setter **本来就允许运行时反复调用**，连接池对象不需要重建：
调小 `MaxOpenConns` 会让超出的连接在归还时关闭，调大立即生效。所以改造只是把
"启动时调一次"变成"启动时调一次 + 配置保存后再调一次"。

```go
// setting/operation_setting/db_pool_setting.go
type DBPoolSetting struct {
    MaxIdleConns       int `json:"max_idle_conns"`
    MaxOpenConns       int `json:"max_open_conns"`
    MaxLifetimeSec     int `json:"max_lifetime_sec"`
    LogMaxIdleConns    int `json:"log_max_idle_conns"`     // 0 = 继承主库
    LogMaxOpenConns    int `json:"log_max_open_conns"`     // 0 = 继承主库
}
```

`LogMaxIdleConns` / `LogMaxOpenConns` 用 **0 表示继承主库**，与
[model/main.go:241-242](../../model/main.go#L241-L242) 现有的
`GetEnvOrDefault("LOG_SQL_MAX_IDLE_CONNS", GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))`
回退语义一致。注意这是**这两个字段独有的例外**：其余字段的 0 一律被钳制到下界 1。
getter 里把两种语义分开写，不要共用一个 `bounded`。

#### 5.6.1 apply 函数与错误边界

区分三类失败，**不要都塞给 `recover`**：

| 失败 | 属于 | 处理 |
|---|---|---|
| `DB` / `LOG_DB` 为 nil | 正常状态（启动早期、测试） | 静默跳过，返回 `nil` |
| `db.DB()` 返回 error | 普通错误 | 返回 error，由调用方汇报 |
| `SetMaxOpenConns` 等 setter panic | 不该发生 | `recover` 兜底，转成 error |

**setter 抽成可注入的接缝**。标准 `*sql.DB` 的三个 setter 在正常参数下不会 panic，
测试无法自然构造，`recover` 分支会永远没有覆盖。因此把它们抽成一个窄接口：

```go
type poolTuner interface {
    SetMaxIdleConns(int)
    SetMaxOpenConns(int)
    SetConnMaxLifetime(time.Duration)
}

var poolTunerOf = func(db *gorm.DB) (poolTuner, error) { return db.DB() }
```

测试替换 `poolTunerOf` 即可分别构造"返回 error"与"setter panic"两种情形。
生产路径零变化——`*sql.DB` 天然满足这个接口。

```go
// model/db_pool.go
// ApplyDBPoolSetting 把当前快照应用到已建立的连接池。
// 启动时与配置发布后各调一次。不重建 *sql.DB，因此不中断在途查询，
// 也不会像重启那样让连接池从零重建。
// 返回 error 而不是静默吞掉：调用方要据此告诉管理员"已保存但本节点未生效"。
func ApplyDBPoolSetting() error {
    snap := operation_setting.GetDBPoolSnapshot()
    var errs []error
    if err := applyPool(DB, snap.MaxIdle, snap.MaxOpen, snap.Lifetime); err != nil {
        errs = append(errs, fmt.Errorf("main db: %w", err))
    }
    if LOG_DB != nil && LOG_DB != DB {
        if err := applyPool(LOG_DB, snap.LogMaxIdle, snap.LogMaxOpen, snap.Lifetime); err != nil {
            errs = append(errs, fmt.Errorf("log db: %w", err))
        }
    }
    return errors.Join(errs...)
}

func applyPool(db *gorm.DB, idle, open int, lifetime time.Duration) (err error) {
    if db == nil {
        return nil       // 尚未初始化不算失败
    }
    // recover 只覆盖 setter 调用，不覆盖上面的 nil 判断与 db.DB() 的错误返回。
    defer func() {
        if recovered := recover(); recovered != nil {
            err = fmt.Errorf("panic while applying db pool setting: %v", recovered)
        }
    }()
    sqlDB, err := db.DB()
    if err != nil {
        return err       // 普通错误，不是 panic
    }
    sqlDB.SetMaxIdleConns(idle)
    sqlDB.SetMaxOpenConns(open)
    sqlDB.SetConnMaxLifetime(lifetime)
    return nil
}
```

`snap.LogMaxIdle` / `LogMaxOpen` 在**构造快照时**就已经把"0=继承主库"解析成了具体数值，
apply 里不再有继承分支——继承是配置语义，不该泄漏到执行层。

#### 5.6.2 钩子挂在哪

[model/option.go:731](../../model/option.go#L731) 的 `handleConfigUpdate` 已经有一个
按 `configName` 分发后处理的 switch（`performance_setting` → `UpdateAndSync()`、
`log_query_setting` → `resetLogStatCache()`），本次沿用同一位置，
但按 §5.3.2 拆成"更新草稿"与"发布+apply"两段：

```go
// publishConfig：整组保存结束时调一次；单项 PUT 在更新草稿后立刻调一次。
func publishConfig(configName string) error {
    switch configName {
    case "rate_limit_setting":
        operation_setting.PublishRateLimitSetting()
        return nil
    case "db_pool_setting":
        operation_setting.PublishDBPoolSetting()
        return ApplyDBPoolSetting()
    }
    return nil
}
```

放在 `model` 而不是 controller，是因为 `UpdateOption`、`UpdateOptionsBulk`
（[option.go:280](../../model/option.go#L280)）与 `LoadFromDB` 三条路径都归到这里。

#### 5.6.3 apply 失败必须让管理员看见

上一版写的是"apply 失败只记 `SysError`，接口照常返回成功"。这对连接池不成立：
**数据库里已经是新值、当前进程用的还是旧值**，管理员看到"保存成功"会以为生效了，
下一次重启才突然变化——这是最难排查的一类不一致。

改为：

- `publishConfig` 返回 error；
- 保存接口在 DB 已提交、apply 失败时**仍返回 `success: true`**（因为确实存了），
  但 `data` 里带 `applied: false` 与 `apply_error` 的 i18n key；
- 前端据此显示警告态，而不是普通成功 toast；
- 同时 `common.SysError` 记录原始错误。

不把它做成 `success: false`，是因为"保存失败"与"保存成功但未应用"是两种不同状态，
前者管理员应该重试保存，后者重试保存没有意义。

**文案必须说"部分"而不是"未"**：`ApplyDBPoolSetting` 先应用主库、再应用日志库
（§5.6.1），主库成功而日志库失败时，本节点已经处于**部分应用**状态。
所以 `applied: false` 的含义是"未能完整应用"，不是"完全没应用"：

> 配置已保存，但当前节点未能完整应用；部分连接池参数可能已经生效。

`ApplyDBPoolSetting` 返回的是 `errors.Join` 的聚合错误，日志里能看出是主库还是
日志库失败，但接口层不把这个细节回传给前端（Rule 9：不暴露内部错误）。

**并且失败会自愈**：§5.3.4 让后台同步在每个周期内重新走一遍
"读 DB → 灌草稿 → 发布 → apply"，所以一次瞬时 apply 失败最多持续一个同步周期。
前端提示因此不应该写"重启后生效"——那会误导管理员去做一次不必要的重启。

#### 5.6.4 下界钳制不是主链路保护

`MaxOpenConns` 被误设成 0 在 `database/sql` 里意味着**无限连接**而不是"禁止连接"；
`MaxIdleConns` 设成 0 会让每次查询都新建连接，在 relay 负载下是灾难。
所以除"0=继承"的两个日志库字段外，其余一律下界为 1。

**但下界 1 只挡住了"无限连接"这一种误设，挡不住"把 relay 压成串行"。**
`max_open_conns=1` 是完全合法的配置，也足以让 30k RPM 的网关瞬间瘫痪。
因此还需要：

- **写入校验**（§10）：`max_idle_conns ≤ max_open_conns`，否则拒绝；
- **前端高风险确认**（§13）：`max_open_conns` 被调到当前值的 1/2 以下时，
  弹二次确认并显示"当前活跃连接数"，让管理员看到自己正在往下压什么；
- 这两条都是**提示与拦截**，不是钳制。钳制永远只负责"不产生非法值"，
  不负责"不产生危险值"——危险值是管理员的决定，系统的责任是让他知道。

**启动路径同时简化**：[model/main.go:197-199](../../model/main.go#L197-L199) 与
[:241-242](../../model/main.go#L241-L242) 的六处 `GetEnvOrDefault` 全部删除，
改为在两个库都建好之后调一次 `ApplyDBPoolSetting()`。这样"启动"和"热更新"
走的是同一段代码，不会出现两条路径行为漂移。

### 5.7 慢查询阈值：查证后剔出第一批

初判它"可以搭车"，看代码后否掉了，理由记在这里防止下次又被提出来。

[gorm_logger.go:51](../../model/gorm_logger.go#L51) 把阈值交给
`gorm.io/gorm/logger.New(..., logger.Config{SlowThreshold: ...})`，
比较发生在 **GORM 自己的 `Trace` 实现**里，我们没有插手的余地。三条路都不通：

1. **包一层 `logger.Interface` 自己实现 `Trace`** —— 正是
   [gorm_logger.go:49-50](../../model/gorm_logger.go#L49-L50) 的注释明确否决过的做法：
   包装层会让 GORM 的 `FileWithLineNum` 把所有 SQL 日志的调用点归因到包装层自身，
   还要转发 `ParamsFilter` 的类型断言。为一个阈值付这个代价不划算。
2. **运行时给 `DB.Config.Logger` 赋新实例** —— GORM 每条语句都读这个字段且不加锁，
   并发赋值是接口值（两个字长）上的数据竞争。
3. `LogWriterMu` 只保护 **writer**（`dynamicGinWriter`），管不到 `logger.Config`。

因此 `SQL_SLOW_THRESHOLD_MS` **保持 env-only**，从 §8.2 的配置表里去掉。
若以后确有需求，正确做法是接受方案 1 的代价并连带处理 `FileWithLineNum`，
属于独立改动。

### 5.8 改动文件清单

| 文件 | 改动 |
|---|---|
| `setting/operation_setting/rate_limit_setting.go` | 新增：草稿结构体、快照类型 + `atomic.Pointer`、`init` 注册、`ApplyRateLimitEnvDefaults`、`PublishRateLimitSetting`、`GetRateLimitSnapshot`、写入校验 |
| `setting/operation_setting/db_pool_setting.go` | 新增：同上一套，外加"0=继承主库"的解析 |
| `middleware/rate-limit.go` | 两个 factory 换签名收 provider；10 个构造函数改从快照取桶；删 `defNext`；删对 `common.*RateLimit*` 的引用 |
| `common/constants.go` | 删除 `GlobalApiRateLimit*` 等 20 个全局变量（保留 `Upload/DownloadRateLimit*` 常量） |
| `common/init.go` | 删除 121-147 行的对应赋值 |
| `model/db_pool.go` | 新增 `ApplyDBPoolSetting`（返回 error）/ `applyPool` |
| `model/main.go` | 删除 6 处 `GetEnvOrDefault` 池参数，改调 `ApplyDBPoolSetting()` |
| `model/option.go` | `handleConfigUpdate` 拆成 `applyConfigDraft` + `publishConfig`；`loadOptionsFromDatabase` 的 DB 读移进 `WithConfigUpdate`；`UpdateOptionsBulk` 改为提交后统一发布一次 |
| `setting/config/config.go` | 新增 `configUpdateMutex` / `WithConfigUpdate`；`LoadFromDB` / `SaveToDB` / `ExportAllConfigs` / `UpdateConfigFromMap` 全部迁到该锁下；加载完成后回调一次发布（§5.2.4、§5.3.4） |
| `controller/option.go` | 新增 `UpdateOptionGroup`；两个新模块的单项 PUT 内部转调整组流程（§5.3.3）；新增 `configGroupRegistry` 白名单（§6.1.1） |
| `router/api-router.go` | `optionRoute` 增 `PUT /group` |
| `main.go` | `InitOptionMap()` 前增加两个 `ApplyEnvDefaults` + 各一次 `Publish` |
| `i18n/locales/*.yaml` | 校验失败与 apply 失败的消息 key |
| `.env.example` | 22 + 5 条注释补"仅作启动默认值"；补录 `LOG_EXPORT_RATE_LIMIT*`；删 `GEMINI_VISION_MAX_IMAGE_NUM` |
| 前端 | 见 §13 |
| 测试 | 见 §14 |

比上一版多出来的是 `controller/option.go`、`router/api-router.go`、
`setting/config/config.go` 与 i18n —— 全部来自"整组原子保存"这一项。
它不是可以省掉的装饰：没有它，§5.2 的快照只保证了"单次读自洽"，
保证不了"单次写自洽"。

---

## 6. API 合同

沿用现有的管理员配置接口，**新增一条整组保存路由**
（[router/api-router.go:315-319](../../router/api-router.go#L315-L319)，
挂在已有 `RootAuth()` 的 `optionRoute` 组下）：

```http
GET  /api/option/          Authorization: RootAuth   # 读取全部配置（不变）
PUT  /api/option/          Authorization: RootAuth   # 保存单项（不变）
PUT  /api/option/group     Authorization: RootAuth   # 新增：整组原子保存
```

新增这条的理由见 §5.3.1：单项 PUT 无法表达"一组字段要么全生效、要么全不生效"，
而连接池与限流恰恰是一组字段互相约束的配置。

### 6.1 `PUT /api/option/group`

```json
// 请求
{
  "module": "db_pool_setting",
  "values": {
    "max_open_conns": "200",
    "max_idle_conns": "20",
    "max_lifetime_sec": "3600"
  }
}
```

#### 6.1.1 白名单，不是"所有已注册模块"

上一版写的是"`module` 必须是已注册的配置模块名"，这会把
`/api/option/group` 变成一个**通用配置写入口**，从而绕过
`controller.UpdateOption`（[option.go:141](../../controller/option.go#L141)）里
逐条积累下来的业务不变量：

| 现有保护 | 位置 |
|---|---|
| `commission_tier_reset_setting.last_reset_at` 禁止手工修改 | [option.go:162](../../controller/option.go#L162) |
| 支付合规确认字段禁止走通用设置接口 | [option.go:171](../../controller/option.go#L171) |
| 启用 OAuth 前必须已填 Client Id（GitHub / Discord / OIDC / LinuxDO / 微信 / Telegram / Turnstile） | [option.go:177](../../controller/option.go#L177) 起 |
| `gemini.safety_settings`、`claude.default_max_tokens`、`GroupRatio`、工具价格等专用校验 | [option.go:259](../../controller/option.go#L259) 起 |

`RootAuth` 拦得住"谁能调"，拦不住"调了会绕过什么"。所以整组端点采用
**显式注册表**，模块与字段都是白名单：

```go
type configGroupSpec struct {
    Module   string
    Fields   map[string]fieldSpec              // 字段白名单 + 各自的类型与范围
    Validate func(merged map[string]string) error   // 跨字段校验
    Publish  func()                            // 构造快照 + atomic.Store
    Apply    func() error                      // 可选，仅需要回写外部对象的模块
}

// 第一批只有两个成员。新增模块必须显式登记 validator / publisher / apply，
// 不能因为"它已经注册进 GlobalConfig"就自动获得整组写入能力。
var configGroupRegistry = map[string]configGroupSpec{
    "rate_limit_setting": {...},
    "db_pool_setting":    {...},
}
```

拒绝条件（全部返回 `success:false`，一个字节都不写）：

| 条件 | 说明 |
|---|---|
| 模块不在注册表 | 包括所有已注册但未登记整组能力的模块 |
| 字段不在该模块白名单 | 含拼写错误、跨模块字段、带模块前缀的键 |
| 服务端维护字段 | 如 `last_reset_at` 这类由任务写入的字段，永不进白名单 |
| `values` 为空 | 空提交没有语义，不当作成功 |
| 键重复或格式异常 | JSON 层面重复键、空键、含 `.` 的键 |

上表里的既有保护**继续留在单项 `PUT /api/option/` 上**，不迁移、不复制到整组端点——
那些模块本来就不在整组白名单里，两条路径互不干扰。

`values` 的键不带模块前缀，服务端拼成 `{module}.{field}` 后落库。

```json
// 成功且已应用
{ "success": true, "data": { "applied": true } }

// 成功但本节点应用失败（§5.6.3）
{ "success": true, "data": { "applied": false, "apply_error": "config.apply_failed_restart_required" } }

// 校验失败：一个字节都没写
{ "success": false, "message": "最大空闲连接数不能大于最大连接数" }
```

`apply_error` 是 i18n key 而不是 Go 错误串（Rule 9）。

### 6.2 持久化与生效

`config.GlobalConfig` 的持久化是自动的：`LoadFromDB` / `SaveToDB` 按
`{module}.{field}` 读写 `options` 表，新增模块无需改控制器。生效则分两步：

1. **发布快照**（所有模块都要）：草稿 → 校验钳制 → `atomic.Store`，见 §5.2。
2. **执行 apply**（仅需要重建/回写外部对象的模块）：如 `ApplyDBPoolSetting`。

两步都在整组保存的最后一次性执行，不逐字段触发（§5.3.2）。

---

## 7. 数据模型变更

无新表、无新列、无迁移。配置写入既有的 `options` 表，键名形如：

```
rate_limit_setting.global_api_num
rate_limit_setting.global_api_duration
db_pool_setting.max_open_conns
```

`options` 表是小表（百级行），不涉及 Rule 8.3 的大表索引问题。

---

## 8. 新增配置参数

### 8.1 `rate_limit_setting`

| 字段 | 默认 | 钳制 | 对应 env（启动默认值） |
|---|---|---|---|
| `global_api_enabled` | true | — | `GLOBAL_API_RATE_LIMIT_ENABLE` |
| `global_api_num` | 2000 | 1 ~ 100_000 | `GLOBAL_API_RATE_LIMIT` |
| `global_api_duration_sec` | 180 | 1 ~ 1200 | `GLOBAL_API_RATE_LIMIT_DURATION` |
| `global_api_user_enabled` | true | — | `GLOBAL_API_USER_RATE_LIMIT_ENABLE` |
| `global_api_user_num` | 360 | 1 ~ 100_000 | `GLOBAL_API_USER_RATE_LIMIT` |
| `global_api_user_duration_sec` | 180 | 1 ~ 1200 | `GLOBAL_API_USER_RATE_LIMIT_DURATION` |
| `global_web_enabled` | true | — | `GLOBAL_WEB_RATE_LIMIT_ENABLE` |
| `global_web_num` | 2000 | 1 ~ 100_000 | `GLOBAL_WEB_RATE_LIMIT` |
| `global_web_duration_sec` | 180 | 1 ~ 1200 | `GLOBAL_WEB_RATE_LIMIT_DURATION` |
| `critical_enabled` | true | — | `CRITICAL_RATE_LIMIT_ENABLE` |
| `critical_num` | 60 | 1 ~ 100_000 | `CRITICAL_RATE_LIMIT` |
| `critical_duration_sec` | 1200 | 1 ~ 1200 | `CRITICAL_RATE_LIMIT_DURATION` |
| `auth_refresh_enabled` | true | — | `AUTH_REFRESH_RATE_LIMIT_ENABLE` |
| `auth_refresh_num` | 60 | 1 ~ 100_000 | `AUTH_REFRESH_RATE_LIMIT` |
| `auth_refresh_ip_num` | 600 | 1 ~ 100_000 | `AUTH_REFRESH_RATE_LIMIT_IP` |
| `auth_refresh_duration_sec` | 1200 | 1 ~ 1200 | `AUTH_REFRESH_RATE_LIMIT_DURATION` |
| `search_enabled` | true | — | `SEARCH_RATE_LIMIT_ENABLE` |
| `search_num` | 10 | 1 ~ 100_000 | `SEARCH_RATE_LIMIT` |
| `search_duration_sec` | 60 | 1 ~ 1200 | `SEARCH_RATE_LIMIT_DURATION` |
| `log_export_enabled` | true | — | `LOG_EXPORT_RATE_LIMIT_ENABLE` |
| `log_export_num` | 1 | 1 ~ 10_000 | `LOG_EXPORT_RATE_LIMIT` |
| `log_export_duration_sec` | 600 | 1 ~ 1200 | `LOG_EXPORT_RATE_LIMIT_DURATION` |
| `redis_timeout_ms` | 100 | 5 ~ 5000 | `RATE_LIMIT_REDIS_TIMEOUT_MS` |

#### 8.1.1 `redis_timeout_ms`：限流查询的超时预算

限流是一条**可降级的旁路**——Redis 不可用时降级到本节点内存计数即可，请求不该跟着
go-redis 的默认超时一起等。本项目创建客户端时只覆盖了 `PoolSize`，其余全用默认值
（`DialTimeout` 5s、`ReadTimeout` 3s、`MaxRetries` 3），而 `redisFixedWindowTake` 用的是
gin 的请求 context，**没有任何截止时间**。实测两种失败模式差了两个数量级：

| Redis 失败模式 | 加预算前单次限流调用 | 加预算后（预算 80ms） |
|---|---|---|
| 连接被拒绝（进程没了） | 72ms | 不变（本来就快） |
| **连得上但永不响应**（卡死 / 网络吞包） | **12.08s** | **85ms** |

一个已认证的 `/api` 请求含两次限流调用（`GlobalAPIRateLimit` + `authHelper` 里的
`applyAPIUserRateLimit`），所以最坏情况原本是 **24.2s**。

预算加在 `redisFixedWindowTake` **内部**而不是各调用点：这样两个生产调用方
（`takeRateLimit`、`redisEmailVerificationRateLimiter`）自动受保护，将来新增调用方
也不会漏掉。

取值依据：

- **默认 100ms**——健康时这条调用是亚毫秒级的，留约两个数量级余量，
  正常抖动不会误触发降级；同时把最坏情况压到 100ms 量级。
- **下界 5ms**——再小会让 Redis 稍有延迟就全量降级到内存计数，
  多实例下等于限流配额被放大 N 倍。
- **上界 5000ms**——对齐 go-redis 的 `DialTimeout`，再大没有意义。
- 钳制函数 `clampRedisTimeoutMs` 与 `clamp` 的区别在于**有下界**：
  配成 1ms 会被抬到 5ms，而不是回落到默认值 100ms——否则运维想调紧却得到更松的值。

回归用例：`TestRateLimitRedisTimeoutBoundsWorstCase`（黑洞 Redis 下必须远低于 12s）、
`TestRateLimitRedisTimeoutIsConfigurable`（调大预算耗时须跟着变长，证明配置真的生效）。

**两个上界都不是拍脑袋定的，实施时不要放宽：**

- **窗口上界**。Redis 侧的 TTL 由 Lua 的 `EXPIRE KEYS[1] ARGV[2]` 直接设成配置的窗口值，
  本身没有上限；真正的约束来自**无 Redis 时的内存兜底**：
  `InMemoryRateLimiter` 的 GC 以 `RateLimitKeyExpirationDuration`
  （[constants.go:257](../../common/constants.go#L257)，20 分钟）为周期，
  最后一次命中超过该时长的 key 会被整个删除。窗口大于 20 分钟时，
  一个"低频但持续超额"的客户端可能在窗口结束前就被清空计数而重新获得配额。
  这正是 [constants.go:205](../../common/constants.go#L205) 那句
  `Shouldn't larger then RateLimitKeyExpirationDuration` 的含义。
  注意 `critical` 与 `auth_refresh` 的默认窗口都是 1200 秒，**恰好卡在这条线上**。
  因此窗口上界取 `RateLimitKeyExpirationDuration`（1200 秒）；
  前端对超过该值的输入给出明确警告，而不是静默钳制。
- **配额上界**。内存兜底路径按 `make([]int64, 0, maxRequestNum)` 给**每个 key**
  预分配切片（[rate-limit.go:64](../../common/rate-limit.go#L64)）。
  配额填 1_000_000 意味着单个 IP 的计数器就要 8 MB，Redis 一挂就是 OOM。
  配额上界收到 **100_000**，并在前端注明"该值同时决定 Redis 故障降级时的内存占用"。

### 8.2 `db_pool_setting`

| 字段 | 默认 | 钳制 | 对应 env |
|---|---|---|---|
| `max_idle_conns` | 100 | 1 ~ 10_000 | `SQL_MAX_IDLE_CONNS` |
| `max_open_conns` | 1000 | 1 ~ 100_000 | `SQL_MAX_OPEN_CONNS` |
| `max_lifetime_sec` | 60 | 1 ~ 86400 | `SQL_MAX_LIFETIME` |
| `log_max_idle_conns` | 0（=继承主库） | 0 或 1 ~ 10_000 | `LOG_SQL_MAX_IDLE_CONNS` |
| `log_max_open_conns` | 0（=继承主库） | 0 或 1 ~ 100_000 | `LOG_SQL_MAX_OPEN_CONNS` |

连接寿命的上界与限流窗口无关，取 86400 秒；`log_*` 两项的 0 是"继承主库"而非"下界"，
见 §5.6。

> `.env.example` 里 `SQL_MAX_OPEN_CONNS=2000`、`SQL_MAX_LIFETIME=3600`，
> 与 [model/main.go](../../model/main.go) 的代码默认值（1000 / 60）不一致。
> 代码优先（Rule 7），本表按代码默认值填；实施时顺带修正 `.env.example` 的注释说明这是示例值。

---

## 9. 关键业务逻辑与边界情况

1. **限流值被调小时，已有计数器不重置**。Redis 固定窗口的计数 key 仍在，
   下一次取值就按新配额判定，因此**调小会立刻开始拒绝**已超额的客户端。
   这是期望行为（事故处置时就是要立刻收紧），但要在前端提示里写明。
2. **限流值被调大时立即放行**，无需等待窗口结束。
3. **`Enable` 从 false 改成 true**：改造后中间件始终注册，所以能立刻生效；
   改造前做不到，这正是必须先拆闭包的原因。
4. **窗口时长被改**：Redis 侧 Lua 只在 key 无 TTL 时设置过期，
   已存在的 key 沿用旧 TTL，最多一个旧窗口后收敛。可接受，不做特殊处理。
5. **连接池调小到低于当前在用连接数**：`database/sql` 不会杀死在途连接，
   只在归还时关闭多余的，不会中断在途查询。
6. **配置保存后 apply 失败或 panic**：配置已落库是既成事实，不回滚保存；
   但必须让管理员看见"已保存、未应用"，见 §5.6.3。`recover` 只包 setter 调用，
   `db.DB()` 的普通错误走 error 返回，两者不混（§5.6.1）。
7. **一次请求内不会看到新旧混合配置**：中间件在闭包开头 `Load` 一次快照，
   之后所有字段都读这一份。发布只换 `atomic.Pointer` 里的地址，
   已被读到的旧快照不会被就地改写，正在处理的请求继续用它跑完。
8. **一次保存内不会暴露中间组合**：整组保存先校验全部、再事务写库、
   再一次发布、再一次 apply（§5.3.2）。任一步失败都不会留下部分生效的状态。
9. **多节点部署**：`options` 表是共享的，但内存配置靠各节点自己 `LoadFromDB`。
   本次不改变既有的配置同步节奏（沿用 `SYNC_FREQUENCY` 的选项刷新），
   因此**限流参数在多节点上不是同一时刻生效**，最坏落后一个同步周期。
   同步节点走的是完整加载 + 一次发布（§5.3.4），所以是"晚一点整体切换"，
   而不是"逐字段抖动"。这一点必须在前端提示里写清楚，
   否则运维会在生效前误判"改了没用"而反复保存。

---

## 10. 校验、钳制与错误处理（Rule 9 / Rule 11）

上一版这一节自相矛盾：既说"校验失败返回错误"，又说"越界值静默钳制"，
却没说清什么时候走哪条。后果是数据库里可以存着 `max_open_conns = -100`、
`GET /api/option` 原样回显 `-100`、实际运行用的是钳制后的 1 ——
**展示值、存储值、生效值三者不一致**，管理员无从判断当前到底跑在什么配置上。

改为职责分明的三层，每层只做一件事：

### 10.1 第一层：写入校验（正门，严格拒绝）

所有经 `PUT /api/option/` 与 `PUT /api/option/group` 进来的值，
**越界即拒绝，不钳制、不写库**：

| 规则 | 示例 |
|---|---|
| 类型与范围 | `max_open_conns` 必须是 `[1, 100000]` 的整数；`-100` / `abc` / `0` 一律拒绝 |
| 窗口上界 | 限流窗口 > `RateLimitKeyExpirationDuration`（1200 秒）拒绝，并在错误信息里说明原因（§8.1） |
| **跨字段** | `max_idle_conns > max_open_conns` 拒绝 |
| **跨字段 + 继承** | 日志库显式值同样要满足 `idle ≤ open`；填 0（继承）时，用**主库本次提交后的最终值**参与校验，而不是主库的旧值 |

最后一条是整组保存必须先于校验合并的原因：管理员在一次提交里同时改主库和日志库时，
校验必须基于"这一组改完之后的完整配置"，不能逐字段各校验各的。

失败一律 `common.ApiErrorI18n(c, key)`，HTTP 200 + `success:false`，
消息说明**哪个字段、允许范围是什么**，不回显 Go 错误串。

### 10.2 第二层：加载钳制（兼容旧数据，必须留痕）

`LoadFromDB` / `ApplyEnvDefaults` 读到的值可能来自：升级前的旧版本、
手工改过的 `options` 表、写错的 `.env`。这一层**钳制而不是拒绝**——
拒绝会让进程起不来，是更坏的结果。但钳制必须留痕：

```go
if clamped != raw {
    common.SysError(fmt.Sprintf(
        "db_pool_setting.max_open_conns=%d out of range [1,%d], clamped to %d",
        raw, maxOpenConnsLimit, clamped))
}
```

**并且把钳制后的值写回草稿结构体**，让 `GET /api/option` 回显的就是生效值。
这样"展示 = 存储（内存）= 生效"，只有 `options` 表里可能残留旧的非法值，
下一次管理员保存时被覆盖。

### 10.3 第三层：快照构造兜底

`buildSnapshot` 里的 `bounded` 是最后一道防线，正常情况下永远不触发
（第一层已拒绝、第二层已钳制并回写）。保留它是为了让快照类型自带"字段一定合法"的不变式，
中间件因此不需要防御性判断。这一层**不记日志**——它若触发说明前两层漏了，
应该由测试而不是运行时日志来发现。

### 10.4 其余错误

- **apply 钩子失败**：见 §5.6.3，返回 `success: true` + `applied: false`，
  前端显示警告态，`SysError` 记录原始错误。不静默。
- **读取路径无错误分支**：`GetRateLimitSnapshot()` 只做 `atomic.Load` +
  nil 兜底，不可能失败，限流中间件不会因为配置模块引入新的失败模式。

---

## 11. 与既有子系统的交互

| 子系统 | 影响 |
|---|---|
| 限流中间件 | 主要代码改动，见 §5.5。分桶维度、mark 命名、Redis key 前缀**全部不变** |
| Redis | 不新增 key、不改 key 命名空间。仅改变从哪里取"配额数值" |
| `options` 表 | 新增两个模块的键，均为标量 |
| 配置同步 | 沿用既有 option 刷新机制；改为加载完整组后一次发布，见 §5.3.4 与 §9.9 |
| 日志导出 | `LOG_EXPORT_RATE_LIMIT` 纳入统一模块，行为不变 |
| relay 主链路 | 见 §12 |

---

## 12. Main Chain Impact（Rule 0）

**同步 / 异步**

- 第一批的两组改动**都不在 `/v1` relay 路径上**：
  - 限流中间件挂在 `/api` 与 web 静态路由，relay 用 `TokenAuth()`，
    不经过 `GlobalAPIRateLimit` / `CriticalRateLimit` 这一族。
  - `relay-router.go` 一行不改。
- 唯一与 relay 共享的是**数据库连接池**：`ApplyDBPoolSetting` 会调
  `sqlDB.SetMaxOpenConns` 等。这三个 setter 内部只取一次 `db.mu`，
  不会阻塞在途查询，也不关闭活跃连接。但**误设仍会伤到 relay**——
  把 `MaxOpenConns` 调到远低于 relay 并发量会让查询排队。
  这是配置本身的语义，靠 §8.2 的下界钳制 + 前端提示约束，不靠代码兜。
- A 类里确有若干变量在 relay 热路径上（`STREAMING_TIMEOUT`、`MAX_REQUEST_BODY_MB`、
  `FORCE_STREAM_OPTION`、`GET_MEDIA_TOKEN` 等），**但不在第一批范围内**。
  它们迁移时的开销是"包级变量读"变成"结构体指针解引用后读字段"，
  同量级、无锁、无分配，不构成主链路风险；届时在对应批次的文档里复述这一节。

**共享资源审计**

| 资源 | relay 是否也用 | 冲突与处置 |
|---|---|---|
| `options` 表 | 否 | relay 路径不读 `options`，只读内存中的配置副本 |
| 主库连接池 | **是** | 见上。只调参数不重建对象。下界钳制只防"误设成 0 = 无限连接"，防不住"压成串行"——后者靠写入校验 + 前端二次确认（§5.6.4） |
| 日志库连接池 | **是**（异步日志管道写入） | 同上。管道有自己的写超时与熔断（见 [relay-log-async-batch.md](relay-log-async-batch.md)），池被调小最多触发重试，不会反压 relay |
| Redis key `rateLimit:v2:*` | 否 | relay 用 `rateLimit:MRRL*` / `rateLimit:<userId>`，前缀不重叠 |
| `common.InMemoryRateLimiter` | **是**（无 Redis 时 `ModelRequestRateLimit` 也用它） | 已有共享结构，本次不改结构、不改 key 前缀 |
| goroutine / 内存 | 否 | 无新增 goroutine。新增常驻内存 = 两个配置快照（合计不足 1 KB），每次发布产生一份新的、旧的随最后一个读者结束被回收 |
| 配置草稿结构体 | 否 | 只被配置读写路径碰，请求路径一律读快照。**这条不变式必须由 `-race` 测试保证**（§14.4） |

**并发分析（100k RPM）**

- relay 每请求新增开销：**0**。第一批不触碰 relay 路径的任何中间件。
- `/api` 每请求新增开销：1 次 `atomic.Pointer.Load`（x86 上就是一条 `MOV`，
  无 `LOCK` 前缀、无内存屏障指令）+ 3 次字段读。无锁、无分配、无 CAS。
  比现在读裸全局变量多的只有一次指针解引用。
- **读写不互斥**：发布用 `Store` 换指针，读用 `Load` 取指针，
  两者不会互相阻塞；旧快照在最后一个持有它的请求结束后被 GC 回收。
  每次发布分配一个快照结构体（限流 7 个桶约 200 字节），
  发布频率是"每天个位数"，GC 压力可忽略。
- 配置保存路径：整组保存 1 次事务 + 1 次发布 + 1 次 apply，
  由 root 手工触发。多节点同步每周期最多 1 次发布 + 1 次 apply（§5.3.4）。
- **`go test -race` 是硬性验收条件**，见 §14.4。设计上读写已分离到
  `atomic.Pointer` 两侧，但草稿结构体仍被反射写——必须用竞态检测
  实测确认没有任何路径绕过快照直接热读草稿。

---

## 13. 前端（Rule 6）

复用系统设置的「系统调优」页，与
[relay-log-pipeline-section.tsx](../../web/src/features/system-settings/maintenance/relay-log-pipeline-section.tsx)
同构，在 [section-registry.tsx](../../web/src/features/system-settings/system-tuning/section-registry.tsx)
增两个 section：

| id | 标题 key | 内容 |
|---|---|---|
| `rate-limit` | `Rate Limiting` | 按桶分组的数值输入，每组一行「启用开关 + 配额 + 窗口」 |
| `db-pool` | `Database Connection Pool` | 主库 / 日志库两栏（空闲连接、最大连接、连接寿命） |

**两个 section 都用整组保存**（`PUT /api/option/group`，§6.1），
一个「保存」按钮提交本 section 的全部字段，不做逐字段即时保存。
这是 §5.3 原子性要求在前端的对应面：逐字段保存的 UI 会让用户在不知情的情况下
产生中间组合。

交互要求（Rule 6「从用户视角写」）：

- 每个桶下面用一句话说明**它按什么维度计**（IP / 用户 / 会话），
  因为"按 IP 计"决定了配额要按出口人数估算，而不是按单人。
  文案直接沿用 [dashboard-api-rate-limit-429.md](dashboard-api-rate-limit-429.md) §4.4 的表述。
- 保存成功的提示必须写明**多节点最坏落后一个同步周期**（§9.9），
  否则运维会以为没生效而反复保存。
- **`applied: false` 要有独立的警告态**，不能复用普通成功 toast：
  文案是"配置已保存，但当前节点未能应用，重启后生效"，不暴露 Go 错误串。
- **日志库两个字段不能用 `min=1`**。它们的 0 是"继承主库"而不是非法值，
  与主库字段的约束相反。UI 上做成一个「跟随主库」开关：
  开（默认）时输入框禁用并显示主库当前值作为占位；关时才允许输入，
  且此时最小值为 1。提交时开=0、关=输入值。
- **主库 `max_open_conns` 需要二次确认**：新值低于当前值的 1/2 时弹确认框，
  显示"当前活跃连接数"与"这会限制并发查询数，可能拖慢 AI 请求"。
  下界钳制只挡得住"无限连接"，挡不住"把 relay 压成串行"（§5.6.4）——
  这一层保护必须由 UI 承担。
- 限流窗口输入超过 1200 秒时给明确警告而非静默钳制，
  说明"超过该值后，Redis 不可用时的内存兜底计数会被提前清理"（§8.1）。
- 沿用既有 section 的按钮区结构，不新增标题与说明段落。

i18n：新增 key 直接写进 7 个 locale 文件（en / zh / zh-TW / fr / ru / ja / vi），
不跑 `bun run i18n:sync`（会顺带回填无关 key，污染 diff）。

---

## 14. 测试计划（Rule 15）

先写用例再写实现。

### 14.1 `middleware/rate_limit_test.go`（补充）

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestRateLimiterReadsConfigPerRequest` | 判定覆盖 | 中间件构造后修改配置，**第二个请求**按新配额判定。这是闭包捕获问题的直接回归测试 |
| `TestRateLimiterEnableToggleTakesEffectWithoutRestart` | 判定覆盖 | 关着构造 → 打开 → 请求被限流；反向同理 |
| `TestRateLimitConfigClampsOutOfRangeValues` | 边界值 | 0 / 负数 / 超上界分别收敛到边界，且不 panic |
| `TestDisabledRateLimiterStillPassesRequestsThrough` | 判定覆盖 | `Enabled=false` 时请求放行且**不创建任何 Redis 计数器**——`defNext` 删除后不能变成"计数但不拒绝" |
| `TestStaticBucketLimitersUnaffectedByConfig` | 等价类 | 改 `rate_limit_setting` 不影响 `Upload` / `Download`（它们走 `staticBucket`） |
| `TestEveryRateLimiterUsesItsOwnCounter` | 路径覆盖 | **已存在**，改造后必须继续通过（证明 mark 隔离没被 provider 重构破坏） |
| `TestSessionCriticalRateLimitNeverChargesValidSessionsToTheIPBucket` | 判定覆盖 | **已存在**，同上 |
| `TestCriticalBucketsDoNotShareAllowance` | 路径覆盖 | **已存在**。`Critical` 与 `PublicQuery` 共用配额值但不共用 mark，provider 重构后必须仍然成立 |

### 14.2 `setting/.../rate_limit_setting_test.go`、`db_pool_setting_test.go`

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestApplyEnvDefaultsThenDBWins` | 路径覆盖 | env 设值 → `ApplyEnvDefaults` → 模拟 `LoadFromDB` 覆盖 → 快照返回 DB 值。**锁定 DB > env > 默认的优先级** |
| `TestSnapshotClamps` | 边界值 + 等价类 | 每个字段的下界 / 上界 / 越界 / 零值在**发布时**收敛 |
| `TestLoadClampWritesBackToDraft` | 判定覆盖 | 草稿里存越界值 → 发布 → **草稿字段本身也被改成钳制后的值**，保证 `GET /api/option` 回显 = 生效值（§10.2） |
| `TestLoadClampLogsSysError` | 语句覆盖 | 钳制发生时打出"原值 → 钳制值"的 `SysError`，不静默篡改 |
| `TestSnapshotIsImmutableAfterPublish` | 路径覆盖 | 取到快照指针 → 再发布一次新配置 → **先前持有的指针内容不变**。这是"一次请求内配置自洽"的直接证明 |
| `TestPublishNeverReturnsNil` | 边界值 | 未发布过时 `GetSnapshot()` 返回内置默认而非 nil |

### 14.3 `model/main_test.go`（补充）

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestApplyDBPoolSettingUpdatesLivePool` | 语句覆盖 | 调用后 `sqlDB.Stats().MaxOpenConnections` 变为新值，且**改前建立的连接仍可用**（不重建池） |
| `TestApplyDBPoolSettingRejectsZero` | 边界值 | `max_open_conns` / `max_idle_conns` 配 0 时实际应用下界 1，不是 `database/sql` 的"无限" |
| `TestLogPoolZeroInheritsMainPool` | 等价类 | `log_max_*` 配 0 走继承主库分支；配正数走自身值。**这是唯一允许 0 的两个字段，必须与上一条区分开** |
| `TestUpdateOptionTriggersDBPoolApply` | 路径覆盖 | 经 `UpdateOption("db_pool_setting.max_open_conns", ...)` 保存后连接池实际生效，锁定 `publishConfig` 的分发接线 |

错误路径按 §5.6.1 的三类拆开，**不要混成一个 `RecoversFromPanic`**：

| 用例 | 构造方式 | 断言 |
|---|---|---|
| `TestApplyDBPoolSettingSkipsNilDB` | `DB = nil` | 返回 `nil`，不是 error、不 panic |
| `TestApplyDBPoolSettingReturnsTunerError` | 桩 `poolTunerOf` 返回 error | 返回该 error，**不经过 recover** |
| `TestApplyDBPoolSettingRecoversSetterPanic` | 桩 `poolTunerOf` 返回一个 setter 里 panic 的实现 | panic 被转成 error 返回，进程不崩 |
| `TestApplyDBPoolSettingReportsPartialApply` | 主库桩成功、日志库桩失败 | 返回聚合 error，且**主库确实已被应用**——对应 §5.6.3 的"部分应用"文案 |

### 14.4 并发与原子性（新增，`-race` 必跑）

这一组是本次设计的验收核心，缺一条都不能算实现完成。

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestRateLimitSnapshotRaceFree` | 竞态检测 | N 个 goroutine 持续 `GetRateLimitSnapshot()` + 读全部字段，同时另一组持续 `UpdateOption` / `PublishRateLimitSetting`，跑够时长。**必须在 `-race` 下零报告** |
| `TestDBPoolSnapshotRaceFree` | 竞态检测 | 同上，针对 `db_pool_setting` |
| `TestConcurrentSyncAndRequestRaceFree` | 竞态检测 | 模拟多节点同步：并发 `LoadFromDB` + 请求路径热读 |
| `TestGroupSaveIsAtomic` | 路径覆盖 | 整组保存过程中，另一 goroutine 持续读快照，**采样到的每一份都必须是合法组合**（`idle ≤ open`），不允许出现中间态 |
| `TestGroupSaveValidationFailureWritesNothing` | 错误路径 | 组内有一项非法 → 返回 `success:false`，且 `options` 表与内存快照**都没有任何变化** |
| `TestGroupSaveAppliesOnce` | 语句覆盖 | 三个字段一次提交，`ApplyDBPoolSetting` 只被调用 **1** 次（用计数桩），不是 3 次 |
| `TestLoadFromDBPublishesOnce` | 语句覆盖 | 同步加载 N 个字段后只发布一次、apply 一次（§5.3.4） |
| `TestConcurrentGroupSavesCannotProduceIllegalCombo` | 判定覆盖 | 并发提交 `open=200` 与 `idle=500`（各自相对旧基准合法），**必须有一笔被拒**，最终 DB 与快照都满足 `idle ≤ open`。§5.3.2 那个 A/B 场景的直接回归测试 |
| `TestStaleSyncCannotOverwriteNewerSave` | 时序 | 桩住 `AllOption()` 使同步在读完 DB 后暂停 → 整组保存提交并发布新值 → 恢复同步 → **最终快照仍是新值**。§5.3.4 的直接回归测试 |
| `TestDraftAccessAlwaysUnderConfigUpdateMutex` | 竞态检测 | 并发跑 `SaveToDB` / `ExportAllConfigs` / 整组保存 / 同步加载，`-race` 零报告。覆盖 §5.2.4 指出的**既有**竞争 |

`-race` 在本机不可用（无 CGO 编译器，见项目测试约定），
**这组用例必须在 CI 以 `CGO_ENABLED=1 go test -race` 跑**，
本机只能跑功能断言部分。这一点要写进 PR 说明，不能默认"本机过了就行"。

### 14.5 写入校验（§10.1）

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestRejectsOutOfRangeOnWrite` | 边界值 | `max_open_conns` 为 `0` / `-100` / 超上界 → 拒绝，不写库；合法边界值 `1` / 上界 → 接受 |
| `TestRejectsIdleGreaterThanOpen` | 跨字段 | 单项与整组两条路径都要拒绝 |
| `TestLogPoolInheritValidatedAgainstNewMainValue` | 跨字段 + 路径覆盖 | 一次提交同时改主库 `open` 与日志库 `idle`（继承模式），校验必须用**本次提交后**的主库值，不是旧值 |
| `TestRejectsWindowAboveKeyExpiration` | 边界值 | 限流窗口 > 1200 秒被拒绝，错误信息说明原因 |
| `TestSinglePutUsesFullCrossFieldValidation` | 判定覆盖 | 当前 `idle=100`，单项把 `open` 改成 50 **必须被拒**——证明单项 PUT 确实走了合并+完整校验，而不是只验自己（§5.3.3） |
| `TestPartialValuesMergeFromLatestDraft` | 等价类 | 只提交 `max_lifetime_sec`，其余字段取草稿最新值参与校验；**只有提交的字段产生 option 写入** |

### 14.6 整组端点的授权边界（§6.1.1）

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestGroupRejectsUnregisteredModule` | 等价类 | 传 `commission_tier_reset_setting` / `gemini` / `billing_setting` 等**已注册但未登记整组能力**的模块 → 拒绝。防止端点退化成通用写入口 |
| `TestGroupRejectsUnknownField` | 等价类 | 拼写错误、跨模块字段、带模块前缀的键、空键、含 `.` 的键 → 拒绝 |
| `TestGroupRejectsEmptyValues` | 边界值 | `values` 为空 → 拒绝，不当作成功 |
| `TestGroupCannotBypassUpdateOptionGuards` | 路径覆盖 | 逐条尝试用整组端点写 `last_reset_at`、支付合规字段、OAuth 开关 → 全部拒绝。**这是"新接口不得绕过既有业务不变量"的守门测试，新增整组模块时必须同步扩充** |

按 Rule 15.5 跑真实项目 DB；完成后按 Rule 15.8 跑 `go test ./...` 全量，
并在 CI 补一轮 `-race`。

---

## 15. 分批计划

| 批次 | 内容 | 依赖 |
|---|---|---|
| **第一批 · 前置** | `configUpdateMutex` + `WithConfigUpdate`，并把 `LoadFromDB` / `SaveToDB` / `ExportAllConfigs` / `UpdateConfigFromMap` / `loadOptionsFromDatabase` 迁到该锁下 | 无。**这一步独立可验证**：不改任何业务行为，只把既有的反射读写收进同一边界，跑通 `-race` 即可合入 |
| **第一批 · 主体** | 快照机制 + 整组保存端点（白名单）+ 限流组（含闭包拆解）+ 数据库连接池 | 依赖前置 |
| 第二批 | A 类的 relay 行为、请求体限制、会话策略、通知与任务 | **依赖第一批**：要复用快照助手与整组保存端点，不能并行 |
| 第三批 | 既有 setting 模块迁移到快照机制（§16.6 选 A 时） | 依赖第一批沉淀出的通用助手 |
| 第四批 | B 类的后台任务间隔；`DEBUG`（含回写 `kitutil.Debug`） | 无 |
| 第五批 | B 类的 relay HTTP 客户端（需先理顺 `RELAY_TIMEOUT` 语义） | 独立设计 |
| 不做 | `REDIS_POOL_SIZE`、订阅缓存容量、`SQL_SLOW_THRESHOLD_MS`、C 类全部 | — |

上一版把第二批标成"可与第一批并行"，现在改了：快照机制与整组保存是**基础设施**，
必须先落地一次并跑通 `-race`，后续批次才是"套用"。两批并行会导致
同一套机制被写两遍再合并。

第一批进一步拆成"前置 / 主体"两个 PR，是因为**前置那部分修的是既有缺陷**
（§5.2.4 的 `SaveToDB` 与 `handleConfigUpdate` 互不排斥），
与限流、连接池毫无关系。混在一个 PR 里，
审阅者要在几百行新功能里分辨哪几行是在修老问题——分开提交，
前置 PR 的验收标准就是一句话：**行为不变，`-race` 从有报告变成零报告**。

---

## 16. 待确认项

1. **第一批范围是否就是限流 + 连接池**，还是要把 A 类一次性做完。
   一次做完的好处是只改一遍 `.env.example` 与前端页面；坏处是 diff 很大，
   而限流那部分是唯一需要改逻辑（拆闭包）的，混在一起不好审。
2. **多节点生效延迟（§9.9）是否可接受**。如果要求"保存即全节点生效"，
   需要额外引入 Redis pub/sub 广播配置变更，属独立需求，本文档未涵盖。
3. **`monitor_setting` 的 env-覆盖-DB 反向优先级（§3.2）是否顺带纠正**。
   纠正会改变现有部署的行为：`.env` 里写了 `CHANNEL_TEST_FREQUENCY` 且后台也改过的
   实例，纠正后会开始以后台值为准。
4. **是否给配置变更加审计**（谁、什么时候、把哪个值从多少改成多少）。
   限流和连接池都是能一键打垮站点的参数，建议做，但属独立需求。
5. **是否接受"删除 `common` 里的限流全局变量"**（§5.0）。这会让任何未来
   引用这些变量的外部补丁编译失败——对本仓库是好事（强制走配置），
   但如果有依赖这些符号的私有分支，需要先知会。
6. **既有 setting 模块的同类竞态是否一并修**（§5.2）。
   `relay_log_setting`、`log_query_setting`、`ledger_pipeline_setting`、
   `performance_setting` 等**全部**是"热读裸结构体 + 反射原地写"的形态，
   与本次要修的是同一个问题，只是读取频率低所以没暴露。三个选项：
   - **A（推荐）**：第一批只做两个新模块，把快照机制沉淀成 `setting/config`
     里的通用助手，其余模块按批次迁移。风险可控，且不会一次改动十几个模块。
   - B：一次全改。彻底，但 diff 巨大、回归面广，不建议和限流改造混在一起。
   - C：不管。那么 `go test -race` 会在既有模块上持续报警，
     新加的竞态测试也会被既有问题淹没——**不可接受**。

   选 A 的话需要确认：允许仓库在一段时间内"新模块无竞态、老模块有已知竞态"，
   并在 §15 分批计划里给老模块排期。

   **补充**：§5.2.4 查出的 `SaveToDB`/`ExportAllConfigs`（反射读）与
   `handleConfigUpdate`（反射写）互不排斥，是**全模块共有**的既有竞争。
   `configUpdateMutex` 一旦引入就必须覆盖这三个入口，
   所以第一批实际上已经**顺带修掉了老模块的这一半问题**——
   剩下的只是各模块自己的"热读裸结构体"。这让选项 A 更划算。
7. **配置保存持锁跨 DB 事务是否接受**（§5.3.2 第二个要点）。
   替代方案是乐观并发：给配置加单调 revision，
   校验时记下基准 revision，发布时 CAS，失败则重读重试。
   它避免了持锁做 I/O，但要在 `options` 表加列、改写全部读写路径、
   处理重试次数上限，复杂度显著高于互斥锁。
   鉴于配置写入是 root 手工触发的低频路径、且这把锁不在任何请求路径上，
   **建议第一批用互斥锁**；如果将来出现"配置写入频率高到会互相阻塞"的场景，
   再迁移到 revision 方案。这条需要明确拍板，因为它是不可逆的架构选择。
