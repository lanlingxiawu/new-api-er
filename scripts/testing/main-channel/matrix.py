"""Real client -> candidate channel -> latest official main -> mock matrix.

Run on the dedicated test host. All credentials stay in config/*.private.json.
The report distinguishes mock wire evidence, main wire usage, and both ledgers.
"""
import argparse
import base64
import calendar
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import socket
import struct
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from review_evidence import review

ROOT=Path('/opt/new-api-main-test')
NETWORK='newapi-main-fixtures'
parser=argparse.ArgumentParser(); parser.add_argument('--smoke',action='store_true'); parser.add_argument('--only'); parser.add_argument('--resume-report',type=Path)
parser.add_argument('--fixed-policy', action='store_true', help='Committed candidate has no configurable stream policy; test its actual fixed behavior once')
parser.add_argument('--cases', help='Explicit fixture cases for a focused supplemental run')
parser.add_argument('--cache-expression', action='store_true', help='Use explicit USD cache pricing in the isolated Claude channels')
STATE_PATH=ROOT/'config/stack.private.json'
STATE=json.loads(STATE_PATH.read_text())

def container_ip(name):
    return json.loads(subprocess.check_output(['docker','inspect','newapi-probe-'+name],text=True))[0]['NetworkSettings']['Networks'][NETWORK]['IPAddress']
def save_state(): STATE_PATH.write_text(json.dumps(STATE)); STATE_PATH.chmod(0o600)
def request(base,path,body=None,token=None,method=None,ctype='application/json',timeout=12):
    headers={'Content-Type':ctype}
    if token: headers['Authorization']='Bearer '+token
    if body is not None and not isinstance(body,bytes): body=json.dumps(body).encode()
    req=urllib.request.Request(base+path,data=body,headers=headers,method=method)
    try:
        with urllib.request.urlopen(req,timeout=timeout) as response: return response.status,dict(response.headers),response.read()
    except urllib.error.HTTPError as exc: return exc.code,dict(exc.headers),exc.read()
def parsed(payload):
    try: return json.loads(payload)
    except (ValueError,UnicodeDecodeError): return None
def api(client,path,body=None,method=None):
    status,_,raw=request(client['base'],path,body,client.get('admin'),method)
    if status==401 and path!='/api/user/login':
        # Long matrices can outlive a dashboard access token. Refresh only this
        # private fixture login, then retry the rejected management operation.
        client['admin']=api(client,'/api/user/login',{'username':'probe_root','password':STATE[client['name']+'_password']})['access_token']
        status,_,raw=request(client['base'],path,body,client['admin'],method)
    value=parsed(raw)
    if status!=200 or not isinstance(value,dict) or not value.get('success'): raise RuntimeError('Management API failed: '+path+' '+str(status)+' '+str(value)[:350])
    return value.get('data')
def setup(name):
    client={'name':name,'base':'http://'+container_ip(name)+':3000'}
    setup_info=parsed(request(client['base'],'/api/setup')[2])
    if setup_info and not setup_info.get('data',{}).get('status'):
        api(client,'/api/setup',{'username':'probe_root','password':STATE[name+'_password'],'confirmPassword':STATE[name+'_password']})
    client['admin']=api(client,'/api/user/login',{'username':'probe_root','password':STATE[name+'_password']})['access_token']
    if name+'_api_key' not in STATE:
        api(client,'/api/token/',{'name':'main-channel-fixtures','unlimited_quota':True,'expired_time':-1,'group':'default'})
        token_id=api(client,'/api/token/?page_size=100')['items'][0]['id']
        STATE[name+'_api_key']=api(client,f'/api/token/{token_id}/key',{})['key']; save_state()
    client['key']=STATE[name+'_api_key']
    return client

