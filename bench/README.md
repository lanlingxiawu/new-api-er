# AI 接口链路压测工具

对 new-api 网关做端到端压测，真实执行鉴权、渠道分发、relay 转发、token 计数、计费、日志、提成结算全链路。

**单二进制、三合一子命令**：mock 上游、压测器、图形控制台编译进同一个 `bench` 程序，用子命令切换。部署时只需拷贝这一个可执行文件（图形控制台会用它自身拉起 mock / 压测子进程，无需 Go 工具链或源码）：

```
go run ./bench            # 图形控制台（默认，= bench webui）
go run ./bench webui      # 图形控制台
go run ./bench mockai     # mock 上游
go run ./bench loadgen    # 压测器

# 或先编译成单文件，之后到处拷这一个：
go build -o bench.exe ./bench        # Windows；Linux 用 -o bench
./bench.exe                          # 图形控制台
./bench.exe mockai -port 18080 ...
./bench.exe loadgen -url ... -token ...
```

各子命令参数用 `go run ./bench <子命令> -h` 查看。三个子命令：

- `mockai`：多接口 mock 上游，按请求 URL 同时模拟多种上游格式，一个进程即可给不同类型渠道当上游：
  - **chat / 多模态**：OpenAI 兼容 `/v1/chat/completions`、Anthropic Claude `/v1/messages`、Google Gemini `/v1beta/models/{model}:generateContent` / `:streamGenerateContent`；
  - **图片**：`/v1/images/generations`、`/v1/images/edits`、`/v1/edits`；
  - **语音**：TTS `/v1/audio/speech`（二进制音频）、STT `/v1/audio/transcriptions`、`/v1/audio/translations`（`{"text"}`）；
  - **视频**：异步任务，覆盖 7 家主流上游的 submit + 轮询 fetch——doubao/volc（`/api/v3/contents/generations/tasks`）、OpenAI Sora（`/v1/videos` + `/remix` + `/content`）、kling（`/v1/videos/{image2video\|text2video}`）、vidu（`/ent/v2/*` + `/ent/v2/tasks/{id}/creations`）、ali/DashScope（`.../video-synthesis` + `/api/v1/tasks/{id}`）、hailuo/MiniMax（`/v1/video_generation` + `/v1/query/video_generation` + 两步 `/v1/files/retrieve`）、jimeng/即梦（volc 签名 `?Action=CVSync2Async{Submit\|GetResult}Task`）；`-video-process-time` 控制"生成耗时"，`task_id` 内编码提交时刻无状态判定 queued→completed。

  **真实可用的媒体**：图片/语音/视频返回真实可播放的字节（图片默认 PNG、语音默认 WAV、视频默认内嵌真实 MP4，H.264 浏览器可播），响应里的 URL 指向 mock 自身（`/media/...` 或 `/v1/videos/{id}/content`）真实可下载。视频默认用编译期内嵌的示例 MP4（`sample.mp4`），开箱即真视频；需要自定义 MP4/MP3/JPEG 等精确素材时用 `-image-file` / `-audio-file` / `-video-file` 指定真实素材文件。响应时间（TTFB、总时长）与内容随机；热路径零锁、近零分配，确保 mock 不是瓶颈。
- `loadgen`：压测器。两种模式：
  - **闭环基准**（默认）：固定并发，流式/非流式按比例混合，SSE 流完整消费；`-format` 切换请求格式（`openai`/`claude`/`gemini`/`image`/`speech`/`transcription`），`-warmup` 预热段不计入统计；输出 p50/p90/p95/p99、流式 TTFB、状态码分布与错误采样。
  - **生产环境模拟**（`-sim`）：并发随**昼夜曲线 + 抖动 + 随机突发**变化，每个虚拟用户请求间带**思考间隔**，多种请求类型按权重**混合**；固定内存对数直方图统计（长跑不 OOM），按 `-report-interval` **滚动打印**区间 RPS/p50/p95/p99 与当前并发，适合长时间稳定性观察。
  - **多令牌多用户**：`-token` 可填多个（逗号/换行/空格分隔），各并发 worker（虚拟用户）按顺序轮流使用不同令牌，等价于多个真实用户同时请求；worker 数多于令牌数时循环复用。
- `webui`：图形控制台。网页表单填参数 → 生成/复制命令（省得记长命令），或**直接启动** mock / 压测并把实时输出流式回浏览器；支持在浏览器里保存参数预设。页面为中文文案。

## 图形控制台（可选，最省事）

不想记长命令就用它：一个表单页，填好参数即可一键生成命令或直接跑。

```
go run ./bench             # 然后浏览器打开 http://127.0.0.1:18090（= bench webui）
```

