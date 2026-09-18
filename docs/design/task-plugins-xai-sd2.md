# xAI 视频 / 第三方 Seedance 2.0 任务插件

## 目标与范围

上游 #7076 把全部内置 Go 任务适配器替换为 `plugins/tasks/<key>/plugin.js` 沙箱 JS 插件（`pkg/jsplugin` 执行，`relay/channel/task/jsplugin` 适配，插件计费表达式 + 宿主轮询/结算）。本分叉独有的两个视频任务适配器以同样方式实现：

| 插件 key | 渠道类型 | 模型 | 上游 |
|---|---|---|---|
| `xai` | `ChannelTypeXai = 48`（与 xAI 对话共用类型，同 sora 声明 OpenAI 类型 1 的做法） | `grok-imagine-video`、`grok-imagine-video-1.5` | xAI 官方 Grok Imagine 视频 API |
| `thirdpartysd2` | `ChannelTypeThirdPartySD2 = 58` | `dreamina-seedance-2-0-260128`、`dreamina-seedance-2-0-fast-260128` | 第三方 Seedance 2.0 服务 |

范围内：提交请求转换与校验、上游鉴权头、轮询与状态映射、结果地址与内容代理、计费事实与计费语义、旧任务兼容。

范围外：xAI 视频编辑/扩展接口；Seedance remix/草稿；真实上游联调。

官方资料：xAI 视频 API https://docs.x.ai/developers/rest-api-reference/inference/videos ，价格 https://docs.x.ai/developers/pricing 。

## 数据流

### 提交

1. 客户端调用 `POST /v1/video/generations`（旧路由，宿主 `ValidateBasicTaskRequest` 解析为 `TaskSubmitReq` 形状的 `requestBody`）、`POST /v1/videos`（`protocols.openai_video.decodeRequest`）或 `POST /v1/responses`（`protocols.openai_responses.decodeRequest`）。
2. 分发选中渠道；渠道类型 48/58 经 `channelTypes` 索引确定执行插件。
3. `ValidateRequestAndSetAction` 调用 `buildSubmitRequest`：插件完成全部请求校验并生成上游描述符；校验失败返回 400 `plugin_request_invalid`，发生在计费之前。
4. 计费（见下文）→ 预扣 → 宿主按描述符发送上游请求 → `parseSubmitResponse` 取上游任务 ID，`taskData` 保存上游原始响应。
5. 宿主写入任务（平台为插件 key `xai` / `thirdpartysd2`），客户端响应由宿主协议层渲染公开任务 ID。

### 轮询

`buildQueryRequest` → 宿主 HTTP（404/410 立即失败退款，401/403/429/5xx 计轮询失败）→ `parseTaskResult` → `extractUsageOnComplete` → 宿主结算。

### 查询与内容

- `GET /v1/videos/:id`：`protocols.openai_video.render`。
- `GET /v1/videos/:id/content`、`/v1/tasks/:id/artifacts/video/content`：`listArtifacts` + `buildContentRequest`。
- `GET /v1/video/generations/:id` 与任务列表：`relay.TaskModel2Dto` / `getExternalVideoURL`。

## API 合约

客户端端点沿用视频/任务路由的 `TokenAuth`；两个插件各自新增一组 `meta.routes` 入站端点（见「级联：下游网关接入」），鉴权同为 `TokenAuth`。

### xai

上游提交 `POST {base}/v1/videos/generations`，头 `Content-Type/Accept: application/json`、`Authorization: Bearer <key>`：

```json
{"model":"grok-imagine-video-1.5","prompt":"...","duration":5,"aspect_ratio":"16:9","resolution":"720p","seed":123,
 "image":{"url":"https://..."},"reference_images":[{"file_id":"file_x"},{"url":"https://..."}]}
```

请求取值：

