# 上线前问题定向修复（11 项）

状态：**用户已批准实施，11项修复已落地并完成针对性回归；不代表全量上线验收通过**。日期：2026-11-20。

## 目标与范围

用户最新确认不改原第3、10、11项，本轮处理原编号1、2、4、5、6、7、8、9、12、13、14，共11项，保留原编号便于核对。明确不处理：签到奖励校验、成本统计回填文件句柄、SSRF 端口范围保存、账本下载即时撤权、手机批量操作、关键词拒绝返回500、OAuth discovery原始错误泄漏、首页提示词传入Playground。第14项只涉及签到日历的可访问名称及JSON编辑器，不涉及奖励业务。范围调整不代表这些排除项已修复或测试通过。

不增加未实现的 files/fine-tunes 等服务，不接受支付合规条款，不部署生产环境。真实支付、外部 OAuth、Veridrop、云部署仍为外部环境阻塞；套餐创建等仍保留授权阻塞。

冻结候选 `31805c5e` 的问题证据见 `docs/design/release-validation-20261119.md`。修复目标是当前工作区，不改写冻结候选、原始日志或历史测试结果。核对起点 HEAD 为 `4d8aae510`，但工作区已有其他任务修改，不能将 HEAD 当作完整构建指纹。本次记录实际测试二进制指纹；未建立独立源码提交快照，后续工作区改动不能自动继承本次验收结论。

## Existing Logic and Related-Path Findings

以下是实际代码核对，不以旧报告代替当前实现。除特别说明外，相关缺陷入口与冻结候选一致。

| 编号 | 当前实现与相关路径 | 拟定修复 |
| --- | --- | --- |
| 1 月度分红导出取整 | `model/employee_commission_export.go` 汇总利润、解析当期等级后直接 `int64(float64(profit) * rate)`；注释要求取整。`service/employee_commission.go` 使用 decimal 四舍五入，且单笔结算有最小额度规则。 | 月度汇总使用 decimal 乘法及 `common.Int64FromDecimal` 安全四舍五入；不得套用单笔 MaxInt32 上限，也不把月度汇总改成逐笔账本之和。单笔最小额度规则不移植到月度汇总。 |
| 2 渠道经营统计分页 | `model/business_daily_stats.go` 的 `GetConsumptionCostByChannelPageWithPlan` 只按可重复的统计值排序；内存回退 `sortConsumptionCostChannels` 同样需要稳定次序。 | SQL 和内存排序都以 `channel_id ASC` 为相等值的唯一第二排序键；保持筛选、主排序方向和分页响应形状。 |
| 4 员工创建客户校验 | `controller/customer.go` 在 required 绑定后 trim，随后直接 Insert；管理员 `controller/user.go:CreateUser` 已有 trim、空值判断、`common.Validate.Struct` 和 `writeUserValidationError`。 | 对服务器构造的客户 User 在写库前复用同一验证器与错误映射；拒绝 trim 后空用户名、超长用户名和非法邮箱。保留员工身份校验和服务器指定的角色、状态、邀请关系，不接收客户端提权字段。 |
| 5 清空渠道标签 | 两个标签弹窗分别以 truthy 条件或 `undefined` 丢弃空字符串。`controller/channel.go:ChannelTag.NewTag` 是 `*string`，省略/null 表示未修改。 | `edit-tag-dialog.tsx` 和 `tag-batch-edit-dialog.tsx` 对用户实际清空发送 `new_tag: ""`，未编辑字段继续省略；复用既有按标签更新及缓存刷新，不改后端 null 合同。 |
| 6 保存模板丢失选列 | `new-export-sheet.tsx` 保存成功先设新 ID 再 invalidate；模板选项来自旧查询结果。`applyTemplate('')` 会触发默认模板 effect。`createExportTemplate` 已返回完整模板。 | 成功响应先按 ID 合并进同一 React Query 模板列表缓存，再选择新 ID；保留当前 columns/options/format，忽略非用户清空造成的空选择。补延迟 refetch 回归。新 ID 尚未进入选项是已确认的状态窗口，Base UI 是否在具体调度下发空事件仍须由回归记录验证，不将推测冒充运行证据。 |
| 7 切期重置等级漏处理 | `commission_period_switch.go` 复用 `ResetEmployeeTierLevelsForPeriod`；后者只选 `baseline_reset_at < resetAt`，是定时重置的幂等守卫。先手动重置再切到更早期首会跳过。切期后还有既有自动升级阶段。 | 切期显式 resetTiers 使用独立的按 ID 游标批次，覆盖所有活跃员工；定时重置原入口与守卫不变。等级回到所属组最低档，基线只前进不倒退，随后保留现有自动升级阶段。不得以“无论业绩永远保持最低档”替换现有合同。 |
| 8 缓存清理弹窗不关闭 | `performance-section.tsx` 的 clearDiskCache 成功 toast+刷新但无关闭状态；AlertDialogAction 是普通按钮封装。该文件另有其他任务的资源统计改动。 | 复用受控 `ConfirmDialog`，请求期间禁用重复提交，业务成功后关闭；失败保留弹窗并反馈。只修改清理交互，不全局改变 AlertDialogAction，也不动其他统计改动。 |
| 9 Kuma 状态过期 | `controller/uptime_kuma.go` 并行读取状态页和心跳，取 `heartbeats[0]`。实际 Kuma 2.5.12 返回由旧到新数组。 | 按该接口顺序取最后一条；保留空列表、单条、uptime 统计和已有超时。测试明确固定该有序合同，不声称支持任意乱序心跳。 |
| 12 动态模型详情缺失 | `controller/model.go:ListModels` 已按组、令牌模型限制和计费配置筛选，再用 `buildOpenAIModel` 补动态模型；RetrieveModel 只查静态 map。 | 抽出列表/详情共用的可见模型名筛选逻辑；详情仅对可见模型复用元数据构建和所属方解析。静态模型也不能绕过可见性。保留列表各协议格式及原 not-found 错误形状，不把元数据查询接到推理热路径。 |
| 13 维护页空按钮 | `maintenance-error.tsx` 的 Learn more 无 href/onClick，也没有对应配置目的地。 | 删除无功能按钮，保留维护状态和说明；不新造帮助页面/外部链接。 |
| 14 可访问性 | `checkin-calendar-card.tsx` 左右图标按钮无名称；`json-code-editor.tsx` 使用 `ariaInvalid ?? !jsonStatus.isValid`，外部显式 false 可遮盖语法错误。 | 月份按钮提供本地化前后月名称；编辑器自身语法错误优先标记 aria-invalid=true，合法 JSON 时保留外部语义错误；将状态说明关联到实际 textarea，并合并已有 aria-describedby。不改 JSON 校验/保存业务及奖励。 |

