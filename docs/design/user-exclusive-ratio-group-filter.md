# 用户列表：按专属倍率分组名称筛选

状态：2026-11-15 用户确认后已实现并完成下述验证；未部署。

## 目标与范围

用户要求「专属倍率」下拉框按分组名称筛选，替换「已配置／未配置」。沿用单选和下拉搜索交互，选项复用用户页已加载的 `/api/group/` 分组清单，不从当前页用户数据推断选项。

选择 `vip` 表示查找 `users.group_ratios` 中含 `vip` 键的用户。例如账户属于 `default`、专属配置为 `{"vip":0.5}` 的用户应命中；账户属于 `vip`、专属配置仅有 `{"default":0.5}` 的用户不命中。现有「用户分组」仍筛选账户归属，两项条件可组合。

不选择时不限制专属倍率分组；选择、切换和清除条件均回到第一页。URL、刷新、前进后退、排序、总数和服务端分页应保持一致。复用现有表格、移动端卡片、加载和空状态。

## 当前实现与后端缺口

- 现有设计为 [user-exclusive-ratio-filter.md](./user-exclusive-ratio-filter.md)，记录的是 2026-10-26 的布尔筛选实现及历史验证，不能作为本次分组筛选的验证结果。
- `UsersTable`、路由搜索 schema、前端 `searchUsers`、`controller.SearchUsers` 和 `model.UserSearchOptions` 目前都使用 `has_exclusive_ratio`。
- `model/user_exclusive_ratio_filter.go` 目前仅判断有没有已存储配置，没有指定分组名称参数。
- **已确认的后端补充**：扩展现有管理员搜索接口与数据库查询；不能只替换前端下拉内容，也不能在浏览器过滤当前页冒充完整结果。

## Existing Logic and Related-Path Findings

- `router/api-router.go` 的 `adminRoute` 使用 `AdminAuth` 和 `AdminMenuUsersView`；`controller.SearchUsers` 对管理令牌先走 `listAdminTokenUsers`，后者拒绝除游标、条数、关键词以外的参数。本次只扩展已有会话搜索分支。
- `model.SearchUsers` 在同一事务内构造基础条件并执行 COUNT、分页，之后补充员工归属和邀请备注。新增条件放在现有专属倍率条件处，保留全部原有筛选、排序和补充逻辑。
- `controller.GetGroups` 返回 `ratio_setting.GetGroupRatioCopy` 的键；`UsersTable` 已通过 `['groups']` 查询缓存获取完整清单。`useTableUrlState` 已负责更改列筛选时清除页码，路由 schema 控制可恢复的参数。
- `DataTableFacetedFilter` 已提供单选、搜索、清除和 `translateLabel: false`；但已选徽标只显示存在于 options 的值。因此专属分组选项须补入 URL 中仍被选中但已不在清单中的名称，保证条件可见可清除。
- `users-columns.tsx` 的专属倍率隐藏列仅供筛选挂载，实际标记由 `hasExclusiveGroupRatios` 判断，后者接受数字/null 的非空对象。本次隐藏列改为对应分组筛选的原始配置字段，不改变标记函数。
- `controller.UpdateUser` 保留未提交的专属配置，校验提交的分组并在保存后刷新用户缓存；`FilterUserGroupRatios` 和后台清理处理分组删除。本次仅查询，不进入以上写入和清理流程。
- `model.UserBase.GetGroupRatios`、`middleware.TokenAuth` 和 `ratio_setting.ResolveGroupRatio` 在 relay 读取并解析用户配置；用户表及 DB 池共享，解析缓存不需要本次读查询触发失效。
- 具体查询采用 SQLite 受 CASE 保护的 `json_each.key` 精确比较；MySQL/PostgreSQL 沿用完整扁平对象词法正则，在完整成员边界插入目标键，接受字符本身和等价 JSON 转义。查询参数绑定；无 JSON 强制转换、无应用层全表扫描。复用现有配置存在性逻辑，避免执行两次配置判断。

## 数据流

管理员打开用户页 → 复用 React Query 的分组清单 → 选择专属倍率分组 → 更新 URL 和用户查询键、重置页码 → 调用用户搜索接口 → 服务端将同一个分组条件用于 COUNT 与分页查询 → 返回既有用户数据与总数。