PROFILES={
 'openai':{'models':['gpt-probe','text-embedding-3-small','gpt-image-1','whisper-1','tts-1','text-moderation-latest','gpt-realtime'],'main':1,'candidate':1},
 'claude':{'models':['claude-probe'],'main':14,'candidate':14},
 'gemini':{'models':['gemini-probe','gemini-embedding-001'],'main':24,'candidate':24},
 'xai':{'models':['grok-probe'],'main':48,'candidate':48},
 'rerank':{'models':['jina-reranker-v2-base-multilingual'],'main':38,'candidate':38},
 'alpha':{'models':['probe-alpha'],'main':60,'candidate':61},
 'deepseek':{'models':['deepseek-search','deepseek-fetch'],'main':43,'candidate':43},
 'mj':{'models':['mj_imagine','mj_describe','mj_blend','mj_upscale','mj_variation','mj_reroll','mj_modal','mj_inpaint','mj_zoom','mj_custom_zoom','mj_shorten','mj_high_variation','mj_low_variation','mj_pan','swap_face','mj_upload','mj_video','mj_edits'],'main':2,'candidate':2},
 'task':{'models':['sora-2','sora-2-pro'],'main':55,'candidate':61},
}
def configure(clients):
    models=[model for key,profile in PROFILES.items() if key!='task' for model in profile['models']]+[model+'-reference' for name in ['openai','claude','gemini','xai'] for model in PROFILES[name]['models']]
    for client in clients:
        # This is a serial protocol fixture on CPU-limited isolated containers,
        # not a load test. Host CPU pressure must not replace mock responses.
        api(client,'/api/option/',{'key':'performance_setting.monitor_enabled','value':'false'},'PUT')
        if client['name']=='candidate':
            # Compare protocol handling under the common STREAMING_TIMEOUT.
            # The fork's independent 300-second response budget otherwise
            # suppresses main's scanner timeout and invalidates parity results.
            api(client,'/api/option/',{'key':'relay_timeout_setting.enabled','value':'false'},'PUT')
            api(client,'/api/option/',{'key':'relay_error_display_setting.enabled','value':'false'},'PUT')
        for key,value in {'ModelRatio':json.dumps({model:1 for model in models}),'CompletionRatio':json.dumps({model:1 for model in models}),'ModelPrice':json.dumps({**{model:.01 for model in PROFILES['mj']['models']},'probe-alpha':.001,'deepseek-search':.001,'deepseek-fetch':.001}),'LogConsumeEnabled':'true','RetryTimes':'0','fetch_setting.allow_private_ip':'true','fetch_setting.allowed_ports':'["3000","8000"]'}.items(): api(client,'/api/option/',{'key':key,'value':value},'PUT')
        existing={item['name']:item['id'] for item in api(client,'/api/channel/?page_size=100')['items']}
        client['channels']={}
        for profile_name,profile in PROFILES.items():
            name='main-fixture-'+profile_name
            if name not in existing:
                setting={'task_extend_plugin_keys':['sora']} if profile_name=='task' and client['name']=='candidate' else {}
                channel={'type':profile[client['name']],'key':STATE['mock_key'] if client['name']=='main' else STATE['main_api_key'],'name':name,'base_url':'http://mock:8000/mock/normal' if client['name']=='main' else 'http://main:3000','models':','.join(profile['models']),'group':'default','status':1,'weight':1,'auto_ban':0,'setting':json.dumps(setting)}
                if client['name']=='main': channel['header_override']=json.dumps({'x-mock-key':STATE['mock_key']})
                api(client,'/api/channel/',{'mode':'single','channel':channel})
            client['channels'][profile_name]=next(item['id'] for item in api(client,'/api/channel/?page_size=100')['items'] if item['name']==name)
        channel=api(client,'/api/channel/'+str(client['channels']['openai']))
        setting=json.loads(channel.get('setting') or '{}'); setting['responses_websocket_enabled']=True
        channel['setting']=json.dumps(setting); channel.pop('key',None);channel.pop('status',None)
        api(client,'/api/channel/',channel,'PUT')
        if client['name']=='main':
            for ref_profile in ['openai','claude','gemini','xai']:
                reference_name='main-fixture-'+ref_profile+'-reference'
                mapping={model+'-reference':model for model in PROFILES[ref_profile]['models'] if model!='gpt-realtime'}
                if reference_name not in existing:
                    channel={'type':PROFILES[ref_profile]['main'],'key':STATE['main_api_key'],'name':reference_name,'base_url':'http://main:3000','models':','.join(mapping),'model_mapping':json.dumps(mapping),'group':'default','status':1,'weight':1,'auto_ban':0,'setting':json.dumps({'responses_websocket_enabled':True})}
                    api(client,'/api/channel/',{'mode':'single','channel':channel})
                else:
                    channel=api(client,'/api/channel/'+str(existing[reference_name]))
                    channel.update(models=','.join(mapping),model_mapping=json.dumps(mapping))
                    channel.pop('key',None);channel.pop('status',None)
                    api(client,'/api/channel/',channel,'PUT')
            # Main does not rewrite a Realtime URL's model query via model_mapping.
            # Use its custom URL adaptor so this reference has exactly two hops.
            if 'main-fixture-realtime-reference' not in existing:
                channel={'type':8,'key':STATE['main_api_key'],'name':'main-fixture-realtime-reference','base_url':'http://main:3000/v1/realtime?model=gpt-realtime','models':'gpt-realtime-reference','model_mapping':json.dumps({'gpt-realtime-reference':'gpt-realtime'}),'group':'default','status':1,'weight':1,'auto_ban':0}
                api(client,'/api/channel/',{'mode':'single','channel':channel})
    save_state()
def set_case(main,profile,case):
    channel=api(main,'/api/channel/'+str(main['channels'][profile]))
    channel['base_url']='http://mock:8000/mock/'+case
    channel.pop('key',None); channel.pop('status',None)
    api(main,'/api/channel/',channel,'PUT')
def strict(candidate,enabled):
    if parser.parse_args(None if __name__ == '__main__' else []).fixed_policy:
        if not enabled: raise ValueError('Cannot disable the committed fixed stream policy')
        return
    api(candidate,'/api/option/group',{'scope':'request-policies.filtering','module':'stream_protocol_validation_setting','values':{'enabled':str(enabled).lower()}},'PUT')

