# 用户渠道排除

状态：用户已批准并完成实现；按用户后续要求取消总开关，排除配置保存后直接生效。已部署香港测试服务器并完成第18节验证，未部署生产。

### 2026-11-02 选择器交互修正

原来的列表仅搜索已加载的一页，且「加载更多」在弹层外，导致管理员误认为渠道缺失。改为弹层滚动接近底部自动续页，加载和失败重试均在弹层内；不再要求点击加载更多。搜索名称或 ID 使用 300ms 防抖的服务端分页查询，切换搜索取消旧请求，已选渠道独立回显，启用和停用渠道均可选择。

`GET /api/channel/exclusion-options` 增加 `keyword`（最多128个Unicode字符，去除首尾空格；与 `ids` 互斥）。名称不区分大小写子串匹配，SQL通配符作为普通字符处理，同时支持精确数字ID或 `#ID`。仍按ID游标每页50条、最多100条，仅读取四个公开管理字段。查询使用2秒context和现有管理搜索限流。子串搜索可能扫描渠道表游标后的行，不声称名称索引能覆盖；此查询仅由管理操作触发，防抖、限流和超时限制共享数据库连接占用，无新增表、字段、索引或启动DDL，不进入relay、计费或渠道选择链。

回归先覆盖滚动续页、末页停止、加载失败重试、搜索未加载渠道、切换搜索的游标复位、已选值保留，以及SQLite/MySQL/PostgreSQL的搜索转义、停用渠道、ID查询和参数边界。原有多选控件调用者的默认过滤及选择行为保持原契约。

交互修正验证结果：

- `bun run test src/features/users/components/__tests__/user-channel-exclusions-field.test.tsx src/components/multi-select/__tests__ src/features/users/components/__tests__/permissions.test.tsx`：5文件30项通过；`tsgo -b`、lint（仅既有警告）、生产构建通过。新增用例先在旧实现复现失败，随后验证滚动续页、末页停止、服务端搜索、已选项保留、续页失败后原位重试。
- `go test ./controller -run TestUserChannelExclusionOptions -count=1 -v`：在独立临时库执行MySQL8.0.46、PostgreSQL15.19及SQLite真实查询，全部通过；临时库已删除，Docker原运行状态已恢复。`go test ./router -run 'Channel|Permission' -count=1`通过。未变更schema，无需本次上线迁移。
- 香港测试机版本 `08bdb0e47-user-channel-exclusions-20261102-r4`，SHA256 `66868b9d940105a52c531b92eb382847fc86809019428b18f6bd2f2ed5433fdd`，健康检查通过。部署前实际在运行的是同HEAD另一次构建（SHA256 `95ad8bbd133e2840815c4c8fd620d9f1efb7624e9248ccf0fa94189765efe895`），其二进制和运行环境保存在 `/root/hk-exclusion-20261102/r4-deploy/rollback`，本次继承该进程环境。
- Chrome中文实际页面使用105条渠道（50条原有+55条隔离停用夹具），验证50→100→105自动续页、直接搜索未加载的第105条、`#ID`精确搜索、跨页选择后再次搜索保留选中项，无加载更多按钮，无浏览器运行错误。截图 `artifacts/hk-picker-auto-scroll-search.png`。仅操作临时账号的未保存草稿；未改既有用户、未发送AI请求、未产生扣费。临时渠道删除、账号归档并撤销会话。
日期：2026-11-02

本文件保留设计及验证结论；文中原始 JSON 测试报告、截图和部署脚本仅在本地保存，不随功能代码提交。

提交前复核：清理无关格式变化和废弃的手动加载提示；前端7文件72项测试、`tsgo -b`、lint通过（仅既有警告）。使用独立MySQL8.0.46主库、PostgreSQL15.19日志库显式提供SQL_DSN/LOG_SQL_DSN，执行 `go test -p 1 ./model ./service ./controller ./relay ./router -count=1`，五包完整测试全部通过，随后删除临时库并恢复容器运行状态。当前工作区没有`.env`，不提供数据库配置直接执行完整测试会触发既有测试环境的nil DB/缺少DSN错误；这不是可用的完整验证方式。本轮未执行压测或真实上游调用。

## 1. 目标与范围

管理员在现有用户编辑抽屉配置「排除渠道」。例如分组 G 的模型 M 可用渠道为 A/B/C，用户 U 排除 A，则 U 的请求只在 B/C 中按现有优先级、权重选择；如果全部候选均被排除，不发送上游请求，用户收到项目对应入口已有的“无可用渠道”提示，具备日志权限的管理员及有客户查看权限的员工在详情中查看具体原因。渠道删除后，自动、可恢复地清理所有用户保存的相应 ID，不能只在界面隐藏残留。

首版采用以下语义：

- 规则归属于用户，按渠道 ID 保存，对该用户全部 API Token、可用分组及模型生效。不是厂商类型排除，也不是只针对用户注册分组的排除。
- 普通请求仍只能选择当前合法分组的候选渠道；`auto` 沿现有允许分组及跨组策略运行，不借排除功能扩大分组权限。
- 空列表表示无额外限制。其他用户、渠道全局状态和全局模型列表不受影响。
- 这是新请求的路由规则，不终止已经发出的上游调用，不迁移已有上游任务，不追溯改变计费。
- 指定渠道、原任务渠道、严格会话绑定仍保留原来的固定目标语义。被排除时拒绝，不能偷偷换到另一家上游。
- 图片中的 Providers 是本次多厂商讨论环境；产品界面沿用本项目用户编辑方式，不新增厂商启停面板。

## 2. 已核对的实现与风险点

| 位置 | 当前行为及本功能影响 |
|---|---|
| `dto/channel_constraints.go`、`model/channel_constraint.go` | 已有 Pins/Filters；内存候选过滤不修改共享切片，适合增加用户排除约束 |
| `model/channel_cache.go:GetRandomSatisfiedChannel` | 先过滤候选再计算优先级；请求级排除策略记录实际拒绝检查，不修改公共错误 |
| `model/ability.go:GetChannel` | 非内存模式也先过滤再选优先级；两种模式均需覆盖 |
| `model/ability.go:filterAbilitiesByConstraints` | 查询渠道失败时部分情况返回未过滤 abilities；必须先按 ID 排除，不能沿该错误分支恢复被排除的渠道 |
| `service/channel_select.go:SelectChannelForRequest` | 指定渠道、亲和、普通选择共用过滤；严格会话绑定不可用时会直接失败 |
| `controller/relay.go:getChannel` | 普通重试直接调用 `CacheGetRandomSatisfiedChannel`，只在 Distribute 检查会漏掉重试 |
| `middleware/task_plugin.go`、`middleware/distributor.go` | 插件预选发生在最终分发之前；约束必须在预选时已经存在，复用预选结果也要验证 |
| `relay/relay_task.go:ApplyChannelPin`、MJ 原任务提交分支 | 原任务/锁定渠道存在直接读取入口，需要单独检查；`relay/mjproxy_handler.go` 的既有图片读取归入结果读取例外 |
| `relay/responses_websocket.go` | 首轮走统一选择，后续轮次复用连接渠道，需要在每轮既有重新校验处检查排除 |
| `model/user_cache.go`、`model/user_auth_cache.go` | 用户资料已有 Redis hash、profile_version 写入栅栏；缓存热命中已有 HGETALL/MGET pipeline |
| `controller/user.go:UpdateUser`、`model/user.go:EditWithTxGroupRatios` | 管理员编辑采用显式字段更新并发布用户缓存，适合扩展 |
| `model/channel.go:Delete/BatchDeleteChannels/deleteChannelsByStatus` | 三类渠道删除均须在删除事务内登记清理工作；现有 `finalizeChannelDeletionAsync` 是 best-effort，不能单独承担可靠清理 |
| `service/request_policy.go:RecordRequestPolicyTermination` | 新增用户排除的终止日志入口，使用请求标记与首次分发拒绝去重 |
| `middleware/utils.go`、`service/relay_error_display.go` | 已有错误出口与可配置替换；关闭替换或异常时回退原文，因此原始公开错误本身就必须安全 |
| `model/log.go:formatEmployeeLogs` | 员工视图保留部分 admin_info；按用户最新要求保留排除诊断，但继续隐藏渠道名称等已有受限字段，并执行客户归属权限 |

相关文档：`per-user-relay-timeout.md`、`group-retry-auto-protection.md`、`channel-probe-routing.md`。以当前代码为准，不能直接套用早期超时文档中“缺失字段按零处理”的宽松缓存兼容策略。

## 3. 存储方案与取舍

保留 `users.excluded_channel_ids` TEXT 字段作为热读取投影，通过 `UserBase` 复用现有用户缓存；同时增加规范化关系表作为排除关系的权威来源与渠道反查索引。两者由同一管理事务维护。删除清理是本轮新增的硬性需求，原稿“只有 JSON、无反向查询需求”的前提已不成立。

