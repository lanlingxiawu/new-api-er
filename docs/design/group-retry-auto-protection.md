# 分组重试统一保存与自动保护

状态：用户已确认设计并授权实施；已实现，专项验证记录见文末。2026-10-19。

## 目标与范围

1. 将当前“保存重试次数 / 保存重试状态码”合并为一个“保存”按钮，次数、状态码、自动保护一起校验和保存，避免半成功。
2. 每个分组可配置统计窗口、RPM、上游响应延时、慢响应次数、冷却策略；满足条件后临时禁止该组重试，冷却结束恢复原策略。
3. 用户已确认：次数指窗口内慢响应累计次数，冷却后自动恢复；统计窗口独立配置，不固定 60 秒；递增对象为冷却时间，支持配置初始时长、倍数、上限和稳定后的重置时间。
4. 自动保护只限制额外重试，首次请求继续正常服务；不禁用渠道、不拒绝该组新请求、不修改管理员设置的重试次数。
5. 用户追加约束：优先复用现有监控逻辑，减少计时、计算与资源开销。本稿据此改为复用现有完成样本，不再新增上游首字节采集回调。

关联现有设计：`group-retry-status-codes.md`、`relay-retry-time-budget.md`、`relay-timeout-retry-config-ui.md`。
现有代码为准：次数使用 `GroupRetryTimes`，状态码使用 `group_retry_status_setting`；前端当前分别调用单键和模块保存接口。
本设计取代上述文档中“两个按钮、分别保存”的旧交互。

## 现有监控复用核对

### 合并到 zhuzhan 的兼容调整

- 保留目标分支按请求结束统一调用 `RecordRelayResult` 的监控方式：在该入口传递保护采样资格，沿用同一个 `Sample` 分发，不恢复旧的额度结算成功采样和控制器失败采样，避免重复计数。
- 普通请求和任务提交沿用目标分支的统一重试决策，在原决策中加入保护暂停判断；保留任务持久化、计费、插件和错误处理流程。任务提交完成时只记录一次保护样本，异步任务轮询完成不重复记录。
- 保留旧 `group_retry_status_setting` 加 `group_retry_times` 的聚合请求兼容入口；新编辑器使用 `group_retry_policy` 一次保存次数、状态码与保护参数，两条路径共用原配置锁和事务写入设施。
- Main Chain Impact：新增同步工作仅为采样资格认领、快照判断和非阻塞队列投递；Redis 聚合仍在独立后台 worker。沿用本设计的独立 Redis 命名空间、连接池和不可变状态快照，没有新增数据库或网络访问。

已核对 `pkg/perf_metrics/metrics.go`、`types.go`、`flush.go`、`setting/perf_metrics_setting/config.go`，以及 controller/relay.go、service/text_quota.go、service/quota.go 中的调用点。

- 现有 `RecordRelaySample` 已计算 Sample 的 Group、LatencyMs、TtftMs、HasTtft、Success，成功/失败调用点已有异步记录。复用一次生成的样本，不再次计时，不新增 HTTP trace，不遍历响应内容。
- 在现有 `Record(Sample)` 入口增加默认关闭的轻量分发，将同一份值类型样本交给保护统计；分发必须在旧监控 enabled 判断之前，保证关闭历史图表采集不意外关闭自动保护。不重复调用整个 Record，不增加原有 recordRedis 的执行次数。
- 历史监控默认小时桶，只保留次数/延时总和；不能据此准确还原自定义短窗口 RPM 或超过任意延时阈值的次数。因此仅补充按组的窗口请求数与慢响应数，不复制模型维度、TPS、成功率、历史 DB 表及清理任务。
- 现有 recordRedis 在记录任务中同步调用共享池；复用它会扩大共享资源竞争。复用样本来源与聚合模式，新增保护数据仍用独立前缀和后台限量连接，不能改写已有 perf:* 键或 drain 历史统计桶。
- `model/log.go` 的 RPM 来自消费日志 DB 查询，不能作为每秒保护判定来源；渠道 ResponseTime 是探测结果，不代表实际用户流量，也不采用。
- 取舍：保护基于已完成请求的观测，与现有监控口径一致；长流未结束时不会提前计入。这比另建实时尝试采集器节省资源，但对长请求的保护触发会更晚，必须在页面说明。
- 任务等没有现成 Sample 的入口只补一次最终结果适配，复用已有开始/结束时间；没有可信延时的路径不纳入慢次数。实施前用测试确认各入口最多产出一次，不把同一结算和错误回调重复累计。

