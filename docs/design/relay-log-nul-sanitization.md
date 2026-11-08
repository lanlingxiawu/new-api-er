# Relay 日志 NUL 入库修复

## 状态与依据

2026-11-08：用户确认后已实现，并原位部署香港测试服务器，通过部署后的集成测试，结果见文末。

本设计补充 `relay-log-async-batch.md`。当前代码的实时批量写入和历史文件回填均调用
`model/relay_log_pipeline.go` 中的 `insertRelayLogs`。修复前该入口直接将日志字符串交给
`CreateInBatches`，未处理实际 NUL（U+0000）。`model/main.go` 的 PostgreSQL 连接使用
`PreferSimpleProtocol: true`。修复前 `model/gorm_logger.go` 将所有 `08P01` 误判为预处理语句冲突。

## 目标与范围

- 防止日志中的实际 NUL 导致整个批次无法入库，同时覆盖新日志、重试和旧兜底文件。
- 将实际 NUL 转成六个可见字符 `\u0000`，保留错误诊断信息；已经可见的 `\u0000` 保持原样。
- 修正数据库错误分类，继续隐藏可能包含敏感数据的驱动错误正文。
- 保留日志时间、类型、用量、金额、ID 回填及关联账务行为。
- 本次只修复 relay 消费/错误日志管线；不扩展到审计日志，不改退出流程、驱动、协议或数据库结构。
- 不连接或操作用户服务器，不改桌面恢复文件。

## 数据流

实时请求 → 既有内存缓冲 → 后台批量 writer → 文本规范化 → 既有 ID 身份核对 → 批量入库。

历史 JSONL → 既有回放解码 → Version 2 先规范化并查找已提交记录 →
同一个批量写入入口 → 幂等文本规范化 → ID 身份核对 → 批量入库。

失败继续走现有有限重试、熔断和兜底文件流程；成功后沿用现有进度统计、文件处理和 continuation。
回放仍保留原始 `created_at`，不会改成回填当天。

## 关键逻辑与边界

1. 在 `insertRelayLogs` 内、身份比较之前规范化日志的持久化字符串字段，避免只修复 `Content`
   而被其他字段中的 NUL 再次阻断。字段包括 Content、Username、TokenName、ModelName、Group、Ip、
   RequestId、UpstreamRequestId、Other；忽略只读的 ChannelName。
2. 仅替换实际零字节，不修改其他控制字符、中文、反斜线或合法 JSON 转义；不解析重写 `Other` JSON。
   `Other` 中原本的 JSON 转义序列是普通文本，应逐字节保留。
3. 使用显式字段处理，避免反射和整条日志序列化。正常字符串无替换分配；异常字符串按需分配。
   操作应幂等，重试不会逐次增加反斜线。
4. 规范化后仍使用既有对象，以保留 GORM 回填 ID 的传播。已核实事件所有权：
   入队拒收/溢出的事件直接转 fallback；正常事件由 worker 从 buffer/pending 取走；
   写入返回后才交给 retry/fallback/continuation。关停取 pending 也受既有 flush 锁保护。
   不在入队锁内规范化，也不新增同步锁。
5. 保留既有“已带 ID 且身份匹配则跳过，否则重新分配 ID”的语义。
   对无 ID 的历史重复导入，本修复不新增全局去重保证。
6. `RequestId`、`UpstreamRequestId` 有长度限制，替换会增加长度。不得静默截断身份字段，
   若超出数据库长度限制，仍明确失败并保留兜底记录。本次不能保证修复所有任意损坏数据。
7. `identifyRecoveredRelayLog` 在执行 Version 2 身份查询前调用同一个规范化函数，避免查询参数
   自身触发 PostgreSQL 协议错误。MySQL/SQLite 可能已有未转义的旧身份，因此这两种表示在
   同一次索引查询中匹配；若匹配到多行，沿用原有歧义错误，不任意选择账务 ID。
   已带 ID 的比较也接受旧身份的规范化表示，不改写已存日志。PostgreSQL 查询从不传入原始 NUL。

