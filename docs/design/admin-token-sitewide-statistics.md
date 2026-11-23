# 管理员 PAT 全站统计查询

日期：2026-11-21。用户已确认设计并要求“实施”。状态：本地实现及专项验证完成，实际结果见文末；尚未部署或进行第三方机器人联调。

## Goals and scope

让已获统计授权的机器人通过现有 `GET /api/log/stat` 查询全站 RPM/TPM。不指定用户时返回全站汇总，指定用户时保持现有精准查询。沿用统计接口的消费额度字段和统计口径；默认全站查询只覆盖最近 60 秒，显式时间范围最多 30 天。

复用 `admin_access_token.users_stats` 与 `log:stats_read`，不新增权限开关、scope、路由或配置。**这会扩大已持有该统计许可和 scope 的有效 PAT 的读取范围**：升级后它们可以直接查询全站汇总，无需重新签发。没有上述许可或 scope 的令牌仍不能访问。权限名称与说明同步明确包含全站统计。

本次只改本地源码和对接文档，不操作真实账号权限或机器人凭据。第三方机器人源码和实际请求日志未提供，不能据此承诺该客户端的未知参数全部兼容；新契约如下明确列出。

## Existing Logic and Related-Path Findings

以下记录设计时核对的实现、相关测试及本轮衔接；已完成的差异同步标注：

| 代码位置 | 当前行为及本次衔接 |
| --- | --- |
| `router/api-router.go:515`、`controller/log.go:GetLogsStat` | 路由已有 `AdminAuth()`；PAT 在入口进入 `adminTokenReadRequest`，网页登录继续走原统计分支。无需新接口或变更路由。 |
| `middleware/auth.go:authHelper`、`middleware/admin_access_token.go:enforceAdminAccessToken` | 先检查管理路由白名单、普通管理员身份、令牌最长 30 天及主库实时授权，再校验 scope。截图文案来自管理授权拒绝；修改统计参数不会自动修复账号未授权、Root PAT 或旧令牌。 |
| `service/admin_access_token.go:AdminTokenRouteModule`、`middleware/access_token_routes.go:416` | `/api/log/stat` 已绑定 `users_stats` 和 `log:stats_read`。本次保留这些稳定标识。 |
| `service/authz/current_permissions.go:ReadCurrentPermissionsFromDB` | 管理 PAT 权限只认普通管理员个人的显式 allow，默认拒绝；每次从主库读取。撤权仍由现有链路生效。 |
| `service/access_token_scope.go:AccessTokenCatalog`、`service/authz/resources_admin_access_token.go` | 签发目录要求总开关、用户菜单和统计分项；两处原单用户描述均已更新为全站或单用户统计。 |
| `controller/admin_token_reads.go:adminTokenReadRequest` | 原先要求恰好一个用户选择器和显式起止时间。本轮已增加无用户的全站分支；有用户时仍精准查主库确认目标角色低于管理员后，以 UserID 过滤日志。 |
| `controller/admin_token_reads.go:adminTokenReadQuery` | 解析 query，拒绝重复、未知字段和坏编码。继续使用，不能把无效用户参数误当作全站请求。 |
| `model/log.go:SumUsedQuotaWithFilter`、`LogFilter.apply` | 没有 UserID/Username 条件即支持全站汇总；额度按传入时间范围，RPM/TPM 固定统计最近 60 秒的消费日志。无需更改模型查询或计费口径。 |
| `model/log_stat_cache.go` | 全站额度可用既有 `logstat` 小时缓存；单用户明确绕过全站缓存。缓存不适用或失败时回源数据库，RPM/TPM 始终实时查询。保持这些条件，测试交替请求防止用户统计被全站数据污染。 |
| `controller/log_query_range.go:parseLogQueryTimeRange` | 网页缺省统计一天且会修正未来时间，不适合直接拿来替代 PAT 严格契约；本次在已有 PAT 分支中直接校验。 |
| `controller/admin_token_reads_test.go:TestAdminTokenRobotReadCapabilities` | 保留双用户隔离、权限撤回和 JWT 全站统计，已将“无用户必须失败”调整为全站汇总；补充缓存开关、历史额度与实时速率、参数拒绝及令牌生命周期断言。 |
| `controller/admin_token_user_test.go:TestAdminTokenRootGrantAndIssuanceLifecycle` | 原 Root 授权、签发流程已经支持统计分项及 scope；已增加签发后全站调用断言，未改签发协议。 |
| `web/src/features/users/components/admin-permissions-editor.tsx`、`web/src/components/permission-matrix.tsx` | 权限说明从后端目录动态翻译渲染；修改目录文案、static keys 和七种翻译即可，不需要变更 React 组件或鉴权客户端。 |

