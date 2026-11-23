# 请求日志重启保留与主动清理

状态：原功能及 2026-11-15“损坏日志不阻断删除和清理”修复已在本地实现，未部署生产。修复验证结果见文末。

## Existing Logic and Related-Path Findings

- `model/request_log_recovery.go: scanFile` 在遇到 JSONL 半行、坏 JSON 或非法记录时调用 `fail`，把整个进程标记为恢复不完整；其他文件恢复成功后仍能显示列表。
- `saveRequestLogCheckpointLocked` 因此拒绝所有检查点保存；`model/request_log.go` 的全清和按时间删除均依赖它。`model/request_log_sweep.go` 同时跳过所有文件回收。这解释了“列表正常、root 删除返回 HTTP 200 + success:false、反复点击仍失败”的本地复现。
- `RequestLogWriter.Record` 先写正文再登记索引，日志 worker 与管理删除共用 `reqLogMu`，relay 只做非阻塞投递。修复不能把文件扫描放到 relay goroutine，也不能把尚未登记的活动分段尾部误记为已删除。
- `.request-log-recovery.json` 保存已删条目的恢复边界。不能直接取消恢复保护并保存残缺的位置表，否则后台回收前再次重启可能复活旧日志。正文损坏和检查点损坏需要分别处理。
- 当前 API 为 `DELETE /api/request-log/all`（AdminAuth + `operations.logs:edit`）以及 `DELETE /api/request-log/?target_timestamp=...`（AdminAuth + 请求日志菜单 view + `operations.request-log:edit`）。接口、权限和返回形状不变，无前端改动。
- 生产发生过重启由用户确认；生产原始错误行尚未取得，因此不把本地复现当成生产根因已最终确认。

## 目标与范围

请求日志设置新增「重启时清理历史请求日志文件」，默认关闭，保存后下次启动生效。关闭时恢复本机历史日志，不能因 Redis 未启用或关停快照失败而在后台清理时误删正文；开启时删除本机历史正文及恢复检查点。启用日志采集与重启清理是独立选项。

保留条数仍受最小/最大日志量约束，管理员主动清空和按时间删除仍有效；这些删除不能因磁盘恢复而复活。不改变计费日志、运行日志、请求体缓存、AI 转发协议和主链路 middleware。

## 数据流

启动：加载设置 → 验证请求日志专用目录 → 按开关清理或恢复 → 启动日志 writer → 启动 sweeper → 开始服务。

关闭时先加载本地检查点中的保留索引，再扫描已提交位置之后的正文尾部及新增文件；没有检查点时扫描旧 JSON/JSONL 重建。恢复成功后保存新检查点，再允许定期清理无索引正文。开启时只删除已认领目录内合法日期目录项及本功能检查点，保留根目录与归属标记。

运行期：relay 仍只向既有有界队列投递 → 既有 writer 写正文 → 在原索引锁内登记元数据并更新文件提交位置。超量淘汰、管理员删除、正常关停时保存本地恢复检查点。管理删除先保存未来索引，再提交内存删除；保存失败则不报告成功。

## API 契约与权限

无新 API。读取 `GET /api/option/`，保存 `PUT /api/option/`，请求包含 `scope: "operations.request-log"`、`key: "request_log_setting.clear_on_restart"`、`value: true/false`（后端归一化字符串）。响应沿用 `{success, message}`。

路由使用 AdminAuth 与对应 view/edit 设置权限；不指定作用域的写入仍限 root。新键仅加入现有请求日志设置白名单，不扩大其他设置作用域。清空/删除接口沿用现有路由权限，失败使用已有 `common.delete_failed` 翻译，不返回内部文件系统错误。

## 配置、数据模型及索引

通过 `setting/operation_setting` 注册 `request_log_setting.clear_on_restart`，bool，默认 false，ConfigManager 持久化到 options。getter 在草稿锁内复制值，仅启动时读取。无新表、列、数据库迁移或 SQL 查询索引。

本地 `.request-log-recovery.json` 包含版本、ID 高水位、保留记录元数据/相对位置、各正文文件已提交字节位置；旧独立 JSON 用 -1 标记已处理。临时文件 `.request-log-recovery.tmp` 写入、Sync、关闭后 Rename 替换正式文件。sweeper 豁免两者及 `.new-api-request-log` 归属标记。

文件位置表受 reqLogMu 保护，检查点时移除已不存在文件的条目。检查点不包含请求/响应正文或原始凭证头；列表 URL 继续脱敏。

## 业务规则与边界

