# 管理员令牌：精准查询与限定用户修改

日期：2026-11-18。状态：**已按用户确认实现，并完成本文记录的专项验证；尚未部署或修改真实账号权限。** 对接契约见 [企业微信机器人接口](../admin-token-robot-api.md)。

2026-11-20 补充：已增加独立授权的分组名称、员工选择列表、单用户消费统计三项读取能力，契约及验证见 [机器人辅助只读能力](admin-token-robot-read-capabilities.md)。充值按用户确认仅使用核实支付后补单；本文其余限定修改边界不变。

本设计调整权限编辑器「管理员令牌访问」中的用户权限，承接 [管理员令牌分项权限](admin-token-management-permissions.md)。本次用户要求：只允许精准查询用户，只允许修改金额、专属倍率、分配员工，其余用户操作不允许。

2026-11-18 用户进一步确认：**“需要无人值守调用，授权后直接操作。”** 因此三类用户修改只需有效 Bearer PAT 和相应授权，不要求每次提供验证码、Passkey、网页会话或 `X-Security-Proof`。用户随后回复“确认”，并提供企业微信机器人说明；机器人源码不可用，用户明确要求“需要对应接口，先做”。补充范围见第 14 节。

## 1. 目标与范围

管理员个人访问令牌（PAT）的用户管理能力仅包括以下四项，全部由服务端校验：

| 能力 | 允许范围 |
| --- | --- |
| 精准查询用户 | 提供一个正整数用户 ID 或一个完整用户名，最多返回一人 |
| 修改金额 | 对指定用户执行现有增加、扣减、覆盖余额操作 |
| 修改专属倍率 | 只更新指定用户的 `group_ratios` |
| 修改分配员工 | 为指定客户分配、更换或解除归属员工 |

禁止无条件列表、分页遍历、前缀/模糊搜索、批量用户操作，以及创建/删除用户、启停、升降级、改密码、改资料、改分组、改超时/重试、改渠道排除、改权限、重置安全因素、解绑、查看或清理凭据等其他用户操作。即使令牌携带历史 `user:write`，也不能调用这些操作。

本设计作用于截图中的管理员 PAT 用户权限。网页登录用户管理继续使用已有权限和交互；独立的「本人创建渠道」分项维持原有范围。root PAT、历史无 scope 管理令牌继续拒绝管理访问。

## 2. Existing Logic and Related-Path Findings

以下记录实施前逐条追踪代码得到的发现与修改依据，行号为审计时位置；最终实现见后续各节及第 13 节，不将这些旧行为描述为当前行为：

| 代码入口 | 当前行为与方案关系 |
| --- | --- |
| `service/authz/resources_admin_access_token.go`、`service/admin_access_token.go:37` | 现有控制资源只有 `use/users_list/channels_owned`；用户白名单仅 GET `/api/user/` 和 `/api/user/search`。需收紧查询并明确三种写动作。 |
| `middleware/admin_access_token.go`、`middleware/auth.go:302` | PAT 同时检查路由 scope、管理分项、主库当前角色/状态/授权。授权查询限时 500ms；默认拒绝未知管理路由。新增动作沿用此边界。 |
| `controller/admin_token_views.go:24`、`controller/user.go:396`、`:421` | 两个 GET 入口均转入 `listAdminTokenUsers`；空 keyword 可遍历，字符串使用 `username LIKE prefix%`，返回六个基础字段和游标。需替换为必填条件的单用户投影。 |
| `service/access_token_scope.go:127` | 管理员当前只能签发用户 `read`，不能签发用户 `write`；静态字典中的广义用户读写说明与实际 PAT 范围不同。新目录需展示准确的四项能力。 |
| `controller/admin_token_permissions.go:31`、`controller/user.go:504` | 保存动作校验和用户详情回读均显式枚举了三项权限；不能只新增目录标签，否则无法保存/回显。 |
| `controller/user.go:1606`、`controller/user_quota.go:13`、`model/user_quota_adjustment.go:25` | `POST /api/user/manage` 的 `add_quota` 分支支持 add/subtract/override；模型事务内锁定目标、复核角色、检查金额边界，提交后只同步余额差值。其余 manage 动作均不可向 PAT 放行。 |
| `controller/user.go:871`、`model/user.go:1216` | 通用更新要求用户名，且写入名称、分组、备注、超时、重试等多列。不能把 PAT 的倍率请求补成完整 User 后交给此通用更新。 |
| `controller/user.go:815–1059`、`model/user_group_ratio_cleanup.go:369` | 专属倍率已有主节点校验、非负有限数检查、历史 null 处理、删除分组清理协调及条件更新机制。新增入口要复用这些业务规则并只写倍率列及 `profile_version`。 |
| `model/user_auth_cache.go:281`、`model/user_cache.go:226` | 元数据发布保留缓存余额；手工余额修改使用原子差值。禁止用删除整个 `user:{id}` hash 或整行覆盖破坏 relay 未落库的扣款。 |
| `web/src/features/users/components/employee-assign-field.tsx`、`controller/employee.go:1493`、`:1581` | 前端通过员工档案 ID 调用分配/解绑接口。分配更新 `users.inviter_id` 后另写 `customer_profiles`，目前忽略后者错误；解除虽使用事务，但未调用邀请人缓存失效。不能把原 handler 原样开放给 PAT。 |
| `controller/customer.go:198`、`:528` | 客户绑定校验拒绝自己、非员工、禁用/非普通客户、员工客户和互相邀请；另有管理员创建客户关系入口也写入相同两处归属。原入口继续不向 PAT 开放。 |
| `model/customer.go:32`、`model/employee.go:25` | 客户归属以 `customer_user_id` 唯一，员工档案以 `user_id` 唯一；不需要添加新的关系表。员工在 users 中仍为普通用户角色。 |
| `model/user.go:1675`、`service/employee_commission.go:101` | 员工结算读取有 TTL 的邀请人内存缓存；归属变更必须提交后失效该用户缓存，不能在事务中提前公布。 |
| `service/admin_token_channel.go:215`、`service/access_token_scope.go:295`、`service/security_verification.go:328` | 当前渠道高危操作有独立 proof；用户通用 proof 被 PAT 拒绝。本次用户修改使用单独的限定处理入口，不走通用用户 proof，也不为三类操作新增 proof。渠道策略不因用户自动化需求被一并取消。 |
| `web/src/features/users/components/admin-permissions-editor.tsx:243` | 权限编辑器按后端 catalog 渲染已有 Checkbox/Field，并沿用完整表单保存和安全验证。直接复用此布局，不另造表单。 |