- 时长：`metadata.durationSeconds` → `metadata.duration` → `duration` → `seconds` → 默认 5。
- 宽高比：`metadata.aspect_ratio|aspectRatio` → `size` 推断（横 16:9 / 竖 9:16 / 方 1:1）→ 16:9。
- 分辨率：`metadata.resolution`（`480`/`720`/`1080` 规范化为带 `p`）→ `size` 短边 → 720p；`grok-imagine-video` 的 1080p 下调为 720p；其他值（如 `4k`）返回 400 `resolution "4k" is not supported; use 480p, 720p or 1080p`。
  - 取舍：xAI 视频接口只接受 `480p`/`720p`（1.5 另有 `1080p`）。旧 Go 适配器的输入校验（时长、参考图、1.5 首图）没有覆盖分辨率，未知值被原样转发并按 480p 倍率计费，属于遗漏而非有意放行，转发后会被上游拒绝。插件在计费前以明确的 400 拒绝。
- 首图：multipart 文件字段 `input_reference`/`image`（宿主以 data URL 内联，上限 20MiB）→ `metadata.image` → `image` → `input_reference` → 仅一张的 `images`。图片值支持 `file_*`、http(s)/data URL、裸 base64。
- 参考图：`metadata.reference_images` → 两张及以上的 `images`。

校验：时长 ≤ 15 秒；参考图 ≤ 7 张；`grok-imagine-video-1.5` 必须有输入图且不支持参考图；`grok-imagine-video` 使用参考图时时长 ≤ 10 秒；`prompt` 必填。

提交响应须含 `request_id`。轮询 `GET {base}/v1/videos/{request_id}`，使用任务保存的渠道密钥（`model.InitTask` 为 xAI 保存私有 key）。

### thirdpartysd2

上游提交 `POST {base}/v1/video/generate`，响应 `{"task":{"id":...}}`；轮询 `GET {base}/v1/video/tasks/{id}`，响应 `{"task":{status, outputs[], usage{completion_tokens,total_tokens}, error}}`，`error` 为字符串或 `{code,message}`。

请求体：`model`（映射后上游模型）、`content`（`images` 转 `image_url` 项；`metadata.content` 整体替换；移除 text 项后在末尾追加 `prompt`）、白名单字段 `callback_url/service_tier/ratio`（字符串）、`execution_expires_after/duration/frames/seed`（整数，接受数字字符串）、`return_last_frame/generate_audio/draft/camera_fixed/watermark`（布尔，接受 `"true"/"false"`）、`tools[].type`、`resolution`、`duration`（`seconds` 优先于 `duration`，覆盖 metadata）。其他 metadata 字段丢弃，`metadata.model` 不能覆盖模型。

`resolution` 取 `metadata.resolution` 与 `size` 规范化后较高者（`480p/720p/1080p/4k`，`2160p`→4k，`WxH` 取短边分档）。缺少可识别分辨率时插件返回 400。

模型接受的分辨率由价格矩阵决定：宿主在计费前调用 `billing_setting.ValidateForkTaskUsageFacts`，`output_resolution` 不在该模型（客户端模型优先，再取映射后模型）矩阵中时返回 400 `plugin_request_invalid`，如 `dreamina-seedance-2-0-fast-260128 does not support 1080p resolution (supported: 480p, 720p)`。管理员在矩阵中为 fast 模型加入 1080p 后即被接受并按该档计费。模型保存了 `thirdpartysd2::<model>` 插件表达式时由该表达式定价，不做矩阵检查。插件本身不维护按模型的分辨率白名单。

## 级联：下游网关接入

### 认领语义与它带来的限制

上游 #7076 的插件认领语义：模型被插件认领后，只能由该插件声明的 `channelTypes` 渠道，或渠道类型为「任务插件」（`ChannelTypeTaskPlugin = 61`，本分叉为 62）且 `setting.task_plugin_key` 等于该插件 key 的渠道承载（`middleware/distributor.go` 的 `channelMatchesExpectedTaskPlugin`）。

因此，改造成插件后，**用 OpenAI(1)/Sora(55) 等普通渠道类型指向另一台网关来转售这些模型不再可行**：分发阶段就返回 503「该模型由任务插件「thirdpartysd2」认领」。上游自带插件（alibaba/doubao/kling 等）同样受此限制，本分叉不改动认领语义，而是像上游插件那样声明厂商形状的入站路由，让级联改走「任务插件」渠道。

### 入站路由

