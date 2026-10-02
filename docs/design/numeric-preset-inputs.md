# 特殊值数字配置改为可输入的下拉框

状态：已实施（2026-10-01），范围见 §7；实际字段与取值见 §8
日期：2026-10-01

## 1. 目标

后台有 38 个数字配置项，其中 0 或 -1 代表特殊含义（继承默认、不限制、关闭、永久保留等），目前全部是普通数字输入框，特殊含义只写在说明文字里，容易填错或看不懂。

改为**可输入的下拉框**：下拉中列出带文字说明的特殊值（如「0 · 继承系统默认」「-1 · 不限制」），选中即填入；同时可以直接输入其他数字。盘点结果中没有「只允许固定几个值」的字段，全部属于「特殊值 + 自由数字」，统一使用可输入下拉框。

纯前端改动：不改后端、接口、数据库与保存格式（提交的仍是原来的数字）。

## 2. 组件设计

两个本分支新文件，不改 main 的 `components/ui/*`：

- `web/src/components/numeric-preset-input.tsx`：`NumericPresetInput`，基于现有 `components/ui/combobox-input.tsx`（`ComboboxInput`），下拉、选项、高亮样式全部沿用它，不新造样式。
- `web/src/components/numeric-preset-field.ts`：react-hook-form 适配 `numericPresetFieldProps(field)`，与 `system-settings/utils/numeric-field.ts` 的 `safeNumberFieldProps` 用法对应（单独成文件：组件文件只能导出组件，否则 oxlint `only-export-components` 报错）。

```ts
type NumericPreset = { value: number; label: string } // label 是 i18n 键，组件内 t() 翻译
type NumericPresetInputProps = {
  value: number | string | null | undefined           // 数字或数字字符串（分组重试、员工门槛的状态是字符串）
  onChange: (value: number) => void                   // 每次编辑都发框里的内容：合法值，或越界数 / NaN（不完整的数字），见下
  onBlur?: () => void
  presets: readonly NumericPreset[]                    // 特殊值，下拉中显示为「说明（值）」
  min: number; max?: number                            // 普通取值范围；特殊值不受其限制
  decimals?: boolean                                   // 允许小数（员工门槛）
  placeholder?: string; disabled?: boolean; id?: string; className?: string
  'aria-invalid'?: …; 'aria-describedby'?: …; 'data-form-root'?: …  // 由共享 FormControl 注入
}
```

RHF 用法：`<FormControl><NumericPresetInput presets={…} min={1} {...numericPresetFieldProps(field)} /></FormControl>`。`FormControl` 注入的 `id`、`aria-describedby`、`aria-invalid`、`data-form-root` 落到内部输入框上，`FormLabel` 的点击聚焦、表单错误关联、提交失败时聚焦到出错字段都不变。

行为：

