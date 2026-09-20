# 使用日志导出：异常排查筛选 与 模板受众分级

> 基线文档：`docs/design/usage-log-export.md`（导出中心的任务化/分片/断点续传/资源治理设计与实现记录）。
> 本文只描述**在此之上的增量**，不重复既有设计。基线文档里已实现的扫描管线、限速闸门、
> 分片写入、下载鉴权全部原样复用。

---

## 1. 背景

### 1.1 直接触发原因

2026-09-18 的一笔请求（`20260918202008166309…`）被多收了约 928 倍：上游实际输出 1,400 token，
本站记为 1,299,013 token，扣费 $155.88。成因是上游流在 `usageMetadata` 下发前 EOF，
计费退化为本地估算，而交付文本里嵌着 base64 图片数据 URL，被估算器按字符权重算成了 130 万 token。

排查这笔账花的时间几乎全耗在**找不到同类记录**上：

- 导出的筛选条件只有 时间/类型/模型/用户/令牌/渠道/分组 七项，没有任何「计费口径异常」的入口。
- 判定这笔账异常的关键字段——`other.stream_result.usage_source`（上游/估算/混合/不计费）、
  `other.stream_status.end_reason`（`upstream_incomplete`）、`settlement_state`——
  **既不能筛，也没有对应的导出列**。`stream_status` 列虽然存在，但它整块吐 JSON，
  在表格软件里无法排序、无法筛选。
- 想回答「过去 30 天还有多少笔是估算计费的、总共多收了多少」，只能人工翻页面。

### 1.2 同时暴露的第二个问题

现有 6 个内置模板（`as_displayed` / `legacy` / `billing` / `performance` / `audit` / `full`）
全部是管理员视角，**没有任何一个能直接发给客户**：

- `builtin:billing` 看起来最接近对账单，但它含 `billing_source`（钱包/订阅）、`billing_mode`、
  `matched_tier`、`request_rules`（计费规则原文）这些内部定价策略字段。
- 唯一给到客户手里的文件是 `/log/self/export` 的固定 14 列 xlsx，里面没有任何 token 明细
  与倍率，客户拿到只能核对总额，一旦对不上就只能找客服。

模板缺的不是列，而是**受众**这个维度：哪些列可以出现在发给客户的文件里，从来没有被定义过。

---

## 2. 目标与范围

### 2.1 目标

1. 让「按异常特征筛选并导出」成为一次点击就能完成的动作，覆盖计费口径、流结束状态、
   数值阈值、重试与配额钳制四类特征。
2. 把判定异常所依赖的字段，从 `other` 的 JSON 黑箱里**拍平成可排序、可筛选的独立列**。
3. 给每一列定义「是否可以出现在发给客户的文件里」，并据此重整内置模板。
4. 提供按维度聚合的汇总导出，回答「这段时间按模型/用户/天，各花了多少」这类统计问题。

### 2.2 范围

**在范围内**

- `model.LogExportFilter` 扩展（SQL 下推条件 + 行级条件）
- 导出扫描管线新增行级过滤与派生的 `anomaly_flags` 列
- 14 个新导出列（流式结算诊断 + 重试计数 + 异常标记）
- 列受众分级（新增 `Audience` 字段）与内置模板重整
- 前端：筛选区新增「异常」板块与快捷筛选 chip；模板下拉按用途分组并标注受众；
  使用日志行内的「查同类」快捷入口（§16）
- **阶段二**：聚合汇总导出（§10）

**不在范围内**

- `/api/log/self/export`（普通用户自助导出）**保持现状不变**——已确认走「管理员代客导出」。
- 相关的计费 bug 修复（估算前剥离 data URL、估算输出加硬上限）属于另一条线，
  本文只负责「能把受影响的单子捞出来」。
- AI 关系链路（`relay/`）零改动。

### 2.3 本轮已确认的决策

| # | 决策 | 取值 |
|---|---|---|
| 1 | 客户对账单的字段深度 | **可复算集**：基础 + token 明细 + 倍率单价 + 费用；不含上游标识、不含内部计费路径 |
| 2 | 用户侧入口 | **管理员代客导出**；`/log/self/export` 不动 |
| 3 | 统计类模板形态 | **两者都要**：阶段一做明细列子集模板，阶段二做聚合汇总 |
| 4 | 异常口径 | 计费用量来源、流异常结束、数值阈值、重试与配额钳制——**四类全做** |

---

## 3. 数据来源：`other` 里的异常特征长什么样

全部来自 `service/log_info_generate.go` 写入的 `other`，无需改动写入侧。

### 3.1 `other.stream_result`（公有，仅流式请求有）

序列化自 `relay/common/stream_outcome.go` 的 `StreamOutcome`：

```json
{
  "failed": true,
  "client_gone": false,
  "effective_content": true,
  "confirmed_usage": false,
  "usage_source": "estimated",
  "settlement_state": "settled",
  "intended_quota": 3117668,
  "reserved_quota": 1000
}
```

`usage_source` 四个取值由 `StreamOutcome.SelectUsageSource()` 决定：

| 取值 | 含义 | 是否可疑 |
|---|---|---|
| `upstream` | 采用上游下发的用量 | 正常 |
| `estimated` | **上游没给用量，按本地估算收费** | **高度可疑**——本次事故的直接特征 |
| `mixed` | 部分上游、部分估算 | 可疑 |
| `none` | 不计费 | 需关注（可能是本该收费却没收） |

