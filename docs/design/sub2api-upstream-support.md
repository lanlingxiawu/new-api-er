# 对接 sub2api 上游：价格巡检与上游日志查询

状态：部分确认，实施中（2026-09-28）

**本期范围（用户 2026-09-28 确认）**：做 G1、G2 与上游日志页对 sub2api 渠道的提示（§6.5）；**sub2api 账号登录（G4）暂不做**，依赖它的逐请求日志查询（§6.2–§6.4）随之推迟，相关章节保留为后续设计。D4 按推荐执行：非 Sub2API 类型渠道在 new-api 接口不可用时自动尝试 sub2api。

**§6.1 与 D2 本期不做**：用户要求转发链路只跟随官方 new-api 主分支。官方 upstream/main 的 `ShouldCopyUpstreamHeader` 只处理 `X-Oneapi-Request-Id`，没有捕获或拦截 `X-Client-Request-ID`，所以本仓库也不改转发链路；本期没有任何转发链路改动（§10–§12 中关于转发链路的内容随之不适用）。

相关文档：`price-monitor-channel-cost-check.md`（成本系数核对）、`channel-upstream-log-query.md`（上游日志查询，§19 起只对接官方 new-api）。

## 1. 背景与结论

价格巡检与上游日志查询目前只对接官方 new-api 的接口。上游是 sub2api（Wei-Shaw/sub2api）时，倍率、价格、逐请求日志都取不到。

以 sub2api 最新 main（0.2.9，2026-09-28）源码核实，能拿到什么：

| 想要的数据 | 只用渠道密钥 | 用 sub2api 用户登录态（JWT） |
|---|---|---|
| 密钥实际生效的倍率 | ✅ `GET /v1/sub2api/billing` | ✅ |
| 密钥属于哪个分组 | ❌（只能按倍率推断） | ✅ `GET /api/v1/keys` 返回 `group_id` |
| 模型价格 | ⚠️ `GET /api/v1/model-plaza`，匿名可访问，但**默认关闭**，且看不到专属分组 | ⚠️ 同一接口，登录后能看到专属分组 |
| 逐请求使用日志（含每条的费用与实际倍率） | ❌ 只有 `GET /v1/usage` 的按天/按模型汇总 | ✅ `GET /api/v1/usage` |
| 按请求 ID 精确查日志 | ❌ | ⚠️ 用户侧没有 request_id 筛选，只能按日期+密钥+模型翻页后本地匹配 |

所以：

- **价格巡检**：倍率只用渠道密钥就能稳定取到。价格要看上游是否开了模型广场，没开就如实显示取不到。
- **上游日志查询**：必须在本站保存一份 sub2api 账号登录态，才能逐请求查询。按本站请求 ID 追溯，还需要在转发时记下 sub2api 返回的请求 ID。

## 2. 目标与范围

### 2.1 目标

1. 成本系数核对支持 sub2api 渠道：取密钥实际生效的倍率，与成本系数对比（G1）。
2. 价格巡检支持 sub2api 渠道的价格来源：从模型广场取该密钥所在分组的价格（G2）。
3. 上游日志查询支持 sub2api 渠道：按本站请求 ID 追溯、按条件浏览，展示每条的费用、实际倍率、token 数（G3）。
4. 为 G3 提供 sub2api 账号登录态的配置与维护（G4）。

### 2.2 不在范围

- sub2api 的失败请求（`/api/v1/usage/errors`）：没有请求 ID 也没有费用，且要上游管理员开关，不接。
- 图片、视频的独立倍率，按时段定价，按推理强度加价：`/v1/sub2api/billing` 不返回这些，核对只针对按 token 计费。
- 带阶梯（`intervals`）的模型价格：本站价格巡检的阶梯表示与 sub2api 不同，V1 不比对这类模型（见 §5.4）。
- 按密钥汇总用量对账（`/v1/usage`）：与逐请求日志是两种视图，另议。
- 本站改为被 sub2api 查询的一方：不涉及。

## 3. 上游接口约定（sub2api 0.2.9）

