# 价格巡检：来源有效性与覆盖率守卫

状态：设计待确认。

## 1. 目标与范围

解决"价格来源看起来成功、实际上一个真实价格都没拿到，于是整列没有参与对比"的问题。当前链路只检查传输层和解析层错误，四种情况会被漏掉：

1. 上游返回 HTTP 200 且能解析，但条目为空（`{"success":true,"data":[]}`、或全部条目 `model_name` 为空），`converted` 是空 map，仍计入成功来源（`controller/ratio_sync.go:522`）。矩阵里该渠道每个模型都被标成 `missing` 并**计为价格差异**，接口故障被伪装成价格不一致。
2. OpenRouter 转换器（`controller/ratio_sync.go:810`）没有零条守卫，行为同上；只有 models.dev 转换器有。
3. 定时巡检对所有非 OpenRouter 渠道硬编码 `/api/pricing`（`controller/price_monitor_task.go:144`）。只暴露 `/api/ratio_config` 的上游每次都失败，没有任何回退尝试。
4. 渠道模型字段为空时 `sourceModels[name]` 是非 nil 空 map，`controller/price_monitor.go:887` 的 `applicableModels` 过滤会把该渠道所有模型跳过：表头在、一个格子都没有，页面按"该渠道未启用此模型，不参与对比"显示，看不出这个渠道整列零对比。

另外补一个可观测性缺口：`ComparisonModelCounts` 统计的是"存在差异的模型数"，没有"该来源返回了几个模型价格、其中几个和平台模型同名"。来源模型名整体对不上（例如上游带厂商前缀）时，表现为全列 `missing`，分不清是**名字对不上**还是**真的没价**。

**不在范围内：**

- 不改动渠道编辑功能，不新增渠道字段，不写 `channels` 表。
- 不改手动"同步上游倍率"弹窗的端点选择行为（`web/src/features/system-settings/models/upstream-ratio-sync.tsx`），那是一次性请求，选择仍不落库。巡检的端点配置独立存放在价格巡检设置里。
- 不做模型别名/相似名自动匹配，不自动改价。

## 2. 数据流

```text
价格巡检定时任务（仅主节点）
  -> 读取快照文件里上次命中的端点记忆 source_endpoints
  -> 第 1 轮 fetchUpstreamPricingSnapshotData(候选端点 = 记忆值 或 /api/pricing；OpenRouter 类型固定 /v1/models)
  -> 对第 1 轮失败的渠道发起第 2 轮，端点 = /api/ratio_config
  -> 合并两轮结果（同名来源以成功结果为准），更新端点记忆
  -> 为每个来源计算 status / fetched_models / matched_models
  -> 构建矩阵（失败、空结果、无交集、无模型的来源不产生差异）
  -> 写入快照 JSON（含端点记忆与来源状态）
  -> 管理页 / 分享页按来源状态展示原因与覆盖率
```

端点探测和覆盖率计算全部在后台巡检任务内完成，管理接口只读快照。

## 3. 关键业务逻辑

### 3.1 空结果判为失败（修复点 1、2）

`fetchUpstreamPricingSnapshotData` 在把结果投递给 channel 之前增加统一守卫：转换结果中 `model_ratio` 与 `model_price` 均为空（或整个 `converted` 为空）时，按失败返回，错误信息固定为 `empty pricing payload`。type1（ratio_config）、type2（pricing）、OpenRouter 三条路径共用该守卫；models.dev 已有的 `no valid models.dev pricing entries found` 保持不变。

此改动同时作用于手动同步接口 `FetchUpstreamRatios`——原本它也会把空来源当成功来源参与 `buildDifferences`，修复后会在测试结果里显示为错误，属于同一个缺陷的修复，不是行为倒退。

### 3.2 端点探测与记忆（修复点 3）