非流式请求没有 `stream_result`，该列为空。**空不等于异常**，筛选与标记逻辑都必须区分
「字段缺失」和「取值异常」。

### 3.2 `other.stream_status`（公有，仅流式请求有）

```json
{ "status": "error", "end_reason": "upstream_incomplete", "error_count": 1 }
```

`status` 只有 `ok` / `error` 两个取值；`end_reason` 是自由字符串（`upstream_incomplete`、
`client_disconnect`、`response_conversion_error`、`upstream_json_error` 等）。
因为取值随协议演进增加，筛选端**不做白名单校验**，按用户输入精确匹配即可。

### 3.3 `other.admin_info`（管理员可见）

- `use_channel`：重试链数组，`len > 1` 即发生过换渠道重试。
- `quota_saturation`：配额计算发生饱和截断时才存在（`common/quota_math.go` 的 clamp 审计）。
  **它存在本身就是异常**——意味着算出来的配额超过了 int 上限被钳住。

### 3.4 logs 表真实列

`quota`、`prompt_tokens`、`completion_tokens`、`use_time`、`is_stream` —— 这几项可以直接下推 SQL。

---

## 4. 字段受众分级（回答「哪些字段可以给用户」）

### 4.1 既有的权威边界

本仓已有一条硬边界，由 `model/log_other.go` 强制：

| 存放位置 | 谁能看到 | 强制点 |
|---|---|---|
| `other` 顶层（`SetPublic`） | **日志属主本人** | `/log/self` 返回给用户 |
| `other.admin_info`（`SetAdmin`） | 管理员 | `formatUserLogs` → `logOtherVisibilityUser` 剥离 |
| `other.root_info`（`SetRoot`） | root | `FormatAdminLogs` 剥离 |
| `other.audit_info`（`SetAudit`） | 管理员 | 同上 |
| `channel_id`/`channel_name`/`channel_type`/`reject_reason` | 管理员 | `legacySensitiveLogOtherKeys` 兜底剥离 |

导出列注册表的 `AdminOnly` / `RootOnly` 已经与这条边界对齐。**所以「技术上能不能给用户」早有答案，
本次要定的是「该不该主动写进发给客户的文件」**——这是两件事：用户在自己的日志详情页点开能看到某字段，
不代表我们要把它批量导出成一份文件发过去。

### 4.2 新增的 `Audience` 维度

给 `LogExportColumn` 增加一个字段：

```go
// Audience 标记该列是否可以出现在发给客户的文件里。
// 它是比 AdminOnly 更严的一道口径：AdminOnly 管「技术上谁能读到」，
// Audience 管「我们愿不愿意主动把它写进客户手里的文件」。
// AdminOnly / RootOnly 的列一律是 AudienceInternal，无需重复标注。
type LogExportAudience uint8

const (
    LogExportAudienceInternal LogExportAudience = iota // 默认：仅内部
    LogExportAudienceCustomer                          // 可发给客户
)
```

默认值是 `Internal`，**新增列不标注就自动落在安全的一侧**——这是刻意选的零值方向，
避免将来有人加了列忘了标注就泄露出去。

### 4.3 逐列裁决表

只列出「公有字段」这一档（`AdminOnly` 的列一律 Internal，不再逐行罗列）。

**收录进客户对账单（`Customer`）**

| 列 | 理由 |
|---|---|
| `created_at` | 对账的时间锚点 |
| `request_id` | 客户报障时的唯一凭据。这是本站自己的请求 ID，不含上游信息 |
| `model_name` | 客户点的是哪个模型 |
| `token_name`、`group` | 费用归属维度，客户自己配的 |
| `prompt_tokens`、`completion_tokens`、`total_tokens` | 计费基数 |
| `cache_tokens`、`cache_creation_tokens`(+`_5m`/`_1h`) | 缓存命中直接影响单价，不给客户就复算不出来 |
| `text_input`/`text_output`/`audio_input`/`audio_output` | 多模态按类分别计价，缺一项就对不上 |
| `image_output`（图片输入 token）、`image_cache_tokens` | 同上 |
| `billing_tokens` | 计费 token 拆分总表 |
| `model_ratio`、`completion_ratio`、`group_ratio`、`user_group_ratio` | 倍率，复算必需 |
| `cache_ratio`、`cache_creation_ratio`(+`_5m`/`_1h`)、`audio_ratio`、`audio_completion_ratio`、`image_ratio` | 同上 |
| `model_price`、`billing_unit`、`fixed_price` | 按次固定价的复算依据 |
| `image_count` | 按张计费的复算依据 |
| `tool_surcharges` | 工具调用附加费，不给就是一笔说不清的差额 |
| `usage_facts` | 任务类（视频/图片）计费的用量事实 |
| `matched_tier` | 阶梯计费命中的档位。**客户按哪一档被收费，是他有权知道的** |
| `quota`、`cost_usd` | 结果 |

**排除（`Internal`），逐条给理由**

