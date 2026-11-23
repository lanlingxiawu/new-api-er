"""Large populated-database upgrade rehearsal using owned disposable databases.

Run from any directory: python scripts/testing/database-upgrade/run.py
No project .env, credentials or existing Docker databases are used. Output stays
under _local. --baseline must identify the actual old release for release use.
"""
import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import time
import urllib.request
import zipfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
parser = argparse.ArgumentParser()
parser.add_argument('--baseline', default='HEAD')
parser.add_argument('--rows', type=int, default=100_000)
parser.add_argument('--log-rows', type=int, default=1_000_000)
parser.add_argument('--databases', nargs='+', choices=['sqlite', 'mysql', 'postgres'], default=['mysql', 'postgres'],
                    help='Defaults to MySQL and PostgreSQL; SQLite is opt-in only.')
parser.add_argument('--output', default='_local/database-upgrade-large-' + datetime.now().strftime('%Y%m%d-%H%M%S'))
parser.add_argument('--reuse-build', action='store_true')
args = parser.parse_args()
if args.rows < 100_000 or args.log_rows < 1_000_000:
    parser.error('large-data acceptance requires >= 100000 business rows and >= 1000000 log rows')
OUT = (ROOT / args.output).resolve()
if not OUT.is_relative_to((ROOT / '_local').resolve()):
    parser.error('output must stay under project _local')
OUT.mkdir(parents=True, exist_ok=True)
OLD, NEW = OUT / 'baseline', OUT / 'candidate'
env = {k: v for k, v in os.environ.items() if not any(k.startswith(p) for p in (
    'SQL_', 'LOG_SQL_', 'REDIS_', 'TEST_', 'AUDIT_', 'SESSION_', 'CRYPTO_',
    'PYROSCOPE_', 'CHANNEL_UPDATE_', 'SQLITE_', 'REQUEST_LOG_', 'NODE_'))}
env.update(GOWORK='off', LARGE_DATA_ISOLATED='yes', LARGE_DATA_ROWS=str(args.rows),
           LARGE_DATA_LOG_ROWS=str(args.log_rows), DEBUG='false', GIN_MODE='release',
           MEMORY_CACHE_ENABLED='true', PASSWORD_LOGIN_ENCRYPTION_ENABLED='false',
           SESSION_SECRET=secrets.token_hex(32), CRYPTO_SECRET=secrets.token_hex(32))
results = []


def run(command, cwd, label, extra=None, timeout=2400):
    start = time.monotonic()
    with (OUT / (label + '.log')).open('w', encoding='utf-8') as log:
        proc = subprocess.run(command, cwd=cwd, env=env | (extra or {}), stdout=log,
                              stderr=subprocess.STDOUT, timeout=timeout)
    result = {'step': label, 'exit': proc.returncode, 'seconds': round(time.monotonic()-start, 3)}
    results.append(result)
    print(json.dumps(result), flush=True)
    save()
    if proc.returncode:
        raise RuntimeError(f'{label} failed; see its log')


def save():
    (OUT / 'results.json').write_text(json.dumps(results, indent=2), encoding='utf-8')


def verify_counts(kind):
    def read(label):
        rows = {}
        for line in (OUT / (label + '.log')).read_text(encoding='utf-8').splitlines():
            if line.startswith('{'):
                value = json.loads(line)
                if 'table' in value:
                    rows[value['table']] = value['rows']
        return rows
    before = read(kind + '-seed-old')
    for name, count in read(kind + '-seed-new').items():
        before[name] = max(before.get(name, 0), count)
    after = read(kind + '-counts')
    lost = {name: {'before': count, 'after': after.get(name)} for name, count in before.items()
            if after.get(name, -1) < count}
    # These fixtures deliberately include expired artifacts. Real startup workers
    # execute their retention rules; retain that churn as evidence, not lost data.
    retention_tables = {'auth_flows', 'channel_daily_usages', 'channel_limit_period_usages',
                        'subscription_pre_consume_records'}
    retention = {k:v for k,v in lost.items() if k in retention_tables}
    lost = {k:v for k,v in lost.items() if k not in retention_tables}
    # Fixtures add a within-day offset and seeding/startup consumes wall time.
    # Rows from days 0..5 remain strictly inside seven-day retention; day 6 can
    # cross the boundary during this long test and must not be asserted present.
    subscription_rows = before.get('subscription_pre_consume_records', 0)
    recent_subscription_rows = sum(i % 30 < 6 for i in range(1, subscription_rows + 1))
    if after.get('subscription_pre_consume_records', 0) < recent_subscription_rows:
        raise RuntimeError('subscription retention removed recent fixture rows')
    results.append({'step':kind+'-data-preservation', 'tables':len(after), 'rows':sum(after.values()),
                    'unexpected_row_loss':lost, 'retention_deltas':retention})
    save()
    if lost:
        raise RuntimeError('row preservation failed; see results.json')


