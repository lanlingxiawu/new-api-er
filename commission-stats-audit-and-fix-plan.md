# 提成统计/员工管理重构 — 审计报告与修复方案

> 范围：我的提成 / 我的客户 / 员工管理 / 业务概览 相关代码（对应 12 项需求）。
> 本文档包含「审计发现」与「修复方案」两部分，尚未实施任何修复。

---

## 一、最高优先级问题（H1-H3）

### H1. "我的客户"提成总额改读日统计后，对老实例升级数据缺口无兜底
**位置**：`model/employee.go:1063-1085`（`GetCustomerCommissionTotals`），调用方 `controller/customer.go:95,115,127`

**问题**：
```go
err := DB.Model(&EmployeeCustomerCommissionDailyStat{}).
    Select("customer_user_id, COALESCE(SUM(commission_quota), 0) as commission_quota").
    Where("employee_user_id = ? AND customer_user_id IN ?", employeeUserId, customerUserIds).
    Group("customer_user_id").
    Scan(&rows).Error
```
无日期范围限制、无 `NeedsBusinessStatsBackfill()` 感知、无回退到 `EmployeeCommissionLog` 明细聚合的兜底。需求 8 专门为"`4fbbc8ef` 已上线、`BusinessStatsBackfillCompleted=true` 但 `EmployeeCustomerCommissionDailyStat` 缺失"新增了检测能力，但该信号只在管理员"业务概览"页暴露，"我的客户"页面完全感知不到。升级后未执行回填前，所有员工看到的客户提成总额会是 0 或偏低，且无任何提示。

**修复方案**（推荐 C，必要时叠加 B）：
- **方案 C（数据正确性兜底，推荐）**：在 `GetCustomerCommissionTotals` 中，先调用一次轻量判断——若 `EmployeeCustomerCommissionDailyStat` 中该 `employeeUserId` 完全没有任何记录，但 `EmployeeCommissionLog` 中存在该 `employeeUserId` 的记录（说明尚未回填），临时回退到对 `EmployeeCommissionLog` 做 `WHERE employee_user_id=? AND customer_user_id IN (...) GROUP BY customer_user_id` 的实时聚合。回填完成后该分支不再触发，性能影响可控（只在"该员工日统计完全为空"时触发一次判断查询）。
- **方案 B（用户提示，配合方案 C 或单独使用）**：在"我的提成/我的客户"相关接口响应中附加 `data_incomplete`（或复用 `needs_backfill`）标志，前端在检测到为 `true` 时展示"数据统计中，请联系管理员执行历史数据回填"的提示条。
- 涉及文件：`model/employee.go`（`GetCustomerCommissionTotals`/`GetCustomerCommissionTotal`）、`controller/customer.go`、（如做方案B）`web/default/src/features/customer-console/index.tsx`、`web/classic/src/pages/Business/index.jsx` 的 CustomerConsole 部分。

---

### H2. 月度重置批量更新缺并发/幂等守卫，"立即重置"未加重入锁
**位置**：`model/commission_tier.go:430-495`（`ResetEmployeeTierLevelsForPeriod`）、`service/commission_tier_reset_task.go`、`model/business_stats_buffer.go`（`FlushBusinessStatBuffers`/常规 flush 循环）

**问题**：
1. `SELECT` 用 `effective_at < resetAt` 做幂等守卫，但 `UPDATE`（491行）只按 `id` 匹配：
   ```go
   tx.Model(&EmployeeTierLevel{}).Where("id = ?", level.Id).Updates(updates)
   ```
   SELECT 与 UPDATE 之间，若有并发的 `SetTierLevel`（管理员手动调级 / `TryAutoUpgradeTier` 经常规 5s flush 触发）修改了同一行，本次重置会用读到的旧值静默覆盖该并发修改，不报错。
2. 常规业务统计 flush 循环（每 5s 调 `FlushBusinessStatBuffers()`）与重置任务（重置前也调 `FlushBusinessStatBuffers()`）之间无互斥，二者可能同时触碰同一行 `EmployeeTierLevel`，是上述竞态的真实触发源。
3. `AdminTriggerTierReset`→`RunCommissionTierResetNow`（"立即重置"）未使用 `tierResetRunning` 原子守卫，可能与定时任务并发执行，对同一批行重复重置（产生两条 `source="reset"` 日志，baseline 被先后覆盖两次）。