| 插件 | 方法与路径 | 类型 | 钩子 | 对应上游端点 |
|---|---|---|---|---|
| thirdpartysd2 | `POST /sd2/v1/video/generate` | submit | `native.createTask` / `native.taskCreated` | `POST {base}/v1/video/generate` |
| thirdpartysd2 | `GET /sd2/v1/video/tasks/:task_id` | query | `native.taskStatus` | `GET {base}/v1/video/tasks/{id}` |
| xai | `POST /xai/v1/videos/generations` | submit | `native.createVideoTask` / `native.videoCreated` | `POST {base}/v1/videos/generations` |
| xai | `GET /xai/v1/videos/:request_id` | query（`taskIdParam: request_id`） | `native.videoStatus` | `GET {base}/v1/videos/{request_id}` |

`/sd2`、`/xai` 前缀是必需的：`router/plugin-router.go` 的 `routeIntersectsStaticRoute` 拒绝与静态路由相交的插件路由，而不带前缀的 `/v1/video/generate`、`/v1/videos/:id` 与本网关自身的任务路由冲突。

请求与响应体与厂商 API 同形，因此下游网关运行同一插件即可把本网关当作厂商：

- SD2 提交体即厂商提交体（`model`、`content`、`resolution`、`duration`、白名单字段），`native.createTask` 把整个 body 作为 `metadata`、从 `content` 的 text 项取 `prompt`，因此本网关转发给厂商的请求与入站请求逐字段一致；响应为 `{"task":{id,model,status,created_at}}`。
- SD2 查询响应为 `{"task":{id,status,outputs,usage,duration_seconds,ratio,error}}`：`id` 为本网关公开任务 ID，`status` 由本网关任务状态映射（queued/processing/succeeded/failed），`usage` 透传厂商用量（下游据此按 token 结算）。
- xai 提交体即厂商提交体（`model`、`prompt`、`duration`、`aspect_ratio`、`resolution`、`seed`、`image`、`reference_images`），`native.createVideoTask` 把除 `model`/`prompt` 外的字段作为 `metadata`，转发请求与入站请求一致；响应为 `{"request_id"}`，查询响应为 `{"request_id",status,model,video{url,duration},error}`，状态映射为 queued/processing/done/failed。

### 结果地址：网关相对路径

插件拿不到自己的服务地址（`native` 渲染钩子只有请求上下文与任务视图），因此查询响应里的结果地址是本网关上的**绝对路径** `/v1/videos/{task_id}/content`，而不是厂商地址：

- 厂商地址不外泄：SD2 的输出地址需要渠道密钥，xai 的 CDN 地址也不下发给下游。
- 下游插件（`buildContentRequest`）把以 `/` 开头的地址按渠道 Base URL 的 origin 还原为绝对地址，并附带渠道密钥——该地址就在渠道主机上，符合宿主的 `ValidateRequestURL` 与凭据边界。
- 下游对自己的客户端只暴露自己的内容代理地址：`relay/relay_task.go` 的 `getExternalVideoURL` 把相对结果地址改写为本网关的 `taskContentProxyURL`，xai 的 `openai_video` 渲染只在结果地址是 http(s) 时才给出 `metadata.url`。
- 内容下载始终经网关代理：下游 `GET /v1/videos/:id/content` → 上游 `GET /v1/videos/:id/content`（渠道密钥即上游 token）→ 厂商。

### 下游部署需要的渠道配置

| 项 | 值 |
|---|---|
| 渠道类型 | 任务插件（`ChannelTypeTaskPlugin`，本分叉编号 62） |
| 渠道设置 | `{"task_plugin_key":"thirdpartysd2"}` 或 `{"task_plugin_key":"xai"}` |
| Base URL | `https://<上游网关>/sd2` 或 `https://<上游网关>/xai` |
| 密钥 | 上游网关上的 API token（该 token 的用户即上游任务归属者，轮询与内容下载都用它） |
| 模型 | `dreamina-seedance-2-0-260128` / `dreamina-seedance-2-0-fast-260128`，或 `grok-imagine-video` / `grok-imagine-video-1.5` |

