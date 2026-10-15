# 价格巡检：动态计费适配与快速改价

状态：完整功能已实现，相关自动化验证通过；环境验证限制见 §13。
日期：2026-10-13。

## 1. 目标与范围

让管理员在价格巡检中完成“发现差异 → 看清具体阶梯/分项 → 修改 → 预览 → 保存 → 重新比较”，无需跳离当前模型。以表达式作为计费事实来源，兼容历史倍率和按次价格。

一次完整交付，以下能力共同验收，不拆分交付阶段：

- 对齐上游模型价格读写契约；巡检内编辑已有价格、补充空字段、为未定价模型配置价格；标准表达式按档位和分项对比；对严格可证明的范围提供保本建议。
- 请求参数、请求头、时间、插件 usage 的场景试算与场景价差；不同阶梯边界的区间对齐；多选模型批量草稿及可预览的批量价格调整。
- 未配置、继承、归入其他分项、显式零和来源缺价各自有明确语义；编辑能力不依赖有报价、有差异或存在保本下限。

不自动迁移全部历史价格，不自动跟随官方改价，不修改实际扣费规则，不把样本一致称为任意请求都等价。本设计的批准仅授权此功能及必要依赖适配，不意味着把整个 upstream/main 无差别合并到当前分支。

## 2. 审查基线与现状

本轮已 fetch 远程引用，没有 checkout、merge 或改动业务代码：

- 当前工作分支：`zhuzhan-f0b-latest`。
- 本地 `main` / `origin/main`：`7aaa53326`，2026-06-02。
- 最新核验的 `upstream/main`：`63cecab82`，2026-10-13。

用户所说的全面动态化应以最新 upstream/main 为适配基线。上游已表达式优先并提供内建默认表达式，但仍兼容历史 ratio / ModelPrice，并未强制迁移所有旧配置。

### 已确认的问题

| 问题 | 代码依据 | 影响 |
| --- | --- | --- |
| 表达式模型跳去模型计价页，不能在巡检改价 | `price-monitor-panel.tsx` 的 `isTieredPlatformPrice` / `buildRepairTarget` | 全动态后主要操作入口失效 |
| `apply_price` 拒绝表达式，patch 只接受 float | `controller/price_monitor_apply_price.go`、`model/pricing_options.go` | 不能只移除前端禁用 |
| 解析器仅识别最多两档及部分线性表达式 | `controller/price_monitor.go:parsePriceMonitorExpression` | 三档、fixed、请求规则等不能准确展示比较 |
| 复杂规则用原文不同表示价格不同 | `markPriceMonitorDifferences` | 文本差异、数值差异和无法判断混在一起 |
| 亏损比较要求相同模式和完全相同档位条件 | `controller/price_monitor_loss.go` | 表达式与倍率混用时覆盖不完整 |
| 保本下限绑定旧倍率字段 | `buildPriceMonitorRepairFloor` | 动态模型没有可用快速修复建议 |
| 旧版本号仅覆盖数值 option | `model/pricing_options.go:IsPricingOptionKey` | 不能拿全局 int64 版本保护表达式编辑 |
| 无现价且无参考价的字段直接隐藏 | `price-monitor-apply-dialog.tsx:buildRows` | 输出/缓存/图片/音频等空字段没有新增入口 |
| 参考价收集只遍历平台已有维度 | `price_monitor_loss.go:observePriceMonitorFloor` / `observeLanes` | 即使来源有该项报价，也可能无法进入编辑器 |
| 编辑入口依赖 repair_floor | `price-monitor-panel.tsx:buildRepairTarget` | 定价编辑和风险分析耦合，缺价整行可能不可编辑 |

相关背景文档：`price-monitor-normalized-tier-comparison.md`、`price-monitor-inline-repair-floor.md`、`price-monitor-loss-detection-and-price-repair.md`。其中“不支持表达式改价”是旧功能的真实边界，本设计批准并实现后再更新这些文档。

`pkg/billingexpr/expr.md` 的 Storage 段仍写 `ModelBillingMode` / `ModelBillingExpr`，部分前端路径也已过时；当前代码与上游实际使用的 DB key 均为 `billing_setting.billing_mode` / `billing_setting.billing_expr`。后续修正文档，不依据旧文档虚构 key 迁移。

### 上游已有能力与兼容边界