编辑或清除用户专属倍率后沿用现有列表刷新流程。删除分组后的配置清理仍遵循 [user-exclusive-group-ratio-deletion.md](./user-exclusive-group-ratio-deletion.md)。下拉框列出当前分组；历史 URL 指向已删除分组时保留可清除的已选名称，按当时存储配置查询，清理完成后自然变为空结果，不自动切换成其他条件。

## API 契约与授权

扩展 `GET /api/user/search`，不新增路由。沿用现有管理员会话、`AdminAuth` 与用户管理权限。管理令牌的 `listAdminTokenUsers` 是独立、受限的查询契约，继续拒绝不支持的筛选参数，不扩大其权限或返回字段。

新增可选参数 `exclusive_ratio_group`：

- 省略或空字符串：不按专属倍率分组限制。
- 非空字符串：精确匹配配置对象中的分组键，区分大小写；不做子串匹配，不将 `%`、`_`、正则或 JSON 路径字符作为通配符。
- 示例：`/api/user/search?group=default&exclusive_ratio_group=vip&p=1&page_size=20`。
- 前端 URL 使用 `exclusive_ratio_group` 数组形式，与现有单选筛选状态一致；API 只发送选中的一个字符串。
- API 保留 `has_exclusive_ratio` 供已有调用者使用；与新参数同时出现时按 AND 组合。因此 `has_exclusive_ratio=false` 加非空分组返回零条，不能忽略任一条件。
- 新页面停止发送旧布尔参数；旧页面 URL 中的 `has_exclusive_ratio` 不再产生隐藏筛选，也不把 `true`/`false` 当成分组名称。

成功响应保持 `{ "success": true, "message": "", "data": { "items": [], "total": 0, "page": 1, "page_size": 20 } }`。既有关键词、角色、状态、排序、排除条件保持原契约。

## 数据模型、查询与索引设计

复用 `users.group_ratios` TEXT 字段；无新表、字段、迁移、持久化设置或配置写入。仅扩展 `UserSearchOptions` 和现有筛选逻辑。

沿用既有配置识别口径：倍率 0 命中；历史 null、负数条目仍属于已存储配置，不代表正在生效。NULL、空串、空对象、JSON null、数组、非法 JSON，以及含非数字且非 null 值的对象不命中。不更改计费解析和列表标记含义。

查询须满足以下实现约束：

1. SQLite 在现有合法性保护内通过 `json_each.key = ? COLLATE BINARY` 匹配对象键；MySQL/PostgreSQL 在现有兼容查询基础上增加精确键条件，避免对损坏 TEXT 做会抛错的 JSON 强制转换。验证版本为 MySQL 5.7.44、PostgreSQL 9.6.24。
2. 已扩展现有扁平数字/null 对象的参数化词法匹配，目标键置于完整对象成员边界。`exclusiveRatioGroupKeyPattern` 同时接受字符本身、短转义及大小写十六进制 Unicode 转义，补充非 BMP 字符的 UTF-16 代理对；所有字面文本经正则转义。三数据库已验证同一套行为用例。
3. 条件在 COUNT 和 LIMIT/OFFSET 之前应用，不加载全部用户到 Go 或浏览器逐个过滤，不产生逐用户查询。
4. 所有用户输入通过绑定参数传入，不拼接 SQL；JSON 编解码遵守 `common` 包约定。

该 TEXT 内容条件不能直接通过普通列索引加速，现有布尔筛选也存在扫描。此次不为动态 JSON 键添加无效索引，不引入需要回填、写入同步的新投影表。已完成真实三数据库、十万行完整用户表的 EXPLAIN 和 COUNT/分页耗时实验，结果见下方。此实验不代表生产混合负载验收；更大数据规模或高频管理查询仍需独立索引投影方案，不能仅凭本次结果宣称不会影响共享数据库。

## 配置、交互及错误处理

无新增配置参数。复用 `DataTablePage.toolbarProps.filters` 和相邻「用户分组」的选项映射，不创建平行筛选组件。保留标题 `t('Exclusive Ratio')`；分组名称原样展示，不作为翻译键。预期无需新增翻译；若实施确需新增提示，按项目规范补齐七语言。

