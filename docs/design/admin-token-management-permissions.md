# 管理员令牌分项权限与渠道创建者隔离

日期：2026-11-14。用户已批准开始实现；本文件记录实际实现。未部署、未修改真实账号授权。

2026-11-18 经用户确认已调整为精准查询及金额、专属倍率、员工分配三类限定修改，并为企业微信机器人新增支付核实补单，授权后支持无人值守。最终实现和本轮验证见 [精准用户管理设计](admin-token-exact-user-management.md)，调用示例见 [机器人接口](../admin-token-robot-api.md)。以下渠道部分维持原实现，尚未部署或修改真实授权。

2026-11-20 已补充三项独立读取授权：分组名称、员工选择列表、指定用户消费统计，详见 [机器人辅助只读能力](admin-token-robot-read-capabilities.md)。新增项默认关闭，已有令牌不会自动获得相应 scope。

## 1. 目标与范围

超级管理员逐个配置普通管理员通过个人访问令牌（PAT）使用精准用户查询、限定修改、支付核实补单以及本人渠道的能力。有效权限为：

**当前账号权限 ∩ root 配置的令牌许可 ∩ PAT scopes ∩ 目标限制。渠道操作另检查创建者关系和适用的高危验证；限定用户操作和核实补单不逐次验证。**

PAT 包括 nap_ 令牌及历史管理令牌，网页登录的短期 dashboard access token 沿用原会话鉴权。AI API key 的 relay 鉴权不在本次修改范围。

| 凭据 | 管理策略 |
| --- | --- |
| 普通管理员 scoped PAT | 默认拒绝，显式授权后仅开放下列精准用户操作、核实补单、本人渠道入口 |
| root PAT、历史无 scope 管理令牌 | 拒绝管理功能 |
| 普通用户、员工 PAT | 不因提交管理 scope 获得管理员能力 |
| 网页登录会话 | 保留原角色、权限、安全验证 |

管理 PAT 必须在签发时设置有限有效期，最长 30 天。长期令牌不会因为剩余时间缩短而恢复管理能力。管理员/root PAT 也不能通过个人接口修改认证因素、新建/修改/取出 AI API key 或签发另一枚 PAT。其他个人许可沿用原规则。

## 2. 权限配置与生命周期

2026-11-15 审查清理：持久化旧规则的空效果、all/own/other 范围及附加约束统一由 `legacyPolicyEffect` 规范化，供 Casbin 适配器和主库实时入口复用。无效效果仍分别导致快照加载失败/实时拒绝。两个读取入口、管理 PAT 显式授权及即时撤权规则不变，未改变 SQL 或缓存；一致性回归见 [审查问题的局部修复](review-simplification-20261115.md)。

复用 Casbin 和现有权限编辑器，增加资源 admin_access_token：

| 动作 | 含义 |
| --- | --- |
| use | 总开关，本身不授予模块 |
| users_list | 精准查询单个用户（保留原存储键） |
| users_quota | 修改金额 |
| users_group_ratios | 修改专属倍率 |
| users_employee | 分配、更换或解除员工归属 |
| topups_reconcile | 按精确订单号核实支付后补发 |
| channels_owned | 本人创建渠道的已支持功能 |

七项默认 false，不加入角色基线；仅当前普通管理员的显式用户 allow 生效。不存在、deny、异常效果、受限旧策略或冲突 deny 均拒绝，root 无自动放行。本资源不能成为 PAT 可授予 scope。新增分项不自动授予旧只读令牌。

仅 root 的真实网页登录会话可编辑，沿用管理员修改的安全验证；在其他用户字段写入前校验资格。省略整个资源或其中动作保留原值，false 显式关闭。关闭 use 保留分项；重新开启后，有效 PAT 恢复原 scopes 与当前许可的交集。疑似泄露应撤销令牌，不能用关开代替换令牌。