| 列 | 排除理由 |
|---|---|
| `upstream_model_name`、`is_model_mapped` | 暴露模型映射关系，等于告诉客户我们把他的请求转给了谁 |
| `upstream_request_id` | 上游供应商的请求 ID，泄露上游身份 |
| `ip` | 是客户自己的 IP，但批量导出 IP 属于个人数据，没有对账必要性 |
| `content` | 「详情」字段会携带错误信息与内部提示文本 |
| `billing_source`、`billing_mode` | 钱包/订阅、计费路径属于内部计费实现 |
| `request_rules` | 计费规则表达式原文，等于把定价策略发出去 |
| `stream_result`/`stream_status` 系列、`frt`、`tokens_per_sec`、`use_time`、`is_stream` | 诊断与性能字段，与对账无关；放进去只会引出「为什么这条慢」的二次追问 |
| `login_method`、`user_agent`、`request_path` | 登录审计字段，与用量对账无关 |

**两处曾有争议的判断，已确认收录**（2026-09-20）：

1. `matched_tier`——客户按哪一档阶梯价计费，属于他该知道的信息，收录。
2. `request_id`——本站自己的请求 ID，不含上游信息，是客户报障的唯一凭据，收录。

---

## 5. 阶段一之一：筛选扩展

### 5.1 `LogExportFilter` 扩展

```go
type LogExportFilter struct {
    // ── 既有字段（不动）────────────────────────────
    LogType        int    `json:"type"`
    StartTimestamp int64  `json:"start_timestamp"`
    EndTimestamp   int64  `json:"end_timestamp"`
    ModelName      string `json:"model_name"`
    Username       string `json:"username"`
    TokenName      string `json:"token_name"`
    ChannelId      int    `json:"channel"`
    Group          string `json:"group"`
    UserId         int    `json:"user_id"`

    // ── 新增：SQL 下推（logs 表真实列）───────────────
    // 一律用指针：0 是有意义的取值（quota_max=0 就是「找零费用的行」），
    // 非指针 + omitempty 会把它静默丢掉（Rule 5）。
    QuotaMin            *int  `json:"quota_min,omitempty"`
    QuotaMax            *int  `json:"quota_max,omitempty"`
    PromptTokensMin     *int  `json:"prompt_tokens_min,omitempty"`
    CompletionTokensMin *int  `json:"completion_tokens_min,omitempty"`
    UseTimeMin          *int  `json:"use_time_min,omitempty"`
    UseTimeMax          *int  `json:"use_time_max,omitempty"`
    IsStream            *bool `json:"is_stream,omitempty"`

    // ── 新增：行级过滤（other JSON）─────────────────
    UsageSource     []string `json:"usage_source,omitempty"`     // estimated/mixed/none/upstream
    StreamEndReason []string `json:"stream_end_reason,omitempty"`
    SettlementState []string `json:"settlement_state,omitempty"`
    StreamErrorOnly bool     `json:"stream_error_only,omitempty"` // stream_status.status == "error"
    MinRetryCount   *int     `json:"min_retry_count,omitempty"`   // len(admin_info.use_channel) >= n
    QuotaSaturated  bool     `json:"quota_saturated,omitempty"`   // admin_info.quota_saturation 存在

    // AnomalyPreset 一键「可疑计费」：等价于
    // usage_source ∈ {estimated, mixed} OR stream_status.status == "error"
    //   OR 重试次数 > 1 OR quota_saturation 存在。
    // 与细粒度条件是 **AND** 关系（都填就都要满足），不是替代关系。
    AnomalyPreset bool `json:"anomaly_preset,omitempty"`
}
```

**向后兼容**：任务状态整体 JSON 序列化进 Redis。新字段全部 `omitempty` + 指针/零值语义，
升级前创建的任务反序列化后新字段为 nil/false = 不过滤，行为与升级前完全一致。

### 5.2 两类条件的分工

| 类别 | 落点 | 理由 |
|---|---|---|
| 数值 / 布尔 | `applyLogExportFilter` 追加 `WHERE` | 是 logs 表真实列，加在已有时间窗口 range scan 之上只是多一个过滤谓词，**不增加扫描量，还能减少返回行数** |
| `other` JSON 条件 | 扫描循环内的 Go 行级过滤 | 三库通用，语义精确 |

**被否决的方案：`other LIKE '%"usage_source":"estimated"%'` 预筛。**
它能让数据库少返回一批行，看着很划算，但它的正确性依赖「JSON 编码后字段名与值之间没有空格」
这个隐含前提。一旦序列化实现变化（换 JSON 库、开启缩进），LIKE 会静默不匹配，
导出结果**少行且不报错**——这是最坏的一种 bug。不采用；Go 侧过滤是唯一权威判定。

### 5.3 行级过滤的代价（必须写进 UI）

行级过滤意味着：

1. **强制 `NeedOther`**：即使用户选的列一个都不依赖 `other`，只要带了 JSON 条件，
   SQL 就必须 `SELECT logs.other` 并逐行解析。`other` 是行宽的绝对大头，
   这会把单批的网络传输与反序列化开销拉高一个量级。
2. **扫描量不减**：JSON 条件没有索引可走，31 天的范围就是要扫 31 天。
   加了异常筛选的任务，**耗时与不加筛选的全量导出相同**，只是产出的文件小得多。

这两点必须在新建导出抽屉里明说，否则管理员会以为「只导 300 行怎么跑了 8 分钟」是故障。
文案见 §8.3。

### 5.4 估算与进度的语义变化