- `model/model_pricing_config.go`：逐模型字符串版本（SHA256）、configured/effective 分离、完整模型草稿事务保存。
- `model/model_pricing_conversion.go`：旧价格转表达式预览，包含不能安全转换时的原因。
- 模型价格界面已有 `ModelPricingPanel` / `ModelPricingEditorPanel`；优先组合复用，避免再维护一套编辑器。
- 内建默认价格不一定存在于显式 option map；巡检必须读取有效价格描述，不能把默认价误判成缺失。
- 插件价格包含 `plugin_billing_expr`、插件变体和 usage schema；不能仅用模型名把不同插件的费用当作同一份 token 报价。
- 上游支持 `fixed(...)`、`image_count`、任务 `u(...)` 等，本地当前引擎尚不完整。不能只复制前端并报告可保存、可执行；相应引擎依赖未就绪时只展示“不支持此计费能力”。
- 上游 option/model_pricing 路由是 RootAuth；本 fork 巡检是 AdminAuth 加细粒度权限。不能原样接 API 后让原管理员入口全部报权限错误，也不能为省事放宽所有 option API。

## 3. 推荐交互

保留现有模型行、平台/官方/渠道列、筛选和分页。每行提供“对比 / 修改价格”，打开同页抽屉，关闭后保留筛选、页码、滚动位置和勾选。

抽屉展示当前有效价格、选定参考来源、修改后的草稿。标准价格按“适用条件 × 输入/输出/缓存/图片/音频”对齐，标出具体差额及比例；零基准时不计算无意义的百分比。显示报价时间、来源状态和比较覆盖范围。

- 简单表达式：直接填实际单价，不向管理员展示内部倍率换算。
- 标准多档：逐档编辑价格、看清阈值；复制某来源仅填入草稿，先预览再保存。
- 请求/时间规则：默认保留完整原文，可编辑、校验；显示“含条件规则，尚不能完整比较”。
- 可视化切换必须保证可逆。解析失败不能退回默认模板，不能把无法解析的请求规则变成空数组；保留 `v1:`、`|||`、变量存在性和未改动内容。
- 内建价格显示“默认 / 自定义”；普通改价只写实际修改。恢复默认是显式操作，预览恢复后的有效价格。
- 插件模型明确显示变体及单位；不支持的 schema 显示原因，不伪装成 token 单价。

保存中禁用重复提交；失败保留草稿；冲突展示“价格已更新，请重新加载后比较”，不自动覆盖。保存成功后刷新当前模型价格并重算巡检平台列；如果巡检刷新失败，明确区分“价格已保存”和“巡检结果待刷新”，不能诱导用户重复保存。

场景页使用同一份原始 usage 分别按两侧表达式归一化，显示每侧命中规则、费用和差额；提供阈值前/等于/后的样本、缓存命中样本及用户自定义请求条件。样本只代表这些场景。真实请求日志、密钥和请求正文不自动导入。

### 3.1 没有价格也可以编辑

行级“修改价格”只取决于管理员权限、模型身份和支持的定价能力，不取决于 `repair_floor`、现价、参考价或巡检结果。平台尚未定价时显示“配置价格”；有模型记录但无任何来源报价的模型仍应能在巡检的模型搜索/全部模型视图找到。巡检查询保留分页，不要求把全量来源矩阵一次加载到浏览器。

编辑字段由计费能力 schema 生成，而不是从当前有价格的字段推导：基础输入/输出始终可见；缓存读、5 分钟/1 小时写入、图片输入/输出、音频输入/输出等支持项通过“添加计价项”展示；来源独有的支持项自动列为待补充参考。按次、按图片、插件 usage 分别显示其单位和字段。schema 不支持的维度解释原因并提供适用的表达式/模式转换入口，不允许保存到不生效字段。

| 状态 | 展示 | 可执行操作 |
| --- | --- | --- |
| 未配置但有内建/继承价格 | 默认价格及来源 | 设置覆盖；恢复默认 |
| 表达式未单列某变量 | 随输入/输出计费（按引擎真实语义） | 添加独立计价；预览归一化变化 |
| 完全未配置且无有效价格 | 未设置 | 填写价格；选择表达式/合法计费模板 |
| 显式零 | 0，免费 | 修改；显式移除覆盖 |
| 来源未提供该项 | 来源未报价 | 平台仍可手填；只禁用复制该来源值 |
| 引擎不能表达/上游能力未知 | 原因与单位 | 可用模式转换或原文编辑；无法执行的语法禁止保存 |

每个字段草稿有明确操作 `unchanged / set / inherit / include_in_base`。新增字段初始空白，不能为空转 Number('')=0；用户填写 0 才表示明确免费。未编辑的空字段不阻止保存其他字段，也不生成删除或零值。已点“添加”的空字段要求填写或撤销添加。恢复默认和移除独立变量使其并入基础计价是不同操作，按能力提供并显示生效含义，不用清空输入框隐式执行。

