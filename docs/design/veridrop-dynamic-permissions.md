# 真伪检测动态权限控制设计

## 背景与问题

真伪检测虽然已有 `admin_menu.veridrop_detection:view`，但目前只控制菜单、页面入口和 Veridrop 路由组的第一层访问。各接口还分别叠加了与本功能无关的权限：

- 读取目标和结果依赖 `channel:read`；
- 发起检测依赖 `channel:operate`；
- 清理记录依赖 `channel:sensitive_write`；
- 读取配置走未带 scope 的 `/api/option/`，普通管理员无法通过动态权限访问；
- 保存配置走未带 scope 的 `/api/option/group`，普通管理员无法通过动态权限保存；
- 页面读取全量系统任务依赖 `system_settings.operations.logs:view`，且接口会暴露其他任务类型；
- 手动检测的模型发现复用 `/api/channel/fetch_models`，依赖渠道菜单和敏感写权限。

因此，单独授予真伪检测菜单权限并不能形成可用、可预测的授权闭环，撤销渠道或系统设置权限也会让页面部分失效。

## Goals and scope

### 目标

- 让真伪检测使用自身资源完成动态授权，不再借用渠道、日志维护或其他系统设置权限。
- 为 `admin_menu.veridrop_detection` 增加 `edit` 动作：
  - `view`：查看菜单、页面、检测目标、检测记录、详情、配置脱敏值及本功能任务状态；
  - `edit`：保存配置、发现手动检测模型、发起单渠道/批量/手动检测、清理检测记录。
- 后端作为最终安全边界；前端依据同一能力矩阵隐藏或禁用不可执行操作。
- Root 继续拥有全部权限；普通管理员的 `view` 基线保持不变，`edit` 默认不授予，由 Root 按账号动态分配。

### 不在范围内

- 不修改 Veridrop 上游协议、检测算法、评分规则或轮询策略。
- 不修改渠道本身的增删改、密钥查看权限。
- 不允许真伪检测权限读取完整渠道密钥或其他类型系统任务。
- 不进入 AI relay 主链，不根据检测结果自动修改渠道状态。

## 权限模型与兼容策略

资源保持 `admin_menu.veridrop_detection`，动作变为：

| 动作 | 能力 | 默认普通管理员 |
| --- | --- | --- |
| `view` | 页面、配置脱敏值、目标、记录、详情、Veridrop 任务 | 保持允许 |
| `edit` | 保存配置、模型发现、发起检测、清理记录 | 默认拒绝，Root 或显式授权账号允许 |

权限归一化遵循与系统设置、价格巡检相同的约束：`edit=true` 自动带来 `view=true`；`view=false` 强制 `edit=false`。前后端都把 `admin_menu.veridrop_detection` 纳入该归一化规则。

升级时不把现有 `channel:*`、`system_settings.operations.logs:*` 覆盖规则迁移为真伪检测规则，因为这些资源的业务语义不同，自动合并会扩大权限。`view` 继续使用现有基线和覆盖规则；新增 `edit` 使用安全默认值 `false`。Root 可在员工权限编辑器中显式授权。

## Data flow

### 页面读取

1. 登录态返回的 `admin_permissions` 包含真伪检测 `view/edit` 能力。
2. 路由守卫以 `view` 判断是否允许进入 `/channels/detection`。
3. 页面并行请求：
   - 带真伪检测 scope 的配置读取；
   - 检测结果列表；
   - 仅在批量页签需要时读取目标和 Veridrop 任务。
4. 后端逐个接口校验 `view`，仅返回当前功能所需数据。
5. 配置中的 `admin_api_key` 继续不返回明文。

### 配置保存

1. 只有具备 `edit` 的用户显示设置入口并可提交。
2. 前端向现有配置组接口提交真伪检测 scope、模块名和变更字段。
3. 中间件把该 scope 映射到 `admin_menu.veridrop_detection:edit`。
4. `settingsaccess` 白名单限制模块只能是 `veridrop_monitor_setting`，字段只能是真伪检测配置字段。
5. ConfigManager 校验、保存并发布不可变配置快照，响应沿用现有配置组响应。

### 检测与清理

1. 用户点击运行、手动检测或清理。
2. 后端先校验 `admin_menu.veridrop_detection:edit`。
3. 控制器校验请求后创建对应系统任务；手动检测继续按现有实现异步执行。
4. 后台任务调用 Veridrop 并写入 `channel_veridrop_detections`。
5. 页面通过仅返回三类 Veridrop 任务的接口轮询状态，不读取其他系统任务。

