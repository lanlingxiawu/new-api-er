# 表单校验提示本地化（zod 默认文案）

状态：已实施（2026-10-01）

## 问题

zod 4 的英文默认文案由 `zod` 包内的模块副作用（`config(en())`）注册，而 `zod` 的 `package.json` 声明了 `sideEffects: false`，生产构建会把这段注册摇树删掉。结果是所有没写自定义文案的 schema 校验失败时都只显示 `Invalid input`（例如上限 100 的字段填 200），用户看不出该怎么改。开发模式不摇树，所以只在生产构建出现。

## 做法

本分支新文件 `web/src/lib/zod-error-messages.ts`：

- `zodErrorMessage(issue)` 按 zod 问题类型生成可操作的本地化提示，文案走 i18n（7 个语言文件）。
- `installZodErrorMessages()` 用 `z.config({ localeError })` 注册；由 `main.tsx` 显式调用（显式调用不会被摇树删掉）。
- 用 `localeError` 槽位，优先级最低：schema 里自己写的文案（如 `z.string().min(1, t('…'))`）仍优先生效。
- 文案在校验时用当前语言生成；`FormMessage` 再对其 `t()` 一次时找不到同名键，原样显示。

| zod 问题 | 提示 |
|---|---|
| `invalid_type`：期望数字（含 NaN、空） | 请输入有效的数值 |
| `invalid_type`：`int()` 收到小数 | 请输入整数 |
| `invalid_type`：其他类型且值为空 | 此项为必填项 |
| `too_small` / `too_big`（数字） | 不能小于 {{min}} / 必须大于 {{min}}；不能大于 {{max}} / 必须小于 {{max}} |
| `too_small` / `too_big`（字符串） | 最小值 ≤1 时「此项为必填项」；否则「至少 / 最多 {{count}} 个字符」，`length()` 为「必须为 {{count}} 个字符」 |
| `too_small` / `too_big`（数组、Set） | 最小值 ≤1 时「请至少选择一项」；否则「至少 / 最多 / 必须正好包含 {{count}} 项」 |
| `not_multiple_of` | 必须是 {{divisor}} 的倍数 |
| `invalid_format` | 邮箱、网址各自提示；其他格式「格式不正确，请检查后重新输入」 |
| `invalid_value`（enum、literal） | 请从可选项中选择 |
| 其他 | 请输入有效的值 |

## 影响面

纯前端，影响全站所有 zod 表单的默认提示文案，不改变校验规则与保存行为。`main.tsx` 增加 2 行（import 与调用），与已有的 `installToastDedupe()` 等写法一致。

## 测试

前端无测试框架（Rule 15.7）：`bun run typecheck`、`bun run lint` 通过；用一次性脚本以真实 zh/en 语言包逐类核对文案（含「schema 自带文案优先」）；部署测试服后在日志导出设置页实际触发越界提示核验。
