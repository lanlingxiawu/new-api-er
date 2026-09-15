# 价格巡检：高于平台价格筛选

状态：已实现（后端 + 管理页 + 分享页），价格巡检定向测试与 `go test ./...` 全绿。

## 1. Goals and Scope

在价格巡检页新增一个对比筛选项「高于平台价格」，用于定位**平台配置价格低于可比来源报价**的模型。这是价格差异信号；是否亏损还取决于实际采购成本、分组倍率与用户专属倍率，不能仅凭本筛选判断。成本风险口径见 [亏损检测设计](price-monitor-loss-detection-and-price-repair.md)。

范围：

- 新增 comparison 取值 `above_platform`：逐模型判断是否存在「价格高于平台配置」的来源。
- 候选来源为**渠道价格 + 正式官方价格**（`source_headers[].type` 为 `channel` 或 `official`）。models.dev 是独立参考数据源、不代表实际采购成本，且既有设计（`price-monitor-comparison-model-counts.md`）要求官方与 models.dev 不得合并统计或筛选，因此 **models.dev 不参与本筛选，也不计入本统计**。
- 命中该筛选的模型，表格展示「平台配置列 + 所有高于平台的来源列」；只要比平台高就必须显示，不做只留最高一列的裁剪。
- 在这些高于平台的来源中，标记价格最高的那一列（`highest`），对应用户诉求中的「每个模型最高价格的渠道」。
- 顶部统计卡片新增一项「高于平台价格的模型」，口径与筛选完全一致。
- 分享页同步新增该快捷筛选与「最高」标记。

不在范围内：

- 不修改价格抓取来源、抓取流程和巡检调度。
- 不修改既有 comparison 取值的语义与统计口径。
- 不自动修正平台价格，不产生告警或通知。
- 不新增数据库表、列或配置项。

## 2. Data Flow

```text
价格巡检后台任务（不变）
  -> 抓取平台 / 官方 / models.dev / 渠道价格
  -> priceMonitorCell 生成 PriceMonitorPriceCell
  -> markPriceMonitorDifferences 标记与平台的差异
  -> buildPriceMonitorMatrix 生成 SourceHeaders + MatrixItems
  -> countPriceMonitorComparisonModels 新增累计 above_platform
  -> 写入 price_monitor/snapshot.json，发布内存快照

管理页 GET /api/price_monitor/status
  -> data.snapshot.comparison_model_counts.above_platform -> 新增统计卡片

管理页 GET /api/price_monitor/results?comparison=above_platform
分享页 POST /api/price_monitor/public_query {"comparison":"above_platform"}
  -> queryPriceMonitorMatrix
  -> priceMonitorMatrixItemDisplayKeys(item, headers, "above_platform")
       -> 返回 平台列 + 全部高于平台的来源列
  -> priceMonitorHighestAbovePlatformKey(item, headers)
       -> 在返回的单元格副本上置 highest=true
  -> 前端渲染表格 + 「最高」徽标
```

统计仍然只在巡检任务完成时计算一次，状态接口不重复扫描矩阵。筛选仍然只在查询时对内存快照做一次线性扫描，与现有 comparison 一致。

## 3. API Contracts

不新增端点，扩展现有三个接口的取值与字段。

### 3.1 `GET /api/price_monitor/status`

鉴权不变：`AdminAuth()` + `AdminMenuPriceMonitorView`。

`data.snapshot.comparison_model_counts` 新增字段：

```json
{
  "comparison_model_counts": {
    "platform_official": 12,
    "platform_models_dev": 7,
    "platform_channel": 41,
    "channel_official": 19,
    "channel_models_dev": 9,
    "above_platform": 23
  }
}
```

### 3.2 `GET /api/price_monitor/results`

鉴权不变。`comparison` 参数新增合法取值 `above_platform`；非法值仍回退为 `all`。

### 3.3 `POST /api/price_monitor/public_query`

鉴权不变（动态访问口令）。同样接受 `comparison=above_platform`。

### 3.4 单元格新增字段

`PriceMonitorPriceCell` 新增只在 `above_platform` 视图下出现的可选字段：

```json
{ "mode": "per_token", "input": 3, "output": 15, "highest": true }
```