## 交互与保存

- 复用当前分组行、数字输入、开关及底部按钮布局，仅保留一个主保存按钮。
- 自动保护全局开关默认关闭；每组可独立启用，在该行展开统计窗口（秒）、RPM 阈值、延时阈值（秒）、慢响应次数、冷却模式（固定/递增）及初始冷却（秒），避免全部字段挤在一行。
- 递增模式再展示倍数、冷却上限、稳定多久重置；固定模式隐藏这些字段。窗口默认值 60 秒只是初始建议，可以修改，例如 30、120、300 秒。
- 展示“收集样本中 / 正常 / 重试已暂停 / 监测暂不可用”，暂停时显示本轮冷却时长、递增级别及恢复时间，正常时显示所选窗口内折算 RPM 和慢响应次数。
- 无修改时禁用保存；保存期间禁用编辑和重复提交；统一校验所有行，定位重复分组、无效数字和状态码。
- 保存失败保留完整草稿；只有 `success=true && data.applied=true` 才清除修改标记、刷新配置、提示一次成功。不能只检查 HTTP 200 或 success。
- 删除一行同时删除该行次数、状态码及自动保护规则；运行状态随规则版本变化失效。
- 保存与运行状态查询分开，状态刷新不得覆盖未保存草稿。
- 新 UI 文案覆盖七种语言；实施时先加载 i18n-translate 技能。后端错误沿用仓库实际语言资源与消息常量。

## 核心业务规则

每组统计窗口 W = window_seconds，可配置 10–3600 秒，默认建议 60 秒。采用 1 秒桶，边界最多存在 1 秒统计粒度误差。
RPM 始终是每分钟速率：`RPM = 窗口内请求数 × 60 / W`；慢响应次数是该窗口内的原始次数，不折算。
例如 W=120 秒、窗口内 600 个请求，RPM=300，而不是 600。判定用整数交叉相乘避免取整边界错误，展示可保留小数。
启用或规则变更后收集满一个窗口再判定，避免用局部采样错误代表完整窗口；冷却结束无需重新预热 RPM，仅重新累计慢样本。

触发条件：`RPM >= rpm_threshold` 且 `slow_count >= slow_count_threshold`。
慢响应条件：复用样本的响应延时 `>= latency_threshold_ms`。

