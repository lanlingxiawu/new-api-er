# 9 月 10 日及后续流式改动审计

后续已确认变更：受管流大小预算从本报告审计时的 8 MiB 提升至 100 MiB，详见 [大小上限实施记录](stream-size-limit-100mib.md)；本报告的历史风险与微基准不构成新上限下的容量保证。

## 范围与结论

静态审计范围：`ccb9e01bc^..ddb988d77`，包含 9 月 10 日 `ccb9e01bc`、9 月 12 日 `a6c693898`、9 月 14 日 `ddb988d77`。净差异 205 个文件，其中 Go 生产代码 82 个、Go 测试 87 个，其余为设计文档、前端和翻译。另复核本次用户断开估算补丁。

覆盖入口/开关/重试、HTTP 与 WS 成功门控、扫描与关闭、SSE/JSON/SDK/原生媒体、Claude/Responses/Realtime 工具计数、确认用量与估算选择、预扣/结算/成本流水、日志隐私/Root 权限、前端诊断与翻译。验证使用本地 fixture，未调用真实上游，未访问生产账单，未部署。

本次已修复用户主动断开无确认用量时直接清零的问题。有确认用量采用确认值；无确认且已收到业务响应采用输入与接收输出估算；零响应免收费。下列发现超出已确认修复范围，保留原代码，待用户决定。

## 发现 1 — P1：Realtime 钱包耗尽后仍可继续已有连接

归属：9 月 14 日的新调用链。`relay/channel/openai/managed_realtime.go` 的 `finishRound` 调用 `service/stream_realtime_reservation.go:ReserveRealtimeStreamUsage`，再进入 `BillingSession.Reserve`。

`service/billing_session.go:reserveFunding` 钱包分支直接调用 `model.DecreaseUserQuota`，其合同允许负余额，余额不足不返回错误。令牌额度检查是独立条件，使用不限额令牌或剩余令牌额度较高时不会阻止该路径。外层只有 Reserve 返回错误才结束连接，因此初次连接通过后，后续轮次可能持续透支钱包。

对照：旧 `service/quota.go:PreWssConsumeQuota` 在逐轮扣款前明确执行 `userQuota < quota` 与令牌余额检查。ForcePreConsume 只禁止信任免预扣，不等于恢复每轮钱包余额校验。

影响：新流程仍记录费用，但可能形成持续扩大且收不回的欠费；这不是本次断开估算问题。建议将已发生费用的结算与是否准许后续轮次分开，使用受限额的后续轮次预留；保持已发生费用的真实结算，避免把失败预留误作免费。

验证：调用链与资金函数静态确认；本次未做真实钱包集成写入。

## 发现 2 — P1 财务策略风险：上游异常无有效交付仍会漏记成本

归属：9 月 10 日 Claude 策略，9 月 14 日扩大到全渠道。

`relay/common/stream_outcome.go:SelectUsageSource` 首先检查 `Failed && !ClientGone && !EffectiveContent`，即使存在上游确认用量也选择 none。`service/text_quota.go` 将 Quota 与 LedgerQuota 同时置零，`service/consume_settlement.go` 零费用异常只记录错误日志并返回，不进入成本入队。

复现：上游确认输入 100/输出 7，但没有完整成功写出正文，随后上游异常；选择结果仍为 none。本地纯函数探针也验证了 `Failed=true, ConfirmedUsage=true, EffectiveContent=false` 返回 none。

影响：若上游对此收取费用，平台漏记实际成本。本次修改仅限用户主动断开，按确认范围保留此上游异常策略。这是原计费合同与财务核算目标的冲突，而非宣称原矩阵实现不符。

建议：用户退款口径和上游成本口径独立，零收入也支持成本流水；涉及利润/佣金展示与历史对账，应另立方案。

## 发现 3 — P2：非成功 HTTP 响应的故障细节在日志中丢失

归属：9 月 14 日默认全流式隐私标记与门控组合。

`service/stream_lifecycle.go:BeginStreamAttempt` 在上游响应前设置私有流及 response-only 标记；`logger/logger.go` 将私有流 LogError/LogWarn 文案统一替换；`service/log_info_generate.go:StreamPublicErrorSummary` 仅留下状态/内部错误码，而 `AppendStreamErrorDiagnostic` 对未通过成功门控的请求删除诊断。`middleware/request_logger.go` 也跳过这类请求日志投递。