| 指标 | 变化 |
|---|---|
| `GET /estimate` 的 `rows` | 只能算 SQL 下推部分 → 变成**上限**。UI 文案从「约 N 行」改为「最多 N 行」 |
| xlsx 可行性判断 | 用上限判断，方向是保守的（可能把实际能用 xlsx 的判成要降级），可接受 |
| `Progress` | **不受影响**——本来就按时间轴推进（`logExportProgress`），不依赖行数 |
| `RowCount` | 语义变成「命中并写出的行数」 |
| `ScannedRows`（**新增**） | 扫描过的行数。没有它，管理员看到「扫了 8 分钟只出 340 行」会以为出了故障 |

任务列表与详情同时展示两者：`340 / 已扫描 1,204,551`。

---

## 6. 阶段一之二：新增导出列

新增列分组 `LogExportGroupDiagnostic = "diagnostic"`（前端标签「诊断」），
放在 `performance` 与 `admin` 之间。

| 列 key | 来源 | 权限 | 说明 |
|---|---|---|---|
| `usage_source` | `stream_result.usage_source` | 公有 | **本次事故的核心列**：上游/估算/混合/不计费 |
| `settlement_state` | `stream_result.settlement_state` | 公有 | pending/settled/released/failed/partial |
| `stream_failed` | `stream_result.failed` | 公有 | 流是否异常结束 |
| `client_gone` | `stream_result.client_gone` | 公有 | 下游断开 |
| `effective_content` | `stream_result.effective_content` | 公有 | 是否已交付有效内容（估算收费的资格前提） |
| `confirmed_usage` | `stream_result.confirmed_usage` | 公有 | 是否拿到上游确认用量 |
| `intended_quota` | `stream_result.intended_quota` | 公有 | 策略期望收取的额度 |
| `reserved_quota` | `stream_result.reserved_quota` | 公有 | 实际预扣的额度。与 `intended_quota`、`quota` 三者对照可查结算差额 |
| `stream_status_text` | `stream_status.status` | 公有 | ok / error。拍平自既有的 `stream_status` JSON 列 |
| `stream_end_reason` | `stream_status.end_reason` | 公有 | `upstream_incomplete` 等 |
| `stream_error_count` | `stream_status.error_count` | 公有 | 流内错误计数 |
| `stream_diagnostic_attempt` | `other.stream_diagnostic_attempt` | 公有 | 第几次尝试留下的诊断 |
| `retry_count` | `len(admin_info.use_channel)` | AdminOnly | 重试链长度。既有的 `retry_chain` 是文本，无法按次数排序筛选 |
| `anomaly_flags` | 派生 | AdminOnly | 命中的异常规则名，逗号分隔 |

既有的 `stream_status` 整块 JSON 列**保留不动**（有人的自定义模板在用它）。

### 6.1 `anomaly_flags` 的取值与一致性要求

取值（逗号分隔，顺序固定，便于按前缀排序分组）：

```
estimated_usage | mixed_usage | no_usage | stream_error | settlement_unsettled | retried | quota_saturated
```

**硬性要求：判定 `anomaly_flags` 的函数，必须与 `AnomalyPreset` 筛选用的是同一个函数。**
两边各写一份判断，迟早会漂移，结果是「筛出来的行标记为空」或「标记了却筛不到」——
排查功能自己出这种问题，比没有这个功能更糟。

实现上抽一个 `func logAnomalyFlags(l *Log, ctx *rowCtx) []string`，
筛选端判空、渲染端 join，只此一处。

### 6.2 i18n

每列需要 4 处登记（后端 i18n 契约由 `i18n/consistency_test.go` 强制）：

- `i18n/locales/en.yaml`、`zh-CN.yaml`、`zh-TW.yaml` 各一条 `log_export.col.<key>`
- `i18n/keys.go` 一个 `MsgLogExportCol<Name>` 常量

新增分组标签需要前端 `column-picker.tsx` 的 `GROUP_LABELS` / `GROUP_ORDER` 各加一项，
以及前端 locale 的 `Diagnostics` 一条。

---

## 7. 阶段一之三：模板重整

### 7.1 模板结构扩展

```go
type BuiltinLogExportTemplate struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Columns   []string `json:"columns"`
    IsDefault bool     `json:"is_default"`
    // Purpose 用途分组：reconciliation / analytics / diagnostic / audit。
    // 前端按它给模板下拉分组，避免 9 个模板平铺成一长条。
    Purpose   string `json:"purpose"`
    // Audience customer 表示该模板的列全部是 AudienceCustomer，
    // 可以直接把导出文件发给客户；前端据此打「可发给客户」徽章。
    Audience  string `json:"audience"`
}
```

`Audience == "customer"` 的模板，**其列清单必须全部是 `AudienceCustomer`**。
这条由单元测试强制（§11.1），不靠人工维护——否则某天有人往对账单模板里加了一列渠道名，
没人会发现。

### 7.2 重整后的模板清单

| ID | 名称 | Purpose | Audience | 变化 |
|---|---|---|---|---|
| `builtin:as_displayed` | 页面所见 | analytics | internal | 不变（仍是默认） |
| `builtin:customer_invoice` | **客户对账单** | reconciliation | **customer** | **新增**，§4.3 的 Customer 全集 |
| `builtin:billing` | 计费明细（内部） | reconciliation | internal | 不变，仅补 Purpose/Audience 标注 |
| `builtin:operations` | **运营统计** | analytics | internal | **新增**，见下 |
| `builtin:anomaly` | **异常排查** | diagnostic | internal | **新增**，见下 |
| `builtin:performance` | 性能诊断 | diagnostic | internal | 不变 |
| `builtin:legacy` | 旧版导出 | reconciliation | internal | 不变 |
| `builtin:audit` | 充值与历史审计 | audit | internal | 不变 |
| `builtin:full` | 全部列 | diagnostic | internal | 不变 |

