"""Deterministic, authenticated upstream fixtures for real main-channel tests.

No model provider is contacted. Unknown routes fail closed; each response is
recorded with its request id, actual wire bytes and declared usage evidence.
"""
import base64
import copy
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import re
import secrets
import socket
import struct
import threading
import time
import urllib.parse
import wave
from email.parser import BytesParser

ROOT=Path(os.environ.get('MOCK_DATA','/data'))
ROOT.mkdir(parents=True,exist_ok=True)
KEY=os.environ['MOCK_API_KEY']
LOCK=threading.Lock()
TASK_FILE=ROOT/'tasks.json'
TASKS=json.loads(TASK_FILE.read_text()) if TASK_FILE.exists() else {}
PNG=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP1sAAAAASUVORK5CYII=')
audio=io.BytesIO()
with wave.open(audio,'wb') as wav:
    wav.setnchannels(1); wav.setsampwidth(2); wav.setframerate(8000); wav.writeframes(b'\0\0'*800)
WAV=audio.getvalue()
CASES=['normal','zero_usage','missing_usage','cache_usage','reasoning_usage','usage_string','usage_negative','usage_overflow','usage_array','usage_regression','empty','invalid_json','truncated_json','null_json','array_json','html','provider_error','http_400','http_401','http_403','http_429','http_500','http_502','http_503','wrong_content_type','missing_terminal','duplicate_terminal','wrong_sequence','open_block','tool','truncated_tool','unknown_event','foreign_protocol','bytewise_crlf','no_final_newline','read_reset','short_body','idle_timeout','slow_headers','heartbeat_timeout','cancelled','blocked','empty_results','task_failed','task_pending']
CASES+=['max_tokens_open_block','zero_total_positive_details']
CASES+=['realtime_audio','realtime_tail','realtime_error_after_usage','realtime_two_rounds','realtime_minimum_rounds','realtime_delayed_round','realtime_large_frame']
CACHE_CASES=['cache_split_exceeds_total','cache_split_corrected','cache_split_explicit_zero','cache_total_missing','cache_zero_total_positive_split','cache_negative_split','cache_normal_read_only','cache_normal_total_only']
CACHE_CASES+=['cache_sidecar_inflated','cache_sidecar_correct','cache_read_negative']
CASES+=CACHE_CASES

