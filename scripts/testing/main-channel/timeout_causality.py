"""Trace actual main SSE delivery under controlled ordering of two deadlines.

Isolated, serial channel diagnostic. Never changes gateway code or waives the
uncontrolled boundary comparisons. Request credentials are never recorded.
"""
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading
import time
import urllib.request
import matrix as m


class TraceProxy(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_):
        pass

    def do_POST(self):
        if not hmac.compare_digest(self.headers.get('Authorization', ''),
                                   'Bearer ' + m.STATE['main_api_key']):
            self.send_error(401)
            return
        mode, path = self.path.removeprefix('/').split('/', 1)
        if mode not in ['inner_first', 'outer_first', 'race'] or path not in ['v1/chat/completions', 'v1/completions']:
            self.send_error(404)
            return
        started = time.monotonic()
        trace = {'mode': mode, 'events': [], 'finished': threading.Event()}
        self.server.traces.append(trace)
        request = urllib.request.Request('http://main:3000/' + path,
            data=self.rfile.read(int(self.headers['Content-Length'])),
            headers={'Content-Type': 'application/json', 'Authorization': self.headers['Authorization']})
        received, sent = bytearray(), bytearray()
        try:
            with urllib.request.urlopen(request, timeout=10) as upstream:
                trace['upstream_request_id'] = upstream.headers.get('X-Oneapi-Request-Id')
                trace['upstream_headers_at'] = time.monotonic() - started
                if mode == 'inner_first':
                    time.sleep(1)  # Inner deadline is already running; outer scanner has not started.
                self.send_response(upstream.status)
                self.send_header('Content-Type', upstream.headers.get('Content-Type', 'text/event-stream'))
                self.send_header('Connection', 'close')
                self.end_headers()
                self.wfile.flush()
                trace['downstream_headers_at'] = time.monotonic() - started
                delayed = False
                for line in upstream:
                    received.extend(line)
                    event = {'received_at': time.monotonic() - started,
                             'line_sha256': hashlib.sha256(line).hexdigest(), 'bytes': len(line)}
                    value = m.parsed(line[5:]) if line.startswith(b'data:') else None
                    if isinstance(value, dict) and 'usage' in value and value['usage']:
                        event['usage'] = value['usage']
                        if mode == 'outer_first' and not delayed:
                            time.sleep(1)  # Usage remains the real main output, delivered after the outer deadline.
                            delayed = True
                    trace['events'].append(event)
                    self.wfile.write(line)
                    self.wfile.flush()
                    sent.extend(line)
                    event['sent_at'] = time.monotonic() - started
        except (BrokenPipeError, ConnectionResetError):
            trace['downstream_closed'] = True
        except Exception as exc:
            trace['error'] = type(exc).__name__
        finally:
            trace['received_body'] = bytes(received)
            trace['sent_body'] = bytes(sent)
            trace['finished_at'] = time.monotonic() - started
            self.close_connection = True
            trace['finished'].set()


