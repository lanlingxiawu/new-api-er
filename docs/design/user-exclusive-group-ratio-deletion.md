# 分组删除时清理用户专属倍率

> 2026-09-10 资源分析补充：500 行 LIMIT 限制返回行数，非空 JSON 谓词没有专属索引，不能保证每批只扫描 500 行；50ms 批间让步也不保证固定 50% 占空比。单 worker 能约束并发连接数量，不能单独证明大表 IO 对 relay 无影响。部署验收应使用真实数据分布的 SQL 计划和耗时，见 [整体设计第 4、7 节](uncommitted-changes-overview.md)。

## 1. 规则

四条，全部围绕“分组名的历史覆盖不得残留”：

| # | 规则 | 位置 |
|---|---|---|
| 1 | 分组下存在**启用的渠道**时，拒绝删除该分组，提示先移除渠道 | 保存 `GroupRatio` 时 |
| 2 | 删除分组时，**同时删除**所有用户 `group_ratios` 中该分组的 key | 保存 `GroupRatio` 时 |
| 3 | 新增分组时，**清除同名的历史残留**覆盖，避免旧倍率复活 | 保存 `GroupRatio` 时 |
| 4 | 保存用户时，自动剔除 `group_ratios` 中已不存在的分组 key，不再报错 | `controller.UpdateUser` |

规则 2 和 3 是**同一个操作**：把一批分组名从所有用户的 `group_ratios` 中删掉。删除时这批名字是「被移除的名称」，新增时是「新出现的名称」，实现上合并为一次扫描。

规则 4 负责存量数据：升级前已经删除的分组，其残留 key 今天会阻塞用户资料保存（报障现象），在该用户下次被编辑时清掉。

不新增端点、表、后台任务、配置项。

### 1.1 实现落点

| 规则 | 代码 |
|---|---|
| 1、2、3 的入口与渠道检查 | `controller.planGroupRatioCleanup`（`controller/option.go`），在 `UpdateOption` 的 `GroupRatio` 分支调用 |
| 清理执行 | `model.CleanupUserGroupRatios` + `model.casUpdateUserGroupRatios`（`model/user_group_ratio_cleanup.go`） |
| 渠道检查查询 | `model.GroupsWithEnabledChannels`（同上） |
| JSON 剔除 | `ratio_setting.FilterUserGroupRatios`（`setting/ratio_setting/user_exclusive_ratio.go`） |
| 规则 4 | `controller.UpdateUser`（`controller/user.go`） |
| 前端失效行标记 | `web/src/features/users/components/users-mutate-drawer.tsx` |

## 2. 现状与根因

- 有效计费分组存放在 option `GroupRatio`；用户专属倍率存放在 `users.group_ratios`，格式 `{"group": ratio}`。
- 分组删除只修改设置，不会修改用户 JSON，两者之间没有外键。
- `controller.UpdateUser`（`controller/user.go:763`）把“分组不存在”和“倍率非法”合并为同一个错误，历史 key 因此阻塞该用户的资料保存——这就是报障现象。`controller.CreateUser` 通过 `cleanUser` 白名单构造用户，不接受 `group_ratios`（`controller/user.go:1108`），创建路径不受影响。
- 分组倍率会随业务调整，删掉的 `vip` 与之后重建的 `vip` 不是同一个定价对象，用户当初按旧倍率约定的覆盖不得在新定价上复活——这是规则 3 的理由。

## 3. 删除前置检查（规则 1）

在保存 `GroupRatio`、检测到有名称被移除时，检查这些名称下是否存在**启用的渠道**；存在则整体拒绝保存，提示“分组 X 下仍有启用的渠道，请先移除或禁用这些渠道”。

实现：查询所有 `status = ChannelStatusEnabled` 的渠道，只取 `id` 与 `group` 两列，用既有的 `Channel.GetGroups()`（`model/channel.go:307`，已处理逗号分隔与空格）解析，命中 `removed` 中任一名称即拒绝。查询写法沿用 `model/channel.go:386` 的模式；`group` 是保留字，列名用 `commonGroupCol`。

**查 `channels` 而不是 `abilities`。** `abilities` 是派生表：渠道创建/修改时先保存 `channels` 再重建 `abilities`，能力更新失败会留下不一致状态。此时查 `abilities` 可能得出“该分组没有启用渠道”，允许删除；之后某次渠道编辑重建了能力，分组又有了渠道却没有定价。以源表为准是保守且正确的方向。