**修复方案**：
1. 将 UPDATE 改为带乐观锁的条件更新并检查 `RowsAffected`：
   ```go
   res := tx.Model(&EmployeeTierLevel{}).
       Where("id = ? AND effective_at = ?", level.Id, level.EffectiveAt).
       Updates(updates)
   if res.Error != nil {
       return res.Error
   }
   if res.RowsAffected == 0 {
       // 该行被并发修改，本周期跳过，留待下个 tick 的 SELECT 重新评估
       continue
   }
   ```
   注意：循环退出条件是"`processed == 0`"，若本批全部因并发被跳过，需要确保 `processed` 仍按"读取到的行数"计数（避免死循环式重复读到同一批），或在跳过时记录日志但仍计入已访问行数。
2. 给 `FlushBusinessStatBuffers()` 增加包级互斥（`sync.Mutex` 或复用一个 `CompareAndSwap` 标志），确保常规 flush 循环与重置任务调用的 flush 互斥执行，避免二者同时操作同一 `EmployeeTierLevel`/`UserExtension` 行。
3. `RunCommissionTierResetNow` 在执行前先 `tierResetRunning.CompareAndSwap(false, true)`；若已被占用，返回"系统正在执行重置，请稍后重试"的错误，而不是静默并发执行；执行完毕 `defer Store(false)`。

---

### H3. 经典 UI"立即重置"确认弹窗 / 保存提示 i18n key 全语言缺失
**位置**：`web/classic/src/pages/Business/index.jsx`（`TierResetSettingsCard`），8 个 locale 文件（`en/zh/zh-CN/zh-TW/fr/ru/ja/vi.json`）

**问题**：代码中使用 `t('确认立即重置')`、`t('立即重置')`、`t('设置已更新')`，但这三个 key 在全部 8 个 locale 文件（含 `zh`）中均不存在 → 所有语言下均会渲染原始中文 key 字面量。

**修复方案**：在 8 个 locale 文件中补充以下三个 key（参照默认 UI `tier-reset-settings-card.tsx` 中等价文案 `Confirm reset now` / `Reset now` / `Setting updated successfully` 的各语言翻译）：

| key | en | zh | zh-CN | zh-TW |
|---|---|---|---|---|
| `确认立即重置` | Confirm reset now | 确认立即重置 | 确认立即重置 | 確認立即重置 |
| `立即重置` | Reset now | 立即重置 | 立即重置 | 立即重置 |
| `设置已更新` | Setting updated successfully | 设置已更新 | 设置已更新 | 設定已更新 |

fr/ru/ja/vi 按各自语言补齐对应翻译（可先填英文占位，再走 i18n:sync 流程）。

---

## 二、中优先级问题（M1-M12）

### M1. tier-reset 设置卡：隐藏字段导致 isDirty 误判，保存会静默覆盖 reset_hour/min/sec
**位置**：`web/default/src/features/employees/components/tier-reset-settings-card.tsx:80-87`

**问题**：`isDirty` 的判断包含 UI 不可见的 `reset_hour/minute/second`（只要后端值非 0 就为 `true`）。保存时无条件把这三个字段强制写回 `0`，管理员可能在未做任何可见改动时就能点击"保存"，并静默覆盖这三个隐藏字段。

**修复方案**：把 `isDirty` 的比较范围缩小为仅 `enabled`/`resetDay`/`timezone` 三个真正可编辑字段；保存请求仍可固定携带 `reset_hour=0/minute=0/second=0`（维持字段完整性），但不参与 `isDirty` 判定。

---

### M2. tier-reset 设置卡：last_reset_at / next_reset_at 取数但未展示
**位置**：`web/default/src/features/employees/components/tier-reset-settings-card.tsx`

**问题**：`getTierResetConfig` 已返回 `last_reset_at`/`next_reset_at`，i18n key `Last reset time`/`Next reset time` 已添加，`formatTs` 工具函数已定义，但组件中从未渲染——管理员看不到上次/下次重置时间。

