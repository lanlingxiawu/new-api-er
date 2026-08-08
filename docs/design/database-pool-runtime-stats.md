# 数据库连接池运行状态展示

日期：2026-08-02

状态：已实现

实现日期：2026-08-02

实际实现文件：

- `model/db_pool.go`、`model/db_pool_test.go`
- `controller/db_pool_status.go`、`controller/db_pool_status_test.go`
- `router/api-router.go`
- `web/src/features/system-settings/api.ts`
- `web/src/features/system-settings/types.ts`
- `web/src/features/system-settings/system-tuning/hot-config-sections.tsx`
- `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json`

实现与本文设计一致：页面首次进入读取一次并支持手动刷新，不自动轮询；保存数据库连接池配置成功后会使运行状态查询失效并重新读取。后端错误复用现有 `MsgRetryLater` 国际化提示，没有增加新的后端文案 key。

## 1. 目标与范围

在管理后台“系统设置 → 系统调优 → 数据库连接池”现有页面中展示当前节点的数据库连接池运行状态，让 Root 管理员在调整连接池参数时能同时看到实际占用、空闲连接和等待情况。

本功能只读取 Go `database/sql` 连接池在当前应用节点内的统计值，不查询 MySQL、PostgreSQL 或 SQLite 服务端的全局会话，不展示其他应用实例的连接，也不保存历史数据。

展示范围：

- 主数据库连接池。
- 独立日志数据库连接池；仅在 `LOG_DB` 与主库使用不同 `*gorm.DB` 时单独展示。
- 主库与日志库共用连接池时明确标注“日志数据库复用主数据库连接池”，避免重复展示同一组数据。
- 页面首次进入时加载一次，并提供手动刷新。第一版不自动轮询，避免后台页面长时间打开后产生无必要请求。

不在本次范围内：

- 数据库服务器全局连接数、连接来源、活动 SQL、慢查询和锁等待明细。
- 多节点聚合、历史曲线、告警、指标持久化或 Prometheus 接入。
- 修改现有连接池配置保存和热生效逻辑。

## 2. 现有实现与允许使用的 API

- `model.DB` 和 `model.LOG_DB` 分别保存主库与日志库的 GORM 连接；未配置 `LOG_SQL_DSN` 时，`InitLogDB()` 会令 `LOG_DB = DB`（`model/main.go`）。
- `gorm.DB.DB()` 返回底层 `*sql.DB`；现有 `model.applyPool()` 已使用该方法获取连接池并调用运行时 setter（`model/db_pool.go`）。
- 标准库 `sql.DB.Stats()` 返回即时 `sql.DBStats` 快照，读取不执行 SQL、不向连接池借连接，也不创建数据库连接。允许读取：`MaxOpenConnections`、`OpenConnections`、`InUse`、`Idle`、`WaitCount`、`WaitDuration`、`MaxIdleClosed`、`MaxIdleTimeClosed`、`MaxLifetimeClosed`。
- 现有 Root 设置接口位于 `/api/option` 路由组，并统一使用 `middleware.RootAuth()`（`router/api-router.go`）。
- 前端数据库连接池区域由 `DBPoolHotConfigSection` 渲染，复用 `SettingsSection`、Tailwind/Base UI 样式、TanStack Query 与现有系统设置 API 模块（`web/src/features/system-settings/system-tuning/hot-config-sections.tsx`、`web/src/features/system-settings/api.ts`）。

禁止为本功能使用数据库方言专属状态 SQL（如 MySQL `SHOW STATUS` 或 PostgreSQL `pg_stat_activity`），也不允许修改上游所有的 `web/src/lib/api.ts`、`http-client.ts` 等文件。

## 3. 数据流

1. Root 管理员进入“数据库连接池”区域，前端发起一次运行状态请求。
2. 请求经过现有 `/api/option` 路由组的 `RootAuth()`。
3. Controller 调用 model 层的连接池状态读取函数。
4. model 对主库调用 `DB.DB()`，再调用 `Stats()`；若日志库独立，对 `LOG_DB` 执行同样读取。
5. 若 `LOG_DB == DB`，响应标记日志库复用主库，不生成第二份重复统计。
6. Controller 返回统一成功响应；前端在现有数据库连接池配置表单上方展示状态卡片。
7. 用户点击“刷新”时重新获取快照。数据仅在内存中传递，不写数据库、Redis 或文件。

