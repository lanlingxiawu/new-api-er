# 系统设置保存确认

日期：2026-08-07

状态：已实施并验证

## Goals and scope

目标是在 `/system-settings/*` 下，任何真正把系统配置写入后端的用户操作执行前，都显示一次确认弹窗。管理员取消后不得发送写请求；确认后继续沿用现有保存逻辑、权限、成功提示和错误提示。

本次按“是否发送持久化请求”界定范围，不按按钮文字界定：

- 纳入普通表单保存、列表整体保存、即时持久化开关、整组热配置保存、模型价格同步，以及自定义 OAuth Provider 的创建和更新。
- 一次用户提交即使会写多个 option key，也只确认一次，不把每个 `mutateAsync` 当成独立保存。
- 仅修改父表单内存草稿的内层 `Save` / `Add` / `Update` 不弹确认；最终把该草稿写入后端时再确认。例如公告条目、FAQ 条目、支付方式、折扣、渠道亲和规则和分组覆盖编辑器。
- 清日志、清缓存、重置价格、删除 Provider、支付合规确认等已有专项确认的独立操作维持现状，不叠加通用确认。
- 上游价格同步发生计费模式冲突时继续使用现有冲突详情弹窗；无冲突时使用通用保存确认，任何路径都只出现一个确认弹窗。

非目标：

- 不修改设置值的含义、校验、保存顺序或多节点生效语义。
- 不把现有逐 key 保存改成批量或事务保存。
- 不新增后端接口、审计日志、权限、配置项或数据库结构。
- 不为只读、刷新、测试连接、缓存清理、日志清理或外部 Waffo 商店/商品创建动作增加“保存确认”。

## Current entry-point inventory

### Shared form actions

以下 41 个当前可达 section 使用 `SettingsPageFormActions`，但表单 `onSubmit` 也可由 Enter 直接触发。因此实施不能只包裹共享按钮，确认必须放在各 section 完成表单校验和 changed-value 计算之后、第一次写请求之前。

| 路由分组 | 保存实现 |
|---|---|
| Site | `general/system-info-section.tsx`; `maintenance/notice-section.tsx`; `maintenance/header-navigation-section.tsx`; `maintenance/sidebar-modules-section.tsx` |
| Auth | `auth/basic-auth-section.tsx`; `auth/oauth-section.tsx`; `auth/passkey-section.tsx`; `auth/bot-protection-section.tsx` |
| Billing | `general/quota-settings-section.tsx`; `general/pricing-section.tsx`; `integrations/payment-settings-section.tsx`; `general/checkin-settings-section.tsx` |
| Models | `models/global-settings-card.tsx`; `models/routing-reliability-section.tsx`; `models/gemini-settings-card.tsx`; `models/claude-settings-card.tsx`; `models/grok-settings-card.tsx`; `integrations/ionet-deployment-settings-section.tsx` |
| Security | `request-limits/rate-limit-section.tsx`; `request-limits/sensitive-words-section.tsx`; `request-limits/ssrf-section.tsx`; `request-limits/token-limit-section.tsx` |
| Content | `content/dashboard-section.tsx`; `content/chat-settings-section.tsx`; `content/drawing-settings-section.tsx` |
| Operations | `integrations/node-control-service-section.tsx`; `general/system-behavior-section.tsx`; `integrations/monitoring-settings-section.tsx`; `integrations/email-settings-section.tsx`; `integrations/worker-settings-section.tsx`; `maintenance/log-settings-section.tsx`; `maintenance/request-log-settings-section.tsx`; `maintenance/performance-section.tsx` |
| System tuning | `maintenance/business-stats-circuit-breaker-section.tsx`; `maintenance/ledger-pipeline-section.tsx`; `maintenance/relay-log-pipeline-section.tsx`; `maintenance/export-settings-section.tsx`; `maintenance/log-query-section.tsx`; `maintenance/log-export-section.tsx`; `maintenance/ledger-detail-section.tsx`; `maintenance/fallback-backfill-section.tsx` |

`PaymentSettingsSection` 的同一次 `Save all settings` 在 Waffo Pancake 绑定变化时还会调用专用保存接口；它仍属于同一个确认批次。

### Non-shared persistence actions