添加 `cr` 等独立变量会改变 p/c 归一化，并可能影响其他阶梯（引擎依据整式变量集合）。预览必须覆盖所有受影响档位；标准表达式由转换器给未编辑档位补足保持原收费含义的项，展示完整变更清单，不能只追加一项并假设其余不变。无法证明保留原语义的复杂表达式，进入完整表达式编辑及场景检查，不自动重写。

例：平台和所有渠道均没有“音频输出单价”，仍可添加并填写；平台没有缓存独立价而来源有价，显示该参考价并可复制；输入价格为 0、输出价格为正，可用表达式合法保存，不再被旧倍率除法要求输入大于 0 阻断。

历史倍率下输出锁定、1 小时缓存价依赖其他项等限制，需要在本抽屉内提供上游转换预览和转表达式后的独立编辑；转换不能安全完成时显示具体原因，不伪装成保存成功。无价格的新模型也可选择固定或 token 表达式并补齐必要项，不要求先有一份保本下限。

### 3.2 批量编辑与对比

批量支持对选定模型/档位/计价项设置绝对值、按百分比调整、复制选定来源和补充缺失项；每次操作先生成逐模型草稿及前后差异。百分比调整只作用于有数值的目标；缺价必须选择显式补充值或保留，不能默认为零。来源缺字段时保持原值并列出未应用项。

任意表达式不做字符串替换数字（会误改阈值、倍率或时间条件）。可结构化目标按 AST 修改；其他目标保留完整表达式手改。批量预览列出可应用、需处理、跳过的模型，用户明确确定提交集合；服务端对提交集合全量验证、整批原子保存。

## 4. 统一对比语义

保留原始计费配置，另生成只读标准价格描述，供差异筛选、表格、亏损及建议共同使用，不改写原始单元格来“凑成同一种模式”。

比较结果显式分为：`equal`、`different`、`partial`、`unknown`、`unavailable`。另外保留配置文本是否变化，避免将不同文本直接当作真实价差。复杂表达式原文完全一致且版本、运行语义一致时可标注规则相同；不同文本但无法证明数值差异时用 unknown。

静态可证明的范围：完整 AST 校验通过的非负线性价格、标准有序 `len` 分段、支持的固定按次表达式。只接受完整语法树符合白名单的表达式，不通过正则截取子表达式推断全式。复杂表达式同时提供完整编辑和场景试算；无法证明全域等价是数学与输入覆盖边界，不是缺失交付功能。多档数量设合理上限，超限返回能力原因，避免无限解析。

- 表达式变量省略、显式零、独立子价格必须区分：省略 `cr` 表示归入 p，`cr * 0` 表示该部分免费，不能相互消除。变量使用集合按真实引擎的整式 AST 语义处理，不能逐档猜测。
- `len` 与归一化后的 `p` 不是同一变量；不把 p 条件自动改写为 len。
- 单档表达式和历史按量价可在规范化后比较，但继续保留平台真实写入模式。
- `cc1h`、`img_o`、音频输入/输出单独处理；旧倍率语义以生效计费口径为准，不能继续固化展示层的错误公式。
- 字段集合来自引擎 schema、原文 AST 和来源维度的并集，并标明能否写入；包含最新上游的 `img_cr`，不能把当前本地旧变量列表作为完整能力。来源语义版本未知时不强行按本地规则归一化：例如 Claude 未单列 cr 时，本地旧引擎与上游新引擎对其并入 p 的行为可能不同，同样的表达式文本不能直接证明实际费用相同。
- `fixed(0.01)` 表示美元/请求；token 系数为美元/百万 token；任务 usage 表达式单位按 schema 和引擎定义，不能统一除以百万。
- 对两侧标准 len 分段收集全部边界，按整数 token 域切分共同区间，保留 < / <= 的开闭含义及区间出处，再逐段比较价格；不改写原始表达式或阶梯名称。条件使用不同变量、复杂条件或不同单位时转场景比较，显示未覆盖原因。不以采样证明全局等价。
- 未报告价格、来源失败、占位价格、合法零价格分别保留；未比较的维度和来源不计为安全。

保本建议按每个已覆盖分项和区间聚合所有可比渠道，沿用已有成本系数与分组售价系数口径，并记录决定下限的渠道。分组系数为零、缺失、源数据过期或复杂规则无法证明时返回原因。平台零价且成本为正必须报告亏损，不能因分母为零跳过。部分覆盖只能叫“已比较范围的下限”，不能标注整体保本。

### 4.1 完整场景试算

