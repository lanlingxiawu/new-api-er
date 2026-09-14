# 流式错误来源与日志解耦

## 目标与范围

2026-09-14 用户确认修复本轮审计问题：日志函数只输出日志，终止原因在实际出错处登记。仅修复新增受管流程的错误分类及尾部读取误判；渠道自动禁用逻辑保持现状。

## 当前问题与数据流

`LogLegacyStreamError` 通过上下文回调把所有错误登记为 `response_conversion_error`，抢先覆盖随后 `StreamResult.Error` 的上游 JSON 分类。独立扫描器还会将正常完成后的读取报错重新标成失败。

修复后：上游读取/解析或本地转换出错 → 业务处理器显式登记首个原因 → 日志只输出既有定位信息 → 统一补错、计费选择与日志封装。

## 错误处理与边界

- 上游字段类型/JSON 错误：`upstream_json_error`，保留上游诊断资格。
- 未完成的读取错误：`EndRead` 决定 `upstream_read_error` 或 `upstream_incomplete`；正常完成后的运输层读错忽略。
- Tencent 的尾部读错只记录日志；Coze 的独立扫描器在受管且已正常完成时保留用量返回，避免将已忽略的读错再次交给上层失败兜底。非受管 Coze 仍返回原读取错误。
- 本地转换/序列化失败：`response_conversion_error`，不冒充上游异常。
- 下游写入/刷新失败：写入器先登记客户端错误，后续转换/解析登记不覆盖它。
- SDK 未知事件类型：显式 `upstream_protocol_error`；上游原生 error 沿用现有原帧保留机制。
- 独立扫描器在受管 DTO 解析失败或下游写失败后停止处理；非受管继续原有软错误行为。
- 复核全部日志桥调用点；Cohere、PaLM 的旧分支及 OpenAI 旧延迟转换分支不新增受管处理。
- 计费六行合同、成功 HTTP 门控、请求体透传、非流式及关闭开关的行为不变。

## API、存储与配置

无新增端点、字段、表、索引或配置。继续使用 RootAuth 诊断接口及 `stream_diagnostic_available`；原始响应仅符合上游异常条件才保存。没有请求头/请求体采集。沿用请求内冻结的 `stream_error_setting`。

## 实施步骤与验证

1. 先写回归，验证日志无副作用、DTO 类型错误、正常结束后读错、未完成读错、本地转换和客户端失败。
2. 移除日志到会话的接口桥；共享回调增加明确的转换错误入口，独立扫描器/SDK/收尾转换器在错误源登记。
3. 检查全部调用点、中文注释及旧边界；运行相关包、relay 全包和根构建。
4. 本地 fixture 仅调用用量选择及日志封装，不操作真实余额、数据库或 AI 服务。

## Main Chain Impact

同步只涉及错误分支的会话登记和停止判断；日志沿用现有输出路径，移除其隐式会话访问。正常帧不新增缓冲、网络调用、等待下一帧或数据库访问。异步日志/结算队列保持原样。

## Shared Resource Audit

仅使用请求局部 StreamSession 的既有短锁及既有日志输出锁，不增加锁嵌套。无新增共享缓存、Redis 命名空间、表读写、连接池或工作池；同一 BillingSession 的终止和结算所有权不变。

## Concurrency Analysis

30k/100k RPM 下新增 DB、Redis、文件及外部请求次数为 0，新增 goroutine 为 0。Tencent/Coze 独立扫描器受管帧只做既有会话短锁检查；错误分支少量登记不随正文长度增长。不以单元测试代替生产容量压测。

## 风险与实施状态

主要风险是移除桥后遗漏仅打印日志的调用点，或把客户端错误重新标成上游错误；通过逐调用点复核及首因/交付/计费回归覆盖。

### 调用点复核与涉及模块

| 模块 | 登记方式 |
|---|---|
| logger / relay/common | 删除上下文接口回调和旧桥方法，仅输出既有公开定位提示 |
| relay/helper | `Error` 保持上游 JSON，`ConversionError` 明确本地转换；两者保留首因及旧软错误语义 |
| Dify、Baidu、xAI | 解析走 `Error`，输出走 `ConversionError` |
| OpenAI | 逐帧 DTO 已解析后，转换失败走 `ConversionError`；收尾解析及转换按来源登记 |
| Claude、Gemini | SDK 共用 Claude 解析及两类收尾转换显式登记，不改 Claude 专用状态机 |
| Tencent、Coze | 独立扫描器登记 DTO 来源并停止；完成后的读取错误不重新标记失败 |
| AWS | 未知联合事件记录 `upstream_protocol_error`；原错误载荷保留逻辑不变 |
| Cohere、PaLM、OpenAI 旧延迟分支 | 日志调用仅在旧路径，已有受管专用实现保持不变 |

新测试位于 logger、relay/helper 及 Dify/Tencent/Coze/AWS 的相邻测试文件。原计费、权限、控制器自动禁用及存储代码本轮未修改。

### 验证记录（2026-09-14）

- 修复前复现：日志回调被调用、Dify/Coze/AWS/Tencent DTO 错误误标本地转换、Tencent 完成后读错改判失败、Coze 完成后读错再次返回上层失败入口。
- 修复后新增回归及既有 Dify 用量/客户端尾帧失败矩阵连续执行 20 次通过；覆盖透传标志、原始 body 保存、有效交付与零收费选择、SDK 未知事件及禁用/等待成功响应边界。
- `go test ./relay/... ./logger ./common ./router ./setting/operation_setting ./i18n -count=1` 通过。
- 根模块 `go build ./...`、`git diff --check` 通过；relaykit 源码及前端未修改。
- service/model/controller/middleware 的流式、诊断、历史策略字段与私有日志回归通过。沿用仓库外临时 Go overlay 跳过数据库 TestMain 初始化，仅执行 DB 无关测试，不修改仓库初始化或真实余额。
- 未进行数据库集成验证、生产压测、部署或 Git 提交。实施完成。