上游网关侧需要配置 `ServerAddress`（或 `TaskPublicAddress`）以便自身对外地址正确；级联结果地址本身是相对路径，不依赖该配置。原先用普通渠道类型转售这些模型的下游渠道必须改成上述配置，否则提交返回 503。

「任务插件」渠道只在宿主协议端点与插件自有路由上参与渠道选择：分发前的 `PinTaskPluginEndpoint` 只认 `pluginruntime.LookupHostProtocolOperation` 登记的协议路径，旧版 `POST /v1/video/generations` 不在其中，`expected_task_plugin_key` 为空时 `FilterTaskPluginIdentity` 会排除类型 62 的渠道。因此下游网关的客户端要走 `POST /v1/videos`（以及 `GET /v1/videos/:id`、`GET /v1/videos/:id/content`）或 `POST /v1/responses`，而不是旧版 `POST /v1/video/generations`。这是上游的既有语义，对所有插件一致。

计费两端各自独立：下游按自己的定价向自己的用户计费，上游按 token（SD2 矩阵表达式）或按次（xai）向下游 token 计费。

## 数据模型变更

无新表、字段或迁移。

- `tasks.platform`：新任务为插件 key；旧 Go 适配器写入的 `"48"` / `"58"` 保持不变。
- `RelayInfo.InheritedPricingMetadata` 已删除（仅旧 SD2 适配器消费）。

## 配置参数

不新增配置。沿用：

- `ModelPrice`：`grok-imagine-video`（默认 0.05）、`grok-imagine-video-1.5`（默认 0.08），即 480p 每秒基础价。
- `thirdpartysd2_pricing.matrix`：SD2 分辨率 × 是否含参考视频的 $/1M tokens 价格矩阵，管理后台「分组与模型计费」中的 ThirdPartySD2 标签页继续生效。
- `billing_setting.plugin_billing_expr`：管理员可为 `xai::<model>` / `thirdpartysd2::<model>` 保存插件表达式，优先级最高。

## 计费

### 插件用量事实

| 插件 | 事实 | 说明 |
|---|---|---|
| xai | `seconds`（second）、`output_resolution`（enum 480p/720p/1080p）、`input_images`（count） | 分辨率为实际发往上游的值；图片按 URL/file_id 去重计数，multipart 上传计 1 |
| thirdpartysd2 | `tokens`（token）、`output_resolution`（enum 480p/720p/1080p/4k，两个模型相同）、`video_input`（enum none/video） | 提交时 `tokens=1,000,000`；完成时覆盖为上游 `total_tokens`，缺失时 `completion_tokens`。fast 模型 profile 只让展示示例保持默认矩阵的 480p/720p |

事实 key 刻意不用 `resolution`：宿主会按 usage schema 递归校验请求体中同名字段，客户端 `metadata.resolution` 传 `1080P`、`1920x1080` 等原先可接受的写法会被 enum 校验拒绝。

### xai：按次价格 × 单位倍率（默认）

模型未配置表达式时走 `ModelPriceHelperPerCall`。`extractUsage` 在 `usagePurpose = "billing_ratios"` 时只返回一个倍率：

```text
xai_video_units = seconds × resolution_ratio + input_images × image_unit
grok-imagine-video:     480p 1.0, 720p 1.4 (0.07/0.05)；image 0.04 (0.002/0.05)
grok-imagine-video-1.5: 480p 1.0, 720p 1.75 (0.14/0.08), 1080p 3.125 (0.25/0.08)；image 0.125 (0.01/0.08)
```

`quota = ModelPrice × QuotaPerUnit × groupRatio × xai_video_units`，按次任务成功后不再差额结算，失败全额退款。

倍率钩子只返回合成倍率，`RelayTaskSubmit` 在按次分支再以事实模式调用一次 `extractUsage`，由 `recordXaiTaskPricingMetadata` 写入 `PriceData.PricingMetadata`（`duration_seconds`、`resolution`、`reference_image_count`、`xai_video_model`），随 `BillingContext` 持久化，在日志 `other` 与任务 DTO 中为 `pricing_*` 字段，与旧 Go 适配器任务同名；读取失败只记警告，不影响提交。`other.xai_video_units` 继续展示。管理员若改用表达式，可使用上表事实，例如：