| 方案 | 优点 | 代价与结论 |
|---|---|---|
| users 投影 + 有索引的关系表 + 既有用户缓存 | 热路径不增加网络往返；删除按 channel_id 直接定位受影响用户 | 管理侧需事务维护投影及关系、可靠清理队列。修订后采用 |
| 只有 users JSON | 正常请求简单 | 删除渠道需要扫描用户 JSON；不能支持大用户量下有界、精确清理。不采用 |
| 放进 `UserSetting` JSON | 不加列 | 用户自行修改设置路径容易覆盖管理规则，职责与写入权限混杂。不选 |
| 独立关系表 + 本地全量快照 | 可按 channel_id 反查；热路径纯内存 | 反查能力需要，但全量本地快照引入额外跨节点同步和内存成本；仅采用关系表，不另建热缓存 |
| 每次请求查表或额外 Redis Set | 写入与查询直观 | 新增热路径 I/O，失败处理扩大主链影响。不选 |

字段细节：

- TEXT 不设数据库字面量 default，兼容 SQLite/MySQL/PostgreSQL；历史 SQL NULL/空字符串在可信数据库读取处转换为 `[]`。
- 新提交值为正整数 ID 数组；最多 256 项，原始字段编码最多 8 KiB，排序去重后存储。限制在解析/校验前后都生效，不能靠去重容忍无限输入。
- 限制是本接口的固定输入上限，不额外增加可调设置。超限返回可理解的校验错误，禁止截断保存。
- 不创建 JSON 索引，不使用 JSON 数据库函数。反查只访问关系表的 `(channel_id,user_id)` 索引，再按 users 主键更新，不扫描全体用户。
- 渠道停用/改名/移组/更换密钥均不清除排除规则；渠道真正删除后必须清理关系表、用户投影及缓存。新建渠道使用新 ID，不继承旧排除；不支持复用已删除渠道 ID 的导入。
- 保存表单时剔除已经不存在的 ID，并向有权限的管理员返回 `removed_excluded_channel_ids`；存在但无权操作的新增 ID 返回标准权限错误。非法数字/数量越界仍拒绝，不能伪装成自动清理。
- 仅对该字段显式提交时写入，遗漏不得从较早读出的用户快照覆盖该字段；加入 `model/user.go:adminOwnedUserColumns`，普通用户自助更新不能写此列。

### 3.1 Data model 与 Index design

- `user_channel_exclusions`：`user_id`、`channel_id`，联合主键或唯一索引 `(user_id,channel_id)`；反查覆盖索引 `(channel_id,user_id)`。无 JSON、无软删除列，每用户至多256行。管理员保存按 user_id 读有序集合，删除按 channel_id + user_id 游标定位。
- `user_channel_exclusion_cleanups`：`channel_id` 主键、`after_user_id`、可空 `pending_user_id`、`next_attempt_at`、有限错误分类、尝试次数和更新时间。待执行扫描索引 `(next_attempt_at,channel_id)`；成功完成删除队列行，历史状态走既有管理审计/SystemTask结果，避免积累无界完成记录。
- 使用现有 SystemTask 框架新增独立任务类型 `user_channel_exclusion_cleanup`，复用该类型的跨节点租约、取消和管理状态展示；独立类型不能占用 async_task_poll/log_cleanup 的租约。持久队列是恢复依据，不能依赖 SystemTask 一次运行“成功/失败”状态保存待办。
- users 的 JSON 投影不是第二份可独立编辑的权威数据。管理写入与清理同时修改关系集合、投影、`profile_version`；不允许旁路只修改一侧。读取缓存仍只取投影，不在 relay 中 JOIN 新表。
- 新功能尚未上线，正常迁移无需扫描历史用户建关系。若部署环境曾试写本功能字段，启用前单独按 users 主键窗口、可恢复地回填关系并校验投影，不能一边接收新配置一边遗漏旧记录。
- 三库验证全部DDL、唯一约束、游标/索引执行计划及重复启动。新表写入只由管理/后台触发，无按请求量增长的关系记录。

### 3.2 渠道删除与清理生命周期

1. 单删、批量删、按状态删共用模型层事务逻辑。使用事务内确认的实际删除 ID；同事务删除 channels/abilities/成本配置并写入每渠道一条持久清理记录。登记失败则整笔删除回滚，不能删完渠道再依赖内存调度。重复删除已有清理任务时幂等保留进度。
2. 删除提交后刷新渠道缓存、关闭既有绑定 WebSocket、保留现有非关键收尾。用户排除清理由独立任务类型在后台处理，不把大量用户 UPDATE 放入渠道删除事务。删除接口成功表示渠道删除且清理任务已持久登记，不表示清理全部完成；管理审计/任务状态须区分排队、清理、缓存同步、完成、失败待重试。
3. 单个集群 worker 每轮取最多10条到期渠道任务，每渠道最多处理100名用户；按反查索引逐用户定位，每用户一个短事务。先读取当前用户的profile_version，再移除目标渠道并读取剩余关系、重建完整投影，只写排除字段并自增profile_version；其他排除、专属倍率、超时、余额均保留。以本次新读到的版本CAS，不使用任务创建时版本；CAS影响0行时整个用户事务回滚并延期60秒，下次重新读取两侧，不能跳过该用户。读版本必须早于读关系，避免用旧关系配新版本“成功”回写。所有写关系的业务入口须在同事务更新用户版本。
4. 删除该用户关系、更新投影与把 `pending_user_id` 写入队列行在同一事务提交。随后用现有保留 Quota 的版本化机制发布最新用户缓存；发布确认成功后，才事务性推进 `after_user_id` 并清空 pending。即使关系已删除、进程在发布前崩溃，重启仍能通过 pending 找回待刷新用户。
5. pending 用户恢复时先处理 pending，再扫描下一批；重发缓存/确认游标必须幂等。Redis未启用可直接完成缓存阶段；Redis不可用时保留pending并退避重试，不DEL用户hash。用户已硬删除则确认无须回填缓存；软删除用户也清投影和关系，缓存仅推进资料版本下限且不复活其会话/用户hash。
6. 游标尾部确认没有残留关系、没有pending后删除清理队列行并记录完成。进程崩溃、租约丢失、数据库超时均保留队列；调度器下次运行继续。持有者变更后旧worker不可推进游标。反复失败告警并展示管理状态，不自动丢弃任务。
7. 持久队列不使用 `ON DELETE CASCADE` 先删关系，否则失去尚未刷新的用户索引；也不把 `finalizeChannelDeletionAsync` 或分组清理的进程内 best-effort 队列当可靠任务。

每处理一名用户让步50ms（最多20人/秒），每阶段DB/Redis操作使用1秒context。调度检查间隔15秒，失败延期60秒；取消后恢复以持久队列为准。后台单并发，不用无界重试卡住一个用户。SystemTask结果保存processed_users、deferred_channels、last_failure_phase；一次运行成功只表示本轮完成，剩余队列由后续运行继续。

已核对 `MarkSystemTaskLeaseExpired`：现有框架将过期运行标记failed并清active_key，不会自动重新执行该任务。新类型调度器必须在队列仍有到期待办且没有活跃同类型任务时创建下一次运行，沿用 `(type,id)` 索引及独立类型租约。不得只声明“框架有租约所以会自动续跑”。任务历史被管理员清除不得删除持久清理队列。

管理保存与删除竞态：在同一保存事务内按固定ID顺序使用 `lockForUpdate(tx)` 重新确认提交集合中的现存渠道，再锁用户行、重写关系及投影；用户行锁也覆盖原来没有关系或两个管理员提交不相交集合的情况。删除事务对同一渠道行互斥。保存先提交则删除任务能看到新关系，删除先提交则保存剔除失效ID。锁不进入relay；管理事务死锁/SQLite busy时整笔回滚并返回项目数据库错误，由管理员重试，未增加自动重试。清理事务使用1秒超时并持久延期。不持有DB事务等待Redis发布。

旧抽屉回传已删除ID必须重新剔除，不能恢复旧关系。仅改用户其他字段时不覆盖排除列，清理仍由持久任务保证。用户软删除保留与恢复的管理行为必须覆盖关联清理；用户硬删除时同时移除关系，pending处理不得重新创建用户。

修复范围检查：当前 `deleteChannelsByStatus` 删除渠道及成本配置，却未像单删/批量删一样删除 abilities。这是已发现的实际路径差异；实现时补齐同事务 abilities 清理，并用状态变化竞争用例确保只清理实际被删除渠道。历史报表名称快照、既有任务与历史日志保留，不进行“关联全删”。