渠道表规模是数十到数千，一次带状态过滤的查询即可，不需要新增索引，也不需要 LIKE 或按名称逐个查询。

该检查同时降低了删除分组的计费风险：没有启用渠道的分组通常无法被路由到，也就不会产生按缺省倍率计费的流量。

## 4. 清除用户专属倍率（规则 2、3）

入口在既有的 `controller.UpdateOption` 的 `GroupRatio` 分支（`controller/option.go:306`），不新增接口：

1. 既有 `ratio_setting.CheckGroupRatio` 校验通过。
2. 解析提交值得到新 key 集合，与 `ratio_setting.GetGroupRatioCopy()` 比较，得到 `removed`（被删除的名称）与 `added`（新出现的名称）。
3. `removed` 非空时执行规则 1 的渠道检查，不通过则拒绝保存，不做任何写入。
4. 保存 option 并发布内存快照（既有逻辑不变）。
5. 以 `targets = removed ∪ added` 为目标名单，**在后台 goroutine 中**分批清除所有用户 `group_ratios` 中这些 key。
6. 立即返回，不等待清理完成。

`targets` 为空时（只改倍率数值、名称集合没变）直接跳过第 5 步，不访问 `users`。

**清理不放在请求内。** 按 §4.1 的限速跑完一张百万级 `users` 表需要一到两分钟，挂在管理员的 HTTP 请求上必然超时；而放宽限速又会打满数据库。既然清理本来就是“尽力完成”（§6），把它移出请求是自然选择：管理员立即拿到删除结果，清理在后台按节奏推进。

这里用的是一个普通的 `gopool.Go` + `recover()`，**不是** SystemTask 那套持久化任务——不新增任务类型、租约、状态表或调度。代价是进程退出即中止，与“尽力完成”的语义一致，残留由规则 3、规则 4 收敛。

**待清理的名称要合并，不能逐次排队。** 每保存一次分组配置就派发一次独立扫描的话，管理员连续改十次就排出十趟全表扫描，而后面几趟基本什么都清不到。实现为「一个待处理名称集合 + 单个 worker」：新名称并入集合；已有 worker 在跑就直接返回，由它下一圈捡走。一轮突发编辑因此只花一趟扫描。（`model.ScheduleUserGroupRatioCleanup`）

### 4.1 清理实现

按主键顺序分批处理，只碰真正持有覆盖的行。**限速与并发约束是这一节的核心，不是可选优化**：

| 约束 | 取值 | 理由 |
|---|---|---|
| 每批读取行数 | 500（常量） | 单次查询耗时可控，内存占用恒定 |
| 批间让步 | 固定 50ms（常量） | 占空比约 50%，把扫描摊平，不让它连续占满 `users` 的读取能力 |
| 并发度 | 1，全程串行 | 任一时刻最多占用 1 条数据库连接（默认池 1000，即 0.1%） |
| 事务 | **不使用**，逐行 CAS 自动提交 | 避免长事务持有 MVCC 快照与行锁 |
| 同时运行的清理 | 1，包级 `sync.Mutex` 串行化 | 连续删除多个分组时不会叠加出多个并发全表扫描 |

后一次清理若发现已有清理在运行，就等待它结束再开始（等待发生在后台 goroutine 里，不阻塞任何请求）。

清理全程**不访问 Redis**（原因见下），因此对缓存与 Redis 连接没有任何负载。

后台 goroutine 用默认的 `gopool.Go`（同 `model/user.go:1538` 的用法），**不得使用 `common.RelayCtxGo` / `relayGoPool`**——那是 relay 专用池（`common/gopool.go:11`），管理任务不应占用它。goroutine 内必须 `recover()`。

不得用 `Find` 一次性加载全表，必须 keyset 分页；不得为了“快点跑完”提高并发或去掉让步——这个操作的目标是不打扰 relay，不是尽快结束。

查询与更新如下：

```
SELECT id, group_ratios FROM users
WHERE id > ? AND group_ratios IS NOT NULL AND group_ratios <> '' AND group_ratios <> '{}'
ORDER BY id LIMIT ?
```