def encoded(value): return json.dumps(value,separators=(',',':'),ensure_ascii=False).encode()
def usage(case,kind):
    p,c=101,0 if kind in ['embedding','rerank'] else 17
    if case=='zero_usage': p,c=0,0
    if case=='usage_string': c='17'
    if case=='usage_negative': c=-1
    if case=='usage_overflow': c=9223372036854775808
    if case=='usage_array': return []
    cached=0 if case=='zero_usage' else 23
    if kind=='claude':
        if case in CACHE_CASES:
            value={'input_tokens':4,'output_tokens':3426,'cache_read_input_tokens':1130,'cache_creation_input_tokens':336,'cache_creation':{'ephemeral_5m_input_tokens':336,'ephemeral_1h_input_tokens':0}}
            if case=='cache_split_exceeds_total': value['cache_creation']['ephemeral_5m_input_tokens']=1466
            if case=='cache_split_corrected': value.pop('cache_creation')
            if case=='cache_split_explicit_zero': value.update(cache_creation_input_tokens=0,cache_read_input_tokens=1466,cache_creation={'ephemeral_5m_input_tokens':0,'ephemeral_1h_input_tokens':0})
            if case=='cache_total_missing': value.pop('cache_creation_input_tokens')
            if case=='cache_zero_total_positive_split': value['cache_creation_input_tokens']=0
            if case=='cache_negative_split': value['cache_creation']['ephemeral_5m_input_tokens']=-1
            if case=='cache_normal_read_only': value.update(cache_creation_input_tokens=0,cache_creation={'ephemeral_5m_input_tokens':0,'ephemeral_1h_input_tokens':0})
            if case=='cache_normal_total_only': value.pop('cache_creation')
            if case=='cache_read_negative': value['cache_read_input_tokens']=-1
            if case in ['cache_sidecar_inflated','cache_sidecar_correct']:
                authoritative=copy.deepcopy(value)
                if case=='cache_sidecar_inflated': authoritative['cache_creation']['ephemeral_5m_input_tokens']=1466
                else: value['cache_creation']['ephemeral_5m_input_tokens']=1466
                value['billing_usage']={'source':'claude_messages','semantic':'anthropic','claude_usage':authoritative}
            return value
        return {'input_tokens':p,'output_tokens':c,'cache_read_input_tokens':cached,'cache_creation_input_tokens':0 if case=='zero_usage' else 11,'cache_creation':{'ephemeral_5m_input_tokens':0 if case=='zero_usage' else 7,'ephemeral_1h_input_tokens':0 if case=='zero_usage' else 4}}
    if kind=='gemini':
        return {'promptTokenCount':p,'candidatesTokenCount':c,'totalTokenCount':p+c if isinstance(c,int) else 118,'cachedContentTokenCount':cached,'thoughtsTokenCount':3 if case=='reasoning_usage' else 0}
    if kind=='realtime':
        if case=='realtime_audio':
            return {'input_tokens':101,'output_tokens':17,'total_tokens':118,'input_token_details':{'text_tokens':91,'audio_tokens':10,'cached_tokens':23},'output_token_details':{'text_tokens':12,'audio_tokens':5}}
        return {'input_tokens':p,'output_tokens':c,'total_tokens':0 if case=='zero_total_positive_details' else p+c if isinstance(c,int) else 118,'input_token_details':{'text_tokens':p,'audio_tokens':0,'cached_tokens':cached},'output_token_details':{'text_tokens':c,'audio_tokens':0}}
    if kind in ['responses','compact','realtime','image','audio_transcription','audio_translation']:
        return {'input_tokens':p,'output_tokens':c,'total_tokens':p+c if isinstance(c,int) else 118,'input_tokens_details':{'cached_tokens':cached},'output_tokens_details':{'reasoning_tokens':3 if case=='reasoning_usage' else 0}}
    return {'prompt_tokens':p,'completion_tokens':c,'total_tokens':p+c if isinstance(c,int) else 118,'prompt_tokens_details':{'cached_tokens':cached},'completion_tokens_details':{'reasoning_tokens':3 if case=='reasoning_usage' else 0}}