| 接口 | 鉴权 | 用途 | 限制 |
|---|---|---|---|
| `GET /v1/sub2api/billing` | 渠道密钥（`Authorization: Bearer`） | 倍率：`group_rate_multiplier`、`user_rate_multiplier`（与分组不同时才有）、`resolved_rate_multiplier`、高峰设置、`effective_rate_multiplier` | 简易模式 404；密钥未分配分组 403；旧版 sub2api 没有此接口（404）。不校验余额与有效期 |
| `GET /api/v1/model-plaza` | 可匿名；带 JWT 可看专属分组 | 各分组下各模型的价格（每 token 美元，不含分组倍率） | 默认关闭（404）；可设为必须登录（401）；每 IP 每分钟 300 次 |
| `POST /api/v1/auth/login`、`/auth/login/2fa` | 邮箱+密码（+TOTP） | 取得登录态 | 上游开了人机验证时无法自动登录；每分钟 20 次 |
| `POST /api/v1/auth/refresh` | refresh token | 换新的 access token（24 小时）与 refresh token（30 天） | **每次刷新都作废旧 refresh token，无宽限期** |
| `GET /api/v1/keys` | JWT | 账号下的密钥（完整 `key`、`group_id`） | 用户接口每分钟 240 次 |
| `GET /api/v1/usage` | JWT | 逐请求日志：`request_id`、`model`、token 数、`total_cost`、`actual_cost`、`rate_multiplier`、`duration_ms`、`first_token_ms`、`created_at` | 筛选只有 `api_key_id`、`model`、按天的 `start_date`/`end_date`+`timezone` 等，**没有 request_id**；`page_size` ≤ 1000；`/usage*` 每分钟 60 次 |

请求 ID：sub2api 对每个 `/v1` 请求生成 UUID，放在响应头 `X-Client-Request-ID`（流式、错误响应都有），日志里存为 `request_id = "client:" + <该值>`。它不采用客户端传入的请求 ID。

上游开启"后端模式"时，非管理员的所有用户接口与模型广场都返回 403，G2/G3 不可用，G1 不受影响。

## 4. 价格巡检：倍率（G1）

### 4.1 取数顺序

在现有顺序（官方 new-api 接口 → 上游日志补充）中插入 sub2api：

```text
渠道类型是 Sub2API，或上一轮已识别为 sub2api：
    只走 sub2api billing
其他渠道：
    1. 配了上游账号令牌：官方 new-api 接口（不变）
    2. 官方接口不可用或没配：sub2api billing（按渠道密钥，1 次请求/密钥）
    3. 仍取不到：new-api 日志补充（不变，受每主机配额约束）
```

- 识别结果记在快照的核对行里（`upstream_kind`: `new-api` / `sub2api`），下一轮直接走对应路径，不再白打另一种接口。
  - 失败响应也能识别：sub2api 自己的错误结构（`{"type":"error","error":{...}}`，例如密钥未分配分组的 403）确认上游是 sub2api，以它的原因为准，不再往 new-api 日志补。别的网关对未知路径回的 403/404 不算。
  - 记下的类型失效时清掉、下一轮重新识别：记成 sub2api 却回 `sub2api_unsupported`，或记成 new-api 却回 `not_supported`（换了网关或改了渠道地址）。