相关现有设计以 `release-validation-20261119.md`、`audit-log-export.md`、`frontend-interaction-review-20261118.md` 为背景；实际代码优先。导出 API/类型、performance 页面、员工控制器及各语言资源已有并行改动，只做局部增量。

## 数据流与 API 合同

- 管理统计/导出：原权限中间件 → 控制器参数校验 → 原汇总读取 → 稳定排序或安全取整 → 原响应/文件。无账本、钱包和历史数据修改。
- 员工客户：UserAuth → Employee 身份检查 → trim/完整验证 → 原 Insert/关联 → 原响应。非法输入必须在任何用户写入前返回；校验失败沿用 HTTP 200、success=false 的本地化业务错误。
- 标签：原管理员渠道权限 → `{tag,new_tag:""}` → 既有模型更新 → 既有渠道缓存刷新 → UI 重新查询。字段缺省和 null 仍是不修改。
- 模板：原导出权限 → 原 `/templates` POST，name/columns/format/options 不变 → 成功模板写入查询缓存 → 更新选择 → 后台刷新。保存失败保持草稿，不显示成功。
- 切期：原管理员权限及 `POST /tiers/switch-period` → 切期重置批次 → 提交等级/日志 → 刷新已提交员工缓存 → 原可选期间数据处理 → 原自动升级 → 原处理数量响应。
- 清理：原配置编辑权限及 `DELETE /api/performance/disk_cache` → 既有清理服务 → success 判断 → 关闭/保留弹窗。此设计不改服务端文件生命周期。
- Kuma：原公开 `/api/uptime/status`（既有公开状态展示，无新增公开面）→ 两个现有 HTTP 读取 → 选末条心跳 → 原 categories/monitors 响应。
- 模型：原 API TokenAuth → 与列表一致的可见性 → 单模型元数据或原 not-found；不接受 dashboard 身份替代 API 令牌。
- 维护/可访问性均不增加后端接口。关键词拒绝、discovery和首页提示词流程保持现状，不在本轮修改合同内。

## 切期事务、边界与数据模型

不新增表、列、索引、配置参数或迁移。切期专用分支按等级记录主键游标向前扫描，批次上限沿用300，每个事务最多30行；不得按“处理数量为零”反复扫描同一批而死循环。

