"""Reconcile actual MySQL wallet deltas against PostgreSQL settled log sums.

Run only after the serial protocol matrices finish. Every reference request may
charge two internal main hops; reconcile all rows, not just the outer request.
"""
import json
from pathlib import Path
import subprocess
import time
import matrix as m

ROOT=Path('/opt/new-api-main-test')


def snapshot(name):
    sql="SELECT quota FROM fixture_"+name+".users WHERE username='probe_root';"
    value=subprocess.check_output(['docker','exec','newapi-probe-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -N -e "$1"','sh',sql],text=True)
    log_id=subprocess.check_output(['docker','exec','newapi-probe-pgsql','psql','-U','postgres','-d','fixture_'+name+'_logs','-Atc','SELECT COALESCE(max(id),0) FROM logs;'],text=True)
    return {'quota':int(value.strip()),'last_id':int(log_id.strip())}


def charges(name,last_id):
    sql='SELECT id,quota,request_id FROM logs WHERE id>'+str(last_id)+' AND type=2 ORDER BY id;'
    value=subprocess.check_output(['docker','exec','newapi-probe-pgsql','psql','-U','postgres','-d','fixture_'+name+'_logs','-At','-F','\t','-c',sql],text=True)
    return [{'id':int(a[0]),'quota':int(a[1]),'request_id':a[2]} for line in value.splitlines() if (a:=line.split('\t')) and len(a)==3]


main=m.setup('main')
candidate=m.setup('candidate')
for client in [main,candidate]:
    existing=m.api(client,'/api/channel/?page_size=100')['items']
    client['channels']={profile:next(item['id'] for item in existing if item['name']=='main-fixture-'+profile) for profile in m.PROFILES}
records=[]
failures=[]
for case in ['normal','cache_split_exceeds_total','cache_split_corrected','cache_split_explicit_zero']:
    m.set_case(main,'claude',case)
    for spec in [s for s in m.SPECS if s[0] in ['claude_json','claude_sse']]:
        for client in [main,candidate]:
            before={name:snapshot(name) for name in ['main','candidate']}
            status,headers,body=m.invoke(client,spec)
            # Batch accounting/logging flushes every second; wait for both
            # durable stores, then verify two consecutive stable observations.
            previous=None
            deadline=time.monotonic()+20
            while time.monotonic()<deadline:
                time.sleep(1)
                after={name:snapshot(name) for name in ['main','candidate']}
                logs={name:charges(name,before[name]['last_id']) for name in ['main','candidate']}
                state=(after,logs)
                required_stores=['main'] if client['name']=='main' else ['main','candidate']
                reconciled=all(logs[name] and before[name]['quota']-after[name]['quota']==sum(log['quota'] for log in logs[name]) for name in required_stores)
                # Wallet batches flush separately from the five-second log
                # pipeline. Stable but unreconciled observations are not final.
                if state==previous and reconciled:break
                previous=state
            comparison={name:{'wallet_delta':before[name]['quota']-after[name]['quota'],'ledger_sum':sum(log['quota'] for log in logs[name]),'entries':logs[name]} for name in ['main','candidate']}
            row={'case':case,'spec':spec[0],'client':client['name'],'status':status,'stores':comparison}
            records.append(row)
            for name,item in comparison.items():
                if item['wallet_delta']!=item['ledger_sum']:
                    failures.append({'case':case,'spec':spec[0],'client':client['name'],'store':name,'error':'wallet delta differs from settled ledger sum',**item})
            print(json.dumps({'case':case,'spec':spec[0],'client':client['name'],'wallet_matches':all(item['wallet_delta']==item['ledger_sum'] for item in comparison.values())}),flush=True)
m.set_case(main,'claude','normal')
result={'status':'failed' if failures else 'passed','requests':len(records),'records':records,'failures':failures,'main_database':'MySQL','log_database':'PostgreSQL'}
path=ROOT/'reports'/('wallet-'+time.strftime('%Y%m%d-%H%M%S'))
path.mkdir()
(path/'result.json').write_text(json.dumps(result,indent=2))
print(json.dumps({'status':result['status'],'requests':len(records),'failures':len(failures),'report':str(path)}),flush=True)
raise SystemExit(bool(failures))