**修复方案**：在设置卡展开区域增加两行只读展示：
```tsx
<div>{t('Last reset time')}: {formatTs(config?.last_reset_at)}</div>
<div>{t('Next reset time')}: {formatTs(config?.next_reset_at)}</div>
```
经典 UI `TierResetSettingsCard` 同步补充（当前完全没有相关展示）。

---

### M3. 经典 UI 员工列表缺"本期提成"列
**位置**：`web/classic/src/pages/Business/index.jsx`（`EmployeesTab` 列定义，约 2078-2259 行）

**问题**：已有 `current_performance_quota`（本期业绩）、`total_commission_quota`（历史累计提成）列，但缺少绑定 `current_commission_quota`/`current_commission_usd` 的"本期提成"列，需求 3 在经典 UI 仅实现一半。

**修复方案**：参照默认 UI `web/default/src/features/employees/index.tsx` 中"Current Commission"列的实现（含字段绑定与 tooltip 文案），在经典 UI `current_performance_quota` 列之后新增对应列。

---

### M4. 重置遇到 orphaned tier_id 时静默"假装已处理"
**位置**：`model/commission_tier.go:471-480`

**问题**：当 `level.TierId` 在 `tierById`（`GetAllTiersCached()`）中找不到（tier 已删除/缓存过期）时，仅记录 `SysError`，但仍把该行的 `effective_at`/`baseline_*`/`source` 全部刷新为"本周期已处理"。该员工此后会被永久标记为"已重置"，但 `tier_id` 实际未修正，且 `TryAutoUpgradeTier` 对 orphaned tier 的早退逻辑会一直生效，问题被掩盖。

**修复方案**：当 `currentTier` 查找失败时，**跳过该行的 UPDATE**（不刷新 `effective_at`/baseline/source），仅记录 `SysError`，使其在下个重置周期继续被 `effective_at < resetAt` 捕获并持续告警，直到管理员手动修正该员工的 `tier_id`。

---

### M5. 员工日/员工-客户日/员工月三个统计维度缓冲写入互不关联，单维度失败造成长期漂移
**位置**：`model/business_stats_buffer.go:479-491`（`BufferCommissionDailyStat`）及 `bufferCustomerCommissionStatRedis`/`bufferCommissionMonthlyStatRedis`

**问题**：三个维度各自独立 pipeline 写 Redis，任一失败时仅 `common.SysError`，无内存兜底（`BufferCommissionAndProfit` 的 `bufferEmployeeExtMem` 兜底未应用到这两个新维度）。偶发失败会导致"员工日统计提成总额"与"该员工下所有客户日统计提成之和"长期对不上，无审计/告警。

**修复方案**：为 `bufferCustomerCommissionStatRedis`/`bufferCommissionMonthlyStatRedis` 增加与现有 `bufferEmployeeExtMem` 同构的内存兜底——pipeline 失败时调用对应的 `bufferCustomerCommissionStatMem`/`bufferCommissionMonthlyStatMem` 把本次增量写入内存缓冲，等待下次 flush 重试（最终计入 dead-letter 重试上限）。

---

### M6. Redis upsert 与 Del 之间崩溃 → 重启后重复计入
**位置**：`model/business_stats_buffer.go`（`flushOneCustomerCommissionKey`/`flushOneCommissionMonthlyKey`，及既有的 `flushOnePlatformKey`/`flushOneCommissionKey` 同构问题）

**问题**：DB upsert（`+=` 累加）成功后才 `RDB.Del(key)`；二者之间进程崩溃，重启后会对同一份 Redis 增量重复 `+=` 一次，造成对应维度的收入/成本/利润/提成数据重复计入，无检测手段。

**修复方案（双缓冲模式）**：flush 时先用 `RENAME key processing:key`（原子操作）将待处理数据移至临时 key；DB upsert 成功后删除 `processing:key`。若进程在 upsert 后、删除前崩溃，重启时先扫描遗留的 `processing:*` key：
- 若对应原始 `key` 已存在新数据，将 `processing:*` 内容与之合并后统一处理；
- 若 `key` 不存在，直接重试对 `processing:*` 的 upsert（此时由于原 `key` 已清空、新一轮 flush 不会再读到这部分增量，重试 upsert 是安全的——需确保 upsert 本身不是简单 `+=`，而是基于 `processing:*` 的"是否已成功"标记，或者接受很小概率的重复但通过定期对账兜底）。
也可考虑更轻量的折中：将"upsert + Del"改为单条 Redis Lua 脚本配合 DB 事务标记（如在 upsert 行写入 `last_applied_batch_id`），重启后先比较 batch_id 决定是否跳过。

