# 审查问题的局部修复

用户在复核四项问题后要求“进行处理”。本次按该范围修复，不增加功能或改变鉴权架构。

## 范围和数据流

- 普通邮件和监控邮件共用 SMTP 发送、TLS 选择、邮件组装及 Resend 请求/响应校验。调用方继续提供配置快照、context、SMTP 拨号器和 HTTP client。监控保留独立连接、8 秒发送预算、取消即关闭连接；Resend 保留 publicDial、防重定向和禁用代理。管理员指定的 SMTP 主机继续允许内网服务器。
- 隐式 TLS 优先于 STARTTLS；465 端口仅在没有显式 STARTTLS 时自动采用隐式 TLS，与原普通邮件一致。不引入失败后切换供应商或自动重投。
- 普通 SMTP 的拨号预算为 5 秒、发送总预算为 20 秒；监控的 8 秒父 context 优先。发件人缺省回退仅影响本次发送，不再写回全局 SMTPFrom；收件人和发件人使用地址解析、统一 MIME 组装。
- 流式策略只保存通用/Claude 两份必要策略和一个冻结标记。删除可从 Claude 策略推导的 enabled 镜像及 MainStreamMode 的冗余条件；保留实际处理所有权、生命周期状态和日志来源。
- 权限仅共享持久化旧规则的效果/范围规范化。PAT 继续逐请求查询主库，网页继续使用 Casbin 快照；用户覆盖优先、deny 优先、管理 PAT 必须显式授权等规则不变。无效效果仍使适配器加载失败，实时入口仍拒绝。
- 删除没有产品调用的旧 Claude 设置组件及专属测试；现用通用组件及旧配置兼容保留。

## API、模型与配置

无 HTTP 契约、认证等级、数据表、索引、迁移或用户配置变更。邮件共享函数属于内部 Go API，不新增第三方依赖。

## 错误与生命周期

发送失败沿用既有错误返回和通知任务重试；不返回服务商响应正文或凭据。所有 SMTP 退出路径关闭连接，context 取消中断握手和命令。Resend 限制响应读取为 4096 字节，必须有有效 ID 才算成功。关闭/暂停监控仍由原 worker 控制。策略每次渠道尝试重算，重试不会沿用上一渠道值。

## Main Chain Impact

relay goroutine 只减少布尔字段写入和判断，没有新同步 I/O、锁或异步任务。策略开关、处理器选择、响应观察、计费和重试规则保留原合同。邮件和管理鉴权的改动不进入 AI relay。

## Shared Resource Audit

邮件继续读取原选项，监控不使用普通邮件的共享 HTTP client；SMTP 每次独立连接，无新池。权限沿用原 casbin_rule 查询和 Casbin 内存快照，无新查询/事务/缓存/Redis key。策略仅修改每次尝试私有 RelayInfo，未添加共享结构。

## Concurrency Analysis

30k/100k RPM 的 relay 每请求新增 DB/Redis 调用、锁、goroutine 均为 0，且减少镜像状态。此为调用路径分析，不是负载测试结果。

## 先行测试方案

- 本地 SMTP fixture 覆盖明文、STARTTLS、隐式 TLS、两开关同时开启；验证两条发送入口合同一致，取消可终止等待。注入拨号器覆盖 465 自动 TLS/显式 STARTTLS 优先的端口边界。
- Resend 使用本地 HTTP fixture 验证 context、收件人/发件人规范化、失败状态、空或非法响应 ID，无真实投递。
- 策略验证两种读取顺序、冻结后配置指针变化、重试重算、旧 Claude 覆盖及所有权模式。
- 权限验证普通管理员/root/普通用户、用户覆盖、allow/deny 冲突、旧空效果/all/own/other 约束，保留撤权无须快照重载的原测试。
- 删除组件后检查引用、通用组件回归、前端类型检查及 lint。

## 验证记录

2026-11-15 本地验证完成：

- 修复前 `TestNotificationSMTPTransportModes/both_flags` 稳定失败，实际 SMTP 命令为 `EHLO localhost` 后再次 `STARTTLS`；修复后通过。
- `go test ./common ./relay/common ./service/authz ./service/auditsecurity -count=1` 的四个包均有最新通过结果。监控包期间另有并行修改扫描实现/测试，曾出现编译和扫描断言失败；待其更新后重新运行整包通过（8.721 秒），本任务未改扫描实现。
- SMTP 本地协议 fixture 覆盖明文、STARTTLS、隐式 TLS、双开关、465 默认 TLS、465 显式 STARTTLS、多收件人、普通邮件入口、发送中取消。Resend 使用注入的 HTTP transport，覆盖成功、拒绝、空/空白 ID、非法 JSON、4096 字节读取上限及 context 取消。未发送真实邮件。
- `go test ./relay/channel/claude ./relay/channel/openai ./relay/channel/aws ./relay/helper -run 'Test.*(Stream|Realtime|Claude|SMTP|Email|Resend)' -count=1` 通过。
- 同一筛选的 `go test ./service` 在真实 **MySQL 5.7.44** 和 **PostgreSQL 17.11** 通过，测试自行创建并清理隔离数据库/schema。MySQL 原测试容器默认字符集导致既有 employee_commission_tiers 迁移失败，改用本任务独立 utf8mb4 容器后通过；该独立容器及卷已清理。记录：`_local/review-simplification-20261115/service-results.json` 及同目录日志。此为服务回归，不代表新迁移或全三库矩阵；本轮未更改 SQL/模型/迁移。
- `go vet ./common ./relay/common ./service/authz ./service/auditsecurity`、`go build ./...` 通过。
- 前端通用策略组件、旧配置表单兼容、请求策略设置三个文件 **38 项测试**通过；`bun run lint` 通过，有工作区既有警告。`bun run typecheck` 因本机 Bun 缺少 `check` 子命令失败，已用项目安装的 `bunx --no-install tsgo -b --pretty false` 等价检查通过。
- 删除的组件、布尔镜像和冻结镜像无剩余引用；Go 格式/差异检查通过，五个受保护前端会话/HTTP 文件没有工作区差异。

未运行全仓测试、真实邮件投递或负载测试，未部署。授权规则参考 [OWASP ASVS](https://owasp.org/www-project-application-security-verification-standard/)、[Authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)、Authentication 和 Session Management Cheat Sheets；本轮官方站点读取失败，不能声称已核对最新在线版本。