降级沿用清理授权、递增身份版本、撤销会话；重新升级不自动恢复。禁用/删除账号和撤销/到期令牌继续受原身份校验约束。保存权限可能要求目标管理员重新登录。

每次管理 PAT 请求从主库读取角色、状态和所需权限，不使用跨请求允许缓存。撤权提交后开始的新授权检查立即拒绝；已经通过检查的在途操作不保证取消。权限编辑页仍沿用原 Casbin 快照同步；PAT 可授予目录和执行授权读取主库，界面旧快照不能放行。

权限事务提交后若重载、会话撤销或缓存发布失败，返回 committed=true 和明确的“已保存，同步未完成”提示，记录内部原因，编辑器重新读取实际值；后续同步尽量全部执行。

## 3. 数据模型、迁移与创建者生命周期

新增管理专用 channel_creators 表：

| 字段/索引 | 用途 |
| --- | --- |
| channel_id | 不自增的主键，每渠道最多一条创建事实 |
| creator_user_id | 服务端认证操作者 ID；0 为系统来源，不授权 PAT |
| created_at | 服务端创建时间 |
| idx_channel_creator_user (creator_user_id, channel_id) | 本人渠道筛选、游标、批量归属检查 |

不向 users/channels 增加字段，不加入 relay DTO/缓存，不加级联外键。许可复用 casbin_rule 及现有索引。

网页/PAT 的单条、批量创建与复制均由服务端写入创建者；channel、abilities、creator 在同一事务提交。丢弃客户端渠道 ID，不接受自报创建者。复制先检查源渠道，副本归当前操作者。

内部无人工主体的创建保持无可授权归属；存量渠道无可靠记录默认拒绝 PAT，不从名称、标签、修改者或不完整日志推断。首版无认领/转移接口。

删除渠道在同一事务清理创建者关系。批量/标签更新和单条更新、删除的渠道 SQL 保留归属条件；批量混入越权 ID 整批拒绝。创建者不可变，标签更名、复制或普通编辑不能转移已有渠道。

## 4. 数据流与拦截边界

1. 服务端区分网页登录会话和 PAT，验证身份、账号状态/角色；不信任 UA、Origin、Referer 或自报客户端类型。
2. 执行既有管理 API 用户限流；session-only 接口仍拒绝所有 PAT。
3. 根据 method + Gin 路由模板与权限资源识别管理请求；root/历史令牌、未声明模块、过长有效期直接拒绝。
4. 主库按用户主键、有限主体/资源读取授权。各新增授权阶段最长 500ms，异常/取消/超时拒绝，不回退到旧 allow 缓存。
5. 检查菜单、动作和字段 scope；渠道 SQL 加创建者条件，批量最多 100 个目标，标签解析为冻结 ID 集合。
6. 渠道高危操作消费独立 proof 后进入 handler；限定用户修改和核实补单直接执行已授权动作。读取用安全 DTO，写入返回 ID/结果投影。
7. 复用 PAT 访问审计，记录结果、模块、受限目标和固定拒绝原因；合法写操作保留管理操作审计。

管理 scope 即使挂在 UserAuth 下也检查。TryUserAuth 不向 PAT 返回管理员可选视图。未审查的新管理路由默认拒绝，不能只按 HTTP 动词判断风险。

## 5. API 契约

不新增公开接口；使用现有 success/message/data 外壳。网页分页和权限契约不变。

### root 配置与令牌生命周期

GET /api/authz/catalog 增加资源；网页 GET /api/user/:id 返回目标权限。创建/编辑管理员仍使用原 POST/PUT /api/user/ 和安全验证。

```json
{"admin_permissions":{"admin_access_token":{"use":true,"users_list":false,"channels_owned":true}}}
```

GET /api/user/access_tokens/catalog 仅提供当前可用 scopes，增加 management_max_expiry_days=30 和 management_restricted。令牌管理仍要求网页登录；旧令牌可以改名、缩减、撤销，扩张需当前许可及有效期合规。历史 scope 字典保留说明，但排除控制资源。