```text
u("output_resolution") == "720p" ? tier("720p", u("seconds") * 0.07 + u("input_images") * 0.002) : tier("480p", u("seconds") * 0.05 + u("input_images") * 0.002)
```

### thirdpartysd2：价格矩阵生成的任务表达式

价格矩阵可以精确表达为插件表达式，因此 SD2 走标准任务表达式链路，同时保留管理员矩阵：

1. `model_setting.RebuildThirdPartySD2PricingIndex`（启动与 `thirdpartysd2_pricing` 选项变更时）在合并后的矩阵上为每个模型生成表达式，存入原子指针索引：

   ```text
   u("output_resolution") == "480p" && u("video_input") == "none" ? tier("480p", u("tokens") * 7 / 1000000) :
   u("output_resolution") == "480p" && u("video_input") == "video" ? tier("480p_video", u("tokens") * 4.3 / 1000000) :
   ... : tier("4k_video", u("tokens") * 2.4 / 1000000)
   ```

   分辨率按档位排序，最后一个组合作为 else。宿主在求值前用 `ValidateForkTaskUsageFacts` 拒绝矩阵中没有的分辨率，而每个分辨率都同时有无/有参考视频两个价格，所以 else 分支不会承接未定价的组合。

2. `billing_setting.ResolveTaskBillingExpr` 在插件显式覆盖之后、模型级表达式之前调用 `resolveForkTaskBillingExpr`（`setting/billing_setting/fork_task_billing.go`）：插件为 `thirdpartysd2` 时按客户端模型、再按映射后模型取矩阵表达式。

3. `RelayTaskSubmit` 因此进入 tiered 分支：以 `tokens=1,000,000` 求值预扣（等于旧适配器 `单价 × 1M tokens` 的预扣），冻结表达式与事实到 `BillingSnapshot`。

4. 轮询成功时 `extractUsageOnComplete` 返回 `{tokens}`，`settleTaskBillingOnComplete` 覆盖事实后重新求值并差额结算，结果等于旧适配器 `tokens × 单价/1M × QuotaPerUnit × groupRatio`。上游无用量时保留预扣（与旧行为一致）；失败任务全额退款。

5. 日志 `other` 带 `billing_mode=tiered_expr`、`expr_b64`、`matched_tier`（如 `1080p_video`）、`usage_facts`。

矩阵表达式不写入 `billing_setting`。公开定价 `GET /api/pricing`（`model.updatePricing`）在模型没有模型级表达式时调用 `billing_setting.ResolveForkPublicTaskBillingExpr(pluginKey, model)`：对 thirdpartysd2 模型返回实际计费表达式（插件覆盖优先，其次矩阵），填入 `billing_mode=tiered_expr` / `billing_expr`，定价页按任务表达式渲染各档价格；其他插件不受影响。fast 模型的 schema 含四档而默认矩阵只有两档，定价页最后一档以 tier 名（`720p_video`）显示，不展开条件。

### 任务计费维度的展示

| 位置 | 新任务（插件） | 旧 Go 适配器任务 |
|---|---|---|
| 任务列表成本列（`task-logs-columns.tsx`） | xAI 读 `pricing_*`；SD2 读 `usage_facts` | 读 `pricing_*` |
| 管理员日志详情「用量参数」（`log-detail-body.tsx`） | 有 `usage_facts` 时逐项列出；没有时把 `pricing_duration_seconds/resolution/reference_image_count/video_input` 以 `seconds/output_resolution/input_images/video_input` 列出，并附 `xai_video_units` | 同左 |
| 日志导出 | 管理员「Raw Other JSON」列包含上述字段，没有专门列 | 同左 |

## 旧任务兼容

上游对自身迁移的适配器保留 `relay/relay_adaptor.go` 的 `taskPluginKeys`，把旧的数字平台映射到插件 key。本分叉追加：