SPECS=[
 ('xai_sse','openai','xai','/v1/chat/completions',{'model':'grok-probe','messages':[{'role':'user','content':'fixture'}],'stream':True,'stream_options':{'include_usage':True}},'sse'),
 ('chat_json','openai','openai','/v1/chat/completions',{'model':'gpt-probe','messages':[{'role':'user','content':'fixture'}]},'json'),
 ('chat_sse','openai','openai','/v1/chat/completions',{'model':'gpt-probe','messages':[{'role':'user','content':'fixture'}],'stream':True,'stream_options':{'include_usage':True}},'sse'),
 ('completion_json','openai','openai','/v1/completions',{'model':'gpt-probe','prompt':'fixture'},'json'),
 ('completion_sse','openai','openai','/v1/completions',{'model':'gpt-probe','prompt':'fixture','stream':True},'sse'),
 ('moderation','openai','openai','/v1/moderations',{'model':'text-moderation-latest','input':'fixture'},'json'),
 ('claude_json','claude','claude','/v1/messages',{'model':'claude-probe','max_tokens':100,'messages':[{'role':'user','content':'fixture'}]},'json'),
 ('claude_sse','claude','claude','/v1/messages',{'model':'claude-probe','max_tokens':100,'messages':[{'role':'user','content':'fixture'}],'stream':True},'sse'),
 ('claude_to_chat_json','openai','claude','/v1/chat/completions',{'model':'claude-probe','messages':[{'role':'user','content':'fixture'}]},'json'),
 ('claude_to_chat_sse','openai','claude','/v1/chat/completions',{'model':'claude-probe','messages':[{'role':'user','content':'fixture'}],'stream':True,'stream_options':{'include_usage':True}},'sse'),
 ('claude_to_responses_json','openai_responses','claude','/v1/responses',{'model':'claude-probe','input':'fixture'},'json'),
 ('claude_to_responses_sse','openai_responses','claude','/v1/responses',{'model':'claude-probe','input':'fixture','stream':True},'sse'),
 ('claude_to_gemini_json','gemini','claude','/v1beta/models/claude-probe:generateContent',{'contents':[{'role':'user','parts':[{'text':'fixture'}]}]},'json'),
 ('claude_to_gemini_sse','gemini','claude','/v1beta/models/claude-probe:streamGenerateContent?alt=sse',{'contents':[{'role':'user','parts':[{'text':'fixture'}]}]},'sse'),
 ('responses_json','openai_responses','openai','/v1/responses',{'model':'gpt-probe','input':'fixture'},'json'),
 ('responses_sse','openai_responses','openai','/v1/responses',{'model':'gpt-probe','input':'fixture','stream':True},'sse'),
 ('responses_compact','openai_responses_compaction','openai','/v1/responses/compact',{'model':'gpt-probe','input':[{'role':'user','content':'fixture'}]},'json'),
 ('alpha_search','openai_alpha_search','alpha','/v1/alpha/search',{'model':'probe-alpha','query':'fixture','queries':['fixture']},'json'),
 ('deepseek_search','deepseek_tools','deepseek','/v1/tools/search',{'query':'fixture'},'json'),
 ('deepseek_fetch','deepseek_tools','deepseek','/v1/tools/fetch',{'url':'https://example.invalid/fixture'},'json'),
 ('gemini_json','gemini','gemini','/v1beta/models/gemini-probe:generateContent',{'contents':[{'role':'user','parts':[{'text':'fixture'}]}]},'json'),
 ('gemini_sse','gemini','gemini','/v1beta/models/gemini-probe:streamGenerateContent?alt=sse',{'contents':[{'role':'user','parts':[{'text':'fixture'}]}]},'sse'),
 ('gemini_v1_alias','gemini','gemini','/v1/models/gemini-probe:generateContent',{'contents':[{'role':'user','parts':[{'text':'fixture'}]}]},'json'),
 ('gemini_embedding','gemini','gemini','/v1beta/models/gemini-embedding-001:embedContent',{'model':'models/gemini-embedding-001','content':{'parts':[{'text':'fixture'}]}},'json'),
 ('gemini_batch_embedding','gemini','gemini','/v1beta/models/gemini-embedding-001:batchEmbedContents',{'requests':[{'model':'models/gemini-embedding-001','content':{'parts':[{'text':'fixture'}]}}]},'json'),
 ('embedding','embedding','openai','/v1/embeddings',{'model':'text-embedding-3-small','input':['fixture']},'json'),
 ('rerank','rerank','rerank','/v1/rerank',{'model':'jina-reranker-v2-base-multilingual','query':'fixture','documents':['fixture document'],'top_n':1},'json'),
 ('image_json','openai_image','openai','/v1/images/generations',{'model':'gpt-image-1','prompt':'fixture','n':1,'size':'1024x1024'},'json'),
 ('image_sse','openai_image','openai','/v1/images/generations',{'model':'gpt-image-1','prompt':'fixture','n':1,'size':'1024x1024','stream':True},'sse'),
 ('image_edit','openai_image','openai','/v1/images/edits',{'model':'gpt-image-1','prompt':'fixture','n':'1'},'image_multipart'),
 ('audio_speech','openai_audio','openai','/v1/audio/speech',{'model':'tts-1','input':'fixture audio','voice':'alloy','response_format':'wav'},'binary'),
 ('audio_transcription','openai_audio','openai','/v1/audio/transcriptions',{'model':'whisper-1','response_format':'verbose_json'},'audio_multipart'),
 ('audio_translation','openai_audio','openai','/v1/audio/translations',{'model':'whisper-1','response_format':'verbose_json'},'audio_multipart'),
 ('realtime_ws','openai_realtime','openai','/v1/realtime?model=gpt-realtime',{'type':'response.create','response':{'modalities':['text'],'instructions':'fixture'}},'ws'),
 ('responses_ws','openai_responses','openai','/v1/responses',{'type':'response.create','model':'gpt-probe','input':'fixture'},'ws'),
 ('task_generic','task','task','/v1/tasks/sora',{'model':'sora-2','prompt':'fixture','seconds':4,'size':'720x1280'},'task'),
 ('video_create','task','task','/v1/videos',{'model':'sora-2','prompt':'fixture','seconds':'4','size':'720x1280'},'task'),
 ('task_responses_background','openai_responses','task','/v1/responses',{'model':'sora-2','input':'fixture','background':True},'task'),
 ('task_responses_sync','openai_responses','task','/v1/responses',{'model':'sora-2','input':'fixture'},'task_sync'),
 ('task_responses_stream','openai_responses','task','/v1/responses',{'model':'sora-2','input':'fixture','stream':True},'task_stream'),
 ('midjourney','mj_proxy','mj','/mj/submit/imagine',{'prompt':'fixture','botType':'MID_JOURNEY'},'task'),
]

def multipart(fields,kind):
    # Import only public fixture bytes, never its required server credential.
    boundary='fixture_'+secrets.token_hex(8); body=bytearray()
    for key,value in fields.items(): body.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n').encode())
    filename='fixture.png' if kind=='image_multipart' else 'fixture.wav'; field='image' if kind=='image_multipart' else 'file'
    if kind=='image_multipart': content=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP1sAAAAASUVORK5CYII=')
    else:
        import io,wave
        stream=io.BytesIO()
        with wave.open(stream,'wb') as wav: wav.setnchannels(1);wav.setsampwidth(2);wav.setframerate(8000);wav.writeframes(b'\0\0'*800)
        content=stream.getvalue()
    body.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="{field}"; filename="{filename}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()+content+f'\r\n--{boundary}--\r\n'.encode())
    return bytes(body),'multipart/form-data; boundary='+boundary
