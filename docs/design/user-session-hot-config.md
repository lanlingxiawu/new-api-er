# 用户 Session 策略热更新

日期：2026-08-02

状态：已实施

## 1. 目标与范围

将以下五个仅在启动时读取的环境变量迁移为管理后台可整组保存、无需重启即可生效的 Session 策略：

- `USER_SESSION_ACTIVE_LIMIT`
- `USER_SESSION_ISSUANCE_LIMIT`
- `USER_SESSION_ISSUANCE_WINDOW_SECONDS`
- `USER_SESSION_REVOKED_RETENTION_DAYS`
- `USER_SESSION_HOURLY_ALERT_THRESHOLD`

配置优先级统一为 **数据库 > 环境变量 > 内置默认值**。环境变量继续作为未在后台保存过配置时的启动默认值，已有部署不需要立即迁移配置。

本次不迁移 `SESSION_SECRET`、`SESSION_COOKIE_SECURE` 或 `SESSION_COOKIE_TRUSTED_URL`；这些配置仍是只能重启生效的安全边界配置。本次也不改变 Session 创建、撤销、刷新、缓存和清理算法。

## 2. 用户可见行为

管理后台“系统设置 → 系统调优”新增“登录 Session 策略”区域，复用现有网关限流和数据库连接池配置区的表单、按钮布局、加载状态、保存反馈和整组保存方式。

保存成功后：

- 活跃 Session 上限立即约束之后的 Session 创建；降低上限不会主动撤销现有 Session。
- 签发总量上限和签发窗口立即约束之后的 Session 创建，窗口内已经存在的记录仍参与计数。
- revoked 保留天数由下一轮 Session 清理任务使用；保存请求本身不触发同步清理。
- 每小时告警阈值由下一轮全局签发量检查使用；它只告警，不拒绝登录。

## 3. 配置模型与校验

在 `setting/operation_setting/` 新增 `user_session_setting.go`，注册模块名 `user_session_setting`：

```go
type UserSessionSetting struct {
    ActiveLimit          int `json:"active_limit"`
    IssuanceLimit        int `json:"issuance_limit"`
    IssuanceWindowSec    int `json:"issuance_window_sec"`
    RevokedRetentionDays int `json:"revoked_retention_days"`
    HourlyAlertThreshold int `json:"hourly_alert_threshold"`
}
```

默认值分别为 `50`、`100`、`86400`、`7`、`5000`。所有字段必须为正整数。整组校验额外要求 `issuance_window_sec <= revoked_retention_days * 86400`。

管理后台保存非法组合时直接拒绝并保持旧快照生效，不沿用当前启动逻辑的静默钳制。启动阶段读取历史环境变量时，为保持兼容，仍允许将过大的签发窗口钳制到 revoked 保留期并记录系统错误日志。

| 字段 | 最小值 | 最大值 |
|---|---:|---:|
| `active_limit` | 1 | 10,000 |
| `issuance_limit` | 1 | 100,000 |
| `issuance_window_sec` | 1 | 31,536,000（365 天） |
| `revoked_retention_days` | 1 | 365 |
| `hourly_alert_threshold` | 1 | 10,000,000 |

## 4. 并发与发布模型

沿用现有热配置的草稿/快照模式：

- `UserSessionSetting` 草稿注册进 `config.GlobalConfig`，只由配置加载和保存路径访问。
- `UserSessionSnapshot` 是不可变值，通过 `atomic.Pointer` 发布。
- 登录签发、清理和告警路径每次操作只加载一次快照，并在该次操作内使用同一份值。
- 后台整组保存沿用 `model.configGroupMu` 和配置草稿锁，在事务提交后发布新快照。

不把 getter 返回的草稿指针缓存到请求或后台 goroutine 中。

## 5. 数据流

### 5.1 启动

1. 内置默认值初始化草稿。
2. `ApplyUserSessionEnvDefaults()` 从五个环境变量覆盖草稿并执行兼容性钳制。
3. `model.InitOptionMap()` 从 `options` 表加载已持久化字段，覆盖环境默认值。
4. 配置发布逻辑校验最终草稿并原子发布快照。
5. 后续消费者只读取快照，不再读取 `common.UserSession*` 全局变量。

### 5.2 管理后台保存

1. Root 管理员编辑五个字段。
2. 前端调用现有 `PUT /api/option/group`，提交 `module=user_session_setting` 和完整字段集合。
3. 后端按模块、字段双白名单解析完整草稿并做跨字段校验。
4. 校验通过后，在同一事务中更新 `options` 表的五行配置。
5. 事务提交后发布新快照并返回 `data.applied=true`。
6. 前端刷新 `system-options` 查询并提示保存成功。