def docker(*parts, extra=None):
    p = subprocess.run(['docker', *parts], env=env | (extra or {}), capture_output=True,
                       text=True, timeout=120)
    if p.returncode:
        raise RuntimeError('owned Docker operation failed')
    return p.stdout.strip()


def prepare():
    revision = subprocess.check_output(['git', 'rev-parse', args.baseline], cwd=ROOT, text=True).strip()
    archive = OUT / 'baseline.zip'
    subprocess.run(['git', 'archive', '--format=zip', '-o', str(archive), revision], cwd=ROOT, check=True)
    OLD.mkdir(exist_ok=True)
    with zipfile.ZipFile(archive) as z:
        z.extractall(OLD)
    NEW.mkdir(exist_ok=True)
    manifest = {}
    files = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT).decode().split('\0')
    for name in sorted(set(files)):
        if not name or name.startswith(('.env', '_local/', '.git/', 'web/')):
            continue
        source = ROOT / name
        if not source.is_file():
            continue
        dest = NEW / name
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, dest)
        manifest[name] = hashlib.sha256(source.read_bytes()).hexdigest()
    # The test exercises backend startup. Frontend build output is irrelevant.
    (NEW / 'web/dist').mkdir(parents=True, exist_ok=True)
    (NEW / 'web/dist/index.html').write_text('<!doctype html><title>Isolated startup fixture</title>', encoding='utf-8')
    (OUT / 'source-manifest.json').write_text(json.dumps({'baseline': revision, 'candidate': manifest}, indent=2), encoding='utf-8')
    for snap, current in [(OLD, False), (NEW, True)]:
        folder = snap / 'cmd/largeupgradefixture'
        folder.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(HERE / 'fixture.go.txt', folder / 'main.go')
        hook = ('package main\nimport ("context"; "github.com/QuantumNous/new-api/model"; "gorm.io/gorm")\n'
                'func seedNewSchema(db *gorm.DB) { if err := model.MigrateAuditSecurity(context.Background(),db); err != nil { panic(err) }; if err:=db.AutoMigrate(&model.ChannelCreator{});err!=nil {panic(err)} }\n') if current else (
                    'package main\nimport "gorm.io/gorm"\nfunc seedNewSchema(db *gorm.DB) {}\n')
        (folder / 'schema.go').write_text(hook, encoding='utf-8')
        run(['go', 'build', '-o', str(OUT / ('fixture-new.exe' if current else 'fixture-old.exe')), './cmd/largeupgradefixture'], snap, 'build-fixture-' + ('new' if current else 'old'))
    # Observation only: no DDL / migration / business logic changes in snapshot.
    target = NEW / 'model/gorm_logger.go'
    source = target.read_text(encoding='utf-8')
    source = source.replace('newGormLogger(dynamicGinWriter{})', 'upgradeDDLLogger{newGormLogger(dynamicGinWriter{})}', 1)
    target.write_text(source, encoding='utf-8')
    shutil.copyfile(HERE / 'ddl_logger.go.txt', NEW / 'model/zz_upgrade_ddl_logger.go')
    run(['go', 'build', '-o', str(OUT / 'candidate.exe'), '.'], NEW, 'build-candidate')