- 用 `Unscoped()`：`users` 有软删除（`model/user.go:108`），跳过软删行会让用户恢复后把旧 key 带回来。
- 每行解析后删除 `targets` 中的 key，无变化则跳过。
- **条件更新**：`UPDATE users SET group_ratios = <new> WHERE id = ? AND group_ratios = <读到的原值>`，只更新这一列，不整行回写。影响 0 行说明有人并发改过该用户，跳过即可。

  这一条不能省：读出 JSON、计算、写回之间存在窗口，管理员此刻保存该用户新增的有效倍率会被整段覆盖。
- **不触碰 Redis 用户缓存**：既不 DEL，也不做字段级更新。清理只写数据库，缓存靠 TTL 自然收敛。

  **不能调用 `InvalidateUserCache`。** 它 DEL 整个 `user:{id}` hash，`Quota` 字段一并消失；而 `DecreaseUserQuota` 在 `BatchUpdateEnabled` 下是「Redis 立即扣减、DB 推迟到批量更新器（`BATCH_UPDATE_INTERVAL` 默认 5 秒）」（`model/user.go:1534-1548`），这个窗口内 Redis 才是权威值。DEL 之后下次认证从偏高的 DB quota 重建，`writeUserCache` 的 Lua 判断 `HEXISTS Quota == 0` 成立并把过期高值写回，用户等于把已消费的额度又拿回去花一次。单用户影响小，但清理是对所有命中用户批量执行，会放大这个既有的潜在问题。

  不失效也是安全的：删除场景下规则 1 已保证该分组没有启用渠道，请求路由不过去，缓存里那份过期覆盖是惰性的；重新添加同名分组的场景下，过期覆盖最多生效一个缓存 TTL（`SyncFrequency`，默认 60 秒），落在“尽力清理”已接受的窗口内。缓存的每次完整写入都会 `EXPIRE`，过期后下次请求即从已清理的数据库重建。
- 留痕用**一条汇总日志**，在清理结束后输出：目标名单、命中用户数、以及**有限数量的用户 ID 样本**（如前 20 个）。不要逐用户、逐倍率写 `LogWarn`——命中量大时日志本身会拖慢删除，也会淹没有用信息。

**按名单删除，不按“不在注册表中就删”。** 名单来自本次请求，无论内存注册表处于什么状态都不可能误删其他分组的覆盖。

## 5. 保存用户（规则 4）

1. 管理员提交用户资料，解析 `group_ratios`；非 JSON、负数、NaN、Infinity 仍作为非法输入拒绝。
2. 剔除当前 `GroupRatio` 中不存在的 key，记录被移除的名称，不再报错。
3. 走现有单用户事务与缓存发布流程。
4. 响应返回 `removed_group_ratios`，前端在非空时提示“已自动移除不存在的分组”。

`updateUserRequest` 应用 `*string` shadow 字段接收 `group_ratios`：字段省略表示保留 `originUser.GroupRatios`，显式 `{}` 表示清空。否则非前端 API 调用方省略该可选字段时会意外清空全部专属倍率。

注意 shadow 字段一旦存在，嵌入的 `model.User.GroupRatios` 就不再被 JSON 解码填充（外层同名字段优先），非 nil 分支必须显式回填 `updatedUser.GroupRatios = *request.GroupRatios`。实现时这里漏过一次，由用户更新的测试挡下。

前端编辑抽屉加载时，把不在当前分组列表中的行标记为「分组已删除，保存时将移除」。行内选择器是 `GroupCombobox`（`web/src/features/users/components/users-mutate-drawer.tsx`），失效值不在候选中，不标记就表现为一个无法解释的空选择框。

用户列表页展示 `group_ratios` 的列（`users-columns.tsx:183`）**未改动**：该列的渲染工厂拿不到分组列表，而删除分组现在会自动清理，口径不一致的窗口只存在于清理跑完之前。若之后要对齐，需要把分组列表传进列工厂。

## 6. 边界与错误处理

- 只改倍率、名称集合不变：不访问 `users`。
- 分组重命名：按“删除旧名 + 新增新名”处理，两个名字都进 `targets`，旧覆盖被清除，不迁移到新名字。
- 同名重建：新增时清除同名残留（规则 3），旧倍率不会复活。
- 用户 `group_ratios` 为空或 `{}`：被扫描谓词排除。
- 显式倍率 `0`：不在 `targets` 中就保留，不能当作空值丢弃。
- 某行 JSON 损坏：跳过并记录用户 ID，不中断整次清理，也不覆盖未知内容。
- 渠道检查失败（DB 错误）：拒绝保存并提示重试，不做任何写入。
- 清理中途失败或进程退出：**本设计的清理是“尽力完成”，不承诺一定跑完，也不提供重试入口。** 清理在后台异步进行，接口在保存成功时即返回，前端提示“分组已删除，历史专属倍率正在后台清理”。

  重新保存相同配置**不会**重新扫描——此时 `removed` 已为空，这是设计的一部分，不要把它当成重试路径。残留只有两条收敛途径：规则 3（该名称被重新添加时清除）与规则 4（该用户被编辑时清除）。

  这是刻意的取舍：要保证“删除必定清理完成”，就得引入持久化重试任务或长事务，两者都比残留本身的代价大。残留是惰性的——分组已不存在，规则 1 又保证它没有启用渠道，因此这些 key 不会产生错误计费。
