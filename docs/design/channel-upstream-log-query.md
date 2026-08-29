# 渠道上游日志查询

## 1. 背景

当前消费日志已经保存：

- 本实例请求 ID：`logs.request_id`
- 上游实例返回的请求 ID：`logs.upstream_request_id`
- 最终使用渠道：`logs.channel_id`
- 多令牌渠道实际使用的令牌序号：`logs.other.admin_info.multi_key_index`

New API 渠道同时已经配置上游 Base URL 和 API 令牌。管理员排查请求时仍需手工复制上游请求 ID、登录上游站点、切换日志页并重新筛选，链路长且容易选错渠道或令牌。

本功能把这些已有信息连接起来：仅在管理员“使用日志”页面中，根据当前日志或当前筛选条件查询指定渠道对应的上游日志。渠道管理页面不增加任何入口或操作。

## 2. 目标与范围

### 2.1 目标

1. 仅在管理员使用日志详情中，对存在 `upstream_request_id` 的 New API 渠道提供“一键查询上游日志”。普通用户的自助日志详情不展示入口，也不请求或保存任何上游数据。
2. 将“上游日志”作为管理员使用日志页头范围 Tab 的第三个真实页面，与“全部 / 仅自己”并列；不再使用按钮打开侧抽屉。
3. Base URL、渠道令牌、多令牌序号由系统自动带入，令牌永不返回浏览器。
4. 查询过程保持上下文清晰：用户始终能看到当前渠道、令牌序号、查询范围和上游是否支持精确过滤。
5. 查询失败时给出可执行的下一步，不展示原始网络错误、内部地址或令牌。
6. 上游收费、成本、倍率、额度等运营信息严格限定管理员可见，普通用户无论通过 UI 还是直接调用接口都不能获取。
7. `upstream_request_id` 由前端从当前已加载的日志记录直接提交；后端不接收本地日志 ID，也不为解析请求 ID再次查询本地 `logs` 表。

### 2.2 V1 范围

- 仅支持 `ChannelTypeNewAPI`（61）。该类型具有稳定的 New API 日志接口契约。
- 支持单令牌渠道。
- 支持多令牌渠道，但一次只查询一个明确的令牌序号。
- 支持两种查询：
  - 精确查询：按 `request_id` 查询一条或少量匹配日志。
  - 筛选查询：发送与现有管理员日志查询一致的分页、时间、类型、用户、令牌、模型、分组、渠道及请求 ID 条件。
- 使用日志页头入口与日志详情快捷入口进入同一个上游日志独立页面，共用日志管理接口。
- 上游 New API 本实例新增兼容当前日志筛选模型的 `/api/log/token/query`；既有 `/api/log/token` 保持不变，供旧版上游降级。

### 2.3 不在 V1 范围

- OpenAI、Anthropic、Gemini 等官方平台各自不同的审计/用量 API。
- 任意自定义日志 URL、请求头模板或响应映射。
- 跨多个渠道或多个令牌的全局聚合搜索。
- 定时同步、缓存或持久化上游日志。
- 修改本地 relay 请求、响应、计费或日志写入逻辑。
- 从浏览器直连上游。
- 向普通用户开放上游日志、上游收费或渠道成本信息。
- 在渠道列表、渠道卡片、渠道编辑抽屉或渠道行操作菜单中增加上游日志入口。

## 3. 核心交互（V2，替代侧抽屉）

### 3.1 顶部范围 Tab 与路由

管理员在通用日志页头看到一个统一的 `TabsList`：`全部 / 仅自己 / 上游日志`。`上游日志`是实际路由 `/usage-logs/upstream`，不是模拟 Tab 的按钮。普通用户和员工不显示该 Tab；即使手工输入路由，也在前端回退到 `/usage-logs/common`，管理 API 仍由 `AdminAuth()` 做最终鉴权。

- 从“全部”进入：保留当前已经应用的渠道、时间、类型、用户名、令牌、模型、分组和请求 ID 查询参数。
- 从“上游日志”切回“全部 / 仅自己”：回到通用日志页并保留可复用筛选。
- 任务日志和导出中心不显示该范围 Tab，避免混合不同信息架构。

### 3.2 上游日志独立页面

页面复用项目现有 `SectionPageLayout`、`DataTablePage`、`LogsFilterToolbar`、`ComboboxInput`、`CompactDateTimeRangePicker`、`StatusBadge`、`Skeleton`、`Empty` 与响应式移动卡片，不再挂载 `Sheet`。

1. **筛选工具栏**：第一行依次是目标 New API 渠道、令牌序号（仅多令牌渠道渲染）、本站请求 ID、上游请求 ID；“更多筛选”承载时间范围、类型、用户名、令牌名称、模型、分组。令牌序号是多令牌渠道的必填项，因此固定在第一行，不能藏进折叠区，否则用户会对着看不见的字段收到“请选择令牌序号”的报错。
2. **渠道搜索**：输入关键字后调用 `/api/log/upstream/channels?keyword=`，服务端最多返回 50 个匹配项；详情入口提供的当前渠道作为已选项保留，不依赖它恰好位于最近 50 条。
3. **结果摘要**：工具栏下显示查询范围徽章、总数、耗时和目标渠道。旧版上游降级时用项目 `Alert` 明确提示“仅近期结果”，不把它混进普通空状态。
4. **桌面结果**：使用项目数据表，列为时间、类型、模型、请求 ID、令牌、输入/输出 token、额度、耗时和操作；服务端分页，页码或页大小变化会重新查询。
5. **移动结果**：使用与现有使用日志一致的卡片结构，保留时间、模型、请求 ID、token、额度、耗时和详情入口。
6. **详情展示**：行操作打开居中的项目 `Dialog`，按现有日志详情的 `DetailSection / DetailRow` 视觉层级展示标准化字段；不显示未知原始 JSON，不使用侧栏内联展开。

查询不会在每次输入时自动发送。只有点击“查询”或在请求 ID 输入框按 Enter 才提交；提交时页码归 1。切换渠道会清空令牌序号和旧结果，多令牌渠道必须明确选择序号。新查询通过 `AbortController` 取消前一个请求。

**三种查询模式与互斥表现。** 后端在本站请求 ID 有值时会完全接管渠道、令牌序号和上游请求 ID，其余筛选条件一律不下发。为了不让用户填完才发现被忽略，界面在本站请求 ID 非空时把渠道、上游请求 ID 与全部高级筛选置为 `disabled`，渠道占位符改为“由本站请求 ID 自动确定”，并在工具栏摘要区显示当前生效模式：

| 输入 | 生效方式 | 界面表现 |
|---|---|---|
| 本站请求 ID 有值 | 按本站日志反查渠道 / 令牌序号 / 上游请求 ID | 其余字段禁用并给出说明 |
| 仅上游请求 ID 有值 | 在所选渠道内精确查询 | 正常可编辑 |
| 两者皆空 | 按渠道 + 高级筛选浏览 | 正常可编辑 |

**金额与耗时格式。** 额度列走 `formatLogQuota`、耗时列走 `formatUseTime`，与通用日志列保持一致，不显示原始整数配额或裸秒数。

**降级模式不显示分页。** `recent_fallback` 只能拿到上游最近一页日志，页码对它无意义，因此隐藏分页器，并在 `Alert` 中列出该模式无法应用的筛选条件（用户名、分组——它们不在上游返回体里）。

### 3.3 使用日志详情：一键精确查询

现有日志详情中的“查询上游”按钮保持紧凑样式，**且只在该日志记录了 `upstream_request_id` 时渲染**。没有上游请求 ID 的日志（非 New API 渠道、或上游未回传该头）跳过去必然报错，不提供入口比提供一个必失败的入口更好。