跨节点边界：删除控制器目前仅调用本节点 `InitChannelCache`，其他节点靠 `SyncChannelCache` 周期刷新（默认SYNC_FREQUENCY为60秒）。因此“用户配置清理完成”和“所有节点瞬时撤销渠道”不是同一个保证，清除用户排除ID不能被当作删除渠道的失效通知。测试必须覆盖其他节点仍持有旧渠道快照时的行为，管理状态不宣称全节点即时生效。若部署要求严格立即撤销，需先补齐并验证渠道缓存的跨节点失效/就绪机制；不能靠固定sleep冒充确认，也不能向本功能每个请求增加DB存在性查询。正常配置清理本身保证持久恢复和最终一致，不能隐瞒已有渠道缓存收敛窗口。

## 4. API contracts 与权限

### 4.1 用户读取与保存

沿用 `/api/user/:id` GET 和 `/api/user/` PUT。现有用户管理路由的 `AdminAuth()`、用户菜单权限、目标用户角色限制继续生效。

GET 在管理详情 data 中增加：

```json
{"excluded_channel_ids": [12, 35]}
```

PUT 在现有用户编辑请求上增加可选字段（下例只展示增量，不是完整请求）：

```json
{"id": 123, "excluded_channel_ids": [12, 35]}
```

- 字段缺省：保持现状；`[]`：明确清空；`null`、小数、字符串、负数、0、超界 ID：校验失败。
- 成功沿用 `{"success":true,"message":""}`；剔除失效ID时在可选 data 返回 `removed_excluded_channel_ids`，前端刷新管理详情并提示已移除失效选项。管理端业务错误沿用 common/gin.go 的标准响应。
- 排除变更与用户资料同一事务保存，增加 `profile_version`，不单独增加 `auth_version`，不因路由偏好变更踢掉登录会话。
- 缓存发布完成才报告保存成功。DB已提交而缓存发布失败时使用项目现有数据库错误提示并记录底层错误；前端保留草稿，未自动重新读取覆盖草稿。保存失败不代表DB回滚，重新打开详情或重试可确认状态。
- 该字段的实际变更要求具备渠道读取权限，以免没有渠道权限的管理员借用户编辑探测/管理隐藏渠道；未变更/未提交时仍允许保存其他资料。
- 管理员并发提交同一字段沿用现有编辑接口的最后提交覆盖语义；省略该字段的旧客户端/其他资料修改必须保留它。首版不新增通用版本冲突协议。

### 4.2 渠道选项

增加管理只读端点 `GET /api/channel/exclusion-options`，放在 `router/channel-router.go` 已有管理员渠道组并使用 `ChannelRead` 权限，不开放公共访问。

- 查询：`after_id`（默认 0）、`limit`（默认 50，上限 100）、可选 `ids`（最多 256 个，用于回显选中项）。`ids` 与游标模式互斥。
- 响应：`{"success":true,"data":{"items":[{"id":12,"name":"channel-a","type":1,"status":1}],"next_after_id":12,"has_more":true}}`。
- 仅查询必要列，绝不返回 key、base_url、headers、渠道设置等敏感资料。只读且受权限约束的端点可复用现有渠道授权查询方法。
- 采用ID游标分页，弹层滚动自动续页；名称/ID搜索使用 `keyword` 查询全部渠道，300ms防抖、管理搜索限流与2秒查询超时。ids查询独立用于已选项回显。名称子串查询不属于索引覆盖查询，资源限制见文首交互修正。
- 首版不新增服务端分组过滤：排除本身对用户所有分组生效。选项查询使用 channels 主键的范围扫描或有界 IN，不直接在逗号分隔 group 字符串上全表 LIKE。实施时三库检查 EXPLAIN 与现有渠道授权查询的索引使用情况。
- 该端点只服务管理编辑，不在 relay 请求中调用。
- 清理运行沿用现有SystemTask管理列表/详情及其权限，结果提供处理人数、延期渠道数和最后失败阶段；逐渠道pending/游标保留在专用队列表，未新增逐渠道进度界面。删除接口保留既有响应形状，不把JSON投影仍待清理的任务描述为“全部清理完成”。

## 5. 数据流与缓存一致性

管理保存：表单 → 身份/权限/目标校验 → 有界数组校验 → 事务内确认渠道存在/权限并剔除失效ID → 同事务关系集合、用户投影、profile_version更新 → 现有 profile fence 缓存发布 → 成功响应。

请求：现有 Token/Playground 身份上下文 → 已加载 UserBase → 一次解析排除列表 → 请求级 ChannelConstraints → 原有合法候选过滤 → 优先级/权重选择 → 上游 → 原有响应与结算。

- 不新增请求级用户查询。正常命中只在既有 `user:<id>` hash 增加一个有界字段；`auth:user:profile:<id>` 继续由现有资料缓存机制管理。
- 新格式 hash 中必须显式存在该字段，空列表编码为 `[]`。缺字段表示缓存不完整，不能解释成允许全部渠道。采用兼容的缓存版本/字段完整性检查，通过现有缓存回源路径恢复。
- 禁止为排除规则更新直接 DEL 整个 `user:<id>` hash：它含有额度增量状态。升级预热必须使用已有版本化整行发布协议，保留 Quota；实际实施需同时检查缓存schema版本及字段缺失，而非只增加Go字段。
- 热路径每个请求只解析一次。保存/缓存加载使用 `common.Marshal/Unmarshal`；解析后保留排序数组并用二分查找，不反复解析 JSON，也不为每次重试重复建 map。
- 规则快照贯穿同一普通 HTTP 请求的全部尝试。保存成功后开始鉴权的新请求读取新规则；与保存并发的在途请求允许完成其旧快照生命周期。
- Redis 关闭/不可用时沿用既有用户缓存回源机制；不可把读取失败、损坏 JSON、超限内容解释为空列表。无法取得可信规则时拒绝该用户本次路由，不影响其他用户。
- 现有缓存回源有同步 DB 行为，不能把本设计的“新增热路径 DB=0”解释为整个系统任何情况下 DB=0。本次不另建回源循环、不主动清空全体用户缓存；回源恢复要有请求时间上限，不能等待无界外部 I/O。
- 资料版本栅栏只保证较旧快照不能覆盖新缓存，不会回溯改变已取得旧快照的请求；这与上述生效边界一致。
- 不新增跨节点进程内用户副本，因此无需新建 Pub/Sub、广播 goroutine 或缓存失效队列。

## 6. 路由与业务边界

新增 `FilterUserExcludedChannels`，附着请求级排除集合。初始化必须覆盖插件预选、普通 relay、Playground 和 Responses WebSocket；约束附加需幂等，不能每次选择重复增长 Filters。

选择顺序：合法分组/模型/协议候选 → 用户 ID 排除 → 既有探测限制 → 剩余优先级 → 原有权重选择。权限排除是硬约束，优先级、权重、亲和、pin 均不能放宽它。

- 高优先级渠道全部被排除：本次直接在较低的合法优先级选择，不消耗一次“失败重试”，不向被排除渠道探测发包。
- 同优先级部分被排除：仅在剩余渠道上计算权重，保留各模式既有权重算法，不顺便重构内存/DB权重差异。
- 精确模型和规范化模型回退都应用相同排除，不因模型别名恢复禁用渠道。
- 不修改 `group2model2channels`、`channelsIDM`、渠道状态、abilities 或渠道每日限额；过滤结果只属于当前请求。
- DB 路径在加载 Channel 对象之前按 `Ability.ChannelId` 排除；后续元数据查询失败不能把已排除 ID 加回来。
- `auto`：逐组累积排除检查；某组全被排除可继续现有候选组搜索，最后无候选仍返回原有公开错误。数据库错误不会被当成空组吞掉。
- 普通软亲和命中被排除渠道：忽略本次亲和并选择其他合法渠道；不要删除可能被其他用户共享的亲和键。
- 严格会话亲和/Token指定渠道/原任务渠道：无法替代，内部记录具体冲突原因，对外沿用对应入口已有的不可用/绑定失败提示；不绕过规则，不切换可能无法承接会话/任务的渠道。
- 插件预选及预选复用都要验证；普通、视频/任务重试、LockedChannel 等直达分支均需检查，不能仅修改首次分发。
- Responses WebSocket：每次 `response.create` 在既有重新鉴权/检查处读取本轮规则。连接渠道已被排除则拒绝该轮并提示重新连接；不能把旧 lockedContext 覆盖回新的用户约束。
- 传统 Realtime 连接按连接建立时快照执行，不新增每帧查询或后台断链机制；新连接生效。
- 已创建异步任务的后台轮询、结果收集、退款/结算继续走任务原渠道，避免孤儿任务和余额损失；既有任务结果查询/下载（含图片代理）继续允许，保持现有访问控制，不因排除规则额外增加用户查询。用户主动提交基于原任务的新推理/生成操作必须检查当前排除规则，不能改投别的上游。排除不承担既有结果撤销访问功能。
- 系统健康检查、管理员渠道测试不冒用被编辑用户，不受此用户规则控制。

