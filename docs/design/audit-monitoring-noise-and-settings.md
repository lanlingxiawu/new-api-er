# 安全监控降噪、证据摘要与参数修复

日期：2026-11-21。状态：**用户已确认“普通 VPN/代理只记录，不单独告警；Tor、滥用及其他异常检测保留”，本地实现与定向验证已完成，未部署。** 用户另行确认异地移动阈值保持固定，要求详情突出重点、减少无用参数。

依据：本轮提供的 `security-alerts-2026-11-21.json`、截图及实际工作区代码。工作区原有修改保留；本轮没有读取或修改线上设置，没有部署。

## Goals and Scope

- 普通 VPN/代理仅保留观察与情报事实，停止单独创建匿名网络告警；Tor、明确滥用及其他异常规则继续独立判断。
- 证据默认展示触发条件、实际操作和 IP 摘要；完整参数仍可展开。
- 中、高、严重使用不同颜色，统一管理、个人和客户页面。
- 修复输入草稿和 URL 校验；去掉无法有效调整的本地缓存分钟参数。异地移动条件继续固定为 1000 公里/小时、200 公里。
- 保留现有规则制检测，不引入权重评分，也不增加策略开关或其他参数。

## Failed Login IP Annotation Follow-up — 2026-11-21

用户补充告警 #21 的登录失败截图：IP 为 204.1.132.33，尝试账号 root，但 IP 没有风险标注。该告警不在之前的 19 条导出中；截图中的搜索摘要不能证明当前配置的情报源命中。本节修复已存在的 IP 情报与风险展示链路，保留上述降噪政策及原有触发等级。

### Existing Logic and Related-Path Findings

- `ip_rules.go:ipIntelligenceCandidate` 的统一 `e.Success` 条件排除了所有 `auth.login_failed`；`store.go:commitSecurityBatch` 已保留 invalid_credentials / invalid_code 失败事件，但这些事件不会进入 pending 情报补查。
- `worker.go:scanPage` 在后台富化事件，`retryIPIntelligence` 使用同一轮预算、每次最多 20 个到期回执；`ip_provider.go:lookupProviders` 已有按供应商配置/IP 的缓存、每日额度和熔断，网络调用不在数据库事务或登录请求内。新增失败候选仍沿用这些边界，先处理本页成功事件，避免本页失败洪峰先用完预算。
- `ip_risk.go:RiskMarkers` 已按 event_id、IP、事件时间和告警可见性返回本地行为风险；false_positive 排除、resolved 单独展示。不能仅按 IP 把一个事件的结论扩散到所有历史记录。
- `store.go:GetAlert` 从告警内保留的证据读取详情，独立于原始审计日志的留存；原来没有返回风险标记。详情复用 `RiskMarkers`，对保留证据补充 `ip_markers`（普通告警最多 20 条，账号组合证据最多 201 条），避免再读可能已清理的源日志。
- `SecurityEventCard` 原来仅使用 `AuditIPAddress`，没有复用列表中的 `AuditIPCell`。桌面证据表的目标 ID 对失败登录恒为空，改为带标记的 IP；卡片使用同一组件。点击原因复用当前详情弹窗导航，避免叠加弹窗。
- `evaluateSensitiveFindings` 要求成功且已认证的 actor，自动处置资格另要求原始身份凭证；失败登录只补 Tor / 滥用情报，不产生“风险 IP 成功登录”或封禁被尝试账号。

### Lifecycle, API, Data and Failure Handling

新增有效的公网登录失败事件 → 既有后台扫描及情报预算 → 保存 IP 情报或 pending 回执 → 既有 Tor / 滥用规则 → 原有告警、证据关联与通知。仅 invalid_credentials / invalid_code、success=false 的规范事件进入补查；私网、无效地址及其他失败原因不查询。超过 24 小时的候选跳过；旧空状态回执不自动回填历史。没有情报的旧事件明确显示“未记录”，不等同于没有风险。

