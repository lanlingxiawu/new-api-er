# 充值订单平台到账状态持久化与手动查询

## 背景

充值订单历史弹窗当前只展示 `top_ups.status`。该字段表示本系统是否已经处理充值，不能单独证明支付平台是否已经收款。Webhook 延迟、丢失或本地处理失败时，本地状态与平台状态可能不一致。

本设计中的 `inf` 按现有支付实现解释为 **Infini**。首期只支持官方支付宝（`alipay_official`）和 Infini（`infini`）订单。

## Goals and scope

### 目标

- 在订单历史弹窗中展示持久化的平台支付状态及最后确认时间。
- 只有管理员可以主动查询平台状态。
- 管理员既可以查询单笔订单，也可以批量查询当前页支持的订单。
- 每次成功取得可识别的平台响应后，先更新数据库，再向前端返回“已更新”。
- 查询必须从本地订单号出发，由后端读取真实支付平台与平台订单号，不能信任前端提供的平台类型。
- 普通用户不能触发查询，但可以在自己的订单历史中看到管理员已保存的平台状态。

### 范围外

- 首期不支持易支付、Stripe、Creem、Waffo、微信支付或订阅订单。
- 不根据平台查询结果自动补单、增加额度、回滚额度、关闭订单或修改 `top_ups.status`。
- 不新增自动轮询、定时对账、后台重试或告警功能。
- 不修改 CSV 导出内容。

## 用户界面

### 所有用户可见

- 复用订单卡片头部现有 `StatusBadge`，在本地订单状态旁展示持久化的平台状态。
- 已查询状态：
  - `credited`：成功色圆点标识“平台已到账”。
  - `not_credited`：中性色标识“平台未到账”。
  - `unknown`：警告色标识“平台状态未知”。
- 标识旁或提示中展示“最后确认：{{time}}”。
- 从未成功查询的平台状态为空。普通用户不显示额外占位，避免把“未查询”误解成“未到账”。
- 本地状态与平台状态允许并列且不互相覆盖。例如本地“待支付”、平台“已到账”时，仍由管理员决定是否使用现有补单动作。

### 仅管理员可见

- 支付宝官方和 Infini 订单卡片在右上角平台状态旁显示紧凑刷新按钮，用于单笔查询；按钮使用现有 ghost 图标按钮和 Tooltip 样式，不额外撑高订单卡片。
- 弹窗筛选工具栏增加“查询当前页”按钮，批量查询当前页所有支付宝官方/Infini 订单；不增加复选框或新的选择模式。
- 当前页没有支持的订单时，批量按钮禁用。
- 单笔查询只禁用对应订单按钮；批量查询期间禁用批量按钮以及本批次涉及的单笔按钮，避免重复提交。
- 返回部分失败时，成功订单立即更新标识，失败订单保留原持久化状态，并以一条汇总 toast 告知“已更新 {{success}} 笔，{{failed}} 笔查询失败”。不为每条失败订单弹 toast。
- 平台查询失败不擦除最后一次成功确认的标识；管理员可以稍后重试。