相关设计为 [机器人辅助只读能力](admin-token-robot-read-capabilities.md)。本文实施后替代其原单用户限制，相关接口契约已同步更新；历史验收记录保留原日期和范围。

## API contracts and authorization

### 身份及授权

保持普通管理员 PAT、管理总开关、统计分项、用户菜单、`log:stats_read` 的交集检查。Root PAT、旧版无 scope 令牌及超过 30 天期限的管理 PAT 保持原有拒绝逻辑。无需每次交互验证。原始日志、用户列表及其他管理能力仍按各自授权检查，本次不改变其契约。

### 请求

```http
GET /api/log/stat
Authorization: Bearer <PAT>
```

无用户、无起止时间时，服务端取一次当前 Unix 秒时间戳 `now`，额度查询窗口为 `[now-60, now]`。RPM/TPM 继续由现有模型函数按查询时刻的最近 60 秒计算，日志异步落库可能有延迟。

```http
GET /api/log/stat?start_timestamp=1795132800&end_timestamp=1795219200&type=2
GET /api/log/stat?user_id=123&start_timestamp=1795132800&end_timestamp=1795219200
GET /api/log/stat?username=customer_a&start_timestamp=1795132800&end_timestamp=1795219200
```

参数规则：

- 不出现 `user_id` 和 `username` 时查询全站；出现其中一项时精准查询单用户；同时出现两项拒绝。
- `user_id` 必须是正整数；完整用户名沿用 UTF-8、非空、最多 32 字符的校验及等值查询。`user_id=0`、空用户名、无效或未知用户都不能退回全站。
- 全站请求的两个时间参数可同时省略；若提供，必须成对提供。单用户仍必须提供两个时间参数。
- 时间必须为正整数 Unix 秒，结束不早于开始、不晚于当前时间，跨度最多 30 天。空值、单边、负数、溢出及非法数字拒绝，不作静默修正。
- `type` 可省略，提供时只接受 `2`。继续拒绝渠道、模型、分组等额外筛选、重复字段和坏 URL 编码。

### 响应

全站示例：

```json
{
  "success": true,
  "message": "",
  "data": {
    "quota": 500000,
    "quota_per_unit": 500000,
    "rpm": 120,
    "tpm": 60000,
    "start_timestamp": 1795219140,
    "end_timestamp": 1795219200
  }
}
```

全站不返回 `user_id`。单用户继续返回原有 `user_id` 及其余字段。`quota` 是所选窗口内的已记录消费额度，`quota_per_unit` 取实际配置；RPM/TPM 不受历史额度窗口影响，不是历史平均值，也不统计错误日志。全站集合与网页登录全站统计一致，包括各角色以及历史上已删除用户留下的消费日志，不联表筛掉这些历史记录。

## Data flow and complete lifecycle

1. Root 通过原权限编辑器授权；管理员通过原访问令牌流程签发或编辑 `log:stats_read`。已具有这些授权的有效 PAT 直接适用扩大后的统计范围。
2. 请求依次进入既有 API 限流、PAT 识别、账号/权限实时检查、scope 检查、用户菜单检查及严格参数校验。
3. 有用户时精准确认目标并构造 UserID 过滤；全站时不查询目标用户，也不枚举所有用户。
4. 调用现有 `SumUsedQuotaWithFilter`；全站额度复用小时缓存及查询降级，RPM/TPM 使用既有实时查询。查询上下文仍以 5 秒为上限，并与日志查询配置共同取更早的截止时间。
5. 返回聚合字段，沿用 PAT last-use 和访问审计。数据读取不修改用户余额、计费、渠道或日志行。
6. 账号禁用、令牌过期/撤销、scope 移除或权限收回后，新请求由现有检查拒绝；已通过授权的在途请求保持原生命周期，不承诺强制取消。
7. 回滚源码可恢复旧单用户限制；无需数据回填、迁移或撤销新增配置。部署与真实机器人联调另行执行，不能用本地 HTTP 测试代替。

