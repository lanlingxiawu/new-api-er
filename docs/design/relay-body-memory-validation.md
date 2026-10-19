# Relay body memory validation evidence

> 本文保留实验事实和历史评审意见。最新必要性裁定见 [最小修复方案](relay-body-memory-governance.md)：测试确认某个行为存在，不代表该行为一定是缺陷或必须在本次修改。

修复后的测试结果见后文「2026-10-18：最小修复后的验证」及「持续负载与正常请求/计费对照」；前文诊断结果保留为历史，不代表修复后的行为。

## 验证状态（2026-10-18）

### 本轮跨协议补充

- `relay/provider_memory_diagnostic_test.go`：OpenAI chat/compact、Claude、Gemini native 四个实际 handler，256KiB−1/256KiB/256KiB+1/4MiB，分别 EOF/UnexpectedEOF，32 个子场景。Windows 与 Linux 各重复三次通过：全部读完畸形正文后才拒绝，Body 都关闭。
- `relay/channel/aws/body_memory_diagnostic_test.go`：实际 Bedrock SDK + 本地假 HTTP transport；四个响应大小均完整进入 SDK Body。标准 Retryer 在一次 InvokeModel 内尝试三次 503，三个 Body 均关闭。Windows/Linux 各重复三次通过；测试将退避设为零，不代表生产退避时长。
- `common/...failure_diagnostic_test.go` 的 UnderreportedLength：声明 1MiB、实际 3MiB，通过 2MiB 磁盘预算；Windows 三次通过。这是存储 API 层测试，不代表普通 HTTP framing 允许客户端任意越过 Content-Length。
- `middleware/body_compression_memory_diagnostic_test.go`：实际 gzip 中间件保留压缩 Content-Length，3087 字节压缩为 3MiB 解压体，写入超过 2MiB 磁盘预算；Windows/Linux 各三次通过，证明该长度差异存在真实中间件入口，不仅是任意自制 reader。
- Windows：`go test ./relay/channel/openai ./relay/channel/claude ./relay/channel/gemini ./relay/channel/aws ./relay/helper ./relay/common -count=1` 全部通过；`go test ./common -count=1` 通过；实际清理中间件文件列表测试三次通过。

复现：`go test relay/provider_memory_diagnostic_test.go -count=3`；`go test ./relay/channel/aws -run TestAwsMemoryDiagnostic -count=3`；`go test middleware/gzip.go middleware/body_compression_memory_diagnostic_test.go -count=3`。

本文保存旧方案的验证结果及修订依据。旧方案未通过审查；现行建议见 [最终修复方案](relay-body-memory-governance.md)。本轮仅增加本地诊断测试，没有修改生产逻辑，也没有取得设计实施批准。诊断测试通过表示当前风险得到复现，不表示风险已经修复。

### 本地复现结果

环境：Go 1.25.1，Windows 主机及本地 Ubuntu/WSL（linux/amd64、CGO_ENABLED=0）。下表使用 Linux 结果。只执行请求体存储、Gemini DTO 解码/复制/编码和清理，不访问 DB、Redis、生产服务器或真实 AI 上游；不代表完整 relay 压测。

测试文件：`common/body_storage_memory_diagnostic_test.go`、`common/body_storage_stress_diagnostic_test.go`。

| 场景 | 观测 |
|---|---|
| 12 个 4MiB Gemini 请求状态同时保留，10MiB 阈值 | GC 后存活堆增量约 145.6MiB；缓存计数为 96MiB/24 条 |
| 相同请求，1MiB 阈值且磁盘额度充足 | 存活堆约 48.1MiB；内存缓存计数为 0；磁盘约 96MiB |
| 相同请求，磁盘额度不足 | 退回内存，存活堆再次约 145.6MiB |
| 顺序 10,000 个 8KiB 请求，每次模拟 3 次出站编码/存储 | 累计分配约 872MiB；全部释放并 GC 后净堆增量约 0.08MiB，无遗留缓存计数或 goroutine 增量 |
| 32 并发 × 20 波，每个 256KiB，每次 2 次编码/存储 | 内存/落盘/磁盘满三种模式各完成 640 请求；最终堆增量约 0.05–0.15MiB，文件和缓存计数回零 |
| 16 个 1MiB reader 同时通过磁盘准入检查，磁盘额度 2MiB | 全部落盘，实际计数 16MiB，超出配置 8 倍；证明 check-then-act 不构成硬约束 |
| 内存、磁盘各 100 次超长请求体失败 | 无遗留缓存文件或字节计数 |
| 256KiB 截断一个完整、合法的较大 quota 错误 JSON | 截断后无法解析；不能保证原结构化错误分类和重试语义不变 |

