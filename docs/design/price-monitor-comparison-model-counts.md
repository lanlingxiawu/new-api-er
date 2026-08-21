# 价格巡检模型对比数量

状态：已实现（含 models.dev 来源拆分修订）

## Goals and Scope

将价格巡检页顶部现有的“不一致模型”和“差异条目”两个汇总卡片替换为按对比关系统计的模型数量。正式官方源指 basellm 官方倍率预设，models.dev 是独立的可选参考源，两者不得合并显示、统计或筛选：

1. 平台对比官方
2. 平台对比渠道
3. 渠道对比官方
4. 平台对比 models.dev（仅启用 models.dev 时展示）
5. 渠道对比 models.dev（仅启用 models.dev 时展示）

本设计中的“模型对比数量”解释为：在最近一次巡检快照中，该对比关系下存在至少一项可比较价格差异的唯一模型数。一个模型即使与多个渠道不一致，也只在对应卡片中计数一次。不可用、抓取失败、占位价格等不参与价格比较的状态不计入数量。

保留“上次巡检”和“价格来源”卡片。未启用 models.dev 时页面顶部展示五张汇总卡片；启用后增加两个 models.dev 专属卡片，共七张。筛选条件只影响下方表格，不改变顶部最近一次巡检的全局汇总。

本次修订会拆分现有官方筛选语义：原有 `platform_official`、`channel_official` 和 `official_missing` 只针对正式官方源；新增 models.dev 专属筛选。管理页和分享页同步展示独立来源与筛选；巡检来源、价格比较算法、价格矩阵数据和 AI 请求转发链路不变。

## Data Flow

```text
后台价格巡检任务
  -> 构建 SourceHeaders 与 MatrixItems
  -> 将来源显式分类为 platform / official / models_dev / channel
  -> 使用五种 comparison 判定规则逐模型判断
  -> 分别累计 platform_official / platform_models_dev /
     platform_channel / channel_official / channel_models_dev
  -> 将汇总写入最新快照 JSON，并发布内存快照
  -> GET /api/price_monitor/status 读取内存快照
  -> React 根据 include_models_dev 展示三项或五项模型对比数量
```

统计在巡检后台任务完成时一次性计算，状态接口不重复扫描矩阵。

## API Contracts

不新增端点。扩展现有管理接口：

- `GET /api/price_monitor/status`
- 鉴权：现有 `AdminAuth()` + `AdminMenuPriceMonitorView` 权限

`data.snapshot` 新增：

```json
{
  "comparison_model_counts": {
    "platform_official": 12,
    "platform_models_dev": 7,
    "platform_channel": 41,
    "channel_official": 19,
    "channel_models_dev": 9
  }
}
```

管理端矩阵查询和分享页公开查询接口沿用 `comparison` 参数并新增以下取值：

- `platform_models_dev`：平台配置与 models.dev 不一致
- `channel_models_dev`：渠道与 models.dev 不一致
- `models_dev_missing`：models.dev 未收录模型

原有 `platform_official`、`channel_official`、`official_missing` 保持字段名不变，但只匹配正式官方源，不再隐式包含 models.dev。`all`、`input`、`output`、`cache`、`billing` 和 `source_failed` 仍可覆盖所有已启用来源。

`source_headers[].type` 的枚举扩展为 `platform | official | models_dev | channel`。models.dev 表头返回 `type=models_dev`，使管理页和分享页不依赖中文名称或 URL 猜测来源身份。

现有 `model_count` 与 `item_count` 暂时保留在后端快照和接口中，避免破坏已有调用方；管理页面不再展示这两个字段。响应仍使用现有成功结构 `{"success": true, "message": "", "data": ...}`。

## Data Model Changes

不新增数据库表、列或迁移。为 `PriceMonitorSnapshot` 增加内嵌汇总结构并持久化到现有独立文件 `price_monitor/snapshot.json`：

```go
type PriceMonitorComparisonModelCounts struct {
    PlatformOfficial  int `json:"platform_official"`
    PlatformModelsDev int `json:"platform_models_dev"`
    PlatformChannel   int `json:"platform_channel"`
    ChannelOfficial   int `json:"channel_official"`
    ChannelModelsDev  int `json:"channel_models_dev"`
}
```

快照矩阵版本递增。启用价格巡检时，旧快照会按现有刷新机制触发一次后台巡检，避免把缺少新字段的旧快照误显示为真实的零值。巡检完成前页面显示 `0`，并沿用现有运行状态提示。

该文件只保留最新快照，不属于持续增长数据，无需索引设计。

## Config Parameters

不新增配置参数。继续使用 `setting/price_monitor_setting` 的现有配置。

## Key Business Logic and Edge Cases

### 统计口径