支持请求 JSON、非敏感 header、固定时间点、原始 usage 语义（OpenAI/Claude/插件）、插件标识及 usage schema 版本；所有输入均有结构、长度与次数限制。对平台现价、草稿、选定来源分别计算，统一展示美元费用、命中档位、规则倍率、价差及未知项。混合 token/按次维度只在引擎允许且完整知道单位时相加；schema 或来源语义不明则返回该侧不可求值原因，仍可试算其他侧。

自动样本覆盖共同区间边界、缓存/媒体分项和用户指定请求条件；不能自动穷举所有请求或任意表达式路径。已发现亏损显示“此场景亏损”；未发现显示“所测场景未发现亏损”，与静态证明范围明确区分。每个样本带固定时间和输入摘要，可重复运行，不能使用两侧各自运行时的当前时间。

当前本地及核验的上游 `pkg/billingexpr/run.go` 时间函数使用 time.Now，尚无固定时间注入，这是本次必须补齐的后端能力。新增管理侧 `EvalAt/EvalContext` 入口，通过不可变函数环境注入固定 clock；沿用真实引擎解析与归一化，不另写前端计算器，不改进程全局时钟。既有 relay 调用保持默认时间和执行路径，必须以测试证明无行为变化。

preview 仅执行表达式，不调用插件任务执行器或远程 AI，不进行扣费。使用独立、有界的分析缓存/worker；先设置表达式和 AST 上限、引擎执行预算，再施加请求超时，不能用“goroutine 超时但计算继续”充当资源限制。

## 5. 数据流

后台巡检读取渠道及来源报价 → 获取生效平台价格描述 → 解析能力识别和标准化 → 统一比较/亏损/下限 → 发布带版本的不可变快照 → 管理界面与分享页读取展示。

打开抽屉 → 读取最新 configured/effective/version → 从 configured 完整克隆草稿（含插件变体）→ 局部编辑 → 服务端预览/校验 → 带 expected_version 保存 → 同一事务局部替换目标模型、保留其他模型 → 更新生效设置 → 审计 → 重算巡检并刷新查询。

effective 仅用于展示和预览，不能直接当 configured 全量回写。完整草稿遗漏键表示继承/移除覆盖，和 patch 的“不修改”不同，必须由接口类型和回归测试锁定。

## 6. API contracts 与权限

优先引入/复用上游模型价格 API 和 model 层原子写入，不新增另一套表达式持久化协议：

| 接口 | 请求 / 响应 | 本 fork 权限 |
| --- | --- | --- |
| GET `/api/option/model_pricing?model=...` | 返回 entries 中 model_name、version、configured、effective、plugin_variants、usage_schema | AdminAuth + 模型计价查看或编辑权限 |
| POST `/api/option/model_pricing/preview` | `{model_name, pricing}` → 生效价格描述 | AdminAuth + 模型计价编辑权限 |
| POST `/api/option/model_pricing/convert` | `{model_name, pricing}` → 转换草稿/原因 | 同上；只预览 |
| PATCH `/api/option/model_pricing` | `{changes:[{model_name,expected_version,pricing,reset?}]}` → updated_models | AdminAuth + `SystemSettingsEdit("billing.model-pricing")` |

以上为待实施契约；当前分支缺少对应后端，不用前端假保存替代。本设计确认包含补齐这些接口和所需模型服务。单独注册精确路径及权限，其他 `/option` 权限保持现状；前端 useCanEditModelPricing 与后端同源。

巡检 `GET /results` 增加 schema/capabilities、comparison_status、comparison_reason、按维度 differences 和 coverage；管理员编辑数据通过上述模型接口获取。公开分享页只增加脱敏后的比较状态，不返回 configured、插件内部配置、请求头条件正文或管理版本信息。

现有 POST `/api/price_monitor/apply_price` 的旧数值请求在过渡期继续兼容；内部最终委托统一模型保存服务，避免两套 writer 并行绕开版本/锁。表达式从统一 API 保存，不把字符串塞入旧 float fields。

巡检“应用保本建议”增加只读 POST `/api/price_monitor/preview_price`，权限为原改价权限；请求 `{checked_at, changes}`，返回 `{models:[{model_name,comparison_status,coverage,violations}], context_version}`。它是业务校验，不是新的设置保存接口。

新增只读 POST `/api/price_monitor/evaluate`，AdminAuth + 模型计价编辑权限，请求 `{model_name,expected_version,pricing,source_keys,scenarios:[{id,usage_semantics,usage,request_body,headers,at,plugin_key?,schema_version?}]}`；返回 `{context_version,results:[{scenario_id,sides:[{source_key,status,amount_usd,matched_rules,reason}],differences}],coverage}`。usage 依据标准/插件 schema 校验，客户端提供的能力标签不视为可信引擎版本；服务端与来源能力数据核验。该接口不存储设置或请求正文，失败使用项目错误 helper。