波次测试中不强制 GC，仅在开始和全部完成后强制 GC 以区分活对象与待回收对象；波次内记录的是采样堆占用，不是连续测得的绝对峰值。半数 fixture 使用未知 Content-Length。重试测试只覆盖多次出站体构造与释放，不覆盖真实重试控制器、计费、断连或异常响应生命周期。

最终 Linux 复跑上述四组生命周期场景 3 次，共完成 35,760 个请求 fixture；全部通过。各组结束后强制 GC 的净堆增量为 0–0.18MiB，goroutine 增量为 0，缓存与文件清理断言通过。并发磁盘超发及错误 JSON 截断测试也重复 3 次通过（通过表示成功复现风险，并非风险已修复）。

### 大量长请求补充验证

`TestBodyMemoryStressLongHTTPRequests` 使用本地 `httptest.NewServer` 接收真实 HTTP 请求；每个请求含约 1MiB inlineData，解析并构造出站缓存后等待释放信号，模拟上游迟迟未完成。逐步增加到 8/32/64 个未完成请求，64 个请求全部就绪后再保持 30 秒，最后分两批各结束 32 个。请求数量检查点及释放检查点强制 GC，30 秒等待期间不强制 GC。

| Linux 场景 | 8 个在途 | 32 个在途 | 64 个在途 | 64 个等待 30 秒后 | 全部结束后 |
|---|---:|---:|---:|---:|---:|
| 请求体保留内存 | 24.90MiB | 99.34MiB | 198.57MiB | 198.57MiB | 0.33MiB |
| 请求体落盘、额度充足 | 8.23MiB | 32.93MiB | 65.88MiB | 65.89MiB | 0.13MiB |
| 磁盘额度不足、回退内存 | 24.79MiB | 99.17MiB | 198.34MiB | 198.34MiB | 0.10MiB |

三种模式共 192 个本地长 HTTP 请求，测试耗时约 91 秒，全部通过，最终内存/磁盘缓存计数均回归基线。

以上均为相对测试基线的进程堆增量，包含本地 HTTP 客户端/服务器开销，不是生产 RSS。此实验表明：在该路径中，占用随同时未完成请求数增加；固定数量的等待请求没有仅因等待时间增加而持续分配。实际到达率持续不变时，耗时增长会使在途数增加（稳定条件下约为到达率乘平均耗时），从而扩大工作集。30 秒缩小规模的本地模拟不能代替真实数分钟请求、计费、超时/取消和诊断缓冲的完整验证，也不能据此把等待原因归到上游。

### 异常与生命周期发散验证

新增 `common/body_storage_failure_diagnostic_test.go`、`middleware/body_cleanup_memory_diagnostic_test.go`。Linux/WSL 下下列新增测试重复 3 次，全部完成；通过表示观察到并断言了当前行为，不代表发现的问题已经修复。

| 场景 | 结果与边界 |
|---|---|
| 内存/磁盘 × 已知/未知长度，读入 64KiB 后返回 UnexpectedEOF，各 100 次 | 返回错误，缓存计数及临时文件无残留 |
| 实际 BodyStorageCleanup 中间件：正常、Abort、畸形 JSON、取消后返回、超时后返回，各内存/磁盘 20 次 | 均正常清理；取消/超时用例中的 handler 主动响应 context 后返回，不证明任意阻塞 handler 都能终止 |
| 实际 BodyStorageCleanup 中间件：handler panic，由外层 Gin Recovery 恢复，各内存/磁盘 20 次 | **清理被跳过**：内存模式残留 20 条/5MiB 计数，磁盘模式残留 20 个文件/5MiB 计数。生产的 Recovery 位于该清理中间件外层，顺序一致 |
| 磁盘目录创建失败（用同名普通文件阻止创建，无需填满磁盘） | byte-slice 接口回退内存；reader 接口返回错误。不能把所有磁盘失败一概描述为安全回退 |
| 两个活跃磁盘体各 1MiB，把其中一个 mtime 设置为 20 分钟前，再执行 10 分钟清理 | **仍活跃的旧文件被 unlink**；Linux 下打开的句柄仍可读取。它随后 Close 再扣减一次，计数变为 0，但另一个 1MiB 文件仍存在 |
| 文件已写 256KiB、总大小 1MiB 时扫描同步计数，再完成写入 | **重复记账**：1MiB 文件记为 1.25MiB/2 文件，Close 后残留 0.25MiB。当前未找到非测试运行路径调用 SyncDiskCacheStats，因此作为工具函数/未来接入风险，不认定为本次事故原因 |
| 8 worker 共 800 次创建，同时切换磁盘启用/阈值/额度 1000 次，每个对象并发 Close 两次 | 操作完成，计数回零、无剩余文件；不是 race 检测的替代 |