## 错误处理

- 普通 `08P01` 只输出 SQLSTATE 和固定的协议错误说明，不根据错误码推断代理冲突。
- 只有 `08P01` 消息匹配明确的预处理语句名称冲突特征时，才附加预处理语句诊断提示；
  匹配时可以读取消息，输出不能包含消息或语句名。
- `42P05` 表示重复预处理语句，输出固定说明；代理配置只能作为排查方向，不能断定是其他客户端造成。
- 其他驱动、包装错误和非驱动错误保留当前脱敏契约。
- 不新增前端提示或 API 错误，不需要新增 i18n 文案。后台错误继续走现有系统日志。

## API、数据模型及配置

无新增 endpoint、请求/响应字段或鉴权变动；现有管理回放入口与权限保持不变。
无新增表、列、索引、迁移或查询次数，不扫描历史数据库。无新增用户配置。
Version 2 回填仅在非 PostgreSQL 且身份含 NUL 时，将原有身份等值查询扩展为原值/规范化值的
`IN` 查询；仍有原有身份约束、`LIMIT 2` 和 3 秒查询超时，使用现有索引。
继续使用现有日志管线开关、回放开关、超时、批量大小和并发限制；不改变 relay 执行流程，
因此不新增主链功能开关。发布后已有兜底文件由既有回放流程处理。

## Main Chain Impact

同步 relay goroutine 不增加文本扫描、DB/Redis 调用、文件操作、锁或 goroutine。
规范化仅在已经脱离主链的后台写入/回放阶段执行，在现有 DB 调用前完成。
不改变额度结算、成本/提成 continuation 的调度和日志 ID 关联。

## Shared Resource Audit

| 资源 | 当前共享情况与本次影响 |
|---|---|
| `logs` 表 | 后台写入、管理查询和其他日志功能共用；保留原批量 INSERT 和按主键身份查询；Version 2 的旧身份兼容不增加 SQL 次数或锁 |
| `LOG_DB` 连接池 | 未配置独立日志 DSN 时 `LOG_DB = DB`，与主链共享池；本次不增加连接或 SQL 次数，不延长事务内处理 |
| 日志缓冲、事件对象 | 沿用既有固定 worker、有界队列和 flush 互斥；在 worker 独占事件时处理，不在入队锁内扫描 |
| fallback 文件与 writer | 沿用既有文件管理和句柄，不另开文件或在主链执行 I/O |
| continuation 与账务缓冲 | 不新增访问或改动 payload、金额、调用次数 |
| Redis | 不读取、不写入，无新 key 或命名空间 |

现有共池是部署配置中的既有耦合，本次不引入新的共享资源冲突，也不通过主链协调锁解决问题。

## Concurrency Analysis

- 30k RPM 约 500 请求/秒；100k RPM 约 1,667 请求/秒。
- 每请求新增同步 DB/Redis 调用为 0，新增主链锁和 goroutine 为 0。
- 后台额外成本与被处理字符串总字节数线性相关；正常文本不创建替换字符串。
- INSERT 批量大小、已有 ID 回查次数、writer 数量、池容量保持不变。
- 异常文本每个 NUL 增加 5 字节，分配受既有批次/队列及原日志大小约束；不增加永久缓存。
- 用普通文本和含 NUL 文本的基准测试记录吞吐与分配；这些是局部测量，不能宣称已完成 100k RPM 压测。

## 测试设计（先写测试，再改实现）

已扩展现有 `relay_log_idempotent_insert_test.go`、`relay_log_recovery_test.go` 和
`gorm_logger_test.go`，使用 testify 与真实数据库。

1. 等价类/边界：空字符串、普通中文、实际 NUL 位于开头/中间/末尾、连续多个 NUL、
   原本可见的 `\u0000`、合法 Other JSON；逐字段比较存储原值，跳过查询脱敏钩子。
2. 原始故障回归：构造 51 条合成日志，第 43 条 Content 含 NUL；整个批次成功，
   原始时间、类型、数值不变，只有 NUL 被替换。不把真实用户日志纳入仓库测试夹具。
