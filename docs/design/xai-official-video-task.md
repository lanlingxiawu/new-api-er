# xAI 官方视频任务支持

## 目标与范围

为 xAI 官方渠道（`ChannelTypeXai`）补齐视频任务能力，使现有 `/v1/video/generations` 与 `/v1/videos` 能通过 xAI 原生视频 API 提交和轮询 `grok-imagine-video`、`grok-imagine-video-1.5`。

实现必须满足：

- 不影响任何非 xAI 模型计费。
- 不改变现有 Sora/OpenAI 视频任务流程。
- 不改共享计费引擎和通用价格语义。
- xAI 视频计费逻辑隔离在 xAI task adaptor 内，继续使用现有按次任务计费管线。
- 管理后台可继续通过现有模型价格配置动态调整 xAI 视频基础价。
- 用户可在任务日志中看到最终扣费和关键计费维度。

原本不在首版范围内、后续已补充：

- 异步视频任务日志的用户可见计费展示。

不在范围内：

- xAI 视频编辑和扩展接口：`/v1/videos/edits`、`/v1/videos/extensions`。
- 真实 xAI 上游调用测试。

官方资料核对日期：2026-08-14。

- xAI 视频 API：https://docs.x.ai/developers/rest-api-reference/inference/videos
- xAI 视频生成指南：https://docs.x.ai/developers/model-capabilities/video/generation
- xAI 价格文档：https://docs.x.ai/developers/pricing

## 当前状态

项目已有 xAI 文本/图像渠道，`relay/channel/xai/constants.go` 已包含 `grok-imagine-video`，但 `relay.GetTaskAdaptor` 没有 `ChannelTypeXai` 分支。因此通过官方 xAI 渠道路由到视频任务时，无法取得任务适配器。

现有 Sora adaptor 可以作为兼容参考，但 xAI 官方视频协议不同：

- 提交接口：`POST /v1/videos/generations`
- 提交响应：`{ "request_id": "..." }`
- 轮询接口：`GET /v1/videos/{request_id}`
- 成功状态：`done`
- 成功结果地址：`video.url`

## 数据流

### 提交流程

1. 客户端向现有视频任务路由发送请求。
2. 现有 relay 路由选择渠道；当渠道类型为 `ChannelTypeXai` 时，`GetTaskAdaptor` 返回新的 xAI task adaptor。
3. adaptor 使用现有 task request helper 校验请求，并把 `relaycommon.TaskSubmitReq` 存入上下文。
4. `ModelMappedHelper` 执行现有模型映射。
5. `ModelPriceHelperPerCall` 读取当前配置中的模型价格。
6. xAI adaptor 根据时长、分辨率和参考图数量计算一个 xAI 专用 `OtherRatio`：`xai_video_units`。
7. 现有 `RelayTaskSubmit` 应用该倍率，执行预扣费，构造 xAI 上游 JSON body，并提交给 xAI。
8. adaptor 解析 `request_id`，向客户端返回公开 New API task 对象，并把 xAI `request_id` 存为上游任务 ID。
9. 现有任务持久化保存公开 task ID、上游 task ID、计费上下文、私有渠道 key、脱敏后的上游响应。

### 轮询流程

1. 现有视频轮询 worker 使用已保存的上游 `request_id` 调用 `FetchTask`。
2. xAI adaptor 使用任务私有渠道 key 请求 `GET {baseURL}/v1/videos/{request_id}`。
3. `ParseTaskResult` 将 xAI 状态映射为内部任务状态：
   - `queued`、`pending` -> queued
   - `processing`、`in_progress`、`generating`，或无 OpenAI error 的空状态 -> in progress
   - `done`、`completed`、`success` -> success
   - `expired`、`failed`、`error` -> failure
4. 成功时把 `video.url` 写入 `TaskInfo.Url`；现有轮询逻辑再持久化到 `PrivateData.ResultURL`。
5. 现有结算/退款逻辑完成最终额度处理，不新增结算路径。

### 查询与内容获取流程

1. 客户端通过现有视频任务查询路由获取公开任务。
2. xAI adaptor 将存储任务转换为现有 OpenAI-compatible video response。
3. 如果存在结果 URL，继续通过现有公开内容接口访问：
   - `GET /v1/videos/{task_id}/content`

### 任务日志展示流程

