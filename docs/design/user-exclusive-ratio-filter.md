# 用户列表专属倍率筛选

状态：用户已确认，2026-10-26 实施。

## 目标与范围

用户需要从用户管理列表筛出截图中带「专属倍率」标记的用户。现有「用户分组」仅筛选 `users.group`，专属倍率单独存放在 `users.group_ratios`；两者不能混为一个分组名称。

在「用户分组」旁增加「专属倍率」单选筛选：已配置、未配置；不选择表示全部。支持与关键词、状态、角色、账户分组组合查询。本次不增加按专属配置中的具体分组名称查询。

复用 `UsersTable` 的 `DataTablePage.toolbarProps.filters`、URL 筛选状态和现有加载/空状态。变更筛选回到第一页，清除筛选恢复全部；刷新和浏览器前进后退保留选择。筛选依据配置是否存在，不依据倍率总开关是否开启，也不要求专属配置的分组与账户所属分组相同。倍率 0 仍是已配置。

## 现状与关联设计

- `web/src/features/users/components/users-columns.tsx` 使用 `parseGroupRatioRows(group_ratios).length > 0` 展示标记。
- `web/src/features/users/components/users-table.tsx` 在存在筛选时请求搜索接口，并由服务端分页。
- `controller.SearchUsers` → `model.SearchUsers` 目前没有专属倍率条件。
- 分组删除和专属倍率清理遵循 `user-exclusive-group-ratio-deletion.md`；本功能不改变其生命周期。

## 数据流与 API 契约

管理员选择条件 → URL 状态与 React Query 查询键更新 → `GET /api/user/search` → controller 解析条件 → model 同时对总数和分页查询应用条件 → 返回既有分页响应。

扩展现有管理员接口，不新增路由或修改授权：沿用 `router/api-router.go` 的管理员路由组及既有权限控制。

新增可选参数 `has_exclusive_ratio=true|false`；省略或空字符串表示不筛选。非法非空值返回国际化业务错误，HTTP 200，`{ "success": false, "message": "..." }`。既有关键词、分组、角色、状态、排序和分页参数保持原有契约。

成功仍返回 `{ "success": true, "message": "", "data": { "items": [...], "total": 0, "page": 1, "page_size": 20 } }`，分页字段以现有 `PageInfo` 为准。条件必须在数据库分页之前生效，不能在浏览器仅过滤当前页。其他 `SearchUsers` 调用者未传参数时保持原行为。

## 数据模型与查询方案

复用 `users.group_ratios`，不新增表、列或迁移，不写用户数据。非空的数字/null 值对象表示已配置；NULL、空串、纯空白、`null` 和空对象（含格式化空白）表示未配置。历史 null/负数条目仍属于已存储配置，本筛选不判断是否生效。数组、非数字值（字符串、布尔值、嵌套对象）及非法 JSON 不算配置；列表标记使用相同口径，编辑器与计费解析保持原样。