## Data model, indexes and configuration

无新增表、列、索引、迁移或设置。权限继续存于 `casbin_rule`，scope 继续存于 `user_access_tokens`；不重写真实授权数据。无需新增业务开关。

复用查询访问模式：

| 查询 | 现有索引及约束 |
| --- | --- |
| 全站最近 60 秒消费计数、token 合计 | `logs.idx_created_at_type(created_at,type)`；先按时间范围缩小，再筛消费。token 列不在索引内，会访问命中日志行，不能称为覆盖索引查询。 |
| 全站时间段额度和小时缓存回源 | 同上时间索引；既有缓存聚合还会读其他日志类型的计数，不返回这些内部缓存字段。 |
| 单用户额度及 RPM/TPM | `idx_log_export_user_time(user_id,created_at,id)`；维持当前查询。 |
| 用户精准确认 | users 主键或 username 现有唯一索引；全站请求跳过这一步。 |

本次开放的是已经被网页登录使用的查询路径，不引入新 SQL、JOIN、排序、分页或额外索引写入成本。默认 60 秒窗口避免裸 `rpm` 命令读取全天额度。显式长窗口可能冷缓存回源；恰好 30 天的闭区间还可能超过默认 720 小时缓存预算，沿用既有回源行为。不能把“有时间范围”当成恒定低成本，也不能把超时当成吞吐保障。

已在隔离的真实 MySQL/PostgreSQL 上检查默认、跨整点、显式长窗口和小时缓存回源的 SQL 计划，并通过接口验证缓存启用/关闭及请求取消；实际版本、扫描方式、耗时及验证边界见下方记录。未修改持续写入 logs 的 schema，也未把历史设计的测量当成本次结果。

## Main Chain Impact

本次不进入 AI relay 中间件、转发、上游调用、流式响应或结算路径。relay goroutine 不执行新增同步或异步代码。全站统计只在管理 HTTP 请求中同步读取既有数据；复用已有 PAT 审计，不创建新后台任务、worker 或逐请求 goroutine。

## Shared Resource Audit

| 资源 | 与 relay 的关系及处理 |
| --- | --- |
| `users`、主库连接池 | relay 使用用户数据；本次沿用管理鉴权的点查，全站分支反而少一次目标用户点查。无新增锁、事务或全用户枚举。 |
| `casbin_rule`、`user_access_tokens` | 沿用管理授权、令牌有效性和 last-use；不增加授权缓存或向模型 API token 表写入。与其他主库业务共享连接池，调用量由机器人管理请求产生。 |
| `logs`、日志连接池 | 与 relay 日志写入共享资源。复用时间索引、既有小时缓存、请求取消及查询预算，默认窗口 60 秒；不加索引或锁。大范围冷查询的池竞争须在实现验证记录中明确。 |
| `logstat` Redis/内存缓存 | 已核对统计入口与消费者；属于管理统计和预热，不是 relay 的 token、quota、channel 命名空间。复用现有 all 指纹和 TTL，不把单用户值写入全站键。内存同步沿用原 HybridCache，不增加全局映射。 |
| Redis 连接池、API 限流 | 沿用已有 API 全局/用户限流和 Redis 连接。未新增前缀或热路径 Redis 操作；限流是否启用取决于已有配置，不能假设必然开启。 |
| PAT 审计表及队列 | 继续记录管理查询结果和模块，无新增审计事件类型或凭据字段；不重用消费结算任务。 |

SQL 查询接受请求上下文；共享 Redis 缓存读写沿用既有客户端超时（`HybridCache.Get/SetWithTTL` 不接收这个请求上下文），因此不将 SQL 的 5 秒预算误报为整条 HTTP 请求的硬墙钟保证。此处不重写通用缓存或连接池。

