# 管理员 PAT 机器人辅助只读能力

日期：2026-11-20。用户在明确三项接口、逐项授权和返回范围后要求“进行修改”，本次按该范围实施；充值仅使用已有核实补单接口。本文先于实现建立，完成后补充实际验证结果。不部署、不修改真实账号授权。

2026-11-21 经用户确认已在本地扩展全站统计，具体接口、授权和本轮验证见 [管理员 PAT 全站统计查询](admin-token-sitewide-statistics.md)。下文统计契约已同步；2026-11-20 验证记录仍仅代表当时的单用户实现。尚未部署或修改真实账号授权。

## Goals and scope

为普通管理员的 PAT 提供三项默认关闭、分别授权的能力：分组名称、精简员工选择列表、全站或指定用户的有界消费统计。保留原网页登录请求的响应和行为。仍不开放分页用户列表、完整用户详情、封禁/解封/提权、原始日志、独立支付查单及强制补单。

## Existing Logic and Related-Path Findings

以下保留 2026-11-20 设计时的实现基线；2026-11-21 全站统计的当前行为和验证以文首链接及下方接口契约为准。

- `middleware/auth.go:authHelper/enforceAccessTokenRoute` 先识别登录 JWT 与 PAT；session-only 路由先拒绝 PAT。`middleware/admin_access_token.go` 再校验普通管理员、有效期不超过 30 天、管理白名单和主库实时授权。不能只修改路由 scope。
- `service/admin_access_token.go:AdminTokenRouteModule` 当前仅列出精准用户操作、员工归属、核实补单和本人渠道。`service/access_token_scope.go:AccessTokenCatalog` 也只签发这些已授权管理 scope，两处须同步扩展。
- `service/authz/resources_admin_access_token.go` 及 `AdminAccessTokenPermissions()` 驱动 Root 编辑、保存校验和用户详情回读。`ReadCurrentPermissions` 只接受普通管理员个人的显式 allow；新动作没有角色默认授权。
- `controller/group.go:GetGroups` 从 `ratio_setting.GetGroupRatioCopy()` 的内存快照返回名称，无 DB/外部调用；网页 `web/src/features/users/api.ts:getGroups` 已使用这个契约。
- `controller/employee.go:AdminListEmployees` 返回完整员工档案、邮箱、备注、成本、利润、提成并执行多组聚合；`model.GetAllEmployees` 还支持按财务指标排序。PAT 必须在进入这套逻辑前分流，执行只取选择字段的查询。网页继续使用原 page/page_size 契约。
- `controller/log.go:GetLogsStat` → `model.SumUsedQuotaWithFilter` 返回消费额度及实时 RPM/TPM，现有文本筛选允许 `%`；默认时间范围不适合作为“必须指定客户和日期”的授权边界。PAT 先在主库精准确认用户，再以不可由请求覆盖的用户 ID 查询独立日志库。
- `model.Log` 已有 `idx_log_export_user_time(user_id,created_at,id)`。新增 UserID 过滤必须同时进入 `LogFilter.apply`、全站计数缓存适用判定、统计缓存适用判定，防止跨用户复用全站缓存。现有 HTTP 查询解析器不接受此内部字段。
- `controller/admin_token_user_test.go` 已覆盖真实鉴权、分项保存、签发、撤权和限定写入；本次扩展同一组专项测试。现有员工测试仅测网页接口，需补 PAT 精简投影和分页隔离断言。
- 前端 `admin-permissions-editor.tsx` 按 catalog 的 actions 动态渲染和保存，访问令牌 scope 也由后端目录驱动；复用组件，不新增按钮、独立页面或 HTTP 客户端。七语言文案及静态 key 同步补齐。

## Authorization and API contracts

现有三个路由保持 `AdminAuth()`；员工路由继续使用 `AdminMenuEmployeesView`。有效权限均为：启用的普通管理员账号 ∩ 管理令牌总开关 ∩ 本项显式许可 ∩ 用户菜单 ∩ 对应 PAT scope。员工选择另需员工菜单。Root PAT、旧令牌、超过 30 天的管理 PAT 仍拒绝。三个请求均不要求逐次 proof。

| 管理许可 | PAT scope | 路由 |
| --- | --- | --- |
| `groups_read` | `group:read` | `GET /api/group/` |
| `employees_read` | `user:employee_read` | `GET /api/admin/employee` |
| `users_stats` | `log:stats_read` | `GET /api/log/stat` |

### 分组名称