- 支持旧 `<date>/<bucket>/*.json` 与当前 `<date>/seg-*.jsonl`。单文件逐行补读，目录每批 1000 项，单条恢复上限 256 MiB；索引扫描期间最多约两倍最大条数，超量最终保留最新最小条数。
- 记录文件位置而非重新推导路径，详情继续按偏移读取。旧进程 ID 冲突时重分配重复 ID，详情响应与列表 ID 一致，新日志 ID 继续递增。
- 已有检查点保存删除边界，补读只看尾部，手动清空、按时间删除和正常超量淘汰不会从同一分段的残留正文复活。
- 正常退出保存本地检查点，不再写 Redis 关停快照。启动限时删除旧 Redis 快照，恢复不依赖 Redis。既有 Redis 快照兼容函数/测试保留，但不再用于生产启动与关停。
- 新部署首次启动和空目录正常处理。首次升级没有本地检查点时，以仍存在的正文为恢复来源；先前版本已经物理删除的文件无法恢复，先前仅删内存索引但尚未回收的正文无法可靠区分。
- 不跟随目录符号链接扫描；通过 `os.OpenRoot` 限制文件访问和删除范围。部署继续要求独占日志目录，不支持多个进程共用同一目录；日志目录必须在重启后仍存在。
- 首次全量重建可能延长启动时间，之后从检查点补读；不在服务接收请求后执行恢复扫描。

## 错误处理

正文损坏按记录隔离：JSONL 坏行（包括重启留下的半行）跳过，后续正常行继续恢复；旧单文件 JSON 损坏则跳过该文件。恢复位置包含被跳过的字节，避免下次重启重复解释坏记录。超过恢复预算的记录不继续分配内存，跳过该文件剩余部分。坏正文不设置全局恢复失败标记，无有效索引引用的文件可由既有 sweeper 回收；一个分段里仍有正常记录时，整个分段继续保留到这些记录被删除。

检查点损坏、目录不可读或链接异常仍记系统日志，并保护无法确认归属的旧历史；检查点损坏时不重新解释其删除边界，不扫描复活旧记录。管理员主动全清或按时间删除时，若恢复不完整，则通过已有的受限目录遍历，仅枚举正文文件及长度，不解析正文。关闭的 JSONL 以 EOF、旧 JSON 以 -1 记录删除边界；活动分段及枚举期间新建的本进程分段仅使用锁内已提交位置，保留在途写入。完整枚举后原子保存目标索引与位置表，保存成功才提交内存删除并恢复后台回收。枚举、写入或替换失败仍保留原索引和保护状态。恢复标记改为原子读写，避免管理员解除保护与 sweeper 并发访问产生竞态。

按时间删除保留仍在索引中的时间范围外记录；无法可靠恢复的旧损坏记录不参与保留，正文随后按索引引用回收。返回条数仍为实际删除的内存索引条数，未恢复的坏记录不虚构计数。请求日志恢复 panic 在启动入口 recover，不阻断 AI 主服务。

检查点保存失败记录底层错误。管理员删除失败保持原索引，并返回已有本地化删除失败信息。自动超量淘汰失败时仍按数量限制裁剪内存，避免无界增长；如果本地盘故障导致检查点未保存，后续恢复可能重新看到未物理删除的旧记录，但仍受条数限制。

主动启动清理逐目录继续执行，删除失败记日志并交由既有后续清理重试。Redis 遗留快照删除限制为 5 秒，失败不影响本地恢复。保存设置本身不触发删除。

## Main Chain Impact

relay goroutine 上无新增配置读取、文件操作、DB/Redis 调用、锁或 goroutine。既有日志采集与有界非阻塞投递不变。

恢复和启动清理在请求服务启动之前同步执行。每批异步 writer 在既有索引临界区更新文件位置；低频数量淘汰时写检查点。检查点持锁会延迟其他日志 writer/列表查询，但 relay 不访问此锁，不等待写盘；日志队列满时沿用原有丢弃策略。用户主动删除在管理 API 上同步保存，只有恢复不完整时额外枚举文件元数据，按每批 1000 项处理且深度受现有布局限制；正常删除路径不增加扫描。正常关停排空日志队列后保存。

## Shared Resource Audit

| 资源 | 访问与隔离 |
| --- | --- |
| options 表 | 沿用设置保存/启动读取，无 relay 新增 DB 访问 |
| `request_log:snapshot` | 仅启动限时删除旧快照，使用既有请求日志 Redis 客户端；不触及 token/额度命名空间 |
| 请求日志专用目录 | 正文与恢复文件共用专用目录；不触及运行日志和请求体缓存；恢复先于 writer/sweeper |
| reqLogItems、reqLogMu、文件位置表 | 仅日志异步 worker、管理查询/删除、启动/关停使用，relay 不持锁 |
| 恢复状态、活动分段表 | 状态以原子变量发布；异常恢复后的主动删除读取活动分段快照，不新增锁或 IO 到 relay 路径 |
| 连接池 | 不新增 DB/Redis 池或连接，仅复用原有 Redis 客户端 |
| worker/句柄 | 不新增 worker 或逐请求 goroutine，扫描深度受两种已知目录布局限制，句柄及时关闭 |

## Concurrency Analysis