点击后保留当前详情弹窗，并将弹窗扩展为左右对比布局：左侧继续显示本站日志，右侧根据本站 `request_id` 请求上游日志并显示同类型的标准化详情。不跳转 Tab，避免用户丢失当前日志的阅读位置。窄屏时改为上下堆叠，保证字段可读性。

- 进入页面后自动执行一次按本站请求 ID 的反查。
- 同时预填渠道与上游请求 ID：万一本站日志没记录令牌序号导致反查失败，用户清掉本站请求 ID 就能立刻手动查，无需回到原日志重新抄写。
- 浏览器后退返回原日志列表及原筛选；不在全局 store 或 LocalStorage 保存结果。

普通用户路径不加载上游页面组件、不预取渠道选项，也不会产生管理员 React Query cache。

## 4. 数据流

### 4.1 从使用日志精确查询

```text
本地日志详情
  -> 前端从已加载的日志对象读取 channel_id / upstream_request_id / multi_key_index
  -> POST /api/log/upstream/query
  -> AdminAuth（管理员使用日志路由）
  -> 从 channels 表读取该渠道的 type/base_url/key/channel_info
  -> 校验 New API 类型并选择明确的令牌
  -> 独立的上游日志 HTTP Client
  -> GET {upstream}/api/log/token/query?request_id=...
       Authorization: Bearer <selected channel key>
  -> 上游 TokenAuthReadOnly
  -> 上游 logs 表按 request_id + token_id 精确读取
  -> 标准化/裁剪响应
  -> 导航到上游日志独立页面并自动查询
  -> 数据表展示结果，居中详情弹窗展示标准化字段
```

### 4.2 从使用日志页头按筛选条件查询

```text
管理员使用日志页头 -> 上游日志 Tab -> /usage-logs/upstream
  -> 使用当前筛选的本地 channel_id，或在独立页面选择一个 New API 渠道
  -> 单令牌自动查询；多令牌等待选择序号
  -> 前端发送当前管理员日志筛选条件与分页参数
  -> POST /api/log/upstream/query
  -> GET {upstream}/api/log/token/query?<same log filters>
  -> 返回该令牌范围内符合条件的上游日志
  -> 标准化列表展示
```

所有网络访问都由后端完成。前端只提交目标渠道 ID、筛选条件、分页参数和可选令牌序号，不接触 Base URL 对应的真实令牌。

这里的上游请求 ID 完全信任为“查询条件输入”并在后端做格式校验，不通过本地日志 ID 反查、补全或验证。走该分支时本实例后端不会访问本地 `logs` 表；是否存在匹配记录由上游日志接口回答。

**多令牌序号的兜底。** 反查模式下令牌序号优先取本站日志 `other.admin_info.multi_key_index`。历史日志可能没有记录该字段；此时如果渠道是多令牌，允许使用客户端提交的 `key_index` 兜底，而不是让整条链路走不下去——同一管理员本来就能通过“渠道 + 上游请求 ID”分支查询任意渠道任意序号，拒绝并不带来额外安全性，只制造死路。响应中 `source.key_index_from_log` 标明序号来源，为 `false` 时前端显示琥珀色徽章提示结果不一定精确对应该次请求。日志记录了序号时，客户端提交的值永远被忽略。

## 5. API 契约

### 5.1 本实例管理接口

`POST /api/log/upstream/query`

鉴权（管理员专属）：

- `middleware.AdminAuth()`
- 注册在现有 `/api/log` 管理员路由组，与 `GetAllLogs` 保持相同管理员边界
- **注册顺序要求**：必须与 `logRoute.GET("", AdminAuth(), GetAllLogs)` 等管理员路由并列，注册在 `logRoute.Use(CORS(), PublicQueryRateLimit())`（`router/api-router.go:471`）**之前**。Gin 的 `Use` 只对其后注册的 handler 生效；若排在其后，会误挂公开查询限流并脱离管理员频控语义。
- 复用管理 API 的请求频控，并在服务内部增加独立并发上限

不得为普通用户增加 `/self`、token-auth 或 public 版本。即使普通用户知道渠道 ID、上游请求 ID 或前端接口地址，也会在读取渠道、发起上游请求之前被 `AdminAuth` 拒绝。该能力不依赖渠道页面权限，也不会出现在渠道页面。

请求：

```json
{
  "channel_id": 12,
  "key_index": 2,
  "page": 1,
  "page_size": 50,
  "filters": {
    "type": 2,
    "username": "alice",
    "token_name": "production",
    "model_name": "gpt-5",
    "start_timestamp": 1787500800,
    "end_timestamp": 1787587199,
    "channel": 7,
    "log_id": 0,
    "group": "default",
    "request_id": "req_abc123",
    "upstream_request_id": "req_next_hop"
  }
}
```

接口不接受“本地日志 ID”作为查询上下文，也不会用任何字段回查本实例 `logs` 表。`filters.log_id` 如果非零，表示要发送给上游的日志 ID 筛选值；它与本地日志对象没有关联。

从本地日志详情点击“一键查询”时，前端进行明确映射：

- 本地 `log.upstream_request_id` -> 上游查询的 `filters.request_id`
- 本地 `log.channel_id` -> 请求体 `channel_id`
- 本地 `log.other.admin_info.multi_key_index` -> `key_index`

其他入口按字段原样发送。`filters.upstream_request_id` 表示查询“上游实例记录的下一级上游请求 ID”，不能与上述一键映射混淆。

字段规则：

| 字段 | 必填 | 规则 |
|---|---:|---|
| `channel_id` | 是 | 本实例目标渠道 ID；只用于后端读取 Base URL 和令牌，不作为上游筛选条件 |
| `key_index` | 多令牌必填 | 从 0 开始，必须存在；单令牌渠道必须省略或为 `0` |
| `page` | 否 | >= 1，默认 1 |
| `page_size` | 否 | 1–100，默认 50 |
| `filters.type` | 否 | 与现有日志类型枚举一致；0/缺省表示全部 |
| `filters.username` | 否 | trim 后最多 64 字符 |
| `filters.token_name` | 否 | trim 后最多 64 字符 |
| `filters.model_name` | 否 | trim 后最多 128 字符，沿用现有显式文本筛选语义 |
| `filters.start_timestamp` | 否 | Unix 秒；与结束时间同时提供时必须小于等于结束时间 |
| `filters.end_timestamp` | 否 | Unix 秒；列表查询默认沿用当前日志页时间范围 |
| `filters.channel` | 否 | 上游实例内部的渠道 ID，不是请求体中的本地 `channel_id` |
| `filters.log_id` | 否 | 上游实例日志 ID，>= 0 |
| `filters.group` | 否 | trim 后最多 64 字符 |
| `filters.request_id` | 否 | 上游实例本级请求 ID，trim 后 1–128 字符，拒绝控制字符 |
| `filters.upstream_request_id` | 否 | 上游实例记录的下一级请求 ID，trim 后 1–128 字符 |

后端只复制 allowlist 中的字段，空字符串、0 值的可选筛选不发送；未知字段由绑定校验拒绝。前端当前日志页的 `channel` 筛选用于填写请求体顶层 `channel_id`，不能自动复制成 `filters.channel`，因为两个实例的渠道 ID 空间不同。

成功响应继续使用项目标准 `ApiSuccess` 包装：