- 多密钥渠道逐把查未禁用的密钥（最多 5 把，与现有一致），取倍率最高的；被拒（401）或未分配分组（403）的密钥跳过，全部如此才算失败。
- billing 接口不占每主机日志配额，但有自己的保护：sub2api 在同一 IP 60 秒内出现 120 次无效密钥时会封禁该 IP 60 秒（连同我们的转发流量）。本进程在任意 60 秒内发往同一主机、被拒的密钥不超过 20 把；收到 429 后 60 秒内不再向该主机发送密钥。限额是进程级的滑动窗口（`priceMonitorSub2APIKeyLimiter`，单调时钟计时），跨巡检轮次与手动"同步上游倍率"累计，连点几次手动同步也不会越过。价格步骤（§5，模型广场取价时也用密钥查 billing）与手动同步最多用 15 把，剩下 5 把留给倍率步骤（`priceMonitorSub2APIRatioReserve`）；同一轮里两步共用一次取数（`priceMonitorSub2APIRun`），价格步骤问过的密钥，倍率步骤直接用它的答复，不再发第二次。可复用的答复只有成功与只取决于密钥本身的失败（401、403、404/405、非 sub2api 响应）；超时、5xx、429 这类暂时性失败倍率步骤会重问。名额用完时剩下的渠道记"排队中"、不计为已尝试，下一轮按上次尝试最早优先。发送前先占名额（窗口内被拒数 + 在途数 < 可用上限），并发取价时被拒数也不会越过上限。（留出倍率步骤名额的原因：一个主机上有 20 把以上本地启用、上游已作废的密钥时，价格步骤每轮先把名额用完，没被价格步骤问过的渠道在倍率步骤里会永远"排队中"。）
  - 等名额受调用方超时约束：名额被在途请求占满时等它们结束，调用方的 ctx 到期就放弃、不发送（等待者占着取价的并发槽，不能等过自己的超时），按"不可用"（暂时性失败）处理。
  - 一次取数内同一把密钥（同一地址 + 密钥）并发只发一次：多个渠道共用地址与密钥、或价格步骤与倍率步骤同时问时，先到的去问，其余等它的答复（等待同样受各自超时约束）。答复不可复用（暂时性失败，或名额不够没问成）时等待者重新判断、必要时自己去问。被拒的密钥因此只记一次。
  - **限额的边界**：限额只在本进程内、按渠道地址的主机名计。巡检只在主节点运行；但手动"同步上游倍率"可以在任意节点发起，各节点各自计数——N 个节点经同一出口 IP 时，同一 60 秒内最多可能发出 N × 20 把被拒密钥。同一个 sub2api 用两个主机名（或 IP 与域名）接入时，两个主机名各有一份额度。sub2api 按来源 IP 封禁，因此多节点部署时应避免在多个节点上短时间内反复手动同步有大量失效密钥的 sub2api 渠道，同一上游尽量只用一个地址接入。没有把手动同步限定到主节点：它还负责所有非 sub2api 来源的取价，限定后管理员请求落到从节点就整个不可用，代价大于这个边界情形。
- 快照升级（版本 21）后第一轮，所有渠道视为到期、立即重取一次倍率（`priceMonitorCostsDueNow`），上一轮因"不是 new-api"取不到的 sub2api 渠道不必等 6 小时；日志查询仍受每主机配额约束，不会突增。

### 4.2 倍率取值

- 取 `resolved_rate_multiplier`（已含给本账号单设的倍率）。
- 分组开了高峰倍率（`peak_rate_enabled`）时，取 `resolved × peak_rate_multiplier` 与 `resolved` 中较大的，按最坏情况估成本；核对列表显示高峰时段与倍率。
- 高峰倍率低于 1（高峰打折）时不影响最坏情况，核对列表仍显示高峰时段与倍率。
- 来源标记 `ratio_source = sub2api`。403（未分配分组）、404（简易模式或旧版）各有原因枚举：`sub2api_no_group`、`sub2api_unsupported`。

## 5. 价格巡检：价格来源（G2）

### 5.1 端点

新增特殊端点 `sub2api`（与现有 `openrouter` 同类）：请求 `{渠道地址}/api/v1/model-plaza`，匿名（登录态本期不做）。

- 渠道类型是 Sub2API：默认就用它。
- 其他类型：追加为自动探测的第三个候选（`/api/pricing` → `/api/ratio_config` → `sub2api`）。探测发现上游不是 sub2api 时（广场请求失败、非 sub2api 结构的 404、响应无法识别），失败带 `sub2apiNotDetectedPrefix` 标记，**不覆盖**前面候选的端点与失败原因，页面照旧显示 new-api 端点的失败。
- 人工指定端点的规则不变（指定了就只试它）。
- **地址与密钥只取自数据库里的渠道**：这个端点也能从"同步上游倍率"弹窗调用，那里的上游地址由前端提交；请求地址与渠道地址不一致就拒绝，不发出任何请求，绝不把渠道密钥发往别的地址。
- 先匿名取广场，广场没开或不是 sub2api 时不发送渠道密钥；广场可用后才用密钥查倍率接口（逐把试未禁用的密钥，跳过被拒与未分配分组的）。发送密钥受 §4 的每主机被拒密钥限额约束（取价最多用到 15 把，与倍率步骤、手动同步同在一个 60 秒窗口）；额度用完时该渠道价格来源本轮失败，下一轮再取。手动"同步上游倍率"每次调用新开一次取数（密钥答复只在本次内复用），但限额与巡检共用同一个进程级窗口。

### 5.2 选哪个分组的价格