## 7. Error handling strategy

沿用各传输现有 relay 错误封装（HTTP OpenAI/Claude格式、任务格式、WebSocket事件），不在 relay 使用管理 API 的 HTTP 200 business-error 包装。公开错误与内部诊断分开构造，内部原因绝不能放入最终序列化的公开错误对象。

| 情况 | 公开状态/错误码 | 内部诊断原因 |
|---|---|---|
| 此次选择过滤过排除渠道，最终无候选 | 首次分发沿用503 / `model_not_found`；重试沿用既有无渠道分支的状态/code | `no_eligible_channel_after_user_exclusion`；不声称排除是唯一原因 |
| 本来就没有合格候选 | 既有无可用渠道错误契约 | `no_eligible_channel`，不误称被用户排除 |
| 指定、原任务或严格会话渠道被排除 | 保留传输原有拒绝状态分类，使用既有通用访问/选路错误码 | `user_channel_excluded`，附pin来源/strict状态，不自动换渠道 |
| 规则损坏/无法取得可信快照 | 沿用该入口既有的选路不可用错误契约，不附底层异常 | `user_channel_policy_unavailable`，不放行 |
| 用户编辑参数错误 | HTTP 200 / `success:false` | common错误helper；管理界面保留输入 |

公开message直接复用对应入口原有文案及参数来源，不统一改成“服务暂时不可用，请稍后重试”，不新增排除专用提示。已核对实际代码与 `i18n/locales/zh-CN.yaml`：

| 现有入口/情况 | 现有语言键与原文 | 本功能使用方式 |
|---|---|---|
| 普通首次分发无候选 | `distributor.no_available_channel`：`分组 {{.Group}} 下模型 {{.Model}} 无可用渠道（distributor）` | 排除后无候选进入同一分支，复用 `noAvailableChannelMessage` |
| 任务插件首次分发无候选 | `distributor.no_available_channel_task_plugin`：`分组 {{.Group}} 下模型 {{.Model}} 无可用渠道：该模型由任务插件认领，但当前没有启用的渠道可服务此模型（distributor）` | 沿用现有插件分支，不新增排除信息 |
| 普通重试无候选 | `relay.retry_no_available_channel`：`分组 {{.Group}} 下模型 {{.Model}} 的可用渠道不存在（retry）` | 复用 `controller/relay.go:getChannel` 的无候选输出 |
| 已有指定渠道禁用情况 | `distributor.channel_disabled`：`该渠道已被禁用` | 实际禁用保持原分支；排除不修改全局渠道状态，也不自创排除专用文案 |

原有分组、模型插值及既有任务/会话不可用错误码保持兼容；“不暴露具体信息”指本功能不额外向用户说明排除策略、具体被排除渠道、名单、内部故障和筛选诊断，不是删除项目原有提示中的分组/模型。公开code/type/param、HTTP头、WS关闭原因、任务错误字段均不得新增 `user_channel_*` 内部原因或泄露受限诊断。

继续经过 `PresentLocalRelayAbort` / `PresentRelayError` 等既有输出链。管理员已有错误替换规则如果命中，最终显示项目当前配置的替换结果；关闭替换、规则keep、未命中或替换器panic时，回退上表对应的既有文案。不要绕过替换规则，也不要强制调用默认兜底提示。内部排除原因保存在独立诊断中，不拼进 `get_channel_failed` 的 `{{.Error}}` 参数。

需要在候选过滤阶段保留准确的内部失败原因，穿过精确/规范化模型、auto组循环、首次选择及重试转换，不做额外DB计数查询来判断错误原因。`middleware/distributor.go` 的现有 `NoAvailableChannel` / `noAvailableChannelMessage` 分支继续负责公开文案，诊断结构独立保留排除原因，不需要为隐藏该原因绕开现有提示。只要仍有允许渠道，用户排除不是最终失败原因；后续由探测/上游失败引起的错误保留其真实内部原因。

策略拒绝不计入上游渠道故障、不自动封禁、不扣额度、不制造逐候选日志或新统计任务。内部异常使用请求logger；后台故障使用系统日志。新增UI文案需先按i18n-translate技能补齐七语言；后端复用已存在的语言键，新增键按实际仓库的 `i18n/locales/*.yaml` 资源补齐，避免修改上游专有前端错误映射文件。

### 7.1 管理员及员工日志详情

通过 `model.LogOther` 的受限域写入 `other.admin_info.user_channel_routing`，实际结构如下：

```json
{"reason":"no_eligible_channel_after_user_exclusion","phase":"selection","excluded_count":2,"rejected_count":2,"channel_ids":[12,35],"truncated":false}
```

- 列表最多记录20个实际被过滤的渠道ID，附总数与截断标记，整个诊断预算≤4KiB；不保存用户完整排除列表，不逐渠道记录事件，不包含API Key、headers、URL、原始规则字符串或底层错误堆栈。诊断只存ID，管理员可沿用已有渠道展示，员工不因此获得名称/密钥权限。渠道后来被删除，已保存的reason/ID/计数仍可解释当时失败，不通过实时渠道表JOIN补原因。
- `phase`记录selection、pin、strict_affinity、origin_task或websocket_round等实际入口；有pin时追加pin_source。分组/模型使用原有日志列。rejected_count是拒绝检查次数，可能包含重复检查；channel_ids最多20个且去重，truncated是保守截断提示。没有伪造剩余渠道总数/策略版本。DB路径提前按ID过滤，因此归因表述为“过滤过且最终无候选”，而非断言所有其他约束都可满足。
- `logs.content`、公共 `error_code/error_type/status_code`、顶层Other及content_parts仅保存安全公开摘要。内部reason只放受限结构，不能复制到public字段、通用reject_reason或可被用户导出的扁平列。
- 普通用户self、Token日志、用户导出：后端剥离诊断，不依赖前端隐藏。管理员有日志权限可看详情；员工仅对已有授权客户的日志保留诊断及渠道ID，延续现有渠道名称/root/audit信息隐藏规则。员工导出按相同可见范围处理；员工身份调用self/token端点仍使用公开投影，不按角色自动提升。
- 前端使用日志视图权限（管理员/授权员工）渲染诊断块；不能只用 `isRealAdmin` 导致员工看不到，也不能只用客户端 `isAdmin` 代替后端授权。查询、详情、导出均验证客户归属，防止拿另一客户request_id/log_id绕过。
- 导出不是列表渲染的自动延伸：需核对 `model/log_export_columns.go` 的详情内容/列白名单及员工导出授权、下载时再次校验路径；只在详情组件显示并不会让授权员工导出自动包含原因。新增受限诊断导出应为明确字段，不把整个admin_info不加选择地输出。

### 7.2 首次拒绝也必须有日志

此前`RecordRequestPolicyTermination`只写请求内事件，首次候选全被排除时尚未进入`ProcessChannelError`。现在终止入口和`abortWithOpenAiMessage`显式调用有去重标记的`RecordUserChannelRoutingRejection`。

- 新增明确的本地选路拒绝记录入口，以安全摘要和上面的受限诊断调用既有 `model.RecordErrorLog` / 有界异步错误日志管线。未调用上游时channel_id=0、quota=0、无上游request_id，不冒充某渠道故障，不调用自动封禁逻辑。
- 同一逻辑请求/WS轮次只写一次本地选路终止日志，用请求内标记去重；若现有错误日志已包含该终止诊断则不重复。此前真正发过的上游失败尝试日志仍保留。普通成功回退可把简要诊断附在已有成功日志，不另写拒绝日志。
- 非阻塞入队，遵守现有错误日志开关、容量限制、隔离fallback和告警；日志故障不得改写公开响应或反向放行渠道。不增加同步DB写或每请求goroutine。管理员详情可见的前提是错误日志功能开启且管线成功持久化；极端队列/磁盘耗尽沿既有丢弃告警处理，不能承诺无损审计。
- 本功能新增日志仅进入既有error车道；压测全请求被拒绝时每秒约1667条错误日志的缓冲、fallback和落库压力，保证不能挤占消费/计费保留容量。沿用日志保留期，不新建按请求增长的表。

规则校验本身是请求必要的路由决策，不能异步执行；“缓存失败就忽略排除”会破坏功能承诺。Rule 0 对非关键旁路失败的静默降级不用于放宽用户明确配置的限制；拒绝只影响该次无法安全路由的请求。管理员可清空该用户的排除列表以取消其额外限制。

## 8. 用户交互