```json
{
  "success": true,
  "message": "",
  "data": {
    "channel": {
      "id": 12,
      "name": "Upstream A",
      "type": 61,
      "key_index": 2,
      "is_multi_key": true
    },
    "query": {
      "filters": {
        "type": 2,
        "request_id": "req_abc123"
      },
      "scope": "exact",
      "upstream_supports_exact": true,
      "page": 1,
      "page_size": 50
    },
    "total": 1,
    "items": [
      {
        "id": 987,
        "created_at": 1787558400,
        "type": 2,
        "request_id": "req_abc123",
        "model_name": "gpt-5",
        "upstream_model_name": "gpt-5-2026-08-01",
        "prompt_tokens": 120,
        "completion_tokens": 45,
        "quota": 1650,
        "use_time": 2,
        "content": "",
        "other": {
          "frt": 0.42,
          "is_stream": true
        }
      }
    ],
    "elapsed_ms": 86
  }
}
```

`other` 只保留显式允许的诊断字段；未知字段、`admin_info`、`audit_info` 和任何疑似凭证字段均丢弃。`id` 是上游日志 ID，只用于本次展示，不作为本地资源 ID。`quota`、上游实际费用、成本倍率、缓存/推理附加费用等收费字段属于管理员运营数据，只能存在于该管理员接口的响应和管理员独立页面状态中。

### 5.2 上游令牌日志筛选接口

新增接口，既有接口不改：

`GET /api/log/token/query`

鉴权使用 `middleware.TokenAuthReadOnly()`，并强制增加当前 token 的 `token_id` 条件。支持与当前通用日志查询一致的 query 参数：

`p`、`page_size`、`type`、`username`、`token_name`、`model_name`、`start_timestamp`、`end_timestamp`、`channel`、`log_id`、`group`、`request_id`、`upstream_request_id`。

- 所有筛选条件与 `token_id = 当前认证 token` 组合，不允许扩大到其他 token 的日志。
- Body 使用与现有日志列表一致的 PageInfo：`items`、`total`、`page`、`page_size`。
- 响应增加 Header：`X-NewAPI-Log-Query: filters-v1`，表示上游原生执行了完整筛选。
- `request_id` 或 `log_id` 精确筛选存在时可不要求时间范围；普通列表默认由前端发送当前日志页时间范围。
- `page_size` 上限 100；所有查询始终有 Limit，禁止无界返回。

本实例管理接口用响应 Header 判断上游是否原生支持精确查询：

- Header 为 `filters-v1`：上游原生执行完整筛选；含请求 ID 时 `scope = exact`，否则 `scope = filtered`。
- 接口为 404 或无能力 Header（旧版上游）：回退调用既有 `/api/log/token`，仅在其近期数组中按前端发送的条件做本地过滤，`scope = recent_fallback`。结果与 total 都只代表这批近期数据，UI 必须提示“不代表完整范围”。

### 5.3 管理员使用日志的目标渠道选项

`GET /api/log/upstream/channels?keyword=<name-or-id>`

鉴权：`middleware.AdminAuth()`，与上游查询接口相同。仅供上游日志独立页面选择目标，不在渠道页面使用。

响应只包含：`id`、`name`、`type`、`is_multi_key`、`key_count`、`status`。只返回 New API 渠道，不返回 Base URL、Key、完整 settings、模型列表或成本配置。按 ID/名称搜索，结果上限 50，不做全量渠道加载。

### 5.4 错误契约

控制器使用 `ApiErrorI18n` 返回用户可执行的错误信息，底层错误先用 `logger.LogError(c, ...)` 记录。建议错误键：

- `upstream_log.channel_not_found`
- `upstream_log.unsupported_channel_type`
- `upstream_log.base_url_missing`
- `upstream_log.key_missing`
- `upstream_log.key_index_required`
- `upstream_log.key_index_invalid`
- `upstream_log.query_busy`
- `upstream_log.timeout`
- `upstream_log.unauthorized`：提示检查渠道令牌是否仍有效
- `upstream_log.upstream_unavailable`：提示稍后重试或先测试渠道连接
- `upstream_log.invalid_response`
- `upstream_log.not_found_exact`
- `upstream_log.not_found_recent_fallback`

上游 HTTP 状态、DNS 错误、IP、完整 URL 和原始响应体只进入脱敏服务日志，不直接返回浏览器。

## 6. 后端关键逻辑

### 6.1 渠道与令牌选择

1. 按 ID 查询渠道，只选择实现所需列，避免 `SELECT *`。
2. 仅接受 `constant.ChannelTypeNewAPI`。
3. 单令牌渠道使用 `channel.Key`，不把 Key 写入响应或日志。
4. 多令牌渠道解析 `GetKeys()`：
   - 必须提交 `key_index`；
   - 索引必须存在；
   - 禁用令牌默认仍允许只读查询，因为上游的 `TokenAuthReadOnly` 允许非禁用状态查询，但本地已标记禁用的 key 需要在 UI 明示；
   - 不调用 `GetNextEnabledKey()`，避免改变轮询状态或与 relay 竞争渠道锁。
5. 本地使用日志中的 `multi_key_index` 仅用于预填，后端仍重新校验。

V1 不提供“全部令牌”选项，避免一个管理操作按令牌数放大为大量外部请求。

### 6.2 上游 URL 生成

- URL 只能来自数据库中该渠道的 `base_url`，不接受请求体传入 URL。
- 仅允许 `http` / `https`。
- 去掉已知末尾 `/v1`，保留可能存在的部署前缀，再追加 `/api/log/token/query`；旧版降级时改用同前缀下的 `/api/log/token`：
  - `https://host/v1` -> `https://host/api/log/token/query`
  - `https://host/prefix/v1` -> `https://host/prefix/api/log/token/query`
  - `https://host/prefix` -> `https://host/prefix/api/log/token/query`
- 禁止 URL userinfo 和 fragment。
- Redirect 默认拒绝；如未来允许，只能同 scheme、同 host，且不得转发 Authorization 到其他主机。

渠道本身已获准向该 Base URL 发起 relay 请求，但日志查询仍使用上述收紧规则，防止管理接口变成任意 URL 代理。

### 6.3 HTTP Client 隔离

- 使用上游日志查询专用 `http.Client` / `Transport`，不复用 relay 的连接池。
- 总超时 8 秒，响应头超时 5 秒，连接超时 3 秒。
- 响应体上限 2 MiB，超限立即中止并返回可读错误。
- 专用有界信号量默认 8 个并发查询；满时快速返回“查询较多，请稍后重试”，不排长队。
- 请求 Context 继承管理请求，浏览器取消、发起新查询或离开独立页面时中断上游请求。
- 不自动重试，避免用户单次操作产生重复外部流量；用户可明确点击重试。

### 6.4 响应解析与脱敏

- 使用 `common.DecodeJson` / `common.Unmarshal`，不得直接调用 `encoding/json` 的编解码函数。
- 接受 New API 标准 `success/message/data` 结构；`data` 必须为数组。
- 最多解析 `page_size` 条，字符串字段设置长度上限。
- 只映射 UI 所需字段，并对 `other` 做 allowlist 投影。
- 永不记录 Authorization Header、渠道 Key、上游响应原文或含凭证的字段。

## 7. 数据模型与索引

### 7.1 数据模型变更

无新增表、列或迁移。功能只读取现有：

- 主库 `channels`
- 上游实例日志库 `logs`

不缓存、不持久化查询结果。

### 7.2 查询与索引设计

上游精确查询：

```sql
WHERE request_id = ? AND token_id = ?
ORDER BY id DESC
LIMIT 20
```

复用已有 `idx_logs_request_id`。请求 ID 高选择性，数据库先定位极少行，再校验 `token_id`。V1 不给 relay 高频写入的 `logs` 热表增加复合索引，避免每次消费日志写入承担额外索引维护成本。

实现阶段必须在实际日志数据库上对该查询执行 `EXPLAIN`：

