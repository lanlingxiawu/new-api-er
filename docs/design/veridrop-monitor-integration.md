# Veridrop Monitor 检测接口接入设计

## 背景

当前项目负责渠道、模型、权限、任务调度和管理前端。`E:\code\veridrop-monitor` 只作为外部检测能力接口使用，不接入它的前端，也不把渠道控制权交给 Veridrop。

已实现的接入原则：

- 批量检测默认覆盖所有已启用渠道。
- 每个渠道检测其配置中的已启用模型。
- 检测目标是渠道上游 `base_url`，不是当前项目的中转入口。
- 检测失败默认只记录结果，不自动禁用渠道。
- 自动禁用作为显式开关提供，默认关闭。
- 前端在当前项目中提供独立检测页面 `/channels/detection`。

## 目标和范围

目标：

- 在当前项目中新增 Veridrop 检测后台任务。
- 读取当前项目的渠道、模型、上游地址和密钥，提交给 Veridrop 后端检测。
- 保存检测历史，供后台页面查看。
- 保持渠道启停控制由当前项目负责。
- 支持批量检测、手动测试、任务状态和结果查看。
- 不影响 AI 请求主链路。

范围内：

- Veridrop 后端地址与检测参数配置。
- 单渠道检测接口。
- 手动填写上游地址、API Key、模型和协议进行一次性检测。
- 批量检测已启用渠道和模型。
- 检测结果表和查询接口。
- 后台系统任务接入。
- 自动禁用失败渠道开关。
- 前端独立检测页面 `/channels/detection`。

范围外：

- 不接入 Veridrop 自带前端。
- 不把 Veridrop 作为渠道状态的唯一事实来源。
- 不在 AI relay 请求过程中同步调用 Veridrop。
- 不做检测成功后的自动启用。
- 不修改模型能力表 `abilities`。

## 数据流

### 批量检测

1. 管理员在 `/channels/detection` 查看“批量检测”页签。
2. 前端先请求 `GET /api/channel/veridrop/targets`，展示本次会检测的渠道、模型、上游 Base URL，以及因协议不支持或没有模型而不会纳入检测的渠道。
3. 管理员点击“运行检测”后，前端请求 `POST /api/channel/veridrop/detect_enabled`。
4. Controller 校验管理员渠道操作权限，并创建 `veridrop_detection` 系统任务。
5. 系统任务读取 `veridrop_monitor_setting` 配置。
6. 服务层按 `id > last_id` keyset 分页读取已启用渠道。
7. 每个渠道使用 `channel.GetModels()` 展开已启用模型。
8. 每个渠道/模型组合创建一条 `channel_veridrop_detections` 记录，初始状态为 `queued`。
9. 后台 worker 按 `MaxConcurrent` 限制并发提交检测。
10. 服务层调用 Veridrop：
    - `POST {veridrop_base_url}/api/detect/{protocol}`
    - `GET {veridrop_base_url}/api/status/{job_id}`
    - `GET {veridrop_base_url}/api/result/{job_id}.json`
11. 检测完成后保存分数、结论、摘要、错误和脱敏后的原始 JSON。
12. 如果自动禁用开关开启，且结果满足禁用条件，则调用当前项目已有 `DisableChannel`。
13. 前端轮询系统任务和检测结果，展示最新状态。

### 单渠道检测

1. 调用 `POST /api/channel/veridrop/detect`，传入 `channel_id`。
2. 服务层读取指定渠道。
3. 模型选择顺序为：请求中的 `model`、渠道 `TestModel`、渠道模型列表第一项。
4. 其余流程与批量检测一致。

### 手动检测

1. 管理员在“手动测试”页签填写临时上游 `base_url`、`api_key`、`model`、`protocol` 和检测模式。
2. 前端请求 `POST /api/channel/veridrop/detect_manual`。
3. Controller 校验渠道操作权限和必填字段。
4. 服务层创建一条 `channel_veridrop_detections` 记录，`channel_id = 0`，`channel_name = "Manual Test"`。
5. 后端异步调用 Veridrop，不创建系统任务，不把手动填写的 API Key 写入任务或响应。
6. 检测结果进入现有检测结果列表；手动检测永远不会触发自动禁用渠道。

### 存储和响应

- 任务状态存在现有 `system_tasks`。
- 每个渠道/模型检测结果存在新表 `channel_veridrop_detections`。
- API 只返回任务和结果元数据，不返回 API Key。
- Veridrop 返回内容入库前进行密钥脱敏。

