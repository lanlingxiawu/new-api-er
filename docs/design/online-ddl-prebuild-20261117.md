# 上线前审计索引预建

本文说明如何提前创建当前代码需要的审计索引。目标服务器的地址、版本、schema 和备份位置记录在本地 `docs/production-environment.md`。

新功能不追溯旧数据，不执行历史回填。预建脚本和 `MigrateAuditLogs` 不执行历史行 UPDATE；旧行 `actor_is_employee` 保持 NULL，新事件写入时记录快照。建索引会读取已有记录，但不补写业务字段。相关约束见 [新功能不回填旧数据](new-feature-no-history-backfill.md)。

## 手工操作的优先级

程序启动会自动迁移。可以提前创建 PostgreSQL 审计表所需的 nullable 字段和 7 个索引，将索引构建耗时移出停服窗口；实际清单必须与部署代码一致。MySQL 令牌索引为可选预建项。

| 位置 | 操作 | 原因 |
| --- | --- | --- |
| PostgreSQL `public.audit_logs` | 添加 nullable `actor_is_employee boolean`，不设默认、不回填历史 | 当前代码已经需要的字段，是员工筛选索引的前置条件；不是本轮另加需求。加列仍需要短暂表锁。 |
| 同一张审计表 | 顺序执行下列 7 个 `CREATE INDEX CONCURRENTLY` | 首次升级的主要等待来源；并发建索引允许正常写入继续。 |
| MySQL `tokens` | 可选：`idx_tokens_user_deleted_id(user_id, deleted_at, id)` | 当前代码已有索引；采用 `ALGORITHM=INPLACE, LOCK=NONE`，不允许降级为阻塞写入的算法。 |

| 审计索引 | 字段顺序 |
| --- | --- |
| `idx_audit_actor_time` | `actor_role, created_at, event_id` |
| `idx_audit_employee_time` | `actor_role, actor_is_employee, created_at, event_id` |
| `idx_audit_auth_time` | `auth_method, created_at, event_id` |
| `idx_audit_route_time` | `route, created_at, event_id, actor_role` |
| `idx_audit_ip_time` | `ip, created_at, event_id` |
| `idx_audit_customer_cursor` | `user_id, created_at, event_id` |
| `idx_audit_user_route` | `user_id, route, created_at, actor_role` |

本次不需要手工处理新功能表、不需要改使用日志 `logs`、不需要修改旧字段类型，也不回填员工历史快照。正常旧版 343 没有这 7 个索引；“修复四个索引”是中间版本留下同名错误结构时的兼容分支，本次基线升级没有触发，不能预先删除四个索引。

## 操作步骤

先在目标服务器的结构副本确认适用性，保留数据库备份和旧二进制。选择低峰，避免与导出、清理、备份或其他建索引任务叠加。

1. 在日志库检查当前库名、版本和长事务。先处理已确认的长事务来源，不自动终止业务连接。

   ```sql
   SELECT current_database(), version();
   SELECT pid, usename, state, xact_start,
          now() - xact_start AS transaction_age, wait_event_type, wait_event
   FROM pg_stat_activity
   WHERE datname = current_database()
     AND pid <> pg_backend_pid()
     AND xact_start < now() - interval '30 seconds'
   ORDER BY xact_start;
   ```

2. 使用 [预建 SQL](../../scripts/operations/prebuild-audit-indexes-postgres.sql)。这是 **psql 脚本**，含 `\gexec`，不直接粘贴到普通 GUI SQL 编辑器。通过交互密码或已有安全认证连接；把地址、端口、用户替换成现有 PostgreSQL 配置：

   ```sh
   psql -X -h <PG地址> -p <端口> -U <用户名> -W -d logdb \
     -v ON_ERROR_STOP=1 -f prebuild-audit-indexes-postgres.sql
   ```

   不加 `--single-transaction`，不包在 `BEGIN/COMMIT` 内。脚本先校验已有字段和同名索引，缺少的才创建；7 个索引串行构建。加列锁等待上限 500ms，建索引锁等待上限 2 秒，每条语句执行上限 30 分钟。不要与新版启动迁移同时执行。

   结尾必须看到 7 个目标索引均 `indisvalid=t`、`indisready=t`，且字段顺序正确。完整成功后再安排替换应用。已建好的索引会被新版启动识别并跳过。

