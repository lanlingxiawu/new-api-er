# 全功能集成复核后的确认修复

本文记录 2026-11-17 的修复范围和当日验证。下文“尚未实施”的监控能力是当时的范围边界；后续多人分配、AI 请求 IP 观察和个人通知见 [2026-11-18 功能补齐说明](audit-security-completion-20261118.md)。

日期：2026-11-17。用户已明确确认修复本轮发现的问题，并再次确认监控设置必须从右侧抽屉打开。本次落实既有设计和已确认缺陷；验证结果在执行后补充，不把页面可达当作全部功能验收。

## Goals and Scope

- 落实 `audit-security-customer-response.md` 第 2、9 节的监控设置右侧抽屉、参数说明、失败保留草稿及二次验证交互。
- 修复上线复核报告 F01–F07；核查并处理 F08 的开发依赖公告。
- 不扩展尚未实施的多人分配、自动账户处置、AI IP 采集和个人通知，不改变生产配置或部署。

## Existing Logic and Related-Path Findings

- `security/settings-dialog.tsx` 使用居中 Dialog，与已确认右侧抽屉设计冲突。复用 `components/ui/sheet.tsx` 与 `drawer-layout.ts`，与主题设置等既有抽屉的标题、滚动区、固定按钮区一致；移动端全宽。
- 监控设置从 `security/api.ts/getMonitorSettings` 读取 Root scope；保存先用 `useSecureVerification` 为完整值快照取得 `audit.security.settings` proof，再调用原分组保存接口。保留此权限及原子配置合同。验证取消、失败不清空草稿，保存未完成时禁止重复提交和关闭。
- `setting/operation_setting/audit_security_setting.go` 注册 21 个字段；前端 defaults 已包含全部字段，但渲染漏了 IP 缓存时长。其有效范围 10–10080 分钟；`ip_provider.go` 消费该值。现有设置只监测/通知，不自动封禁。参数说明按 `rules.go`、`finding_snapshot.go`、`worker.go`、`notify.go`、`audit_cleanup.go` 的实际时间窗口和清理条件编写。
- `log-export-section.tsx` 已有 XLSX 上限 schema/defaults/保存映射，只有 outputFields 未渲染。范围 1–1000000，复用已有字段布局及保存确认，不增加 API。
- `log-query-section.tsx` 的四个输入缺少后端 getter 的上限：缓存小时 8760、当前小时刷新秒数 300、启动预计算小时 720、查询超时毫秒 60000。补充 schema 和控件上限，保留两个 0 值预设；不修改后端历史配置兼容的 clamp 行为。
- `customer-risk-detail.tsx` 使用的 `View related audit` 缺少全部七种译文；另四处已确认插值错误。沿用项目翻译脚本修复，并验证源代码键及各语言变量集合。
- `i18n/keys.go` 少了语言资源已有的 `log_export.col.log_raw` 常量。保留资源，补齐常量；既有一致性测试覆盖此回归。
- `token_upstream_cases_test.go` 是分支适配测试。模型限制/IP 白名单属于已确认可审计配置值，真实凭据仍禁止记录。测试应断言其位于所属用户的字段变化收据中，同时保留凭据、外用户对象和原始错误排除。矩阵 PAT fixture 改用已有 scoped PAT 工厂和所需授权，避免依赖全局旧令牌过渡期；旧令牌生命周期保留在既有专门测试中。
- 扩大实库矩阵后，`access_token_audit_upstream_cases_test.go/TestAuditDatabaseMatrix` 的 MySQL 5.7 旧库夹具未执行 `user-channel-exclusions.md` 第 17 节明确要求的发布前加列。`model/user_channel_exclusion_schema.go` 有意只允许启动时 INSTANT 加列，5.7 不支持时快速拒绝。修正仅在本测试的独立临时数据库模拟预迁移；保留产品保护及 `TestUserChannelExclusionDatabaseMatrix` 的拒绝/预迁移合同。

