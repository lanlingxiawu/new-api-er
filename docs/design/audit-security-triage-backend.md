# 审计安全监控：全量后端合同与生命周期

> IP 数据源合同更新（2026-11-16）：当前网关仅允许一个自建兼容接口，第三方适配和调度移至独立服务。本文多 provider 设置描述属于早期合同；其余告警合同不变。当前 IP 配置和实现合同见 [集中调度](audit-ip-central-orchestration.md)。

2026-11-16。用户已明确“全部进行实现，不要分期”。本文是后端实施合同；前端和总设计由协作代理维护。尚未部署、启用线上监控或调用真实付费/外部数据服务。

## Existing Logic and Related-Path Findings

- `store.go` 的通用历史证据条件会排除 `login_after_failures` 所需的 `auth.login_failed`；告警合并键缺少资源类型。检测计数已经按资源隔离，告警保存也必须一致。
- 告警控制器依赖 worker 私有 `primary`，监控全关或暂停冷启动时不可读取/处理历史告警；`model.InitAuditReadPools` 已在 worker 前初始化独立主库池。
- `last_seen` 是最近事件时间，不是记录更新时间；迟到事件和人工动作不保证推进它。ID 游标只有确定性排序，不是快照。
- 清理会删除未结案告警，且跨轮删除 links/actions/deliveries 后再删 alert；不能依赖已删除的 action 判断清理资格，固定最老候选也会被永久保留项堵塞。
- 生产主库为 MySQL、独立日志库为 PostgreSQL。告警快照、关联和时间线均在主库完成；源审计只通过独立日志池分批回读，禁止跨库 JOIN。
- 现有认证关联 Root-only，普通管理员需要 `audit:read` + `audit_security:read`；处理需要 manage 和浏览器身份。上述边界不扩大。
- `ipapi.is` 官方支持 POST `{q,key}`；匿名响应只有精简归属/位置，完整响应的检测字段为 `is_vpn/is_proxy/is_tor/is_abuser` 等。完整地域字段为 `location.state`，匿名响应为 `region`。依据：[开发者文档](https://ipapi.is/developers.html)、[免费层说明](https://ipapi.is/free-tier.html)。

## 实施范围

1. 修正失败证据；三个资源规则和 `login_after_failures` 使用各自 `rule_version=2`，其余规则保持 1。
2. 历史告警读写与 worker 生命周期解耦；统一使用审计控制面主库池，人工动作仍是有界写事务。
3. 告警列表支持 ID、最近命中、完整活动、全部未结案、归档视图及组合筛选。
4. 逐次 finding 快照、系统重开、通知跳过/限额/失败原因、负责人和结构化结论。
5. `closed_at`、归档、可恢复有界清理、容量提示和有权限导出。
6. 同账号/IP/请求关联时间线，单事件 IP 原因有界分页。
7. 默认关闭的 `ipapi.is` 兼容 provider：地域、VPN/proxy/Tor、abuser 和不可能旅行检测。无真实响应不生成对应结论，不自动封禁。

## API 合同

所有路径位于既有 `/api/audit/security` 组，要求 AdminAuth + `audit:read` + `audit_security:read`。写操作另需 manage + 浏览器身份；导出另需 `audit_security:export` + 浏览器身份。不可见与不存在保持相同外部语义。

### 告警列表 `GET /alerts`

查询参数：

| 参数 | 合同 |
| --- | --- |
| `view` | `id`（默认）、`recent_hits`、`activity`、`unresolved`、`archived` |
| `start/end` | id/recent_hits 按 `last_seen`，activity 按 `updated_at`；默认最近 24 小时，最多 31 天。unresolved/archived 默认全时间，显式给定时仍最多 31 天 |
| `before` | id/unresolved/archived 的数字 ID keyset；其他视图禁止 |
| `cursor` | 仅 activity 的不透明字符串，绑定筛选、角色、用户和 revision |
| `limit` | 默认 50，1..100；recent_hits 固定 20，省略或传 20 |
| `status` | open/investigating/resolved/false_positive；unresolved 仅允许前两者，archived 仅允许后两者 |
| `resource` | channel/user/account；与 `history_scope` 互斥 |
| `history_scope` | 仅 `legacy`：三个资源规则且 `rule_version<2`，不表示这些记录一定混合，只表示旧口径需复核 |
| `rule` | 白名单规则标识，未知值拒绝 |
| `severity` | 1/2/3 |
| `actor_id` / `assignee_id` | 正整数；`assignee_id=0` 表示未分配 |
| `ip` | 精确 IPv4/IPv6，通过主库 finding/trigger 或证据关联筛选，不 JOIN 日志库 |
| `archived` | 仅 id/activity 可用的显式 true/false；默认 false，archived 视图隐含 true |

响应：`{items:[SecurityAlert],next_cursor:number|string,revision?:number,capacity:SecurityCapacity}`。id/unresolved/archived 的 `next_cursor` 为数字，activity 为字符串；recent_hits 无下一页。activity 在查询开始和结束检查 revision，变化返回可重试的 `activity_changed`，不伪装空页。recent_hits 是单页整体替换，不称“最近更新”。

索引：`idx_sec_episode_v2(rule,subject_ref,visibility,resource_type,rule_version,last_seen)`、非 Root `(visibility,last_seen,id)`、Root `(last_seen,id)`、activity `(visibility,updated_at,id)` 与 Root `(updated_at,id)`。筛选/归档/负责人/IP 关联索引按实际 WHERE 和 EXPLAIN 补齐，不把 LIMIT 当扫描保证。

### 详情与关联

- `GET /alerts/:id` 返回 `{alert,events,findings,actions,deliveries}`。events 最多 20；findings 最多 100，按 ID 倒序；actions 最多 100；deliveries 最多 100。旧记录没有 findings 时明确为空，不用当前配置补算。
- `GET /alerts/:id/relations?relation=account|ip|request&cursor&limit` 返回 `{items:[SecurityRelation],next_cursor,coverage}`。仅使用当前操作者可见的持久化 finding/trigger 证据，关系是线索而非同一攻击者证明。limit 默认 20、1..50；游标为 `(at,id)`，revision 变化时重新查询。
- `GET /ip-reasons?event_id&after_id&limit` 返回 `{items:[IPRiskReason],next_cursor,revision,coverage}`。先通过日志库验证 event_id 可见性及 IP/时间，再查询主库关联；limit 默认 20、1..50，按 alert_id 有界前进，不合并历史 episode。
- `GET /assignees` 返回当前可分配的启用管理员 `[{id,username,role}]`，最多 100 个，按 username/id 排序；需要 manage。
- `GET /health` 继续 Root-only，分别返回 worker ready、控制面存储、采集/检测、capacity、activity revision 和 IP provider 非敏感健康状态。

### 处理、归档与导出

`POST /alerts/:id/actions`：

```json
{
  "version": 3,
  "status": "investigating",
  "note": "排查说明",
  "assignee_id": 42,
  "conclusion": "authorized"
}
```

- `version` 必填；status 可选 open/investigating/resolved/false_positive。
- `assignee_id` 可选，0 清除负责人；省略表示不变。负责人必须是启用管理员。
- `conclusion` 可选 `authorized/expected/malicious/false_positive/other`，空字符串清除；`note` 同时保存到动作和当前结论说明。至少存在一项实际变更。
- 返回更新后的 alert。陈旧 version 返回 conflict，不自动覆盖。

`POST /alerts/:id/archive` 请求 `{version,archived:boolean}`。只有 resolved/false_positive 可归档；取消归档不改变 closed_at。重新命中自动重开时同时清除 closed_at/archived_at，使记录回到未结案。

`GET /alerts/export` 接受 id/unresolved/archived 相同筛选，limit 默认 50、最大 100。返回 JSON 附件 `{success:true,data:{exported_at,filters,truncated,items:[{alert,events,findings,actions,deliveries}]}}`。仅包含当前可见记录，通知目的地和凭据永不导出；最多 100 条并明确 truncated，不创建无界后台任务。

## 类型合同

`SecurityAlert` 在现有字段上增加：

```text
rule_version:int
updated_at:int
closed_at:int|null
assignee_id:int
conclusion:string
conclusion_note:string
archived_at:int|null
latest_finding_id:int
```

`SecurityFindingSnapshot`：

```text
id, alert_id, rule, rule_version
trigger_event_id, trigger_at, detected_at, source:live|repair
subject_ref, actor_id, actor_role, resource_type, target_id, ip, request_id
severity, threshold, window_seconds, matched_count
dimensions:object<string,number>
truncated:boolean
```

`SecurityAction` 增加 `source/from_status/to_status/event_id/event_at/detected_at/assignee_id/conclusion`；系统动作 actor_id=0，人工动作保留实际操作者。可见性先校验 alert，系统动作不再因 actor_role=0 被隐藏，人工动作仍按原 Root 隔离。

`SecurityDelivery` 增加 `finding_id/wait_reason/updated_at`；`kind=none,status=skipped` 表示未创建真实投递。wait_reason 为 disabled/backfill/cooldown/no_destination/rate_limited/destination_removed；发送失败继续使用 error_code，不把限额等待当作失败。

`SecurityCapacity`：`total/open/archived/retention_unknown` 为有界统计，`truncated/warning` 表示达到采样或配置容量门槛，不能伪造精确无限总数。

## 设置字段（audit_security_setting）

新增：

| 字段 | 默认与约束 |
| --- | --- |
| `archive_retention_days` | 365，30..3650；仅已归档且已关闭记录按此期限清理 |
| `ip_intelligence_enabled` | false；启用时至少一个 enabled provider，空列表可在关闭状态保存 |
| `ip_intelligence_providers_json` | 默认 `[]`，旧设置空字符串同样按空列表；最多 20 个 |
| `ip_intelligence_cache_ttl_minutes` | 1440，10..10080；proxycheck 阳性另 capped 15 分钟、其他结果 60 分钟 |
| `ip_intelligence_timeout_seconds` | 5，1..8 |
| `ip_intelligence_max_lookups_per_cycle` | 5，1..20；限制整个 worker 周期的外部调用数 |
| `ip_intelligence_impossible_travel_kmh` | 1000，100..10000 |
| `ip_intelligence_min_distance_km` | 200，50..5000 |

provider 合同：`{id,name,type,endpoint,enabled,priority,api_key,key_configured,clear_secret,daily_limit,capabilities,allowed_private_hosts}`。ID 稳定且唯一，priority 小者优先；同优先级且具备所需能力的健康源进行真正的 round-robin，故障/超额再切换。`daily_limit=0` 表示不限量。type 支持 `ipapi_is`、`proxycheck`、`ipinfo_lite` 和 `custom_ipapi`；只扩展明确协议，不执行任意模板。capabilities 为 geo/vpn/proxy/tor/abuse 子集，自建源必须显式声明，geo-only 不会把风险字段当作 false。

GET options 不返回原始 providers_json，只返回脱敏列表：每项 `api_key:""`, `key_configured:boolean`, `clear_secret:false`，绝不返回 key。保存时同 ID 的 api_key 空/空白表示保留旧 key，`clear_secret:true` 才显式清除，非空新值替换；删除列表项即删除其配置和 secret。proof 仍绑定原始 values，持久化前再做保留/清除归一化。key 的存储保护沿用现有 secret option 机制，不进入日志、审计正文、导出、通知或 provider URL。

endpoint 仅允许 HTTPS、无 userinfo/query/fragment。IPinfo 接受 base 或以 `/lite` 结尾的地址；proxycheck 使用 v3 endpoint。公网地址沿用解析后 SSRF 校验；自建内网地址还必须把 endpoint 主机精确列入 `allowed_private_hosts`，不支持通配符，不因其他 URL 关闭 SSRF。禁重定向，key 只发送给该固定 endpoint。

## 证据、快照与活动一致性

- `login_after_failures` 保留当前成功事件和最新 19 条同账户、同可见性、前 15 分钟内的有效失败，event_id 去重；展示按 `(at,event_id)`。旧 v1 只提示证据缺口。
- 三个资源规则及该登录规则合并时同时匹配 `resource_type` 和 `rule_version=2`，新事件不写入旧组；其他规则保持版本 1。
- 每次 finding 持久化阈值、窗口、实际维度、trigger、live/repair、检测时间和截断标记。matched_count 是峰值，occurrences 是触发次数，不能混拼不同 finding 的快照。
- 所有 alert 变更写入 `updated_at` 并推进单例 activity revision，包括迟到命中、人工状态、负责人、结论、归档和通知状态。activity 游标包含 revision/binding/updated_at/id；开始和结束均校验。
- 自动重开写系统 action，保存前后状态、event_id/event_at/detected_at/source；迟到修复不描述成关闭后新攻击。closed_at 和 archived_at 同时清空。

## 保留、清理与容量

- open/investigating 永不自动删除；unresolved 视图提供全时间入口。
- resolved/false_positive 在首次/再次关闭时写 closed_at；重开清空。旧关闭记录 closed_at 为空时，仅从仍存在的最新匹配 action 做有界补写；无 action 或状态不一致标记 retention_unknown，保守保留。
- 普通已关闭记录按 alert_retention_days，从 closed_at 计算；归档记录按 archive_retention_days。取消归档不改变 closed_at。
- 新 `audit_security_cleanup_states` 持久化 cycle_max_id/cursor_id/closed_cutoff/archive_cutoff/last_complete。每轮按主键前进，跳过项也推进，避免最老候选堵塞。
- 开始删除前先持久化 `purge_state=deleting` 并递增 alert version；扫描合并、人工动作、投递认领和列表查询均排除该状态。部分清理的旧组不会被新命中重开成空证据，新命中建立新组。
- 部分清理的 alert 不推进游标；links、deliveries、findings、actions 每轮各最多 500 条，全部子记录清空后才删除 alert 并推进。清理资格来自持久化 closed_at/status/archived_at/purge_state，不依赖将被删除的 actions，重启可继续。
- 每个候选在事务内锁定并重新检查 status/version。重开先提交则清理跳过；清理先提交则旧组更新返回 version conflict，扫描重试后建立新组。pending 投递可取消，sending 投递不删除；sending 有超时恢复，网络提交与数据库仍不承诺恰好一次。
- 容量提示采用有界统计和明确 warning/truncated；归档但 closed_at 未知也计入 retention_unknown。活跃、未知闭合时间和长期归档可能增长，归档/导出权限不替代磁盘预算和生产确认。

## 多 provider、轮换与检测

- 仅 worker 后台对成功登录且通过项目 SSRF 公网分类的 IP 查询；不进入 relay、登录响应或普通审计查询。按当前缺失能力在 enabled provider 中选源，同优先级健康源实际 round-robin；单源 geo-only、字段缺失或失败后继续选择有能力源，不能将缺字段当 false。
- 每个 provider/UTC 日在主库持久化配额、配置签名、失败次数和 circuit_until。原子预留后才调用，重启或多节点不会无限重置预算；配置签名变化只重置熔断状态，绝不清零当天 used。429 按 Retry-After/UTC 午夜熔断，超时/5xx 有界指数退避，403 长退避；随后自动切换下一个有能力源。配额/熔断持久化失败记录稳定系统错误。
- 每个请求使用独立 HTTP transport、解析后 SSRF 校验、禁重定向、响应最大 256 KiB、总超时和每轮调用预算。ipapi.is 与 custom_ipapi 使用单 IP POST `{q,key}`；proxycheck 使用 v3 表单 POST，IPinfo 使用 Lite GET，均只接受明确协议，不执行模板。proxycheck 响应键只匹配本次 IP，通用 risk 分数不当作 abuser。
- 成功结果按 IP+provider 配置签名缓存，容量最多 10,000；unknown/失败使用短负缓存。proxycheck 匿名阳性缓存最多 15 分钟、其他结果最多 60 分钟，暂不解析 delist_datetime。外部失败不影响既有认证审计和其他规则。
- ipapi.is 完整响应解析 `location.country/country_code/state/city/latitude/longitude`；匿名响应解析 `country/region/city/lat/lon`。`is_vpn/is_proxy/is_tor/is_abuser` 使用可空布尔；匿名、缺字段或无效响应均为 unknown，不能转为 false。坐标必须 finite 且经纬度在合法范围。
- 多源合并按可靠坐标质量整体升级地理位置，不跨源拼接国家、省州和城市；anycast/satellite 采用 true 优先。ObservedAt 只来自实际有数据的 provider 响应，不用当前时间伪装新采样。
- 保存最小证据：各字段 provider、是否匿名、查询时间、地域、精度及安全标志；不保存 key、账户信息、abuse 联系方式、完整响应或精确街道。
- 新规则：`anonymous_network`（VPN/proxy/Tor 为 true，需关注）、`ip_abuse`（is_abuser 为 true，高风险）、`impossible_travel`（与账户上一有效地点距离和速度同时超过配置，高风险）。只有明确 VERY_HIGH/HIGH/MEDIUM/LOW 精度且非 anycast/satellite 的合法坐标才允许旅行判断；空、UNKNOWN、VERY_LOW 或缺坐标均排除。
- 地理基线只保存账户 ID、粗略坐标、国家、最近有效事件时间；迟到旧事件不回写较新基线。新规则只表示 provider 信号，不自动封禁，也不把 VPN/Tor 本身等同恶意。

## Main Chain Impact / Shared Resource Audit

AI relay 新增同步 DB/Redis/HTTP/goroutine 为 0。新增工作位于既有后台 worker 和管理 API；不修改 relay、认证凭据判定、计费或流式处理。主库新增监控表/索引，日志库只按 event_id/request_id/IP+时间分批读取；独立连接池仍共享物理 CPU/I/O，须做真实负载和 EXPLAIN。provider 使用独立 transport，不占用 relay 连接。

## 测试与验收

先写定向反例，再实现：四规则版本隔离、失败证据 20 条边界、历史快照为空、活动 revision 失效、Root/Admin 查询计划、系统重开、通知原因、清理多轮续跑/跳过/重开并发/重启、归档导出权限、关联时间线、IP 原因分页、secret 留空/清除/不回显、provider 匿名/完整/429/403/SSRF/预算/缓存及三规则 unknown 边界。

测试只用隔离数据库和按测试 ID 清理，禁止 truncate 共享库。SQLite 仅作快速诊断；发布验收使用本次新版稳定 MySQL + PostgreSQL、真实分库完整应用、新建/旧版升级/二次启动、代表性数据 EXPLAIN 和生产 schema-only 演练。不得部署、启用线上监控或调用真实外部 provider 作为单元测试。