- 平台对比官方：`platform_official` 只选择正式官方源；来源存在、可比较且与平台不同，则该模型计数一次。
- 平台对比 models.dev：`platform_models_dev` 只选择 models.dev；来源存在、可比较且与平台不同，则该模型计数一次。
- 平台对比渠道：复用现有 `channel_platform` 规则；任一可比较渠道与平台不同，则该模型计数一次。
- 渠道对比官方：`channel_official` 只用正式官方源与可比较渠道逐一比较。
- 渠道对比 models.dev：`channel_models_dev` 只用 models.dev 与可比较渠道逐一比较。
- 同一模型命中多个对比关系时，可同时进入多个卡片的数量。
- 同一模型命中同一关系下多个渠道时，仅计数一次。
- 任何一侧没有实际价格时都不具备比较条件。官方价格预设未收录、渠道价格接口未提供、渠道未启用、来源抓取失败和占位价格，均不能单独使模型进入“全部差异”或任何价格对比筛选，也不得计入对应的顶部数量；同一模型若另有两侧均具备实际价格的差异，仍按那一组有效关系参与对比。
- `unavailable_reason=missing` 统一由 `priceMonitorComparisonIgnores` 排除，不能把“未提供价格”误判成价格不一致。
- “官方未收录模型”“models.dev 未收录模型”“渠道未提供模型”和“来源检查失败”专项筛选分别展示对应状态，便于管理员单独排查覆盖范围；这些专项状态与价格差异互斥。
- 部分来源失败时，基于成功且可比较的来源计算数量，页面继续沿用 `partial` 状态提示。
- 没有可比较模型时五个数量均为 `0`；未启用 models.dev 时两个 models.dev 数量为 `0`，前端不展示对应卡片和筛选项。

### UI

- 保留现有卡片样式和响应式布局，不引入新的颜色或独立样式体系。
- models.dev 列标题明确显示 `models.dev price`，不得再被通用 `official` 分支覆写为“官方价格”；正式官方列继续显示“官方价格”。
- 未启用 models.dev 时保持五张卡片；启用后七张卡片在足够宽的屏幕等宽排列，中小屏按现有网格规则换行。
- `platform_models_dev`、`channel_models_dev` 和 `models_dev_missing` 只在 models.dev 已启用且快照包含该来源时显示，避免提供必然为空的筛选项。
- 分享页同样根据 `available_source_headers` 判断是否显示 models.dev 筛选，并为 models.dev 使用独立缺失提示；桌面端平台、官方、models.dev 三个参考列使用互不重叠的固定位置。
- 在“全部差异”等因渠道差异而保留模型的视图中，如果官方价格有效且与平台配置完全一致，官方价格单元格仍列出完整官方价格，便于直接与渠道价格对照；不得用“与官方一致”替代价格明细。
- 官方未收录、检查失败、占位价格等状态继续显示各自原因。
- 新增 models.dev 表头、两张统计卡、两项价格对比筛选和一项缺失筛选所需 i18n 文案；通过项目脚本同步七种前端语言。

## Error Handling Strategy

- 统计逻辑只处理内存中的已构建矩阵，不产生新的外部错误。
- 快照保存失败继续使用现有系统错误日志和旧快照保留策略，不发布不完整快照。
- 状态接口继续返回已有内存快照，不向客户端暴露内部错误。
- 前端缺少新字段时按零值兼容，防止滚动升级期间页面崩溃。

## Interaction With Existing Subsystems

- 价格巡检：复用已有矩阵与比较规则，按明确来源类型选取参考列，保证卡片数量和五个对比筛选器的模型口径一致。
- 快照存储：扩展现有 JSON 文件和原子内存快照，不增加第二份存储。
- 分享页：摘要保持不变；公开查询接受新增 comparison 值，来源表头、筛选、缺失提示及固定列位置与管理页保持一致。
- 权限：沿用价格巡检查看权限。
- i18n：通过项目规定的脚本同步 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`。

## Main Chain Impact

该功能不接入 AI relay chain。统计只在现有价格巡检后台任务中执行；relay 请求 goroutine 上没有新增同步或异步操作。

共享资源审计：

- Redis：不访问，无键空间冲突。
- 数据库：不新增读写，不与 relay 表产生锁竞争。
- 内存：扩展现有不可变价格巡检快照两个整数及来源类型，不增加共享锁。
- 文件：只扩展价格巡检专用 `price_monitor/snapshot.json`，relay 不访问该文件。
- goroutine/连接池：复用现有受控巡检任务，不新增 goroutine、worker pool 或连接池占用。

## Test Plan

先补测试，再修改实现：

1. 等价类：五个关系分别只有相同价格、只有不同价格、缺少来源、来源不可比较。
2. 条件覆盖：平台/正式官方/models.dev 存在与否、渠道类型判断、忽略状态、价格相等判断分别覆盖真和假。
3. 去重：一个模型与多个渠道不同，同一关系只计数一次。
4. 多关系：同一模型同时命中两个或三个关系，各自数量均增加。
5. 边界值：空矩阵、单模型、仅平台列、无官方来源、无渠道来源。
6. 价格缺失：正式官方、models.dev 或渠道 `missing` 单元格均被 `all` 和价格对比筛选排除且不增加对应数量；三个专项缺失筛选只返回各自来源。若模型还存在其他有效价格差异，`all` 仅返回参与有效对比的来源列。
7. 来源隔离：正式官方和 models.dev 同时存在且结果不同，五项筛选与五个数量互不串值；来源顺序变化不影响结果。
8. 价格展示：正式官方与 models.dev 表头标注明确；有效价格无论是否与平台一致都返回并展示具体价格；所有不可用状态仍显示原因。
9. 快照/API：新增字段 JSON round-trip，并验证 `GET /api/price_monitor/status` 返回五个数量。
10. 兼容性：旧快照缺少新字段时可加载，并被矩阵版本刷新机制识别。
11. 前端：运行 `bun run i18n:sync`、`bun run typecheck` 和目标文件 lint；不新增测试框架。
12. 后端：先运行价格巡检相关 Go 测试，最后运行 `go test ./...`。
