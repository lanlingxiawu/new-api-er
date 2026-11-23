# 2026-11-15 上线问题修复与复测

本文记录 2026-11-15 候选的修复与验证边界；依赖告警等状态以当日结果为准。后续前端及开发依赖修复见 [2026-11-17 修复说明](release-frontend-fixes-20261117.md)，不将历史结果视为当前版本已重新验收。

本轮承接上线验收及六项复核（本地记录：`docs/design/release-validation-20261115.md`），执行用户确认的五项修复：依赖安全、后端 i18n 常量登记、前端请求断言、三个任务插件格式、渠道回滚测试数据隔离，并补正复测发现的日志查询取消边界。原始验收记录保留，本文件记录修复后的候选；不把原候选的请求验收结果直接算作新候选通过。

**最终根模块全量测试、前端全量测试及构建均通过。** 后端 21,127 个父/子测试通过事件、0 失败、59 跳过；前端 255 文件、2,935 测试通过。开发工具仍有 2 条告警；二进制仍保留 3 条本项目模块级公告记录，适用性复核见下文。未部署生产，未将未执行的发布专项记作通过。

## 范围与实际实现

| 项目 | 修复 |
| --- | --- |
| 依赖安全 | 根模块及 Docker 构建改用 Go 1.26.11；升级确认受影响的 Go 和前端依赖，重新生成锁文件并扫描 |
| 后端 i18n | `i18n/keys.go` 登记已有的三个 `audit_filter.*` 键，`controller/audit_filters.go` 统一引用常量；键值、现有翻译和接口行为不变 |
| 前端断言 | 正向断言检查真实传入的 `AbortSignal`；反向断言使用部分对象匹配，仍检查未按 token 筛选时不携带 `token_ref` |
| 插件格式 | 仅按现有格式工具整理 hailuo、kling、vidu 的 `plugin.js`；未改插件逻辑或格式脚本 |
| 测试数据隔离 | 渠道回滚用例使用唯一模型名并保留无关能力记录，清理仅定位自己的复合主键；另为 model 测试夹具补建应用已有的 `CasbinRule` 表，避免独立运行依赖其他测试包先建表 |
| 复测补正：日志查询取消 | `queryLogHourStatRange` 在 DB Scan 返回后再次检查 `ctx.Err()`；已取消的查询返回取消错误，不把驱动已完成的扫描误作有效成功 |

缓存公开字段差异不在本轮改动范围。已有证据不能证明本站少记缓存或少计费，原始 `billing_usage`、用量归一化、消费日志写入及钱包结算实现均保持不变。后续若发现少计费，继续区分提供方、官方 main、本分支和本站结算；多收不在用户指定的修复范围内。本轮修复未改 request ID 的生成、响应附注和记录规则。

## Existing Logic and Related-Path Findings