## API 合约

所有接口都挂在已有渠道管理路由下，使用后台权限控制，无公开接口。

### 启动单渠道检测

`POST /api/channel/veridrop/detect`

权限：渠道操作权限。

请求：

```json
{
  "channel_id": 1,
  "model": "claude-haiku-4-5",
  "protocol": "anthropic",
  "mode": "quick",
  "include_long_context": false,
  "include_long_context_extreme": false,
  "openai_wire_api": "chat_completions"
}
```

响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "task": {},
    "created": true
  }
}
```

### 启动已启用渠道批量检测

`POST /api/channel/veridrop/detect_enabled`

权限：渠道操作权限。

请求：

```json
{
  "mode": "quick",
  "include_long_context": false,
  "include_long_context_extreme": false,
  "openai_wire_api": "chat_completions"
}
```

可选调试字段：

- `channel_ids`：限制到指定渠道集合。
- `model`：覆盖模型选择。
- `protocol`：覆盖协议推断。
- `max_channels`：限制本次最多处理的渠道数。

响应同单渠道检测。

### 启动手动检测

`POST /api/channel/veridrop/detect_manual`

权限：渠道操作权限。

请求：

```json
{
  "base_url": "https://api.example.com/v1",
  "api_key": "sk-...",
  "model": "gpt-5",
  "protocol": "openai",
  "mode": "quick",
  "include_long_context": false,
  "include_long_context_extreme": false,
  "openai_wire_api": "chat_completions"
}
```

响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "id": 123,
    "channel_id": 0,
    "channel_name": "Manual Test",
    "protocol": "openai",
    "model": "gpt-5",
    "status": "queued"
  }
}
```

### 查询本次批量检测范围

`GET /api/channel/veridrop/targets`

权限：渠道读取权限。

查询参数：

- `mode`：可选，检测模式。
- `model`：可选，覆盖模型选择。
- `protocol`：可选，覆盖协议推断。
- `max_channels`：可选，限制预览渠道数；默认不限制。
- `include_long_context`：可选，是否启用长上下文探针。
- `include_long_context_extreme`：可选，是否启用极限长上下文探针。
- `openai_wire_api`：可选，OpenAI 兼容检测协议。

响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "items": [
      {
        "channel_id": 1,
        "channel_name": "主站 OpenAI",
        "channel_type": 1,
        "channel_type_name": "OpenAI",
        "protocol": "openai",
        "base_url": "https://api.openai.com/v1",
        "models": ["gpt-5", "gpt-4o"],
        "model_count": 2
      }
    ],
    "channel_count": 1,
    "model_count": 2,
    "skipped_channel_count": 0
  }
}
```

该接口只读取当前已启用渠道和模型，不创建系统任务，不读取或返回 API Key。前端用它在“运行检测”前展示会检测哪些渠道、哪些模型，并在确认弹窗里给出准确数量。

### 查询检测结果列表

`GET /api/channel/veridrop/results`

权限：渠道读取权限。

查询参数：

- `channel_id`：可选，按渠道过滤。
- `channel_name`：可选，按渠道名称模糊过滤。
- `model`：可选，按模型名称模糊过滤。
- `mode`：可选，按检测模式过滤。
- `verdict`：可选，按检测结论模糊过滤。
- `keyword`：可选，在结论、摘要和错误信息中检索。
- `status`：可选，按状态过滤。
- `protocol`：可选，按协议过滤。
- `min_score` / `max_score`：可选，按检测分数范围过滤。
- `updated_after`：可选，只返回该 Unix 秒之后更新的结果。
- `error_only`：可选，只返回带有错误信息的结果。
- `before_id`：可选，keyset 翻页游标。
- `limit`：可选，最大 100。

响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "items": [],
    "next_before_id": 123
  }
}
```

### 查询单条检测结果

`GET /api/channel/veridrop/results/:id`

权限：渠道读取权限。

响应：

```json
{
  "success": true,
  "message": "",
  "data": {}
}
```

## Veridrop 后端接口

当前项目调用 `E:\code\veridrop-monitor` 已有接口：

- Claude/Anthropic：`POST /api/detect/claude`
- OpenAI 兼容：`POST /api/detect/openai`
- Gemini：`POST /api/detect/gemini`
- 任务状态：`GET /api/status/{job_id}`
- JSON 结果：`GET /api/result/{job_id}.json`

