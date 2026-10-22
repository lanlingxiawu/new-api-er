# 前端保存、导出提示与样式一致性修复

日期：2026-10-22

状态：用户已确认“实施”，代码修复完成并通过相关自动化检查。本文件不表示全站已通过浏览器交互验收。

## 目标和范围

针对用户提供的日志维护、普通用户导出中心、会话与重试截图，修复配置无法保存、保存范围不清、卡片分隔和按钮不一致的问题，并检查其他可达前端页面的同类问题。

检查范围包括系统设置全部 section registry 及其实际表单、共享保存组件、普通用户/管理员/员工导出视图，以及其他功能中同类表单提交和操作区。逐项记录确认的问题与验证结果，不能把静态搜索等同于全站实测。

保留已有主题、设计 token、组件、认证基础模块和业务配置含义。布局与按钮修复复用 SettingsSection、SettingsCard、SettingsForm、SettingsPageFormActions、Button 和现有表格操作组件。上一轮会话默认设置与规则卡片的间距修复保留。

## 初步确认的问题

| 位置 | 代码证据 | 用户影响与修复 |
| --- | --- | --- |
| 运维 / 日志维护 | `maintenance/log-settings-section.tsx` 提交 `log_setting.user_export_enabled`、`log_setting.max_range_days`；`service/settingsaccess/scopes.go` 的 `operations.logs` 未包含这两个键 | 有权限的管理员仍被拒绝为无效参数；读取也会遗漏。补齐该页面的精确配置键登记，保持其他页面和普通用户不能修改 |
| 计费 / 额度设置 | `general/quota-settings-section.tsx` 使用 `quota_setting.trust_quota_usd`、`quota_setting.pre_consume_multiplier`；`billing.quota` 未登记 | 门槛与倍率无法按页面正常读取/保存；补齐相应页面键，不改额度算法 |
| 会话与重试 | `routing-section.tsx` 顶部动作只提交策略编辑器；`runtime-settings.tsx` 的探针识别长度由另一表单保存 | 顶部保存不包含页面底部修改；合并同页提交入口，探针区使用同级分区样式，去掉重复标题 |
| 渠道健康 | `section-registry.tsx` 组合 ChannelHealthSection 与独立 daily runtime 表单 | 同页同样存在两套保存入口；纳入统一页面提交 |
| 运行参数 | `system-tuning/hot-config-sections.tsx` 的 ConfigGroupSection 使用底部默认尺寸 Save，其他页使用顶部小尺寸保存动作 | 网关限流、数据库池、登录会话等页面复用同一设置页动作组件，明确保存中/只读状态 |
| 普通用户导出中心 | `usage-logs/export/index.tsx` 对 self 和管理员无区别展示 throttled_ms 及服务器磁盘错误文案 | 普通用户不再看到服务器让出时长、磁盘空间或扫描实现说明；展示任务状态和粗略剩余时间，失败时给出可执行提示 |
| 导出操作区 | 同一行下载使用 `size=sm`，删除/取消使用默认 `size=icon` | 按现有表格操作规范统一高度、图标尺寸和间距，保留无障碍标签 |

实现中同时修复了日志维护及共享 useSettingsForm 的后台刷新覆盖草稿问题，并覆盖部分保存失败后重试。ConfigGroupSection 保留未提交的本地修改，对其现有整数型配置拒绝空值、小数和非安全整数；其配置项已与后端类型核对，其他表单的小数输入不受影响。网络失败由通用错误处理展示，确认对话框返回的拒绝会被捕获。

已扫描所有系统设置 section registry 的显式 settings 字段与后端登记，以上四个缺失键已人工核实；动态构造键、实际 scope 归属、其他表单及交互状态继续核查，不能据此宣称没有其他问题。

## 数据流与保存生命周期

读取既有按 scope 过滤的配置 → 表单建立服务端基线 → 用户修改本地草稿 → 校验整页所有待保存分区 → 复用现有确认流程 → 提交有变化且有权限的配置 → 根据真实响应更新对应基线 → 完成后刷新关联查询并显示结果。