---

### M7. "我的提成"月度日历 UTC 自然日 vs 浏览器本地月份范围，时区不一致
**位置**：
- 后端：`model/employee.go`（`GetCommissionCalendarStats`，`unixDayStart` 按 UTC `ts/86400*86400` 分桶，`date` 字段用 `.UTC().Format("2006-01-02")`）
- 默认 UI：`web/default/src/features/employees/components/commission-financial-calendar.tsx`（`monthValueToRange`/`toDateKey` 按浏览器本地时区）
- 经典 UI：`web/classic/src/pages/Business/index.jsx`（`CommissionMonthlyCalendar` 的 `dateKey()`）

**问题**：在 UTC+8（项目默认中国时区）下，本地"6 月 1 日"对应的查询区间起点是 UTC `5 月 31 日 16:00`，被 `unixDayStart` 归入 UTC "5 月 31 日"桶；后端返回的 `"2026-06-01"` 行实际对应本地 `6 月 1 日 08:00 ~ 6 月 2 日 08:00`。月度汇总卡片总额可能与日历格子逐日相加结果对不上，跨月边界数据错位甚至不显示。

**修复方案（统一"日"的定义为 `commission_tier_reset_setting.timezone`，默认 Asia/Shanghai）**：
1. 后端：`unixDayStart`（及 `GetCommissionCalendarStats` 用到的所有"按日分桶/格式化"逻辑）改为基于 `operation_setting.GetCommissionTierResetSetting().Timezone`（复用 `tierResetLocation`/`commissionMonthlyStatLocation`，并统一二者实现，见 L2）对齐自然日边界与格式化输出。
2. 前端：`monthValueToRange`/`toDateKey`（默认 UI）与 `dateKey()`（经典 UI）改为按同一时区（`Asia/Shanghai` 或 `Local`，与后端配置一致）计算月份范围与日期 key，而不是浏览器本地时区。可由后端在响应中附带所用时区标识，前端据此选择 `Intl.DateTimeFormat` 的 `timeZone` 参数。
3. 影响文件：`model/employee.go`、`web/default/.../commission-financial-calendar.tsx`、`web/classic/src/pages/Business/index.jsx`。

---

### M8. 历史回填若包含"今日"，与常规 flush 竞态导致当日数据重复计入
**位置**：`controller/business_stats.go`（`AdminBackfillBusinessStats`）、`model/business_daily_stats.go`（`backfillBusinessDailyStatsDay`）

**问题**：回填对"今日"做"SELECT 明细求和 → DELETE → 重建"，与同时运行的常规 5s flush 循环（可能正把"今日"已被回填 SELECT 统计过的缓冲增量 `+=` 到刚重建的行上）竞态，导致当日数据被重复计入一次。

**修复方案**：
- 默认将回填的结束日期限制为"昨天"：`maxDate = min(请求的 maxDate, 今天 0 点 - 1)`，今日数据由常规 flush 自然产出，无需回填。
- 若确需回填"今天"（如紧急修复），增加一个全局开关（如 `business_stats_flush_paused` 原子标志），回填前设置该标志使常规 flush 循环临时跳过 flush，回填完成后清除。
- 涉及文件：`controller/business_stats.go`、`model/business_daily_stats.go`、`model/business_stats_buffer.go`（flush 循环读取暂停标志）。

---

### M9. `NeedsBusinessStatsBackfill` 每次概览加载都做明细表全表 GROUP BY
**位置**：`model/business_daily_stats.go`（`needsEmployeeCustomerCommissionStatsBackfill`），调用方 `controller/employee.go:647`

**问题**：通过对 `EmployeeCommissionLog` 做 `FLOOR(created_at/86400) GROUP BY (date, employee, customer)` 全表分组计数、与 `EmployeeCustomerCommissionDailyStat` 分组计数比较，得到 `expectedGroups`/`actualGroups`，**每次** `/api/admin/employee/overview` 请求都执行一次全表扫描——即便已永久追平，仍每次重复扫描，与需求 9"避免明细聚合"的目标相悖。