为保证巡检单改和批改都不能漏掉风险审查，现有 POST `/api/price_monitor/apply_price` 增加与旧 `items` 互斥的 `changes` 请求分支：`{checked_at,context_version,changes,force_below_floor,acknowledge_unknown}`，changes 与统一模型 API 完全相同。此控制器只包装巡检保护，内部调用同一个模型事务核心，不另建持久化实现。巡检抽屉及批量改价必须经过该包装入口，不能直接调用通用 PATCH。force_below_floor 允许管理员接受已知低于下限的风险；acknowledge_unknown 独立表示了解未覆盖范围，不能将“不知道”当成“已证亏损”。两者都不能绕过权限、版本、语法、能力检查，接受风险绑定 context_version 与草稿摘要，草稿变化后重新审查。旧 items/force 仅兼容原数值协议。普通模型价格编辑保持既有自由定价语义，不宣称保本。

新模型/无平台价的模型通过模型配置接口取得空 configured 的 version，服务端依据真实模型/渠道清单确认目标，不要求旧快照已有 platform price cell。缺少来源快照只意味着风险未知，管理员明确接受未知后仍可手动定价，不因此禁用编辑。

价格草稿与期望版本必须在统一写入事务内核验。事务核心为巡检包装提供纯内存审查入口，使用事务读到的真实配置、已编译候选草稿及预先捕获的成本上下文复核；锁内不编译复杂表达式、不查外部来源。报价快照、渠道成本及分组配置使用不可变分析上下文并绑定指纹；上下文变化则重做预览。不能把未版本化的旧巡检快照当作实时成本保证。校验后提交过程中发生的外部报价变化无法保证，UI 标明报价时间及适用上下文。

新增业务错误遵守本 fork common.ApiErrorI18n、HTTP 200 的 success=false；不照搬上游 400/409 和原始 Go error。预览的 violations 为成功返回的业务数据；最终保存失败用现有错误 helper，前端重新预览取得详情。底层错误记录日志，用户只收到可操作信息。前端适配放 feature API 模块，禁止改动五个 upstream-owned auth/http plumbing 文件。

## 7. 数据模型、配置和生命周期

- 不增加业务表。继续使用 options，规范键为 `billing_setting.billing_mode`、`billing_setting.billing_expr`；插件键随上游模型服务完整支持。
- 采用逐模型 version 作为编辑并发依据；旧全局 pricing_version 仅在兼容接口保留，不能与字符串 version 混用。
- 模型页、巡检、新旧批量 option 保存等所有价格 writer 使用统一锁序和版本更新策略；不能只改巡检入口后保留可绕过并发检查的旧 writer。
- 不新增持续增长的大表，不产生大表扫描。options 按 key 精确读取，实施时核验现有索引与三种数据库执行计划。管理审计沿用现有存储与留存机制。
- 巡检快照 schema/matrix version 递增。旧快照在刷新前标注旧版本能力不足，不能把缺字段默认解释为 equal。刷新失败保留最后成功结果及时间。
- 管理页面本身不增加独立功能开关，访问由既有模型计价权限控制。必要的中转定价快照修正单独由 billing_setting 配置开关控制，见实施记录；不通过关闭页面退回旧倍率误写路径。
- 已保存表达式不随功能开关回滚。应用回滚必须保留支持这些表达式的计费引擎；不能部署旧引擎去执行 fixed/u 等新表达式。
- 批量失败全单回滚；重试先刷新版本，重新保存前保留草稿供对比。无变化保存不产生覆盖。恢复默认按上游 reset 契约显式执行。
- 前端文案实施前加载 i18n-translate 技能，覆盖七种语言；后端用户消息补 en/zh。

## 8. Main Chain Impact

不在 relay goroutine 添加抓价、解析、比较、DB 查询、写入、审计或新 goroutine；不插入新 middleware。巡检沿用后台任务，预览/保存为管理请求。

必要的上游计费基础适配必须作为独立依赖变更核验，不能把它说成“纯管理 UI 无主链影响”。本功能仅使用已验证引擎，不为巡检修改结算规则。若引入依赖实际改变 relay 执行，需补充对应设计、开关及独立 30k+ RPM 分析再实施。

价格保存会更新 relay 已使用的生效配置，这是明确预期的业务影响。沿用现有请求 BillingSnapshot 语义，在途请求不因巡检重算而重新定价；验证跨组重试和结算快照保持一致。

## 9. Shared Resource Audit