若持久化成功但节点本地发布失败，沿用现有协议返回成功且 `data.applied=false`，提示该节点需要重启；不得把未通过校验的草稿发布到请求路径。

### 5.3 消费

- `service.CreateLoginSession` 加载一次快照，执行活跃数与签发数检查。
- Session 清理任务每轮开始时加载一次快照，计算 issuance cutoff 和 revoked cutoff。
- 每小时签发告警检查每轮开始时加载一次快照。

## 6. API 合同与权限

不新增 endpoint，复用现有 Root 权限配置接口：

```http
PUT /api/option/group
Authorization: Bearer <root access token>
Content-Type: application/json

{
  "module": "user_session_setting",
  "values": {
    "active_limit": "50",
    "issuance_limit": "100",
    "issuance_window_sec": "86400",
    "revoked_retention_days": "7",
    "hourly_alert_threshold": "5000"
  }
}
```

成功响应沿用 `{"success":true,"message":"","data":{"applied":true}}`。非法字段、缺失字段、非整数、越界或非法跨字段组合均使用现有 i18n 参数错误响应，不向客户端返回 Go 错误文本。底层错误先通过带请求上下文的 `logger.LogError` 记录。

## 7. 数据模型与数据库兼容性

不新增表、列或迁移。继续使用 `options` 表，每个字段保存为一行：

```text
user_session_setting.active_limit
user_session_setting.issuance_limit
user_session_setting.issuance_window_sec
user_session_setting.revoked_retention_days
user_session_setting.hourly_alert_threshold
```

保存使用现有 GORM 事务和 `SaveConfigGroup` 路径，不增加数据库方言专用 SQL，兼容 SQLite、MySQL 5.7.8+ 和 PostgreSQL 9.6+。

## 8. 错误处理与边界情况

- 管理后台整组提交五个字段；底层兼容既有单键设置入口，单键更新会与当前草稿合并后再做完整跨字段校验。未知字段均拒绝。
- 配置解析或业务校验失败时不写 DB、不发布快照。
- DB 写入失败时回滚事务并保留旧快照。
- 发布失败时 DB 值已保存，响应 `applied=false`；旧快照继续服务，节点重启后加载 DB 值。
- 降低 `active_limit` 不进行批量撤销，避免管理员保存设置时产生大范围登出和额外 DB 写入。
- 缩短 issuance window 会让窗口之外的旧记录立即不再计数，但不会删除记录。
- 缩短 revoked 保留期只影响后续后台清理，不在保存请求中执行删除。
- 多节点部署共享数据库时，本次不引入新的 Redis 广播通道，沿用现有配置同步机制。

## 9. 与现有子系统的交互

- **认证服务**：替换五个 `common.UserSession*` 读取点，不改变查询与错误映射。
- **Session 模型**：不改变 `user_sessions` 表、索引、缓存键或事务。
- **后台清理**：只改变每轮读取的 cutoff 参数，不改变调度频率和删除批次。
- **告警**：只改变阈值，不改变日志内容和检查频率。
- **AI relay、计费、配额、渠道缓存**：无交互。

## 10. 共享资源冲突检查

| 资源 | 本次变化 | AI relay 是否使用 | 隔离结论 |
|---|---|---|---|
| `options` 表 | Root 保存时更新五行 | relay 不按请求读取该表 | 非 relay 路径，无热路径锁竞争 |
| `user_sessions` 表 | 不增加查询或写入，只改变既有阈值/cutoff | Dashboard Access Token 校验可能读取 Session 缓存/回源 | 不加表锁、不改索引、不增加每请求 DB 次数 |
| `userSessionSnapshot` | 新增小型不可变内存快照 | relay 不读取 | 原子读写，无协调锁和持续分配 |
| Redis Session 缓存 | 不改键、不新增操作 | 认证校验可能使用 | 无命名空间冲突 |
| DB/Redis 连接池 | 不新建连接或连接池 | relay 共用现有池 | 调用量不变 |
| goroutine/worker pool | 不新增 goroutine、channel 或 worker | 无 | 不会抢占 relay worker |

## 11. Main Chain Impact

本功能不挂接 AI relay 链，也不在 relay goroutine 上执行新逻辑。AI 请求的中间件、上游调用、流式响应、计费和日志路径均不修改。

唯一相邻处是 Dashboard Bearer Session 的认证校验，但本次五项只控制 Session 创建、保留和告警，不改变既有 Session 有效性缓存读取。在 100k RPM relay 压力下：