## Concurrency Analysis

在 relay 30k/100k RPM 下，每个 relay 请求新增 DB、Redis、锁和 goroutine 成本均为零。全站统计有间接共享资源成本：最近 60 秒消费记录约为 30k/100k 行时，RPM/TPM 要聚合这些行；默认额度还会读取同一时间窗口。

每次全站管理请求沿用现有鉴权读取和审计。默认额度窗口通常 1 次查询，跨小时边界最多分为 2 段，再执行 1 次 RPM/TPM 聚合；若缓存不可用，额度直接一次范围 SUM。历史全站查询可读取多个小时缓存并在缺失时回源，受现有缓存小时上限、最长 30 天参数及 SQL 截止时间约束，不保证只做两次 DB 查询。不增加 per-relay-request 工作，也不声称已有 100k RPM 混合负载实测。

## Error handling and user-visible copy

身份失效、scope 缺失、账号分项/菜单不允许分别沿用既有 401/403 响应。无效参数走 `ApiErrorI18n`；更新 `admin_token.usage_query_required` 的说明，使其准确区分全站和单用户的参数要求，同步实际三种后端语言 `en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`。

查询取消、超时或数据库错误继续由 `writeAdminTokenUserError` 记录底层原因并返回脱敏失败，不返回假的零统计或部分成功。机器人应检查 `success`，并用响应 request ID 定位原先的授权拒绝。

两处权限目录的新文案统一为 `Allow usage statistics`，说明 `View site-wide or individual user usage statistics for up to 30 days. Requires the Users menu.`。按 i18n-translate 技能通过脚本同步七种前端语言和 `static-keys.ts`，保留工作区已有改动；不编辑五个上游拥有的认证/HTTP 文件。

## Files and verification plan

实现文件：`controller/admin_token_reads.go`；权限文案 `service/access_token_scope.go`、`service/authz/resources_admin_access_token.go`；后端现有消息的三种语言；前端 `web/src/i18n/static-keys.ts` 和七种 locale。不需要修改 `model/log.go`、路由或通用鉴权执行逻辑。

先在现有 `controller/admin_token_reads_test.go` 补回归，再实施。覆盖：

- 同一有效 PAT 裸请求及 `type=2` 返回双用户汇总；历史额度和实时 RPM/TPM 各自口径正确；响应不泄露用户/渠道/原始日志字段。
- 显式全站窗口、30 天边界、默认最近 60 秒、空数据、跨整点、错误日志不计入消费。
- 单用户原结果保持精确；全站与单用户交替请求及缓存预热后仍隔离。
- 双选择器、空/零/错误选择器、未知用户、重复/未知参数、坏编码、缺一个时间、未来/倒序/超 30 天/溢出时间全部拒绝。
- 管理总开关、统计分项、用户菜单、scope 分别缺失及撤回；Root/旧版/超期 PAT 拒绝；撤销或过期后拒绝。默认授权不能因不传用户而绕过。
- 请求取消/数据库失败返回脱敏失败，网页登录已有全站行为和其他机器人能力不变。
- 在现有签发生命周期测试中，授权保存后签发的 `log:stats_read` 能调用全站统计；权限目录和七语言表述一致。

测试环境遵循已读的 [release-checklist](../release-checklist.md) 和本地生产环境记录：真实 MySQL、PostgreSQL 及 MySQL 主库 + PostgreSQL 独立日志库组合，用隔离测试库/Redis，不操作共享业务数据。本次源码不产生 DDL；记录实际版本、命令、SQL 计划及通过/未执行项。Go 运行相关 controller、service/authz、service、middleware、router 回归；前端执行相关权限测试、类型检查、lint 和翻译完整性检查。若现有仓库检查失败，记录具体失败与改动范围，不能写成全部通过。

实施后同步 [机器人接口文档](../admin-token-robot-api.md) 和已有读取能力设计，补充实际测试记录，清楚区分本地实现、部署及第三方联调状态。

## Security references