## 4. API 契约

### 4.1 获取当前节点连接池状态

- 方法：`GET /api/option/db-pool/stats`
- 权限：Root，仅放入已经应用 `middleware.RootAuth()` 的 `/api/option` 路由组。
- 请求体：无。
- 成功 HTTP 状态：200。

成功响应示例：

```json
{
  "success": true,
  "message": "",
  "data": {
    "sampled_at": 1785686400000,
    "main": {
      "max_open_connections": 1000,
      "open_connections": 24,
      "in_use": 7,
      "idle": 17,
      "wait_count": 16,
      "wait_duration_ms": 238,
      "max_idle_closed": 4,
      "max_idle_time_closed": 0,
      "max_lifetime_closed": 31
    },
    "log": null,
    "log_reuses_main": true
  }
}
```

字段语义：

| 字段 | 含义 |
|---|---|
| `sampled_at` | 当前节点生成快照的 Unix 毫秒时间 |
| `max_open_connections` | 当前连接池最大打开连接数 |
| `open_connections` | 当前已打开连接总数，包含使用中和空闲连接 |
| `in_use` | 当前正在使用的连接数 |
| `idle` | 当前空闲连接数 |
| `wait_count` | 自进程启动以来，等待连接的累计次数 |
| `wait_duration_ms` | 自进程启动以来，等待连接的累计时长（毫秒） |
| `max_idle_closed` | 因超过最大空闲连接数而关闭的累计连接数 |
| `max_idle_time_closed` | 因超过最大空闲时间而关闭的累计连接数 |
| `max_lifetime_closed` | 因超过最大生命周期而关闭的累计连接数 |
| `log_reuses_main` | 日志库是否复用主库连接池 |

`wait_*` 和 `*_closed` 均为进程生命周期累计值，页面必须明确标注，不能将其解释为当前值或最近时间窗口数据。

### 4.2 错误响应

- 未认证和权限不足继续由现有中间件处理。
- `gorm.DB.DB()` 失败时，底层错误使用 `logger.LogError(c, ...)` 记录；客户端通过 `common.ApiErrorI18n` 收到可操作的通用提示，不返回原始 Go 错误。
- 主库状态读取失败：整个请求失败，不展示可能误导的残缺快照。
- 独立日志库状态读取失败：整个请求失败，保证同一响应中的统计来自一个完整采样动作。

## 5. 数据模型、迁移与配置

本功能不新增或修改数据库表、字段、索引、Redis key、环境变量或 `setting/` 配置项，也不产生迁移。

统计数据来自当前进程内的 `database/sql`，进程重启后累计值归零。多实例部署时每个节点返回本节点数据；第一版不宣称其为集群总量。

## 6. 后端设计

在 `model/db_pool.go` 增加稳定的领域类型与读取函数，例如：

- `DBPoolStats`：把 `sql.DBStats` 转换为 API 需要的 JSON 字段，并将 `time.Duration` 显式转换为毫秒。
- `DBPoolRuntimeStats`：包含 `Main`、可空的 `Log` 与 `LogReusesMain`。
- `GetDBPoolRuntimeStats()`：获取底层池并形成快照。

读取逻辑直接复用 `gorm.DB.DB()` 与 `sql.DB.Stats()`，不执行探活 SQL。主库与日志库的两次 `Stats()` 是相邻的即时快照，不承诺严格原子性；`sampled_at` 表示响应采样时刻。

在 `controller/option.go` 增加只读 Controller，并在 `router/api-router.go` 的现有 Root `/api/option` 路由组注册。响应 DTO 使用明确的 JSON tag；不直接暴露标准库结构，以固定前后端契约并避免 Duration 纳秒值泄漏到 API。

## 7. 前端交互设计

在现有 `DBPoolHotConfigSection` 内展示“运行状态”，位置位于连接池配置输入项上方，不新增独立导航页。

每个实际连接池展示：

