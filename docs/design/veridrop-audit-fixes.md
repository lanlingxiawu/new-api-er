# Veridrop 审查问题修复设计

## 状态

设计已确认，修复已按本文方案实现并完成定向回归测试。

## 目标与范围

本次修改解决 Veridrop 代码审查发现的全部十项问题，并清理仓库工作区中的未跟踪部署二进制文件。范围包括：

- 拆分 Veridrop 菜单权限时，继承现有管理员的显式拒绝覆盖；
- 将单渠道检测与批量、定时检测的任务去重及冷却计时隔离；
- 防止清理任务删除仍在运行的检测批次中的部分结果；
- 保证 `batch=latest` 的游标分页始终固定在同一个批次；
- 让现有数据库可靠地升级已经改变定义的 `updated_at` 索引；
- 拒绝非法的 OpenAI `wire_api` 和检测间隔，不再静默改写；
- 由后端统一提供检测结果分类，页面和下载报告不再重复实现判定规则；
- 收敛 Controller 中重复的检测启动、错误处理和响应逻辑；
- 忽略并删除本地 `new-api-hk` 编译产物。

不在本次范围内：修改 Veridrop 上游检测协议、修改 `veridrop-monitor` 的评分计算、修改渠道启停逻辑、计费逻辑或 AI 主转发链路。

## 数据流

### 权限迁移

1. 主节点初始化授权系统时，检查 Veridrop 菜单权限迁移的一次性标记。
2. 如果尚未迁移，读取所有针对 `admin_menu.channels:view` 的用户级显式拒绝记录。
3. 如果该用户还没有针对 `admin_menu.veridrop_detection:view` 的显式规则，则写入等价的拒绝规则；已经存在的新资源允许或拒绝规则都不覆盖。
4. 复制规则和写入迁移标记在同一个事务中完成。
5. Casbin enforcer 随后加载迁移后的策略。迁移完成后不再重复运行，后续两个菜单权限可以独立编辑。

### 检测任务隔离

1. 单渠道检测请求进入新的 `veridrop_detection_single` 系统任务类型。
2. 已启用渠道批量检测、显式选择渠道批量检测和定时检测继续使用 `veridrop_detection`。
3. 系统任务执行器为两个任务类型注册同一套有界 Veridrop 执行逻辑。
4. 定时调度器只读取 `veridrop_detection` 的历史记录来计算定时批量检测间隔。单渠道检测既不会复用活动批量任务，也不会刷新批量任务冷却时间。

同一时刻最多运行一个由系统任务承载的单渠道检测。这样可以继续使用现有有界任务执行机制，避免为每个渠道创建无界任务类型或 goroutine。

### 清理协调

1. 创建清理任务时固定截止时间，包括 `retention_days=0` 的全量清理。
2. 清理只选择早于固定截止时间的终态记录。
3. 清理开始时读取活动批量任务并固定其 `task_id` 快照，后续所有计数和分批删除均排除这些 `batch_task_id`。
4. 批次结束后，后续清理可以删除整个符合条件的批次；正在执行的清理不能只删除活动批次中已先完成的部分记录。

前端在检测任务处于活动状态时也会禁用清理入口，但最终正确性由数据库查询保证，以覆盖直接调用 API、多节点和并发竞态场景。

### 稳定的最新批次分页

第一次请求 `batch=latest` 时，服务端继续解析当前最新批次。如果后续请求包含 `before_id`，服务端读取游标对应记录并使用该记录的非空 `batch_task_id` 固定查询批次，不再重新解析当前最新批次。

响应继续返回 `latest_batch_id`，其含义调整为“本次响应固定使用的批次 ID”。

### 统一结果分类

后端定义结果分类常量、低分阈值和唯一的检测记录分类函数。列表和详情响应增加计算后的 `outcome`。SQL 结果筛选复用同一组后端常量和阈值。React 页面和 HTML 下载报告直接消费 `result.outcome`，不再分别根据 verdict 和 score 猜测结果分类。

## API 契约

现有端点路径和鉴权级别不变。

### `POST /api/channel/veridrop/detect`