同一页面仅保留一个主要“保存更改”入口，覆盖页面内可编辑的配置。刷新、测试、下载、清理等即时操作保留各自按钮；清理操作不并入配置保存。

请求策略与探针/每日限额目前使用不同接口，不能直接把额外字段塞进 request_policy：`model.IsRequestPolicyOption` 不接受它们。优先在页面表单层组合现有提交，先整体校验再按有变化的分区提交；不为视觉统一新增事务接口或修改中继配置发布方式。

多个既有请求不是数据库原子事务。若中途失败，已成功部分按响应更新基线，未成功部分保留草稿；不显示整页保存成功，不提前清空全部 dirty 状态，允许重试剩余修改。一次保存期间禁止重复提交，错误提示只出现一次。无变化不发送请求。

只读权限及旧权限映射保持原有边界；无编辑权限的区块不得因页面合并而进入提交载荷。后台刷新只更新未修改字段，不覆盖用户当前编辑。

## 普通用户导出交互

普通用户的进度区域显示：排队、导出中、可下载、失败或已取消。运行中优先显示“预计还需约 N 分钟”或不足一分钟的粗略提示，不展示服务器节点、CPU、磁盘、资源闸门等待或扫描诊断。

现有 API 没有 ETA 字段，`progress` 按扫描的日志时间轴计算，不代表精确工作量。前端仅以既有轮询中的有效进度增量与服务端更新时间间隔计算估算，按粗粒度取整；不增加全表 COUNT、估算查询或轮询频率。首次观察、没有有效增量、进度回退、打包阶段或刚刷新页面时显示“正在估算所需时间”；排队时保留排队状态。完成即显示可下载，不能倒计时到零但任务仍在执行。

估算只是用户等待参考，日志密度不均、排队和打包阶段会使其变化。避免把 created_at 到当前的排队时长直接当作执行吞吐量。任务切换、取消、失败、完成和卸载时释放对应前端观察状态。

失败提示保留：普通用户只看到“导出失败，请重试或缩短时间范围”等可操作内容；不能为了只显示时间而隐藏失败。管理员原有诊断展示可以保留。创建弹窗保留时间范围校验、压缩文件说明与既有下载能力。

## 样式和按钮规则

- 设置页动作统一复用 SettingsPageFormActions：保存位置、尺寸、图标、保存中文字及禁用状态一致。
- 独立设置分区使用现有容器与 `gap-4` 等间距；检查 fieldset 嵌套后间距丢失、重复标题、边框粘连和内容溢出。
- 表格行操作复用同模块的紧凑操作区，下载与图标按钮高度一致；删除沿用现有次要危险操作规范。
- 弹窗按钮使用既有 Dialog footer；布局随窄屏换行，不新增硬编码颜色或平行 CSS。
- 不对全部 space-y、Button 或 fieldset 做机械替换；逐个确认实际渲染与语义。

## API 契约和权限

沿用 `GET /api/option/?scope=...`、`PUT /api/option/` 的 `{scope,key,value}`、`PUT /api/option/group` 的 `{scope,module,values}`、请求策略现有 GET/PATCH 接口。

沿用管理员认证和系统设置 scope 的查看/编辑校验。只补 `operations.logs` 与 `billing.quota` 各自所需的两个键；不扩大其他 scope、根用户旁路或通配前缀。

导出沿用普通用户 `/api/log/self/export/jobs` 与管理员 `/api/log/export/jobs` 接口及归属检查。本次预计时间显示不新增接口，不修改导出内容/授权，不把普通用户请求切到管理员接口。

## 数据模型与配置

无新增表、字段、索引、迁移、用户配置参数或数据库协议改动。复用现有 options 存储及设置模块。普通用户时间估算保存在页面内存，不持久化为服务器任务的真实完成时间。

## 错误处理和国际化

