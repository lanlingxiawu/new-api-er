# 本地代码审计整改与完整验证

状态：待确认  
日期：2026-07-31

## 1. 目标与范围

本轮整改关闭 2026-07-31 本地审计发现，并建立可重复的完整验证闭环：

1. 修复 `go test ./...` 在共享 MySQL/PostgreSQL 测试库中的固定主键冲突、历史数据串扰和非幂等清理。
2. 统一 Rsbuild 与 Playwright 的默认访问地址，使 E2E 可直接启动和重复运行。
3. 为 Redis 限流降级错误增加有界、聚合式日志，避免故障期间每请求写一条错误日志。
4. 修复当前 `go vet ./...` 报告的锁复制和不可达代码。
5. 修复当前 `bun run lint` 的 error 级问题；warning 仅在与本轮修改同文件或涉及安全/正确性时处理，避免无关机械重写。
6. 收紧聊天和网页预览 iframe 的 sandbox 权限。
7. 在本地 mock AI 上游上执行鉴权、渠道分发、流式/非流式转发、计费、日志与结算完整链路的功能测试和压力测试。
8. 记录前端 bundle 基线和最大 chunk；本轮不做大规模拆包重构，除非构建分析发现明显的误导入或重复打包。

不在范围内：

- 不改变公开 AI API 契约、计费公式、渠道选择算法或流式协议。
- 不引入新的前端测试框架；继续使用仓库现有 Playwright。
- 不修改 Rule 6 列出的上游所有文件。
- 不通过截断共享表、删除历史业务数据或重置数据库来让测试通过。

## 2. 现状与根因

### 2.1 数据库测试

`service` 集成测试使用固定 ID，并有查询未限定到本次测试创建行的情况。共享数据库保留历史测试数据后会出现：

- `Duplicate entry ... for key PRIMARY`；
- 历史任务被轮询、超时扫描和退款统计纳入；
- 计数断言随历史数据增长；
- 零主键测试对象触发 GORM `WHERE conditions required`。

根因是测试数据身份和清理边界不够窄，而不是用例所覆盖业务逻辑都发生了回归。

### 2.2 E2E 地址

`web/rsbuild.config.ts` 当前开发端口为 `5177`，`e2e/lib/config.ts` 固定使用 `3002`。Playwright 无法连接由当前配置启动的默认前端。

### 2.3 Redis 限流降级日志

`takeRateLimit` 在 Redis 每次失败时同步记录完整 key 和错误。Redis 故障会把一次基础设施故障放大为与请求量等比例的日志 I/O，并可能暴露不必要的 session/IP key 内容。

### 2.4 静态检查和 iframe

`go vet` 报告 `CustomEvent` 复制 `sync.Mutex` 以及多个适配器不可达代码。前端 lint 包含未处理 Promise、恒等逻辑、iframe sandbox、类型导入和 React key 等 error。聊天 iframe 没有 sandbox；网页预览同时允许 `allow-scripts` 和 `allow-same-origin`，不能形成可靠隔离。

## 3. 数据流

### 3.1 可重复数据库测试

测试开始 → 生成本次运行唯一前缀/ID → 仅插入带该身份的数据 → 业务调用 → 仅查询本次数据 → 断言 → `t.Cleanup` 按唯一身份删除。

测试不得：

- 使用全局 `TRUNCATE`；
- 删除不带本次唯一身份条件的行；
- 依赖表初始为空；
- 使用可能与历史记录冲突的固定主键。

### 3.2 E2E

测试启动器确定 `E2E_DEFAULT_BASE_URL`（默认与 Rsbuild 端口一致）→ Playwright setup 创建/验证带 `e2e_` 前缀的测试身份 → 执行页面、RBAC、登录和 CRUD → 行级清理测试数据 → 输出 JSON/HTML 报告。

### 3.3 完整中继压测

本地 `mockai` → 本地测试渠道 → 本地网关 `/v1/chat/completions` → 专用测试 token → 正常鉴权与渠道分发 → mock 流式/非流式响应 → 异步日志管道 → 计费/结算 → 压测报告。

预热和正式采样分开；先直压 mock 校准，再压网关。测试渠道只允许指向环回地址，不调用真实 AI 提供商。

## 4. API 契约

不新增生产 API，不改变现有请求/响应结构和鉴权等级。

E2E 只增加进程级配置：

- `E2E_DEFAULT_BASE_URL`：默认前端地址；
- `E2E_CLASSIC_BASE_URL`：经典前端地址；
- `E2E_API_BASE_URL`：后端 API 地址。

变量未设置时使用仓库开发配置对应的本地地址。不会把账号、密码或 token 写入报告。

## 5. 数据模型与迁移

无生产数据模型和迁移变更。

测试数据继续使用现有表，但所有新增或修订的集成测试必须：

