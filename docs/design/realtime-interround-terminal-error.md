# Realtime 轮间异常补发错误

## 授权、目标与范围

用户确认“Realtime 漏补错误这个问题也按推荐修复”。修复审计 A2：至少一轮正常 response.done 后，下一轮尚未开始，上游异常断开或轮间超时已产生诊断，但下游没有 error 消息。

仅调整受管 Realtime 的轮间终止分支。活动轮次、首轮尚未完成、正常关闭、原始上游错误及预留失败已有的终止入口保持原处理；非流式、失败握手与普通 HTTP 流不改变。

## 数据流与业务逻辑

真实 WS 101 → 既有双向消息转发 → 每轮选择用量、累计并预留 → 创建下一轮空会话 → 轮间异常 → 只补发终止错误 → 汇总原累计用量/诊断 → 关闭连接及等待原读取者退出 → 外层既有一次资金结算与异步日志。

- active=true 或没有已完成轮次：继续 finishRound，不增加第二个补发入口。
- 已完成轮次且 active=false：保留原账本，只有存在结束错误、来源判定为上游异常且下游仍可写时，调用现有 WriteStreamTerminalError。
- 受管请求超时导致的 context canceled，在轮间按 timeout 收尾，不作为用户主动断开；沿用现有专用超时错误写入通道。
- 普通用户取消、下游关闭不补发；正常 1000/1001 关闭不补发。
- 已转发上游 error 不重复发送；预留失败已经发送的错误不在轮间再次发送。
- 轮间不调用 finishRound、FinalizeStreamUsage、Reserve、Settle 或 Refund，不虚构空轮次用量、不重计已完成轮次，也不退款已完成轮次。

## API、数据模型、配置与错误处理

复用现有 Realtime JSON 文本帧：type=error，error.type=server_error，error.code=upstream_stream_error；公开 message 使用现有翻译，不暴露底层原因。不插入 SSE 外壳，不补造 response.done，不改已完成的 101 握手。

无新接口、字段、数据库结构/索引、迁移或配置；保持 stream_error_setting.enabled、成功响应门控、capture_response 和 Root 诊断权限。复用有界响应采集器记录实际成功写出的错误载荷，随后生成诊断快照；写失败时不伪造已发送正文。

## Main Chain Impact

同步：仅轮间异常结束时读取一次请求局部快照和超时来源，最多通过原 WS 写入器补发一次错误；沿用 15 秒写截止时间。正常转发热循环无新工作。异步：既有日志/资金外围机制不变，无新后台任务。

## Shared Resource Audit

只使用既有请求局部 StreamSession、StreamOutcome、诊断缓冲和 ClientWs；快照锁在写入前释放。无新增 Redis、DB、跨请求缓存、连接池、工作池或文件句柄；原读取者数量与退出等待保持不变。不额外访问 BillingSession。

## Concurrency Analysis

30k/100k RPM 下新增 DB/Redis 调用、协程、定时器及跨请求锁均为 0；仅命中轮间异常时增加一次有界小 JSON 写入。原正文预算、读工作者与写截止时间不变；负载压测仍在部署环境执行。

## 测试与实施步骤

先添加真实本地 WS 回归并确认原实现漏发，再实施轮间只补错误分支。覆盖一轮/多轮完成后的异常断链、正常关闭、原始 error、普通取消、受管超时、预留失败，检查 error 次数、JSON 格式、费用累计、Reserve/Settle/Refund 次数、原始下游诊断正文和公开来源标记。

只使用 httptest 与内存计数资金替身，不操作真实余额或上游 AI。重跑 Realtime、通用流式及渠道回归、根构建和格式检查；更新原审计及相关设计说明。

## 实施结果

已完成，业务改动仅位于 `managedRealtimeHandler` 的原轮次结算分支之后：增加互斥的轮间补发分支。补发前检查原上游来源、已交付错误、客户端取消及受管超时，不改动正常消息循环、读取者、原始错误转发和逐轮资金逻辑。

- 新回归在修复前明确复现：一轮后异常断链、两轮后异常断链和轮间受管超时均期望一条 error，实际为零。
- 修复后新增 9 种真实本地 WS 场景通过：上述三种异常，以及正常关闭、GoingAway、上游原始 error、客户端关闭、普通 context 取消、预留失败。
- Realtime 回归连续重复 50 次通过，验证已完成轮次的输入/输出用量不变；轮间失败没有新增 Reserve，处理器没有调用 Settle/Refund；原错误和已有预留错误均无重复补发。
- 下游错误为 JSON 文本消息，而非 SSE；新诊断保存的下游 body 与实际接收字节一致，包含补发 error。用户取消/正常关闭不因此新增上游诊断正文。
- `go test ./relay/... ./logger ./common ./router ./setting/operation_setting ./i18n` 与 `go build ./...` 通过。中文注释、gofmt、UTF-8 无 BOM 和差异空白检查通过。
- 原审计复现变为 `failed=true diagnostic=true error_event=true`；同时重跑 A1 复现，尾帧仍完整保留。

错误写入沿用既有截止时间，最多尝试一次；仅记录成功写出的字节，不承诺客户端应用已消费。原活动轮次和首轮结算路径不在本次重写范围。

无 API、价格、数据结构、权限、前端、依赖或部署变更，未提交 Git。未运行真实数据库/上游集成及容量压测；当前 CGO 工具链限制下未运行 race 检测。