| 资源 | 与 relay 是否共享 | 约束 |
| --- | --- | --- |
| options 价格行和设置内存 | 是 | 复用唯一写入服务；编译/预览在事务外；固定锁序、短事务；不得持写锁做网络抓取 |
| channels / 分组设置 | 是 | 复用巡检批量读取与已有配置快照；不逐 cell 查库；不新增大表扫描 |
| DB 连接池 | 是 | 后台巡检沿用有界并发；预览限制批量；事务内不进行远程调用 |
| 表达式编译缓存 | 可能共享 | 不把任意预览草稿无限写入 relay 全局缓存；优先隔离有界管理分析缓存 |
| 巡检快照内存及 snapshot.json | 否 | 不可变发布；沿用现有文件写入与主节点约束，拒绝过期重算覆盖新快照 |
| Redis | 不新增访问 | 不引入 relay key 命名空间，也不新开连接池 |
| worker / CPU / GC | 进程资源共享 | 沿用受限后台任务；按表达式摘要复用分析结果，限制 AST 深度/长度/批量/场景数量 |
| 审计存储 | 沿用现有 | 仅记录模型、版本、变更摘要和强制接受的风险，不记录凭证或场景请求正文 |

## 10. Concurrency Analysis

本功能对每次 AI 请求新增 DB=0、Redis=0、锁=0、goroutine=0。在 30k 与 100k RPM 下不随 AI 请求数增加巡检任务。后台成本按模型×来源×受限阶梯数增长；解析按不同表达式去重，避免矩阵每格重复编译。

管理请求上限沿用旧接口 100 个模型；支持单模型和多模型草稿，批量提交不隐式增加后台抓取。场景每请求最多 32 个、来源最多 10 个；AST 最多 4096 节点、深度 64、标准分段最多 128 个，表达式/请求内容沿用更严格的已有 body 限制，执行预算按引擎约束落实。超限明确返回缩小范围提示，不截断后显示全量安全。外部报价抓取复用超时与限流。发布前在实际模型/渠道规模下测后台巡检和管理改价与 relay 并行时的延迟，确认共享池与 GC 不挤压主链。

前端按需加载抽屉编辑器，只查询选中模型；复用 React Query 查询和局部 invalidation，不为每个表格单元格挂完整编辑器。

## 11. 测试与验收

批准后先写测试用例再实现，不调用真实 AI upstream：

1. 对比：倍率/单档双向同价；多档条件一致/不同；三档以上；完整 AST 尾部额外运算不能被忽略；项顺序/重复项；边界 t-1/t/t+1；未知版本和不支持能力。
2. 语义：省略 cr 与 cr*0；全式变量集合；cc1h/img_o/音频；p 与 len；OpenAI/Claude usage 归一化；fixed 单位与任务 USD 单位；零价非缺失；平台零价且成本正。
3. 编辑：不修改时原文往返不变；v1/||| 保留；解析失败不能切到默认可视模板；configured 与 effective 分离；插件变体不丢失；恢复默认；复制参考后再编辑。
4. 保存：单/批量成功、100/101 上限、重复模型、非法值、同模型版本冲突、不同模型互不误覆盖、事务失败整单回滚、旧接口和模型页并发写、先保存后刷新失败。
5. 风险：所有可比渠道取最严；分组/成本变化使 context_version 过期；partial/unknown 不算保本；force 不能绕过版本/权限/编译；更改一个价格不得静默改其他分项。
6. 权限：有巡检查看权无计价编辑权；有计价编辑权的管理员；root；匿名；公开结果不泄漏配置和请求条件。路由不得意外落入宽松组。
7. DB：按项目环境运行相关 model/controller 测试；无真实 DB 时记录 SQLite fallback，MySQL/PostgreSQL 行锁及 SQLite busy 冲突分别验证；测试仅清理自己的行。
8. 前端：使用现有已配置测试工具，不新装 runner；`bun run typecheck`、涉及文件 lint、生产 build；手工验证草稿、冲突、loading、刷新失败、主题及窄屏。
9. 后端：相关 controller/model/billing_setting/billingexpr 测试；如依赖涉及 relaykit，必须 `cd relaykit` 且 `GOWORK=off go build ./...`。不得修改 upstream-owned 测试以适配本地。
10. 缺字段：平台和来源均缺值仍能新增；仅来源有值可复制；来源失败不禁编辑；继承值 no-op 不持久化；新增未填项报错但未添加的空字段不拦截；显式 0、输入 0 且输出正、全模型无价、恢复默认和并入基础各自验证。
11. 全局语义：新增/移除 cr、img_cr、cc、img_o 影响所有表达式叶和 p/c 条件；安全补项保持旧档费用，无法证明时不自动重写；不同 Claude 引擎语义不误判一致。
12. 场景和区间：不同阈值取并集、整数开闭边界、固定时间跨日/时区、不同请求规则、插件 schema 不匹配、混合单位、执行预算超限、不调用真实插件执行器、不污染 relay 缓存。
13. 批量补价：明确目标字段与档位；缺字段新增；百分比操作遇空值；来源不提供值；跳过项可见；未知风险和已知亏损分别接受；更改草稿后旧风险确认失效。