**修复方案**：增加一个新的运行态 option（如 `business_stats_employee_customer_backfill_caught_up`，类比 `BusinessStatsBackfillCompleted`）。当某次检查发现 `actualGroups >= expectedGroups`（且既有的按天覆盖检查也通过）时，持久化写入该 flag=true；后续 `NeedsBusinessStatsBackfill()` 优先读取该 flag，为 `true` 时直接返回 `false`，跳过全表 GROUP BY。

---

### M10. `AdminListEmployees` 列表排序：`current_commission_quota` 无效、`current_performance_quota` 排序口径与展示口径不一致
**位置**：`model/employee.go:284-320`（`GetAllEmployees` 排序 `switch`）

**问题**：
- `case` 分支中没有 `"current_commission_quota"`，命中 `default`（按 `id` 排序），点击"按本期提成排序"不生效。
- `current_performance_quota` 命中 `case "total_profit_quota", "current_performance_quota"`，按**历史累计利润总和**排序，但页面展示的"本期业绩"是"累计 - baseline（clamp ≥0）"，重置后两者顺序会不一致。

**修复方案**：
1. 拆分 `current_performance_quota` 为独立 `case`，LEFT JOIN `employee_tier_levels`，排序表达式为：
   ```sql
   CASE WHEN COALESCE(commission_rollup.total_profit_quota,0) - COALESCE(employee_tier_levels.baseline_profit_quota,0) < 0
        THEN 0
        ELSE COALESCE(commission_rollup.total_profit_quota,0) - COALESCE(employee_tier_levels.baseline_profit_quota,0)
   END
   ```
   （`CASE WHEN` 写法跨 SQLite/MySQL/PostgreSQL 兼容，符合 Rule 2）
2. 新增 `case "current_commission_quota"`，新增一个按 `commission_quota` SUM 的 rollup 子查询，排序表达式同理为 `total_commission_quota - baseline_commission_quota` 并 clamp ≥0。

---

### M11. 经典 UI `总消费` i18n key 全语言缺失（含本次新增代码复用同一坏绑定）
**位置**：`web/classic/src/pages/Business/index.jsx`（新增 `EmployeeConsole` "明细" Tab，约 3792 行；旧 `BusinessOverview` 约 4425/4534 行同样问题）

**问题**：`t('总消费')` 在全部 8 个 locale 文件中均不存在（旧代码已有此问题，本次新增代码复制了同样的绑定），所有语言下显示原始字符串 `总消费`。

**修复方案（推荐复用已有翻译，最简）**：将新增代码中的 `t('总消费')` 改为复用已存在且已正确翻译的 `t('累计消费')`。如需保留"总消费"独立措辞，则需在 8 个 locale 文件中补全该 key 的翻译（同时修复旧 `BusinessOverview` 中的同名问题，一次性解决）。

---

### M12. 共享 i18n key `"Period"` 中文翻译被改写，影响无关页面
**位置**：`web/default/src/i18n/locales/{en,zh,fr,ru,ja,vi}.json`

**问题**：`"Period"` 的 zh 翻译从 `"时间范围"` 改为 `"统计周期"`。该 key 同时被 `web/default/src/features/rankings/components/rankings-hero.tsx:62` 用作 `aria-label`，本次"我的提成"相关改动连带改写了无关功能的措辞。

**修复方案**：将 `"Period"` 的 zh 翻译改回 `"时间范围"`；本次"我的提成/月度统计"场景下需要"统计周期"措辞的地方，使用已存在但当前未被引用的 `"统计周期"` key（已在本次 diff 中添加，目前是孤立 key，正好复用）。

---