### 手动模型发现

1. 页面向真伪检测路由组提交协议、Base URL 和临时 API Key。
2. 后端校验 `edit`，构造仅用于本次请求的临时渠道对象并复用上游模型发现逻辑。
3. API Key 不落库、不写任务、不写审计字段、不记录到日志。
4. 返回去重后的模型 ID；失败时返回可操作的脱敏错误信息。

## API contracts

所有接口均先经过 `middleware.AdminAuth()`。Root 由 authz 的 superuser 规则自动放行。

### 现有 Veridrop 接口权限调整

| 方法与路径 | 权限 | 说明 |
| --- | --- | --- |
| `GET /api/channel/veridrop/targets` | `view` | 读取可检测目标 |
| `GET /api/channel/veridrop/results` | `view` | 查询检测记录和统计 |
| `GET /api/channel/veridrop/results/:id` | `view` | 查询单条检测详情 |
| `POST /api/channel/veridrop/detect` | `edit` | 发起单渠道检测 |
| `POST /api/channel/veridrop/detect_enabled` | `edit` | 发起全部启用渠道检测 |
| `POST /api/channel/veridrop/detect_batch` | `edit` | 发起指定渠道批量检测 |
| `POST /api/channel/veridrop/detect_manual` | `edit` | 发起手动检测 |
| `POST /api/channel/veridrop/results/cleanup` | `edit` | 创建记录清理任务 |

以上接口不再叠加 `channel:read/operate/sensitive_write`。

### 新增：手动检测模型发现

`POST /api/channel/veridrop/manual_models`

权限：`admin_menu.veridrop_detection:edit`

请求：

```json
{
  "protocol": "openai",
  "base_url": "https://api.example.com",
  "api_key": "temporary-key"
}
```

成功响应沿用管理 API 约定：

```json
{
  "success": true,
  "message": "",
  "data": ["model-a", "model-b"]
}
```

### 新增：真伪检测任务列表

`GET /api/channel/veridrop/tasks?limit=30`

权限：`admin_menu.veridrop_detection:view`

只返回以下类型，`limit` 边界为 1～100，默认 30：

- `veridrop_detection`
- `veridrop_detection_single`
- `veridrop_detection_cleanup`

响应继续使用现有 `SystemTaskResponse[]` 结构，避免前端维护第二套任务类型。

### 配置 scope

新增设置访问 scope 常量 `veridrop-detection`：

- `GET /api/option/?scope=veridrop-detection` 映射到真伪检测 `view`；
- `PUT /api/option/group` 请求体携带 `scope: "veridrop-detection"`，映射到真伪检测 `edit`；
- scope 仅允许读取 `veridrop_monitor_setting.*`，仅允许写入模块 `veridrop_monitor_setting` 的既有字段。

不带 scope 的 Root 兼容行为保持不变。

## Data model changes and index design

不新增业务表、字段或 Redis key。

`system_tasks` 是持续增长表。新增的类型过滤查询访问模式为：

```text
WHERE type IN (三种 Veridrop 类型) ORDER BY id DESC LIMIT ?
```

现有 `type` 单列索引不足以稳定覆盖类型过滤后的倒序限量读取，因此增加跨数据库兼容的复合索引 `(type, id)`。迁移使用 GORM，兼容 SQLite、MySQL 5.7.8+ 和 PostgreSQL 9.6+。查询仅选择构造 `SystemTaskResponse` 所需列，最大返回 100 条。

`channel_veridrop_detections` 沿用现有索引和有界查询，不做结构变更。

## Config parameters

不新增 Veridrop 配置参数。只为既有 `veridrop_monitor_setting` 增加受权限控制的访问 scope 和字段白名单。

## Frontend behavior

- `view=false`：菜单隐藏，路由守卫拒绝进入。
- `view=true, edit=false`：只显示“检测记录”页签及其筛选、刷新、下载、详情；不显示设置、批量检测、手动检测、清理和渠道行“真伪检测”操作。
- `edit=true`：显示全部现有操作。
- 权限判断直接从当前用户能力矩阵派生，不复制到本地 state，不用 effect 同步。
- React Query 继续负责请求去重；任务和目标只在相应页签且用户具备 `edit` 时启用，避免无权限请求和无关轮询。
- 沿用当前 Base UI/shadcn 组件、按钮变体、布局与 design token，不增加新的颜色或平行样式。
- 后端 403 仍需被正常处理，防止权限热更新后已打开页面继续操作。