Panic 测试为验证清理是否执行而保留 storage 引用并在测试结束主动释放。它证明清理和统计生命周期有缺口，**不能证明这 5MiB 在生产里永久不可 GC**；磁盘文件残留则可直接观测。年龄清理使用 mtime 模拟长生命周期，不等待实际 20 分钟，也不应将 mtime 当作活跃引用状态。

额外运行了现有 `relay_timeout_test.go` 和 `relay_timeout_upstream_detach_test.go`，使用真实超时控制器与本地 mock 上游：覆盖响应/总时限、流式首输出/idle、心跳、重试重置、客户端断开后继续上游调用、断开后仍受有限时限约束、handler 完成后取消关联上游。用例通过。无限时限及故意保留断连后计费的请求，仍须作为容量治理输入，不能因这些测试通过就视为无资源风险。

复现清理与超时测试可用显式文件列表，避开无关 TestMain 的 DB 初始化：

```sh
go test middleware/body_cleanup.go middleware/body_cleanup_memory_diagnostic_test.go -count=3 -v
go test middleware/relay_timeout.go middleware/relay_timeout_test.go middleware/relay_timeout_upstream_detach_test.go -count=3
go test ./common -run '^TestBodyMemoryDiagnostic(Interrupted|DiskCreation|LiveFile|StatsSync|ConcurrentConfig)' -count=3 -v
```

下一版 P0 还须纳入：panic 栈展开中的可靠清理、活跃磁盘文件与过期清理的所有权边界、重复释放/扫描对预算计数的影响。这些问题在修复前不可通过“统计归零即安全”或“清理旧文件即可释放空间”来验收。

Linux 堆 profile 中主要分配来源为入站 `io.ReadAll`、解码字符串和出站 `Marshal`，各约 48MiB。该样本的 DeepCopy 总分配仅约 0.036MiB，不支持“深拷贝完整复制每份大字符串”的解释。全部对象释放后堆回落，不支持这些组件中存在按已完成请求数单调增长的泄漏；仍不能排除其他生产路径的泄漏。

### 必须修订的设计问题

1. **硬上限与无条件内存兜底互相矛盾（阻断）。** 当前第④分支及错误处理仍允许无界分配。必须明确选择“仅软缓解”或“资源耗尽时有界拒绝/降级”；后者改变 relay 可用性行为，须显式设计和批准。两种策略不能同时承诺“永不拒绝”及“任何流量不 OOM”。默认 4GiB 也没有生产容量验证依据。
2. **预算须在分配/写入前预留（阻断）。** 先完整读入、后判断上限，不能约束分配瞬时峰值；未知长度的 `io.ReadAll` 尤其明显。内存与磁盘都需原子预留、失败回滚、成功结算、Close 幂等释放；未知长度需分块预算及转换策略。现有磁盘计数是写完后增加，并发允许超发，不能继续作为可靠的兜底容量。
3. **缓存计数不是进程堆或 RSS 上限（阻断）。** 入站、出站均计数，同一请求可有两条；解码字符串、DTO、JSON 临时缓冲、响应数据并未完整纳入。`memoryStorage.Close()` 只减计数，尚有引用时数组仍可达。需定义预算对象、容量/长度口径及所有权；4GiB 缓存限额不能推导出 4GiB 进程内存限额。
4. **系统监控不是硬保护（阻断）。** `common/system_monitor.go` 大约每 5 秒更新系统使用率，准入检查使用缓存值，且不能阻止已接纳请求继续分配。它只能辅助保护，不能弥补无界兜底或替代原子预留。
5. **错误体截断会影响业务语义（阻断）。** `service/error.go:RelayErrorHandler` 在读取后解析 JSON，并通过 `quotaExhaustedErrorOptions` 设置 SkipRetry；截断导致解析失败，可能丢失原本禁止重试的分类。须规定超限时的状态、错误分类、重试/渠道禁用行为，使用 limit+1 区分截断，并审计关闭响应体是否还会继续无界读取。不能声称只影响展示。
6. **同步 I/O 与主链影响描述不实（阻断）。** 把原本内存路径转成磁盘路径，会增加 relay goroutine 的同步写入、磁盘读取和文件描述符占用，不只是“一次原子读”。入站与出站双份落盘，本地样本写入总量约为原始体积两倍；重试还会继续增加。必须预算磁盘吞吐、延迟、容量、文件句柄及与 DB/日志的共享磁盘竞争，并解决与项目 Rule 0 的要求如何兼容。
7. **P1 必须独立验证。** 收到上游响应头不充分证明上传读取已结束，也不证明重试、计费表达式和诊断不再使用原请求体；提前释放需要明确所有权及完成信号。取消断连请求会改变现有计费行为，与“billing 无交互”不符。本轮未验证或批准这两项。
8. **事故归因和发布验收需要降级措辞。** 上游饱和、5–10 倍内存、配置已改到 1MiB/70% 均未由当前证据证实；100GiB 是历史描述的部署值，代码默认磁盘配置为 1GiB。不得用本地组件结果宣称完整复现线上 390GiB OOM。也不应预先要求发布后调回 10MiB；先测量再决策。