def ws_call(client,path,message):
    address=urllib.parse.urlsplit(client['base']); connection=socket.create_connection((address.hostname,address.port),timeout=12); connection.settimeout(8)
    key=base64.b64encode(secrets.token_bytes(16)).decode()
    connection.sendall((f'GET {path} HTTP/1.1\r\nHost: {address.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nAuthorization: Bearer {client["key"]}\r\n\r\n').encode())
    source=connection.makefile('rb'); statusline=source.readline().decode(); status=int(statusline.split()[1]); headers={}
    while True:
        line=source.readline()
        if line in [b'\r\n',b'']: break
        k,_,v=line.decode().partition(':');headers[k.strip()]=v.strip()
    events=[]
    if status==101:
        payload=json.dumps(message).encode(); mask=secrets.token_bytes(4); size=len(payload)
        head=bytes([129,128|size]) if size<126 else bytes([129,254])+struct.pack('!H',size)
        connection.sendall(head+mask+bytes(value^mask[i%4] for i,value in enumerate(payload)))
        fragments=[]
        try:
            while len(events)<40:
                raw=source.read(2)
                if len(raw)!=2: break
                op=raw[0]&15; final=bool(raw[0]&128); size=raw[1]&127
                if size==126: size=struct.unpack('!H',source.read(2))[0]
                elif size==127: size=struct.unpack('!Q',source.read(8))[0]
                data=source.read(size)
                if op==8: break
                if op not in [0,1]: continue
                fragments.append(data)
                if not final: continue
                data=b''.join(fragments); fragments=[]
                events.append(data)
                value=parsed(data)
                if isinstance(value,dict) and value.get('type') in ['response.done','response.completed','response.failed','error']:
                    # Keep the terminal burst, including duplicates, before
                    # closing. Stopping at the first terminal races upstream
                    # accounting and conceals frames from the wire comparison.
                    connection.settimeout(1)
        except (socket.timeout,TimeoutError): pass
        # Preserve an incomplete message as evidence instead of hiding it.
        if fragments: events.append(b''.join(fragments))
    else: events=[source.read(int(headers.get('Content-Length','0')))]
    connection.close(); source.close()
    return status,headers,b'\n'.join(events)
def wire_usage(payload):
    values=[]
    whole=parsed(payload)
    frames=[whole] if isinstance(whole,dict) else [parsed(line[5:].strip() if line.startswith(b'data:') else line) for line in payload.splitlines()]
    for frame in frames:
        if not isinstance(frame,dict): continue
        for source in [frame,frame.get('message'),frame.get('response')]:
            if not isinstance(source,dict):continue
            if 'usage' in source: values.append(source['usage'])
            if 'usageMetadata' in source: values.append(source['usageMetadata'])
    return values

def error_response(body):
    values=[parsed(body)]+[parsed(line[5:].strip() if line.startswith(b'data:') else line) for line in body.splitlines()]
    return any(isinstance(value,dict) and (value.get('error') or value.get('type') in ['error','response.failed']) for value in values)

def comparable_stream(body):
    """Keep protocol meaning; ignore generated IDs/time and error request-ID annotations."""
    identities={}
    def normalize(value,key='',error_context=False):
        if isinstance(value,dict):
            is_error=error_context or value.get('type')=='error'
            return {k:normalize(v,k,is_error or k in ['error','last_error']) for k,v in value.items()}
        if isinstance(value,list): return [normalize(v,key,error_context) for v in value]
        if key in ['created','created_at'] and isinstance(value,(int,float)): return '<time>'
        if isinstance(value,str):
            if key in ['id','response_id','item_id','call_id','tool_call_id','stream_id']:
                if value not in identities:identities[value]='<id:'+str(len(identities))+'>'
                return identities[value]
            if key=='model':return value.removesuffix('-reference')
            if key=='message' and error_context:
                # Both gateways record request IDs independently. An appended
                # diagnostic is not an error-semantic difference; never strip
                # user content, error type/code, or text inside the message.
                value=re.sub(r'(?:[ \t]+\(request id: [A-Za-z0-9_.:-]+\))+$','',value)
            return re.sub(r'20\d{21}[A-Za-z0-9]+','<request-id>',value)
        return value
    frames=[]
    for line in body.replace(b'\r\n',b'\n').splitlines():
        if not line:continue
        payload=line[5:].strip() if line.startswith(b'data:') else line
        value=parsed(payload)
        if value is not None:
            frames.append(normalize(value))
        else:
            # Truncated JSON still contains the mock's generated ID. Normalize
            # only that declared dynamic field; preserve the malformed bytes.
            raw=line.decode(errors='replace')
            raw=re.sub(r'("(?:id|response_id|item_id|call_id|tool_call_id|stream_id)"\s*:\s*")((?:fixture_|msg_|resp_)[0-9a-f]{16})(")',lambda m:m[1]+normalize(m[2],'id')+m[3],raw)
            frames.append(raw)
    return frames
def logs(client,request_id):
    if not request_id: return []
    return [row for row in client.get('log_cache',[]) if row.get('request_id')==request_id and row['type'] in [2,5]]

def websocket_logs(client,row):
    # Gorilla's upgrade does not include Gin's request-id header. Match the
    # request-id's nanosecond UTC prefix inside this serial call's time window.
    matched=[]
    model='gpt-realtime' if row['spec']=='realtime_ws' else 'gpt-probe'
    if row['layer']=='main': model+='-reference'
    for log in client.get('log_cache',[]):
        ident=log.get('request_id','')
        if len(ident)<23 or log.get('model_name')!=model or log['type'] not in [2,5]: continue
        stamp=calendar.timegm(time.strptime(ident[:14],'%Y%m%d%H%M%S'))+int(ident[14:23])/1e9
        if row['started_at']<=stamp<=row['finished_at']: matched.append(log)
    return matched