- 页面分「Mock 上游」「压测器 loadgen」两栏，loadgen 可切换**闭环基准 / 生产模拟**两种模式，字段随模式自动增减；令牌栏可填多个（每行/逗号一个），模拟多用户并发。
- 每栏底部实时显示等价命令（预览里令牌自动脱敏），「复制命令」复制的是含真实令牌的可直接运行命令；「启动」则由控制台**用本二进制自身**以 `bench mockai` / `bench loadgen` 拉起子进程（无需 Go 工具链；可用 `-mockai` / `-loadgen` 覆盖基命令，`-repo` 指定子进程工作目录），实时输出流式显示在下方日志框，「停止」结束进程。
- 「保存预设」把当前 loadgen 表单存到浏览器本地，下次下拉直接加载。
- 仅监听本地回环（`-bind` 默认 127.0.0.1），会以配置好的基命令 + 表单参数启动子进程，**请勿绑定公网**。

## 步骤

### 1. 启动 mock 上游

```
go run ./bench mockai -port 18080 -ttfb-min 50ms -ttfb-max 300ms -latency-min 300ms -latency-max 2s -tokens-min 20 -tokens-max 200
```

同一进程按 URL 同时暴露 OpenAI / Claude / Gemini 三种上游端点，无需分别启动；三者共用同一套 `-ttfb-*` / `-latency-*` / `-tokens-*` / `-error-*` 参数。`curl http://127.0.0.1:18080/stats` 返回分格式（openai/claude/gemini）的 served/stream/errors 计数与在途请求数。

把 `-latency-*` 调小（如 `-latency-min 1ms -latency-max 5ms`）可获得"上游近零延迟"模式，用于压出网关自身极限。

**错误注入**：加 `-error-rate 0.05` 让 mock 按 5% 概率随机返回错误状态码，权重表由 `-error-codes` 控制（默认 `429:1,500:1,502:1,503:1` 等权），用于压网关的重试/换渠道/错误计费路径。注意：
- 不要配置 401/403 —— 网关可能据此自动禁用渠道，压测中途渠道下线会污染结果；
- 429 也可能触发渠道自动暂停，开错误注入前确认后台"失败自动禁用渠道"相关开关已关闭，或把错误率控制在阈值以下。

### 2. 基线校准（证明 mock 不是瓶颈，必做）

绕过网关直压 mock：

```
go run ./bench loadgen -url http://127.0.0.1:18080/v1/chat/completions -c 500 -d 30s -stream-ratio 0.5
```

直压的吞吐即 mock+压测器的能力上限。后续通过网关压测的目标 QPS 应远低于该值（建议 ≤1/3），否则先调小延迟参数或加大 `-c` 重新校准。

### 3. 在 new-api 配置渠道与令牌

1. 管理后台新建渠道，Base URL 都填 `http://127.0.0.1:18080`，密钥任意填（mock 不校验），分组按需。按要压的接口类型选择：
   - **OpenAI 兼容**：类型 OpenAI，模型 `gpt-4o-mini`（或 mock `-models` 列表中任意值）。网关转发到 mock 的 `/v1/chat/completions`。
   - **Claude**：类型 Claude/Anthropic，模型如 `claude-3-5-sonnet-20241022`。网关转发到 mock 的 `/v1/messages`。
   - **Gemini**：类型 Gemini，模型如 `gemini-2.0-flash`。网关转发到 mock 的 `:generateContent` / `:streamGenerateContent`。
   - **图片**：类型 OpenAI，模型如 `dall-e-3` / `gpt-image-1`。网关转发到 mock 的 `/v1/images/generations`。
   - **语音**：类型 OpenAI，模型如 `tts-1`（TTS）/ `whisper-1`（STT）。网关转发到 mock 的 `/v1/audio/speech` / `/v1/audio/transcriptions`。
   - **视频**：类型选 doubao/volc、Sora、kling、vidu、ali、hailuo、jimeng 任一，Base URL 填 mock。这 7 家上游的"视频创建 + 轮询"格式 mock 都已实现，submit 后轮询到 completed，视频内容真实可下载（默认内嵌真实 MP4，或 `-video-file` 换自定义素材）。
2. 建一个测试用户/令牌，**额度给足**（压测会真实扣费、写日志、跑提成结算）。
3. 如需压提成链路：给测试用户设置一个员工邀请人。

### 4. 压网关





```
go run ./bench loadgen -url http://127.0.0.1:3000/v1/chat/completions -token sk-xxxx -model gpt-4o-mini -c 200 -d 120s -stream-ratio 0.5
```

常用参数：`-c` 并发、`-d` 时长（统计窗口）、`-stream-ratio` 流式占比、`-max-tokens`、`-prompt-words`、`-timeout`、`-warmup` 预热时长（这段照常发压但不计入统计，消除冷启动抖动）。

