# 提成周期重置配置的设置作用域

## 背景与问题

员工页的「提成周期重置」卡片（`web/src/features/employees/components/tier-reset-settings-card.tsx`）
保存时逐条调用 `updateSystemOption` 写 `commission_tier_reset_setting.*`，走的是
`PUT /api/option/`。该路由挂了 `middleware.RequireSystemSettingsScope(authz.ActionEdit)`
（`router/api-router.go`），而请求体里没有 `scope`：

- root 管理员：`scope == ""` 时中间件直接放行，保存正常；
- 非 root 管理员：`scope == ""` 走 `common.ApiErrorI18n(c, i18n.MsgInvalidParams)` 并 abort，
  卡片只能提示「参数错误」。

同一张卡片上的「立即重置」「安全切换」走 `/api/admin/employee/tiers/*`，门槛是
`authz.AdminMenuEmployeesView`，对这些管理员是通的。也就是说：能重置周期、能切换周期口径，
却改不了周期配置本身。

`commission_tier_reset_setting.*` 也不在 `service/settingsaccess/scopes.go` 的任何作用域里，
即便前端补上一个 scope，`settingsaccess.Resolve` 也认不出来。

## 方案

新增作用域 `employees.commission-tier-reset`（`settingsaccess.ScopeCommissionTierReset`）。

它**不是**系统设置页的分区——配置的入口在员工页，不在 `/system-settings/*`。因此：

- 不登记进 `service/authz/resources_system_settings.go` 的 `systemSettingsScopeDefinitions`，
  权限编辑器里不会多出一个指向不存在页面的条目；
- 不登记进前端 `web/src/features/system-settings/access.ts` 的 `SYSTEM_SETTINGS_SECTIONS`，
  否则「系统设置」索引路由会把管理员导向一个不存在的分区；
- 在 `middleware/system_settings_auth.go` 里把该 scope 的权限映射到
  `authz.AdminMenuEmployeesView`，与同卡片其它操作同一道门槛。

这条路径已有先例：`ScopeVeridropDetection` 同样是「有作用域、无系统设置分区」，映射到
`authz.ResourceAdminMenuVeridropDetection`。`service/settingsaccess/registry_test.go` 的
`nonSystemSettingsScopes` 现在显式列出这三个作用域，新增时必须同步登记。

### 键白名单

用 `configScope` 登记（该配置由 ConfigManager 支撑，`AllowsGroup` 要求 `GroupKeys[module]` 存在）：

`enabled`、`period_mode`、`reset_day`、`reset_hour`、`reset_minute`、`reset_second`、`timezone`。

`last_reset_at` **不登记**：它由重置任务维护，`controller/option.go` 的 `UpdateOption` 本就拒绝手工写入，
白名单再挡一道。

### 前端

卡片在每条 `updateSystemOption` 上带 `scope: 'employees.commission-tier-reset'`。
值与后端常量同名，改一侧必须改另一侧。

## 权限矩阵

| 角色 | 结果 |
| --- | --- |
| root | 放行（原本就放行，不带 scope 也放行） |
| 有 `admin_menu.employees:view` 的管理员 | 放行（默认内置 admin 角色即有） |
| 被显式撤销 `admin_menu.employees:view` 的管理员 | 403 |

## 与其它子系统的关系

不触碰 AI 中继链路：配置只在管理员保存时写一次，读取走
`setting/operation_setting/commission_tier_reset_setting.go` 的快照。
保存后 `controller/option.go` 的 `isCommissionTierResetScheduleKey` 照旧触发
`service.RearmCommissionTierResetScheduleIfNeeded()`，行为不变。

## 测试

`middleware/system_settings_auth_test.go`：

- `TestRequireSystemSettingsScope_CommissionTierResetUsesEmployeeMenuPermission`
  ——有员工管理权限放行并写入 scope 上下文；被撤销的管理员 403。
- `TestCommissionTierResetScopeAllowsOnlyItsOwnKeys`
  ——七个键放行，`last_reset_at` 与 `SystemName` 拒绝，配置组同样按键校验。

`service/settingsaccess/registry_test.go`：`TestRegistryCoversEverySystemSettingsScope`
锁住「每个作用域要么是系统设置分区，要么在 `nonSystemSettingsScopes` 里」。