### 下一版验收要求

- 大量短请求、多波慢请求、不同在途数量，分别检查自然 GC 过程与释放后残留；以在途数量/字节数解释峰值，而非仅累计请求数。
- 内存与磁盘预留边界、并发超发、未知长度、磁盘故障、读失败、重复关闭与配置下调必须有测试；预算耗尽行为必须明确定义。
- 基于实际 handler 的取消、超时、重试、panic 清理及计费回归须另行验证，不能由本轮 fixture 测试替代。
- 错误体小于/等于/大于上限、尾部包含错误字段、quota 禁重试、HTML/畸形 JSON、慢读取和关闭路径均需覆盖。
- 100k RPM 资源预算须同时包含入站/出站体、DTO/解析瞬时内存、响应缓冲、文件句柄与磁盘读写；硬边界不允许依靠未限制的并发数乘以最大请求体积来证明。

复现命令：`BODY_MEMORY_STRESS=1 GIN_MODE=release go test ./common -run '^TestBodyMemory(Stress|Diagnostic)' -count=1 -v`（Linux）。普通 `go test ./common -count=1` 跳过显式启用的较长波次测试。已运行 common 包测试通过；Windows race 构建因缺少 C 编译器未完成，不能宣称 race 验证通过。

# 2026-10-18：最小修复后的验证（优先于前文历史诊断结论）

用户批准实施后，已完成 BodyStorage 清理/容量保护及共享磁盘租约修复。本节只记录本地验证，不将受管字节账本等同于进程 RSS 上限。

## 已执行

- 根模块 `go build ./...`；`relaykit` 使用 `GOWORK=off go build ./...` 独立构建及 `go test ./types`，通过。
- `go test ./common ./setting/performance_setting ./service/settingsaccess ./i18n`，通过。
- Windows GCC 环境下 `go test -race ./common ./setting/performance_setting`，通过。
- `relay`、`relay/common`、OpenAI、Claude、Gemini、AWS 相关包测试通过；上游全部使用本地 fixture，不调用真实 AI API。
- middleware 使用同目录生产源码文件列表及容量/清理/压缩测试运行三轮，通过，避免依赖该包会访问共享数据库的 TestMain。覆盖 JSON、非 JSON、MJ 容量拒绝；正常、Abort、畸形输入、取消、超时、panic 清理；gzip/br/zstd 解压后真实大小限制。
- controller 容量错误不重试/不封渠道、service 既有 BillingSession 单次退款、model 配置持久化失败不发布及单键/分组/批量配置校验的定向测试通过。service 使用生产源码文件列表运行，未声称整包数据库集成测试通过。
- 容量 sentinel 丢失、读取中关闭配置、文件创建后关闭配置、首次落盘创建失败回退：新增测试先在未修复实现上失败，再于修复后通过。另验证首次写入预算不足回退及成功迁移后的写入失败不返回截断内容。

## Linux 并发持有测试

WSL Ubuntu 执行交叉编译的 common 测试程序，`BODY_MEMORY_STRESS=1`，64 并发、每次请求持有入站和出站各 64KiB、持有 5 秒，三种模式各连续三轮。检查持有期间主动清理不破坏数据、结束后字节计数和文件归零。