def boot(kind, phase, db_env, work):
    label = kind + '-' + phase
    ddl = OUT / (label + '-ddl.jsonl')
    ddl.write_text('', encoding='utf-8')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    stop = work / (phase + '.stop')
    if stop.exists():
        stop.unlink()
    probe_log = (OUT / (label + '-probe.log')).open('w', encoding='utf-8')
    probe = subprocess.Popen([str(OUT / 'fixture-new.exe')], cwd=work,
                             env=env | db_env | {'LARGE_DATA_MODE':'probe','LARGE_DATA_STOP':str(stop)},
                             stdout=probe_log, stderr=subprocess.STDOUT)
    with (OUT / (label + '.log')).open('w', encoding='utf-8') as log:
        start = time.monotonic()
        proc = subprocess.Popen([str(OUT / 'candidate.exe'), '--port', str(port), '--log-dir', str(work / 'logs')], cwd=work,
                                env=env | db_env | {'LARGE_DATA_DDL':str(ddl)}, stdout=log, stderr=subprocess.STDOUT)
        ready = False
        try:
            while time.monotonic()-start < 600 and proc.poll() is None:
                try:
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/api/status', timeout=1) as response:
                        ready = response.status == 200 and json.load(response).get('success') is True
                except (OSError, ValueError):
                    pass
                if ready:
                    break
                time.sleep(.1)
            elapsed = time.monotonic()-start
        finally:
            stop.touch()
            try:
                probe.wait(timeout=10)
            except subprocess.TimeoutExpired:
                probe.kill(); probe.wait()
            probe_log.close()
            if proc.poll() is None:
                proc.terminate()
            proc.wait(timeout=20)
    statements = [json.loads(line) for line in ddl.read_text(encoding='utf-8').splitlines()]
    result = {'step':label, 'ready':ready, 'seconds':round(elapsed,3), 'ddl_count':len(statements),
              'ddl_failed':sum(x['failed'] for x in statements), 'probe_exit':probe.returncode}
    results.append(result); save(); print(json.dumps(result), flush=True)
    if not ready:
        raise RuntimeError(label + ' never became ready')
    if phase != 'upgrade' and statements:
        raise RuntimeError(label + ' repeated DDL; evidence preserved')


if not args.reuse_build:
    prepare()
for kind in args.databases:
    name = 'newapi-large-upgrade-' + secrets.token_hex(5) + '-' + kind
    work = OUT / (kind + '-runtime-' + secrets.token_hex(4))
    work.mkdir()
    db_env = {'SQL_DSN':'', 'LOG_SQL_DSN':'', 'SQLITE_PATH':str(work/'fixture.db')}
    owned = False
    try:
        if kind != 'sqlite':
            password = secrets.token_hex(20)
            extra = {'MYSQL_ROOT_PASSWORD':password, 'POSTGRES_PASSWORD':password}
            port = '3306' if kind == 'mysql' else '5432'
            flags = ['-e','MYSQL_ROOT_PASSWORD','-e','MYSQL_DATABASE=fixture'] if kind == 'mysql' else ['-e','POSTGRES_PASSWORD','-e','POSTGRES_DB=fixture']
            docker('run','-d','--name',name,'--label','purpose=newapi-large-upgrade','--memory','1536m','--cpus','2','-p',f'127.0.0.1::{port}',*flags,'mysql:5.7' if kind=='mysql' else 'postgres:9.6-alpine',extra=extra)
            owned = True
            mapped = docker('port', name, port+'/tcp').rsplit(':',1)[1]
            for _ in range(60):
                try:
                    if kind == 'mysql':
                        version = docker('exec','-e','MYSQL_PWD',name,'mysql','-uroot','-Nse','SELECT VERSION()',extra={'MYSQL_PWD':password})
                        docker('exec','-e','MYSQL_PWD',name,'mysql','-uroot','-e','ALTER DATABASE fixture CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci',extra={'MYSQL_PWD':password})
                        db_env['SQL_DSN'] = f'root:{password}@tcp(127.0.0.1:{mapped})/fixture?parseTime=true'
                    else:
                        version = docker('exec',name,'psql','-U','postgres','-d','fixture','-Atc','SELECT version()')
                        db_env['SQL_DSN'] = f'postgres://postgres:{password}@127.0.0.1:{mapped}/fixture?sslmode=disable'
                    results.append({'database':kind,'version':version}); save()
                    break
                except RuntimeError:
                    time.sleep(2)
            else:
                raise RuntimeError('database startup timeout')
        run([str(OUT/'fixture-old.exe')], work, kind+'-seed-old', db_env | {'LARGE_DATA_MODE':'seed'})
        boot(kind,'upgrade',db_env,work)
        boot(kind,'second-start',db_env,work)
        run([str(OUT/'fixture-new.exe')], work, kind+'-seed-new',db_env | {'LARGE_DATA_MODE':'seed-new'})
        boot(kind,'populated-new-tables',db_env,work)
        run([str(OUT/'fixture-new.exe')],work,kind+'-counts',db_env | {'LARGE_DATA_MODE':'count'})
        verify_counts(kind)
    except Exception as e:
        results.append({'database':kind,'failure':str(e)});save();print(json.dumps(results[-1]),flush=True)
    finally:
        if owned:
            docker('rm','-f',name)
        # Leave only schema/counts/log evidence, not large fixture database files.
        for f in work.glob('fixture.db*'):
            f.unlink()
save()
if any('failure' in result for result in results):
    raise SystemExit(1)