提交表单字段：

- `base_url`：渠道上游地址，来自 `channel.GetBaseURL()`。
- `api_key`：渠道密钥，来自当前项目渠道密钥选择逻辑。
- `model`：本次检测模型。
- `mode`：检测模式。
- `include_long_context`：是否启用长上下文检测。
- `include_long_context_extreme`：是否启用极限长上下文检测。
- `wire_api`：仅 OpenAI 协议使用，默认 `chat_completions`。

鉴权：

- 如果配置了 `admin_api_key`，请求 Veridrop 时附带 `Authorization: Bearer <admin_api_key>`。
- 当前实现不要求 Veridrop 必须开启鉴权。

## 数据模型

新增表：`channel_veridrop_detections`

主要字段：

- `id`
- `channel_id`
- `channel_name`
- `channel_type`
- `protocol`
- `model`
- `mode`
- `status`
- `veridrop_job_id`
- `score`
- `verdict`
- `summary`
- `run_error`
- `error`
- `result_json`
- `created_at`
- `started_at`
- `finished_at`
- `updated_at`

状态枚举：

- `queued`
- `running`
- `done`
- `error`
- `timeout`
- `cancelled`
- `skipped`

索引设计：

- `channel_id`：支持按渠道查看历史。
- `protocol, id`：支持按协议过滤和倒序列表。
- `status, id`：支持按状态过滤和倒序列表。
- `finished_at, id`：支持后续按完成时间清理或统计。
- `veridrop_job_id`：支持从 Veridrop job 反查。
- `created_at`：支持时间相关排序和清理。

兼容性：

- 使用 GORM AutoMigrate。
- JSON 结果以 `TEXT` 保存，避免数据库特定 JSON 类型。
- 时间字段使用 Unix 秒级整数。
- 兼容 SQLite、MySQL 和 PostgreSQL。

## 配置参数

新增配置模块：`veridrop_monitor_setting`

结构：

```go
type VeridropMonitorSetting struct {
    Enabled bool `json:"enabled"`
    BaseURL string `json:"base_url"`
    AdminToken string `json:"admin_api_key"`
    DefaultMode string `json:"default_mode"`
    DefaultOpenAIWireAPI string `json:"default_openai_wire_api"`
    IncludeLongContext bool `json:"include_long_context"`
    IncludeLongContextExtreme bool `json:"include_long_context_extreme"`
    AutoDisableEnabled bool `json:"auto_disable_enabled"`
    AutoDisableFailedThreshold int `json:"auto_disable_failed_threshold"`
    MaxConcurrent int `json:"max_concurrent"`
}
```

配置通过 `setting/operation_setting` 注册，由现有设置系统持久化。前端配置入口放在检测页右上角“检测设置”弹窗内，不直接铺在页面首屏，避免挤压批量检测、手动测试和结果查看区域。

## 关键业务逻辑和边界

- 批量检测只处理启用状态的渠道。
- 渠道模型为空时跳过该渠道，并在检测范围里展示原因。
- 协议只支持 OpenAI 兼容、Anthropic/Claude、Gemini；其他协议跳过。
- 手动检测不复用渠道密钥，不写入 API Key。
- 手动检测永远不触发自动禁用。
- 自动禁用默认关闭；开启后只对批量/单渠道检测生效。
- Veridrop 调用失败时只更新检测记录和任务状态，不影响其它渠道继续检测。
- 页面下拉框显示本地化文案，例如“快速”“标准”“完整”“OpenAI 兼容”，不直接显示内部值。

## 前端交互设计