复用所属组最低等级、现有等级日志及缓存刷新能力。读取到的 id、tier_id、baseline_reset_at、effective_at、source、updated_by 参与条件更新（等级记录没有 updated_at 字段）；并发变化导致当前子批事务回滚并反馈可重试，之前已提交子批保留。无变化记录不重复写日志；基线使用 max(现值, 目标期首)，不回退最近重置时间。孤儿等级仅维护基线并记录内部错误，不猜测所属组或伪造等级日志；本切期入口的处理计数包含实际更新的孤儿基线行，不等同于全部完成有效等级重置。停用员工排除。

已有 includePeriodData=true 的重分桶行为不是本轮新功能，保留并回归；默认不触发额外历史回填。不修正此前已产生的财务数据，不修改已结算分红。

依据 `docs/production-environment.md`，生产主库为 MySQL，独立日志库为 PostgreSQL；本轮数据库测试仍需 SQLite/MySQL/PostgreSQL 三种真实实例，不能只测主库方言。统计分页保留时间范围和现有过滤索引；在真实实例记录查询计划，唯一第二排序列不能凭空承诺消除聚合排序。若必须增改索引或数据模型，补充设计后再审批。

## Main Chain Impact

第3项已由用户排除，本轮不直接修改 relay 执行逻辑、关键词拒绝状态码或重试策略。允许请求的同步工作、DB/Redis 调用、上游请求和流式交付均不增加。

其余修复发生在管理/元数据接口或浏览器。分红导出不更改 settlement；切期重置不接入 relay goroutine；模型可见性抽取仅供模型元数据接口调用，不用于每次推理。不得新增每请求 goroutine、日志同步写库或阻塞锁。

### Shared Resource Audit

| 资源 | 关联及隔离措施 |
| --- | --- |
| users、员工关系 | 客户创建仍使用原写入路径，不额外扫描/锁定用户集合；验证前置只减少失败写入，不改钱包及用户额度缓存。 |
| channels、渠道缓存 | 标签清空复用原更新及失效；不新增 Redis 命名空间，不重新实现路由缓存。 |
| 经营日统计、分红等级/日志及缓存 | 与异步结算统计关联；导出只读原范围，切期限定小事务和主键批次，提交后刷新受影响员工；禁止增加 relay 侧协调锁。 |
| 日志导出模板/React Query | 后端仍原保存路径；新增一致性处理在浏览器查询缓存，不改任务队列和共享 worker。 |
| 磁盘缓存文件句柄 | 本轮只改确认交互，文件清理实现不变；成本回填句柄问题明确排除。 |
| HTTP/DB/Redis连接池 | 不新增后台池/客户端循环或每请求额外数据库连接；Kuma沿用超时及调用数量，discovery不修改。模型详情仅管理元数据流量，使用原连接池与列表可见性规则。 |

### Concurrency Analysis

在100k RPM（约1667请求/秒）的 AI 热路径上，本轮新增 DB调用=0、Redis调用=0、锁=0、goroutine=0；关键词拒绝错误选项也保持不变。以上为代码路径分析，不是负载测试结果。验收需以实际请求负载确认允许流式请求、失败策略和统计一致性未回退；管理员批次不冒用 AI 并发指标。

## 错误处理与国际化

复用现有控制器错误辅助函数，用户提示不回显 Go 错误或上游正文。后台/请求日志分别使用 common.SysError 或带上下文的 logger，必要错误仅保留脱敏分类、状态及安全原因。不得记录密钥、口令、prompt 或带凭据 URL。

后端实际语言资源为 `i18n/locales/en.yaml`、`zh-CN.yaml`、`zh-TW.yaml`，按现有消息键机制补齐，不新建不被加载的 JSON。前端遵循项目 i18n skill，所有新增按钮/提示文案覆盖 en/zh/zh-TW/fr/ru/ja/vi；检查生成差异，不覆盖并行新增翻译。UI 复用现有 ConfirmDialog、Base UI 和主题样式。

## 原设计验证清单（覆盖目标，不作为全部执行证明）