def refresh_logs(client):
    rows=[]
    for page in range(1,100):
        value=api(client,'/api/log/?'+urllib.parse.urlencode({'start_timestamp':STARTED_AT,'end_timestamp':int(time.time())+2,'page_size':100,'p':page}))
        rows.extend(value['items'])
        if len(value['items'])<100:break
    # Offset pages can overlap while asynchronous writes arrive. Deduplicate
    # identical row IDs, never distinct consumption records for a request.
    client['log_cache']=list({row['id']:row for row in rows}.values())

def invoke(client,spec):
    name,_,_,path,body,mode=spec
    if client['name']=='main' and spec[2] in ['openai','claude','gemini','xai'] and (mode in ['sse','ws'] or spec[2]=='claude'):
        body=dict(body)
        for model in PROFILES[spec[2]]['models']:
            if body.get('model')==model: body['model']=model+'-reference'
            path=path.replace('/models/'+model+':','/models/'+model+'-reference:').replace('?model='+model,'?model='+model+'-reference')
    if mode=='ws': return ws_call(client,path,body)
    if mode.endswith('multipart'):
        content,ctype=multipart(body,mode);return request(client['base'],path,content,client['key'],ctype=ctype)
    return request(client['base'],path,body,client['key'],timeout=110 if mode in ['task_sync','task_stream'] else 12)
def record_response(label,spec,case,enabled,status,headers,body):
    record={'layer':label,'spec':spec[0],'format':spec[1],'mode':spec[5],'case':case,'strict':enabled,'status':status,'request_id':next((v for k,v in headers.items() if k.lower()=='x-oneapi-request-id'),None),'wire_usage':wire_usage(body),'response_sha256':hashlib.sha256(body).hexdigest(),'response_bytes':len(body)}
    filename=label+'-'+spec[0]+'-'+case+'-'+str(enabled)+'.bin'; (REPORT/'responses'/filename).write_bytes(body);record['body_file']='responses/'+filename
    value=parsed(body)
    if spec[5].startswith('task'):
        if isinstance(value,dict): ident=value.get('task_id') or value.get('id') or value.get('result')
        else:
            match=re.search(rb'"id"\s*:\s*"(resp_[A-Za-z0-9]+)"',body);ident=match.group(1).decode() if match else None
        if ident: record['task_id']='task_'+ident[5:] if ident.startswith('resp_') else ident
    return record

def audit_records():
    path=ROOT/'data/mock/requests.jsonl'
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

def task_lifecycle(clients,records,failures):
    pending=[row for row in records if row.get('task_id') and row['case'] in ['normal','task_failed','task_pending'] and row['spec']!='midjourney']
    deadline=time.monotonic()+100
    while pending and time.monotonic()<deadline:
        for row in pending[:]:
            client=next(client for client in clients if client['name']==row['layer'])
            status,_,body=request(client['base'],'/v1/tasks/'+row['task_id'],token=client['key'])
            value=parsed(body); row['lifecycle']={'http_status':status,'task':value}
            if not isinstance(value,dict) or value.get('status') not in ['SUCCESS','FAILURE']: continue
            pending.remove(row)
            expected='FAILURE' if row['case']=='task_failed' else 'SUCCESS'
            if value['status']!=expected: failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'unexpected terminal task status','actual':value})
            if value['status']=='SUCCESS':
                path='/v1/tasks/'+row['task_id']+'/artifacts'
                st,_,artifacts=request(client['base'],path,token=client['key']); row['lifecycle']['artifacts']={'status':st,'body':parsed(artifacts)}
                st,_,content=request(client['base'],path+'/video/content',token=client['key'])
                row['lifecycle']['content']={'status':st,'bytes':len(content),'sha256':hashlib.sha256(content).hexdigest()}
                if st!=200 or not content.startswith(b'\0\0\0\x18ftyp'): failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'video artifact download failed','status':st,'body':content[:200].decode(errors='replace')})
                st,_,video=request(client['base'],'/v1/videos/'+row['task_id'],token=client['key'])
                row['lifecycle']['native_video']={'status':st,'body':parsed(video)}
                if st!=200 or not isinstance(parsed(video),dict) or parsed(video).get('status')!='completed': failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'native video query did not report completion'})
                st,_,content=request(client['base'],'/v1/videos/'+row['task_id']+'/content',token=client['key'])
                row['lifecycle']['native_content']={'status':st,'bytes':len(content)}
                st,_,content=request(client['base'],'/v1/videos/'+row['task_id']+'/content',token=client['key'],method='HEAD')
                row['lifecycle']['native_head']={'status':st,'bytes':len(content)}
                if st!=200 or content: failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'video HEAD contract failed'})
                if row['spec'].startswith('task_responses'):
                    st,_,response=request(client['base'],'/v1/responses/resp_'+row['task_id'][5:],token=client['key'])
                    row['lifecycle']['response_retrieval']={'status':st,'body':parsed(response)}
                    if st!=200 or not isinstance(parsed(response),dict) or parsed(response).get('status')!='completed':failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'background response retrieval did not complete'})
        if pending: time.sleep(2)
    for row in pending: failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'task did not reach terminal status within 100 seconds','actual':row.get('lifecycle')})
    pending=[row for row in records if row['spec']=='midjourney' and row['case'] in ['normal','task_failed','task_pending'] and row.get('task_id')]
    deadline=time.monotonic()+100
    while pending and time.monotonic()<deadline:
        for row in pending[:]:
            client=next(client for client in clients if client['name']==row['layer'])
            st,_,body=request(client['base'],'/mj/task/'+row['task_id']+'/fetch',token=client['key'])
            value=parsed(body);row['lifecycle']={'fetch_status':st,'task':value}
            if st!=200 or not isinstance(value,dict) or value.get('status') not in ['SUCCESS','FAILURE']:continue
            pending.remove(row)
            expected='FAILURE' if row['case']=='task_failed' else 'SUCCESS'
            if value['status']!=expected: failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'unexpected Midjourney final state','actual':value})
            st,_,body=request(client['base'],'/mj/task/list-by-condition',{'ids':[row['task_id']]},client['key'])
            row['lifecycle']['batch']={'status':st,'body':parsed(body)}
            if st!=200 or not isinstance(parsed(body),list) or not any(item.get('id')==row['task_id'] and item.get('status')==expected for item in parsed(body)):
                failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'Midjourney task query failed','status':st,'body':body[:200].decode(errors='replace')})
        if pending:time.sleep(2)
    for row in pending:failures.append({'spec':row['spec'],'case':row['case'],'layer':row['layer'],'error':'Midjourney did not reach terminal status within 100 seconds','actual':row.get('lifecycle')})