- RPM：最近 W 秒已完成且实际调用过上游的客户端请求数折算为每分钟速率，一次请求只记一次，不重复计入内部重试；归属最终实际分组，auto 不能记为字面量 auto。不计鉴权失败、选不到渠道等未发送请求。
- 慢响应次数：每个完成样本最多累计一次，成功与失败的慢响应均纳入。无需保留每次尝试、跨组去重 map 或请求 ID 集合。
- 延时口径沿用监控：流式优先使用有效 TtftMs（首次响应），非流式使用 LatencyMs（总响应耗时）；流式无首响应且上游超时按已等待时间判断，不能把正常长流总时长当慢首响应。现有时间含请求准备及可能的重试等待，不宣称纯上游单次 HTTP 首字节耗时。
- 样本补充最少的流式/上游已调用/取消或超时证据，复用已有标志判断，不加新的观测回调。仅旧 Sample 的 HasTtft 不足以区分非流式与无首响应流式，不能直接混用。
- 本地错误、客户端主动取消不作为慢响应；无可靠延时的 SDK/WebSocket 路径不伪造样本。采集缺失时标明覆盖范围。
- 已暂停时，新请求仍有首次调用，原本允许的任何额外重试均被保护闸门否决，包括用户级重试次数覆盖。既有状态码、总次数、预算、流已交付、客户端断开等限制仍有效。
- 采用失败尝试的实际组（auto 使用 AutoGroup），不使用用户注册分组或已前移的候选下标。该组触发后结束该请求的重试，不引入“跳过被保护组继续跨组”的新路由策略。
- 冷却截止后恢复已有策略，清空该轮慢响应累计，需新的慢响应样本才可再次触发；RPM 可继续统计。暂停期间不重复延长冷却时间。
- 冷却固定模式：每次使用 cooldown_seconds。递增模式：第 k 次关联触发使用 `min(max_cooldown_seconds, cooldown_seconds × cooldown_multiplier^(k-1))`，例如 60→120→240→300→300 秒。只在冷却结束后再次达到完整触发条件才升一级，后台轮询或暂停期间样本不会升级。
- 递增重置：恢复重试后连续 reset_after_seconds 未再次触发，且监测持续可用，则级别归零；下次从初始冷却开始。监测中断不视为稳定恢复。规则修改、删除或关闭也清除级别。
- 递增计算只发生在后台状态转换时，使用有上限的安全整数乘法；不在每次请求上求幂，也不增加采集频率。各节点通过同一 Redis 原子状态决定级别，避免重复升级。
- 规则关闭、删除或修改时，该组旧版本运行状态不再生效；总开关关闭立即绕过本功能。
- 不终止已经发送的上游请求，不改变扣费、退款或流式生命周期。普通 relay 与已有任务重试循环均加保护判断；不为 MJ、Realtime 新增重试机制。

## 配置与数据模型

新增 `setting/operation_setting/group_retry_guard_setting.go`，通过 ConfigManager 注册不可变配置快照：

| 字段 | 默认 | 含义 |
|---|---|---|
| enabled | false | 功能总开关 |
| rules | "{}" | 分组名到保护参数的 JSON 字符串 |
| revision | "" | 服务端生成的配置版本；不可由页面或单键接口编辑，用于使 Redis 旧状态失效 |

规则示例，仅为格式示例，不自动给现有分组启用这些数值：

```json
{
  "vip": {
    "enabled": true,
    "window_seconds": 120,
    "rpm_threshold": 300,
    "latency_threshold_ms": 10000,
    "slow_count_threshold": 5,
    "cooldown_mode": "progressive",
    "cooldown_seconds": 60,
    "cooldown_multiplier": 2,
    "max_cooldown_seconds": 300,
    "reset_after_seconds": 600
  }
}
```

校验：窗口 10–3600 秒，RPM 1–10000000，延时 1–3600000 ms，次数 1–10000000，初始/最大冷却 1–86400 秒；递增倍数为整数 2–10，最大冷却不得小于初始值，稳定重置 10–86400 秒。模式仅 fixed/progressive，固定模式允许省略递增字段，递增模式要求完整提供。其他必填字段拒绝缺失、null、错误类型和未知字段，数值均为整数。界面秒输入转换为整数毫秒，拒绝不可表示精度。分组非空、无首尾空白；最多 256 组、rules 最多 64 KiB。关闭状态不应阻止管理员关闭已有无效配置。

复用 options 表，新增 `group_retry_guard_setting.enabled/rules/revision`，无需新表、列或大型索引。既有次数和状态码持久化键保持兼容。不将运行状态写回 options。
修改保护规则或总开关会更新整份保护配置的 revision，因此所有保护组重新预热并清除旧冷却级别；只修改次数或状态码不会重置保护状态。此选择避免节点在快速关闭/重启保护后重新采用旧冷却状态。

## API 契约与权限