结果：例如上游 HTTP 401/429/500，返回给客户端的原错误路径仍在，但后台故障日志可能既没有供应商原因，也没有可查询的私有诊断；通用提示却引导查看私有诊断。不能据此判断错误响应已被保存。

建议：不扩展原始 body 保存范围的前提下，保留脱敏且有界的错误分类/供应商错误码，或明确显示该阶段未保存诊断；避免给出不存在的排障入口。

验证：静态追踪成功门控、日志覆盖及请求日志排除分支。

## 发现 4 — P2：交付侧转录计数器重复累计 delta 和 done 快照

归属：9 月 14 日 `relay/common/stream_session.go:CommitDelivery`。

该函数对 `transcript.text.delta` 累计 delta，又对 `transcript.text.done` 累计完整 text。探针先输入 delta `hello world` 再输入 done `hello world`，得到已交付文本 `hello worldhello world`；当前 gpt-4o 本地估算为 4，而单份为 3。

影响边界：进入已交付内容估算时存在多计风险；已确认用量优先的路径不受该计数器影响，不能据此推断所有正常转录均重复收费。此次新接收侧专门跳过重复完成快照，用户断开接收估算已覆盖回归；按原确认范围未改变其他终止原因下的既有交付计数。

建议：交付侧也按转录身份区分增量与完成快照，另补只有 done、delta+done、多片 delta、跨轮的回归测试。

## 测试与工具结果

- `go test ./relay/... ./common/... ./logger ./middleware ./router ./setting/... -count=1`：通过（69 个有测试包）。覆盖所有渠道及现有媒体/工具/门控/取消夹具。
- `go test ./model ./controller -run 'Test(ClaudeDiagnostic|StreamDiagnostic|StreamDiagnosticCompatibility|.*Diagnostic.*|.*Stream.*)' -count=1`：通过；诊断投影测试在无真实 DB 配置时使用真实 SQLite/GORM。
- service 的生产源文件加 12 个 `stream*_test.go` 用显式文件列表运行：通过。它绕过包级真实 DB 初始化，仅验证流式纯逻辑/本地资金接口夹具，不等同于 service 全包集成测试。
- `go build ./...`、相关包 `go vet`：通过。
- 前端 frozen-lockfile 安装完成，依赖声明/锁文件未变。`bun run typecheck`、`bun run build`、本轮 usage-logs 三个相关文件的 oxlint：通过。新增显示复用七种语言已有 `Local Token Counting`，没有新增 locale key。
- 全量 `go test ./...` 未全绿：环境没有 `.env`/SQL_DSN，service TestMain 停止；model/controller 中既有 Veridrop 测试在缺 DB 时触发 nil DB panic。这些测试/初始化不在本次审计提交改动清单内。首次还缺 web/dist，前端构建后该问题已排除。
- 全量 `bun run lint` 未通过：home、setup 等既有文件存在 no-array-index-key / no-nested-ternary 等错误；本轮文件定向检查通过。
- `go test -race` 在当前 Windows 环境以 `0xc0000139` 退出，未产生可用的数据竞争检测结果；应在正常支持 race 的 CI/运行环境补跑。
- 没有执行生产级 30k/100k RPM 压测、MySQL/PostgreSQL 跨库集成、真实上游账单对账。因此本报告是静态审计与本地回归结果，不作为全环境验收通过声明。

## 本次补丁主链与资源复核

接收估算是请求/轮次局部的同步增量工作，不增加 DB/Redis 调用、全局锁或工作协程；保持立即关闭上游、唯一结算、既有异步成本队列。工具状态上限沿用 128 项/8 MiB，接收侧与交付侧各自有界，不保留完整正文。重试及 Realtime 轮次重建估算器。

本机 i9-12900F 微基准：纯事件观察约 3008 ns/op、400 B/op、5 allocs；带接收估算约 5119 ns/op、768 B/op、10 allocs。差约 2.1 微秒和 368 B/帧。假设 100k RPM 且每请求 100 个事件，新增 CPU 约 0.35 核、分配约 61 MB/s；真实数值取决于帧长、工具和媒体，需压测观察 GC 与队列压力。

## 后续优先级

先处理 Realtime 后续轮次钱包准入，再设计零收入成本记账；随后补齐错误日志可观测性和交付侧转录去重。环境验证缺口应在上线前补齐。上述额外修复需用户另行确认。
