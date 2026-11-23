# 全站界面、操作组件与布局统一

日期：2026-11-20。状态：**已实施，已完成本地验证，未部署**。

## Goals and scope

按操作的实际含义统一组件、布局和反馈，覆盖整个 `web/` 前端。用户提供的用户编辑、管理员权限、导出中心、使用日志详情和性能设置截图是问题示例，不是范围边界。

本轮追加要求：日志详情中标出的“上游交换”普通用户不可见，管理员和员工可见；同时优化整个详情弹窗。按截图内框将该可见性要求作用于上游交换信息，普通用户继续查看自己的用量、费用和公开错误信息。性能设置页纳入同一轮重点重排，原全站范围继续保留。

目标是让用户能稳定判断：哪里切换页面，哪里选择条件，哪些修改需要保存，哪些操作立即执行，以及失败后如何继续。相同操作采用相同组件和排列方式；不同业务的权限、金额单位、提交契约和分页方式继续由各自业务模块负责。

实施期间用户进一步确认：同类页签统一采用监控设置截图中的圆角分段 Tabs。以现有 `TabsList` 默认样式为基线，替换用户、渠道、插件、日志和性能等入口的线形样式；移除局部不同的页签背景覆盖。窄屏保留必要的横向滚动或已有的两列排列，面板状态和权限不变。本次明确指令替代本文初稿中的线形页签方案。

包含管理后台、普通用户和员工工作台、系统设置、公共页面、登录与账号安全、聊天外围界面。优先复用现有 Base UI 组件、业务封装和主题。保留首页的品牌版式、图表的业务结构、聊天的消息布局、初始化向导的步骤语义。外部聊天 iframe 内部页面不属于本仓库，检查其宿主、加载和错误界面。

此次不新增后端能力、权限资源、计费规则、数据库结构或用户配置。不恢复代码中已注释的功能。实现中若发现需要改变后端契约，先单独记录并补充设计，不以模拟数据替代后台能力。

## 调查方法与覆盖边界

本轮对工作区进行全量文件和组件调用盘点：**78 个路由文件、39 个 feature 目录、899 个非测试 TSX 文件**；系统设置的 9 份 section registry 共登记 **57 个分区**。文件数包含布局、跳转和公共组件，不等于页面数量。组件候选搜索排除了测试文件，并复查块注释，避免将已停用的客户额度调整代码当作现有操作。

人工阅读了页面骨架、共享组件实现、各类页面入口，以及用户编辑/权限、令牌权限选择、导出列表/筛选/创建、设置保存、员工选择器、原生确认框等具体流程及关联测试。用户编辑和导出追踪到路由鉴权、控制器和 API 载荷；追加核查日志三类访问范围、元数据裁剪、员工详情调用、上游计时采集边界，以及性能配置、统计、清理、GC 和 pprof 的实际请求与权限。未将文件名、旧文档或搜索命中直接当作行为证据。

已完成全量静态盘点、重点流程实现与回归。浏览器使用隔离的 API fixture 检查用户编辑、权限、日志详情、导出中心和性能页，角色和异常状态通过组件/API 契约测试覆盖。覆盖清单区分源代码检查与实际浏览器检查，不把静态盘点等同于逐页真实后端验收；本轮未部署。

参考了现有设计：`user-edit-layout.md`、`export-filter-component-consistency.md`、`frontend-settings-save-and-export-consistency.md`、`monitor-page-layout-unification.md`、`relay-local-wait-optimization.md`、`body-cache-settings-guidance.md`。当前代码优先；保留已有正确交互。

## Existing Logic and Related-Path Findings

以下为实施前调查时核实的代码基线和处理依据，行号对应当时版本；最终实现和验证结果见文末。

| 编号 | 代码证据与现状 | 影响与确定的处理方式 |
| --- | --- | --- |
| U01 | `components/ui/checkbox.tsx:31` 使用 `size-4 rounded-sm`；`styles/theme.css:88` 的小圆角为基础圆角的 0.6 倍，默认基础圆角是 1rem | 默认圆角已大于 16px 控件半边，复选框呈圆形，容易与 Radio 混淆。仅在 Checkbox 内限制圆角上限，继续响应直角主题；不改全站主题圆角偏好 |
| U02 | Checkbox 的 Indicator 固定显示 Tick；`users-columns.tsx:61` 等表头实际传入 `indeterminate` | 部分选择缺少独立图形。统一未选、勾选、部分选择三态，部分选择显示短横线，保留 Base UI 的键盘和 ARIA 行为 |
| U03 | `users-mutate-drawer.tsx:529` 使用 `sm:max-w-4xl`，基本资料全部单列；Tabs 顶部固定，Panel 已 `keepMounted`；`channel-configuration.tsx:115` 有同类线形 Tabs | 保留五类任务页签和跨页签草稿，统一内容宽度、导航间距及滚动区域。基本信息用适度双列，去掉与当前页签重复的标题，长说明和备注占整行 |
| U04 | `admin-permissions-editor.tsx:156` 使用 Checkbox + Field；系统设置权限内再嵌套 Accordion；`components/permission-matrix.tsx:204` 使用 Toggle，供令牌创建/编辑调用 | 同样的授权多选出现两套外观。统一权限选项行到 Checkbox + 标签 + 说明；复用现有权限模块中的选项渲染。管理员默认授权/view 依赖与令牌 scopes 转换继续分开处理 |
| U05 | `users-mutate-drawer.tsx:501` 的 `refreshUserData()` 执行整表 `form.reset`；`UserQuotaDialog` 的成功回调在 1246 行使用它；额度本身通过 `/api/user/manage` 独立提交 | 调整余额后会覆盖当前资料、权限等未保存输入。额度成功仅刷新余额和列表；保存返回 `committed` 时仍按现有契约重新读取已提交数据，两种回读必须区别处理 |
| U06 | 用户加载 effect 在 `users-mutate-drawer.tsx:306` 直接 `getUser().then(form.reset)`，没有目标切换后的过期响应检查；提交按钮主要依赖 `isSubmitting` | 快速切换目标或加载失败时缺少明确的编辑边界。增加当前目标的加载/失败状态和过期响应隔离；数据未加载成功时禁止编辑提交，失败提供重试 |
| U07 | `usage-logs/index.tsx:235` 把 all/self/upstream 放在同一 Tabs 中；导出时又增加 common/export Tabs；`export/index.tsx:112` 再放 usage/quota ToggleGroup，操作区在子列表中另起一行 | 数据范围、路由入口和导出类型混在一起。范围用分段选择；上游日志和返回使用日志用路由链接；导出类型与列表操作放同一工具栏，消除连续堆叠的切换条 |
| U08 | `LogExportCenter` 的 source 只在本地 state 中；`ExportTaskList` 已有 responsive 能力，但此调用未启用；列表用游标和活动任务轮询 | 刷新后导出类型回到 usage，窄屏未采用已有适配。将导出类型写入校验后的 URL 参数，启用已有响应式列表；维持游标、权限隔离和原轮询频率 |
| U09 | `user-quota-dialog.tsx:137` 用 Button 循环模拟模式单选；`new-export-sheet.tsx:903` 用三个 Button 选择 detail/summary/both | 表单参数应有单选语义。统一为 RadioGroup，保持原枚举值、模式切换和校验，不把选择动作当成保存/执行按钮 |
| U10 | `employees/index.tsx:2582` 的员工搜索框手写绝对定位弹层、`z-[120]` 和 `li onMouseDown` 选项，查询由既有 infinite query 负责 | 选择行为依赖鼠标，键盘及弹层组合与其他搜索选择不同。用现有 ComboboxInput 的受控值、搜索、分页页脚和清除能力替换外壳，保留员工对象和分页请求 |
| U11 | `employees/components/performance-adjust-dialog.tsx:171,184` 有原生 Checkbox/Select；`node-pool/index.tsx:516` 有原生分页 Select | 同类控件仍使用浏览器默认外观。改为项目 Checkbox/Select；继续提交原月份与 pageSize 值。节点表先复用 StaticDataTable 的渲染，保留本地分页算法 |
| U12 | `waffo-pancake-settings-section.tsx:282`、`price-monitor-price-form.tsx:1306` 仍用 `window.confirm`；共享 `ConfirmDialog` 已支持危险态、等待态和自定义内容 | 两处改为共享确认弹窗。前者确认后才创建产品；后者只更新恢复默认的本地草稿，仍须保存。不能统一成“确认即保存” |
| U13 | `ConfirmDialog` 在 isLoading 时禁用按钮但不渲染统一进行中反馈；`UserQuotaDialog` 的 Enter 直接调用 handleConfirm，函数未检查 loading | 补齐共享确认的进行中反馈；提交入口自身防重入，覆盖键盘和按钮，失败保持可重试。金额调整不能因为连续 Enter 发出多个写请求 |
| U14 | `components/dialog.tsx`、`drawer-layout.ts`、DataTablePage、StaticDataTable、CopyButton、EmptyState/ErrorState 已存在；客户管理等模块仍直接拼基础 Dialog、label 和页脚 | 复用行为封装，统一标题、滚动正文、页脚、复制反馈与错误状态。无必要不新增平行 Dialog/Table/Select 体系 |
| U15 | SettingsPage / SettingsForm / SettingsPageFormActions 已负责 scope、只读边界和动作区；useSettingsForm 有草稿保留逻辑；模型编辑已用 ConfirmDialog 防止误丢草稿 | 以这些实现为复用基线。保留页面级保存、部分成功、只读锁定和已保存基线，不为外观统一重写配置持久化 |
| U16 | `usage-logs-table.tsx:105` 给员工 common 日志设置 `showAdminFields=true`，但 `common-logs-columns.tsx:894` 将 `canUseAdminActions=false` 传为详情 `isAdmin`；`log-detail-body.tsx:1095` 用这个标记拦住上游交换。员工接口 `model/log.go:935` 保留该数据，普通用户投影 `model/log_other.go:328` 删除整个 admin_info | 员工缺失的是前端只读展示。单独传递 `canViewUpstreamExchange`，由已有 showAdminFields 决定；不把员工 isAdmin 改成 true，不扩大上游查询、查同类、内部费用或 Root 原始诊断能力 |
| U17 | `relay/common/upstream_timing.go:10` 的四个字段都是可选指针，0 有效；`relay/channel/api_request.go:513` 在 diagnosticClient.Do 返回后记录 end_ms；`service/log_info_generate.go:97` 写入 admin_info.upstream_exchange | 四个数是相对整个请求开始的时间点，不能相加、直接当四段耗时，end_ms 也不等于完整响应体/流结束。改为每次尝试的里程碑布局，缺失明确标记，解释只出现一次；不改采集与日志写入 |
| U18 | `details-dialog.tsx:113` 普通日志宽度仅 sm:max-w-lg，分层计费扩至 4xl/5xl；`upstream-log-details-dialog.tsx` 又用 2xl；主体含约 30 个分区，并复用 `LOG_DETAIL_ROW` 对齐本站/上游 | 常规详情统一适度宽度、摘要及任务分区；复杂计费和上游比较保留所需空间。共享主体、分区分类和比较行表，避免三种入口各做一套 |
| U19 | `performance-section.tsx` 顺序为长配置表单、Root pprof、监控；页面保存通过 portal 固定在标题处。`profiling-controls.tsx:183` 将采样秒数与 8 类 profile 加 goroutine 文本下载横排 | 改为运行概况、缓存与保护、诊断与维护三个页内面板。只在配置面板显示保存；pprof 类型用 Select，持续采样才出现秒数输入，单一下载动作 |
| U20 | 性能统计已有 statsLoading/statsFailed/receivedAt 和过期请求序号，但主监控只在 stats 存在时显示，错误反馈主要在 CacheGuidance；resetStats/forceGC 没有 pending/防重入。pprof 读取失败会落到 enabled=false 的默认展示 | 首次加载、失败、旧快照和缺失指标明确区分；保留旧数据并标识更新时间。即时维护动作补齐等待、防重入和真实结果反馈；未知 pprof 状态不能显示成“已关闭” |
| U21 | `SettingsPageFrame` 默认用 disabled fieldset 锁整个只读页面，包含刷新与将新增的 Tabs；已有 `interactiveReadOnlySections` 可局部豁免，operations 尚未使用。性能写接口由 operations.performance edit 权限控制，pprof 状态和文件仍是 RootAuth | 仅把 performance 加入现有可交互只读分区列表；在性能组件内将配置字段锁入独立 fieldset，并给全部写动作显式 canEdit 校验。允许查看者切换和刷新，不扩大编辑或 pprof 权限 |
| U22 | `common/disk_cache_config.go:155` 的 reset 只清零内存/磁盘命中次数；`common/disk_cache.go:137` 只重试删除本进程已关闭且超过 10 分钟的文件；`controller/performance.go:190` 的 ForceGC 调用 runtime.GC；统计接口仍通过 getDiskCacheInfo 扫描目录 | “重置统计”改为明确的“重置缓存命中统计”，不暗示会清空 Redis/GC/使用量。维护按钮放入独立面板，保留清理确认，补充重置确认和动作状态；不添加自动刷新、自动 GC 或清理 |