验收示例：动态模型在巡检抽屉中修改一个阶梯的输出价后，表达式其他规则保持原样；实际生效配置、矩阵差异与模型定价页一致。无法完整比较的规则始终有明确状态，不出现“未检查但显示安全”。

## 12. 多模型讨论结论与实施次序

本轮分别审查了上游兼容性和编辑/比较体验，并交叉复核方案。初始“给旧 apply_price 加字符串 patch”的想法被撤回：最新上游已有完整草稿及版本机制，再维护一套 writer 会加重合并与并发风险。

一致结论：编辑字段来自计费能力而不是已有价格，行级编辑与保本分析解耦；先对齐上游有效价格和原子保存，再组合编辑器。结构化比较、跨区间比较、场景试算、补价和批量操作都在本次完整交付中。复杂规则可以编辑和试算，不能以少量样本代替全域安全证明。

内部开发顺序（同一次交付，所有项目通过后才验收）：

1. 盘点并引入模型价格服务所需最小上游依赖，适配本 fork 权限和错误契约；列清影响 relay 的依赖，不做全库合并。
2. 实现管理端标准价格分析、比较状态、旧快照兼容与回归测试。
3. 接入同页编辑、预览、唯一写入服务和保存后刷新；兼容旧改价入口。
4. 完成空字段新增、无价模型配置、跨边界比较、固定时间场景试算及插件 usage 支持。
5. 完成保本覆盖分析、批量变换、风险确认、i18n 和全部验证；更新本设计与旧文档的实际实现记录。

用户已明确要求“开始实现”，上述完整交付已获授权。

## 13. 实施记录

实现保持当前分支实际计费引擎语义，新增管理侧能力识别，而非合并整个 upstream/main。模型 API 对齐上游逐模型字符串版本/完整草稿，旧数值 API 保持兼容并共用写入锁及版本行。

- 当前引擎可执行标准 token 变量、请求条件及时间函数；fixed、image_count、img_cr、插件 usage 标记为不支持，拒绝保存到本地引擎。按次定价继续使用 ModelPrice。不能因为远端已支持某语法就在本地假执行。
- 本地倍率表持久化后是整表替换，删除键并不总是恢复内建默认。Reset 恢复该模型内建数值，完整草稿删除覆盖显示其实际生效结果；未知模型无内建价时恢复后仍无价。
- 指定时间试算使用独立 CompilePreview/RunAt，不填充 relay 编译缓存，不执行远程请求；场景接口返回覆盖范围 scenarios_only。
- 对尚未声明归一化版本的远端表达式，静态系数比较保留未知覆盖范围，不宣称所有上游格式都不亏损。
- 标准区间及 UsedVars 存入巡检快照，不持久化或公开原始请求条件表达式；快照版本升级后自动重新巡检。
- 无法完整证明的动态规则保留在结果中，提供 `needs_review` 筛选，不把未知状态计为已证明的数值差异。
- 三档以上用区间上下界展示；旧两档字段兼容。来源显示格式兼容旧单档归一化，同时另存 billing_mode 保留真实模式。
- 全部后端新增文案按实际项目的 en/zh-CN/zh-TW YAML 文件维护；旧设计中提到的 JSON 路径不适用于当前后端。

保存采用数据库中的全局价格版本绑定预览，并在持有版本行锁时再次检查；不以当前实例可能滞后的内存版本代替数据库版本。旧定价页通过同一接口的 `options + expected_options` 分支一次保存关联设置，避免动态模式与表达式拆开提交。USD 快捷编辑使用服务端返回的 `token_price_factor`，不假设配额单位恒定；受引擎约束的输出倍率单独返回 `completion_locked`。

静态价格相同但全式变量集合不同的表达式不判为完全一致：OpenAI 与 Claude 的缓存归一化可能产生不同费用。动态模式缺少表达式时保持不可用状态，不回退到残留的旧倍率。场景试算在单次请求内复用隔离的编译结果，拒绝非有限数或负价格。

### 验证记录