已先编写配置识别、分页和前端筛选测试，再实现查询。SQLite 用 CASE + json_valid 保护 json_type/json_each；MySQL/PostgreSQL 对固定的扁平数字/null 对象格式使用参数化 JSON 词法正则，无 JSON 强制转换，因此损坏文本不会触发查询异常。MySQL 显式使用大小写敏感排序规则，避免接受非法的 NULL 字面量。常见 NULL、空串、`{}` 和 `null` 在进入正则前直接判为未配置，降低旧版 PostgreSQL 的空配置校验开销。相关接口见 [SQLite JSON 文档](https://www.sqlite.org/json1.html)、[MySQL 5.7 模式匹配](https://dev.mysql.com/doc/mysql-tutorial-excerpt/5.7/en/pattern-matching.html)、[PostgreSQL POSIX 模式匹配](https://www.postgresql.org/docs/9.6/functions-matching.html)。

采用既有管理员查询内的条件扩展，不引入缓存或后台全表加载。`group_ratios` 是 TEXT，普通索引无法直接加速 JSON 内容条件；此次不新增索引。EXPLAIN 确认该条件的 COUNT 仍需要数据库扫描，应用只接收分页结果，不读取全部用户。1 万条配置投影记录（1% 有配置）的独立表实验中，COUNT + 20 条分页的实测约为 SQLite 1.5–1.6 ms、MySQL 9.6–11.0 ms、PostgreSQL 4.3–9.0 ms。SQLite 使用表扫描和 JSON 虚拟表，MySQL 为 ALL / Using where，PostgreSQL 为 Seq Scan。该实验不等同于生产全宽用户表、百万用户规模或 relay 并发压测；大规模配置查询仍可能需要独立索引投影。本次未做此类规模扩展或增加主链写入。

## 配置、错误与交互

无新增配置项。前端复用现有筛选控件、请求加载反馈、空状态和错误处理。复用 Configured / Not configured，修正 Exclusive Ratio 的七语言词条（原先非中文语言也显示中文）。后端复用现有 MsgInvalidParams、MsgDatabaseError，不新增错误词条；仓库实际资源为 `i18n/locales/*.yaml`，与旧规范中的 JSON 路径不同。查询失败记录底层错误，返回国际化消息，不暴露 SQL 或 Go 错误。

删除/修改用户专属倍率后，沿用列表已有刷新机制重新查询；删除分组的后台清理尚未完成时，列表反映当时存储的配置，不宣称这些配置正在计费中生效。

## Main Chain Impact

没有代码在 relay goroutine 上新增执行，也不新增 relay 异步任务。改动仅在管理员列表的读路径；不更改计费、专属倍率解析缓存、用户认证缓存或分组清理逻辑。

## Shared Resource Audit

- `users` 表：管理员筛选和 relay 缓存回源共享。只读查询、不加行锁、不更新行；仍需通过执行计划和实际耗时检查额外扫描的影响。
- 主数据库连接池：复用现有用户搜索连接，不新增连接池、逐用户查询或并行扫描；不得将长时间扫描隐藏在管理请求中。
- Redis：不新增访问，不修改任何 key 或命名空间。
- 内存及 worker pool：仅既有分页结果和前端查询缓存；不创建用户全量缓存，不占用 relay worker pool。

在 30k–100k RPM relay 流量下，本功能的每个 relay 请求新增 DB/Redis 调用、锁和 goroutine 均为 0；管理员查询的共享数据库负载仍须单独验证。

## 测试与验收

先写测试，再实施：

1. 后端覆盖省略/true/false/非法参数，NULL、空值、空对象、格式化空对象、一个/多个专属倍率及倍率 0；覆盖历史异常 JSON 的既定处理。
2. 覆盖关键词、账户分组、角色、状态组合，匹配用户位于原第二页时依然可筛出，total 与分页结果一致；不筛选时兼容已有调用者。
3. 扩展用户列表现有筛选测试，覆盖选择、清除、翻页归一、URL 恢复、请求参数以及筛选失败反馈。
4. 在真实 SQLite、MySQL、PostgreSQL 实例运行查询测试并记录版本、命令、EXPLAIN 和结果。仅清理测试创建的数据；不可清空共享数据库。任何不可执行项如实记录，不以单一数据库测试替代完整验证。
5. 运行受影响 Go 包测试、前端相关 Vitest、`bun run typecheck` 和 `bun run lint`。不修改上游专属认证基础文件。

## 实际验证记录

### 真实前端页面集成测试（2026-10-26）

- 用例：`e2e/user-exclusive-ratio.spec.ts`，独立配置 `e2e/user-exclusive-ratio.config.ts`。从 `e2e/` 执行 `bunx playwright test --config user-exclusive-ratio.config.ts`；依赖本机 Go、Bun、Chrome，以及已安装的 `web/` 和 `e2e/` 依赖。
- 全链路：编译当前 Go 后端、构建当前前端生产资源、通过 Rsbuild preview 代理真实 API，使用独立临时 SQLite。浏览器从登录表单登录，不注入认证状态、不拦截或模拟接口。结束自动关闭服务、删除本次临时数据库和构建资源，不覆盖工作区 `web/dist`。
- 数据：25 个已配置、24 个未配置用户，涵盖倍率 0、NULL、空串、空对象和格式化空对象；default/vip 两个账户分组。未筛选首页全部为未配置用户，用于检出“仅过滤当前页”的错误。
- 桌面：实际点击筛选、翻页、组合用户分组、刷新、切换未配置、清除；逐行核对用户名顺序、行数、总数及专属倍率标记，确认第二页切换分组后返回首页，无页面脚本异常。
- 手机：390×844 卡片布局，实际选择已配置/未配置，核对卡片数据、总数、刷新后的筛选状态。
- 结果：Chrome 桌面和手机 2 项均通过；截图已人工检查。报告与截图在 `e2e/results/user-exclusive-ratio/`（被 Git 忽略），失败时另保存 trace 和截图。早期开发服务器的调试浮层遮挡分页按钮，最终测试改用生产构建预览，保留正常点击检查，没有使用强制点击绕过遮挡。
- 本轮页面集成使用 SQLite；MySQL/PostgreSQL 的模型与控制器集成结果见下方，未宣称浏览器用例覆盖三种数据库。

### 模型、控制器与组件验证

- SQLite 3.50.4：`go test ./model -run 'TestSearchUsers|TestNewUserSortOptions|TestUserSortOptions' -count=1`，新功能查询测试在独立内存数据库运行通过；未配置外部数据库的其他集成用例按既有 harness 跳过。
- MySQL 5.7.44 / PostgreSQL 9.6.24：分别设置 SQL_DSN 指向专用测试容器，执行 `go test ./model -run '^TestSearchUsersExclusiveRatio$' -count=1 -v`，22 组输入、互补筛选、分页总数及组合条件通过。MySQL 测试库使用 utf8mb4；首轮空容器默认 latin1 导致既有其他模型的迁移错误，调整测试库后重跑通过。
- EXPLAIN/耗时：临时 `TestExclusiveRatioQueryProbe` 在三个引擎上创建唯一命名的投影表，插入 1 万行（每 100 行 1 条 `{"vip":0.5}`），测量筛选 true/false 的 COUNT 和倒序 LIMIT 20；实验表均在 cleanup 删除，临时探针代码已移除。
- `bun run test src/features/users`：6 个测试文件、53 项测试通过，覆盖筛选切换/清除、组合 URL 恢复、回到首页及配置标记口径。
- `bun run typecheck`、`bun run lint` 通过；lint 有本次改动范围外的既有 warning。
- Go 整包回归：新建 utf8mb4 MySQL 专用主库、PostgreSQL 专用日志库后，`go test -p 1 ./model ./controller` 全部通过（model 56.477 s，controller 219.093 s）。最新控制器参数契约测试另用 PostgreSQL 执行 `go test ./controller -run '^TestSearchUsers' -count=1` 通过。首次整包验证的 latin1 残留表导致员工/配置用例失败，换用从创建起即为 utf8mb4 的独立测试库后消除；未修改这些无关业务代码。
- 无数据模型/迁移改动，因此未做前版 schema 升级或服务器迁移演练；没有访问生产服务器。