- PostgreSQL / MySQL 应使用 `request_id` 索引而不是全表扫描。
- SQLite 回退测试验证语义兼容。
- 若真实数据证明单个请求 ID 存在大量重复，另起设计评估 `(request_id, token_id, id)` 复合索引，不能在本功能中直接加到热表。

筛选查询始终先加 `token_id = ?`，再复用当前日志查询的条件构造方式。请求 ID 精确查询优先使用 `idx_logs_request_id`；普通列表沿用现有 token、时间和排序索引。总数计算复用当前用户日志查询的有界计数策略，不能因组合筛选退化为无界全表 COUNT。

## 8. 配置参数

V1 不新增用户配置，也不新增环境变量：

- 上游地址复用渠道 `base_url`。
- 上游查询令牌复用渠道 `key`。
- 超时、响应体上限、并发上限作为保守的实现常量；若上线数据证明需要运营可调，再通过 `setting/` 单独设计热配置。

## 9. 权限与安全

1. 管理查询与目标渠道选项接口必须位于 `/api/log/upstream` 日志管理路由，使用 `AdminAuth`；不得注册到渠道路由或普通用户 `/self` 路由。
2. 前端同时按权限隐藏入口；后端权限是最终边界。
3. 令牌只在后端从数据库读取并写入上游 Authorization Header，不经过浏览器。
4. 所有管理查询写操作审计日志，但只记录管理员 ID、渠道 ID、是否精确查询、令牌序号和结果状态；请求 ID 可记录，令牌绝不记录。
5. 审计写入沿用非 relay 管理操作方式，失败不影响查询结果。
6. 上游日志内容可能包含用户错误文本，前端按普通文本渲染，不使用未消毒 HTML。
7. 只有请求 ID 与当前令牌所属日志同时匹配才返回，防止使用一个有效令牌枚举同一上游其他用户日志。
8. 普通用户自助日志接口与组件不得新增上游查询、上游收费、成本倍率、上游原始 quota 等字段；普通用户仍只看到现有本地计费口径。
9. 管理员查询结果不得写入全局共享 store，也不得复用普通用户日志的 query key；使用独立的管理员 query key，并在离开上游日志页面或管理员退出登录时清理。
10. 前端权限判断只用于体验优化。直接构造请求、篡改前端角色状态或复用旧缓存均不能越过后端 `AdminAuth`。

## 10. Main Chain Impact

本功能不修改 relay 路由、中间件、渠道选择、上游请求发送、响应流、计费结算或消费日志写入。

- relay goroutine 同步新增工作：0。
- relay 每请求新增 DB 调用：0。
- relay 每请求新增 Redis 调用：0。
- relay 每请求新增锁：0。
- relay 每请求新增 goroutine：0。

`upstream_request_id` 和 `multi_key_index` 已由现有 relay 日志记录，本功能只在管理员主动操作后读取已有值。

## 11. Shared Resource Audit

| 资源 | 本功能访问 | relay 是否访问 | 隔离/影响结论 |
|---|---|---|---|
| 主库 `channels` 表 | 每次查询 1 次只读，按主键选必要列 | 是，relay 读取渠道并依赖缓存 | 管理低频主键查询；不加锁、不写表、不扫表 |
| 上游实例 `logs` 表 | 由上游令牌接口精确或近期只读 | 上游 relay 写入该表 | 精确查询走 `request_id` 索引并限制 20；不新增索引写放大 |
| 本实例 `logs` 表 | 前端入口使用已经加载的日志数据；查询接口不再读本地 logs | 是，relay 异步/批量写入 | 无新增共享访问 |
| Redis | 不访问 | relay 广泛访问 | 无冲突、无新 key namespace |
| 内存缓存/map | 不新增结果缓存 | relay 使用多个缓存 | 无共享 map、无 GC 常驻增长 |
| HTTP 连接池 | 专用上游日志查询 Transport | relay 使用自己的 Transport | 完全分离，管理查询不能耗尽 relay 上游连接池 |
| 并发/worker | 专用容量 8 的信号量 | relay 使用自身并发资源 | 不共享 worker，不创建无界 goroutine |
| 渠道多 key 轮询锁 | 不访问 | relay 可能访问 | 按索引直接只读，不调用 `GetNextEnabledKey()` |

在 100,000 RPM relay 负载下，本功能对每个 relay 请求成本仍为零。管理员并发查询最多占用 8 个专用外部连接和少量短生命周期内存，不会占用 relay HTTP 连接池或 worker。

## 12. 关键边界与降级

| 场景 | 行为 |
|---|---|
| 非 New API 渠道 | 使用日志目标渠道选项不返回；直接调用查询接口时返回“不支持该渠道类型” |
| 渠道 Base URL 为空/非法 | 提示先编辑渠道地址 |
| 渠道 Key 为空 | 提示先填写渠道令牌 |
| 多令牌未选序号 | 不请求上游，要求选择 |
| 令牌序号越界 | 清空旧结果并提示重新选择 |
| 令牌在上游无效 | 提示检查令牌或先测试渠道连接 |
| 上游 403 | 提示该令牌无权查询日志 |
| 上游超时/断开 | 保留输入和上下文，提供重试按钮 |
| 上游旧版本 | 在近期结果内本地过滤，并标记“仅近期结果” |
| 精确查询无结果 | 新版上游明确提示未找到；旧版提示可能超出近期范围 |
| 上游返回多个同 ID 日志 | 全部展示，按时间倒序，不擅自选一条 |
| 渠道已禁用 | 仍允许主动只读查询，但显示渠道禁用状态 |
| 用户快速重复查询 | 前端取消前次；后端受专用并发上限保护 |
| 响应过大/结构异常 | 中止解析，提示上游响应格式不兼容 |

## 13. i18n

实现时所有新增前端文案必须同步 7 个语言包：`en`、`zh`、`zh-TW`、`fr`、`ru`、`ja`、`vi`。后端用户可见错误加入 `i18n/locales/{en,zh-CN,zh-TW}.yaml` 并用 `ApiErrorI18n` 引用键，不在控制器里硬编码文案。

前端 key 以英文源文案为 key。本功能共引入 34 个 key，分四组：

**页面与导航**：`Upstream Logs`、`Search upstream logs`、`No upstream logs found`、`Upstream Log Details`、`Upstream Log ID`、`View normalized fields returned by the upstream instance.`

**筛选字段**：`Select a New API channel`、`No matching channel`、`Local request ID`、`Upstream request ID`、`Select a channel key`、`Channel key #{{index}}`、`Resolved from the local request ID`、`Resolved automatically from the local request ID.`

**模式提示与结果状态**：`Tracing by local request ID: ...`、`Looking up this upstream request ID inside the selected channel.`、`Select a channel to browse its upstream logs, or enter a local request ID to trace one request.`、`Exact upstream query`、`Upstream filters applied`、`Recent results only`、`Recent upstream results`、`Channel key not recorded in the local log`、`{{count}} results · {{elapsed}} ms`

**空状态与错误**：`Enter a local request ID, or select a New API channel to browse upstream logs.`、`Check the request ID or adjust the channel filters, then search again.`、`The upstream instance has no log for this request. ...`、`The upstream instance does not support exact filters yet, ...`、`These filters could not be applied: {{filters}}.`、`Select a channel to query upstream logs`、`Loading channel information...`、`Failed to load channels`、`Query parameters are incomplete.`、`The start time must be earlier than the end time.`、`Query Upstream`

**验收口径**：`bun run i18n:sync` 后每个 locale 的 `missingCount` 必须为 0，且 `_reports/*.untranslated.json` 中不得出现本功能的任何 key（即 7 种语言都拿到真实译文，而不是回落英文占位）。仅当英文与目标语言天然同形（如法语的 `Type`、`Quota`）时才允许 key 与 value 相同。

