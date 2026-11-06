# Infini Acquiring API v2 升级设计

状态：用户已确认，代码已实现；2026-11-06。

## 目标与范围

将本分支 Infini 托管充值的下单和管理员查单迁移至 Ed25519 v2，同时更新后台凭证配置界面。保留 USD 金额、汇率折算、额度上限、支付方法选择和到账幂等规则。此次仅升级代码，不配置生产凭证、不向真实商户发起订单、不部署。

官方依据：
- https://developer.infini.money/docs/en/4-authorization
- https://developer.infini.money/docs/en/6-api-ducumentation
- https://developer.infini.money/docs/en/7-webhook

官方已弃用 HMAC v1，迁移截止日期依环境通知；Webhook 签名协议不受此次迁移影响。

## 现状与相关设计

`controller/topup_infini.go` 使用 HMAC 下单，`controller/topup_platform_status.go` 使用同一签名函数查单。旧凭证是 `InfiniApiKey`、`InfiniApiSecret`；Webhook 使用独立的 `InfiniWebhookSecret`。参考 `payment-infini-usd-and-stripe-promo-credit.md`、`topup-platform-credit-status.md`，以实际代码为准。

## 数据流

1. 管理员在现有支付设置中保存 v2 Key ID 和完整 Ed25519 PKCS#8 PEM 私钥；后端校验格式，私钥不回显。
2. 用户报价后下单；在插入本地订单前验证 v2 凭证可用，沿用原有金额和余额容量校验。
3. 生成 UUID `request_id`，通过 `common.Marshal` 序列化一次，对同一份字节计算摘要、签名并发送 `/v2/acquiring/order`；此次不引入自动重试。
4. 验证 HTTP 状态、业务 code、order_id 和 checkout_url，再保存平台订单绑定并返回结账地址。
5. Webhook 沿用 HMAC 验签及原有金额、币种、订单绑定和幂等到账校验。
6. 管理员查单调用 `/v2/acquiring/order?order_id=...`，沿用已有超时、并发限制、状态映射和快照存储逻辑。

## API 合约与权限

本地 `/api/user/infini/amount`、`/api/user/infini/pay` 及现有管理员查单、设置接口保留路由和权限。Webhook `/api/infini/webhook` 继续匿名接收，原因是支付平台回调无法携带登录态，必须通过独立签名验证来源。

上游接口：`POST /v2/acquiring/order`、`GET /v2/acquiring/order?order_id=...`。请求保留官方支持的金额、USD、UUID request_id、client_reference、order_desc、success_url、failure_url、pay_methods。官方 v2 下单字段未列出 notify_url，移除该未公开字段；Webhook 接收地址须在 Infini 商户后台配置，设置界面说明这一点。

响应需兼容文档示例的直接对象及现有 code/message/data 包装；存在 code 时必须为 0，拒绝缺少关键字段的成功响应。查单不能因响应不完整而推断已支付。

## 签名与凭证

使用 Go 标准库 Ed25519。签名内容为以下七行，以 LF 连接并包含末尾 LF：固定 `INFINI-OPENAPI-V2`、Key ID、大写 HTTP 方法、原始转义路径及完整查询串、Unix 秒时间戳、nonce、`SHA-256=<摘要 Base64>`。

每次请求生成 16 字节加密随机 nonce，使用无填充 Base64URL；摘要和签名使用标准带填充 Base64。GET 也计算空请求体摘要。发送 X-API-Key-ID、X-API-Timestamp、X-API-Nonce、Digest、Authorization；Authorization 为 `Ed25519 signature="..."`。移除旧 Date/HMAC 请求头。签名后不重新编码路径或 JSON。

私钥仅接受单个未加密 PKCS#8 Ed25519 PEM 块，拒绝错误算法、旧 HMAC secret、OpenSSH、裸 seed、多块及多余内容。Key ID 校验 32 字符 Base58。凭证错误应在网络请求前失败；不自动退回 v1。

## 配置与数据模型

新增 `setting/infini_setting/`，按 ConfigManager 模式注册 `infini_setting`，字段为 `key_id` 与 `private_key`（均默认空），通过原子快照 getter 读取。更新现有 billing.payment 设置权限范围和表单保存逻辑，沿用逐项设置 API，不新增组保存接口。private_key 纳入敏感配置过滤，读取配置不返回私钥，留空保存保留现有私钥；更换时校验后保存，前端保存后清空输入框。

