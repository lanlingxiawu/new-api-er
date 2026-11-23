# 安全监控：按异常规则自动清理凭据

日期：2026-11-18。状态：已完成本地实现及专项验证，尚未部署。

用户已明确：自动清理；每条异常规则分别选择“封禁账户 / 清空 API Key / 清空访问令牌”，允许只清理不封禁；新增清理选项默认关闭。此前确认的布局要求继续有效：标签切换保持宽高，新增文案先确认。

## Goals and Scope

在现有 4 条自动处置规则中加入两个独立动作，不增加新的异常判定条件。每条规则有 3 个独立复选框；全部未选时该规则只告警。总开关继续控制全部自动动作，界面名称由“自动封禁”改为“自动处置”。原来启用的封禁选择保持不变，两个清理选项默认关闭。

| 动作 | 清理或处理对象 | 独立开启时的行为 |
| --- | --- | --- |
| 封禁账户 | 目标账户的状态、认证版本 | 沿用现有封禁流程及手工解封方式 |
| 清空 API Key | 目标账户清理开始时已有的模型调用 Key，含已停用或过期但未删除的记录 | 停用、清理缓存、软删除；账户仍可登录并创建新 Key |
| 清空访问令牌 | 目标账户清理事务开始时已有的个人访问令牌（PAT），以及旧版账号访问令牌 | 撤销访问令牌；不修改账户状态，不退出网页登录 |

清理不删除账号、余额、订阅、消费日志或渠道上游密钥。仅凭 IP 风险、异地、代理、普通登录失败等低置信线索仍只告警；不把清理扩大到共享出口的其他账号。人工清理沿用用户管理已有入口，本轮不增加另一套人工入口。

## Existing Logic and Related-Path Findings

以下为实施前从实际代码和相关测试核实的基线；后续章节记录本次实现。旧设计只作为补充：

| 代码 | 已核实行为与本次改动 |
| --- | --- |
| `setting/operation_setting/audit_auto_ban.go` | `AutoBanRule.Selected` 当前表示自动封禁；固定 4 个规则 ID，严格校验字段。扩展两个布尔字段，保留旧 `selected` 的封禁语义。 |
| `setting/operation_setting/audit_security_setting.go` | 总开关、角色范围、排除账户、规则 JSON 由 setting 注册和持久化；策略变化推进生效时间。继续复用，不另建设置接口。 |
| `service/auditsecurity/account_compromise.go` | 组合判定独立于 Selected，成功登录和真实取钥/渠道变更形成有界证据；可在不选封禁时继续生成同一判定。 |
| `service/auditsecurity/store.go`、`account_response.go` | 判定落库时创建动作；自动执行键按告警和目标去重，当前只排队 `disable_account`。需要为两种清理分别生成独立回执与执行键。 |
| `model/security_account_response.go` | 封禁在短事务中复核策略、身份、角色与范围，只写 status/auth_version；缓存发布在事务外。清理可复用检查原则，不能伪造人工操作者调用已有管理员接口。 |
| `model/user_api_keys.go`、`service/user_api_keys.go` | 人工清理固定最大 Key ID，每批最多 100 条；先停用，随后 Redis fence + 删除缓存，再软删除；失败保留停用记录。现有函数绑定管理员身份及全局 DB，不能直接传 operator=0 或 Root ID 作为后台授权。 |
| `model/token_cache.go`、`model/token.go` | 模型 Key 缓存使用 `token:<HMAC>` 和 `token:fence:{token:<HMAC>}`，fence 为 10 秒。已有缓存写入会检查 fence。必须继续调用同一失效协议，不能只删除数据库行。 |
| `model/user_access_tokens_clear.go`、`model/user_access_token.go` | 人工清理锁定 users 行，将 PAT 删除和旧版 access_token 置空合为同一事务；PAT 创建也先锁 users。此逻辑可抽取共享的事务内撤销操作，人工授权入口保持独立。 |
| `middleware/auth.go:lookupAccessToken` | PAT/旧版凭据每个请求查主库，只有请求内复用，没有跨请求的凭据允许缓存。撤销访问令牌不需要递增 auth_version 或扫描浏览器会话。 |
| `service/auditsecurity/worker.go` | 现有固定后台 worker，每轮最多 5 个处置、总预算 5 秒、单项 1 秒；即使关闭监控也执行已提交操作的恢复。清理需分批跨轮执行，不能把人工清理的 20 秒循环塞进一项。 |
| `model/audit_security.go:OpenAuditSecurityDB` | 监控主库池最多 2 个连接，日志读/写池各最多 1 个，日志不打印绑定凭据。新 SQL 显式传监控 DB，不回落到 model.DB。 |
| `loadAlertResponses`、`notify.go`、`customer_access.go` | 结果列表只查最近一次封禁；通知只等待封禁；客户风险投影只含一个 response。三处均须增加清理结果，否则后端完成而界面遗漏。 |
| `model/audit_cleanup.go:purgeSecurityAlertBatch` | 仅阻止删除 pending/committed/sync_pending 回执。新增清理进行中状态必须一起纳入，避免恢复信息被删。 |
| `controller/option.go`、`service/security_verification.go` | 保存监控设置要求 Root 浏览器会话及绑定完整 values 的 `audit.security.settings` proof。自动任务以已批准策略授权，不保存或重用管理员 proof。 |
| 前端 `auto-ban-fields.tsx`、`settings-drawer.tsx` | 已有规则卡、Checkbox、Field、固定抽屉和稳定滚动槽。直接组合现有组件，保留草稿和跨字段校验；不加动态宽高。 |
| `account-response-status.tsx`、`account-response-section.tsx`、客户风险详情 | 状态文字和人工封禁禁用条件目前只适用于封禁。清理必须按 action 显示状态，不让“清理成功”禁用人工封禁按钮或显示为“已封禁”。 |
| 现有回归测试 | 覆盖 API Key 批次/缓存故障/迟到回填，PAT 回滚/并发创建/真实鉴权，自动封禁过期回执/重放/手工解封。新增用例应接在这些合同上。 |