**多令牌模拟多用户并发**：`-token` 填多个（逗号/换行/空格分隔），worker 按序号轮流使用不同令牌，等价于多个用户同时压测（用于验证按用户维度的限流、配额、提成结算等）。先在后台给多个测试用户各建一个令牌，再：

```
go run ./bench loadgen -url http://127.0.0.1:3000/v1/chat/completions -c 300 -d 120s \
  -token "sk-user1xxxx,sk-user2xxxx,sk-user3xxxx"
```

300 个 worker 会均匀分摊到 3 个令牌上（每令牌约 100 个并发会话）。

**压测网关的原生 Claude / Gemini ingress**（`-format`，默认 `openai`）：网关除 OpenAI 入口外还提供 Claude、Gemini 原生入口，可分别直压：

```
# Claude 入口：-url 指向 /v1/messages
go run ./bench loadgen -url http://127.0.0.1:3000/v1/messages -format claude -token sk-xxxx -model claude-3-5-sonnet-20241022 -c 200 -d 120s

# Gemini 入口：-url 用网关根地址，loadgen 自动拼 /v1beta/models/{model}:{action}（流式走 :streamGenerateContent）
go run ./bench loadgen -url http://127.0.0.1:3000 -format gemini -token sk-xxxx -model gemini-2.0-flash -c 200 -d 120s -stream-ratio 0.5
```

**压测图片 / 语音接口**（一次性响应，`-stream-ratio` 忽略）：

```
# 图片：-url 指向 /v1/images/generations
go run ./bench loadgen -url http://127.0.0.1:3000/v1/images/generations -format image -token sk-xxxx -model dall-e-3 -c 50 -d 60s

# 语音 TTS：-url 指向 /v1/audio/speech（响应为二进制音频）
go run ./bench loadgen -url http://127.0.0.1:3000/v1/audio/speech -format speech -token sk-xxxx -model tts-1 -c 50 -d 60s

# 语音 STT：-url 指向 /v1/audio/transcriptions（loadgen 发 multipart）
go run ./bench loadgen -url http://127.0.0.1:3000/v1/audio/transcriptions -format transcription -token sk-xxxx -model whisper-1 -c 50 -d 60s
```

> **视频**为异步任务（submit → 轮询），mock 已完整支持（`-video-process-time` 控制生成耗时），但 loadgen 暂无 video 驱动；可用 curl 直压 mock 的 `/api/v3/contents/generations/tasks` 验证 submit→running→succeeded 生命周期，或通过网关的 `/v1/video/generations` 走真实任务链路。

### 5. 生产环境模拟（长跑）

模拟真实生产流量：并发随昼夜曲线起伏、叠加随机抖动与突发，用户请求之间有思考间隔，多种请求类型混合。适合跑数小时～数天观察稳定性、内存/连接泄漏、长尾延迟。`-url` 填**网关根地址**（sim 自行拼接各类型 endpoint）。

```
go run ./bench loadgen -sim -url http://127.0.0.1:3000 -token sk-xxxx \
  -c 200 -d 12h -sim-period 1h \
  -sim-mix "chat-stream:45,chat:35,image:10,speech:7,transcription:3" \
  -report-interval 60s -report sim.json \
  -perf-url http://127.0.0.1:3000/api/performance/stats -admin-token <root系统访问令牌>
```

- `-c`：并发上限（虚拟用户池，绝对天花板）；实际活跃并发随曲线在 `-sim-night-frac`×c 与 `-sim-day-frac`×c 之间起伏，突发时向 c 逼近。
- `-sim-period`：一个完整昼夜周期时长（低谷→高峰→回落）。`-sim-jitter` 抖动幅度；`-sim-spike-rate` / `-sim-spike-mult` 突发概率与倍数。
- `-sim-think-min` / `-sim-think-max`：用户两次请求之间的思考间隔（默认 0.5s～8s）。
- `-sim-mix`：请求类型权重（`chat`/`chat-stream`/`claude`/`claude-stream`/`image`/`speech`/`transcription`）；各类型模型用 `-model`（chat）、`-image-model`、`-tts-model`、`-stt-model`、`-claude-model`。
- `-report-interval`：滚动报告间隔；终端每隔该时长打印一行「当前并发 + 区间 RPS + p50/p95/p99」，`-report` 的 JSON 含完整 `timeline` 时间序列（可画曲线）。
- 统计用固定 4096 桶对数直方图（约 32KB，与运行时长无关），长跑不累积内存。`-perf-url` / `-mysql-dsn` 资源采样在 sim 模式同样生效。

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