def validate_records(records,clients,failures):
    audits=[row for row in audit_records() if row['time']>=STARTED_AT]
    (REPORT/'mock-audit.json').write_text(json.dumps(audits,indent=2))
    checks={'mock_hops':0,'http_rejections':0,'provider_rejections':0,'strict_protocol_rejections':0,'normal_usage':0,'ledger_pairs':0,'protocol_disabled_parity':0,'protocol_disabled_wire_parity':0,'protocol_disabled_ledger_parity':0,'nonnegative_accounting':0,'single_accounting':0}
    for client in clients: (REPORT/(client['name']+'-ledger.json')).write_text(json.dumps(client['log_cache'],indent=2))
    for row in records:
        def fail(message,**extra): failures.append({'spec':row['spec'],'case':row['case'],'strict':row['strict'],'layer':row['layer'],'error':message,**extra})
        row['mock_evidence']=[audit for audit in audits if row['started_at']<=audit['time']<=row['finished_at'] and audit['case']==row['case'] and (audit.get('method')=='POST' or row['mode']=='ws')]
        if not row['mock_evidence']: fail('request did not reach the configured main -> mock channel')
        else: checks['mock_hops']+=1
        if row['case'].startswith('http_'):
            rejected=error_response((REPORT/row['body_file']).read_bytes())
            if row['status'] in [200,201,202,101] and not rejected: fail('HTTP upstream rejection became a successful business response')
            else: checks['http_rejections']+=1
        body=(REPORT/row['body_file']).read_bytes()
        row['response_class']='http_error' if row['status']>=400 else 'provider_error' if error_response(body) else 'empty' if not body else 'payload'
        if row['case']=='normal' and error_response(body): fail('normal response contains a provider/protocol error')
        if row['case']=='provider_error':
            if row['status']<400 and not error_response(body): fail('provider error disappeared from the response')
            else: checks['provider_rejections']+=1
        if row['spec'].startswith('claude_') and row['mode']=='sse' and row['layer']=='candidate' and row['strict'] and row['case'] in ['open_block','wrong_sequence','max_tokens_open_block','foreign_protocol']:
            if row['status']<400 and not error_response(body): fail('strict Claude accepted invalid upstream protocol')
            else: checks['strict_protocol_rejections']+=1
        for log in row['logs']:
            if any(log.get(key,0)<0 for key in ['quota','prompt_tokens','completion_tokens']): fail('negative billing value')
            else: checks['nonnegative_accounting']+=1
        charges=[log for log in row['logs'] if log['type']==2]
        if len(charges)>1: fail('duplicate consumption settlement')
        else: checks['single_accounting']+=1
        if row['case']=='normal' and row['wire_usage'] and not row['mode'].startswith('task') and row['spec'] not in ['moderation','audio_transcription','audio_translation','gemini_embedding','gemini_batch_embedding']:
            value=row['wire_usage'][-1]
            if isinstance(value,dict):
                p=value.get('prompt_tokens',value.get('input_tokens',value.get('promptTokenCount')))
                c=value.get('completion_tokens',value.get('output_tokens',value.get('candidatesTokenCount',0)))
                expected_prompt=135 if row['spec'].startswith('claude_to_') else 101
                if p!=expected_prompt or c not in [0,17]: fail('normal returned usage differs from the fixture',usage=value)
                elif not charges: fail('normal token response has no consumption ledger')
                elif (charges[0]['prompt_tokens'],charges[0]['completion_tokens'])!=(101,c): fail('wire usage differs from persisted usage after protocol normalization',wire=[p,c],ledger=[charges[0]['prompt_tokens'],charges[0]['completion_tokens']])
                else: checks['normal_usage']+=1
        if row['layer']=='candidate':
            upstream_id=next((log.get('upstream_request_id') for log in row['logs'] if log.get('upstream_request_id')),None)
            upstream_logs=logs(clients[0],upstream_id) if upstream_id else websocket_logs(clients[0],row) if row['mode']=='ws' else []
            if not upstream_id and upstream_logs: upstream_id=upstream_logs[0]['request_id']
            row['actual_main_request_id']=upstream_id; row['actual_main_logs']=upstream_logs
            # Token counts and cache evidence are compared across the actual two
            # hops. A gateway may deliberately estimate after a malformed stream.
            if charges and upstream_logs and row['case']=='normal' and row['mode']!='task':
                upstream_charge=next((log for log in upstream_logs if log['type']==2),None)
                if upstream_charge:
                    fields=['prompt_tokens','completion_tokens','quota']
                    if any(charges[0][key]!=upstream_charge[key] for key in fields): fail('actual main/candidate settlement mismatch',main={key:upstream_charge[key] for key in fields},candidate={key:charges[0][key] for key in fields})
                    else: checks['ledger_pairs']+=1
            if row['mode'] in ['sse','ws'] and not row['strict']:
                direct=next((other for other in records if other['layer']=='main' and other['spec']==row['spec'] and other['case']==row['case'] and not other['strict']),None)
                if direct is None:
                    fail('main reference request failed; parity cannot be evaluated')
                    continue
                if row['status']!=direct['status'] or row['wire_usage']!=direct['wire_usage']: fail('disabled protocol policy differs from equal-depth main wire status/usage',main_status=direct['status'],main_usage=direct['wire_usage'],candidate_usage=row['wire_usage'])
                else: checks['protocol_disabled_parity']+=1
                if comparable_stream((REPORT/row['body_file']).read_bytes())!=comparable_stream((REPORT/direct['body_file']).read_bytes()):
                    fail('disabled protocol policy differs from equal-depth main ordered events')
                else: checks['protocol_disabled_wire_parity']+=1
                # Compare equal-depth persisted rows for every case, including
                # malformed streams. Inner-hop estimates may legitimately differ.
                # Preserve row multiplicity and missing/error rows; ignore IDs.
                fields=['type','prompt_tokens','completion_tokens','quota']
                main_accounting=sorted(tuple(log.get(key,0) for key in fields) for log in direct['logs'] if log['type'] in [2,5])
                candidate_accounting=sorted(tuple(log.get(key,0) for key in fields) for log in row['logs'] if log['type'] in [2,5])
                if main_accounting!=candidate_accounting:
                    fail('disabled protocol policy differs from equal-depth main persisted accounting',fields=fields,main=main_accounting,candidate=candidate_accounting)
                else: checks['protocol_disabled_ledger_parity']+=1
    return checks