1. 现有任务列表 API 返回 `quota` 和安全的 `other` 计费信息。
2. `other` 来源于任务保存时的 `TaskPrivateData.BillingContext`，只包含模型价格、分组倍率、`xai_video_units` 和 `pricing_*` 元数据。
3. `other` 不暴露渠道 key、上游 task ID 等私有信息。
4. 前端任务日志表使用与普通用量日志一致的额度格式展示最终扣费。
5. xAI 视频任务额外展示时长、计费分辨率、参考图数量等维度。

## API 合约

不新增公开 endpoint。

复用现有入口：

- `POST /v1/video/generations`
- `POST /v1/videos`
- `GET /v1/video/generations/:task_id`
- `GET /v1/videos/:task_id`
- `GET /v1/videos/:task_id/content`

鉴权级别不变：这些路由继续走现有 relay/video 路由鉴权链路。

支持的入参：

- `model`：`grok-imagine-video` 或 `grok-imagine-video-1.5`，也支持已有模型映射别名。
- `prompt`：必填。
- `duration` 或 `seconds`：可选时长，受现有任务时长上限保护。
- `size`：可选 OpenAI 风格尺寸；仅在未显式提供 metadata 时用于推断方向/分辨率。
- `image`、`images`、`reference_images` 或 metadata 中的图片字段：可选图片输入，支持公开 URL、data URL、file ID 风格引用。
- `metadata.resolution`：可选，`480p`、`720p`、`1080p`。
- `metadata.aspect_ratio`：可选 xAI aspect ratio 字符串。
- `metadata.seed`：可选整数。

xAI 上游提交 body：

```json
{
  "model": "grok-imagine-video-1.5",
  "prompt": "string",
  "duration": 5,
  "aspect_ratio": "16:9",
  "resolution": "720p",
  "seed": 123,
  "image": { "url": "https://..." },
  "reference_images": [{ "url": "https://..." }]
}
```

空的可选字段不会发送。

## 数据模型变更

不新增表、字段或迁移。

复用现有任务存储：

- `tasks.platform`：保存 `TaskPlatform(strconv.Itoa(ChannelTypeXai))`。
- `tasks.task_id`：公开 task ID。
- `tasks.private_data`：保存上游 xAI `request_id`、渠道 key、结果 URL、计费上下文。
- `tasks.data`：保存脱敏后的上游提交/轮询响应。

DTO 变更：

- `dto.TaskDto.other`：安全计费展示元数据，由 `TaskPrivateData.BillingContext` 生成，不包含凭据和上游私有 ID。

## 配置参数

不新增 setting package 配置。

价格继续由现有 `ModelPrice` 设置控制。管理员可在：

`系统设置 -> 分组与模型计费 -> Model prices`

动态配置：

- `grok-imagine-video`
- `grok-imagine-video-1.5`

默认价格已加入：

- `grok-imagine-video`：`ModelPrice = 0.05`，对应官方 480p 每秒基础价。
- `grok-imagine-video-1.5`：`ModelPrice = 0.08`，对应官方 480p 每秒基础价。

xAI adaptor 会在该基础价上应用独立倍率 `xai_video_units`。

## 计费策略

不修改：

- `types.PriceData` 的通用语义。
- `relay/relay_task.go` 的通用 quota 应用规则。
- `service/billing*`。
- 结算/退款逻辑。
- 其他模型的 ratio/price 计算方式。

xAI adaptor 计算一个单独倍率：

```text
xai_video_units = duration_seconds * resolution_multiplier + reference_image_units
```

该倍率相对于当前配置的 xAI 视频 `ModelPrice`。现有按次计费公式保持不变：

```text
quota = configured_model_price_base_quota * xai_video_units
```

### 官方价格折算

`grok-imagine-video`：

- 480p：基础倍率 `1.0`。
- 720p：`0.07 / 0.05 = 1.4`。
- 输入图片：`0.002 / 0.05 = 0.04`。
- 1080p 当前不按该模型支持能力计费，按 720p 上限处理。

`grok-imagine-video-1.5`：

- 480p：基础倍率 `1.0`。
- 720p：`0.14 / 0.08 = 1.75`。
- 1080p：`0.25 / 0.08 = 3.125`。
- 输入图片：`0.01 / 0.08 = 0.125`。

如果遇到未知 xAI 视频模型，则回退为仅按时长计费，并写入 pricing metadata，便于管理员在日志中识别。

这种方案避免在共享计费引擎中增加加法计费逻辑，把 xAI 特有价格计算限制在 xAI task adaptor 内。

### 计费展示元数据

