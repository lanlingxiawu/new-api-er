"""Wallet/ledger checks for the stream compatibility fixes; isolated fixtures only."""
import hashlib
import argparse
import json
from pathlib import Path
import subprocess
import time
import matrix as m

parser = argparse.ArgumentParser()
parser.add_argument('--extended', action='store_true', help='Include audio, partial-close and multiple-round debit boundaries')
parser.add_argument('--only-cases', help='Comma-separated fixture cases for a focused regression')
args = parser.parse_args()


def snapshot(name):
    sql = ("SELECT quota,(SELECT COALESCE(SUM(used_quota),0) FROM fixture_" + name
           + ".tokens WHERE user_id=users.id) FROM fixture_" + name + ".users WHERE username='probe_root';")
    quota = subprocess.check_output(['docker', 'exec', 'newapi-probe-mysql', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -N -e "$1"', 'sh', sql], text=True)
    log_id = subprocess.check_output(['docker', 'exec', 'newapi-probe-pgsql', 'psql', '-U', 'postgres',
        '-d', 'fixture_' + name + '_logs', '-Atc', 'SELECT COALESCE(max(id),0) FROM logs;'], text=True)
    wallet, token_used = map(int, quota.split())
    return {'quota': wallet, 'token_used': token_used, 'last_id': int(log_id.strip())}


def charges(name, last_id):
    sql = 'SELECT id,quota,request_id FROM logs WHERE id>' + str(last_id) + ' AND type=2 ORDER BY id;'
    raw = subprocess.check_output(['docker', 'exec', 'newapi-probe-pgsql', 'psql', '-U', 'postgres',
        '-d', 'fixture_' + name + '_logs', '-At', '-F', '\t', '-c', sql], text=True)
    return [{'id': int(parts[0]), 'quota': int(parts[1]), 'request_id': parts[2]}
            for line in raw.splitlines() if len(parts := line.split('\t')) == 3]


def candidate_costs(entries):
    ids = ','.join(str(log['id']) for log in entries) or '0'
    sql = 'SELECT COUNT(*),COALESCE(SUM(revenue_quota),0) FROM fixture_candidate.consumption_costs WHERE log_id IN (' + ids + ');'
    value = subprocess.check_output(['docker', 'exec', 'newapi-probe-mysql', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -N -e "$1"', 'sh', sql], text=True).split()
    return {'entries': int(value[0]), 'revenue_quota': int(value[1])}


deployment = json.loads((m.ROOT / 'manifests/deployment.json').read_text())
latest = subprocess.check_output(['git', 'ls-remote', 'https://github.com/QuantumNous/new-api.git', 'refs/heads/main'], text=True).split()[0]
if latest != deployment['main_revision']:
    raise SystemExit('Official main advanced; rebuild before testing')
main, candidate = m.setup('main'), m.setup('candidate')
m.configure([main, candidate])
pricing = {}
for client in [main, candidate]:
    values = {item['key']: item['value'] for item in m.api(client, '/api/option/')}
    pricing[client['name']] = {key: values.get(key, '{}') for key in ['billing_setting.billing_mode', 'billing_setting.billing_expr']}
options = m.api(candidate, '/api/option/')
original = next(item['value'] for item in options if item['key'] == 'stream_protocol_validation_setting.enabled')
report = m.ROOT / 'reports' / ('fix-wallet-' + time.strftime('%Y%m%d-%H%M%S'))
report.mkdir()
records, failures = [], []
try:
    for client in [main, candidate]:
        modes = json.loads(pricing[client['name']]['billing_setting.billing_mode'])
        expressions = json.loads(pricing[client['name']]['billing_setting.billing_expr'])
        for model in ['grok-probe', 'grok-probe-reference']:
            modes[model] = 'tiered_expr'
            expressions[model] = 'tier("stream-fix", p * 4 + cr * 0.2 + c * 20)'
        # The management API validates that an expression exists before mode activation.
        for key, value in [('billing_setting.billing_expr', expressions), ('billing_setting.billing_mode', modes)]:
            m.api(client, '/api/option/', {'key': key, 'value': json.dumps(value)}, 'PUT')
    m.strict(candidate, False)
    for spec_name, cases in [
        ('xai_sse', ['normal', 'cache_usage', 'reasoning_usage']),
        ('realtime_ws', ['normal', 'duplicate_terminal', 'zero_total_positive_details', 'invalid_json', 'provider_error', 'missing_usage']
         + (['zero_usage', 'missing_terminal', 'read_reset', 'realtime_audio', 'realtime_tail', 'realtime_error_after_usage', 'realtime_two_rounds', 'realtime_minimum_rounds', 'realtime_delayed_round', 'realtime_large_frame'] if args.extended else [])),
    ]:
        spec = next(spec for spec in m.SPECS if spec[0] == spec_name)
        for case in cases:
            if args.only_cases and case not in args.only_cases.split(','):
                continue
            m.set_case(main, spec[2], case)
            for client in [main, candidate]:
                before = {name: snapshot(name) for name in ['main', 'candidate']}
                status, headers, body = m.invoke(client, spec)
                filename = spec_name + '-' + case + '-' + client['name'] + '.bin'
                (report / filename).write_bytes(body)
                previous = None
                stable = False
                expected_entries = {'main': 2 if client['name'] == 'main' else 1,
                                    'candidate': 0 if client['name'] == 'main' else 1}
                deadline = time.monotonic() + 20
                while time.monotonic() < deadline:
                    time.sleep(1)
                    after = {name: snapshot(name) for name in before}
                    logs = {name: charges(name, before[name]['last_id']) for name in before}
                    observed = (after, logs)
                    if observed == previous and all(len(logs[name]) >= expected_entries[name] for name in before):
                        stable = True
                        break
                    previous = observed
                stores = {name: {'wallet_delta': before[name]['quota'] - after[name]['quota'],
                                  'token_delta': after[name]['token_used'] - before[name]['token_used'],
                                  'ledger_sum': sum(log['quota'] for log in logs[name]), 'entries': logs[name]}
                          for name in before}
                row = {'spec': spec_name, 'case': case, 'client': client['name'], 'status': status,
                       'body_file': filename, 'response_sha256': hashlib.sha256(body).hexdigest(),
                       'wire_usage': m.wire_usage(body), 'stores': stores, 'stable': stable,
                       'expected_entries': expected_entries}
                if not stable:
                    failures.append({'spec': spec_name, 'case': case, 'client': client['name'], 'error': 'accounting did not stabilize'})
                for name, values in stores.items():
                    if len(logs[name]) != expected_entries[name]:
                        failures.append({'spec': spec_name, 'case': case, 'client': client['name'], 'store': name,
                                         'error': 'unexpected consumption ledger count', 'expected': expected_entries[name],
                                         'actual': len(logs[name])})
                    if values['wallet_delta'] != values['ledger_sum']:
                        failures.append({'spec': spec_name, 'case': case, 'client': client['name'], 'store': name,
                                         'error': 'wallet delta differs from settled ledger sum', **values})
                if spec_name == 'xai_sse':
                    # (101-23)*4 + 23*0.2 + 17*20 USD/Mtokens, with 500000 quota/USD.
                    expected = 328
                    row['expected_charge_per_hop'] = expected
                    if any(log['quota'] != expected for entries in logs.values() for log in entries):
                        failures.append({'spec': spec_name, 'case': case, 'client': client['name'],
                                         'error': 'xAI cache-priced charge differs from known fixture', 'expected': expected})
                if client['name'] == 'candidate':
                    expected_costs = {'entries': sum(log['quota'] != 0 for log in logs['candidate']),
                                      'revenue_quota': stores['candidate']['ledger_sum']}
                    # Wait for async cost writes and exercise positive controls too.
                    # A permanently empty cost table must not make the zero case pass.
                    time.sleep(3)
                    deadline = time.monotonic() + 20
                    row['cost_ledger'] = candidate_costs(logs['candidate'])
                    while row['cost_ledger'] != expected_costs and time.monotonic() < deadline:
                        time.sleep(1)
                        row['cost_ledger'] = candidate_costs(logs['candidate'])
                    if row['cost_ledger'] != expected_costs:
                        failures.append({'spec': spec_name, 'case': case, 'error': 'cost revenue differs from settled charge',
                                         'expected': expected_costs, 'actual': row['cost_ledger']})
                if client['name'] == 'candidate' and case == 'zero_total_positive_details':
                    if len(logs['candidate']) != 1 or stores['candidate']['ledger_sum'] != 0:
                        failures.append({'spec': spec_name, 'case': case, 'error': 'main zero-total charge was not preserved'})
                    if row['cost_ledger']['entries'] or row['cost_ledger']['revenue_quota']:
                        failures.append({'spec': spec_name, 'case': case, 'error': 'zero charge created a cost/revenue entry', **row['cost_ledger']})
                records.append(row)
                print(json.dumps({'spec': spec_name, 'case': case, 'client': client['name'], 'stable': stable,
                                  'wallet_matches': all(v['wallet_delta'] == v['ledger_sum'] for v in stores.values())}), flush=True)
finally:
    m.strict(candidate, original == 'true')
    for profile in ['xai', 'openai']:
        m.set_case(main, profile, 'normal')
    for client in [main, candidate]:
        for key, value in pricing[client['name']].items():
            m.api(client, '/api/option/', {'key': key, 'value': value}, 'PUT')
compatibility = []
if not records:
    raise SystemExit('No matching wallet fixture cases')
for row in records:
    if row['client'] != 'candidate':
        continue
    reference = next(peer for peer in records if peer['client'] == 'main'
                     and (peer['spec'], peer['case']) == (row['spec'], row['case']))
    pair = {key: row[key] for key in ['spec', 'case']}
    pair['same_status_usage'] = (row['status'], row['wire_usage']) == (reference['status'], reference['wire_usage'])
    pair['same_ordered_wire'] = m.comparable_stream((report / row['body_file']).read_bytes()) == m.comparable_stream((report / reference['body_file']).read_bytes())
    for key in ['wallet_delta', 'token_delta', 'ledger_sum']:
        # The reference has two main hops in one store; the candidate chain has one per store.
        expected = reference['stores']['main'][key]
        pair['same_' + key] = all(row['stores'][name][key] * 2 == expected for name in ['main', 'candidate'])
        pair['main_per_hop_' + key] = expected / 2
        pair['candidate_' + key] = row['stores']['candidate'][key]
    pair['passed'] = all(value for key, value in pair.items() if key.startswith('same_'))
    compatibility.append(pair)
result = {'status': 'failed' if failures else 'passed', 'requests': len(records),
          'compatibility_status': 'passed' if all(pair['passed'] for pair in compatibility) else 'failed',
          'compatibility': compatibility,
          'note': 'Wallet-versus-log failures are retained separately from actual main compatibility; matching a main defect does not make the ledger internally consistent.',
          'records': records, 'failures': failures, 'deployment': deployment,
          'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
(report / 'result.json').write_text(json.dumps(result, indent=2))
print(json.dumps({'status': result['status'], 'compatibility_status': result['compatibility_status'],
                  'requests': len(records), 'failures': len(failures), 'report': str(report)}), flush=True)
raise SystemExit(bool(failures))