**`builtin:operations`（运营统计明细子集）**

```
created_at, username, user_id, group, token_name, model_name,
is_stream, prompt_tokens, completion_tokens, total_tokens,
cache_tokens, cache_creation_tokens, quota, cost_usd, use_time
```

不含渠道、不含诊断、不含 `other` 里的倍率——**刻意不依赖 `other`**，
于是 SQL 不 `SELECT other`、不做 JSON 解析，这是所有模板里最快、文件最小的一个，
适合拉整月数据丢进 Excel 透视。

**`builtin:anomaly`（异常排查）**

```
created_at, request_id, username, model_name,
channel_id, channel_name, retry_count, retry_chain,
anomaly_flags, usage_source, settlement_state,
stream_status_text, stream_end_reason, stream_error_count,
confirmed_usage, effective_content, client_gone,
prompt_tokens, completion_tokens, quota, cost_usd,
intended_quota, reserved_quota, quota_saturation,
use_time, frt, content
```

配合 §5 的异常筛选，这个组合就是本次事故排查该有的样子：
筛 `usage_source=estimated`，导出后按 `cost_usd` 倒序，一眼看到所有被多收的单子。

### 7.3 自定义模板

不加 `Audience`。用户自己存的模板由他自己负责，加一道标记只会让人以为系统替他把过关。
前端在列选择器里对 `AudienceInternal` 的列标一个小的「内部」角标，靠标注而非阻断。

---

## 8. API 契约

### 8.1 `GET /api/log/export/columns`

响应新增：

```json
{
  "columns": [
    { "key": "usage_source", "label": "Usage Source",
      "group": "diagnostic", "admin_only": false, "audience": "internal" }
  ],
  "builtin_templates": [
    { "id": "builtin:customer_invoice", "name": "Customer Invoice",
      "columns": ["..."], "is_default": false,
      "purpose": "reconciliation", "audience": "customer" }
  ],
  "anomaly_presets": [
    { "key": "suspicious_billing", "label": "Suspicious billing" },
    { "key": "estimated_usage",    "label": "Estimated usage" },
    { "key": "stream_error",       "label": "Stream ended abnormally" },
    { "key": "retried",            "label": "Retried across channels" },
    { "key": "quota_saturated",    "label": "Quota clamped" }
  ]
}
```

`anomaly_presets` 由后端下发而不是前端硬编码：判定规则住在后端，
前端硬编码一份迟早和后端漂移（与 §6.1 同一个理由）。

### 8.2 `POST /api/log/export/jobs`

请求体新增 §5.1 的全部筛选字段，命名与 JSON tag 一致。校验：

| 条件 | 处理 |
|---|---|
| `quota_min > quota_max`（两者都给） | `MsgLogExportRangeInvalid` 同族的新 key，400 语义走 `ApiErrorI18n` |
| `usage_source` 含未知取值 | 拒绝，列出合法取值（这个枚举是闭集，可以校验） |
| `stream_end_reason` / `settlement_state` | **不校验取值**，随协议演进增加，白名单只会挡住新出现的异常 |
| 数组长度 > 20 | 拒绝，防止拼出畸形条件 |
| `min_retry_count < 0` | 拒绝 |

### 8.3 `GET /api/log/export/estimate`

响应新增 `"upper_bound": true`——当请求里带了任何行级条件时为 true，
前端据此把「约 N 行」改成「最多 N 行，实际以异常命中为准」。

### 8.4 审计

`logExportFilterScope` 必须把新条件压进审计摘要。现在它只拼了 7 个老字段，
加了异常筛选后，审计记录里「导了什么」会漏掉最关键的部分。

---

## 9. 数据模型与配置

### 9.1 数据模型

**不新增表、不新增列、不新增索引、无迁移。**

- 筛选条件存在 Redis 的任务状态里（`LogExportFilter` 是 `LogExportJob` 的内嵌字段）。
- 自定义模板表 `log_export_templates` 结构不变——`Audience` 只加在内置模板上。
- 数值下推条件（`quota`、`*_tokens`、`use_time`）**不加索引**：它们永远与时间窗口
  同时出现，时间窗口已经把索引区间限死（`idx_created_at_id`），
  再为它们建索引只会增加 logs 表的写入放大——而 logs 是关系链路的写入目标（§12）。

### 9.2 配置（Rule 12）

`log_export_setting` 新增两项：

| 字段 | 默认 | 说明 |
|---|---|---|
| `row_filter_batch_size` | `1500` | 带行级过滤时的单批读取行数。比默认 `batch_size=3000` 小一半——每行都要解析 `other`，同样批次的 CPU 与内存占用翻倍，缩小批次让闸门的反应粒度保持不变 |
| `max_filter_values` | `20` | 单个数组型条件的取值个数上限（§8.2） |

沿用既有约定：**在使用点现调 getter，不快照**，因此这两项对运行中的任务即时生效
（`row_filter_batch_size` 从下一批开始生效）。

---

## 10. 阶段二：聚合汇总导出

### 10.1 形态

新增任务模式 `mode: "summary"`（既有明细导出为 `mode: "detail"`，默认值，向后兼容）。