| 实现 | 持久化动作 | 处理方式 |
|---|---|---|
| `content/{announcements,api-info,faq,uptime-kuma}-section.tsx` | `Save Settings` 和即时 `Enabled` 开关 | 分别在 changed-value/目标值确定后请求通用确认 |
| `general/channel-affinity/index.tsx` | 多 key `Save` | 一次确认后执行原循环 |
| `system-tuning/hot-config-sections.tsx` | 四类 `PUT /api/option/group` 保存 | 每次整组保存只确认一次 |
| `models/ratio-settings-card.tsx` | 模型价格、分组倍率保存 | 每个用户触发的保存批次确认一次 |
| `models/thirdpartysd2-price-settings.tsx` | 第三方 SD2 价格保存 | 通用确认 |
| `models/tool-price-settings.tsx` | 工具价格保存 | 通用确认 |
| `models/upstream-ratio-sync.tsx` | `Apply Sync` | 有冲突用现有详情确认，无冲突用通用确认 |
| `auth/custom-oauth/components/provider-form-dialog.tsx` | 创建/更新 Provider | 表单校验通过后确认；删除仍用现有删除确认 |
| `features/profiling/components/profiling-controls.tsx` | 性能设置页内即时保存的 pprof 启用开关 | 目标值变化后通用确认；确认并保存后才刷新运行状态 |

当前未被 section registry 引用的 `content/json-toggle-section.tsx` 也接入统一机制，避免未来重新启用时绕过确认。

## Interaction contract

通用弹窗复用项目现有 `ConfirmDialog`（Base UI `AlertDialog`），不新增平行样式：

- 标题：`Confirm Changes`
- 说明：`Are you sure you want to save these changes?`
- 取消按钮：`Cancel`
- 确认按钮：`Save Changes`
- 普通保存使用默认按钮样式，不标记为 destructive。

交互顺序必须是：

1. 先执行现有客户端校验、JSON 解析、规范化和 changed-value 计算。
2. 校验失败或没有变化时，不显示确认弹窗，也不发送请求。
3. 将本次已校验的提交值/更新列表作为快照交给确认协调器；弹窗打开后不重新读取可变表单状态。
4. 取消、Esc 或关闭弹窗时，解析为取消；保留所有编辑内容，零写请求、零 query invalidation。
5. 确认后只执行一次已捕获的保存回调。保存期间禁用取消和重复确认，确认按钮显示 `Saving...`。
6. 成功后关闭弹窗，继续执行现有 toast、baseline/reset 和 React Query invalidation。
7. 失败后关闭弹窗但保留编辑内容，继续使用现有可操作错误提示，管理员可重新点击保存。

鼠标点击、键盘 Enter 提交以及即时保存开关都遵循同一契约。协调器同一时刻只接受一个待确认保存；快速双击或第二个并发请求不会替换第一个回调，也不会造成重复写入。

### Risk-specific copy

已有或已设计的高风险说明优先于通用说明，并且替代通用弹窗而不是追加第二个弹窗：

- 主库 `max_open_conns` 降到当前值一半以下时，说明当前活跃连接数，以及降低并发查询上限可能拖慢 AI 请求。
- relay log pipeline 从启用改为关闭时，说明新 relay 消费/错误日志会停止记录且无法事后补齐，但额度结算不受影响。
- 开启 relay log concurrent flush 时，说明可能增加 PostgreSQL 连接竞争；若两个风险条件同时出现，在一个弹窗中同时列出。
- 上游价格同步存在计费模式冲突时，继续显示现有冲突表格。

这些专项说明只改变确认文案，不改变已有保存载荷或服务端行为。

## Component design

新增 system-settings 专用确认协调器，挂在 `SettingsPageProvider` 内，并通过 hook 暴露类似以下契约：

```ts
type SettingsSaveConfirmationOptions = {
  title?: ReactNode
  description?: ReactNode
  confirmText?: ReactNode
}

requestSaveConfirmation(
  action: () => void | Promise<void>,
  options?: SettingsSaveConfirmationOptions
): Promise<boolean>
```

- `action` 保存在 ref 中，不把用户动作建模成 `state + effect`；确认按钮的事件处理器直接执行它。
- Promise 在取消时返回 `false`，在 action 成功完成后返回 `true`，action 抛错时保持原错误语义并完成弹窗清理。
- 待确认 resolver/action 在关闭、完成和 provider 卸载时清空，避免陈旧闭包或悬挂 Promise。
- 第二次并发请求立即按取消处理，不复用第一个 Promise，避免两个调用者在一次确认后都继续写入。
- context value 和公开回调保持稳定，避免确认状态让大型设置表单产生无意义的重渲染。

保存 handler 在已有校验和差异计算之后调用协调器，并把现有 API 调用、成功后的本地 baseline 更新放进同一个 action。这样一次多 key 保存只弹一次，Enter 与按钮点击也天然走同一条 handler。

## Data flow

```text
edit form/draft
  -> validate and normalize
  -> compute changed values
  -> no change / invalid: stop
  -> choose generic or risk-specific confirmation copy
  -> capture save snapshot
  -> cancel: preserve edits, no request
  -> confirm: execute existing write request(s) once
  -> existing success/error handling
  -> existing cache invalidation and form baseline update
```