前端校验定位到具体输入项，服务端失败走既有错误处理并保留草稿；多分区提交的部分成功不得伪装为全部成功。普通用户导出失败使用经过角色区分的文案，不能展示 Go 错误或要求用户清理服务器磁盘。

新增文案同步 en、zh、zh-TW、fr、ja、ru、vi，按 i18n-translate 技能执行，并保持翻译改动范围可审查。后端如需新增用户错误键，同步 en/zh。

## Main Chain Impact

本次只修复管理配置的读写登记及前端行为。没有新的代码在 relay goroutine 执行，没有新的中继中间件、同步 I/O、重试策略或计费计算变更。

额度两个参数已存在且已被计费读取，本次让授权的设置页能够保存它们；值的合法性必须沿用现有校验，不更改配置含义。探针、重试、每日限额继续使用已有设置保存和发布流程。

## Shared Resource Audit

- options 表、配置管理器及原有快照：仅管理写入复用，不新增锁和查询；继承既有发布逻辑。
- 日志导出任务 Redis 和文件：前端继续既有列表轮询/下载/取消，不新增服务端存储、任务或文件句柄。
- 主库/日志库/Redis 连接池、后台导出工作池：不新增连接或调整容量；不会因 ETA 增加扫描。
- 前端 Query 缓存与草稿状态：保持按 scope、任务和角色隔离，成功后按实际保存范围刷新。

## Concurrency Analysis

30k/100k RPM 中继每请求新增 DB/Redis 调用、锁、协程均为零。仅管理保存交互和浏览器本地显示发生变化。此结论为变更范围分析，不声称完成容量压测。

## 验证计划（实施前先补业务回归）

1. scope 测试：四个缺失键在正确页面读写允许，在其他页面拒绝；覆盖只读管理员，保持普通用户无设置写权限。
2. 表单测试：日志开关/时间范围分别及同时修改；额度门槛/倍率按契约提交；刷新和失败保留草稿；连续点击只有一次提交；无修改不发请求。
3. 页面提交测试：单独修改探针长度、每日限额以及同时修改其他字段，顶部保存覆盖正确范围；无权限区块不提交；第二个请求失败时不丢未保存字段。
4. 导出测试：普通用户不显示服务器提示；估算初始、进度增长、停滞、回退、页面重载、完成、失败、取消；管理员诊断保持可见；下载/删除/取消行为不回归。
5. 类型检查、改动文件 lint、相关 Vitest 和后端权限测试、生产构建。
6. 浏览器验收：桌面/窄屏、浅色/深色、普通用户/管理员/只读管理员；重点核对截图三页及复用 ConfigGroupSection 的页面。
7. 本计划不改 DB 行为；如实施发现必须改事务、ORM、模型或迁移，则先补设计并执行真实 SQLite/MySQL/PostgreSQL 验证矩阵。不能用单一数据库或构建替代。

## 交付记录

### 2026-10-22 分组重试保存 403 追查

分组重试页面实际提交 `module=group_retry_policy`，字段为 `times/status_enabled/status_rules/guard_enabled/guard_rules`。页面 scope 的 GroupKeys 允许该组合请求，但 UpdateOptionGroup 随后把每个字段拼成 `group_retry_policy.*` 再调用 AllowsSystemSettingsOption；真实 OptionKeys 是 `GroupRetryTimes`、`group_retry_status_setting.*`、`group_retry_guard_setting.*`。因此带 scope 的 root 和已授权管理员都在第二层检查被错误返回空 403。

修复在控制器将这五个组合请求字段映射为真实配置键，再执行现有逐字段权限检查。保留页面编辑权限、GroupKeys 白名单和旧权限交集；不增加虚假 OptionKeys，不绕过检查。测试用完整页面请求及无效次数在持久化之前停止，断言正确 scope 到达参数校验，错误 scope、未知字段及内部 revision 被拒绝。无需更改请求契约、存储事务、缓存、配置发布或 relay 路径；共享资源和 Main Chain Impact 与本文原结论一致。

### 扩大管理设置审计

