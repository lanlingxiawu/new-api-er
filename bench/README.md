# AI 接口链路压测工具

对 new-api 网关做端到端压测，真实执行鉴权、渠道分发、relay 转发、token 计数、计费、日志、提成结算全链路。包含两个纯标准库程序：

- `bench/mockai`：多格式 mock 上游，按请求 URL 同时模拟三种原生上游格式——OpenAI 兼容（`/v1/chat/completions`）、Anthropic Claude（`/v1/messages`）、Google Gemini（`/v1beta/models/{model}:generateContent` / `:streamGenerateContent`）。一个进程即可同时给 OpenAI / Claude / Gemini 三类渠道当上游。响应时间（TTFB、总时长）与内容（词数、词面）随机；热路径零锁、近零分配，确保 mock 不是瓶颈。
- `bench/loadgen`：压测器。闭环并发，流式/非流式按比例混合，SSE 流完整消费；`-format` 切换 ingress 格式（openai/claude/gemini），`-warmup` 预热段不计入统计；输出 p50/p90/p95/p99、流式 TTFB、状态码分布与错误采样。

## 步骤

### 1. 启动 mock 上游

```
go run ./bench/mockai -port 18080 -ttfb-min 50ms -ttfb-max 300ms -latency-min 300ms -latency-max 2s -tokens-min 20 -tokens-max 200
```

同一进程按 URL 同时暴露 OpenAI / Claude / Gemini 三种上游端点，无需分别启动；三者共用同一套 `-ttfb-*` / `-latency-*` / `-tokens-*` / `-error-*` 参数。`curl http://127.0.0.1:18080/stats` 返回分格式（openai/claude/gemini）的 served/stream/errors 计数与在途请求数。

把 `-latency-*` 调小（如 `-latency-min 1ms -latency-max 5ms`）可获得"上游近零延迟"模式，用于压出网关自身极限。

**错误注入**：加 `-error-rate 0.05` 让 mock 按 5% 概率随机返回错误状态码，权重表由 `-error-codes` 控制（默认 `429:1,500:1,502:1,503:1` 等权），用于压网关的重试/换渠道/错误计费路径。注意：
- 不要配置 401/403 —— 网关可能据此自动禁用渠道，压测中途渠道下线会污染结果；
- 429 也可能触发渠道自动暂停，开错误注入前确认后台"失败自动禁用渠道"相关开关已关闭，或把错误率控制在阈值以下。

### 2. 基线校准（证明 mock 不是瓶颈，必做）

绕过网关直压 mock：

```
go run ./bench/loadgen -url http://127.0.0.1:18080/v1/chat/completions -c 500 -d 30s -stream-ratio 0.5
```

直压的吞吐即 mock+压测器的能力上限。后续通过网关压测的目标 QPS 应远低于该值（建议 ≤1/3），否则先调小延迟参数或加大 `-c` 重新校准。

### 3. 在 new-api 配置渠道与令牌

1. 管理后台新建渠道，Base URL 都填 `http://127.0.0.1:18080`，密钥任意填（mock 不校验），分组按需。按要压的渠道类型选择：
   - **OpenAI 兼容**：类型 OpenAI，模型 `gpt-4o-mini`（或 mock `-models` 列表中任意值）。网关转发到 mock 的 `/v1/chat/completions`。
   - **Claude**：类型 Claude/Anthropic，模型如 `claude-3-5-sonnet-20241022`。网关转发到 mock 的 `/v1/messages`。
   - **Gemini**：类型 Gemini，模型如 `gemini-2.0-flash`。网关转发到 mock 的 `:generateContent` / `:streamGenerateContent`。
2. 建一个测试用户/令牌，**额度给足**（压测会真实扣费、写日志、跑提成结算）。
3. 如需压提成链路：给测试用户设置一个员工邀请人。

### 4. 压网关





```
go run ./bench/loadgen -url http://127.0.0.1:3000/v1/chat/completions -token sk-xxxx -model gpt-4o-mini -c 200 -d 120s -stream-ratio 0.5
```

常用参数：`-c` 并发、`-d` 时长（统计窗口）、`-stream-ratio` 流式占比、`-max-tokens`、`-prompt-words`、`-timeout`、`-warmup` 预热时长（这段照常发压但不计入统计，消除冷启动抖动）。

**压测网关的原生 Claude / Gemini ingress**（`-format`，默认 `openai`）：网关除 OpenAI 入口外还提供 Claude、Gemini 原生入口，可分别直压：

```
# Claude 入口：-url 指向 /v1/messages
go run ./bench/loadgen -url http://127.0.0.1:3000/v1/messages -format claude -token sk-xxxx -model claude-3-5-sonnet-20241022 -c 200 -d 120s

# Gemini 入口：-url 用网关根地址，loadgen 自动拼 /v1beta/models/{model}:{action}（流式走 :streamGenerateContent）
go run ./bench/loadgen -url http://127.0.0.1:3000 -format gemini -token sk-xxxx -model gemini-2.0-flash -c 200 -d 120s -stream-ratio 0.5
```

加 `-report report.json` 可在结束时输出 JSON 报告文件（含压测配置、吞吐、成功/失败数、分形态 p50/p90/p95/p99/max、流式 TTFB、状态码分布、错误采样），便于存档和多轮对比。

**资源性能采样**：加 `-perf-url http://127.0.0.1:3000/api/performance/stats -admin-token <root系统访问令牌>` 后，压测期间每 `-perf-interval`（默认 5s）采样一次网关的主机 CPU%/内存%、Go 堆内存、goroutine 数、GC 次数。终端输出 avg/max 摘要，JSON 报告含 `perf_summary` 汇总和 `perf_samples` 完整时间序列（可画曲线对照延迟变化）。系统访问令牌在管理后台"个人设置"中生成（root 账号）。

## 压测期间观察项

- 压测器输出：吞吐、p99、TTFB、非 200 状态码与错误采样（出现 `Error 1040`/超时即连接池见顶）。
- MySQL：`SHOW PROCESSLIST` 看连接占用与堆积语句类型；`SHOW GLOBAL STATUS LIKE 'Threads_connected'`。
- 网关日志：`flush_business_stats:` 行的 items/took/dropped（提成台账刷盘健康度）、`batch update` 行。
- pprof（`ENABLE_PPROF=true` 后 `:8005/debug/pprof/`）：goroutine 数量、阻塞点。
- mock 侧：`curl http://127.0.0.1:18080/stats` 看 active 在途请求数是否与压测并发一致（差值大说明请求堆在网关）。

## 注意

- 压测写真实数据（logs、quota、consumption_costs、commission 等），只在测试环境/测试库执行。
- 压测用户与令牌的额度消耗是真实的，跑长压测前确认额度充足，避免中途变成 403 限额错误干扰结果。
- 本目录代码为压测工具，非业务代码，未使用 `common.Marshal` 封装（避免引入业务依赖）。
