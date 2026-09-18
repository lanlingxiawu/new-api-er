# Infini 只受理 USD 与 Stripe 促销码按比例到账

## 目标与范围

本文档描述充值链路（Infini 托管结账、Stripe 动态金额结账）当前的到账口径与保护性校验，覆盖：

- Infini 结算币种限定为 USD（下单、报价、配置保存三处）
- Infini 下单前的「不可入账订单先拒付」预校验与单笔金额上限
- Infini 汇率开关（实时/手动兜底），与 Stripe 对齐
- Stripe 促销码 + 动态 `price_data` 下按实付比例缩放到账额度

不涉及主中继链路（`relay/`）：充值链路与中继链路无共享 Redis key、无共享内存结构；
仅共享 `users` 表的钱包列（`quota`），写入通过 `creditTopUpQuota` 的单条条件 UPDATE 完成，
不持锁跨越外部调用。

## 到账换算口径

两条链路共用 `controller/topup_infini.go: rawQuotaFromPayMoney`：

```
rawQuota = round(实付美元 × 汇率(元/美金) ÷ operation_setting.Price × QuotaPerUnit)
```

该公式只在实付金额为**美元**时成立。Infini 若按某个非 USD 币种的 `unit_price` 收款、
再按上式折算，就会成倍超发或少发（例如 `JPY/unit_price=150` 约超发 150 倍，
`EUR/unit_price=0.92` 约少发 8%），而 webhook 只比对同币种金额，无法发现错额。
因此 Infini 的结算币种限定为 USD。

`rawQuotaFromPayMoney` 在取整前先与 `common.MaxWalletQuota` 比较：
`decimal.IntPart()` 对超出 int64 的值会截断出无意义（可能为负）的结果，
直接写进订单快照会变成负额度。超限时返回 `MaxWalletQuota+1`，由
`validateCreditedQuota` 统一拒绝；不足 1 额度时下限为 1。

## Infini 币种限制

| 位置 | 行为 |
|---|---|
| `controller/topup_infini.go: RequestInfiniPay` / `RequestInfiniAmount` | 解析出的币种非 USD → 返回 `payment.infini_currency_unsupported`（中英双语，含币种代码），不创建订单 |
| `controller/option.go: UpdateOption`（`InfiniCurrency` / `InfiniCurrencies`） | 保存时拒绝：`payment.infini_currency_config_unsupported`；JSON 无法解析时 `payment.infini_currencies_invalid` |
| `controller/topup.go: topup_info` | 只向用户展示 `setting.GetSupportedInfiniCurrencyOptions()` 过滤后的币种 |
| 前端 `web/src/features/system-settings/integrations/infini-settings-section.tsx` | 币种输入与币种 JSON 下方标注「仅支持 USD」，检测到非 USD 时给出行内提示 |

选择「拒绝保存」而不是「保存但标注不支持」：错误配置在管理员面前立刻可见，
避免用户付款后才发现到账口径不对。校验只在保存路径生效，
**数据库中已存在的非 USD 配置仍可正常加载**（`updateOptionMap` 不做校验），
不会导致服务启动失败；这类配置在运行时由下单/报价路径拒绝，并且不再向用户展示。

## Infini 下单前预校验（与其它渠道对齐）

`RequestInfiniPay` 在算出额度快照后、`topUp.Insert()` 之前调用
`rejectInvalidCreditedQuota`（与 epay / stripe / creem / waffo 同一函数）：

1. `validateCreditedQuota`：额度必须可表示（≤ `MaxWalletQuota`）且 > 0；
2. `model.ValidateTopUpQuotaCapacity`：用户当前余额 + 本次到账不得超过钱包上限。

`RequestInfiniAmount`（报价）用同一口径校验，因此前端在拉起支付前就能看到拒绝原因。
没有这层校验时，钱包接近上限的用户可以下单并付款成功，但 `creditTopUpQuota`
会让整个结算事务回滚，订单永久 pending、webhook 反复重试。

单笔充值数量上限 `infiniMaxTopUpAmount = 10000`，与 Stripe 一致，防止超大 `amount`
把报价金额与额度快照推到无意义的取值。

## Infini 汇率开关

| 配置项 | 默认 | 含义 |
|---|---|---|
| `InfiniUseRealtimeRate` | `true` | 用实时 USD/CNY 汇率折算到账；取不到时回退手动汇率 |
| `InfiniExchangeRate` | `8.0` | 手动汇率（元/美金）；手动模式下始终使用，实时模式下作兜底 |

`controller/topup_infini.go: infiniExchangeRate()` 与 `stripeExchangeRate()` 逻辑一致，
手动值 ≤ 0 时回退到 `service.GetUSDCNYRate()`，避免到账额度被算成 0。

每个请求只取一次汇率：`RequestInfiniAmount` 用同一个值做预校验并随响应返回
`exchange_rate`，前端据此展示到账额度，不再自行取实时汇率。
报价与下单是两次 HTTP 请求，实时模式下两者之间仍可能有汇率刷新；
需要完全确定的口径时把 `InfiniUseRealtimeRate` 关掉（与 Stripe 相同权衡）。

两项配置注册在 `settingsaccess` 的 `billing.payment` scope 中。

## Stripe 促销码：按实付比例缩放到账

本仓的 Stripe 结账用动态 `price_data`（按计算出的美元金额收款）并开放促销码
（`StripePromotionCodesEnabled`）。`model/topup.go: Recharge` 的到账规则：

- 币种必须与下单币种一致；
- 实付高于下单预期 → 拒绝（防超发，保留上游语义）；
- 实付低于预期 → **按比例缩放**：`到账 = 快照 × 实付 ÷ 预期`，
  用 decimal 运算后经 `common.WalletQuotaFromDecimalStrict` 严格转换。

若仍按快照满额发放（上游语义），一张 99% off 的券就能用 $0.01 换走 $100 的额度。

边界：缩放后不足 1 额度时按 **1 额度**入账并结单。直接失败会让订单永久 pending、
webhook 无限重试，用户付了钱却拿不到任何结果；与 `rawQuotaFromPayMoney` 的下限策略一致。
快照本身 ≤ 0 时仍返回 `ErrInvalidTopUpQuota`（数据异常，不入账）。

`model/topup.go` 与上游共用，该分叉点在代码中写有注释，避免下次合并被回退。

## 错误处理与 i18n

充值接口沿用既有的 `{"message":"error","data":"..."}` 信封（前端支付流程依赖该形状），
用户可见的新文案走 i18n（`i18n/locales/{en,zh-CN,zh-TW}.yaml`）：

- `payment.infini_currency_unsupported`
- `payment.infini_currency_config_unsupported`
- `payment.infini_currencies_invalid`

管理端保存失败走 `common.ApiErrorMsg` / `common.ApiErrorI18n`（HTTP 200 + `success:false`）。

## 测试

| 测试 | 覆盖 |
|---|---|
| `controller/topup_infini_test.go` | 非 USD 下单/报价被拒且不落单；USD 到账与 Stripe 同口径；钱包上限预校验；金额上下限边界；`rawQuotaFromPayMoney` 上界饱和；手动汇率模式；管理端保存拒绝 |
| `model/topup_test.go: TestRecharge_Stripe_PromoLowerAmountScalesCredit` | 实付=预期 → 满额；50%/1% → 按比例；缩放后不足 1 → 1；超付仍拒绝（`TestRecharge_Stripe_Rejections`） |
| `setting/payment_test.go` | 币种校验函数、受支持币种过滤、新配置默认值 |

测试不访问真实支付网关与实时汇率源（固定手动汇率）。
