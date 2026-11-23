# 用户与管理员编辑页整理

日期：2026-11-20。用户反馈页面拥挤且分散，并确认对象为用户／管理员编辑页（含令牌权限）。本次是已有编辑界面的布局及导航调整，沿用原权限与保存行为。

## Goals and scope

把长表单按任务集中到五个页签：基本资料、分组与额度、请求设置、管理员权限、绑定信息。创建用户只展示适用页签；普通用户不展示管理员权限。余额调整、员工归属等即时操作保留现有独立流程及提示。管理员令牌权限独立成组，读取与修改分开排列。

## Existing Logic and Related-Path Findings

- `users-mutate-drawer.tsx` 使用一个 react-hook-form 表单，桌面侧栏列出最多十余个锚点，手机隐藏整条导航。所有字段一次性渲染，协议校验插在基本信息之后，备注在余额区域，超时／重试／渠道排除分散为独立导航。
- `AdminPermissionsEditor` 仅有上述调用点，目前同时提供独立侧栏及嵌入模式。`admin_access_token` 混在 Other permissions，操作和长说明全部以两列排列。权限来源为 `getPermissionCatalog`，值通过 `normalizeAdminPermissions` 继承角色默认值；view 依赖由 `viewDependentActions` 维护，不能因布局变化改动。
- `getUser` 加载后 `form.reset(transformUserToFormDefaults(...))`；`transformFormDataToPayload` 保存完整字段，未改的权限矩阵会移除，避免普通资料编辑要求验证。修改密码／权限和创建管理员仍通过 `requestVerification` 后调用原 API。
- `UserQuotaDialog` 即时修改额度并重新读取用户；`EmployeeAssignField` 即时修改归属。此次不将这两项误改为随主表单提交。
- `components/ui/tabs.tsx` 封装 Base UI Tabs，Panel 支持 keepMounted。`channel-configuration.tsx` 已有横向页签和保留面板的模式；复用 Tabs、Accordion、Field 和 drawer-layout，不新增导航组件库或修改共享组件。
- `userFormSchema` 包含隐藏页签中的超时、重试、备注等字段。校验失败必须激活错误所属页签并在显示后聚焦，不能只留下不可见错误。

## Layout and interaction

沿用现有 `bg-background/text-foreground/text-muted-foreground/border-border` 语义配色、项目字体和字号；不新增色板或字体。去掉重复侧栏和用户信息卡片，抽屉正文获得完整宽度。页签位于正文滚动区域之外，窄屏横向滚动；底部保留统一关闭／保存按钮。

```text
编辑用户 · 用户名
基本资料 | 分组与额度 | 请求设置 | 管理员权限 | 绑定信息
---------------------------------------------------
当前页签内容（可独立滚动）
管理员权限：菜单 / 令牌 / 渠道 / 系统设置 / 其他，按需展开
令牌：总开关 → 查询权限 → 修改及操作权限
---------------------------------------------------
关闭                                  保存更改
```

基本资料集中名称、密码、备注及适用的员工归属；分组与额度先余额再分组／专属倍率；协议校验、超时、重试、渠道排除集中在请求设置。绑定信息使用宽屏两列、窄屏一列。权限选项使用单列有间距的行，标题与说明有清楚层级，令牌总开关独立于分项。关闭总开关不清空或自动选中分项；未知新增 catalog 动作不能被过滤丢失。

同一表单跨页签保留挂载，不丢编辑值；错误处理切到对应页签。打开其他用户／重新打开编辑器时回到基本资料。导航不触发提交。展示与可编辑范围仍来自原角色及 catalog。

## Data flow / API contracts / storage

打开 → 既有 GET `/api/user/:id`、`/api/group/`、`/api/authz/catalog` → 表单状态 → 本地切换／编辑 → 原 schema 校验 → 必要时原安全验证 → 原 POST/PUT `/api/user/` → 原成功刷新或失败提示。请求形状、鉴权、数据库、配置参数均不变，无新接口和后台任务。

## Error handling and related subsystems

保留 `handleServerError`、权限变更验证、committed 后回读及额度/归属独立操作。新导航使用表单错误选择面板；无效密码的手动校验也定位基本资料。所有新增文案补齐七语言。没有 relay、服务端存储、缓存、连接池或共享资源变更；不影响 AI 请求链。

## Verification

