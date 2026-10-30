# 2026-10-30 官方上游合并

用户已明确授权合入官方 upstream/main 的 24 个提交。基线为 zhuzhan `6b6eda7b5`，目标为 `a2defd2a8`；本次仅同步和兼容既有改动，不部署或推送。

## 目标与范围

合入协议转换、流终止、渠道亲和重试、缓存计费字段、任务统计修复与前端依赖更新。保留本地渠道编号、分组重试、流诊断、额度安全、日志导出和定价扩展。部分定价修复已提前同步，应避免重复或回退。

## 数据流与 API 契约

沿用请求鉴权、渠道选择、协议转换、上游调用、响应和结算链。Azure 的 Responses WebSocket 继续使用既有显式渠道开关与鉴权。无新增管理 API、权限层级或响应结构。即时成功任务在结算后记录既有性能统计；提交失败不记健康样本。

## 数据模型与配置

无新增表、列、索引或迁移；model/channel_constraint.go 为内存渠道过滤逻辑。保留本地配置系统及已有管理员覆盖，核对上游默认任务超时 72 小时与本地持久化设置的一致性。依赖变动限于前端和构建工具，不更新 ORM/数据库驱动。

## 业务边界与错误处理

终止标记必须完全匹配，不能把含 [DONE] 前缀的普通内容当终态；保留本地流写失败和诊断语义。亲和渠道失败后从普通最高优先级重试，跨组重试保留本地预算限制。转换保留工具参数数字精度、Gemini 文本及 imageConfig、Bedrock 安全参数、缓存创建用量。既有错误协议、脱敏和退款处理不变。

## Main Chain Impact

同步执行原有转换、扫描和渠道筛选，新增或调整的处理是内存操作。参数正则按管理员配置文本缓存，避免每请求重复编译。即时任务统计更新既有内存计数，投递到既有 gopool 异步更新 Redis；数据库聚合由既有后台 worker 完成，不新增同步外部调用。Azure WebSocket 和性能统计沿用原有功能开关。

## Shared Resource Audit

- Redis：渠道亲和沿用 `new-api:channel_affinity:v1` 及 usage stats 命名空间。即时成功任务新增一次既有 `perf:{model}:{group}:{bucket}` 异步事务管线，包含计数累加和一小时过期；一秒超时，失败不传播至响应。与普通中继性能统计共用命名空间和 Redis 连接池，不与额度、令牌或渠道缓存键混用。
- DB：原有 channels、tasks、额度及性能统计存储继续使用原有访问流程，不增加查询、锁、连接或表扫描；任务超时改变保留时长，需要关注长期未完成任务数量。
- 内存：正则缓存使用 sync.Map、模式由管理员覆盖规则提供；缓存不设淘汰，上游实现的长期配置变更累积风险记录为限制。统计使用现有原子桶；亲和偏移量仅存请求上下文。
- 连接池、worker、文件：即时任务统计复用现有 gopool 和 Redis 池，没有新增独立 goroutine 或协调锁。池容量并未新增专用预留；即时任务比例较高时会增加后台统计资源竞争，性能统计开关可关闭此路径。没有新增文件句柄。

## Concurrency Analysis

在 30k 至 100k RPM 下，普通中继新增 DB/Redis 调用为零。开启统计时，每个即时成功任务新增一次池任务和一次异步 Redis 事务管线；若所有请求均为此类任务，分别增加约 500/1667 次管线每秒。同步只做原子计数、sync.Map 和池投递；Redis 一秒超时限制单任务等待，池压力仍须容量验证。正则缓存命中为内存查找，未命中才编译；重试偏移按尝试次数执行常数运算。静态分析不能替代真实负载测试，本次不运行生产压测。

## 验证计划