- 缓存：清理不操作 Redis，用户缓存最迟在 TTL（默认 60s，`common/redis.go:26`）后自然收敛，不存在缓存相关的失败路径。
- 多实例：option 按既有同步周期（默认 60s）收敛，本设计不改变该语义。
- 错误消息走 `ApiErrorI18n`，新增键加入 `i18n/keys.go` 与 `i18n/locales/en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`；前端提示覆盖 en、zh、zh-TW、fr、ru、ja、vi。

## 7. 主链影响与性能

**relay 热路径零代码增量。** 本设计不修改 `ResolveGroupRatio`、认证准入、渠道选择或任何每请求执行的代码。relay 每请求新增 DB / Redis / 锁 / goroutine 全部为 0。

**但存在低频、被限速的数据库资源竞争，不能宣称“与 relay 无冲突”**：清理会扫描整张 `users` 表，与 relay 在缓存未命中时的用户回源共用同一个数据库连接池。§4.1 的限速把这份占用压到任一时刻 1 条连接、约 50% 占空比。

清理不触碰 Redis，因此不产生额外的缓存失效与回源放大。

**主链影响逐项结论**：

| 检查项 | 结论 |
|---|---|
| 每请求执行的代码 | 零改动，不碰 `ResolveGroupRatio`、认证、distributor、渠道选择 |
| `users` 行锁 | 主键等值 UPDATE，单行锁立即提交，无 gap lock |
| 长事务 | 不使用事务，每批 SELECT 与每行 UPDATE 均 autocommit，PostgreSQL 不持有长快照 |
| 连接池 | 串行 1 条，约 50% 占空比 |
| SQLite | 库级写锁，但实际更新行数很少（仅含目标 key 的行），每次持锁极短 |
| goroutine | 1 个，走默认 `gopool`，不占用 `relayGoPool`，内部 `recover()` |
| Redis | 不访问 |
| `groupRatioMap` 写锁 | 频率与持有时间均不变，本设计不增加配置写入 |

**管理员请求的成本**（同步部分）：

- 渠道检查：一次带状态过滤的 `channels` 查询，只取两列。
- option 保存：既有逻辑。
- 清理任务派发：并入待处理集合；仅在当前无 worker 时启动一次 `gopool.Go`。

请求耗时与用户规模无关。

**后台清理的成本**（异步部分）：

- 扫描：`users` 主键顺序分批。非空谓词把 JSON 解析压到实际持有覆盖的行，但不减少扫描行数——`group_ratios` 是跨库 TEXT，无法建可移植索引，也不为此在 relay 热表 `users` 上加索引。
- 写入：只有真正含目标 key 的行会被更新，通常个位数到几十行。
- 节奏：500 行/批 + 50ms 让步，1M 用户约 2000 批、总时长一到两分钟；期间串行占用 1 条连接（默认 `MaxOpenConns = 1000`，`setting/operation_setting/db_pool_setting.go:26`）。
- Redis：0 次访问。

时长随用户规模线性增长，但由于全程限速且不在请求内，规模增大只是清理跑得更久，不会造成超时或数据库压力尖峰。

**共享资源**：`users` 仅在删除分组与编辑用户时写入，清理的扫描与 relay 的用户回源共用连接池（见上，已限速）；`channels` 为只读查询；Redis 不访问。不新建连接池、不新建 goroutine 池。

## 8. 测试计划

### 8.1 渠道检查