确认层不缓存配置值，不重新排序多请求保存，也不把多个现有请求合并成新事务。支付设置的 option 写入与 Waffo Pancake 专用写入仍保持当前顺序和非原子语义。

## API contracts and authorization

不新增或修改 API。确认后继续使用现有契约：

- `PUT /api/option/`：`{ key, value, scope }`
- `PUT /api/option/group`：`{ module, values, scope }`
- `POST /api/option/waffo-pancake/save`：支付设置现有专用载荷
- `POST /api/custom-oauth-provider/`：创建 Provider
- `PUT /api/custom-oauth-provider/{id}`：更新 Provider

读取和写入仍由 `/system-settings/*` 的管理员路由保护，并继续使用现有细粒度 scope 编辑权限。只读用户看不到可执行保存按钮；前端确认不是权限边界，也不替代后端鉴权。

## Data model changes

无数据库表、字段、迁移或索引变化。现有 option 和 Custom OAuth Provider 存储保持不变。

## Config parameters

无新增配置参数或 feature flag。该改动只影响管理端保存交互，不接入 relay 执行流，因此不需要 relay-adjacent feature flag。

## Key business logic and edge cases

- 每次逻辑保存最多一个确认；多 key 循环不得逐 key 弹窗。
- 即时开关在确认成功且请求成功前不提交新的受控值；取消后视觉状态回到已保存值。
- 对同一表单的快速双击只保留第一次确认请求。
- 弹窗打开后路由卸载视为取消，不得延迟执行陈旧 action。
- 专项确认满足本次“保存前确认”，不能在其后再显示通用确认。
- 保存失败后的表单值、列表草稿和 dirty 状态必须可重试；确认层不能提前 reset baseline。
- 已有逐 key 保存发生部分成功时保持当前行为；本次不承诺事务性回滚。
- 长文案在移动端允许自然换行，继续使用主题 token，兼容浅色/深色模式。
- 新增系统设置持久化入口必须显式接入确认协调器；仅使用 `useUpdateOption` 不代表已经满足确认要求。

## Error handling strategy

- 取消不是错误，不显示 toast。
- 表单校验错误仍由现有 `FormMessage`/toast 显示，并发生在确认弹窗之前。
- 保存请求错误继续由现有 mutation/handler 显示经过用户化处理的消息；确认协调器只负责关闭、解锁和清理，不展示原始服务端或 JavaScript 错误。
- action 同步抛错和异步 reject 都必须走同一清理路径，不能让确认弹窗永久 loading。
- 已有 specialized dialog 的错误和 loading 行为保持不变。

实际实现中，HTTP/JavaScript reject 路径满足上述约束。项目现有 `useUpdateOption` 会把后端 `{success:false}` 作为 fulfilled mutation 返回，少数旧表单随后仍可能推进自身 baseline；这是本次改动前已存在的重试语义缺口，确认协调器不提前 reset，但也不在本次范围内改变全局 mutation 契约。

## i18n

通用说明新增一个 English-source key：

- `Are you sure you want to save these changes?`

