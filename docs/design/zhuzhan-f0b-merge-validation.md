# zhuzhan 合并验证（2026-10-08）

合并源：`zhuzhan-f0b-latest` 的 `b4b282bb1`；目标：`zhuzhan` 的 `33b3f544f`。
实现与独立审查记录见相关功能设计文档。本次恢复工作只修正测试和文档。

## 测试服与隔离

香港测试服运行版本 `v0.0.0-zhuzhan-merge-f0b-20261008`。
部署备份：`/root/backup/20261008_084524/`；数据库备份：
`/root/dbbackup/merge-20261008/`。沿用之前已部署的业务二进制。

数据库测试使用专用 MySQL `newapi_mergetest` 与 PostgreSQL
`logdb_mergetest`，不会清理业务库。服务器 MySQL 客户端版本 8.0.46，
PostgreSQL 客户端版本 14.24。测试包以 Linux/amd64、CGO_ENABLED=0
交叉编译，在服务器依次执行 `-test.count=1`，设置
`TEST_DB_CLEANUP=true`，用 `nice -n 10` 降低调度优先级。
连接信息不写入仓库。
全部测试结束后两个隔离测试库已删除，测试输出仍保存在
`/root/mergetest/`。清理后网关健康检查成功，版本保持合并测试版。

## 恢复后的验证

- 根模块 `go build ./...`、`go vet ./...` 通过。
- 除 controller/model/service 外，根模块所有包 `go test -p 4` 通过。
- relaykit 使用 `GOWORK=off go build ./...` 和 `go test ./...` 通过。
- 服务器 model、service、middleware 全包测试通过。
- controller 首轮有三项失败，修正测试后最终全包重跑退出 0、PASS。
- 前端沿用上一轮结果：类型检查通过、lint 仅既有警告、1798 个测试通过；
  本轮没有改动前端代码。

## 本轮审查与修正

员工导出模板删除和任务创建已写入 `audit_logs`，旧测试仍查询使用日志表。
测试改为验证独立审计表的事件及导出范围；清理仅针对本测试员工的审计行。
Suno 原生提交已由插件声明路由接管，旧测试直接挂 RelayTask，缺少请求解析。
测试现独立加载内置 Suno 插件，从注册表按声明路径取得路由并执行
PrepareTaskPluginRoute，再进行渠道分配。初次修正将中间件放在固定路由上，
漏掉测试后来注册的 Claude/Gemini 路由；最终改回共享中间件链，完整重跑通过。
聊天和 Midjourney 测试按生产顺序在渠道分配后启动用户超时。

这些改动不执行在生产中继链上，不增加生产 DB/Redis 调用、锁或工作池使用。
测试使用 httptest 上游，不调用真实 AI 服务。复核了请求准备、渠道约束、
超时与计费顺序及按行清理。认证相关的五个前端基础文件与
当前 `upstream/main` 字节一致；本地 `main` 是旧引用，不能用它验证本次上游基线。

## 已知范围与行为变化

Gemini 推理映射保留上游统一 reasoning 实现，行为差异见
`gemini-relay-conversion-fixes.md` 第 5 节，尚未恢复旧版省成本策略。
此次真实数据库执行覆盖 MySQL 主库和 PostgreSQL 日志库组合；
未完整执行 SQLite 主库及 PostgreSQL 主库的全量验证，也未执行竞态检查或负载测试。
界面登录、员工日志、导出中心和价格巡检的人工验收仍待用户完成。