- **维度**（多选，至少一个）：`date`(天) / `username` / `group` / `model_name` /
  `token_name` / `channel`(AdminOnly)

  **时间粒度只做「天」，不做「小时」**（2026-09-20 确认）。小时把组合数直接乘 24，
  `hour × username × model` 在几千活跃用户下轻易撞上 §10.4 的组合数上限而失败，
  为此要在 UI 里加一层勉强的劝阻——不如不给这个选项。
  真要看峰谷分布，是监控页面该解决的问题，不是导出。
- **指标**（固定全给）：`calls`、`prompt_tokens`、`completion_tokens`、`total_tokens`、
  `cache_tokens`、`quota`、`cost_usd`
- 筛选条件与明细导出**完全共用**——包括异常筛选。
  「过去 30 天按模型统计，估算计费的单子各多收了多少」就是一次勾选。

### 10.2 实现：在导出管线里做流式聚合

**不用 SQL `GROUP BY`。** 理由：

1. 31 天的 logs 表做 hash aggregate，中间结果全落在数据库进程的内存里，
   而这个库同时在承接关系链路的日志写入（§12）。把内存压力推给数据库，
   等于把导出的代价转嫁到关系链路上。
2. `GROUP BY` 是一条长事务查询，**绕过了现有的全部资源治理**——限速令牌桶、
   CPU 水位闸门、批间休眠对它统统无效。一旦跑起来就只能等它结束。

改为复用**已有的 keyset 扫描循环**，在 Go 侧用 map 累加：

```go
type summaryKey struct{ Date, Username, Group, Model, Token string; ChannelId int }
type summaryRow struct {
    Calls                             int64
    PromptTokens, CompletionTokens    int64
    CacheTokens, Quota                int64 // 必须 int64：月度求和远超 int32
}
```

扫描结束后按维度排序写出。这样限速、CPU 闸门、超时、断点全部原样生效。

### 10.3 `quota_data` 表为什么不用

`model/usedata.go` 的 `QuotaData` 已按 用户×模型×分组×令牌×渠道×小时 预聚合，
看起来是现成的数据源，但它：

- 只有 `TokenUsed`（token 总数），**没有 prompt/completion 拆分，没有缓存 token**；
- 时间桶固定按小时且按服务器时区落库，无法按导出时区重新切「天」；
- 不含任何异常口径字段，与 §5 的筛选无法组合。

用它就等于只能出一张比现有页面还弱的表。不采用。

### 10.4 内存上限

聚合结果驻留内存，上限由维度组合数决定。加配置 `summary_max_groups`（默认 `200000`，
约 30 MB）；超限时任务失败，文案提示「维度组合过多，请减少维度或缩小时间范围」——
而不是 OOM 掉整个进程。

按实际规模估算：`date × model` 31 天 × 200 个模型 = 6,200 组；
`date × username × model` 在 5,000 活跃用户下可达千万级——所以这个上限是必需的，
且默认值必须偏小，宁可让人重试。

### 10.5 输出

仍走既有分片写入器（`model/log_export_writer.go`），格式与明细导出一致（csv.gz / xlsx）。
聚合结果通常远小于 `rows_per_file`，实际产出单文件。

---

## 11. 测试计划（Rule 15）

### 11.1 必须有的测试

集中在 `model/log_export_columns_test.go` 与 `model/log_export_scan_test.go`
两个既有文件里扩展（Rule 15.9：不为一个功能把测试散到三个包）。

| 测试 | 覆盖 |
|---|---|
| `TestLogExportTemplates_CustomerAudienceIsClosed` | **最重要的一条**：遍历 `Audience=="customer"` 的模板，断言其每一列都是 `AudienceCustomer`。防止将来往对账单里混进内部列 |
| `TestLogExportColumns_NewColumnsResolve` | 14 个新列都能解析、都有 i18n key、`AdminOnly` 标注正确 |
| `TestLogAnomalyFlags` | 表驱动，每个 flag 一个用例 + 组合用例 + **非流式行（无 `stream_result`）必须为空** |
| `TestLogExportFilter_RowLevel` | 判定与筛选共用同一函数的一致性：对同一批构造行，筛选结果集 == flags 非空的行集 |
| `TestApplyLogExportFilter_NumericPushdown` | 边界值：`quota_min` 等于/小于/大于行值；`quota_max=0`（指针非 nil 的零值必须生效，Rule 5） |
| `TestLogExportFilter_BackwardCompat` | 反序列化升级前的任务 JSON，新字段为 nil，行为与升级前一致 |
| `TestLogExportSelectFields_ForcesOtherWhenRowFilter` | 带行级条件时 `SELECT` 必须含 `other` |
| `TestLogExportSummary_Aggregate`（阶段二） | 聚合正确性 + `int64` 不溢出 + 超 `max_groups` 时失败而非 OOM |

数据库测试按 Rule 15.5 走真实 MySQL + PostgreSQL（见记忆里的测试环境约定），
数值下推条件三库各跑一遍。

### 11.2 回归

- 不带任何新条件时，导出结果与升级前**逐字节一致**（用同一份 fixture 对比）。
- `builtin:as_displayed` / `legacy` / `billing` / `performance` / `audit` / `full`
  六个既有模板的列清单不变。

---

## 12. Main Chain Impact 与共享资源审计（Rule 0）