def json_response(kind,case,model,request,ident):
    text='fixture output'
    u=usage(case,kind)
    if kind=='claude':
        result={'id':ident,'type':'message','role':'assistant','model':model,'content':[{'type':'text','text':text}],'stop_reason':'end_turn','usage':u}
    elif kind=='responses':
        result={'id':ident,'object':'response','created_at':1,'model':model,'status':'completed','output':[{'id':'item_fixture','type':'message','role':'assistant','status':'completed','content':[{'type':'output_text','text':text,'annotations':[]}]}],'usage':u}
    elif kind=='compact': result={'id':ident,'object':'response.compaction','created_at':1,'output':[{'id':'cmp_fixture','type':'compaction','encrypted_content':'synthetic_compaction'}],'usage':u}
    elif kind=='gemini': result={'candidates':[{'index':0,'content':{'role':'model','parts':[{'text':text}]},'finishReason':'STOP'}],'usageMetadata':u,'modelVersion':model}
    elif kind=='gemini_embedding': result={'embedding':{'values':[0.1,0.2,0.3]},'embeddings':[{'values':[0.1,0.2,0.3]}],'usageMetadata':usage(case,'gemini')}
    elif kind=='embedding': result={'object':'list','model':model,'data':[{'object':'embedding','index':0,'embedding':[0.1,0.2,0.3]}],'usage':u}
    elif kind=='rerank': result={'id':ident,'results':[{'index':0,'relevance_score':0.9,'document':{'text':'fixture document'}}],'usage':u}
    elif kind=='moderation': result={'id':ident,'model':model,'results':[{'flagged':False,'categories':{'violence':False},'category_scores':{'violence':0.0}}],'usage':u}
    elif kind=='image': result={'created':1,'data':[{'b64_json':base64.b64encode(PNG).decode(),'revised_prompt':'fixture image'}],'usage':u}
    elif kind in ['audio_transcription','audio_translation']: result={'text':text,'duration':0.1,'usage':u}
    elif kind in ['alpha','deepseek_search']: result={'id':ident,'results':[] if case=='empty_results' else [{'url':'https://example.invalid/fixture','title':'fixture','snippet':'fixture result'}]}
    elif kind=='deepseek_fetch': result={'url':'https://example.invalid/fixture','content':'' if case=='empty_results' else 'fixture page'}
    elif kind=='video':
        taskid=request.get('_task_id') or 'video_'+secrets.token_hex(6)
        state='failed' if case=='task_failed' else 'cancelled' if case=='cancelled' else 'in_progress' if case=='task_pending' else 'completed'
        result={'id':taskid,'object':'video','model':model or 'sora-2','status':state,'progress':100 if state=='completed' else 0,'created_at':1,'completed_at':2,'seconds':'4','size':'720x1280','error':{'code':'fixture_failed','message':'Synthetic task failure'} if state=='failed' else None}
        with LOCK:
            TASKS[taskid]=[case,result,time.time()]
            TASK_FILE.write_text(json.dumps(TASKS))
    elif kind=='mj':
        taskid=request.get('_task_id') or 'mj_'+secrets.token_hex(6)
        now=int(time.time()*1000)
        result={'code':1,'description':'Submit success','result':taskid,'properties':{},'id':taskid,'action':'IMAGINE','status':'FAILURE' if case=='task_failed' else 'IN_PROGRESS' if case=='task_pending' else 'SUCCESS','progress':'0%' if case=='task_pending' else '100%','imageUrl':'http://mock:8000/asset/image.png','prompt':'fixture','buttons':[],'submitTime':now,'startTime':now,'finishTime':0 if case=='task_pending' else now,'failReason':'Synthetic task failure' if case=='task_failed' else ''}
        with LOCK:
            TASKS[taskid]=[case,result,time.time()]
            TASK_FILE.write_text(json.dumps(TASKS))
    elif kind=='completion': result={'id':ident,'object':'text_completion','created':1,'model':model,'choices':[{'index':0,'text':text,'finish_reason':'stop'}],'usage':u}
    else: result={'id':ident,'object':'chat.completion','created':1,'model':model,'choices':[{'index':0,'message':{'role':'assistant','content':text},'finish_reason':'stop'}],'usage':u}
    if case=='missing_usage':
        result.pop('usage',None); result.pop('usageMetadata',None)
    if case=='blocked' and kind=='gemini': result={'candidates':[],'promptFeedback':{'blockReason':'SAFETY'},'usageMetadata':u}
    if case.startswith('finish_'):
        reason=case.removeprefix('finish_')
        if kind=='claude': result['stop_reason']=reason
        elif kind=='gemini':
            for candidate in result.get('candidates',[]): candidate['finishReason']=reason
        elif kind in ['chat','completion']:
            for choice in result.get('choices',[]): choice['finish_reason']=reason
    if case.startswith('status_') and kind=='responses':
        result['status']=case.removeprefix('status_')
        if result['status']=='incomplete': result['incomplete_details']={'reason':'max_output_tokens'}
        if result['status']=='failed': result['error']={'code':'server_error','message':'Synthetic response failure'}
    return result

