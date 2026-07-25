# -*- coding: utf-8 -*-
"""
空流 / 首字超时 mock 上游 —— 专门复现"HTTP 200 但零 SSE 数据"这个计费缺陷的触发条件。
支持 Claude 原生(/v1/messages)与 OpenAI 兼容(/v1/chat/completions)两种上游格式。

用法:
    python bench/emptystream/mock.py 18090

切换行为(默认 empty):
    curl -X POST http://127.0.0.1:18090/__mode/empty    # 200 + 零 SSE(复现 bug 触发条件)
    curl -X POST http://127.0.0.1:18090/__mode/normal   # 200 + 正常 SSE(带真实 usage)
    curl -X POST http://127.0.0.1:18090/__mode/hang      # 200 后挂起 ttfb 秒再断开(首字超时)

渠道配置:在 new-api 后台建渠道,Base URL 指向本 mock:
    - Claude 渠道(类型 Anthropic): Base URL = http://127.0.0.1:18090
    - OpenAI 渠道:                  Base URL = http://127.0.0.1:18090
密钥随便填(mock 不校验),模型填 claude-opus-4-8 / gpt-4o 等。
"""
import sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODE = {"v": "empty"}
SEQ = []            # 按请求消费的行为队列(用于复现"渠道A失败→渠道B"的重试)
HANG_SECONDS = 90
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18090

CLAUDE_EVENTS = [
    ('message_start', '{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"usage":{"input_tokens":37,"output_tokens":1}}}'),
    ('content_block_start', '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'),
    ('content_block_delta', '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}'),
    ('content_block_delta', '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}'),
    ('content_block_stop', '{"type":"content_block_stop","index":0}'),
    ('message_delta', '{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}'),
    ('message_stop', '{"type":"message_stop"}'),
]
OPENAI_CHUNKS = [
    '{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}',
    '{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}',
    '{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":37,"completion_tokens":12,"total_tokens":49}}',
]

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass

    def _drain(self):
        cl = int(self.headers.get('Content-Length', '0') or 0)
        if cl: self.rfile.read(cl)

    def _sse_head(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.end_headers()

    def do_POST(self):
        if self.path.startswith('/__mode/'):
            MODE["v"] = self.path.rsplit('/', 1)[-1]
            self.send_response(200); self.end_headers()
            self.wfile.write(b'{"mode":"%s"}' % MODE["v"].encode()); return
        if self.path.startswith('/__seq/'):
            # /__seq/error,normal —— 按请求依次消费,耗尽后回落到 MODE。用于复现重试链路。
            spec = self.path.rsplit('/', 1)[-1]
            SEQ[:] = [s for s in spec.split(',') if s] if spec else []
            self.send_response(200); self.end_headers()
            self.wfile.write(b'{"seq":"%s"}' % spec.encode()); return

        is_claude = self.path.endswith('/v1/messages')
        is_openai = self.path.endswith('/chat/completions') or self.path.endswith('/v1/completions')
        if not (is_claude or is_openai):
            self.send_response(404); self.end_headers(); return

        self._drain()
        m = SEQ.pop(0) if SEQ else MODE["v"]
        if m == "error":
            # 上游可重试错误(500),整段无 SSE、无 usage —— 触发换渠道重试
            self.send_response(500)
            self.send_header('Content-Type', 'application/json'); self.end_headers()
            if is_claude:
                self.wfile.write(b'{"type":"error","error":{"type":"api_error","message":"mock upstream 500"}}')
            else:
                self.wfile.write(b'{"error":{"message":"mock upstream 500","type":"server_error"}}')
            return
        if m == "partial":
            # 先发 message_start + 内容(ReceivedResponseCount>0),再发 error 事件 → 可重试
            # 用于验证:上一尝试已收数据时,本次空流是否仍被误按估算计费(靠 controller 重试归零守住)
            self._sse_head()
            if is_claude:
                for ev, data in CLAUDE_EVENTS[:4]:   # message_start..content_block_delta
                    self.wfile.write(("event: %s\ndata: %s\n\n" % (ev, data)).encode()); self.wfile.flush(); time.sleep(0.01)
                err = '{"type":"error","error":{"type":"overloaded_error","message":"mock mid-stream error"}}'
                self.wfile.write(("event: error\ndata: %s\n\n" % err).encode()); self.wfile.flush()
            else:
                self.wfile.write(("data: %s\n\n" % OPENAI_CHUNKS[0]).encode()); self.wfile.flush()
                self.wfile.write(b'data: {"error":{"message":"mock mid-stream error","type":"server_error"}}\n\n'); self.wfile.flush()
            return
        if m == "normal":
            self._sse_head()
            if is_claude:
                for ev, data in CLAUDE_EVENTS:
                    self.wfile.write(("event: %s\ndata: %s\n\n" % (ev, data)).encode()); self.wfile.flush(); time.sleep(0.01)
            else:
                for ch in OPENAI_CHUNKS:
                    self.wfile.write(("data: %s\n\n" % ch).encode()); self.wfile.flush(); time.sleep(0.01)
                self.wfile.write(b"data: [DONE]\n\n"); self.wfile.flush()
        elif m == "hang":
            # 200 后挂起 HANG_SECONDS 秒不发任何数据,再断开 —— 复现"首字超时"
            self._sse_head()
            try: time.sleep(HANG_SECONDS)
            except Exception: pass
        else:  # empty
            # 200 + event-stream 头,但一条 SSE 数据都不发,立即结束 —— bug 触发条件
            self._sse_head()
            time.sleep(0.05)

    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok mode=%s' % MODE["v"].encode())

if __name__ == '__main__':
    if len(sys.argv) > 2: HANG_SECONDS = int(sys.argv[2])
    print("empty-stream mock on %d (mode=%s, hang=%ss)" % (PORT, MODE["v"], HANG_SECONDS), flush=True)
    ThreadingHTTPServer(('127.0.0.1', PORT), H).serve_forever()