## 14. 测试先行计划

实现前先编写以下测试，再写实现代码。

### 14.1 Model

新增 `GetLogByTokenIdAndRequestId` 测试：

- 等价类：正确 token + request ID 命中。
- 权限隔离：相同 request ID、不同 token 不能互相读取。
- 边界：空结果、1 条、20 条、超过 20 条。
- 顺序：重复 request ID 按 id 倒序。
- 方言：真实项目 DB 优先，SQLite 仅作为无真实 DB 时回退。
- 行清理：测试数据按唯一 ID 删除，不全表 truncate。

### 14.2 Controller / Middleware

- 既有 `/api/log/token` 行为和 Body 结构保持不变。
- `/api/log/token/query` 可单独及组合应用 type、username、token_name、model_name、时间范围、channel、log_id、group、request_id、upstream_request_id，并始终叠加认证 token_id。
- 完整筛选响应返回 `X-NewAPI-Log-Query: filters-v1`。
- `request_id` / `upstream_request_id` 长度 0、1、128、129；控制字符；前后空格。
- `page_size` 的 0、1、100、101 和非数字输入；page 的 0、1 和非数字输入。
- 本地目标渠道 ID 不会被误传为上游 `filters.channel`。
- 无 token、无效 token、禁用 token、被封用户的现有鉴权契约不回归。
- 管理接口对非管理员、渠道不存在、非 New API、空 Base URL、空 Key、多 key 缺索引、索引越界分别拒绝。
- 普通用户携带有效登录会话直接调用管理查询接口仍返回 403，且测试上游服务器必须确认没有收到请求。
- 普通用户自助日志响应不新增上游收费、成本或倍率字段。
- 使用 `gin.CreateTestContext` 与 `httptest`，断言项目标准错误结构。

### 14.3 Service / 上游适配

全部使用 `httptest.NewServer`，不得访问真实上游：

- 单令牌 Authorization 正确，响应中不回显 token。
- 多令牌按明确序号选择，且不改变 polling index。
- Base URL 为根路径、`/v1`、前缀、前缀加 `/v1` 的 URL 生成。
- 非 http(s)、userinfo、跨主机 redirect 被拒绝。
- 精确能力 Header 存在时标记 `exact`。
- 前端发送的每个 allowlist 筛选字段都按当前日志查询的参数名和语义转发；空值与未知字段不转发。
- 一键查询把本地 `upstream_request_id` 映射为上游 `request_id`，不会误传为上游的 `upstream_request_id`。
- 旧版无 Header 时仅过滤近期数组并标记 `recent_fallback`。
- 401、403、429、500、超时、连接失败、超大 Body、非法 JSON、错误 data 类型。
- `other` allowlist 生效，凭证样式字段被丢弃。
- 专用信号量满时快速失败，不发起第 9 个上游请求。
- 客户端取消后上游请求收到 Context 取消。

### 14.4 Frontend correctness gates

项目不新增测试框架。实现后运行：

- `bun run typecheck`
- `bun run lint`
- `bun run i18n:sync` 仅用于检查，翻译文件手工保持范围内变更

交互人工验证：

- 管理员使用日志桌面与移动布局中的页头入口，以及日志详情快捷入口。
- 渠道列表、渠道卡片和渠道编辑页确认没有新增入口。
- 从使用日志自动精确查询。
- 管理员可见入口，普通用户自助日志页完全不可见且不产生预取请求。
- 单 key / 多 key 切换。
- Loading、空、超时、旧上游降级、无权限状态。
- 深色/浅色主题、键盘 Enter、焦点顺序、桌面数据表、小屏卡片与居中详情 Dialog。

### 14.5 必跑后端测试

- 新增/相关包测试。
- 根目录 `go test ./...`。
- 不调用真实 AI 或真实上游日志服务。

## 15. 实施顺序

1. 先补 model、controller、service 测试用例。
2. 新增上游 `/api/log/token/query` 的完整筛选契约，保持 `/api/log/token` 不变。
3. 实现专用上游日志查询 service、URL 规范化、并发隔离和响应投影。
4. 注册 `/api/log/upstream/query`、`/api/log/upstream/channels` 与管理员权限。
5. 按现有使用日志数据表、筛选工具栏和详情 Dialog 模式实现上游日志独立页面。
6. 接入管理员使用日志页头范围 Tab 区与日志详情快捷入口，不修改渠道页面。
7. 按 i18n skill 同步全部前端语言和后端双语错误。
8. 执行 Go 测试、前端 typecheck/lint 和手工交互验证。
9. 回写本文档，使契约与最终代码一致。

## 15.1 实现情况（回写）

已按方案实现，契约与代码一致：

后端：
- `model/log.go` — 新增 `GetLogByTokenIdWithFilters`，强制 `token_id` 作用域 + 通用日志筛选，计数受 `logSearchCountLimit` 有界。
- `controller/log.go` — 新增 `GetLogByKeyQuery`（`/api/log/token/query`），响应头 `X-NewAPI-Log-Query: filters-v1`；既有 `GetLogByKey` 不变。
- `service/upstream_log.go` — 隔离的上游查询服务：专用 `http.Client`/`Transport`、容量 8 信号量、URL 规范化（去 `/v1`、拒 userinfo/fragment/非 http(s)）、2 MiB 响应上限、`other` allowlist 投影、能力 Header 判定与旧版 `/api/log/token` 近期降级过滤。
- `controller/upstream_log.go` — `QueryUpstreamLog`、`GetUpstreamLogChannels`（均 AdminAuth）；令牌选择、字段校验（trim/限长/控制字符/时间范围）、错误映射到 i18n。
- `router/api-router.go` — 管理员两个接口注册在 `logRoute.Use(PublicQueryRateLimit)` 之前；`/token/query` 走 `TokenAuthReadOnly`。
- `i18n/keys.go` + `en/zh-CN/zh-TW.yaml` — 新增 `upstream_log.*` 错误键（一致性测试通过）。

前端（历史 V1，已由 V2 替换）：
- `web/src/features/usage-logs/types.ts`、`api.ts` — 类型与 `queryUpstreamLog` / `getUpstreamLogChannels`。
- `web/src/features/usage-logs/upstream-log/context.ts` — 抽屉 Context 与 `useUpstreamLogSheet`（与组件文件分离以满足 fast-refresh 规则）。
- `web/src/features/usage-logs/upstream-log/upstream-log-sheet.tsx` — `UpstreamLogSheetProvider` + 查询抽屉（可搜索渠道选择器 `ComboboxInput`、令牌序号选择、请求 ID 输入、scope 徽章、结果列表、近期降级提示、AbortController 取消）。
  - **多令牌处理**：`isMultiKey` 由日志 `admin_info.is_multi_key` 同步带入；序号已知则自动查询，未知则渲染选择器并禁用查询按钮（不盲目用 0 号或轮询）。
  - **结果详情**：每条结果可点击展开标准化字段（上游日志 ID、类型、是否流式、令牌名、`other` allowlist 项），不渲染上游原始 JSON；请求 ID / 下一级请求 ID 单击复制；错误 content 高亮。
- 入口：日志详情「上游请求 ID」行的「查询上游」按钮（管理员）、通用日志页头范围 Tab 旁的「查询上游日志」按钮（管理员），均从当前上下文/已应用筛选带入条件。
- i18n：`en.json` + `zh.json` 新增功能文案。

### 15.2 V2 交互改造实现情况