- 分组下有启用渠道 → 拒绝删除，`GroupRatio` 未变更，`users` 无任何写入。
- 分组下只有已禁用渠道 → 允许删除。
- 分组下无渠道 → 允许删除。
- 多个分组同时删除，其中一个有启用渠道 → 整体拒绝。
- 渠道属于多个分组（`group` 为逗号分隔、含空格）时能正确命中，覆盖 `Channel.GetGroups()` 的解析。
- 查询走 `channels` 而非 `abilities`：构造“`channels` 中有启用渠道、`abilities` 中缺少对应行”的不一致状态，断言仍然拒绝删除。

### 8.2 清理

- 删除分组后，所有用户对该分组的 key 消失，其他 key 与显式 `0` 保留（测试中等待后台清理完成）。
- 管理员请求**不等待**清理：保存接口在派发后立即返回，响应耗时与用户规模无关。
- 分批与限速：构造超过一批的数据量，断言按 keyset 分页多批处理、不是一次性 `Find` 全表。
- 串行化：连续触发两次清理，断言第二次等待第一次结束，不并发扫描。
- 逐行 CAS 自动提交，不包在单个大事务里。
- 新增分组时清除同名残留（规则 3 的核心用例：删除 → 残留 → 重新添加同名 → 残留消失）。
- 只改倍率数值时**不产生任何 `users` 的 SELECT/UPDATE**。
- 重命名：旧名与新名的覆盖都被清除，不发生迁移。
- CAS：读取后、写回前修改该用户的 `group_ratios`，断言并发写入不被覆盖、该行被跳过。
- 只更新 `group_ratios` 单列，`quota`、`group`、`auth_version` 不变。
- 软删除用户同样被清理。
- 损坏 JSON 的行被跳过且清理继续。
- **清理不触碰 Redis**：断言被修改用户的 `user:{id}` 缓存在清理后依然存在且 `Quota` 字段未被改动（防止回退到会造成配额回滚的 DEL 实现）。
- 清理后序列化为 `{}` 或空串，`ParseUserGroupRatios` 走快速返回。

### 8.3 保存用户

- 历史失效 key 被自动移除，其他字段保存成功（报障回归锁）。
- 有效 key 正常保存；显式 `0` 保留。
- 非法 JSON、负倍率仍失败。
- `group_ratios` 省略时保留原值；显式 `{}` 才清空。
- 响应返回 `removed_group_ratios`。
- 创建用户接口不受影响。

### 8.4 兼容与前端

统一使用 GORM，不含数据库专属 SQL；CAS 更新与 keyset 分页需在 SQLite / MySQL / PostgreSQL 上验证一致。优先用项目真实数据库环境跑集成测试。

前端：`bun run typecheck`、`bun run lint`；验证删除受阻时的渠道提示、编辑抽屉的失效行标记、保存后的自动移除提示。

实现完成后运行相关包测试，并最终运行 `go test ./...`。

## 9. 已接受的残余

- **清理是尽力完成，不承诺一定跑完**：中途失败留下的 key 只能靠规则 3（重新添加同名分组时清除）或规则 4（编辑该用户时清除）收敛。重新保存相同配置不会重新扫描，不是重试路径。要做到“删除必定清理完成”需要持久化重试任务或长事务，代价大于残留本身，故不做。
- 残留是惰性的：分组已不存在，且规则 1 保证它没有启用渠道，因此这些 key 不会产生错误计费。
- 清理在后台 goroutine 中执行，**进程退出即中止**，未完成部分同样由规则 3、规则 4 收敛。
- 清理期间会与 relay 争抢数据库连接与 IO，已由 §4.1 的批量与让步限速压到任一时刻 1 条连接、约 50% 占空比。
- ~~清理只写数据库、不动缓存，因此被清理用户的 relay 计费最多在一个缓存 TTL（默认 60 秒）内仍使用旧的专属倍率……重新添加同名分组的场景下它落在“尽力清理”窗口内。~~ **已修订（§13）**：HK 测试服实测重建同名分组后 15 秒内的请求全部仍按旧专属倍率计费，不再接受。现在清理改写用户后立即刷新其缓存里的专属倍率，且清理完成前拒绝重建同名分组。
- 多实例按既有 option 同步周期收敛，不保证集群瞬时一致。分组倍率与用户专属倍率的**写入**只允许在主节点进行（§11.1），因此清理计划与豁免表（§11.2）不受从节点陈旧视图影响。
- token 显式绑定被删分组时，既有校验返回 403「分组已被弃用」（`middleware/auth.go:471-476`），本设计不改动。
- 本设计只处理用户专属倍率与启用渠道两类引用；subscription 计划快照、充值配置等仍可能保存被删分组名称，属独立议题。

