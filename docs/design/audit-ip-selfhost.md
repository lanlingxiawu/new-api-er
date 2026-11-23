# 自建 IP 情报服务

> 网关只调用一个自建接口，数据与第三方来源由独立服务管理。下文保留接口和能力扩展设计；现行合同见 [集中调度](audit-ip-central-orchestration.md) 和 [配置说明](audit-ip-provider-setup.md)。具体部署地址、发布与验收记录仅在本地保留。

用户已确认免费自建方案与多个数据源自动轮换一并实施。本组件是可选、独立部署的数据提供端，不在网关进程中加载地理库。

## 免费能力扩展（2026-11-16）

### VPN / 代理补充（2026-11-16，用户要求继续完善）

网关接入使用既有监控配置和二次验证，具体设置见 [配置说明](audit-ip-provider-setup.md)。独立服务的部署和接入记录仅在本地保留。

**Existing Logic and Related-Path Findings：** 已逐项读取 `app.py`、`datasets.py`、`update.py` 及对应测试；查询只在本地匹配固定数据，`FeedCache` 按文件身份更新、`atomic_json` 原子替换。网关 `ip_provider_http.go` 已完整接收 vpn/proxy 布尔值和来源/网段证据，`applySelfHostedIPMetadata` 验证新鲜度与能力，最多 16 个来源；ProxyCheck v3 已支持无 key POST 查询。故本次无需发布网关代码、修改表结构或主链资源，只扩展独立服务并使用既有设置 API/UI。

**数据及生命周期：** 新增 X4BNet `output/vpn/ipv4.txt`、`output/vpn/ipv6.txt`（仅 VPN 分类，排除 datacenter 合集）和 Proxmint 公共代理快照。VPN 每 24 小时更新，通过 GitHub 最新文件提交取得 SHA/时间，再下载该 SHA 固定内容；GitHub raw 失败回退同 SHA 的 jsDelivr。来源提交超过 7 天或下载超过 48 小时撤销结论。代理快照每小时更新、有效 6 小时，必须验证发布的 updatedAt/count/许可和每条公网 IP/端口/协议；原始代理地址仅用于识别名单，不连接或转发用户流量。两者均只在命中时返回 true，未命中保持未知。记录来源、命中网络、发布时间、有效期；不同 IP 族不串用。

**后台集成：** 自建源能力增加 vpn/proxy，保留 geo/tor/abuse，优先级 100。增加 ProxyCheck v3 匿名免费源、每日预算不超过官方 100 次、优先级 200，补足本地未知项及故障回退，复用配额、缓存、熔断和自动选源。免费限制以实际官方响应为准；不自动创建付费账户。住宅代理由外部实际 proxy 标志辅助判断，当前合同不声称独立的住宅代理分类或全量覆盖。

**错误与资源：** 每份下载、条数、解析和时间有上限；整份校验成功才替换；持久化 last_attempt 防重启突破频率；失败保留旧数据但不延长其有效期。既有 Gunicorn/更新器容器和文件锁复用，无主库/日志库/Redis调用、无主转发链新增资源。更新器每小时唤醒，各源分别限频。源名单是风险线索，不自动封禁或冒充某次登录必然使用 VPN。

**授权及展示：** 保留 X4BNet MIT 与其 IPLocate CC BY-SA 4.0 上游署名、Proxmint CC BY 4.0 署名；返回 source.name/url 供现有递归详情显示，附独立服务 NOTICE 文件，数据快照保留授权信息，代码许可不因此混写。纯来源数据适配，没有新增用户可配置参数或接口。

**验收与发布：** 覆盖双栈边界、同前缀跨族拒绝、未命中未知、过期/替换撤销、时间/条数/私网/HTML/过大下载拒绝、镜像失败回退及重启限频。服务器独立候选镜像先运行全部测试和真实下载；备份现有源文件及镜像，仅替换两个情报容器，保留共享 key/卷。公网 HTTPS 查询实际名单样本并核对证据；后台按正常二次验证保存，读回脱敏配置及健康状态。现有 SSH/HTTPS/安全检查完成后记录结果，不以单元测试冒充真实住宅代理覆盖。

