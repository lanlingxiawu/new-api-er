"""Deploy only the dedicated main/mock/candidate fixture stack on Linux.

Run after uploading the two gzip binaries, mock source and build manifest to
/opt/new-api-main-test. Does not read or change an existing gateway's data.
"""
import gzip
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time
import urllib.request

ROOT=Path('/opt/new-api-main-test')
NETWORK='newapi-main-fixtures'
LABEL='newapi.main-fixtures=owned'
ROOT.mkdir(mode=0o700,parents=True,exist_ok=True)
for directory in ['config','bin']: (ROOT/directory).mkdir(mode=0o700,exist_ok=True)
def command(args): return subprocess.check_output(args,text=True,encoding='utf-8',stderr=subprocess.STDOUT).strip()
def digest(path):
    h=hashlib.sha256()
    with path.open('rb') as source:
        for part in iter(lambda:source.read(1024*1024),b''): h.update(part)
    return h.hexdigest()
def private(path,value):
    path.write_text(value,encoding='utf-8'); path.chmod(0o600)

manifest=json.loads((ROOT/'build-manifest.json').read_text())
latest=command(['git','ls-remote',manifest['official_remote'],'refs/heads/main']).split()[0]
if latest!=manifest['revision']: raise SystemExit('Official main advanced: rebuild the latest before deployment')
if not (ROOT/'runtime-loaded').exists():
    with gzip.open(ROOT/'runtime-images.tar.gz','rb') as archive:
        subprocess.run(['docker','load'],stdin=archive,stdout=(ROOT/'runtime-load.log').open('w'),stderr=subprocess.STDOUT,check=True)
    (ROOT/'runtime-loaded').touch()
for name in ['main','candidate']:
    target=ROOT/'bin'/('new-api-'+name)
    with gzip.open(str(target)+'.gz','rb') as source,target.with_suffix('.new').open('wb') as output: shutil.copyfileobj(source,output)
    target.with_suffix('.new').chmod(0o755); target.with_suffix('.new').replace(target)
if digest(ROOT/'bin/new-api-main')!=manifest['sha256']: raise SystemExit('Main binary checksum mismatch')
candidate_hash=digest(ROOT/'bin/new-api-candidate')
state_path=ROOT/'config/stack.private.json'
if state_path.exists(): state=json.loads(state_path.read_text())
else:
    state={'mock_key':secrets.token_urlsafe(48),'main_password':secrets.token_urlsafe(28)+'A1!','candidate_password':secrets.token_urlsafe(28)+'A1!'}
    private(state_path,json.dumps(state))
if NETWORK not in command(['docker','network','ls','--format','{{.Name}}']).splitlines(): command(['docker','network','create','--internal',NETWORK])
if not json.loads(command(['docker','network','inspect',NETWORK]))[0]['Internal']: raise SystemExit('Fixture network must be internal; refusing external-provider access')
for name in ['mock','main','candidate']:
    existing=subprocess.run(['docker','inspect','newapi-probe-'+name],capture_output=True,text=True)
    if existing.returncode==0:
        item=json.loads(existing.stdout)[0]
        if item['Config'].get('Labels',{}).get('newapi.main-fixtures')!='owned': raise SystemExit('Container ownership conflict: '+name)
        command(['docker','rm','-f','newapi-probe-'+name])
for name in ['mock','main','candidate']:
    data=ROOT/(name+'-data'); data.mkdir(mode=0o700,exist_ok=True)
    args=['docker','run','-d','--name','newapi-probe-'+name,'--label',LABEL,'--network',NETWORK,'--network-alias',name,'--restart','unless-stopped','--cap-drop','ALL','--security-opt','no-new-privileges:true','--pids-limit','128','--memory','256m' if name!='mock' else '128m','--memory-swap','384m' if name!='mock' else '192m','--cpus','0.5','-v',str(data)+':/data','-w','/data']
    if name=='mock':
        envfile=ROOT/'config/mock.env'; private(envfile,'MOCK_API_KEY='+state['mock_key']+'\nMOCK_DATA=/data\nPYTHONUNBUFFERED=1\n')
        args+=['--env-file',str(envfile),'-p','127.0.0.1:18301:8000','-v',str(ROOT/'mock_server.py')+':/opt/mock_server.py:ro','--entrypoint','python3','python:3.13-slim','/opt/mock_server.py']
    else:
        sql_dsn=(state['mysql_user']+':'+state['mysql_password']+'@unix(/var/run/mysqld/mysqld.sock)/'+state['mysql_databases'][name]+'?parseTime=true') if state.get('mysql_ready') else ''
        env={'SQL_DSN':sql_dsn,'LOG_SQL_DSN':'','SQLITE_PATH':'/data/gateway.db','SQL_MAX_OPEN_CONNS':'4','SQL_MAX_IDLE_CONNS':'2','REDIS_CONN_STRING':'','SESSION_SECRET':state.setdefault(name+'_session',secrets.token_hex(32)),'CRYPTO_SECRET':state.setdefault(name+'_crypto',secrets.token_hex(32)),'GLOBAL_API_RATE_LIMIT_ENABLE':'false','GLOBAL_API_USER_RATE_LIMIT_ENABLE':'false','GLOBAL_WEB_RATE_LIMIT_ENABLE':'false','UPDATE_TASK':'true','MEMORY_CACHE_ENABLED':'true','BATCH_UPDATE_ENABLED':'true','BATCH_UPDATE_INTERVAL':'1','ERROR_LOG_ENABLED':'true','LOG_LANGUAGE':'en','STREAMING_TIMEOUT':'3','GOMEMLIMIT':'160MiB','GIN_MODE':'release'}
        if sql_dsn: args+=['-v','/var/run/mysqld:/var/run/mysqld']
        envfile=ROOT/'config'/(name+'.env'); private(envfile,''.join(key+'='+value+'\n' for key,value in env.items()))
        args+=['--env-file',str(envfile),'-p','127.0.0.1:'+('18300' if name=='main' else '18302')+':3000','-v',str(ROOT/'bin'/('new-api-'+name))+':/opt/new-api:ro','--entrypoint','/opt/new-api','newapi-main-test-runtime:20261110','--port','3000']
    command(args)
private(state_path,json.dumps(state))
health={}
for name,port in [('mock',18301),('main',18300),('candidate',18302)]:
    # Docker internal networks deliberately suppress published ports. The host
    # reaches the private bridge IP; gateways use the private DNS aliases.
    container=json.loads(command(['docker','inspect','newapi-probe-'+name]))[0]
    address=container['NetworkSettings']['Networks'][NETWORK]['IPAddress']
    url='http://'+address+(':'+str(8000)+'/health' if name=='mock' else ':3000/api/status')
    for _ in range(120):
        try:
            result=json.load(urllib.request.urlopen(url,timeout=2))
            health[name]={'ok':True,'private_address':address,'url':url,'version':result.get('data',{}).get('version')}; break
        except Exception: time.sleep(.5)
    if name not in health: raise SystemExit('Health failed for '+name+'; inspect its private container log')
latest_after=command(['git','ls-remote',manifest['official_remote'],'refs/heads/main']).split()[0]
if latest_after!=manifest['revision']: raise SystemExit('Official main advanced while starting: rebuild and redeploy before testing')
result={'status':'ready','main_revision':manifest['revision'],'main_sha256':manifest['sha256'],'candidate_sha256':candidate_hash,'latest_verified_at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'health':health,'network':NETWORK,'private_network_only':True,'external_provider_access':False}
(ROOT/'deployment.json').write_text(json.dumps(result,indent=2))
print(json.dumps(result))
