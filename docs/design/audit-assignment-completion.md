# 告警自动归属补漏

2026-11-24。用户在审查报告后明确要求“进行修复”，本次在已有普通账户敏感操作补丁上实施以下修复。

## Goals and scope

修复可信密码恢复结果、客户自身批量敏感操作的员工归属，并使负责人显示、筛选、汇总和导出一致。保留当前客户归属和员工权限边界。生产告警 #90 的具体 action 尚未取得，不能仅凭截图断言属于哪条分支。

## Existing Logic and Related-Path Findings

- `model/audit_security_capture.go:CapturePasswordRecoveryOutcome` 记录密码提交成功及提交后清理失败，保留匿名操作者 role=0；`Other.Op.Identity` 含事务中验证的账户角色、原始及生效认证版本。`rules.go:Normalize` 只接收这一服务端身份来源。
- 修复前 `customer_access.go:customerSensitiveEvent` 只接收 role=1，遗漏上述已经确认受影响账户的恢复结果。现在由 `customerSensitiveEventVersion` 统一返回版本 4/5；`accountAlertAccessible` 同时纳入客户批量敏感操作。
- `rules.go:Evaluate` 的批量计数按账户、资源、可见性聚合，未按操作身份及目标隔离；不能仅看最后一个事件就认定整条批量告警属于客户本人。
- `store.go` 按规则版本合并告警，保存 finding 和精简 evidence；已消费事件不会在升级后重新检测。
- `assignment.go` 在读取时根据当前 inviter、员工状态及客户角色计算 owner，不持久化员工自动归属。原 SQL 的 NULL 三值逻辑与 Go 的零值读取不同，导致“未分配”以及 session_reuse 负责人筛选漏行。
- `login_target_repair.go` 和 `cmd/audit-login-target-repair` 已提供显式、限定时间和编号、默认仅预览的维护流程。历史修复复用这一流程，不增加启动或后台全量回填。

## Data flow and business rules

1. 普通账户自身敏感操作使用版本 4；同时用于批量规则。新增版本 5 仅用于服务端证实的密码恢复结果：动作必须为 committed/cleanup_failed，成功，account 资源、actor>0、role=0、自身目标，并具有匹配 actor、普通账户角色、recovery 方法及递增认证版本的完整 Identity。
2. 原始 actor role 保持 0。版本 5 是受影响账户已验证的持久化证明，不伪造已登录操作者。
3. 客户批量计数、告警合并和 evidence 均隔离相同身份类别、客户及资源。管理操作、其他目标、缺少身份的事件不混入客户批量告警。管理类检测继续沿用原规则。
4. 列表、详情、待处理计数和员工调查共用客户可访问性判断。个人视图、个人通知和自动处置保持原来的规则白名单。
5. SQL 对可空标量按 Go 读取语义归零；无完整证明的历史记录归为未分配。历史敏感操作原有完整 finding 证明继续有效。
6. 历史密码恢复及批量记录用现有维护命令新增的客户归属模式修复。时间范围最多 31 天、显式编号范围、每批 100 条、乐观版本检查。恢复事件要求完整 finding 集合及可信事件身份；旧批量仅在单次命中且完整 evidence 能精确复算 matched_count 时升级。缺失、混合角色/目标、截断或冲突证据一律跳过。更新规则版本和调查版本，写 repair action 及活动修订号，不改原始日志和 finding，不写 owner。

## API contracts, storage and configuration

现有 HTTP 接口、返回结构、认证等级和员工鉴权不变；没有新 API、配置、表、列或迁移。维护 CLI 增加显式模式开关，仍必须指定 SQL_DSN，默认 report-only，只有 `-apply` 才写入。升级不会自动运行历史修复。

## Error handling and lifecycle

查询错误继续向现有调用者返回，维护命令不打印连接凭据；证据不足返回可审查的跳过状态。维护写入在短事务中按告警版本和 purge 状态比较更新，冲突保留原记录，可重新预览。员工转移、禁用、删除及客户角色变化仍在读取时生效。重复执行维护命令应幂等。

## Main Chain Impact

全部修改位于审计后台检测、管理查询及显式维护程序；AI relay 同步路径不增加工作，认证和密码修改流程不变。

## Shared Resource Audit and index design

