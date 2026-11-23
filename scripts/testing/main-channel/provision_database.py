"""Create isolated fixture databases using a server-local admin credential.

Never prints, copies out, or modifies the existing gateway's configuration.
"""
import json
import os
from pathlib import Path
import re
import secrets
import subprocess

ROOT=Path('/opt/new-api-main-test')
state_path=ROOT/'config/stack.private.json'
state=json.loads(state_path.read_text())
if state.get('mysql_ready'):
    print(json.dumps({'status':'already_provisioned','databases':state['mysql_databases']})); raise SystemExit()
config=Path('/root/.env')
values={}
if config.exists():
    for line in config.read_text().splitlines():
        key,sep,value=line.partition('=')
        if sep and not key.lstrip().startswith('#'): values[key.strip()]=value.strip().strip('"').strip("'")
dsn=values.get('SQL_DSN','')
match=re.match(r'^(.*?)@(?:tcp|unix)\(',dsn)
if not match: raise SystemExit('No usable local MySQL admin DSN; no database changes performed')
username,separator,password=match.group(1).partition(':')
env=os.environ.copy(); env['MYSQL_PWD']=password
def sql(statement):
    result=subprocess.run(['mysql','--protocol=socket','-u',username,'--batch','--skip-column-names'],input=statement,text=True,capture_output=True,env=env)
    if result.returncode: raise RuntimeError('Fixture database provisioning failed (MySQL client exit '+str(result.returncode)+'); no credentials are logged')
    return result.stdout.strip()
suffix=secrets.token_hex(3)
user='np_probe_'+suffix
pw=secrets.token_hex(24)
databases={name:'np_probe_'+name+'_'+suffix for name in ['main','candidate']}
if sql('SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name IN ('+','.join("'"+name+"'" for name in databases.values())+')')!='0': raise SystemExit('Unexpected database ownership conflict')
sql("CREATE USER '"+user+"'@'localhost' IDENTIFIED BY '"+pw+"';"+''.join('CREATE DATABASE `'+db+'` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; GRANT ALL PRIVILEGES ON `'+db+"`.* TO '"+user+"'@'localhost';" for db in databases.values()))
state.update(mysql_ready=True,mysql_databases=databases,mysql_user=user,mysql_password=pw)
for name in databases: state.pop(name+'_api_key',None)
state_path.write_text(json.dumps(state)); state_path.chmod(0o600)
print(json.dumps({'status':'provisioned','mysql_version':sql('SELECT VERSION()'),'databases':databases,'max_connections_per_gateway':4,'existing_gateway_tables_touched':False}))