- 当前使用：`in_use / max_open_connections`，同时展示占用百分比。
- 已打开、空闲连接。
- 累计等待次数、累计等待时长。
- 三类累计关闭连接数。
- 本次采样时间。

交互状态：

- 首次加载：状态区域显示与现有页面一致的加载占位，不阻塞配置表单使用。
- 成功：展示快照及“刷新”按钮。
- 刷新中：保留上一份数据，禁用刷新按钮并显示正在刷新，避免卡片闪空。
- 失败：在状态区域展示用户可理解的提示和“重试”按钮；配置表单仍可编辑和保存。
- 共用连接池：只展示主库卡片，并显示日志数据库复用主数据库连接池。
- 独立日志库：主库、日志库分别展示，窄屏纵向排列，宽屏使用现有响应式 grid。

颜色仅使用现有 Base UI token 与 Tailwind 语义类。占用率只用于辅助展示，不以颜色单独传达状态。第一版不设置阈值或“健康/异常”结论，因为连接池瞬时占用高并不必然代表故障。

前端 API 与响应类型放在 `web/src/features/system-settings/api.ts` 和 `types.ts`；不能编辑上游所有的 `web/src/lib/api.ts`。所有新增可见文案直接补齐 `en`、`zh`、`zh-TW`、`fr`、`ru`、`ja`、`vi` 七个 locale 文件，不运行会回填无关键的全量同步命令。

## 8. 关键边界条件

- `MaxOpenConnections == 0`：标准库语义是无限制；占用百分比显示为“无限制/不适用”，不得除以零。
- `OpenConnections`、`InUse` 或 `Idle` 在渲染前后发生变化：正常现象，界面明确这是采样快照。
- 计数器很大：后端使用 `int64`，前端按 number 展示；第一版不做差值速率计算。
- 独立日志库尚未初始化或任一 GORM 连接为空：返回受控错误，不 panic。
- 保存连接池配置成功后：使连接池状态查询失效并重新获取，使 `max_open_connections` 尽快反映热更新后的值；状态刷新失败不改变保存成功结果。
- 多节点部署：页面仅显示处理该 HTTP 请求的节点数据。若负载均衡没有会话粘性，多次刷新可能命中不同节点；页面文案明确“当前响应节点”，本期不引入节点标识或集群聚合。

## 9. 错误处理策略

- model 层返回带上下文的错误，Controller 记录底层错误并向用户返回国际化通用错误。
- 前端网络错误不会清空配置表单或已有快照，并提供重试入口。
- 状态接口不得因统计读取失败影响现有设置读取、保存或连接池热应用接口。
- 不记录 DSN、数据库账号、地址或任何凭据。

## 10. 与现有子系统的交互

- 设置系统：只在连接池配置区域组合展示；不改变 `db_pool_setting` 的读取、校验、保存与 apply 流程。
- 数据库：读取进程内统计，不执行 SQL、不占用数据库连接。
- Relay：共享被观察的主库/日志库连接池对象，但本功能不修改池状态，也不进入 relay 请求路径。
- Billing、缓存、Redis、日志异步管道：无新增调用或状态修改。

## 11. Main Chain Impact

本功能不在 AI relay goroutine、relay middleware 或 provider adapter 中执行任何代码。只有 Root 管理员打开或手动刷新系统调优页面时，管理 API goroutine 同步调用 `gorm.DB.DB()` 和 `sql.DB.Stats()`；两者均为本地内存/连接池元数据读取，不发起数据库查询，不等待获取数据库连接。

对 100k RPM relay 流量而言，每个 relay 请求新增 DB 调用 0 次、Redis 调用 0 次、锁 0 次、goroutine 0 个。管理端单次刷新只读取一次主池统计，独立日志库存在时再读取一次日志池统计。

## 12. Shared Resource Audit

| 共享资源 | Relay 是否使用 | 本功能访问方式 | 隔离与影响 |
|---|---|---|---|
| 主库 `*sql.DB` 连接池 | 是 | 只调用 `Stats()` 读取元数据 | 不借连接、不执行 SQL、不修改池配置 |
| 日志库 `*sql.DB` 连接池 | 异步日志管道使用 | 独立池存在时只调用 `Stats()` | 不借连接、不执行 SQL；共用时不重复读取展示 |
| 数据库表/行 | 否 | 不访问 | 无锁竞争、无索引需求 |
| Redis key/连接池 | Relay 使用 Redis | 不访问 | 无 key 冲突、无连接消耗 |
| 内存缓存/map | 多处使用 | 不新增 | 无 GC 或同步压力 |
| goroutine/worker pool | Relay 使用 | 不新增 | 无抢占或池容量影响 |