- 检测页面使用三个页签：“批量检测”“手动测试”“检测结果”。
- 页面右上角显示“检测设置”按钮和当前失败策略徽标。
- 检测设置通过弹窗编辑，保存成功后关闭弹窗；保存失败时弹出用户可理解的错误提示。
- 批量检测页签优先展示运行按钮、检测目标、范围、失败策略、活跃任务和完整检测范围列表。
- 手动测试页签提供协议、检测模式、Base URL、API Key、模型、OpenAI Wire API 和长上下文开关。
- 检测结果页签展示最近结果、运行中数量和失败数量。
- 检测结果筛选会传到后端结果接口，而不是只筛当前前 100 条；支持按状态、协议、模式、更新时间、渠道名称/ID、模型名称、结论、是否有错误、最低分、最高分和关键字筛选；下载报告按钮导出当前筛选后的 CSV，便于排查和留档。
- 结果筛选区域使用明确标签而不是只靠占位符，避免多语言文案或窄屏下被挤压后无法判断字段含义。
- 所有加载、空状态和错误状态都告诉使用者下一步可做什么，例如刷新、补全设置或保存配置。
- 布局避免窄栏承载长文案；批量检测卡片和检测范围卡片纵向排列，减少文案挤压。
- 页面尽量减少卡片嵌套：检测运行、检测范围、手动测试和结果区都使用单层 section；统计信息、空状态和任务状态不再额外套多层卡片。
- 所有新增文案使用 `t('English key')`，并同步到 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`。

## 错误处理策略

- Controller 使用 `common.ApiErrorMsg` 或现有成功响应结构，不返回原始 Go 错误给前端。
- 服务层记录内部错误，前端只展示可操作文案。
- Veridrop 网络错误、超时、结果解析错误都会落到检测结果记录。
- API Key 和 Bearer token 不写日志、不返回给客户端。

## 与现有系统交互

- 渠道：读取启用渠道、base_url、密钥和模型列表；自动禁用时复用现有禁用逻辑。
- 模型：只读取渠道配置中的模型，不修改模型广场或能力表。
- 系统任务：批量检测写入 `system_tasks`，手动检测不创建系统任务。
- 设置：通过现有 `setting` 系统保存 Veridrop 配置。
- 前端路由：新增 `/channels/detection`，在管理员菜单中显示。
- i18n：前端新增文案进入现有 i18next 语言包。

## Main Chain Impact

该功能不挂入 AI relay 主链路。

同步执行：

- 仅后台管理 API 请求中的权限校验、参数校验、配置读取、目标预览和任务创建。
- 检测页面的前端请求只访问管理接口。

异步执行：

- 批量检测由系统任务后台执行。
- 单渠道和手动检测提交 Veridrop 后在后台轮询状态并写入结果。

不会在 relay goroutine 上执行：

- Veridrop HTTP 调用。
- 检测结果写库。
- 自动禁用判断。
- 前端轮询。

## Shared Resource Audit

- DB 表 `channels`：检测任务读取启用渠道和模型。relay 链路也会读取渠道信息，但本功能在后台任务中分页读取，不在 relay 请求路径中执行。
- DB 表 `channel_veridrop_detections`：新增独立结果表，relay 链路不访问。
- DB 表 `system_tasks`：后台管理任务表，relay 链路不访问。
- DB 配置表：通过现有 setting 系统读写 `veridrop_monitor_setting`，relay 链路不访问该配置。
- Redis：当前实现不新增 Redis key。
- 内存结构：只使用任务局部变量和有限 worker 并发，不新增全局缓存。
- 连接池：检测任务会占用数据库连接和 Veridrop HTTP 连接；并发由 `MaxConcurrent` 限制，默认值较小，避免后台任务挤占 relay 资源。

## Concurrency Analysis

管理后台路径不要求 100k RPM relay 级别并发，但仍做以下限制：

- 目标预览使用 keyset 分页读取渠道，避免无界全表扫描。
- 批量检测按 `MaxConcurrent` 限制同时提交 Veridrop 的数量，默认 2。
- 每个渠道/模型组合最多产生一条结果记录和一组状态更新。
- 无分布式锁。
- 无 relay 每请求 DB/Redis 调用增量。
- 在 100k RPM relay 流量下，本功能不会随 relay 请求数增长；只随管理员启动的检测任务增长。

## 测试和验证

后端：

- `service/veridrop_monitor_test.go` 覆盖手动检测、目标展开、任务状态和 Veridrop 调用分支。
- `controller/channel_veridrop_test.go` 覆盖手动检测必填字段校验。
- 目标命令：`go test ./service ./controller ./router`。

前端：

- 目标命令：`cd web && bun run typecheck`。
- 目标命令：`cd web && bunx oxlint -c .oxlintrc.json src/features/veridrop-detection src/routes/_authenticated/channels/detection.tsx`。
- 构建命令：`cd web && bun run build`。

部署后：

- 确认 `/channels/detection` 返回 200。
- 确认 `GET /api/channel/veridrop/targets` 能返回启用渠道和模型统计。
- 确认 `POST /api/channel/veridrop/detect_manual` 能创建手动检测记录。
- 确认 `POST /api/channel/veridrop/detect_enabled` 能创建系统任务并推进到终态。