继续使用 `GET /api/option/?scope=system-tuning.group-retry-times` 读取配置。
复用 `PUT /api/option/group`，为当前页面增加聚合保存模块 `group_retry_policy`：

```json
{
  "scope": "system-tuning.group-retry-times",
  "module": "group_retry_policy",
  "values": {
    "times": "{\"vip\":1}",
    "status_enabled": "true",
    "status_rules": "{\"vip\":\"429,500-503\"}",
    "guard_enabled": "false",
    "guard_rules": "{}"
  }
}
```

这是保存聚合模块，映射到既有次数/状态码键及新增 guard 键，不持久化第二套 policy 副本。
只接受这五个字段的完整请求，拒绝未知字段；复用次数/状态码原校验，补充根对象/null/类型检查。
所有候选配置预解析、校验成功后，通过 `persistOptionsTx` 一个事务写入五个用户配置键和一个内部 revision 键；失败不更新内存。
事务提交后发布已验证配置并更新 OptionMap。后台同步仍按现有机制传播，各节点不承诺同时生效。
旧单键与模块入口继续兼容并共用相应校验、设置写入串行化规则，避免与统一保存交错写库；不在 relay 上获取保存锁。
数据库原子提交不等于多个既有内存模块原子切换：热发布期间允许极短的新旧策略组合，既有安全上限始终生效，不宣称请求级全配置事务。

成功响应：`{"success":true,"message":"","data":{"applied":true}}`。
业务校验失败用 `ApiErrorI18n`；保存 DB 故障同样沿用设置接口的 `ApiErrorI18n(MsgDatabaseError)`，返回 HTTP 200、success=false，不返回数据库内部错误。现有 helper 固定使用 HTTP 200，本功能未引入另一种错误响应结构。
不得返回 success=true 掩盖写入失败；现有 applied=false 分支前端作为失败处理。

新增只读 `GET /api/option/group-retry/status?scope=system-tuning.group-retry-times`：

```json
{
  "success": true,
  "message": "",
  "data": {
    "available": true,
    "updated_at": 1792382400,
    "groups": [{"group":"vip","window_seconds":120,"ready":true,"rpm":320,"slow_count":5,"cooldown_level":1,"current_cooldown_seconds":60,"retry_disabled_until":1792382460}]
  }
}
```

两个接口均在既有 option 的 AdminAuth 组内，复用 RequireSystemSettingsScope 的 Edit/View 权限。
扩展该 scope 的模块与键白名单，不扩大其他页面权限；状态接口不能借别的合法 scope 越权。
无公开端点。状态页每 5 秒刷新当前节点最近同步的集群快照，过期明确显示不可用。

## 数据流与后台运行

保存：页面完整草稿 → 权限 → 完整校验 → options 事务 → 配置热发布 → 前端确认 applied。

监测：现有请求完成/结算监控入口 → 同一份 Sample 轻量分发 → 非阻塞队列 → 单个后台 worker 聚合 → Redis 秒桶及保护状态 → 各节点后台同步不可变快照 → 重试前本地读取。

