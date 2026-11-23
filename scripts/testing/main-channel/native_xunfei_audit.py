"""Supplemental native-adapter comparison, distinct from the chained matrix."""
import argparse
import hashlib
import json
from pathlib import Path
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import matrix as m

parser = argparse.ArgumentParser()
parser.add_argument('--label', required=True)
parser.add_argument('--cases', default='normal,delayed,large,missing_terminal')
args = parser.parse_args()
deployment = json.loads((m.ROOT / 'manifests/deployment.json').read_text())
latest = subprocess.check_output(['git', '-c', 'http.version=HTTP/1.1', 'ls-remote', 'https://github.com/QuantumNous/new-api.git', 'refs/heads/main'], text=True).split()[0]
assert latest == deployment['main_revision'], 'Official main changed'
clients = [m.setup(name) for name in ['main', 'candidate']]
m.configure(clients)
candidate = clients[1]
original_policy = next(x['value'] for x in m.api(candidate, '/api/option/') if x['key'] == 'stream_protocol_validation_setting.enabled')
credentials = json.loads((m.ROOT / 'native-xunfei/credentials.private.json').read_text())
report = m.ROOT / 'reports' / ('native-xunfei-' + args.label + '-' + time.strftime('%Y%m%d-%H%M%S'))
report.mkdir()
records, failures, originals = [], [], {}


def snapshot(name, last=0):
    sql = "SELECT quota,(SELECT COALESCE(SUM(used_quota),0) FROM fixture_" + name + ".tokens WHERE user_id=users.id) FROM fixture_" + name + ".users WHERE username='probe_root';"
    raw = subprocess.check_output(['docker', 'exec', 'newapi-probe-mysql', 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -N -e "$1"', 'sh', sql], text=True)
    quota, token = map(int, raw.split())
    raw = subprocess.check_output(['docker', 'exec', 'newapi-probe-pgsql', 'psql', '-U', 'postgres', '-d', 'fixture_'+name+'_logs', '-At', '-F', '\t', '-c',
        'SELECT id,quota,prompt_tokens,completion_tokens,request_id FROM logs WHERE id>'+str(last)+' AND type=2 ORDER BY id;'], text=True)
    logs = [{'id': int(p[0]), 'quota': int(p[1]), 'prompt_tokens': int(p[2]), 'completion_tokens': int(p[3]), 'request_id': p[4]}
            for line in raw.splitlines() if len(p := line.split('\t')) == 5]
    return {'quota': quota, 'token_used': token, 'logs': logs}


try:
    context = ssl.create_default_context(cafile=str(m.ROOT / 'native-xunfei/ca.pem'))
    try:
        urllib.request.urlopen('https://spark-api.xf-yun.com/v1.1/chat', context=context, timeout=5)
        raise AssertionError('Native fixture accepted an unauthenticated request')
    except urllib.error.HTTPError as exc:
        assert exc.code == 401
    m.strict(candidate, False)
    for client in clients:
        options = {x['key']: x['value'] for x in m.api(client, '/api/option/')}
        originals[client['name']] = {key: options[key] for key in ['ModelRatio', 'CompletionRatio']}
        for key in originals[client['name']]:
            value = json.loads(options[key]); value['SparkDesk-v1.1'] = 1
            m.api(client, '/api/option/', {'key': key, 'value': json.dumps(value)}, 'PUT')
        existing = [x for x in m.api(client, '/api/channel/?page_size=100')['items'] if x['name'] == 'native-xunfei-fixture']
        channel = {'type': 18, 'name': 'native-xunfei-fixture', 'models': 'SparkDesk-v1.1', 'group': 'default', 'status': 1, 'weight': 1, 'auto_ban': 0,
                   'key': '|'.join(credentials[key] for key in ['app', 'secret', 'key'])}
        if existing:
            channel['id'] = existing[0]['id']
            channel.pop('status')
            m.api(client, '/api/channel/', channel, 'PUT')
        else:
            m.api(client, '/api/channel/', {'mode': 'single', 'channel': channel})
        native = next(x for x in m.api(client, '/api/channel/?page_size=100')['items'] if x['name'] == 'native-xunfei-fixture')
        m.api(client, '/api/channel/'+str(native['id'])+'/status', {'status': 1}, 'POST')
    # Channel creation/cache refresh is asynchronous in official main. Keep
    # setup lag outside the measured protocol requests.
    time.sleep(3)
    for case in args.cases.split(','):
        for client in clients:
            before = snapshot(client['name'])
            last_id = max((x['id'] for x in before['logs']), default=0)
            started = time.monotonic()
            status, headers, body = m.request(client['base'], '/v1/chat/completions', {'model': 'SparkDesk-v1.1', 'messages': [{'role': 'user', 'content': case}], 'stream': True}, client['key'], timeout=30)
            elapsed = time.monotonic() - started
            previous, stable = None, False
            for _ in range(15):
                time.sleep(1)
                after = snapshot(client['name'], last_id)
                if after == previous and len(after['logs']) == 1:
                    stable = True
                    break
                previous = after
            filename = case + '-' + client['name'] + '.bin'
            (report / filename).write_bytes(body)
            row = {'case': case, 'client': client['name'], 'status': status, 'elapsed_seconds': elapsed, 'body_file': filename,
                   'response_sha256': hashlib.sha256(body).hexdigest(), 'stable': stable, 'wallet_delta': before['quota']-after['quota'],
                   'token_delta': after['token_used']-before['token_used'], 'logs': after['logs']}
            records.append(row)
        left, right = records[-2:]
        for field in ['status', 'wallet_delta', 'token_delta']:
            if left[field] != right[field]:
                failures.append({'case': case, 'field': field, 'main': left[field], 'candidate': right[field]})
        for field in ['quota', 'prompt_tokens', 'completion_tokens']:
            if [x[field] for x in left['logs']] != [x[field] for x in right['logs']]:
                failures.append({'case': case, 'field': 'logs.'+field})
        if m.comparable_stream((report / left['body_file']).read_bytes()) != m.comparable_stream((report / right['body_file']).read_bytes()):
            failures.append({'case': case, 'field': 'ordered_wire'})
        for row in [left, right]:
            if not row['stable'] or row['wallet_delta'] != sum(x['quota'] for x in row['logs']):
                failures.append({'case': case, 'client': row['client'], 'field': 'accounting_stability_or_ledger'})
finally:
    m.strict(candidate, str(original_policy).lower() == 'true')
    for client in clients:
        for key, value in originals.get(client['name'], {}).items():
            m.api(client, '/api/option/', {'key': key, 'value': value}, 'PUT')
        for channel in m.api(client, '/api/channel/?page_size=100')['items']:
            if channel['name'] == 'native-xunfei-fixture':
                m.api(client, '/api/channel/'+str(channel['id'])+'/status', {'status': 2}, 'POST')
    result = {'status': 'failed' if failures else 'passed', 'deployment': deployment, 'records': records, 'failures': failures,
              'scope': 'direct native adapters on both binaries; this is supplemental, not candidate->main chain coverage',
              'fixture_sha256': hashlib.sha256((m.ROOT / 'native-xunfei/server.py').read_bytes()).hexdigest()}
    (report / 'result.json').write_text(json.dumps(result, indent=2))
print(json.dumps({'report': str(report), 'status': result['status'], 'requests': len(records), 'failures': failures}))