### 精准用户操作与核实补单

仅 GET /api/user/search，要求 use + users_list + 用户菜单 + user:read。GET /api/user/ 分页入口为 session-only，携带精准条件也不能由 PAT 访问。

必填 `id`、`username`、`keyword` 三选一，按 ID 或完整用户名等值查询，最多一人。兼容 `after_id=0`、`page_size=1..100`；不能遍历。仅查未删除、低于操作者角色的用户。

data 为 `{items,next_cursor:null,has_more:false,quota_per_unit}`，每项包含基础信息、余额、已用额度、专属倍率；有员工菜单时包含员工档案及归属。没有邮箱、绑定、个人设置、权限矩阵和凭据。

新增独立 scope `user:quota_write`、`user:group_ratios_write`、`user:employee_assign`，分别开放限定金额、倍率和归属动作；`billing:topup_reconcile` 仅开放 `POST /api/user/topup/reconcile`。`POST /api/user/manage` 仅允许 `add_quota` 金额动作，封禁、解封、提权、降权及删除全部拒绝。正文严格白名单，其他用户操作、强制补单、列表导出等仍拒绝。字段、菜单依赖、支付平台支持和响应以 [调用文档](../admin-token-robot-api.md) 为准。

### 本人渠道

以下路径均以 /api/channel 为前缀，统一要求 use + channels_owned + 渠道菜单 + 当前账号动作权限 + PAT scope。

| 方法/路径 | 动作 scope 和验证 |
| --- | --- |
| GET /、/search、/:id | channel:read，安全投影 |
| GET /models、/models_enabled、/tag/models、/exclusion-options | channel:read，仅从本人渠道页派生 |
| POST /、/copy/:id | channel:sensitive_write + create/copy proof |
| PUT / | channel:write；name/remark 以外需 change proof，敏感字段另需 sensitive_write |
| DELETE /:id、POST /batch | channel:sensitive_write + delete proof |
| POST /:id/status、/status/batch、/tag/enabled、/tag/disabled | channel:operate + change proof |
| POST /batch/tag、PUT /tag | channel:write + change proof；参数/头/日限额另需 sensitive_write |
| POST /batch/daily_limit | channel:sensitive_write + change proof |
| POST /multi_key/manage | channel:operate；状态查询脱敏；启停需 proof，删除等另需 sensitive_write |

列表参数 after_id/page_size/keyword/tag/type/status/model/group；未知参数（包括未实现的 tag_mode）拒绝。游标分页，无全站计数/余额汇总。详情字段 id/type/name/status/models/group/tag/priority/weight/created_time。

模型辅助接口返回当前渠道页派生的去重模型字符串 items，以渠道 ID 的 next_cursor 翻页，调用者跨页合并去重；models_enabled 仅启用渠道，tag/models 可按 tag 过滤。排除选项仅 id/name/type/status。

写成功返回 channel_ids，创建返回 ids，复制返回 id。批量最多 100 个正整数 ID、先去重；他人、未知和无归属渠道均返回不可访问，不披露存在性。标签仅选择本人渠道且执行集合被冻结，不能按新标签扩大范围。

### 不开放的渠道入口

未列出的入口均拒绝，包括明文 key、全站测试/刷新/修复、全局排序、后台/流式任务、临时预览、供应商凭据刷新、原始诊断和插件绑定。首版不授予 task_plugin.bind，因此插件创建/复制/变更绑定被拒绝。

**单渠道 test、update_balance、fetch_models 也不开放 PAT。** 已审查发现共用 relay/provider 出网客户端，尚不能单独保证重定向、代理和凭据转发边界，按已批准设计的兜底拒绝，避免改动 relay。默认上游地址辅助入口也不在首版允许表。

## 6. 渠道高危验证与泄露后的保护边界

创建、复制、删除、启停、批量/标签修改、key/base URL/proxy/headers/参数、模型/分组/映射/权重/优先级/成本/日限额都需独立验证。仅 name/remark 展示字段允许普通 write；混合敏感字段仍执行完整检查。