## 10. 实现状态

已实现并通过测试（`go test ./...` 83 个包全绿；前端 `bun run typecheck` 通过，`bun run lint` 无新增问题）：

- 规则 1～3：`controller/option.go` 的 `planGroupRatioCleanup` 与 `model/user_group_ratio_cleanup.go`，9 个用例（`controller/group_ratio_cleanup_plan_test.go`）。
- 规则 4：`controller/user.go`，7 个用例（`controller/user_group_ratio_test.go`）。
- 清理与渠道检查：15 个用例（`model/user_group_ratio_cleanup_test.go`），含 CAS 拒绝陈旧值、软删除用户、损坏 JSON 跳过、缓存未被触碰（真实 Redis）。
- JSON 剔除函数：11 个用例（`setting/ratio_setting/user_group_ratio_filter_test.go`），含绕过解析记忆表。

与设计的唯一偏差：用户列表列的口径未对齐（见 §5）。

### 10.1 端到端验证

在隔离的 MySQL schema + 独立 Redis db 上真实启动网关（`web/dist` 内嵌），上游指向本地 mock（OpenAI 兼容 `/v1/chat/completions`），用并发驱动随机模拟用户压测。

**对照实验（2 万用户，16 并发中继，各 30 秒）**

| | 基线（无 churn） | 清理 churn |
|---|---|---|
| 中继成功 | 33426 | 34464 |
| 中继失败 | 0 | **0** |
| 平均延迟 | 14ms | 14ms |
| 最大延迟 | 42ms | 221ms |
| 吞吐 | 1114 rps | 1149 rps |

churn 组期间发生 49 轮「新增分组 → 删除分组」，共 98 次配置写入，每次删除都派发一次全表清理。日志显示单次清理 `scanned=20001 cleaned=20001`，耗时约 2 秒——与 §4.1 的限速（500 行/批 + 50ms 让步 → 40 批）完全吻合。

结论：吞吐与平均延迟无差异；唯一可见代价是批量清理期间的单次延迟尖峰（42ms → 221ms），出现在一次性重写 2 万行的那一趟扫描中。

**规则断言（对运行中的服务）**：7 项全部通过——分组下有启用渠道时拒绝删除且注册表不变、无渠道时正常删除、失效规则不阻塞用户保存并回报 `removed_group_ratios`、有效分组的负倍率仍然报错、重建同名分组不复活旧倍率。

**并发正确性**：2 万用户、约 1150 rps 持续中继下，`e2e_stale` 残留归零，`vip`（有效分组）规则 20001 条全部保留，无一条被误删。

**两个与本功能无关的观察**（已排除）：一是网关自带的过载守卫（CPU>90% 直接 503）在单机同时跑压测端、网关、mock、MySQL、Redis 时会触发，测试中关闭；二是并发编辑用户会因 `auth_version` 围栏让该用户的在途请求短暂失败，只在「用户编辑」workers 开启时出现，仅跑清理 churn 时为 0 失败——该路径本设计未改动。

## 11. 未提交代码审计修订（2026-09-15）

两处缺陷都经真实复现确认（先写断言正确行为的用例，在修复前失败）。

### 11.1 分组倍率与用户专属倍率只允许在主节点修改

`planGroupRatioCleanup` 以**本节点内存**里的分组表为基准判断「新增 / 删除」；`UpdateUser` 也用本节点内存过滤「已删除分组」。从节点内存落后于主节点（最多一个 option 同步周期）时：

- 在从节点保存分组倍率，会把主节点刚建的分组误判为新增，清掉其用户专属倍率。实测只把某分组倍率从 1.5 调到 2，用户规则就被清成 `{}`。
- 在从节点编辑用户，会把刚建分组的专属倍率当成失效条目丢弃。

部署约定是管理操作只在主节点做，现把约定落到代码：`UpdateOption` 保存 `GroupRatio`、`UpdateUser` **改动** `group_ratios`（按解析后的 map 比较，表单原样回传不算改动）时，非主节点返回 `group.ratio_master_required`。其它用户字段的编辑不受影响。

### 11.2 清理窗口内新分配的倍率被扫掉

新增 / 删除分组会调度一次对该分组的全表清理。清理扫描尚未走到某用户时，管理员给该用户分配了该分组的新倍率——扫描读到的是新值，CAS 也挡不住（CAS 只挡「扫描读行之后」的并发保存），新规则被当作旧残留删除。