在修改前使用上游已有回归场景：亲和回退与跨组、精确 DONE、缓存用量兼容、工具数字精度、Azure 开关、成功/失败统计、超时边界。完成冲突处理后运行相关 Go 测试和构建、独立 relaykit 测试和 GOWORK=off 构建、前端 typecheck/lint/test/build。核对受保护前端文件与官方目标字节一致，检查渠道类型硬编码和冲突标记。数据库行为若发生实质修改，补做三方言验证并记录不可用项。

## 合并与验证结果

- 创建备份分支 `backup/zhuzhan-before-upstream-20261030`。README 保留本地结构；本地 README 没有上游所修改的类型检查段落。前端规范合入 Bun 检查命令，保留本地组件复用规则。
- 渠道选择同时保留探针固定组逻辑、按用户/组解析的重试额度和上游亲和偏移。跨组阈值使用实际重试次数，优先级选择扣除亲和尝试；总尝试预算不变。
- 流扫描保留本地停止及诊断测试，加入上游精确 DONE 边界用例；避免重复引入停止测试和与全局配置冲突的并行标记。AWS 测试合并双方所需导入；亲和缓存测试采用上游唯一键实现。
- 模型广场分类修复此前已同步，自动合并无额外 UI 改动；五个认证/HTTP 基础文件与 `a2defd2a8` 逐字节一致。新增代码未引入 58–63 范围的错误渠道编号；全仓检索中既有测试描述和独立 relaykit 示例编号不在本次改动范围。
- 本机 Go `1.26.10 windows/amd64`。Bun `1.4.4` 下载至未跟踪的 `output/merge-20261030-tools/bun-windows-x64`，未替换全局 Bun。`bun install --frozen-lockfile` 完成；新 Bun 下 `bun run typecheck` 通过（1564 文件、2 项目）。
- `bun run test`：241 文件、2772 项全部通过。`bun run build` 通过；`bun run lint` 无错误，保留既有 7 条警告。本次变动的 8 个 TS/TSX 文件 oxfmt 检查通过，源码均为 UTF-8 无 BOM。
- `GOWORK=off go test ./...` 和 `GOWORK=off go build ./...`（relaykit 目录）通过。根模块 `go build ./...` 和 service 测试编译通过。relay 全部包、common、pkg/perf_metrics、pkg/billingexpr、middleware 和 setting 全部包测试通过。
- service 亲和、重试、探针、缓存计费、任务轮询及表达式相关定向回归通过；另单独运行 `TestSweepTimedOutTasksAtTheDefaultDeadline`、`TestCalculateTextQuotaSummaryBillsCacheCreationInputTokens`、`TestChannelSelect_`、`TestCacheGetRandomSatisfiedChannel` 均通过。
- 独立临时容器数据库为 MySQL `8.0.46`、PostgreSQL `15.19`。配置临时 `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`，执行 `go test -count=1 -timeout 180s ./controller -run 'TestChannelAffinityRetryPriorities|TestRequestPolicyRoutingDatabaseMatrix|Test.*ChannelTest.*|Test.*Bearer.*|Test.*Probe.*'` 通过，矩阵包含真实 SQLite。无业务数据库、迁移或数据库驱动改动，未执行历史 schema 升级及二次启动 DDL 检查。
- 插件 lint 无错误，保留既有警告；插件 format 检查仍报告未改动的 `plugins/tasks/xai/plugin.js`，与上次合并文档中的既有问题一致。本次没有修改插件源码。
- 初次全量 model/controller 在未配置数据库时因既有用例未跳过而发生空指针。设置独立临时主库和日志库后，`go test -count=1 -timeout 300s ./model` 全量通过（84.182 秒），同命令 `./controller` 全量通过（226.357 秒）。构建最终前端产物后再次执行根模块 `go build ./...` 通过。未运行全仓所有 Go 测试、Redis 集成和真实流量压测，不声称生产容量已验证。
- 验证完成后停止两个 `--rm` 临时数据库容器；原有未跟踪 `output/` 保留，测试日志及临时 Bun 不加入提交。