先更新现有权限交互测试，覆盖导航不提交、跨页签内容保存、隐藏字段错误定位、三项令牌读取权限独立保存、总开关保留分项、Root／普通管理员的编辑边界。运行受影响用户编辑测试、类型检查、涉及文件 lint 和前端构建；浏览器检查桌面及窄屏、权限展开和保存区。使用本地合成用户及 mock API 的独立 UI 预览，不操作真实账号。

## Results

- 已实现五个页签，顶部固定显示被编辑的显示名称／用户名，移除重复锚点侧栏。页签与底部保存区固定，正文独立滚动；沿用渠道编辑器的横向 Tabs 样式，窄屏可滚动。
- 令牌权限从 Other permissions 中独立，默认展开；总开关、查询权限、修改与操作权限分别排列。菜单、渠道、系统设置及其他权限按需展开并显示启用数量；未知 catalog 动作仍展示，授权归一化与 view 依赖逻辑保留。
- 表单面板 keepMounted，切换不提交、不丢值；schema 错误和手动密码错误会切换页签并在渲染后聚焦。保存、身份验证、额度调整及员工归属的现有流程保留。
- 新增四个界面文案，通过临时 `add-missing-keys.mjs` 与 `bun run i18n:sync` 补齐 en、zh、zh-TW、fr、ja、ru、vi。逐语言比较前后，均仅新增四键，无旧值变更／删除，无 BOM。
- 测试先补失败场景再实现。权限、step-up verification、额度展示、渠道排除共 4 个测试文件、44 项通过；最终页签间距／账号标识调整后权限测试 15 项再次通过。隐藏字段错误定位、跨页签保留值、不触发额外提交、Root 编辑边界及令牌分项保存均覆盖。
- `.\node_modules\.bin\tsgo.exe -b`、涉及文件的 oxlint／oxfmt 检查、`bun run build` 均通过。项目现有 `typecheck` 脚本引用不存在的 `bun check`，因此直接调用已安装的 TypeScript 检查器。
- 本地独立 mock API 预览使用真实组件与服务端令牌 catalog 文案，在 Edge 无头浏览器检查 1440×1000、390×844、320×740，含浅色／深色、普通用户／管理员／新建用户、额度／请求／绑定页。11 个截图场景正文与页面均无水平溢出，保存区均在视口内，无运行时异常；未使用真实用户或发送管理操作。预览脚本与截图保存在系统临时目录，仓库内临时入口清理。
- 五个受保护的登录基础模块与当前 HEAD 无差异。本地 `main` 不含这些 TypeScript 模块，不能作为本次一致性对照；未修改这五个文件。
- 复用 Tabs、Accordion、Field 和 drawer-layout，没有新组件库、共享样式实现、API／数据库／缓存／relay 变更。本次完成本地代码与构建，未部署。

## 2026-11-21 权限分组标题吸顶

- 根据截图反馈，为 `AdminPermissionsEditor` 展开的分组标题增加局部 sticky 样式。现有 `AccordionTrigger` 将按钮包在 Base UI 的 `h3` 中，吸顶作用于该标题容器，并由对应 `AccordionItem` 限定范围；收起后恢复普通布局，滚过分组末尾后标题退出。
- 正文由 `sideDrawerFormClassName` 提供带上下内边距的滚动容器。标题使用与移动端／桌面端顶部内边距对应的负偏移，贴住页签下方，避免滚动内容从标题上方露出；沿用 `bg-background` 和局部层级遮挡下方内容。没有新增滚动监听、React 状态、共享组件 API 或服务端资源访问，权限计算与保存流程保持原实现。
- 验证：现有权限交互测试 15 项通过；直接执行 `tsgo.exe -b`、修改文件 oxlint／oxfmt 检查通过。`bun run typecheck` 仍受现有 `bun check` 脚本缺失影响；全量 `bun run lint` 的错误来自其他改动文件（`upstream-timing.ts`、`detail-preview.test.tsx`、`.tmp/monitor-parameter-review/parameters.test.tsx`）。
- 使用真实用户抽屉与本地合成权限 catalog 的隔离预览，在 Edge 无头浏览器验证 1440×1000、390×844 和浅／深主题：菜单、令牌、系统设置、其他权限四个长分组均吸顶，收起／再展开及滚过分组边界正常，共 18 项检查通过；标题可点击、页脚可见、无水平溢出或运行时异常。截图及验证脚本保存在系统临时目录 `newapi-permission-sticky-20261121`；未访问真实管理 API。
