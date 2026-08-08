# 系统关键设置整理与加固

日期：2026-08-07

状态：设计待确认

## Goals and scope

管理后台 `/system-settings/*` 下现有 8 个分组、51 个 scope。这些 scope 里既有「改错了只是首页文案难看」的设置，也有「改错了管理员自己登不进来、或全站 AI 中继立刻中断」的设置，但两者在界面上和保存流程上完全同构。本次目标是把后者识别出来、在原地标记出来，并让它们的保存动作产生与风险相称的阻力，从而降低误触概率。

范围：

- 建立一份**关键设置登记表**，作为「哪些 option key 是关键设置」的唯一事实来源。
- 在现有通用保存确认之上引入**三档确认**：普通档沿用现状；敏感档在弹窗内列出「字段：旧值 → 新值」；关键档使用破坏性样式并附具体风险说明。
- 给全部 `type='number'` 输入加**滚轮防护**，消除「滚动页面时静默改数」这一最直接的误触路径。
- 把**未保存离开提醒**从现在的 4 个 section 铺到全部 section。
- 在关键字段**原地**加视觉标记，不改变任何设置的所属分组、路由或位置。

非目标：

- 不移动任何设置的位置，不新增或拆分路由，不改动 `service/settingsaccess` 的 scope 划分与权限映射。
- 不改变任何设置值的语义、取值范围、校验规则或生效方式。
- 不新增后端接口、数据库结构、配置项或 feature flag。
- 不引入需要手动输入确认文本的档位（评估后认为对当前规模是过度阻力，若后续需要可在同一登记表上追加档位）。
- 不把前端确认当作权限边界；后端鉴权与范围校验保持不变。

## 现状与问题

### 已有的防护

| 机制 | 覆盖 | 位置 |
|---|---|---|
| 通用保存确认 | 全部 51 个 scope 的持久化入口 | `components/settings-save-confirmation.tsx` |
| 专项风险文案 | 仅 3 处 | DB 连接池腰斩、Relay 日志管道关闭、并发刷盘开启 |
| 逐 scope 编辑权限 | 全部 | `canEditSystemSettingsScope` + `SettingsPageFrame` 的 `fieldset disabled` |
| 后端范围校验 | hot-config 四组 | `ValidateRateLimitSetting` / `ValidateDBPoolSetting` / `ValidateUserSessionSetting` / `ValidateRelayTimeoutSetting` |
| 风险确认弹窗组件 | 仅支付合规 | `components/risk-acknowledgement-dialog.tsx` |

### 待解决的误触路径

**P1 — 确认弹窗无差别，因此不携带信息。**
改站点页脚和把全站 API 限流压到 1 次 / 1200 秒，弹出的是同一句「Are you sure you want to save these changes?」。管理员会形成条件反射式点击，危险改动与文案改动在确认这一步无法区分。

**P2 — 弹窗不展示将要写入的内容。**
`settings-save-confirmation.tsx:140-144` 只渲染固定标题与描述。即使管理员在确认时保持警惕，也看不到本次实际会改哪些字段，所以确认这一步无法拦下前一步的误操作。

**P3 — 滚轮静默修改数值。**
`web/src/features/system-settings` 下有 82 个 `type='number'` 输入，分布在 39 个文件；全项目 `onWheel` 只在 3 个 combobox 里出现，`components/ui/input.tsx` 没有任何防护。数值输入获得焦点后，滚动页面就会改值且无任何提示。Relay 日志管道单页 17 个数值字段、日志导出 26 个 key，都是长滚动页面，这是当前最直接的误触来源。P3 造成的错误只有 P2 修好之后才能在确认这一步被发现。

**P4 — 关键字段没有任何视觉区分。**
`hot-config-sections.tsx:216-320` 用同一个循环渲染 `global_api_num`（改错导致管理员自锁）和 `search_num`（改错只是搜索变慢），两者的标签、输入框、间距完全一致。