已核实两种人工清理有后端实现；本次缺口是后台策略授权、持久化恢复以及多动作结果展示。用户已确认动作相互独立，方案不再依赖未决产品选项。

## Configuration

继续使用 `audit_security_setting`，不增加全局变量或环境开关：

```json
{
  "id": "multi_source_key_access",
  "selected": false,
  "clear_api_keys": true,
  "clear_access_tokens": true,
  "window_seconds": 300,
  "login_ips": 5,
  "key_ips": 3
}
```

- `selected` 仍表示封禁；新增 `clear_api_keys`、`clear_access_tokens` 缺省为 false，显式值只能是布尔值，拒绝 null、字符串和其他未知字段。
- 保留存储字段 `auto_ban_enabled`，新界面将其显示为“自动处置”，控制 3 个动作。避免把已启用的旧总开关误迁移为关闭，或无意启用清理。
- 角色范围和排除列表统一约束三个动作；`auto_ban_include_staff` 控制普通管理员/员工；界面“包含超级管理员”默认关闭，对应兼容存储字段 `auto_ban_exclude_root=true`。`auto_ban_excluded_user_ids_json` 始终优先排除指定账号。详见下方 2026-11-20 范围调整。
- 总开关开启时至少有一个规则选择一个动作；会话重放规则只要选择任意动作，就要求安全事件采集开启。保留现有阈值校验。
- 清理选项或其他策略变化推进 `auto_ban_effective_at`。启用后只处理新事件，不补跑历史告警。仍要求可信身份、完整证据和 120 秒首次执行窗口。
- 老配置不含清理字段时正常读取，行为仍是原封禁选择。旧前端完整覆盖规则 JSON 时若省略新增字段，按关闭清理处理，不暗中保留旧清理权限；新的界面保存会明确携带两个值。

## Data Flow and Lifecycle

### 2026-11-20：超级管理员参与开关（方向修正）

用户明确确认“统一为包含，默认关闭”：界面使用“包含超级管理员”，关闭时只告警，开启后才执行所选自动处置。此前将“排除超级管理员”默认设为 false，错误地让默认角色范围包含 Root。本次修正开关方向和缺省行为，不增加规则、动作或执行入口。

**Existing Logic and Related-Path Findings：** 已逐项核实 `AutoBanFields` → `SettingsDrawer` → `saveMonitorSettings` → `UpdateOptionGroup` → `ParseAuditSecurityUpdate` / `SaveConfigGroup` 的保存链路。字段白名单同时生成设置 scope；保存要求 Root 浏览器会话及绑定完整 values 的 `audit.security.settings` proof。`DefaultAuditSecuritySetting` 和前端 defaults 原先都将 `auto_ban_exclude_root` 设为 false，控件直接绑定同名排除字段。`DisableUserForSecurity` 和 `authorizeSecurityCleanup` 已在该字段为 true 时跳过 Root；两处同时检查角色、排除 ID、策略时效与认证版本，因此本次复用现有执行判断。人工封禁仍在服务入口和执行事务内保护 Root。已有配置和组件测试覆盖独立开关、三种动作与值绑定验证，扩展这些测试验证缺省保护及界面反向映射。

- 设置字段：保留 `audit_security_setting.auto_ban_exclude_root` 的既有 API/存储语义，后端和前端缺省值改为 true。前端标签“包含超级管理员”，说明“关闭时，超级管理员只接收告警，不执行自动处置。”；checked 仅在存储值为 `"false"` 时开启，切换保存 `String(!checked)`。沿用现有 `Field` / `Switch`、加载和禁用状态，七种语言同步。普通管理员/员工开关维持原行为。
- 界面开关关闭时，超级管理员只告警；开启时，角色恰为 `RoleRootUser` 的目标可以执行已选自动动作，独立于 `auto_ban_include_staff`；排除 ID、可信身份、认证版本、完整证据、策略时效和人工停止仍须通过。未知角色不获得权限；人工封禁仍拒绝 Root。
- GET `/api/option/?scope=security.audit-monitor` 返回字符串 `"true"` / `"false"`；PUT `/api/option/group` 在既有 values 中接收该字段，严格拒绝其他布尔表示。缺少存储值时使用 true，对应界面默认关闭；已保存的显式 true/false 保留实际处置行为，并分别回显为关闭/开启，避免仅更名就反转已保存策略。旧客户端省略字段时保留当前值。无新配置键、表、列、迁移或索引，沿用 options 持久化。
- 开关改变时推进 `auto_ban_effective_at`，未开始动作和后续批次按当前配置复核；旧策略动作不能继续启动。已经停用的固定 Key 批次仍按原恢复协议完成缓存/删除回执收尾，不扩展范围，也不回滚已完成操作。
- 错误复用现有参数校验和处置回执；界面关闭“包含超级管理员”时，Root 自动动作返回 `protected_account`，ID 排除返回 `excluded_account`。不新增用户可见错误字符串。保存失败保留草稿。