分组加载和搜索失败使用现有 React Query 错误处理，避免将请求失败表现为成功的空列表。数据库错误先记录底层错误，再返回现有国际化错误；不向用户暴露 SQL 或 Go 内部错误。沿用 `MsgInvalidParams`、`MsgDatabaseError` 和现有响应格式。

## Main Chain Impact

不新增任何 relay goroutine 同步执行、异步任务或回调。倍率解析、认证缓存、账单结算和分组清理保持现有实现。本次仅改变管理员读取用户列表的条件。

## Shared Resource Audit

| 资源 | 与 relay 的共享关系及处理 |
| --- | --- |
| `users` 表 | relay 缓存回源也会读取。新增条件只读、不加行锁、不更新数据；必须检查 COUNT 与分页的扫描成本，不能仅凭只读就断言没有共享负载。 |
| 主数据库连接池 | 复用已有搜索请求连接，不新增池或并行扫描；新增参数本身不增加 SQL 次数，实测检查查询占用时间。 |
| Redis 和用户倍率缓存 | 无新增读写、key、失效操作或缓存对象。 |
| 前端查询缓存 | 复用 `['groups']` 查询；用户查询键加入选中的分组，不缓存全量用户。 |
| goroutine / worker pool | 无新增后台任务或 relay worker 消耗。 |

## Concurrency Analysis

在 30k–100k RPM relay 流量下，每个 relay 请求新增 DB 调用、Redis 调用、锁和 goroutine 均为 0。每次管理员搜索沿用现有 COUNT、分页查询以及已有用户资料补充查询；不增加逐用户 SQL。共享数据库的扫描与连接占用仍须实测，不能据此宣称已通过 relay 混合负载测试。

## 测试与验收计划

已先扩展现有测试，再写实现；前端新筛选用例在旧实现上出现预期失败，实施后通过。验证范围如下：

1. 前端筛选测试：下拉列出全部分组名称，不再显示已配置/未配置；选择与清除、组合账户分组、从第三页选择后回到第一页、URL 恢复及旧布尔 URL 不产生隐藏条件；验证实际请求参数。
2. 后端查询：目标键位于首/中/末位置、多分组、零倍率、历史 null/负数、空值及损坏 JSON；`vip` 与 `vip2`、`VIP` 区分；中文、JSON Unicode 转义、引号、反斜线、点号、百分号及正则字符按字面匹配。
3. 分页回归：命中用户在原列表第二页之后仍可返回；COUNT、排序、分页一致；与账户分组/关键词/角色/状态组合后结果准确。
4. 契约回归：未传新参数保持旧行为；原布尔 API 仍兼容；两参数组合语义明确；管理令牌契约不扩大。
5. 真实 SQLite、MySQL、PostgreSQL 实例分别执行受影响 Go 测试和执行计划/耗时探针，记录版本、命令、结果；仅清理本次创建的数据，不清空共享表。没有模型/迁移变更，不做无关 schema 升级。
6. 运行用户模块 Vitest、`bun run typecheck`、`bun run lint`；更新现有 `e2e/user-exclusive-ratio.spec.ts`，验证桌面与手机实际筛选、刷新及分页。
7. 实施完成后回写本设计和旧设计的行为说明，记录真实验证结果及任何未执行项；历史验证记录保留为历史。

## 本轮交付

已完成管理员搜索模型/控制器、前端类型/API、URL schema、隐藏筛选列和下拉选项的修改。`exclusive_ratio_group` 精确匹配专属配置键；旧布尔 API 保留兼容，前端旧 URL 不再产生隐藏条件。分组名称原样展示，已删除但仍在 URL 中选中的分组保持可见可清除。未增加翻译键、依赖、配置、迁移或 relay 执行。

## 实际验证记录（2026-11-15）