## 13. 测试设计（先于实现）

后端测试先写在相邻测试文件中，使用 `testify`：

1. 主库和日志库共用：只返回主库统计，`log == nil`，`log_reuses_main == true`。
2. 主库和日志库独立：返回两组各自的 `MaxOpenConnections` 等统计，`log_reuses_main == false`。
3. 最大连接数为 0：原样返回 0，后端不把它解释为错误。
4. 底层 DB 获取失败或 nil：返回错误且不 panic；通过可注入的小接口或测试接缝覆盖错误分支，不 mock GORM 查询。
5. Duration 转换：等待时长按毫秒返回，覆盖 0 和非整毫秒边界并明确采用截断语义。
6. Controller 成功响应：使用 `httptest` 与 `gin.CreateTestContext` 验证 JSON shape。
7. Controller 失败响应：验证不暴露底层错误，并符合项目错误响应约定。
8. 路由权限：确认端点位于 RootAuth 路由组，不创建公共或普通管理员入口。

前端当前无测试框架，不新增测试依赖；使用 `bun run typecheck` 和 `bun run lint` 验证类型及静态规则，并人工验证加载、成功、失败、共用池、独立池、刷新和移动端布局。

## 14. 实施阶段与验证

### 阶段一：后端测试与读取模型

- 先写上述 model 测试，再实现 `sql.DBStats` 到稳定 DTO 的转换与主/日志池判定。
- 参考 `model/db_pool.go` 获取底层 `*sql.DB` 的现有方式，不复制或重建连接池。
- 验证：`go test ./model/...`。
- 防误用：不得执行 `Ping`、`SHOW STATUS`、`pg_stat_activity` 或任何 SQL；不得用 `encoding/json` 直接编解码。

### 阶段二：Root API

- 先写 Controller 成功/错误用例，再增加 Controller 和 `/api/option/db-pool/stats` 路由。
- 参考 `controller/option.go` 的只读状态响应模式与 `/api/option` Root 路由组。
- 验证：相关 Controller/Router 测试和 `go test ./...`。
- 防误用：不得返回原始错误、DSN 或数据库地址；不得把端点放到公开、UserAuth 或 AdminAuth 路由组。

### 阶段三：连接池页面展示

- 在 feature-local `api.ts`、`types.ts` 增加契约；在 `DBPoolHotConfigSection` 内组合运行状态，不新建页面。
- 复用同模块 `SettingsSection`、按钮区、TanStack Query、loading/error 模式和现有响应式 grid。
- 保存成功后刷新设置与运行状态 query；状态失败不得阻塞保存。
- 补齐七种前端语言。
- 验证：`bun run typecheck`、`bun run lint`、`bun run build`，并人工完成全部交互状态检查。
- 防误用：不得编辑上游所有的 API plumbing 文件，不得硬编码颜色，不得启动自动秒级轮询。

### 阶段四：全量验证与文档回写

- 运行 `go test ./...` 以及前端三项检查。
- 检查接口权限、错误脱敏、七种 locale、UTF-8 无 BOM。
- 在 SQLite、MySQL、PostgreSQL 环境确认功能仅依赖标准 `database/sql` 统计且行为一致。
- 实现完成后更新本文状态、实际文件和与设计的差异。

## 15. 验收标准

- Root 管理员能在现有数据库连接池页面看到当前响应节点的主库运行状态。
- 独立日志库显示第二组数据；共用连接池时只展示一组并清楚说明复用关系。
- 可手动刷新，刷新或加载失败不影响配置表单。
- 保存连接池参数后状态重新获取。
- 接口不执行 SQL、不占用连接、不保存统计、不泄露数据库凭据。
- 不改变 relay 主链行为；后端和前端规定的验证命令全部通过。