- 审计事件、告警、finding、action 和活动修订表：主 relay 不读取；复用审计独立数据库连接池和现有事务。维护程序使用 `OpenAuditSecurityDB` 的独立 2 连接池，单批有限、超时 30 秒、单次写事务超时 5 秒。
- `users`、`employee_profiles`：现有归属查询不变，仅按客户/员工主键关联，不加锁、不修改用户数据。relay 的 `middleware/auth.go:TokenAuth` 经 `model.GetUserCache` 使用用户数据；本次只在独立审计读池执行已有主键关联，不新增主链调用、用户扫描或锁。relay 不访问员工资料表。
- 历史候选按 `last_seen,id` 游标及时间/编号过滤，finding 按 `alert_id` 现有索引读取；事件按 event_id 读取。没有新扫描路径或索引迁移。
- 不增加 Redis、共享内存缓存或 goroutine；不占用 relay 工作者。主链每请求增加 DB/Redis 调用、锁及 goroutine 均为 0。

## Validation

2026-11-24 已完成。Go 1.26.11 windows/amd64；先用新增断言复现密码恢复、批量归属、NULL 筛选失败，再实施修复。结果不使用 overlay 替代生产实现。

| 环境 | 范围与结果 |
| --- | --- |
| SQLite，项目驱动 modernc.org/sqlite v1.40.1 | auditsecurity 全包：655 个测试/子用例通过、0 失败、22 跳过。 |
| MySQL 5.7.44，独立临时数据库 | 相关身份、客户、员工、负责人、历史修复及查询计划：294 通过、0 失败、0 跳过。 |
| PostgreSQL 18.7，独立临时数据库 | 同上：294 通过、0 失败、0 跳过。 |
| 补充历史多次恢复触发证明 | 在三库分别追加验证：每库 3 个测试/子用例通过；保留源事件可补齐证明，缺失任一触发证据则拒绝修复。 |

SQLite 跳过项要求独立 Redis、真实数据库 EXPLAIN、指定 MySQL/PostgreSQL/ClickHouse 源库或并发 fixture 环境；不能把这些项算成通过。相关 MySQL/PostgreSQL 的负责人、客户、员工、待处理和本人查询计划已在真实数据库覆盖。没有新增 schema；既有升级/重复迁移测试在三库通过，第二次不发 DDL。未运行生产 schema 演练、生产部署或此次浏览器验收。

可信/伪造恢复身份、管理员/其他目标混入、NULL 显示/筛选/统计/导出、员工转移/停用及历史修复预览/应用/幂等/版本冲突均覆盖；历史 finding、匿名 actor role 和 evidence 保留。`go build ./...`、`go vet ./service/auditsecurity ./cmd/audit-login-target-repair`、维护命令 `-h` 与 `git diff --check` 通过。

完整日志和不含连接密码的运行脚本位于本地忽略目录 `_local/audit-assignment-review-20261124/`：`fixed-{sqlite,mysql,postgres}.log` 为首轮结果；`final-{sqlite,mysql,postgres}.log` 仅为补充证明用例，不覆盖前一轮结果。新增补充用例之后业务实现未变。

```powershell
go test -c -o _local/audit-assignment-review-20261124/fixed.test.exe ./service/auditsecurity
python _local/audit-assignment-review-20261124/run_fixed.py sqlite fixed
python _local/audit-assignment-review-20261124/run_fixed.py mysql fixed
python _local/audit-assignment-review-20261124/run_fixed.py postgres fixed
go test -c -o _local/audit-assignment-review-20261124/final.test.exe ./service/auditsecurity
python _local/audit-assignment-review-20261124/run_fixed.py sqlite final extra
python _local/audit-assignment-review-20261124/run_fixed.py mysql final extra
python _local/audit-assignment-review-20261124/run_fixed.py postgres final extra
```

## Historical maintenance

先部署修复后的应用。普通客户旧版 sensitive_operation 若已有完整普通角色快照，读取时即可归属；旧版密码恢复与批量告警按如下命令预览。维护程序需通过环境变量 `SQL_DSN` 指定目标主库，不自动加载 .env 或迁移 schema。时间和编号必须替换为实际待处理范围。

```text
go run ./cmd/audit-login-target-repair -customer-assignments -start <起始Unix秒> -end <结束Unix秒> -min-id <最小告警编号> -max-id <最大告警编号>
```

输出 `candidate` 表示证据可验证，`already_linked` 表示身份已经支持归属，实际负责人仍取决于当前有效员工关系。检查预览后，使用相同范围追加 `-apply`，成功输出 `repaired`。`missing_evidence`、`conflicting_evidence`、`unsupported`、`purged` 不写入，`version_conflict` 可重新预览。超过一次命中的旧批量告警不能用滚动 evidence 证明每个历史窗口，继续保留给管理员调查。

本次仅完成代码和隔离测试，未连接截图环境修改 #90 或批量更新其线上历史记录。