- 导出运行中的权限注册定义，逐项核对 62 个 scope、95 个分组字段登记。仅 `group_retry_policy` 的五个别名不符合通用 `module.field` 形式；已由控制器映射修复。新增遍历注册表的回归测试，防止其他配置模块的 GroupKeys 与 OptionKeys 脱节。
- 对照全部 settings section registry 的字段和实际编辑器，新增发现 `site.system-info` 缺少 `general_setting.docs_link`，造成文档链接无法正确读取/保存；补充该页精确键，保留原有 `billing.quota` 兼容登记。分组价格页传入工具价格的属性属于隐藏 tab 的复用参数，不因静态扫描误报而扩大写权限。
- 核查 `/api/option/`、`/group`、`/request_policy`、运行状态、Passkey、支付、模型价格等调用与路由门槛。任务插件无 scope 调用对应前后端均 root-only 的页面，保留既有边界。
- `useResetForm` 订阅 dirtyFields，后台刷新保留已修改字段；额外将 26 处设置表单同步逻辑接入该 hook，加上原有 7 个调用者，覆盖认证配置、模型设置、安全限制、内容、导航、日志导出/查询、结算保护、账本及性能等页面。保存成功后的显式 reset 和打开不同对象的编辑弹窗仍保留原语义。新增普通字段及嵌套字段刷新回归。
- 顶部 SettingsPageFormActions 与原生 SettingsForm 提交捕获异步失败，复用可去重的 handleServerError；保留调用方的失败语义，不把失败当作成功。新增点击保存及表单提交失败后保留输入、允许重试的测试。
- 审计是代码契约及自动化回归，不等同于所有管理页面的线上账号、真实数据库写入、浏览器视觉验收。

扩大验证：`go test ./controller -run 'Test(GroupRetry|OptionScope|SettingsWriteScope)' -count=1`、`go test ./middleware ./service/settingsaccess -count=1` 通过。先复现 root/已授权管理员组合请求的 403，再验证修复通过；未知字段、内部版本号和错误 scope 继续拒绝。文档链接缺项和刷新覆盖草稿也先通过失败测试复现。

尝试 `go test ./controller ./middleware ./service/settingsaccess -count=1`：后两包通过；controller 全量未通过。环境没有 SQL_DSN，既有数据库相关用例出现缺表/唯一约束错误，最终 `TestAdminAssignCustomerToEmployee_EmployeeNotFound` 因 nil DB panic 中断；没有修改这些无关测试来掩盖问题。本次权限映射回归在持久化前停止，不依赖数据库，不声称已验证真实写入。

设置页与导出专项 34 文件/314 测试通过；随后增加嵌套字段用例并扩大全前端测试。`bun run typecheck`、改动文件 lint、`bun run lint` 和生产构建通过。全量 lint 有 6 条既有 warning，分布在 setup-guide 测试、Vitest 配置及 footer，本次改动文件无 lint 报错。

最终 `bun run test` 全前端通过：217 个测试文件、2459 项测试，耗时约 195 秒。测试环境输出 scrollTo/canvas 未实现的提示，未造成失败。再次运行类型检查和改动文件 lint 通过。33 个 useResetForm 调用页面已确认。以上为部署前本地验证；香港测试服务器部署记录见文末。

已完成以下修改：

- 日志维护、额度设置：补齐各自 scope 下的四个精确配置键。服务端读取和写入恢复正常授权，不扩大其他页面权限。权限审查遵循 [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) 的默认拒绝和逐请求授权原则。
- 会话与重试、渠道健康：探针和每日限额分别并入所在页面表单，一次顶部保存提交各自已有接口；全部校验通过后才开始保存。记录成功部分，失败草稿可重试。保留已有页面确认机制，不增加浏览器原生提示。
- 日志维护、运行参数页：统一使用 SettingsPageFormActions；保存期间禁用控件，日志多键成功只提示一次。运行参数使用原有确认流程，拒绝无效整数并保留失败草稿。
- 会话页面：fieldsets 使用 flex/gap 分隔默认设置、规则、重试预算、探针卡片；去除重复标题和底部独立保存按钮。
- 普通用户导出：隐藏扫描数、服务器让出时间及磁盘错误细节；运行任务显示等待估算、不到一分钟或约 N 分钟，保留任务状态和可操作的失败提示。管理员诊断保持可见。统一创建、刷新、下载和删除按钮尺寸。
- 七种语言新增三个剩余时间提示，执行 i18n:sync；没有新增配置、接口、数据库行为或中继逻辑。五个受保护认证模块没有修改。