**真实联调发现及修正：** ProxyCheck v3 无 query 的 POST `/v3/` 实际重定向到查询者自身 IP，现有适配器禁重定向会记录失败；带官方 `tag=0` 的 POST 返回正确目标。调整 `callIPProvider` 始终附 `tag=0`（关闭服务商阳性记录标签），可选 key 独立合并，继续禁止跟随重定向和验证返回 IP。真实位置字段为 `city_name/region_name`，保留旧 `city/state` 兼容回退。此修正需要仅重建网关后端：从已部署版本完整快照复制，只改 HTTP 适配文件和定向测试，前端产物、模型/迁移、relay、依赖保持一致。无需新增配置参数、表或迁移；原独立 worker 池/限额/缓存均复用。旧“无需发布网关代码”判断由实际接口反例修正，不放宽 endpoint 校验或通过静态 IP URL 冒充动态查询。

用户要求优先完全免费的强能力方案，现成方案不足时进行二次开发；本次沿用已授权的自建服务和多源接入范围。免费是软件和所选数据无订阅费，不包括机器与带宽。软件开发不能补出没有数据支撑的全球 VPN、住宅代理或实时信誉结论。

### Existing Logic and Related-Path Findings — 扩展前调查

- 扩展前 `tools/ip-intelligence/app.py` 每次请求读取 City MMDB 和 Tor JSON；只有地理与 Tor，没有 ASN、网段证据或数据健康接口。`update.py` 固定地址拉取并原子替换，失败保留旧文件；实施继续复用隔离与更新模式，新增 `datasets.py` 管理有界缓存及网段索引。
- 扩展前 `service/auditsecurity/ip_provider_http.go:requestIPAPI` 只接收地理和布尔值，丢弃响应中的来源；`ip_provider.go` 按配置能力过滤、合并、缓存，`ip_rules.go` 仅在后台处理成功登录时生成规则，结果存入现有 `SecurityEvent.Facts` JSON。实施已将新增来源与证据随该 JSON 保存，不新增表、索引或查询。
- `SecurityEventCard` 已经通过 `AuditDetailFields` 递归显示事实；新增字段使用现有标签和值映射，补七种语言，不另造样式。详情已有静态来源链接，增加 DROP 归属标注。
- 新加坡主机已有 nginx 与多个业务容器，80/443 已占用；部署必须使用独立 Compose 项目、回环端口及单独域名 nginx 配置，不能直接运行当前占用 80/443 的 Caddy 服务。

### 实施范围与数据流