广场按分组列出价格，同一模型在不同分组可能价格不同（渠道覆盖价）。需要知道密钥属于哪个分组：

本期没有登录态，用倍率接口返回的 `group_rate_multiplier` 与高峰设置在广场的分组里匹配（有登录态后可改用 `GET /api/v1/keys` 的 `group_id` 精确定位）：

- 恰好一个分组匹配：用它。
- 多个匹配：每个模型每个价格字段取候选分组里的最高价（按最坏情况）。
- 没有匹配（多半是专属分组，匿名看不到）：来源失败，原因 `sub2api_group_unknown`。

### 5.3 换算

广场价格单位是每 token 美元，换算成本站价格表结构（与 OpenRouter 换算一致，`ratio = 每 token 价 × 1000 × ratio_setting.USD`）：

| sub2api | 本站 |
|---|---|
| `input_price` | `model_ratio` |
| `output_price / input_price` | `completion_ratio` |
| `cache_read_price / input_price` | `cache_ratio` |
| `cache_write_price / input_price` | `create_cache_ratio` |
| `per_request_price`（`billing_mode` 为按次） | `model_price`（美元/次） |

广场价格**不含分组倍率**，正好对应本站"上游列表价"的含义；实测成本 = 列表价 × §4 的倍率，不会重复乘。

### 5.4 不比对的情形

- 分组开了长上下文阶梯（`long_context_pricing_enabled`）且模型带 `intervals`：跳过该模型（任一候选分组如此即跳过）。分组没开阶梯时只按首档价计费，广场顶层价格就是首档价，照常比对。
- 按时段定价（`time_pricing`）：只用基础价，不计入时段倍率（本期页面不单独注明）。
- 图片、视频按张计价：跳过。
- 广场关闭（404）：来源失败，原因 `sub2api_plaza_disabled`，提示"上游未开放模型广场，拿不到价格"。

## 6. 上游日志查询（G3）

### 6.1 记录 sub2api 的请求 ID（改转发链路）

转发时若上游响应没有 `X-Oneapi-Request-Id`，改读 `X-Client-Request-ID`，写入现有的 `UpstreamRequestIdKey`，随使用日志落到 `upstream_request_id` 列。

- 只多一次响应头读取，不改变转发流程（§10）。
- 改动前产生的 sub2api 日志没有上游请求 ID，只能按条件浏览，不能按本站请求 ID 追溯。

**待确认（D2）**：现在 `X-Client-Request-ID` 会原样透传给我们的客户端，暴露了上游是 sub2api 以及上游的请求 ID。建议与 `X-Oneapi-Request-Id` 一样不透传。

### 6.2 按本站请求 ID 追溯

```text
本站日志：channel_id、upstream_request_id（sub2api 的 UUID）、created_at、上游模型名、多密钥序号
  -> 渠道有 sub2api 登录态
  -> GET /api/v1/keys，按完整密钥找到本渠道这把密钥的 api_key_id（结果缓存 10 分钟）
  -> GET /api/v1/usage?api_key_id=&model=&start_date=D&end_date=D&timezone=UTC
       &page_size=1000&sort_by=created_at&sort_order=<见下>
     D 为请求发生的 UTC 日期
  -> 逐页在本地匹配 request_id == "client:" + upstream_request_id
```

- 用户接口没有 request_id 筛选，只能翻页。按请求在当天的位置选排序方向（上半天升序、下半天降序），命中即停。翻过请求时间 ±10 分钟仍未命中就停止。
- 最多 5 页（5000 条）。超出时如实提示"该密钥当天请求过多，没有在前 5000 条里找到"，不再继续打上游（上游 `/usage*` 每分钟 60 次）。
- 本站日志的上游模型名与 sub2api 记录的 `model`（请求模型）一致时带上 `model` 筛选；取不到上游模型名就不带。
- 多密钥：用日志记录的序号；没记录时逐把试，命中即停（沿用现有规则）。

### 6.3 按条件浏览

- 筛选映射：时间范围 → 按 UTC 日期下发，再在本地按时间戳精确过滤；模型 → `model`；令牌 → 该令牌的 `api_key_id`；其余本站筛选项 sub2api 不支持，页面置灰。
- 分页由上游完成，本站每页 ≤ 100 条。上游的总数是估算值，页面显示"至少 N 条"。