巡检取价与手动"同步上游倍率"走同一套来源和端点口径：来源就是弹窗里的可同步渠道（启用且 BaseURL 为 http 开头）加官方预设、models.dev 预设，端点就是弹窗里的那几个（`web/src/features/system-settings/models/constants.ts` 的 `ENDPOINT_OPTIONS`、`OPENROUTER_ENDPOINT` 以及 custom 自定义地址）。差别只在于巡检没有人现场来选，所以：**配了就用配的，没配就自动按顺序试。**

端点优先级（每个渠道逐个确定）：

1. **人工指定**：价格巡检设置里为该渠道配置的端点，取值范围与同步弹窗一致（`/api/pricing`、`/api/ratio_config`、custom 完整 URL）。
2. **上次探测命中的端点**（仅当没有人工指定时）。
3. `/api/pricing`。
4. `/api/ratio_config`。

规则细节：

- 人工指定的端点**不做回退**。配了却取不到价格就是 `failed`，表头显示实际使用的端点，让管理员知道是自己配的地址有问题，而不是被系统悄悄换成别的端点后掩盖掉。
- `ChannelTypeOpenRouter` 类型渠道固定走 `openrouter` 分支（`/v1/models` + 渠道密钥），不参与探测，也不接受人工指定（它的取价方式与其它端点不同构）。
- 官方预设与 models.dev 预设端点固定，不参与探测，也不出现在人工配置列表里。
- 自动探测的第 2 轮只对第 1 轮失败的渠道发起，按 `maxConcurrentFetches` 并发；单轮超时沿用 `TimeoutSeconds`。
- 合并规则：来源名相同时成功结果覆盖失败结果；两轮都失败时保留**最后一次**的错误信息，并记录实际尝试过的端点，便于定位。

不改 `fetchUpstreamPricingSnapshotData` 的签名与单次请求语义，上述编排全部在 `price_monitor_task.go` 完成。

**两类端点数据分开存放：**

- **人工指定**是用户配置，按 Rule 12 存进 `price_monitor_setting`（DB 持久化、多节点一致、可随时改）。
- **探测命中**是派生缓存，存在价格巡检自己的快照文件里（`source_endpoints`，key 为渠道 ID），丢失或损坏时退化为重新探测，不影响正确性。

两者都不写 `channels` 表（Rule 0 共享资源检查）：`channels` 是中继主链路读取的热表，写它会触发渠道缓存失效并与中继查询争锁。渠道被删除或 BaseURL 变更时，探测记忆在下次巡检自然失效；人工配置里已不存在的渠道 ID 在巡检时忽略，并在设置页标灰提示可删除。

稳态成本：每渠道每轮巡检 1 次 HTTP 请求（默认间隔 360 分钟）；仅在端点变化或上游故障时退化为 2 次。

### 3.2.1 custom 地址校验

保存价格巡检设置时校验每条自定义地址，不合法直接拒绝保存（`ApiErrorI18n`，不回显原始 Go 错误）：

- 必须是 `http://` 或 `https://` 开头的绝对地址，能被 `url.Parse` 解析且 Host 非空；
- 去掉 URL 里的 userinfo 后再存，避免把凭据写进配置和页面；
- 单条长度上限 512 字符，条目数上限 200；
- key 必须是存在的渠道 ID。

自定义地址由管理员在受 `AdminAuth()` 保护的设置页填写，与现有手动同步接口能请求任意 URL 的权限面一致，不扩大风险面。

### 3.3 来源状态与覆盖率（修复点 4 + 可观测性）

每个来源在快照中带一个明确状态：

| status | 含义 | 是否产生对比格子 |
|---|---|---|
| `ok` | 取到价格且与平台模型有同名交集 | 是 |
| `failed` | 抓取或解析失败（含空结果） | 否，整列 `source_failed` |
| `no_overlap` | 取到 N 个模型价格，但与该来源应比较的模型无同名交集 | 否 |
| `no_models` | 渠道未配置任何模型，无可比较范围 | 否 |