现有测试已覆盖列表投影、权限撤销、跨令牌 proof、金额原子调整、倍率与清理冲突、发布倍率保留缓存余额、员工绑定及 root 编辑权限。本次需要更新列表旧契约并增加允许动作和绕过场景。

已明确的后端缺口：精准查询、三项细分 PAT scope、只写倍率的入口、归属原子变更及安全结果投影尚未实现。本文件提出这些实现，未经确认不以界面或空实现替代。

### 可用性审计结果

以下结论来自当前代码及已有回归测试，不代表本次已修复：

| 问题及触发条件 | 证据与影响 | 处理范围 |
| --- | --- | --- |
| 用户管理能力与新要求不符 | `AdminTokenRouteModule` 只放行用户列表和搜索，`AccessTokenCatalog` 只签发 `user:read`；三个写操作当前均不可调用。空 keyword 能遍历、字符串是前缀搜索。 | 本设计新增限定写能力并收紧查询；必须有仅带 Bearer 的成功用例。 |
| 合法下划线名称被当作通配攻击拒绝 | `controller/admin_token_views.go:45`、`controller/admin_token_channel.go:326,353` 直接拒绝含 `_/%/反斜杠` 的输入。完整用户名 `staff_a` 或模型名含下划线时也无法查询。 | 用户查询改为参数化等值，字符按字面处理。渠道搜索仍是独立现有问题；后续应采用明确的 LIKE 转义并验证三种数据库，不能简单放开通配。 |
| 只改渠道名称也可能额外要求敏感权限 | `service/admin_token_channel.go:81–95` 按字段是否出现判断；提交 `id/name/type` 时，即使 type 与当前值一致，也要求 `channel:sensitive_write` 和 proof。网页的 `controller/channel_authz.go:5` 则比较实际值。 | 当前脚本可只提交 `id/name`。记录渠道修正方向：在已校验归属的记录上比较真实变化，并维持事务校验；本次用户功能不改渠道执行链。 |
| 渠道目录宣称有实际不可调用的功能 | `service/access_token_scope.go:163` 复制 `resources_channel.go:35` 的测试/刷新余额说明，但 `AdminTokenRouteModule` 明确拒绝 provider probes；HTTP 回归也断言拒绝测试接口。 | PAT 目录使用符合白名单的专用说明；保留网页原说明。探测接口目前未建立独立出站边界，不能为了文案一致而直接放行。 |
| 渠道写 scope 可签发，却没有可用验证方式 | `AccessTokenCatalog` 不看安全因素；`securityVerificationPolicy` 的渠道分支仅接受已有 TOTP/Passkey，没有因素时返回空方法列表。PAT 签发本身允许密码/OAuth，因此可以得到无法执行高危渠道动作的令牌。 | 界面说明渠道逐次验证前提及设置入口，不把它变成新用户 scope 的前提；本设计的用户写入完全不调用该策略。 |
| 有效期选项直到提交才拒绝 | `access-token-create-dialog.tsx:175` 展示所有预设，`:89` 到提交时才限制管理 scope 最长 30 天。 | 选择管理 scope 后即时提示并禁用不支持的预设，保留个人 scope 原选项；不修改既有最长有效期。 |
| 员工归属可部分成功或缓存滞后 | `controller/employee.go:1525–1532` 忽略客户关系写入错误；`model/customer.go:260` 解除关系未失效邀请人缓存。 | 允许调用前修正分配/解除的事务与提交后缓存处理，返回真实结果。 |

“没有逐次 MFA”不等于跳过授权：每次调用仍校验当前账号、分项、PAT scope、目标和字段。相反，已授予写权限且已知目标 ID 的调用不应额外要求先查询用户、提供读取 scope 或登录网页。

## 3. 权限模型与生命周期

复用 `admin_access_token` 资源，采用以下动作：