## Key business logic and edge cases

- 后端权限是最终判断；隐藏按钮不替代鉴权。
- `edit` 必须蕴含 `view`，禁止出现能执行但不能进入页面的能力组合。
- 权限被动态撤销后，下一次 API 请求立即返回 403；进行中的后台任务不取消，避免留下半完成记录。
- `view` 用户能查看既有任务状态，但不能创建任务。
- 真伪检测任务接口绝不返回日志清理、渠道测试、模型更新等其他任务。
- 手动模型发现的临时密钥只存在于请求内存；任何错误日志均不得拼接请求体、密钥或 Authorization。
- 配置读接口继续过滤敏感键；空白 `admin_api_key` 保存语义保持“保留旧值”。
- `limit=0`、负数、超过 100、非数字分别覆盖边界校验，不能退化为无界查询。

## Error handling strategy

- 未认证由 `AdminAuth()` 返回 401。
- 权限不足由 `RequirePermission` 或 scope 中间件返回 403 和本地化的权限不足消息。
- 请求参数错误使用 `ApiErrorI18n(c, i18n.MsgInvalidParams)`。
- 数据库、配置保存或上游模型发现失败先用带请求上下文的 `logger.LogError` 记录脱敏原因，再向用户返回可重试、可操作的本地化消息；不返回原始 Go 错误或上游正文。
- 检测任务运行失败仍只更新任务和检测记录，不影响 relay 或其他任务。

## Interaction with existing subsystems

- **Authz/Casbin**：注册 Veridrop `edit` 动作，能力矩阵和员工权限编辑器自动读取目录。
- **配置系统**：复用 ConfigManager、`settingsaccess` 和通用 option API，不新增保存通道。
- **渠道**：检测任务仍按 ID 读取渠道运行所需信息，但授权不再借用渠道管理权限；不开放密钥读取 API。
- **系统任务**：创建逻辑保持不变；新增只读、按类型过滤的列表入口。
- **审计**：既有设置、检测和清理审计动作保留，临时 API Key 不进入审计字段。
- **前端授权**：菜单、路由、页签、按钮和渠道行操作统一使用 Veridrop `view/edit`。
- **i18n**：新增权限动作说明、错误或 UI 文案时同步七种前端语言；新增后端用户可见消息时同步后端中英文语言文件。

## Main Chain Impact

本修改不进入 AI relay 路由、中间件、上游转发或流式响应链路。relay 请求同步新增 DB、Redis、锁、连接池和 goroutine 成本均为 0。

真伪检测 API 的鉴权同步执行一次内存中的 Casbin 判断，与其他管理 API 一致。检测和清理继续由既有后台任务执行；不会在 relay goroutine 上调用 Veridrop。

## Shared Resource Audit

| 资源 | 本功能访问 | relay 主链是否访问 | 隔离结论 |
| --- | --- | --- | --- |
| `casbin_rule` / authz 内存快照 | 管理 API 权限判断、Root 保存权限时更新 | relay 认证不使用 Veridrop 资源 | 使用独立资源名，无热路径协调锁 |
| `options` 中 `veridrop_monitor_setting.*` | 管理配置读写 | 否 | 独立配置命名空间 |
| `channels` | 后台检测任务按 ID 读取渠道信息 | 是 | 不新增 relay 请求访问；后台行为既有且有界，不加协调锁 |
| `channel_veridrop_detections` | 后台写入、管理端查询和清理 | 否 | 独立表和既有索引 |
| `system_tasks` | 创建和查询 Veridrop 类型任务 | 否 | 新查询使用 `(type, id)`，最大 100 条 |
| DB 连接池 | 管理请求和后台任务使用现有池 | relay 同池 | 不新增常驻连接或并发 worker；查询有界，避免长时间占用 |
| Redis | 不访问 | relay 会访问其他命名空间 | 无冲突 |
| goroutine/worker | 复用既有系统任务 runner | relay 使用自身执行路径 | 不新增每个 relay 请求的 goroutine |

## Concurrency analysis

虽然功能不在 relay 主链，仍明确其成本：