- `highest` 仅在 `comparison=above_platform` 时对「该模型价格最高的来源列」置 `true`，其余视图不返回该字段（`omitempty`）。
- 平台列永远不会带 `highest`。
- 老前端忽略未知字段，向后兼容。

响应仍使用既有成功结构 `{"success": true, "message": "", "data": ...}`。

## 4. Data Model Changes

无数据库表、列、迁移或索引变更。快照文件仍是只保留最新一份的 `price_monitor/snapshot.json`，不属于持续增长数据。

Go 结构变更：

```go
type PriceMonitorComparisonModelCounts struct {
    PlatformOfficial  int `json:"platform_official"`
    PlatformModelsDev int `json:"platform_models_dev"`
    PlatformChannel   int `json:"platform_channel"`
    ChannelOfficial   int `json:"channel_official"`
    ChannelModelsDev  int `json:"channel_models_dev"`
    AbovePlatform     int `json:"above_platform"`
}

type PriceMonitorPriceCell struct {
    // ...既有字段不变
    Highest bool `json:"highest,omitempty"`
}
```

`priceMonitorMatrixVersion` 由 `12` 递增到 `13`，让部署后旧快照按既有刷新机制触发一次后台巡检，避免把缺少 `above_platform` 的旧快照当成真实的 0。巡检完成前统计显示 `0` 并沿用现有运行状态提示。

## 5. Config Parameters

不新增配置参数，继续使用 `setting/price_monitor_setting` 的现有配置。

## 6. Key Business Logic and Edge Cases

### 6.1 可比较前提

来源与平台在下列任一情况下**不参与**高低判断（既不命中筛选，也不计入统计）：

1. 来源或平台单元格 `unavailable_reason != ""`（未收录 `missing`、占位价 `placeholder`、抓取失败 `source_failed`）——复用既有 `priceMonitorComparisonIgnores`。
2. 计费方式不同（`platform.Mode != source.Mode`，如按量 vs 按次 vs 阶梯）——价格量纲不同，无法判断高低，这类差异由既有 `billing` 筛选覆盖。
3. `tiered_expr` 模式下任一侧 `Dynamic=true`（含 `header()`/`param()`/时间函数等无法安全解析的表达式）。
4. `tiered_expr` 模式下两侧阶梯数量不同，或任一档的条件变量 / 操作符 / 条件值不一致——档位口径不同，逐档比较没有意义。
5. 来源类型不是 `channel` 或 `official`（即平台列自身与 models.dev 列）。

### 6.2 「高于平台」的判定

按用户确认的口径：**任一可比价格维度严格高于平台即命中**。相等判定复用既有 `nearlyEqual` 容差，只有 `source > platform && !nearlyEqual(source, platform)` 才算更高。

| 计费方式 | 参与比较的维度 |
|---|---|
| `per_token` | `input`、`output`，以及按 key 对齐的 lane（cache_read / cache_write / image_input / audio_input / audio_output） |
| `per_request` | `price` |
| `tiered_expr` | 每一档的 `input`、`output`，以及该档按 key 对齐的 lane |

维度缺失处理：某个维度只有一侧有值（例如平台只配了输入价、渠道额外提供输出价，或平台配了缓存倍率而渠道没有）时，该维度**跳过**，不视为更高。这样避免把「未配置」误判成「更贵」。若所有维度都不可比，该来源不命中。

### 6.3 最高价来源的选择

在该模型命中的（即高于平台的）来源中，按打分取最大值：

| 计费方式 | 打分 |
|---|---|
| `per_token` | `input + output`（缺失按 0） |
| `per_request` | `price`（缺失按 0） |
| `tiered_expr` | 各档 `input + output` 的最大值 |

由于 6.1 已要求命中来源与平台计费方式一致，同一模型下所有命中来源的计费方式相同，打分可直接横向比较。分数相同时取 `source_headers` 顺序中的第一个（官方在前，渠道按名称排序），保证结果稳定可复现。

注意：打分高于平台的来源必然在某个维度高于平台，因此「命中集合内的最高分」等于「全部可比来源中的最高分」，不会漏标。

### 6.4 展示规则