**Main Chain Impact / Shared Resource Audit / Concurrency Analysis：** 本次仅修正界面映射及默认配置，既有角色判断仍只在后台处置事务中执行。复用监控独立 DB 池、固定 worker 与既有 users / employee_profiles / tokens / user_access_tokens / audit_security_* 表、用户和 Key 缓存失效协议；主链使用 users / tokens 与对应缓存，但本次不增加查询、写入种类、锁范围或连接，仍按主键及既有用户索引执行有界事务。Root 不进入普通管理员/员工查询分支。AI relay 在 30k–100k RPM 下每请求新增 DB/Redis 调用、锁、goroutine 均为 0；自动处置仍受原总开关、每轮最多 5 项和单项超时约束。

验收覆盖：缺省排除 Root、显式 true/false、非法布尔、策略时间变化；三种动作在默认设置及 Root/staff 开关各组合下的真实结果、ID 排除优先、角色变更、人工封禁保护；前端默认关闭、已有配置反向回显、切换、验证绑定、保存和重新打开。后端使用真实 MySQL、PostgreSQL 的隔离测试库；SQLite 补充回归。无 schema 变化，不增加迁移验收。安全控制沿用本轮重新核对的 [OWASP Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 的敏感操作重新验证和 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 的服务端失效原则，并参考 [ASVS 5.0.0](https://asvs.owasp.org/)。本次不改变登录、会话格式或人工操作权限。

方向修正前的本地验证记录（2026-11-20，未部署；不代替本次修正的验收）：

| 检查 | 命令与结果 |
| --- | --- |
| 后端包回归 | `go test ./setting/operation_setting ./service/settingsaccess ./service/auditsecurity -count=1 -timeout=240s` 全部通过；安全监控完整包使用 SQLite 隔离测试库，耗时约 189 秒。需要显式 Redis/外部数据库环境变量的已有用例不纳入该 SQLite 结果；受本次变更影响的真实数据库专项见下一行。 |
| MySQL 5.7.44 / PostgreSQL 18.7 | 两种真实数据库均执行 `go test ./service/auditsecurity -run 'Test(AutomaticResponseRootExclusion\|RootExclusion)' -count=1 -timeout=240s`，全部通过。覆盖三种动作的 13 种角色/开关/ID 组合、人工封禁保护，以及开关双向切换后旧策略动作失效。使用独立临时数据库并清理，日志位于 `_local/audit-root-exclusion-20261120/{mysql,postgres}-focused.log`。PG 首轮发现测试短凭据被 CHAR 列补空格，断言改用既有 TrimSpace 规范后复测通过，未改变业务代码。 |
| 前端组件 | `bun run test src/features/usage-logs/audit/security src/features/customer-console`，9 个文件、83 项通过。包含加载 true/false、独立切换、值绑定验证、保存与处置原因展示。API 为组件测试 mock，不代表真实浏览器端到端验收。 |
| 类型、规范、构建 | `bunx --no-install tsgo -b`、本次 7 个 TSX 文件的 scoped oxlint、`go build ./...`、`git diff --check` 均通过。`bun run typecheck` 因当前 Bun 1.3.13 不支持 `bun check` 失败，已用仓库已安装的类型编译器完成检查；全量 `bun run lint` 被本次未修改的 `log-detail-body.tsx:1098` 数组索引 key 错误阻塞，未改动该已有工作。 |
| 翻译 | 按 `add-missing-keys.mjs` 加入 3 个键并执行 `bun run i18n:sync`；七种语言共 21 个值齐全、无 BOM，与操作前工作副本比较没有其他翻译值变化。临时脚本已移除。 |

自动封禁回执及历史记录同步按 source 区分文案：自动策略排除显示“超级管理员不参与自动处置”，人工封禁仍显示原保护说明，避免继续宣称 Root 永远不可自动封禁。本次未执行生产部署、真实浏览器保存或 Redis 故障专项；未新增认证流程或迁移。

方向修正验收（2026-11-20）：

- 先修改既有回归测试：旧代码的前端 3 个场景失败；后端缺省保护用例实测得到 `succeeded` 而非预期 `skipped`，确认能捕获原问题。修正后对应测试通过。
- `bun run test src/features/usage-logs/audit/security src/features/customer-console`：9 文件、83 项通过，覆盖默认关闭、两种已存值的回显、独立切换、proof 绑定及保存后重新打开。
- `go test ./setting/operation_setting ./service/settingsaccess -count=1 -timeout=120s` 通过；SQLite 专项 `go test ./service/auditsecurity -run 'Test(AutomaticResponseRootExclusion|RootExclusion)' -count=1 -timeout=120s` 通过。
- 真实 MySQL 5.7.44、PostgreSQL 18.7 均通过同一处置专项，覆盖默认保护、14 种角色/开关/ID 组合、三种动作、人工封禁保护和策略切换失效。命令：`python -X utf8 _local/audit-root-inclusion-20261120/verify_db.py <mysql|postgres> 'Test(AutomaticResponseRootExclusion|RootExclusion)'`；独立临时数据库均已清理，日志位于同目录 `{mysql,postgres}-focused.log`。没有 schema、SQL 或迁移改动。
- 七种语言各新增 2 条文案，通过脚本和 `bun run i18n:sync` 写入；核对无其他翻译值变化、UTF-8 无 BOM，保留原有键顺序。
- 全量 `bun run lint` 通过（仅既有无关 warning）；3 个修改 TSX 的 oxfmt 检查通过；`bun run build`、使用最新前端产物的 `go build ./...` 和 `git diff --check` 通过。标准 `bun run typecheck` 被本地 Bun 1.3.13 不支持 `bun check` 阻塞，替代命令 `bunx --no-install tsgo -b` 通过。
- 本次未部署、未修改实际保存的监控配置，也未执行真实浏览器端到端保存。已有显式配置按实际行为回显；缺少该配置时默认只告警。

默认值改为 false 后的补充验证：`go test ./setting/operation_setting ./service/auditsecurity -run 'Test(AuditSecuritySettingsValidation|AutoBan|Automatic|RootExclusion|ResponseScope)' -count=1 -timeout=120s` 通过；设置抽屉 12 项测试、`tsgo -b` 和两个改动 TSX 文件的 scoped oxlint 通过。默认开关及缺省保存值均为 false，已保存 true/false 的加载与切换仍通过测试；原 Root 排除测试显式开启该策略，不再依赖默认值。

1. 复用增量扫描、4 条规则判定及每账号独立证据。在判定/告警事务中为勾选动作各写一条 `SecurityAction`，保存规则、策略生效时间、触发事件及目标认证版本快照。没有动作时只告警。
2. 封禁保留原执行键；清理键分别使用新的 HMAC 域（例如 `response-api-keys`、`response-access-tokens`）加 alert ID、target ID。重复事件和扫描不能重复创建动作。已跳过且未产生任何修改的动作，只允许既有条件下被更新的完整新事件接替；开始清理后的动作不得通过换事件扩大范围。
3. 首次执行在监控 DB 的短事务内按 alert → action → users 顺序锁定，重新核验策略/范围/Root/身份/目标认证版本/事件时效。清理允许目标已禁用。用户的认证版本或角色发生额外变化则停止尚未开始的清理。
4. 同一批触发选择多个动作时优先封禁，再清访问令牌，再分批清 API Key。仅依赖同一次触发中新建的封禁回执；允许该封禁产生的认证版本递增。封禁发生未知提交结果时先读取持久回执；不能靠猜测原版本继续清理。若封禁明确未修改账户且当前清理仍满足独立授权，可以继续所选清理。
5. API Key 首次执行固定当前未删除 Key 的 ID 上界，并保存到回执。每轮只处理至多 100 个 ID；先在短事务中停用该批并持久化“已准备的 ID 列表”，随后释放锁，在事务外清理缓存。缓存成功后，在事务中将这一批软删除并更新实际删除数量/游标/批次回执。删除与计数一并提交，防止响应丢失导致重复计数。
6. API Key 中断恢复先读回执，继续同一批 ID，重新设置 fence 并删缓存，再结束这一批；不会重新取一个更大的上界。即使进程在停用后、缓存失效后、软删除提交后退出，也不能重新清空后来新建的 Key。批次已被人工删除时按实际影响行数记账，不将其他操作的删除量冒算为本动作。
7. 访问令牌在同一短事务中锁目标 users、复核权限，删除该用户现有 PAT、清空旧版 access_token/created_at，并同时写成功回执和数量。PAT 创建受同一 users 锁串行化：事务前提交的被清理，事务后创建的保留。提交结果不明时读取回执，而不是重新执行“清空全部”。
8. 失败重试只恢复已持久化的具体操作；每批最多 3 次失败、间隔 10 秒。成功批次重置失败次数。错误返回后重新读取持久回执，按 outcome、result、attempts 比较本次尝试的快照；若其他 worker 已推进批次，迟到的错误不消耗下一批重试次数。达到上限保留 failed/partial 和已确认数量，交由用户管理的既有人工入口处理，不显示自动清理成功。整个任务最多处理到首次开始后 10 分钟，超期停止发起新批次；已提交批次的缓存收尾仍须有界恢复。
9. 暂停、关闭、改规则、排除目标、手工解封或认证版本改变时，不再开始新批次。已经停用的批次只完成其既定缓存/回执收尾，不重新启用凭据、不扩大删除范围。已经撤销的凭据不会因取消策略恢复。
10. 每次回执变更推进 alert.version/updated_at 和活动 revision。结果历史保留，通知和客户风险投影只暴露安全摘要；存在处理中批次时告警清理不能删除恢复回执。

没有凭据时完成并记录 0；不把此情况显示为失败。API Key 软删除保留计费关联；访问令牌硬删除保持现有撤销合同。调用已经通过鉴权的在途请求、流式连接和任务不主动中断，结算沿用原流程。

## API Contracts and Authorization

不新增自动执行 HTTP 接口，后台任务不绕经人工接口、不伪造管理员身份。保留现有人工清理及封禁接口合同。

| 接口 | 扩展及授权 |
| --- | --- |
| GET `/api/option/?scope=security.audit-monitor` | 返回原设置和规则 JSON 中的两个新布尔值；沿用设置查看权限 |
| PUT `/api/option/group` | 仍为 `{scope:"security.audit-monitor",module:"audit_security_setting",values:{...}}`，Root + 浏览器会话 + 单次、值绑定的 `audit.security.settings` proof；后端校验完整组合 |
| GET `/api/audit/security/alerts`、`/alerts/:id` | AdminAuth + AuditRead + AuditSecurityRead + 原 visibility；添加 `responses` 数组，每个 action 最多一个最新摘要，保留原 `response` 为封禁摘要兼容字段 |
| 员工客户风险列表/详情 | 沿用实时员工归属检查，只返回该客户自己的清理状态及数量，不返回其他客户证据或原始执行数据 |
| 既有安全告警通知/Webhook | 保留 `response`；新增 `responses` 与对应结果文本，按各动作独立报告，不把清理失败写成封禁失败 |

安全摘要示例（`affected_count` 仅在数量确认时存在）：

```json
{
  "id": 123,
  "action": "clear_api_keys",
  "source": "automatic",
  "outcome": "succeeded",
  "reason_code": "",
  "target_user_id": 456,
  "affected_count": 8,
  "created_at": 1794988800
}
```

按 `(alert_id, action)` 批量取最近结果，无列表逐行 HTTP/SQL 请求。详情动作历史同样附 action 和安全计数，分页、游标合同不变。内部执行键、Key ID 批次、目标认证版本和原始 result 不向客户端或通知投影。

## Data Model and Index Design

复用 `audit_security_actions` 的 execution_key、action、outcome、attempts、next_attempt_at 和 result；不增加表、列或索引。`SecurityResponseResult` 按动作保存有界元数据：目标认证版本、同次封禁依赖、开始时间、ID 上界、最多 100 个正在恢复的 Key ID、实际处理计数及阶段。没有 API Key、PAT、旧版凭据、密码或可用会话值；禁止 JSON 日志打印完整 result。

状态区分 pending（未开始）、clearing（还有后续批次）、sync_pending（已停用批次等待缓存收尾）、succeeded、skipped、partial、failed、unknown、superseded。封禁的 committed 状态继续保持原含义。为 API Key 恢复增加的状态同步用于 worker、通知等待和告警删除保护，不只改前端枚举。

| 查询 | 既有索引/范围 | 验收 |
| --- | --- | --- |
| 到期动作 | `(outcome,next_attempt_at,id)`，每轮最多 5 条 | MySQL/PostgreSQL EXPLAIN，不扫动作历史 |
| 各动作最新摘要 | `(alert_id,action,id)`，最多 100 个已授权告警、每告警 3 个动作 | 批量分组，仅范围内取 MAX(id) |
| API Key 上界/批次 | `(user_id,deleted_at,id)`；沿用排序后在有限 ID 批次裁掉上界外项的做法 | 避免已有 MySQL index merge 问题，分别验上界和后续批次 |
| API Key 停用/删除/恢复 | user_id + 已持久化的最多 100 个主键 ID | 显式列，只在服务器内短暂加载缓存失效必需的 Key |
| PAT 撤销 | `user_access_tokens.user_id` 索引 | 按目标用户删除，不全表扫描；同事务写回执 |
| users 权限/状态/旧凭据 | 主键 | 窄列与短锁，不重写额度及其他属性 |

全部 SQL 用 GORM，`lockForUpdate(tx)` 兼容 SQLite/MySQL/PostgreSQL。没有新的迁移，但须验证既有自动迁移不因投影字段或 result 扩展误生成 DDL；新增投影字段明确 `gorm:"-:all"`。旧回执按原封禁解析，不回填清理任务。

回退前关闭自动处置，等待进行中清理结束或人工确认其范围；旧版本无法执行新的清理阶段，不能在存在未收尾批次时直接回退并宣称恢复完成。

## Main Chain Impact

AI relay goroutine 新增 0 次检测、SQL、Redis、锁、队列投递或 goroutine。自动判断和清理只在监控 worker 执行；模型请求继续走既有 Key/用户状态鉴权，PAT 继续走既有主库验证。未清理的其他用户和正在执行的请求保持原协议及计费流程。

API Key 已停用而 Redis 暂时失败时，旧缓存可能存在短暂有效窗口；只有数据库与缓存两步完成才显示成功。现有 fence 有 10 秒保护窗口，不能承诺进程无限暂停或 Redis 回滚快照后仍自动满足即时撤销；故障验收必须包含旧快照回填和缓存恢复。

### Shared Resource Audit

| 资源 | relay 是否使用 | 隔离与限制 |
| --- | --- | --- |
| audit_security_* 表 | 否 | 继续使用监控独立主库池；有界回执查询和短事务 |
| tokens 目标行 | 是，认证及配额更新 | 授权撤销必须更新权威行；只按目标 ID 批量窄更新 status/deleted_at，不持锁调用 Redis，不改 quota，不加 relay 协调锁 |
| users 目标行 | 是，认证及结算 | 已有封禁及旧版访问令牌撤销窄更新；有截止时间的主键短锁，不刷新余额快照 |
| user_access_tokens | 面板 PAT 使用，模型 relay 不使用 | 按 user_id 定位的撤销事务，与 PAT 创建保持 users → PAT 的锁顺序 |
| token 缓存/fence | 是 | 必须使用既有鉴权键的失效协议，只触碰选定用户的具体 Key；监控执行元数据留独立表，禁止 SCAN/FLUSH/通配删除 |
| Redis 连接 | 共享 Redis 服务 | 为自动 Key 批量失效创建最多 1 个连接的后台客户端，复制现有 RDB.Options 的连接/TLS/DB 信息；独立短超时、禁止客户端隐式重试；worker 退出关闭，不修改 common.RDB 或公共池 |
| DB 连接 | 共享数据库服务 | 监控主库仍最多 2 个连接，不新开每动作池；不占用 relay SQL pool |
| 内存及后台执行 | 与进程共享 | 固定 worker；每批最多 100 个凭据短暂留内存，用后释放，不累积账户全量 Key；无 per-relay-request 工作 |

### Concurrency Analysis

在 30k 和 100k RPM 下，每次模型请求新增工作为零。撤销成本取决于真实命中动作和待清理数量。

保持每轮最多 5 个动作、总预算 5 秒、每项 1 秒；每项最多做一个 100-Key 批次或一次 PAT 事务。按正常成功路径静态计数（默认检查员工身份、启用 Redis，不含事务 BEGIN/COMMIT），首次非空 API Key 批次为 18 次 SQL，有同次封禁依赖时为 19 次；后续批次少一次上界读取。每批至多 2 个短事务、100 个 Redis Lua 命令，合并到一个 pipeline；每个 Lua 只写一个 fence 并删除一个缓存 hash。PAT 不调用 Redis，含旧版令牌更新时为 10 次 SQL，有封禁依赖时为 11 次；异常存量仍受事务超时限制。零条、无需员工检查及重试路径的次数不同，以上不是压测测量结果。

独立池限制连接争用，不能消除同机 IO、Redis CPU 或目标行短锁成本。本轮验证了真实鉴权和迟到缓存/额度写入，但未执行 mock 上游的完整流式结算对照、30k/100k RPM 压测；这些仍属于发布前的集成与负载验收，不能把上述分析写成压测通过。

## Error Handling and Recovery

- 设置 API 沿用 common 标准错误外壳、后端 i18n 和服务端权限检查；不向页面透传 Go/SQL/Redis 错误。
- worker 的内部错误用英文 SysError，保留既有 panic 恢复和超时；禁止打印明文凭据、完整 Redis 命令或带凭据的结构体。
- SQL 失败回滚本批；已提交批次不回滚。持久回执与副作用同事务，提交未知先读回，不重新“清空全部”。未知数量不能显示为 0 或成功。
- 一种动作失败不抹去另一动作的成功结果。通知等待进行中的动作，所有动作达到可报告状态后附各自结果；失败次数和任务时间限制保证不会因永久故障无限推迟通知。
- 清理不自动关闭告警。人工解封不恢复已清凭据；同一告警动作完成后，重复扫描不能继续删除新凭据。

设计参考本轮查验的 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)、[Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#require-re-authentication-for-sensitive-features) 和 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。采用服务器授权、单次值绑定的设置验证、持久撤销及无凭据审计；不新增认证旁路。

## Frontend and Copy

- 设置页将“自动封禁”页签/总开关改为“自动处置”。每张规则卡直接显示三个可独立选择的动作，阈值仍用原“规则条件”折叠区；无额外总选择复选框，避免两级开关含义重复。
- 复用 Checkbox、Field、Collapsible、固定 Sheet、现有表单验证及安全验证；规则动作由同一个 JSON 草稿推导，不用 effect 同步另一份选中状态。
- 新清理选项旁简述影响，取消后保留原服务器值；切换页签保留草稿。勾选会话重放的任意动作时提示采集依赖，修正后及时消除错误。
- 沿用抽屉固定宽高、稳定滚动槽、底部按钮区。桌面和手机、7 语言、深浅色检查，页签切换几何变化小于 1px，不引入动态尺寸动画。
- 列表、详情、处理历史、通知、员工客户风险均显示各动作及结果；清理成功采用既有数量文案，0 条使用既有“没有需要清空的…”文案。单独清理不显示为封禁。
- 复用既有文案：`Ban account`、`Clear API keys`、`Clear access tokens`、`Clearing...`、两种清理数量/零条提示、`Awaiting processing`、`Skipped`、结果未知及人工处理时限提示；这些已批准文本不重写。
- 新增/替换文案详见 [完整 7 语言确认稿](audit-automatic-credential-cleanup-copy.json)。用户确认后，已通过项目 i18n 脚本写入 23 条 × 7 语言；逐项核对与确认稿一致，并验证 UTF-8 无 BOM。后端通知已同步实际加载的 en/zh-CN/zh-TW 资源，各新增 10 条，使用相同获批文案或已有译文。

主要文案：

| 使用位置 | 简体中文 |
| --- | --- |
| 页签/总开关 | 自动处置 |
| 总开关说明 | 命中规则后执行所选操作。 |
| 规则动作区 | 命中后操作 |
| API Key 说明 | 清空开始时已有的全部 API Key，相关应用需更换密钥。 |
| 访问令牌说明 | 清空访问令牌（含旧版），不会退出网页登录。 |
| 清理提醒 | 清空后无法恢复，仅处理开启后的新事件。 |
| 排除账号说明 | 这些账号只告警，不自动处置。 |
| 自动历史动作 | 自动清空 API Key / 自动清空访问令牌 |
| 部分失败 | 清理未完成，请到用户管理检查。 |

## Verification Plan

先写有行为断言的测试，再实现。复用现有隔离 DB/Redis 夹具，不读取或清理真实用户凭据。

1. **配置组合：** 4 条规则逐条覆盖 3 个动作的全部 8 种组合、旧 JSON、缺失/非法布尔值、总开关/暂停/角色范围/排除账户、生效时间、会话重放采集依赖；保存 proof 绑定值不可被替换。
2. **触发与范围：** 新有效事件、历史/过期/未来/缺证据、共享 IP 非目标账号、Root/员工/管理员、已禁用目标；单清理不改 status/auth_version，旧会话继续有效，另一凭据类型保持可用。
3. **API Key：** 0/1/99/100/101/1001 条、上界后新建、同时人工清理/其他规则、迟到回填/额度更新；Redis 故障及恢复，停用/缓存/删除各阶段重启与回执丢响应，不重复计数、不误删新 Key。
4. **访问令牌：** PAT/过期 PAT/旧版单独及混合、0/20/超历史限额，删除和旧版清空的原子回滚、并发创建、提交未知查回执；旧凭据后续真实 HTTP 鉴权被拒绝，其他账号和浏览器会话正常。
5. **生命周期：** 独立/组合动作，先封禁的预期版本变化、人工解封/改密码/角色变更、暂停/配置替换；每批失败 3 次与任务期限边界；新事件接替只跳过未开始的旧回执，已清理回执不重做。
6. **关联路径：** 告警删除等待恢复完成；通知分别显示成功/部分失败且不重复发送；客户详情及历史过滤目标，分页结果不丢失；清理结果不错误禁用人工封禁按钮。
7. **真实数据库：** SQLite、真实 MySQL/PostgreSQL、Redis；依据 [生产环境](../production-environment.md) 和 [发布清单](../release-checklist.md) 验既有索引、新旧 schema 与二次启动无 DDL，记录版本和 EXPLAIN。生产 schema 演练留到发布阶段，不连接生产做设计验证。
8. **前端：** 保存/取消/重载/异常提示、加载/空态/错误态、所有 7 语言准确翻译及占位符；手机/桌面、深浅色、键盘、固定尺寸与无横向溢出。执行相关 Vitest、项目实际类型编译器、lint、构建及受影响 Go 测试。

## Verification Results

本节只记录本次自动清理实现的验证，不用前轮自动封禁结果替代。测试使用本地隔离夹具，未清理实际用户凭据。原始日志和截图位于 `_local/audit-credential-cleanup-20261118/`，前端宽高测量保存在 `ui-geometry.json`。

| 范围 | 实际结果 |
| --- | --- |
| 数据库版本 | 真实 MySQL 26.10.0、PostgreSQL 18.7、Redis 7.4.11；另执行 SQLite 专项 |
| 配置与后台完整包 | MySQL/PostgreSQL 的 `./service/auditsecurity ./setting/operation_setting -count=1` 均通过；最后的并发错误恢复修复及客户投影测试另在两库执行专项回归 |
| 自动清理专项 | 覆盖 4 规则 × 8 动作组合、旧配置、API Key 0/1/99/100/101/1001 条、PAT/旧版混合、范围和身份变化、同次封禁、事务回滚、Redis 故障恢复、重试/期限、幂等及新建凭据保留 |
| 并发与缓存 | 迟到 worker 失败不会扣减下一批重试次数；真实 Redis 验证旧启用快照回填、迟到额度/元数据更新不能恢复已撤销 Key，其他用户 Key 不受影响 |
| HTTP 鉴权 | 实际 Gin 鉴权分别拒绝已清 PAT/旧版令牌及模型 Key；仅清理时浏览器会话和另一类凭据仍有效。SQLite/MySQL/PostgreSQL 通过 |
| 主库/日志库组合 | MySQL 主库 + PostgreSQL 日志库清理合同通过。首次运行缺 `LOG_SQL_DSN`，修正隔离夹具后专项重跑通过；不把初次失败记为通过 |
| 客户权限与删除 | 客户列表/详情只投影所属客户的结果和数量，越权员工被拒绝；进行中的 clearing 回执阻止告警清理 |
| 迁移 | SQLite/MySQL/PostgreSQL 新库、旧 schema 升级、两次启动均通过；投影字段不生成列，第二次迁移无 DDL |
| 查询计划 | MySQL 到期队列命中 `idx_sec_response_due`、汇总命中 `idx_sec_response_alert`；PostgreSQL 到期队列命中 `idx_sec_response_due`，汇总按告警范围使用既有 `idx_sec_action_cursor`；两库 Key 查询使用 `idx_tokens_user_deleted_id`，PAT 使用 user_id 索引 |
| 前端单测 | 审计与客户模块 18 个文件、214 项通过；最后的设置/结果文案收尾后，再跑 3 个文件、24 项通过 |
| 浏览器 | Chrome 实际渲染 7 语言 × 2 主题 × 2 尺寸 × 3 页面，共 84 组通过；设置、管理员告警、客户风险切换标签宽高变化均小于 1px，无横向溢出或页面异常。API 使用隔离响应，不能据此宣称页面到真实后端的全链路通过 |
| 构建 | 前端 lint 通过（仍有仓库原有无关警告）、`tsgo -b` 通过、`bun run build` 通过；使用最新前端产物的根模块 `go build` 通过 |
| 认证公共文件 | 五个受保护前端认证/HTTP 文件相对本轮开始和 HEAD 未修改；本地 main 不含这些路径，不能声称已完成相对 main 的逐字节校验 |

可复核命令（凭据仅由本地夹具注入，不写入文档）：

```text
python _local/audit-auto-ban-implementation-20261118/db-fixture.py test mysql ./service/auditsecurity ./setting/operation_setting -count=1
python _local/audit-auto-ban-implementation-20261118/db-fixture.py test postgres ./service/auditsecurity ./setting/operation_setting -count=1
python _local/audit-credential-cleanup-20261118/test-db.py mysql cleanup-regression ./service/auditsecurity -run "TestAutomatic.*Cleanup|TestAccountResponse(QueryPlans|Migration)" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py postgres cleanup-regression ./service/auditsecurity -run "TestAutomatic.*Cleanup|TestAccountResponse(QueryPlans|Migration)" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py mysql final-contracts ./service/auditsecurity -run "TestAutomaticCleanupCustomerProjectionAndPurgeProtection|TestAccountResponseMigrationFreshUpgradeAndRestart" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py postgres final-contracts ./service/auditsecurity -run "TestAutomaticCleanupCustomerProjectionAndPurgeProtection|TestAccountResponseMigrationFreshUpgradeAndRestart" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py mysql controller-mixed ./controller -run "^TestUserAccessTokensClearMySQLWithPostgresAudit$" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py mysql model ./model -run "Test(UserAPIKeys|SecurityCleanupFences)" -count=1 -v
python _local/audit-credential-cleanup-20261118/test-db.py postgres model ./model -run "Test(UserAPIKeys|SecurityCleanupFences)" -count=1 -v
go test ./service/auditsecurity ./setting/operation_setting -run "TestAutomatic.*Cleanup|TestAccountResponse(Migration|QueryPlans)|TestAutomaticCredential" -count=1
```

前端命令从 `web/` 执行：`bun run test src/features/usage-logs/audit src/features/customer-console`、`bun run lint`、`node_modules/.bin/tsgo.exe -b`、`bun run build`。最终根模块构建命令为 `go build -o _local/audit-credential-cleanup-20261118/new-api.exe .`。

未执行：生产 schema 演练与部署、目标吞吐压测、页面真实保存到异常产生再到完整 AI 流式结算的端到端联调。未将这些项标记为通过。本次没有新增迁移；完整发布仍须按[发布清单](../release-checklist.md)执行。

## Implementation

- 配置：`setting/operation_setting/audit_auto_ban.go`、`audit_security_setting.go` 与对应测试。
- 后台：`service/auditsecurity/account_response.go`、`worker.go`、新增 `credential_cleanup.go`；新增 `model/security_credential_cleanup.go`，直接复用既有事务内 PAT 删除及 Key 缓存协议，保留人工授权入口行为。
- 数据投影：`model/audit_security.go`、`audit_cleanup.go`、`service/auditsecurity/customer_access.go`、`notify.go`、动作历史投影。
- 前端：`audit/security/auto-ban-fields.tsx`、`lib/auto-ban.ts`、`settings-drawer.tsx`、`api.ts`、`lib/security-display.ts`、结果/历史组件、客户风险组件及 locale；复用现有布局。五个 upstream-owned 认证/HTTP 文件不修改。
- 审计完整性：动作回执作为同库持久处置记录，设置变更沿用现有管理审计；只输出 action、目标 ID、状态、数量和安全原因。

确认记录：用户已确认“各动作独立选择、可只清理不封禁”，并于本轮回复“继续，开始实现”，批准本文件及 23 条新增译文。
