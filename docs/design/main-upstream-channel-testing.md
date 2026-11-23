# 官方 main 上游渠道对照测试

## 测试流程

mock 先接入官方 main 服务，再将该 main 服务作为本分支的实际渠道上游，之后进行协议、异常、用量和计费对比。实际测试主机和实例配置记录在本地环境文档，服务器凭据不写入源码或提交。

用户追加要求：永远部署最新官方 main。每次部署先获取远端 main 最新提交，在构建结束、启动之前再次核对远端 HEAD；有更新则重新获取和构建，不以历史固定提交或旧镜像替代最新部署。准确提交和产物散列仍须落入报告，旧版本验收结果不能冒充最新结果。

请求路径：测试客户端 → 本分支候选服务 → 配置的官方 main 渠道 → mock。对照请求直接请求同一 main 渠道，分别采集 mock 发送、main 返回、本分支返回及两层消费日志。mock 用量为可控已知数；main 的返回和落库用量是实际经过上游网关后的观测值，不等同于真实模型供应商实际计量。

## 部署和隔离

先只读核对测试机的现有服务、端口、磁盘和容器。为官方 main 与 mock 使用独立目录、进程/容器、端口和数据库；不得覆盖测试机原有分支服务或接入其用户、渠道、额度数据。保留可重复使用的 main/mock 服务和测试专用账号、渠道；记录准确官方提交、构建散列、端口和健康状态。候选服务也使用本次专用数据库和测试账号。

main 源码保持官方提交原样，不在 main 内添加伪造控制器；mock 作为该 main 的真实配置渠道。mock 仅用于测试，要求鉴权且不接入真实付费供应商。部署命令和报告不输出 token、服务器密码、会话密钥。

## 配置、API 和数据模型

使用两端已有的后台鉴权、渠道管理、token、系统配置和日志查询 API；管理接口按既有 Root/Admin 权限访问。main 与候选的测试 token 分离，候选仅持有 main 的测试 API token。无产品新端点、新配置、新表或新列；新增数据只属于专用实例。测试凭据保留在受限的服务器私有配置中。

## Main Chain Impact

本轮只增加部署和验收夹具，不新增产品 relay 热路径代码。所有测试通过实际 HTTP/流式渠道执行，不能以内存直接调用 handler 或绕过 main 代替验收。复用之前已验证的本分支候选构建。

## Shared Resource Audit

main、mock、候选均独立进程和数据目录。不得共用现有网关的数据库、Redis namespace、日志队列或连接池；测试连接数有界，不进行破坏性压测。新增服务器磁盘和监听端口在 inventory 后确认容量和冲突。无新 relay 同步数据库查询或共享锁。

## 验收矩阵和错误处理

协议范围以本次官方 main 的 `relaykit/types/relay_format.go` 为准，构建时生成 `protocol-inventory.json`，运行前比较其提交与部署提交，检查每一种格式都有测试入口。main 新增格式而夹具尚未补齐时，测试直接失败，不允许沿用旧的“全覆盖”结论。

| main 格式 | 实际入口和返回方式 |
| --- | --- |
| openai | Chat、旧 Completions、Moderations；JSON / SSE |
| claude | Messages；JSON / SSE，严格校验开关分别开启、关闭 |
| gemini | v1beta / v1 原生生成；JSON / SSE、单条 / 批量向量 |
| openai_responses | Responses；JSON / SSE / WebSocket |
| openai_responses_compaction | Responses compact JSON |
| openai_alpha_search | Alpha search JSON |
| deepseek_tools | Search / Fetch JSON |
| openai_audio | Speech WAV、Transcription / Translation multipart |
| openai_image | Images generations JSON / SSE、Edits multipart |
| openai_realtime | Realtime WebSocket |
| rerank | Rerank JSON |
| embedding | Embeddings JSON |
| task | 通用 Sora task、OpenAI video，轮询、产物列表与视频内容 |
| mj_proxy | Midjourney 提交及任务查询 |

相同协议下的所有供应商适配器不等于新的 RelayFormat；本矩阵的“全部协议”指上述源码枚举，不能解释为每家供应商、每个模型、所有任务插件和每个业务动作已逐一认证。未实现的官方入口不能用伪造成功响应计入覆盖。