- 命中的模型返回 `平台列 + 全部高于平台的来源列`；未高于平台的渠道列、models.dev 列不在该视图返回，表头也随之收敛（复用既有 `visibleKeys` 逻辑）。
- 最高价的那一列单元格带 `highest=true`，前端在该单元格顶部显示一个「最高」徽标；徽标复用现有 `Badge` 组件，不新增配色体系。
- 一个模型即使有多个来源高于平台，也只在统计中计一次。
- 同一模型可以同时命中本筛选和其他既有筛选，各卡片独立计数，互不影响。
- `buildPriceMonitorMatrix` 只保留「至少存在一项差异」的模型；任何高于平台的来源必然已被 `markPriceMonitorDifferences` 标记为 `Different`，因此矩阵构建逻辑无需改动，不会漏掉模型。

### 6.5 边界情况

- 空矩阵 / 没有渠道来源 / 没有官方来源 → 统计为 0，筛选返回空表并显示既有「未发现价格差异」空态。
- 只有 models.dev 高于平台 → 不命中、不计数（按第 1 节的来源隔离要求）。
- 渠道未启用该模型（`prices` 中无该 key）→ 不参与。
- 部分来源抓取失败时，基于成功且可比的来源计算，页面沿用既有 `partial` 状态提示。
- 「显示来源」筛选（`source_keys`）会缩小参与判断的渠道集合，因此表格结果可能少于顶部统计——与既有筛选行为一致：顶部统计始终是最近一次巡检的全局口径，不随表格筛选变化。

## 7. Error Handling Strategy

- 新增逻辑只读内存中已构建的快照矩阵，不发起外部调用，不产生新的错误路径。
- 非法 `comparison` 取值继续静默回退到 `all`，不返回错误（保持既有行为）。
- 快照保存失败继续沿用现有系统错误日志与旧快照保留策略，不发布不完整快照。
- 前端在 `comparison_model_counts.above_platform` 缺失时按 `0` 兼容，`highest` 缺失时不渲染徽标，防止滚动升级期间页面异常。
- 无新增用户可见的错误文案，因此不需要新增后端 i18n key（Rule 13）。

## 8. Interaction With Existing Subsystems

- **价格巡检**：复用既有矩阵、来源类型分类与 `nearlyEqual` 容差；新增判定与既有差异标记相互独立，不改写 `Different` / `InputDifferent` 等既有字段。
- **快照存储**：扩展同一份 `price_monitor/snapshot.json`，不新增第二份存储。
- **分享页**：新增一个快捷筛选按钮与「最高」徽标样式；摘要卡片不变（分享页的「价格不一致模型」本身就是当前筛选下的模型总数，选中该筛选时即为高于平台的模型数）。
- **权限**：沿用价格巡检查看/编辑权限，不新增权限点。
- **计费 / relay / Redis / 数据库**：完全不涉及。
- **i18n**：新增前端文案 key，按 Rule 6 直接补齐 `en / zh / zh-TW / fr / ru / ja / vi` 七种语言，保持 diff 收敛。

## 9. Main Chain Impact

该功能**不接入 AI relay chain**。

- 同步/异步划分：全部逻辑发生在价格巡检后台任务与管理/分享页查询接口上，relay goroutine 上没有任何新增同步或异步操作。
- 共享资源审计：
  - Redis：不访问，无键空间冲突。
  - 数据库：不新增任何读写，不与 `tokens` / `channels` / `users` 等 relay 路径表产生锁竞争。
  - 内存：在既有不可变巡检快照结构上增加一个 int 与一个 bool 字段，不新增共享锁、不改变 GC 压力量级。
  - 文件：只读写巡检专用 `price_monitor/snapshot.json`，relay 不访问该文件。
  - goroutine / 连接池：复用既有受控巡检任务，不新增 goroutine、worker pool 或连接池占用。

因此不需要功能开关，也不涉及 100k RPM 并发分析。

## 10. Test Plan

按 Rule 15.2 先写测试用例再改实现，测试放在 `controller/price_monitor_above_platform_test.go`（同包、`testify/require`）。

**等价类划分**

1. 来源高于平台 → 命中；来源等于平台 → 不命中；来源低于平台 → 不命中。
2. 来源类型：渠道命中、官方命中、models.dev 不命中、平台列自身不参与。
3. 不可用状态：`missing` / `placeholder` / `source_failed` 三类均不命中、不计数。