- `fetched_models`：来源返回数据中出现过价格的去重模型名数量（`model_ratio` ∪ `model_price` ∪ 阶梯表达式）。
- `matched_models`：`fetched_models` 与"该来源应比较的模型集合"的交集大小。渠道来源的应比较集合 = 平台模型白名单结果 ∩ 渠道启用模型；官方与 models.dev 来源 = 平台模型白名单结果。
- `no_overlap` 与 `no_models` 不写入 `missing` 差异，避免把"接口/配置问题"计成价格不一致；其对应的模型格子不生成，整列由表头状态解释。

顶层 `SourceOK` 只统计 `status == ok` 的来源，`Status` 仍按 `sourceOK < len(upstreams)` 判定 `partial`。全部来源非 `ok` 时沿用现有逻辑：保留旧快照、记录运行时错误、不覆盖。

## 4. API 契约

不新增端点，不改鉴权。扩展现有接口返回的 `source_headers` 元素（`GET /api/price_monitor/results`、`/status`、分享页 `public_query`）：

```json
{
  "key": "渠道名(12)",
  "name": "渠道名",
  "type": "channel",
  "api_url": "https://example.com",
  "status": "no_overlap",
  "status_message": "来源返回 128 个模型价格，与平台模型没有同名交集",
  "fetched_models": 128,
  "matched_models": 0,
  "endpoint": "/api/ratio_config"
}
```

`status` 缺省视为 `ok`，旧快照兼容。`endpoint` 只回显路径，不含主机与查询串（沿用 `priceMonitorDisplayURL` 的清洗口径，避免泄露凭据）。

矩阵筛选 `comparison` 现有取值不变；`source_failed` 仍按单元格筛选。`no_overlap` 与 `no_models` 的来源在矩阵里没有任何单元格，做成行筛选永远是空结果，因此改为在管理页表格上方用一条汇总提示列出（数据取自 `available_source_headers`）。

自定义端点的读写走现有管理员设置接口（`price_monitor_setting` 的保存与读取），不新增保存端点，鉴权沿用 `AdminAuth()` + `AdminMenuPriceMonitorView` 现有口径。设置页需要的渠道列表复用已有的 `GET /api/ratio_sync/channels`（`GetSyncableChannels`），不新增接口。

## 5. 数据模型变更

无数据库表或列变更，无迁移。改动集中在两处结构：

快照 JSON（本地文件，主节点）：

- `PriceMonitorSourceHeader` 新增 `Status`、`StatusMessage`、`FetchedModels`、`MatchedModels`、`Endpoint`。
- `PriceMonitorSnapshot` 新增 `SourceEndpoints map[string]string`（探测记忆，key 为渠道 ID）。
- `priceMonitorMatrixVersion` 17 → 18，部署后主节点自动重新巡检，旧快照不再用于展示。

配置（走 ConfigManager，落 `options` 表的 `price_monitor_setting.custom_endpoints` 一行）：

- `PriceMonitorSetting` 新增 `CustomEndpoints map[string]string`。ConfigManager 对 map 字段原生做 JSON 序列化（`setting/config/config.go:188` / `:297`），不需要手写字符串编解码。

## 6. 配置参数

新增一项，沿用现有设置接口保存，不加自定义保存端点：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `price_monitor_setting.custom_endpoints` | `map[string]string` | 空 | 渠道 ID → 价格接口端点（`/api/pricing`、`/api/ratio_config` 或完整 URL）。不配的渠道自动探测。 |

其余沿用 `TimeoutSeconds`、`IntervalMinutes`、`IncludeOfficial`、`IncludeModelsDev`、`ModelWhitelist`。`Normalized()` 里顺带丢弃不合法条目，保证运行期拿到的都是可用值。

## 7. 前端展示