| 项 | 真实回归与针对性测试 |
| --- | --- |
| 1 | 利润37、费率0.5导出19；正负半值、零和大额边界；真实AI产生费用后核对页面/导出，各自保持既有统计口径。 |
| 2 | 所有支持排序字段双方向、相同指标、多页以及移动端继续加载；全集无重复无遗漏；三数据库及 SQL/内存分支。并发新增导致 offset 漂移不是本轮快照分页保证。 |
| 4 | 空格用户名、长度边界、邮箱错误、重复账号、正常创建；员工/非员工/未登录；失败无新增行，三数据库；测试记录按ID清理，不清空共享表。 |
| 5 | 两弹窗清空、普通改名、不编辑、取消、重新打开、API回查和渠道缓存；不改变其他标签组。 |
| 6 | 选择2列后保存仍2列，延迟refetch、失败、重复名称和再次打开；真实导出文件列与选择相符。单元测试不得在响应前偷偷更新模板列表掩盖窗口。 |
| 7 | 已手动重置后调高等级再切期；最低档、停用、孤儿、超过300行、重复切期、并发改档、部分批次失败恢复；定时任务原幂等行为、includePeriodData及自动升级回归；三数据库。 |
| 8 | 取消不发请求，成功关闭，业务/网络失败保留，双击单次请求；真实隔离缓存文件清理，其他正在使用文件由既有保护机制负责。 |
| 9 | 真实Kuma旧在线/最新离线及反向变化；空/单/多心跳；超时/非200仍遵守现有响应合同。 |
| 12 | 动态/静态、无权限、停用、令牌模型限制、auto组、未定价开关；列表与详情一致；详情返回的模型实际可请求。 |
| 13 | 维护页不再包含无功能Learn more，其他错误页不变，桌面/手机布局与键盘操作。 |
| 14 | 两月份按钮有正确名称；全局/Claude/Gemini设置JSON错误对应textarea aria-invalid，修正后清除，外部错误保留，说明关联有效；桌面/手机。 |

下表后的实施记录列出实际完成范围；本表细分场景未逐项形成证据的部分不标为通过。测试夹具与真实服务结果分开记录；本轮未修改 Go relaykit。

真实数据库记录版本、命令、结果和查询计划；启动隔离构建至少两次检查无意外 DDL。现有未修复事项/并行任务失败不混入本轮通过项，也不擅自修复。最终每项标注通过、失败、阻塞或未执行，附对应新候选指纹及证据位置；任何缺失的真实环境均明确保留阻塞。

## 实施记录与实际验收

用户以“进行修复”批准本设计后实施，保留全部8个排除项及其他任务已有改动。

### 实现补充

- 客户验证复用 `common.Validate.Struct`；User.Email 原标签仅限制长度，另用 `omitempty,email` 验证格式并复用既有本地化错误键，不改密码、角色或认证机制。
- 模板缓存使用包含 viewerId、viewerRole 的原查询键，先合并成功响应再选择模板，不跨身份共享。
- 实际 vLLM 模型名包含 `/`，发现原模型详情 `/:model` 无法匹配；元数据路由改为 `/*model`，保留 TokenAuth，去掉参数前导斜线。`/v1/models/` 重定向列表并保留查询参数。此改动不进入推理处理器，不改变 AI 流式或结算热路径。
- 切期使用员工ID分块300、等级ID游标子批30；并发条件更新冲突回滚该子批，已提交批次可幂等重试。定时重置入口不变。
- 日历复用七种语言已有 Previous month/Next month 键，本轮没有新增翻译资源；JSON编辑器保留调用方描述并追加状态描述。

### 候选与环境

- 实际隔离服务：`http://127.0.0.1:59557`，容器 `targeted-fixes-20261120`，独立 Redis、SQLite数据目录；未替换冻结候选服务。
- 测试二进制：`_local/targeted-fixes-candidate`，SHA256 `47218efcd6a1dd86dc98d630945999482522f3da480ef29f0bfa98803a4dfec9`。构建包含当前工作区其他任务改动，本次结论只覆盖下列测试。
- 实测数据库：MySQL 5.7.44、PostgreSQL 18.7、SQLite 3.50.4；前两者使用独立 `targeted_fixes_20261120` 测试库。不是所有支持版本的兼容性证明。
- 真实上游：vLLM / `HuggingFaceTB/SmolLM2-135M-Instruct`；真实监控服务：Uptime Kuma 2.5.12。

### 通过：11项针对性验证