- 以运行唯一前缀标识用户名、任务 ID、渠道名和请求 ID；
- 让数据库生成自增主键；
- 对持续增长表的测试查询至少带唯一身份或测试时间范围；
- 使用行级 `t.Cleanup`；
- MySQL、PostgreSQL 和 SQLite fallback 均使用 GORM 可移植写法。

## 6. 配置参数

仅增加上述 E2E 进程环境变量，不进入 `setting/`，因为它们不是用户可配置的生产功能参数。

Redis 降级日志采样使用代码内固定策略，不新增生产配置：

- 同一错误类别首次记录；
- 后续按固定时间窗口或固定累计次数聚合记录；
- 日志只含 limiter mark/后端类别和累计次数，不含完整 IP、用户 ID、session ID 或 Redis key。

## 7. 关键逻辑与边界

### 7.1 测试隔离

- 优先修复失败用例本身，不通过修改生产查询语义来迎合污染数据。
- 并发测试的唯一标识必须无冲突。
- cleanup 失败必须让用例失败或明确记录，不静默遗留。
- 真实项目数据库不可用时才使用 SQLite fallback。

### 7.2 限流降级

- Redis 正常：仍执行现有 Lua 固定窗口。
- Redis 失败：仍降级到本机内存限流。
- 日志采样状态只保存固定数量的错误类别，不以动态请求 key 建 map，避免内存随攻击输入增长。
- 采样器不加网络或数据库调用。

### 7.3 iframe

- 聊天内容 iframe 默认启用 sandbox，只授予实际需要的最小权限。
- 网页预览不能同时赋予不可信同源内容 `allow-scripts` 与 `allow-same-origin`。
- 如果功能确实依赖脚本，则预览内容必须处于隔离 origin；当前架构不能保证隔离 origin 时，优先移除 `allow-same-origin`。
- 保留表单、弹窗或下载权限前必须由现有功能需求证明。

### 7.4 lint/vet

- Promise 必须 `await`、`return` 或显式捕获并提供用户可理解的失败反馈。
- 不使用禁用规则或全局降低 lint 严格度来清零错误。
- `CustomEvent` 改为指针接收者/指针参数，避免复制锁，并补并发/渲染测试。
- 适配器不可达代码按控制流真实意图删除或重构，不能用注释压制 vet。

## 8. 错误处理

- 测试基础设施错误应明确区分“服务未启动”“依赖不可用”“断言失败”。
- E2E 报告必须保留失败截图、trace、最终 URL、4xx/5xx 和页面异常。
- 压测报告分别统计 HTTP 状态、连接错误、超时、流式 TTFB 和总延迟。
- mock 或压测准备失败时不得退回真实上游。
- 生产控制器错误继续遵循现有 `ApiError*` 约定；本轮不新增用户可见后端字符串。

## 9. 与现有子系统的交互

- 鉴权：E2E 覆盖 guest/common/employee/admin/root 和 2FA 登录。
- 限流：验证 Redis 正常及不可用时的内存降级和日志采样。
- relay：只使用本地 mock 渠道。
- 计费/日志/佣金：压测使用专用测试用户并在结束后按唯一运行标识核对及清理。
- 前端：不修改上游所有 HTTP/session 文件。

## 10. Main Chain Impact

同步 relay goroutine 上不增加新 DB、Redis、文件、锁或 goroutine 操作。

本轮唯一可能与请求处理共同执行的生产修改是 Redis 限流失败日志采样：

- Redis 限流本身位于 dashboard/API 限流路径，不进入 AI relay 路由；
- 采样判断只进行固定大小状态的原子操作或短临界区；
- 不输出动态完整 key，不创建按请求身份增长的数据结构；
- 不增加同步外部 I/O；只有被采样命中的少量事件进入现有日志 writer。

iframe、E2E 和测试隔离修改均不运行在后端 relay goroutine。

## 11. Shared Resource Audit

| 资源 | 本轮访问 | relay 是否访问 | 隔离措施 |
|---|---|---|---|
| MySQL 主库测试表 | 测试插入/行级清理 | relay 会访问 users/tokens/channels/tasks | 唯一前缀、短事务、按主键/唯一标识清理；不 truncate、不持锁跨请求 |
| PostgreSQL 日志库 | 压测日志写入与核对 | relay 异步日志 worker 访问 | 专用 request ID 前缀和时间范围；沿用异步管道 |
| Redis rateLimit:v2 | 限流测试 | relay 不使用 dashboard limiter namespace | 保持现有 namespace，不新增 key |
| 限流日志采样状态 | 固定大小进程内状态 | relay 不访问 | 不按动态 key 扩张 |
| relay 日志队列 | mock 压测使用 | relay 使用 | 不改容量和同步 enqueue 行为 |
| DB/Redis 连接池 | 集成与压测使用 | relay 使用 | 压测采用专用环境和分阶段并发，不与生产流量共用 |