3. 每个持久化文本字段独立含 NUL；验证二次处理幂等；覆盖有限长字段替换后的合法边界，
   以及超长时不静默截断且回放失败记录仍可恢复。
4. 文件回放：通过真实 writer 回放包含 NUL 的旧 JSONL，核对成功计数、失败计数及全部数据库记录。
5. ID 回归：含 NUL 的 RequestId 规范化后重试仍匹配已写记录；同时覆盖旧 ID 冲突和新旧记录混批，
   验证返回 ID 与 continuation 关联没有错位。
6. 错误分类：普通/未知 `08P01`、明确预处理冲突、`42P05`、包装后的 PgError；
   确认输出无驱动消息中的敏感标记，保留其他数据库的现有用例。
7. 运行相关 model 测试和后台所有权相关竞态检查；如果环境不支持 race，明确记录。

## 验证与交付

在本地隔离的真实 SQLite、MySQL、PostgreSQL 上验证写入及文件回放，使用项目当前驱动；
PostgreSQL 保持 simple protocol。隔离测试配置须在运行前核对，避免测试 harness 自动连接共享库。
若独立日志库的 ClickHouse 测试条件可用，追加兼容验证，否则明确披露该项未测。
不改变 schema，因此无需迁移升级矩阵；所有测试数据按用例范围清理。

### 验证环境与命令

本地隔离实例：SQLite 3.50.4（项目 glebarez 驱动）、MySQL 5.7.44、PostgreSQL 17.11。
MySQL/PostgreSQL 使用专用 Docker 容器及 `nul_test` 数据库，只绑定本机回环地址。
每次命令显式设置 `SQL_DSN` / `LOG_SQL_DSN` 指向隔离实例，覆盖测试 harness 的 `.env` 默认值。
版本通过真实 SQL 查询确认；PostgreSQL 沿用项目 simple protocol。

- 修复前：51 条批次与 Version 2 身份查询均复现 `08P01 invalid message format`；
  新增测试先失败，再修改实现。
- 三库针对性测试通过：普通文本、九个持久化文本字段、实际/可见 NUL、旧文件回填、51 条混合日志逐字段比较、
  生成 ID、重复回填、长度边界、错误脱敏。MySQL/SQLite 的旧原始身份兼容与歧义检测测试也通过。
- 竞态检查通过：
  `go test -race ./model -run 'Test(InsertRelayLogs|RelayLogs_NUL|RecoveredRelayLog|ReplayRelayLogFallback|PersistRelayLogEvents|SanitizeDBError|GormLogger)' -count=1`。
  Windows 下显式开启 `CGO_ENABLED=1`，使用 MSYS2 GCC 15.1.0 并将编译器目录加入该进程 PATH。
- 最终完整 model 包测试及基准通过：
  `go test ./model -count=1 -bench '^BenchmarkNormalizeRelayLogText$' -benchmem`，耗时 71.761 秒。
  Windows/amd64、Ryzen 7 8700G 上，约 1.1 KiB 普通 Content 为 63.76 ns/op、0 B/op、0 allocs/op；
  同尺寸含一个 NUL 为 329.2 ns/op、1280 B/op、1 alloc/op。测量只覆盖文本规范化，未包含数据库 I/O。
- 已对身份查询执行 EXPLAIN：小型隔离测试库中 MySQL 使用 `idx_created_at_id`，
  PostgreSQL 使用 `idx_logs_channel_id`；均为索引访问。现有 `idx_logs_request_id` 可用于身份过滤，
  无需增加大表索引；这不是生产大表查询计划或负载验证。
- ClickHouse 未运行真实实例验证；未执行 100k RPM 混合负载测试。
- 本地验证阶段没有数据库结构变更或服务器操作；后续经用户明确授权的测试服务器部署见下一节。

本地测试输出保存在 `_local/relay-log-recovery/nul-*-test.log`，不作为仓库测试数据提交。

## 香港测试服务器部署与集成测试