- 已删除 `UpstreamLogSheetProvider`、Sheet Context 和页头按钮，没有保留双实现。
- `section-registry.tsx` 增加管理员专属 `upstream` 路由；页头统一为 `全部 / 仅自己 / 上游日志` 三个真实 Tab。
- `upstream-logs-page.tsx` 实现独立工作区：现有日志筛选工具栏、远程渠道搜索、多令牌选择、React Query 请求取消、服务端分页、桌面数据表、移动卡片、能力徽章和近期降级 Alert。
- `upstream-log-details-dialog.tsx` 使用项目居中 Dialog 展示标准化字段、错误信息和 allowlist `other`，请求 ID 可复制，不显示未知原始 JSON。
- 日志详情快捷入口已改为路由导航，通过 URL 参数传递渠道 ID、令牌序号和精确请求 ID；渠道令牌、Base URL、上游响应与收费数据不写入 URL。
- React Query 只在管理员进入上游页面且提交有效查询后请求日志；渠道元数据搜索和日志查询使用不同 query key，普通日志 cache 不包含上游结果。
- `ComboboxInput` 增加可选 `onSearchValueChange`，用于 250ms 防抖后的服务端渠道检索；不改变既有调用者行为。
- API、数据模型、后端鉴权、并发隔离和 relay 主链未改动；本轮仅替换前端信息架构与展示层。
- 按 i18n skill 通过脚本写入并同步 `en/zh/zh-TW/fr/ja/ru/vi` 七个语言包，所有 locale `missingCount` 为 0。

### 15.3 V3 使用体验修正实现情况

针对上一轮 review 暴露的“功能上说得通、用起来走不通”的点做了以下修正：

后端：

- `controller/upstream_log.go`
  - 反查模式下，本站日志缺失 `multi_key_index` 且渠道为多令牌时，允许客户端提交的 `key_index` 兜底；日志记录了序号时客户端值仍被忽略。响应新增 `source.key_index_from_log` 标明来源。
  - `logger.LogError/LogWarn` 统一传 `c` 而非 `c.Request.Context()`，保证 requestId 被采集（Rule 10）。
- `service/upstream_log.go` — `recent_fallback` 分支下 `page > 1` 直接返回空列表，不再把第 1 页内容当成第 2 页重复返回。
- `i18n/locales/{en,zh-CN,zh-TW}.yaml` — `upstream_log.trace_key_index_missing` 改为可执行文案，明确告知“清空本站请求 ID，改用渠道 + 上游请求 ID 查询”。

前端：

- `details-dialog.tsx` — 「查询上游」按钮只在日志有 `upstream_request_id` 时渲染；点击后在同一弹窗内展开本站/上游日志左右对比，关闭弹窗时重置对比状态。
- `upstream-compare-pane.tsx` — 按用户点击事件才发起反查，提供骨架屏、可重试错误态、无结果说明和多结果提示；结果字段与独立上游日志详情弹窗共用同一渲染组件。
- `upstream-logs-page.tsx`
  - 多令牌序号选择器从折叠的高级筛选移到第一行（渠道之后），不再出现“对着看不见的必填字段报错”。
  - 本站请求 ID 非空时禁用渠道、上游请求 ID 与全部高级筛选，并在摘要区显示当前生效模式。
  - 额度改用 `formatLogQuota`、耗时改用 `formatUseTime`，桌面表与移动卡片一致。
  - `recent_fallback` 隐藏分页器，并在 Alert 中列出该模式无法应用的筛选条件。
  - 空状态按“未查询 / 反查无结果 / 筛选无结果”给出三种不同的下一步说明；移动端与桌面端共用同一份文案。
  - `source.key_index_from_log === false` 时显示琥珀色徽章。
- `combobox-input.tsx`、`compact-date-time-range-picker.tsx` — 新增可选 `disabled`，向后兼容，既有调用者行为不变。

i18n：

- 补齐本功能全部 34 个前端 key 的 7 语言译文。此前有 25 个 key 只存在于代码里、`en.json` 也没有，i18next 直接回落 key 本身，导致中文界面显示 `Upstream Logs`、`Select a New API channel`、`Local request ID`、`Search upstream logs` 等英文原文。
- 顺带补齐 `Upstream Model` 在 fr/ru/ja/vi 的译文（日志详情复用，此前回落英文）。
- 验收：7 个 locale `missingCount = 0`，且 `_reports/*.untranslated.json` 不含本功能任何 key。

新增回归测试：

- `TestQueryUpstreamLog_TraceKeyIndexFallback`（真实 MySQL）— 覆盖“日志无序号则用客户端值”“日志有序号则客户端值被忽略”两条分支及 `key_index_from_log` 取值。
- `TestQueryUpstreamLogs_RecentFallbackHasNoSecondPage` — 锁定降级模式第 2 页为空。

实现前验证矩阵：

| 场景 | 输入/操作 | 预期 |
|---|---|---|
| 管理员直接进入 | 点击“上游日志”Tab | 进入独立页面，不自动查询；保留通用日志已应用筛选 |
| 普通用户越权 | 手工访问 `/usage-logs/upstream` | 前端回退通用日志；直接请求管理 API 仍为 403 |
| 单令牌精确查询 | 从日志详情进入，渠道 + 请求 ID 已知 | 页面加载后仅自动查询一次 |
| 多令牌序号已知 | 从日志详情进入并带 `key_index` | 使用指定序号自动查询一次 |
| 多令牌序号未知 | 从旧日志详情进入 | 不查询，要求明确选择序号 |
| 渠道切换 | 已有结果后切换渠道 | 清空序号、页码和旧结果，不展示跨渠道数据 |
| 手动筛选 | 修改筛选但未提交 | 不发请求；点击查询/请求 ID Enter 后页码归 1 并请求 |
| 服务端分页 | 切换页码或 page size | 使用已提交筛选重新请求，不采用前端切片 |
| 请求竞态 | 慢查询未结束时提交新查询/离页 | 取消旧请求，只展示最新响应 |
| 能力降级 | 上游不支持 filters-v1 | 显示“仅近期结果”Alert，结果仍可查看 |
| 无结果/错误 | 返回空列表或业务错误 | 分别显示项目 Empty 与可执行 toast，不暴露内部错误 |
| 响应式 | 宽屏/小屏 | 宽屏数据表，小屏卡片；详情均为居中 Dialog |

测试：
- `service/upstream_log_test.go` — URL 规范化、查询构造、`other` 投影、精确/筛选/近期降级 scope、鉴权/非法响应、信号量满快速失败。
- `controller/upstream_log_test.go` — 字段校验（trim/限长/控制字符/时间范围）、单/多令牌序号选择。
- `controller/upstream_log_integration_test.go` — 安全契约：无凭证 → 401、普通用户（真实登录令牌）→ 403，两种被拦截路径下**上游零请求**；管理员命中上游且渠道令牌以 Bearer 转发、绝不回显；非 New API 渠道被拒。
- `model/log_test.go` — `GetLogByTokenIdWithFilters` 令牌作用域、跨令牌隔离、类型/模型/分组/时间筛选、空结果。

`go build ./...`、`bun run typecheck` 通过；`bun run lint` 对新增文件无告警（仓库既有无关告警未处理）；`service`/`controller`/`i18n` 测试全绿；`model` 新增测试在隔离运行下通过（该包整包并发测试存在与本功能无关的既有 flaky，已用 `git stash` 在干净树上复现证明）。

### 15.4 V4 Tab 交互与筛选状态生命周期（已实现）

#### 15.4.1 现状问题

