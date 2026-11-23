"""Deploy the isolated fixture stack in one directory, with MySQL/PG logs.

Run on the explicitly authorized dedicated test server. Never reads credentials
from another application. Credentials are generated in config/*.private.json.
"""
import gzip
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.request

ROOT = Path('/opt/new-api-main-test')
NETWORK = 'newapi-main-fixtures'
LABEL = 'newapi.main-fixtures=owned'
RESUME = '--resume' in sys.argv


def command(args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT).strip()


def private(path, value):
    path.write_text(value, encoding='utf-8')
    path.chmod(0o600)


def owned_run(name, args):
    found = subprocess.run(['docker', 'inspect', name], capture_output=True, text=True)
    if found.returncode == 0:
        item = json.loads(found.stdout)[0]
        if item['Config'].get('Labels', {}).get('newapi.main-fixtures') != 'owned':
            raise RuntimeError('Container ownership conflict: ' + name)
        if not RESUME:
            raise RuntimeError('Existing owned container requires --resume: ' + name)
        if not item['State']['Running']:
            command(['docker','start',name])
        return item['Id']
    return command(['docker', 'run', '-d', '--name', name, '--label', LABEL,
                    '--network', NETWORK, '--restart', 'unless-stopped',
                    '--ulimit', 'nofile=65536:65536', '--log-opt', 'max-size=20m',
                    '--log-opt', 'max-file=3', *args])


manifest = json.loads((ROOT / 'manifests/20261112/build-manifest.json').read_text())
candidate_manifest = json.loads((ROOT / 'manifests/20261112/candidate-manifest.json').read_text())
latest = command(['git', 'ls-remote', manifest['official_remote'], 'refs/heads/main']).split()[0]
if latest != manifest['revision']:
    raise SystemExit('Official main advanced: rebuild before deploying')
for directory in ['bin', 'config', 'scripts', 'fixtures', 'data/main', 'data/candidate',
                  'data/mock', 'data/mysql', 'data/pgsql', 'reports', 'logs']:
    (ROOT / directory).mkdir(parents=True, exist_ok=True)
(ROOT / 'config').chmod(0o700)
if not (ROOT / 'manifests/runtime-loaded').exists():
    with gzip.open(ROOT / 'bin/runtime-images.tar.gz', 'rb') as archive:
        subprocess.run(['docker', 'load'], stdin=archive,
                       stdout=(ROOT / 'logs/runtime-load.log').open('w'),
                       stderr=subprocess.STDOUT, check=True)
    (ROOT / 'manifests/runtime-loaded').touch()
for name, expected in [('main', manifest['sha256']), ('candidate', candidate_manifest['sha256'])]:
    target = ROOT / 'bin' / ('new-api-' + name)
    with gzip.open(str(target) + '.gz', 'rb') as source, target.open('wb') as dest:
        while part := source.read(1024 * 1024):
            dest.write(part)
    target.chmod(0o755)
    if hashlib.file_digest(target.open('rb'), 'sha256').hexdigest() != expected:
        raise SystemExit('Binary checksum mismatch: ' + name)
state_path = ROOT / 'config/stack.private.json'
if state_path.exists():
    if not RESUME: raise SystemExit('A fixture state exists; --resume is required')
    state=json.loads(state_path.read_text())
else:
    state = {key: secrets.token_urlsafe(32) for key in ['mock_key', 'mysql_root', 'mysql_password', 'pg_root', 'pg_password']}
    for name in ['main', 'candidate']:
        state[name + '_password'] = secrets.token_urlsafe(28) + 'A1!'
        state[name + '_session'] = secrets.token_hex(32)
        state[name + '_crypto'] = secrets.token_hex(32)
    private(state_path, json.dumps(state))
if NETWORK not in command(['docker', 'network', 'ls', '--format', '{{.Name}}']).splitlines():
    command(['docker', 'network', 'create', '--internal', NETWORK])