- 鉴权：`AdminAuth`、`admin_menu.veridrop_detection:view`、`ChannelOperate`。
- 改为创建 `veridrop_detection_single` 任务。
- 非空 `openai_wire_api` 只允许 `chat_completions` 或 `responses`。

### `POST /api/channel/veridrop/detect_enabled`

### `POST /api/channel/veridrop/detect_batch`

- 保持现有鉴权。
- 继续创建批量任务类型 `veridrop_detection`。
- 应用相同的 `openai_wire_api` 校验。

### `POST /api/channel/veridrop/detect_manual`

- 保持现有鉴权。
- 非法 OpenAI wire API 返回参数错误，不再静默替换。

### `GET /api/channel/veridrop/results`

- 保持现有参数兼容。
- `batch=latest&before_id=N` 根据记录 `N` 固定批次。
- 每条记录增加 `outcome`，取值为：`in_progress`、`passed`、`completed`、`low_score`、`failed`、`skipped` 或 `cancelled`。

### `POST /api/channel/veridrop/results/cleanup`

- 保持现有鉴权和请求结构。
- `retention_days=0` 表示清理任务启动前已经结束的终态记录，同时排除活动批次中的记录。

### 配置 API

- 检测间隔必须位于 `15..43200`。
- 默认 wire API 必须是 `chat_completions` 或 `responses`。
- 非法值使用现有经过清理的参数错误响应。

Controller 继续使用 `ApiErrorI18n`。公共 Veridrop 启动辅助逻辑接收操作对应的审计字段和兜底错误消息键，使手动检测的参数错误与任务入队的临时错误保持可区分，同时消除重复分支。

## 数据模型变更与迁移

不新增业务表。

- 增加系统任务类型常量 `veridrop_detection_single`。任务类型保存在文本字段中，因此不需要新增数据库列。
- 在检测响应结构中增加不持久化的 `outcome`，或使用等价的响应 DTO；不新增数据库列。
- 将 `(updated_at, id)` 复合索引改名为 `idx_veridrop_updated_id_v2`。
- AutoMigrate 创建 v2 索引后，通过显式、跨数据库兼容的 GORM 迁移删除仍然存在的旧单列 `idx_veridrop_updated_id`。先创建新索引再删除旧索引，避免查询期间失去可用索引。
- 在 `casbin_rule` 中增加一个不可匹配真实用户的一次性迁移标记，不新增业务表或配置项。

### 索引设计

- `idx_veridrop_updated_id_v2(updated_at, id)`：支持默认的最近结果排序和带时间范围的扫描。
- 现有 `idx_veridrop_batch_id(batch_task_id, id)`：支持稳定的批次游标分页。
- 现有系统任务索引和活动任务唯一键：支持排除活动批次的子查询。实现时在当前配置数据库上检查 `EXPLAIN`；如果 `task_id/type/status` 查询没有可用索引，则通过 GORM 为三种数据库增加覆盖索引。
- 现有 `idx_veridrop_cleanup(status, updated_at, id)`：继续支持有界批次清理。

所有迁移必须同时兼容 SQLite、MySQL 5.7.8+ 和 PostgreSQL 9.6+，不得使用没有跨数据库回退方案的专属 SQL。

## 配置参数

不新增用户可配置的 Veridrop 参数。现有校验继续作为权威规则：

- `detection_interval_minutes`：`15..43200`；
- `default_openai_wire_api`：`chat_completions` 或 `responses`。

快照归一化只作为兼容历史非法持久化值的启动期防御措施；管理端写入必须在持久化之前拒绝非法值。

## 关键业务逻辑与边界情况

- 权限迁移必须保留拒绝语义、可重复执行且不能覆盖新资源上已经存在的显式策略。
- Root 继续保持超级用户语义，不受用户级拒绝策略影响。
- 单渠道任务和批量任务可以同时存在；两个单渠道任务仍相互去重，以保护有界工作池。
- 定时批量检测只受之前的批量或定时检测影响，不受单渠道检测影响。
- 清理永远不删除 `queued` 或 `running` 记录，也不删除系统任务仍为 `pending` 或 `running` 的批次记录。
- 批次在清理期间结束时，只允许在下一次清理中被删除，避免当前清理留下不完整批次。
- `batch=latest` 携带的 `before_id` 如果不存在、已删除、不属于批次或不合法，则返回现有参数错误，不得静默切换到新批次。
- 空 wire API 继承配置默认值；非空非法值返回错误。非 OpenAI 协议不得向上游发送 `wire_api`。
- 未知或历史检测记录根据统一分类器获得稳定的 `completed` 或 `cancelled` 分类；前端不再独立推导。
- 在 `.gitignore` 中增加仓库根目录 `/new-api-hk` 规则，并删除当前未跟踪二进制文件。该文件是可重新构建的产物，且从未进入 Git，因此删除后不能从 Git 恢复。