**P5 — 未保存离开提醒覆盖 4/51。**
只有 `oauth-section`、`pricing-section`、`quota-settings-section`、`system-info-section` 挂了 `FormNavigationGuard`。在 Gateway Rate Limiting 或 AI Request Timeout 页改了一半切走，改动静默丢弃，不一致本身也会误导管理员。

### 关键设置审计结果

以下改动均在后端校验允许的范围内可达，即后端不会拦截：

| 设置 | 合法范围 | 处于边界时的后果 |
|---|---|---|
| `rate_limit_setting.global_api_num` + `global_api_duration_sec` | 1..100000 / 1..1200 | `GlobalAPIRateLimit()` 挂在整个 `/api`（`router/api-router.go:25`）和 dashboard（`router/dashboard.go:14`）上。压到 1 次 / 1200 秒后，管理后台自身也被限流，**管理员自锁** |
| `rate_limit_setting.auth_refresh_num` / `auth_refresh_ip_num` / `auth_refresh_duration_sec` | 1..100000 / 1..1200 | token 刷新被限流，所有后台会话在下次刷新时掉线 |
| `rate_limit_setting.critical_*` | 同上 | 登录、注册、找回密码、支付下单被限流 |
| `rate_limit_setting.global_web_*` | 同上 | 后台静态资源被限流，页面打不开 |
| `relay_timeout_setting.response_timeout_seconds` / `total_timeout_seconds` | 0..604800 | 设成 1，**每个 AI 请求都会被中止**，全站中继中断。当前无任何专项提示 |
| `relay_timeout_setting.enabled` | 开关 | 关闭后超时不再生效，挂死的上游连接会持续累积 |
| `user_session_setting.active_limit` | 1..10000 | 调低会驱逐既有会话 |
| `db_pool_setting.max_open_conns` | 1..100000 | 调低拖慢 AI 请求（已有腰斩提示，但仅覆盖降幅超一半的情况） |
| `PasswordLoginEnabled` | 开关 | 关闭且未配置可用的 OAuth/Passkey 时，无人能登录 |
| `fetch_setting.enable_ssrf_protection` 关 / `allow_private_ip` 开 | 开关 | SSRF 防护降级 |
| `QuotaPerUnit` | 数值 | 是 quota 与 USD 的换算基数（`common/quota.go:12`），改动会重新定义所有已存余额和价格的显示口径 |
| `billing_setting.billing_mode` | 枚举 | 切换计费模式，立即作用于线上流量 |
| `LogConsumeEnabled` | 开关 | 关闭后消费日志停止记录 |
| `relay_log_pipeline_setting.enabled` | 开关 | 关闭后中继日志停止记录且无法事后补齐（已有提示） |
| `AutomaticDisableChannelEnabled` / `ChannelDisableThreshold` | 开关 / 数值 | 阈值过低会批量自动禁用渠道 |
| `ModelRequestRateLimit*` | 数值 | 调紧直接影响终端用户可用性 |

**恢复路径的事实**：`relay_timeout_setting.go:79` 的注释明确说明「Saved DB options still win during ConfigManager.LoadFromDB」，`ApplyRateLimitEnvDefaults` / `ApplyUserSessionEnvDefaults` 同构。也就是说，环境变量只是**启动期兜底**，一旦某个值被保存进 DB，重启并设置环境变量**不能**把它救回来，唯一恢复手段是直接改数据库 `options` 表。这一点必须写进关键档的风险文案里，因为它决定了管理员是否值得多花五秒确认。

## Component design

### 1. 关键设置登记表

新增 `web/src/features/system-settings/critical-settings.ts`，按 option key 索引。这是本次「整理」的落点：回答「哪些是关键设置」不再需要读 15 个 section 组件。

```ts
export type CriticalTier = 'sensitive' | 'critical'

export type SettingChange = {
  key: string
  from: unknown
  to: unknown
}

export type CriticalSettingRule = {
  /** English-source i18n key，用于变更清单与字段标记 */
  label: string
  /** 该 key 发生任何变化时的基础档位 */
  tier: CriticalTier
  /** 命中时把本次变更提升到 critical，并展示 risk 文案 */
  escalate?: (change: SettingChange) => boolean
  /** English-source i18n key，仅在本次变更落到 critical 时展示 */
  risk?: string
  /** 变更清单里的值格式化，例如 0 -> "Unlimited" */
  format?: (value: unknown) => string
}

export const CRITICAL_SETTINGS: Record<string, CriticalSettingRule>
```