无 query 参数，返回 `{"success":true,"message":"","data":["default","vip"]}`。名称排序稳定；不返回系统倍率、权限配置或预填组。仅打开此项不允许修改分组。

### 员工选择

接受 `page`（默认 1，1–10000）、`page_size`（默认 20，1–100）、`status`（默认 1，0=全部、1=启用、2=禁用）。拒绝重复/未知参数和无效数值。此表为管理人员目录，沿用有限页码分页；不扫描消费/提成历史。

只读取未删除且启用的普通用户所对应的员工档案；档案状态按 status 筛选，按档案 ID 降序。返回：

```json
{"success":true,"message":"","data":{"items":[{"id":7,"user_id":45,"username":"staff_a","display_name":"员工 A","status":1}],"total":1,"page":1,"page_size":20}}
```

`id` 是分配/解绑接口需要的员工档案 ID，`user_id` 是用户 ID。空页返回空数组。读取计数和本页均带请求取消及最长 5 秒预算，不调用财务聚合，也不创建新缓存。

### 全站及单用户消费统计

不提供 `user_id` 和 `username` 时返回全站汇总，时间也同时省略时默认最近 60 秒。复用 `users_stats` 与 `log:stats_read`，此前已获统计授权的有效 PAT 随服务升级获得此全站查询能力。

单用户请求仍须提供 `user_id` 或 `username` 中的一项，用户名按等值匹配（含 `%`、`_` 也不作通配），并显式提供两个时间戳。全站请求也可显式提供两个时间戳。时间必须为正整数 Unix 秒、起止顺序正确、结束不晚于当前时间、跨度至多 30 天。兼容 `type=2`，不接受其他日志类型或额外筛选。拒绝重复参数、空目标和未知目标，不会误退为全站；单用户目标仍只允许角色低于操作者的未删除账号。

```http
GET /api/log/stat
GET /api/log/stat?start_timestamp=1795046400&end_timestamp=1795132800
GET /api/log/stat?user_id=123&start_timestamp=1795046400&end_timestamp=1795132800
```

返回原有 `quota/rpm/tpm` 并附带 `quota_per_unit/start_timestamp/end_timestamp`，单用户请求额外返回 `user_id`。`quota` 统计指定时间区间的消费日志；`rpm/tpm` 按所选对象（全站或单用户）统计当前最近 60 秒，不受历史额度时间窗口影响。全站复用小时缓存，单用户按 UserID 读取且不得命中全站小时缓存。

## Data flow and lifecycle

Root 经原权限编辑和安全验证保存新分项 → 管理员通过原会话签发/扩展对应 scopes → 请求经现有令牌识别、限流和管理许可实时回读 → 严格参数校验 → 精简查询 → `success/message/data`。旧令牌不会自动增加新 scope。关闭总开关/分项或撤销令牌后，后续授权检查立即拒绝；在途请求按既有语义结束。

新增只读请求继续记录 PAT 访问审计及 last-use；不修改余额、倍率、归属、订单或会话。API 返回和日志不包含 token、凭据、邮箱、备注、利润或原始请求内容。

## Data model, indexes and configuration

无表/列/索引/迁移变更。许可继续存 `casbin_rule`；新增 scope 继续存 `user_access_tokens` 的现有 scopes。无环境变量或新业务配置；用默认关闭的三项许可开关控制开放。查询预算复用现有日志超时并加 5 秒请求上限。

访问模式：用户精准选择使用 users 主键/username 唯一索引；员工查询使用 employee_profiles 主键、user_id 唯一索引并连接 users 主键；日志按 user_id 等值及 created_at 有界范围，使用既有 idx_log_export_user_time。不增加持续写入 logs 的索引开销。实施时在隔离真实 MySQL、PostgreSQL 的代表性数据上执行 EXPLAIN 并记录，不能用 LIMIT 或编译替代。

## Main Chain Impact and Shared Resource Audit

本功能不挂接 AI relay、不改变转发中间件/计费/日志写入；30k–100k RPM 的 AI 请求新增执行成本为零。下述均发生在管理请求：

