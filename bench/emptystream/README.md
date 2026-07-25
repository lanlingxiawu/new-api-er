# 空流零响应计费缺陷 —— 测试与验收

## 这个缺陷是什么

流式请求上游返回 **HTTP 200 但整条流一条 SSE 数据都不发**(连接挂起后断开 / 首字超时)时,
旧代码会把**请求体估算的输入 token**(可达数百万)当成真实用量计费,输出=0,且不退款。
表现:日志里 **输入=数百万、输出=0、花费>0** 的幽灵消费。

修复后:这种"零响应"请求计费归零,预扣费全额退还。

> 普通压测(`bench/mockai` + `bench/loadgen`)只走正常路径,**触发不了这个 bug 的关键条件**。
> 必须用本目录的 mock 制造"200 + 零 SSE"这个特定条件,才能验证修复。

---

## 前置

- 测试服务器已部署**修复后**的二进制。
- 装了 Python 3(仅用标准库,无需 pip)。

## 一、受控复现测试(最直接,必做)

### 1. 启动空流 mock 上游

```bash
python bench/emptystream/mock.py 18090
```

### 2. 在网关后台建一个"指向 mock 的测试渠道"

- 类型:**Anthropic**(复现 claude 事故)或 OpenAI
- Base URL:`http://127.0.0.1:18090`
- 密钥:随便填(mock 不校验)
- 模型:`claude-opus-4-8`(或你要测的模型)
- 建议:给这个渠道单独分组/优先级,避免抢到真实渠道
- 建一个测试令牌,**额度给足**(会真实预扣费+结算)

### 3. 复现事故:空流 + 大请求

```bash
# mock 切到"零响应"
curl -X POST http://127.0.0.1:18090/__mode/empty

# 发一个 8MB 大 prompt 的流式请求(估算会到数百万 token)
python bench/emptystream/send.py --url http://127.0.0.1:3000 --token sk-你的令牌 \
    --format claude --model claude-opus-4-8 --size-mb 8
```

到后台**日志**页看刚产生的这条记录:

| 结果 | 判定 |
|---|---|
| 输入=0,输出=0,**花费=0**,内容"上游没有返回计费信息,无法扣费" | ✅ **修复生效** |
| 输入=数百万,输出=0,**花费>0** | ❌ 修复未生效(二进制没更新对) |

再确认用户/令牌额度**没有净减少**(预扣费已全额退还)。

### 4. 首字超时变体(可选,更贴近线上"挂起 59~96s")

```bash
# mock 改为:200 后挂起 90s 再断开
python bench/emptystream/mock.py 18090 90
curl -X POST http://127.0.0.1:18090/__mode/hang
python bench/emptystream/send.py --url http://127.0.0.1:3000 --token sk-你的令牌 \
    --format claude --model claude-opus-4-8 --size-mb 8
```

期望同上:花费=0。(注意网关的流式首字超时 `StreamingTimeout` 要小于 90s 才会判超时收尾。)

## 二、正常路径回归(必做,确认没把正常计费改坏)

```bash
curl -X POST http://127.0.0.1:18090/__mode/normal
python bench/emptystream/send.py --url http://127.0.0.1:3000 --token sk-你的令牌 \
    --format claude --model claude-opus-4-8 --size-mb 0.05
```

期望:日志这条 **输入=37、输出=12、花费>0**(按上游真实 token 计费,不是按 8MB 估算)。

## 三、真实流量监控:判断"会不会还出现"

修复不依赖 mock —— 部署后直接在**真实测试/生产库**上定期跑这条 SQL,扫"零输出却按大额输入计费"的幽灵消费。
只要部署时间点之后**没有新行**,就是没再出现。

**MySQL**(把 `:deploy_ts` 换成你部署修复的 Unix 秒级时间戳):

```sql
SELECT FROM_UNIXTIME(created_at) AS t, username, token_name, model_name,
       prompt_tokens, completion_tokens, quota, LEFT(content,40) AS content
FROM   logs
WHERE  type = 2                 -- 消费
  AND  completion_tokens = 0    -- 零输出
  AND  prompt_tokens   > 100000 -- 大额输入(阈值按需调)
  AND  quota           > 0      -- 却扣了费
  AND  created_at      >= :deploy_ts
ORDER BY quota DESC
LIMIT 200;
```

- **部署后此查询恒为空** = 缺陷已消除。
- PostgreSQL:把 `FROM_UNIXTIME(created_at)` 换成 `to_timestamp(created_at)`,`LEFT(...)` 换成 `left(...)`。
- SQLite:去掉 `FROM_UNIXTIME`,直接选 `created_at`。

对照修复前的历史数据(应能查到大量此类行,即事故本体):

```sql
SELECT COUNT(*), SUM(quota)
FROM logs
WHERE type=2 AND completion_tokens=0 AND prompt_tokens>1000000 AND quota>0;
```

建议把上面的监控 SQL 设成告警看板的一条规则(阈值可下探到 `prompt_tokens>50000`),持续盯几天。

---

## 覆盖范围(哪些通道已修)

零响应归零守卫已覆盖全部**流式**处理器:claude、gemini、openai(chat/responses)、
cloudflare、cohere、coze、dify、tencent、xai。非流式处理器(embedding、palm 等)不受此 bug 影响,无需改。
重试场景也已兜住:`ReceivedResponseCount` 每次尝试前归零,避免上一尝试的计数误导本次空流的计费判断。

## 一个已知的边界(不是回归,可不处理)

若上游**发出了 `message_start`**(带真实 input_tokens)但随后截断、输出为 0 —— 这时
`ReceivedResponseCount>0`,**按上游真实输入计费**(正确,不归零)。
只有"连 message_start 都没有"的纯零响应才归零,此时上游自身也没上报任何 usage、几乎不会向你计费,两侧一致。
唯一理论残余:上游已完成并向你计费,但网络在首字节前断开 —— 极罕见,且旧代码在此处也是错的(计估算值)。
如需兜住,建议加"零响应请求打点/告警 + 人工核上游账单",而不是回退到按估算计费。