| 控制动作 | 界面含义 | PAT scope |
| --- | --- | --- |
| `use` | 允许令牌调用管理功能 | 总开关，不作为可签发 scope |
| `users_list`（保留存储键） | 允许精准查询用户 | `user:read` |
| `users_quota`（新增） | 允许修改用户金额 | `user:quota_write` |
| `users_group_ratios`（新增） | 允许修改用户专属倍率 | `user:group_ratios_write` |
| `users_employee`（新增） | 允许修改分配员工 | `user:employee_assign` |
| `topups_reconcile`（新增，机器人补充范围） | 允许支付核实补单 | `billing:topup_reconcile` |
| `channels_owned` | 现有本人渠道能力 | 沿用原 scope |

保留 `users_list` 键，只收紧其查询含义；三个用户写分项和支付核实补单均默认 false，旧只读授权不会自动升级。旧 `user:write` 不等价于任何新写 scope。root 必须显式授权分项，管理员再通过既有令牌编辑/签发流程选择对应 scope。

每次请求的有效权限是：当前启用的普通管理员身份 ∩ `use` ∩ 对应分项 ∩ 菜单权限 ∩ PAT scope ∩ 目标限制。查询、金额、倍率要求用户菜单；复用的员工归属接口保留现有员工菜单要求，并检查用户菜单。把员工菜单纳入实时授权快照及菜单专用判断，不能要求令牌额外携带菜单 scope。权限编辑器明确提示这些既有菜单依赖，避免“已勾选但不可用”。各写 scope 独立生效，不强制附带 `user:read`。

沿用最长 30 天有效期、撤销/到期校验、主库实时撤权、降级清理权限及身份版本机制。关闭总开关保留分项选择，但所有管理调用被拒绝。已经通过授权检查的在途事务不保证被追溯取消；撤权提交后的新请求必须拒绝。授权和令牌创建/扩权仍使用原有人工管理验证；授权后的业务调用无需再次交互。长期脚本需在有效期内轮换令牌，本次不新增续期或长期令牌政策。

## 4. API 契约

不新增公开接口，不扩大路由组。仍使用 `router/api-router.go` 中已有 `AdminAuth` 路由及菜单校验。为允许的用户和员工路由增加 PAT 专用处理边界；网页请求继续原 handler，PAT 成功或失败后终止执行，不能落入宽泛 handler。

### 4.1 精准查询

仅向 PAT 开放 GET `/api/user/search`，必须提供一个精准选择器。按用户 2026-11-18 的补充要求，GET `/api/user/` 整个入口改为网页登录专用，携带精准条件也不能通过令牌访问。推荐下列参数二选一：

```text
GET /api/user/search?id=123
GET /api/user/search?username=alice
```

`id` 必须为正整数；`username` 必须是非空完整用户名，遵循已有 32 字符上限。保留既有 `keyword` 参数作兼容选择器：数字仍表示 ID，其余改为完整用户名等值查询；全数字用户名可用显式 `username` 查询。三个选择器只能提供一个，拒绝空值、重复和含糊输入。

兼容现有 `page_size=1..100` 和 `after_id=0`，但始终最多返回一人，不能用它们代替选择器；非零游标、排序、分组、角色、状态及其他参数拒绝。不为保持 URL 兼容保留列表或前缀查询。字符串使用参数化 `username = ?`，`%/_/反斜杠` 均作为普通字符；沿用数据库现有用户名等值规则，不另加一套与账号查询不一致的大小写规范。响应返回实际存储的用户名与 ID，后续修改按 ID 进行。

只查询未软删除、角色低于管理员的用户；不存在和越权目标均返回空结果。保留轻量响应外壳，最多一项：

```json
{"success":true,"message":"","data":{"items":[{"id":123,"username":"alice","display_name":"Alice","role":1,"status":1,"group":"default","quota":500000,"used_quota":100000,"group_ratios":"{\"default\":0.8}","assigned_employee_user_id":45,"assigned_employee_profile_id":7,"assigned_employee_username":"staff_a","assigned_employee_name":"Staff A","employee_profile_id":0}],"next_cursor":null,"has_more":false,"quota_per_unit":500000}}
```

`quota`、`used_quota` 是现有数据库额度单位，分别除以 `quota_per_unit` 得到美元金额；可能与尚未落库的消费余额存在短时差异。`employee_profile_id` 仅用于精准查询到员工账号时识别分配接口需要的档案 ID；无员工档案为 0。当前归属通过邀请关系和员工档案受限查询解析，不回传员工收入、提成、客户列表或备注。员工相关字段仅在管理员拥有员工菜单权限时返回。

不返回邮箱、绑定、认证材料、权限矩阵、完整 setting、密码或凭据。`GET /api/user/` 及通用 `GET /api/user/:id` 均对 PAT 拒绝，避免遍历或泄露完整用户详情。页面原有列表/详情不受此 PAT 边界影响。

### 4.2 三类写操作

| 方法/路由 | 唯一允许的请求 | scope |
| --- | --- | --- |
| `POST /api/user/manage` | `{"id":123,"action":"add_quota","mode":"add","value":500000}`；mode 仅 add/subtract/override | `user:quota_write` |
| `PUT /api/user/` | `{"id":123,"group_ratios":"{\"default\":0.8}"}`；显式 `"{}"` 清空 | `user:group_ratios_write` |
| `POST /api/admin/employee/:id/assign-customer` | `{"user_id":123}`；路径 id 为员工档案 ID | `user:employee_assign` |
| `DELETE /api/admin/employee/:id/customer/:user_id` | 无正文；两个 ID 精确指定要解除的关系 | `user:employee_assign` |

