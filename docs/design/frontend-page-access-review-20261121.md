# 全页面权限与显示状态复查（2026-11-21）

## Goals and scope

用户反馈普通用户的日志详情仍出现“请求诊断”，要求再次检查全部页面。本轮检查现有路由、菜单、页签、弹窗、数据加载及错误/空状态，直接修复可复现的缺陷，不增加业务功能或改变服务端授权政策。保留工作区其他进行中的修改。

## Existing Logic and Related-Path Findings

1. `usage-logs/components/columns/common-logs-columns.tsx` 已将管理操作与员工只读能力分开传给 `DetailsDialog`。`log-detail-sections.ts` 却以公开的 `stream_status`、模型映射等字段直接开启页签，没有要求管理/员工视角；且 `status=ok`、缺失实际映射模型、退款的转换字段也会误判为有诊断内容。`log-detail-body.tsx` 有自己的条件，导致入口和内容不一致。
2. 普通用户的日志接口由 `model/log.go:formatUserLogs` 调用 `model/log_other.go:formatLogOtherJSON` 删除 `admin_info`、`root_info`、`audit_info`。原始响应仍经 `router/api-router.go` 的 RootAuth；上游查询是 AdminAuth。本轮只修复显示与状态，不修改这些接口或主链。
3. `dashboard/section-registry.tsx` 与 `dashboard/index.tsx` 隐藏普通用户的 users 页签，但 `routes/_authenticated/dashboard/$section.tsx` 只校验 section 名称。直接访问 users 仍挂载用户分析组件；补角色拦截并测试直接访问。
4. `DetailsDialog` 的比较展开与页签状态不绑定日志身份。关闭会复位，但打开期间换记录/失去管理能力仍可能保留状态。`useUpstreamCompareQuery` 的 disabled 只控制请求，不清除已经返回的数据；展示也需要即时检查当前能力。
5. 其他页面入口使用 `requireAdminMenu`、员工身份、Root 身份或 `requireSystemSettingsSection`。系统设置包含 9 个分组、57 个分区，展示/编辑权限分别由既有权限矩阵控制。继续逐项核对页面内部按钮、请求条件及直接地址访问。
6. `request-logs/index.tsx` 只禁用打开按钮，已打开的详情没有随查看权限撤销卸载；详情查询缓存仅按日志 ID 区分，不能区分 Root/管理员的头部脱敏投影。修复为权限约束挂载、查询及下载，并把账号与角色纳入查询键，身份改变时中止旧下载。
7. 浏览器实际复现：`content/chat` 的两个页签均被外层只读 fieldset 禁用；模型定价桌面页签使用 portal 而可切换，手机页签处于禁用 fieldset 内而不可切换。复用已有 `SettingsPageTabsContent`（将面板渲染为受权限控制的 fieldset）和 `interactiveReadOnlySections`，仅让导航可交互，价格/聊天编辑继续禁用；价格重置确认也检查编辑权限。
8. `SettingsForm` 与页面保存按钮已限制编辑权限，但 `SettingsPageProvider` 外层的保存确认会保留旧作用域的回调。确认框打开后切换分区或撤销编辑权限，旧确认仍可点击。将确认与表单会话绑定到 scope/canEdit，复用现有卸载取消逻辑，避免旧回调跨权限/分区继续执行；已经提交的请求仍由服务端鉴权处理。
9. 只读限流设置的 Visual/JSON 切换是普通按钮，原来也被外层 fieldset 一并禁用。允许切换表示方式，显式禁用限流开关、数值输入及编辑区；保存继续使用既有权限检查。聊天与限流的可视编辑器最小宽度造成手机端内容区横向溢出，为相关 grid/flex 子项和 fieldset 补 `min-w-0`，操作区沿用相邻设置页的换行布局。

## Data flow and API contracts

沿用已有流程：可信登录信息 → 路由/菜单与页签权限 → 既有查询 → 后端按身份投影 → 页面显示。页面权限只收窄界面和挂载，不作为后端鉴权替代。普通用户与仅自己视角不显示诊断页签，其公开流状态、模型与计费说明保留在直接详情中；员工保留已授权的上游交换和策略记录，管理动作仍单独限制。

没有新接口、请求/响应字段、数据库模型、迁移、索引或配置参数。错误反馈与重试复用当前组件；禁止为检查或演示伪造正式业务后端。

## Shared Resource Audit / Main Chain Impact

仅前端页面及本地测试修改；无 relay 执行、Redis、数据库、连接池、后台任务或共享服务端内存变化。已有浏览器查询缓存的显示必须受当前身份/查看范围限制。浏览器验收使用本地开发页和明确隔离的 API fixture，不执行真实管理写入或付费模型调用。

## Verification

先增加可复现的行为测试，再修复实现。覆盖普通用户、员工、受限管理员、管理员、Root、仅自己视角；正常/错误/缺失日志、直接地址、关闭重开及打开期间权限/记录变化。全路由与设置分区建立检查清单；运行当前全量 Vitest、类型检查、lint、构建，并用浏览器检查入口、页签和桌面/窄屏布局。

两项已有测试原先检查已经删除的提示文案，现检查折叠按钮的 `aria-expanded` 与管理员专属扫描统计，保留原权限/交互断言。单次浏览器巡检、截图和测试计数仅保存在本地验收记录。