def stream_events(kind,case,model,ident):
    u=usage(case,kind)
    if kind=='claude':
        initial=copy.deepcopy(u)
        if isinstance(initial,dict): initial['output_tokens']=0
        if case in ['cache_sidecar_inflated','cache_sidecar_correct']: initial['billing_usage']['claude_usage']['output_tokens']=0
        if case in ['cache_split_corrected','cache_split_explicit_zero']:
            initial.update(cache_read_input_tokens=0,cache_creation_input_tokens=1466,cache_creation={'ephemeral_5m_input_tokens':1466,'ephemeral_1h_input_tokens':0})
        events=[{'type':'message_start','message':{'id':ident,'type':'message','role':'assistant','model':model,'content':[],'usage':initial}}, {'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}}, {'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'fixture output'}}, {'type':'content_block_stop','index':0}, {'type':'message_delta','delta':{'stop_reason':'end_turn'},'usage':u}, {'type':'message_stop'}]
        if case in ['open_block','wrong_sequence','max_tokens_open_block']: events.pop(3)
        if case=='max_tokens_open_block':
            events[-2]['delta']['stop_reason']='max_tokens'
            events[-2]['usage']['output_tokens']=32000
        if case in ['tool','truncated_tool']:
            events[1]['content_block']={'type':'tool_use','id':'tool_fixture','name':'lookup','input':{}}
            events[2]['delta']={'type':'input_json_delta','partial_json':'{"location":"Tokyo"}' if case=='tool' else '{"location":'}
            events[4]['delta']['stop_reason']='tool_use' if case=='tool' else 'max_tokens'
        if case=='usage_regression': events.insert(-1,{'type':'message_delta','delta':{'stop_reason':'end_turn'},'usage':{'output_tokens':1}})
        if case.startswith('finish_'): events[-2]['delta']['stop_reason']=case.removeprefix('finish_')
    elif kind=='responses':
        response=json_response(kind,case,model,{},ident)
        created=copy.deepcopy(response); created.update(status='in_progress',output=[]); created.pop('usage',None)
        item=response['output'][0]
        events=[{'type':'response.created','response':created,'sequence_number':0},{'type':'response.in_progress','response':created,'sequence_number':1},{'type':'response.output_item.added','output_index':0,'item':{**item,'status':'in_progress','content':[]},'sequence_number':2},{'type':'response.content_part.added','output_index':0,'content_index':0,'item_id':'item_fixture','part':{'type':'output_text','text':'','annotations':[]},'sequence_number':3},{'type':'response.output_text.delta','delta':'fixture output','output_index':0,'content_index':0,'item_id':'item_fixture','sequence_number':4},{'type':'response.output_text.done','text':'fixture output','output_index':0,'content_index':0,'item_id':'item_fixture','sequence_number':5},{'type':'response.content_part.done','part':item['content'][0],'output_index':0,'content_index':0,'item_id':'item_fixture','sequence_number':6},{'type':'response.output_item.done','item':item,'output_index':0,'sequence_number':7},{'type':'response.completed','response':response,'sequence_number':8}]
        if case.startswith('status_'): events[-1]['type']='response.'+case.removeprefix('status_')
    elif kind=='gemini':
        events=[{'candidates':[{'index':0,'content':{'role':'model','parts':[{'text':'fixture output'}]}}]},json_response(kind,case,model,{},ident)]
    elif kind=='image':
        events=[{'type':'image_generation.partial_image','partial_image_index':0,'b64_json':base64.b64encode(PNG).decode()},{'type':'image_generation.completed','b64_json':base64.b64encode(PNG).decode(),'usage':u}]
    else:
        base={'id':ident,'object':'chat.completion.chunk','created':1,'model':model}
        events=[{**base,'choices':[{'index':0,'delta':{'role':'assistant','content':''},'finish_reason':None}]},{**base,'choices':[{'index':0,'delta':{'content':'fixture output'},'finish_reason':None}]},{**base,'choices':[{'index':0,'delta':{},'finish_reason':'stop'}]},{**base,'choices':[],'usage':u},'[DONE]']
        if kind=='completion':
            for event in events:
                if isinstance(event,dict):
                    event['object']='text_completion'
                    for choice in event['choices']: choice['text']=choice.pop('delta',{}).get('content','')
        if case.startswith('finish_'):
            for event in events:
                if isinstance(event,dict):
                    for choice in event.get('choices',[]):
                        if choice.get('finish_reason') is not None: choice['finish_reason']=case.removeprefix('finish_')
    if case=='missing_usage':
        for event in events:
            if isinstance(event,dict):
                event.pop('usage',None); event.pop('usageMetadata',None)
                for key in ['message','response']:
                    if isinstance(event.get(key),dict): event[key].pop('usage',None)
    if case in ['missing_terminal','read_reset','short_body','idle_timeout','heartbeat_timeout']: events=events[:-1]
    if case=='duplicate_terminal': events.append(copy.deepcopy(events[-1]))
    if case=='wrong_sequence': events.insert(0,copy.deepcopy(events[min(2,len(events)-1)]))
    if case=='unknown_event': events.insert(1,{'type':'future_event','fixture':True})
    if case=='foreign_protocol': return [{'type':'response.completed','response':{'status':'completed','usage':{'input_tokens':999,'output_tokens':999}}}] if kind!='responses' else [{'type':'message_stop'}]
    if case=='provider_error': events=events[:2]+[{'type':'error','error':{'type':'overloaded_error','message':'Synthetic upstream failure'}}]
    return events