if not json.loads(command(['docker', 'network', 'inspect', NETWORK]))[0]['Internal']:
    raise SystemExit('Fixture network must be internal')
sysctls = {'net.core.somaxconn': '8192', 'net.ipv4.tcp_max_syn_backlog': '8192',
           'net.ipv4.ip_local_port_range': '10240 65535', 'fs.file-max': '1048576'}
before = {key: command(['sysctl', '-n', key]) for key in sysctls}
private(ROOT / 'config/sysctl.conf', ''.join(key + '=' + value + '\n' for key, value in sysctls.items()))
command(['sysctl', '-p', str(ROOT / 'config/sysctl.conf')])
(ROOT / 'manifests/20261112/performance.json').write_text(json.dumps({'cpus': os.cpu_count(), 'sysctl_before': before, 'sysctl_after': sysctls, 'cpu_quota': 'unrestricted', 'mysql_buffer_pool_mb': 256, 'pg_shared_buffers_mb': 128, 'gateway_pool_per_database': 12, 'durable_database_writes': True}, indent=2))
private(ROOT / 'config/mysql.env', 'MYSQL_ROOT_PASSWORD=' + state['mysql_root'] + '\n')
private(ROOT / 'config/pgsql.env', 'POSTGRES_PASSWORD=' + state['pg_root'] + '\n')
owned_run('newapi-probe-mysql', ['--network-alias', 'mysql', '--env-file', str(ROOT / 'config/mysql.env'), '-v', str(ROOT / 'data/mysql') + ':/var/lib/mysql', 'mysql:8.0', '--innodb-buffer-pool-size=256M', '--max-connections=64', '--performance-schema=OFF'])
owned_run('newapi-probe-pgsql', ['--network-alias', 'pgsql', '--env-file', str(ROOT / 'config/pgsql.env'), '-v', str(ROOT / 'data/pgsql') + ':/var/lib/postgresql/data', 'postgres:17', '-c', 'shared_buffers=128MB', '-c', 'effective_cache_size=512MB', '-c', 'work_mem=4MB', '-c', 'max_connections=64'])
deadline = time.monotonic() + 120
while time.monotonic() < deadline:
    mysql = subprocess.run(['docker', 'exec', 'newapi-probe-mysql', 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -e "SELECT 1"'], capture_output=True)
    pg = subprocess.run(['docker', 'exec', 'newapi-probe-pgsql', 'pg_isready', '-U', 'postgres'], capture_output=True)
    if mysql.returncode == 0 and pg.returncode == 0:
        break
    time.sleep(1)
else:
    raise SystemExit('Isolated databases did not become ready')
mysql_sql = "CREATE USER IF NOT EXISTS 'fixture'@'%' IDENTIFIED BY '" + state['mysql_password'] + "';"
for name in ['main', 'candidate']:
    mysql_sql += 'CREATE DATABASE IF NOT EXISTS fixture_' + name + " CHARACTER SET utf8mb4; GRANT ALL PRIVILEGES ON fixture_" + name + ".* TO 'fixture'@'%';"
created = subprocess.run(['docker', 'exec', '-i', 'newapi-probe-mysql', 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot'], input=mysql_sql, text=True, capture_output=True)
if created.returncode:
    raise SystemExit('Fixture MySQL provisioning failed; inspect private database state')
pg_sql = "DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='fixture') THEN CREATE USER fixture WITH PASSWORD '" + state['pg_password'] + "'; END IF; END $$;\n"
for name in ['main', 'candidate']:
    pg_sql += "SELECT 'CREATE DATABASE fixture_" + name + "_logs OWNER fixture' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='fixture_" + name + "_logs')\\gexec\n"
created = subprocess.run(['docker', 'exec', '-i', 'newapi-probe-pgsql', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres'], input=pg_sql, text=True, capture_output=True)
if created.returncode:
    raise SystemExit('Fixture PostgreSQL provisioning failed; inspect private database state')
private(ROOT / 'config/mock.env', 'MOCK_API_KEY=' + state['mock_key'] + '\nMOCK_DATA=/data\nPYTHONUNBUFFERED=1\n')
owned_run('newapi-probe-mock', ['--network-alias', 'mock', '--env-file', str(ROOT / 'config/mock.env'), '-v', str(ROOT / 'data/mock') + ':/data', '-v', str(ROOT / 'fixtures/mock_server.py') + ':/opt/mock_server.py:ro', 'python:3.13-slim', 'python3', '/opt/mock_server.py'])
for name in ['main', 'candidate']:
    env = {'SQL_DSN': 'fixture:' + state['mysql_password'] + '@tcp(mysql:3306)/fixture_' + name + '?charset=utf8mb4&parseTime=True&loc=Local', 'LOG_SQL_DSN': 'postgres://fixture:' + state['pg_password'] + '@pgsql:5432/fixture_' + name + '_logs?sslmode=disable', 'SQL_MAX_OPEN_CONNS': '12', 'SQL_MAX_IDLE_CONNS': '6', 'REDIS_CONN_STRING': '', 'SESSION_SECRET': state[name + '_session'], 'CRYPTO_SECRET': state[name + '_crypto'], 'GLOBAL_API_RATE_LIMIT_ENABLE': 'false', 'GLOBAL_API_USER_RATE_LIMIT_ENABLE': 'false', 'GLOBAL_WEB_RATE_LIMIT_ENABLE': 'false', 'UPDATE_TASK': 'true', 'MEMORY_CACHE_ENABLED': 'true', 'BATCH_UPDATE_ENABLED': 'true', 'BATCH_UPDATE_INTERVAL': '1', 'ERROR_LOG_ENABLED': 'true', 'LOG_LANGUAGE': 'en', 'STREAMING_TIMEOUT': '3', 'GOMEMLIMIT': '320MiB', 'GIN_MODE': 'release'}
    private(ROOT / ('config/' + name + '.env'), ''.join(key + '=' + value + '\n' for key, value in env.items()))
    owned_run('newapi-probe-' + name, ['--network-alias', name, '--env-file', str(ROOT / ('config/' + name + '.env')), '-v', str(ROOT / ('data/' + name)) + ':/data', '-w', '/data', '-v', str(ROOT / ('bin/new-api-' + name)) + ':/opt/new-api:ro', '--entrypoint', '/opt/new-api', 'newapi-main-test-runtime:20261110', '--port', '3000'])
health = {}
for name, port, path in [('mock', 8000, '/health'), ('main', 3000, '/api/status'), ('candidate', 3000, '/api/status')]:
    ip = json.loads(command(['docker', 'inspect', 'newapi-probe-' + name]))[0]['NetworkSettings']['Networks'][NETWORK]['IPAddress']
    for _ in range(120):
        try:
            result = json.load(urllib.request.urlopen('http://' + ip + ':' + str(port) + path, timeout=2))
            health[name] = {'ok': True, 'private_address': ip, 'version': result.get('data', {}).get('version')}
            break
        except Exception:
            time.sleep(.5)
    if name not in health:
        raise SystemExit('Health check failed: ' + name)
latest = command(['git', 'ls-remote', manifest['official_remote'], 'refs/heads/main']).split()[0]
if latest != manifest['revision']:
    raise SystemExit('Official main advanced during deployment')
result = {'status': 'ready', 'main_revision': manifest['revision'], 'main_sha256': manifest['sha256'], 'candidate_revision': candidate_manifest['revision'], 'candidate_sha256': candidate_manifest['sha256'], 'candidate_source_clean': True, 'main_database': 'MySQL 8.0', 'log_database': 'PostgreSQL 17', 'health': health, 'network': NETWORK, 'private_network_only': True, 'external_provider_access': False}
(ROOT / 'manifests/deployment.json').write_text(json.dumps(result, indent=2))
print(json.dumps(result))
