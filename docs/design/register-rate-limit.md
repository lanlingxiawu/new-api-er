# 注册次数限制：同一 IP 在 W 秒内最多注册成功 N 次

状态：**已实现**
作者：Claude
日期：2026-09-12

> **实现记录**
>
> - **第一版（2026-09-12）**：同一 IP 注册成功后 N 秒内不能再注册（每窗口 1 次，只能调间隔）。
>   §8 的四个待确认项均按推荐方案确认：默认 120 秒、按 IP、保留 `CriticalRateLimit`、OAuth 建号不纳入。
>   已部署到香港测试服务器。
> - **第二版（2026-09-12）**：按用户反馈"次数也需要可以调整"，改为**同一 IP 在任意 W 秒内最多注册成功 N 次**，
>   N、W 都可在后台配置，默认 1 次 / 120 秒（与第一版行为一致）。计数从"单个占位"改为滑动窗口名额（§4.1）。
>   配置键 `register_cooldown_enabled` / `register_cooldown_sec` 保持不变，只新增 `register_cooldown_num`，
>   测试服务器上已保存过的值无需迁移。
> - **审计优化（2026-09-14）**：预占名额与成功计时拆开，数据库插入成功后才从成功时刻开始 W 秒窗口；
>   内存降级每次只清理当前 IP，不再扫描全部 IP，也不引入后台清理任务。
> - 核心逻辑在 [service/register_cooldown.go](../../service/register_cooldown.go) 的 `ClaimRegisterCooldown`；
>   [controller/user.go](../../controller/user.go) 的 `Register` 在 `cleanUser.Insert` 之前调用，插入失败时归还名额。
> - Redis 完成操作用 `context.WithoutCancel(ctx)`：客户端断开不会让确认或归还跟着被取消。
> - **controller 层没有端到端测试**：仓库里没有能跑 `Register` 的测试环境，按 §7 的约定不为此新搭 DB 桩，
>   以 service 层测试为准（Redis / 内存两套存储各跑一遍，含滑动窗口、并发恰好放行 N 个）。

---

## 1. 需求

- 注册单独做一个限制，不和登录共用。
- **只有注册成功才算一次**；用户名重复、验证码错误等失败不计。
- 同一 IP 在一段时间内最多注册成功若干次，**次数和时间都在后台可配置**。

## 2. 现状

