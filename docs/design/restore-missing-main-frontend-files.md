# 补回合并遗漏的 main 前端能力

## Goals and scope

本改动补回标准合并后仍未进入当前前端的必要上游能力，同时保留本仓现有认证会话、员工、计费和自定义页面逻辑。

纳入范围：

- 登录完成后的同源重定向校验与用户语言恢复。
- Telegram 登录组件和授权载荷校验，替换当前“coming soon”占位行为。
- Telegram 绑定弹窗回调解析、跨窗口结果传递和超时/关闭监测。
- 旧 `/login`、`/console/*` 地址到当前扁平路由的兼容跳转。
- 渠道优先级/权重编辑的 800ms 合并更新与离开控件时立即提交，避免连续点击产生多次请求。
- JSON 编辑器纯函数拆分及相应回归测试，保持上游编辑、缩进和格式化行为。
- 恢复上述逻辑的上游测试文件。

不纳入范围：

- `web/public/favicon.ico`：品牌图标由本仓站点配置和现有静态资源负责，不恢复上游默认图标。
- 仅为测试覆盖存在、且不对应缺失运行时能力的其他上游测试文件。
- 修改后端接口或认证协议。

## Data flow

### 登录和 OAuth

1. 登录页或 OAuth 回调从 URL 读取 `redirect`。
2. `sanitizeAuthRedirect` 只接受当前站点同源的 HTTP(S) 地址，并返回站内路径。
3. 登录成功后保存认证 bundle、拉取当前用户、恢复语言，再导航到安全目标。
4. Telegram 登录按钮打开 Telegram 登录对话框；组件取得 Telegram 授权数据后交给既有登录 API。

### Telegram 绑定

1. 资料页打开绑定弹窗并等待回调窗口。
2. OAuth 回调页解析 `telegram_bind`、`flow_token` 和稳定错误码。
3. 回调结果通过限定 `targetOrigin` 的 `postMessage` 返回发起窗口。
4. 发起窗口在收到结果、窗口关闭或超时后清理计时器并向用户反馈。

### 旧路由兼容

1. 根路由在正常页面加载前检查当前 URL。
2. `resolveLegacyRoute` 将已知旧地址转换为当前路由，保留查询参数和 hash。
3. 未识别且不属于旧控制台的地址保持不变。

### 渠道字段更新

1. 数字控件连续变更时由调度器覆盖待提交值。
2. 静止 800ms 后只提交最后一个值。
3. 控件结束交互时 flush，确保最后一次变更不会丢失。

## API contracts

不新增 API 端点，也不改变鉴权等级。复用现有公开登录/OAuth 接口、已认证的绑定接口和管理员渠道更新接口。

前端纯函数契约：

- `sanitizeAuthRedirect(value, origin): string | null`
- `pickTelegramAuthorization(value): TelegramAuthorization | null`
- `parseTelegramBindCallback(search): TelegramBindCallback`
- `resolveLegacyRoute(rawHref): string | null`
- `createChannelFieldUpdateScheduler(onUpdate, timers)` 返回 `schedule` 和 `flush`

## Data model changes

无数据库表、字段、迁移或索引变更。

## Config parameters

不新增配置。Telegram 是否展示继续使用现有系统状态字段。

## Key business logic and edge cases

- 拒绝跨域、协议相对、反斜杠和非 HTTP(S) 登录跳转，避免开放重定向。
- Telegram 授权必须同时包含 `id`、`auth_date` 和非空 `hash`。
- 绑定回调缺少 `flow_token` 时按无效请求处理，不向 opener 发送不完整结果。
- `postMessage` 使用当前 origin，不使用 `*`。
- 弹窗关闭、超时和组件卸载均取消计时器，避免重复通知和监听泄漏。
- 旧路由仅处理明确映射；未知 `/console/*` 安全回退到 dashboard。
- 渠道字段连续输入只发最后一次请求，flush 可重复调用但不会重复提交。
- JSON 编辑器对无效草稿只显示校验状态，不擅自覆盖用户输入。

## Error handling strategy

- 用户可恢复错误使用现有 toast 和翻译文案，不显示服务端内部错误。
- OAuth/Telegram 初始化失败恢复按钮状态，允许用户重试。
- 解析类纯函数返回 `null`/`invalid`，不抛出到 React 渲染链。
- 不修改后端错误响应格式。

## Interaction with existing subsystems

- 认证：保留 `auth-session.ts`、`auth-session-sync.ts`、`http-client.ts`、`api.ts`、`server-error-message.ts` 字节级上游一致性；调用点围绕这些模块组合。
- i18n：优先复用 main 已有键；若当前语言包缺键，按项目七种前端语言补齐。
- 渠道管理：只改变前端请求触发节奏，不改变管理员 API、请求体或权限。
- 路由：只在根路由增加同步字符串映射，无网络请求。
- Relay：本改动不进入 AI relay chain，不访问 relay 使用的 Redis、数据库、缓存或工作池。

## Test plan

先恢复/编写测试，再接入实现：

- 重定向：同源有效地址；跨域、`//`、反斜杠、非法协议和非法 origin。
- Telegram 登录：有效载荷、缺字段、空 hash、错误类型。
- Telegram 绑定：成功/失败/缺 token/无 opener/窗口关闭/超时/清理。
- 旧路由：各旧入口、设置 tab、查询参数/hash、未知路径。
- 渠道调度：防抖、覆盖旧值、flush、重复 flush。
- JSON 编辑器：缩进、Tab、括号和格式化纯函数。
- 验证命令：`bun run typecheck`、`bun run lint`、现有前端测试命令及 `bun run build`。

## Implementation notes

实施时不整文件覆盖本仓已有调用组件；以 main 文件为参考，将必要调用逐段合入，避免覆盖本仓 OIDC 显示名、Auto 分组、员工功能和登录会话修复。完成后更新本节记录实际采用的文件和任何与上游不同的兼容处理。

Actual implementation restores the missing auth redirect, Telegram login/binding,
OAuth popup, legacy-route, and channel-field scheduling modules together with their
upstream regression tests. Auth/profile call sites were synchronized with `main`,
while the local `is_employee` user field, channel tag batch-confirmation flow, and
Auto-group badge behavior were retained. Secure-verification scopes were added to
channel-key viewing and passkey mutations. No backend, database, configuration,
Redis, or relay-chain code was changed.