**判定覆盖（decision / condition coverage）**

4. `per_token`：仅 input 更高、仅 output 更高、仅某个 lane 更高、全部更高、全部更低，各自独立验证。
5. `per_request`：price 更高 / 相等 / 更低。
6. `tiered_expr`：非动态且档位结构一致时某档更高命中；动态表达式不命中；档位数量不同不命中；条件值不同不命中。
7. 计费方式不同（per_token vs per_request、per_token vs tiered_expr）→ 不命中。
8. 维度单侧缺失（平台无 output / 渠道无 output、平台有 lane 渠道无 lane、反之）→ 该维度跳过，不误判为更高。

**边界值**

9. `nearlyEqual` 容差边界：差值在容差内视为相等（不命中），刚好超出容差视为更高（命中）。
10. 空矩阵、单模型、仅平台列、无渠道来源、无官方来源。

**最高价标记**

11. 多个渠道高于平台时，`highest` 只落在打分最大的一列，且平台列不带 `highest`。
12. 打分并列时取 `source_headers` 顺序中的第一个，来源顺序变化不改变既定优先级结果。
13. 其他 comparison 视图（`all`、`channel_platform` 等）返回的单元格不带 `highest`。

**展示与筛选**

14. 命中模型返回平台列 + 全部高于平台的来源列，低于平台的渠道列与 models.dev 列不返回。
15. `source_keys` 缩小渠道集合后，表格结果随之收敛而顶部统计不变。
16. 一个模型多个渠道更高时统计只 +1；多个模型分别命中时统计正确累加。
17. 既有 comparison 取值与统计口径不受影响（回归断言原有五项计数不变）。

**接口与兼容性**

18. `comparison=above_platform` 通过管理接口与公开查询接口均生效；非法值仍回退 `all`。
19. `GET /api/price_monitor/status` 返回六项计数；快照 JSON round-trip 保留新字段。
20. 旧快照缺少 `above_platform` 时可正常加载，并被矩阵版本递增触发刷新。

**执行**

21. 前端：`bun run typecheck` + 目标文件 lint；不新增测试框架（Rule 15.7）。
22. 后端：先跑 `go test ./controller/ -run PriceMonitor`，最后 `go test ./...` 全绿。

## 11. 实现记录

- 新增判定与打分逻辑集中在 `controller/price_monitor.go`：`priceMonitorAbovePlatformCandidate` / `priceMonitorSourceAbovePlatform` / `priceMonitorPriceHigher` / `priceMonitorLanePriceHigher` / `priceMonitorTierPriceHigher` / `priceMonitorHighestAbovePlatformKey` / `priceMonitorCellScore`。
- `priceMonitorMatrixItemDisplayKeys` 新增 `above_platform` 分支；`queryPriceMonitorMatrix` 只在该 comparison 下计算最高价 key，并在返回的单元格副本上显式写入 `Highest`（其余视图恒为 false）。
- `countPriceMonitorComparisonModels` 复用同一分支累计 `AbovePlatform`，保证统计与筛选口径一致。
- 新增测试文件 `controller/price_monitor_above_platform_test.go`；`controller/price_monitor_test.go` 中两处整体结构断言按新增字段更新（`TestCountPriceMonitorComparisonModels` 的 fixture 实际包含 4 个高于平台的模型，`TestPriceMonitorOfficialAndModelsDevComparisonsAreIndependent` 为 1 个，其中仅 models.dev 更贵的模型如设计所述不计数）。
- 前端在 `price-monitor-panel.tsx` 把原 `PriceCell` 主体拆为 `PriceCellContent`，外层只负责在 `highest` 时叠加一个 `Badge variant="warning"`，避免在每个计费模式分支里重复插入徽标。
- 统计卡片网格由 5/7 张调整为 6/8 张（`xl:grid-cols-3 2xl:grid-cols-6` / `xl:grid-cols-4 2xl:grid-cols-8`）。
- 分享页新增 `above_platform` 快捷筛选按钮与 `.highest-note` 徽标样式，复用既有 `--danger` 配色变量，未引入新配色体系。