旧 InfiniApiKey/InfiniApiSecret 不作为 v2 凭证自动迁移，保留历史配置兼容读取但不用于请求；UI 改为明确的 v2 凭证说明及多行私钥输入。InfiniWebhookSecret、开关、Sandbox、金额等现有配置继续使用原逻辑。

不增加表、列、索引、SQL 或迁移，不修改订单结算事务。沿用配置表存储机制。若实现时需要变更数据库行为，补充设计并执行 SQLite/MySQL/PostgreSQL 验证矩阵。

## 关键边界与错误处理

缺失或无效 v2 凭证时不展示可下单入口，下单服务端也拒绝，Webhook 接收能力需与下单凭证分开判断，确保历史订单可继续到账。已有 Webhook secret 的历史回调不得因尚未配置 v2 私钥而被禁用。

保持当前超时与查单并发上限，请求传播 context；认证失败、HTTP 错误、非零业务码、无效 JSON、缺失订单信息均返回本地化的可操作提示。新增控制器错误使用 common 错误助手；日志仅记录必要上下文，不输出私钥、签名或完整上游响应。只调整此次触及的错误分支。

前端复用现有设置组件和保存/加载模式，七种语言同步新增文案；后端新增提示同步 en/zh。旧密钥无法转换成 Ed25519，管理员需自行生成密钥并在商户后台注册公钥。

## Main Chain Impact

改动只在支付管理和充值请求执行，AI relay goroutine 新增同步操作为零，新增异步 hook 为零。不修改 relay、计费表达式或请求中间件。

## Shared Resource Audit

- 配置表/ConfigManager：复用现有设置设施，新增独立 infini_setting 命名空间；无 relay 逐请求读取或锁。
- top_ups、users 和余额缓存：继续使用既有充值和到账流程；users 与 relay 共享，但此次不新增查询、写入、事务或锁，不改锁范围。
- Redis：不新增 key 或操作。
- HTTP：仅支付请求签名和发送，不改变 relay 客户端/连接池；沿用独立查单超时与并发限制。
- DB 连接池：不新增连接池或数据库调用；无额外 worker、goroutine 或共享缓存。

## 测试方案（实现前先写用例）

扩展现有 Infini 测试，使用 testify 和 httptest 本地服务：

- 测试公钥验证真实收到的 POST/GET 签名、完整转义查询串、空体摘要及末尾换行；修改 body/path 后验签失败。
- 检查 nonce 格式、连续请求 nonce 不同、无旧认证头，签名随机数失败可返回错误。
- 私钥格式/算法/多块/空值、Key ID 长度与 Base58 边界、旧 HMAC 配置不可下单。
- v2 下单/查单成功，HTTP 401/403/500、非零业务码、坏 JSON、缺 order_id/checkout_url、订单绑定不匹配和取消 context。
- 新凭证缺失时历史 Webhook 仍能正确验签和处理；保留重复回调、金额/币种校验回归。
- 设置保存校验、敏感私钥不回显、留空不覆盖；前端保存状态和多行 PEM 输入。

执行相关 Go 包测试、前端相关 Vitest、类型检查和 lint。不调用真实 Infini 接口；真实 Sandbox 联调需要管理员注册后的 v2 凭证。

## 实现与验证记录

- 下单和查单均使用 v2；POST/GET 使用同一 Ed25519 签名实现，GET 摘要覆盖空体。签名使用原始 RequestURI，不重新编码查询参数。下单客户端拒绝 HTTP 重定向，传递 context，限制读取响应大小。
- 旧凭证只保留兼容字段，不参与 v2 调用。Webhook 是否可用仅依赖现有启用/合规开关和 Webhook secret，避免迁移期间旧订单回调因缺少 v2 私钥被拒。
- 本地 httptest 验证下单成功（直接对象与包装响应）、HTTP 401/403/500、业务拒绝、坏 JSON、缺少平台订单/结账地址、取消 context；查单验证签名、转义查询串、订单及商户参考号不匹配。
- 配置测试覆盖实际设置 API 存储/重新加载、无效凭证拒绝、私钥不回显及空值保留。无 SQL_DSN，使用隔离 SQLite 内存库作回退；真实 MySQL/PostgreSQL 未执行。测试未修改业务表结构、SQL 或结算事务。
- Webhook 回归覆盖迁移后缺少 v2 私钥时有效历史签名可接收、错误签名/超时签名被拒。到账事务及重复回调逻辑沿用原实现，此次未改动。
- `go test ./controller -run 'Infini|QueryInfini' -count=1` 与 `go test ./setting/infini_setting ./service/settingsaccess`：通过。
- `go build ./...`：通过。
- 前端 Infini 组件 Vitest：通过；`bun run lint` 通过（存在其他文件既有警告）；`bunx tsgo -b` 通过。仓库 `bun run typecheck` 调用不存在的 `bun check -b`，因此使用已安装的 tsgo 执行等效类型检查，未改动该脚本。
- 完整 `go test ./controller ./setting/infini_setting ./service/settingsaccess` 未通过：SQL_DSN 未配置，controller 的既有 `TestAdminAssignCustomerToEmployee_EmployeeNotFound` 在 model.DB 为 nil 时崩溃。未将完整套件标为通过。
- 尚未进行真实 Infini Sandbox/生产联调，未配置或上传商户密钥，未部署。

