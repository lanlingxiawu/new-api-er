# 集中式 IP 情报调度

日期：2026-11-16。用户明确要求将其余数据源整合到自建服务，NEXAXIS 仅保留一个接口。本变更落实该方向，不增加付费账户或订阅。

## Existing Logic and Related-Path Findings

以下是实施前核对的基线，实际完成状态见本文末尾。

- `service/auditsecurity/ip_rules.go:enrichSecurityEvents` 仅后台丰富成功登录，调用 `ip_provider.go`；不在登录响应或 AI relay 热路径执行。
- `ip_provider.go` 当前在网关选择多个源、缓存、合并，并通过 `ip_provider_quota.go` 在独立审计池保存配额。`ip_provider_http.go` 实现三个公网适配器和自建协议。
- `tools/ip-intelligence/app.py` 已提供鉴权 POST `/` 和 `/health`，本地八份数据通过只读卷查询；未命中部分名单保持 unknown，来源及到期时间受网关 schema v2 校验。
- `settings-dialog.tsx` 使用 Root 设置 API 及绑定 values 的二次验证；`ip-providers-field.tsx` 当前暴露多个源。`MergeIPIntelligenceProviders` 按稳定 ID 保留空白密钥，GET 脱敏。复用这些安全合同和存储键，避免数据库迁移。
- 从旧版多源配置升级时，先部署兼容原 v2 的聚合服务，再经正常后台验证把网关配置收敛到一个源，最后部署单接口代码。

## 目标、数据流与 API

`审计 worker → 唯一 HTTPS 自建接口 → 本地有效数据 + 按缺失能力选外部源 → 带来源/到期时间的 v2 响应 → 原有告警判断`。

网关只保留 `custom_ipapi` HTTP 实现，不再包含 ProxyCheck、ipapi.is、IPinfo Lite 的请求/解析或轮换算法。旧类型仅保留配置读取兼容，保存只允许零个或一个自建源；运行时拒绝多个自建源，绝不直连旧公网源。历史事件不改写。既有请求上限、短期缓存和失败冷却仅保护单一自建接口，并非外部源调度。

2026-11-17 审计修正：相对本次审阅基线 HEAD，上述配置和运行时均为新增代码。已复现旧类型启用时通过设置校验、实际查找却返回空情报的问题。按用户“确认是本次改动导致的直接修改”要求，设置校验、运行时与健康检查共用唯一启用自建源的选择规则。开启 IP 情报必须存在且只存在一个启用的 `custom_ipapi`；旧配置仍可读取或关闭。已持久化的不适用配置由健康接口和后台扫描报告现有 `invalid_provider_config`，后台日志只在故障状态变化时记录，其他安全规则继续扫描。不新增 API、设置、存储或 relay 同步工作。

自建服务保持 POST `/` `{q,key}`；POST `/health` 同一只读查询密钥，增加脱敏 upstream 状态（用量、预算、熔断、错误码），不返回 endpoint/key。无新增公开无鉴权接口。所有外部判断转换为本协议来源和正向 evidence；缺失/过期/失败保持 unknown。

## 自建配置与生命周期

独立服务配置文件 `upstreams.json` 作为只读 secret 挂载，包含最多 20 个稳定 ID、type、enabled、priority、daily_limit、capabilities、api_key，以及仅兼容源使用的 HTTPS endpoint。配置通过服务器管理员编辑并重启 provider 生效；提供不含密钥的 example 和检查命令。NEXAXIS 无需保存这些第三方密钥。

支持 ProxyCheck v3（匿名默认 100/UTC 日）、ipapi.is（无 key 仅地理，预算 30）、IPinfo Lite（需 key，仅 geo/ASN）、自建兼容源。未配置的注册源默认禁用。不注册账户，不宣称所有源都能匿名提供完整安全字段。

优先使用本地数据和已有有效缓存；同优先级在未缓存查询之间持久轮换，按缺失能力补充，不为轮换重复消耗查询预算。每次最多 3 个外呼，总外呼预算 3.5 秒，每源最多 1.5 秒。公网源固定官方 HTTPS 地址；兼容源只允许 HTTPS、公网解析地址、禁重定向，解析后固定拨号地址，禁环境代理。请求体/响应有界，校验响应 IP、布尔类型、坐标和响应错误，错误仅稳定代码。

独立可写 `/state` 卷：进程间 flock 保护短暂原子 JSON 状态更新（不持锁外呼），先预留 UTC 日预算，再发请求，失败也计次；跨重启保留每日计数、配置变更不清预算。同 IP 在途租约避免重复计费；租约超时恢复。缓存使用摘要文件名，最多 512 项、每项 128 KiB、定期有界淘汰；保存规范化数据，不保存原始响应或密钥。ProxyCheck 阳性最多 15 分钟、阴性 1 小时；其他外呼最长 1 小时。来源过期逐项丢弃，不输出陈旧判断。429/401/403/超时/5xx 熔断并切换，局部失败保留本地结果。损坏状态禁止外呼，避免配额被静默重置；健康显示可定位的状态错误。

## 存储、索引与资源

网关不新增表/列/索引，旧审计配额表继续只计自建请求。自建服务不连接业务数据库、Redis 或共享连接池；状态和缓存都在独立卷。其有界文件索引、20 源状态及 512 缓存文件足够当前独立低频查询，不引入主库跨库查询。

## Main Chain Impact

AI relay 同步执行内容为零；仅现有审计 worker 外呼。无新增 relay middleware、业务 users/channels/tokens 查询、Redis key 或共享 worker。Go 本地缓存最多 10000 项沿用；Python 2 workers/4 threads、CPU/内存限制和独立卷隔离资源。不会因外部故障拒绝登录、封号或阻断 AI 转发。

## 验证与发布

检查适配协议、错 IP/空字段/错误类型、SSRF、总超时、配额并发跨进程/重启/UTC 翻日、缓存到期、429/失败切换、同优先级轮换、能力缺失、来源保留、状态损坏拒绝外呼；Linux 容器跑全部自建测试。网关运行单源/禁止旧公网源/原有 schema v2 回归及真实 MySQL/PostgreSQL 定向测试，前端类型/构建/七语言及正常浏览器保存。

发布使用精确现行 gateway 源快照加本次改动，保留原业务代码与 relay，备份源/镜像/配置和状态，失败回滚；自建先兼容部署，确认实际外呼来自新加坡，再收敛 NEXAXIS 配置。最终通过实际 HTTPS、后台设置和真实登录事件验证单一 provider ID 与各底层 source。结果与限制在完成后追加。

## 实际实现

实现位于 `tools/ip-intelligence/orchestrator.py`、`upstream_http.py`；`app.py` 接入本地查询后的能力补充及鉴权健康接口。`compose*.yaml` 新增只读 upstream 配置和独立 `ip_state` 卷。网关 `ip_provider_http.go` 只保留兼容协议，`ip_provider.go` 只接受一个启用的自建服务；配置保存校验、单接口表单和七种语言同步完成。现有 schema v2 来源/证据有界上限调整为 32。历史外部类型仅用于读取和提示迁移，不会继续外呼。

HTTP 总截止时间覆盖 TLS、响应头及慢速逐字节响应。DNS 查询由四个线程和有界入口限制；响应限制 256 KiB。跨进程配额/租约采用短暂文件锁，外呼期间不持锁；规范化缓存优先于未缓存来源，避免相同 IP 因轮换重复花费预算。异常状态禁止外部请求，但继续返回有效本地数据。