- 每个 relay 请求新增 DB/Redis 调用、锁和 goroutine：均为 0。
- 每个新登录 Session：DB 调用数不变，仅新增一次原子快照读取。
- 每轮清理/告警：DB 调用数不变，仅新增一次原子快照读取。

## 12. 前端与 i18n

在现有 `hot-config-sections.tsx` 的通用整组配置区域中增加 `user_session_setting` 模块，复用相同的 `SettingsSection`、三列输入布局和右对齐保存按钮，不修改上游拥有的认证 plumbing 文件。

需要更新：

- `web/src/features/system-settings/types.ts`
- `web/src/features/system-settings/system-tuning/defaults.ts`
- `web/src/features/system-settings/system-tuning/hot-config-sections.tsx`
- `web/src/features/system-settings/system-tuning/section-registry.tsx`
- 七个 `web/src/i18n/locales/{lang}.json`

新增标题及五个字段标签的七语言翻译。错误反馈复用现有文案。数值输入设置与后端一致的 `min`/`max`，保存期间禁用按钮并显示 `Saving...`。

## 13. 测试先行计划

实现前先提交以下测试，再修改生产代码。

### 13.1 setting 单元测试

| 测试 | 技术 | 覆盖内容 |
|---|---|---|
| 默认值与环境变量覆盖 | 等价类 | 无 env、合法 env、非正 env、非数字 env |
| 签发窗口边界 | 边界值 | 等于保留期、少 1 秒、多 1 秒 |
| 每个字段上下界 | 边界值 | min、max、min-1、max+1 |
| 完整组合校验 | 决策/条件覆盖 | 每个字段分别非法及合法组合 |
| 快照一致性 | 并发/竞态 | 并发发布和读取只观察到完整旧值或完整新值 |

### 13.2 model/controller 测试

| 测试 | 技术 | 覆盖内容 |
|---|---|---|
| `SaveConfigGroup` 白名单 | 等价类 | 正确模块、未知模块、未知字段、缺失字段 |
| 原子保存回滚 | 路径覆盖 | DB 失败时无部分行更新且旧快照不变 |
| 发布接线 | 语句覆盖 | 成功保存后 getter 立即返回新快照 |
| Root API 参数错误 | 决策覆盖 | 非整数、越界、窗口超过保留期均返回标准失败形状 |

### 13.3 service/model 行为回归

| 测试 | 技术 | 覆盖内容 |
|---|---|---|
| 活跃上限热更新 | 边界值 | `count=limit-1` 可签发，`count=limit` 拒绝，修改后立即按新值判断 |
| 签发上限热更新 | 边界/路径覆盖 | revoked/expired 但仍在窗口内的记录计数，窗口外记录不计数 |
| 清理参数热更新 | 路径覆盖 | 下一轮使用新 cutoff，不同步删除 |
| 告警阈值热更新 | 边界值 | `count=threshold` 不告警，`threshold+1` 告警 |

### 13.4 验证命令

```text
go test ./setting/operation_setting ./model ./service ./controller
go test ./...
go test -race ./setting/operation_setting ./model ./service
cd web && bun run typecheck
cd web && bun run lint
cd web && bun run build
```

前端当前无测试框架，不新增测试框架。

## 14. 实施文件清单

预计修改：

- `setting/operation_setting/user_session_setting.go`（新增）
- `setting/operation_setting/user_session_setting_test.go`（新增，先写）
- `main.go`（启动默认值应用顺序）
- `common/init.go`、`common/constants.go` 保留旧启动解析和兼容符号；生产消费点已全部迁移到快照，启动时由 setting 模块重新读取环境默认值
- `service/auth_session.go`、`service/auth_cleanup.go`
- `model/user_session.go`
- 当前 `SaveConfigGroup` 模块分发文件及对应测试
- `web/src/features/system-settings/` 下的类型、默认值、section 注册与 UI
- 七个前端 locale JSON
- `.env.example` 与部署示例注释
- 本设计文档（实施后更新状态和实际差异）

明确未修改五个上游拥有的 `web/src/lib/` 认证 plumbing 文件，也未修改 `relay/` 或 relay middleware。

## 15. 实施结果

2026-08-02 完成实现。实际代码沿用现有整组配置保存基础设施，新增 `user_session_setting` 原子快照，并将登录签发、清理和告警消费点迁移为每次操作读取一次快照。管理后台新增独立导航区和七语言字段文案。

验证结果：`go test ./...`、前端 `bun run typecheck`、`bun run build` 通过；全仓库 `bun run lint` 仍被与本次无关的既有 lint 错误阻塞，本次修改的四个前端源文件没有新增 lint 错误。