`POST /api/user/register` 原本只挂了 `CriticalRateLimit()`（[router/api-router.go:90](../../router/api-router.go#L90)），
它按**请求次数**计数（成功、失败都算），且与登录、找回密码、OAuth 入口共用同一个 IP 计数器
（`rateLimit:v2:ip:CT:<ip>`）。它做不到"只算成功"，也没法单独给注册设限。

限流中间件跑在 handler **之前**，看不到注册结果，所以"只算成功"必须放在注册业务逻辑里实现。

## 3. 目标与非目标

**目标**

1. 同一客户端 IP 在任意 W 秒内最多注册成功 N 次；超出时拒绝，并提示最早还要等多少秒。
2. 注册失败不占用名额。
3. 开关、N、W 在后台「网关限流」页配置，保存后立即生效。

**非目标**

- **`CriticalRateLimit` 保持不动。** 它按请求次数兜底，防止有人反复刷验证码、撞用户名；
  新的限制只管"成功次数"，两者互补。
- **OAuth / 微信 / Telegram 首次登录自动建号不纳入。** 只限制 `/api/user/register` 这一条账号密码注册接口。
- 不改前端注册表单：它已经用 toast 显示后端返回的 `message`
  （[sign-up-form.tsx:177](../../web/src/features/auth/sign-up/components/sign-up-form.tsx#L177)）。

## 4. 方案

### 4.1 核心：预占名额，写库后确认（滑动窗口）

"检查 → 注册 → 成功后计数"这种写法有并发漏洞：同一 IP 同时发多个注册请求，全部都能通过检查。
所以改用**原子预占名额，写库后确认结果**：

```
controller.Register
  … 现有校验全部通过（开关、参数、验证码、用户名/邮箱是否已存在）
  → 预占名额：Redis ZSET register:cooldown:ip:<ip>，member = 随机 token
      先删掉已到期的名额；剩余名额数 < N 才加入，否则拒绝
      拒绝 → 返回"注册过于频繁，请在 X 秒后重试"（X = 最早到期的名额还剩多少秒，向上取整、至少 1）
      占到 ↓
  → cleanUser.Insert(...)
      插入失败 → 完成(false)，删除自己的 token → 返回原错误    ← 失败不计
      插入成功 → 完成(true)，到期时间改为成功时间 + W          ← 成功算一次
  → 生成默认令牌等后续步骤（不影响名额：账号已经建出来了，就算一次）
```

- **占名额时机放在所有校验之后、写库之前**：参数错误、验证码错误、用户名已存在都在占名额之前就返回，完全不碰计数。
- 预占名额使用 120 秒保护期（若 W 更长则取 W），足够覆盖正常的数据库写入；进程崩溃或确认失败时，名额到保护期结束自行释放。
- **完成只操作自己的 token**：失败时删除，成功时从成功时刻重新计算窗口，不会误改别人的名额。
- **为什么是滑动窗口而不是固定窗口**：固定窗口在两个窗口交界处能连续放行 2N 次；每个名额按自己的到期时间
  逐个空出，"任意 W 秒内最多 N 次"才严格成立。N 上限 1000，单个 IP 的 ZSET 大小可控。
- **当前时间由服务端传给脚本**（毫秒）。多实例间的时钟偏差（通常毫秒级）只会让窗口边界差这么多，可接受；
  好处是 Redis 分支和内存分支用同一个时钟，测试可以精确控制时间。

Redis 预占脚本（原子执行）：

```lua
-- KEYS[1]: key；ARGV: token, now(ms), window(ms), limit, reservation(ms)
local now = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if redis.call('ZCARD', KEYS[1]) < tonumber(ARGV[4]) then
  local reservation = tonumber(ARGV[5])
  redis.call('ZADD', KEYS[1], now + reservation, ARGV[1])
  redis.call('PEXPIRE', KEYS[1], math.max(reservation, redis.call('PTTL', KEYS[1])))
  return {1, 0}
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
return {0, tonumber(oldest[2]) - now}
```

完成脚本按 token 原子处理：成功时用 `ZADD XX` 把 score 更新为“完成时间 + W”，失败时 `ZREM`；随后按最晚成员刷新 key TTL。

### 4.2 Redis 不可用时

与现有限流同一套取舍：**降级到进程内存，不放行也不报错**。

- 内存结构：`map[ip][]{token, expiresAt}` + 互斥锁，每次占名额只清理当前 IP 的到期名额，不在请求路径扫描全部 IP。
  其它 IP 的过期条目会保留到该 IP 再次注册或进程重启；这是无 Redis 降级路径的简单取舍。
- 降级期间每个实例各自计数（M 个实例 = 同一 IP 每窗口最多 M×N 次），与限流降级的行为一致。
- Redis 调用复用网关限流的超时预算 `redis_timeout_ms`（默认 100ms），超时即降级。
- 没开 Redis 的部署（`common.RedisEnabled == false`）直接走内存。

### 4.3 配置（Rule 12）

加在 `RateLimitSetting`（[rate_limit_setting.go](../../setting/operation_setting/rate_limit_setting.go)），
出现在同一个「网关限流」页里：

| 字段 | 默认 | 取值范围 | env（启动默认值） |
|---|---|---|---|
| `register_cooldown_enabled` | true | — | `REGISTER_COOLDOWN_ENABLE` |
| `register_cooldown_num` | **1** | 1 ~ 1000 | `REGISTER_COOLDOWN_NUM` |
| `register_cooldown_sec` | **120** | 1 ~ 86400（1 天） | `REGISTER_COOLDOWN_SEC` |

- `RateLimitSnapshot` 增加 `RegisterCooldown time.Duration` 与 `RegisterCooldownNum int`，关闭时两者都为 0；
  注册时从原子快照读取，不加锁。
- `ValidateRateLimitSetting` 分别校验次数与窗口范围。窗口**不受 1200 秒上限约束**（那是计数桶的窗口上限）。
- **改配置的生效方式**：新的 N 立即作用于下一次判断（名额已满时调大 N 会立刻放行）；新的 W 只作用于之后新占的名额，
  已占的名额按原到期时间空出。关闭开关后立即不再检查，残留的 key 自然过期。
- **升级兼容**：老库没有这几行 option，`LoadFromDB` 保留默认值，升级后按 1 次 / 120 秒生效，无需迁移。

### 4.4 提示文案（Rule 13）

后端 i18n key `user.register_too_frequent`，通过 `common.ApiErrorI18n` 返回
（HTTP 200、`success: false`，和 Register 里其它错误一致）：

| 语言 | 文案 |
|---|---|
| en | `Registrations from your network are too frequent. Please try again in {{.Seconds}} seconds.` |
| zh-CN | `当前网络注册过于频繁，请在 {{.Seconds}} 秒后再试。` |
| zh-TW | `目前網路註冊過於頻繁，請在 {{.Seconds}} 秒後再試。` |

### 4.5 后台配置页

`web/src/features/system-settings/system-tuning/`：`rateLimitFields` 在 `critical_duration_sec` 之后按
「开关 / 次数 / 窗口」顺序加三项，与页面上其它限流行的布局一致；`defaults.ts`、`../types.ts` 同步。

| key（en） | zh | zh-TW |
|---|---|---|
| `Registration limit enabled` | 启用注册次数限制 | 啟用註冊次數限制 |
| `Registrations allowed per IP` | 同一 IP 注册次数 | 同一 IP 註冊次數 |
| `Registration window (seconds)` | 注册统计窗口（秒） | 註冊統計窗口（秒） |

fr / ru / ja / vi 按同样含义翻译，逐文件直接加，不跑 `i18n:sync`。

保存走现有的 `SaveConfigGroup("rate_limit_setting", ...)`，不新增接口；三个字段已加入
[model/config_group.go](../../model/config_group.go) 的 `rateLimitFields` 与
[service/settingsaccess/scopes.go](../../service/settingsaccess/scopes.go) 的两个字段列表。

## 5. 边界情况

| 场景 | 行为 |
|---|---|
| 同一 IP 并发多个注册请求 | 恰好 N 个能占到名额，其余立即被拒并提示剩余秒数 |
| 注册失败（用户名已存在、验证码错误、参数错误） | 在占名额之前就返回，不计数 |
| 写库失败（插入报错、邮箱并发冲突） | 归还名额，不计数 |
| 用户已建出来，但默认令牌生成失败 | 计数（账号已存在） |
| 公司 / 学校 NAT，多人共用一个出口 IP | 同一出口每个窗口共享 N 个名额。**这是按 IP 限制的必然代价**，集中开户时调大 N 或关闭 |
| 客户端 IP 识别 | 用 `c.ClientIP()`，依赖现有可信代理配置，和所有 IP 限流一致。前面有 CDN 而未还原真实 IP 时，按 CDN 节点 IP 计 |
| 进程在占名额后、确认前崩溃 | 名额保留到保护期结束（120 秒，W 更长则取 W），可接受 |
| 写库成功但确认失败（Redis 超时等） | 同上，名额按保护期到期；只会偏严，不会多放账号 |
| 写库耗时超过保护期 | 名额已先过期，成功后不再补记，这次注册不计数；此时数据库通常已超时，可接受 |

## 6. 改动清单

| # | 文件 | 改动 |
|---|---|---|
| 1 | `setting/operation_setting/rate_limit_setting.go` | 3 个字段、默认值、快照、校验、env |
| 2 | `service/register_cooldown.go`（新） | `ClaimRegisterCooldown(ctx, ip)` → `(finish func(success bool), retryAfterSec int64, ok bool)`；Redis 脚本 + 内存降级 |
| 3 | `controller/user.go` `Register` | 在 `cleanUser.Insert` 前预占；插入失败归还，成功后从成功时刻开始窗口 |
| 4 | `i18n/keys.go` + `i18n/locales/{en,zh-CN,zh-TW}.yaml` | `user.register_too_frequent` |
| 5 | `model/config_group.go` | `rateLimitFields` 加 3 个字段 |
| 6 | `service/settingsaccess/scopes.go` | 两个字段列表各加 3 个字段 |
| 7 | 前端 3 个文件 + 7 个 locale | 见 §4.5 |
| 8 | `.env.example` | 补三个变量 |
| 9 | `docs/design/env-hot-config-migration.md` §8.1 | 表格补三行 |

## 7. 测试方案（Rule 15）

**`service/register_cooldown_test.go`**（miniredis + 内存分支，共用可拨动时钟）

| 用例 | 技术 | 断言 |
|---|---|---|
| 同 IP 放行 N 次、第 N+1 次被拒 | 边界值 + 等价类 | N=2 时前两次成功，第三次被拒且提示 120 秒；另一个 IP 不受影响 |
| N=1 | 边界值 | 第二次被拒 |
| 滑动窗口 | 路径覆盖 | N=2、W=120：0s、60s 各占一个；121s 第一个到期可再注册一次；紧接着被拒且提示 59 秒 |
| Redis key 与 TTL | 语句覆盖 | key 为 `register:cooldown:ip:<ip>`，ZSET 1 个成员，TTL = 窗口 |
| 失败完成恰好空出一个 | 判定覆盖 | N=2 占满后完成(false) → 可再占一个 → 再下一个被拒。**"失败不计"的回归测试** |
| 成功时间开始窗口 | 路径覆盖 | 预占后等待 30 秒才成功，窗口从成功时刻重新计算 |
| 预占保护期 | 边界值 | W=1 秒时，执行中的注册超过 1 秒仍占用名额 |
| 窗口边界 | 边界值 | W=1：999ms 时被拒且提示 1 秒；1000ms 时放行 |
| 关闭开关 | 判定覆盖 | 连续占都成功，不创建任何 key |
| Redis 断开时降级内存 | 错误路径 | 占到、再次被拒、归还后又可占，全程无 panic、无 500 |
| 并发恰好放行 N 个 | 路径覆盖 | N=3、20 个并发请求 → 恰好 3 个成功 |
| 内存分支清理 | 路径覆盖 | 只清理当前 IP，不扫描其它 IP |

**`setting/operation_setting/rate_limit_setting_test.go`**：快照（开启 / 关闭 / 回落默认 / 钳制上界）、
校验（次数拒绝 0 / 1001、接受 1 / 1000；窗口拒绝 0 / 86401、接受 1 / 1201 / 86400）、env 读取。

**`model/config_group_test.go`**：白名单字段数 → 26；`SaveConfigGroup` 保存次数 3、窗口 300 后落库、快照生效；
保存 `0` 被拒绝且内存不变。

## 8. 已确认项

1. 默认 1 次 / 120 秒；次数和窗口都可在后台调整。
2. 按 IP 限制；办公室 / 学校共用出口时需要调大次数或临时关闭。
3. `关键操作限流` 继续保留在注册接口上，与新的成功次数限制叠加生效。
4. OAuth / 微信 / Telegram 首次登录自动建号不受这个限制。

## 9. Main Chain Impact（Rule 0）

**同步 / 异步**：只改 `/api/user/register` 的 handler，**不触碰 `/v1` relay 链路的任何中间件或 handler**。
注册请求新增 2 次 Redis 调用（预占 + 成功确认或失败归还），各有超时预算；注册量级极低。

**共享资源审计**

| 资源 | relay 是否也用 | 冲突与处置 |
|---|---|---|
| Redis key `register:cooldown:ip:*`（新增，ZSET） | 否 | 独立前缀，不与 relay 的 `rateLimit:*`、也不与网关限流的 `rateLimit:v2:*` 重叠；单 key 最多 N（≤1000）个成员 |
| Redis 连接池 | 是 | 只在注册时使用，每次最多 2 次调用、带超时预算，不构成池争用 |
| 进程内存 map（降级用） | 否 | 新建独立结构，不复用 `InMemoryRateLimiter`；条目数 = 窗口内注册成功的名额数 |
| `RateLimitSnapshot`（原子指针） | 否 | 多两个值字段，读路径仍是一次原子 Load |
| DB `users` 表 | 否（relay 读的是缓存） | 写入逻辑不变，只是写之前多了一次 Redis 占名额 |
| DB `options` 表 | 否 | 只在后台保存时写 3 行 |

**并发分析**：relay 侧每请求的 DB / Redis / 锁 / goroutine 成本变化均为 0。