## 错误处理策略

- Controller 使用带请求上下文的日志记录底层错误，向用户返回现有本地化安全消息。
- 非法 wire API、检测间隔和最新批次游标统一返回现有参数错误。
- 权限迁移或索引迁移失败时终止初始化，不能在授权弱化或数据库结构未知的状态下继续运行。
- 清理查询失败只会使清理系统任务失败，不影响检测任务或 relay 响应。
- 不向浏览器返回上游内部错误、凭据或数据库细节。

## 与现有子系统的交互

- 授权系统：执行一次性 Casbin 用户级拒绝迁移，继续使用现有策略同步机制。
- 系统任务：新增一个任务类型，复用现有执行器、租约、心跳、进度和有界工作池。
- Veridrop 服务：增加请求校验并复用现有执行逻辑，不进入 relay 链路。
- 前端：结果标签、筛选、详情面板和下载报告统一使用后端 outcome；继续使用现有 Base UI、Tailwind 和组件组合方式。
- 国际化：优先复用现有参数错误和任务消息。确有新增的用户可见文案时，同步前端七种语言及后端两种语言。
- 计费、配额、relay 渠道选择、请求日志、Redis 和供应商适配器均不修改。

## Main Chain Impact

本设计不会在 AI relay 请求 goroutine 上同步执行任何新增逻辑。每个 relay 请求新增的数据库调用、Redis 调用、锁、goroutine 和外部请求均为 0。

所有修改只发生在启动迁移、管理员请求、系统任务调度、后台 Veridrop 检测或浏览器渲染中。

## Shared Resource Audit

| 资源 | 新增或改变的访问 | relay 是否访问 | 隔离和影响 |
| --- | --- | --- | --- |
| `casbin_rule` | 启动时一次性复制用户级菜单拒绝并写入迁移标记 | 否 | 仅主节点、事务执行、迁移标记使用不可匹配真实用户的主体 |
| `options` | 现有 Veridrop 设置 | relay 不读取这些键 | 仅低频管理操作 |
| `system_tasks` | 单渠道独立任务类型及清理时活动批次排除 | 否 | 使用现有租约和有界执行器，查询必须有索引 |
| `channel_veridrop_detections` | 稳定批次分页、结果分类读取、受保护清理 | 否 | 独立表，继续有界批量清理 |
| `channels` | 保持现有检测目标读取 | 是 | 不新增查询或锁；检测仍是管理端后台负载 |
| 数据库连接池 | 启动迁移及管理、后台查询 | 是 | relay 不新增访问；清理保持串行和有界 |
| Redis | 不访问 | relay 可能使用其它命名空间 | 不新增键或命名空间 |
| goroutine 工作池 | 增加一个注册任务类型 | relay 使用其它执行路径 | 最多一个单渠道任务和一个批量任务，不按请求创建无界 goroutine |

## 100k RPM 并发分析

每个 relay 请求的新增成本仍为：数据库调用 0、Redis 调用 0、新增锁 0、新增 goroutine 0。

- 调度器：现有定期最新任务查询增加一个已注册类型，与 relay 流量无关。
- 单渠道检测：同类型最多一个活动任务，内部继续受 Veridrop `MaxConcurrent` 限制。
- 批量检测：保持最多一个活动批量任务，上游并发继续受 `MaxConcurrent` 限制。
- 清理：最多一个活动清理任务，每批最多处理 500 个 ID；每批执行一次索引选择、一次有界删除和一次进度更新。
- 权限迁移：只执行一次启动事务，不产生请求期成本。
- relay 路径不新增分布式锁或应用锁。

## 测试先行计划

实现代码之前先增加以下测试。

### 授权迁移

