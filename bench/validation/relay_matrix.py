"""Local-only relay contract matrix; uses a synthetic upstream, never real AI.

The state file is private and contains username/password/api_token for an already
initialized isolated gateway. Results contain case names and status, no secrets.
"""
import argparse
import io
import json
import threading
import urllib.error
import urllib.request
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse


class Fixture(BaseHTTPRequestHandler):
    calls = []

    def log_message(self, *_):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        payload = json.loads(body) if "json" in self.headers.get("Content-Type", "") else {}
        self.calls.append((self.path, payload))
        usage = {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
        result = {"id": "chatcmpl-fixture", "object": "chat.completion", "model": payload.get("model"),
                  "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}], "usage": usage}
        if self.path.endswith("/embeddings"):
            result = {"object": "list", "data": [{"object": "embedding", "index": 0, "embedding": [0.1, 0.2]}], "usage": usage}
        elif self.path.endswith("/images/generations"):
            result = {"created": 1, "data": [{"b64_json": "aGVsbG8="}]}
        elif self.path.endswith("/moderations"):
            result = {"id": "modr-fixture", "model": payload.get("model"), "results": [{"flagged": False, "categories": {}, "category_scores": {}}]}
        elif self.path.endswith("/responses"):
            result = {"id": "resp_fixture", "object": "response", "status": "completed", "model": payload.get("model"),
                      "output": [{"id": "msg_fixture", "type": "message", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": "ok", "annotations": []}]}],
                      "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}
        elif self.path.endswith("/messages"):
            result = {"id": "msg_fixture", "type": "message", "role": "assistant", "model": payload.get("model"), "content": [{"type": "text", "text": "ok"}], "stop_reason": "end_turn", "usage": {"input_tokens": 10, "output_tokens": 5}}
        elif ":generateContent" in self.path:
            result = {"candidates": [{"index": 0, "content": {"role": "model", "parts": [{"text": "ok"}]}, "finishReason": "STOP"}], "usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}}
        elif self.path.endswith("/completions") and not self.path.endswith("/chat/completions"):
            result["choices"] = [{"index": 0, "text": "ok", "finish_reason": "stop"}]
        data = json.dumps(result).encode()
        content_type = "application/json"
        if self.path.endswith("/audio/speech"):
            audio = io.BytesIO()
            with wave.open(audio, "wb") as wav:
                wav.setnchannels(1)
                wav.setsampwidth(2)
                wav.setframerate(8000)
                wav.writeframes(bytes(1600))
            data = audio.getvalue()
            content_type = "audio/wav"
        if payload.get("stream"):
            chunk = {"id": "chatcmpl-fixture", "object": "chat.completion.chunk", "model": payload.get("model"), "choices": [{"index": 0, "delta": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}], "usage": usage}
            data = ("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode()
            content_type = "text/event-stream"
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--gateway", default="http://127.0.0.1:23000")
    parser.add_argument("--state", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    if urlparse(args.gateway).hostname not in ("127.0.0.1", "localhost"):
        parser.error("only an isolated loopback gateway is allowed")
    state = json.loads(Path(args.state).read_text(encoding="utf-8"))

    def request(method, path, body=None, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        data = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(args.gateway + path, data=data, headers=headers, method=method)
        try:
            resp = urllib.request.urlopen(req, timeout=20)
        except urllib.error.HTTPError as exc:
            resp = exc
        return resp.status, resp.read()

    _, body = request("POST", "/api/user/login", {"username": state["username"], "password": state["password"]})
    admin = json.loads(body)["data"]["access_token"]
    server = ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    names = []
    results = []
    try:
        for channel_type, models in [(1, "gpt-4o,text-embedding-3-small,dall-e-3,text-moderation-latest,tts-1"), (14, "claude-sonnet-4-5-20250929"), (24, "gemini-2.5-flash")]:
            name = f"validation-matrix-{channel_type}"
            _, body = request("POST", "/api/channel/", {"mode": "single", "channel": {"name": name, "type": channel_type, "key": "synthetic-fixture", "base_url": f"http://127.0.0.1:{server.server_port}", "models": models, "group": "default", "status": 1, "priority": 100, "cost_ratio": 1}}, admin)
            assert json.loads(body)["success"], "fixture channel creation failed"
            names.append(name)
        chat = {"model": "gpt-4o", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 16}
        cases = [
            ("chat", "/v1/chat/completions", chat, 200, True),
            ("explicit zero", "/v1/chat/completions", dict(chat, temperature=0, top_p=0, presence_penalty=0, frequency_penalty=0), 200, True),
            ("stream with usage", "/v1/chat/completions", dict(chat, stream=True, stream_options={"include_usage": True}), 200, True),
            ("tools", "/v1/chat/completions", dict(chat, tools=[{"type": "function", "function": {"name": "weather", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}]), 200, True),
            ("legacy completion", "/v1/completions", {"model": "gpt-4o", "prompt": "hello", "max_tokens": 16}, 200, True),
            ("responses", "/v1/responses", {"model": "gpt-4o", "input": "hello"}, 200, True),
            ("embedding", "/v1/embeddings", {"model": "text-embedding-3-small", "input": ["hello", "world"]}, 200, True),
            ("image", "/v1/images/generations", {"model": "dall-e-3", "prompt": "cat", "n": 1, "size": "1024x1024"}, 200, True),
            ("moderation", "/v1/moderations", {"model": "text-moderation-latest", "input": "hello"}, 200, True),
            ("Claude", "/v1/messages", {"model": "claude-sonnet-4-5-20250929", "max_tokens": 16, "messages": [{"role": "user", "content": "hello"}]}, 200, True),
            ("Gemini", "/v1beta/models/gemini-2.5-flash:generateContent", {"contents": [{"role": "user", "parts": [{"text": "hello"}]}]}, 200, True),
            ("OpenAI to Claude", "/v1/chat/completions", dict(chat, model="claude-sonnet-4-5-20250929"), 200, True),
            ("Claude to OpenAI", "/v1/messages", chat, 200, True),
            ("OpenAI to Gemini", "/v1/chat/completions", dict(chat, model="gemini-2.5-flash"), 200, True),
            ("Gemini to OpenAI", "/v1beta/models/gpt-4o:generateContent", {"contents": [{"role": "user", "parts": [{"text": "hello"}]}]}, 200, True),
            ("speech synthesis", "/v1/audio/speech", {"model": "tts-1", "input": "hello", "voice": "alloy", "response_format": "wav"}, 200, True),
            ("malformed JSON", "/v1/chat/completions", b'{"model":', 400, False),
            ("missing model", "/v1/chat/completions", {"messages": chat["messages"]}, 400, False),
            ("negative max tokens", "/v1/chat/completions", dict(chat, max_tokens=-1), 400, False),
            ("overflow max tokens", "/v1/chat/completions", dict(chat, max_tokens=2**63-1), 400, False),
            ("image overflow", "/v1/images/generations", {"model": "dall-e-3", "prompt": "cat", "n": 2**31-1}, 400, False),
            ("Gemini count disabled", "/v1beta/models/gemini-2.5-flash:countTokens", {}, 404, False),
        ]
        for name, path, data, expected, forwarded in cases:
            before = len(Fixture.calls)
            status, body = request("POST", path, data, state["api_token"])
            passed = status == expected and (len(Fixture.calls) > before) == forwarded
            if expected == 200:
                passed = passed and (b"[DONE]" in body if isinstance(data, dict) and data.get("stream") else b'"error"' not in body)
            if name == "explicit zero" and forwarded and len(Fixture.calls) > before:
                passed = passed and all(Fixture.calls[-1][1].get(k) == 0 for k in ("temperature", "top_p", "presence_penalty", "frequency_penalty"))
            results.append({"case": name, "status": status, "expected": expected, "forwarded": len(Fixture.calls)-before, "passed": passed})
        for path in ("/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/images/generations", "/v1/embeddings", "/v1/audio/speech", "/v1/rerank", "/v1/videos"):
            status, _ = request("POST", path, {})
            results.append({"case": "anonymous " + path, "status": status, "passed": status == 401})
    finally:
        _, body = request("GET", "/api/channel/?page_size=100", token=admin)
        for channel in json.loads(body).get("data", {}).get("items", []):
            if channel["name"] in names:
                request("DELETE", f'/api/channel/{channel["id"]}', token=admin)
        server.shutdown()
        server.server_close()
        Path(args.report).write_text(json.dumps(results, indent=2), encoding="utf-8")
    print(json.dumps({"total": len(results), "passed": sum(r["passed"] for r in results), "failures": [r for r in results if not r["passed"]]}))
    return 0 if all(r["passed"] for r in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
