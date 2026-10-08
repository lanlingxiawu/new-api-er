# 本地 token 估算：分词有界化与媒体取不到时的兜底

对应测试报告 `docs/test-reports/2026-09-29-openai-format.md` 的 D-1（高）与 D-7（低），均继承自上游。

## 目标与范围

1. **分词耗时有界、线性**（D-1）。`service.CountTextToken` 对 OpenAI 文本模型用 tiktoken 真分词。
   tiktoken 的预分词正则把一段连续字母 / 数字 / 标点 / 空白切成一个 piece，BPE
   （`codec.mergePairs`）对单个 piece 是 O(n²)：连续 "a" 20 KB 196 ms、40 KB 726 ms、80 KB 2.99 s；
   33 MB 请求体在 `EstimateRequestToken`（`controller/relay.go` 分发后、预扣前同步执行）里占满一核数天，
   客户端断开也不能取消。正则引擎还会把整段输入复制成 `[]rune`（4 字节/字符）。
2. **媒体取不到不拒绝请求**（D-7）。流式请求里的图片 URL 由网关下载以计 token；下载失败（403、墙、慢站、
   SSRF 拒绝、超限）或解不出图片尺寸时，原先直接返回 500 `count_token_failed`，请求到不了上游。
3. 文档修正：`MAX_REQUEST_BODY_MB` 代码默认值为 128（`common/init.go`，与 main / upstream 一致，
   `.env.example` 也是 128），README 各语言版本误写为 32，已改为 128。

不在范围：请求体大小上限本身、非 OpenAI 模型的字符加权估算（`EstimateTokenByModel`，本来就是线性单遍扫描）。

## 方案一：分段计数（`service/tokenizer.go`）

`getTokenNum` → `countTokensBounded`，不再把整段文本交给 `Codec.Count`：

| 常量 | 值 | 作用 |
|---|---|---|
| `tokenizeMaxRunBytes` | 256 | 连续 256 字节内没有任何安全边界就在此处强制切开。每个 piece 必落在两个安全边界之间，因此 piece ≤ ~256 字节，总成本线性 |
| `tokenizeMaxSegmentBytes` | 16 KiB | 每段上限，到达后回退到最近的安全边界切段，同时限制正则引擎的 `[]rune` 拷贝 |
| `tokenizeMaxExactBytes` | 8 MiB | 只精确分词前 8 MiB，其余按前缀实测的 token/字节比例外推（向上取整），单次调用 CPU 封顶在数秒（8 MiB 连续 "a" 1.4 s；每字节一个 token 的输入约 3–6 s，与整段直接计数同价） |

强制切分按"距上一个安全边界的距离"判断，而不是按"同一字符类的连续长度"：后者挡不住由多类字符交替
组成的单个 piece（cl100k 把 `.` + 组合符交替视为一个标点 piece；o200k 的 `[\r\n/]*` 后缀让 `.\n/\n/…`
成为一个 piece），1 MiB 这类输入在只按同类长度切分时需 5.6–6.9 s。

**安全边界**（`isSafeTokenBoundary`）：在此处切开，cl100k / o200k / p50k / r50k 四套切分正则产生的 piece
与整段切分完全相同，因此计数精确相等。依据：这些正则不回看，唯一的前瞻 `\s+(?!\S)` 只在前一字符是空白时
起作用（这种位置一律不算安全），且 regexp2 的 `\s` 即 `unicode.IsSpace`，所以只要没有任何 piece 能同时
包含边界两侧字符即可。能把两个非空白字符连成一个 piece 的只有：字母串（o200k 还并入组合符和 `'s`/`'t`…
后缀）、"一个非字母非数字前缀 + 字母串"、数字串、"标点/符号/组合符串 + 结尾 `\r\n`（o200k 为 `\r\n/`）"、
"撇号 + 缩写字母"。其余都安全：

- 非空白 → `\r` `\n` 以外的空白；
- 字母 / 数字 → `\r` `\n`（只有标点 piece 会吞掉结尾换行）；
- 字母 → 数字，或撇号以外的标点；
- 数字 → 非数字；
- 标点 / 符号 / 组合符 → 数字。