- 当前值等于某个特殊值且下拉关闭时，输入框显示「说明（值）」，如英文「Unlimited (0)」、中文「无限制（0）」（格式本身走 i18n 键 `{{label}} ({{value}})`，中日文用全角括号）；否则显示数字。
- 获得焦点即打开下拉，**列出全部特殊值**（键入数字后只列出值以该数字开头的特殊值，如键入 `-` 仍列出 `-1`；不按说明文字匹配，否则在「默认 300 秒（0）」的字段键入 300 会列出它，回车就存成 0）；没有匹配项且所键入的是合法普通值时，下拉显示「自定义值：{{value}}」，否则显示「没有匹配的预设选项」。下拉底部常驻范围提示「或输入 1–100 之间的数字」。实现上让 `ComboboxInput` 以选择器模式运行（`allowCustomValue={false}`）：它在自由输入模式下会用当前值预填搜索词，当前值为普通数字（如 300）或为 0 时会把其他特殊值过滤掉；选择器模式打开时搜索词为空，当前值作为占位提示显示。键入的文字从冒泡的 change 事件读取。
- 选中特殊值后焦点留在输入框、显示说明文字；此时键入、删除或粘贴会先全选说明文字再替换，不会接在说明文字后面。
- 输入只接受数字；负号仅在 `min < 0` 或存在负数特殊值时可输入，小数点仅在 `decimals` 时可输入（按键与粘贴都拦截）。
- 输入过程中每次编辑都把框里的内容交给调用方：合法值（特殊值或 `[min, max]` 内的数；整数字段还要求是安全整数）原样；越界数原样；不是完整数字的文字（如单独的 `-`、清空）为 NaN。表单状态因此始终与界面一致，由调用方原有的保存校验拦下非法值（`ConfigGroupSection` 提示「请输入有效数字」、zod、分组重试的行校验、价格监控禁用保存、员工门槛的提示）。若只把合法值交出去、非法中间态留在组件里，表单会持有输入途中最后一个合法的前缀——在上限 100 的字段键入 200，表单里是 20，点保存（或回车提交）会静默写入 20——所以不这样做。
- 失焦提示：文字不合法时保留原文字、输入框标红（`aria-invalid`，沿用 `Input` 的 destructive 样式），下方显示「请选择列表中的选项，或输入 {{min}}–{{max}} 之间的数字」（无上限时为「…不小于 {{min}} 的数字」），并通过 `aria-describedby` 关联。提示等到失焦才出现，避免键入 `-1` 时在 `-` 这一步就标红。所在 RHF 表单已对该字段报错（`FormControl` 注入 `aria-invalid`）时不再重复显示组件自己的提示。父组件把值改成别的数（表单重置、保存后回读）时自动丢弃这段草稿。
- 键盘：上下键选择、回车确认、Esc 关闭（沿用 `ComboboxInput`）；下拉打开时按 Esc 额外恢复到获得焦点前的值（即使该值超出当前范围，如日志查询里早先存下的超大值，也原样恢复）。`ComboboxInput` 自身不会在 Tab 离开时关闭下拉，组件在失焦且下拉仍开着时重新挂载它来关闭（副作用：下拉开着时切走窗口再切回，焦点不会回到输入框）。
- 深浅色与尺寸沿用 `Input` / `ComboboxInput` 的 token（`bg-popover`、`text-muted-foreground`、`text-destructive`、`border`），不硬编码颜色（Rule 6）。

`hot-config-sections.tsx` 的 `ConfigGroupField` 与各维护分区的字段定义数组新增可选 `presets` 键：有 `presets` 时渲染 `NumericPresetInput`，否则仍是原来的数字输入框。带 `presets` 的字段把 `min` 改为普通取值的下限（如 1），特殊值另由 `presets` 放行；`ConfigGroupSection` 保存前的校验相应改为「特殊值或 `[min, max]`」，可保存的取值集合与改动前一致。`ConfigGroupSection` 只在服务端值真正变化（按序列化比较）时重置表单：父组件每次渲染都会重建 `defaults` 对象（如连接池分区在表单显示后才拿到连接池状态），按引用比较会把未保存的修改清掉。

## 3. 字段清单（38 个）

「文件」一列：**fork** = 本分支独有文件；**main** = main 也有的文件（会增加以后合并上游的冲突面，见 §5）。