xAI adaptor 会在 `PriceData.PricingMetadata` 中记录：

- `pricing_xai_video_model`
- `pricing_duration_seconds`
- `pricing_requested_resolution`
- `pricing_resolution`
- `pricing_reference_image_count`

任务日志前端会用这些字段解释最终扣费。

## 关键业务逻辑和边界情况

- 缺失 adaptor：在 `GetTaskAdaptor` 中注册 `ChannelTypeXai`。
- 渠道 key 持久化：`model.InitTask` 对 xAI 保存私有 key，保证轮询使用原始选中 key。
- 提交响应 `request_id`：作为上游 task ID 保存。
- 轮询空状态且无结构化错误：视为非终态，避免早期轮询误判失败。
- 轮询 OpenAI 风格错误：保持现有错误语义，失败后走退款。
- 状态 `done`：标记成功并保存 `video.url`。
- 状态 `expired`：标记失败，原因 `task expired`。
- 图片输入：接受 data URL 和公开 URL；不会在 relay 代码中抓取远程 URL。
- multipart 上传：只在 xAI task adaptor 内读取，不改变共享请求解析行为。
- 未知分辨率：默认解析为安全的 720p，并在计费元数据中记录最终分辨率。
- 缺失时长：本地默认 5 秒，保证预扣费和上游请求一致。
- `grok-imagine-video` 的 1080p：按当前模型能力限制到 720p，避免超收或发送不支持参数。
- 模型映射：保持现有映射顺序；计费基于客户端可见 origin model，上游 body 使用 mapped upstream model。

## 错误处理策略

task adaptor 使用现有 task error wrapper：

- 客户端输入错误：`TaskErrorWrapperLocal(..., "invalid_request", 400)`
- 上游提交响应解析失败：`TaskErrorWrapper(..., "unmarshal_response_failed", 500)`
- 缺少 `request_id`：`TaskErrorWrapper(..., "invalid_response", 500)`
- 上游提交非 200：沿用 `RelayTaskSubmit` 的非 200 处理

不记录明文凭据。轮询日志继续使用现有 logger 与 task ID 格式化逻辑。

## 与现有子系统的关系

### 计费

- 使用现有 `ModelPriceHelperPerCall`、`OtherRatios`、预扣费、结算、退款流程。
- 新增的唯一计费维度是 xAI adaptor 的 `xai_video_units`。
- 非 xAI adaptor 不受影响。
- 管理员可动态覆盖两个 xAI 视频模型的 `ModelPrice`。
- xAI adaptor 始终把配置价格视为 480p 每秒基础价，再叠加分辨率/图片单位。

### 任务日志

- 任务列表 DTO 暴露安全计费元数据，用户可以看到异步任务最终扣费。
- 普通 consume log 不变，仍由现有 `LogTaskConsumption` 记录相同 quota。

### 任务轮询

- 使用现有 `UpdateVideoTasks` 和 `updateVideoSingleTask`。
- xAI 空状态在 adaptor 内规范化为 `IN_PROGRESS`，不改变共享轮询失败逻辑。

### 视频代理

- 成功后使用现有 `PrivateData.ResultURL`。
- 不需要为 xAI 成功任务额外请求上游内容接口。

### 模型列表

- 新增 `grok-imagine-video-1.5`。
- 保留 `grok-imagine-video`。

## Main Chain Impact

同步执行在 relay goroutine 上的内容：

- 使用现有 task helper 校验/解析请求。
- xAI 请求 body 转换。
- xAI 计费倍率计算。
- 现有任务提交流程本来就会执行的同步上游 submit HTTP 请求。

异步或延后执行：

- 轮询仍在现有后台 task polling worker 中。
- 结算/退款仍走现有异步任务计费路径。
- 成本台账和员工提成仍走现有异步代码。

未新增 middleware。未向每次任务 relay 请求增加新的同步 DB 读取；仍只使用现有任务提交已有的模型价格、渠道、计费操作。

## Shared Resource Audit

Redis：

- 不新增 Redis key 或 namespace。
- 现有 billing/token quota Redis 使用不变。

数据库表：

- `tasks`：xAI 任务作为普通视频任务保存。现有视频轮询已查询该表，不新增查询形态。
- `channels`：不新增渠道查询形态。
- `logs` / ledger 表：只通过现有日志和计费路径写入。
- 用户/令牌额度表：只通过现有预扣、退款、结算路径访问。

内存结构：

- 不新增包级可变 map。
- xAI 模型能力和价格倍率为静态常量。

