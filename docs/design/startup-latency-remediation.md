# 启动耗时治理：主库迁移收敛、一次性回填终结、预热与端口解耦

状态：**待确认**
作者：Claude
日期：2026-07-31

> 前置文档：[startup-log-schema-migration.md](./startup-log-schema-migration.md)
> 已修掉 **日志库** 的 110 秒卡死。本文档处理它遗留的同类问题——
> **主库**（`SQL_DSN`）上完全一样的非幂等迁移，以及另外四类拖慢启动的操作。

---

## 1. 目标与范围

> **范围原则（2026-07-31 用户确认）：这是一次优化，不是改造。**
> 凡是无法证明"零语义变化或严格等价"的改动，一律保持原样、记为已知残留。
> 经三轮收敛，范围为 §5 的六项。

### 目标

1. 主库每次开机重发的 4 条 `ALTER` 里，消掉**可证明安全**的那两条
   （MySQL 4→2、PG 2→1），减少对结算路径热表的排他 MDL。
2. 消除 `user_extensions.extra` 在 `STRICT_TRANS_TABLES` 下**导致进程起不来**的隐患。
3. `InitializeUserAuthVersions` 稳态不再对 `users` 开写事务。
4. 把两项无人依赖的启动前操作移出关键路径。

### 非目标

- 不改任何列的**类型、索引、`not null` 约束、JSON 契约**。
- 不把主库换成幂等闸门（§3.2）。
- **不碰 relay 运行期的任何代码**——无新增中间件、无就绪门、无端口顺序调整（§4.5）。
- 不新增任何功能（周期清理任务、迁移完成标记等一概不做）。
- 不动两个 bool 列的 `default` tag（§3.3）、不动
  `InitializeExternalIdentityClaims`（§8.3）、不动 `sql_mode`（§8.2）。

---

## 2. 现场证据（本地 MySQL 8.0.46 + PostgreSQL，与生产同构）

`migrateDB()` 每次启动对 49 个模型跑 `AutoMigrate`，实测：

| | 条数 | 本地耗时 |
|---|---|---|
| `information_schema.TABLES` / `SCHEMATA` | 313 | 127 ms |
| `HasIndex` | 166 | 78 ms |
| `SELECT * FROM <表> LIMIT 1`（GORM `ColumnTypes`） | 49 | — |
| `information_schema.columns` | 49 | 31 ms |
| **`ALTER TABLE`（每次开机重发）** | **4** | 38 ms |
| 合计 | **845 条** | ~300 ms |

845 由 schema 决定，生产完全一样；变的只有单条 RTT。按 1 ms RTT 算，端口 bind 前先耗掉约 1 秒纯往返。

### 2.1 四条永不收敛的 ALTER