**关系链路改动：零。** 本功能只读 logs 表，不在 `relay/` 路径上执行任何代码。

| 共享资源 | 关系链路是否也用 | 冲突分析 |
|---|---|---|
| `logs` 表（LOG_DB） | **是**——关系链路异步批量写入（`relay-log-async-batch`） | 本功能只增加 `SELECT` 的**谓词**与**行级 CPU**，扫描的索引区间与今天的全量导出完全相同，不新增锁、不新增写入。**明确不为数值条件加索引**，避免增加关系链路写入侧的索引维护成本 |
| `channels` 表 | 是（读渠道配置） | 沿用既有的查询时解析 + 跨任务 map 缓存，每个 `channel_id` 至多一次查询。本次不改 |
| Redis `logexport:*` | 否 | 键名空间与关系链路完全隔离，仅任务状态与信号量。新增字段只是让同一个 key 的 value 变长几十字节 |
| CPU | 共享 | **这是本功能唯一的实质风险**：行级过滤把每行的 CPU 成本从「格式化几个字段」提高到「解析一整个 `other` JSON」。缓解手段有三层：① 既有 CPU 软/硬水位闸门（70%/85%）原样生效，CPU 一高就自动让出；② `row_filter_batch_size` 减半，让闸门的反应粒度不变；③ 行速率令牌桶（`max_rows_per_sec`）本来就是按行计的，行变贵不会突破速率上限。一键刹车仍是 `cpu_hard_limit=0` |
| 内存 | 共享 | 明细导出：单批 1500 行 × 含 `other` 的行宽，比现在小。聚合导出：受 `summary_max_groups` 硬限约 30 MB |
| 数据库连接池 | 是 | 不变——导出始终只占 1 个连接，`max_concurrent_jobs` 默认 1 |

**100k RPM 下的行为**：本功能不在请求路径上，不随 RPM 变化。唯一的耦合是它与关系链路
共享数据库和 CPU，而这两者已由既有闸门治理，本次只是让单行更贵——闸门是按资源水位
而非按行数触发的，因此治理效果不变。

---

## 13. 错误处理（Rule 9 / Rule 6）

| 场景 | 处理 |
|---|---|
| `quota_min > quota_max` | `ApiErrorI18n`，文案「最小值不能大于最大值」 |
| `usage_source` 取值非法 | `ApiErrorI18n` 带上合法取值列表 |
| 异常筛选命中 0 行 | **不是错误**：正常 `ready`，产出仅含表头的文件，页面提示「该时间范围内没有命中异常的记录」——这恰恰是好消息，文案不能写成失败 |
| 行级过滤时任务超时 | 沿用既有 `timeout` 错误码，但文案补一句「异常筛选需要扫描整个时间范围，请缩短范围后重试」 |
| 聚合维度组合超限 | 新错误码 `too_many_groups`，文案「维度组合过多，请减少维度或缩小时间范围」 |

所有文案走 `i18n/locales/*.yaml`，前端错误码 → 文案映射沿用 `ERROR_MESSAGES` 的既有模式。

---

## 14. 实施顺序

| 阶段 | 内容 | 可独立交付 |
|---|---|---|
| 1 | 14 个新列 + `anomaly_flags` + i18n + 列测试 | 是——先让字段可见，页面不改也能用 `builtin:full` 导出 |
| 2 | `LogExportFilter` 扩展 + SQL 下推 + 行级过滤 + `ScannedRows` | 是 |
| 3 | `Audience` 分级 + 模板重整（含 `customer_invoice` / `operations` / `anomaly`） | 是 |
| 4 | 前端：异常筛选板块、快捷 chip、模板分组与受众徽章、扫描行数展示 | 依赖 1-3 |
| 5 | **阶段二**：聚合汇总导出 | 独立 |

1-4 是一个完整的可用增量；5 单独交付。

---

## 15. 决策记录

2026-09-20 全部确认，本文无待确认项。

| # | 事项 | 结论 |
|---|---|---|
| 1 | `matched_tier` / `request_id` 是否进客户对账单 | **都收录**（§4.3） |
| 2 | `builtin:legacy`（旧版 14 列）去留 | **保留不动**。它是老用户拿惯了的文件结构，留着的成本只是模板下拉多一项 |
| 3 | 聚合的时间粒度 | **只做「天」**，不做小时（§10.1） |
| 4 | `summary_max_groups` 默认值 | **200,000**（§10.4）。它是配置项，跑出问题随时热改 |
| 5 | 「查同类」快捷入口 | **做**（§16） |

---

## 16. 「查同类」快捷入口

在使用日志页面的某一行上提供「查同类」，点击后带着该行的异常特征直接跳到导出抽屉，
筛选条件已经填好。排查时从「发现一笔可疑」到「捞出全部同类」只需一步。

**后端零改动**——它只是前端把该行的特征拼成导出抽屉的初始筛选：

| 该行的特征 | 带入的筛选条件 |
|---|---|
| `other.stream_result.usage_source` 非 `upstream` | `usage_source = <该值>` |
| `other.stream_status.end_reason` 非空且 `status == "error"` | `stream_end_reason = <该值>` |
| `admin_info.use_channel` 长度 > 1 | `min_retry_count = 2` |
| `admin_info.quota_saturation` 存在 | `quota_saturated = true` |
| 总是带上 | `model_name` = 该行模型；时间范围 = 该行时刻前后各 3 天（夹在 `admin_max_range_sec` 内） |