- `"48" → xai`、`"58" → thirdpartysd2`，`GetTaskAdaptor` / 轮询 / realtime fetch / 协议查询均可解析旧任务。
- 数据格式未变：旧 xAI 任务 `Data` 为 `{"request_id"}` 或轮询体，旧 SD2 任务为 `{"task":{...}}`，插件按同样结构解析。
- 旧任务的 `BillingContext` 未变：旧 xAI 任务为按次计费（成功不结算）；旧 SD2 任务带矩阵换算的 `ModelRatio` 与 `PricingMetadata.pricing_mode=resolution_video_matrix`，插件 `parseTaskResult` 返回 `completionTokens/totalTokens`，由 `RecalculateTaskQuotaByTokens` 结算。
- `RecalculateTaskQuotaByTokens` 与上游一致：按全局 `GetModelRatio` 与分组倍率计算，模型未配置倍率或倍率 ≤ 0 时返回 false 并保留预扣，倍率为 0 的按次任务不会按 0 额度全额退款。例外是旧 SD2 任务（`BillingContext` 无表达式快照、非按次，且 `pricing_mode=resolution_video_matrix` 或平台 `"58"`）：全局倍率中没有这些模型，改用快照里的 `ModelRatio`/`GroupRatio`，倍率 ≤ 0 同样不结算。
- 内容访问：旧任务没有插件执行记录，`VideoProxy` 回落到 `ResultURL`；`controller/video_proxy.go` 的 `thirdPartySD2ContentRequest` 对平台 `"58"` 的旧任务使用与插件相同的凭据策略：结果地址主机为渠道 Base URL 主机或 thirdpartysd2 插件 `allowedHosts` 中的主机时附带渠道 Bearer（`jsplugin.ValidateRequestURL` 判定），其他主机无凭据访问。
- 结果地址：SD2 的上游输出地址需要渠道密钥。插件仍返回 `url`，宿主写入 `PrivateData.ResultURL`（旧任务轮询完成后也可继续下载）；`relay_task.go` 的 `isThirdPartySD2Task` 同时匹配 `"58"` 与 `thirdpartysd2`，`getExternalVideoURL` 对外只返回网关内容代理地址。`service/task_polling.go` 在 SD2 渠道成功但无输出时不构造代理地址。
- `openai_video.render` 不回传 `task.data`（SD2 的 `outputs` 为私有地址），失败时从 `task.error` 取 message/code。插件拿不到服务地址，由宿主补齐：`GET /v1/videos/:id` 的 `videoFetchByIDRespBodyBuilder` 对成功且有上游输出的 SD2 任务（新旧平台）调用 `withThirdPartySD2ContentURL`，在 `metadata.url` 写入绝对内容代理地址 `{TaskPublicAddress，未配置时 ServerAddress}/v1/videos/{task_id}/content`（两者都未配置时为相对路径），保留渲染出的其他 metadata；`TaskModel2Dto` 的 `result_url` 使用同一地址。xAI 结果地址为公开 CDN，`metadata.url` 由插件返回。

保留的分叉代码及原因：

| 位置 | 原因 |
|---|---|
| `relay/relay_task.go` `getExternalVideoURL` 等 | 隐藏 SD2 私有输出地址（新旧任务） |
| `relay/relay_task.go` `withThirdPartySD2ContentURL`、`recordXaiTaskPricingMetadata`、`ValidateForkTaskUsageFacts` 调用 | SD2 `metadata.url`；xAI 计费维度；SD2 分辨率按矩阵接受 |
| `controller/video_proxy.go` `thirdPartySD2ContentRequest` | 旧 SD2 任务无插件执行记录，内容代理需渠道密钥（仅渠道主机与声明主机） |
| `model/pricing.go` 调用 `ResolveForkPublicTaskBillingExpr` | 公开定价页展示 SD2 矩阵价格 |
| `service/task_billing.go` `isLegacyThirdPartySD2BillingContext` | 旧 SD2 任务 token 结算读取快照倍率 |
| `service/task_polling.go` SD2 守卫、`formatPollingTaskLogID` | 无输出时不暴露空代理地址；日志带上游 ID |
| `controller/channel-test.go` 不支持测试列表中的 SD2 | 类型 58 映射到 OpenAI API 类型，对话测试没有意义 |
| `controller/model.go` `channelOwnerName` | `/v1/models` 的 `owned_by` 保持 `third-party-sd2` |
| `setting/model_setting/thirdpartysd2_pricing.go` | 价格矩阵与表达式生成 |