**误差**：只有强制切分会改变计数，每次约 ±1 token。正常文本（英文、代码、带标点的中日文、JSON、数字数组、
base64 / 十六进制串）每隔几个字节就有安全边界，计数与整段完全相同。现实中唯一会触发的是不写空格也不写
标点的文字：连续约 86 个以上汉字 / 泰文等，每处强制切分可能 ±1 token（实测 636 字节无标点中文 137 对 136）。
8 MiB 精确前缀大于任何模型上下文（1M token ≈ 4 MB 英文），上游能接受的文本不会被外推；超过 8 MiB 的外推
误差在均匀文本上 < 0.01%（实测 2677470 对 2677437）。病态输入 12 KB 的实测偏差：连续空格 / 换行 0.8%，
其余（字母、大小写交替、数字、标点、无标点中文、类 base64、标点+组合符、`.\n/`、缩写串）为 0。

非法 UTF-8 字节会被正则引擎换成 3 字节的 U+FFFD，256 字节的片段可膨胀为 768 字节的 piece（连续 `\xff`
约 1.4 s/MiB）。经 relay 进入的文本都来自 `common.Unmarshal`（`encoding/json` 已把非法字节替换掉），
到不了这里；若将来出现不经 JSON 的调用方，需要把宽度为 1 的 `RuneError` 按 3 字节计入片段长度。

## 方案二：媒体兜底（`service/token_counter.go`）

- 第一轮取文件类型时 `LoadFileSource` 失败：记 `unreadable[i]`，打 `LogWarn`（不含完整 URL，见下），继续。
- 第二轮：OpenAI 文本模型的图片若 `unreadable[i]`，直接用 `imageTokenFallback(model, detail)`，
  **不再二次下载**；`getImageToken` 返回错误（解码失败等）同样改用兜底并 `LogWarn`。
- `imageTokenFallback` = 关闭本地媒体计数（`GET_MEDIA_TOKEN=false`）时 `getImageToken` 的返回值：
  glm-4 为 1047；tile 类模型 `detail=low` 为 `baseTokens`；其余为 `3 × baseTokens`
  （4o 255、4o-mini 8499、gpt-5 210、o1/o3 225……）。模型分类抽成 `imageTokenModelParams`，与 `getImageToken` 共用。
  高分辨率图片的真实值可达数千，兜底会低估，只影响预扣（见"行为变化"）。
- 类型未知且取不到的文件仍按原默认 4096；非 OpenAI 模型的图片仍按 520。
- 日志脱敏：URL 的 query 可能带签名，日志只写 `url host=<host>`。下载错误会以改写后的形式引用 URL
  （userinfo 被替换为 `***`、路径被百分号编码、重定向目标），只替换原字符串挡不住，所以错误文本中
  所有形如 `scheme://…` 的片段都替换为 `<url>`。

上游 main 在同一路径同样直接失败（`fmt.Errorf("error getting file type")`，自 2025-08 的
`77b100ba2b` 起），代码与提交中没有"必须失败"的设计理由：本地计数只影响预扣估算，结算以上游 usage 为准。
改为兜底后，上游自己取得到的图片正常转发；上游也取不到则由上游返回 4xx，预扣照常退还。
SSRF 拒绝同样只意味着网关不去取，URL 交给上游在其网络中获取，不扩大网关自身的 SSRF 面。

## 行为变化

- 正常文本（≤ 8 MiB）token 数与修复前完全相同；例外是连续 86 个以上无标点的汉字 / 泰文等，每 256 字节可能 ±1。
- 病态长串：估算耗时从平方级降为线性（~5–6 MB/s/核，与普通文本相同），计数偏差 ≤ 1%。
- 图片取不到 / 解不出：请求不再 500，按兜底估算预扣并转发上游。仅当上游不返回 usage 时，
  结算会沿用这份较低的估算（与关闭 `GET_MEDIA_TOKEN` 时一致）。

## 错误处理

分词不返回错误（与原实现一致，`Count` 的正则错误被忽略）。媒体读取失败只打 `LogWarn`，不再中断请求。
没有新增面向用户的文案，不涉及 i18n。

## Main Chain Impact