`gorm@v1.25.2` [migrator.go:508](file:///C:/Users/1/go/pkg/mod/gorm.io/gorm@v1.25.2/migrator/migrator.go) 用**裸字符串比对**默认值：

```go
} else if (field.GORMDataType != schema.Time && dv != field.DefaultValue) || ...
```

| 列 | 当前 tag | GORM 期望 | MySQL 实存 |
|---|---|---|---|
| `custom_oauth_providers.enabled` | `default:false` | `false` | `0` |
| `log_export_templates.is_shared` | `index;default:false` | `false` | `0` |
| `channel_cost_configs.cost_ratio` | `not null;default:1.0` | `1.0` | `1` |
| `user_extensions.extra` | `type:text;default:''` | `''` | `NULL` |

### 2.2 危害不是"慢"，是两个可用性风险

**(a) 每次开机一把排他 MDL，且等待无上界。**
实测这几条在 MySQL 8 上是纯元数据操作（10 万行与 50 万行都是 4–7 ms，与行数无关，不重建表）。
但 `ALTER TABLE` 仍要拿排他元数据锁，而生产 `@@lock_wait_timeout = 31536000`（一年）。
这与造成 110 秒卡死的 PG `lock_timeout = 0` 是**同一个结构**：ALTER 一旦排队，该表后续所有读写全部堵在它后面。
`channel_cost_configs` 正在结算路径上被读（[service/employee_commission.go:43](../../service/employee_commission.go#L43) → `GetChannelCostRatio`）。

**(b) `user_extensions.extra` 在严格模式下直接让进程起不来。**

```
Error 1101 (42000): BLOB, TEXT, GEOMETRY or JSON column 'extra' can't have a default value
```

`STRICT_TRANS_TABLES` 是本机 MySQL 8 的**全局默认**。现在能跑，仅仅因为
[.env.example:40](../../.env.example#L40) 的 DSN 里塞了 `sql_mode=NO_ENGINE_SUBSTITUTION`。
任何 DSN 没带这个覆盖的部署：

- 全新安装 → `CREATE TABLE user_extensions` 失败
- 已有库 → `MODIFY COLUMN` 失败

两条路都是 `migrateDB` 返回 error → `InitResources` → `FatalLog` → **进程根本不启动**。
而这个绕法把严格模式对**全应用所有写入**都关掉了，不只是迁移期间。

### 2.3 两个每次开机重跑的一次性回填

- [InitializeExternalIdentityClaims](../../model/external_identity_claim.go#L89)：捞出所有
  `telegram_id <> ''` 的用户，然后在**单个事务里对每个用户发 3 条语句**
  （`Create` + 2 次 `First`），每次启动，永远跑。
  5000 个绑定用户 ≈ 15,000 次串行往返，压在一个长写事务里。
- [InitializeUserAuthVersions](../../model/user_auth_cache.go#L241)：
  `UPDATE users SET auth_version=1 WHERE auth_version IS NULL OR auth_version < 1`。
  `auth_version` 无索引 → 每次开机对 `users` 全表扫描 + 写事务。

### 2.4 挡在 bind 前面的预热与目录扫描

`ListenAndServe` 是 [main.go:216](../../main.go#L216) 才执行的，前面还压着：

- [main.go:96](../../main.go#L96) `InitChannelCache()` — `Find(全部渠道)`（含 key 大字段）+ `Find(全部 abilities)`
- [main.go:104](../../main.go#L104) `GetPricing()` — `abilities ⋈ channels` + 全部 models + 全部 vendors
- [main.go:367](../../main.go#L367) `CleanupOldCacheFiles()` — `ReadDir` + 逐文件 `Stat` + `Remove`，
  同步、无上界，而且它是磁盘缓存清理的**唯一**调用方（没有周期任务），
  崩溃后缓存目录堆积时会直接堵住启动。

这正好构成前置文档 §7.3 描述的自持循环：启动慢 → 健康检查超时 → 被踢 → 再启动慢。

---

## 3. 方案选型（已用实验排除错误选项）

对每种 tag 写法，在 MySQL / MySQL 严格模式 / PostgreSQL 上各跑两遍 `AutoMigrate`，
看**第二遍**是否零 DDL：

| tag 写法 | MySQL | MySQL 严格 | PostgreSQL |
|---|---|---|---|
| `default:false`（bool，现状） | ✗ 每次 ALTER | ✗ | ✓ |
| `default:0`（bool） | ✓ | ✓ | ✗ 每次 2 条（含 `TYPE ... USING`） |
| **无 default tag（bool）** | **✓** | **✓** | **✓** |
| `default:false;-:migration` | ✓ | ✓ | ✓ |
| `not null;default:1.0`（float，现状） | ✗ | ✗ | ✗ |
| `not null;default:1`（float） | ✓ | ✓ | ✗ 每次 `SET DEFAULT` |
| **`not null`（float，无 default）** | **✓** | **✓** | **✓** |
| `type:text;default:''`（现状） | ✗ | **建表失败** | ✗ |
| **`type:text`（无 default）** | **✓** | **✓** | **✓** |

### 3.1 已排除：`-:migration`

表面上三库全收敛，实际是**陷阱**。实测全新建表时该列**根本不会被创建**：

```
MySQL       fresh CreateTable -> keep=true flag=false ratio=false
MySQL       INSERT with the ignored fields -> Error 1054: Unknown column 'flag' in 'field list'
PostgreSQL  fresh CreateTable -> keep=true flag=false ratio=false
PostgreSQL  INSERT -> 错误: 关系 "zz_ig_test" 的 "flag" 字段不存在 (SQLSTATE 42703)
```

"收敛"只是因为列不存在所以没什么可迁移的。全新部署会直接坏掉。**不采用。**

### 3.2 已排除：主库也上幂等闸门

把 `migrateLogTable` 泛化到 49 个模型技术上可行，但代价是**彻底失去自动列迁移**：
今后每次给任何模型加一个列类型变更，都要手写迁移。
`logs` 表值得付这个代价（4 亿行、生产热表、schema 极稳定），
主库这 49 个模型不值得——它们仍在活跃演进。**不采用。**

### 3.3 已排除：一刀切"三列全摘 default"

从矩阵看，"不写 `default`"是唯一能让三库同时收敛的写法。
但它对**两个 bool 列**代价过高：PG 已有库摘掉 default 后
`AlterColumn` 只发 `TYPE ... USING`、从不发 `DROP DEFAULT`，
差异永不收敛（实测 `_local/upgradepath`），必须再配一个一次性
`DROP DEFAULT` 补偿迁移——那是新增迁移代码，属于改造而非优化。

### 3.4 采用：只动两列，各取代价最小的写法

| 列 | 写法 | 为什么是这个 |
|---|---|---|
| `user_extensions.extra` | **摘掉 default** | MySQL 的 TEXT 本就存不下默认值，摘掉后三库全收敛，零语义变化，且顺带修掉严格模式建表失败 |
| `channel_cost_configs.cost_ratio` | **保留 default，`1.0` → `1`** | 只改字面量即可让 MySQL 收敛；PG 侧与现状同为一条 `SET DEFAULT`，不变差；**零值语义完整保留** |
| 两个 bool 列 | **不动** | MySQL 与 PG 互斥，任何改法都要么让一边变差、要么需要补偿迁移 |

安全性依据（`_local/zerowrite` / `_local/conservative` 实测，详见 §4.1.1）：
既有行逐字段不变，零值 `Create` 落库结果与现状一致，`not null` 约束保留。

---

## 4. 改动方案

### 4.1 只改两个可证明零语义变化的 tag

四条重发 ALTER 里，只有两条存在"既修好 MySQL、又不让 PG 变差、且零语义变化"的写法：

| 文件 | 现状 | 改为 | 依据 |
|---|---|---|---|
| [model/user_extension.go:24](../../model/user_extension.go#L24) | `gorm:"type:text;default:''"` | `gorm:"type:text"` | MySQL 的 TEXT 列本就存不下默认值；实测两种写法落库都是 `""` |
| [model/employee.go:44](../../model/employee.go#L44) | `gorm:"not null;default:1.0"` | `gorm:"not null;default:1"` | **保留 default**，只改字面量。零值 `Create` 仍落库 `1`；PG 侧与现状同为一条 `SET DEFAULT`，不变差 |

**不改**（§3 矩阵：MySQL 与 PG 互斥，无安全解）：

| 文件 | 现状 | 为何不动 |
|---|---|---|
| [model/custom_oauth_provider.go:45](../../model/custom_oauth_provider.go#L45) | `gorm:"default:false"` | 改 `default:0` → MySQL 收敛但 PG 多发 `TYPE ... USING`；摘掉 default → PG 已有库永不收敛（`AlterColumn` 只发 `TYPE`、从不发 `DROP DEFAULT`），需配套补偿迁移，超出本次范围 |
| [model/log_export_template.go:27](../../model/log_export_template.go#L27) | `gorm:"index;default:false"` | 同上 |

两处改动都要留一条**终结性注释**（feedback_doc_comment_current_state）：

```go
// cost_ratio: default 必须写 `1` 而不是 `1.0`。GORM v1.25.2 用裸字符串比对
// 默认值（migrator.go:508），MySQL 的 information_schema 返回 "1"，
// 写 "1.0" 会让 AutoMigrate 每次开机重发一条 ALTER TABLE，
// 在这张结算路径要读的表上取排他 MDL，而 lock_wait_timeout 默认一年。
```

```go
// extra: 不要加 default。MySQL 的 TEXT 列在 STRICT_TRANS_TABLES 下
// 根本不允许默认值（Error 1101），建表直接失败、进程起不来；
// 非严格模式下 MySQL 静默忽略该默认值，于是 GORM 每次开机都判定
// "期望 '' 实际 NULL" 并重发一条 ALTER TABLE。
// 零值 "" 由 Go 侧写入，行为与加 default 时完全一致（实测）。
```

### 4.1.1 行为等价性实测：为什么这两处是零语义变化

GORM 对**带 `default` tag 且值为零值**的字段会在 `INSERT` 里**跳过该列**，让 DB 填默认值。
这意味着"摘掉 default"可能改变零值的落库结果。实测四列在各写法下的实际落库值
（`_local/zerowrite` + `_local/conservative`，`Create(&T{Uid:n})` 其余字段全零值）：

| 列 | 现状实存 | 本次改后实存 | Go 侧读回 | 结论 |
|---|---|---|---|---|
| `extra` (text) | `""` | `""` | 一致 | **零变化** |
| `cost_ratio` (float) | `1` | `1` | 一致 | **零变化**（保留 default，仅改字面量） |
| `enabled` (bool) | `0` | 不动 | — | 不适用 |
| `is_shared` (bool) | `0` | 不动 | — | 不适用 |

`cost_ratio` 之所以选择 `default:1` 而不是摘掉 default，正是为了保住这一列。
摘掉的话 `Create(&ChannelCostConfig{CostRatio: 0})` 会从落库 `1.0` 变成 `0`
（虽然全项目并不存在这条路径——唯一写入是
[model/employee.go:466](../../model/employee.go#L466) 显式带 `cost_ratio` 的
`Create(map[...])` 与同函数的 `Updates(map[...])`，
且无任何原生 `INSERT INTO` / `Select(...).Create` / `Omit(...).Create`），
但既然有零成本的等价写法，就不必让调用方去承担这个假设。

既有数据不受影响：`_local/conservative` 里用现状 tag 写入的行，
`AutoMigrate` 到新 tag 后逐字段重读完全一致
（`{Uid:1 Extra:x Ratio:1 Enabled:false IsShared:false}`）。

### 4.2 升级路径（实测）

现有库改 tag 后的实际表现（`_local/conservative`，表由现状 tag 建成、含数据）：

| | MySQL | PostgreSQL |
|---|---|---|
| 现状每次开机 | 4 条 ALTER | 2 条 ALTER |
| 改后第 1／2／3 次开机 | **2 条**（稳定，只剩两个 bool） | **1 条**（稳定，只剩 `cost_ratio` 的 `SET DEFAULT`，与现状同） |
| 既有行 | `{Uid:1 Extra:x Ratio:1 Enabled:false IsShared:false}` 逐字段不变 | 同左 |
| 零值 `Create` 后 `ratio` | `1`（与现状一致） | `1`（与现状一致） |

**没有一次性补偿迁移**：因为两个 bool 列保持原样，PG 侧不会被引入
`TYPE ... USING`，也就不需要 `DROP DEFAULT` 补偿。
（若将来要动 bool 列，必须配套补偿——`postgres@v1.5.2` 的 `AlterColumn`
只发 `TYPE ... USING`、从不发 `DROP DEFAULT`，仅改 tag 永不收敛，
实测见 `_local/upgradepath` 与 `_local/pgdropdef`。）

### 4.3 `InitializeUserAuthVersions` 先判存在再更新

**不能用 `Limit(1).Count()`** —— 实测它生成 `SELECT count(*) ... LIMIT 1`，
LIMIT 作用在聚合结果那一行上，扫描一行不少（`n=5`，表里就是 5 行）：
**不能用 `Limit(1).Count()`** —— 实测它生成 `SELECT count(*) ... LIMIT 1`，
LIMIT 作用在聚合结果那一行上，扫描一行不少（`n=5`，表里就是 5 行）：

```go
// 错：SELECT count(*) FROM users WHERE ... LIMIT 1   —— 仍是全表 count
// 对：SELECT id       FROM users WHERE ... LIMIT 1   —— 命中第一行即停
var probe []int
if err := DB.Model(&User{}).Where("auth_version IS NULL OR auth_version < ?", 1).
    Limit(1).Pluck("id", &probe).Error; err != nil { return err }
if len(probe) == 0 { return nil }
return DB.Model(&User{}).Where("auth_version IS NULL OR auth_version < ?", 1).
    Update("auth_version", 1).Error
```

稳态下这是一次索引外的顺序扫描，但**命中即停**，且不再开写事务、不再取行锁。
不为 `auth_version` 加索引：为每次启动省一条查询而付全表写放大不划算（Rule 8.3 反向）。

### 4.3.1 "能不能整体挪到启动完成后" —— 逐项依赖判定

对 `InitResources()` + `main()` 里每个同步操作，判断"是否有东西依赖它在端口 bind 前完成"。

**可以挪（改成 goroutine，逻辑一行不动）**

| 项 | 判定依据 |
|---|---|
| [main.go:367](../../main.go#L367) `CleanupOldCacheFiles()` | 清的是上次进程遗留的缓存残片，全项目无消费者 |
| [main.go:104](../../main.go#L104) `GetPricing()` | **自带懒加载兜底**（`len(pricingMap)==0` → `updatePricing()`）。消费者全在管理/展示 API：`controller/model_meta.go`、`controller/pricing.go`、`service/rankings.go`、`GetModelEnableGroups`、`GetModelQuotaTypes` —— **无一在 relay 路径**。不预热最坏是首个访问定价的请求承担一次重建 |

**不能挪（含一个易误判的陷阱）**

| 项 | 硬依赖 |
|---|---|
| **`InitializeUserAuthVersions`** | **看着像纯回填，实为鉴权前置条件。** [middleware/auth.go:137](../../middleware/auth.go#L137) `identity.UserAuthVersion <= 0` → 鉴权失败；[service/auth_session.go:61](../../service/auth_session.go#L61) `user.AuthVersion <= 0` → 登录失败。挪走等于制造一个"所有老用户被踢"的窗口 |
| `migrateDB` / `migrateLogTable` | schema 必须先于任何 DB 访问就绪。**这是 845 条语句的大头，挪不动** |
| `InitChannelCache` | `GetRandomSatisfiedChannel` 直读 `group2model2channels`，**无兜底重建**，nil 时 relay 全部退化为"无可用渠道" |
| `InitializeExternalIdentityClaims` | telegram 登录绑定，窗口期内行为异常 |
| `MigrateRetiredFrontendOptions` | 必须在 `InitOptionMap` **之前**，否则 OptionMap 读到已废弃的旧 key |
| `InitOptionMap` / `authz.Init` / `CheckSetup` / `InitRedisClient` / `i18n.Init` | 全局配置与鉴权，API 与 relay 均依赖 |

**结论**：启动耗时的主体（`AutoMigrate`）是 schema 就绪的前提，无法后移；
可后移的两项中 `GetPricing` 随模型目录规模增长，`CleanupOldCacheFiles`
正常时接近零、崩溃后能救命。二者都属于"纯挪位置"，符合本次"优化而非改造"的边界。

### 4.4 `CleanupOldCacheFiles` 与 `GetPricing` 改异步

两处都改为带 `recover` 的 goroutine，被调函数**一行不改**：

- [main.go:367](../../main.go#L367) `CleanupOldCacheFiles()` — 清的是上次进程遗留的残片。
- [main.go:104](../../main.go#L104) `GetPricing()` — 仅为预热；`GetPricing` 自身的
  懒加载兜底保证挪走后功能不变（§4.3.1）。必须仍排在 `InitChannelCache()` **之后**
  派发，保持原注释描述的顺序意图（Advanced Custom 路由配置先于定价构建可读）。

> **不做**：给磁盘缓存清理补周期任务。那是新增功能，不在本次"优化而非改造"的边界内。
> 现状仍是"仅启动时清一次 + [controller/performance.go:160](../../controller/performance.go#L160)
> 手动入口"，记为已知残留。

### 4.5 ~~端口先 bind + 就绪门~~ —— **本次不做**

曾计划加 `common.ServiceReady atomic.Bool`，把 `ListenAndServe` 提前，
预热窗口内让 relay 返回 503。**已否决**，理由：

它改变的是**运行期对外行为**——把"端口未开、连接被拒"换成"端口已开、HTTP 503"。
上游若有依赖连接失败（而非状态码）的重试策略，行为会变，且无法静态验证。
本次范围是优化，不是改造。

代价：`InitChannelCache`（不可后移，见 §4.3.1）仍排在 bind 之前，
§2.4 的"启动慢 → 健康检查超时 → 被踢"循环没有从根上断开，
只是被前面几项优化削弱。编排侧调大 `start_period` / `initialDelaySeconds` 仍然需要。

### 4.6 删除 `migrateDBFast`

[model/main.go:342](../../model/main.go#L342) 全项目无调用者（已确认），
且是同一个 `AutoMigrate` 陷阱的第二份拷贝。
前置文档 §5 改动点 #4 已标记删除但未执行，本次一并删掉。

---

## 5. 改动点清单

范围已收敛为「只做可证明安全的优化」，不含任何行为改造。

| # | 文件 | 改动 | 性质 |
|---|---|---|---|
| 1 | [model/user_extension.go:24](../../model/user_extension.go#L24) | `type:text;default:''` → `type:text` + 终结注释 | 零语义变化（§4.1.1 实测两边都存 `""`）；顺带消除严格模式建表失败 |
| 2 | [model/employee.go:44](../../model/employee.go#L44) | `not null;default:1.0` → `not null;default:1` + 终结注释 | 零语义变化（零值 `Create` 仍存 `1`） |
| 3 | [model/user_auth_cache.go:241](../../model/user_auth_cache.go#L241) | `Limit(1).Pluck` 先判存在再 UPDATE | 严格等价（UPDATE 0 行 ≡ 不 UPDATE） |
| 4 | [main.go:104](../../main.go#L104) | `GetPricing()` 改 goroutine + recover | 纯挪位置，被调函数不改；有懒加载兜底 |
| 5 | [main.go:367](../../main.go#L367) | `CleanupOldCacheFiles()` 改 goroutine + recover | 纯挪位置，被调函数不改 |
| 6 | [model/main.go:342](../../model/main.go#L342) | 删除 `migrateDBFast` | 死代码 |

**效果**（实测，`_local/conservative`）：

| | MySQL（本项目主库） | PostgreSQL |
|---|---|---|
| 现状每次开机 ALTER | 4 条 | 2 条 |
| 改后每次开机 ALTER | **2 条** | **1 条** |

**明确不改**：

- 任何列的类型、索引、`not null` 约束、JSON 契约。
- **`custom_oauth_providers.enabled` / `log_export_templates.is_shared` 两个 bool 保持原样。**
  `default:false` 在 MySQL 上每次发 ALTER，改成 `default:0` 会让 PG 变差
  （多发 `TYPE ... USING`，见 §3 矩阵），两边互斥且无安全解 —— 记为已知残留。
- **`InitializeExternalIdentityClaims` 保持原样**（见 §8.3）。
- **`ServiceReady` 就绪门与端口提前 bind**（见 §4.5）。
- **磁盘缓存清理的周期任务**（新增功能，见 §4.4）。
- **`.env` / `.env.example` 里的 `sql_mode=NO_ENGINE_SUBSTITUTION`**（见 §8.2）。

> 工作区里 `.env.example` 有一处未消解的 `<<<<<<< Updated upstream` 冲突标记。
> 那是本方案之外的既有问题，不在本次改动范围内。

---

## 6. 测试方案（Rule 15，测试先行）

真实 MySQL（`SQL_DSN`）+ 真实 PG（`LOG_SQL_DSN`），行级清理，不 truncate（Rule 15.5）。

| 用例 | 技术 | 断言 | 对应改动 |
|---|---|---|---|
| `TestAutoMigrateConvergesForFixedColumns` | 会退回归 | **核心回归**：对 `migrateDB` 的完整模型列表连跑两遍，第二遍**不得**出现针对 `user_extensions.extra` / `channel_cost_configs.cost_ratio` 的 `ALTER`。两个 bool 列的 ALTER 是已知残留，白名单放行 | #1 #2 |
| `TestUserExtensionCreatesUnderStrictMode` | 边界（错误路径） | `SET SESSION sql_mode='STRICT_TRANS_TABLES'` 下建表 + 插入成功（今天会 1101 失败） | #1 |
| `TestChannelCostRatioDefaultUnchanged` | 等价性（回归） | `Create(&ChannelCostConfig{})` 零值仍落库为 `1.0`；`UpsertChannelCostConfig` 显式值正确写入 | #2 |
| `TestZeroValuesRoundTripAfterTagChange` | 等价类 | `extra=""` 写入读回一致；既有行迁移后逐字段不变 | #1 #2 |
| `TestUserAuthVersions_NoUpdateWhenComplete` | 判定覆盖 | 全部为 1 时不发 `UPDATE`；探测语句是 `SELECT id ... LIMIT 1` 而非 `count(*)`；有遗留行时仍正确回填 | #3 |
| `TestPricingLazyLoadWithoutWarmup` | 路径覆盖 | 不调用预热，直接 `GetPricing()` 返回完整结果（证明 #4 挪走后功能不变） | #4 |

**不写的测试**：`TestServiceReadyGate`、`TestDropRetiredColumnDefaults_Idempotent`、
`TestExternalIdentityBackfill_*`、`TestModelsHaveNoUnconvergeableDefaults`
—— 对应改动已全部移出本次范围。

完成后按 Rule 15.8 跑 `go test ./...` 全量。

---

## 7. Main Chain Impact（Rule 0）

**同步 / 异步**

- 六项改动**全部发生在进程启动阶段**，relay 运行期**零新增代码、零新增中间件**。
- 净效果是**减少**主链路阻塞：今天每次启动那 4 条 ALTER 会对
  `user_extensions` / `channel_cost_configs` / `custom_oauth_providers` /
  `log_export_templates` 取排他 MDL（`@@lock_wait_timeout = 31536000`，无上界等待）。
  改后前两张表一条不发；后两张 bool 表是已知残留，维持现状、不变差。

**共享资源审计**

| 资源 | relay 是否也用 | 冲突与处置 |
|---|---|---|
| `channel_cost_configs` | **是**，结算读（`GetChannelCostRatio`，有缓存） | 核心冲突点。改后启动不再对它下 DDL，缓存未命中回源时不会再撞上排他 MDL |
| `user_extensions` | **是**，业绩缓冲批量写（`business_stats_buffer`） | 同上，改后零 DDL；顺带消除严格模式下的启动失败 |
| `custom_oauth_providers` / `log_export_templates` | 否 | 仅管理面。**本次不动**，每次启动仍各发一条元数据 ALTER（实测 4–7 ms，与行数无关） |
| `users` | **是**，鉴权读 | #3 把每次开机的全表 `UPDATE` 降级为 `LIMIT 1` 只读探测，**减少**锁竞争 |
| 磁盘缓存目录 | **是**，relay body 落盘 | #5 把扫描移出启动关键路径；清理逻辑一行不改，仍按 `maxAge` 保护在用文件 |
| 定价缓存（`pricingMap` 等） | 否，消费者全在管理/展示 API | #4 改为异步预热；`GetPricing` 自带 `updatePricingLock`，并发安全，懒加载兜底 |
| Redis / 其它内存结构 / goroutine 池 | — | 本次不新增任何 Redis key、常驻内存结构或 goroutine 池；#4 #5 各多一个一次性 goroutine |

**并发分析**

- 启动期：主库 DDL 从 4 条降到 2 条（MySQL）／2 条降到 1 条（PG）；
  `users` 的全表写事务降级为只读探测；两项预热移出关键路径。
- **稳态 QPS 影响为 0**：无新增中间件、无新增 DB 调用、无新增 Redis 调用、无新增锁、无新增周期任务。
  100k RPM 下的行为与今天逐字节一致。

---

## 8. 已决定事项

范围经三轮收敛，全部待确认项已关闭：

1. ~~§4.5 就绪门 + 端口提前 bind~~ —— **不做**。改变运行期对外行为（连接被拒 → HTTP 503），
   上游重试策略可能依赖前者，无法静态验证。本次是优化不是改造。
2. ~~去掉 `sql_mode=NO_ENGINE_SUBSTITUTION`~~ —— **不做**（详见 §8.2）。
3. ~~磁盘缓存清理补周期任务~~ —— **不做**，属新增功能。
4. ~~两个 bool 列的 default tag~~ —— **不改**。MySQL 与 PG 互斥、无安全解（§3 矩阵），记为已知残留。
5. ~~`InitializeExternalIdentityClaims` 反连接改写~~ —— **不做**（详见 §8.3）。

**本次剩余范围 = §5 的六项，全部可证明零语义变化或严格等价。**

---

## 8.3 `InitializeExternalIdentityClaims`：本次保留，及正确写法备查

**决定（2026-07-31，用户确认）：本次不动。**

它每次开机对每个 Telegram 绑定用户发 3 条语句、压在单个事务里，是启动开销里
仅次于 `AutoMigrate` 的一项。不动的理由：任何改写都触及**迁移的冲突检测语义**，
而该语义是原代码显式声明的（注释：「Existing duplicate ownership fails migration
rather than preserving an ambiguous login identity」），不属于"优化"。

**若将来要做，唯一严格等价的写法**（连接条件必须三项全匹配才跳过）：

```sql
SELECT u.id, u.telegram_id
FROM users u
LEFT JOIN external_identity_claims c
  ON c.provider = 'telegram'
 AND c.user_id  = u.id
 AND c.subject  = u.telegram_id
WHERE u.telegram_id <> '' AND c.user_id IS NULL
```

只有「user_id 与 subject 都对上」才排除——那种情况原逻辑走完 3 条查询后确实是纯 no-op。
其余全部进入原循环，冲突检测一字不动。

两个已实测的错误写法，留作后人警示（`_local/equivalence` §3）：

| 连接条件 | 结果 |
|---|---|
| `c.subject = u.telegram_id` | **静默跳过冲突用户**。数据 user1=tg-a(已认领)/user3=tg-a(冲突) 时只返回 `[2]`，user3 的冲突永远发现不了 |
| `c.user_id = u.id`（仅此一项） | 跳过"改了 telegram 绑定但 claim 表仍是旧 subject"的用户 —— 原逻辑会报 `AlreadyClaimed` 暴露不一致，这里会静默放过 |

---

## 8.2 `sql_mode=NO_ENGINE_SUBSTITUTION`：本次保留，理由与后续

**决定（2026-07-31，用户确认）：本次改动不动 DSN 里的 `sql_mode`。**

### 机制澄清

`NO_ENGINE_SUBSTITUTION` 这个标志本身与严格模式无关（它管的是"指定存储引擎不可用时
报错还是静默替换"）。真正关掉严格模式的是**赋值动作**——DSN 里写
`sql_mode=NO_ENGINE_SUBSTITUTION` 会把 session 的 sql_mode **整体覆盖**成这一个值，
`STRICT_TRANS_TABLES` 连同全局其余标志一起被挤掉：

```
@@global.sql_mode   ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION
@@session.sql_mode  NO_ENGINE_SUBSTITUTION
```

### 为什么单独拆出去

摘掉它会让严格模式对**全应用所有写入**生效，可能暴露其它一直被静默截断/强转的写入
（超长字符串、非法日期、除零）。这类问题静态查不出来，只能靠预发跑全量测试加真实流量观察。
那是一个与"启动耗时"无关的独立风险面，混进本次改动会让回滚粒度变粗。

### 本次改动仍然消除了那个隐患

关键点：**改动点 #4（`type:text;default:''` → `type:text`）本身是无条件的净赢，
与 sql_mode 摘不摘无关。** 全新安装跑完整 51 个模型实测，严格模式下唯一的失败项就是它：

```
sql_mode 来自 .env（NO_ENGINE_SUBSTITUTION）：all 51 models created OK
STRICT_TRANS_TABLES（MySQL 8 出厂默认）：1/51 FAILED
    *model.UserExtension
    Error 1101 (42000): BLOB, TEXT, GEOMETRY or JSON column 'extra' can't have a default value
```

所以 #4 落地之后，"严格模式下起不来"这个隐患就**已经消失**了，
只是我们暂时不去依赖这一点、也不去掉兜底。这反而是更好的顺序：
将来真要摘 sql_mode 时，schema 侧的雷已经排完，只剩运行期写入这一类风险要验证。

`TestUserExtensionCreatesUnderStrictMode`（§6）**保留**——
它在测试里显式 `SET SESSION sql_mode='STRICT_TRANS_TABLES'`，
不依赖生产配置，用来锁住 #4 确实修对了。

### 历史成因（终结记录）

这个绕法来自 `4fbbc8efe`（2026-06-08，引入 `user_extensions` 的那次提交），
当时就撞上了 1101，用全局关严格模式绕了过去。#4 修好后它失去存在理由，
但摘除动作留给后续独立改动。

---

## 9. 附：复现用的本地探针

全部在 `_local/`（gitignored，Go 工具链跳过下划线目录），可直接 `go run` 复现本文所有数字：

| 目录 | 作用 |
|---|---|
| `_local/startupprobe` | 主库 AutoMigrate 两遍的语句分类统计 + 各回填/预热耗时 |
| `_local/coldump` | 四个问题列的实际 `column_default` |
| `_local/alterbench` | 重复 ALTER 在 10 万 / 50 万行下的耗时（证明不重建表） |
| `_local/strictcheck` | 严格模式下 TEXT 默认值的 1101 报错 |
| `_local/tagmatrix` | §3 的 tag 写法 × 三库收敛矩阵 |
| `_local/ignoremig` | 证明 `-:migration` 不建列（排除该方案） |
| `_local/upgradepath` | 改 tag 后的升级路径：MySQL 收敛 / PG 不收敛 |
| `_local/pgdropdef` | PG 补一次 `DROP DEFAULT` 后收敛 |
| `_local/strictfresh` | 严格模式下全新安装跑完整 51 个模型，定位唯一失败项（§8.2） |
| `_local/equivalence` | 行为等价性三连：既有数据完整性 / `Limit(1).Count` 是否真带 LIMIT / 反连接条件对冲突检测的影响 |
| `_local/zerowrite` | 摘 default 后零值实际落库值对照（§4.1.1 那张表） |