- 在现有用户编辑抽屉增加「排除渠道」字段，建议放在分组/额度区域；与现有管理员“Channel permissions”权限矩阵分开，避免混淆。
- 复用项目现有多选、搜索/列表、表单错误和底部保存按钮，按 shadcn-ui 项目技能实施，不新增自动保存动作。
- 选项显示渠道名称与ID；已选项可移除；选项分页加载，打开抽屉时只请求必要摘要，未加载的已选项保留ID占位。
- 提示该设置影响用户所有令牌和分组，空列表表示不额外限制。不要把列表展示成“启用渠道白名单”。
- 用户资料加载失败不允许提交该字段的空默认值；候选加载失败展示重试，保留已选 ID；无渠道权限时字段只读/隐藏，提交省略它。
- 保存时禁用重复提交；失败保留草稿，成功再刷新。排除规则保存后直接生效，无须在系统设置中另行开启。
- 不增加每次请求的前端模型可用性探测；模型展示不会因规则而自动隐藏，实际请求错误提供可执行提示。

## 9. Config parameters

不新增全局开关。用户明确要求取消总开关，优先于项目通用的relay功能开关约定；已移除配置注册、系统调优界面及专用权限。用户排除列表始终参与选路，空列表保持原有可用渠道范围，非空列表自动生效。

已登记的删除清理持续执行。清理批量/让步/操作超时采用上述固定保护上限，不新增环境变量或可调参数。

## 10. Main Chain Impact

同步：既有用户缓存读取中多解码一个有界字符串；非空时一次解析；候选循环中 ID 集合判断；无候选时返回本地错误。全都在上游调用之前完成，不扫描请求/响应正文。

异步/管理侧：资料保存、关系维护、缓存发布、迁移和清理均在管理/后台流程；独立SystemTask类型按租约串行执行删除清理。选路拒绝向既有错误日志管线非阻塞入队，后台批量写日志库；无新增逐请求goroutine。原有生成任务轮询与结算不变。

不增加热路径网络调用、分布式锁、用户全局锁、上游试探请求或每请求审计写入。不修改计费表达式或 relaykit 公共协议；内部错误码用宿主代码定义，若实际实现需要修改 relaykit，必须额外执行 `GOWORK=off go build ./...`。

## 11. Shared Resource Audit

| 资源 | 与 relay 的关系 | 隔离/容量措施 |
|---|---|---|
| users 行 | 已有鉴权缓存回源及额度更新访问 | 管理编辑及清理按主键短事务更新，不新增每请求读写；限制后台频率与锁等待，不批量锁住大量用户；同用户额度更新竞争需压测 |
| channels/abilities | 既有候选缓存和DB选择访问 | 管理保存短暂锁定有界目标渠道防止删除竞态；删除事务补齐abilities清理；不持锁清理用户、不反向扫描users |
| user_channel_exclusions / user_channel_exclusion_cleanups | 新增专用管理表，relay不访问 | 双向索引定位关系、持久pending记录，不扫描用户JSON；队列按完成删除，冲突仅管理侧解决 |
| system_tasks / system_task_locks | 既有后台任务框架；生成任务轮询也使用各自类型 | 新类型独立租约，既有类型不受阻；清理保持单worker，不借用relay worker池 |
| `user:<id>` | 既有鉴权与额度共享 hash | 增加独立字段；沿原 Lua 原子发布，不覆盖 Quota；限制 payload，禁止引入第二套操作同一 key 的缓存代码 |
| `auth:user:profile:<id>` 及 auth fence keys | 既有缓存一致性 | 复用既有发布协议，不擅自复用键表达新语义，不新增 key/锁 |
| DB/Redis连接池 | 原 relay 共用 | 热命中新增占用为零；清理限制单并发、有界超时和批间让步，测主链池等待；预热分批限速，避免缓存同时失效 |
| `group2model2channels`/`channelsIDM`/`channelSyncLock` | 全局渠道缓存 | 保留现有读锁范围，仅有界判断；绝不原地删除/排序共享候选，不在读锁下做用户 JSON 解析/I/O |
| 亲和缓存 | 部分键可能跨用户共享 | 只忽略本次不合法命中，不因用户规则主动删除共享绑定 |
| 请求级排除数组/Filters | 单请求所有尝试共享 | 一次建立且只读；不保存为全局可变用户 map，不按请求启动 goroutine |
| 日志error缓冲/fallback/LOG_DB | 已有relay日志和消费管线 | 每请求至多一条本地终止日志，诊断≤4KiB；使用已有error容量隔离与溢出告警，不抢占消费/结算保留容量；不新增同步文件写入 |

本次复用用户缓存是扩展其现有资料协议，不创建竞争的缓存子系统。若测得大字段增加 Redis 延迟/GC 不达标，先收紧上限或基于证据修订设计；不得用热路径锁“协调”争用。

## 12. Concurrency Analysis

30,000 RPM = 500 请求/秒；100,000 RPM ≈ 1,667 请求/秒。设 E 为用户排除数量（≤256），C 为本次分组/模型候选数量，A 为实际允许的尝试数。

- 空列表：常数判断，不做 JSON 解析、不分配排除数组或过滤器。
- 非空：每请求一次 O(E) 解析，过滤 O(C log E)，各次选择复用排除数组；整个请求新增成本受 E、候选数、现有重试总预算限制。
- **新增热命中 DB 调用 0、Redis 命令/往返 0、分布式锁 0、goroutine 0**。已有数据库回源、已有Redis pipeline不能算作本功能消除了的成本。
- 最坏 8 KiB 字段在全部请求均配置满额时，100k RPM额外网络载荷上界约 13.7 MB/s（不含协议与副本开销）；256 个 int64 的最终数组至少 2 KiB，JSON解码过程分配另计。必须测 allocs/B/op 与 GC，不能因为不加 I/O 次数就宣称零成本。
- 候选过滤复用已有遍历，最多产生请求局部候选切片；不为“被排除的每个渠道”记录日志或发独立事件。
- 管理侧保存做有界ID校验、至多256项关系重写和用户投影事务/缓存发布。清理成本与受影响用户数相关，不随relay RPM增长；单用户正常清理包含版本/关系读取、关系删除、投影更新、pending登记、缓存发布和游标确认，无扫描其他用户。
- 失败日志成本单独计入：若100k RPM全部在选路前拒绝，新增既有error日志入队约1667条/秒，4KiB诊断上界约6.8MB/s，尚未含日志其他列/索引/fallback。正常成功请求不新增日志行；这不是“整体后台DB写入为零”。

性能验收：E=0/1/8/64/256的解析微基准已执行，见第15节。不同候选规模、重试/auto及并发锁等待，以及30k/100k RPM mock-upstream的p95/p99、Redis带宽、CPU/GC、池等待和错误率仍需在预发布环境压测。本地解析均值不代表完整选路p99或网关吞吐。

## 13. 测试用例先行与发布

实现前先写有输入/输出断言的测试：

1. 数组边界：缺省/空/null、0/负数/小数、重复、256/257项、8KiB边界、无效/已删除ID；自助更新及旧客户端不得覆盖管理规则。
2. 普通选路：排除单个/全部/无交集、高优先级全排后下层可用、剩余权重、精确与规范化模型；证明被排除上游调用次数为0且共享缓存未改变。
3. 自动分组/重试：首组全排而下组可用、全部组耗尽、实际失败重试、预算边界，过滤不消耗上游attempt且不扩大分组权限。
4. 直达分支：软/严格亲和、指定Token、原任务、插件预选复用、任务LockedChannel、MJ、WebSocket首轮和后续轮；后台轮询/结算仍可完成原任务。
5. 错误归因：本来无渠道、排除后无渠道、探测阻断、DB/缓存故障分开；政策拒绝不封禁渠道、不产生最终消费。
6. 缓存竞态：旧资料回填不能覆盖新规则；保存成功后的跨节点读取；字段缺失/损坏不能放行；发布失败不能提示成功；额度hash字段不得被旧快照覆盖；在途请求保留旧快照。
7. 真数据库矩阵：SQLite、MySQL、PostgreSQL的fresh/旧schema升级、二次启动无DDL、NULL兼容、更新/查询/EXPLAIN。先对目标服务器schema-only副本演练，记录版本、命令和结果。测试数据隔离、按行清理。
8. 前端：读取回显、选择/删除、清空和遗漏语义、加载/保存失败保留草稿、权限不足、失效渠道、七语言；实施时运行对应Vitest、typecheck、lint与build。
9. 删除生命周期：单删/批删/按状态删、重复ID/不存在ID、0个/1个/100个/101个受影响用户、同用户多个删除渠道、软删/硬删用户；停用/改名/移组保留规则；删除失败事务回滚；仅清实际删除渠道的abilities/关系。
10. 竞争与恢复：保存检查后并发删除、旧表单回传、清理与用户新增其他排除/专属倍率/额度更新竞争；DB提交后缓存发布前崩溃、发布后确认前崩溃、队列登记失败、租约过期旧worker、Redis断连、CAS失败重试。最终断言关系/JSON/缓存一致且Quota不回退，不只检查界面标签消失。
11. 公开契约与脱敏矩阵：同一入口/分组/模型下，“原本无候选”和“排除后无候选”的公开文案、状态/code和错误替换结果保持一致；覆盖HTTP各协议、任务、WS消息/关闭原因、错误替换开启/关闭/keep/未命中/panic。原有分组/模型插值保留，公开正文/code/type/param/headers及self/token日志、用户导出不新增排除原因/渠道ID/名单/内部诊断；管理员和授权员工详情能见原因，员工跨客户查询/导出禁止，已有名称隐藏规则保留。
12. 日志边界：首次分发拒绝必须出现一条持久化日志；重试终止不重复本地记录；无上游不得写成渠道故障/扣费；记录被删除渠道时不依赖实时JOIN；错误日志关闭、队列满、日志DB不可用沿既有告警路径，不能阻塞路由。若新增诊断改变日志查询/编码，包含单独ClickHouse日志库验证。