在 30k/100k RPM 下，relay 每请求新增 DB 调用 0、Redis 调用 0、文件操作 0、锁 0、goroutine 0。异步 writer 每条增加一次位置登记；检查点写入由超量淘汰事件触发，频率取决于配置最大/最小条数差，通常远低于请求数，不额外逐条写磁盘索引。保留条数过近会增加日志 worker 检查点成本，主链仍通过非阻塞队列隔离。

## 验证

先编写并运行回归用例，确认旧实现会在无 Redis 重启后被 sweep 删除，再实施修正。

通过：

- 请求日志模型与 sweeper 回归：无 Redis 重启 → 列表/详情恢复 → 多轮清理仍保留。
- 检查点之后的新尾部、手动清空、按时间删除、超量淘汰、最小保留数 0/1、重复重启不复活。
- 旧格式及 ID 冲突、损坏正文/检查点保留、删除检查点失败不修改索引、开启清理删除正文和检查点。
- 请求日志 controller/middleware/router 测试，设置/权限包测试，主包编译检查。
- TypeScript 检查、改动前端文件 lint、七种语言同步、前端生产构建。

限制：本机没有启用 CGO/C 编译器，`go test -race` 未能运行；常规并发 writer/read/sweep 测试通过。既有依赖真实 Redis 的老用例在环境缺失时跳过，新开关 Redis 场景使用 miniredis。此前扩大检查发现 model 全包中其他功能用例在 SQL_DSN 缺失时 panic，以及价格监控文件既有 lint 错误，未把全仓检查报告为通过。未重启用户服务或操作真实日志目录。

### 2026-11-15 修复实现与验证

- 先添加失败用例：坏 JSONL 行/截断尾部与好记录混存、坏旧 JSON，分别全清和按时间删除；删除后在 sweep 前及 sweep 后连续重启，验证已删记录不复活、保留记录详情正常。
- 损坏检查点的全清/按时间删除可重新建立边界，坏文件能回收；删除检查点写入失败保持旧索引及保护状态。
- 检查活动分段的已提交字节与在途未登记记录，删除成功后新日志仍可恢复；并发删除和 sweep 不产生数据竞争。
- 无新 SQL、表、连接池、Redis 键、配置或 API；数据库矩阵不适用本次纯文件/内存修复。测试只使用临时专用目录。
- 重跑相关 model/controller/middleware 测试、原 9 场景隔离复现，并记录实际执行结果和未执行项。

实际结果（Windows amd64，Go 1.26.11；文件夹具均为 `t.TempDir()`）：

- 先运行新增回归，旧实现确实在损坏正文、坏检查点及失败重试场景报错；证据 `_local/request-log-clear-repro-20261115/before-fix.log`。修复后同组及已有重启/删除回归通过：`after-fix.log`。
- `GOMAXPROCS=2 GOMEMLIMIT=1GiB go test -p 1 ./model ./controller ./middleware ./router ./_local/request-log-clear-repro-20261115 -run 'RequestLog|TestSweep_|ReadRequestLog|ResolveRequestLog|KeepRequestLog|ParseRequestLog|AuditResponseSuccess|RLAudit' -count=1 -json`：5 个包通过，236 个测试项（含子用例）通过，10 个依赖 Redis 的旧快照用例因环境未配置而跳过。证据 `regression-limited.jsonl`。
- 原 9 场景控制器复现已按修复后的预期复测：损坏检查点和损坏正文均返回 HTTP 200 + `success:true`；目录不可用、临时文件创建失败、检查点替换失败仍返回 `success:false`，原索引和正文保留。2080 条正常日志、单条 12 MiB 日志正常清空。
- `GOMAXPROCS=2 GOMEMLIMIT=1GiB go build -p 1 ./...` 通过；`git diff --check`（本次 7 个改动文件）通过。证据 `build.log`。
- 第一次默认并发扩大测试遇到编译器内存分配失败，未进入完整测试；保留 `regression.jsonl`、`after-fix-http.log`。降低编译并发后的上述结果才是本次有效回归，未把编译失败算作通过。
- Linux race 检查通过：Go 1.27.3 + GCC 12，`CGO_ENABLED=1 go test -p 1 -race ./model -run 'TestRequestLogRecovery|TestRequestLogDeletionAfterCorruptCheckpoint|TestSweep_ConcurrentWithWrites|TestRequestLogWriter_ConcurrentWritersReadsAndSweeps' -count=1 -v`，32 个测试项（含子用例）通过，0 个竞态警告。覆盖删除解除保护与 sweep 并发、writer/read/sweep 并发、重启与在途尾部保留。证据 `linux-race-gcc.log`。
- Linux 验证环境说明：Go 1.26.11 自动下载因校验服务网络 EOF 未完成；本机已有 Go 1.27.3 镜像最初缺少 C 编译器，最终将该工具链挂载到带 GCC 12 的隔离容器执行。源码只读挂载且禁用网络，不连接生产服务。Windows 构建/回归与 Linux race 的编译器版本不同，分别以上述实测版本为准。

本次不修改数据库行为或 relay 请求执行路径，因此未调用真实上游、生产数据库或生产请求日志目录，也未执行生产删除操作。