## 三、低优先级 / Nit 问题

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| L1 | `/api/admin/employee/commission/monthly`、`/api/employee/commission/monthly`（财务周期口径，`ResolveCommissionMonthlyPeriod`）实现后未被前端调用；"月份切换"实际走口径不同的 `/commission/calendar`（UTC自然日） | `controller/employee.go`, 前端两端 | 确认是否应将"我的提成"月度切换迁移到 `/commission/monthly`，与重置周期口径统一；若确认不需要，移除未用的接口/路由以减少维护面 |
| L2 | `commissionMonthlyStatLocation`（`model/business_daily_stats.go:116-128`）与 `tierResetLocation`（`service/commission_tier_reset_task.go:53-66`）两份独立时区解析实现，行为不完全一致（前者对非 Local/Asia-Shanghai 静默退化为 Asia/Shanghai，后者走 `time.LoadLocation`） | `model/business_daily_stats.go`, `service/commission_tier_reset_task.go` | 抽取为共用函数（如 `operation_setting` 包内的 `ResolveCommissionTimezone()`），两处统一调用 |
| L3 | `model/main.go` 的 `migrateDBFast`（疑似当前未被调用的死代码）漏注册 `EmployeeCustomerCommissionDailyStat`，但加了 `EmployeeCommissionMonthlyStat` | `model/main.go` | 补充注册以保持一致；若确认 `migrateDBFast` 完全未使用，评估是否可删除 |
| L4 | 设计文档 `snoopy-shimmying-salamander.md` 默认 `ResetDay=1`，与实现默认 `10` 不一致 | `snoopy-shimmying-salamander.md` | 更新文档或标注"以实现为准"，避免后续误导 |
| L5 | `model/employee.go:1063` 起 `GetCustomerCommissionTotals` 等改动未把 `EmployeeCustomerCommissionDailyStat` 纳入 `getBusinessStatsDailyBounds` 的边界判断 | `model/business_daily_stats.go` | 与 M5/M9 一并处理：边界判断函数加入对该表的覆盖检查 |
| L6 | `web/default/.../employees/index.tsx:2604`、`employee-console/index.tsx:481` 用中文字面量 `t('月度统计')` 作为 key，与已存在、翻译相同的 `"Monthly Stats"` key 重复；`commission-financial-calendar.tsx` 的 `t('统计月份')` 同理 | 默认 UI 多处 + 全部 locale | 改为 `t('Monthly Stats')`，删除冗余的 `月度统计`/`统计月份` key |
| L7 | 经典 UI `CommissionMonthlyCalendar` 非 embedded 模式下传了死 `pagination` prop（`stats` 无 `.paginate`，`ClassicPagination` 恒返回 `null`） | `web/classic/.../Business/index.jsx` | 移除该 `pagination` prop |
| L8 | 残留对已移除 `commission_rate` 字段的引用：`web/classic/.../Business/index.jsx:3721` `profile?.commission_rate ?? 0` | `web/classic/.../Business/index.jsx` | 清理为 `tierInfo?.tier_rate ?? 0`（与默认 UI 一致） |
| L9 | 默认 UI 新增多个孤立未引用 i18n key：`记录数`(与`Records`重复)、`统计周期`、`周期开始时间戳`、`最后记录`、`暂无月度提成统计`、`Last reset time`/`Next reset time`(待 M2 修复后会被使用) | 全部 locale | M2/M12 修复后部分 key 会被消费；其余确认无用后删除 |
| L10 | `service/commission_tier_reset_task_test.go` 未覆盖 `Timezone="Asia/Shanghai"`（默认配置）路径；`commission_tier_test.go` 未覆盖 orphaned tier_id、多批循环、并发 SetTierLevel 场景 | 测试文件 | 修复 H2/M4 后补充对应单测 |
| L11 | `controller/business_stats.go` 的 `businessStatsBackfillRunning` 是进程内 `int32`，多实例部署可能并发回填重叠区间，触发唯一索引冲突（有重试兜底，但产生日志噪音） | `controller/business_stats.go` | 如有多实例部署场景，改为基于 DB/Redis 的分布式锁 |

---

## 四、修复优先级建议（落地顺序）

1. **H1**（数据正确性，用户可见的错误金额）
2. **H2**（财务数据并发安全，修复成本不高但影响审计完整性）
3. **H3**（i18n 缺失，纯文案补充，成本极低，建议随手修）
4. **M10**（排序行为不一致，影响管理员体验）
5. **M5/M6**（缓冲一致性，长期数据漂移风险，优先级随 Redis 是否启用而定）
6. **M7**（时区/日历错位，影响"我的提成"展示准确性）
7. **M1/M2/M3/M11/M12**（前端体验/i18n，成本低，可批量处理）
8. **M8/M9** + L 系列（性能与边界场景，可排期处理）
