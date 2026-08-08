# 后台 429：`/api` 全组按 IP 限流，同出口 IP 的用户互相挤兑

状态：**已实现并验证**
作者：Claude
日期：2026-07-30

> **实际实现（2026-07-30）**
>
> - `/api` 入口保留按 IP 的粗粒度桶，默认提高为 2000/180s。
> - `AdminAuth` / `UserAuth` 鉴权成功后，仅当 `RouteTag == "api"` 时追加
>   按用户 ID 的 `GAU` 桶，默认 360/180s；不会进入 relay 路径。
> - 匿名请求只受 IP 桶保护；用户桶可通过
>   `GLOBAL_API_USER_RATE_LIMIT_ENABLE=false` 独立回滚。
> - 429 保持 HTTP 状态码与 `Retry-After`，并返回本地化 JSON message。
>
> **第二轮（2026-07-31）：拆分 `CriticalRateLimit`**
>
> 第一轮只动了 `/api` 组的 `GA` 桶，登录链路不经过它，因此"同网段员工无法登录"
> 没有被修复。第二轮把原来共用一个 `CT` 桶的 35+ 条路由拆成四个互相隔离的桶，
> 并把 `CRITICAL_RATE_LIMIT` 默认值从 20 提到 60。详见 §4.4。

> **本次要解决的现场问题**
> 管理后台日志列表页弹出 `Request failed with status code 429`，
> 请求是 `/api/log?startTime=1785340800000&endTime=1785390694739&page=1`。
> 现象特征：**同一个网段（同一 NAT 出口）下的用户会互相把对方顶掉**，
> 单个用户独自使用时不容易复现。
>
> 与 [startup-log-schema-migration.md](startup-log-schema-migration.md) 是同一天的两起独立故障，
> 因果链不相交，分开成文。

---

## 1. 先排除掉的两个方向

### 1.1 不是"合并把可信代理功能删了"