修复（2026-09-15 审查后简化，见 §12）：该分组的清理仍在排队或扫描时，`UpdateUser` 拒绝为它**新增或改值**（`changedGroupRatioNames`，原样回传的旧条目不算），提示 `user.group_ratio_cleanup_in_progress`「分组刚被删除或重建，历史专属倍率仍在清理中，请约 1 分钟后再设置」。判断由 `model.GroupRatioCleanupsActive` 读清理队列的 pending / running 集合，依赖 §11.1：保存与清理都只发生在主节点。

用例：`model/user_group_ratio_cleanup_test.go`（`TestGroupRatioCleanupsActive`）；`controller/group_ratio_master_gate_test.go`（两处主节点校验、原样回传放行、清理中拒绝新规则、`changedGroupRatioNames`）。

## 12. 审查修订（2026-09-15）

- **从节点编辑用户仍会丢规则**：§11.1 只拦住了「改动」，原样回传时仍按从节点可能落后的分组表过滤，主节点刚建的分组上的规则会被当成失效条目删掉。现在从节点对原样回传保持库里的值不动，不做自愈过滤。用例 `TestUpdateUserOnSlaveKeepsRatiosForGroupsUnknownLocally`。
- **豁免表简化为「清理中拒绝设置」**：原先的豁免登记表（登记 / 撤销 / 代际作废，约 80 行加 5 个测试）换成上面 §11.2 的一次检查。代价是分组重建后，管理员要等约 1 分钟才能给用户分配新倍率。扫描锁 `groupRatioCleanupMu` 一并删除（清理队列本就只有一个 worker）。
- **管理端解析不再挤进 relay 的解析缓存**：`UpdateUser` 本地解析一次，不走 `ParseUserGroupRatios`（那是 relay 的记忆化缓存）。
- **清理扫描改为按主键窗口**：原先 `WHERE id > ? AND group_ratios <> '' … LIMIT 500` 在绝大多数用户没有专属倍率时要沿主键走很远才能凑满一批，第一条语句就可能扫完整张表，§4.1 / §7 所说的「每批让步」实际不成立。现在每一步先用 `ORDER BY id LIMIT 1 OFFSET 4999` 取这一窗口的上界（最多走 5000 条主键索引项），再只在 `(lastID, upper]` 内筛选，窗口之间让步 50ms；不依赖 id 连续。100 万用户约 200 个窗口。
- **前端**：
  - 分组倍率与「可选分组」「充值倍率」一起保存时，先保存分组倍率，被拒（仍有启用渠道、非主节点）就停止，不再出现「分组倍率没删、可选分组已删」的部分提交。
  - 删除分组的保存确认框列出被删的分组，并说明这些分组上的用户专属倍率会被永久清除。
  - 用户编辑抽屉：分组列表未加载成功时不再把所有规则标成「分组已删除」；保存返回 `removed_group_ratios` 时提示被丢弃的规则。
  - 恢复了被整文件重新格式化的 `users-mutate-drawer.tsx`，diff 只剩逻辑改动。

仍未处理：打开很久的设置页整张覆盖 `GroupRatio` 时，会把别人刚建的分组判成删除并清掉其用户规则（此前重新加回分组即可恢复，现在不可逆）。需要给 `GroupRatio` 加版本校验，另行设计。

controller 测试进程里 `InitEnv` 不运行，`IsMasterNode` 零值为 false，与生产默认（`NODE_TYPE` 未设置即主节点）相反；TestMain 现对齐为 `true`，需要从节点行为的用例显式切换。

## 13. 计费正确性修订：重建同名分组不再沿用旧专属倍率（2026-09-16）

**问题（HK 测试服套件 D 实测）**：用户在分组 H 上有专属倍率 0.25。调用进行中停用 H 的渠道、删除 H，待清理完成后立即以新全局倍率 3 重建 H：重建后 15 秒内的 50 个请求**全部仍按 0.25 计费**。其余场景（调用中删除专属倍率、修改分组倍率、删除其它分组、删除后请求被拒且不扣费、预扣退回、日志与余额一致）全部正确。

**根因**：

1. 清理只写数据库；relay 从 Redis 用户缓存 `user:{id}` 的 `GroupRatios` 字段取专属倍率，缓存要到 TTL（`SyncFrequency`，默认 60 秒）过期才从数据库重建。
2. 清理扫描尚未走到的用户，数据库里的旧条目本身还在（5 万用户时清理约 5 分钟，HK 测试 E3）。在这段时间内重建同名分组，同样会按旧倍率计费。