def protocol(path,ws=False):
    if path.endswith('/messages'): return 'claude'
    if '/responses/compact' in path: return 'compact'
    if '/responses' in path: return 'responses'
    if '/realtime' in path: return 'realtime'
    if ':embedContent' in path or ':batchEmbedContents' in path or '/engines/' in path: return 'gemini_embedding'
    if '/models/' in path: return 'gemini'
    if '/embeddings' in path: return 'embedding'
    if '/rerank' in path: return 'rerank'
    if '/images/' in path or path.endswith('/edits'): return 'image'
    if '/audio/speech' in path: return 'audio_speech'
    if '/audio/transcriptions' in path: return 'audio_transcription'
    if '/audio/translations' in path: return 'audio_translation'
    if '/moderations' in path: return 'moderation'
    if '/alpha/search' in path: return 'alpha'
    if '/tools/search' in path: return 'deepseek_search'
    if '/tools/fetch' in path: return 'deepseek_fetch'
    if '/videos' in path: return 'video'
    if '/mj/' in path: return 'mj'
    if '/chat/completions' in path: return 'chat'
    if path.endswith('/completions'): return 'completion'
    return None

def frame_read(stream):
    head=stream.read(2)
    if len(head)!=2: raise EOFError()
    op=head[0]&15; size=head[1]&127; masked=head[1]&128
    if size==126: size=struct.unpack('!H',stream.read(2))[0]
    elif size==127: size=struct.unpack('!Q',stream.read(8))[0]
    if size>4*1024*1024: raise ValueError('fixture frame too large')
    mask=stream.read(4) if masked else b''
    data=stream.read(size)
    if mask: data=bytes(value^mask[index%4] for index,value in enumerate(data))
    return op,data