2026-11-08 按用户要求覆盖 `43.251.102.17` 上的 `/root/new-api`，没有并行启动第二个网关实例。
原版本为 `v1.0.0-rc.48-343-g127c82331`，本次版本为
`v1.0.0-rc.48-343-g127c82331-hk-20261108-nul`。

### 部署过程与结果

- 使用 Go 1.26.10、Linux/amd64、`CGO_ENABLED=0`、greenteagc 及项目 moejs PGO profile 构建。
  构建时源码基于 `127c82331`，包含当时尚未提交的本次日志修复；部署版本号保留该构建基线。
- 上传压缩程序并核对 SHA256，正常 SIGTERM 停止旧 PID `19502` 后原位替换，
  使用原工作目录、环境及日志输出启动替代进程。新 PID `1329455`，恢复健康耗时 3.78 秒。
- 程序 SHA256：`4c06ab881c98681d57a9353c30c4d7db9a055092507d9fcbff94d47ef822059b`。
- `/root/.env` 哈希不变，批量更新保持开启；部署前后主库和日志库 schema 快照一致。
- 公网 `/api/status` 返回新版本，首页 HTTP 200，首页内容哈希与部署前一致。
- 回滚副本、原配置、主库备份及 schema 快照保存在 `/root/hk-update-20261108-nul/`，
  旧程序为 `new-api.previous`。未变更数据库配置、现有渠道或定价。

### 集成测试结果

直接通过原 Nginx 入口 `127.0.0.1:3000` 访问运行中的新网关，使用服务器原
MySQL 8.0.46 与 PostgreSQL 14.24。模拟上游仅由测试脚本中的临时回环 HTTP 线程提供，
未启动额外网关，也未调用真实 AI 服务。

命令：`python3 -u /root/hk-update-20261108-nul/integration.py`。
29 项检查通过，正式执行耗时 15.79 秒。首次脚本启动因列表解包数量写错在创建测试数据前退出，
修正脚本后完整重跑；该次启动不计为产品测试失败或通过。

| 场景 | 结果 |
|---|---|
| 正常非流式和 SSE | 均 HTTP 200，响应内容正确，SSE 正常结束，用量 20 输入/10 输出均保留 |
| 上游错误含实际 NUL | 客户端收到预期 HTTP 400，后台错误日志成功入库并显示可见 `\u0000` |
| 上游错误含原本可见的 `\u0000` | HTTP 400，文本没有重复转义 |
| 混合历史 JSONL | 38 条消费、13 条错误，第 43 条含 NUL；51/51 回填，失败 0，逐字段核对一致 |
| 日志查询 | 管理员与所属普通用户均可按历史时间查到全部 51 条 |
| 已带 ID 的同批再次回填 | 再处理 51 条，数据库仍只有原来的 51 条 |
| Version 2 请求 ID/模型名含 NUL | 首次回填成功；模拟丢失 ID 后重放，仍只保留一条日志 |
| 已结算消费日志回放 | 原消费日志和成本记录均未重复；余额、已用额及成本收入不变 |
| 回放权限 | 未登录为 401，普通用户为 403 |
| 最终管线 | 熔断关闭，重试积压 0，待回填文件 0，没有新增 `08P01` 或启动 panic |

测试共保留 56 条日志，ID `2724755`–`2724810`，归属测试用户 `hk_nul_1_ffb0c90f`。
51 条历史夹具保留北京时间 2026-10-28 01:15:46–01:15:48，数值 quota 为 0；
2 条实时成功请求合计消费 10 quota，用户余额与已用额合计仍为初始 1,000,000，成本记录收入与消费一致。
测试用户（含控制账号）、令牌、渠道均已停用，登录会话已注销，模拟上游已退出。
原有渠道 ID/状态、配置哈希保持不变，集成测试期间网关 PID 不变且始终为单实例。

服务器证据为 `/root/hk-update-20261108-nul/integration-summary.json`、`integration-logs.json`、
`deployment.json`；本地留存于 `_local/hk-deploy-20261108-nul/`。
该轮为功能集成测试，未执行高并发压测，也未覆盖 ClickHouse。