- 每个 Veridrop 管理 API 请求：1 次内存 authz 判断；按接口产生 0～2 次有界 DB 查询。
- 配置读取：读取内存 OptionMap，不访问 Redis；配置保存沿用 ConfigManager 的既有持久化流程。
- 任务列表：1 次 `system_tasks` 索引查询，`LIMIT <= 100`。
- 检测和清理：继续受既有 active task 去重及任务 runner 并发边界控制，不创建无界 goroutine。
- 在 100k relay RPM 下，每个 relay 请求新增成本为 0；管理请求只与 relay 共享数据库连接池，复合索引和严格 limit 将单次占用保持有界。

## Test-first plan

实现前先补以下测试：

1. Authz 目录：Veridrop 同时注册 `view/edit`；普通管理员默认 `view=true, edit=false`；Root 两者为 true。
2. 权限归一化：`edit=true` 推导 `view=true`，`view=false` 强制 `edit=false`，其他资源不受影响。
3. 路由表：每个 Veridrop GET 使用 `view`，每个变更接口和模型发现使用 `edit`，不再出现 `ChannelRead/Operate/SensitiveWrite`。
4. 中间件：`veridrop-detection` scope 的 view/edit 分别映射到 Veridrop 权限；未知 scope 和越权字段拒绝。
5. 配置白名单：全部既有 Veridrop 字段允许，其他 option/module 以及混入非法字段拒绝。
6. 任务查询：只返回三种 Veridrop 类型，按 ID 倒序；测试 limit 为 `0/1/100/101` 和非数字。
7. 模型发现：缺字段、未知协议、上游成功、上游失败；使用 `httptest.NewServer`，绝不请求真实供应商。
8. API 鉴权集成：仅 view 用户可读不可写；edit 用户可读写；无 Veridrop 权限用户全部 403；Root 全部允许。
9. 前端验证：`bun run typecheck`、目标目录 lint/format、七语言 i18n 完整性和生产构建。
10. 后端验证：相关包测试后运行 `go test ./...`；数据库迁移在项目真实数据库环境验证，SQLite 仅作后备。

## Acceptance criteria

1. Root 可在员工权限编辑器中独立配置真伪检测“可见”和“允许编辑”。
2. 只授予 `view` 的管理员可稳定查看记录、详情和任务状态，页面不会请求无权接口。
3. 未授予 `edit` 时，设置、运行、手动检测、模型发现和清理在 UI 不可用，直接调用 API 也返回 403。
4. 授予 `edit` 后，不需要任何渠道权限或日志维护权限即可完整使用真伪检测工作台。
5. 真伪检测任务查询不泄露其他系统任务。
6. 手动模型发现不落库、不记录临时 API Key，错误响应不泄露内部错误或上游正文。
7. 所有测试和构建通过，AI relay 主链代码与行为不变。

## Implementation record

已于 2026-08-19 按本文完成：

- `admin_menu.veridrop_detection` 已注册 `view/edit` 两个动作；普通管理员继续默认拥有 `view`，`edit` 默认关闭，Root 自动拥有全部权限。
- 前后端权限归一化均保证 `edit => view`、`view=false => edit=false`。
- Veridrop 查询接口统一使用 `view`，配置保存、模型发现、检测和清理接口统一使用 `edit`，不再借用 `channel:*` 或日志维护权限。
- 配置请求已使用 `veridrop-detection` scope，并通过 `settingsaccess` 精确限制 `veridrop_monitor_setting` 模块及其既有字段。
- 新增 `/api/channel/veridrop/manual_models`，临时密钥只在请求内存中使用；错误返回脱敏，日志至少移除请求密钥。
- 新增 `/api/channel/veridrop/tasks`，只查询并返回三种 Veridrop 任务；`system_tasks` 增加 `(type, id)` 复合索引，查询使用显式列、类型过滤和最多 100 条限制。
- 前端直接从能力矩阵派生 `canEditVeridrop`：只读管理员仅看到检测记录；设置、批量、手动、清理、相关弹窗和渠道行检测操作只对 `edit` 用户显示。任务读取改为 Veridrop 专用接口，模型发现不再调用渠道管理接口。
- AI relay 主链未修改，每个 relay 请求的新增 DB、Redis、锁和 goroutine 成本均为 0。

验证结果：动态权限、路由、scope、任务过滤、模型发现和边界测试均已补充；`go test ./...`、前端 `bun run typecheck`、目标文件 oxlint、目标文件 oxfmt、`bun run build` 与 `git diff --check` 全部通过。