新增验证 scopes：admin.channel.create/change/copy/delete。使用现有 /api/verify、Passkey 流程和 X-Security-Proof。仅 TOTP/备用码或 Passkey，不退回密码、OAuth 或 PAT 自证；独立因素应与运行 PAT 的机器分离保存。

四类 scope 共用 context：action 为精确 method + 路由模板，channel_id 为路径 ID，query 为实际单值参数，body 为实际 JSON。PUT 从 body.id 取 ID，批量从 ids 取目标；创建无已有 ID。

```json
{
  "scope":"admin.channel.change",
  "method":"2fa",
  "code":"<独立验证器的一次性验证码>",
  "context":{
    "action":"POST /api/channel/:id/status",
    "channel_id":123,
    "query":{},
    "body":{"status":2}
  }
}
```

获取 proof 后，以同一 PAT/body/query 调用 POST /api/channel/123/status，使用 X-Security-Proof 传递 proof，不放进 URL。

服务端读取目标、配置和成本，生成 HMAC 绑定；不接受客户端 hash 替代授权。绑定 PAT 身份、动作、body/query、冻结 IDs 和原配置；排除 relay 计数、探测时间和多 key 轮询游标。配置、标签成员或 key 状态变化会使旧 proof 失效。沿用原短时有效期和数据库单次消费，使用时再次检查权限/归属；消费后业务失败也需重新验证。

客户端服务器被攻破时，攻击者仍可读取令牌合法允许的数据、执行允许的普通写，但不能靠伪造客户端、扩张 scope 或换 ID 绕过网关授权。独立因素/会话同时失窃会扩大风险；网关服务端、数据库或签名密钥被攻破时，应用自身权限检查不能成为独立防线。

## 7. 错误、审计与交互

通过 common/gin.go：403 ADMIN_ACCESS_TOKEN_DISABLED；403 CHANNEL_NOT_ACCESSIBLE；证明错误保留原安全验证代码；身份无效 401；主库异常/超时 500；参数/业务错误 success=false。底层错误仅记录内部日志。

PAT 请求 body 最多 2MiB，旧 handler 响应最多缓冲 1MiB，输出白名单结果；不返回 key、key_preview、嵌套 setting/header/proxy 凭据或上游原始错误。历史名称可能含人为写入的秘密，字段投影不能识别其语义；PAT 创建禁止 key 前缀自动写入 name。

访问审计复用 token_ref、请求 ID、身份、路由、IP、成功标记，补充分项和受限 IDs；操作审计保留字段名/验证方式。不记录原 PAT、密码、验证码、proof、请求体或 HMAC 原文，不靠告警查询决定授权。

UI 复用管理员权限编辑器的三项复选框及现有保存/验证流程。前端文案覆盖七语言，后端实际 en/zh-CN/zh-TW.yaml。五个 upstream-owned 前端鉴权/HTTP 文件不修改。保存提交后同步失败明确提示并重新加载值。

## 8. 配置与索引

不新增系统配置，逐人权限复用权限 API。30 天、100 目标和 500ms 是固定校验边界。

身份按主键、权限按有限主体/资源读取所需列；用户列表按 ID 游标，关键词用 ID/username 前缀；渠道通过创建者索引和主键关联后筛选。无日志聚合、全站 total 或先读取全站再裁剪。新表随渠道数量变化，不是无限事件日志。

## 9. Main Chain Impact

新逻辑只在 dashboard/PAT 鉴权、安全验证及管理 CRUD。未改 TokenAuth、AI 请求 DTO、上游适配器或 relay 出网实现。

relay goroutine 新增 DB/Redis 次数、锁、goroutine 均为 **0**。网页不执行 PAT 授权查询；网页创建/删除在原事务维护新表。授权后的配置变更仍会影响正常路由，因此需要高危验证。

## 10. Shared Resource Audit