发布顺序：三库迁移演练 → 全部节点升级新读写与删除登记协议 → 关系/投影一致性检查与有界缓存预热 → 确认无旧节点仍能写缺字段hash或删除渠道绕过队列 → 清理崩溃恢复/脱敏/日志验证 → 为测试用户配置排除规则 → 观察指标。新配置保存后直接生效；禁止在新旧路由节点混跑时承诺排除规则全面生效。回滚旧二进制需处理/保留可恢复清理队列，并明确排除规则将不再执行。

实际验证结果见第15节；上述测试清单含发布前验收目标，不代表每个组合均已完成实测。

## 14. 多厂商评审记录

已完成评审：主代理 Codex 与 Kimi K2.8 Preview。另尝试调用 Gemini 3.8 Flash。外部评审通过本机已配置的厂商代理进行，只读访问代码，不是模拟不同角色。

Kimi 首轮支持 users 专用字段 + UserBase + 既有 profile fence，提出必须加入 `adminOwnedUserColumns`、处理旧缓存schema、禁止直接DEL用户hash。主代理据代码采纳这些建议。

主代理对其首轮意见提出五项复核：损坏规则不能降为空；profile fence不代表所有在途请求强一致；解析耗时与GC必须实测；保护管理员专属字段且不得删除额度hash；软亲和不清共享键、严格绑定不能切换。Kimi 第二轮明确同意五项纠正。没有采纳首轮未测量的1–3μs估计，也没有把不正确的RPM换算写成性能结论。

Gemini 在读取草案并开始核查后以 `TypeError: fetch failed` / 500 结束，未返回有效最终意见，因此不计入方案共识。路由结论由主代理依据实际代码完成核对；本轮已完成的是 OpenAI/Codex 与 Moonshot/Kimi 两厂商讨论，不声称三厂商评审均成功。

2026-11-02补充复核：Kimi支持关系表为权威、JSON为投影，提醒租约恢复、软删用户和员工导出边界。主代理保留持久队列/新读版本CAS：其“仅best-effort、死引用无害、下次编辑再修剪”的替代不满足用户明确要求的自动清理；仅logger限频也不能满足管理员/员工在日志详情查看首次拒绝原因。复用现成SystemTask及异步error日志管线，避免重建任务/日志框架。用户随后明确允许员工查看，故诊断保留在授权客户日志，禁止加入员工隐藏清单；self/token可见范围不变。

## 15. 实施验证记录

实现包括管理端字段和分页选项、关系/投影双写、缓存schema 5、统一过滤器、固定渠道/原任务/WS检查、删除持久队列及员工可见的受限日志。按用户后续要求移除总开关，配置保存后直接参与后续请求选路；没有部署或修改运行环境的业务配置。

### 数据库与恢复

隔离测试环境：SQLite 3.50.4（临时文件）、MySQL 8.0.46、PostgreSQL 17.11；另使用独立Redis 7验证用户缓存。Docker端口仅绑定127.0.0.1；没有访问真实上游模型或迁移生产数据库。

`TestUserChannelExclusionDatabaseMatrix`验证新建、移除新列后模拟旧用户表升级、已有额度保留、唯一键、两次迁移、反查执行计划，并在三库执行优先级回退与清理重放。SQLite使用覆盖索引，MySQL使用range + Using index，PostgreSQL使用Bitmap Index Scan；均命中`idx_exclusion_channel_user`。

新增列/表未出现重复迁移DDL。发现已有PostgreSQL `users.access_token` 的char(32)规范化在重复AutoMigrate时仍发DDL；这是原字段的既有行为，本次未修改该认证字段，因此不能声称整个项目二次启动完全无DDL。旧schema测试为本地模拟，尚未对目标服务器schema-only副本演练。后续已补测MySQL5.7/PG9.6最低版本，见第16节。

生命周期断言覆盖三种渠道删除、软删除与已消失用户清理、pending重放、并发编辑后的最新投影发布、失去租约禁止确认、Redis额度不回退、旧资料回填被fence拒绝、缺字段hash重新加载，以及管理保存缺省/清空/非法输入。没有使用DEL用户hash实现排除更新。

### 路由与日志

新增用例覆盖内存/DB两种选路、排除高优先级后的下层选择、无交集用户不受影响、损坏规则拒绝、固定Token/原任务/WS连接渠道、软/严格亲和及共享绑定保留。公开契约测试比较相同分组/模型的“全排除”和“无可用渠道”，状态、code和message相同。

首次失败通过现有异步管线实际写入日志库：重复出口仅一行，channel_id=0、quota=0，内部诊断只在admin_info。WS首轮拒绝另经真实本地WebSocket和mock-upstream fixture验证，上游连接次数为0。投影测试验证self/用户导出剥离诊断，管理员/员工保留诊断且员工仍不见渠道名称。

### 命令与结果

- `go test ./model -count=1`（随相关包整包运行）：通过。
- `go test ./model ./controller ./service ./relay -run 'TestUserChannelExclusion|TestParseUserChannelExclusions|TestSelectResponsesWSChannel' -count=1 -v`：通过，包含三库矩阵与Redis回归。
- 扩展整包回归发现并修复测试fixture遗漏新增表、系统设置scope数量更新，以及auto探测错误分支未保留probe.Group。`go test ./controller -count=1`最终通过（218.665秒）；service、relay、middleware、service/authz、service/settingsaccess整包回归通过。最后的WS日志补丁另经控制器整包及路由定向回归通过。
- `go build ./...`：通过。
- 前端`node_modules/.bin/tsgo.exe -b`：通过。仓库`bun run typecheck`内部调用`bun check -b`，本机Bun 1.3.13不支持该子命令，因此使用项目已安装的TypeScript编译器执行相同build类型检查，未修改项目脚本。
- `bun run lint`：通过；输出既有其他模块warning，无error。
- 用户表单、日志详情和系统设置权限的定向Vitest：3个文件、122个测试通过。
- `bun run build`：通过。七种语言同步本功能文案，取消开关时移除两个开关专用键并更新用户字段说明，未修改受保护的前端认证/HTTP模块。
- `git diff --check`：通过。
- 取消总开关后重新验证：无需设置即可应用排除、空列表不建立过滤器、损坏规则拒绝、WS首轮拒绝落日志、固定渠道和软/严格亲和测试通过；系统设置权限注册测试通过。前端类型检查、lint、2个文件118项Vitest及生产构建通过。七语言已同步删除开关文案。

### 解析微基准

Windows amd64、AMD Ryzen 7 8700G，`go test ./model -run '^$' -bench BenchmarkParseUserChannelExclusions -benchmem -count=1`。每个ID为7位数，以下为一次运行的均值，其他测试进程并行运行，不能视为稳定p99：

| 排除项数 | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 0 | 2.122 | 0 | 0 |
| 1 | 283.4 | 184 | 4 |
| 8 | 1,185 | 296 | 7 |
| 64 | 7,768 | 1,192 | 10 |
| 256 | 27,667 | 4,264 | 12 |

后续已执行本地完整HTTP链路负载，见第16节。尚未执行生产环境长时间负载、跨节点缓存失效压测、ClickHouse日志库验证或浏览器交互验收；本地结果不能替代这些发布验收。

## 16. 扩展测试：正常、异常、关联、集成与压测（2026-11-02）

全部使用临时SQLite文件或专用Docker数据库，没有连接真实模型上游、清空开发数据库或部署变更。环境：Windows amd64、Go1.26.10、AMD Ryzen7 8700G；MySQL8.0.46/5.7.44、PostgreSQL17.11/9.6.24、SQLite3.50.4、Redis7.4.11。

### 正常流程、异常与关联逻辑