- `controller/audit_filters.go` 已通过 `common.ApiErrorI18n` 查询翻译；遗漏的是常量注册，不是语言内容。现有 i18n 一致性测试能够检出该遗漏，修复不修改断言。
- 审计查询 API 的 `AbortSignal` 来自 TanStack Query，请求取消是既有行为；修改测试使其准确匹配实际调用，不移除运行时信号。
- 渠道创建在一个事务中分批写渠道及能力，第二批 DB 错误和 panic 均应回滚；原测试错误地要求全表能力数为零。新断言同时验证本次行不存在、无关行仍存在，没有通过删除业务断言让测试通过。
- Brotli 被 relay 请求解压中间件直接使用，因此必须升级真实生产依赖；[GO-2026-6888](https://pkg.go.dev/vuln/GO-2026-6888) 的修复版本是 1.2.7。Go 和 compress 分别按 [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859)、[GO-2026-6865](https://pkg.go.dev/vuln/GO-2026-6865) 升级。没有发送攻击载荷，不把依赖命中写成已复现利用。
- 日志查询取消回归曾返回 nil 而非 `context.Canceled`。该查询在数据库扫描后缺少 context 复查；既有[查询治理设计](log-database-query-hardening.md)要求取消后停止后续处理。增加复查不修改 SQL、统计口径、缓存命名空间或收费。查询入口来自 `controller/log.go` 的日志列表/统计，非 relay 结算。
- `deleteUserAuthenticationData` 已删除用户对应的 Casbin 策略，但 model 测试 harness 没有迁移 `CasbinRule`；独立新库运行出现三条 `casbin_rule` 不存在的错误。补齐测试初始化，不修改删除用户逻辑、生产 schema 或上游测试文件。

## 依赖调整

| 依赖 | 修复前 → 修复后 |
| --- | --- |
| Go | 1.26.10 → 1.26.11 |
| `github.com/andybalholm/brotli` | 1.2.0 → 1.2.7 |
| `github.com/klauspost/compress` | 1.18.7 → 1.20.2 |
| `golang.org/x/net` | 0.60.0 → 0.62.0 |
| `golang.org/x/crypto` | 0.57.0 → 0.59.0 |
| OpenTelemetry / trace | 1.41.0 → 1.42.0 |
| node-forge | 1.4.0 → 1.4.3 |
| zod | 4.4.3 → 4.6.7 |
| colord | 2.9.3 → 2.9.4 |
| decode-uri-component | 0.4.1 → 0.5.1 |
| follow-redirects | 1.16.0 → 1.16.3 |
| mermaid | 11.15.0 → 11.16.1 |
| seroval | 1.5.6 → 1.6.11 |
| undici | 7.28.0 → 7.30.1 |
| shadcn CLI | 4.12.0 → 4.23.0 |
| Vite（测试工具依赖） | 8.1.5 → 8.1.6 |
| postcss | 8.5.15 → 8.5.33 |
| fast-uri / hono / ip-address / qs | 分别固定到 3.1.9 / 4.13.18 / 10.7.6 / 6.17.1 |
| js-yaml / systeminformation / browserslist / source-map-js | 分别固定到 4.3.2 / 5.33.23 / 4.29.5 / 1.2.2 |

Go 的 x/sync、x/sys、x/text 等传递依赖随版本解析同步更新，完整列表以 `go.mod` / `go.sum` 为准。GORM、MySQL/PostgreSQL/SQLite 驱动没有升级。`Dockerfile` 固定 Go 镜像索引摘要 `sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb`；CI 从 `go.mod` 读取版本。

移除旧 `brace-expansion=2.1.1` 全局覆盖，让包管理器按父依赖要求选择匹配的新版本。shadcn 的升级由其自身采用新的 MCP / Zod 依赖，不把旧 CLI 所需的 Zod 3 强行替换为 Zod 4。实际 shadcn CLI 版本命令运行成功。

### 扫描结论和剩余开发工具告警

- 修复后 `govulncheck -json ./...` 退出 0，`finding` 为 0；扫描输出中的 OSV 元数据条目不计作命中。
- 最终 Linux 二进制扫描退出 0，但原始 JSON 中仍有 **3 条本项目模块级 finding，0 条包/符号级 finding**。它们不等同于 OSV 元数据，也不能被省略；退出 0 不表示原始记录为零。当前调用路径复核见下表。
- 最终锁文件 `bun audit --production --json` 退出 0、结果 `{}`；原生产依赖 48 条告警已消除。
- 包含开发依赖的 `bun audit --json` 仍退出 1，只剩 **2 条**（低危 1、中危 1），未隐藏或忽略这两条记录。见审计汇总（本地记录：`_local/release-fixes-20261115/dependency-audit-summary.json`）。

| 剩余项 | 依赖路径、边界及处理 |
| --- | --- |
| [deepmerge 对象原型污染](https://github.com/advisories/GHSA-gcqx-w48m-w2h4) | `shadcn → deepmerge` 及 `shadcn → @shadcn/registry → deepmerge`；当前 registry 最新仍是受影响的 4.3.1，没有已发布修复版。保留告警，不能声称已修复 |
| [minimatch glob 放大](https://github.com/advisories/GHSA-g3hv-75wg-m249) | `shadcn → ts-morph → @ts-morph/common → minimatch 10.2.5`；修复版本 11.0.1 超出父依赖 `^10.0.1`。保留告警，未通过跨主版本强制覆盖制造兼容性风险 |

两个包均来自组件生成 CLI；本项目页面源码与脚本没有直接导入它们，生产依赖图也不包含它们。这个判断只说明当前部署依赖边界，不代表开发者执行 CLI 处理不可信内容时没有风险；仍需跟进上游兼容修复。

| 二进制本项目模块告警 | 当前实现复核 |
| --- | --- |
| [GO-2025-4154 / 重定向 SSRF](https://github.com/QuantumNous/new-api/security/advisories/GHSA-9f46-w24h-69w4) | 上游公告标明修复于 0.9.6，Go OSV 记录没有对应 fixed 边界。当前 `DoDownloadRequest` 非 Worker 分支使用受保护客户端，`CheckRedirect` 每跳调用 `ValidateSSRFProtectedFetchURL`；既有私网重定向拒绝测试纳入全量运行，外部 Worker 行为不在本轮验证范围 |
| [GO-2026-4813 / passkey 验证绕过](https://github.com/QuantumNous/new-api/security/advisories/GHSA-5353-f8fq-65vc) | 当前 `/api/verify` 的 `VerifySecurityInput` 对 passkey 返回 `ErrVerificationFlowRequired`；专用 `PasskeyVerifyFinish` 必须完成 WebAuthn `ValidateLogin`，才签发绑定 scope/context 的证明。不存在公告描述的“只要已注册 passkey 即通过”分支 |
| [GO-2026-5652 / 0.0.0.0 SSRF](https://github.com/QuantumNous/new-api/security/advisories/GHSA-v5c3-6wvc-pc2q) | 当前 `common/ssrf_protection.go` 同时检查 `IsUnspecified()` 和 `0.0.0.0/8`；字面 IP 验证探针不发网络请求，复核拒绝未指定地址、保留公共地址正常行为 |

这三项保留为模块版本范围命中及实现适用性复核，未通过改版本号或屏蔽扫描使记录消失；不把这次代码核对解释为对全部 SSRF/WebAuthn 配置组合的渗透测试。

## Main Chain Impact

本次没有增加同步或异步 relay 处理、middleware、DB/Redis 调用、锁或 goroutine。请求解压仍在原位置同步执行，只替换已修复的库实现；安全修复可能拒绝原本异常的压缩输入。其他产品代码变更为等值 i18n 常量引用，以及日志读取返回前的 context 检查。测试夹具不编入产品。

### Shared Resource Audit

没有新增或修改 Redis 命名空间、内存缓存、连接池、后台队列和文件句柄使用方式。渠道回滚测试涉及 relay 同样读取的 `channels` / `abilities`，因此只在本轮专用 `channel_test` 数据库执行；夹具按模型名和复合主键定位，未连接开发或生产共享库。为触发第二批写入失败使用的临时唯一索引仅存在于隔离测试库，按原测试生命周期移除。

日志查询仍使用既有 `LOG_DB`、`logs` 和小时统计缓存，与日志写入 worker 共享日志连接池；新增取消检查只读 context，不发额外查询、持锁或扩池，不改写日志记录。

`CasbinRule` 的补建只发生在 model 测试初始化，使用测试专用主库；不是新增产品迁移，不清理其他账号或策略。

### Concurrency Analysis

在 30k RPM 和 100k RPM 下，本次代码增加的每请求 DB 调用、Redis 调用、锁和 goroutine 均为 0；不存在新增资源随 RPM 线性增长的路径。依赖内部性能仍需负载测试评价，本轮不将功能测试结果解释为 30k/100k RPM 容量通过。

## 复测环境与证据

本轮证据位于 _local/release-fixes-20261115/（本地记录：`_local/release-fixes-20261115`），属于忽略目录。后端在当前工作区的同源副本运行，排除真实 `.env`；MySQL **9.7.4**、PostgreSQL **18.7** 和 Redis 均是本轮专用容器，服务端版本及不可变镜像摘要见 database-versions.json（本地记录：`_local/release-fixes-20261115/database-versions.json`）。未连接生产，未调用真实付费提供方。

| 检查 | 已取得的结果 |
| --- | --- |
| **最终根模块 `go test -json -count=1 -p 1 ./...`** | **退出 0；21,127 通过事件、0 失败、59 跳过**，包含最终 model 夹具及日志查询取消修复；约 838 秒 |
| **最终根模块 `go vet ./...`** | **退出 0** |
| MySQL / PostgreSQL 渠道创建、失败回滚和 panic 回滚 | 两种数据库均 `-count=2` 通过，各 46 个父/子测试通过事件；包含保留无关能力记录的断言 |
| 根模块 `go build ./...` / `go vet ./...` | 均退出 0 |
| 根模块全量 `go test -json -count=1 -p 1 ./...` | 21,107 通过、3 失败事件、58 跳过，退出 1；3 个失败事件是同一流式超时测试组的两个子用例及其父项 |
| 流式超时原断言复测 | 整组连续 10 轮均通过，110 个父/子通过事件；随后在新隔离数据库复跑整个 middleware 包，退出 0。未修改超时实现、阈值或断言，原全量失败记录保留 |
| i18n 全包（包含于全量） | 81 通过、0 失败 |
| relaykit 独立 `GOWORK=off` build/test | 均退出 0；1,584 通过，无跳过 |
| tokenkit 独立 build/test | 均退出 0；239 通过，无跳过 |
| 前端原失败文件及审计 viewer | 2 文件、51 测试通过 |
| 前端最终锁文件全量 `bun run test --maxWorkers=4` | 255 文件、2,935 测试通过，退出 0；升级开发工具前的 2 worker 全量也同样通过 |
| 插件 lint / format check | 均退出 0，使用工作区标准脚本 |
| 最终锁文件 frozen install / typecheck / lint / build | 均退出 0 |
| 日志查询取消修复后复测 | MySQL、PostgreSQL 日志库各连续 50 次通过；相关 controller 9 项通过。后续完整 model 运行暴露的三条缺表错误已补齐测试夹具；三条删除用户用例在 MySQL、PostgreSQL 各通过，最终根模块全量通过 |

测试数量包含父项与子项事件，复测有重叠，不能累加为不同测试的总数。最终全量通过不改写首次失败记录，所有命令、退出码和跳过明细见最终汇总（本地记录：`_local/release-fixes-20261115/summary.json`）。

最终全量比首轮多跳过 1 项 PostgreSQL token 列迁移测试：前置认证专项已创建 `tokens` 表，迁移用例为保护已有表而跳过。该用例另用新的专用库补验通过（1 通过、0 跳过）；其余 58 个跳过仍保持未执行。此前验收的生产拓扑、旧版本升级、实际容量等未执行项仍然有效，不因本轮修复而自动通过。

### 候选一致性与中途变更

测试期间发现工作区另有 6 个请求日志文件更新（`request_log.go`、`request_log_recovery.go`、`request_log_recovery_test.go`、`request_log_startup.go`、`request_log_store.go`、`request_log_sweep.go`）。这些改动原样保留，后续 model 回归及最终构建使用包含它们的新副本；没有把首次旧副本结果冒充当前源码结果。首次 Go 源码清单及变更前文件保留于本轮证据目录。

五个受保护的前端登录/请求文件与已核实的官方 main `b388140acfd80369ec6243b16869daff1b4051f4` 逐字节一致，见保护文件检查（本地记录：`_local/release-fixes-20261115/protected-files.json`）。

最终 Windows、Linux 程序均从新前端产物和 Go 1.26.11 生成，采用 Docker 对应的 `CGO_ENABLED=0`、PGO 和 `GOEXPERIMENT=greenteagc`；Linux 程序的 `govulncheck -mode=binary` 退出 0。启动旧系统 Go launcher 且传入 GOEXPERIMENT 时首次构建发生 launcher panic，改为直接调用已下载的 Go 1.26.11 编译器后通过；未修改系统 Go 安装。VERSION 文件本来为空，产物身份以最终 SHA256 为准。

| 最终产物身份 | 值 |
| --- | --- |
| 分支 / 基础提交 | `zhuzhan` / `caed15f301729c766ea9b9f3668761c5e8510f2e`，包含工作区改动 |
| 源码清单 SHA256 | `1d784f843d9cc783c392aa8b7866b6b4a0bb0228e6a529b9b6f282d0cb3535cf`，4,296 文件 |
| Windows 程序 SHA256 | `c83edd8475ef69c1a31dd60ae949d4f688b440fd46e0cd9c4c2b8e1174b6f562` |
| Linux 程序 SHA256 | `cb86963352e45ccc6bff93faadbc89f93e7daa9429e7aaea40cf1d8fe2e120ac` |

完整身份见 candidate-identity.json（本地记录：`_local/release-fixes-20261115/candidate-identity.json`）。最终非文档源码与已测副本一致；本轮专用容器和匿名卷清理结果见 cleanup.json（本地记录：`_local/release-fixes-20261115/cleanup.json`）。

以上构建不是生产部署，也未重新执行新候选的完整 SDK/浏览器/公网渠道矩阵。原验收的未执行项目仍需按发布范围补验。