额外边界：客户工作台中的历史转额度和部分日志页签处于块注释内，不纳入功能恢复；聊天实际页面在路由内嵌外部 iframe；`price-monitor/index.tsx` 的实际 UI 位于系统设置目录，必须沿引用检查，不能只检查 feature 入口。

## 操作与组件的统一规则

| 用户意图 | 统一组件/封装 | 交互规则 |
| --- | --- | --- |
| 前往另一个页面、返回列表 | TanStack Link，必要时由 Button 的 render 组合；Breadcrumb 用于层级 | 保留 URL、前进后退和原搜索条件；不使用没有关联 Panel 的 Tabs 假装页内切换 |
| 在同一工作区切换不同内容面板 | Tabs + TabsList + TabsContent | 统一默认圆角分段样式；表单切换不提交、不丢值，错误能定位到所属面板；窄屏单行滚动或保留已有两列排列 |
| 切换同一列表的范围、类型或显示方式 | ToggleGroup | 如全部/仅自己、导出数据类型、表格/卡片；必须选一项时拒绝空选择，不能通过再点一次进入无值状态 |
| 填写互斥的业务参数 | RadioGroup + FieldSet | 如加款/扣款/覆盖、明细/汇总/两者；用可点击标签展示完整含义 |
| 选择多项、勾选权限、选中记录 | Checkbox | 方形外观、清楚的部分选择态；全选仅作用于当前定义的范围，显示数量，不暗中扩大到全部分页 |
| 启用/关闭一个设置 | Switch + Field，设置页优先 SettingsSwitchField | Switch 不代表自动保存；遵守原页面保存方式。即时生效的操作和待保存配置明确区分 |
| 固定选项的下拉单选 | Select | 状态、周期、每页数量等；使用 SelectGroup，具备标签和禁用状态 |
| 从较多/远程候选中搜索选择 | 业务选择器 → Combobox/ComboboxInput | 优先保留 ExportFilterSelect 等业务封装；候选使用稳定 ID，支持加载、重试和分页；仅原业务允许时接受自定义输入 |
| 多值筛选 | 现有 MultiSelect / DataTableFacetedFilter | 选择、清空、已选数量和应用时机保持一致；分页候选不丢已选项 |
| 短表单、一次性输入任务 | `components/dialog` | 内容自适应高度；正文超过视口才滚动，标题和动作可触达；不为了两个字段固定很高的弹窗 |
| 长表单、复杂配置或侧边详情 | Sheet + drawer-layout | 固定标题、可选导航、一个主要正文滚动区、固定页脚。宽度随任务复杂度分档，不按页面随意写尺寸 |
| 需要并列核对的只读详情 | `components/dialog` + 业务分区 | 使用日志继续用可展开比较的 Dialog，统一本站/上游宽度和分区；摘要、导航与正文分层。不能仅因内容长就破坏现有双栏比较 |
| 删除、撤销、重置及放弃草稿确认 | ConfirmDialog；身份校验继续原 verification 流程 | 写清对象和后果，具体动词作为确认按钮；无改动的关闭不确认。浏览器整页关闭仍由 beforeunload 提供原生保护 |
| 行内高频操作/更多操作 | Button / DataTableRowActionMenu / StaticRowActions | 高频动作可直接显示，其他放菜单；危险操作与普通操作隔开；图标按钮必须有可访问名称，提示不能仅靠 hover |
| 复制 | CopyButton / useCopyToClipboard | 统一成功与失败反馈。按需兼容 Button 的紧凑尺寸，不在业务页重做计时和图标切换 |
| 筛选查询 | DataTableToolbar；特殊游标列表复用现有筛选封装 | 输入草稿、搜索、重置、更多筛选、活动条件分开；已使用提交查询的页面继续按搜索/Enter 应用，不改成逐键请求 |
| 加载、空结果、失败、状态 | LoadingState/Skeleton、EmptyState、ErrorState、StatusBadge/Badge | 首次无数据与筛选无结果区别展示；失败保留重试入口，后台刷新保留旧内容；运行状态不是可点击按钮 |