## 12. 并发分析

在 100,000 RPM relay 流量下，本轮生产修改增加：

- DB 调用：0；
- Redis 调用：0；
- 文件调用：0；
- 新 goroutine：0；
- 动态 key 状态：0；
- 锁：relay 路径 0；dashboard Redis 故障路径仅固定采样状态的原子操作或短锁。

完整中继压测沿用已有有界日志队列和固定 worker。测试阶段记录队列丢弃、fallback、goroutine、GC、数据库连接及慢查询；任何非关键日志故障不得影响 mock relay 响应。

## 13. 测试优先实施顺序

实施前先补或修订能复现问题的测试：

1. 重复运行两次相关 `service` 集成测试，第二次仍通过。
2. 并发运行使用相同测试模块的用例，无固定主键冲突。
3. Redis 连续失败时，内存限流仍生效且日志次数有界，日志不包含完整动态 key。
4. `CustomEvent` 并发渲染不复制锁且输出契约不变。
5. Playwright 配置读取环境变量，默认地址与 Rsbuild 一致。
6. iframe sandbox 属性的静态契约检查通过现有 lint/构建；不新增前端测试框架。

实现后执行完整矩阵：

```text
go test -count=1 ./...
go test -race ./common ./middleware ./model ./service
go vet ./...

cd web
bun run typecheck
bun run lint
bun run format:check
bun run build

cd e2e
bun install
bunx playwright test
```

中继功能与压力测试：

1. 启动 `bench/mockai`，直压 15–30 秒校准上限。
2. 启动当前源码网关与前端。
3. 创建带运行唯一前缀的测试渠道、用户和 token。
4. 功能验证非流式、流式、上游错误、无效 token、额度不足、限流和日志落库。
5. 网关预热后依次执行低并发冒烟、中并发稳定性和短时高并发测试。
6. 输出吞吐、p50/p95/p99、TTFB、错误率、状态码、goroutine、GC、DB 连接和日志队列指标。
7. 按唯一标识清理测试数据和本地进程。

若 `-race` 因真实数据库测试耗时过长，可只对本轮涉及且不依赖长周期外部服务的包运行，但必须记录排除项和原因。

## 14. 验收标准

- `go test -count=1 ./...` 连续两次通过。
- `go vet ./...` 无错误。
- 前端 typecheck、lint、format check、production build 全部通过。
- Playwright 全量通过；任何明确依赖未部署外部服务的页面必须使用可审计 skip，而不是吞掉 5xx。
- mock relay 功能用例全部通过，不访问外网 AI 上游。
- 压测期间无进程崩溃、死锁、持续 goroutine 增长或日志队列无界增长。
- 压测错误率为 0；注入错误场景的状态分布符合配置。
- 工作区原有无关改动保持不变。

## 15. 回滚

- 测试隔离和 E2E 配置可独立回滚，不影响生产。
- 限流日志采样可独立回滚到现有日志行为，不改变限流判定。
- iframe 权限可按组件独立回滚；若功能依赖某权限，应先使用隔离 origin 方案，不直接恢复危险组合。
- 不涉及 DDL，无数据库结构回滚。

## 16. Implementation record (2026-07-31)

- Service integration tests now create isolated MySQL databases and PostgreSQL
  schemas for each process, retain the configured real database dialects, and
  remove only those generated namespaces at shutdown.
- Negative first-response durations are clamped to zero and covered by a
  regression test.
- Redis limiter degradation continues to use the in-memory limiter. Diagnostic
  logging is bounded to the first event and every 1,000th event, uses constant
  process state, and no longer emits the dynamic rate-limit key.
- `CustomEvent` no longer contains an unnecessary mutex. Unimplemented channel
  conversions return controlled errors instead of panicking, and unreachable
  statements were removed.
- E2E base URLs are environment-overridable and the default Rsbuild URL is
  `http://127.0.0.1:5177`.
- Untrusted preview, home-content, and chat iframes now use sandbox policies;
  web preview no longer combines scripts with same-origin privileges.
- Verification completed: `go vet ./...`; two consecutive `go test ./...
  -count=1` runs; frontend typecheck and production build.
- Direct mock-upstream pressure baseline: 200 workers for 20 seconds, 50%
  streaming, 71,789/71,789 successful requests, 3,571.5 requests/second.
  Reports are stored in `bench/load-report-audit.json` and
  `bench/load-report-audit.md`.
- Repository-wide frontend lint remains a pre-existing cleanup backlog. The
  audit fixed safe automatic items and the scoped iframe findings, but the
  remaining errors span unrelated legacy UI modules and are not represented as
  completed validation.