| 模式 | 配置 | 三轮结果 |
|---|---|---|
| 内存 | 内存 4MiB | 每轮接纳 28、拒绝 36；受管预留约 3.93MiB，未超预算 |
| 磁盘充足 | 内存 4MiB、磁盘 16MiB | 每轮接纳 64、无拒绝；磁盘占用 8MiB |
| 磁盘耗尽 | 内存 4MiB、磁盘 1MiB | 接纳 29/36/10，拒绝 35/28/54；预留均未超预算，全部释放 |

拒绝数量随并发竞争、读取临时缓冲及新旧数组重叠变化，不把接纳数当确定吞吐指标。测试按比例缩小容量验证不超发与清理，不模拟生产 390GiB 内存，也不证明 1万/3万/10万 RPM 容量。

## 仍需灰度验证

Linux race、真实 MySQL/PostgreSQL 配置集成、生产规模的混合长请求与磁盘延迟/GC/Heap/RSS 对照尚未完成。未运行依赖共享数据库的整仓 `go test ./...`；不能把定向测试表述为全仓测试通过。没有事故 heap profile，不能断言请求体是唯一 OOM 来源。预算默认关闭，启用前须设置正数内存预算并确认磁盘空间；未知跨重启文件由运维确认归属后处理。

---

## 持续负载与正常请求/计费对照（2026-10-18 补测）

部署后复核补充：新增 `common/body_governance_boundary_test.go`，验证字节数组和未知长度 reader 两种入口在 1MiB−1、1MiB、1MiB+1 的落盘边界，保留完整内容；验证内存按 `cap` 而不是 `len` 计量、扩容新旧数组重叠预留、恰好达到预算可用但再多 1 字节拒绝、负数及整数溢出拒绝。与 common 生命周期/所有权、配置及六协议正常链路对照一起运行 Windows race 三轮，全部通过。本次只增加测试，没有更改已部署生产代码。香港测试机仍保留默认关闭的 `body_governance_enabled`；不能将测试通过表述为服务器已经启用预算保护。

本轮仅新增/扩展测试，未扩展生产修复范围。上游全部为本地 HTTP fixture，未调用真实 AI API，未写生产账本。以下结果补充前文验证，不替代生产灰度。

### 大量长时间请求

`common/body_governance_stress_test.go` 在 WSL Ubuntu 运行。每条成功请求同时持有入站、出站缓存，直到统一释放；持有期间每秒检查预算，执行清理验证活跃文件不被删除，释放后核对内容、计数与文件。返回仅允许成功 204 或容量拒绝 503。

| 并发 × 单条入站大小 × 持有时间 | 内存模式成功/拒绝 | 磁盘充足成功/拒绝 | 磁盘耗尽成功/拒绝 |
|---|---|---|---|
| 256 × 64KiB × 30 秒 | 28/228 | 256/0 | 36/220 |
| 64 × 1MiB × 30 秒 | 29/35 | 64/0 | 28/36 |
| 1024 × 64KiB × 60 秒 | 228/796 | 1024/0 | 236/788 |
| 128 × 4MiB × 60 秒 | 29/99 | 128/0 | 2/126 |
| 10000 × 64KiB × 120 秒 | 未运行 | **10000/0** | 未运行 |

前四组内存预算依次为 4/64/32/256MiB，磁盘充足预算为 64/256/256/2048MiB；耗尽组磁盘预算为 1MiB。最后一组内存预算 512MiB、磁盘预算 2500MiB。全部通过；每组结束后受管内存、磁盘预留和缓存文件数均为零。拒绝数量受并发竞争和扩容临时预留影响，不是固定容量承诺。

1 万并发组实际持有磁盘 1,310,720,000 字节（入站、出站各一份），测试耗时 124.28 秒；持有时测试进程 HeapAlloc 1,701,852,264 字节、goroutine 50004，释放并 GC 后 HeapAlloc 127,937,336 字节。goroutine 包含本地客户端和服务端，不代表生产每请求成本。128 条 4MiB 磁盘组持有时 HeapAlloc 约 1.02GiB，证明磁盘缓存并不会消除临时完整字节等堆驻留。**字节账本归零不等于 RSS 立即归零；这些测试也不等同于 1 万条 4MiB 生产请求。**

Linux 复现最后一组（需足够文件句柄及临时磁盘空间）：