`POST /api/user/manage` 对 PAT 仅保留 `add_quota` 金额动作；封禁 `disable`、解封 `enable`、提权 `promote`、降权 `demote`、删除 `delete` 全部拒绝。它们与金额共用 URL，因此不删除整条路由。补充回归测试覆盖列表入口带/不带查询条件均拒绝、上述动作无副作用，以及金额 add/subtract/override 仍可无人值守执行。

写接口不接受额外 query。所有 JSON 只接受上述字段及正确类型；未知字段、大小写别名、重复键、多段 JSON、null 必填值和批量数组均拒绝整次请求。禁止默默忽略越权字段，也不能先修改白名单字段再报告其余字段不允许。请求体沿用管理 PAT 的 2MiB 上限。

金额 add/subtract 必须为正整数；override 允许 0，沿用钱包现有有符号余额和 `MaxWalletQuota` 边界。不得把 value 缺失当作 0，也不添加浮点换算。三个写入口都只接受单一明确目标，并在实际更新时复核目标角色/删除状态。

成功仍返回 `success/message/data`：金额只返回目标 ID 及本次已提交的余额变化；倍率只返回 ID、保存后的倍率及被移除的失效分组；员工归属只返回客户 ID 及新员工 ID。不返回完整 User。失败不透传原 handler 的原始 Go 错误。

## 5. Data flow 与无人值守调用

1. 既有身份识别、管理限流、路由声明和当前授权检查先执行。对不在用户允许表中的请求直接拒绝。
2. PAT 专用入口读取有界正文、检查字段、解析目标。精准查询走投影读取，不进入网页列表查询。
3. 写操作使用已完成的当前授权检查及独立 scope，不调用 `/api/verify`，不读取验证码、Passkey、refresh cookie 或 `X-Security-Proof`。具有合法令牌但未绑定 MFA 的管理员也能执行已获授权的三类操作。
4. 在短事务中复核实际目标和业务条件。余额沿用原子调整；倍率只写限定列且不覆盖并发变更；员工归属同时更新两处数据。不能依赖客户端传来的角色、操作者或当前归属作授权依据。
5. 提交后按现有机制更新相关缓存，记录安全投影的审计结果并响应。锁不跨 Redis 或外网等待，不为每次请求创建新的登录/验证流程。
6. 调用流程为：root 配置分项 → 管理员签发相应 scopes 的令牌 → 脚本可选地精准查询 → 直接修改 → 查询确认。分配员工需要的档案 ID 可通过员工账号的精准查询获得，无需开放员工列表。

通用用户更新、删除和安全验证 scope 仍不向 PAT 放行。新入口不能因为省略逐次验证而落入通用 handler。

重复设置相同倍率或分配同一员工应成功且不制造重复关系。金额 add/subtract 沿用现有非幂等语义：网络中断后不确定是否提交时，脚本不能盲目重试；响应提供已提交结果和 request ID 便于核对。当前没有金额幂等键，本次不引入额外幂等存储，也不把移除 proof 后的调用描述成具备重放去重。

## 6. 业务实现与相关路径

### 金额

复用 `model.AdjustUserQuota` 的十进制计算、事务内目标校验、钱包边界、相同 override 成功及缓存增量逻辑。PAT 的授权和参数白名单在调用前完成。复用额度操作审计和充值日志；PAT 错误分支记录内部错误并返回本地化提示。

### 专属倍率

从 `UpdateUser` 中提取可复用的专属倍率校验业务，保留现有页面规则及已有回归用例。PAT 不构造完整用户更新请求；新增模型内单用户倍率更新，写入范围只有 `group_ratios/profile_version`，条件同时限制目标 ID、角色、软删除状态和读取时倍率。并发变更冲突不覆盖新值。

保留 0 倍率，拒绝负数、非有限值、新增 null；复用主节点/分组清理校验，删除不存在的分组并在响应返回名称。提交后调用现有元数据发布，不覆盖 `Quota`。普通请求不触发分组全表清理，不更改计费表达式、分组本身或 relay 倍率解析。

### 分配员工

在 `model/customer.go` 增加共享的客户员工归属事务，将 `users.inviter_id` 与 `customer_profiles` 的创建/更新/删除放在同一事务，更新失败整体回滚。按明确顺序使用 `lockForUpdate(tx)` 锁定有限目标，复核员工档案、员工/客户状态、普通用户角色、员工客户、自身和互相邀请限制。禁止使用缓存代替事务内归属检查；相同归属是成功的无变化操作。

本次分配/解除接口的网页和 PAT 分支共用该事务，以消除两张表部分成功和解除后缓存未失效的问题；界面语义维持分配、更换、解除。解除不能因为员工已停用而失去清理原关系的能力；它检查具体客户和原员工关系，不套用新增分配时的启用条件。`AdminCreateCustomer` 仍拒绝 PAT；它另有创建关系和备注语义，本次不扩大为员工模块重构，其两步写入风险单独记录。

提交后失效指定客户的 `inviterIdCache`，不清全表/全缓存，不回算历史提成。其他节点仍遵循原有 TTL 收敛，不宣称新增了跨节点实时广播。审计使用事务确认的原/新员工 ID，不写其他员工财务或客户资料。

## 7. 数据模型、配置与索引

无新增表、列、DDL 或系统配置；逐人授权继续由既有权限 API 和 `casbin_rule` 持久化，PAT scopes 继续使用已有存储。三个新增写分项默认关闭，即可随时禁用。