| # | 页面 / 字段 | 特殊值 | 其他取值 | 文件 |
|---|---|---|---|---|
| 1–4 | 用户编辑 › 流式 / 非流式 响应、总时长超时 | 0 继承系统默认、-1 不限制 | 1–604800 秒 | main |
| 5 | 用户编辑 › 重试次数 | 0 跟随分组 / 系统、-1 不重试 | 1–20 | main |
| 6 | 系统调优 › 分组重试次数 | 0 跟随系统、-1 不重试 | 1–20 | fork |
| 7–8 | 系统调优 › AI 请求超时：响应 / 总时长 | 0 不限制 | 1–604800 秒 | fork |
| 9 | 同上 › 重试最小剩余预算 | 0 关闭 | 1–604800 秒 | fork |
| 10 | 同上 › 单请求最大尝试次数 | 0 不限制 | 1–100 | fork |
| 11–12 | 系统调优 › 日志库连接池 空闲 / 最大 | 0 继承主库 | 1–10000 / 1–100000 | fork |
| 13 | 账本管道 › 成本队列每轮上限 | 0 每轮全部写入 | ≥1 | fork |
| 14 | 账本管道 › 成本队列批大小 | 0 继承结算批大小 | ≥1 | fork |
| 15 | 账本管道 › 缓存 TTL 抖动 | 0 不抖动 | 1–100 % | fork |
| 16 | 补写回填 › 刷盘间隔 | 0 仅按批满刷盘 | ≥1 秒 | fork |
| 17 | 日志查询 › 当前小时缓存秒数 | 0 实时统计 | ≥1 秒 | fork |
| 18 | 日志查询 › 启动预计算小时数 | 0 只算之后完成的小时 | ≥1 | fork |
| 19 | 日志导出 › 并发任务数 | 0 停止接收新任务 | 1–16 | fork |
| 20 | 日志导出 › CPU 硬上限 | 0 紧急暂停 | 1–100 % | fork |
| 21 | 模型 › 专属倍率解析缓存上限 | 0 不缓存 | ≥1 | main |
| 22 | 价格监控 › 每主机上游日志查询数 | 0 关闭 | 1–20 | fork |
| 23 | 监控 › 指标保留天数 | 0 永久保留 | ≥1 | main |
| 24 | 请求限制 › 模型请求速率次数 | 0 不限制 | 1–100000000 | main |
| 25 | 请求限制 › 分组速率弹窗 最大请求数 | 0 不限制 | 1–2147483647 | main |
| 26 | 渠道编辑 › 每日额度上限 | 0 不限制 | ≥0.01 USD 等值，小数 | main |
| 27 | 渠道批量操作 › 每日上限 | 留空 不修改、0 移除上限 | >0 | main |
| 28 | 编辑标签 › 每日上限 | 留空 不修改、0 移除上限 | >0 | main |
| 29 | 订阅套餐 › 总额度 | 0 不限 | >0 | main |
| 30 | 订阅套餐 › 每人限购 | 0 不限 | ≥1 | main |
| 31 | 渠道亲和规则 › TTL | 0 使用默认 TTL | ≥1 秒 | main |
| 32 | 渠道亲和 › 最大条目数 | 0 使用默认（100000） | ≥1 | main |
| 33 | 渠道亲和 › 默认 TTL | 0 使用默认（3600 秒） | ≥1 秒 | main |
| 34 | 性能 › CPU / 内存 / 磁盘保护阈值（3 项） | 0 关闭该项检查 | 1–100 % | main |
| 35 | 路由可靠性 › 自动禁用阈值秒数 | 0 不自动禁用 | >0，小数 | main |
| 36 | 路由可靠性 › 重试次数 | 0 不重试 | 1–10 | main |
| 37 | 员工 › 阶梯业绩门槛 | 0 不限 | >0 USD | fork |
| 38 | 日志导出冷却 / 批次间隔、账本明细导出间隔 | 0 无冷却 / 不等待 | ≥1 | fork |

第 32–38 项的特殊含义目前只在后端代码中，界面没有说明，本次一并补上下拉说明（文案来自后端实际行为，已核对：`service/channel_affinity.go:83/89`、`middleware/performance.go:50`、`controller/channel-test.go:923`）。

刻意不改：成本系数（0 就是 0 成本）、价格 / 额度 / 充值金额、`RequestLogMinCount`、签到额度、Gotify 优先级等——0 没有特殊含义。令牌的「无限额度」是独立开关，不是特殊值。

## 4. i18n

直接写入 7 个语言文件的 `translation` 对象末尾（未用 `i18n:sync`，避免回填无关键），UTF-8 无 BOM，均已校验可解析。

- 复用已有键：`Unlimited`、`Disabled`（`No limit` 在 zh-TW/fr/vi 仍是英文，员工门槛改用同义且已全翻译的 `Unlimited`）。
- 新增组件键 7 条：`{{label}} ({{value}})`、`No matching preset`、`Custom value: {{value}}`、`Or enter a number from {{min}} to {{max}}`、`Or enter a number of at least {{min}}`、`Choose an option from the list or enter a number from {{min}} to {{max}}`、`Choose an option from the list or enter a number of at least {{min}}`。
- 新增特殊值说明 13 条：`Same as main database`、`No retry`、`Follow system default`、`Drain all each cycle`、`Same as settlement queue`、`Flush only when full`、`Always count in real time`、`Only hours finished from now on`、`Default 300 seconds`、`Stop accepting new jobs`、`No pause`、`Pause all exports`、`Default 100 ms`。

