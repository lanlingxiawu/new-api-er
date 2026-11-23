"""Authenticated native Xunfei WS fixture, confined to the local Docker network.

The official adapter hardcodes spark-api.xf-yun.com. A private network alias and
fixture-only CA make the unmodified official binary reach this TLS server.
Never log query strings: Xunfei authentication is carried in the URL.
"""
import base64
import hashlib
import hmac
import http.server
import json
import os
from pathlib import Path
import re
import ssl
import struct
import threading
import time
import urllib.parse

ROOT = Path(os.environ['NATIVE_FIXTURE_ROOT'])
CREDENTIALS = json.loads((ROOT / 'credentials.private.json').read_text())
LOCK = threading.Lock()


def frame_read(stream):
    head = stream.read(2)
    if len(head) != 2:
        raise EOFError()
    size = head[1] & 127
    if size == 126:
        size = struct.unpack('!H', stream.read(2))[0]
    elif size == 127:
        size = struct.unpack('!Q', stream.read(8))[0]
    if size > 20 << 20:
        raise ValueError('fixture request exceeds test boundary')
    mask = stream.read(4) if head[1] & 128 else b''
    data = stream.read(size)
    if mask:
        data = bytes(value ^ mask[index % 4] for index, value in enumerate(data))
    return head[0] & 15, data


def frame_write(stream, data):
    size = len(data)
    header = bytes([129, size]) if size < 126 else bytes([129, 126]) + struct.pack('!H', size) if size < 65536 else bytes([129, 127]) + struct.pack('!Q', size)
    stream.write(header)
    stream.write(data)
    stream.flush()


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_):
        pass

    def do_GET(self):
        parsed = urllib.parse.urlsplit(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        authorized = False
        try:
            auth = base64.b64decode(query['authorization'][0]).decode()
            fields = dict(re.findall(r'(\w+)="([^"]*)"', auth))
            data = '\n'.join(['host: ' + query['host'][0], 'date: ' + query['date'][0], 'GET ' + parsed.path + ' HTTP/1.1'])
            signature = base64.b64encode(hmac.digest(CREDENTIALS['secret'].encode(), data.encode(), 'sha256')).decode()
            authorized = fields['username'] == CREDENTIALS['key'] and hmac.compare_digest(fields['signature'], signature)
        except (KeyError, ValueError, UnicodeError):
            pass
        if not authorized:
            self.send_response(401)
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        key = self.headers['Sec-WebSocket-Key']
        accept = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
        self.send_response(101)
        self.send_header('Upgrade', 'websocket')
        self.send_header('Connection', 'Upgrade')
        self.send_header('Sec-WebSocket-Accept', accept)
        self.end_headers()
        record = {'authorized': True, 'frames': [], 'started_at': time.time()}
        try:
            _, raw = frame_read(self.rfile)
            request = json.loads(raw)
            assert request['header']['app_id'] == CREDENTIALS['app']
            case = request['payload']['message']['text'][-1]['content']
            assert case in ['normal', 'delayed', 'large', 'missing_terminal']
            record['case'] = case
            for index, usage in enumerate([(7, 2), (9, 3)]):
                if index and case == 'missing_terminal':
                    break
                if index and case == 'delayed':
                    time.sleep(4)
                p, c = usage
                event = {'header': {'code': 0, 'sid': 'native_fixture'}, 'payload': {
                    'choices': {'status': index + 1, 'seq': index, 'text': [{'role': 'assistant', 'content': ['first', 'last'][index], 'index': 0}]},
                    'usage': {'text': {'prompt_tokens': p, 'completion_tokens': c, 'total_tokens': p+c}}}}
                data = json.dumps(event, separators=(',', ':')).encode()
                if index and case == 'large':
                    data += b' ' * ((200 << 20) + 1 - len(data))
                record['frames'].append({'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest(), 'usage': event['payload']['usage'], 'sent_at': time.time()})
                frame_write(self.wfile, data)
        except (OSError, EOFError) as exc:
            record['transport_error'] = type(exc).__name__
        finally:
            self.close_connection = True
            record['finished_at'] = time.time()
            with LOCK, (ROOT / 'requests.jsonl').open('a') as target:
                target.write(json.dumps(record) + '\n')


if __name__ == '__main__':
    server = http.server.ThreadingHTTPServer(('0.0.0.0', 443), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(ROOT / 'server.pem', ROOT / 'server-key.private.pem')
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