| 访问 | 复用索引及限制 |
| --- | --- |
| 用户 ID 查询、余额/倍率写入 | `users` 主键；只读/写指定用户 |
| 完整用户名查询 | 现有 username 唯一索引；不增加 LIKE、排序、COUNT 或 OFFSET |
| 员工映射/资格 | `employee_profiles` 主键及 `user_id` 唯一索引 |
| 客户归属 | `customer_profiles.customer_user_id` 唯一索引 |
| 当前许可 | 现有 Casbin 主体/资源查询及索引，沿用 500ms 授权时限 |

只读投影显式选择所需列。用户名和主键最多命中一行，无必要为角色/软删除过滤再加覆盖索引，更不能给高写入 users 表增加包含 quota/倍率的宽索引。事务中读取必要校验字段，不做客户总数、日志或历史账目扫描。

本轮在本地隔离测试容器做了只读查询计划检查：PostgreSQL **18.7**（`audit_test_ipupgrade_model_1794811156`）的用户 ID、username、员工 user_id、客户 customer_user_id 查询均为对应 Index Scan；MySQL **5.7.44**（`stream_policy`）确认 users 主键/username 唯一索引，空测试数据使 EXPLAIN 返回 `no matching row in const table`，不能当作有数据时的执行计划验证。未读写生产；这些结果不替代实现后的新版本数据库矩阵或代表性数据测试。

## 8. Main Chain Impact

新授权、校验、SQL 和缓存发布只在管理请求中执行。AI 请求的鉴权、预扣、结算、转发和流式处理不增加同步代码；每个 relay 请求新增 DB/Redis 调用、锁和 goroutine 均为 **0**。这属于代码路径分析，尚不是压测结果。

授权后的金额和倍率变更会按现有业务机制影响用户后续可用余额及价格；员工变更影响后续归属计算。这是本次允许的管理行为，不能描述成业务值完全不影响 relay。保留已有异步账本/提成处理，不往 relay 注入新 hook。功能开关由默认关闭的 PAT 分项和 `use` 承担。

## 9. Shared Resource Audit

| 共享资源 | relay 关系与控制 |
| --- | --- |
| `users` / 主库连接池 | relay 身份缓存回源、余额落库也访问；只按主键/唯一用户名查询、单用户短事务、按用户级既有限流，管理事务不跨网络等待。复用原钱包事务，拒绝批量操作。 |
| `user:{id}`、身份/资料版本 Redis keys | relay 也读写；余额只能应用已提交差值，倍率只能按版本发布元数据，禁止 DEL hash 或全余额回填。没有新的 Redis namespace/连接池。 |
| `employee_profiles`、`customer_profiles` | 管理和异步业务统计共享；按唯一键读取/更新，有限行锁，不查询/重算提成历史。 |
| `inviterIdCache` | 既有后台员工结算使用；提交后仅失效一个用户，沿用现有短临界区，不新增 relay 锁。 |
| 分组快照、清理状态、倍率解析缓存 | 复用管理校验；不修改 relay 解析缓存结构，不派发全表扫描，不让管理输入占用 relay 解析缓存。 |
| `casbin_rule`、`user_access_tokens` | 管理授权使用；不进入 AI API token 热路径。业务调用不新增 AuthFlow 或 proof 记录。 |
| `audit_logs`、原额度日志 / 日志库池 | 复用现有访问和业务审计，记录 PAT 引用、操作名、目标、前后白名单字段和结果；不读取日志做授权。 |
| worker / 文件 | 无新增 worker、周期任务、每请求 goroutine 或文件句柄。 |

共享 users 行锁和连接池仍存在原管理写入与后台扣款的短暂竞争，不能声称物理资源完全隔离。采用现有管理限流、有界目标和短事务；新管理 SQL 使用有界 context，验证时测锁等待和取消，绝不在 relay 增加协调锁。

## 10. Concurrency Analysis

30k RPM 到 100k+ RPM 的 AI relay 都不执行新逻辑，热路径增量成本为零。管理调用成本按单次操作估计：授权阶段沿用账号/规则两次查询；精准查询一次用户索引查找，加最多两次有界员工/归属映射读取；写入在授权后执行一次短事务。金额事务沿用原读/写及一次缓存差值更新，倍率事务只更新指定用户并执行既有元数据发布，分配事务仅涉及指定客户和员工。

不增加逐次验证请求或验证存储读写。既有 PAT 身份检查、审计以及元数据发布仍有额外成本，元数据发布可能重读最多三轮；不能宣称整个修改过程总共仅三次 DB 调用。此设计不把这部分移到 relay，也不扩大连接池。实际调用次数和耗时在实现后的集成测试中记录。

## 11. 错误、前端与审计

参数/业务失败使用现有 `common` 错误封装返回 HTTP 200 + `success:false`；权限拒绝 403，未认证交由鉴权层返回 401，数据库不可用/授权超时返回脱敏 500。底层错误先记录 request context 日志。倍率提交后缓存同步失败明确返回 `committed:true`，避免用户误以为未保存而重复修改；金额维持现有增量同步语义及错误日志，不把同步失败伪装成事务回滚。