Claude 转换路径的兼容性对照必须具有相同转发层数：main 上另建仅供测试的模型别名渠道 `claude-probe-reference`，映射回 `claude-probe`，上游指向同一 main 的真实地址。这样对照链路为 client → main 别名渠道 → main 原渠道 → mock，候选链路为 client → candidate → main 原渠道 → mock。别名与目标模型不同，不会递归。两条链路实际消费相同的、经内层 main 规范化后的协议数据；不能用 main 单跳转换原始异常数据的结果，直接断言候选双跳链路不兼容。该对照只增加隔离实例中的一个渠道，不增加服务、数据库或产品逻辑。

异常分组采用等价类和边界分析：HTTP 400/401/403/429/500/502/503，空体、null、数组、破损/截断 JSON、HTML、错误 Content-Type，零/缺失/字符串/负数/溢出/数组 usage，SSE 缺终止、重复终止、事件乱序、内容块未关闭、工具参数截断、未知事件、混入其它协议、逐字节 CRLF、末行无换行、RST、短体、慢响应、空闲/心跳，以及任务失败/处理中。按协议适用性选取场景，不把同名但实际没有改变响应的夹具算作独立语义覆盖。

实际断言包括：请求确实到达 main 配置的 mock 渠道；上游 HTTP/业务错误仍然可见；严格 Claude 拒绝错误顺序；关闭时与 main 的 HTTP 状态和 usage 相同；正常 wire usage 与持久化 token 用量相同；候选请求的实际 main 上游日志与本层日志对比；消费记录不重复、额度和 token 非负。完整保留逐层正文、mock 审计、两层日志和失败记录。异步任务必须等待最终状态，成功后验证产物和内容，不能仅凭提交 HTTP 200 判定通过。

覆盖 Claude、Chat Completions、Responses、Gemini 的实际入口及 main 支持的协议转换，正常响应、缺失/异常事件、错误 JSON、上游拒绝、缺失/零/非法用量和连接故障。严格开关开启检查错误、交付和结算；关闭时比较直接 main 的实际响应和 usage。上游 main 可能已转换、截断或修正 mock 数据，因此逐层保留证据，不能假定原始 mock 异常仍原样到达候选。

按请求关联两层 request id；对已完成可计费请求核对上游返回 usage、本分支日志 usage、缓存细分、额度和每请求单次结算。失败不以改预期或隐藏重试视为通过；区分主机/网络、测试夹具和产品失败，修正后保留原始失败证据并完整重跑。

## 回滚与交接

### 同截止时间的因果对照（2026-11-13）

两层都使用 3 秒扫描超时时，内层 OpenAI 处理器延迟一帧转发，usage 可能到内层收尾才写出。新增仅用于隔离栈的 `timeout_causality.py`，在外层真实渠道与内层 main 之间记录逐行接收/发送时间及正文散列。分别延后外层响应头 1 秒（内层先结束）、延后 usage 转发 1 秒（外层先结束），并保留不干预的竞争组及 main/main 重复对照。比较相同条件下的线上响应、实际用量和落库账单；原始同截止失败不能因补测通过而删除或改判。

代理只转发到内部 main，验证现有测试 token，不保存请求头或凭据。它只用于串行诊断，不增加产品代码、数据库表、共享缓存或生产连接池；线程和文件属于隔离测试进程。本对照不能证明 100k RPM 容量，也不能用人工时序替代原始矩阵。

仅停止或删除本次明确命名的实例，不变更现有服务。保留 main/mock 以便后续复用，报告部署版本、连接方式、可重复执行命令及测试结果。后续 main 更新必须重新记录提交并重复基线验证；此前版本的通过结果不自动适用于新版本。

## 可复用脚本入口

- `scripts/testing/build-latest-main.py`：获取并构建最新官方 main，生成版本与协议清单。
- `scripts/testing/main-channel/provision_database.py`：创建隔离测试数据库。
- `scripts/testing/main-channel/deploy.py`、`deploy_isolated.py`：部署专用 main、candidate 和 mock 实例；执行前核对脚本的实例路径及配置。
- `scripts/testing/main-channel/matrix.py --smoke`：全协议正常基线；省略 `--smoke` 执行异常矩阵。
- `scripts/testing/main-channel/wallet_audit.py`、`fix_wallet_audit.py`：隔离实例的账务核对。
- `scripts/testing/main-channel/test_acceptance_audit.py`、`test_wire_comparison.py`：无需部署凭据的离线回归。

实际部署版本、主机调优、单次编排与验收结果仅保存在本地，不随测试工具提交。