升级判定统一用「是否收紧」表达，只依赖单个 key 的 from/to，不需要跨字段读取当前表单状态：

- `*_num`：`to < from` 即收紧。
- `*_duration_sec`：`to > from` 即收紧（窗口变长、配额不变 = 更紧）。
- `*_enabled`（限流类）：`false → true` 即收紧。
- `relay_timeout_setting.enabled`：任一方向都升级——关闭意味着超时不再生效，开启意味着请求可能被切断。
- `relay_timeout_setting.*_seconds`：`to !== 0 && (from === 0 || to < from)` 升级；`to === 0`（不限）留在敏感档。
- `db_pool_setting.max_open_conns`：`to < from / 2` 升级（沿用现有阈值），其余降低留在敏感档。
- `user_session_setting.active_limit`：`to < from` 升级。
- 开关型关键设置：`PasswordLoginEnabled`、`LogConsumeEnabled`、`relay_log_pipeline_setting.enabled`、`fetch_setting.enable_ssrf_protection` 在 `true → false` 时升级；`fetch_setting.allow_private_ip` 在 `false → true` 时升级。
- `QuotaPerUnit`、`billing_setting.billing_mode`：任何变化都是关键档。

未登记的 key 即普通档，行为与现在完全一致。这保证本次改动是纯增量的：不动登记表就不动任何现有交互。

### 2. 确认协调器扩展

`requestSaveConfirmation` 增加一个可选入参，签名保持向后兼容：

```ts
requestSaveConfirmation(
  action: () => void | Promise<void>,
  options?: SettingsSaveConfirmationOptions & { changes?: SettingChange[] }
): Promise<boolean>
```

协调器内部按登记表解析本次批次：

```
resolveConfirmation(changes) -> {
  tier: 'normal' | 'sensitive' | 'critical'   // 取批次内所有已登记 key 的最高档
  rows: Array<{ label, from, to }>            // 仅已登记 key，用于变更清单
  otherCount: number                          // 未登记 key 的数量
  risks: string[]                             // critical 行的风险文案，按文案去重
}
```

三档渲染：

| 档位 | 弹窗 | 内容 |
|---|---|---|
| normal | 现有 `ConfirmDialog`，默认按钮 | 与现状完全一致 |
| sensitive | 现有 `ConfirmDialog`，默认按钮 | 通用描述 + 变更清单；未登记 key 汇总成「另有 N 项修改」 |
| critical | 现有 `ConfirmDialog`，`destructive` 按钮 | 风险说明（可多条）+ 变更清单 |

调用方显式传入的 `title` / `description` / `confirmText` 优先级最高，覆盖登记表推导出的文案。这样 `relay-log-pipeline-section.tsx` 与 `hot-config-sections.tsx` 里已有的专项文案不会被降级，也不会出现两个弹窗。这两处的现有 `getConfirmationOptions` 逻辑迁移进登记表后即可删除，行为等价。

### 3. 变更清单的数据来源

`use-settings-form.ts` 的 `handleSubmit` 已经算出了 `changedEntries`，其中 `baselineRef.current[key]` 就是旧值。在这里顺带构造 `changes` 并作为第三个参数传给 `onSubmit`，约 30 个使用该 hook 的 section 无需逐个改动即可获得变更清单。

其余入口手动构造：`ConfigGroupSection`（一处覆盖 gateway-rate-limit / relay-timeout / database-pool / login-session-policy 四个关键分区）、`basic-auth-section`、`relay-log-pipeline-section`、`ledger-pipeline-section`、`ssrf-section`、`routing-reliability-section`、`log-settings-section`、以及 `ratio-settings-card` 里涉及 `QuotaPerUnit` / `billing_setting.billing_mode` 的保存路径。