上述语义依据 [WAI-ARIA Tabs](https://www.w3.org/WAI/ARIA/apg/patterns/tabs/)、[Switch pattern](https://www.w3.org/WAI/ARIA/apg/patterns/switch/) 与本地 Base UI 封装。表单面板保留挂载使用已有 [Base UI Tabs keepMounted](https://base-ui.com/react/components/tabs)；不能因此同时启动所有隐藏列表的数据请求。

## 视觉与布局

### 现有主题与排版

继续使用主题 token。当前默认浅色参考为 background `#f7fbff`、foreground `#25324a`、card `#ffffff`、primary `#2563eb`、muted `#eef6ff`、muted-foreground `#48627f`；这些仅记录当前主题，业务组件仍使用 `bg-background` 等语义类，不能硬编码该色板。暗色、其他配色、用户字体/密度偏好继续生效。

沿用现有字体选择：auto 使用系统 sans，显式 sans 使用项目 Public Sans。页面标题沿用 `text-lg`，分区/字段标题 `text-sm`，正文 `text-sm`，次要短说明 `text-xs`；长说明使用易读行高，不强制挤为一行。

页面区块优先 `gap-4`，紧邻工具栏 `gap-2/gap-3`，表单分区 `gap-5/gap-6`。同一按钮区使用同一尺寸层级；表单页脚采用默认尺寸，紧凑列表工具栏采用 sm/icon-sm 配对。避免用自定义背景模拟 active Button，避免嵌套多层边框卡片。

### 页面骨架

管理列表采用：标题及页面级操作 → 可选导航/范围 → 筛选 → 列表 → 分页。继续由 SectionPageLayout 和 DataTablePage 管理伸缩、滚动及 PageFooterPortal。游标列表保留“继续加载”，不伪造总页数或改成 OFFSET。

表单采用：必要的身份上下文 → 按任务分区的字段 → 局部帮助 → 唯一明确的保存区。即时动作留在对应业务区，不能混入保存表单。

在 320/390px 下表单一列，操作允许换行，标签不被切断；页签可以横向滚动但整页不能溢出。大表格只在表格容器横向滚动；字段详情、弹层正文不出现重复的纵向滚动条。保留浮层焦点圈与下拉 Portal，不靠新增任意 z-index 解决覆盖问题。

### 用户编辑与权限

用户编辑统一为不超过 `max-w-3xl` 的常规宽度，手机全宽；不随当前页签变动宽度。字段在空间允许时两列：用户名/显示名一行，密码、备注整行。只读绑定使用清楚的标签值展示和必要复制入口，不统一伪装为禁用输入框。

```text
更新用户 · 账号                                           关闭图标
基本信息  分组与额度  请求设置  管理员权限  绑定信息
──────────────────────────────────────────────────────────
用户名                         显示名称
[只读账号]                     [输入]
密码
[留空保持不变]
备注
[输入]
──────────────────────────────────────────────────────────
未保存状态（有修改时）                    [取消] [保存更改]
```

权限使用一层按领域的 Accordion。菜单、令牌、渠道、系统设置、其他的现有领域划分保留；系统设置的内层分类改为 FieldSet 标题，避免两层折叠相互遮挡。默认展开和选择状态独立，展开动作不会修改授权。

```text
管理员令牌访问                                    已选 4/10  ∧
  [开关] 允许令牌调用管理功能                 随本表单保存
  权限仍受管理员菜单权限限制
  查询权限
  □ 精准查询用户        对应说明
  □ 查询分组名称        对应说明
  修改与操作权限
  □ 修改金额            对应说明
  □ 修改专属倍率        对应说明
```

总开关表达能力启停，使用 Switch；具体授权为 Checkbox。总开关关闭不清空分项，分项选择不暗中开启总开关。安全后果、依赖和作用范围直接可见，不仅藏在 Tooltip。短选项可两列，长权限说明仍单列，避免阅读来回跳跃。

`components/permission-matrix.tsx` 已承载权限选择，但不能直接替代 AdminPermissionsEditor：前者处理 scope 多选，后者含管理员默认授权及 view 依赖。本次在现有模块中提取两者共同使用的权限选项行（Checkbox、标签、说明、禁用状态），让两处复用；不新增第二套权限算法，不给管理员编辑器新增批量授权功能。

### 导出中心

```text
使用日志 / 导出中心
导出中心                                  范围 [全部 | 仅自己]

数据类型 [使用日志 | 额度数据]     [员工导出模板] [+ 新建导出] [刷新]
创建者          客户            状态              任务创建时间
[可搜索选择]    [可搜索选择]    [选择]            [日期范围]
[搜索] [重置] [更多筛选]                        已应用条件摘要
──────────────────────────────────────────────────────────
任务列表 / 空结果 / 可重试错误
──────────────────────────────────────────────────────────
加载更多（仅服务端返回后续游标时）
```

将当前孤立的类型 ToggleGroup 放入 ExportTaskList 工具栏左侧；动作区靠右并可换行。通过给现有列表增加可选的工具栏前置内容插槽实现，调用者仍负责类型状态和创建弹层。员工模板保持员工管理权限条件；刷新只刷新当前列表。

common/export 双页签改为使用日志返回链接或 Breadcrumb。all/self 用 ToggleGroup，上游日志是独立 Link；上游视图保留返回本地视图的入口和原 localSection 状态。

新增前端 URL 参数 `exportSource=usage|quota`，由现有 usage-logs route 的 Zod schema 校验，缺失/非法值退回 usage；普通用户和仅自己视图只允许 usage。既有 `exportFilters`、quotaTaskListFilters / mergeQuotaTaskListFilters 继续负责字段适配，隐藏的用量条件不得发到额度接口。保持旧链接兼容。

复用 ExportTaskList 的 responsive 分支覆盖日志、额度和审计入口，按角色保留不同字段。普通用户仍使用短自助导出 Dialog，管理员复杂筛选和模板仍使用 Sheet；这两者任务复杂度不同，不强行换成同一种弹层。

### 使用日志详情与上游交换

常规只读详情采用约 `max-w-3xl` 的 Dialog，手机保留视口边距并接近全屏；高度受视口约束。固定标题/分区导航，正文只有一个主要纵向滚动区。摘要每行一个字段，长请求 ID 独占可换行的值区域，复制使用 CopyButton；“查询上游”“查同类”与页签位于同一工具栏，不挤压 ID。操作区仍按现有管理员权限显示。

摘要展示当前记录的类型、模型、时间、请求标识和已有主要耗时，费用保留在计费分区；异常或结算警告保持直接可见。消费/请求日志正文按“用量与计费”“请求诊断”组织，存在至少两个有内容且有权限的面板时才显示圆角分段 Tabs。正常消费默认展示用量与计费，错误日志直接展示实际内容；普通用户的公开流状态、计费说明不移入管理员专属区域。充值、退款、管理操作等记录按各自已有业务分区直接显示，不硬套两个空页签。弹窗按内容自然伸缩，最大正文高度受视口限制，不用固定高度撑出空白。

```text
日志详情 · 消耗                                               ×
模型 · 创建时间             总耗时 · 首响应 · 费用（已有值）
请求 ID       完整标识                                  [复制]
上游请求 ID   完整标识                                  [复制]
必要的异常/结算提示
[用量与费用] [请求诊断]                [查询上游] [查同类]
────────────────────────────────────────────────────────────
请求诊断 / 上游交换
以下时间均从本次请求开始计时；未观测到的节点保留缺失状态。
第 1 次尝试
取得连接        请求体发送完成       上游首字节        HTTP 调用返回
5 ms            5 ms                 28 ms             29 ms
请求转换、策略决策及其他当前权限允许的诊断分区
```

上游交换从紧凑 DetailRow 中移为独立 DetailSection。每次尝试一组四个里程碑，宽屏四列、窄屏两列/一列，label/value 分层并使用等宽数字；有多次尝试时保留服务端数组顺序和清楚的编号。所有点都相对请求开始，**不堆叠成百分比进度条、不求和、不将首字节等同于首个 Token**。`end_ms` 显示为“HTTP 调用返回”，说明可能是收到响应或传输失败，不能暗示响应体已经读完。0 ms 正常展示；缺失显示“未观测到”；整个数组缺失/为空时不显示空分区，也不从 use_time 补造采样值。历史数据中的非数组/无效元素安全跳过，非有限或负数时间按无效值处理。不把数组下标直接关联为某条 request_policy 的尝试或渠道；现有结构没有这种关联标识。

可见性按有效日志范围定义，服务端裁剪仍是安全边界：

| 视图 | 上游交换 | 保留的限制 |
| --- | --- | --- |
| 普通用户 `/api/log/self` | 不返回、不渲染 | 自己的用量和费用详情继续可读；即使测试向 UI 注入含该字段的数据也不能显示 |
| 员工 `/api/log/employee` | 有记录时可见 | 仅既有范围：本人及有权访问的客户；不开放管理员动作、渠道名称、平台超时成本和 Root 诊断 |
| 管理员/Root 的管理视图 `/api/log` | 有记录时可见 | 继续区分普通管理员与 Root 原始诊断 |
| 管理员切到“仅自己” | 维持 self 投影，不展示 | 已有 access.test 明确要求 self 使用普通用户视图；查看管理诊断用管理范围，不绕过此契约 |
| 已获准打开的上游详情/对比 | 仅展示上游实际返回的数据 | 不向上游新增查询权限，不按上游请求 ID 请求本站私有诊断 |

新增的前端 `canViewUpstreamExchange` 从 `showAdminFields` 传入 DetailsDialog / LogDetailBody，不能复用 canUseAdminActions，也不能仅通过 admin_info 是否存在决定。保留 canViewPolicyDecisions、isAdmin、isRoot 的各自职责。普通用户禁止展示的要求针对内框上游交换；整个详情弹窗仍按角色展示原有合法信息。

本站与上游详情继续复用 LogDetailBody 和 log-detail-layout，补齐两处 DetailRow 的一致样式与既有 warning 能力，不复制新的详情渲染器。上游比较继续使用同一个 Dialog 扩宽到已有约 90rem 上限，两侧共用当前面板和 LOG_DETAIL_ROW，缺失分区留空，同类分区横向对齐；小屏按本站、上游顺序展示。关闭/切换记录后重置面板和对比状态，不泄露前一条记录的结果。原始流诊断继续 Root + 主动点击按需查询，并在关闭时清理原缓存。

### 性能设置页

保留 operations.performance 路由与 SettingsPage 骨架，三个页签均使用页面可用宽度。配置按缓存、资源保护与建议分栏；诊断将采样与维护并排，窄屏回到单列。页面标题下面使用默认圆角分段 Tabs：“运行概览”“缓存与保护”“诊断与维护”。默认进入运行概览；这是三个内容面板，使用 Tabs，不使用按钮模拟选择，也不增加侧栏菜单。

```text
性能                                 已有只读/未保存状态
[运行概况] [缓存与保护] [诊断与维护]
────────────────────────────────────────────────────────────
运行概况                             更新于 HH:mm:ss [刷新]
请求体缓存                          Go 运行时
内存预留/使用、磁盘预算/占用         当前堆内存、Go 申请内存
活动数量、缓存命中                   GC、Goroutines、主机内存

缓存目录所在磁盘                    Redis 连接池
已用 / 可用 / 总量、容量条           配置大小、使用中、空闲、等待超时
目录路径、目录文件与大小             指标说明（可展开）
```

“运行概况”采用有一致标题和间距的两列区块，窄屏单列。主要值用 text-base/tabular-nums，附属计数对齐显示，去掉把计数包装成状态按钮的误导。目录信息并入磁盘区，避免单独横跨整页。Go 当前堆、累计分配、向系统申请内存与请求体缓存含义保持分开，累计 TotalAlloc 放次级明细并标注“进程启动以来累计”；不将它或 Sys 标成当前 RSS。保留关键简短解释，更长说明放可键盘展开的说明区域，不只藏在 hover 中。Redis 禁用明确显示“本节点未启用”；字段缺失显示不可用，真实 0 继续显示为 0。

初次统计加载显示 Skeleton；首次失败显示 ErrorState 和重试。刷新保留上次成功数据，显示正在刷新；失败明确标记旧快照，不能留下看似实时的正常数字。页面统一使用同一个 stats 状态与刷新函数，CacheGuidance 继续共享，不为四个区块分别发请求。保持进入页面、手动刷新和成功操作后的更新，不新增轮询；统计 API 实际仍扫描目录，不能因为页面分区而增加调用。

“缓存与保护”按请求体内存保护 → 磁盘缓存 → 系统负载保护排列，复用 SettingsControlGroup、SettingsSwitchField、SettingsFormGrid 等封装。开关与从属字段靠近，依赖关闭时保持值但禁止无意义编辑；MiB 与百分比标签清楚。CacheGuidance 保留 30 秒有效期、路径变更锁定和低于预留量的限制，“填入建议”仅修改草稿。负载保护超过阈值会拒绝新请求的现有说明保持直接可见。

保存按钮仅由当前配置面板挂载 SettingsPageFormActions 到标题动作区；其他面板不显示“保存”来暗示即时操作需要提交。表单在父组件持有并保留挂载，切换不丢草稿；有未保存更改时标题持续提示，并可返回配置继续编辑。保持现有 changedKeys 整组提交、scope、确认、校验、已保存基线和 applied=false 处理，不新增配置默认值或自动启用。

“诊断与维护”分两块呈现：

- **pprof（仅 Root）**：开关独立显示“立即生效”，沿用现有确认及保存入口，以状态接口回读为准。下方一行“采样类型 Select → 必要时的采样时长 Input → 下载按钮”。选项来自服务端 profiles，保留 goroutine 文本变体及 profile 原名；只有 CPU/trace 显示带秒单位的 1–120 整数输入，其他类型为即时快照。默认选择服务器支持的 CPU，否则选择第一个可用项。下载中显示真实的“采样/下载中”状态并防重入，不用虚假百分比。开关关闭时显示原因；状态加载失败提供重试并禁止开关/下载，不展示为已关闭。保留内存快照敏感性说明和文件格式/离线分析说明。面板首次打开才加载 pprof；已发起的下载不会因切换 Tabs 重置成可再次执行。普通管理员、员工和用户不渲染也不请求 pprof。
- **维护操作（性能编辑权限）**：带名称的操作行“清理不活跃缓存”“重置缓存命中统计”“执行 GC”，简述各自真实作用，不与刷新按钮混排。清理沿用 ConfirmDialog，仍只清理本进程已关闭且满足时间条件的文件；重置增加明确的确认，仅清零两类缓存命中；GC 保留直接执行和就地等待反馈，不添加无依据的多重确认。每次仅一个维护写动作执行，按钮与处理函数都防重入；只在 success=true 后提示成功并刷新，失败保留可重试状态。

只读管理员可切换 Tabs、展开说明和刷新统计。复用 SettingsPage 的 `interactiveReadOnlySections`，只放开 performance 的阅读交互；配置内部的 fieldset、保存入口和所有维护处理函数继续检查 canEdit。Root pprof 独立判断角色，不能因拥有性能查看/编辑权限而开放。

## Data flow and lifecycle

### 编辑器

1. 打开记录，以当前目标 ID 加载既有详情；展示加载状态，失败留在当前编辑器并允许重试。关闭或换目标后忽略旧请求结果；语言变化或列表引用变化不重置用户草稿。
2. 页面内选择、折叠、切换页签只修改本地状态。已访问表单面板保留值，依赖远程数据的重组件按需加载。错误数量/是否修改由表单状态推导，不另建一份持续同步的状态。
3. 提交先校验，打开第一个错误所属页签并定位具体控件。沿用密码、角色、权限、超时、分组及渠道排除原约束。
4. 普通资料更新继续省略未修改的管理员权限矩阵。密码/权限修改及新建管理员继续原 requestVerification 和 proof scope/context；取消验证不提交，不丢草稿。
5. 提交入口防重复，进行中禁用相关动作。成功更新既有缓存/列表再关闭；失败展示已有安全错误并保留草稿。`committed=true` 按服务器已写入处理，重新校准表单，不把重试当作首次写入。
6. 有未保存内容时，关闭按钮、Esc、遮罩通过同一个关闭处理和 ConfirmDialog；无改动直接关闭。复用 `model-mutate-drawer.tsx:208` 已有模式，路由离开使用现有 FormNavigationGuard 思路。保存成功的关闭绕过放弃确认。
7. 额度调整是独立即时操作。成功回读仅更新 `quota_dollars` 的显示/基线和列表，不重设资料、密码、权限或其 loadedPermissions 基线。员工归属继续现有即时提交。两类操作的已保存结果不会被取消主表单撤销。

### 列表和筛选

路由/角色确定可见范围 → 加载对应列表 → 编辑筛选草稿 → 搜索或 Enter 应用 → 重置原分页/游标 → 展示结果。重置清除适用条件、错误和分页，不改变账号权限。

权限或数据源变化时使用原有 scope + userId 查询隔离；403 按 ExportTaskList 现有逻辑停止查询并移除相关缓存。维持列表选择范围，不把跨页/隐藏行纳入批量操作。员工搜索保留原去重、搜索与游标，选中候选后保留账号可读名称。

### 导出任务

新建弹层使用当前适用筛选、服务端 max_range_sec、列目录和模板 → 本地校验 → 原 POST → 获得 job_id → 刷新当前 scope 列表 → 仅存在 pending/running 时原有 3 秒轮询 → ready 后申请短期下载 URL → 浏览器原生下载。

取消/删除继续既有 DELETE 的不同任务状态语义；任务及文件删除确认保留，原日志不被删除。不得为了统一进度 UI 新增估算请求、全表 COUNT 或后台轮询。关闭/隐藏页面时沿用现有停止策略；不会通过两个同时挂载的列表扩大轮询。

### 系统设置

按 scope 读取 → 表单建立已保存基线 → 修改本地草稿 → 原页面级确认及保存 → 按各请求真实结果更新对应基线。保持部分成功、失败重试和后台刷新不覆盖草稿的已有实现。

测试连接、同步、立即巡检、清理等独立动作继续使用各自 API；移动按钮不自动保存设置。恢复价格默认值仍仅修改草稿；创建支付产品仍是确认后的独立操作。定价表达式与换算遵循 `pkg/billingexpr/expr.md`，此次不改转换、单位、精度或版本检查。

### 日志详情和性能页面的补充生命周期

日志列表按原 scope 请求 → 后端按用户/员工/管理员/Root 投影 → 行详情使用已经返回的日志数据 → 纯前端分区与上游交换展示。展示计时不新增详情 API；只有管理员主动查询上游或 Root 主动请求原始诊断时才使用原有专属接口。员工只读能力不升级操作能力，角色/范围切换沿用原查询隔离。

性能页同时保留两条独立数据流：settings scope → RHF 草稿 → 确认并 PUT 配置 → 回读配置；stats → 只读快照 → 手动刷新。维护动作和 pprof 不提交 RHF 草稿，统计回读不 reset 草稿。pprof 开关经原配置接口持久化并热更新后才回读状态；采样文件由原鉴权 HTTP 客户端下载 Blob，检查业务错误后才保存文件，继续释放 object URL。新面板不能把下载或即时维护动作作为表单 submit，也不能在页面挂载时自动执行。

## API contracts and authorization

不新增服务端端点，不修改 HTTP 状态、字段含义或鉴权。新增 exportSource 仅是浏览器 URL 状态，不发送给后端。

| 流程 | 已有契约 | 必须保留的边界 |
| --- | --- | --- |
| 用户编辑 | GET `/api/user/:id`；POST/PUT `/api/user/`，载荷由 transformFormDataToPayload 构造 | AdminAuth + users 菜单权限，控制器检查目标角色；密码/权限变更需原 proof；余额不在 PUT 载荷中 |
| 权限目录 | GET `/api/authz/catalog` → `{success,data:{resources,roles}}` | 目录为权威来源；Root 编辑边界、默认授权、view 依赖和未知新增目录项继续保留 |
| 余额 | POST `/api/user/manage`，`{id,action:'add_quota',mode,value}` | 原额度单位/模式/服务端校验；独立提交，不并入用户主表单 |
| 管理员导出 | `/api/log/export/jobs` 的 GET/POST；DELETE `/:job_id`；GET `/:job_id/download-url` | AdminAuth + usage_admin scope；POST 继续已有完整 CreateExportJobRequest |
| 本人导出 | `/api/log/self/export/jobs`，创建 `{filters,options}` | UserAuth + usage_self；仅接受本人支持的筛选和输出选项，不能复用管理员载荷 |
| 额度导出 | `/api/data/quota/export/jobs`，创建 `{filters,options}` | AdminAuth + dashboard session + quota_admin；Root 专有字段继续受后端约束 |
| 员工导出/模板 | `/api/user/employee/export/*`；`/api/admin/employee-export/*` | 员工能力、客户归属、模板权限和管理员菜单权限保持原样 |
| 日志详情数据 | GET `/api/log`、`/api/log/self`、`/api/log/employee` → `{success,data:{items,total,...}}`；other 内为已有上游交换数组 | 管理端 AdminAuth；self 为 UserAuth 且限定本人；employee 为 UserAuth + IsEmployee + 本人/客户归属查询。普通用户移除 admin_info，员工保留合法子项；无新增接口、字段或查询 |
| 上游与私有诊断 | POST `/api/log/upstream/query`；GET `/api/log/claude-diagnostic` | 前者 AdminAuth，后者 RootAuth；展示员工上游计时不改变两个接口权限，也不自动发起请求 |
| 设置 | GET `/api/option/?scope=...`，既有单项/分组/定价等保存 API | 精确 scope、只读状态、原请求和确认方式；不扩大设置键 |
| 性能设置与快照 | PUT `/api/option/group`，原 `{module:'performance_setting',values,scope:'operations.performance'}`；GET `/api/performance/stats` → `{success,data:{cache_stats,memory_stats,disk_space_info,disk_cache_info,redis_pool,config,system_status,guidance}}` | 配置编辑与统计查看权限分离；只提交原表单 changedKeys，快照更新不触发保存 |
| 性能维护 | DELETE `/api/performance/disk_cache`；POST `/api/performance/reset_stats`、`/api/performance/gc` | AdminAuth + operations.performance edit；无新请求体。各自已有动作、响应和共享资源语义不变 |
| pprof | GET `/api/system-info/pprof-status` → `{success,data:{enabled,profiles}}`；GET `/api/system-info/pprof/:name?seconds=...&debug=...`；原单项 option 更新 pprof_setting.enabled | 状态、下载和开关仍为 Root；CPU/trace 整数 1–120 秒，goroutine 文本为 debug=2，其余快照不发送 seconds。Blob 成功才下载；HTTP 200 JSON 业务失败仍按错误处理 |
| 访问令牌/登录/绑定/MFA | 当前 security/auth 模块的原 API 与验证组件 | PermissionMatrix 仅更换选项渲染；保持 scopes、有效期、一次性验证、敏感凭据只显示一次及登录会话规则 |

常规响应仍是 `{success,message,data?}`；导出列表 data 保留 jobs/enabled/max_range_sec/next_cursor 等字段。详细 TS 类型继续使用现有类型文件，不在共享 UI 中另造一个业务响应模型。

认证交互只调整呈现和可用性，不削弱验证。相关验收参考 [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。受保护的五个 lib 认证/HTTP 模块保持不改。

## Data model / configuration

无新增表、列、迁移、索引或服务端配置。无新增 localStorage 配置；exportSource 是非敏感的路由参数。草稿、确认状态、错误定位只存在组件内存中，不持久化密码或令牌。

后台查询和分页 SQL 不变，因此本设计不要求数据库迁移验收。若实施超出此边界，必须按项目规则补充真实数据库矩阵，不能用 UI 测试替代。

## Error handling and i18n

复用 handleServerError、字段错误和项目 Query 错误处理，不修改受保护的 server-error-message.ts。失败后保留当前目标和输入；成功提示只在实际成功后展示。共享确认组件的进行中反馈与操作名称一致。部分成功、权限撤销、导出关闭等状态沿用现有业务信息。

文案优先复用现有键。新增或调整的操作名、状态、描述按 i18n-translate 技能补齐 en、zh、zh-TW、fr、ja、ru、vi；通过脚本写入并检查同步后的差异范围及 UTF-8 无 BOM。长翻译通过布局容纳，不依赖截断隐藏必要信息。说明中的金额单位、权限后果和即时/保存语义不能为了短而删除。

本轮新增 23 个界面文案键，已通过临时脚本补齐全部 7 个语言包并执行 i18n 同步，检查无 BOM。复用已有文案键的模型、创建时间等标签不重复新增；保留工作区其他文案任务的修改。

## Main Chain Impact

没有代码新增到 relay goroutine，也没有新中间件、同步 I/O、后台 worker 或计费分支。所有变化在浏览器呈现、交互事件和已有管理 API 调用处执行。开关和选择不会因为替换组件而自动保存；pprof 本来就是经确认的即时开关，继续保持该语义。

性能设置和维护操作会作用于现有的共享运行时资源，不能把“仅前端改动”解释为操作自身没有成本。原有 GC、采样及清理只经用户明确触发，不在布局切换、刷新或组件 effect 中执行，不改变阈值、采样开关默认值或已有热更新路径。上游交换仅展示已有采样，不改 AttachUpstreamExchangeTrace、超时计费钩子或 UPSTREAM_EXCHANGE_TRACE_DISABLED。

### Shared Resource Audit

| 资源 | 现有相关关系 | 本次处理 |
| --- | --- | --- |
| users、用户鉴权缓存/权限策略 | 管理 API 与 relay 都依赖用户数据；UpdateUser 已维护会话撤销、权限重载和缓存发布 | 原后端提交与发布顺序不变；无新读写、锁、轮询或表结构 |
| options 与设置快照 | relay 读取部分配置 | 只改操作呈现；提交仍使用原 scope 和原入口，不改配置含义 |
| 导出任务存储、日志/额度数据、文件 | 既有导出控制器/worker 管理访问和后台导出 | 不新增任务、不增加估算或扫描；保留查询频率、游标和下载方式 |
| 数据库/Redis/后台工作池 | 后端已有共享资源 | 不建立新连接、不改池容量、不增加 relay 资源占用 |
| 请求体缓存计数、cacheFiles/lease、目录文件 | relay 使用同一套缓存与计数；维护接口读取统计或清理已关闭文件 | 保留原有限范围与时间阈值；不增加扫描、清理频率或重置活动占用；不改 lease 锁和资源治理算法 |
| Go runtime/pprof、common.RDB 连接池统计 | GC/采样作用于同进程；GetRedisPoolStats 只读本地池统计，统计接口另含目录读取 | 无新 runtime/Redis 操作；采样和 GC 仍手动且防重复，刷新不自动执行它们，不新增池、不改 Redis 键空间 |
| React Query 缓存、表单与列表状态 | 浏览器多个页面共享 QueryClient | 保持 userId/scope/dataSource 隔离；按实际成功失效，不清空无关草稿，不同时挂载重复请求列表 |

### Concurrency Analysis

在 30k/100k RPM 中继负载下，本变更为每次 relay 请求增加的 DB/Redis 调用、锁及 goroutine 均为 0；管理页保持既有请求触发方式。防重复提交会减少非预期管理写入。这个结论是变更范围分析，不是新的压力测试结果。

## 全站覆盖清单

下表所有目录已纳入源代码复查。发现的组件与布局缺口按 U01–U22 处理；其他入口已使用统一组件的，继承共享 Checkbox、确认框和选择器改进，保留业务专用布局。它们不等于全部经过浏览器逐页测试，浏览器实际覆盖见验收记录。

| feature（非测试 TSX 数） | 实施核对重点 |
| --- | --- |
| about（1） | 公共内容加载、空内容、富文本宽度和主题 |
| auth（17） | 登录/注册/重置/OTP/回调的字段、错误和等待反馈；保留认证状态机 |
| business（1） | 金额展示公共组件；单位、精度和格式不回归 |
| channels（52） | 主操作/行操作/批量操作、长编辑 Sheet、模式选择、模型/分组选择、测试弹层 |
| chat（0） | 实际 UI 在 chat 路由；宿主加载、错误、外部链接与返回操作 |
| commission-overview（3） | 概览/账本 Tabs、时间范围、滚动表格和详情 |
| customer-console（3） | 创建/编辑客户 Dialog、字段关联、查询/重置/导出动作、审计详情 |
| customers（1） | 管理员客户列表/充值记录、编辑弹层、分页；不恢复注释代码 |
| dashboard（23） | 路由导航与数据范围选择区分、图表筛选/刷新/隐藏敏感信息 |
| employee-console（1） | 月度/明细 Tabs、筛选宽度、只读及未开通状态 |
| employees（6） | 员工选择器、月份选择、业绩调整、等级与佣金记录、宽屏/窄屏页签 |
| errors（5） | 未登录/禁止访问/不存在/服务错误的恢复与返回操作 |
| home（1） | 主导航、按钮/链接、移动菜单、焦点；保留现有品牌排版 |
| keys（20） | 列表与多选、创建/编辑 Sheet、复制/显示密钥、删除/重置、草稿关闭 |
| legal（3） | 文档内容宽度、加载错误、公共页布局 |
| marketplace（1） | 市场页实际组合、检索/选择/详情、空状态 |
| model-pricing（5） | 定价工作区、预览/确认/保存层级；保留金额和表达式契约 |
| models（33） | 路由导航、元数据与关联操作、厂商 Dialog、模型 Sheet、批量菜单 |
| node-pool（1） | 节点选择、状态徽标、账户表、分页 Select、刷新与错误 |
| performance-metrics（0） | 无独立 TSX 页面；跟随实际图表/监控调用入口检查 |
| playground（14） | 发送/停止/重试/编辑/复制、参数面板、消息滚动和输入区 |
| price-monitor（1） | 实际 PriceMonitorPanel、设置/立即巡检/修价权限、确认和筛选 |
| pricing（21） | 搜索、多选筛选、视图切换、详情和移动端选择；保留价格解释 |
| profile（7） | 个人资料、语言、签到、菜单配置的保存/即时生效和局部错误 |
| profiling（1） | U19–U22；采样类型 Select、时长字段、Root 边界、状态失败、下载等待和重复点击 |
| rankings（8） | 时间/范围选择、排行榜表格/卡片、筛选和空状态 |
| redemption-codes（12） | 创建/编辑/复制、行/批量操作、筛选、有效期选择 |
| request-logs（3） | 筛选/分页/详情、长内容、权限拒绝、失败与空结果区分 |
| security（21） | 权限选择、令牌创建/编辑/撤销、会话、绑定、Passkey/2FA 和一次性展示 |
| setup（6） | 步骤导航、参数单选、校验定位、初始化等待/失败；不变更初始化请求 |
| subscriptions（12） | 创建/编辑/重置、开关和保存边界、用户管理弹层与合规锁定 |
| system-info（5） | 实例与系统任务的刷新、状态、详情和取消操作 |
| system-settings（149） | 下列 57 个分区；统一设置布局/保存组件、确认、只读、草稿和局部操作；性能页重点落实 U19–U22 |
| system-update（2） | 检查/下载/更新的步骤、确认与真实进度，不在验收中部署更新 |
| task-plugins（25） | 安装/市场面板、详情 Sheet、上传/编辑/删除及启停确认，保持 host/runtime 契约 |
| usage-logs（85） | U16–U18；通用/任务/绘图/上游/审计、导出、条件选择、详情、多种角色和移动适配 |
| users（18） | U01–U06、U09、U13；另核对列表、批量操作、绑定及敏感操作验证 |
| veridrop-detection（1） | 结果/批量/手动的任务面板、设置、筛选、选择和运行状态 |
| wallet（12） | 充值/支付/转移/记录/订阅的选择、确认、等待、失败；保持金额与支付流程 |

系统设置逐分区验收清单：

- auth（6）：login-protection、basic-auth、oauth、passkey、bot-protection、custom-oauth。
- billing（6）：quota、currency、model-pricing、group-pricing、payment、checkin。
- content（7）：dashboard、announcements、api-info、faq、uptime-kuma、chat、drawing。
- models（5）：global、gemini、claude、grok、model-deployment。
- operations（9）：node-control、behavior、alerts、email、worker、logs、request-log、performance、update-checker。
- request-policies（3）：filtering、routing、health。
- security（3）：rate-limit、ssrf、token-limits。
- site（4）：system-info、notice、header-navigation、sidebar-modules。
- system-tuning（14）：gateway-rate-limit、database-pool、login-session-policy、settlement-guard、ledger-pipeline、relay-log-pipeline、export-settings、log-query、log-export、ledger-detail、fallback-backfill、relay-timeout、group-retry-times、relay-error-display。

公共组件另覆盖 app-header/sidebar/mobile-drawer、SectionPageLayout/Main/PageFooter、Dialog/Sheet/ConfirmDialog、PermissionMatrix、DataTable 工具栏/分页/空状态、各类选择器、日期时间、CopyButton、主题与语言选择。第三方/AI 专用组件只修改实际业务调用所需部分，不机械重写未使用的组件库代码。

## 实施顺序与具体改动入口

按下面顺序执行整个范围；这些是实施批次，不是将其余页面移出任务。

1. **公共组件与行为约定**：修复 ui/checkbox 的形状和三态；完善 confirm-dialog 的等待反馈；统一 drawer-layout、dialog、表格动作/筛选的排列；在 permission-matrix 内提供共同权限选项行。保持原 props 默认行为，按真实缺口兼容扩展。
2. **截图流程与草稿安全**：修改 users-mutate-drawer、admin-permissions-editor、user-quota-dialog；调整 usage-logs/index、export/index、export-task-list 及路由 search schema；复查所有 ExportTaskList 调用。日志详情和性能设置按下述具体入口一并处理。优先覆盖过期加载、额度刷新、只读权限和连续提交等实质行为。
3. **管理列表与工作台**：渠道、模型、密钥、用户、兑换码、订阅、员工/客户、节点、监控及各类日志。替换已核实的手写选择器和原生控件，统一行操作、分页与移动布局。
4. **设置与其余页面**：逐个 registry 分区检查；用 ConfirmDialog 替换两处 window.confirm。覆盖个人、安全、钱包、公共页面、聊天宿主和初始化，统一操作反馈和可访问性。
5. **全站验收与文档回填**：记录每个入口的结果、复用组件、需要保留的特殊布局及证据；更新本文和受影响旧设计。不能以全站正则替换或仅 typecheck 作为验收。

不引入新 UI 依赖，不批量切换图标库，不修改五个受保护的认证/请求基础文件。当前工作区已有审计功能等未提交修改；实施必须按现状继续、保留其他工作，不能通过回退文件取得干净 diff。

追加截图的具体改动位置：

- 日志能力传递：`usage-logs/components/columns/common-logs-columns.tsx` → `components/dialogs/details-dialog.tsx` → `log-detail-body.tsx`，补独立上游交换能力及回归；不将员工提升为 isAdmin。
- 日志布局：`log-detail-layout.tsx`、`log-detail-grid.ts`、`details-dialog.tsx`、`log-detail-body.tsx`、`upstream-log-details-dialog.tsx`、`upstream-compare-pane.tsx`。共同分区、宽度、复制和比较对齐；现有 Dialog 缺少固定导航位置，增加可选 `navigation` 插槽，放在 DialogHeader 与滚动正文之间，不传时保持原行为。业务权限留在 feature。
- 性能设置：`system-settings/maintenance/performance-section.tsx` 持有既有表单、统计和动作状态；按业务面板拆组件，使用现有设置/错误/确认封装。`operations/index.tsx` 使用现有 interactiveReadOnlySections 指定 performance，不更改所有设置页的默认锁定策略。
- 采样下载：`profiling/components/profiling-controls.tsx` 将按钮阵列改为 Select + 有条件的时长字段 + 单一动作，`profiling/api.ts` 按实际业务成功校验状态结果，失败进入明确重试态。主 HTTP/认证模块不动。
- `controller/performance.go`、`controller/pprof.go`、`model/log.go`、`model/log_other.go`、`service/log_info_generate.go` 与 relay 采集代码只作为现有契约核查依据，本方案不修改其生产实现。

## 验证方案

行为改变先补有意义的回归用例；单纯间距/颜色/对齐通过视觉检查，不为每个 class 写镜像测试。

| 场景 | 必须验证的可观察结果 |
| --- | --- |
| Checkbox | 单选行、多选行、部分选择的视觉与 ARIA 一致；键盘 Space 可操作；各种圆角偏好不再呈 Radio 外观 |
| 用户编辑 | 普通用户/管理员/Root、创建/更新；跨页签保值；隐藏字段错误定位；切换目标时旧响应不覆盖新记录 |
| 余额与主表单 | 修改显示名/备注/权限后调整余额，草稿仍在；余额刷新准确；连续 Enter/点击只有一次请求；committed 回读仍校准已保存数据 |
| 关闭与保存 | 无改动直接关闭，有草稿可继续编辑或放弃；Esc/遮罩/关闭按钮一致；保存失败留在当前表单；成功不再弹放弃确认 |
| 权限选项 | 管理员 view 依赖和总开关保留分项；令牌 scopes、目录外权限过滤和有效期限制不变；仍需既有二次验证 |
| 导出导航 | 深链接、刷新、前进后退保持合法类型；all/self/upstream 独立语义；普通用户无法进入额度/管理导出；原筛选和异常预填不丢失 |
| 导出列表 | usage/quota/audit/self/employee；候选加载和重试；游标追加；403 清缓存；只有原活动任务轮询；下载/取消/删除的目标一致 |
| 选择与筛选 | 员工选择键盘上下/Enter/Esc、搜索、清除、下一页和已选值；月份/pageSize 值未变；中文输入法 Enter 不误提交 |
| 确认框 | 两处确认取消无副作用；创建产品确认才发原请求；恢复价格只改变草稿，保存前不发修价请求 |
| 系统设置 | 有权/只读、修改/未修改、保存中/失败/部分成功；即时操作不隐式保存；后台刷新不覆盖本地输入 |
| 上游交换可见性 | 普通用户与 self 视图均隐藏；员工可看本人/有权客户记录；管理员管理视图可看。员工仍没有查询上游/查同类/Root 诊断等动作；测试用意外含 admin_info 的普通用户数据确认 UI 也拒绝展示 |
| 上游计时语义 | 单次/多次、0 ms、缺字段、空数组、无效历史值及禁用采样后的日志；保留数组顺序，所有值按请求开始的时间点展示，不求和、不补造数据 |
| 日志弹窗 | 普通消费、错误、分层计费、充值/退款/管理类型；请求 ID 复制、窄屏长 ID；面板切换、关闭/换记录、Root 按需诊断；本站/上游同类分区对齐、上游加载/错误/无结果、公开计费字段无丢失 |
| 性能布局与草稿 | 三面板可访问，切换保持表单；保存仅作用配置；配置草稿、填入建议、手动刷新、即时开关及维护操作互不误提交；applied=false 不显示完全生效 |
| 性能只读与刷新 | 查看权限可切换/刷新但不能改配置或执行维护；首次加载/失败/重试/旧数据刷新失败正确；缺失指标不是 0；Redis 禁用、容器路径、目录长路径可读；不因拆面板增加轮询 |
| 性能维护 | 清理/重置取消不发请求；成功才关闭确认和刷新，失败可重试；连续点击/Enter 只执行一次；仅重置缓存命中，不误示 GC/Redis/占用归零；验收使用隔离后端或 mock |
| pprof | 只有 Root 加载状态与下载；服务端/网络失败不能当成关闭；仅支持列表内类型；CPU/trace 1 与 120 秒可用，0/121/小数/空值拒绝；goroutine 文本传 debug=2，其他快照无 seconds；业务失败不保存文件，下载中不能重复发起 |
| 金额与安全 | 金额单位/精度、枚举值、zero/false/null 和原请求载荷不改变；验证取消/失败不发敏感写请求；不在预览存储真实凭据 |

复用现有用户 permissions/step-up/quota 测试、导出筛选/创建/额度/员工测试、settings-submit/use-settings-form、模型 vendor-management、权限令牌及相关组件测试文件；追加真实回归，不复制上游测试文件改造。

追加日志复用 `detail-preview.test.tsx` 的 employee/admin/user 参数化矩阵，以及 `usage-facts.test.tsx`、`access.test.ts`、现有比较相关测试；性能复用 `performance-cleanup.test.tsx` 并补只读/状态/动作边界，pprof 补选择与失败下载测试。后端 `log_other_branch_audit_test.go`、`log_branch_audit_test.go`、`controller/pprof_test.go` 和 settingsaccess 性能 scope 测试是已读契约参考；如增加针对上游交换投影的测试，放本地独立测试文件，保持上游测试原样。没有数据库行为修改，不借此运行会清空共享开发库的测试。

实施后运行受影响 Vitest，公共组件覆盖广时运行前端完整测试；运行 `bun run typecheck`、`bun run lint`、相关格式检查和 `bun run build`。当前 typecheck 脚本内容是 `bun check -b`；若工具链不支持，记录原始失败和所用等价编译器检查，不能声称原命令通过。

浏览器验收至少覆盖 1440×900、1920×1080、390×844、320×740，浅色/深色与大圆角主题；中文/英文及法文、俄文等长文案。检查键盘导航、焦点恢复、错误可见性、页脚可达性、长名称和字段溢出。逐路由基本检查加上按权限/状态的重点交互矩阵；使用隔离测试数据或明确的 mock，不执行真实充值、删除账号、部署更新或付费模型调用。

验收记录明确区分“静态已查”“交互已测”“未覆盖及原因”，记录本轮实际测试命令和结果，不将历史测试结果当作本轮验证。

## 实施与验收记录

用户以“开始修改”确认实施，随后指定圆角分段页签，并指出性能页右侧空白。全站原有 8 处线形 Tabs 切换为同一默认组件；性能运行概览取消 max-w-7xl，同一行的卡片等高；后续修正取消配置和诊断的 max-w-4xl，三个页签均利用可用页面宽度。

实现结果：

- U01–U02：Checkbox 限制圆角并保留主题，部分选择使用短横线；键盘、标签点击和禁用行为有回归。
- U03–U06：用户抽屉宽度与字段重排；共用权限行；总开关与具体授权分别使用 Switch/Checkbox；加载失败可重试、旧请求隔离；调整余额仅同步余额字段，其他草稿保留；关闭草稿有确认。
- U07–U09：日志范围、路由入口和导出类型拆清；导出类型进入 URL；工具栏合并、窄屏响应；额度模式与导出输出方式使用 RadioGroup。
- U10–U14：员工远程选择复用 ComboboxInput，保留分页并补齐页脚焦点退出/Esc；业绩调整和节点分页复用 Checkbox/Select/表格组件；两处原生确认改为 ConfirmDialog；共享确认显示等待并拦截等待中的关闭，敏感提交入口防重入；复制按钮复用。
- U15：保留 SettingsPage、SettingsForm、现有保存确认、只读和部分成功契约。未重写配置持久化。
- U16–U18：独立上游交换查看能力；普通用户隐藏、员工可查看但不扩大管理动作；0 ms 与缺失值区分；计时解释正确；本站、上游和并列比较复用主体与分区表；弹窗宽度、固定页签、长 ID 和自然高度统一。
- U19–U22：性能拆为运行概览/缓存与保护/诊断与维护；配置跨页签保值，仅配置页显示保存；概览区分首次失败与旧快照；只读者可以查看/刷新；维护确认、等待和防重入；Root pprof 按需加载、类型选择、时长验证、状态失败重试与下载等待。

全站源代码检查后的保留理由：公共内容、认证、安全验证、初始化向导、聊天与游乐场、图表/榜单、支付/订阅等保留各自阅读、步骤、消息或计费结构；已有标准 Dialog/Sheet、Select、Tabs、分页和操作区不额外包装。渠道、插件及模型弹层的线形页签已替换；价格详情局部覆盖的页签背景已归一。57 个设置分区继续沿用 registry 和原权限/保存机制，本轮重点重排 performance，并替换 Waffo 与价格恢复的原生确认。未修改外部 iframe 页面。

浏览器验收采用本地 `http://localhost:3002`、页面级 API 拦截与虚构账号，不访问真实用户或执行实际管理写入。已检查用户抽屉/权限（1440×1000、390×844）、日志详情（1440×1000、390×844）、导出中心（1440×1000、320×740）、性能概览/诊断（1920×1080、390×844），并核对刷新和浏览器后退后的导出类型。捕获的 mock 结构错误已修正，不能算作生产接口缺陷。真实服务端联调、全部路由×语言×主题的穷举未执行。

验证结果（命令在 `web/` 执行，除非另行说明）：

| 检查 | 结果 |
| --- | --- |
| `bun run test --silent --reporter=json --outputFile=<temp>/ui-refinement-full-tests.json` | 全量 3147 项：3143 项通过，4 项因权限控件从 Toggle 改为 Checkbox，原测试仍查询 button/aria-pressed 而失败。未发现其他失败 |
| `bun run test src/features/security/components/__tests__/access-tokens-card.test.tsx src/features/security/components/__tests__/access-token-create-dialog.test.tsx src/features/usage-logs/export/components/quota-export-sheet.test.tsx src/features/system-settings/maintenance/performance-cleanup.test.tsx --silent --reporter=json --outputFile=<temp>/ui-refinement-final-recheck.json` | 选择器更新后 34/34 通过，包含上述 4 项；权限载荷、安全验证、取消与目录外授权断言保留。此次仅修正测试选择器，未再次重跑其余已通过用例 |
| `bunx --no-install tsgo -b` | 最终通过。项目现有 `bun run typecheck` 指向不可用的 `bun check -b`，本轮使用等价 TypeScript 项目检查，没有修改 package.json |
| `bun run lint` | 通过，0 error；7 条既有 warning 位于本轮未修改的测试/页脚/脚本 |
| 受影响 52 个 TS/TSX 文件的 oxfmt | 已格式化，保留原版权头；写入遇 Windows 短暂占用时重试恢复，最终类型检查与构建通过 |
| `bun run build` | 最终生产构建通过，输出在 `web/dist`；未部署 |
| `git diff --check`（仓库根目录） | 通过 |
| i18n | 本轮 23 个新增键在 en/zh/zh-TW/fr/ja/ru/vi 全部存在，7 文件均无 BOM；临时翻译脚本已移除 |
| 受保护基础文件 | 5 个文件与当前 HEAD 逐字节一致。本地 main 不含这些新路径，无法用该分支执行对照；未修改或替换它们 |

浏览器还核对了性能诊断暗色模式及窄屏，未出现整页横向溢出。测试环境的 jsdom scrollTo/canvas 未实现提示与本轮功能失败无关。没有后端、数据库或 relay 代码变更，因此本轮没有进行数据库迁移/并发压测。

## 截图反馈后的修正（2026-11-20）

本节为已授权界面优化的后续修正。此前只验证了性能概览宽度，遗漏另外两个页签；日志摘要改成纵向标签和大边框 ID 后占用过高，用户再次明确要求查询上游时便于对比。

### Existing Logic and Related-Path Findings

- `performance-section.tsx` 的 configuration 和 diagnostics 仍有 `max-w-4xl`，造成宽屏右侧空白。取消固定内容上限；配置按缓存、资源保护和建议分两列，诊断将采样与维护并排，窄屏回到单列。保存、确认、草稿、只读权限与按需 pprof 请求沿用现有逻辑。
- `details-dialog.tsx` 在点击查询后才启用 `useUpstreamCompareQuery`，请求 `/api/log/upstream/query`，载荷为本站 `local_request_id` 和既有分页参数。`upstreamItemToUsageLog` 已将上游结果转成共用详情模型；`collectLogMetrics` 和 `findLowerMetrics` 已定义可比用量、单价、倍率、费用及精度容差。原对比只对齐分区，不保证分区内同一字段对齐。
- 日志单条摘要按用户最新反馈改为单列逐项排列，每行只有一个字段，标签和值固定对齐；长 ID 自然换行并保留固定位置的复制图标，不使用输入框外观。普通详情宽度为 `max-w-3xl`，动态计费保留所需的较宽布局。圆角页签和查询/收起按钮在同一工具栏。查询成功后以“字段／本站日志／上游日志”逐行展示元数据、耗时和计费关键项；移动端标签独占一行，下面并列两侧值，优先展示用量、费用，长请求 ID 放在后面。复用指标提取与金额格式，不重新实现计费计算。缺失值显示未记录，保留真实 0；本站低于上游继续用既有提示。完整详情保留在可展开区域，诊断页继续展示现有两侧诊断分区。加载、失败、空结果保留本站详情与可重试的上游区域。切换页签不重复发起查询，收起对比恢复单条详情。
- `quota-export-sheet.tsx` 的窄弹窗和固定高度让字段、操作区难以同时观察。调整为适度加宽、桌面双列、移动端单列，沿用共用 Dialog 的可用高度和固定页脚；`buildQuotaExportRequest` 与 Root 专属筛选不变。
- 安全监控原来同时渲染整页错误占位、错误条和空表。改为单条紧凑可重试提示；仅列表成功时展示表格，保留独立成功的健康状态或告警数据，重试只重新请求失败项。

### 生命周期与边界

所有修正只改变前端布局、呈现和既有查询状态，无新 API、数据模型、配置或持久化。查询权限、员工能力、普通用户诊断裁剪沿用原契约；不改后台、数据库、Redis 或 relay，不引入轮询和共享资源。对比结果关闭后不保留弹窗 UI 状态，React Query 沿用原请求键和缓存时间。验证包括正常、缺失、零值、失败恢复、复制、收起对比及桌面/窄屏实测；最终结果在本节补记。

### 修正验证结果

- 相关 9 个测试文件共 **108/108** 项通过（`ui-refinement-correction-final.json`）。覆盖权限与公开信息、详情计费、上游指标/适配、额度导出、性能维护/pprof，以及安全监控的独立失败、重复提示去除与重试恢复。
- 最终对比表调整后，`log-comparison.test.tsx` **3/3** 项再次通过：同字段对齐、缺失与零值、金额精度、仅本站低于上游时告警、按需查询、失败保留本站详情、重试、跨页签复用结果和收起对比。
- `bunx --no-install tsgo -b` 通过；`bun run lint` 0 error、7 条既有 warning；`bun run build` 通过（9.91 秒）。`git diff --check` 无空白错误。2 个新增键在全部 7 种语言存在，JSON 无 BOM，临时翻译脚本已删除；5 个受保护基础文件与 HEAD 逐字节一致。
- 浏览器使用本地虚构 fixture，实际检查性能三个页签（1920×1080、390×844）、额度导出（1440×900、390×844）、单列日志详情/对比/诊断（1440×1000、390×844）与对比暗色模式。性能三个面板均宽 1672px，未发现整页横向溢出。额度表单底部开关与提交/取消可达。
- 浏览器核对复制内容完全等于原请求 ID；上游查询携带本站 `local_request_id`，切换页签、收起/再次展开和移动端重开复用缓存，整个检查只产生一次上游查询请求。安全监控失败时仅 1 条紧凑提示、无误导空表，重试后正常恢复。浏览器记录无未捕获异常。
- 检查截图与报告保存在系统临时目录，未写入真实管理数据。本轮完成本地实现和验证，未部署；不能将隔离 fixture 验证等同于生产上游联调或全部路由穷举。

### 2026-11-21 权限复查更正

此前“普通用户诊断裁剪沿用原契约”的描述遗漏了页签本身：公开流状态、响应模型等字段仍会触发“请求诊断”入口。现已明确普通用户和仅自己视角不显示此页签，公开事实直接保留在详情主体；员工仅保留已授权的诊断内容。日志/权限切换会重置详情会话，避免旧比较内容残留。全页面复查、只读导航和保存确认生命周期修复记录见 [frontend-page-access-review-20261121.md](frontend-page-access-review-20261121.md)。