if __name__ == '__main__':
    args=parser.parse_args()
    if args.only:
        unknown=set(args.only.split(','))-{spec[0] for spec in SPECS}
        if unknown:raise SystemExit('Unknown test specs: '+','.join(sorted(unknown)))
    REPORT=ROOT/'reports'/('smoke-'+time.strftime('%Y%m%d-%H%M%S') if args.smoke else 'matrix-'+time.strftime('%Y%m%d-%H%M%S'))
    if args.resume_report:
        REPORT=args.resume_report.resolve()
        if not REPORT.is_relative_to((ROOT/'reports').resolve()) or (REPORT/'result.json').exists():raise SystemExit('Resume requires an unfinished report inside the fixture reports directory')
    else:
        (REPORT/'responses').mkdir(parents=True)
    STARTED_AT=int(time.time())-2
    deployment=json.loads((ROOT/'manifests/deployment.json').read_text())
    latest=subprocess.check_output(['git','ls-remote','https://github.com/QuantumNous/new-api.git','refs/heads/main'],text=True).split()[0]
    if latest!=deployment['main_revision']:raise SystemExit('Official main advanced: build and deploy the newest main before testing')
    latest_checked_at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())
    main=setup('main'); candidate=setup('candidate'); configure([main,candidate])
    # Task submissions reserve real fixture quota. Exhaustion can otherwise
    # masquerade as a protocol rejection halfway through a repeated matrix.
    for client in [main,candidate]:
        if api(client,'/api/user/self')['quota'] < 100_000_000:
            raise SystemExit('Insufficient isolated fixture quota in '+client['name']+'; fund the test account before running the matrix')
    if args.cache_expression:
        expression='tier("standard", p * 4 + cr * 0.2 + cc * 5 + cc1h * 8 + c * 20)'
        for client in [main,candidate]:
            api(client,'/api/option/',{'key':'billing_setting.billing_expr','value':json.dumps({model:expression for model in ['claude-probe','claude-probe-reference']})},'PUT')
            api(client,'/api/option/',{'key':'billing_setting.billing_mode','value':json.dumps({model:'tiered_expr' for model in ['claude-probe','claude-probe-reference']})},'PUT')
    records=[]; failures=[]
    if args.resume_report:
        progress=json.loads((REPORT/'progress.json').read_text())
        if progress.get('deployment') and progress['deployment']!=deployment:raise SystemExit('Deployment changed since the interrupted matrix')
        records,failures=progress['records'],progress['failures']
        STARTED_AT=int(min(row['started_at'] for row in records))-2
    inventory=json.loads((ROOT/'manifests/20261112/protocol-inventory.json').read_text())
    if inventory['revision']!=deployment['main_revision']: raise SystemExit('Protocol inventory revision differs from the deployed main')
    missing_formats=set(inventory['relay_formats'])-{spec[1] for spec in SPECS}
    if missing_formats: raise SystemExit('Main added protocols without fixtures: '+','.join(sorted(missing_formats)))
    covered_host_operations={'openai_responses.create','openai_responses.retrieve','openai_video.create','openai_video.retrieve','openai_video.content','openai_image.generate','openai_image.edit'}
    missing_operations=set(inventory.get('host_operations',[]))-covered_host_operations
    if missing_operations:raise SystemExit('Main added host protocol operations without fixtures: '+','.join(sorted(missing_operations)))
    cases_json=['normal','zero_usage','missing_usage','usage_string','usage_negative','usage_overflow','usage_array','empty','invalid_json','truncated_json','null_json','array_json','html','provider_error','http_400','http_401','http_403','http_429','http_500','http_502','http_503','wrong_content_type','short_body']
    cases_sse=cases_json+['cache_usage','reasoning_usage','missing_terminal','duplicate_terminal','wrong_sequence','open_block','tool','truncated_tool','unknown_event','foreign_protocol','bytewise_crlf','no_final_newline','read_reset','idle_timeout','slow_headers','heartbeat_timeout']
    for spec in ([] if args.resume_report else SPECS):
        if args.only and spec[0] not in args.only.split(','):continue
        mode=spec[5]
        cases=['normal'] if args.smoke else cases_sse if mode=='sse' else ['normal','zero_usage','missing_usage','invalid_json','provider_error','missing_terminal','duplicate_terminal','read_reset','http_429','http_500'] if mode=='ws' else ['normal','empty','short_body','http_429','http_500','http_503'] if mode=='binary' else ['normal','http_400','http_429','http_500','invalid_json','task_failed','task_pending'] if mode=='task' else cases_json
        cases=list(cases)
        if mode in ['task_sync','task_stream']: cases=['normal']
        if not args.smoke and spec[2]=='claude' and mode=='sse': cases=cases+['usage_regression','max_tokens_open_block']
        if not args.smoke and spec[0] in ['gemini_json','gemini_sse']:cases=cases+['blocked']
        if not args.smoke and spec[0] in ['alpha_search','deepseek_search','deepseek_fetch']:cases=cases+['empty_results']
        if not args.smoke and spec[0]=='realtime_ws':cases=cases+['zero_total_positive_details']
        if not args.smoke and mode in ['json','sse']:
            if spec[2]=='claude': cases += ['finish_'+reason for reason in ['end_turn','stop_sequence','max_tokens','tool_use','pause_turn','refusal','model_context_window_exceeded','future_reason']]
            if spec[0].startswith(('chat_','completion_')): cases += ['finish_'+reason for reason in ['stop','length','content_filter','tool_calls','function_call','future_reason']]
            if spec[0] in ['gemini_json','gemini_sse']: cases += ['finish_'+reason for reason in ['STOP','MAX_TOKENS','SAFETY','RECITATION','OTHER','BLOCKLIST','PROHIBITED_CONTENT','SPII','MALFORMED_FUNCTION_CALL','UNEXPECTED_TOOL_CALL','IMAGE_SAFETY','IMAGE_PROHIBITED_CONTENT','IMAGE_RECITATION','NO_IMAGE','LANGUAGE','FINISH_REASON_UNSPECIFIED','FUTURE_REASON']]
            if spec[0] in ['responses_json','responses_sse']: cases += ['status_incomplete','status_failed','status_cancelled']
        if args.cases: cases=args.cases.split(',')
        for case in cases:
            set_case(main,spec[2],case)
            for enabled in ([True] if args.fixed_policy else [False,True]):
                strict(candidate,enabled)
                pair=[]
                for client in [main,candidate]:
                    try:
                        started_at=time.time()
                        status,headers,body=invoke(client,spec)
                        row=record_response(client['name'],spec,case,enabled,status,headers,body)
                        row.update(started_at=started_at,finished_at=time.time())
                        if case=='normal' and not (200<=status<300 or mode=='ws' and status==101): raise AssertionError('normal request rejected: '+body.decode(errors='replace')[:500])
                        if case=='normal' and not body: raise AssertionError('normal response empty')
                        pair.append(row);records.append(row)
                    except Exception as exc:
                        failures.append({'spec':spec[0],'case':case,'strict':enabled,'layer':client['name'],'error':str(exc)})
                if len(pair)==2:
                    pair[1]['comparison']={'main_status':pair[0]['status'],'main_wire_usage':pair[0]['wire_usage'],'candidate_wire_usage':pair[1]['wire_usage']}
        print(json.dumps({'completed':spec[0],'cases':len(cases),'failures':len(failures)}),flush=True)
        (REPORT/'progress.json').write_text(json.dumps({'records':records,'failures':failures,'deployment':deployment},indent=2))
    print(json.dumps({'phase':'task_lifecycle','requests':len(records)}),flush=True)
    task_lifecycle([main,candidate],records,failures)
    time.sleep(3)
    ledger_deadline=time.monotonic()+30
    while True:
        for client in [main,candidate]: refresh_logs(client)
        # A normal request early in a fast matrix may already be durable while
        # later cache/error-usage requests are still in the five-second batch.
        # Wait for every successful usage-bearing response, not just normal.
        missing_usage_rows=[row for row in records if 200<=row['status']<300 and row['wire_usage'] and row['mode']!='ws' and not logs(main if row['layer']=='main' else candidate,row['request_id'])]
        if not missing_usage_rows or time.monotonic()>=ledger_deadline:break
        time.sleep(1)
    for client in [main,candidate]:
        for row in records:
            if row['layer']!=client['name']:continue
            row['logs']=logs(client,row['request_id']) if row['request_id'] else websocket_logs(client,row) if row['mode']=='ws' else []
            if row['mode']=='ws' and row['logs']: row['request_id']=row['logs'][0]['request_id']; row['request_id_evidence']='unique nanosecond request-id timestamp within serial WebSocket call'
            if any(log['quota']<0 for log in row['logs']):failures.append({'spec':row['spec'],'case':row['case'],'layer':client['name'],'error':'negative charge'})
    checks=validate_records(records,[main,candidate],failures)
    for profile in PROFILES: set_case(main,profile,'normal')
    strict(candidate,True)
    formats=sorted({spec[1] for spec in SPECS if not args.only or spec[0] in args.only.split(',')})
    result={'status':'passed' if not failures else 'failed','formats':formats,'protocol_inventory':inventory,'latest_checked_at':latest_checked_at,'specs':len({row['spec'] for row in records}),'requests':len(records),'checks':checks,'failures':failures,'records':records,'deployment':deployment}
    result['evidence_review']=review(result)
    failures.extend(result['evidence_review']['failures'])
    result['status']='passed' if not failures else 'failed'
    (REPORT/'result.json').write_text(json.dumps(result,indent=2))
    print(json.dumps({'status':result['status'],'formats':len(formats),'specs':result['specs'],'requests':len(records),'failure_count':len(failures),'report':str(REPORT),'failures':failures[:10]}),flush=True)
    raise SystemExit(bool(failures))