| 资源 | 操作及隔离 |
| --- | --- |
| `users`、`casbin_rule` | 既有管理鉴权限时读取；目标/员工只读查询，无 FOR UPDATE。users 与 relay 共享，禁止无界搜索或批量修改。 |
| `employee_profiles` | 单页目录读取，独立于提成汇总，不访问财务表。 |
| `logs`、日志连接池 | 与 relay 异步落库共享；按有界时间查询，单用户另加 UserID 过滤。沿用 5 秒 SQL 预算和已有 API 限流；全站计划与缓存影响见 2026-11-21 设计。不加索引、锁、worker 或连接池。 |
| 分组内存快照 | 读取独立拷贝并排序，沿用快照同步；不变更 relay 的倍率或缓存。 |
| PAT/审计表、Redis | 沿用既有鉴权、last-use、审计与限流；没有新 key，也不使用全站统计缓存存用户结果。 |

## Error handling and edge cases

鉴权沿用 401/403 及现有稳定错误码；无效参数使用 `ApiErrorI18n` 的业务错误。新统计参数错误提供 en/zh 可执行说明。DB 失败/取消先记录上下文日志，返回脱敏消息；不返回 GORM 原错误。员工未知字段、排序、关键字均不透传到原网页 handler。检查 soft-delete、账号禁用、员工档案禁用、分页边界、重复/越权/未知用户、时间边界及缓存隔离。

## Verification plan

先写测试再实现。专项测试覆盖：三项默认拒绝、逐项签发/调用、缺 scope/缺菜单/关闭开关/即时撤权、Root/旧版/过期 PAT 拒绝、保存回读、JWT 原行为；员工精简字段/状态/空页/禁用用户；统计双用户隔离、真实包含通配符的用户名、金额/时间边界、近期 RPM/TPM 与历史额度的区别、开启全站缓存也不能污染单用户结果；旧限定写接口继续通过。

真实 MySQL、PostgreSQL 均运行专项 HTTP/SQL 契约测试和查询计划；数据库为本任务隔离容器和库。生产为 MySQL 主库 + PostgreSQL 日志库，另做该组合。SQLite 用于兼容回归，不声称代替真实方言。无 DDL 变更，升级/两次启动不引入新的迁移步骤。前端运行权限编辑器相关 Vitest、七语言完整性、typecheck、lint，验证五个上游鉴权文件未改动。不调用真实 AI/支付服务，不连接生产。

## Security references