### 6.4 结果展示

映射到现有 `UpstreamLogItem`，复用上游日志页与详情排版：

| sub2api | 本站字段 |
|---|---|
| `created_at`（RFC3339） | `created_at`（Unix 秒） |
| `request_id`（去掉 `client:` 前缀） | `request_id` |
| `model` | `model_name` |
| `input_tokens` / `output_tokens` | `prompt_tokens` / `completion_tokens` |
| `actual_cost`（美元） | `quota`（× 本站每美元额度，页面按本站格式显示金额） |
| `duration_ms` | `use_time`（秒） |
| `stream` | `is_stream` |
| `first_token_ms`、`total_cost`、`rate_multiplier`、缓存 token 与各项费用、`billing_type` | `other` 白名单新增 `sub2api_*` 键；详情里显示"上游原价 / 实际倍率 / 实际扣费" |

`ip_address`、`user_agent`、`session_id` 不下发。

### 6.5 本期：上游日志页的 sub2api 提示

不做登录态时，sub2api 渠道查不了逐请求日志。Sub2API 类型的渠道照常查询（渠道类型不作为闸门，有人会把 new-api 网关配成别的类型），上游确实没有 new-api 日志接口（404）时提示：sub2api 的逐请求日志需要登录其账号，本站暂不支持，请到 sub2api 自己的后台查看。

## 7. sub2api 账号登录态（G4，本期不做）

### 7.1 为什么不能从浏览器复制令牌

sub2api 的 refresh token 每用一次就作废。从浏览器复制出来后，浏览器自己一刷新，复制的就失效了。所以由本站自己登录，拿一份独立的登录态。

### 7.2 配置方式

在渠道编辑抽屉的"上游账号"区域（仅 Sub2API 类型渠道，或上一轮已识别为 sub2api 的渠道显示）：

- 输入 sub2api 的邮箱、密码，开了两步验证的再输入 6 位验证码，点"登录"。
- 本站后端调用上游登录，**只保存返回的 refresh/access token，不保存密码**。
- 上游开了人机验证时登录会失败。提示改用"粘贴 refresh token"：管理员在浏览器登录 sub2api 后，从本地存储复制 `refresh_token`，**然后直接关闭该页面，不要再用这个浏览器会话**，否则会被浏览器刷新作废。

页面只显示状态：已配置 / 最近刷新时间 / 过期时间 / 最近一次错误。任何接口都不返回令牌本身。提供"退出登录"（删除本站保存的登录态）。

### 7.3 令牌维护

- access token 有效期内直接用；距过期不足 1 小时时刷新。
- 刷新会作废旧 refresh token，必须串行且结果必须落库，否则登录态永久失效：
  - 进程内按渠道加互斥锁；
  - 写库用条件更新：`UPDATE ... SET refresh_token=新, access_token=新 WHERE channel_id=? AND refresh_token=旧`。多节点时抢输的一方重读库里的新令牌，不再刷新。
  - 刷新成功、写库失败（极少见）时登录态可能丢失，记错误日志并在页面提示重新登录。
- 30 天没有任何查询时 refresh token 会过期，页面提示重新登录。不做定时保活，避免为不用的渠道持续访问上游。
- 登录态绑定登录时的上游地址；渠道地址变了就视为失效。
- 删除渠道时一并删除登录态。

## 8. API 契约