- 管理保存→资料缓存发布→Token鉴权→Distribute→Relay→本地mock上游→计费/日志的真实HTTP集成覆盖普通JSON和SSE；依次验证无排除、排除高优先级渠道后选低优先级、全部排除、清空恢复、删除渠道并清理投影后继续请求。7次成功扣7单位，3次拒绝不扣费。
- 单删、批删、按状态删在队列INSERT处注入失败，验证渠道及Ability仍存在、队列无半成品；清理投影UPDATE失败后关系、版本、pending全部回滚，下一次可重试。用真实SQL事务和GORM错误注入，未mock GORM。
- Redis客户端断连时发布失败，pending保留、游标不前移、任务延迟重试；恢复后重放最新投影，原Redis扣减额度不回退。worker层另验证任务状态failed/cache_publish，以及新任务接续完成。
- SQLite/MySQL/PostgreSQL执行10轮同时保存与删除，事务冲突重试后只保留仍存活渠道的关系与JSON。SQLite测试连接池为1，串行单写者；MySQL/PostgreSQL使用多连接行锁。另验证普通用户更新不能覆盖管理员新排除或倍率、旧版本回填不能恢复旧规则。
- 后台真实SystemTask创建、认领、执行、完成覆盖0/1/100/101名用户：每轮最多100，剩余用户下一任务处理，软删除用户也清理，额度不变。
- 自动分组过滤保持总attempt预算，不扩大Token授权分组；固定渠道、原任务、WS、软/严格亲和及共享绑定保留沿既有回归覆盖。
- 选项API覆盖分页、按ID回显、非法分页参数、四字段响应且无密钥。日志formatter和前端详情验证管理员/授权员工视图保留诊断，普通用户视图隐藏诊断；员工归属与导出权限由既有整包回归覆盖。

### 本地HTTP负载结果

可重跑入口：`TestUserChannelExclusionLoad`，默认跳过；仅在`TEST_EXCLUSION_LOAD=1`且DSN明确指向专用`exclusion_http`数据库时启用。设置`TEST_RESPONSES_SQL_DSN`、`TEST_RESPONSES_LOG_SQL_DSN`、`TEST_EXCLUSION_REDIS_URL`和可选报告路径`TEST_EXCLUSION_LOAD_REPORT`后执行：

```powershell
go test ./controller -run '^TestUserChannelExclusionLoad$' -count=1 -v -timeout 5m
```

128个有界worker，普通JSON/SSE各50%，真实HTTP/Redis/MySQL/PostgreSQL，内存渠道缓存、批量额度更新和异步消费/错误日志开启。每阶段10秒，延迟包含计划发送后的排队时间。三轮总215,040次请求；下表及JSON保存修复日志边界后的最后一轮（错误日志开启）的71,680次结果。不是多节点压测或长时间浸泡测试，mock上游无真实模型推理延迟。

| 场景 | 目标RPS | 实际RPS | p95/ms | p99/ms | 请求数 | 非预期失败 |
|---|---:|---:|---:|---:|---:|---:|
| 无排除，30k RPM | 500 | 500.05 | 2.346 | 2.755 | 5,000 | 0 |
| 无排除，100k RPM | 1,667 | 1,666.83 | 2.594 | 4.013 | 16,670 | 0 |
| 排除高优先级1项 | 1,667 | 1,666.80 | 2.721 | 4.668 | 16,670 | 0 |
| 排除256项 | 1,667 | 1,666.64 | 2.656 | 3.797 | 16,670 | 0 |
| 全部排除（预期503） | 1,667 | 1,666.86 | 1.963 | 2.382 | 16,670 | 0 |

断言：所有被排除上游调用次数为0；55,010次成功转发对应55,010条消费日志，16,670次预期拒绝对应16,670条零额度/channel_id=0且model_name正确的错误日志；用户和Token SQL额度及用户Redis额度与成功次数精确一致。对比为顺序短时采样，不能据p99差异推断排除使系统变快。

原始结果：[user-channel-exclusions-load.json](user-channel-exclusions-load.json)。错误仍沿既有无可用渠道契约返回，压测不引入总开关。

### 执行命令和验收边界

- 真实最低版本矩阵：`go test ./model -run TestUserChannelExclusionDatabaseMatrix -count=1 -v`，DSN分别指向MySQL5.7.44和PostgreSQL9.6.24专用`exclusion_matrix`；fresh/模拟升级/二次迁移/索引计划/清理重放/事务回滚/并发保存删除通过。
- `go test ./service -run '^TestUserChannelExclusion' -count=1 -v`：worker及故障恢复通过。
- `go test ./controller -run '^TestUserChannelExclusionHTTPIntegration$' -count=1 -v`：MySQL主库+PostgreSQL日志库通过。
- 竞态检测：使用MSYS2 GCC15.1.0、`CGO_ENABLED=1`，`go test -race ./model ./service ./relay -run 'TestUserChannelExclusion(ConcurrentSaveDelete|CachePreservesQuotaAndFence|ContextAlwaysApplies|AutoGroupsAndRetryBudget|AffinityAndPublicError|LockedPaths)$' -count=1 -timeout 5m`通过。
- 整包回归：model（118.361秒）、controller（229.502秒）、service、relay、middleware、service/authz、service/settingsaccess通过。日志修复后重跑service/relay/middleware整包，以及HTTP集成、选项接口和负载，全部通过。`go build ./...`通过。
- 前端用户管理、日志、系统设置权限共42文件/670项用例：扩大回归发现permissions夹具缺少新增选项API，补齐后重跑涉及的2文件/42项全部通过，其余41文件初轮通过。类型检查（tsgo）、lint和生产构建通过；新增详情用例覆盖诊断可见/不可见和截断提示。

实测发现并修复两项日志边界：首次HTTP分发失败发生在`original_model`写入之前，导致日志模型为空；现在从请求内排除策略保留的模型名补齐，不改变其他上下文或公开错误。损坏规则且无候选时，诊断`channel_ids`原为null，现在固定输出空数组，满足详情组件契约。两项均先添加失败断言再修复；热路径只增加请求内字符串赋值，无额外DB/Redis调用。

未覆盖的发布条件仍包括生产服务器schema副本迁移、生产拓扑/慢上游长期负载、跨节点发布故障及ClickHouse独立日志库；香港测试机schema演练见第18节。本次没有修改ClickHouse查询/表结构。上述限制不等于这些组合已经通过。

## 17. 启动迁移保护

用户要求避免生产启动时长时间执行DDL。本功能新增一个可空且无默认值的`users.excluded_channel_ids TEXT`列；不回填既有用户。两个新表的索引分别为主键`(user_id, channel_id)`及反查索引`(channel_id, user_id)`、主键`(channel_id)`及到期索引`(next_attempt_at, channel_id)`。没有向现有大表新增索引。

在通用AutoMigrate之前检查已有users表的新列。MySQL只执行显式`ALGORITHM=INSTANT`加列（追加到表末尾），不允许隐式降级为INPLACE/COPY；元数据锁等待上限2秒，整段检查/DDL上下文上限3秒，另给连接会话恢复最多1秒。PostgreSQL在事务中设置局部lock_timeout=2秒、statement_timeout=3秒后添加无默认值可空列；SQLite保留简单ADD COLUMN并使用3秒上下文。新库仍由正常建表一次创建完整用户表。既有列兼容性由回归矩阵验证，重复启动不得因本功能再改列。

MySQL5.7或不支持INSTANT的表必须在发布前由运维完成加列，再启动新版本；应用只快速报告缺少所需迁移，绝不为了启动成功自行重建生产users表。这不是功能开关，也不在缺列时绕过排除规则。保护范围为本功能新增列，不能替代项目其他既有迁移及目标schema演练。

测试先覆盖：fresh/upgrade/第二次零新增DDL；MySQL8即时迁移、MySQL5.7快速拒绝及预迁移后启动；三库已有用户额度和空规则兼容；真实锁占用时快速退出且无半成品；MySQL连接归还池前恢复会话锁等待配置，失败则丢弃连接。