1. **同步执行**：`countTokensBounded` 与原 `Codec.Count` 一样在 relay goroutine 上同步执行
   （`EstimateRequestToken`，以及非流式 / responses 无 usage 时的补算、realtime 计数）。新增的只是逐 rune
   的字符分类扫描；≤ 256 字节的文本直接走原路径。基准（o200k，16 核开发机，`-benchtime 5x`）：

   | 输入 | 修复前 | 修复后 |
   |---|---|---|
   | 100 B 英文 | 32.5 µs | 21.9 µs |
   | 2 KB 英文 | 382 µs | 380 µs |
   | 2 KB 中文 | 297 µs | 307 µs |
   | 8 KB 代码 | 2.24 ms | 2.13 ms |
   | 64 KB 混合 | 13.3 ms | 13.4 ms |
   | 连续 "a" 20 / 40 / 80 KB | 180 ms / 684 ms / 2.95 s | 3.5 / 6.6 / 13.3 ms |
   | 连续 "a" 1 / 2 / 4 / 8 MB | 分钟～天级 | 0.20 / 0.35 / 0.70 / 1.35 s（线性） |
   | 连续 "a" 10 / 32 MB | 天级 | 1.36 / 1.35 s（超过 8 MiB 后封顶） |
   | 其余病态输入各 1 MiB（空格、换行、标点、数字、无标点中文、类 base64、标点+组合符、`.\n/`、缩写串） | — | 0.11–0.36 s |

   正常请求耗时差异在噪声范围内，热路径没有变慢。媒体兜底只改变失败分支，成功路径不变，且失败时少一次重复下载。
2. **异步**：无。

## Shared Resource Audit

- 编码器：沿用 `tokenEncoderMap` 中的共享 `tokenizer.Codec`（只读；regexp2 自带 runner 池，可并发）。
  分段后每请求多几次 `Count` 调用，不新增锁。
- 不涉及 Redis、DB、连接池、goroutine 池；不新增内存常驻结构。每次调用的临时内存由"整段 `[]rune`"
  降为"≤ 16 KiB 段的 `[]rune`"。
- 媒体：沿用 `LoadFileSource` 的 context 缓存；失败不缓存，但已标记为不可读，不会在同一请求内重复下载。

## Concurrency Analysis（100k RPM）

每请求 DB 0 次、Redis 0 次、锁 0 个（编码器缓存命中为 RLock，与原实现相同）、goroutine 0 个。
CPU 与文本长度线性，单次调用上限为数秒（8 MiB 精确前缀，见方案一），不再存在单请求无限占核。

## 测试

- `service/tokenizer_test.go`
  - 真实文本（英文 / 中文 / 日文 / 代码 / 多语种与各类边角字符 / 混合），1 B～100 KB 多种长度 × 四套编码：
    分段计数与整段计数逐一相等；
  - 性质测试：对语料以及 3000 条固定种子的"敌意字符表"随机串（撇号与缩写字母及其大小写折叠变体、CR/LF、`/`、
    组合符、各类 Unicode 空白、数字、全角标点、emoji、非法 UTF-8），每一个被判为安全的边界，左右分别计数之和
    都等于整段计数；
  - 边界判定与字符分类的判定表；
  - 病态输入 12 KB 的偏差上限；每种病态输入 1 MiB 的耗时上限（cl100k、o200k）；
  - 超过 8 MiB 的外推（32 MiB 连续字母、略超上限的正常文本、恰好 8 MiB 精确）；
  - 基准 `BenchmarkTokenizeNormalPrompt`、`BenchmarkTokenizeUnbroken`（`*/Direct` 为修复前路径）。
- `service/token_counter_test.go`（关闭 SSRF 防护让下载真正打到 httptest 服务）
  - 图片 URL 返回 403：多个模型都不报错、按兜底计数、恰好下载一次（去掉 `unreadable` 标记即失败）；
  - `detail=low` 兜底为 base；SSRF 拒绝时同样兜底且不发请求；可读图片仍按真实尺寸计数且只下载一次；
  - 类型未知的 URL 取不到按 4096；非 OpenAI 模型按 520；base64 解不出图片按兜底；
  - 非流式且未开启非流媒体计数时不下载（原行为）；
  - `imageTokenFallback` 与关闭媒体计数时 `getImageToken` 的返回值逐模型、逐 detail 一致；
  - 用真实 `http.Client` 错误（userinfo + 空格 + 中文路径的连接中断、重定向目标）验证日志不泄露 URL、签名与口令。