前端继续复用 catalog 驱动的 Checkbox/Field、root 保存及安全验证流程：将「允许查看用户列表」改为「允许精准查询用户」，增加金额、专属倍率、分配员工三个独立选项，说明其他用户操作不授权。PAT 创建/编辑目录同步显示新 scope 和准确文案，旧令牌详情不伪装成已具备新能力。总开关关闭时保留选择，保存中防重复提交，失败保留编辑值，已提交但同步失败重新读取实际权限。

更新 `access-tokens-card.tsx` 的统一“高危修改需独立验证”说明，准确区分用户限定修改可无人值守、现有渠道高危修改仍需独立验证。令牌目录的渠道说明与实际白名单对齐，创建对话框在选择管理 scope 时展示 30 天限制；不在新用户写 scope 上附加 MFA 开通要求。

沿用现有布局/主题；遵循 shadcn-ui、React 及 i18n 技能。所有新文案覆盖前端七语言、动态 catalog key 登记；后端以实际 `i18n/keys.go` 及 en/zh-CN/zh-TW.yaml 为准。五个 upstream-owned 前端鉴权/HTTP 文件不修改。

访问审计复用 PAT token_ref、路由和拒绝原因；业务审计记录已提交的金额/倍率/归属变化及 PAT 身份类型，不伪造 MFA 验证记录。禁止记录 PAT、密码或原始请求体。不给此权限额外开放审计查询/导出。