1. 固定免费数据：DB-IP City Lite、DB-IP ASN Lite、Tor 官方出口、Spamhaus DROP IPv4/IPv6。ASN 仅提供网络归属，不把运营商/机房 ASN 自动判为 VPN。DROP 提供高风险网段证据，不冒充全球完整信誉库。
2. 更新器分别保存更新时间、最后尝试、稳定错误码；Tor 每 6 小时、DROP 每 24 小时（包括失败尝试，持久限频）、DB-IP 按月且失败有退避。下载/解压/条数有界、结构/时间/网段校验、同目录原子替换，更新失败不清空可用数据。
   新加坡实际访问 DB-IP 直链返回 403，因此增加固定的公开 CC BY 4.0 再分发源：City 使用 WP Statistics 的 jsDelivr 数据包，ASN 使用 sapics/ip-location-db 的合并 MMDB（校验发布的 SHA-256）。不执行第三方代码，保持类型/构建日期/大小校验；保存实际下载源，地理数据版权仍归属 DB-IP。来源：[WP Statistics](https://github.com/wp-statistics/DbIP-City-lite)、[sapics/ip-location-db](https://github.com/sapics/ip-location-db)。
3. 查询只读取本地文件。DROP 构建按前缀长度分组的哈希索引；Tor 使用集合；按文件身份变更重新加载，文件缓存有锁且仅限固定几份数据，不缓存无限 IP。
4. `POST /` 保持 `{q,key}`，新增 `schema_version=2,asn?,sources,evidence,expires_at`。仅确实命中 DROP 时返回 `is_abuser=true`；未命中保持未知。Tor 的 false 仅表示未命中当前有效出口名单。证据含 source/capability/matched_network/reference/data_updated_at/expires_at，不含访问凭据。
5. `POST /health` 同样需要 key，返回数据源 fresh/stale/missing/invalid、更新/失效时间、记录数与上次更新错误。无公开敏感运维接口；错误状态不吐 URL、原始响应或内部路径。请求体仍限 4096 字节，拒绝非公网、组播、zone ID。
6. 网关 custom_ipapi 接收有界的来源、ASN、证据及到期时间；按能力过滤证据并保留 provider_id；缓存不能超过数据到期时间。外部源布尔值原有含义不变。不会把免费粗定位伪装成可靠精度，异地高速规则仍需要合格精度数据。
7. 前端复用递归事实组件展示来源、网段、编号、日期和 ASN；自建预设可声明 geo/tor/abuse，未知 VPN/proxy 保持未知。现有优先级、同级轮换、熔断、预算、权限及导出复用。

### 边界、错误及资源

服务自身无 DB、Redis 或网关配置表。数据更新与请求线程隔离，固定 worker/内存/PID 限额。POST 查询不对外发送用户 IP。更新失败仅影响该数据源；超过有效期不输出该字段。DROP 的原始元数据（版权、时间、条数、条款）保存在数据文件，页面给来源链接。升级仅增加 JSON 字段，不改数据库 schema、主链中间件、计费或鉴权。

### 验收

覆盖 IPv4/IPv6 CIDR 边界、重叠前缀、未命中未知、过期撤销、坏文件/HTML/部分下载保留旧数据、跨重启 24 小时限频、ASN 映射、鉴权健康接口、拒绝组播/私网、网关能力过滤和缓存上限、中文证据显示。真实容器 HTTP、镜像构建及数据拉取独立验收。部署先验证回环接口与现有业务健康，再在 DNS 正确时配置独立域名 HTTPS；未完成的外部条件明确记录。

## Existing Logic and Related-Path Findings

网关 `setting/operation_setting/audit_ip_provider.go` 的 custom_ipapi 配置及审计后台 worker 接收 ipapi.is 兼容 JSON，能力按 geo/vpn/proxy/tor/abuse 声明。外部请求由后台预算、缓存、超时和熔断控制，前台不直接发送密钥。复用 POST `{q,key}` 合同；HTTPS 和精确内网主机放行仍由网关强制执行。普通审计的鼠标悬停归属地使用独立只读路径，此服务不改变它。

## 生命周期及接口

`tools/ip-intelligence` 提供 WSGI 服务、固定来源更新器与 Docker Compose。部署者提供域名与随机共享密钥文件；空主机可由 Caddy 终止 TLS，已有 nginx 的主机使用 `compose.nginx.yaml` 的回环端口。POST `/` 查询和 POST `/health` 健康检查都需要认证，正文最多 4096 字节；查询只接受公网单播 IPv4/IPv6 字面量。返回 `schema_version=2,ip,location?,asn?,is_tor?,is_abuser?,sources,evidence,expires_at?`；无数据、省略字段和过期数据都是未知，不填 false 冒充正常。不提供 VPN、proxy 结论；DROP 未命中时 abuse 保持未知。

更新器每六小时抓取 Tor 官方出口列表、每日获取 DROP IPv4/IPv6、每月获取 DB-IP City/ASN Lite MMDB。固定 HTTPS 地址，不接收任意下载 URL；有超时、下载/解压大小上限、结构校验、临时文件及原子替换。失败保留旧文件；Tor 超过 48 小时、地理/ASN 库构建日期超过 45 天、DROP 发布超过 7 天或成功下载超过 48 小时后不输出对应结论。服务不因更新而中断已有读取。不存储用户查询或密钥日志。

部署配置属于独立服务基础设施：域名、只读密钥文件、只读数据卷。网关使用现有 Root 设置配置 endpoint、key、能力、预算与 allowlist。DB-IP Lite 不提供网关旅行规则要求的类别精度，不输出 accuracy，也不将其他 MMDB 的数值 accuracy_radius 冒充类别精度；地理数据用于展示，不能单独触发异地规则。坐标必须有限且处于合法经纬度范围。

## Main Chain Impact / Shared Resource Audit

主转发链零新增调用。服务独立容器、独立文件卷，无主库/日志库/Redis连接。后台每次最多受网关现有周期预算约束；服务 Gunicorn 固定 2 workers × 4 threads、有超时和内存限制。更新器独立单进程，不共享网关 goroutine 或连接池。

## 验证

先覆盖鉴权、非法/私网 IP、正文上限、数据缺失与过期、IPv4/IPv6 Tor 判定、地理映射、更新失败保留旧文件；再验证镜像构建、Compose 配置和独立 HTTP 合同。没有域名/证书或部署凭据时不声称生产 HTTPS 已验收。DB-IP Lite 使用需在显示结果的页面提供来源链接；前端详情附 DB-IP/IPinfo 等数据来源链接。

来源：[DB-IP Lite](https://db-ip.com/db/lite.php)、[Tor 出口列表](https://check.torproject.org/torbulkexitlist)、[MaxMind DB Python](https://maxminddb.readthedocs.io/en/latest/)。数据集免费不代表服务器、域名或流量无成本。