均在管理员路由组，沿用现有权限：

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/api/log/upstream/query` | 不变 | 请求体不变；响应 `query` 增加 `provider`（`new-api` / `sub2api`），sub2api 追溯另有 `scanned`（翻过的条数） |
| GET | `/api/channel/:id/upstream_session` | 渠道查看 | `{configured, provider, base_url_matches, refreshed_at, access_expires_at, last_error}` |
| POST | `/api/channel/:id/upstream_session/login` | 渠道编辑 | `{email, password, totp_code?}`；成功返回同上状态；密码不落库、不写日志 |
| POST | `/api/channel/:id/upstream_session` | 渠道编辑 | `{refresh_token}`，粘贴方式；先刷新一次验证可用再保存 |
| DELETE | `/api/channel/:id/upstream_session` | 渠道编辑 | 删除登录态 |

错误一律用 `ApiErrorI18n`，新增键：`upstream_session.login_failed`、`.captcha_required`、`.totp_required`、`.totp_invalid`、`.backend_mode`、`.expired`、`.not_configured`、`upstream_log.sub2api_scan_limit`、`upstream_log.sub2api_not_found` 等，en/zh 两份。上游原始错误只进服务日志。

## 9. 数据模型

新表 `channel_upstream_sessions`（与 `channels` 分开，刷新令牌的频繁写入不碰渠道表与渠道缓存）：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | 主键 | GORM 生成 |
| `channel_id` | int，唯一索引 | |
| `provider` | varchar(32) | 目前只有 `sub2api` |
| `base_url` | varchar(512) | 登录时的上游地址 |
| `refresh_token` | text | |
| `access_token` | text | |
| `access_expires_at`、`refresh_expires_at`、`updated_at` | bigint | Unix 秒 |
| `last_error` | varchar(255) | 已脱敏的错误摘要 |

- GORM AutoMigrate，三种数据库兼容；表很小（每渠道一行），不需要额外索引。
- 令牌与渠道密钥同等对待（明文存储、仅管理员接口可触达、永不下发）。
- 快照：核对行增加 `upstream_kind`、`group_rate_multiplier`、高峰信息；来源状态增加 `sub2api_*` 失败原因。快照版本 20 → 21。

## 10. Main Chain Impact

- **同步在转发 goroutine 上**：仅 §6.1 的一次 `resp.Header.Get("X-Client-Request-ID")`，在已有的 `X-Oneapi-Request-Id` 读取旁边，只在前者为空时执行。无锁、无 IO，分配一个短字符串。D2 若选择不透传，再多一次响应头名比较。
- **异步/非转发**：倍率、价格、日志、登录态全部在巡检任务或管理员请求里执行，不在转发链路上。
- 不新增开关：只读一个响应头，不改变转发的任何分支与返回（按"修复/极小改动不加开关"的约定；如需开关可加在 D2 的透传行为上）。

## 11. Shared Resource Audit

| 资源 | 本功能 | 转发链路是否访问 |
|---|---|---|
| 响应头读取 | §6.1 | 是，只读 |
| `logs.upstream_request_id` | 写入值多了 sub2api 来源；追溯时按 `idx_logs_request_id` 读一条 | 是，列已存在，写入量不变 |
| `channel_upstream_sessions` | 新表，读写 | 否 |
| `channels` | 读（地址、密钥、设置） | 是，只读，沿用现有查询 |
| HTTP 客户端 | 巡检沿用现有专用客户端；日志查询沿用上游日志专用客户端 | 否，与转发的连接池隔离 |
| Redis / 内存缓存 | 进程内 `api_key_id` 缓存（按渠道+密钥，10 分钟）；进程内刷新互斥锁 | 否 |

## 12. Concurrency Analysis（100k RPM）

> 本期无转发链路改动，下文转发链路一条不适用。巡检里广场按渠道各取一次（不是每主机共享一次），渠道数有限且每 6 小时一轮，未做合并。

- 转发链路：每请求 0 次 DB、0 次 Redis、0 把锁；多 1 次响应头查找（纳秒级）。
- 巡检：每渠道每 6 小时 ≤ 5 次 billing 请求 + 每主机 1 次广场请求（结果本轮内共享），与请求量无关。
- 日志查询：由管理员手动触发；单次追溯最多 1 次 keys + 5 次 usage + 可能 1 次刷新，受上游每分钟 60 次限制，页面对连续查询不做自动重试。

## 13. 错误处理与降级

| 情况 | 表现 |
|---|---|
| billing 404 / 403 | 核对行显示"上游不支持"或"密钥未分配分组"；继续 new-api 日志补充 |
| 广场关闭 / 找不到分组 | 该渠道价格来源失败，注明原因，不影响其他来源 |
| 未配置登录态 | 上游日志页提示"为该渠道登录 sub2api 账号后可查询逐请求日志" |
| 登录态失效（被作废、过期、改密码） | 提示重新登录；标记 `last_error`，不反复重试 |
| 上游后端模式 | 提示"上游关闭了用户接口，无法查询" |
| 上游 429 | 提示稍后再试，立即停止后续翻页 |
| 翻页达到上限 | 提示扫描范围，建议按条件浏览 |

## 14. 测试计划

- `service`：sub2api 日志响应解析与字段映射；翻页匹配（命中、跨页、达到上限、时间越界停止、排序方向选择）；429/401/403/404 映射；凭证脱敏。
- `model`：登录态表 CRUD；条件更新（抢输的一方不覆盖）；删除渠道级联。真实 MySQL + PostgreSQL，按行清理。
- `controller`：
  - billing 取值：高峰取大、user 倍率、404/403、多密钥取最高、识别结果记忆与下一轮直走；
  - 广场换算：各字段、按次、跳过阶梯与图片、分组推断（唯一/多个取最高/无匹配）、广场关闭；
  - 登录接口：2FA 两段式、人机验证失败、密码不落库不入日志；粘贴方式先刷新验证；
  - 刷新并发：两个并发查询只刷新一次；
  - 密钥限额与一次取数：并发同一密钥只发一次、被拒只记一次、暂时性失败由等待者重问、等名额与等答复都随 ctx 到期放弃且不发送（`price_monitor_sub2api_run_test.go`）；
  - 上游日志查询按 provider 分派，new-api 路径行为不变（既有测试全部保留）。
- `relay` / `service/http`：`X-Client-Request-ID` 捕获（有 `X-Oneapi-Request-Id` 时不覆盖）；D2 的透传行为。
- 上游一律用 `httptest.NewServer` 模拟 sub2api，不访问真实上游。
- 前端：`bun run typecheck`、`lint`、`oxfmt`；7 种语言文案补齐。

## 15. 实现结果（本期）

- `controller/price_monitor_sub2api.go`：倍率接口（`fetchSub2APIKeyBilling`、进程级每主机被拒密钥限额 `priceMonitorSub2APIKeyLimiter`、一次取数内复用密钥答复的 `priceMonitorSub2APIRun`、倍率步骤查询器 `priceMonitorSub2APIProber`）、模型广场取价与换算（`fetchSub2APIPlazaPricing`、`convertSub2APIPlazaToRatioData`）。
- `controller/price_monitor_channel_cost.go`：取数顺序（`resolvePriceMonitorChannelRatio` 带上一轮识别的 `kind`）、核对行新增 `upstream_kind` / `peak_multiplier` / `peak_window`、类型记忆与失效。
- `controller/price_monitor_source.go`、`ratio_sync.go`：候选端点与 sub2api 分支，失败枚举 `sub2api_plaza_disabled` / `sub2api_group_unknown`。
- `controller/price_monitor_task.go`：快照升级后所有渠道立即重取；快照版本 21。
- `controller/upstream_log.go`：Sub2API 渠道照常查询（类型不作为闸门，见 channel-upstream-log-query.md §2.2），上游确实没有 new-api 日志接口时给出 sub2api 专属提示（`upstream_log.sub2api_unsupported`）。
- 前端：核对列表的来源、原因、高峰说明；价格来源的两种失败文案；分享页文案。7 种语言补齐。
- 同类问题一并修复（用户 2026-09-28 要求）：原有 OpenRouter 取价分支会把渠道密钥发往"同步上游倍率"弹窗提交的任意地址，且用 `GetNextEnabledKey` 取密钥（会推进转发的多密钥轮询）。现在 OpenRouter 与 sub2api 共用 `loadPricingSourceChannel`（`controller/ratio_sync.go`）：从数据库读渠道，请求地址与渠道地址不一致就拒绝、不发任何请求；取第一把未禁用的密钥，不碰轮询状态。两条合法调用路径（价格巡检、弹窗）传的地址本来就是 `GetBaseURL()`，行为不变。测试：`controller/ratio_sync_openrouter_key_test.go`。

## 16. 需要确认的决定（已确认）

- **D1 范围**：G1–G4 全做（推荐）；或先做 G1+G2（只用密钥，不存账号登录态），G3+G4 另行。
- **D2 透传**：`X-Client-Request-ID` 不再透传给我们的客户端（推荐），还是保持现状。
- **D3 登录方式**：本站代为登录（推荐，密码不保存）+ 粘贴 refresh token 兜底；还是只做其中一种。
- **D4 其他类型渠道的自动识别**：非 Sub2API 类型的渠道在官方 new-api 接口不可用时自动尝试 sub2api 接口（推荐，每渠道每 6 小时多 1 次请求）；还是只认 Sub2API 类型。