- 前端先运行修改后的筛选测试，旧实现失败 5 项（仍显示布尔选项、错误请求参数、旧 URL 隐藏筛选等）；实现后 `bun run test src/features/users`：8 个文件、64 项通过。修正测试的 lint 非空断言后，筛选文件的 7 项重新运行通过。
- `bun run lint` 通过，只有工作区既有的其他文件 warning；本次六个 TS/TSX 文件的 `oxfmt --check` 通过。
- `bun run typecheck` 在本机 Bun 1.3.13 失败，原脚本为 `bun check -b`，报 `Script not found "check"`。使用已安装的类型检查器 `bunx --no-install tsgo -b` 验证通过；未改无关 package.json。
- MySQL 5.7.44 / PostgreSQL 9.6.24：为本次创建独立 Docker 容器和随机测试库，设置 SQL_DSN 指向该库、LOG_SQL_DSN 为空，执行 `go test -p 1 ./model ./controller -run '^TestSearchUsers' -count=1 -v`，搜索、旧布尔兼容、新分组条件、分页及管理令牌拒绝不支持参数全部通过。模型包含 40 组名称/JSON 边界和组合分页用例；测试结束删除本次容器及其匿名卷，不操作其他测试库。
- SQLite 3.50.4：同命令 SQL_DSN 为空，模型筛选测试使用独立内存库，通过；既有需要外部 DB 的控制器集成用例按 harness 跳过，控制器端到端由下面的真实 SQLite 浏览器测试覆盖。
- `cd e2e; bunx playwright test --config user-exclusive-ratio.config.ts`：桌面和手机 2 项通过。构建当前 Go 后端及当前前端生产资源，真实登录、真实 API、独立 SQLite、无网络拦截。49 个测试用户中，25 个有 default 专属倍率，5 个同时有 vip；初始首页只有无配置用户。验证了跨页筛选、零倍率、两页总数、账户分组组合、切换专属分组、刷新恢复与清除。生产构建成功，截图已检查，资源和临时服务自动清理。
- 浏览器报告和截图：`e2e/results/user-exclusive-ratio/`；后端日志和实验脚本：`_local/exclusive-ratio-group/`（均为忽略的本地产物）。

### 十万行查询实验

独立库使用当前完整 `users` 模型，插入 100,000 行，包含完整列及 256 字节设置字段。80% 无专属配置，10% 仅 default，10% 同时有 default/vip/专属。使用同一筛选函数查询 COUNT 与 `ORDER BY id DESC LIMIT 20`，只删除唯一测试分组下的实验行。临时 Go 探针经 `-overlay` 注入，未加入业务代码。复测命令：`python _local/exclusive-ratio-group/run-matrix.py --probe`。

下表单位为毫秒，每格为 COUNT / 首页查询；单次本机测量，不是并发压测或延迟分位数。

| 条件 | SQLite 3.50.4 | MySQL 5.7.44 | PostgreSQL 9.6.24 |
| --- | --- | --- | --- |
| 原已配置条件（20,000 行命中） | 52.4 / 0.5 | 386.8 / 3.2 | 501.2 / 2.6 |
| vip（10,000 行命中） | 59.5 / 0.5 | 494.2 / 3.7 | 849.9 / 3.8 |
| 专属（10,000 行命中） | 56.7 / 0.5 | 511.8 / 3.2 | 817.4 / 3.7 |
| 不存在的分组 | 55.2 / 53.6 | 536.2 / 589.5 | 812.0 / 844.9 |

EXPLAIN：COUNT 在三数据库仍扫描用户表；MySQL 分页使用 PRIMARY，PostgreSQL 使用 users_pkey 反向扫描，SQLite 为表扫描与相关 JSON 虚拟表。无命中时分页也需要扫描，因此最慢场景 COUNT 加分页约 1.66 秒。未改现有搜索的两次查询结构；十万用户且高频筛选时该成本必须纳入共享 DB 容量评估，不宣称通过 30k–100k RPM relay 混合压测。

探针首次种子数据的 aff_code 超过 32 字符，在 MySQL/PostgreSQL 建数据阶段失败；缩短测试标识后，三库查询断言全部通过。实验驱动最后打印 SQLite 微秒符号时触发 Windows 控制台编码错误，已写出的三库 Go 日志均为 PASS，驱动已改用 UTF-8 输出；这不是查询失败。

未访问生产服务器、未执行部署；无 schema 修改，未进行无关的前版 schema 升级演练。Go 验证范围为用户搜索相关测试，并非当前整个工作区的全量回归。