现有 Admin 详情 GET `/api/audit/security/alerts/:id` 和使用同一详情结构的管理导出增加 `ip_markers: IPRiskMarker[]`；访问角色、权限和各字段定义与已有 IP 标记接口相同。个人/客户详情使用独立受限投影，不增加数据。无新增端点、表、列、索引或配置。风险查询使用既有 `audit_security_alert_events.event_id` 索引和可见性过滤；失败沿现有详情错误处理返回，不把失败伪装为无风险。

### Main Chain Impact and Shared Resource Audit

登录与 AI relay 同步路径均不新增外部查询、SQL、Redis、锁或 goroutine。后台仍用既有独立连接池、IP 缓存和供应商额度表，受单轮预算、每日额度、超时、熔断、24 小时到期控制；每个 AI 请求新增操作为 0（100k RPM 同样如此）。详情仅对已有保留证据执行一次有界关联查询，访问安全告警表和关联表；这些表不在 relay 路径。无新增共享资源或全库扫描，不修改 users、tokens、channels。

### Validation

本地验证完成，未部署：

- `ip_coverage_test.go` 覆盖 22 种事件候选、私网排除、失败事件从 pending 到补全、重试/重放不重复判定、旧失败回执不自动补查、目标账号不能成为认证操作者；覆盖新增组合风险在详情/服务端筛选中的一致性、权限/IP 匹配、单 IP 情报不标记其他来源，以及登录失败/失败后成功/找回/拒绝/密码喷洒的阈值计数去重。
- 连同原有预算、代理降噪、跨规则证据筛选、多账号/多来源取钥、迟到登录修复、有效滥用证据和逐目标处置边界，共 18 个顶层回归用例分别在 SQLite、真实 MySQL（服务端 VERSION 26.10.0）、PostgreSQL 18.7 通过。每次使用独立 `audit_test_*` 数据库，测试后清理本次创建的数据库。命令为 `go test ./service/auditsecurity -run <coverage pattern> -count=1`；双数据库运行入口 `_local/monitor-parameter-review/run_policy_tests.py --coverage`，脱敏结果保存于 `policy-coverage-{mysql,postgres}.log`。
- 前端 5 个文件 45 个用例通过；最后修改事件卡后再跑受影响 2 个文件 17 个用例通过。`bunx tsgo -b`、涉及文件 oxlint / oxfmt 和 diff 检查通过。
- 真组件隔离浏览器预览，桌面 1440×960、手机 390×844，各测浅色/深色共 4 个场景：失败 IP 的“需关注”、原因导航、未记录情报、单弹窗及无横向溢出通过；无页面错误。使用合成数据，不代表访问了线上告警 #21。截图/结果位于 `web/_local/monitor-ui-review/failed-ip-*.png`、`ip-results.json`，预览服务已停止。
- 只读核对 [Spamhaus DROP 官方 IPv4 数据](https://www.spamhaus.org/drop/drop_v4.json)：本次取得的 1751 个网段未包含 204.1.132.33。[DROP 官方说明](https://www.spamhaus.org/faqs/do-not-route-or-peer-drop/)明确它只是 SBL 的小部分；其他黑名单和线上供应商当时的响应未核实。因此该 IP 的已知依据是截图中的失败行为，不能用搜索摘要替代本项目的滥用命中证据。

### Other Events and Correlation Coverage

用户追加要求检查其他遗漏与组合判定。逐项追踪 `CaptureSecurityEvent`、`CapturePasswordRecoveryOutcome`、session refresh、`Normalize`、`highRisk`、`successfulSensitiveOperation`、`EvaluateAccountCompromise`、风险关联/筛选/导出，发现同一候选条件还遗漏以下已有事件：已验证会话重用、已受理找回密码请求、明确权限拒绝、已提交密码重置，以及仅被 highRisk 捕获的其他敏感管理操作。补齐候选覆盖，但不新增规则、权重、阈值或自动处置资格。服务故障、过期请求、普通展示修改仍不加入风险查询；登录锁定已有同请求的 login_failed，避免再按锁定计一次失败。

风险标签的旧白名单还缺少已有的 multi_account_key_access、multi_source_key_access、new_source_channel_rewrite、new_source_sensitive_operation，补充为 high；已达到阈值的 recovery_requests / permission_failures 补充为 attention。共用同一高风险规则集合，保持徽标、原因、服务端筛选和导出一致。普通 sensitive_operation、bulk_sensitive_operations、bulk_key_access 仍不能单凭操作名称证明 IP 异常，不纳入 IP 风险标签。已有证据关联可立即参与读取，无需重放历史或新增告警；不存在关联的历史事件不伪造补齐。

组合判定保持原有身份、时间窗、客户端指纹和每个目标自己的成功取钥证据要求；IP 相同、普通 VPN、地域变化不能单独授权封禁。另验证重复事件和同一请求不会虚增认证失败/找回/拒绝计数，组合命中只纳入符合该规则的证据，不把相关查询读到的其他账号/时间的行一并标注。

进一步核实：认证计数分支未按请求去重，而敏感操作/账号取钥分支已有去重；补齐认证历史按事件与同动作 request_id 去重，并统一可见性，避免重复采集抬高阈值。`store.go` 的通用同账号/同动作证据逻辑还会给单 IP 情报告警附带其他 IP 的历史操作；限制 Tor/滥用/异地检测只追加本次命中事件，保留此前真实命中的告警证据，不扩散当前 IP 结论。历史证据不自动改写。

## Existing Logic and Related-Path Findings

### 导出证据

导出含 19 条告警：12 条匿名网络、6 条敏感操作、1 条连续登录失败。匿名网络占 63.2%，涉及 62 个判定快照；其中能看到完整正文的 44 个去重事件包含 39 次 AI IP 观察和 5 次登录，全部 VPN=true、proxy=false、Tor=false、abuser=false。另 18 个判定缺少完整事件正文。该比例只说明降噪候选数量，不是真实误报率，也不表示所有账户安全。72 条通知回执均为 skipped/disabled，样本主要反映页面噪声。

### 已核实的端到端行为

| 实现位置 | 修改前行为及本轮处理 |
| --- | --- |
| `service/auditsecurity/rules.go:Evaluate/highRisk` | 固定风险等级 1/2/3，没有加权分数引擎。部分计数可配置，其他规则为固定条件。本轮不改规则等级、敏感操作政策或自动处置资格。 |
| `service/auditsecurity/ip_rules.go:evaluateIPIntelligenceFindings` | VPN、proxy、Tor 任一为 true 都生成中风险匿名网络告警；登录、敏感操作、AI IP 观察共用此函数。本轮只将该分支改为明确 Tor=true，滥用等后续分支照常执行。 |
| `ip_rules.go:retryIPIntelligence` → `store.go:Apply` | 首次扫描与情报补查共用判定和事务路径。降噪放在判定生成处，因而列表、统计、个人/客户投影和新通知均生效，而非仅隐藏主表行。 |
| `ip_provider.go:reliableGeoForTravel`、`ip_rules.go` | 异地移动检查坐标质量、Anycast、卫星网络，原有地理基线和匿名属性处理不变。本轮不另行修改异地检测策略。 |
| `setting/operation_setting/audit_security_setting.go` | 读取和规范化均固定移动速度/距离；`TestTravelThresholdsCannotBeConfigured` 明确此合同，按用户要求保留。 |
| `settings-drawer.tsx:numberFields` → `ip_provider.go:ipProviderCacheTTLSeconds` | UI 接受 10～10080 分钟，实际本地缓存上限 60 秒，合法分钟值均不能改变该上限。本轮移除误导性的输入，保留后端兼容配置及现有缓存行为，不添加秒级替代项。 |
| `ip-providers-field.tsx` → `lib/ip-providers.ts` | 主机每次键入立即 split/trim/filter 再 join，吞掉逗号；每日上限清空立即变 0，而后端 0 表示不限量。本轮保存前才规范化，编辑期间保留原始字符串。 |
| `settings-drawer.tsx:schema` → `audit_ip_provider.go:validateIPProviderEndpoint` | 前端曾允许 HTTP、凭据、query、fragment，后端拒绝。本轮前端对齐 HTTPS 且不含后三者，在二次验证之前提示具体问题。 |
| `controller/option.go:UpdateOptionGroup` → `model/config_group.go:SaveConfigGroup` | Root 浏览器身份、绑定最终 values 的二次验证、字段白名单、整组校验、事务持久化与配置快照发布保持不变。普通数字并非全部不能保存。 |
| `SecurityAlertDialog` → `SecurityEvidence` → `SecurityEventCard` → `AuditDetailFields` | 原页面递归展开所有 IP 字段，重复展示来源 UUID。桌面和移动端共用事件卡片；本轮用局部摘要并折叠技术详情，不改变通用递归组件。 |
| `security-alert-columns.tsx`、`audit/self/columns.tsx`、客户详情 | 中、高原来均为 warning，客户部分位置只有文字。本轮统一 info/warning/danger，使用现有主题 token。 |
| `security-rules.ts`、`security-rule-summary.tsx` | 匿名网络曾统称“登录”，但样本多数是 AI 请求。改为“匿名网络访问”，按保存的快照区分历史 VPN/代理/Tor 和新 Tor 命中条件。 |
| `finding_snapshot.go`、`customer_access.go`、`self_access.go`、`export.go` | 告警与判定快照供不同权限投影使用，规则版本还参与客户访问资格。保留版本 2，在新匿名网络快照加整数维度 tor=1，不把旧 VPN 告警解释为 Tor。 |

排查覆盖原有 11 个普通数字字段、9 个自动处置数字字段，以及总开关、通知地址等表单。三组前端载荷已通过隔离真实数据库验证保存、配置快照和 GET 读回；运行时另查明缓存参数语义不符。截图中的“监控异常”依赖健康状态与丢弃计数，导出不含健康响应，不能由告警数量推断原因。

## Business Logic and Edge Cases

### 代理降噪

`IsVPN` / `IsProxy` 事实保持原值，不单独生成 finding；`IsTor=true` 仍产生 anonymous_network，等级与原来一致；`IsAbuser=true` 独立产生 ip_abuse。VPN 与滥用同时为 true 时，只抑制代理分支，不提前返回。nil 始终表示未知，不能当作 false 或安全。

策略作用于后续扫描和未完成的情报补查，不重扫已完成事件。历史告警、调查记录、判定快照和已排队的通知保留原生命周期，不自动关闭、删除或重写。原有自动动作、登录失败、凭据重放、取钥关联和异地移动继续独立判定。新匿名快照保存 `dimensions: {detected: 1, tor: 1}`；历史缺少 tor 维度时保留旧条件说明。

### 证据展示

告警标题直接显示规则与风险；证据页先显示简短触发条件和实际操作，再显示默认折叠的命中快照。操作者、时间、IP、动作/结果、目标和有意义的参数保持可见。

`SecurityIPSummary` 合并地点和网络组织，显示明确命中的 VPN/代理/Tor/滥用标记及最多三条证据摘要；未知标记合并显示。普通 VPN/代理使用 info，Tor 使用 warning，滥用使用 danger。只有四项均明确 false 才显示无命中，不能把缺失查询结果当作安全。

请求编号、客户端指纹、完整 IP 对象进入默认关闭的“技术详情”，来源 UUID、坐标、时间和原始 false/0 仍能查看。复用 `DetailRow`、`DetailSection`、`StatusBadge`、`Collapsible`、`AuditDetailFields`。切换事件重置展开状态；异常 facts 保留原回退。隐藏字段不是权限控制，详情 GET 权限不变。

### 设置草稿

主机和每日上限作为表单字符串保留，跨标签和背景刷新沿用已有 keepDirtyValues 行为。保存前验证非空整数 0～1000000000，以及不含端口/路径的主机或 IP 列表，再统一规范化并生成 provider JSON。0 由用户明确输入才表示不限量。内部草稿键不传给后端。最终 JSON 先生成，再申请绑定这一载荷的 security proof；取消和失败保留草稿。

删除缓存分钟输入但保留后端原键兼容，不发送新缓存值。异地移动阈值继续以固定说明显示。

## Data Flow and API Contracts

设置 GET → 表单草稿 → 前端校验/规范化 → 绑定最终 values 的 Root 二次验证 → PUT → 既有后端整组校验 → options 事务 → 配置快照发布 → worker 下轮读取。

既有审计/IP 观察 → 增量扫描 → IP 查询/缓存 → 新判定条件 → 事件回执、finding、alert 与游标事务 → 列表、统计、导出、个人/客户投影、通知。

- GET `/api/option/?scope=security.audit-monitor`：仍为 `{success,data:[{key,value}]}`，无新增字段。
- PUT `/api/option/group`：仍为 `{scope:"security.audit-monitor",module:"audit_security_setting",values:{...}}` 和 `X-Security-Proof`；Root 浏览器身份及 proof 要求不变。
- 详情 `/api/audit/security/alerts/:id` 与使用同一详情结构的管理导出增加 `ip_markers`；其他列表、summary、IP 标记和个人/客户接口形状不变。管理读取保留 AdminAuth 与 audit/security read；管理动作仍需 manage。
- 没有新端点、公开接口或权限豁免。

## Data Model, Config and Error Handling

无新增表、列、索引、迁移或配置键。使用现有 finding dimensions JSON 保存 tor 标记，旧记录向后兼容。详情增加一次针对保留证据 ID 的既有风险关联查询，沿用主键索引；列表保持时间范围、游标及现有分页，不增加全表聚合。

前端格式错误在提交前给出可操作的提示，未知 IP 情报明确显示未知。持久化和 worker 错误保留现有日志、重试、超时、恢复与安全错误响应，不新增吞错路径。身份验证取消或 API 保存失败不丢弃草稿。

## Main Chain Impact

未修改 relay、middleware、计费、token 检查、日志采集或认证流程。新的判定条件只在既有后台安全 worker 执行；AI 请求同步路径新增 SQL、Redis、HTTP、锁、goroutine 均为 0。

### Shared Resource Audit

| 资源 | 与 relay 的关系和本轮影响 |
| --- | --- |
| options / 配置快照 | 沿用管理端保存，未新增键或 relay 设置查询。 |
| audit_logs / IP 观察队列 | 保留既有保存后非阻塞队列与有界扫描，不增加采集或读取。 |
| audit_security_events / geo_baselines | 监控专用，事件事实和地理基线处理不变，无新增查询。 |
| audit_security_alerts / findings / alert_events / states / deliveries | 监控专用，普通代理减少告警、关联证据和通知写入；原始回执保留。 |
| IP provider state / 本地缓存 | 保留原配额、超时、缓存键和 60 秒上限，不修改数据结构。 |
| users / employee_profiles / tokens / 认证缓存 | 原有身份读取与自动动作不变，无新增共享表、锁或缓存访问。 |
| DB/Redis 连接池和 worker | 沿用监控隔离池（主库最多 2、每个日志池最多 1）与固定 worker，没有新连接池、goroutine 或 Redis key。 |

### Concurrency Analysis

30k RPM 约 500 请求/秒，100k RPM 约 1667 请求/秒；每个 AI 请求的新增 DB/Redis/锁/协程成本均为 0。后台保留每页最多 500 条、每事务 20 条、IP 查询每轮默认 5/上限 20、单次 1～8 秒超时。降噪只收窄既有常数时间分支并减少写入。本段为静态影响分析，不是负载实测。

## Implementation and Verification

实现文件：`service/auditsecurity/ip_rules.go`、`ip_retry_test.go`；前端安全监控的事件卡片/IP 摘要、规则说明、设置/provider 输入、统一风险 variant，以及个人/客户风险显示；七种语言翻译。未修改受保护的 auth/HTTP 文件。

验收覆盖：VPN/proxy/未知/Tor/滥用 × 登录/敏感操作/AI 观察；记录留存；其他检测不受抑制；历史快照条件；IP 补查；摘要与完整参数切换；未知标记；输入草稿、URL 拒绝、取消/失败/成功保存；类型、lint、真实浏览器亮暗主题与窄屏。

### 排查阶段已完成的验证

- 固定阈值和设置校验：`go test ./setting/operation_setting -run 'TestAuditSecuritySettingsValidation|TestAutoBanConfigurationBoundaries|TestTravelThresholdsCannotBeConfigured' -count=1` 通过。
- 修复前临时前端诊断 9 项中 3 项通过，6 项成功复现主机分隔符、空上限及四类 URL 缺陷。该失败报告是修复前证据，不属于修复后结果。
- `python _local/monitor-parameter-review/run_backend.py`：隔离真实数据库中，三组前端载荷的 options、配置快照、GET 读回一致；proof 缺失、载荷不匹配、正确绑定与重放用例通过。四类非法 IP URL 被后端拒绝。
- 本地服务端版本：MySQL 协议容器 `complete-preflight-20261119-mysql` 的 `SELECT VERSION()` 返回 **26.10.0**；PostgreSQL 容器为 **18.7 (Debian 18.7-1.pgdg13+2)**。凭据不写入报告。测试创建唯一命名的隔离数据库，仅清理自己的夹具，未连接线上。

### 修复后验证

- `python _local/monitor-parameter-review/run_policy_tests.py`：上述两个隔离数据库中各 9 个顶层测试通过。内部命令为 `go test ./service/auditsecurity -run 'TestOrdinaryProxy|TestIPRetry|TestSelfHostedEvidence|TestIPResponseRequires|TestSingleRiskyIP|TestNewSourceSingleKey|TestIPIntelligenceQuality' -count=1 -v`。新用例覆盖六类 IP 情报与三种操作，确认普通代理无独立 finding、情报事实留存、Tor/滥用及敏感操作继续命中；既有补查游标、查询预算、证据留存和自动处置证据要求测试通过。修改判定前同一回归确实因额外代理告警失败，未知与 Tor 用例通过；前后日志见 `_local/monitor-parameter-review/policy-{before,after}-{mysql,postgres}.log`。
- 在 `web/` 运行 `bun run test`，指定 `security-event-card.test.tsx`、`settings-drawer.test.tsx`、`ip-providers-field.test.tsx`、`security-rule-summary.test.tsx`、`security-relations.test.tsx`、`monitor.test.tsx` 和 `customer-audit-dialog.test.tsx`：**7 文件、79 测试通过**。涵盖新旧匿名网络条件、折叠参数、未知值、URL 拒绝、草稿及 security proof 载荷。
- `bunx tsgo -b` 通过全前端类型检查。仓库原 `bun run typecheck` 使用当前 Bun 不支持的 `bun check -b`，本次直接调用已安装的 TypeScript 检查器，未修改无关脚本。
- `bunx oxlint -c .oxlintrc.json <本轮 20 个前端文件>` 通过；格式化及作用域内 `git diff --check` 通过。
- 七种语言经脚本补齐并执行 `bun run i18n:sync`；静态复查本轮组件 136 个翻译键全部存在，七个 JSON 均为无 BOM UTF-8。
- `python web/_local/monitor-ui-review/review.py`：实际组件和主题通过独立本地 Rsbuild 预览，使用合成数据与只读替身，不连接真实后端。Chromium 的 1440×960 / 390×844、亮/暗主题四种组合通过：重点信息可见、UUID 默认隐藏且可展开、三等级计算后颜色不同、无横向溢出、无页面异常。已查看截图；图像及 `results.json` 在 `web/_local/monitor-ui-review/`。这是浏览器组件验收，不是线上全链路验收。

没有 schema/驱动/迁移改动，本次验证不替代整应用升级演练。未部署、未进行线上配置读回或生产负载测试；线上现有历史告警仍会保留。

## Trigger Severity Assessment — 2026-11-21

2026-11-23 的复核收尾另见 [安全告警复核收尾](audit-security-review-completion-20261123.md)：补齐个人/客户详情去重、实际来源时间快照、未知操作的中性账号标签、当前页关联事件分组；旧规则继续遵守现有可见性限制，管理员清理已通过三种数据库验证。类型检查脚本也已修正为 `tsgo -b`。

本节记录 2026-11-21 的分级评估。2026-11-23 已按该表实现：普通 PAT 创建和已知权限收缩不再单独告警，但仍计入批量关联；新来源单次操作和滥用 IP 登录不再一律严重；批量告警只在凭据、目标改写、破坏、额度、安全因子移除或权限增加时为严重。阈值、自动处置资格和 `ipRiskLevel` 标记等级没有改变。新分级使用新的规则版本，不并入旧的更高告警，也不改写旧快照。

### 复核证据与相关路径

- 导出中 6 条高风险告警里，#13、#14、#16、#19 均为普通用户单次 `access_token.generate`，占 4/6。可见证据没有其他已确认异常，但 VPN/proxy/abuse 有未知值，不能据此证明操作安全。#18 为两次管理员额度增加；#15 为一次渠道状态变化和两次同时涉及 `setting` 的修改，不能把后两次当作纯模型/分组维护而直接降级。
- 实施前 `rules.go:highRisk` 把普通 PAT 创建/权限变化标为高，管理员 PAT 标为严重，且不区分权限增加与收缩。`Normalize` 的 `PermissionIncrease` 现已用于单项分级：已知收缩只记录，未知方向仍告警。名称修改走独立 `access_token.rename`，保持不告警。
- `controller/access_token.go:CreateAccessToken/UpdateAccessToken`：创建和权限变化有权限范围校验及绑定具体操作的二次验证。普通创建不等于绕过认证；二次验证本身也不保证账号没有被盗。`controller/user.go` 的权限变更事实只表明确有变化，没有区分授予和撤销方向，不能照搬 PAT 的扩权判断。
- `rules.go:Evaluate` 与 `store.go:loadCorrelationHistory`：默认 5 分钟累计影响数 ≥5 即为严重；按同操作者、资源类型和可见性汇总，可混合不同敏感动作。它不是“五个独立攻击证据”，一次影响 5 个对象的请求也可触发。默认批量取钥则为 5 分钟 ≥10 个不同渠道。
- `sensitive_operations.go:evaluateSensitiveFindings`：新管理来源的一次敏感操作、精确且有效的滥用 IP 登录、滥用 IP 敏感操作都直接为严重。新来源要求有至少一天、近 30 天活跃的旧管理基线，但不要求独立的新设备或额外异常证据；代理换出口仍会命中。
- `store.go:commitSecurityBatch` 与 `ip_rules.go`：新登录来源普通用户为中、管理员为高；异地移动为高，可靠性检查未排除代理出口。固定 1000 公里/小时、200 公里仍按用户要求保留。
- `account_compromise.go` 的多账号/多 IP 取钥和新来源批量改上游，有成功登录/实际操作、去重、时间窗口及对象数量要求；与单一 IP 属性有明显区别。会话重放由认证层确认，分级继续为严重。
- 已核对 `TestRiskRulesRespectResultsAndActualChanges`、`TestHighRiskOperationsDoNotRequireSuspiciousLogin`、`TestChannelRoutineMaintenanceAlertPolicy`、`TestChannelRoutineHistoryDoesNotEscalateAddressChange` 及 `TestNewSourceSingleKeyCreationAndIPRetryKeepOriginalIdentity`。其中已有测试明确要求渠道删除、查看渠道密钥、额度修改等可独立触发，不应在降噪时统一变成“只有异常登录才检测”。

### 建议分级

“仅记录”表示不创建该操作的独立安全告警，原始审计、观察事实及其他关联检测仍保留；不是新增一个前端风险等级。

| 触发场景 | 当前等级 | 建议与边界 |
| --- | --- | --- |
| 普通 VPN/代理属性 | 仅记录（上一轮已修改） | 保持。Tor 仍为中，滥用信号仍为高。 |
| 普通用户单次创建 PAT | 高 | 仅记录；新来源、滥用、批量取钥等独立关联仍评估。 |
| 管理员创建 PAT / PAT 真正扩权 | 管理员严重、普通用户高 | 管理员保持高，普通扩权按实际权限影响保留中/高；仅权限收缩只记录。不能仅凭操作者角色判严重。 |
| 单次渠道启停、常规维护 | 多数高；纯模型/分组/优先级/权重已不告警 | 启停可降中；已不告警的纯维护继续保持。`key/base_url/proxy/header_override/param_override` 等影响凭据或请求目标的改动保留高，不能把 `setting` 全部视为普通配置。 |
| 手工额度变更、删除账户/渠道、移除安全因子、修改关键权限 | 高或严重 | 额度/破坏性操作/移除安全因子保留高。权限撤销不应与实际提权同为严重；缺少变更方向时保守保留，不凭操作名猜测。样本不足以校准“正常额度”或大额阈值。 |
| 仅新 IP 登录 / 仅 IP 异地移动 | 新登录中/高；异地高 | 中；代理环境中此类信号不宜单独为高/严重。移动阈值不变，地理精度和未知属性继续保留。 |
| 新来源 + 一次敏感操作 | 严重 | 普通令牌创建等为中，取钥/改上游/扩权等为高；需要更强关联才升级严重。 |
| 精确有效滥用 IP + 登录 | 严重 | 高；恶意来源不直接等于账号已失陷。与凭据导出、目标改写等高影响动作关联时才保留严重。 |
| 默认 5 分钟累计影响数 ≥5 | 严重 | 按实际动作区分：一般操作最多中/高；批量破坏或凭据/目标改写可严重。不能简单保留“所有单项风险动作累计到 5 就严重”。 |
| 5 分钟同账户登录失败 ≥5 | 中 | 保持，错误密码/验证码计数；服务故障和过期请求继续排除。 |
| 密码喷洒（5 分钟 ≥20 次、≥5 个账户标识）；失败后成功登录（15 分钟 ≥5 次失败） | 高 | 保持，已有重复或关联证据。以上为代码默认/固定阈值，不冒充线上当前设置。 |
| 10 分钟 ≥3 次找回密码；同令牌 5 分钟 ≥10 次权限拒绝 | 高 | 中；单凭被拒绝/请求恢复，不等同于成功入侵。 |
| 会话重放；多账号登录后取钥；多 IP 登录后取钥；新来源批量改上游；批量查看渠道密钥 | 严重 | 保持；不能仅因 VPN/代理而豁免实际成功取钥、改写或已确认重放。 |

优先级：先处理普通 PAT 的单项告警、新来源单次操作一律严重、以及普通敏感操作累积自动升级严重，再调整弱信号等级。样本只覆盖 19 条告警，以上是按代码和业务场景作出的分级建议，不是统计验证后的误报率或最优阈值。

### 后续实现约束

- `highRisk` 同时决定扫描候选、历史查询、单项告警和批量计数；不能仅把返回值改为 0 而遗漏其他关联的事件。需区分“保留检测事实”和“创建独立告警”。改变普通操作等级也必须处理批量升级分支。
- 自动封禁/清理由 `queueAutomaticAccountResponse` 按规则选择、动作、身份证据和时效判断，**没有 severity 门槛**。只降级不会阻止已启用的自动处置；任何实施方案必须另行核对相应规则资格，不能向用户宣称降级等于取消封禁。
- 合并告警使用历史最高 severity；同规则版本 15 分钟内仍可能并入旧高等级告警。实施时需要明确版本和展示过渡，同时兼容客户/个人访问投影，不能重写旧快照。
- 方案限定既有后台评估，不修改 AI 请求主链、认证证明或原始日志采集。不新增配置、端点或表；如需区分权限/渠道 setting 子项，先补可信、脱敏的变更事实，不能用前端猜测代替后端证据。
- 2026-11-23 已在既有规则中实现上表，没有新增配置、端点或表，也没有改动 AI 请求主链。自动封禁仍只看规则和动作，不看 severity。

分级原则参考 [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html#which-events-to-log)：按业务风险选择告警，结合重复、关联和上下文，避免告警过多掩盖真实异常；[Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#adaptive-or-risk-based-authentication) 将 IP/设备信息视为辅助风险信号。上表具体等级为本项目建议，不是 OWASP 规定的阈值。