风险专项文案若当前 locale 中不存在，也为 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi` 七种语言补齐。所有 locale 写入必须通过临时 `web/scripts/add-missing-keys.mjs`，随后运行 `bun run i18n:sync`；不得直接编辑 locale JSON。脚本完成后删除。

已有 `Confirm Changes`、`Save Changes`、`Cancel` 和 `Saving...` key 直接复用。

## Interaction with existing subsystems

- React Hook Form：确认放在 resolver/handler 校验成功和差异计算之后，不改变 schema。
- React Query：取消时不调用 mutation，因此不触发 invalidation；确认后沿用各 mutation 的现有 invalidation。
- Settings access：继续复用 `SettingsPageProvider` 的 scope/canEdit，不扩大权限。
- Existing dialogs：删除、重置、合规确认和冲突详情弹窗继续负责各自场景；确认协调器不得造成嵌套或连续弹窗。
- Upstream-owned frontend plumbing：不修改 `auth-session.ts`、`auth-session-sync.ts`、`http-client.ts`、`api.ts` 或 `server-error-message.ts`。

## Main Chain Impact

本功能只有管理端前端代码，没有代码在 AI relay goroutine 上同步或异步执行。

- 每个 relay 请求新增 DB 调用：0
- 每个 relay 请求新增 Redis 调用：0
- 每个 relay 请求新增锁、内存缓存或 goroutine：0
- 100k RPM 下的 relay 开销变化：0

管理员确认后发送的服务端请求数量与当前完全相同。部分被保存的配置会按既有机制影响后续 relay 行为，但本功能不修改配置发布、缓存读取、计费、限流或日志管线的实现。

## Shared Resource Audit

- Redis：无新 key、namespace 或调用。
- DB：仅在管理员确认后调用现有 option/Custom OAuth 写接口；无新表、查询、索引、事务或连接池。
- In-memory：仅每个已打开设置页面持有一个待确认 action/resolver ref 和少量弹窗状态，不与 relay 共享。
- Connection/goroutine pools：无新增连接或 worker；取消反而避免原本将发生的保存请求。
- React Query cache：只在实际保存成功后由现有逻辑失效；取消路径不触碰 cache。

## Test plan

仓库当前没有配置前端测试 runner，本次不新增测试框架。实施前以本节作为验收用例，完成后用类型、静态检查、构建和浏览器网络面板验证。

| 技术 | 用例 | 预期 |
|---|---|---|
| 等价类 | 有变化 / 无变化 / 非法输入 | 仅“有变化且有效”显示确认 |
| 决策覆盖 | 取消 / 确认 / 关闭 / Esc | 只有确认发送写请求 |
| 条件覆盖 | 单 key / 多 key / Waffo 额外写入 | 每个逻辑批次只显示一次确认，请求数与当前相同 |
| 路径覆盖 | 点击保存 / Enter / 即时开关 | 三条入口行为一致 |
| 并发 | 快速双击、确认中再次触发 | 只执行一个 action、无重复请求 |
| 错误路径 | 第一个请求失败 / 多 key 中途失败 | 弹窗退出 loading，编辑值保留，可重试，现有错误反馈可见 |
| 专项分支 | DB pool 大幅下调；relay log 两个风险开关 | 一个弹窗显示对应风险说明 |
| 专项分支 | 上游同步有冲突 / 无冲突 | 分别显示冲突详情 / 通用确认，均只一次 |
| 权限 | 只读 scope / 可编辑 scope | 只读无保存动作；可编辑需确认 |
| 展示 | 桌面/移动端、明/暗主题、七语言 | 焦点、换行、按钮和主题均正常 |

静态搜索还需再次覆盖本清单中的所有 `updateSystemOption`、`updateSystemOptionGroup`、`updateOption.mutate*`、Provider create/update 和 Waffo save 调用，确认没有可达持久化入口绕过协调器。

实施后的验证命令：

```powershell
git diff --check

Set-Location web
bun run i18n:sync
bun run typecheck
bun run lint
bun run format:check
bun run build

Set-Location ..
go test ./...
```

若全量 lint 被存量问题阻塞，另外对本次改动的 TS/TSX 文件运行定向 `bunx oxlint`，并明确记录全量失败与本次改动的关系。实现时必须保留当前工作树中已有的 relay-timeout/system-tuning 和 locale 修改。

## Implementation notes

实际实现与本设计一致：

- 新增 `web/src/features/system-settings/components/settings-save-confirmation.tsx`，用稳定的 context 回调和 ref 保存单个待确认 action；取消返回 `false`，确认期间锁定弹窗，action 失败沿用原异常语义并完成清理。
- `SettingsPageProvider` 统一挂载协调器；`use-settings-form.ts` 识别取消返回值，取消时不推进 baseline 或 reset 表单。
- 本文入口清单中的 41 个可达共享表单、4 个内容区的整体保存与即时开关、性能页 pprof 即时开关、渠道亲和性、四类价格/倍率保存、上游价格同步、自定义 OAuth Provider 创建/更新，以及当前未注册的 `json-toggle-section.tsx` 均已接入。每个逻辑保存批次只确认一次。
- 数据库连接池大幅下调和 Relay 日志管道风险使用专项说明；上游价格同步的既有冲突弹窗、价格重置、删除、清理和支付合规等既有专项确认未叠加通用弹窗。
- Waffo Pancake 的必填校验移到确认前，普通 option 与专用 Waffo 写入仍保持原写入顺序并共享一次确认。Hot Config 增加本地保存 baseline，使刚保存的相同值不会再次弹窗。
- 新增通用说明和三条风险说明的七语言翻译；locale 写入由临时 `add-missing-keys.mjs` 完成，随后执行 `bun run i18n:sync` 并删除临时脚本。
- 现有工具价格验证测试已补上协调器 Provider，`bun test` 通过。`bun run typecheck`、`bun run build`、定向 Oxlint/格式检查、`git diff --check` 和 `go test ./...` 均通过。全量 lint 与全量格式检查仍被工作树中既有问题阻塞，本次新增和直接修改文件的定向检查无新增问题。
- 持久化入口复扫未发现遗漏。审计同时确认了上述 `useUpdateOption` 对 `{success:false}` 的存量 baseline 语义；为避免把保存确认需求扩大成全局 mutation 错误契约重构，本次保留原行为并在本文记录。

本次没有新增或修改后端 API、数据库结构、配置参数或 Relay 主链代码；Main Chain Impact 与 Shared Resource Audit 的结论保持不变。
