"""Assert resident fixture contracts against actual provider adapter inputs.

Run on HK with sudo python3; only reads private credentials and sends synthetic
requests to the loopback mock. It never changes gateway options or channels.
Failed assertions are retained; a case name or HTTP 200 is not a pass.
"""
import argparse
import hashlib
import json
from pathlib import Path
import time
import urllib.error
import urllib.request


def run(root):
    mock = root / 'mock'
    state = json.loads((mock / 'config/access.private.json').read_text())
    report = mock / 'reports' / time.strftime('semantic-audit-%Y%m%dT%H%M%SZ', time.gmtime())
    (report / 'responses').mkdir(parents=True)
    rows = []

    def check(name, path, body, predicate, expected, case='normal', method=None, headers=None):
        request_headers = {'Content-Type': 'application/json', 'X-Mock-Key': state['mock_key'], 'X-Mock-Case': case}
        request_headers.update(headers or {})
        row = {'name': name, 'path': path, 'case': case, 'expected': expected, 'started_at': time.time()}
        try:
            request = urllib.request.Request('http://127.0.0.1:18301' + path,
                json.dumps(body).encode() if body is not None else None, request_headers, method=method)
            try:
                response = urllib.request.urlopen(request, timeout=12)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                status = response.status
                response_headers = dict(response.headers)
            try:
                value = json.loads(raw)
            except (ValueError, UnicodeError):
                value = None
            row.update(status=status, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest(),
                       passed=bool(predicate(status, value, raw, response_headers)))
            (report / 'responses' / (name + '.bin')).write_bytes(raw)
        except Exception as error:
            row.update(passed=False, error=type(error).__name__)
        row['finished_at'] = time.time()
        rows.append(row)

    providers = [
        ('ollama', '/api/chat', ('prompt_eval_count',), ('eval_count',)),
        ('baidu', '/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions', ('usage', 'prompt_tokens'), ('usage', 'completion_tokens')),
        ('zhipu', '/api/paas/v3/model-api/chatglm_turbo/invoke', ('data', 'usage', 'prompt_tokens'), ('data', 'usage', 'completion_tokens')),
        ('cohere', '/v1/chat', ('meta', 'billed_units', 'input_tokens'), ('meta', 'billed_units', 'output_tokens')),
        ('dify', '/v1/chat-messages', ('metadata', 'usage', 'prompt_tokens'), ('metadata', 'usage', 'completion_tokens')),
        ('tencent', '/', ('Response', 'Usage', 'PromptTokens'), ('Response', 'Usage', 'CompletionTokens')),
        ('coze', '/v3/chat', ('data', 'usage', 'input_count'), ('data', 'usage', 'output_count')),
    ]
    for provider, route, prompt_path, completion_path in providers:
        path = '/provider/' + provider + '/mock/normal' + route
        body = {'model': 'mock', 'messages': [{'role': 'user', 'content': 'fixture'}], 'stream': False}
        for case in ['normal', 'missing_usage', 'zero_usage', 'usage_array']:
            def contract(status, value, raw, headers):
                if status != 200 or not isinstance(value, dict):
                    return False
                observed = []
                for field_path in [prompt_path, completion_path]:
                    field = value
                    for key in field_path:
                        field = field.get(key) if isinstance(field, dict) else None
                    observed.append(field)
                if case == 'normal':
                    return observed == [101, 17]
                if case == 'zero_usage':
                    return observed == [0, 0]
                if case == 'missing_usage':
                    return observed == [None, None]
                # Invalid usage must not silently become valid normal numbers.
                return observed != [101, 17]
            check(provider + '-' + case, path, body, contract,
                  'native usage reflects the selected case; absent differs from zero', case)

    for route, field, expected in [('/v1/responses', 'object', 'response'),
                                   ('/v1/responses/compact', 'object', 'response.compaction'),
                                   ('/v1/messages', 'type', 'message')]:
        check('ollama-' + expected, '/provider/ollama/mock/normal' + route,
              {'model': 'llama3.1', 'input': 'fixture', 'messages': [{'role': 'user', 'content': 'fixture'}], 'max_tokens': 100},
              lambda s, v, r, h: s == 200 and isinstance(v, dict) and v.get(field) == expected,
              'Ollama compatible endpoint must return its endpoint protocol, not native chat')
    check('cohere-rerank', '/provider/cohere/mock/normal/v1/rerank',
          {'model': 'rerank-v3.5', 'query': 'fixture', 'documents': ['fixture document']},
          lambda s, v, r, h: s == 200 and isinstance(v, dict) and bool(v.get('results')) and v['results'][0]['index'] == 0,
          'Cohere rerank returns ranked documents')
    check('doubao-image', '/provider/doubao/mock/normal/api/v3/images/generations',
          {'model': 'doubao-seedream-4-0-250828', 'prompt': 'fixture', 'size': '2048x2048'},
          lambda s, v, r, h: s == 200 and isinstance(v, dict) and bool(v.get('data')) and bool(v['data'][0].get('url') or v['data'][0].get('b64_json')),
          'Seedream image response includes image data, not a video task')
    check('minimax-provider-error', '/provider/minimax/mock/normal/v1/image_generation',
          {'model': 'image-01', 'prompt': 'fixture'},
          lambda s, v, r, h: s == 200 and isinstance(v, dict) and v.get('base_resp', {}).get('status_code', 0) != 0,
          'MiniMax business rejection uses base_resp.status_code', 'provider_error')
    check('sd2-submit', '/provider/thirdpartysd2/mock/normal/v1/video/generate',
          {'model': 'seedance-2.0', 'prompt': 'fixture'},
          lambda s, v, r, h: s == 200 and isinstance(v, dict) and bool(v.get('task', {}).get('id')),
          'fork thirdpartysd2 driver requires task.id')
    for provider, route in [('kling', '/v1/videos/text2video/'), ('vidu', '/ent/v2/tasks/'),
                            ('doubao', '/api/v3/contents/generations/tasks/'), ('alibaba', '/api/v1/tasks/'),
                            ('xai', '/v1/videos/'), ('replicate', '/v1/predictions/')]:
        check(provider + '-unknown-task', '/provider/' + provider + '/mock/normal' + route + 'never_submitted_audit', None,
              lambda s, v, r, h: s in [404, 410] or (isinstance(v, dict) and bool(v.get('error'))),
              'query of an unknown task must not create a successful task')
    check('native-unknown-route', '/provider/cohere/mock/normal/not-an-api', {'input': 'fixture'},
          lambda s, v, r, h: s in [404, 405], 'unknown provider route is rejected explicitly')
    check('video-container', '/asset/video.mp4', None,
          lambda s, v, r, h: s == 200 and b'ftyp' in r and b'moov' in r and b'mdat' in r,
          'successful video artifact contains a playable MP4 structure')
    check('video-range', '/asset/video.mp4', None,
          lambda s, v, r, h: s == 206 and len(r) == 8 and h.get('Content-Range', '').startswith('bytes 0-7/'),
          'fixture supports the byte-range content path used by video consumers', headers={'Range': 'bytes=0-7'})
    # Structured tool arguments cannot be truncated by silently reusing valid ones.
    check('gemini-truncated-tool', '/mock/normal/v1beta/models/gemini-probe:generateContent',
          {'contents': [{'parts': [{'text': 'fixture'}]}]},
          lambda s, v, r, h: s in [400, 422] or not isinstance(v['candidates'][0]['content']['parts'][0]['functionCall']['args'], dict),
          'truncated-tool must be malformed or explicitly unsupported, not a valid normal call', 'truncated_tool')
    result = {'report': str(report), 'checks': len(rows), 'passed': sum(r['passed'] for r in rows),
              'failed': sum(not r['passed'] for r in rows), 'records': rows}
    (report / 'result.json').write_text(json.dumps(result, indent=2))
    print(json.dumps({k:v for k,v in result.items() if k != 'records'}))
    print(json.dumps({'failures': [r['name'] for r in rows if not r['passed']]}))
    return bool(result['failed'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path('/home/hk-admin/new-api-staging'))
    raise SystemExit(run(parser.parse_args().root))