价格巡检属于独立整理和提交的功能，最终行为与后续交互纠正以[表达式改价设计](price-monitor-expression-first-editing.md)为准；本次设置保存与导出修复不包含价格表差异行的移除。

验证记录：

- `go test ./service/settingsaccess -count=1` 通过；新增四键权限回归先验证失败，再修复通过。
- `bun run test src/features/system-settings src/features/usage-logs/components/__tests__/export-dialog.test.tsx src/features/usage-logs/export/remaining-time.test.ts`：32 个文件、310 个测试通过。
- 随后增加运行参数刷新及网络失败重试用例，先复现草稿被覆盖，再修复；`bun run test src/features/system-settings/system-tuning`：3 个文件、10 个测试通过（新增 1 个测试）。
- `bun run typecheck`、改动 TS/TSX 文件 oxlint、oxfmt、`git diff --check` 通过；`bun run build` 通过。
- 测试覆盖日志部分失败重试、后台刷新、探针顶部保存和无修改再次保存、每日限额边界、运行参数整数验证、导出角色差异及时间估算状态。未将静态审计描述为全站端到端验收。
- 当前 CUA 没有可用浏览器，创建 in-app browser 返回不可用，因此未完成真实浏览器中的窄屏、主题及线上账号验收。
- 无数据库结构、ORM、SQL、事务变更，未运行数据库迁移矩阵。

## 本地提交整理（2026-10-22）

按功能分为设置保存与权限修复、普通用户导出进度提示两笔提交；仅包含源码、回归测试、相关设计文档和七语言新增文案。截图、压测日志、可执行文件及临时数据库不纳入提交，翻译文件剔除无关键排序差异。

提交前复核：设置页与导出相关 33 文件 / 323 用例通过；`go test ./controller -run 'Test(GroupRetry|OptionScope|SettingsWriteScope)' -count=1`、`go test ./middleware ./service/settingsaccess -count=1` 通过；全量 lint 无错误（6 条既有警告），类型检查与差异检查通过。保留上述浏览器验收与 controller 全量测试的范围限制。

## 香港测试服务器部署（2026-10-22）

用户授权后，已将本次全部修复部署至 `43.251.102.17`。版本为 `v1.0.0-rc.44-330-gc1c43b3e9-hk-settings-20261022`，使用 Go 1.26.10 构建 Linux/amd64 程序并嵌入前端生产资源。程序 SHA-256：`ad506718521ac40bc291d0ab4368862683600ee6db9e4c3b1b68edfa54c06d66`。

原程序和配置备份于 `/root/deploy-settings-20261022T0541`。确认上传哈希后，向旧进程发送 SIGTERM 并等待退出，再替换 `/root/new-api`，按原环境、工作目录和启动参数显式启动。部署脚本包含健康检查失败时恢复原程序的处理，本次无需回滚。新 PID 为 `2510867`，配置文件哈希保持不变。

部署后验证：应用 3002、Nginx 3000、外网 `http://43.251.102.17:3000/api/status` 均返回 200/success 和新版本；外网首页及六个入口 JS/CSS 资源均返回 200，资源 SHA-256 与本地构建一致。只有一个应用进程，启动日志没有 panic/fatal；未登录访问设置读取和分组写入接口返回 401。未使用后台账号进行线上表单保存或真实浏览器视觉验收，因此接口权限修复的业务验证仍以本地回归测试为依据。
