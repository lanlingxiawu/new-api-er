"""Serial HK branch -> latest main -> resident mock contract audit.

Uses temporary, pinned channels and restores changed branch options in finally.
The equal-depth reference is main -> the same pinned main channel -> mock.
Secrets and before-config snapshots stay in the private server config directory.
"""
import hashlib
import calendar
import argparse
import json
from pathlib import Path
import subprocess
import sys
import time
import urllib.parse

ROOT = Path('/home/hk-admin/new-api-staging')
sys.path.insert(0, str(ROOT / 'mock/scripts'))
from mockctl import Main, CATALOG
import protocol_client as p


def response_contract(spec, raw, status):
    name, fmt, profile, path, body, mode = spec
    if status not in [200, 201, 202, 101] or not raw:
        return False
    value = p.parsed(raw)
    if isinstance(value, dict) and value.get('error'):
        return False
    if mode == 'binary':
        return raw.startswith(b'RIFF') and b'WAVE' in raw[:16]
    if mode in ['sse', 'ws', 'task_stream']:
        return (b'[DONE]' in raw or b'message_stop' in raw or b'response.completed' in raw or
                b'response.done' in raw or b'finishReason' in raw or b'finish_reason' in raw or
                b'image_generation.completed' in raw)
    if not isinstance(value, dict):
        return False
    if mode.startswith('task'):
        return bool(value.get('id') or value.get('task_id') or value.get('result'))
    if 'embedding' in name:
        return bool(value.get('data') or value.get('embedding') or value.get('embeddings'))
    if fmt == 'rerank':
        return bool(value.get('results'))
    if fmt == 'openai_image':
        return bool(value.get('data'))
    if fmt == 'openai_audio':
        return bool(value.get('text'))
    if fmt == 'claude':
        return value.get('type') == 'message' and bool(value.get('content'))
    if fmt == 'gemini':
        return bool(value.get('candidates'))
    if fmt == 'openai_responses':
        return value.get('object') == 'response' and bool(value.get('output'))
    if fmt == 'openai_responses_compaction':
        return value.get('object') == 'response.compaction' and bool(value.get('output'))
    if name == 'moderation' or fmt in ['openai_alpha_search', 'deepseek_tools']:
        return bool(value.get('results') or value.get('content'))
    return bool(value.get('choices'))