- `go test ./controller ./model -run 'PriceMonitor|ModelPricing|PricingOption|PricingPublication' -count=1` 通过。
- `router`、`pkg/billingexpr`、`setting/billing_setting`、`relay/helper`、`i18n` 全包测试通过；包含原子配置发布与 relay 捕获表达式的回归测试。
- 当前环境未配置 `SQL_DSN`，本次新增数据库用例使用隔离 SQLite；未声称验证真实 MySQL/PostgreSQL。全包 controller/model 测试分别遇到已有 `TestListChannelVeridropDetectionTargets` / `TestListChannelVeridropDetectionsFiltersAndPages` 未初始化 DB 的 panic，不属于本次定价用例。
- `-race` 因环境未提供 CGO C 编译器而无法运行；未执行实际 30k/100k RPM 压测。主链成本分析为代码分析，不代替部署环境压测。
- `go build ./...` 与 `setting/ratio_setting` 全包测试通过。
- 前端 `bun run typecheck`、`bun run lint`、`bun run build` 通过，12 条草稿/解析/批量编辑回归用例通过。全项目 lint 仅有已有 footer `dangerouslySetInnerHTML` 警告；未执行登录后的浏览器人工验收。
- 七种前端语言补齐本次文案，未改动受保护的认证/HTTP 基础模块。弹窗按打开时加载，旧定价页和上游价格同步复用原子保存接口。


## Implementation dependency: coherent dynamic billing publication

### Main Chain Impact

The model editor commits expression and mode together. Publishing those keys one by one could expose a mode without its expression to concurrent relay requests. The necessary compatibility fix publishes one immutable `BillingSetting` snapshot through the existing ConfigManager snapshot mechanism. `GetModelBilling` loads mode and expression from the same snapshot once; `ModelPriceHelper` carries that expression into pre-consumption unchanged. No billing arithmetic, provider behavior, or settlement behavior changes. `billing_setting.atomic_pricing_enabled` defaults to true and can restore legacy reads immediately.

Only one atomic pointer load and two map lookups run synchronously on relay; there are no new locks, DB/Redis calls, allocations, or goroutines on that path. ConfigManager publication deep-copies maps on the low-frequency administrator/sync path. All model-price writers coalesce mode/expression publication after committing the database transaction; periodic option refresh coalesces the same pair. The shared memory resource is the existing billing configuration, now exposed immutably. Model mutation locks remain management-only. At 30k?100k RPM, added work is constant and independent of model count; map copies happen only during configuration changes, not per request. Existing request billing snapshots continue to freeze the selected expression for settlement.

This provides a coherent mode/expression pair, not an atomic snapshot spanning unrelated legacy ratio maps or group settings. Concurrent configuration publication tests and relay fixture tests cover first dynamic pricing, expression removal, and repeated model-mode switches without partially published expression state. No upstream network calls are used.

## 14. 香港测试服务器部署与实测（2026-10-13）

已部署至 `43.251.102.17`，版本 `test-price-monitor-dynamic-20261013`。使用当前工作区构建内嵌前端及 Linux amd64 程序，保留 HEAD 已有的日志详情优化等功能。二进制 SHA256：`dfaaca2065a9a36e422bf04ed1630f1bf191bc79f57995c0349d268764ab1fab`。

备份和验证记录位于服务器 `/root/deploy-price-monitor-dynamic-20261013/`：保存上一版程序、原启动参数与环境、`.env` 和 options 快照，以及 `verification.json`。目录权限为 0700，含配置的备份文件为 0600。部署沿用原进程环境和参数，优雅停止后替换 `/root/new-api`，启动失败自动恢复旧程序；本次启动和健康检查成功。应用端口 3002、代理端口 3000 均已核验，外网入口 `/api/status` 和 `/price-monitor` 返回正常。

服务器上 23 项检查通过：

- 两端口的版本、管理页面入口及匿名请求 401；全部 122 个 JavaScript/CSS 资源与本机构建 SHA256 一致。
- 临时普通用户访问定价管理接口返回 403；临时 root 用户通过正常登录取得短期会话。
- 无价模型可读取空草稿和版本；两模型动态定价预览、未知风险确认、一次批量保存成功。
- 修改草稿后旧确认无效；未确认风险不写库；过期版本拒绝覆盖；不支持的表达式使整批失败，无部分写入。
- 阈值 1000/1001 的费用分别为 0.0024/0.003603 USD；补充 `cr*0` 后 OpenAI 与 Claude 原始缓存用量各自归一化，费用均为 0.0008 USD。
- `needs_review` 筛选可访问。测试结束删除独立模型价格，并确认所有原有价格 option map 与测试前相同；临时用户和会话已清理，保留测试审计记录，`.env` 逐字不变。

上述保存测试实际访问服务器现有 MySQL 主库，未创建真实上游渠道、未调用 AI 服务、未产生模型消费。当前工具环境没有可用浏览器，因此未声称完成页面点击、布局和弹窗的人工验收；未进行高并发压测。
