"""Repeat equal-deadline timeout fixtures without changing matrix expectations.

Run after matrix/policy tests; temporarily changes only isolated fixture policy
and mock case. Captures each wire response so scheduling differences remain
reviewable rather than being normalized out of the primary parity assertion.
"""
import collections
import hashlib
import json
import time
import matrix as m

main = m.setup('main')
candidate = m.setup('candidate')
main['channels'] = {item['name'].removeprefix('main-fixture-'): item['id']
                    for item in m.api(main, '/api/channel/?page_size=100')['items']}
options = m.api(candidate, '/api/option/')
original = next(item['value'] for item in options
                if item['key'] == 'stream_protocol_validation_setting.enabled')
if original not in ['true', 'false']:
    raise SystemExit('Refuse to migrate an unconfigured fixture policy')
deployment = json.loads((m.ROOT / 'manifests/deployment.json').read_text())
latest = m.subprocess.check_output(
    ['git', 'ls-remote', 'https://github.com/QuantumNous/new-api.git', 'refs/heads/main'],
    text=True).split()[0]
if latest != deployment['main_revision']:
    raise SystemExit('Official main advanced; refresh before testing')
report = m.ROOT / 'reports' / ('timeout-boundary-' + time.strftime('%Y%m%d-%H%M%S'))
report.mkdir()
m.STARTED_AT = int(time.time()) - 2
records = []
try:
    m.strict(candidate, False)
    for name in ['chat_sse', 'completion_sse']:
        spec = next(spec for spec in m.SPECS if spec[0] == name)
        for case in ['idle_timeout', 'heartbeat_timeout']:
            m.set_case(main, 'openai', case)
            for repeat in range(3):
                for client in [main, candidate]:
                    start = time.monotonic()
                    started_at = time.time()
                    status, headers, body = m.invoke(client, spec)
                    filename = f'{name}-{case}-{repeat}-{client["name"]}.bin'
                    (report / filename).write_bytes(body)
                    records.append({'spec': name, 'case': case, 'repeat': repeat,
                                    'layer': client['name'], 'mode': 'sse', 'status': status,
                                    'request_id': next((value for key, value in headers.items()
                                                        if key.lower() == 'x-oneapi-request-id'), None),
                                    'started_at': started_at, 'finished_at': time.time(),
                                    'duration': time.monotonic() - start,
                                    'usage': m.wire_usage(body), 'body_file': filename,
                                    'sha256': hashlib.sha256(body).hexdigest()})
finally:
    m.strict(candidate, original == 'true')
    m.set_case(main, 'openai', 'normal')
groups = []
for layer in ['main', 'candidate']:
    for spec in ['chat_sse', 'completion_sse']:
        for case in ['idle_timeout', 'heartbeat_timeout']:
            counts = collections.Counter(json.dumps(row['usage'], sort_keys=True)
                                         for row in records if (row['layer'], row['spec'], row['case'])
                                         == (layer, spec, case))
            groups.append({'layer': layer, 'spec': spec, 'case': case,
                           'usage_variants': [{'usage': json.loads(key), 'count': value}
                                              for key, value in counts.items()]})
result = {'deployment': deployment, 'requests': len(records), 'groups': groups, 'records': records,
          'script_sha256': hashlib.sha256(m.Path(__file__).read_bytes()).hexdigest(),
          'note': 'Observational repeat only; primary matrix failures are unchanged.'}
deadline = time.monotonic() + 30
while True:
    for client in [main, candidate]:
        m.refresh_logs(client)
        for row in records:
            if row['layer'] == client['name']:
                row['logs'] = m.logs(client, row['request_id']) if row['request_id'] else []
    missing_rows = [row for row in records if row['status'] == 200
                    and row['usage'] and not row['logs']]
    if not missing_rows or time.monotonic() >= deadline:
        break
    time.sleep(0.5)
paired = []
for row in records:
    if row['layer'] != 'candidate':
        continue
    peer = next(peer for peer in records if peer['layer'] == 'main'
                and (peer['spec'], peer['case'], peer['repeat']) == (row['spec'], row['case'], row['repeat']))
    fields = ['type', 'prompt_tokens', 'completion_tokens', 'quota']
    main_accounting = sorted(tuple(log.get(key, 0) for key in fields) for log in peer['logs'] if log['type'] in [2, 5])
    candidate_accounting = sorted(tuple(log.get(key, 0) for key in fields) for log in row['logs'] if log['type'] in [2, 5])
    paired.append({'spec': row['spec'], 'case': row['case'], 'repeat': row['repeat'],
                   'same_status_usage': (row['status'], row['usage']) == (peer['status'], peer['usage']),
                   'same_ordered_wire': m.comparable_stream((report / row['body_file']).read_bytes()) == m.comparable_stream((report / peer['body_file']).read_bytes()),
                   'same_persisted_accounting': main_accounting == candidate_accounting,
                   'main_accounting': main_accounting, 'candidate_accounting': candidate_accounting})
result['paired_comparison'] = paired
result['failures'] = [pair for pair in paired if not all(pair[key] for key in
                       ['same_status_usage', 'same_ordered_wire', 'same_persisted_accounting'])]
result['status'] = 'failed' if result['failures'] else 'passed'
wire_matches = []
for row in records:
    if row['layer'] != 'candidate':
        continue
    actual = m.comparable_stream((report / row['body_file']).read_bytes())
    peers = [peer for peer in records if peer['layer'] == 'main'
             and (peer['spec'], peer['case']) == (row['spec'], row['case'])]
    wire_matches.append({'spec': row['spec'], 'case': row['case'], 'repeat': row['repeat'],
                         'observed_in_main': any(actual == m.comparable_stream(
                             (report / peer['body_file']).read_bytes()) for peer in peers)})
result['repeated_main_wire_matches'] = wire_matches
result['repeat_match_note'] = 'Matching any repeated main response describes variability only; it does not replace the same-repeat comparisons or waive differences.'
(report / 'result.json').write_text(json.dumps(result, indent=2))
print(json.dumps({'report': str(report), 'status': result['status'], 'failed_pairs': len(result['failures']), 'requests': len(records), 'groups': groups}))
raise SystemExit(bool(result['failures']))