- Redis 为运行状态权威来源，支持多实例累加；不查询消费日志计算 RPM，也不复用限流计数。
- 独立命名空间 `group_retry_guard:v1:{groupHash}:...`，包含规则内容指纹隔离版本，避免旧事件污染新规则。只使用规范配置中的分组，不接受任意请求字符串创建键。
- 每组最多 W 个环形秒槽及一份冷却/递增状态，有界存储；秒槽逻辑保留 W 秒，随时间推进清除，不给零流量的秒预建计数。整个状态 hash 的 TTL 为剩余冷却+稳定重置期+W+60 秒。不能在冷却刚结束时删除递增级别。节点批次去重键 TTL 为 W+60 秒；旧规则键依 TTL 回收，不做全库扫描。
- 用滑动总数随秒桶进入/过期增减，不在每次判定时重扫整个窗口；长时间无数据时直接清空过期统计，不逐秒遍历历史。递增冷却不会增加桶数或改变窗口。
- 使用 Redis 服务器时间统一桶与冷却时钟；复用 Sample 已算好的延时，本地完成时间只用于过期样本过滤。
- 每秒批量汇总一次，Lua 在同组 hash slot 原子更新计数、检查阈值并设置冷却；仅首次触发设置截止时间。恢复后忽略上一轮遗留慢响应事件。
- 每组批次带节点序列号，防止同一批重复累计；去重数据随 TTL 过期。生产遇到执行结果不确定的超时会丢弃该批，不使用新序列号重放，以免制造慢响应计数。后台同步约 1 秒一次，保护传播通常延迟 1–2 秒，不承诺触发阈值的那次请求立即停止重试。
- 使用独立 Redis client/pool（复用连接配置，连接数上限 2），一个后台 worker 兼做批次聚合与状态同步，不再拆多个采集/同步线程，不占主链 Redis 池。所有外部操作设置 500ms 超时；启停幂等，退出关闭连接并停止 worker。
- 队列容量建议 8192；入队使用非阻塞 select，满则丢弃监测事件，限频记录丢样并标记观测不完整。不能阻塞 relay 或改用每事件 goroutine。
- 事件丢失时计数可能偏低，保护可能延迟触发；这是保护功能的降级边界，不能宣称精确实时熔断。
- 未配置 Redis 时自动保护不可用，界面提示配置 Redis 后启用；次数/状态码仍可保存。Redis 临时失败时保留上次状态至其有效期，快照超过 5 秒未同步则绕过保护，恢复既有重试策略；不得无限维持暂停。
- 本地仅保留有界事件队列、短期批次和 Redis 派生只读快照，不作为第二套独立计数权威。

## 错误处理与现有子系统

配置保存错误记录请求上下文日志并返回本地化可操作提示；底层 DB/Redis 错误不直接展示。
后台 worker 捕获 panic、限频记录 SysError，故障不会传播到 relay；下一轮可恢复，避免静默永久失效。
只记录分组、阈值、状态和错误原因，不记录请求正文、密钥或令牌。
自动暂停与恢复只产生状态转换日志，不增加逐请求日志写入。
新功能未启用时只做开关快速返回，不建立采集队列依赖。不新增或改变 timeout trace，不影响现有超时标记和计费送达证据。
relaykit 不依赖本功能，不修改其代码或公共接口。

## Main Chain Impact

主 relay 同步新增错误后重试判断的开关/规则/运行快照查找，以及监控回调排队前的既有发送/取消证据检查和一次原子去重。只有保护开启时才为请求创建去重标志。复用已有监控异步任务分发 Sample，不重新计算延时或新增逐请求 goroutine。任务提交原先没有性能样本，在成功/失败末尾利用已有 StartTime 构造一个完成样本并非阻塞投递。
首请求正常服务；暂停中的组仅在额外重试前被拦截，返回已有上游错误。
不增加同步 DB/Redis 调用、等待、分布式锁或每请求 goroutine。
解析、统计、Redis 聚合、触发和冷却判断、跨节点同步全部在后台完成。
全部新增采集与重试决策受默认关闭的 enabled 控制。

## Shared Resource Audit