连接池：

- 不新增 DB 或 Redis client。
- xAI 上游 HTTP 使用现有 `service.GetHttpClientWithProxy`。

goroutine / worker pool：

- 不新增每请求 goroutine。
- 不新增 worker pool。
- 现有任务轮询 worker 容量不变。

冲突检查：

- 主 AI 文本 relay 链路不会读取 xAI task adaptor 常量或任务专用转换代码。
- 共享计费结构只通过现有 API 使用，不新增协调锁。

## Concurrency Analysis

100k RPM 提交路径：

- 新增 DB 调用：0。
- 新增 Redis 调用：0。
- 新增锁：0。
- 新增 goroutine：0。
- 额外 CPU：小规模 JSON 映射和标量计费计算。
- 额外内存：一个请求 DTO 和一个上游请求 body；multipart 文件处理只在客户端实际上传 multipart 时发生。

轮询路径：

- 与现有视频任务相同的轮询节奏和 batch 行为。
- 每个被轮询任务一次 xAI 上游 HTTP 调用。
- 不新增数据库表或全局扫描。

## 测试计划

xAI adaptor 测试：

- `GetTaskAdaptor` 对 `ChannelTypeXai` 返回 xAI task adaptor。
- `BuildRequestURL` 使用 `/v1/videos/generations`。
- `BuildRequestHeader` 设置 JSON content type、accept、bearer authorization。
- `BuildRequestBody` 映射 prompt、model、duration、resolution、aspect ratio、seed、reference image。
- `DoResponse` 接受 `{ "request_id": "..." }`，向客户端返回公开 task ID，并保存上游 request ID。
- `DoResponse` 拒绝畸形 JSON 和缺失 `request_id`。
- `FetchTask` 调用 `/v1/videos/{request_id}`。
- `ParseTaskResult` 映射 `queued`、`pending`、`processing`、`in_progress`、`generating`、`done`、`completed`、`success`、`expired`、`failed`、`error`。
- `ParseTaskResult` 提取 `video.url`。
- `ConvertToOpenAIVideo` 保留公开 task ID、model、status、progress、completion time、failure reason。

计费测试：

- `grok-imagine-video` 480p 时长使用 `duration * 1.0`。
- `grok-imagine-video` 720p 使用倍率 `1.4`。
- `grok-imagine-video-1.5` 720p 使用倍率 `1.75`。
- `grok-imagine-video-1.5` 1080p 使用倍率 `3.125`。
- 参考图单位计入 `xai_video_units`。
- `OtherRatio` key 只有 `xai_video_units`，不影响现有 Sora/Gemini/Kling 计费测试。
- 任务 DTO 只暴露安全计费元数据，不暴露私有 key。

已执行验证：

- `go test ./relay/channel/task/xai ./relay` 通过。
- `go test ./model -run TestInitTaskPreservesXaiPrivateKey` 通过。
- `go test ./setting/ratio_setting -run TestDefaultModelPrice_WellKnownAnchors` 通过。
- `cd web && bun run typecheck` 通过。
- `go test ./...` 仍因现有环境/无关问题失败：缺少 `web/dist`、`SQL_DSN` 未配置、controller/model 中既有 DB nil panic。

所有上游行为测试使用 fixture JSON 和 `httptest`，不调用真实 xAI。

## 实现记录

新增文件：

- `relay/channel/task/xai/adaptor.go`
- `relay/channel/task/xai/dto.go`
- `relay/channel/task/xai/helpers.go`
- `relay/channel/task/xai/adaptor_test.go`
- `relay/xai_task_adaptor_test.go`
- `docs/design/xai-official-video-task.md`

更新文件：

- `relay/relay_adaptor.go`：注册 xAI task adaptor。
- `model/task.go`：xAI 任务保存私有 channel key 供轮询使用。
- `relay/channel/xai/constants.go`：加入 `grok-imagine-video-1.5`。
- `setting/ratio_setting/model_ratio.go`：加入 xAI 480p 每秒默认价格。
- `dto/task.go`、`relay/relay_task.go`、`service/task_billing.go`：任务日志暴露安全计费元数据。
- `web/src/features/usage-logs`：任务日志展示扣费和 xAI 计费维度。
- 相关测试文件：覆盖 adaptor 注册、计费、轮询解析、私有 key 保存、任务 DTO 安全计费展示。

共享 service polling 无需修改，因为 xAI 空状态已经在 xAI adaptor 内规范化。