值的展示规则：布尔渲染为「已启用 / 已关闭」；`format` 存在时用 `format`；其余用 `String(value)`，超长值（如 JSON 倍率表）截断到 80 字符并标注省略。变更清单只做展示，不参与任何写入逻辑。

### 4. 滚轮防护

改 `web/src/components/ui/input.tsx`：`type === 'number'` 且输入处于焦点时，`onWheel` 里调用 `blur()`，同时透传调用方自带的 `onWheel`。

- 用 `blur()` 而不是 `preventDefault()`：React 的 wheel 监听在根节点上，`preventDefault` 行为在不同版本下不可靠；`blur()` 让浏览器停止修改数值并让页面正常继续滚动，是通行做法。
- 该组件是全局 UI 原语，改动会覆盖后台所有数值输入，不止系统设置。这是期望效果——滚轮改数在任何表单里都是缺陷。
- 对键盘输入、点击、上下箭头、粘贴无任何影响，因此不改变现有校验与 dirty 语义。
- `components/ui/input.tsx` 不在 CLAUDE.md Rule 6 的上游托管文件清单内，可以直接修改。

### 5. 未保存离开提醒铺开

不逐个 section 挂 `FormNavigationGuard`（会挂出 51 个 blocker 和 51 个 `beforeunload` 监听）。改为：

- 新增 `hooks/use-settings-dirty-registration.ts`，section 用它上报自己的 dirty 状态；context 内部维护一个计数。
- `use-settings-form.ts` 自动注册，覆盖约 30 个 section。
- 其余自建表单的 section 手动调用一行。
- `SettingsPageFrame` 挂唯一一个 `FormNavigationGuard`，条件为「存在任一脏表单」。
- 删除 4 个 section 里现有的局部 `FormNavigationGuard`，避免双重弹窗。

### 6. 原地视觉标记

新增 `components/critical-setting-marker.tsx`，按登记表渲染：

- 关键档：字段标签旁一枚 `destructive` 色 `Badge`，文案「关键」，`title` 属性给出该字段的风险摘要。
- 敏感档：`secondary` 色 `Badge`，文案「敏感」。
- 含登记 key 的 section 顶部加一行 muted 说明，指明本分区包含需谨慎修改的项。

全部使用现有 `Badge` / `Alert` 组件与主题 token，不新增平行样式，不改变字段顺序、分组归属、路由或权限 scope。

## Data flow

```text
编辑表单 / 草稿
  -> 现有校验、JSON 解析、规范化
  -> 现有 changed-value 计算
  -> 无变化或非法：停止，不弹窗、不发请求（与现状一致）
  -> 构造 changes: [{key, from, to}]
  -> 按 CRITICAL_SETTINGS 解析 tier / rows / risks
  -> 调用方显式文案覆盖推导文案（若有）
  -> 渲染对应档位的确认弹窗
  -> 取消：保留全部编辑内容，零写请求、零 invalidation
  -> 确认：执行既有写请求，顺序与数量完全不变
  -> 现有 toast、baseline 更新、React Query invalidation
```

确认层不缓存配置值，不重排多请求保存，不合并请求，不引入事务。

## API contracts

不新增、不修改任何后端接口。确认后仍使用现有契约：`PUT /api/option/`、`PUT /api/option/group`、`POST /api/option/waffo-pancake/save`、Custom OAuth Provider 的创建与更新。

前端确认不是权限边界。读写仍由管理员路由与 `service/settingsaccess` 的逐 scope 编辑权限保护，只读用户看不到保存动作。

## Data model changes

无。不新增表、字段、索引或迁移。

## Config parameters

无新增配置项或 feature flag。改动全部位于管理端前端，不接入 relay 执行流，因此不需要 relay-adjacent feature flag。

## Key business logic and edge cases