```bash
BODY_MEMORY_STRESS=1 BODY_STRESS_CLIENTS=10000 \
BODY_STRESS_HOLD_SECONDS=120 BODY_STRESS_PAYLOAD_KB=64 \
BODY_STRESS_MEMORY_MB=512 GIN_MODE=release \
go test ./common -run 'TestBodyGovernanceLongHTTP/disk$' -count=1 -v -timeout=240s
```

### 持续组件负载与延迟

`relay/provider_body_governance_test.go` 覆盖 OpenAI、Claude、Gemini 原生协议各自非流式/流式六种 fixture。每条请求经过实际 BodyStorage、重放读回、本地 HTTP 上游及实际响应处理器，校验上游请求字节、usage、计费表达式结果和上游调用次数。

四种模式：off 为治理关闭且内存缓存；memory 为治理开启且内存缓存；legacy_disk 为治理关闭且启用既有磁盘缓存；disk 为治理开启且落盘。64 worker、每请求 64KiB，按计划速率持续发送。每模式分别运行 15000 条（500 请求/秒）及 50000 条（1667 请求/秒），总计 **260000 条请求**，无响应/usage/额度断言失败，无多余上游调用；结束后缓存计数与文件均归零。

| 模式 | 3 万 RPM 目标：实际 RPM / P99 | 10 万 RPM 目标：实际 RPM / P99 |
|---|---|---|
| off | 30001 / 1.584ms | 100020 / 1.745ms |
| memory | 30002 / 2.156ms | 100019 / 2.396ms |
| legacy_disk | 30001 / 7.662ms | 100019 / 2.167ms |
| disk | 30001 / 2.170ms | 100019 / 3.722ms |

单模式持续约 30 秒。延迟从 worker 开始处理计时，不含计划发送到 worker 的等待；部分采样与长请求测试并行，存在资源竞争，不能用单次 P99 差异宣称加速或零开销。磁盘写入可命中 OS 页缓存，未执行 fsync。此为本机组件负载，不含完整认证、分发、真实网络、真实账本写入，不证明生产整条网关已达到 10 万 RPM。

Linux 复现单模式矩阵（测试内部遍历四模式）：

```bash
BODY_PROVIDER_LOAD=1 BODY_PROVIDER_REQUESTS=50000 BODY_PROVIDER_RPS=1667 \
GIN_MODE=release go test ./relay -run '^TestProviderBodyGovernanceLoad$' -count=1 -v -timeout=240s
```

### 正常链路与计费契约

- 六种 fixture 在四种缓存模式下返回内容、usage 和计算额度一致；请求体完整发送，上游调用恰好一次，流式最终 usage 覆盖早期计数。`TestProviderBodyGovernanceParity` 普通测试及 Windows race 各三轮通过。
- fixture 明确区分厂商 token 口径：输入 100、输出 7、缓存命中 20；Claude 输入不含缓存命中，OpenAI/Gemini 输入含缓存命中。使用合成定价 `p*2+c*10+cr*.2`、QuotaPerUnit=500000、分组倍率 1，期望额度分别为 Claude 137、OpenAI/Gemini 117。验证的是按上游 usage 正确归一化并执行配置价格，不是各厂商价格或应付金额相同。
- `service/body_cache_refund_test.go` 验证 off/memory/disk、预扣 0/100/117/200、实际额度 117；32 并发结算仅应用一次差额，结算后退款不重复执行；容量失败退款也只执行一次。使用测试 FundingSource 与 playground 隔离，不读取/修改真实用户余额。
- service 定向退款/结算及 `gen_billing_usage_test.go` 三轮普通和 race 通过；OpenAI、Claude、Gemini、AWS 适配器及 `pkg/billingexpr` 测试通过。AWS 沿用实际适配器已有 usage/取消等回归，未纳入上述六 fixture 负载矩阵。

**结论边界：** 本轮未发现已覆盖正常请求及计费契约的回归；有完整上游 usage 时，归一化及配置定价与 fixture 对齐。没有最终 usage 的断流仍可能走既有估算，真实供应商账单、渠道成本价、数据库持久化账本对账尚未验证。不能据此承诺所有请求均与供应商账单金额相等，亦不能承诺所有未覆盖路径无新问题。生产灰度仍须观察长请求工作集、磁盘/DB 争用和实际账本；治理只约束批准范围内持有的缓存，不是进程内存硬上限。