2026-11-21 查阅官方 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)、[Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)、[Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。对应控制为每请求服务端授权、默认拒绝、明确权限扩展、令牌生命周期及拒绝回归。本次不改变登录、JWT、Cookie、会话轮换或敏感操作二次验证。下述验证支持本轮接口契约，不代表完整 ASVS 认证。

## Implementation and verification — 2026-11-21

实现采用上文契约，没有新增路由、scope、配置、SQL 或模型改动。全站响应只包含聚合字段和窗口；单用户保留 `user_id`。七种前端语言均增加两条新文案，原统计权限 key 保留；后端三种语言更新原参数错误说明。五个上游认证/HTTP 文件与本轮开始时一致，`git diff HEAD` 均为空；它们与本地 `main` 的既有差异不属于本轮修改。

### 隔离环境与执行方式

本机工具为 Go 1.26.11 windows/amd64、Bun 1.3.13。真实数据库使用本任务独立容器：MySQL 26.10.0、PostgreSQL 18.7、Redis 7.4.11；测试覆盖 MySQL 主/日志库、PostgreSQL 主/日志库、MySQL 主库 + PostgreSQL 独立日志库。通过显式 `SQL_DSN`、`LOG_SQL_DSN`、`TEST_ADMIN_TOKEN_DSN`、`TEST_ADMIN_TOKEN_LOG_DSN` 和隔离 Redis 配置覆盖本地 `.env`，没有使用生产或其他开发库。

可复查的命令、JSON 测试输出及 SQL 计划保存在本地 `_local/pat-sitewide-20261121/`，执行器为 `run_checks.py`，SQL 样本执行器为 `explain.py`。验证结束后已移除本任务三个容器及其匿名卷、临时翻译脚本，保留报告。以下计数含 Go 子测试及父测试。

| 检查 | 命令 / 范围 | 结果 |
| --- | --- | --- |
| 修改前回归 | 两个新全站 controller 契约 | 按预期失败：旧逻辑仍要求指定用户。 |
| MySQL PAT 契约 | `go test -p 1 ./controller -count=1 -json -run 'TestAdminToken\|TestAccessToken'` | 61 项通过，无跳过。 |
| PostgreSQL PAT 契约 | `TestAdminToken(RobotReadCapabilities\|RootGrantAndIssuanceLifecycle\|UserAutomationWithoutSessionOrProof\|TopupReconcileRequiresProviderPayment)` | 18 项通过，无跳过。 |
| MySQL + PostgreSQL 独立日志库 | `TestAdminToken(RobotReadCapabilities\|RootGrantAndIssuanceLifecycle)` | 16 项通过，无跳过。 |
| 日志过滤及缓存回归 | 混合数据库：`go test -p 1 ./model -count=1 -json -run 'Test(LogFilter\|LogStat\|LogHour\|SumLog\|QueryLog\|SumUsed\|GetAllLogs)'` | 38 项通过；另在 MySQL 运行 `TestLogFilterUserIDIsolatedFromGlobalStatCache` 通过。 |
| 通用鉴权与路由 | `go test -p 1 ./middleware ./service/authz ./service ./router ./i18n -count=1 -json` | middleware、service/authz、router 通过；router 的两项数据库插件用例因环境条件跳过。service/i18n 的范围外失败见下。 |
| 根模块构建 | `go build ./...` | 通过。 |
| 前端权限与签发 | `bun run test` 指定 permissions、access-token-create-dialog、access-tokens-card 三个测试文件 | 39 项通过。 |
| 前端类型 | `bun run typecheck`；随后直接执行 `node_modules/.bin/tsgo.exe -b` | 现有 script 中 `bun check -b` 找不到 check；直接 TypeScript 检查通过。 |
| 本轮前端文件 | 对 `static-keys.ts`、`permissions.test.tsx` 运行 oxlint 和 oxfmt `--check` | 均通过；全量 lint 范围外失败见下。 |
| 翻译与格式 | 按技能执行添加脚本及 `bun run i18n:sync`；对照修改前快照，检查 UTF-8 BOM 和 scoped `git diff --check` | 完成本轮同步时七个 locale 各仅增加两条 key；收尾再次确认两条 key 七语言完整、无 BOM，差异检查通过。期间其他任务继续增删翻译，保留这些并行改动，未强行恢复快照。 |

MySQL 第一次扩大测试时命中了前一次专项遗留的 Redis auth-version floor，尚未进入统计 handler 即返回 500。确认所有连接均属于本任务容器后，执行器为每组测试划分 Redis DB 并清理其专属范围；上述 MySQL 61 项是隔离修正后的通过结果，未为此修改业务鉴权。

专项断言包括：裸请求/显式全站窗口、缓存开关、完成小时回源、30 天边界、空数据、历史额度与实时速率分离、管理员及已删除用户消费计入、错误日志排除、全站响应字段投影、单用户交替查询隔离、未知/空/重复/越权参数拒绝、授权撤回、scope 移除和令牌过期/撤销。取消用例在请求进入鉴权前取消上下文，验证脱敏失败；未另外注入运行中数据库断连或驱动超时。

### 本轮 SQL 计划

从真实测试迁移后的日志表复制结构与索引，在隔离库各生成 300,000 条记录，其中最近 60 秒有 100,000 条消费，其余分布于前 30 天。真实消费合计校验为 RPM 100,000、TPM 500,000、额度 700,000。使用带 5 秒数据库查询预算的实际聚合 SQL；MySQL 读取 JSON EXPLAIN，PostgreSQL 使用 EXPLAIN ANALYZE/BUFFERS。下表是单次本地 SQL 耗时，不能外推生产吞吐。

| SQL | MySQL 毫秒 | PostgreSQL 毫秒 | 计划观察 |
| --- | ---: | ---: | --- |
| 默认 60 秒额度 | 84.039 | 53.150 | 近期记录占全表约三分之一，两者选择表扫描 / 并行顺序扫描。 |
| 最近 60 秒 RPM/TPM | 61.875 | 16.248 | 同上；不能声称一定使用时间索引。 |
| 一天额度 | 40.238 | 22.040 | 表扫描 / 并行顺序扫描。 |
| 30 天额度 | 46.492 | 87.416 | 表扫描 / 并行顺序扫描。 |
| 完成小时缓存缺失回源 | 1.760 | 1.218 | 使用既有时间相关索引；命中 291 行。 |
| 跨整点头段 | 1.009 | 0.913 | 使用既有时间相关索引；命中 2 行。 |

另将最近 60 秒窗口推进 59 秒且不注入新日志，命中 1,725/300,000 行时，两种数据库均选择既有索引：MySQL 使用 `idx_cleanup_created_id`，PostgreSQL 克隆表使用 `type_created_at_id` 索引。生产数据分布、冷缓存和索引组合可能使计划不同。没有新增索引、强制索引或改变通用统计缓存；这些测量仅核对复用查询的行为，未进行 relay 30k/100k RPM 混合负载测试。

### 范围外失败与未执行项

- 扩大检查时 `service` 首次编译被工作区其他清理任务改动阻断；相关函数随后就绪，完整重跑后 2,987 项通过、6 项环境用例跳过，剩余 `TestSysTask_FailSystemTask` 失败：该测试仍断言底层 `boom`，当前清理逻辑已改为 `cleanup.failed`。涉及 `service/system_task.go`、`service/gen3_systemtask_test.go`，本轮未修改。
- `i18n` 的 `TestLocales_NoOrphanKeys`、`TestLocalesRenderEveryKey` 因工作区的 `cleanup.invalid_log_date`、`cleanup.invalid_scope` 缺少 key 常量失败；与本轮更新的 `admin_token.usage_query_required` 无关。
- `bun run lint` 在其他工作文件 `security-ip-summary.tsx:64` 报数组索引作为 key，在 `.tmp/monitor-parameter-review/parameters.test.tsx:100` 报缺少花括号；本轮修改文件的定向 lint 已通过。
- 未运行 SQLite 专项、旧版 MySQL/PostgreSQL 矩阵或完整应用升级启动；本轮无 schema、驱动、ORM 或 SQL 改动，未执行服务器迁移。当前本地数据库版本也不冒充生产精确版本。
- 未部署、未变更真实账号权限、未持有或调用真实机器人凭据，未进行第三方机器人联调；截图中的管理授权拒绝仍需该账号具备本文要求的授权。
