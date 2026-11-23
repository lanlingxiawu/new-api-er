# 安全监控操作者用户名展示修复

后续范围更新（2026-11-18）：用户进一步确认保存失败登录所尝试的账号，见 [失败登录账号留存修复](audit-security-attempted-account.md)。下文记录首次“已验证操作者当前用户名展示”修复的边界与验收，新增明文账号字段由后续文档说明。

## Existing Logic and Related-Path Findings

2026-11-18 用户指出安全监控“操作者”列只有 ID 或“未识别账号”。核对 `security-alert-columns.tsx`、详情/证据/关联组件后，确认均直接显示 actor_id；`ListAlerts`、`GetAlert` 和 `GetAlertRelations` 没有提供用户名。现有负责人加载器已在独立审计读取池按 users 主键批量查询姓名，可以沿用相同读取方式。

`model/audit_security_capture.go:CaptureSecurityEvent` 对匿名登录失败只保留 `SecurityAccountRef(identifier)` HMAC 摘要，actor_id 为 0；`controller/user.go` 的成功登录由服务端提供确认后的用户 ID。失败时输入的用户名不等于经验证的操作者，现有摘要也不能反解。这次修复已有账号的名称展示，不改变认证采集或推断匿名告警身份。

## Goals and scope / Data flow

已获授权的监控列表、详情及关联记录 → 沿用原权限/分页查询 → 收集本次响应中正数 actor_id → 在独立读取池按主键批量查询当前 username → 返回可选 actor_username → 列表、概要、证据、命中快照、处理历史及关联统一展示 `username (#id)`。没有名称时保留 `#id`；无有效 ID 时保留已有未知/系统提示。导出复用详情，包含同一名称投影。

这是已有显示缺陷修复；不新增账号采集功能、搜索条件或产品文案。已删除账号显示 ID；改名后显示当前用户名，不伪装成事件发生时的历史快照。个人和员工专用 DTO 不新增其他账号信息。

## API contracts / Authorization / Errors

既有 `/api/audit/security/alerts`、`alerts/:id`、`alerts/:id/relations` 和导出接口的记录新增可选 `actor_username`，其他字段及 AdminAuth、审计读取/导出权限、Root 可见性不变。只为已通过可见性检查的响应记录取名，不接受前端传入用户名或用 subject_ref 匹配用户。

查询失败沿用现有错误处理与日志；不把数据库故障伪装成不存在。没有匹配用户是正常 ID 降级。不会写入告警、来源审计、用户或历史证据。

## Data model / Configuration / Index design

模型展示字段使用 `gorm:"-:all"`、JSON omitempty，不能生成列或参与写入。无新表、迁移、索引、配置或翻译。users 只投影 id/username，去重后每批最多 100 个主键，复用已有主键索引；返回中同一用户的多个位置共用一次读取结果，不逐行查询，不扫描日志或全体用户。

## Main Chain Impact / Shared Resource Audit

不修改 relay、登录流程、后台检测或自动处置。只增加管理端读取请求，使用原 `AuditReadDB(false)` 独立有限连接池及 3 秒上下文。users 与 relay 共用表，但此处仅有界主键只读、无行锁/写入；无 Redis、缓存、额外连接池或 goroutine。30k/100k RPM 下 relay 每请求新增 DB/Redis 调用、锁、goroutine 均为 0。

## Verification

覆盖可见已知账号的列表/详情/证据/快照/人工历史/关联/导出用户名、匿名不推断、软删除及不存在账号降级、Root 隔离、当前改名、批量去重和只读字段不迁移。

2026-11-18 本地验证：

- MySQL **5.7.44**、PostgreSQL **18.7**：每种数据库 19 项定向服务测试通过、0 跳过，包含新增 `TestSecurityActorUsernames`、`TestSecurityActorNamesBatchingAndFailure` 及既有详情、查询计划、本人和客户边界回归。205 个唯一账号、410 个显示位置只生成 100/100/5 三批姓名读取；取消上下文返回错误；四种模型均无 actor_username 数据库列，原证据 JSON 保持不变。
- 前端审计及客户详情：19 个文件、238 项测试通过，新增真实监控列表→打开详情的用户名展示及证据卡的名称/ID/未知降级断言。类型检查、9 个涉及文件 lint、前端生产构建通过；前端构建完成后根模块构建通过。首次并行根构建曾撞上前端清理 dist，顺序重跑通过。
- Chrome **390×844 / 1840×1000**：本次构建产物配合本地模拟 API，用户名及 ID 可见，手机前后翻页仍归顶，无 JavaScript 错误；不等同于真实登录后的全栈验收。

执行脚本、数据库版本/命令/结果、日志及截图保存在 `_local/audit-actor-usernames-20261118/`。数据库测试仅操作自有 `audit_test_*` 临时库，已清理；未访问生产或部署。没有新增 DDL，无历史用户名补写，也没有新增登录失败明文账号采集。