| 资源 | 与主链关系及隔离措施 |
|---|---|
| options 表 | 已有配置表；仅管理员事务写五个用户配置键和内部版本键，使用既有 key 索引，无大表扫描；relay 不查表 |
| logs/tokens/channels/users | 不新增读写，不用消费日志统计 RPM，不与计费或鉴权行争锁 |
| Redis 键 | 已搜索代码，未发现 group_retry_guard/retry_guard 命名空间；新前缀独立于限流、额度、日志、缓存键 |
| Redis 连接 | 新增固定最多 2 个连接/节点，不借用主链池；部署容量需计入总节点连接预算，仍共享 Redis 服务 CPU，必须测批处理成本 |
| 配置锁/OptionMap | 复用后台保存与同步方式，不在主链新增获取这些锁的操作 |
| 运行快照 | 新功能专用不可变 map，原子替换，有效期 5 秒；主链只读 |
| 队列/后台批次 | 专用有界队列与固定 worker，不借用计费、日志或 relay worker 池；队列压力只丢监测事件 |
| 现有 perf Sample/hotBuckets | 复用 Sample 值，不增加模型/分组历史序列，不修改或 drain 既有桶，不重复调用 recordRedis；保护用独立有界聚合 |
| 请求局部记录 | 复用既有时间和发送/取消证据；新增请求私有原子去重标志，保护关闭时不分配；新增队列只传值，不保留 Gin context 或请求 body |
| HTTP transport | 复用现有 WroteRequest 回调和请求私有原子标记；保护开启时，未启用超时管理的请求也注册该回调。不新增计时器、上游连接或连接参数变更 |

## Concurrency Analysis

30k RPM 约 500 请求/秒，100k RPM 约 1667 请求/秒。复用现有完成样本，一次请求至多一个事件，约 500/1667 事件每秒。快慢阈值比较在后台完成，复用已算好的延时。
内部重试、跨组不增加采样事件数；只统计最终请求样本，不增加每次尝试计时成本。
新增同步 DB 0、Redis 0、跨请求业务锁 0、goroutine 0；有界 channel 自身的短临界区需基准验证，满队列立即返回。
后台按活跃分组批量操作，256 组最坏每秒约 256 次有界 Lua 聚合与一批状态读取，使用 pipeline 减少往返，不按请求发 Redis 命令。
稳态脚本仅更新新增/过期桶及滑动总数，不随窗口长度逐秒全量扫描，不使用 SCAN。内存上限随配置窗口增长，最多 256×3601 个逻辑秒桶，需验证 Redis 实际占用；不存单请求明细。窗口默认 60 秒，不预分配所有分组的最大窗口。冷却递增只增加几个状态字段，不增加每请求计算量。
每秒最多发布一份运行快照，只包含配置中的分组；性能测试记录 allocations、P99 入队/判断耗时、CPU、Redis 脚本耗时和丢样率。
以上为容量分析，尚不是实测通过结论；目标热路径新增开销低于 1ms，实施后用 30k/100k RPM 等价 mock 负载验证。

## 实施前测试计划

1. 先写后端测试：统一保存任一字段非法全部不落库；事务中途失败全部回滚；旧接口兼容；五键读取/权限白名单完整；不同 scope 不可越权。
2. 阈值边界：RPM、延时、慢次数各测试阈值前/等于/之后，三个条件未满足时不触发；两组互不影响。
3. 窗口与恢复：窗口 9/10/60/120/3600/3601 秒边界、非 60 秒 RPM 折算与精度、预热与窗口变更、冷却到期、新旧 epoch、重复事件、延迟事件、多实例累计和并发触发；固定/递增模式、倍数和上限、溢出保护、暂停不升级、稳定期重置及监测中断不误重置；使用可控时钟，不靠 sleep。
4. 采集：现有成功/失败/结算调用点不重复记数，同组重试不放大 RPM/慢次数、最终实际组正确归属；成功慢响应、慢错误、首响应前超时；长流快速首响应不误报；客户端取消和本地失败排除；关闭历史监控不关闭保护；历史监控 Redis 调用次数和统计结果不变。
5. 保护：首次调用允许，用户级重试不能绕过，普通/任务循环一致，auto 不能归属错误组；流交付、504/524、预算/总尝试限制保持原样。
6. 隔离：禁用时旧行为完全不变；队列满、Redis 超时/断开、worker panic、快照过期、规则删除/热更新均不影响响应与结算。
7. `httptest` mock 上游调用计数和计费守卫，不访问真实 AI 服务；Redis 使用隔离测试前缀，DB 只清理测试自己的配置行，恢复原值。
8. Go 相关包测试与基准；可用时 race；前端 `bun run typecheck` 和修改文件 lint。统一保存、失败草稿保留、禁用重复提交、状态刷新不覆盖草稿列为交互回归项，不引入测试框架。
9. 使用真实项目 DB 环境验证事务；无 DB 才回退 SQLite。事务仅用 GORM，保持 SQLite/MySQL/PostgreSQL 兼容。