`cffb9bd10`（"ip限流添加信任ip取代理ip, 其他ip使用真实ip"）引入的能力**仍然在**，
经 `8aa5e754a` 重构后落在 [middleware/trusted_proxies.go:13-21](../../middleware/trusted_proxies.go#L13-L21)：

```go
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8", "::1",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
	"199.15.79.186/32", // 部署所在反代公网地址
}
```

生产**未设置** `TRUSTED_PROXIES`，因此走 [trusted_proxies.go:25-28](../../middleware/trusted_proxies.go#L25-L28)
的内置默认列表分支，反代地址在列 → Gin 会解析 `X-Forwarded-For` → **真实客户端 IP 拿得到**。
现场日志里出现 `123.112.242.79`、`84.32.220.177` 这类公网客户端 IP，也印证了这一点。

> ⚠️ 反过来是个坑，要写进 `.env.example`：`TRUSTED_PROXIES` 一旦被设置就会**整体覆盖**
> 内置默认列表。仓库里开发用的 `.env` 写的是 `TRUSTED_PROXIES=127.0.0.1,::1`，
> 这个值如果被误同步到生产，`199.15.79.186` 就会掉出信任列表，
> `c.ClientIP()` 会对**所有**经反代进来的请求返回反代自己的 IP，
> 全站用户共用一个限流桶 —— 那才是真正的灾难级故障。见 §6 改动点 #5。

### 1.2 不是中转链路（`/v1`）的限流

| 路径 | 中间件 | 限流键 | 默认 |
|---|---|---|---|
| `/v1/*` 中转 | `ModelRequestRateLimit` [relay-router.go:75](../../router/relay-router.go#L75) | **用户 ID** [model-rate-limit.go:81](../../middleware/model-rate-limit.go#L81) | `ModelRequestRateLimitEnabled` 默认关 |
| `/api/*` 后台 | `GlobalAPIRateLimit` [api-router.go:24](../../router/api-router.go#L24) | **`c.ClientIP()`** [rate-limit.go:112](../../middleware/rate-limit.go#L112) | **默认开**，360 次 / 180 秒 |

`/v1` 按用户 ID 限流，与网段无关。现场日志（12:27–12:49 三个切片）里的 **3,099 条 429
全部是 `POST /v1/messages`**，且内容是 `All providers are saturated; retry shortly`
—— 这句话**在本仓库的 Go 代码里搜不到**，是渠道 #324 的上游原样透传的，不是网关发的。
那是另一个独立问题（上游容量），不在本文档范围。

---

## 2. 根因

`/api` 组在 [api-router.go:24](../../router/api-router.go#L24) 顶层套了 `GlobalAPIRateLimit()`：

```go
apiRouter := router.Group("/api")
apiRouter.Use(middleware.RouteTag("api"))
apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
apiRouter.Use(middleware.BodyStorageCleanup())
apiRouter.Use(middleware.GlobalAPIRateLimit())   // <- 这里
```

限流键是 `c.ClientIP()`（[rate-limit.go:44-46, 112](../../middleware/rate-limit.go#L44-L46)），
额度 **360 次 / 180 秒**（[common/init.go:121-123](../../common/init.go#L121-L123)，
`GLOBAL_API_RATE_LIMIT_ENABLE` 默认 `true`）。折算 **2 req/s，且是整个出口 IP 共享**。

`/api/log` 这条路由只有 `AdminAuth()`（[api-router.go:403-404](../../router/api-router.go#L403-L404)），
**没有任何按用户维度的限流器**。于是：

> 同一个 NAT 出口下的 N 个管理员/员工，共用一个 2 req/s 的桶。
> 人越多、越先到的人越容易被后到的人挤掉。这就是"同网段用户互相 429"。

`SetDashboardRouter` 的 `/dashboard/billing/*` 同样挂了 `GlobalAPIRateLimit`
（[dashboard.go:14](../../router/dashboard.go#L14)），共享同一个 `GA` 命名空间的桶。

### 2.1 为什么报错文案是英文原始串而不是中文提示

[middleware/rate-limit.go:139-145](../../middleware/rate-limit.go#L139-L145)：

```go
func writeRateLimited(c *gin.Context, retryAfterSeconds int64) {
	if retryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	}
	c.Status(http.StatusTooManyRequests)   // 只有状态码
	c.Abort()                              // 响应体是空的
}
```

前端 [web/src/lib/http-client.ts:132-138](../../web/src/lib/http-client.ts#L132-L138)：

```ts
const message = messageKey
  ? t(messageKey)
  : error?.response?.data?.message || error?.message || t('Request failed')
toast.error(message)
```

body 为空 → `response.data.message` 是 `undefined` → 回落到 axios 自带的
`error.message`，也就是截图里的 `Request failed with status code 429`。

**这一点同时是个诊断锚**：本仓库其他所有 429（导出频控、台账忙、下载并发）
都带 JSON `message` 会显示成中文；**只有 `middleware/rate-limit.go` 这一族返回空 body**。
所以从"文案是英文原始串"就能反推限流器就是它。

> 约束：`http-client.ts` 属于 CLAUDE.md Rule 6 的**上游所有文件**，必须与 `main` 逐字节一致，
> **不能改前端**。文案修复只能由后端补 body 完成。

### 2.2 生产版本还在加倍消耗配额

生产跑的是 `609ac3f0b`，落后 HEAD 若干提交。该版本的日志路由只注册了带尾斜杠的一条
（`git show 609ac3f0b:router/api-router.go` 第 395 行 `logRoute.GET("/", ...)`）：

- `GET /api/log` → Gin `RedirectTrailingSlash` 回 **301** → 浏览器再请求 `/api/log/`
  → **一次列表加载过两遍限流器**。
- 叠加"全部日志类型"时 react-query 重复 key 造成的重复查询轮次。
- 且当时单次查询要 6~8 秒，用户会反复刷新。

这三条在 HEAD 上已分别由 `07cd0b89b`（消 301 + 重复轮次）和 `7ce2056f3`（整点缓存，
6~8s → ~100ms）修掉，但**尚未部署**。所以"先升级"很可能就是性价比最高的一步。

### 2.3 同族的另外两个 IP 桶（一并记录，避免下次重查）

| 限流器 | 挂载点 | 额度 | 影响面 |
|---|---|---|---|
| `CriticalRateLimit` | 登录/注册/改密/支付等逐条路由 | **20 次 / 20 分钟** per IP | 同出口 IP 下**多人先后登录会被拒**，且同样是空 body |
| `GlobalWebRateLimit` | [web-router.go:26](../../router/web-router.go#L26) | 120 次 / 180 秒 per IP | 只作用于**在它之后注册**的路由（[router/main.go:16-26](../../router/main.go#L16-L26) 的注册顺序决定），即静态资源与 SPA fallback；`/api`、`/v1` 在它之前注册，不受影响 |

`CriticalRateLimit` 的 20/20min 在 NAT 场景下比 `/api` 的 360/180s 更容易踩，
只是触发面窄（只有登录这类动作），**需要一并纳入方案**。

`GlobalWebRateLimit` 的 120/180s 同样偏紧：一次 SPA 冷加载就要消耗整个前端的资源
请求数，同出口 IP 下几个人同时冷加载即可打满，表现为**白屏/资源加载失败**而不是接口
429，更难归因。缓解因素是 [middleware/cache.go:12](../../middleware/cache.go#L12) 给带 hash
的资源发 `max-age=604800`（一周），正常只有冷加载与发版后才回源。默认值已放宽，见 §4.5。

---

## 3. 目标与非目标

**目标**

1. 已登录用户的后台操作，配额按**用户**计算，不再被同出口 IP 的其他用户挤掉。
2. 匿名接口（登录、注册、改密、`/api/status` 等）仍保留 IP 维度防护，强度不下降。
3. 被限流时前端能显示**可操作的中文提示**（含剩余等待秒数），而不是 axios 原始英文串。
4. 不改任何 Rule 6 的上游所有前端文件。

**非目标**

- 不处理 `/v1/messages` 那 3,099 条上游 429（渠道 #324 容量问题，另案）。
- 不改 `ModelRequestRateLimit`（按用户维度，本来就没这个问题）。
- 不引入新的限流算法；沿用 [rate-limit.go:22-36](../../middleware/rate-limit.go#L22-L36)
  已有的固定窗口 Lua 脚本（其注释明确要求不要换成滑动窗口 ZSET，除非有意变更外部可见行为）。

---

## 4. 方案

### 4.1 分三步走，可独立发布

| 步骤 | 内容 | 风险 | 见效 |
|---|---|---|---|
| **S1** | 把 HEAD 部署上去（`07cd0b89b` + `7ce2056f3`） | 无（已有改动） | 请求量砍一半以上，查询快 60 倍，可能直接不再触发 |
| **S2** | 后端补 429 响应体 + i18n 文案 | 极低，纯新增 | 用户看得懂，不再误报为系统故障 |
| **S3** | `/api` 限流改为"IP 桶 + 用户桶"两段式 | 中，动中间件顺序 | 根治同网段挤兑 |

### 4.2 S2：补响应体

`writeRateLimited` 改为返回统一错误体，沿用 Rule 9 的 `{"success": false, "message": ...}` 形状：

```go
func writeRateLimited(c *gin.Context, retryAfterSeconds int64) {
	if retryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"success": false,
		"message": i18n.T(c, "rate_limit.too_many_requests",
			map[string]any{"Seconds": retryAfterSeconds}),
	})
}
```

- `i18n.T` 的签名是 `T(c *gin.Context, key string, args ...map[string]any) string`
  （[i18n/i18n.go:98](../../i18n/i18n.go#L98)），占位符走 go-i18n 的具名模板而非 `%d`。
- 文案按 Rule 13 同步 `i18n/locales/en.json` 与 `zh.json`，
  内容形如「请求过于频繁，请在 {{.Seconds}} 秒后重试」/
  「Too many requests. Please retry in {{.Seconds}} seconds.」。
- 不能用 `common.ApiErrorI18n`：它固定返回 HTTP 200（Rule 9），
  而限流必须保持 429 —— [auth-session.ts:256](../../web/src/lib/auth-session.ts#L256)
  正是靠 `status === 429` 判定"这次刷新失败是被限流、不要清掉登录态"，
  改成 200 会让被限流的 token 刷新被误判为凭据失效而踢出登录。
- 前端不改：`http-client.ts` 已经会优先取 `response.data.message`（§2.1），补上 body 就自动生效。
- `Retry-After` 头保留（Redis 分支拿得到真实 TTL，内存分支用整窗长度做保守上界，
  这是 [rate-limit.go:135-138](../../middleware/rate-limit.go#L135-L138) 现有注释已说明的行为）。

### 4.3 S3：`/api` 改两段式限流

现状的结构性障碍：`GlobalAPIRateLimit` 跑在 `/api` **组顶层**，
而 `AdminAuth()` / `UserAuth()` 是**逐路由**挂的，执行更晚 —— 组中间件里拿不到
`c.GetInt("id")`，所以没法直接换成现成的 `userRateLimitFactory`
（[rate-limit.go:192](../../middleware/rate-limit.go#L192)）。

方案：拆成两个中间件，职责分明。

```
第一段  ApiIPRateLimit()      —— 仍在 /api 组顶层，按 IP
        额度放宽到"足够一个出口 IP 承载 N 个正常用户"，只做粗粒度抗滥用。
        默认建议 2000 次 / 180 秒（原 360 的约 5.5 倍）。

第二段  ApiUserRateLimit()    —— 挂在 auth 之后
        按用户 ID，额度收紧到"单个用户的合理上限"，默认 360 次 / 180 秒
        （即把今天的语义原封不动地平移到用户维度）。
        c.GetInt("id") == 0（匿名）时直接放行 —— 匿名请求已被第一段管住。
```

挂载方式二选一，需要拍板（§8 待确认项 1）：

- **A. 改造 `AdminAuth()` / `UserAuth()`，在鉴权成功后内联调用第二段。**
  改动集中在 [middleware/auth.go](../../middleware/auth.go)，路由文件零改动，
  不会漏挂；代价是把限流职责塞进了鉴权中间件。
- **B. 新增一个组级中间件放在 `/api` 组，内部延后到 `c.Next()` 之后再判断。**
  不可行 —— 限流必须在 handler 执行**前**决定放不放行。
- **C. 逐路由显式追加 `middleware.ApiUserRateLimit()`。**
  语义最清晰，但 `api-router.go` 有 200+ 条路由，**一定会漏挂**，不推荐。

倾向 **A**。

### 4.4 `CriticalRateLimit` 拆桶（已实现）

#### 4.4.1 真正的根因：一个桶装了四类流量

`CriticalRateLimit()` 原本用单一 mark `"CT"` + `c.ClientIP()`，额度 20 次 / 20 分钟，
被 35+ 条路由共用。其中混进了**已登录用户的日常行为**：

| 路由 | 频率 |
|---|---|
| `/api/user/auth/refresh` | access token TTL = 15 分钟（[service/auth_token.go:18](../../service/auth_token.go#L18)），每个常驻页签在 20 分钟窗口内消耗 1~2 次 |
| `/api/user/auth/logout` | 每次登出 |
| `/api/ratio_config`、`/api/usage/token`、`/api/log/token` | 公开只读 / API Key 轮询，可被脚本打满 |
| 支付、改资料、查看密钥、OAuth 绑定 | 用户主动操作 |

于是同一 NAT 出口下约 10~15 个**在线**员工，光 token 刷新就能吃光 20 次配额，
后到的人**一次登录都做不成**——且当时 429 还是空 body，前端只显示 axios 原始英文串。

#### 4.4.2 拆成四个桶

沿用已有的固定窗口 Lua 脚本，只换 key 的维度和 mark：

| mark | 覆盖 | key 维度 | 额度 |
|---|---|---|---|
| `CT` | 匿名登录面：登录 / 2FA / passkey 登录 / 注册 / 找回密码 / OAuth 入口 | 客户端 IP | `CRITICAL_RATE_LIMIT`（默认 20 → **60**） |
| `CTU` | 已登录用户的敏感操作：支付、订阅下单、改资料、查看 token/渠道密钥、OAuth 绑定 | **用户 ID**（`c.GetInt("id")`）；无身份时回落到同 mark 的 IP 桶 | 同 `CRITICAL_RATE_LIMIT`，但已是**每人独立** |
| `CTS` | `/api/user/auth/refresh`、`/api/user/auth/logout` | **登录会话 ID**；无可用 cookie 时回落到同 mark 的 IP 桶 | `AUTH_REFRESH_RATE_LIMIT`（60）+ IP 兜底 `AUTH_REFRESH_RATE_LIMIT_IP`（600） |
| `PQ` | 公开只读查询：`ratio_config`、`usage/token`、`log/token` | 客户端 IP | 同 `CRITICAL_RATE_LIMIT` |

关键点：**登录面（`CT`）从此只被登录类流量消耗**。在线用户再多、脚本轮询再频繁，
都不会再把办公室锁在门外。

#### 4.4.3 `CTS` 为什么能按会话限流

`/auth/refresh` 跑在鉴权之前，拿不到用户 ID。但 refresh cookie 的格式是
`<sid>.<secret>`，[service/auth_session.go:361-370](../../service/auth_session.go#L361-L370)
的 `splitRefreshToken` 会校验 `sid` 是合法 UUID。`sid` 在 token 轮换时**保持不变**，
因此它是一个稳定的、每个浏览器会话唯一的 key，且已被校验过格式、可安全拼进缓存 key。

**带可用会话的请求只计入自己的会话桶，不再额外付一份按 IP 的份额。**
任何按 IP 的配额都会被同出口的其他人抽干——那正是本次拆桶要消除的共命运模式。
一次发版让所有页签同时刷新，就是整片掉线，与原故障同类。所以
`AUTH_REFRESH_RATE_LIMIT_IP` **只作用于拿不到 sid 的请求**（无 cookie / 格式非法）。

伪造 cookie 刷出大量 UUID 撑大 key 空间的问题，由**上游**兜住而不是这里：
这两条路由挂在 `/api` 组下，`GlobalAPIRateLimit`（2000 次 / 180 秒 per IP）
已经限死了单个客户端每窗口能发多少请求，也就限死了它能创建多少个会话计数器。

#### 4.4.4 暴力破解防护未被削弱

`CT` 从 20 放宽到 60 只影响**匿名登录面**这一个桶。放宽的同时，原本挤在这个桶里的
刷新 / 轮询 / 支付流量全部被移走了，因此**留给真实登录尝试的可用配额虽然名义上从 20
提到 60，实际被非登录流量占用的部分降为 0**——净效果是登录更可用，而不是暴破窗口
线性放大 3 倍。按账号维度的防护（`AUTH_SESSION_ISSUANCE_LIMIT`、登录失败计数）
不在本次改动范围内，行为不变。

#### 4.4.5 `GW` 静态资源桶（已实现）

`GlobalWebRateLimit` 的默认额度从 **120 → 2000 次 / 180 秒**，维度不变（仍按客户端 IP）。

不拆桶的理由：`GW` 之后注册的路由只有内嵌静态资源与 SPA fallback，是**同一类流量**，
不存在 §4.4.1 那种"一类流量吃光另一类配额"的问题。这里唯一的错误是**额度按单人而不是
按出口 IP 人数估算**——120 次比 `/api` 的 2000 次紧 16 倍，而静态文件（内嵌 FS + gzip）
比任何 API 请求都便宜得多，配比明显反了。

> 已知的残留共享：[web-router.go:29-34](../../router/web-router.go#L29-L34) 的 `NoRoute`
> 同时承担 `/api`、`/v1`、`/assets` 的 404（`RelayNotFound`），这部分与静态资源共用 `GW` 桶。
> 正常流量下 404 量极低；若将来出现 SDK 配置错误反复打不存在的 `/v1` 路径打空该桶，
> 再按 §4.4.2 的方式拆一个 404 专用 mark。**本次未拆。**

#### 4.4.6 mark 唯一性

两个限流器共用一个 mark 就是共用一个计数器——本次故障的成因。所有 mark 已收敛为
[middleware/rate-limit.go](../../middleware/rate-limit.go) 的常量，且**所有调用点都传常量**。

约束由 `TestEveryRateLimiterUsesItsOwnCounter` 保证：它构造全部 11 个限流器，用同一个
客户端（同 IP、同用户 ID、同会话 cookie）各发一次请求，断言每个限流器都新建了一个
**此前没被别的限流器占用过的** Redis 计数器。

> 这里**不能**改用"把常量列进一张表再断言互不重复"的写法。那种测试校验的是表本身，
> 与实际传参无关——调用点写死字面量时，表照样唯一、测试照样绿。本次审计正是抓到了
> 这个：常量已定义，但 7 处调用点仍传字面量，而列表式测试全程通过。

#### 4.4.7 Redis 不可用时降级而不是失败

原实现在 Redis 报错时 `c.Status(500); c.Abort()`。这条路径的影响面比看上去大：
`GlobalAPIRateLimit` 挂在整个 `/api` 组，`applyAPIUserRateLimit` 又内联在
`authHelper` 里，**一次 Redis 抖动会让每个已鉴权的后台请求都 500**。

现在统一走 [rate-limit.go](../../middleware/rate-limit.go) 的 `takeRateLimit`：
Redis 报错时记 `LogError` 并**降级到进程内存计数器**，而不是失败请求，也不是直接放行。
降级期间限流仍然生效，只是配额变成"每实例一份"（N 个实例即 N 倍配额）——
这是故障期间正确的取舍。回归测试 `TestRedisFailureDegradesToInMemoryInsteadOfFailingRequests`
同时断言"请求被服务"和"配额仍被计数"。

### 4.5 Redis key 命名空间

沿用 [rate-limit.go:15](../../middleware/rate-limit.go#L15) 的 `rateLimit:v2` 前缀，
新增的用户桶用已有的 `redisUserRateLimitKey(mark, userID)`
（`rateLimit:v2:user:<mark>:<id>`），mark 取 `"GAU"`。
**不与 relay 主链路共享任何 key**：relay 侧的限流走
`rateLimit:MRRL*` / `rateLimit:<userId>`（[model-rate-limit.go:86,100](../../middleware/model-rate-limit.go#L86)），
命名空间不重叠。详见 §9。

---

## 5. 配置项

| 变量 | 现值 | 建议新值 | 说明 |
|---|---|---|---|
| `GLOBAL_API_RATE_LIMIT` | 360 | 2000 | 第一段 IP 桶，粗粒度抗滥用 |
| `GLOBAL_API_RATE_LIMIT_DURATION` | 180 | 180 | 不变 |
| `GLOBAL_API_USER_RATE_LIMIT` | — | 360 | **新增**，第二段用户桶 |
| `GLOBAL_API_USER_RATE_LIMIT_DURATION` | — | 180 | **新增** |
| `GLOBAL_API_USER_RATE_LIMIT_ENABLE` | — | true | **新增**，可整段关闭以便回滚 |
| `CRITICAL_RATE_LIMIT` | 20 | **60** | §4.4，现在同时是 `CT` / `CTU` / `PQ` 三桶的额度 |
| `CRITICAL_RATE_LIMIT_DURATION` | 1200 | 1200 | 不变 |
| `AUTH_REFRESH_RATE_LIMIT_ENABLE` | — | true | **新增**，置 false 可整段关闭 `CTS` 桶回滚 |
| `AUTH_REFRESH_RATE_LIMIT` | — | 60 | **新增**，每个登录会话的刷新配额 |
| `AUTH_REFRESH_RATE_LIMIT_IP` | — | 600 | **新增**，`CTS` 的每 IP 兜底，见 §4.4.3 |
| `AUTH_REFRESH_RATE_LIMIT_DURATION` | — | 1200 | **新增** |
| `GLOBAL_WEB_RATE_LIMIT` | 120 | **2000** | §4.4.5，静态资源与 SPA fallback |
| `GLOBAL_WEB_RATE_LIMIT_DURATION` | 180 | 180 | 不变 |

均为部署侧基础设施配置，与既有限流变量同类，按 Rule 12 的例外走 env，不进 `setting/`。

> 限流中间件由 `rateLimitFactory` 在**路由注册时**构造，`*_ENABLE` 与 mark 在启动时固化。
> 调整任何限流环境变量后**必须重启进程**才生效。

---

## 6. 改动点

| # | 文件 | 改动 |
|---|---|---|
| 1 | [middleware/rate-limit.go:139-145](../../middleware/rate-limit.go#L139-L145) | `writeRateLimited` 改为 `AbortWithStatusJSON`，带 i18n message（S2） |
| 2 | `i18n/locales/en.json` / `zh.json` | 新增 `rate_limit.too_many_requests`（Rule 13 两语言同步） |
| 3 | [middleware/rate-limit.go](../../middleware/rate-limit.go) | 新增 `ApiUserRateLimit()`，复用已有的 `userRateLimitFactory`；匿名（`id == 0`）放行而非 401 —— 与现有 `userRateLimitFactory` 的 401 行为不同，需单独实现（S3） |
| 4 | [middleware/auth.go](../../middleware/auth.go) | `AdminAuth()` / `UserAuth()` 鉴权通过后调用第二段（方案 A，S3） |
| 5 | `.env.example` | 补 §5 三个新变量；**并在 `TRUSTED_PROXIES` 处补一句警告**：设置该变量会整体覆盖内置默认列表（含反代公网地址），生产若经反代请务必把反代地址一并写入，否则 IP 限流会把所有用户折叠成一个桶（§1.1） |
| 6 | [common/init.go:121-131](../../common/init.go#L121-L131) | 读取新增环境变量 |
| 7 | `middleware/rate_limit_test.go` | 见 §7 |

**第二轮（§4.4 拆桶）的改动点：**

| # | 文件 | 改动 |
|---|---|---|
| 8 | [middleware/rate-limit.go](../../middleware/rate-limit.go) | 新增 mark 常量 `CT`/`CTU`/`CTS`/`PQ`；新增 `redisSessionRateLimitKey`、`takeRateLimit`、`requestLoginSessionID`；新增 `UserCriticalRateLimit()`、`SessionCriticalRateLimit()`、`PublicQueryRateLimit()` |
| 9 | [common/constants.go](../../common/constants.go) / [common/init.go](../../common/init.go) | `CriticalRateLimitNum` 默认 20 → 60；`GlobalWebRateLimitNum` 默认 120 → 2000；新增 `AuthRefreshRateLimit*` 四个变量 |
| 10 | [router/api-router.go](../../router/api-router.go) | 按 §4.4.2 的表逐条改挂；`auth/refresh`、`auth/logout` 改 `SessionCriticalRateLimit()` |
| 11 | [router/channel-router.go:25](../../router/channel-router.go#L25) | `/channel/:id/key` 改 `UserCriticalRateLimit()`（`RootAuth` 已在前） |
| 12 | `.env.example` | 补 §5 四个新变量；`TRUSTED_PROXIES` 处补 §1.1 的折叠告警 |

**明确不改**：`web/src/lib/http-client.ts`（Rule 6 上游所有文件，必须与 `main` 逐字节一致）。

**挂载纪律**：`UserCriticalRateLimit()` 必须挂在 `UserAuth`/`AdminAuth`/`RootAuth`
**之后**。挂错位置不会报错，只会静默回落到 IP 桶——虽然仍与登录桶隔离，但失去按人隔离的效果。

---

## 7. 测试方案（Rule 15，测试先行）

`middleware/rate_limit_test.go`，用 `gin.CreateTestContext` + `httptest`（Rule 15.1）。

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestWriteRateLimited_ReturnsJSONBody` | 语句覆盖 | 响应体含 `success:false` 与非空 `message`；`Retry-After` 头存在。**本次 bug 的回归测试** |
| `TestApiUserRateLimit_SameIPDifferentUsersIsolated` | 等价类划分 | 同一 `ClientIP` 下两个不同 `id`，各自独立计数，互不影响。**"同网段挤兑"的回归测试** |
| `TestApiUserRateLimit_SameUserAcrossIPsShared` | 等价类划分 | 同一 `id` 换 IP 仍共用同一个桶（防止换代理绕过） |
| `TestApiUserRateLimit_AnonymousPassesThrough` | 判定覆盖 | `id == 0` 时放行，不 401、不计数 |
| `TestApiIPRateLimit_BoundaryAtLimit` | 边界值 | 第 N 次放行、第 N+1 次拒绝；窗口过期后重置 |
| `TestRateLimit_RedisAndMemoryParity` | 路径覆盖 | Redis 分支与内存分支在同一输入下判定一致 |
| `TestRateLimit_RedisFailureBehavior` | 错误路径 | Redis 报错时保持现状（500 + Abort），不静默放行 |

**第二轮（§4.4 拆桶）新增用例，均已通过：**

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestUserCriticalRateLimitIsolatesUsersBehindSameIP` | 等价类划分 | 同 IP 两个不同 `id` 各自独立；且**不写** `CT` 桶的 key |
| `TestUserCriticalRateLimitFallsBackToItsOwnIPBucketWhenAnonymous` | 判定覆盖 | `id == 0` 时回落到 `CTU` 的 IP key，仍不碰 `CT` |
| `TestSessionCriticalRateLimitIsolatesSessionsBehindSameIP` | 等价类划分 | 同 IP 两个不同 sid 各自独立；secret 轮换后仍命中同一计数器。**"员工无法登录"的回归测试** |
| `TestSessionCriticalRateLimitFallsBackToIPWithoutUsableCookie` | 边界值 + 等价类 | 无 cookie / 无分隔符 / sid 非 UUID / secret 为空 —— 四类都回落到 `CTS` 的 IP key |
| `TestSessionCriticalRateLimitNeverChargesValidSessionsToTheIPBucket` | 判定覆盖 | IP 配额设为 1 时，三个不同会话仍全部放行且不创建 IP key；无 cookie 的请求仍受 IP 配额约束。**§4.4.3 共命运模式的回归测试** |
| `TestCriticalBucketsDoNotShareAllowance` | 路径覆盖 | 把 `CTS`/`CTU`/`PQ` 全部打满后，同一 IP 的登录仍然放行。**拆桶本身的回归测试** |
| `TestEveryRateLimiterUsesItsOwnCounter` | 路径覆盖 | 构造全部 11 个限流器，同一客户端各发一次请求，断言每个都新建了独占的计数器。**§4.4.6 的回归测试** |
| `TestRedisFailureDegradesToInMemoryInsteadOfFailingRequests` | 错误路径 | Redis 断开后请求仍被服务（非 500），且第二次请求仍被计数拒绝。**§4.4.7 的回归测试** |

跑真实 Redis；无 Redis 时走内存分支（`common.RedisEnabled == false`），
两条分支都要覆盖。完成后按 Rule 15.8 跑 `go test ./...` 全量。

---

## 8. 待确认项

1. ~~第二段挂载方式选 A 还是 C~~ —— 已选 A，见 §4.3 与 [middleware/auth.go](../../middleware/auth.go)。
2. **第一段 IP 桶放宽到 2000/180s 是否可接受**。这会让单 IP 的匿名滥用成本下降 5.5 倍。
   如果对外暴露面敏感，可以改成"匿名请求走严格 IP 桶、已认证请求走宽松 IP 桶 + 严格用户桶"，
   但那需要在第一段就能区分是否携带凭据，实现更复杂。
3. ~~`CRITICAL_RATE_LIMIT` 从 20 放宽到 60 是否安全~~ —— 见 §4.4.4：拆桶后
   非登录流量已全部移出 `CT`，净效果不是暴破窗口线性放大 3 倍。按账号维度的防护
   （`AUTH_SESSION_ISSUANCE_LIMIT`、登录失败计数）本次未改动。
4. **生产 `TRUSTED_PROXIES` 是否被误设**（§1.1）。仓库开发用 `.env` 里写的是
   `127.0.0.1,::1`，一旦同步到生产会把全站折叠成一个 IP 桶，届时本文档所有按 IP 的
   配额调整都不会生效。**上线前必须核对。**

---

## 9. Main Chain Impact（Rule 0）

**同步 / 异步**

- 本次改动全部在 `/api` 管理后台链路，**不触碰 `/v1` relay 主链路的任何中间件**。
  `relay-router.go` 一行不改。
- 新增的第二段限流只挂在 `AdminAuth()` / `UserAuth()` 之后，而这两个中间件
  **不出现在 relay 路由上**（relay 用 `TokenAuth()`）。
- 每个 `/api` 请求新增 1 次 Redis `EVAL`（固定窗口 Lua，单 key，O(1)）。
  后台 QPS 与 relay 相比可忽略，不构成 Redis 压力。

**共享资源审计**

| 资源 | relay 是否也用 | 冲突与处置 |
|---|---|---|
| Redis key `rateLimit:v2:ip:*` | 否 | relay 侧不读写该前缀 |
| Redis key `rateLimit:v2:user:GAU:*`（新增） | 否 | relay 侧用 `rateLimit:MRRL*` / `rateLimit:<userId>`，前缀不重叠 |
| Redis key `rateLimit:v2:{ip,user}:CTU:*`、`rateLimit:v2:{ip,session}:CTS:*`、`rateLimit:v2:ip:PQ:*`（第二轮新增） | 否 | 同上，命名空间不与 relay 重叠。`CTS` 的 session key 数量由 `AUTH_REFRESH_RATE_LIMIT_IP` 上界约束（§4.4.3），不会无界增长 |
| Redis 连接池 | **是** | 新增每请求 1 次 EVAL，只发生在 `/api`；后台流量远小于 relay，不构成池争用 |
| `common.InMemoryRateLimiter` | **是**（无 Redis 时 `ModelRequestRateLimit` 也用它） | 已有共享结构，本次只新增 key 前缀不改结构；key 命名保持前缀隔离（`GAU:user:<id>`） |
| DB 表 | 否 | 本次不涉及任何 DB 读写 |
| goroutine / 内存 | 否 | 无新增 goroutine，无新增常驻内存 |

**并发分析**

- relay 侧每请求的 DB/Redis 调用次数、锁使用、goroutine 成本**均为 0 变化**。
- `/api` 侧每请求 +1 次 Redis EVAL。按后台实际量级（个位数 QPS）计，
  100k RPM 的 relay 负载下这部分占比可忽略。
- 无新增分布式锁，无新增热路径同步 DB 读。