3. 任一步失败就停止检查，不取消超时限制后盲目重试。加列遇锁超时可以在长事务结束后重跑脚本；并发建索引失败可能留下 `INVALID` 同名索引。脚本会拒绝该状态，不会被 `IF NOT EXISTS` 静默跳过：

   ```sql
   SELECT c.relname, i.indisvalid, i.indisready, pg_get_indexdef(i.indexrelid)
   FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
   WHERE i.indrelid = 'public.audit_logs'::regclass
   ORDER BY c.relname;
   ```

   确认是本次失败构建遗留、无效且没有其他构建任务后，可单独 `DROP INDEX CONCURRENTLY public.<已确认的无效索引名>`，再重跑。不要对正常有效索引批量删除。当前应用迁移没有完整检查 `indisvalid`，因此不能仅凭索引同名存在就上线。

4. MySQL 令牌索引可单独选择提前建。在 **主库** 检查版本和同名索引：

   ```sql
   SELECT DATABASE(), VERSION();
   SHOW INDEX FROM tokens WHERE Key_name = 'idx_tokens_user_deleted_id';
   ```

   如果没有该索引，执行下面的语句；若已存在，核对字段依次是 `user_id, deleted_at, id`，正确则跳过，不同则停止检查：

   ```sql
   SET SESSION lock_wait_timeout = 1;
   ALTER TABLE tokens
     ADD INDEX idx_tokens_user_deleted_id (user_id, deleted_at, id),
     ALGORITHM=INPLACE, LOCK=NONE;
   SHOW INDEX FROM tokens WHERE Key_name = 'idx_tokens_user_deleted_id';
   ```

   遇到不支持的算法或锁等待超时，不删掉 `LOCK=NONE` 改为默认算法重试。此项收益小，不需要为了它单独安排停服。

索引构建需要为最终索引、临时文件和 WAL 预留磁盘空间，不能只按最终索引大小估算。脚本中的超时设置只作用于当前会话。

## Existing Logic and Related-Path Findings

- `model/audit_log.go:MigrateAuditLogs` 在启动时 AutoMigrate 审计表，之后修复少数已知错误索引。当前索引与上表字段顺序一致。并发预建使用相同名称和结构，避免启动重复建索引。
- 旧版 343 的审计模型没有 `actor_is_employee`；写入显式列列表，新增 nullable 字段后旧版继续正常记录审计。旧版重启实测不会删除该字段或新增索引。
- `tokens` 被 relay 鉴权缓存及批量额度更新使用；在线 DDL 仍有元数据锁窗口。`audit_logs` 管理操作写入与 relay 的 `logs` 写入虽不同表，仍共享 PostgreSQL CPU、磁盘和 WAL，不能据此承诺性能完全不变。
- 产品代码、池容量、Redis key、relay 同步调用与 goroutine 数量均未改变。迁移演练使用隔离环境。

依据：[PostgreSQL CREATE INDEX](https://www.postgresql.org/docs/current/sql-createindex.html)、[PostgreSQL ALTER TABLE](https://www.postgresql.org/docs/current/sql-altertable.html)、[MySQL online DDL operations](https://dev.mysql.com/doc/refman/8.0/en/innodb-online-ddl-operations.html)、[MySQL online DDL performance](https://dev.mysql.com/doc/refman/8.0/en/innodb-online-ddl-performance.html)。并发建索引允许写入，普通加列和在线 DDL 仍有锁窗口；并发索引失败后的有效性必须确认。