- 一次逻辑保存最多一个确认；多 key 循环写入不得逐 key 弹窗。这条继承自现有设计，本次不放宽。
- 批次档位取批次内所有已登记 key 的最高档；一次保存里既有关键项又有普通项时，按关键档处理并只列出已登记项的明细。
- 风险文案按文案文本去重：`global_api_num` 与 `global_api_duration_sec` 同时收紧时，自锁风险只说一次。
- 未登记 key 不进明细，只计入「另有 N 项修改」，避免把整张模型倍率表铺进弹窗。
- 已有专项文案（DB 池腰斩、Relay 日志管道）迁入登记表，迁移前后文案与触发条件保持一致，不叠加第二个弹窗。
- 上游价格同步的计费模式冲突弹窗、删除 Provider、清理日志、重置价格、支付合规确认继续走各自既有确认，本次不叠加。
- 取消、Esc、关闭、路由卸载一律视为取消，不得延迟执行陈旧 action。
- 保存失败后表单值、草稿、dirty 状态必须可重试，确认层不提前推进 baseline。
- 滚轮防护只在输入已获得焦点时 `blur`；未获焦点的数值输入本来就不会被滚轮修改，不做多余处理。
- dirty 计数在 section 卸载时必须回收，否则会留下永久阻塞导航的幽灵状态。
- 新增系统设置持久化入口时，若涉及登记表内的 key，必须传 `changes`；只调用 `useUpdateOption` 不代表已满足要求。

## Error handling strategy

- 取消不是错误，不显示 toast。
- 表单校验错误仍由现有 `FormMessage` / toast 处理，且发生在确认弹窗之前。
- 保存请求错误继续由现有 mutation / handler 展示用户化消息；确认协调器只负责关闭、解锁与清理，不展示原始服务端或 JavaScript 错误。
- action 同步抛错与异步 reject 走同一清理路径，不允许弹窗停留在 loading。
- 登记表查不到某个 key 时按普通档处理，不抛错、不阻断保存——登记表缺项只应降低提示强度，绝不能让设置保存不了。
- `format` 抛错时回退到 `String(value)`，同样不阻断保存。

## Interaction with existing subsystems

- React Hook Form：确认仍在 resolver 校验通过与差异计算之后，schema 不变。
- React Query：取消不触发 mutation，因此不触发 invalidation；确认后沿用各 mutation 现有的 invalidation。
- Settings access：继续复用 `SettingsPageProvider` 的 scope / canEdit，不扩大权限，不改 `service/settingsaccess/scopes.go`。
- 既有弹窗：删除、重置、合规、冲突详情各司其职，确认协调器不得造成嵌套或连续弹窗。
- 上游托管前端文件：不修改 `auth-session.ts`、`auth-session-sync.ts`、`http-client.ts`、`api.ts`、`server-error-message.ts`。
- 与 `system-settings-save-confirmation.md` 的关系：本设计是它的增量扩展，不推翻其任何结论；该文档记录的「每个逻辑批次只确认一次」「取消不推进 baseline」等约束在本次继续成立。

## Main Chain Impact

本功能只包含管理端前端代码，没有任何代码在 AI relay goroutine 上同步或异步执行。

- 每个 relay 请求新增 DB 调用：0
- 每个 relay 请求新增 Redis 调用：0
- 每个 relay 请求新增锁、内存缓存或 goroutine：0
- 100k RPM 下 relay 开销变化：0

管理员确认后发出的服务端请求，数量、顺序、载荷与当前完全一致。被保存的配置仍按既有机制影响后续 relay 行为，本功能不修改配置发布、缓存读取、计费、限流或日志管线的任何实现。

反向收益：`relay_timeout_setting` 与 `rate_limit_setting` 是直接作用于主链的 hot config，本次为它们补上了目前完全缺失的风险提示，降低了误配导致全站中继中断的概率。

## Shared Resource Audit

- Redis：无新 key、namespace 或调用。
- DB：仅在管理员确认后调用现有 option 写接口；无新表、新查询、新索引、新事务、新连接。
- 内存：每个已打开的设置页面额外持有一个待确认 action ref、一份变更清单快照和一个 dirty 计数，均为页面级、与 relay 不共享。
- 连接池 / goroutine 池：无新增；取消路径反而避免了原本会发生的写请求。
- React Query 缓存：只在保存成功后由现有逻辑失效，取消路径不触碰。

## i18n