**刻意不带入的**：用户名与令牌。排查的目标是「这个异常影响了多少人」，
预填用户名会把视野锁死在最先被发现的那一个人身上——这恰恰是本次事故排查走过的弯路。

该行一个异常特征都没有时，「查同类」不显示——按钮在那儿却点不出东西比没有更糟。
模板默认选 `builtin:anomaly`。

---

## 17. 实现记录（2026-09-20）

阶段一（1-4）与阶段二（聚合汇总）均已落地。

### 17.1 文件清单

**后端**

| 文件 | 改动 |
|---|---|
| `model/log_export_anomaly.go` | **新增**。异常判定的唯一入口 `logAnomalyFlags`，以及筛选侧的 `logExportRowMatchesAnomaly` |
| `model/log_export_columns.go` | 新增 `diagnostic` 分组与 14 个诊断列；`LogExportAudience` 与 `logExportCustomerColumns` 裁决清单；模板扩展 `Purpose`/`Audience`；新增 3 个内置模板 |
| `model/log_export_scan.go` | `LogExportFilter` 扩展 7 个数值条件 + 6 个 other 条件；`HasRowFilter`/`matchesRow`；数值条件下推 SQL |
| `model/log_export_summary.go` | **新增**。流式聚合累加器与 `writeLogExportSummary` |
| `model/log_export_job.go` | `ScannedRows`、`Mode`/`SummaryDims`；行级过滤接入写出循环 |
| `controller/log_export.go` | 筛选条件解析与校验；列目录下发 audience / anomaly_kinds / summary_dimensions；审计摘要补全 |
| `i18n/` | 14 个诊断列 + 14 个聚合表头 + 5 条错误文案 × 3 locale |

**前端**

| 文件 | 改动 |
|---|---|
| `export/types.ts` | `ExportAudience`/`ExportTemplatePurpose`/`SummaryDimension`/`ExportMode`；筛选与任务字段扩展 |
| `export/components/new-export-sheet.tsx` | 「排查筛选」折叠区、产出形态切换、聚合维度选择、模板按用途排序与受众徽章 |
| `export/components/column-picker.tsx` | `diagnostic` 分组；内部列角标 |
| `export/index.tsx` | 扫描行数展示；`too_many_groups` 错误文案；「查同类」预填 |
| `components/dialogs/details-dialog.tsx`、`log-detail-body.tsx` | 「查同类」按钮与跳转 |
| `routes/_authenticated/usage-logs/$section.tsx` | 三个「查同类」搜索参数 |

### 17.2 与设计的差异

1. **「查同类」不再传异常种类，改传该行可观察的字段值**（`usage_source` /
   `stream_end_reason` / 重试链长度）。设计里写的是「带该行的异常特征」，
   实现时发现那要求前端复制一份异常判定规则——正是 §6.1 明令禁止的事。
   改成传字段值后，它与「按这一行的模型筛选」是同一性质的操作，前端零规则。
2. **聚合指标去掉了 `cache_tokens`**。它住在 `other` 里，为它逐行解析 JSON 会把
   聚合导出拖慢约 45 倍（实测 10.3µs/行 vs 0.23µs/行）；不解析又只能恒为 0，
   而一列恒为 0 比没有这列更误导人。需要缓存明细请走明细导出。
3. **`other` 的解析缓存改为按行归属自动失效**（`rowCtx.otherOwner`）。
   原本 `Render` 在每行开头无条件重置缓存，而行级筛选要在渲染**之前**读 `other`——
   保持原样会导致每行解析两次，而解析占渲染成本的九成。
4. **聚合模式不参与 xlsx 行数降级**。`est_rows` 估的是要扫描的日志行数，
   而聚合产出的是维度组合数：扫 100 万行可能只出 50 行汇总。
   拿扫描量否决 xlsx，会让所有大范围汇总导出都拿不到 Excel 文件。

### 17.3 实现期发现并修掉的既有缺陷

**`target_username` / `target_user_id` 两列在三个 locale 里都没有表头译文。**
新加的 `TestLogExportColumns_EveryColumnHasHeaderTranslation` 一跑就抓到了。
缺失时 `i18n.Translate` 返回 key 本身（那是避免 panic 的兜底），
于是审计模板导出的表头会是 `log_export.col.target_username` 这样的裸 key——
文件照常生成、任务照常成功、没有任何报错，只有拿到文件的人才会发现。
已补齐三个 locale 与 `Msg*` 常量，并把这条守卫永久留在测试里。

### 17.4 测试

| 文件 | 覆盖 |
|---|---|
| `model/log_export_anomaly_test.go` | 13 个异常判定用例（含「非流式缺字段不等于异常」）；筛选与标记一致性；行级筛选 AND/OR 语义；`other` 缓存按行失效 |
| `model/log_export_columns_test.go` | **客户对账单闭合性**（受众标记与模板列清单不得脱节）；逐条排除敏感字段；运营模板不依赖 `other`；每列都有表头译文 |
| `model/log_export_summary_test.go` | 聚合正确性、时区切天、int64 不溢出、组合数超限失败而非 OOM、端到端、聚合与异常筛选组合 |
| `controller/log_export_test.go` | 7 个非法条件用例；演进型取值不做白名单；**显式传入的 0 必须保留**（Rule 5）；列目录下发受众与词表 |