## 关键业务逻辑与边界

- 模型相关校验（xAI 1.5 必须有图、SD2 fast 分辨率）放在 `buildSubmitRequest`，因为协议 `decodeRequest` 阶段只有客户端模型（可能是别名），渠道映射后才有上游模型。
- xAI 轮询：空状态且无 error → IN_PROGRESS；无状态但有 error → FAILURE；`expired` → FAILURE（`task expired`）；未知状态 → UNKNOWN（宿主累计轮询失败）。
- SD2 轮询：未列出的状态保持 IN_PROGRESS 30%（第三方服务未公开完整状态集，旧适配器同样处理），由宿主任务超时兜底；缺少 `task` 对象抛错计轮询失败。
- SD2 内容请求：输出地址主机（小写、去掉默认端口、忽略 userinfo）等于渠道 Base URL 主机或插件常量 `CREDENTIAL_HOSTS` 中的主机时带 `Authorization: Bearer`；其他主机按 credentialless 访问，渠道密钥不会发往第三方主机。`CREDENTIAL_HOSTS` 同时作为 `meta.allowedHosts`，宿主对带凭据的内容请求按同一列表校验。旧 Go 适配器测试与 fixture 中服务商的输出都在渠道自身主机（`/files/...`），没有其他主机的依据，因此内置列表为空；服务商把需鉴权的下载放在其他主机时，管理员上传覆盖插件，在 `CREDENTIAL_HOSTS` 中列出该主机（`host` 或 `host:port`）。内容始终经网关代理。
- xAI multipart 上传通过 `__fileRef` 占位由宿主内联为 data URL，JS 不接触文件字节。
- 插件图标：xai 使用 LobeHub `Grok`，thirdpartysd2 使用 `ByteDance.Color`。

## 错误处理

- 插件抛出的校验错误为面向用户的英文句子，宿主包装为 400 `plugin_request_invalid` / `plugin_usage_invalid`。
- 上游提交非 200 走 `RelayErrorHandler`；提交响应缺 ID 为 502 `plugin_submit_response_failed`。
- 矩阵表达式求值失败在提交时返回 400 `model_price_error`，结算时保留预扣并记警告。

## 与现有子系统的关系

- 计费：xAI 使用按次价格 + `OtherRatios`；SD2 使用任务表达式快照与完成结算，均为宿主既有链路，未新增结算路径。
- 定价设置：`ResolveTaskBillingExpr` 新增一个仅对 `thirdpartysd2` 生效的回退；其他插件和模型不受影响。
- 模型列表：`DashboardListModels` 按上游逻辑用插件模型替换渠道类型模型列表，因此 xAI 渠道类型的默认模型列表只含两个视频模型（与上游 sora 替换 OpenAI 类型一致）。

## Main Chain Impact

同步执行在任务提交请求 goroutine 上：

- 插件 `buildSubmitRequest`、`extractUsage`（xAI 按次模式 2 次：倍率与计费维度元数据各 1 次；SD2 表达式模式 1 次）——与所有上游内置插件相同的 JS 调用，受宿主执行超时和并发池限制。
- `ResolveTaskBillingExpr` 的 SD2 回退与 `ValidateForkTaskUsageFacts`：字符串比较、`atomic.Pointer` 读取和 map 查找（分辨率列表至多 4 项排序）；表达式编译由 `billingexpr` 按字符串缓存，矩阵不变时不重复编译。
- `GET /v1/videos/:id` 对成功的 SD2 任务多一次 JSON 解码与编码。

异步/后台：轮询、完成结算、退款、日志沿用宿主后台任务。

矩阵表达式只在启动和管理员保存 `thirdpartysd2_pricing` 时重建（管理路径）。

聊天主链路（`/v1/chat/completions` 等）不经过任务插件代码；xAI 渠道类型 48 被插件声明只影响任务路由的渠道选择与模型列表展示。

## Shared Resource Audit