- 管理页 `web/src/features/system-settings/models/price-monitor-panel.tsx`：
  - 来源表头在名称下方追加一行覆盖率（`已比对 M / N 个模型`）；`failed`、`no_overlap`、`no_models` 用次要文字标注原因，后两者不使用错误色（属于配置问题而非价格差异）。
  - 巡检设置区新增"价格接口"配置：复用同步弹窗那套交互——用现有 `channel-selector-dialog.tsx` 选渠道、用 `ENDPOINT_OPTIONS` 同款下拉选端点、选 custom 时展开地址输入框，配好的条目以"渠道名 → 端点"列表展示，可改可删。按钮区结构照搬同一页面已有的按钮组，不新增标题段落。
  - 未配置的渠道在列表里不出现；说明文字一句话讲清"不配置即自动探测 pricing / ratio_config"。
  - 保存失败时提示的是"这条地址格式不对，需要以 http:// 或 https:// 开头的完整地址"这类可操作文案，不回显后端错误串。
- 分享页 `controller/price_monitor_page.go` 内嵌页面同步这三种状态文案，保持与管理页一致。
- 文案面向使用者而非实现：
  - `failed` → `价格来源检查失败，请等待下次巡检`（沿用）
  - 空结果 → `该来源返回的价格数据为空`
  - `no_overlap` → `该来源的模型名与平台模型不一致，无法对比`
  - `no_models` → `该渠道未配置模型，没有可对比的范围`

## 8. 错误处理策略

- 探测第 2 轮失败不额外告警，最终状态由来源状态表达；错误详情通过 `common.SysError` / `logger.LogWarn` 记录，不返回给客户端原始 Go 错误串。
- 端点记忆读写失败（快照文件损坏）按"无记忆"处理，退化为从 `/api/pricing` 开始探测，不中断巡检。
- 巡检任务整体沿用现有 `recover()` 保护与 `setPriceMonitorRuntimeError`。

## 9. 与现有子系统的交互

- **AI 中继主链路：不涉及。** 巡检是主节点后台定时任务，不在中继 goroutine 上执行，不新增中继路径代码。
- **共享资源审计：** 只读 `model.GetAllChannels`（与现状一致，不新增读取频率）与 `model.GetPricing()`；新增的端点记忆写入价格巡检独有的本地快照文件，不碰 Redis、不写 `channels`/`tokens`/`logs` 等中继表、不新增连接池或 goroutine 池。HTTP 并发仍受 `maxConcurrentFetches` 限制。
- **手动同步上游倍率：** 共用 `fetchUpstreamPricingSnapshotData`，只受 3.1 的空结果守卫影响（空来源从"成功"变为"错误"）。
- **亏损判定 / 保本下限 / 行内改价：** 读取的是矩阵格子，`no_overlap`、`no_models` 来源不产生格子，这些功能自然不受影响。

## 10. 测试计划（先写用例后实现）

`controller/` 包内，沿用现有 `httptest.NewServer` + testify 模式，不访问真实上游：

- 空结果守卫：`{"success":true,"data":[]}`、条目 `model_name` 全空、OpenRouter `data:[]`、OpenRouter 全部条目价格不可解析 → 均判为失败且不计入成功来源。
- 边界：只有 `model_price` 无 `model_ratio` → 仍判为成功；只有阶梯表达式 `billing_expr` → 仍判为成功（表达式本身就是价格）；只有 `completion_ratio` 或 `cache_ratio` 这类相对倍率 → 判为失败。
- 端点探测：`/api/pricing` 404 而 `/api/ratio_config` 正常 → 第 2 轮命中并写入端点记忆；记忆存在时只发 1 次请求；记忆端点失效时重新探测；OpenRouter 渠道不触发探测。
- 人工指定优先级：配了 custom 地址时只请求该地址、不回退，失败即 `failed` 且表头回显所用端点；人工指定覆盖探测记忆；人工指定的渠道 ID 已不存在时被忽略且不影响其它来源。
- custom 校验：相对路径、缺协议、Host 为空、超长、条目超限、渠道 ID 不存在 → 保存被拒；带 userinfo 的地址存入后凭据已被去除。
- 覆盖率：`fetched>0 && matched==0` → `no_overlap` 且不产生 `missing` 差异；渠道模型为空 → `no_models`；正常来源 → `ok` 且 `matched` 计数正确。
- 统计口径：`SourceOK` 不再把 `no_overlap`/`no_models`/空结果计为成功；全部来源非 `ok` 时保留旧快照。
- 旧快照兼容：`status` 缺省的历史快照按 `ok` 渲染。
- 前端 `bun run typecheck`、目标文件 lint、生产构建；后端 `go test ./...` 全绿。

