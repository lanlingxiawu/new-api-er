# 隔离验收进程监控脚本

用户已要求直接实现每秒检查、kill 后自动拉起的验收脚本。此脚本用于复现已确认的生产操作方式；没有取得实际生产监控脚本，因此只能证明该实现的行为，不能声称与未提供的脚本字节一致。

## 目标和范围

`bench/validation/process_monitor.py` 以前台监控进程运行固定绝对路径的候选程序。每秒检查其子进程退出状态，退出后重启；监控自己退出时停止所拥有的子进程并等待有限退出预算。工作目录锁防止同一份配置重复启动。无 shell 拼接、无进程名扫描、无按名称批量终止。

## 生命周期和数据流

参数校验 → 取得工作目录独占文件锁 → 读取指定私有 JSON 环境配置 → 检查必需 DSN → 校验候选程序 SHA → 启动候选子进程 → 每秒检测 → 记录退出并重新启动。监控收到 TERM/INT 后停止重启，向其子进程发送终止信号，预算耗尽后只强杀该子进程，释放锁。应用就绪和 AI 吞吐由外部测试驱动验证，进程存在不等于 ready。

## 接口和配置

CLI：`--executable`、`--workdir`、`--environment`、`--events`、`--expected-sha256`、`--interval`（默认1秒）、`--stop-timeout`（默认120秒）及 `--` 后的程序参数。JSON 配置只允许字符串键值，必须明确 SQL_DSN、LOG_SQL_DSN 和 REDIS_CONN_STRING，避免错误目录下退回 SQLite。配置只用于子进程，不进入事件日志。

无网络 API、新数据库表、索引、迁移或项目运行配置；监控参数仅为测试工具的执行参数。子进程 stdout/stderr 交给调用者保存；调用者负责限制日志访问权限和脱敏。事件日志为 JSONL，记录 PID、时间、退出码、SHA 与状态，不记录命令参数或凭据。

## 核心逻辑和错误

每次 spawn 前校验产物存在和期望 SHA。初始化参数、配置或锁冲突失败时非零退出；子进程退出或启动失败持续按指定周期重试，并记录有限单条错误类型。不存在默认重启次数上限，调用者可用事件监控告警。只能管理直接创建的进程；不能据此宣称支持任意 daemonize、复杂子进程树或生产 HA。

## 测试设计

先写标准库 unittest：真实短命子进程被 terminate 后在轮询窗口内产生新 PID；第二监控竞争同一工作目录被拒；缺少 DSN 在启动前拒绝且日志不含配置值；候选 SHA 不匹配拒绝；停止监控后子进程退出。所有夹具使用临时目录、受控 Python 子程序和无效假 DSN，不连接网络。再使用生产候选 Linux 二进制、真实隔离 DB/Redis、Nginx 和模拟 AI 上游验证启动/计费/恢复时间。

## Main Chain Impact

脚本运行在验收进程外，不在 relay goroutine、middleware 或生产二进制注入任何逻辑。共享资源仅为本次独占目录中的锁文件/事件文件及其子进程；relay 不访问这些文件。对网关的压力和依赖交互由独立测试驱动产生，数据库和 Redis 均为本轮专用实例。

## 实施状态

本文件先定义测试与实现契约，实际执行结果记录在日期验收报告中。模拟脚本的通过不替代真实生产硬件、准确数据库版本或现场监控程序的验证。

本轮工具测试 4/4 通过，隔离 Linux 网关的空闲/在途 TERM 与 KILL 恢复结果见 [2026-11-06 验收记录](testing-results-20261106.md)。当前项目整体仍未通过生产验收。

## 再次执行

先创建独占工作目录，将仅属于测试实例的 `SQL_DSN`、`LOG_SQL_DSN`、`REDIS_CONN_STRING` 及其他环境变量写入私有 JSON 对象。每个键和值都必须是字符串；不得提交该文件。CLI 示例中的路径与 SHA 必须替换为本次候选的实际值：

```text
python bench/validation/process_monitor.py --executable /absolute/path/new-api --workdir /absolute/path/acceptance-data --environment /absolute/path/environment.private.json --events /absolute/path/monitor-events.jsonl --expected-sha256 CANDIDATE_SHA256 --interval 1 --stop-timeout 115 -- --port 3000
```

该工具以前台运行，子应用被 kill 后会重新拉起。需要结束整轮测试时，向监控进程发送 TERM/INT；Windows 也可在其独占工作目录创建 `.monitor-stop` 文件，触发停止子应用并退出监控。不要仅停止子应用来结束整轮测试。脚本默认停机等待 120 秒；示例 115 秒是工具对自身子进程的等待预算，不代表应用停机及重启能满足整体 120 秒验收门槛。

```text
python -m unittest discover -s bench/validation -p test_process_monitor.py -v
```