生产已知条件为MySQL8.0/8.4、约65,000名用户。专用MySQL8.0.46数据库填充65,000条用户后，包含元数据检查的即时加列实测63.325ms；SQLite与PostgreSQL17的迁移/真实占锁/重复执行测试通过。此数字是本地测量，不承诺生产同样耗时，也不把INSTANT等同于无元数据锁。参考[MySQL在线DDL操作与限制](https://dev.mysql.com/doc/refman/8.0/en/innodb-online-ddl-operations.html)、[元数据锁等待限制](https://dev.mysql.com/doc/refman/8.0/en/innodb-online-ddl-limitations.html)。

推荐在旧版本仍运行时预先检查并加列，成功后再发新包；旧程序忽略此新增可空列。先确认该列尚不存在：

```sql
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = 'users' AND COLUMN_NAME = 'excluded_channel_ids';
```

只有查不到该列时，在专用运维连接执行以下SQL，执行后关闭该连接：

```sql
SET SESSION lock_wait_timeout = 2;
ALTER TABLE users ADD COLUMN excluded_channel_ids TEXT NULL, ALGORITHM=INSTANT;
```

如锁超时，先排查阻塞事务并在合适窗口重试；如INSTANT不支持，先按目标表结构制定独立迁移方案，不能直接删掉ALGORITHM限制发布。应用缺少所需列时会快速报错退出，不能承诺任意数据库状态均能启动。预迁移成功后启动只需检查此列，新建的两个空表及索引仍按正常迁移创建。

## 18. 香港测试服务器部署与验证（2026-11-02）

经用户授权部署至香港测试机`43.251.102.17`，应用保持原端口3002和原进程环境。最终构建版本为`08bdb0e47-user-channel-exclusions-20261102-r2`，Linux amd64二进制SHA256为`091b18e0b1eb4b880332db2316769aec07a8847ff4de710745123a37e0d00601`。包含本工作区未提交的新功能文件，不能用相同HEAD的旧二进制代替。

服务器主库MySQL8.0.46，日志库PostgreSQL14.24；部署前147名用户、50个渠道。先保存实际users建表语句，在独立临时库载入同一结构，验证显式INSTANT加列及重复检查；随后在旧进程仍运行时预加列，客户端完整命令耗时552.253ms（含mysql客户端启动，不是纯SQL计时）。新列为可空TEXT，无历史回填，无既有大表新索引。两个新表的主键、反查索引、到期索引均核对正确。最终构建再次启动成功。

旧二进制、原配置、进程环境保存在测试机`/root/hk-exclusion-20261102/rollback/`，目录权限0700。提供回滚命令`python3 /root/hk-exclusion-stage.py rollback`；只恢复程序，保留兼容的新增数据库列和表。回滚文件已核验，未在最终验证后执行回滚演练。

使用独立测试账号、唯一测试模型及只监听127.0.0.1的模拟上游；未调用真实AI厂商。高/低优先级渠道成本系数分别为0.2/0.7，模型固定每请求1000 quota，因而每笔预期成本分别为200/700 quota。旧版基线与新版相同成功请求均扣1000 quota。

正式验证累计326次HTTP请求：218次成功、108次预期拒绝。逐请求ID核对218条消费日志和218条成本台账，实际命中渠道、日志渠道、台账渠道完全一致；消费合计218000 quota、成本99600 quota。用户SQL余额、Redis余额、令牌余额、请求计数、渠道累计消耗全部对平，拒绝请求无扣费。初轮测试夹具的邀请人字段误设为NULL导致既有成本校验失败，已补齐测试数据的默认值并单独归档初轮结果；正式账务结论仅来自修正后重新记录基线的测试，不将初轮计入通过结果。

已覆盖：空规则、排除高优先级后回落、全部排除且零上游调用、清空恢复、其他用户隔离、省略字段保留、自助修改不能覆盖、重复ID归一、非法/null/负数/小数/超限拒绝、指定渠道不能绕过、上游500后既有重试、损坏缓存拒绝并恢复、渠道删除自动清理关系/投影/缓存且保留额度。聊天和Responses均验证JSON与SSE。管理员及员工日志能查看详细原因；员工不能越过客户归属；用户/令牌日志及用户CSV导出隐藏内部原因，管理员CSV包含诊断。

测试机为共享2GB主机，远程负载限定30 RPS、10个工作线程，三阶段各100次请求、50%流式。基线/排除回落/全部拒绝的p95分别21.481/19.474/11.330ms，p99分别26.355/28.216/13.793ms，非预期错误和误选上游均为0。这是有界功能负载，不是容量、长期稳定性或真实上游压测；本地100k RPM短时结果仍单独记录于第16节。

通过SSH本地隧道用Chrome访问实际部署页面，验证用户抽屉加载、渠道名称搜索、选择、保存、重新打开回显，以及页面保存后实际请求受限，浏览器无运行错误。实测发现共享MultiSelect默认只检索ID；新增可选`searchByLabel`仅由排除字段启用，不改变其他调用方的默认检索行为。先复现失败测试再修复，相关5文件27项测试、tsgo类型检查、lint及生产构建通过；新增测试同时验证名称/ID检索和其他调用方原行为。没有编辑受保护的登录、HTTP客户端等前端基础文件。

详细请求记录、负载数据及清理结果见[user-channel-exclusions-hk-validation.json](user-channel-exclusions-hk-validation.json)。测试渠道与专用价格项删除，测试账号归档，令牌与全部浏览器会话撤销，模拟上游停止；测试消费及审计日志保留用于复核。

### 界面补充修复（r3）

用户编辑抽屉增加“排除渠道”快捷导航，沿用原有滚动定位及选中高亮；设置独立成区，导航与字段共同受编辑模式及渠道读取权限控制。日志详情将四类渠道排除原因、七类执行阶段映射为七语种显示文案，保留后端原始诊断值；未识别的未来代码保留原值便于管理人员排查。公开错误契约和诊断权限投影保持原逻辑。

本次仅调整前端展示，不新增SQL、Redis操作、路由或计费逻辑。回归覆盖中文原因/阶段、未知代码、诊断可见性、菜单定位与无权限隐藏、点击菜单不提交表单，以及原有排除表单和渠道检索；4文件67项测试通过，tsgo类型检查、lint（仅既有警告）、生产构建及Linux二进制构建通过。

香港测试机已更新为`08bdb0e47-user-channel-exclusions-20261102-r3`，SHA256为`ce3383433fd3224b12f74fdc2732407cf77baf0f856a2148a61c476594b6fac1`，健康检查通过；r2二进制另存为`/root/hk-exclusion-20261102/new-api-r2-backup`。通过SSH隧道用Chrome中文界面核对菜单定位/高亮与实际历史错误日志，显示“排除后无可用渠道”“渠道选择”，浏览器无运行错误。截图保存在`artifacts/hk-exclusion-navigation-zh.png`及`artifacts/hk-exclusion-log-detail-zh.png`。本次仅查看既有用户设置和日志，未保存用户修改或调用上游；独立验证账号随后归档并撤销所有会话。

### 真实上游正常功能回归（2026-11-02）

用户随后明确授权真实厂商调用，覆盖AGENTS.md第15.4条限制，并明确不需要压测。本轮在r3上执行20次串行HTTP功能请求；通过已有nexaxis.ai渠道访问真实上游，不使用模拟服务，不独立证明上游所声明的厂商身份。使用独立账号及一小时有效、500000 quota（界面折合1美元）的限额令牌；没有改动原有渠道、定价或系统配置，也没有为测试修改业务代码。

| 模型与接口 | 成功 | 失败 | 结论 |
| --- | ---: | ---: | --- |
| OpenAI gpt-4o-mini，Chat Completions JSON/SSE | 4 | 2 | 已验证JSON/SSE完整回复；初始随机渠道部分返回上游model_not_found或system_disk_overloaded，测试账号排除其他候选后均成功 |
| Claude haiku-4-5，兼容Chat及原生Messages JSON/SSE | 4 | 0 | 全部成功，用量一致 |
| Gemini 2.5 Flash，兼容Chat及原生generateContent JSON/SSE | 5 | 1 | 已验证两类接口普通与流式响应；原生流式首次命中259渠道返回upstream_error，测试账号切换262渠道后成功 |
| Grok，4.20非推理及chat-fast，Chat JSON/SSE | 0 | 4 | 263渠道均返回上游model_not_found，不能计为通过 |

13次成功逐请求对应13条消费日志、13条成本台账。上游响应用量与日志输入/输出计数一致；按日志记录的模型倍率、输出倍率及分组倍率独立复算收费，再按渠道成本系数复算成本，均一致。用户MySQL/Redis余额、令牌余额、请求计数与六个命中渠道的累计消耗全部对平；累计扣费332 quota（测试账户0.000664美元），成本台账332 quota。7次HTTP失败无消费记录、无扣费。所有失败决策记录的来源均为upstream，不是本地排除过滤拒绝；这不能替代上游可用性修复或证明所有未测路径正确。

首8次短提示请求同时命中既有短文本路由策略；测试夹具初设retry_times=0，核对日志后确认其语义是继承全局重试（共出现4次额外尝试），后12次明确设为-1禁用重试，并采用超过128字符的普通业务文本。保留首轮所有失败和重试证据，未将其剔除。最后4次仅修改测试账号排除集，确认OpenAI/Gemini落到未被排除的有效渠道并正常结算。首次对账在异步成本队列完成前发现缺行，待队列落库后全部匹配，没有修改账务数据。

验证后清空测试规则、撤销令牌和全部会话、归档测试账号；渠道启停状态与测试前一致。详细记录见[user-channel-exclusions-real-vendor-validation.json](user-channel-exclusions-real-vendor-validation.json)。本次范围仅为上述文本接口的正常回归，不包含压测、图片、音视频、任务或Responses接口。