| 原编号 | 实际结果与证据 |
| --- | --- |
| 1 | decimal聚合取整边界及大额回归通过；真实HTTP人工利润37、费率0.5导出19。人工利润夹具与真实AI扣费分开记录。 |
| 2 | 内存与三库SQL相等值排序、多字段双方向、多页无重复遗漏回归通过；查询计划保留既有时间索引。 |
| 4 | 空格、超长用户名及非法邮箱拒绝回归通过；真实SQLite HTTP三次非法输入新增行数0。 |
| 5 | 两弹窗组件测试通过；真实桌面浏览器两种编辑均发送空字符串并成功，真实API回查为空。 |
| 6 | 延迟refetch组件回归通过；桌面/手机真实后端保存后两列仍为两列。 |
| 7 | 三库覆盖较新基线、幂等、孤儿及301行并发冲突：首30行提交、后批冲突、重试271行，无重复301条日志；真实HTTP切期回最低档通过。 |
| 8 | 组件成功关闭/失败保留回归通过；桌面/手机真实DELETE成功后弹窗关闭。 |
| 9 | 空/单/多条及正反状态单元测试通过；真实Kuma两个监控项与最新心跳一致。单元HTTP夹具不冒充真实服务。 |
| 12 | 可见静态/动态、隐藏模型、令牌限制及带斜线模型名回归通过；真实vLLM详情与列表相符，非流式和SSE完成。 |
| 13 | 组件及桌面/手机页面均无无效Learn more按钮。 |
| 14 | 日历命名和导航通过；桌面/手机各7个全局/Claude/Gemini JSON编辑器验证invalid与描述关联，修正后恢复；非法设置未保存。 |

真实服务四组结果：`_local/targeted-fixes-20261120/acceptance.json`；真实浏览器11组结果：`_local/targeted-fixes-20261120/browser.json`。实际vLLM非流式请求33输入+8输出，钱包扣41额度；测试实例显式设置模型/补全倍率1。截图在同目录 `desktop-final.png`、`mobile-final.png`。

### 测试命令与结果

- 最新定向后端命令（分别配置MySQL/PostgreSQL测试库；包含独立SQLite包装测试）：
  `go test -p 1 ./model ./controller -run 'TestTargetedFixesSQLite|TestCommissionExport|TestGetCommissionMonthlyEmployeeExport|TestChannelStats|TestEmployeeCreateCustomerValidates|TestKumaUses|TestSwitchPeriodReset|TestSwitchCommissionPeriod|TestAdvanceCommissionBaselineOnly|TestRebucket|TestRetrieveModel|TestListModels|TestResetEmployeeTierLevelsForPeriod' -timeout 90s -count=1`。
  两包在两次运行均通过，证据 `_local/targeted-mysql-final.log`、`_local/targeted-pg-final.log`。
- 较早扩展MySQL运行 `go test -p 1 ./model ./controller -count=1 -json`：两包通过，5661条测试/子测试通过事件、254条跳过事件。证据 `_local/targeted-full-mysql.jsonl`；该运行早于最终新增并发/SQLite包装测试等改动，不能替代最新定向运行，跳过不算通过。
- `web/` 下 `bun run test src/features/channels src/features/usage-logs/export src/features/system-settings/maintenance src/features/profile src/components/json-code-editor src/features/errors`：53文件499测试通过，证据 `_local/targeted-frontend-broad.log`；最终6文件23项定向复测通过，证据 `_local/targeted-frontend-final.log`。
- `bunx --no-install tsc -b`、`bun run build` 通过；13个本轮前端文件定向oxlint通过；`git diff --check` 通过。
- Linux后端构建通过：设置 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`，运行 `go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/QuantumNous/new-api/common.Version=targeted-fixes-20261120" -o _local/targeted-fixes-candidate-next .`，随后替换自有隔离容器二进制并复测。
- 查询计划及版本：`_local/targeted-fixes-20261120/query-plans.json`。MySQL范围索引仍需临时表/filesort；PostgreSQL索引扫描后聚合/排序。仅小型隔离库证据，不是生产数据规模性能承诺。

### 失败

- 全前端 `bun run lint` 仍失败：其他任务文件 `web/src/features/usage-logs/components/dialogs/log-detail-body.tsx:1098` 使用数组索引key，违反 `react(no-array-index-key)`。未越界修复；证据 `_local/targeted-lint-final.log`。

### 阻塞

- 标准 `bun run typecheck` 调用 `bun check -b`，本机Bun 1.3.13没有该子命令；替代实际TypeScript编译检查已通过，不能表述为原脚本通过。
- 真实支付、外部OAuth、Veridrop及云部署缺少外部环境；套餐等条款确认仍为授权阻塞，按用户要求不接受条款。

### 未执行或不在本轮结论内

- 生产部署、30k/100k RPM压力测试、生产规模查询计划、完整全仓测试、所有页面按钮全量验收没有由本轮重新执行。
- 未新增迁移；未执行生产schema导入、旧版本升级及二次启动DDL完整审计，不将容器重启当作迁移审计证明。
- 原设计清单中的额外场景（例如全部客户权限组合、真实导出文件列、统计移动端继续加载、全部Kuma超时路径）没有逐项新证据，不归入本次通过范围。
- 用户排除的8项保持不处理；原有254条跳过不视为通过。原冻结候选及后续并行源码改动没有自动获得本次验收结论。