def frame_write(stream,data,op=1):
    size=len(data)
    head=bytes([128|op,size]) if size<126 else bytes([128|op,126])+struct.pack('!H',size) if size<65536 else bytes([128|op,127])+struct.pack('!Q',size)
    stream.write(head+data); stream.flush()

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args): pass
    def authorized(self):
        supplied=self.headers.get('Authorization','').removeprefix('Bearer ') or self.headers.get('x-api-key','') or self.headers.get('x-goog-api-key','') or self.headers.get('mj-api-secret','') or urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('key',[''])[0]
        return secrets.compare_digest(supplied,KEY) or secrets.compare_digest(self.headers.get('x-mock-key',''),KEY)
    def send_bytes(self,status,body,ctype='application/json',extra=None):
        self.send_response(status); self.send_header('Content-Type',ctype); self.send_header('Content-Length',str(len(body))); self.send_header('Connection','close')
        for key,value in (extra or {}).items(): self.send_header(key,value)
        self.end_headers()
        if self.command!='HEAD': self.wfile.write(body)
        self.wfile.flush(); self.close_connection=True
    def do_HEAD(self): self.handle_request()
    def do_GET(self): self.handle_request()
    def do_POST(self): self.handle_request()
    def handle_request(self):
        if self.path=='/health': self.send_bytes(200,encoded({'ok':True,'cases':CASES})); return
        if not self.authorized(): self.send_bytes(401,encoded({'error':{'type':'authentication_error','message':'Fixture authentication required'}})); return
        if self.path.startswith('/audit'):
            content=(ROOT/'requests.jsonl').read_bytes() if (ROOT/'requests.jsonl').exists() else b''
            self.send_bytes(200,content,'application/x-ndjson'); return
        body=self.rfile.read(min(int(self.headers.get('Content-Length','0')),8*1024*1024))
        request={}
        if 'multipart/form-data' in self.headers.get('Content-Type',''):
            parsed=BytesParser().parsebytes(('Content-Type: '+self.headers['Content-Type']+'\r\nMIME-Version: 1.0\r\n\r\n').encode()+body)
            for part in parsed.walk():
                field=part.get_param('name',header='content-disposition')
                if field and not part.get_filename(): request[field]=part.get_payload(decode=True).decode(errors='replace')
        else:
            try: request=json.loads(body) if body else {}
            except ValueError: pass
        if not isinstance(request,dict): request={}
        url=urllib.parse.urlsplit(self.path); path=url.path
        match=re.match(r'/mock/([^/]+)(/.*)',path)
        case,path=(match.group(1),match.group(2)) if match else ('normal',path)
        kind=protocol(path,self.headers.get('Upgrade','').lower()=='websocket')
        model=request.get('model') or urllib.parse.parse_qs(url.query).get('model',[''])[0]
        if '/models/' in path: model=path.split('/models/',1)[1].split(':',1)[0]
        ident=('msg_' if kind=='claude' else 'resp_' if kind=='responses' else 'fixture_')+secrets.token_hex(8)
        record={'id':ident,'time':time.time(),'case':case,'protocol':kind,'method':self.command,'path':path,'model':model,'request_sha256':hashlib.sha256(body).hexdigest(),'request_bytes':len(body)}
        if kind is None:
            if path.startswith('/asset/'):
                self.send_bytes(200,PNG if path.endswith('.png') else WAV if path.endswith('.wav') else b'\0\0\0\x18ftypmp42synthetic-video','image/png' if path.endswith('.png') else 'audio/wav' if path.endswith('.wav') else 'video/mp4'); return
            self.send_bytes(404,encoded({'error':{'type':'fixture_route_missing','message':'No fixture for this route'}})); return
        try:
            if case.startswith('http_'):
                status=int(case[5:]); record['status']=status
                content=encoded({'type':'error','error':{'type':'api_error','message':'Synthetic HTTP rejection'}})
                record.update(response_sha256=hashlib.sha256(content).hexdigest(),response_bytes=len(content))
                (ROOT/'responses').mkdir(exist_ok=True); (ROOT/'responses'/(ident+'.bin')).write_bytes(content)
                self.send_bytes(status,content,extra={'Request-Id':ident}); return
            if case=='slow_headers': time.sleep(1)
            if kind=='mj' and path.endswith('/list-by-condition'):
                with LOCK:
                    for taskid in request.get('ids',[]):
                        saved=TASKS.get(taskid)
                        if saved and saved[0]=='task_pending' and len(saved)>2 and time.time()-saved[2]>45: saved[1].update(status='SUCCESS',progress='100%',finishTime=int(time.time()*1000))
                    TASK_FILE.write_text(json.dumps(TASKS))
                    results=[TASKS[taskid][1] for taskid in request.get('ids',[]) if taskid in TASKS]
                content=encoded(results)
                record.update(status=200,response_sha256=hashlib.sha256(content).hexdigest(),response_bytes=len(content))
                self.send_bytes(200,content,extra={'Request-Id':ident}); return
            if self.headers.get('Upgrade','').lower()=='websocket':
                self.websocket(kind,case,model,ident,record); return
            is_stream=request.get('stream') is True or 'streamGenerateContent' in path
            if kind=='audio_speech':
                content=WAV
                if case=='empty': content=b''
                if case in ['read_reset','short_body']:
                    self.send_response(200); self.send_header('Content-Type','audio/wav'); self.send_header('Content-Length',str(len(content)+100)); self.end_headers(); self.wfile.write(content[:len(content)//2]); self.wfile.flush(); self.close_connection=True
                else: self.send_bytes(200,content,'audio/wav')
                record.update(status=200,response_sha256=hashlib.sha256(content).hexdigest(),response_bytes=len(content)); return
            if kind in ['mj','video'] and self.command=='GET':
                taskid=path.split('/videos/',1)[1].split('/')[0] if kind=='video' else path.split('/task/',1)[1].split('/')[0]
                request['_task_id']=taskid
                with LOCK: saved=TASKS.get(taskid)
                if saved:
                    case=saved[0]
                    if case=='task_pending' and len(saved)>2 and time.time()-saved[2]>45: case='normal'
                    # Returning a poll must not reset the pending fixture clock.
                    request['_created_at']=saved[2] if len(saved)>2 else time.time()
                else:
                    self.send_bytes(404,encoded({'error':{'message':'Unknown fixture task'}})); return
                if path.endswith('/content'):
                    self.send_bytes(200,b'\0\0\0\x18ftypmp42synthetic-video','video/mp4'); return
            if is_stream:
                events=stream_events(kind,case,model,ident)
                content=b''.join((('event: '+event['type']+'\n') if isinstance(event,dict) and isinstance(event.get('type'),str) else '').encode()+b'data: '+(encoded(event) if not isinstance(event,str) else event.encode())+b'\n\n' for event in events)
                ctype='text/event-stream'
            else:
                result=json_response(kind,case,model,request,ident)
                if kind in ['mj','video'] and request.get('_created_at'):
                    with LOCK:
                        TASKS[request['_task_id']][2]=request['_created_at']; TASK_FILE.write_text(json.dumps(TASKS))
                content=encoded(result); ctype='application/json'
            if case=='empty': content=b''
            elif case=='invalid_json': content=(content[:content.find(b'\n\n')+2] if is_stream else b'')+(b'data: {broken}\n\n' if is_stream else b'{broken}')
            elif case=='truncated_json': content=content[:max(1,len(content)//2)]
            elif case=='null_json': content=b'data: null\n\n' if is_stream else b'null'
            elif case=='array_json': content=b'data: []\n\n' if is_stream else b'[]'
            elif case=='html': content=b'<html>Synthetic unavailable</html>'; ctype='text/html'
            elif case=='provider_error' and not is_stream: content=encoded({'type':'error','error':{'type':'overloaded_error','message':'Synthetic upstream failure'}})
            if case=='wrong_content_type': ctype='text/plain'
            if case=='bytewise_crlf' and is_stream: content=content.replace(b'\n',b'\r\n')
            if case=='no_final_newline': content=content.rstrip(b'\r\n')
            evidence=[]
            for item in content.splitlines() if is_stream else [content]:
                if is_stream and not item.startswith(b'data:'): continue
                try: value=json.loads(item[5:].strip() if is_stream else item)
                except ValueError: continue
                if not isinstance(value,dict): continue
                for source in [value,value.get('message'),value.get('response')]:
                    if not isinstance(source,dict): continue
                    if 'usage' in source: evidence.append(source['usage'])
                    if 'usageMetadata' in source: evidence.append(source['usageMetadata'])
            (ROOT/'responses').mkdir(exist_ok=True)
            (ROOT/'responses'/(ident+'.bin')).write_bytes(content)
            record.update(status=200,response_sha256=hashlib.sha256(content).hexdigest(),response_bytes=len(content),wire_usage_evidence=evidence)
            self.send_response(200); self.send_header('Content-Type',ctype); self.send_header('Request-Id',ident); self.send_header('Connection','close')
            if case=='short_body': self.send_header('Content-Length',str(len(content)+100))
            self.end_headers()
            step=1 if case=='bytewise_crlf' else 113
            for offset in range(0,len(content),step): self.wfile.write(content[offset:offset+step]); self.wfile.flush()
            if case in ['idle_timeout','heartbeat_timeout']:
                for _ in range(5):
                    time.sleep(1)
                    if case=='heartbeat_timeout': self.wfile.write(b': fixture heartbeat\n\n'); self.wfile.flush()
            if case=='read_reset': self.connection.setsockopt(socket.SOL_SOCKET,socket.SO_LINGER,struct.pack('ii',1,0))
            self.close_connection=True
        except (BrokenPipeError,ConnectionResetError,EOFError,socket.timeout): record['disconnected']=True
        finally:
            with LOCK:
                with (ROOT/'requests.jsonl').open('a',encoding='utf-8') as log: log.write(json.dumps(record,ensure_ascii=True)+'\n')
    def websocket(self,kind,case,model,ident,record):
        self.send_response(101); self.send_header('Upgrade','websocket'); self.send_header('Connection','Upgrade'); self.send_header('Sec-WebSocket-Accept',base64.b64encode(hashlib.sha1((self.headers['Sec-WebSocket-Key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()); self.end_headers(); self.wfile.flush()
        self.connection.settimeout(15)
        record['status']=101
        record['wire_usage_evidence']=[]
        if kind=='realtime': frame_write(self.wfile,encoded({'type':'session.created','session':{'id':'sess_fixture','model':model}}))
        for _ in range(20):
            op,data=frame_read(self.rfile)
            if op==8: frame_write(self.wfile,data,8); break
            if op==9: frame_write(self.wfile,data,10); continue
            if op!=1: continue
            message=json.loads(data)
            if message.get('type')=='session.update': frame_write(self.wfile,encoded({'type':'session.updated','session':message.get('session',{})})); continue
            if message.get('type')!='response.create': continue
            if case=='empty': break
            if case=='invalid_json': frame_write(self.wfile,b'{broken}'); break
            if case=='provider_error': frame_write(self.wfile,encoded({'type':'error','error':{'type':'server_error','code':'fixture_failure','message':'Synthetic WebSocket failure'}})); break
            payload={'id':ident,'object':'realtime.response','status':'completed','output':[],'usage':usage(case,'realtime')}
            if kind=='realtime':
                events=[{'type':'response.created','response':{'id':ident,'status':'in_progress'}},{'type':'response.output_text.delta','response_id':ident,'delta':'fixture output'},{'type':'response.done','response':payload}]
                if case=='realtime_tail':
                    events=[{'type':'response.audio_transcript.delta','response_id':ident,'delta':'fixture output'}]
                if case=='realtime_error_after_usage':
                    events.append({'type':'error','error':{'type':'server_error','code':'fixture_failure','message':'Synthetic error after usage'}})
                if case in ['realtime_two_rounds','realtime_minimum_rounds']:
                    second=copy.deepcopy(events[-1]); second['response']['id']=ident+'_second'
                    if case=='realtime_minimum_rounds':
                        events[-1]['response']['usage']=usage('zero_usage','realtime')
                        second['response']['usage']=usage('zero_usage','realtime')
                    events.append(second)
            else: events=stream_events('responses',case,model,ident)
            if case=='missing_terminal': events=events[:-1]
            if case=='duplicate_terminal': events.append(copy.deepcopy(events[-1]))
            if case=='missing_usage':
                for event in events:
                    if isinstance(event.get('response'),dict): event['response'].pop('usage',None)
            for event in events:
                if case=='realtime_delayed_round' and event.get('type')=='response.done':
                    record['terminal_delay_seconds']=4
                    time.sleep(4)
                if isinstance(event,dict) and isinstance(event.get('response'),dict) and 'usage' in event['response']: record['wire_usage_evidence'].append(event['response']['usage'])
                data=encoded(event)
                if case=='realtime_large_frame' and event.get('type')=='response.created':
                    data+=b' '*((200<<20)+1-len(data))
                    record['large_frame_bytes']=len(data)
                frame_write(self.wfile,data)
            if case in ['read_reset','missing_terminal','realtime_tail','realtime_error_after_usage']: break
        self.close_connection=True

class Server(http.server.ThreadingHTTPServer):
    daemon_threads=True
    request_queue_size=64

if __name__=='__main__':
    print(json.dumps({'listening':8000,'fixture_cases':len(CASES),'external_providers':False}),flush=True)
    Server(('0.0.0.0',8000),Handler).serve_forever()