| 资源 | 使用和隔离 |
| --- | --- |
| channel_creators | 管理专用新表；主键/创建者索引，relay 不访问 |
| casbin_rule | 受限主库读取，不在请求中重载全局策略；relay 不依赖新许可 |
| users | 主键只读复查/最小列表投影；无新字段/relay 查询 |
| channels、abilities | 原管理 CRUD 加归属条件；事务限目标，不等待 MFA/网络；relay 缓存结构不变 |
| channel_cost_configs | proof 按目标 ID 读成本；既有 relay 成本缓存不变 |
| user_access_tokens、AuthFlow | 复用管理凭据和单次验证存储，与 AI tokens 分离 |
| 主库池 | 复用现有池，管理限流在前，各新授权阶段限时；不向 relay 加协调锁 |
| 内存缓存 | 无跨请求 allow 缓存；正常管理变更继续原刷新，不修改共享对象来脱敏 |
| audit_logs / LOG_DB | 原访问及操作审计，不新增重复拒绝记录，不查询日志做授权 |
| Redis | 无新 key namespace；沿用原限流、身份发布及管理变更失效机制 |
| worker/goroutine/file | 无新每请求后台任务，不占用 relay 工作池 |

## 11. Concurrency Analysis

30k RPM/100k+ RPM 的 AI relay 不执行新逻辑，新增热路径成本为零；这是路径分析，并非负载测试结果。

管理入口增加账号/权限两次主库查询。用户列表再做有界投影；渠道二次授权读取 PAT/账号/权限/必要归属，标签另查最多 101 个 IDs；高危 proof 生成/消费再次授权并读取配置/成本，复用原身份验证与单次消费事务。不能声称管理请求最多三次读取；归属校验不按每个 ID 发起 N 次读取。

批量管理最多 100 个渠道，事务最多 5 秒。原归属和行锁内比较实际值，无变化不写渠道/能力、不重建缓存；models/group 变化才重建能力，status/priority/weight/tag 定向更新能力，其余配置不写能力表。日限额内部状态复位仍发布缓存，操作审计只计实际配置变化。真实连接池等待、大数据 EXPLAIN 和共享池负载需部署环境验证，不把锁或排队加到 relay。详见 [数据库性能修复](database-performance-review-20261115.md)。

## 12. 验证结果与限制

新增测试覆盖默认拒绝、独立分项、root/历史 PAT、scope/菜单交集、旧 Casbin 快照撤权、有效期、取消、混合归属、创建失败回滚、删除关联回滚、限额周期恢复、安全投影和 HTTP 请求证明校验。

真实矩阵：SQLite **3.50.4**、MySQL **5.7.44**、PostgreSQL **9.6.24**。新增表/旧渠道无归属升级、二次 AutoMigrate 无 DDL、归属查询、更新及事务测试三库通过。SQLite 使用创建者覆盖索引/渠道主键；MySQL 使用创建者索引/主键；PG 小样本为创建者 bitmap index + channels 小表扫描，不代替生产规模查询计划。

proof 过期、跨 PAT、单次消费、配置变化和撤权在真实 MySQL/PostgreSQL 通过。HTTP 集成在 SQLite 通过；前端覆盖独立选择、关总开关保留选择、长期管理令牌阻止，以及提交后同步失败重新读取。

执行命令（DSN 仅指向临时隔离测试实例）：

```text
go test ./model -run 'TestChannelCreator|TestCreatedChannelBatch' -count=1 -v
# 分别使用 TEST_ADMIN_TOKEN_DSN 指向 MySQL/PostgreSQL
go test ./service -run 'TestAdminChannel' -count=1
# 分别使用 SQL_DSN 指向隔离 MySQL/PostgreSQL
go test ./controller ./middleware ./router ./service/authz -count=1
bun run test <users permissions / access-token create / access-tokens-card tests>
bunx --no-install tsgo -b --pretty false
bun run lint
```