新增 UI 文案全部使用 `t(...)`。实现时必须通过 `web/scripts/add-missing-keys.mjs` 一次性写入 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`，随后运行 `bun run i18n:sync`；不直接编辑 locale JSON。

## Data flow

### 读取历史记录

1. 用户或管理员打开订单历史弹窗。
2. 前端继续调用现有充值记录列表接口。
3. `TopUp` 列表响应直接包含持久化的 `platform_payment_status`、`platform_payment_status_raw` 和 `platform_payment_status_checked_at`。
4. 前端只渲染持久化快照，不自动查询平台。

### 管理员手动查询

1. 管理员点击单笔“查询平台状态”，或点击“查询当前页”。
2. 前端将一个或多个 `trade_no` 发送到管理员批量查询接口；单笔查询与批量查询使用同一 API。
3. 后端在一次数据库查询中读取订单，并根据数据库中的 `payment_provider` 分派：
   - 支付宝：以本地 `trade_no` 作为 `out_trade_no` 调用 SDK `TradeQuery`。
   - Infini：以本地 `provider_order_id` 调用 `GET /v1/acquiring/order?order_id=...`。Infini 官方文档提供该查询接口，并将 `paid` 定义为已支付：<https://developer.infini.money/docs/en/6-api-ducumentation>。
4. 每个订单的平台查询独立执行并规范化状态。
5. 对成功取得可识别平台响应的订单，后端使用短小、逐行的 GORM `Updates` 写入持久化字段；外部 HTTP 调用不放在数据库事务内。
6. 只有数据库更新成功的订单才返回 `result = updated`。平台失败或数据库更新失败作为逐项失败返回，不影响同批其他订单。
7. 前端把 `updated` 项合并到当前页记录；失败项继续显示数据库中原有的最后确认状态。

## API contracts

### 管理员查询接口

`POST /api/user/topup/platform-status`

- Auth：放入现有管理员用户管理路由组，继承 `middleware.AdminAuth()` 与 `middleware.RequirePermission(authz.AdminMenuUsersView)`。
- Rate limit：复用 `middleware.UserCriticalRateLimit()`。
- 不提供普通用户查询接口。

### Request

```json
{
  "trade_nos": ["ALI1NO...", "INFINI-1-..."]
}
```

- `trade_nos` 必填。
- 去除首尾空白后必须有 1–100 个非空值。
- 重复订单号只查询一次，响应顺序采用首次出现顺序。
- 数量为 0、超过 100、包含空值或 JSON 非法时，记录原因并使用 `ApiErrorI18n` 返回参数错误。

### Response

接口接收成功后使用标准成功 envelope。单项失败不会把整个请求变成业务失败。

```json
{
  "success": true,
  "message": "",
  "data": {
    "summary": {
      "requested": 3,
      "updated": 1,
      "failed": 2
    },
    "items": [
      {
        "trade_no": "ALI1NO...",
        "provider": "alipay_official",
        "result": "updated",
        "platform_payment_status": "credited",
        "platform_payment_status_raw": "TRADE_SUCCESS",
        "platform_payment_status_checked_at": 1787200000
      },
      {
        "trade_no": "INFINI-1-...",
        "provider": "infini",
        "result": "query_failed"
      },
      {
        "trade_no": "UNKNOWN",
        "result": "not_found"
      }
    ]
  }
}
```

`result` 枚举：

| 值 | 含义 | 是否修改持久化状态 |
|---|---|---|
| `updated` | 平台响应已规范化且数据库更新成功 | 是 |
| `query_failed` | 配置缺失、平台超时、鉴权失败、订单查询失败或响应非法 | 否 |
| `persist_failed` | 平台查询成功，但数据库更新失败 | 否 |
| `unsupported` | 本地订单存在，但平台不在首期范围 | 否 |
| `not_found` | 本地订单不存在 | 否 |

响应不返回平台错误码、错误消息、完整响应体、平台订单号或密钥相关信息。

## 平台状态映射

### 支付宝官方

| 支付宝结果 | 持久化状态 | 原始状态 |
|---|---|---|
| `TRADE_SUCCESS`、`TRADE_FINISHED` | `credited` | 原值 |
| `WAIT_BUYER_PAY`、`TRADE_CLOSED` | `not_credited` | 原值 |
| 明确的交易不存在业务结果 | `not_credited` | `TRADE_NOT_EXIST` |
| API 成功但出现未知的新状态 | `unknown` | 返回的状态原值 |
| SDK、网关、鉴权或限流错误 | 不更新 | 不保存 |

查询使用现有支付宝配置与沙箱开关，并给 SDK 调用传入带 5 秒截止时间的 request context。

### Infini

| Infini 结果 | 持久化状态 | 原始状态 |
|---|---|---|
| `paid` | `credited` | `paid` |
| `pending`、`processing`、`partial_paid`、`expired` | `not_credited` | 原值 |
| API 成功但出现未知的新状态 | `unknown` | 返回的状态原值 |
| HTTP/业务错误、响应非法、订单不存在、缺少 `provider_order_id` | 不更新 | 不保存 |

金额、币种与 `client_reference` 可用于服务端安全校验或诊断，但不得写入日志中的敏感信息。该接口只更新状态快照，不执行 webhook 落账逻辑。

## Data model changes

在 `model.TopUp` 增加：

| 字段 | JSON | 数据库类型 | 含义 |
|---|---|---|---|
| `PlatformPaymentStatus` | `platform_payment_status` | `varchar(32)`, default `''` | `credited`、`not_credited`、`unknown`；空值表示从未成功查询 |
| `PlatformPaymentStatusRaw` | `platform_payment_status_raw` | `varchar(64)`, default `''` | 最后一次成功平台响应的原始状态 |
| `PlatformPaymentStatusCheckedAt` | `platform_payment_status_checked_at` | `bigint`, default `0` | 最后一次成功查询并持久化的 Unix 秒 |

### Migration and compatibility

- 使用现有 GORM AutoMigrate 增加列，不编写数据库专属 DDL。
- 三个字段均为简单标量类型，兼容 SQLite、MySQL 5.7.8+、PostgreSQL 9.6+。
- 不修改既有字段语义，旧订单默认得到空状态和时间 0。
- 不对查询失败进行持久化，避免一次临时故障覆盖最后一次有效确认。

### Index design

- 不新增索引：新字段只随订单列表展示，不用于 `WHERE`、`JOIN` 或 `ORDER BY`。
- 管理员查询仍通过现有 `trade_no` 唯一索引执行 `WHERE trade_no IN (...)`，最大 100 个值。
- DB 读取只选择 `id`、`trade_no`、`payment_provider`、`provider_order_id` 等必需列，不使用 `SELECT *`。
- 持久化通过主键 `id` 逐行更新三个状态字段。

## Config parameters

不新增用户配置。查询复用现有支付宝/Infini 凭据、沙箱环境与 Base URL。批量上限、并发数和超时属于接口保护约束，使用代码内常量。

## Concurrency and persistence strategy

- 单次最多查询 100 单，单请求最多 6 个平台查询 worker。
- 使用仅供“支付平台状态查询”使用的进程级信号量，最多同时执行 12 个上游请求；不与 relay、webhook 或其他支付流程共用 worker 池。
- 每个上游调用超时 5 秒，整批请求总截止时间 20 秒。
- Infini GET 使用 request context；不沿用当前固定 30 秒且不接收 context 的 `infiniPost`。
- 平台查询阶段可以受限并发；持久化阶段逐项、短事务外更新，避免同时占用多条 DB 连接。
- 不在持有数据库锁或事务时调用上游平台。
- 不新增 Redis 或内存状态缓存。数据库是平台状态快照的唯一持久化来源。
- 单笔和批量查询共享同一后端路径，统一应用权限、频控、并发与状态映射。

## Error handling strategy

- 请求级参数或初始 DB 查询失败：记录底层错误，使用 `ApiErrorI18n` 返回可读错误，不返回原始 Go 错误。
- 单个平台查询失败：记录带 requestId、provider、`trade_no` 的服务端日志，不记录密钥、签名、Authorization 或完整平台响应；该项返回 `query_failed`，不修改旧快照。
- 单项 DB 更新失败：记录错误并返回 `persist_failed`，其他订单继续处理。
- 平台配置缺失视为可恢复查询失败，不影响订单列表、已保存状态或用户额度。
- 前端整批网络失败时保留所有旧标识，只显示一次可操作错误提示。

新增后端用户可见错误必须同步到 `i18n/locales/en.json` 与 `i18n/locales/zh.json`，Controller 使用 `ApiErrorI18n`。

## Interaction with existing subsystems

- **充值记录列表**：新增字段随既有 `TopUp` JSON 返回，不改变筛选、分页和导出。
- **管理员补单**：查询与补单是两个独立动作；查询不会自动触发现有 `ManualCompleteTopUp`。
- **Webhook/落账**：不调用 `RechargeAlipay`、`RechargeInfini`，不获取订单锁，不写用户额度。
- **支付配置**：只读取现有支付宝/Infini 配置。
- **前端 API**：新增方法放在 `web/src/features/wallet/api.ts`，不修改 upstream-owned 的 `web/src/lib/api.ts`、`http-client.ts` 等文件。
- **i18n**：新增按钮、标识、时间提示和汇总反馈覆盖全部 7 个前端 locale。

## Main Chain Impact

本功能不挂接 AI relay 路由、middleware、计费、额度预扣或结算流程。

- relay goroutine 同步执行：无任何新增逻辑。
- 管理员查询同步执行：一次索引 DB 读取、受限并发的平台只读查询、成功项的短逐行 DB 更新。
- 普通订单列表：只多读取三个同表标量列，不调用平台。
- 失败隔离：查询/持久化失败只影响平台状态快照，不传播到 relay、本地订单状态或用户额度。

## Shared Resource Audit

| 资源 | 本功能访问 | relay 是否访问同一资源 | 隔离结论 |
|---|---|---|---|
| 主库 `top_ups` 表 | 批量索引读取；成功项逐行更新三个新列 | 搜索现有 relay/middleware 未发现 relay 热路径访问；支付与订阅控制器会访问 | 上游调用期间无事务；更新只锁单行且不修改 webhook 业务列 |
| 主 DB 连接池 | 初始读取 1 次；持久化顺序更新最多 100 次 | relay 使用同一数据库资源 | 仅管理员、带频控；写入顺序执行，不并发占满连接池 |
| 用户/额度表 | 不访问、不写入 | relay 会访问额度相关数据 | 完全隔离 |
| Redis | 不访问 | relay 广泛使用 | 无 key 命名空间冲突 |
| 进程内信号量 | 新建支付状态查询专用容量 12 | relay 不使用 | 不共享，不会饿死 relay worker |
| HTTP 连接 | 仅支付平台查询，最大并发 12 | relay 可能使用默认 transport | 专用信号量限制并发，不占用 relay goroutine/worker pool |
| 订单锁 | 不使用 | relay 不使用 | 不与 webhook/补单争用应用级订单锁 |

## Concurrency Analysis at 100k relay RPM

该功能不在 relay 请求内，因此 100k RPM relay 流量下每个 AI 请求新增：

- DB 调用：0
- Redis 调用：0
- 锁/信号量操作：0
- goroutine：0
- 支付平台请求：0

一次管理员批量查询自身产生 1 次索引 DB 读取、最多 `min(订单数, 100)` 次支付平台查询、最多 6 个请求内 worker，以及每个成功订单 1 次顺序行更新。进程级平台请求并发恒定不超过 12；管理员权限、频控和 20 秒截止时间限制总体压力。

## Key business logic and edge cases

- 平台分派一律使用数据库 `payment_provider`，不使用可能仅代表展示渠道的 `payment_method`。
- 重复 `trade_no` 只查询和更新一次。
- Infini 旧订单缺少 `provider_order_id` 时不得以本地订单号猜测，返回 `query_failed`，保留旧状态。
- 平台成功返回未知新状态时持久化为 `unknown` 并保存原始状态，便于识别协议变化。
- 平台请求失败或 DB 写入失败不得更新 `checked_at`，旧快照保持不变。
- 若平台最新结果从 `credited` 变为其他明确状态，按最新平台响应覆盖快照，但不自动回滚本地充值。
- 当前页没有支付宝/Infini 订单时不发送批量请求。
- 快速重复点击、单笔与批量重叠时，前端按 `trade_no` 维护查询中集合，避免同一订单并发请求。
- API 返回后只合并 `result = updated` 的记录；翻页后到达的旧响应不得写入新页。

## Test-first plan

实现前先提交测试用例，再写生产代码。

### 后端测试

1. 状态映射表驱动测试：覆盖支付宝/Infini 全部已知状态、未知状态、交易不存在、大小写与空白规范化。
2. 请求边界：订单数量 0、1、100、101；空值；重复订单号。
3. 权限：路由必须在 `AdminAuth + AdminMenuUsersView` 组中；不存在普通用户查询路由。
4. 平台选择：只依据数据库 `payment_provider`；其他平台返回 `unsupported`。
5. Infini：使用 `httptest.NewServer` 验证 GET path、URL 编码、签名 request-target、成功/非 2xx/非零业务码/非法 JSON/超时，不访问真实平台。
6. 支付宝：通过窄接口或可替换查询函数注入 fixture，验证 `out_trade_no` 与各状态映射，不访问真实平台。
7. 持久化：成功更新三个字段；平台失败、未知订单、非支持平台不更新；DB 更新失败返回 `persist_failed`；旧有效状态不会被查询错误覆盖。
8. 批量容错：一项超时不覆盖其他项；并发不超过上限；响应顺序稳定；汇总数量正确。
9. Controller：使用 `httptest`、`gin.CreateTestContext` 和 `testify` 验证标准 envelope 与错误处理。
10. 数据库：真实项目 DB 优先、SQLite fallback；测试数据按唯一 `trade_no`/行 ID 清理，不截断共享表。
11. 迁移：验证旧行获得兼容默认值，三个新列在 SQLite fallback 下可正常新增、读取与更新。

### 前端验证

项目暂无测试框架，不新增测试依赖。实现后执行：

- `bun run typecheck`
- `bun run lint`
- `bun run i18n:sync`
- 手工验证普通用户只读、管理员单笔、管理员当前页批量、部分失败、旧状态保留、暗色模式、移动端和快速翻页。

### 完整验证

- 运行新增后端包测试，再运行 `go test ./...`。
- 此功能不修改 `relaykit/`；若实际实现意外触及 `relaykit/`，补跑 `cd relaykit && GOWORK=off go build ./...`。

## Implementation notes

- 后端实现位于 `controller/topup_platform_status.go`、`model/topup.go` 和 `router/api-router.go`。新接口挂载在既有管理员用户管理路由组，并使用 `UserCriticalRateLimit`。
- `TopUp` 实际对应数据库表 `top_ups`。现有 `AutoMigrate` 会增加三个普通标量列；在项目 MySQL 测试环境中已实际执行迁移并通过持久化读写测试。
- 最终保护参数与设计一致：单批最多 100 单、请求内 6 个 worker、进程级最多 12 个平台查询、单次平台调用 5 秒、整批 20 秒。
- 支付宝查询使用本地 `trade_no` 作为 `out_trade_no`；Infini 查询使用持久化的 `provider_order_id`，并校验响应中的订单号及可用的 `client_reference`。
- 前端实现位于 `web/src/features/wallet/`，普通用户只读取持久化标识，管理员可单笔查询或查询当前页。新增文案已同步到全部七个 locale。
- 测试覆盖参数边界、去重、平台状态映射、支付宝/Infini 成功及错误路径、持久化校验和管理员路由保护。后端相关包测试、前端类型检查及本次变更文件 lint 均通过；全仓校验结果以交付记录为准。
- 2026-08-21 已部署香港测试服务器，版本为 `v1.0.0-rc.23-203-g1756f0b4d-hk-test-20260821`，Linux amd64 二进制 SHA-256 为 `6def22fe756e9e0e413ca6cf71d362f20662a50f54a1b0803b16f49c51c8ff84`，服务器与本地构建一致。既有 `graceful_update.sh` 在 1 秒内完成优雅停机，旧二进制备份到 `/root/backup/20260820_163800/new-api`（服务器使用 UTC 时间）。
- 部署后公网及本机 `/api/status` 均返回 HTTP 200 和新版本；未认证调用平台状态查询接口返回 HTTP 401；MySQL `top_ups` 表已确认存在三个新增字段，启动日志未出现迁移、panic 或 fatal 错误。
