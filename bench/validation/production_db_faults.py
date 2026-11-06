"""Opt-in MySQL/PostgreSQL/Redis gateway fault reproduction on owned fixtures.

Requires a fixture directory containing fixtures.json, gateway-endpoints.json,
and gateway-data/state.private.json. Refuses unlabelled Docker resources. Never
targets native services or production hosts. AI responses come from a fixture.
"""
import argparse
import concurrent.futures
import datetime
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request


class GatewayFaultFixture:
    def __init__(self, directory):
        self.directory = directory.resolve()
        self.resources = json.loads((directory / 'fixtures.json').read_text())
        self.label = self.resources['label']
        if not self.label.startswith('newapi-dbfault-'):
            raise ValueError('requires a dedicated newapi-dbfault fixture')
        for role in ['mysql', 'postgres', 'redis', 'gateway', 'nginx', 'mock']:
            name = self.resources[role]
            info = json.loads(self.docker('inspect', name))[0]
            if info['Config']['Labels'].get('newapi.acceptance') != self.label:
                raise ValueError('container ownership does not match fixture')
            if name != self.label + '-' + role:
                raise ValueError('unexpected fixture container name')
        self.state = json.loads((directory / 'gateway-data/state.private.json').read_text())
        endpoints = json.loads((directory / 'gateway-endpoints.json').read_text())
        self.base = 'http://127.0.0.1:' + str(endpoints['nginx_port'])

    def docker(self, *arguments, input=None, check=True):
        result = subprocess.run(['docker', *map(str, arguments)], input=input,
                                capture_output=True, text=True, encoding='utf-8',
                                errors='replace', timeout=60)
        if check and result.returncode:
            raise RuntimeError(result.stdout + result.stderr)
        return result.stdout.strip()

    def mysql(self, statement):
        return self.docker('exec', '-i', self.resources['mysql'], 'mysql',
                           '-uroot', '-N', '-B', 'acceptance_gateway', input=statement)

    def postgres(self, statement, database='acceptance_gateway'):
        # Admin recovery must remain writable while the application database's
        # default transactions are read-only. Application connections do not
        # receive this PGOPTIONS override.
        return self.docker('exec', '-e', 'PGOPTIONS=-c default_transaction_read_only=off',
                           self.resources['postgres'], 'psql', '-U', 'acceptance',
                           '-d', database, '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement)

    def snapshot(self):
        statement = ('SELECT quota,used_quota,request_count FROM users WHERE id=2;'
                     'SELECT remain_quota,used_quota FROM tokens WHERE id=1;'
                     'SELECT used_quota FROM channels WHERE id=1;'
                     'SELECT COUNT(*),COALESCE(SUM(revenue_quota),0),'
                     'COALESCE(SUM(cost_quota),0) FROM consumption_costs WHERE user_id=2;')
        numbers = list(map(int, self.mysql(statement).split()))
        statement = ('SELECT count(*),COALESCE(sum(quota),0),count(DISTINCT request_id) '
                     'FROM logs WHERE type=2 AND user_id=2;')
        numbers += list(map(int, self.postgres(statement).split('|')))
        keys = ['quota', 'used', 'requests', 'token_remain', 'token_used',
                'channel_used', 'cost_records', 'cost_revenue', 'cost_quota',
                'logs', 'log_quota', 'distinct_ids']
        return dict(zip(keys, numbers, strict=True))

    def send(self, stream):
        payload = {'model': 'shutdown-normal', 'stream': stream, 'max_tokens': 64,
                   'messages': [{'role': 'user', 'content': 'isolated database fault'}],
                   'stream_options': {'include_usage': True}}
        request = urllib.request.Request(self.base + '/v1/chat/completions',
                                         data=json.dumps(payload).encode(),
                                         headers={'Content-Type': 'application/json',
                                                  'Authorization': 'Bearer ' + self.state['api_token']})
        receipt = {'stream': stream, 'complete': False}
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                receipt['status'] = response.status
                receipt['request_id'] = response.headers.get('X-Oneapi-Request-Id', '')
                body = response.read()
                if stream:
                    frames = [json.loads(line[6:]) for line in body.splitlines()
                              if line.startswith(b'data: ') and line[6:] != b'[DONE]']
                    usages = [frame['usage'] for frame in frames if frame.get('usage')]
                    receipt['usage'] = usages[-1] if usages else {}
                    receipt['complete'] = b'data: [DONE]' in body and b'hello' in body
                else:
                    result = json.loads(body)
                    receipt['usage'] = result.get('usage', {})
                    receipt['complete'] = result.get('choices', [{}])[0].get('message', {}).get('content') == 'hello'
                receipt['complete'] &= receipt['usage'].get('total_tokens') == 150
        except (OSError, ValueError, urllib.error.HTTPError) as error:
            receipt.update(error_type=type(error).__name__, status=getattr(error, 'code', None))
        return receipt

    def requests(self, count):
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            return list(pool.map(self.send, [i % 2 == 1 for i in range(count)]))

    def reconciliation(self, before, after, receipts):
        successful = [r for r in receipts if r['complete']]
        count = len(successful)
        quota = count * 150
        delta = {key: after[key] - before[key] for key in before}
        checks = {key: delta[key] == quota for key in
                  ['used', 'token_used', 'channel_used', 'cost_revenue', 'cost_quota', 'log_quota']}
        checks.update({key: delta[key] == count for key in
                       ['requests', 'cost_records', 'logs', 'distinct_ids']})
        checks.update(quota=delta['quota'] == -quota,
                      token_remain=delta['token_remain'] == -quota,
                      requests_complete=count == len(receipts))
        return {'delta': delta, 'expected_requests': count, 'expected_quota': quota,
                'checks': checks, 'passed': all(checks.values())}

    def await_accounting(self, before, receipts, seconds=30):
        deadline = time.monotonic() + seconds
        while True:
            after = self.snapshot()
            comparison = self.reconciliation(before, after, receipts)
            if comparison['passed'] or time.monotonic() >= deadline:
                return after, comparison
            time.sleep(1)

    def log_receipts(self, receipts):
        ids = [r['request_id'] for r in receipts if r.get('request_id')]
        if not ids:
            return []
        if any(not value.isalnum() for value in ids):
            raise ValueError('unexpected request ID format')
        sql_ids = ','.join("'" + value + "'" for value in ids)
        rows = self.postgres('SELECT id,request_id,quota FROM logs WHERE type=2 AND request_id IN ('
                             + sql_ids + ') ORDER BY id;')
        records = []
        for line in rows.splitlines():
            log_id, request_id, quota = line.split('|')
            cost = self.mysql('SELECT COUNT(*),COALESCE(SUM(revenue_quota),0) '
                              'FROM consumption_costs WHERE user_id=2 AND log_id=' + str(int(log_id)) + ';')
            records.append({'log_id': int(log_id), 'request_id': request_id,
                            'quota': int(quota), 'cost_count_and_revenue': list(map(int, cost.split()))})
        return records

    def log_read_only(self, enabled):
        command = ('ALTER DATABASE acceptance_gateway SET default_transaction_read_only=on;'
                   if enabled else 'ALTER DATABASE acceptance_gateway RESET default_transaction_read_only;')
        self.postgres(command, database='postgres')
        self.postgres("SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                      "WHERE datname='acceptance_gateway' AND pid<>pg_backend_pid();", database='postgres')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture-dir', required=True, type=Path)
    parser.add_argument('--cases', nargs='+', choices=['mysql_write_rejected',
                        'mysql_connection_outage', 'postgres_log_write_rejected'],
                        default=['mysql_write_rejected', 'mysql_connection_outage',
                                 'postgres_log_write_rejected'])
    parser.add_argument('--report-label', default='')
    parser.add_argument('--recovery-wait-seconds', type=int, default=30)
    args = parser.parse_args()
    if not 1 <= args.recovery_wait_seconds <= 120:
        parser.error('recovery wait must be between 1 and 120 seconds')
    if args.report_label and not re.fullmatch(r'[a-zA-Z0-9_-]+', args.report_label):
        parser.error('report label must be alphanumeric')
    fixture = GatewayFaultFixture(args.fixture_dir)
    versions = {'mysql': fixture.mysql('SELECT VERSION();'),
                'postgres': fixture.postgres('SELECT version();'),
                'redis': fixture.docker('exec', fixture.resources['redis'], 'redis-server', '--version')}
    environment = json.loads((args.fixture_dir / 'gateway-environment.private.json').read_text())
    result = {'versions': versions,
              'candidate_sha256': json.loads((args.fixture_dir / 'gateway-endpoints.json').read_text())['candidate_sha256'],
              'recovery_wait_seconds': args.recovery_wait_seconds,
              'parameters': {name: environment.get(name) for name in
                             ['BATCH_UPDATE_ENABLED', 'BATCH_UPDATE_INTERVAL', 'STREAMING_TIMEOUT']},
              'scope': 'owned isolated real MySQL/PostgreSQL/Redis, Linux candidate, Nginx, simulated AI upstream',
              'baseline': {}, 'faults': []}
    suffix = '-' + args.report_label if args.report_label else ''
    report = args.fixture_dir / ('database-fault-results' + suffix + '.json')
    before = fixture.snapshot()
    receipts = fixture.requests(8)
    after, comparison = fixture.await_accounting(before, receipts)
    result['baseline'] = {'before': before, 'after': after, 'receipts': receipts, **comparison}
    report.write_text(json.dumps(result, indent=2), encoding='utf-8')
    if not comparison['passed']:
        raise RuntimeError('healthy baseline failed; fault injection refused')
    for fault in args.cases:
        before = fixture.snapshot()
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        print(json.dumps({'phase': fault, 'state': 'injecting'}), flush=True)
        try:
            if fault == 'mysql_write_rejected':
                fixture.mysql('SET GLOBAL super_read_only=ON;')
            elif fault == 'mysql_connection_outage':
                fixture.docker('pause', fixture.resources['mysql'])
            else:
                fixture.log_read_only(True)
            receipts = fixture.requests(8)
            # Two real production-configured five-second batch intervals elapse.
            time.sleep(12)
        finally:
            if fault == 'mysql_write_rejected':
                fixture.mysql('SET GLOBAL super_read_only=OFF;')
            elif fault == 'mysql_connection_outage':
                fixture.docker('unpause', fixture.resources['mysql'])
            else:
                fixture.log_read_only(False)
        restored = datetime.datetime.now(datetime.timezone.utc).isoformat()
        # Prove both actual engines accept writes after restoration.
        fixture.mysql('START TRANSACTION; UPDATE users SET quota=quota WHERE id=2; ROLLBACK;')
        fixture.postgres('BEGIN; UPDATE logs SET quota=quota WHERE id=(SELECT MAX(id) FROM logs); ROLLBACK;')
        after, comparison = fixture.await_accounting(before, receipts, args.recovery_wait_seconds)
        row = {'fault': fault, 'started': started, 'restored': restored,
               'before': before, 'after_recovery': after, 'receipts': receipts,
               'log_receipts': fixture.log_receipts(receipts), **comparison}
        # Fresh requests verify the healthy write path still works, independently
        # of whether old failed batches were recovered.
        control_before = fixture.snapshot()
        controls = fixture.requests(2)
        control_after, control_check = fixture.await_accounting(control_before, controls)
        row['healthy_control_after_recovery'] = {'receipts': controls, **control_check}
        logs = subprocess.run(['docker', 'logs', '--since', started, fixture.resources['gateway']],
                              capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=30)
        (args.fixture_dir / (fault + suffix + '.log')).write_text(logs.stdout + logs.stderr, encoding='utf-8')
        result['faults'].append(row)
        report.write_text(json.dumps(result, indent=2), encoding='utf-8')
        print(json.dumps({'phase': fault, 'passed': row['passed'], 'delta': row['delta'],
                          'healthy_control_passed': control_check['passed']}), flush=True)
    result['passed'] = all(row['passed'] and row['healthy_control_after_recovery']['passed']
                           for row in result['faults'])
    report.write_text(json.dumps(result, indent=2), encoding='utf-8')
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