| 资源 | 访问 | 主链路是否访问 | 结论 |
|---|---|---|---|
| Redis | 无新增 key | — | 无冲突 |
| `tasks` 表 | 宿主既有写入/轮询查询 | 任务提交路径 | 未新增查询形态 |
| `channels` / 额度 / 日志表 | 宿主既有预扣、结算、日志 | 是 | 未新增访问 |
| `options` 表 | `thirdpartysd2_pricing` 既有选项 | 否 | 管理路径 |
| `thirdPartySD2PricingIndex`（`atomic.Pointer`） | 重建时整体替换，读取无锁 | 仅 SD2 任务提交 | 无锁竞争 |
| `billingexpr` 编译缓存 | 按表达式字符串缓存 | 是（所有表达式计费） | 每个 SD2 模型 1 条，随矩阵变更增长有限 |
| JS 插件运行时池 | 每插件独立池 | 仅任务路由 | 与其他插件隔离 |
| HTTP 客户端池 | `service.GetHttpClientWithProxy` | 是 | 未新增连接池 |

## Concurrency Analysis（100k RPM 任务提交）

- 新增 DB 调用：0；新增 Redis 调用：0；新增锁：0；新增 goroutine：0。
- 每次提交 JS 调用次数与上游内置插件一致（decode ≤2、buildSubmit 1、usage 1–2、parseSubmit 1）；xAI 按次提交 usage 为 2 次。
- SD2 表达式解析额外成本为常数级 map 查找。

## 测试

`plugins/xai_responses_test.go`、`plugins/thirdpartysd2_responses_test.go`（全部 fixture / `httptest`，不访问真实上游）：

- Responses 协议通用契约（`testVideoResponsesProtocol`）。
- 提交 URL/头/体转换、默认值、别名映射、参考图/首图/base64/multipart 占位。
- 校验错误（xAI 时长/参考图/1.5 首图/未知分辨率文案；SD2 分辨率缺失/prompt/metadata 类型）；SD2 分辨率按矩阵接受（fast 默认拒绝 1080p，矩阵加入 1080p 后接受并按该档计费）。
- xAI `xai_video_units` 倍率表与表达式事实；SD2 事实、矩阵表达式各档价格、完成覆盖结算、矩阵变更后表达式重建、`TaskExprCompatible`。
- 提交响应解析、轮询端点与鉴权（`httptest.NewServer`）、状态映射（含 SD2 字符串/对象错误）。
- 内容请求（xAI credentialless；SD2 同主机与默认端口带鉴权，异主机与 userinfo 伪装按 credentialless，`CREDENTIAL_HOSTS` 声明的主机带鉴权并进入 `allowedHosts`）、`openai_video` 渲染不泄漏 SD2 输出地址。
- 旧平台 `"48"`/`"58"` 解析到插件并可解析旧数据，`TaskModel2Dto` 对新旧 SD2 任务只暴露代理地址。
- `plugins/builtin_plugins_test.go` 登记两个内置 key 与渠道类型。
- `plugins/inbound_cascade_routes_test.go`：入站路由登记（方法/路径/类型/钩子/`taskIdParam`）、`native` 解码后转发给厂商的请求与入站请求逐字段一致、渲染出的提交/查询响应被同一插件的 `parseSubmitResponse`/`parseTaskResult` 解析、输出地址为网关相对路径且不含厂商地址、下游 `buildContentRequest` 按渠道 origin 还原并附带密钥、入站校验错误。
- `relay/relay_task_fork_test.go`：内容地址优先 TaskPublicAddress、`metadata.url` 补齐及不改动的情形、xAI 计费维度元数据。
- `controller/video_proxy_sd2_test.go`：旧 SD2 内容请求只向渠道主机发送密钥。
- `setting/billing_setting/fork_task_billing_test.go`：公开定价表达式解析、矩阵分辨率校验（含映射模型与插件覆盖）。
- `model/pricing_fork_task_expr_test.go`：定价 API 返回 SD2 矩阵表达式。
- `service/gen3_taskbilling2_test.go`：token 重算——倍率 0 不结算、旧 SD2 读快照、正常倍率差额结算。