## Data Flow / API Contracts

页面读取设置 → 用户修改并校验 → 既有保存确认或安全验证 → 原 scope API 持久化 → invalidate/refetch → 重新打开或刷新核验。监控 GET `/api/option/?scope=security.audit-monitor`、PUT `/api/option/group` 及 `X-Security-Proof` 不变；日志设置仍逐 changed key PUT `/api/option/`。Root/设置权限、失败响应及验证快照不变。无新增端点。

## Data Model / Configuration / Shared Resource Audit

不新增表、字段、索引、配置参数或迁移。新增控件只操作既有 option key，复用既有缓存发布；不增加 DB/Redis 连接、后台任务或锁。后端产品改动仅补语言键常量；测试在隔离实例运行。Relay 路径、共享 users/tokens/channels 访问模式和资源预算均不变。

## Error Handling and Interaction

设置读取失败显示原错误及重试；无数据时不允许保存。数字上下界及整数校验阻止验证/写请求。保存失败保留修改；验证取消不写配置。抽屉标题和底部操作固定，内容可滚动，窄屏不横向溢出。复用设计 token，新增说明同步七种语言，不修改上游认证/HTTP plumbing。

## Verification Plan

- 组件回归：右侧 Sheet、全部配置字段可访问、缓存 TTL 边界/保存载荷、真实二次验证组件取消与失败保留草稿；XLSX 控件边界和原 key 保存；四个查询边界以及 0 预设。
- 语言资源：七种 key/占位符检查、后端完整 i18n 包测试。
- 审计：独立进程运行 SQLite/MySQL/PostgreSQL × 同库/独立日志库，及相关审计/投影/凭据测试，禁止仅依赖全包顺序通过。
- 前端 typecheck/lint/test/build；依赖审计及升级兼容验证。实际浏览器连接隔离后端核验抽屉位置、滚动、取消/保存/刷新回查、非法输入不提交和中文文案。
- 保留原失败证据，修复后结果另记；未执行的全功能矩阵条目仍保持缺口。

安全交互参照 [OWASP Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)，继续使用既有敏感操作验证，不放宽认证或凭据日志边界。

## Implementation and Browser Verification

证据目录：_local/frontend-fixes-20261117/（本地记录：`_local/frontend-fixes-20261117`）。`before/` 保存本轮修改前 4467 个文件；`source/` 为中途候选，`final-source/` 为最终产品及测试代码快照。它们包含既有未提交工作，不能用 Git HEAD 单独重建。原先的复核目录不作改写。