## 实施与验证记录

### 本机压测发现的失败采样遗漏

真实 HTTP 503 mock 返回自定义错误码 `overloaded` 时，未启用超时管理的请求没有发送标记，错误码也不是内部 `bad_response_status_code`，导致失败完成样本被排除。请求本身正常重试，但保护 RPM/慢次数一直为零。

先增加真实 httptest 上游回归，确认旧代码失败，再将既有 WroteRequest 采集条件扩展为“超时管理或自动重试保护开启”。仍复用同一回调和请求私有原子标记，不按 HTTP 状态或错误字符串猜测上游是否被调用。关闭两个功能时保持原行为；每次重试仍重置证据；一次客户端请求最多认领一个样本。

Main Chain Impact：额外操作仅为未启用超时管理且保护开启的请求注册已有 HTTP trace、请求私有分配与 atomic.Store，无同步 I/O、计时器、额外 goroutine 或跨请求锁。标记不进入跨请求缓存；Redis/DB/连接池/统计队列资源与既有方案相同。30k/100k RPM 分别约 500/1667 次首尝试 trace 注册每秒，重试按实际尝试数增加；该分配开销需纳入保护开关 A/B 实测，不能用快照判断基准代替整链验证。相关采样和超时发送证据回归已通过；长请求和故障压测结果另记验证报告。

### 分组重试次数输入上限

- 分组重试次数最大可填写 10；前端输入及保存校验、后端旧接口与统一保存共用同一上限。-1 仍为不重试，0 仍为继承。用户级次数设置不在此次范围内。
- 已保存的历史值不静默改写；历史超过 10 的分组在编辑保存时需调整至合法范围。加载和 relay 运行语义保持原样，不新增主链计算或共享资源访问。
- 边界回归覆盖 9/10 接受、11/20 拒绝，统一保存拒绝超限且原配置不落库变更；前端 11 不发请求、10 可提交。

### 分组表单布局与交互调整

- 沿用现有 `bg-card`、`border`、`text-muted-foreground` 和当前主题字体；不新增配色、阴影或动效。每组使用独立边框，卡片内边距与组间距统一为 16px。
- 组内第一行保留分组、重试次数、状态码、删除；自动保护区域用分隔线区分。展开内容按“触发条件”“冷却与恢复”上下顺序排列，在同一轻背景区域内对齐：桌面每段四列，平板两列，手机一列。冷却模式放在该段标题右侧，固定模式只显示初始冷却，递增模式显示完整四项，避免原两栏高度不等的空洞。
- 冷却模式位于冷却参数之前；固定/递增切换保留草稿。按最新反馈，总保护关闭时隐藏全部分组保护配置；分组保护关闭时只保留一行开关和标签，开启才展开参数与摘要。移除重复标题、关闭说明和摘要边框，切换不清空草稿。
- 填写时显示触发规则摘要及最多四步冷却序列；预览只使用当前已通过校验的草稿，递增预览严格截断到上限，不进行后端计算。
- 新增后打开分组选择，不在提交前对空分组报错；选择列表排除其他行已使用的分组，保存仍保留完整重复校验。
- 删除后提供撤销，保存成功后清除撤销记录；保存期间撤销与编辑一起禁用，防止请求中的草稿被覆盖。单一保存入口保留未保存提示。
- 回归：关闭时数值输入为零，开启后固定/递增分别显示五/八项；组开关和总开关往返切换保留已编辑值；两分组各有独立区域；未提交空组不报错；冷却模式在初始冷却之前；预览遵守上限；同组不重复选择、删除撤销保持字段值、失败保留草稿。后端/API/配置语义不变。