## 安全依据

参考 [OWASP ASVS](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [Key Management](https://cheatsheetseries.owasp.org/cheatsheets/Key_Management_Cheat_Sheet.html)。本次沿用设置接口鉴权及审计（只记录配置键名），签名包含时间、随机 nonce、完整路径和请求体摘要；业务日志不主动输出私钥/签名，不回显私钥，不改变登录和会话协议。凭证持久化沿用现有支付设置存储设施。SQL 日志与持久化失败路径另见以下审计发现，不能将本节视为全部安全控制已验证通过。

## 2026-11-06 安全审计与修复结果

审计范围为此次 v2 升级及其依赖的设置存储、路由权限、签名和回调路径。两个事项均属于此次接入复用的通用基础设施问题，现有 v2 私钥路径同样受影响。

1. **运维配置风险，按用户确认不列为本次代码缺陷：DEBUG SQL 日志。** `model/gorm_logger.go:54` 在 DEBUG 开启时关闭参数化日志，SQL 写入报错或出现慢查询时可输出完整 PEM。使用生产 `newGormLogger`、临时生成的 Ed25519 密钥和隔离 SQLite 故障触发器复现：DEBUG=false 不输出私钥，DEBUG=true 输出完整私钥。此次不修改 DEBUG 行为。
2. **已修复：Infini v2 凭证写库失败仍返回成功。** 原通用 `model.UpdateOption` 忽略 FirstOrCreate/Save 的 Error。Infini v2 的 `key_id`、`private_key` 现改用已有 `model.UpdateOptionsBulk` 事务保存，提交成功后才发布 OptionMap 和运行时快照；查询、插入、更新或提交失败会返回错误，事务回滚，不发布失败的值。控制器记录错误类型，并返回已有本地化数据库错误提示，不向客户端泄露驱动报错和凭证。保留空私钥不覆盖、私钥不回显逻辑。修改仅覆盖 Infini v2 凭证入口，未改变通用设置保存函数或其他设置项。

初次审计的临时探针已移除，修复回归用例保留在 `controller/infini_v2_test.go`。没有使用真实商户密钥，也未修改生产配置；此审计不构成全仓库渗透测试。

### 凭证保存修复：数据库验证矩阵

测试先行：实现前，SQLite 的 8 个故障用例均复现原问题；实现后通过。回归覆盖 Key ID、私钥两项的查询失败、创建失败、新建占位行后的更新失败、已有行更新失败和成功保存。断言 API 结果、数据库行、OptionMap、运行时快照一致；失败响应不含内部错误标记和 PEM。新建后更新失败还验证事务回滚，不能留下空配置行。

| 数据库 | 版本 | 用例结果 |
| --- | --- | --- |
| SQLite | 3.50.4 | 10/10 通过 |
| MySQL | 5.7.44 | 10/10 通过 |
| PostgreSQL | 9.6.24 | 10/10 通过 |

MySQL/PostgreSQL 使用本次独立创建、仅绑定本机环回地址的 Docker 容器及专用 `infini_save_test` 数据库，测试检查数据库名称前缀后才安装故障触发器，测试后清理触发器和测试行，验证结束删除容器。不修改共享开发库或本机原生数据库服务。本次没有模型、索引、SQL 模板或迁移变更，沿用既有事务保存方法，故不涉及服务器迁移演练或日志数据库。

运行命令：先将 `INFINI_TEST_MYSQL_DSN`、`INFINI_TEST_POSTGRES_DSN` 设置为专用测试库连接，再执行 `go test ./controller -run '^TestInfiniV2CredentialSaveFailures$' -count=1 -v`，三个数据库共 30 个场景全部通过。相关 Infini 下单、查单、签名及回调回归：`go test ./controller -run 'InfiniV2|QueryInfiniPlatformPaymentStatus' -count=1` 通过；`go test ./setting/infini_setting ./service/settingsaccess` 通过。真实支付联调和生产部署未执行。