def main(only=None,stream_policy='inherit'):
    release = json.loads((ROOT/'apps/main/current/release.json').read_text())
    tip = subprocess.check_output(['git','ls-remote','https://github.com/QuantumNous/new-api.git','refs/heads/main'],text=True,timeout=90).split()[0]
    assert release['revision'] == tip, 'Deploy current official main before this test'
    mock = ROOT/'mock'
    report = mock/'reports'/time.strftime('chain-audit-%Y%m%dT%H%M%SZ',time.gmtime())
    (report/'responses').mkdir(parents=True)
    state = json.loads((mock/'config/access.private.json').read_text())
    credentials = json.loads((ROOT/'ops/credentials.private.json').read_text())
    upstream = Main()
    branch = Main.__new__(Main)
    branch.base = 'http://127.0.0.1:13000'
    branch.token = None
    branch.token = branch.api('/api/user/login',{'username':'hkadmin','password':credentials['branch_admin']})['access_token']
    clients = {'reference':upstream, 'branch':branch}
    options = {name:{v['key']:v['value'] for v in client.api('/api/option/')} for name,client in clients.items()}
    original_channels = upstream.channels()
    suffix = str(int(time.time()))
    private = mock/'config'/('chain-audit-'+suffix+'.private.json')
    private.write_text(json.dumps({'branch_options':options['branch']}))
    private.chmod(0o600)
    created = []
    token_id = None
    changed_options = []
    records, failures = [], []
    keys = {'reference':state['test_token']}
    channel_ids = {'reference':{},'branch':{}}
    profiles = {k:dict(v) for k,v in CATALOG['profiles'].items() if k in ['openai','claude','gemini','xai','rerank','alpha','deepseek','mj','task']}
    # Supplemental provider contracts use OpenAI as the outer gateway protocol.
    for profile in ['type-04-ollama','type-34-cohere','plugin-doubao']:
        if profile in CATALOG['profiles']:
            profiles[profile] = dict(CATALOG['profiles'][profile], main=1, candidate=1)
    try:
        desired = {'fetch_setting.allow_private_ip':'true',
                   'fetch_setting.allowed_ports':json.dumps(sorted(set(json.loads(options['branch'].get('fetch_setting.allowed_ports','[]'))+['13001','18301'])))}
        for key in ['ModelRatio','CompletionRatio','ModelPrice']:
            merged=json.loads(options['branch'].get(key,'{}'))
            for model,value in json.loads(options['reference'].get(key,'{}')).items():
                if model in {m for config in profiles.values() for m in config['models']}:
                    merged.setdefault(model,value)
            desired[key]=json.dumps(merged)
        for key,value in desired.items():
            if options['branch'].get(key) != value:
                branch.api('/api/option/',{'key':key,'value':value},'PUT')
                changed_options.append(key)
        token_name='chain-audit-'+suffix
        branch.api('/api/token/',{'name':token_name,'unlimited_quota':True,'expired_time':-1,'group':'default'})
        token_id=next(t['id'] for t in branch.api('/api/token/?page_size=100')['items'] if t['name']==token_name)
        keys['branch']=branch.api('/api/token/'+str(token_id)+'/key',{})['key']
        for name,client in clients.items():
            for profile,config in profiles.items():
                inner=original_channels[profile]['id']
                setting={'responses_websocket_enabled':True}
                if name=='branch' and stream_policy!='inherit':
                    setting.update(stream_protocol_validation=stream_policy=='on',claude_stream_validation=stream_policy=='on')
                channel_type=config.get('candidate',1) if name=='branch' else config['main']
                if profile in ['task','plugin-doubao']:
                    channel_type=61 if name=='branch' else 60
                    setting['task_extend_plugin_keys']=['sora' if profile=='task' else 'doubao']
                channel={'name':'chain-audit-'+suffix+'-'+name+'-'+profile,
                         'type':channel_type,'key':state['test_token']+'-'+str(inner),
                         'base_url':upstream.base,'models':','.join(config['models']),
                         'group':'default','priority':-1000,'weight':1,'auto_ban':0,
                         'setting':json.dumps(setting),
                         'header_override':json.dumps({'X-Mock-Case':'{client_header:X-Mock-Case}'})}
                client.api('/api/channel/',{'mode':'single','channel':channel})
                listed=client.api('/api/channel/?page_size=100')['items']
                ident=next(c['id'] for c in listed if c['name']==channel['name'])
                channel_ids[name][profile]=ident
                created.append((name,ident))
        baseline={name:client.api('/api/user/self')['quota'] for name,client in clients.items()}
        # Management writes and the gateway's channel cache publish separately.
        # Keep the original immediate-after-create failures in their first report.
        time.sleep(3)
        start=time.time()
        specs=[s for s in CATALOG['specs'] if s[2] in profiles and not s[0].startswith('minimax')]
        specs += [
            ['ollama_responses','openai_responses','type-04-ollama','/v1/responses',{'model':'llama3.1','input':'fixture'},'json'],
            ['ollama_claude','claude','type-04-ollama','/v1/messages',{'model':'llama3.1','max_tokens':100,'messages':[{'role':'user','content':'fixture'}]},'json'],
            ['cohere_rerank','rerank','type-34-cohere','/v1/rerank',{'model':'command-r-plus','query':'fixture','documents':['fixture document']},'json'],
            ['seedream_image','openai_image','plugin-doubao','/v1/images/generations',{'model':'doubao-seedream-4-0-250828','prompt':'fixture','size':'2048x2048','n':1},'json'],
        ]
        if only:
            specs=[s for s in specs if s[0] in only.split(',')]
            assert specs, 'No selected specs'
        for spec in specs:
            spec_name,fmt,profile,path,body,mode=spec
            cases=['normal']
            if spec_name in ['chat_json','chat_sse','claude_json','claude_sse','gemini_json','gemini_sse','responses_json','responses_sse']:
                cases+=['http_429','provider_error','missing_usage','zero_usage']
            for case in cases:
                p.CASE=case
                for name,client in clients.items():
                    row={'layer':name,'spec':spec_name,'format':fmt,'profile':profile,'mode':mode,'case':case,'channel_id':channel_ids[name][profile],'inner_channel_id':original_channels[profile]['id'],'started_at':time.time()}
                    token=keys[name]+'-'+str(channel_ids[name][profile])
                    try:
                        if mode=='ws':
                            status,headers,raw=p.ws_call({'base':client.base,'key':token},path,body)
                        elif mode.endswith('multipart'):
                            payload,ctype=p.multipart(body,mode)
                            status,headers,raw=p.request(client.base,path,payload,token,ctype=ctype,timeout=40)
                        else:
                            status,headers,raw=p.request(client.base,path,body,token,timeout=110 if mode.startswith('task') else 30)
                        filename=name+'-'+spec_name+'-'+case+'.bin'
                        (report/'responses'/filename).write_bytes(raw)
                        row.update(status=status,bytes=len(raw),body_file=filename,sha256=hashlib.sha256(raw).hexdigest(),
                                   request_id=next((v for k,v in headers.items() if k.lower()=='x-oneapi-request-id'),None),wire_usage=p.wire_usage(raw))
                        value=p.parsed(raw)
                        if mode.startswith('task') and isinstance(value,dict):
                            ident=value.get('id') or value.get('task_id') or value.get('result')
                            if isinstance(ident,str):row['task_id']='task_'+ident[5:] if ident.startswith('resp_') else ident
                        if case=='normal':
                            row['contract_ok']=response_contract(spec,raw,status)
                        elif case in ['http_429','provider_error']:
                            row['contract_ok']=status>=400 or (isinstance(value,dict) and bool(value.get('error'))) or b'"error"' in raw
                        else:
                            row['contract_ok']=True  # Usage cases are observations, not correctness passes.
                            row['usage_observation_only']=True
                        if not row['contract_ok']:failures.append({'layer':name,'spec':spec_name,'case':case,'reason':'response_contract'})
                    except Exception as error:
                        row['error']=type(error).__name__
                        failures.append({'layer':name,'spec':spec_name,'case':case,'reason':type(error).__name__})
                    row['finished_at']=time.time()
                    records.append(row)
            (report/'progress.json').write_text(json.dumps({'completed':spec_name,'requests':len(records),'failures':failures},indent=2))
            print(json.dumps({'completed':spec_name,'requests':len(records),'failures':len(failures)}),flush=True)
        p.CASE='normal'
        p.task_lifecycle([{'name':name,'base':client.base,'key':keys[name]} for name,client in clients.items()],records,failures)
        # Wait for asynchronous consumption rows and quota batches to settle.
        previous=None
        for _ in range(30):
            wallets={name:client.api('/api/user/self')['quota'] for name,client in clients.items()}
            if wallets==previous:break
            previous=wallets
            time.sleep(2)
        time.sleep(5)
        ledgers={}
        for name,client in clients.items():
            rows=[]
            for page in range(1,20):
                query=urllib.parse.urlencode({'start_timestamp':int(start),'end_timestamp':int(time.time())+1,'page_size':100,'p':page})
                items=client.api('/api/log/?'+query)['items']
                rows.extend(items)
                if len(items)<100:break
            ledgers[name]=list({r['id']:r for r in rows if r['type'] in [2,5]}.values())
            (report/(name+'-ledger.json')).write_text(json.dumps(ledgers[name],indent=2))
        audits=[json.loads(line) for line in (mock/'data/requests.jsonl').read_text().splitlines()]
        for row in records:
            row['logs']=[r for r in ledgers[row['layer']] if row.get('request_id') and r.get('request_id')==row['request_id']]
            if row['mode']=='ws' and not row.get('request_id'):
                for log in ledgers[row['layer']]:
                    ident=log.get('request_id','')
                    if log.get('channel')!=row['channel_id'] or len(ident)<23:continue
                    try:stamp=calendar.timegm(time.strptime(ident[:14],'%Y%m%d%H%M%S'))+int(ident[14:23])/1e9
                    except ValueError:continue
                    if row['started_at']<=stamp<=row['finished_at']:row['logs'].append(log)
                row['log_correlation']='channel ID and request ID timestamp within the serial WebSocket call'
            row['mock_evidence']=[{k:a.get(k) for k in ['id','protocol','case','path','status','response_sha256']} for a in audits if row['started_at']<=a['time']<=row['finished_at'] and a['case']==row['case'] and (a.get('method')=='POST' or row['mode']=='ws')]
            upstream_ids={r.get('upstream_request_id') for r in row['logs'] if r.get('upstream_request_id')}
            row['main_upstream_logs']=[r for r in ledgers['reference'] if r.get('request_id') in upstream_ids]
        wallet_after={name:client.api('/api/user/self')['quota'] for name,client in clients.items()}
        result={'main_revision':tip,'main_sha256':release['sha256'],'branch_test_stream_policy':stream_policy,
                'branch_release':json.loads((ROOT/'apps/branch/current/release.json').read_text()),
                'requests':len(records),'failures':failures,'records':records,
                'wallet_before':baseline,'wallet_after':wallet_after,
                'log_quota_sum':{name:sum(r['quota'] for r in rows if r['type']==2) for name,rows in ledgers.items()},
                'notes':['Usage observations are not passes; prices can differ between instances.',
                         'WebSocket logs without response IDs use channel ID and nanosecond request timestamp correlation.',
                         'Task content reachability does not prove the resident placeholder is playable.']}
        (report/'result.json').write_text(json.dumps(result,indent=2))
        print(json.dumps({'report':str(report),'requests':len(records),'failure_count':len(failures),
                          'failures':[{k:f[k] for k in ['layer','spec','case','reason','error','status'] if k in f} for f in failures]}),flush=True)
    finally:
        p.CASE='normal'
        for name,ident in reversed(created):
            clients[name].api('/api/channel/'+str(ident),method='DELETE')
        if token_id is not None:branch.api('/api/token/'+str(token_id),method='DELETE')
        for key in changed_options:
            branch.api('/api/option/',{'key':key,'value':options['branch'][key]},'PUT')
        after={r['key']:r['value'] for r in branch.api('/api/option/')}
        restored=all(after.get(key)==options['branch'][key] for key in changed_options)
        (report/'cleanup.json').write_text(json.dumps({'temporary_channels_removed':len(created),'temporary_token_removed':token_id is not None,'options_restored':restored},indent=2))
        assert restored, 'Temporary test options were not restored'
    return bool(failures)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--only',help='Comma-separated spec names for a supplemental run')
    parser.add_argument('--stream-policy',choices=['inherit','on','off'],default='inherit')
    args=parser.parse_args()
    raise SystemExit(main(args.only,args.stream_policy))
