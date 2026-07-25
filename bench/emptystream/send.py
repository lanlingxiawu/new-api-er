# -*- coding: utf-8 -*-
"""向网关发一条请求,验证空流零响应是否被错误计费。配合 bench/emptystream/mock.py。
用法:
  python bench/emptystream/send.py --url http://127.0.0.1:3000 --token sk-xxx \
      --format claude --model claude-opus-4-8 --size-mb 8
发完到后台『日志』看这条:空流应 输入=0/输出=0/花费=0;正常应按真实 token 计费。
"""
import argparse, json, time, urllib.request, urllib.error

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", required=True)
    ap.add_argument("--token", required=True)
    ap.add_argument("--format", choices=["claude", "openai"], default="claude")
    ap.add_argument("--model", default="claude-opus-4-8")
    ap.add_argument("--size-mb", type=float, default=8.0)
    ap.add_argument("--stream", default="true")
    a = ap.parse_args()
    base = "lorem ipsum dolor sit amet consectetur adipiscing elit "
    prompt = base * max(1, int(a.size_mb * 1024 * 1024 / len(base)))
    stream = a.stream.lower() == "true"
    if a.format == "claude":
        path = "/v1/messages"
        body = {"model": a.model, "max_tokens": 256, "stream": stream, "messages": [{"role": "user", "content": prompt}]}
        headers = {"Authorization": "Bearer " + a.token, "anthropic-version": "2023-06-01"}
    else:
        path = "/v1/chat/completions"
        body = {"model": a.model, "stream": stream, "messages": [{"role": "user", "content": prompt}]}
        headers = {"Authorization": "Bearer " + a.token}
    data = json.dumps(body).encode()
    print("POST %s%s format=%s model=%s body=%.2fMB stream=%s" % (a.url, path, a.format, a.model, len(data)/1048576, stream))
    req = urllib.request.Request(a.url + path, data=data, method="POST")
    req.add_header("Content-Type", "application/json")
    for k, v in headers.items(): req.add_header(k, v)
    t0 = time.time()
    try:
        r = urllib.request.urlopen(req, timeout=600); txt = r.read().decode("utf-8", "replace"); code = r.status
    except urllib.error.HTTPError as e:
        txt = e.read().decode("utf-8", "replace"); code = e.code
    except Exception as e:
        print("ERROR %.1fs: %s" % (time.time()-t0, e)); return
    print("HTTP %s elapsed %.1fs\nresp(first 300): %s" % (code, time.time()-t0, txt[:300].replace("\n", "\\n")))

if __name__ == "__main__":
    main()