## 5. 与 main 的差异面

- 新组件、新文案在本分支新文件 / 本分支已有的 i18n 增量中，不改 main 的 `components/ui/*` 与 Rule 6 列出的上游文件。
- 9 个 fork 文件改动无合并顾虑。
- 13 个 main 文件：每个字段把 `<Input type="number">` 换成 `<NumericPresetInput>`（约 3–8 行 / 字段）。以后合并上游时，若上游改了同一处输入框会产生小冲突。可选：**只改 fork 文件**（第 6–20、22、37、38 项，共 18 个字段），main 文件的 20 个字段保持原样。

## 6. 测试

前端无测试框架（Rule 15.7）：以 `bun run typecheck`、`bun run lint`、`bun run build` 为门禁。组件交互另用一次性 happy-dom 脚本核验过（不入库）：显示「说明（值）」、聚焦列出全部特殊值、按键/小数点/负号拦截、每次编辑即把框里的内容交给调用方（合法值、越界数原样，不完整的数字为 NaN）、失焦后标红并关联错误、键入数字时只列出值前缀匹配的特殊值、Tab 离开关闭下拉、点选特殊值、选中后键入先全选说明文字、Esc 恢复、外部改值丢弃草稿、回车确认、RHF `FormControl` 下 label/描述关联与小数提交。测试服（v0.0.0-f0b-20261001-presets2）用无头 Chrome 实际操作核验：19 个字段逐页显示正确；上限 100 的字段键入 1000 后直接点保存被拦下且未发请求（RHF 表单聚焦到该字段）；价格监控键入 30 时保存按钮禁用；员工门槛清空后保存提示「门槛必须大于或等于 0」；合法值保存后刷新回读一致，Esc 恢复聚焦前的值，点选特殊值保存后回读为「无限制（0）」；导出冷却键入 300 时下拉显示「自定义值：300」而不是「默认 300 秒（0）」；连接池状态请求被挂起期间修改的值在状态返回后保留；深色主题下拉与错误样式正常。核验所改配置均已恢复原值。保存的数值与改动前一致，后端无需回归。

## 7. 已确认范围（2026-10-01）

1. **只改本分支独有文件中的 18 个字段**：第 6–20、22、37、38 项。main 文件中的 20 个字段（#1–5、21、23–36）保持原样，不增加上游合并冲突。
2. 取值范围小的字段（#19 并发任务数 0–16、#22 每主机查询数 0–20）同样使用可输入下拉框，与其余字段一致。

## 8. 实施结果（19 个字段）

取值范围按「前端现有校验 ∩ 后端实际行为」核对后确定（后端位置见各行）。「普通取值」是特殊值之外可输入的范围；整数字段只能输入整数。