新增 English-source key，需补齐 en、zh、zh-TW、fr、ru、ja、vi 七种语言。按 Rule 6，直接写入 `web/src/i18n/locales/{lang}.json` 的 `translation` 对象以保持 diff 收敛，不使用临时脚本、不跑全量 `i18n:sync` 回填无关 key。

需要新增的类别：

- 变更清单外壳：「以下 N 项将被修改」「另有 {{count}} 项修改」「已启用」「已关闭」。
- 字段标记：「关键」「敏感」以及分区顶部的谨慎修改提示。
- 登记表内每个 key 的 `label`（复用各 section 现有标签文案，尽量不新增）。
- 关键档风险文案，逐条对应审计表，其中必须包含「该值保存后环境变量无法覆盖，只能改数据库恢复」这一事实。

已有的 `Confirm Changes`、`Save Changes`、`Cancel`、`Saving...` 直接复用。

## Test plan

前端无测试 runner（Rule 15.7），不新增框架。本节作为验收用例，实施后用类型检查、静态检查、构建与浏览器验证。

| 技术 | 用例 | 预期 |
|---|---|---|
| 等价类 | 只改未登记 key / 只改敏感 key / 只改关键 key | 分别落到普通、敏感、关键三档 |
| 边界值 | `max_open_conns` 恰好 `from/2`、略高于、略低于 | 只有低于时升级到关键档 |
| 边界值 | `response_timeout_seconds` 由 0 改为非 0；由大改小；改为 0 | 前两者关键档，改为 0 为敏感档 |
| 条件覆盖 | `*_num` 调低 / 调高；`*_duration_sec` 调高 / 调低 | 仅收紧方向升级 |
| 判定覆盖 | 取消 / 确认 / 关闭 / Esc | 只有确认发送写请求 |
| 路径覆盖 | 点击保存 / 键盘 Enter / 即时开关 | 三条入口档位一致 |
| 组合 | 同一批次混合普通 + 关键 key | 取关键档，明细只列已登记项，其余汇总为计数 |
| 去重 | `global_api_num` 与 `global_api_duration_sec` 同时收紧 | 自锁风险文案只出现一次 |
| 回归 | DB 池腰斩、Relay 日志管道关闭 / 并发刷盘 | 文案与触发条件与迁移前一致，仍是单个弹窗 |
| 并发 | 快速双击、确认执行中再次触发 | 只执行一个 action，无重复请求 |
| 错误路径 | 首个请求失败 / 多 key 中途失败 | 弹窗退出 loading，编辑值保留可重试 |
| 滚轮 | 数值输入获焦后滚动页面 | 值不变，页面正常滚动；键盘与箭头调值不受影响 |
| 导航 | 任一 section 改动后切路由 / 关标签页 | 提示一次，不重复；section 卸载后不再阻塞 |
| 权限 | 只读 scope / 可编辑 scope | 只读无保存动作；标记仍展示 |
| 展示 | 桌面 / 移动端、明暗主题、七种语言 | 焦点、换行、按钮、徽章配色正常 |

实施后的验证命令：

```powershell
git diff --check

Set-Location web
bun run typecheck
bun run lint
bun run format:check
bun run build

Set-Location ..
go test ./...
```

后端无改动，`go test ./...` 用于确认工作树中既有的 relay-timeout 等改动未被波及。若全量 lint 或格式检查被工作树存量问题阻塞，另对本次改动文件做定向 `bunx oxlint`，并明确记录全量失败与本次改动无关。

## 实施顺序

1. `critical-settings.ts` 登记表 + 单元级别的档位判定逻辑。
2. 确认协调器扩展与变更清单渲染；把现有两处专项文案迁入登记表。
3. `use-settings-form.ts` 自动构造 `changes` 与自动注册 dirty。
4. `ConfigGroupSection` 等手动入口接入 `changes`。
5. `components/ui/input.tsx` 滚轮防护。
6. 页面级 `FormNavigationGuard` + 移除 4 处局部 guard。
7. 原地视觉标记。
8. 七语言文案。
9. 全量验证。

每一步都可独立回退：不动登记表，则全部行为与现状一致。