1. 页头的「全部 / 仅看自己 / 上游日志」把“本站日志的数据范围”和“另一个日志工作区”混在同一组 Tab 中，三个选项不是同一维度。
2. 从本站日志进入上游日志时会整包复制当前 URL search；`type`/`model`/`channel` 等同名参数在两个工作区含义不完全相同，导致条件泄漏和意外自动填充。
3. 从上游返回本站日志只清理 `upstreamKeyIndex`/`autoQuery`，其余上游条件仍会污染本站日志。
4. `UpstreamLogsPage` 只在首次挂载时从 URL 初始化 draft；浏览器前进/后退或同路由收到新反查参数后，draft 不会可靠同步。
5. `autoQueryHandledRef` 是组件级一次性开关：同一挂载周期内第二次从详情发起反查可能被忽略。
6. 上游分页只修改本地 state，URL 的 `page`/`pageSize` 不更新；刷新、复制链接和浏览器回退都不能还原用户当前位置。
7. 筛选 draft、已提交条件、URL 和查询结果四份状态缺少明确的主从关系，Tab 切换时用户无法预测哪些会保留、哪些会清除。

#### 15.4.2 目标交互

- 页头使用一组紧凑的真实 Tab：「全部 / 仅自己 / 上游日志」（上游日志仅管理员可见）。虽然前两项是本站范围、后一项是工作区，但统一入口比两组不同视觉样式的 Tab 更易扫读，也避免页头出现两套选中态。
- 切换工作区不复制对方的筛选参数；每个工作区在当前页面会话内保留自己最后一次已提交条件和分页位置。
- 用户还没点「查询」的草稿也保留，切回时可继续编辑；草稿不写入 URL，不触发请求。
- 点「查询」后才将 draft 提交为 committed filters，同步 URL、页码归 1 并发起请求。
- 分页和每页数量属于 committed state，每次变更都使用 `replace` 同步 URL，不为每一页堆叠浏览器历史。
- 「重置」只重置当前工作区，不影响另一个工作区的条件。
- 本站「全部/仅看自己」切换时保留当前筛选，但页码归 1，避免从大数据集的高页码切到小数据集后出现假空状态。
- 从本站日志详情发起反查时，该显式用户意图优先于上游工作区的旧快照：替换为新的本站请求 ID，页码归 1，且每一个不同的反查签名只自动执行一次。
- 浏览器前进/后退和直接打开分享 URL 时，URL 始终是当前工作区 committed state 的权威来源。

#### 15.4.3 状态生命周期矩阵

| 操作 | 当前 draft | 当前已提交条件/结果 | 当前页码 | 另一工作区 |
|---|---|---|---:|---|
| 编辑筛选 | 更新 | 保留 | 保留 | 不变 |
| 点击查询 | 保留并提交 | 替换并请求 | 1 | 不变 |
| 分页/改每页数 | 保留 | 保留 | 同步 URL | 不变 |
| 切换工作区 | 保存快照 | 保存快照 | 保存快照 | 恢复目标快照 |
| 切换全部/仅自己 | 保留 | 用相同筛选重查 | 1 | 不变 |
| 重置 | 恢复默认 | 清空结果/恢复默认查询 | 1 | 不变 |
| 前进/后退 | 按 URL 重建 | 按 URL 重建 | 按 URL 恢复 | 不变 |
| 新的详情反查 | 替换为新 trace | 自动提交一次 | 1 | 本站快照不变 |

#### 15.4.4 实现边界

- 新增页面会话级、按工作区隔离的筛选快照；不使用跨会话 `localStorage`，避免过期条件在数天后意外恢复。
- 工作区切换在 Tab 事件处理器内完成快照与导航，不用 effect 模拟用户交互。
- URL -> draft/submitted 同步使用可比较的 search signature 或 keyed reset，不保存可派生字段，避免多份 state 漂移。
- 不使用 CSS `display:none` 同时常驻两张数据表；隐藏工作区不保留活跃 query observer，不会在后台继续请求或参与重渲染。
- 权限不变：上游工作区仍只对管理员渲染，后端 `AdminAuth()` 仍是最终边界；权限丢失时清除上游快照并返回本站日志。
- 不改后端 API、数据库、Redis、缓存 namespace 或主 relay 链。所有变更仅位于前端路由、页面状态和交互层。

#### 15.4.5 预计文案与验证

- 必要的状态提示通过 `add-missing-keys.mjs` 一次性补齐 en/zh/zh-TW/fr/ja/ru/vi，再运行 `bun run i18n:sync`。
- 前端无测试框架，不新增测试依赖。按 Rule 15.7 使用以下验收矩阵：
  1. 本站与上游各自设置不同筛选，反复切换后两边的 draft、已提交条件、结果和页码都不串扰。
  2. 编辑未提交条件后切走再切回，草稿仍在且没有发起新请求。
  3. 本站高页码上切换「全部/仅自己」，页码归 1，无假空状态。
  4. 上游分页、刷新、复制 URL、前进/后退均还原同一 committed query。
  5. 连续从两条不同本站日志点「查询上游」，两个 trace 都恰好自动执行一次。
  6. 普通用户不看到上游工作区，直达 URL 仍回落本站日志。
  7. 桌面和移动宽度都完成真实浏览器操作与截图验证。
8. `bun run typecheck`、改动文件 lint、`bun run build`和 `bun run i18n:sync` 全部通过。

#### 15.4.6 实现结果

- 页头最终按用户反馈恢复为同一组“全部 / 仅自己 / 上游日志”Tab，使用同一容器、同一选中态和一次点击切换，不再并排显示胶囊式工作区与下划线式范围选择器。
- 本站与上游使用互不重名的 URL 参数。已提交筛选与分页可刷新、分享和浏览器回退；未提交 draft 仅保存在当前页面会话的内存快照中，整页刷新后不会恢复过期草稿。
- 查询才提交 draft；编辑、切换工作区和展开高级筛选均不会隐式请求。重复提交同一查询会显式刷新结果。
- 上游分页使用 `replace` 同步 URL；重置仅清除上游参数。本站范围切换保留筛选并回到第 1 页。
- 隐藏工作区使用 React `Activity` 暂停 effect，并通过活动状态关闭其 footer portal。真实浏览器验收曾发现隐藏的本站分页会穿透显示在未查询的上游空状态中，现已修复。
- 对比结果数量提示等本功能文案已补齐 en/zh/zh-TW/fr/ja/ru/vi，不存在英文 key 回落。
- 已完成 1440×900 桌面、390×844 移动端和日志详情左右对比的真实浏览器操作及截图验证。

## 16. 验收标准

1. 管理员从含上游请求 ID 的使用日志详情点击一次，即进入上游日志独立页面并看到对应结果、上游收费信息或明确的可执行错误。
2. 管理员可通过页头“上游日志”真实 Tab 进入独立页面，选择一个 New API 目标渠道，发送与当前已应用日志查询一致的筛选条件，并通过 Enter 按请求 ID 精确查询。
3. 多令牌查询永远使用明确序号，不轮询全部令牌，不改变 relay 的轮询状态。
4. 渠道令牌不出现在浏览器响应、前端状态、应用日志或审计日志中。
5. 新版上游精确查询与旧版近期降级在 UI 中明确区分。
6. 管理查询的 HTTP 连接池、并发限制与 relay 完全隔离。
7. `go test ./...`、`bun run typecheck`、`bun run lint` 全部通过。
8. 普通用户在自助日志页面看不到入口；直接调用管理接口返回 403；响应、前端 cache 和全局状态中均不存在上游收费信息。
9. 页面中不再存在上游日志 `Sheet`；桌面端为完整数据表，移动端为卡片列表，详情使用居中 `Dialog`。
## 17. V3 修订：通过本站请求 ID 反查上游日志（2026-08-25）