## 11. 实现记录

落地位置：

- 空结果守卫：`controller/ratio_sync.go` 的 `pricingPayloadHasPrices` + `emptyPricingPayloadError`，作用于 type1（ratio_config）、type2（pricing）、OpenRouter 三条路径；models.dev 沿用自己的 `no valid models.dev pricing entries found`。阶梯表达式算价格，只有相对倍率（`completion_ratio`、`cache_ratio`）不算。
- 抓取拆分：`fetchUpstreamPricingSources` 只抓取解析，`fetchUpstreamPricingSnapshotData` 保持原语义（抓取 + 算差异），手动同步走后者，巡检走前者并在合并后统一算一次差异。
- 端点编排：`controller/price_monitor_source.go` 的 `priceMonitorEndpointCandidates` / `resolvePriceMonitorSources`，轮数由候选个数决定（人工指定 1 轮、自动探测最多 2 轮），每轮内部仍受 `maxConcurrentFetches` 和 `TimeoutSeconds` 约束。
- 来源状态：`resolvePriceMonitorSourceStatus` 产出 `ok / failed / no_overlap / no_models` 与 `fetched/matched` 计数，写进 `PriceMonitorSourceHeader`；`buildPriceMonitorMatrix` 对 `no_overlap`、`no_models` 直接跳过，不生成单元格、不计差异。
- 配置：`price_monitor_setting.CustomEndpoints`（map 字段，ConfigManager 原生 JSON 序列化），保存经 `ValidateCustomEndpoints` 严格校验、运行期经 `normalizeCustomEndpoints` 宽松清洗。

与设计的偏差：

1. **取消 `source_no_data` 筛选值**。`no_overlap` 与 `no_models` 的来源在矩阵里没有任何单元格，做成行筛选永远是空结果。改为管理页表格上方的汇总提示（数据取自 `available_source_headers`），分享页则在来源筛选项的副标题里说明。
2. **custom 地址只校验格式，不校验渠道是否存在**。渠道删除后仍留着配置是常态，为此拒绝整份设置的保存会让管理员卡在一个无关条目上；巡检对不存在的渠道 ID 直接忽略。
3. **表头回传 `failure_reason` 枚举（`fetch` / `empty`）而不是错误文本**。分享页也会读到表头，透传上游错误串会泄露部署信息。
4. **渠道配了模型但都不在模型广场内 → `no_overlap` 而非 `no_models`**，两者的文案含义不同：前者是名字对不上，后者是渠道压根没配模型。
5. **`model/config_group.go` 的规范化校验改为 `draft.IsNormalized()`**：结构体带了 map 字段后 `draft != draft.Normalized()` 无法通过编译。
6. **设置页复用 `ChannelSelectorDialog`**（与同步上游倍率同一个组件），并过滤掉官方预设、models.dev 预设与 OpenRouter 类型渠道——这三类不接受人工指定端点，列出来只会让人配了不生效。

开销：稳态每渠道每轮巡检 1 次 HTTP 请求（默认间隔 360 分钟），端点变化或上游故障时退化为 2 次。不触及 AI 中继主链路，不新增 Redis/DB/连接池/协程池占用。
