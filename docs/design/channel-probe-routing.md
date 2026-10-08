# 渠道探针分流：短输入与请求结构识别

日期：2026-09-29  
状态：实现完成，相关回归、编译与前端检查通过。

## 1. 目标、范围与识别

渠道编辑增加“禁止探针请求”，默认关闭。**没有全局启用开关**；全局仅配置统一输入字符阈值，默认 128。正常短问答也可能匹配，但仍由同组真实上游响应和计费，不伪造 OK。

不使用固定文案、User-Agent、IP、周期或缓存命中进行识别。实际缓存命中在上游响应后才知道，历史未命中不能证明本次未命中，普通缓存参数也不单独影响结果。该功能是启发式路由策略，不是访问控制安全边界。

支持客户端入口 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`，不限最终渠道类型。Gemini 原生入口、Realtime、Responses compact、Playground 等暂不识别。平台手动/定时渠道测试、故障恢复和真伪检测仍测试原渠道。

必须同时满足：

- POST、有效 JSON/UTF-8、BodyStorage 在内存，实际请求体不超过 16 KiB；不因识别额外读取网络或磁盘。超限或未知结构按普通请求处理。
- 完整输入解码后的 Unicode code point（Go rune）总数不超过阈值。包括 system/developer/instructions/user 文本；空白也计数，user 至少有一个非空白字符。组合 emoji 可能由多个 code point 组成。
- 恰好一次 user 输入，可以有前置系统指令；没有 assistant 历史、多个 user 回合、工具调用或工具结果。
- 内容是字符串或纯文本块；图片、音频、视频、文件和未知内容块不匹配。
- tools/functions/tool_choice/function_call 缺省、null、空数组或明确 none 可视为不启用；Claude 的 `{"type":"none"}` 也允许。
- 无 previous_response_id、conversation、外部 prompt、context_management、cached_content 等外部内容引用，因为这类请求的完整输入无法从当前正文获知。普通 prompt_cache_key、retention、文本块 cache_control 不影响判定。
- JSON/schema 输出不匹配，普通文本格式允许；n 缺省或为 1。stream、max_tokens/max_output_tokens、temperature、推理配置不作为探针条件。

Responses 使用 input 与 instructions；Chat 使用 messages；Claude 使用 messages 与 system。截图中的“在吗”+短 instructions 会匹配，`max_output_tokens=8296` 不计入输入长度。

实现位于 `pkg/proberouting`：顶层单次遍历，字符累计超限即停止，每请求只识别一次，重试复用，不调用 tokenizer 或对 DTO 序列化往返。

## 2. 数据流与路由规则

```text
既有鉴权/请求解析
→ 读取有效策略快照（没有禁止渠道则跳过识别）
→ 请求特征识别、保存请求级快照与标记
→ 根据原权限确定实际分组/模型/路径候选
→ 在优先级分层前过滤禁止探针渠道
→ 原优先级/权重选择 → 设置实际渠道和 key
→ 既有预扣、上游调用、结算与日志
→ 真实上游失败重试保持同组及同一策略快照
```

- “同组”是当前请求实际路由组，不是渠道标签，也不是渠道所有分组的并集。
- auto 沿原允许列表找有候选的实际分组；该组有候选但全部禁止时立即返回 503，不推进到后续组。选中后后续重试锁定实际组，不重置重试次数及总尝试预算。
- 固定分组只在该组内选择；保持原模型精确名/规范化名与路径兼容规则，不因策略过滤为空新增模型兜底。
- 高优先级全部禁止时选下一层，同层沿用原权重。策略过滤不算上游尝试。
- 指定渠道禁止探针返回 403，不解除显式绑定；指定允许渠道继续使用。
- 疑似探针绕过亲和性读取和记录，不删除或覆盖正常会话记录。
- 被跳过的渠道不取 key、不调用、不计费、不记失败、不自动封禁。无候选在进入 relay/controller 预扣前结束。
- 真实上游失败仍走原重试条件和时长预算；已经开始返回响应时不新增切换行为。
- 请求持有入场快照，管理员修改影响后续新请求。

缓存路径在现有候选遍历中增加 ID 集合过滤。无内存缓存路径将原候选查询扩展为一次读取各优先级，先过滤再选层级；不循环查询、不逐渠道补读设置。

相关设计：`relay-retry-time-budget.md`、`channel-daily-quota-limit.md`。

## 3. API contracts、权限、数据模型

不新增端点、表、列、迁移或索引。

- 渠道字段为既有 `channels.settings` 文本 JSON 内的 `disable_probe_requests`，bool，默认 false。
- 沿用 `GET /api/channel/:id`、`PUT /api/channel/`、创建/复制接口以及 AdminAuth、ChannelRead/ChannelWrite 和既有敏感设置校验，不放宽权限。
- 客户端合并已有 settings；未携带新字段时保留旧策略，显式 false 关闭。未提交 settings 时保留原 settings，避免覆盖其他配置。
- `relaykit/dto.ChannelOtherSettings` 只增加纯配置字段，不导入根模块、不透传上游。

更新请求相关片段（客户端仍需保留其他设置）：

```json
{"id":442,"settings":"{\"disable_probe_requests\":true}"}
```

全局阈值为 `probe_routing_setting.max_input_chars`，整数 1–1024，默认 128。经 `setting/operation_setting` 和 ConfigManager 原子快照注册、保存、加载；沿用 settings API，加入 `models.routing-reliability` 的字段与配置组白名单。逐字段/配置组更新均校验范围。没有 enabled 配置。

Relay 策略错误遵循原 relay 响应形状：

| 场景 | HTTP | code |
| --- | --- | --- |
| 同组无接受探针的候选 | 503 | probe_channel_unavailable |
| 指定渠道禁止探针 | 403 | probe_channel_forbidden |

提示用户稍后重试、调整绑定或联系管理员，不泄露渠道列表、密钥或 Go 错误。管理端新增校验错误使用 ApiErrorI18n；底层错误只写日志。新增后端消息覆盖实际的 en/zh-CN/zh-TW YAML 文件。

不使用 JSONB 或数据库 JSON 查询；文本列及 GORM 逻辑兼容 SQLite/MySQL/PostgreSQL。

## 4. 界面和完整生命周期

渠道编辑 → 高级设置 → 路由策略中显示“禁止探针请求”，紧接“自动封禁”开关之后，所有渠道类型可见。复用该区域的 Switch 布局，保留敏感设置编辑权限控制；配置状态计入路由策略导航标记，不再计入渠道额外设置。说明普通短问答也可能分流、平台渠道测试不受影响。保存可回显，复制继承。

系统设置 → 模型 → 路由可靠性显示“探针识别长度（字符）”，没有全局开关。说明以 128 为例解释输入长度、系统提示词累计和请求特征，并说明同组分流以及调大/调小阈值的影响。界面文案覆盖七语言，沿用主题 token。

策略快照：

- 独立不可变的禁止渠道 ID 集合及刷新时间，atomic.Pointer 发布；请求不获取管理锁。
- 启动/独立后台任务每 60 秒刷新；内存渠道缓存关闭也运行。管理/后台 `InitChannelCache` 完成后刷新本节点。
- 刷新只查询 `channels.id, settings`。独立管理 mutex 串行整个刷新，避免旧查询覆盖后来的保存；该锁不在 relay 路径。
- 单次 DB context 超时 3 秒，最多一个刷新任务占用一个连接。失败或 panic 保留旧快照并记录系统日志。
- 无快照或最后成功刷新超过 180 秒时 fail-open，使用原选路，不为此阻断主链。
- 多实例通常在一个刷新周期内收敛，不承诺全节点立即生效。关闭渠道开关是该渠道的回滚方式。
- 请求结束后旧快照可回收；删除、复制等管理动作通过刷新反映，不维护永久请求历史。

## 5. Main Chain Impact

同步新增：一个快照读取、受限内存扫描/字符累计、请求级元数据及原候选遍历中的 ID 查询。

新增每请求 DB/Redis 调用、磁盘读、远程请求、tokenizer、等待、goroutine、分布式锁均为 0。没有任何禁止渠道时不扫描正文。识别异常只在分类调用边界恢复为普通流量并记录警告，不吞后续 handler panic。

后台执行策略刷新及错误报警；消费/错误日志复用既有异步管线。策略拒绝是业务结果，不触发上游失败计数或渠道封禁。

## 6. Shared Resource Audit

| 资源 | 主链共享与隔离 |
| --- | --- |
| channels | 原主链已使用；新增策略仅在管理/后台查 id/settings，不新增逐请求查询、行锁或计数写入 |
| abilities | 原选路资源；复用原候选查询，不写探针状态、不循环查询 |
| channelSyncLock / 渠道缓存 | 沿原读锁范围执行过滤，不修改共享 Channel；策略发布使用独立管理锁 |
| 探针策略快照 | 新的不可变 ID 集合，O(渠道数)，请求只读，刷新时分配 |
| BodyStorage | 复用内存数据，16 KiB 上限，不新增磁盘读/文件句柄 |
| Redis / 亲和性 | 不新增 namespace 或调用；疑似探针绕过亲和性读写 |
| DB 连接池 | 后台串行占一个连接，3 秒超时，频次与请求量无关 |
| 日志队列 | 复用原消费/错误事件，不为每个过滤候选新增日志行 |
| 用户、令牌、账单 | 只保留最终真实调用的既有计费流程 |

已检索 InitChannelCache 的生产调用点：启动、管理、后台任务及渠道凭据/模型管理；新刷新没有注入 relay 请求栈。

不新增增长表或日志查询。策略刷新是有限渠道配置表的后台扫描，不扫请求日志，不新增需要覆盖索引的查询模式。上线前应确认实际渠道规模及 DB 连接池余量。

## 7. Concurrency Analysis 与耗时实测

30k RPM = 500 RPS；100k RPM ≈ 1667 RPS。每请求新增 DB/Redis/goroutine 均为 0。分类最多扫描 16 KiB；过滤 O(C)，C 为本组模型候选数。

2026-09-29，本机 Windows/amd64、Intel i5-12600KF、Go benchmark，GOMAXPROCS=16，重复测量：

| 操作 | 均值范围 |
| --- | --- |
| 截图同形的短 Responses 请求分类 | 0.69–0.72 μs |
| 4 KiB 长输入分类并排除 | 4.72–4.81 μs |
| 16 KiB 边界请求分类 | 19.21–19.57 μs |
| 有工具请求提前排除 | 0.21–0.25 μs |
| 100 候选原选路 | 2.74–2.84 μs |
| 100 候选增加过滤、全部允许 | 3.09–3.15 μs |
| 1000 候选原选路 | 25.83–26.78 μs |
| 1000 候选增加过滤、全部允许 | 30.52–32.28 μs |

分项估算：短请求识别+1000 候选过滤新增约 5–7 μs；16 KiB 分类+同样过滤新增约 24–26 μs。**这是分项均值相加，不是中间件端到端实测或延迟上界。**

100k RPM 按 26 μs/请求估算，新增 CPU 约 43 ms/秒，即单核约 4.3%，不含原业务和上游耗时。短请求分类分配 112 B/op，16 KiB 请求分配 16 KiB/op；极端全为边界请求时约 27 MB/s 分配，需要关注目标机器 GC。

1000 候选并行 benchmark 原选路约 5.58–5.61 μs/op，增加过滤且全允许约 6.62–6.74 μs/op。这是并行吞吐折算，**不是单请求延迟**，也不包含配置发布写锁竞争。

初版重复读取字段导致 16 KiB 约 93–101 μs，已改为顶层单次遍历降低额外扫描成本。

复现：

```text
go test ./pkg/proberouting -bench BenchmarkClassify -benchmem -count=3
go test ./model -run 'TestProbe|TestPreserveChannelProbe' -bench BenchmarkProbeChannelSelection -benchmem -benchtime=500ms -count=2
```

本机微基准没有 DB 或真实上游网络计时，不能承诺线上 P95/P99 < 1ms。部署前应在目标环境使用 mock 上游，以 500/1667 RPS 混合流量比较增量 P95/P99、CPU、GC 及配置刷新影响。无缓存模式仍受既有 DB 延迟影响。

## 8. 可观测性、测试与验收

`other.admin_info.probe_routing` 在消费/错误事件中记录 `short-text-v1` 规则版本、字符数、实际组、最终渠道 ID。沿用 admin_info 的管理员可见性；不存正文、凭证或完整被过滤列表。无候选直接沿中间件错误路径返回，不创造伪上游尝试。

测试包括：

- 任意文案、三协议、截图、Unicode/JSON 转义、空白、127/128/129 字符、16 KiB 边界。
- 系统文本累计、多轮/assistant/工具/工具结果、多模态、会话续接、未知块、结构化输出、n 和缓存参数独立性。
- 优先级过滤、全部禁止、无模型候选、DB/缓存一致、策略过期、入场快照与新配置隔离。
- 真实项目 MySQL 测试行（按 ID 清理），本地 httptest 上游：禁止渠道零调用、备用调用原正文、指定渠道 403、无候选在下游计费 handler 前返回 503。
- auto 不因过滤或重试逃逸，策略过滤不消耗尝试次数；设置范围与权限白名单，不存在 enabled。
- 分类及 1/10/100/1000 候选与并行选路 benchmark。

验证结果：

- 完整相关包测试通过：pkg/proberouting、model、middleware、service、controller、service/settingsaccess、setting/operation_setting、router；最后的小范围修改另运行探针专项回归通过。
- 根模块构建通过，relaykit 以 GOWORK=off 独立构建通过。
- `bun run typecheck`、`bun run lint` 通过；lint 仅保留已有 footer 的 dangerouslySetInnerHTML warning，没有新增 error。
- 七语言通过受控脚本和 i18n:sync 更新；临时脚本已删除，无新增测试框架。
- 原子快照并发读取/发布测试通过；当前机器 CGO_ENABLED=0 且没有可用 C 编译器，未运行 race detector。没有声称完成线上压测或浏览器交互验收。
- `git diff --check` 通过。

## 9. 香港测试服务器验证

2026-09-29 已部署至香港测试服务器，并用隔离分组中的真实渠道副本验证。实测发现 controller 的错误日志路径缺少探针标记，已补充回归测试、修复并发布 `v0.0.0-f0b-probe-20260929.2`；成功与错误日志均在服务器复核通过。

香港机器上分类器短请求均值约 0.0038ms，16 KiB 边界约 0.0923ms。真实原生 Claude 上游存在 401/503，未将其成功生成计为通过；Messages 协议入口使用真实 OpenAI 后端验证成功。临时账号、渠道、令牌和分组均已清理。

完整部署、验证范围、限制、清理与证据见 [香港测试服务器部署与验证](channel-probe-routing-hk-validation.md)。