参数可见性回归：最新交互替代此前的持续展示方案。输入、预览、字段错误与监测状态只在总开关及该组开关均开启时出现；关闭只折叠，不删除已保存规则或当前草稿。保存中全部编辑控件禁用。本次只修改前端展示，不新增共享资源或主链计算。

分组校验回归：提交校验时，空分组只显示一次“请选择分组”；重复选择同名分组时每行只显示一次重复提示。状态码校验仅负责自定义状态码格式，分组必选及重复校验统一由行校验处理，保存前仍完整验证。新布局中未提交的新行不提前报错。

- 页面合并为一次 group_retry_policy 提交；业务失败或 applied=false 保留草稿，按钮防重复提交；新增 22 条文案覆盖七种语言。
- 配置模块提供严格校验与不可变快照；统一保存事务涵盖六个键，旧次数/状态码入口兼容。跨节点配置加载将 guard 的 enabled/rules/revision 整体发布，非法数据保留有效快照。
- `pkg/retryguard` 复用监控完成样本，单 worker、容量 8192 的队列、专用最多两连接 Redis 池、500ms 超时；Lua 使用 Redis 时间维护滑动总数、固定/递增冷却和稳定重置。
- 默认关闭；Redis 缺失/故障不阻断转发，5 秒过期快照不再禁止重试。新请求首次调用与原有预算/计费规则保持不变；内部流式适配转普通请求的兼容回退也检查同一保护快照。已有监控没有可靠完成/上游证据的请求不补造慢样本。
- 自动配置修改重置整个保护配置的运行状态；不因修改状态码或次数重置。TTL、规则版本、可变窗口和恢复边界已纳入测试。
- 通过：新功能 PostgreSQL 专项（配置事务、热加载、权限、原次数/状态码兼容、采集资格），retryguard/perf_metrics/operation_setting/settingsaccess/relay-common/router 包测试，root `go build ./...`，相关包 vet；前端 typecheck、修改文件 lint、生产 build。
- 基准：本机 Ryzen 7 8700G，保护快照判断约 20.16 ns/op，队列已满时投递约 13.36 ns/op，均 0 B/op、0 allocs/op。这是局部基准，不代表已做 100k RPM 真实部署压测。
- 扩大数据库回归遇到已有问题：PostgreSQL channels JSON 字段空字符串夹具失败；MySQL user_extensions.extra TEXT 默认值迁移失败。两者均已在未修改基线 `2536ecb29` 独立 worktree 复现，本次不修改既有测试/模型来掩盖失败。新增相关测试在临时 PostgreSQL 与配置专用 SQLite fixture 中通过。
- Windows 当前没有可用的 GCC，未运行 race detector；未访问真实 AI 提供商。


## zhuzhan merge validation (2026-10-20)

- Preserved request-boundary performance sampling and the existing task/plugin submission flow. Retry protection reads the local immutable snapshot and submits samples to the bounded non-blocking queue; this merge adds no synchronous database or Redis access to relay.
- Passed root Go build, independent relaykit build, relevant service/middleware/controller regressions, relay provider/body fixtures, and retry guard/settings tests.
- Passed 32 model cases, including unified retry-policy save and rollback on SQLite, MySQL and PostgreSQL. Matrix fixtures use dialect-aware column quoting and restore the guard snapshot during cleanup.
- Passed frontend type checking, changed-file lint, 8 interaction tests, static visibility checks and production build. Authentication/HTTP client plumbing remains unchanged from zhuzhan.
- A broader PostgreSQL run also hit TestFlushPairRetryQueue and TestFlushCostRetryQueue failures: existing business_stats_buffer.go upserts use ambiguous unqualified columns. Both that implementation and its test file are unchanged from pre-merge zhuzhan. These unrelated failures are recorded rather than reported as a passing full suite.