安全依据为 OWASP [ASVS 5.0 授权要求](https://github.com/OWASP/ASVS/blob/master/5.0/en/0x17-V8-Authorization.md)的功能、对象、字段访问控制与服务端执行，以及 [Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) 的最小权限/逐请求检查。按用户明确的无人值守要求，授权后的三类调用依赖有限 scope、有效期、撤销和业务边界，不额外引入交互式二次验证。

同时复核 OWASP [Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 的授权入口验证、凭据生命周期和撤销要求，保留原网页登录及令牌签发 proof 流程；不把无人值守业务调用伪装成经过逐笔人工验证。

## 12. 实施文件与验证计划

实现使用以下文件和已有能力，新增业务先写行为测试：

1. `service/authz/resources_admin_access_token.go`、`controller/admin_token_permissions.go`、`controller/user.go`：新增动作、保存校验和实时回读。
2. `service/admin_access_token.go`、`service/access_token_scope.go`、`middleware/admin_access_token.go`、`middleware/access_token_routes.go`：路由/字段能力交集和签发目录；只把两个员工归属路由从 session-only 改成精确新 scope，其余员工接口保持拒绝。
3. `controller/admin_token_views.go`、新增 `controller/admin_token_user.go`、`router/api-router.go`：精准投影与 PAT 专用动作边界，保证不能落入通用用户 handler。
4. 三类动作的单次请求解析放在 `controller/admin_token_user.go`，复用现有授权结果；不新增通用验证框架或用户 proof scope。共享的倍率校验保留可直接测试的业务函数。
5. `controller/user_group_ratios.go` 复用网页倍率校验，`model/user_group_ratios_update.go` 限定列写入；`controller/employee.go`、`model/customer_assignment.go` 共享分配/解除事务和缓存失效。余额复用 `model/user_quota_adjustment.go`。
6. 权限编辑器、`web/src/features/security/components/access-tokens-card.tsx`、`components/dialogs/access-token-create-dialog.tsx`、相关既有测试、i18n 文件及 static keys：正确文案与有效期提示；实现完成后更新本设计和原设计的实际契约。

验证至少覆盖：

- 查询 ID/完整用户名命中或未命中；既有 keyword 精确语义、page_size/零游标兼容；空白、重复/未知参数、数据库等值规则、数字用户名、Unicode、下划线/百分号的字面匹配；前缀/非零游标/无条件列表不得返回用户；响应无隐私/凭据字段。
- 四个分项独立开关、总开关、菜单/PAT scope 交集、旧 `user:write`、root/历史/过期/撤销令牌、主库撤权而快照未刷新、目标同级/高级/已删除。
- **允许路径必须通过真实路由、中间件和 handler**：仅 Bearer、无 cookie、无 proof、账号未绑定 MFA 时，查询及三类修改均成功；已知 ID 的写入不要求读取 scope。测试 root 授权 → 签发 scopes → 查询 → 修改 → 查询确认的完整生命周期。
- 金额零值/边界/溢出/并发扣款、倍率 0/负值/null/删除分组/清理中/并发冲突、分配更换解除/禁用员工不能新分配但可解除原关系/员工客户/互相邀请/相同归属重复提交/关系两处一致/事务回滚。
- 所有非允许动作、夹带字段、正文类型错误、重复 JSON 键、超大正文、其他员工/客户接口、通用用户 proof 均无法绕过。断言数据库未发生任何部分写入。
- 三类用户写入无 proof 时成功；撤权、失效令牌、角色或目标状态变化仍拒绝。保留现有渠道 proof 测试，不能因用户自动化入口意外绕开渠道保护。金额重复 add 是重复加款，测试与说明不得假定幂等。
- 余额缓存保留并发预扣；倍率发布不覆盖 Quota；分配和解除均在提交后失效缓存；审计只包含允许字段。
- 页面原用户列表/更新/分配行为，root 权限保存回显、默认关闭、旧令牌不升级、渠道 scope 说明与实际能力一致、管理有效期即时提示及七语言文案。

先扩展 `controller/admin_token_views_test.go`、`middleware/admin_access_token_test.go`、现有用户权限/倍率/额度/客户测试；以允许路径和越权路径成对验证，不仅断言所有请求均被拒绝。

执行相关 Go 包回归及前端 Vitest、`bun run typecheck`、`bun run lint`。数据库行为变动须在真实 MySQL/PostgreSQL 和 Redis 上验证，按 [生产环境](../production-environment.md) 与 [发布清单](../release-checklist.md) 选定版本并记录命令、版本、结果；当前只读 MySQL 5.7 检查不代替发布清单要求的新版本验收。数据库最低版本兼容约束继续保留，SQLite 仅在无真实环境时作为明确说明的后备。无 schema 修改，不触发新增迁移设计；若实现不得不改 schema，先补充设计并执行完整升级/二次启动矩阵。

## 13. 本轮已执行的验证与限制

最终实现补充：请求严格解析只供限定管理入口使用；新 `common.UnmarshalJSONObject` 拒绝重复键、多段 JSON 和非对象。倍率只写 `group_ratios/profile_version`，并比较处理前快照。分配/解除在 `ChangeCustomerEmployee` 中同时维护邀请关系和客户档案，修复原分配第二次写入失败被吞掉、解除后缓存未失效的问题；解除以当前邀请关系确认目标，并清除该客户可能残留的旧档案。原网页调用也复用这两个事务入口。查询用主键或唯一用户名等值，无 `LIKE` 和遍历。

管理令牌页面现在显示无人值守和菜单依赖说明；选中管理 scope 后禁用 90 天、180 天、1 年和永不过期预设，保留 30 天及自定义，提交仍做服务端时限校验。渠道目录和历史 scope 字典都明确不支持测试/刷新余额。新文案覆盖前端七语言和后端实际三份语言包。

验证环境为本机专用临时容器，随机测试凭据仅放在进程环境，全部测试结束删除这些容器，未连接生产。版本：**MySQL 26.10.0、PostgreSQL 18.7、Redis 7.4.11**。主库分别为 MySQL/PostgreSQL，日志库为 PostgreSQL；新 HTTP fixture 使用每例独立 SQL 数据库和 Redis 逻辑库。SQLite 还完成首次小范围回归，不能替代以下真实数据库结果。

已通过的命令：

```text
go test ./common ./middleware ./router ./service/authz -count=1
# SQL_DSN / LOG_SQL_DSN / TEST_ADMIN_TOKEN_DSN 指向上述隔离实例；两种主库均执行：
go test ./model -run 'Test(CustomerAssignment|UpdateUserGroupRatiosOnly|VerifiedTopUp|ManualCompleteTopUp|AdjustUserQuota)' -count=1 -timeout=5m
go test ./controller -run 'Test(AdminToken|AccessToken|ManageUserQuota|BranchAuditUpdateUser|AdminAssignCustomerToEmployee|AdminUnassignCustomerFromEmployee|QueryAlipayPlatformPaymentStatus|QueryInfiniPlatformPaymentStatus)' -count=1 -timeout=5m
# TEST_ADMIN_TOKEN_REDIS 指向该次专用 Redis；新增的完整签发流程和平台失败分支在两种主库再次验证：
go test ./controller -run 'TestAdminToken(RootGrantAndIssuanceLifecycle|UserAutomationWithoutSessionOrProof|TopupReconcileRequiresProviderPayment)' -count=1 -timeout=5m
# MySQL 主库、PostgreSQL 日志库：
go test ./service -run 'Test(AdminChannel|AccessToken)' -count=1 -timeout=5m
go build ./...
```

覆盖 root 网页会话授权 → 实时权限回读 → 管理员带签发 proof 创建五 scope PAT → 无 cookie/proof/MFA 的查询和三类修改；写 scope 不依赖读 scope；空查询/前缀/通配符/额外参数和字段拒绝；撤权即时拒绝；三类写入的数据边界、缓存保留、事务回滚和关系一致性；平台未支付/未知/查询失败/不支持均不补发、已支付成功、重放只入账一次、订单快照变更拒绝。支付平台使用 fixture，不调用真实收款服务。

前端权限、令牌创建、令牌管理和 locale contract 共 **4 个文件、45 项测试通过**；`bunx --no-install tsgo -b` 和 `bun run build` 通过。`bun run typecheck` 因本机 Bun 不提供 `bun check` 命令无法运行，使用项目已安装的 tsgo 完成等价类型检查。本次改动的定向 oxlint 通过；全站 `bun run lint` 未通过，错误在已有/并行工作的审计导出与安全监控页面（嵌套三元表达式、组件导出、非空断言），没有为此改动这些业务。前端语言包完整性及无 BOM 检查通过。

路由覆盖检查还发现 `/api/audit/security/alerts/:id/responses` 缺少 PAT 声明，补为与相邻安全告警操作一致的 session-only；没有向机器人增加该能力。五个 upstream-owned 前端鉴权/HTTP 文件未修改。本次无 schema 变更，不需要新增迁移；未做生产 schema 演练、整站发布验收、真实浏览器或第三方机器人联调、生产并发压测。渠道实际值敏感判断、渠道搜索转义和 `AdminCreateCustomer` 的独立问题仍留在原审计记录，不计作本次修复。源码交付不代表生产账号已授权。

2026-11-18 补充收紧：用户要求去除分页列出用户和封禁/解封/提权。`GET /api/user/` 已从管理 PAT 白名单移除，在路由 scope 表标为 session-only，handler 对 PAT 也直接拒绝。`/api/user/search` 保留单用户精准投影；`/api/user/manage` 仅保留金额动作，其余管理动作继续拒绝。先新增测试并确认旧实现未拒绝列表入口，再完成修改；新增用例覆盖分页/空查询/精准参数访问列表均为 403，五种管理动作被拒绝且角色、状态、余额未改变，以及无 proof 的加款、减款、设置余额成功。

本次收紧后的回归命令均通过：`go test ./middleware ./router ./service/authz -count=1 -timeout=3m`、`go test ./controller -run 'Test(AdminToken|AccessToken|ManageUserQuota)' -count=1 -timeout=3m`。此次没有 SQL_DSN，相关自建 fixture 使用 SQLite，依赖外部数据库的既有测试按测试入口约定跳过；未重新执行上文的 MySQL/PostgreSQL 矩阵。此次仅改变管理入口授权，不修改 SQL、schema、事务或缓存。共享资源复核确认无新增 DB/Redis 操作、内存状态、连接池或 goroutine，`AdminTokenRouteModule` 只用于管理鉴权及管理 handler，不进入 AI relay；继续沿用上文 OWASP 授权边界。未部署或修改真实账号权限。

## 14. 企业微信机器人接口补充（用户要求先实施）

机器人由第三方开发，当前无法检查它实际使用的路径和响应结构。实现并交付明确的网关调用契约，不声称已完成第三方机器人联调。

| 机器人功能 | 网关接口和权限 | 机器人自身负责 |
| --- | --- | --- |
| 查询、余额提醒 | 精准用户查询返回 `quota`、`used_quota`、`group_ratios`、员工归属及单位换算参数；`user:read` | 提醒阈值、订阅、每分钟调度、每人最多 10 个、消息通知 |
| 加款、测试加款、减款 | 限定余额操作；`user:quota_write` | 私聊/群聊限制、付款凭证、管理员审批、试用次数/单笔 10 美元、通知群；请求金额须按返回的 quota 单位换算 |
| 测试加款时绑定、分配 | 精准查询员工账号得到档案 ID，调用归属接口；`user:employee_assign` | 企业微信员工身份到本站账号的映射、只在未分配时自动绑定的业务决策 |
| 设置/移除专属倍率 | 读当前专属倍率，仅修改目标键后提交；`user:group_ratios_write` | 群内确认及对话流程；不授权修改用户实际分组 |
| 加款补单 | 新增 `POST /api/user/topup/reconcile`，正文仅 `{"trade_no":"订单号"}`；`billing:topup_reconcile` | 收集精确订单号并展示结果 |
| 授信回款、取消、管理、我的申请/回款/提醒 | 不为这些命令新增网关管理权限 | 机器人订单、审批、回款及订阅记录；回款是否另需调整余额由其业务规则决定 |

新增独立控制动作 `admin_access_token.topups_reconcile`，默认关闭，需要总开关、用户菜单及 `billing:topup_reconcile`。它只允许精准订单核实补发，不允许充值订单全表读取、修改订单金额/用户/支付状态，也不开放原管理员强制补单接口。

### Existing Logic and Related-Path Findings — 补单

- `controller/topup_platform_status.go` 已有受限并发/超时的 Alipay 和 Infini 查询，能区分已收款、未收款、未知。现有批量查询接口只更新状态快照，不入账。
- `controller/topup.go:659` → `model.ManualCompleteTopUp` 是管理员人工强制补单：对 pending 订单入账，不自行查询支付平台。不能直接向机器人放行，否则“平台未收款不补发”没有服务端保证。
- `model.ManualCompleteTopUp` 已有订单行锁、额度边界、订单和余额同事务、已完成不重复入账及提交后缓存差值同步。复用该事务，增加仅供已核实订单使用的入口：在锁内比较查单前的订单用户/金额/支付平台/平台单号，目标用户须为普通用户。后台支付回调并发完成时返回已入账，不重复充值。

处理顺序：Bearer 授权与字段校验 → 按唯一 trade_no 读取订单及受限目标 → 已完成返回成功 → 在事务外向支付平台查询 → 只有明确已收款才进入补发事务 → 事务内复核订单快照和目标 → 沿用缓存与日志 → 返回精确订单的安全结果。查单失败/未收款/未知/不支持的平台均不入账，提供可理解的本地化原因。不信任客户端“已支付”字段或数据库旧快照代替本次核实。

首版复用现有 Alipay/Infini 查询能力，其余支付平台返回明确“不支持自动核实”，不冒充已支持。查单的 HTTP 客户端不进入 AI relay；不在订单或用户行锁内等待外部平台。复用现有 top_ups.trade_no 唯一索引及主库、日志库、余额缓存，无新表/DDL/配置，无 relay 热路径新增开销。订单字段快照比对不增加永久锁或跨请求证明。

补单验证增加：已付款成功补发、未付款/未知/平台失败不入账、重复调用/并发回调仅入账一次、查单期间金额/用户变化拒绝、错误权限/额外字段拒绝。用本地支付平台 fixture 测试，不调用真实支付服务。调用文档需说明精确账号识别、USD 与 quota 换算、有限有效期、金额 add/subtract 非幂等、补单按订单幂等，以及机器人侧流程职责。