bun run typecheck 当前调用本机 Bun 不支持的 bun check -b，因此使用项目已安装的 tsgo 等价检查。最终 controller、middleware、router、service/authz 整包回归通过；后续归属/证明/权限修订的定向回归通过。前端聚焦测试、tsgo 类型检查和 lint 通过（lint 仅有工作区原有警告）。提交后重载失败用例确认返回 committed=true、持久化权限仍在、内部错误不向客户端暴露。

尚未执行生产 schema 副本演练、上一发行版完整数据库的两次应用启动、生产规模 EXPLAIN/共享池压测、真实 MFA 设备或浏览器人工验收。未改 relaykit；其他工作区未提交的 relay 改动不计作本功能结果。

### 12.1 安全审计修正与复测

后续审计按 OWASP ASVS 授权、认证和会话管理要求补齐了以下边界：

- `usage:read` PAT 不再继承管理员的全任务产物视图，只能读取本人任务；网页管理员行为不变。
- 硬删除用户在同一事务清除其 Casbin 规则和渠道创建者关系并同步重载策略，同 ID 新账号不能继承旧授权或旧渠道；清理失败整体回滚。
- 管理员权限编辑先锁定用户行并以锁定后的当前角色决定授权，普通资料/分组编辑保留既有 compare-and-swap 行为。升降级都清理旧权限，重新升级不恢复管理 PAT 许可；策略重载、身份缓存发布和会话撤销在提交后全部尝试，失败返回 `committed=true`。用户详情中的三项令牌许可从主库读取，不使用旧快照。
- proof 配置摘要纳入 `other_info`；批量 ID 先去重再限制为 100 个；多密钥全部启停仅需 operate 和 proof，删除仍要求敏感写。渠道归属错误与 PAT 禁用分别返回 `CHANNEL_NOT_ACCESSIBLE` 和 `ADMIN_ACCESS_TOKEN_DISABLED`。
- 渠道创建和单条更新将渠道、abilities、创建者、日限额和成本配置纳入同一事务；成本写入错误不再被吞掉，缓存只在提交后刷新。统一管理审计统一记录实际二次验证方式。
- 旧 PAT 编辑可显式移除当前不可授予的 scopes；普通改名仍省略 scopes。长期管理 PAT 纯缩权可用，任何扩张仍在消费 proof 前拒绝。
- 后端实际翻译键统一登记到 `i18n/keys.go`，避免语言包与声明不一致。

复测覆盖 SQLite **3.50.4**、MySQL **5.7.44**、PostgreSQL **9.6.24** 的硬删除、同 ID 复用、降级竞争、批量重复 ID、成本/日限额/abilities 失败回滚、proof 和权限提交路径，均通过。MySQL 标准 `go test -p=1 -timeout 20m ./...` 全部通过；PostgreSQL 全模型包（75 秒）通过。前端完整 **253 个文件、2894 项测试**通过；权限相关聚焦 **38 项**通过，另有受影响 **78 项**通过。`go build ./...`、`bunx --no-install tsgo -b --pretty false`、lint 和生产构建通过。变更的 41 个前端文件格式检查通过；全仓格式检查还报告任务范围外的既有格式差异，没有为通过检查而扩大格式化范围。

## 13. 上线和回退

上线前验证 root 网页/独立验证，盘点脚本，按服务器 schema 演练迁移，核查无归属存量渠道和长期/root/历史 PAT 的兼容影响。由 root 逐人授权最小分项，创建最多 30 天的最小 scope PAT；独立因素不要放在自动化服务器。

渠道脚本使用游标/安全字段投影和 proof 契约；用户脚本使用精准查询和独立写 scope，无逐次 proof。混合版本旧节点不会执行新限制，管理入口须只路由到升级节点。回滚旧版会恢复宽权限，需先隔离管理入口或撤销相关 PAT，不能把回滚当安全开关。

本轮交付源码、测试和文档，没有部署、授权真实管理员或撤销真实令牌。