按 OWASP [ASVS 5.0](https://asvs.owasp.org/)、[Authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 和 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 核对默认拒绝、每请求服务端授权、对象边界和会话/PAT 区分。此次不更改登录、Cookie、JWT 或安全验证流程。

## Implementation and verification results

实际实现：

- `service/authz/resources_admin_access_token.go` 增加三项许可；`service/access_token_scope.go` 同步签发目录和说明，逐项判断账号当前许可及菜单。现有权限保存/回读和前端动态目录直接复用。
- `service/admin_access_token.go` 与 `middleware/access_token_routes.go` 增加精确路由白名单及 scope。
- `controller/admin_token_reads.go` 负责 PAT 严格参数、菜单/模块检查和 5 秒预算；原 `GetGroups`、`AdminListEmployees`、`GetLogsStat` 在入口分流。网页登录仍走原 handler。
- `model/admin_token_reads.go:ListEmployeeChoices` 返回五字段投影。`model/log.go` 增加内部 UserID 过滤，计数和消费额度两处全站缓存判定均排除该过滤。
- 后端新增参数错误同步到实际使用的 `en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`；前端六个文案通过 i18n 脚本写入七语言并执行 sync，对比任务前快照确认每种语言仅新增这六项，没有改动既有值。临时脚本已移除。

### 验证环境和命令

2026-11-20，Go 1.26.11、Bun 1.3.13。使用本任务独占 Docker 容器，端口仅绑定本机环回；数据库版本由服务端查询：MySQL `26.10.0`、PostgreSQL `18.7`，Redis `7.4.11`。所有测试显式设置 `SQL_DSN`、`LOG_SQL_DSN`、`REDIS_CONN_STRING`，覆盖 `.env`；专项数据库由 `newAuditTestDatabase` 单独创建。没有连接生产、调用真实 AI 或支付服务。

| 检查 | 结果 |
| --- | --- |
| 实现前 `go test ./controller -run 'TestAdminToken(RobotReadCapabilities\|RootGrantAndIssuanceLifecycle)$' -count=1` | 按预期失败：三项许可未知，保存被拒绝。 |
| MySQL：`go test ./controller -run 'TestAdminToken\|TestAccessToken' -count=1`，`TEST_ADMIN_TOKEN_DSN` 和 `TEST_ADMIN_TOKEN_REDIS` 指向独占容器 | 通过；含已有写操作、充值核实、签发和新读取。追加负例后再次运行 RobotReadCapabilities 通过。 |
| PostgreSQL：四项 RobotReadCapabilities、RootGrantAndIssuanceLifecycle、UserAutomationWithoutSessionOrProof、TopupReconcileRequiresProviderPayment | 通过；追加负例后再次运行 RobotReadCapabilities 通过。 |
| MySQL 主库 + PostgreSQL 日志库：RobotReadCapabilities，额外设置 `TEST_ADMIN_TOKEN_LOG_DSN` | 通过。 |
| SQLite：清空 `TEST_ADMIN_TOKEN_DSN` 后 RobotReadCapabilities | 通过；仅作附加兼容验证，不替代真实方言。 |
| `go test ./middleware ./service/authz ./service -count=1` | 三包完整测试通过。 |
| `go test ./router -run 'TestAccessToken' -count=1` | 通过，实际注册路由与 scope 规则覆盖一致。 |
| `go build ./...` | 通过。 |
| `go test ./model -run 'Test(LogFilter\|LogStat\|LogHour\|SumLog\|QueryLog\|SumUsed\|GetAllLogs)' -count=1`，MySQL 主库 + PostgreSQL 日志库 | 通过；包含全站缓存预热后的用户计数/额度隔离。 |
| MySQL 日志库：`go test ./model -run 'TestLogFilterUserIDIsolatedFromGlobalStatCache$' -count=1` | 通过。 |
| `bun run test src/features/users/components/__tests__/permissions.test.tsx` | 14 项通过，新增三项可独立勾选并经安全验证保存；关闭总开关保留分项。 |
| `web/node_modules/.bin/tsgo.exe -b` | 通过。 |
| 对修改的前端测试与 static-keys 执行 oxlint、`git diff --check` | 通过。五个受保护上游鉴权文件相对任务前未改动。 |

HTTP 专项覆盖：默认关闭、保存回读、签发、逐项撤权、总开关、两类菜单、缺 scope、Root/旧版/超 30 天/过期令牌、禁用/已删除/管理员员工排除、五字段响应、分页与状态边界、重复/未知/坏编码参数、双用户及用户名通配符隔离、30 天边界、取消请求错误脱敏、JWT 原分组/统计行为。已有普通用户全量列表、详情和原始日志仍对 PAT 拒绝。

### 查询计划

在隔离库各插入 20,000 条合成消费日志、200 个用户、100 小时时段，执行 ANALYZE 后检查实际查询形状：

```sql
EXPLAIN SELECT COALESCE(sum(quota), 0) FROM logs
WHERE user_id = 1007 AND type = 2
  AND created_at >= 1795149285 AND created_at <= 1795152885;
EXPLAIN SELECT count(*), COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)
FROM logs WHERE user_id = 1007 AND type = 2 AND created_at >= 1795152825;
```

MySQL 两者均为 `idx_log_export_user_time` 的 Index range scan，预估分别 2/1 行；PostgreSQL 分别为该索引的 Bitmap Index Scan / Index Scan，预估分别 2/1 行。无全日志表扫描。员工选择 MySQL 为 employee_profiles 主键倒序 + users 主键单行查询；PostgreSQL 在小目录 fixture 上选择扫描 employee_profiles 并按 users 角色/状态索引连接，不访问消费、业绩或提成表。合成计划不能代替生产数据的容量评估。

### 已有仓库检查问题及未执行项

- `bun run typecheck` 的原脚本为 `bun check -b`，当前 Bun 返回 `Script not found "check"`；使用已安装 tsgo 完整类型检查通过，没有改 package.json。
- 全量 `bun run lint` 在任务前已修改的 `web/src/features/usage-logs/components/dialogs/log-detail-body.tsx:1098` 报 `react/no-array-index-key`，其余为既有警告。本次涉及前端文件单独 lint 通过。
- `go test ./i18n -count=1` 的 `TestLocales_NoOrphanKeys` 和 `TestLocalesRenderEveryKey` 因既有 `audit_security.account_body` 没有对应 Msg 常量而失败。检查 HEAD 已含该孤立键，非本次新增；本次 `admin_token.usage_query_required` 有常量及三语言定义。未调整无关审计文案。
- 没有新增 schema 或迁移，故未做升级 DDL 演练；没有运行全部仓库测试、ClickHouse 接入或生产负载测试。第三方机器人联调和部署未执行。
- 验证结束已移除本任务独占的 MySQL、PostgreSQL、Redis 容器及其匿名数据卷，未操作其他容器。