| # | 文件 | 字段 | 特殊值（说明键） | 普通取值 | 后端依据 |
|---|---|---|---|---|---|
| 6 | `system-tuning/group-retry-section.tsx` | 分组重试次数 | -1 `No retry`、0 `Follow system default` | 1–20 | `MaxGroupRetryTimes` |
| 7 | `system-tuning/hot-config-sections.tsx` | `relay_timeout_setting.response_timeout_seconds` | 0 `Unlimited` | 1–604800 | `ValidateRelayTimeoutSetting` |
| 8 | 同上 | `relay_timeout_setting.total_timeout_seconds` | 0 `Unlimited` | 1–604800 | 同上 |
| 9 | 同上 | `relay_timeout_setting.retry_min_budget_seconds` | 0 `Disabled` | 1–604800 | 同上 |
| 10 | 同上 | `relay_timeout_setting.max_total_attempts` | 0 `Unlimited` | 1–100 | `MaxRelayTotalAttemptsLimit` |
| 11 | 同上 | `db_pool_setting.log_max_idle_conns` | 0 `Same as main database` | 1–10000 | `ValidateDBPoolSetting` |
| 12 | 同上 | `db_pool_setting.log_max_open_conns` | 0 `Same as main database` | 1–100000 | 同上 |
| 13 | `maintenance/ledger-pipeline-section.tsx` | `cost_flush_max_per_cycle` | 0 `Drain all each cycle` | ≥1 | `GetCostFlushMaxPerCycle` |
| 14 | 同上 | `cost_outer_batch_size` | 0 `Same as settlement queue` | ≥1 | `GetCostOuterBatchSize` |
| 15 | 同上 | `cache_ttl_jitter_percent` | 0 `Disabled` | 1–100 | `GetCacheTTLJitterPercent` |
| 16 | `maintenance/fallback-backfill-section.tsx` | `flush_interval_sec` | 0 `Flush only when full` | ≥1 | `service/business_stats_backfill.go`（≤0 只按批满刷盘） |
| 17 | `maintenance/log-query-section.tsx` | `current_hour_ttl_sec` | 0 `Always count in real time` | ≥1 | `GetCurrentHourTTL`（超过 300 读取时截断） |
| 18 | 同上 | `warm_hours` | 0 `Only hours finished from now on` | ≥1 | `GetWarmHours`（超过 720 读取时截断） |
| 19 | `maintenance/log-export-section.tsx` | `max_concurrent_jobs` | 0 `Stop accepting new jobs` | 1–16 | `GetMaxConcurrentJobs` |
| 20 | 同上 | `cpu_hard_limit` | 0 `Pause all exports` | 1–100 | `GetCPUHardLimit` |
| 22 | `models/price-monitor-panel.tsx` | 每主机上游日志查询数 | 0 `Disabled` | 1–20 | `MaxUpstreamLogQueriesPerHost` |
| 37 | `features/employees/index.tsx` | 阶梯业绩门槛（USD） | 0 `Unlimited` | ≥0，可小数 | `controller/employee.go`（`gte=0`） |
| 38 | `maintenance/log-export-section.tsx` | `user_cooldown_sec` | 0 `Default 300 seconds` | 1–86400 | `GetUserCooldownSec`：≤0 取 `DefaultLogExportUserCooldownSec`=300 |
| 38 | 同上 | `batch_sleep_ms` | 0 `No pause` | 1–60000 | `GetBatchSleepMs`（0 = 不额外休眠） |
| 38 | `maintenance/ledger-detail-section.tsx` | `export_batch_sleep_ms` | 0 `Default 100 ms` | ≥1 | `GetExportBatchSleepMs`：≤0 取 `DefaultLedgerDetailExportBatchSleepMs`=100 |
| 38 | `maintenance/fallback-backfill-section.tsx` | `batch_sleep_ms` | 0 `No pause` | ≥1 | `service/business_stats_backfill.go`（按值休眠，0 = 刷盘后不休眠）；§3 清单遗漏，与 #38 同类一并改 |

与 §3 的差异（以代码为准）：

- **#38 的 0 并非都是「无冷却 / 不等待」**：日志导出冷却 `user_cooldown_sec=0` 实际取默认 300 秒，账本明细导出 `export_batch_sleep_ms=0` 实际取默认 100 毫秒；只有日志导出 `batch_sleep_ms=0` 才是不休眠。下拉说明按后端实际行为写，默认值在前端以注释标明对应的后端常量，后端改默认值时需同步。
- #17、#18 后端读取时会把超过 300 秒 / 720 小时的值截断，但前端 zod 只校验下限；为保持现有校验不变，这两项不设上限（与改动前一致）。
- #37 的「不限」复用已有键 `Unlimited`（`No limit` 在 zh-TW/fr/vi 未翻译），弹窗里的门槛预览同样改用 `Unlimited`，门槛不是数字时预览显示 `-`（与费率预览一致）。

保存格式：`ConfigGroupSection`、价格监控、RHF 表单提交的仍是 number；分组重试与员工门槛的状态仍是字符串、保存时 `Number()`。同一输入保存出的值与改动前一致。