- 已有 Channels deny 且没有 Veridrop override 时，复制 Veridrop deny。
- 已有 Veridrop allow 或 deny 时保持原值。
- 迁移标记存在时，后续启动不再迁移。
- 事务失败时不写入标记，也不留下部分策略。
- Admin 默认权限和 Root 绕过行为保持不变。

### 任务隔离与调度

- 活动批量任务不影响创建单渠道任务。
- 活动单渠道任务会去重另一个单渠道任务。
- 最近运行的单渠道任务不会延迟已经到期的定时批量任务。
- 批量去重和定时冷却行为保持不变。
- 两种 handler 均正确解析 payload；只有批量任务写入批次 ID；进度和最终状态正确。

### 清理

- 覆盖固定截止时间之前、恰好等于和之后的终态记录边界。
- `pending` 和 `running` 永远保留。
- 活动批次中已完成的记录保留，无关且符合条件的记录删除。
- 批次结束后，后续清理删除其全部符合条件的记录。
- 覆盖多批删除、取消、空结果和非法 retention。

### 分页与结果分类

- 最新批次第一页解析当前最新批次。
- 两页之间创建新批次时，后续页仍查询原批次。
- 不存在、已删除和非批次游标被拒绝。
- 覆盖所有状态、verdict 和分数边界，包括 49.9、50、别名、未知 verdict、cancelled、skipped 和活动状态。
- SQL outcome 筛选结果与响应中的 outcome 完全一致。
- 前端类型检查确保标签、筛选、详情和报告消费 outcome，不再包含重复阈值分类逻辑。

### 校验与迁移

- 覆盖空值、默认值、合法和非法 wire API 的 Controller、Service 路径。
- 覆盖检测间隔 14、15、43200、43201，确认在持久化前正确拒绝或接受。
- 索引迁移测试从旧单列索引开始，连续执行迁移两次，确认最终只保留 v2 复合定义；SQLite 必测，并覆盖所有已配置的真实数据库。

### 验证命令

- 开发期间运行相关包测试，完成后执行 `go test ./...`。
- 执行 `go vet ./...` 和 `go build ./...`。
- 前端执行 `bun run typecheck`、目标文件 oxlint、Prettier、`bun run i18n:sync` 和 `bun run build`。
- 执行 `git diff --check`，并检查 locale 文件 UTF-8 编码和 BOM。
- 有可用 CGO 工具链时运行 race 测试；否则记录限制并人工审查快照和任务并发逻辑。

## 实施记录

已完成以下实现：

- 授权初始化增加一次性 deny 迁移及事务回滚保护；
- 单渠道检测使用独立系统任务类型，批量定时任务不再受其冷却时间影响；
- 清理任务固定截止时间并保护启动时仍活动的批次，前端在检测运行时同步禁用清理入口；
- 最新批次翻页通过游标记录反查批次，避免跨页切换到新批次；
- 新建 `idx_veridrop_updated_id_v2(updated_at, id)` 后删除旧同名单列索引；
- wire API、检测间隔改为显式校验并拒绝非法值；
- 后端统一计算 `outcome`，页面、筛选和 HTML 报告消费同一结果；
- 四个检测入口复用统一任务启动和错误响应逻辑；
- 已删除本地 `new-api-hk`，并在 `.gitignore` 中忽略该根目录产物。

## 测试服务器部署记录

- 部署时间：2026-08-16；目标为香港测试服务器。
- 部署版本：`v1.0.0-rc.23-211-veridrop-audit-fixes`。
- Linux amd64 二进制 SHA-256：`e2ecc8c27b8401c90c1892d68d910c6546c2041668ae6dad6a9bd6d50c54e38c`，服务器文件与本地发布包一致。
- 使用服务器既有 `graceful_update.sh` 发布：旧进程收到 SIGTERM 后 1 秒内正常退出，旧二进制备份到 `/root/backup/20260816_110404/new-api`。
- 新进程启动后第一次本机健康探测即返回 HTTP 200；公网 `/api/status` 返回新版本且 `success=true`。
- 启动日志确认 MySQL 主库和 PostgreSQL 日志库迁移完成，系统任务执行器正常启动，未出现 panic、fatal 或迁移错误。