**修订（用户选定方案，relay 热路径仍零改动）**：

- **清理后刷新缓存**：`CleanupUserGroupRatios` 每成功改写一个用户，若启用 Redis 就调用 `PublishUserAuthCache`。它按库里最新值做字段级 `HSET`（仅在缓存已存在时写，走 auth version 围栏），**不 DEL、不写 `Quota`**，因此不会引发 §4.1 所述的批量扣费窗口内配额回滚。刷新失败只记日志，由 TTL 兜底。
- **清理完成前拒绝重建**：`planGroupRatioCleanup` 对本次**新增**的分组名检查 `GroupRatioCleanupsActive`（队列 pending / running），仍在清理则拒绝保存，返回 `group.recreate_cleanup_in_progress`「分组刚被删除，用户的旧专属倍率仍在清理中，请稍后再添加」。删除分组、修改其它分组倍率不受影响。

两者合起来：重建的分组要么被拒（清理未完成），要么出现在数据库与缓存都已清掉之后，旧专属倍率不再参与计费。§4.1、§7 中「清理不访问 Redis」的表述由本节取代。

**代价**：

- 清理对每个**真正持有该分组倍率**的用户多一次 Redis `EXISTS`；只有该用户的缓存存在（近期活跃）时，才再做一次按主键的用户读取和一次 Redis `EVAL`。没有缓存的用户无需刷新——之后回填缓存时读到的就是已清理的数据库值。首版对每个用户都读库刷新，HK E3 的 5 万用户清理从 322 秒拖到 400 秒以上，因此加了这层判断。
- 删除分组后要等清理完成才能重建同名分组（通常数秒；5 万用户约数分钟）。

**仍存在（已知残余，用户决定暂不处理，2026-09-16）**：

- **中断清理的遗留 + 重建同名分组**：「清理完成前拒绝重建」只认得**正在运行**的清理。若某次清理被中断（服务在扫描中途重启、数据库报错，§9 的「尽力清理」），用户行里会留下旧条目；之后重建同名分组时没有运行中的清理可拦，重建会被放行，并照常调度一次针对该名字的扫描。扫描走到该用户之前，这位用户在重建分组上的请求仍按旧专属倍率计费。HK 套件 F 实测：遗留 `{H:0.3}`，重建 H（全局 2）后连续 8 个请求，第 1 个按 0.3、其余按 2 计费；窗口长短取决于扫描走到该用户的时间，用户少时不到 1 秒。触发需要「清理被中断」与「重建同名分组」同时发生，故暂列残余。若要消除，可在添加分组时查询是否仍有用户行持有该名字，有则先清理并拒绝本次保存。
- 多节点部署时，从节点的分组表按 option 同步周期收敛，与 §9 已接受的多实例收敛一致；分组倍率只在主节点写入（§11.1）。

**懒删除（规则 4）的验证**：HK 套件 F——分组不存在时遗留条目既不放行（绑定该分组的令牌 403）也不影响用户其它分组的计费；管理员在用户抽屉保存（请求带 `group_ratios`）时遗留被剔除并通过 `removed_group_ratios` 回报；只改其它字段、请求不带 `group_ratios` 时库里的值原样保留（界面编辑总会带上该字段）。

**用例**：

- `model/user_group_ratio_cleanup_test.go`：`TestCleanupUserGroupRatios_RefreshesCachedRatiosKeepsQuota`（取代原 `LeavesUserCacheIntact`：断言缓存仍在、`Quota` 不变、`GroupRatios` 已去掉被删分组）；`TestCleanupUserGroupRatios_DoesNotCreateMissingUserCache`（无缓存的用户不回库、不新建缓存）。
- `controller/group_ratio_cleanup_plan_test.go`：`TestPlanGroupRatioCleanupRejectsRecreatingGroupUnderCleanup`、`TestPlanGroupRatioCleanupAllowsOtherChangesDuringCleanup`。
- HK 套件 D：调用中删除专属倍率、修改分组倍率、删除其它分组、删除并重建用户所在分组，逐条核对日志倍率、扣费与余额（39/39）；套件 E3：5 万用户清理 349 秒、清理中重建被拒、清理完成后重建并设置倍率（10/10）；套件 F：懒删除与遗留（10/11，失败项即上面的已知残余）。