def run():
    deployment = json.loads((m.ROOT / 'manifests/deployment.json').read_text())
    latest = m.subprocess.check_output(['git', '-c', 'http.version=HTTP/1.1', 'ls-remote',
        'https://github.com/QuantumNous/new-api.git', 'refs/heads/main'], text=True).split()[0]
    if latest != deployment['main_revision']:
        raise SystemExit('Official main advanced; rebuild before testing')
    main, candidate = m.setup('main'), m.setup('candidate')
    m.configure([main, candidate])
    originals = []
    for client, name in [(main, 'main-fixture-openai-reference'), (candidate, 'main-fixture-openai')]:
        channels = m.api(client, '/api/channel/?page_size=100')['items']
        channel = m.api(client, '/api/channel/' + str(next(row['id'] for row in channels if row['name'] == name)))
        channel.pop('key', None)
        channel.pop('status', None)
        originals.append((client, channel))
    options = {row['key']: row['value'] for row in m.api(candidate, '/api/option/')}
    original_policy = options['stream_protocol_validation_setting.enabled']
    if original_policy not in ['true', 'false']:
        raise SystemExit('Missing original fixture policy')
    report = m.ROOT / 'reports' / ('timeout-causality-' + time.strftime('%Y%m%d-%H%M%S'))
    report.mkdir()
    m.STARTED_AT = int(time.time()) - 2
    server = ThreadingHTTPServer(('0.0.0.0', 8000), TraceProxy)
    server.traces = []
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    records, failures = [], []
    try:
        m.strict(candidate, False)
        m.set_case(main, 'openai', 'idle_timeout')
        for mode in ['inner_first', 'outer_first', 'race']:
            for client, original in originals:
                channel = dict(original, base_url='http://newapi-comparison-runner:8000/' + mode)
                m.api(client, '/api/channel/', channel, 'PUT')
            for spec_name in ['chat_sse', 'completion_sse']:
                spec = next(spec for spec in m.SPECS if spec[0] == spec_name)
                for repeat in range(6 if mode == 'race' else 3):
                    clients = [('main', main), ('candidate', candidate)]
                    if mode == 'race':
                        clients.insert(1, ('main_repeat', main))
                    for label, client in clients:
                        trace_count = len(server.traces)
                        start = time.monotonic()
                        status, headers, body = m.invoke(client, spec)
                        duration = time.monotonic() - start
                        if len(server.traces) != trace_count + 1:
                            raise RuntimeError('Expected exactly one real main upstream request')
                        trace = server.traces[-1]
                        if not trace['finished'].wait(12):
                            raise RuntimeError('Proxy did not finish')
                        prefix = f'{mode}-{spec_name}-{repeat}-{label}'
                        (report / (prefix + '.bin')).write_bytes(body)
                        saved_trace = {key: value for key, value in trace.items()
                                       if key not in ['received_body', 'sent_body', 'finished']}
                        for key in ['received_body', 'sent_body']:
                            payload = trace[key]
                            filename = prefix + '-' + key + '.bin'
                            (report / filename).write_bytes(payload)
                            saved_trace[key] = {'body_file': filename, 'sha256': hashlib.sha256(payload).hexdigest()}
                        row = {'spec': spec_name, 'condition': mode, 'repeat': repeat, 'layer': label,
                               'status': status, 'usage': m.wire_usage(body), 'duration': duration,
                               'request_id': next((v for k, v in headers.items() if k.lower() == 'x-oneapi-request-id'), None),
                               'body_file': prefix + '.bin', 'sha256': hashlib.sha256(body).hexdigest(), 'trace': saved_trace}
                        records.append(row)
                        (report / 'progress.json').write_text(json.dumps(records, indent=2))
                        print(json.dumps({'condition': mode, 'spec': spec_name, 'repeat': repeat,
                                          'layer': label, 'usage': row['usage'], 'duration': duration}), flush=True)
    finally:
        for client, original in originals:
            m.api(client, '/api/channel/', original, 'PUT')
        m.set_case(main, 'openai', 'normal')
        m.strict(candidate, original_policy == 'true')
        server.shutdown()
        server.server_close()
        thread.join(5)
    # All requests are serial; wait for both persistence queues, then require exact counts.
    for attempt in range(20):
        for client in [main, candidate]:
            m.refresh_logs(client)
        if all(len(m.logs(candidate if row['layer'] == 'candidate' else main, row['request_id'])) == 1 for row in records):
            break
        time.sleep(1)
    pairs = []
    for row in records:
        client = candidate if row['layer'] == 'candidate' else main
        row['logs'] = m.logs(client, row['request_id'])
        row['upstream_logs'] = m.logs(main, row['trace']['upstream_request_id'])
        row['accounting'] = sorted([log.get(key, 0) for key in ['type', 'prompt_tokens', 'completion_tokens', 'quota']]
                                   for log in row['logs'])
        if len(row['logs']) != 1 or len(row['upstream_logs']) != 1 or row['trace'].get('error'):
            failures.append({'error': 'missing ledger or proxy failure', 'body_file': row['body_file']})
        if row['condition'] != 'race':
            expected = [2, 101, 17, 118] if row['condition'] == 'inner_first' else [2, 9 if row['spec'] == 'chat_sse' else 4, 2, 11 if row['spec'] == 'chat_sse' else 6]
            if row['accounting'] != [expected]:
                failures.append({'error': 'controlled ordering charge differs', 'body_file': row['body_file'], 'expected': expected, 'actual': row['accounting']})
        if row['layer'] == 'main':
            continue
        peer = next(peer for peer in records if peer['layer'] == 'main' and all(peer[key] == row[key] for key in ['condition', 'spec', 'repeat']))
        pair = {key: row[key] for key in ['condition', 'spec', 'repeat', 'layer']}
        pair.update(same_status_usage=(peer['status'], peer['usage']) == (row['status'], row['usage']),
                    same_ordered_wire=m.comparable_stream((report / peer['body_file']).read_bytes()) == m.comparable_stream((report / row['body_file']).read_bytes()),
                    same_accounting=peer['accounting'] == row['accounting'], main_accounting=peer['accounting'], compared_accounting=row['accounting'])
        pairs.append(pair)
        if not all(pair[key] for key in ['same_status_usage', 'same_ordered_wire', 'same_accounting']):
            failures.append({'error': 'paired difference', **pair})
    result = {'status': 'failed' if failures else 'passed', 'deployment': deployment,
              'requests': len(records), 'records': records, 'pairs': pairs, 'failures': failures,
              'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'note': 'Controlled ordering supplements but never replaces the unchanged equal-deadline failures.'}
    (report / 'result.json').write_text(json.dumps(result, indent=2))
    print(json.dumps({'report': str(report), 'requests': len(records), 'failures': len(failures)}))
    raise SystemExit(bool(failures))


if __name__ == '__main__':
    run()