| 项目 | 修复与实际验证 |
| --- | --- |
| 监控设置布局 | `settings-dialog.tsx` 改为 `settings-drawer.tsx`，复用 Sheet 和项目 drawer-layout；桌面 1440×1000 下 x=768、宽 672、高 1000，手机 390×844 下全宽全高。标题/按钮固定，内容独立滚动。 |
| F01 XLSX 行数 | 增加原 `xlsx_max_rows` 控件及说明。浏览器拒绝 0、1000001、1.5；保存 250000 后 API 和刷新均一致，再由页面恢复 200000。组件覆盖 1/1000000 两个合法边界。 |
| F02 IP 缓存时长 | 增加 10–10080 分钟控件；后端 21 个字段与前端 values/编辑控件逐一核对，无缺失或新增配置。桌面和手机拒绝 9、10081、10.5，保存 60 后重新打开和 API 回查一致，再由页面恢复 1440。 |
| F03 查询边界 | 日志超时限制为 1000–60000 毫秒；另三项上限为 8760 小时、300 秒、720 小时，0 预设保持不变。浏览器分别拒绝 120000/8761/301/721，分别保存合法上限、刷新回查并恢复原值。没有用慢 SQL 实测 60 秒截止时间，不将 UI 校验测试表述为运行时计时测试。 |
| F04 前端语言 | 补齐七种语言共 21 个键，修复 3 处日语缺失变量和 1 处越南语多余变量。通过指定脚本写入并执行 i18n:sync；全部 locale JSON 无 BOM。员工中文页面实际显示“查看关联审计”，点击进入操作记录。 |
| F05 后端语言 | 为已有 `log_export.col.log_raw` 资源补 Msg 常量；完整语言资源合同回归通过。没有删除资源或放宽一致性断言。 |
| F06/F07 审计合同及隔离 | 配置新旧值按已确认合同断言，同时检查凭据不泄露、所属用户读取及不匹配所属人投影排除；PAT fixture 使用所需 scope 的正式令牌，断言不改变旧令牌过渡截止时间。独立三库×同库/独立日志库测试通过。 |
| F08 开发依赖 | 锁定 minimatch 11.0.9 修复 [GHSA-g3hv-75wg-m249](https://github.com/advisories/GHSA-g3hv-75wg-m249)。上游 ts-morph 26/common 0.27 仍引用受影响的 10.x，故用 overrides；12 组 glob 语义、实际 ts-morph include/exclude 和更新后的 shadcn info 均通过，bun audit 返回空对象。 |

浏览器使用真实 Chrome 157 → 本地 Go 内嵌前端 → 实际 API → 独立 MySQL 5.7.44 主库、PostgreSQL 18.7 日志库和 Redis 7.4.11，不拦截请求伪造结果。主机运行 Go 1.26.11、Bun 1.4.4、Node 22.20.0，镜像 ID 与版本输出在 `versions.json`。

抽屉额外覆盖：取消二次验证后保持草稿并恢复保存按钮焦点；无通知目的地时，实际服务在 `/api/verify` 拒绝无效操作，取消验证后保留原草稿且配置不变；成功保存后关闭；普通取消再打开仍是已保存值。组件测试另覆盖保存接口失败保留草稿，不能将 mock 的写失败标为实际 HTTP 写失败。初次浏览器确实发现焦点落到 body，随后补修并通过桌面/手机回归；中途脚本误等 PUT，而校验实际发生在 verify，原失败记录也保留。

实际页面结果为 5 个顶层场景：桌面抽屉、手机抽屉、XLSX 上限、四项日志查询边界、员工中文关联审计。所有场景无 pageerror。截图在 `flow-screenshots/`，请求方法、路径和结果在 `browser-flows-root.json`、`browser-flows-mobile.json`、`browser-flows-employee-zh.json`；不保存登录令牌、密码或证明令牌。

## Candidate and Regression Evidence

最终产品快照 4471 个文件，`final-source-manifest.json` SHA-256：`d791d57f3315195d2e541a60bc670d23d368a28fc18fd1a916274b42fdb69b12`。快照之后仅追加本文验证记录，产品/测试文件未漂移。最终 Windows/amd64 `candidate-final.exe` SHA-256：`55715b977553123e558fc1c111e1f32c5c761b4c660a9b6f83d16669906ead97`，浏览器实际连接此二进制。

前端共 1881 个构建产物，清单 SHA-256：`4ef04d03bf80cd7cf76b10945d8861f5b2db4bfb20b200d690109788b87babf6`。版权头归位后重建，所有产物散列与原最终构建完全一致。1880 个非首页资源逐一从候选 HTTP 读取，均 200 且散列一致；`/index.html` 按既有行为 301 到 `./`，首页正常 200，首页运行时注释替换不纳入静态逐字节比较。

| 检查 | 结果与证据 |
| --- | --- |
| 最终前端全量 | `frontend-final-full.json`：`bun run test --maxWorkers=4` 通过，269 个文件、3057 项用例全部通过，1048.154 秒。包含原先超时的渠道配置文件 93 项及修复后的抽屉文件 4 项；没有提高超时、删减用例或放宽断言。 |
| 前端 typecheck / lint / build | 通过，见 `frontend-final-typecheck-retry.json`、`frontend-final-lint.json`、`frontend-release-rebuild.json`；lint 保留 8 个既有 warning。初轮夹具类型及格式问题已修正，原失败日志保留。 |
| 最终锁文件安装 | `bun install --frozen-lockfile` 通过，package.json 和 bun.lock 散列均未改变；见 `frozen-install-final.json`。 |
| 修改文件格式/编码/完整性 | 8 个 TS/TSX 文件 oxfmt 检查、相关 git diff --check、UTF-8/无 BOM 均通过。五个受保护认证/HTTP 文件与任务开始时逐字节一致；本地 `main` 不含这些路径，不能声称完成与最新上游 main 的对照。 |
| 依赖审计与 CLI 兼容 | `bun audit --json` 为 `{}`，exit 0；`dependency-smoke.json`、`shadcn-patched-info.json` 通过。 |
| 后端语言/审计定向 | `go-focused.json`：`go test -json -count=1 -p 1 ./i18n ./controller -run 'TestLocales\|TestAPITokenAuditDatabaseMatrix\|TestAccessTokenLifecycleAndLateRequests\|TestAuditField\|TestAuditChanges'` 通过，69.008 秒；在独立进程覆盖 SQLite 3.50.4/MySQL 5.7.44/PostgreSQL 18.7 及同库/分离日志。 |
| 升级夹具修复后独立复测 | `go-audit-upgrade-retry.json`：`go test -json -count=1 -p 1 ./controller -run '^TestAuditDatabaseMatrix$'` 通过，82.215 秒。三库均覆盖 fresh/模拟旧库 upgrade 和两次启动；MySQL 5.7 仅在隔离夹具模拟文档要求的预加列，未改变生产启动保护。 |
| controller 整包复测 | `go-controller-final.json`：`go test -json -count=1 -p 1 ./controller` 通过，460.854 秒；3543 个叶子用例通过、10 个条件场景 skip，没有剩余失败。含父测试的 test/subtest pass 数为 3839，不能与叶子数混用。 |
| 既有迁移保护专项 | `go-migration-protection.json`：真实三库 `TestUserChannelExclusionDatabaseMatrix` 通过，35.285 秒，保留 MySQL 5.7 缺列时快速拒绝/显式预迁移后成功、锁超时和重复迁移检查；未为通过审计夹具放宽产品迁移保护。 |
| Go vet | `go vet ./...` 通过，355.594 秒。 |

首轮前端全量实际结果：269 文件、3057 项用例中 3055 通过，1 项渠道配置 20 秒超时、1 项中途抽屉焦点失败。首轮 Go 全量命令 `go test -json -count=1 -p 1 ./...` 实际 exit 1：88 包通过、1 包失败、7 包 skip/no tests；叶子 test/subtest 为 20222 pass、1 fail、48 skip。唯一失败为上述 MySQL 5.7 升级夹具。此记录不能改写为“全量首次全绿”；修复后采用完整 controller 包复测确认，未把多个命令合并成一次全量通过。各命令的测试树及 skip 清单见 `go-regression-summary.json`。

## Acceptance Boundary

本次没有生产部署、生产数据库连接或新增产品数据库迁移，没有改动主 AI relay 链路、DB/Redis 共享资源用法。既有工作区修改已按开始时快照保留。

收尾已停止精确路径匹配的本地候选进程，删除本任务 label 的三个容器及其匿名卷，核对其他运行容器未变，移除四个临时凭据文件。构建、源码快照、脚本和脱敏验证证据保留；见 `cleanup.json`。

这些修复不能替代全功能覆盖矩阵（本地记录：`docs/design/release-frontend-coverage-20261117.md`）中尚未执行的逐操作/角色/状态测试，也不能代替目标 Linux、真实生产 schema、代理链路和容量验收；全量 Go 中被条件跳过的外部服务、ClickHouse、压测及专用数据库场景单列在证据中。当前明确的多人分配、自动账户处置、AI IP 采集、个人通知仍保留原设计状态，不宣称已实施或验证。