本节替代前文中“由浏览器提交渠道 ID、令牌序号和上游请求 ID”的精确查询交互。已有按渠道浏览的底层接口保留兼容，但管理员页面和日志详情入口统一采用本站请求 ID 反查，不再要求用户理解或输入“下一级上游请求 ID”。

### 17.1 目标与界面

- 页面第一行按“New API 渠道 / 本站请求 ID / 上游请求 ID”排列，渠道位于首位；时间范围进入第二行高级筛选。不再把含义模糊的 `filters.upstream_request_id`（上游实例的下一跳请求 ID）暴露为用户输入项，但保留 `filters.request_id` 对应的“上游请求 ID”精确查询。
- 日志详情的“查询上游”传递当前本站日志的 `request_id`，而不是把已加载的 `upstream_request_id` 交给浏览器继续拼装查询。
- 渠道、单/多令牌及令牌序号均由后端根据本站日志自动解析；页面只展示解析结果，不允许用户手工猜测令牌序号。
- 查询成功后展示清晰的请求链：`本站请求 ID -> 上游请求 ID`，以及实际渠道和多令牌序号（如有）。
- 保留三种查询路径：填写本站请求 ID 时优先执行可信反查；本站请求 ID 为空且填写上游请求 ID 时，在所选渠道内按 `filters.request_id` 精确查询；两个请求 ID 均为空时按渠道、时间及高级条件浏览。三种方式共用结果区。

### 17.2 数据流

```text
管理员输入本站 request_id（或从日志详情一键带入）
  -> POST /api/log/upstream/query { local_request_id }
  -> AdminAuth
  -> 本站 LOG_DB 使用 idx_logs_request_id 精确读取最近一条匹配日志
     仅选择 request_id / upstream_request_id / channel_id / other / created_at
  -> 从日志解析 channel_id 与 other.admin_info.multi_key_index
  -> 从 channels 表读取 New API 渠道配置并选择唯一确定的渠道令牌
  -> GET {upstream}/api/log/token/query?request_id=<本站日志的 upstream_request_id>
  -> 上游 TokenAuthReadOnly 强制叠加其 token_id
  -> 标准化、裁剪后返回管理员页面
```

若本站请求 ID 不存在、本站日志没有记录上游请求 ID、渠道不是 New API、或多令牌日志缺少明确序号，后端在发起任何上游请求前终止并返回可执行提示。不得轮询渠道或全部令牌。

### 17.3 API 修订

管理接口仍为 `POST /api/log/upstream/query`，新增并优先使用精确反查请求：

```json
{
  "local_request_id": "req_local_abc123"
}
```

- `local_request_id`：必填，trim 后 1–64 字符，拒绝控制字符。
- 精确反查模式不接受浏览器覆盖 `channel_id`、`key_index` 或上游 `filters.request_id`；这些值全部由后端可信数据解析。
- 旧的按渠道筛选请求体继续用于“本站请求 ID 为空”的渠道浏览模式；当 `local_request_id` 非空时，后端忽略客户端同时提交的渠道、令牌序号和上游请求 ID，以本站日志解析结果为准。

成功响应增加追踪上下文：

```json
{
  "source": {
    "request_id": "req_local_abc123",
    "upstream_request_id": "req_upstream_xyz789"
  },
  "channel": {
    "id": 12,
    "name": "Upstream A",
    "key_index": 2,
    "is_multi_key": true
  },
  "query": {
    "scope": "exact"
  },
  "total": 1,
  "items": []
}
```

### 17.4 数据、性能与主链影响

- 不新增表、列或迁移；复用 `logs.request_id` 的 `idx_logs_request_id`。
- 每次管理员主动查询增加一次本站日志索引读取和一次渠道读取，均不在 AI relay 请求链上。
- 不修改 relay、中间件、日志写入、计费或缓存；不新增 Redis key、共享内存结构、连接池或工作池。
- `logs` 表也由 relay 异步写入，但这里只执行高选择性的单行只读查询，不加索引、不加锁、不做 COUNT，不改变热路径写放大。

### 17.5 错误与测试

新增用户可执行错误：本站请求不存在、尚未记录上游请求 ID、多令牌序号缺失。底层数据库错误记录真实原因，响应只返回 i18n 文案。

实现前先补测试，覆盖：

- 本站请求命中后，将其 `upstream_request_id` 精确映射为上游 `request_id`。
- 浏览器不能覆盖后端解析出的渠道或令牌序号。
- 本站请求不存在、缺少上游请求 ID、非 New API 渠道、多令牌序号缺失时，上游服务请求计数始终为零。
- 普通用户仍为 403，且不会读取渠道或访问上游。
- SQLite、MySQL、PostgreSQL 使用同一套 GORM 精确查询；ClickHouse 日志库使用相同字段投影与确定性时间倒序。

### 17.6 实现结果

- `model.GetLogTraceByRequestId` 只投影可信路由字段，并按时间、日志 ID 倒序确定旧数据重复项。
- 管理接口接受 `local_request_id`；该字段非空时覆盖并忽略浏览器提交的渠道、令牌序号和上游请求筛选。
- 页面第一行保留三项：New API 渠道、本站请求 ID、上游请求 ID；高级筛选保留时间范围、多令牌序号、类型、用户名、令牌、模型和分组。“下一级上游请求 ID”输入已移除。
- 类型选择器通过项目 Select 的 `items` 映射显示翻译标签，不再显示原始枚举值 `0`。
- 日志详情按钮改为提交本站 `request_id`；结果摘要展示本站请求 ID 到上游请求 ID 的映射。
- `go test ./...`、`go build ./...`、`bun run typecheck`、`bun run build` 均通过；新增前端文件定向 lint 通过。测试服务器版本为 `zhuzhan-upstreamlog-local-trace-e5465981a`。

### 17.7 V4 测试服务器部署记录

- 2026-08-26 已部署香港测试服务器，版本为 `zhuzhan-upstreamlog-v4-20260826-e5465981a`。
- Linux amd64 发布包 SHA-256 为 `9ae57ac41de08d9e02f83dc6376c0db777d3d4e5eb9c8f5e641075a5bb2a0bdd`；上传后由服务器再次校验一致，并通过 ZIP 与 ELF 格式检查。
- Linux amd64 二进制 SHA-256 为 `73cb41b0f2dd4d8f054eb3ec0a3e4131f4c620061ecb272f5afef885b57309ec`，服务器运行文件与本地构建一致。
- 使用服务器既有 `graceful_update.sh` 发布；旧进程在 1 秒内优雅退出，旧二进制备份到 `/root/backup/20260825_165852/new-api`（服务器使用 UTC 时间）。
- 部署后本机与公网 `/api/status` 均返回 HTTP 200 和新版本；`/usage-logs/upstream` 返回 HTTP 200，首页引用本次构建资源 `static/js/index.29debe06e2.js`。
- 未认证访问上游渠道列表与查询接口均返回 HTTP 401，管理员鉴权边界保持生效。
- 2026-08-26 根据实际使用反馈将页头恢复为统一的“全部 / 仅自己 / 上游日志”Tab，并重新部署版本 `zhuzhan-upstreamlog-tabs-unified-20260826-e5465981a`。发布包 SHA-256 为 `1d656aee06ee465581ec76f7693af2915496a60bc6476acce5bf489cf1ee153e`，二进制 SHA-256 为 `66f52a8d7b3d21513b326a2480a5d130750240dfcfd3ab761d2f5ad3d1018233`；旧版本备份到 `/root/backup/20260825_171340/new-api`（服务器使用 UTC 时间）。公网状态、SPA 路由、新资源 `static/js/index.47a3b9ed71.js` 与未认证接口 401 均已复核。
