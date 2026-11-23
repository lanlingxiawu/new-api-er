"""Exercise all 18 policy combinations through deployed admin APIs and channels.

Run only after the main matrix finishes: this changes the isolated fixture root
user and Claude channel temporarily, and restores their policy in finally.
"""
import itertools
import json
import time
import matrix as m

main=m.setup('main')
candidate=m.setup('candidate')
for client in [main,candidate]:
    client['channels']={item['name'].removeprefix('main-fixture-'):item['id']
                        for item in m.api(client,'/api/channel/?page_size=100')['items']}
user=m.api(candidate,'/api/user/self')
original_user=m.api(candidate,'/api/user/'+str(user['id']))
channel=m.api(candidate,'/api/channel/'+str(candidate['channels']['claude']))
original_settings=json.loads(channel.get('setting') or '{}')
options=m.api(candidate,'/api/option/')
original_global=next(item['value'] for item in options if item['key']=='stream_protocol_validation_setting.enabled')
if original_global not in ['true','false'] or original_user.get('claude_stream_validation') is not None or original_settings.get('claude_stream_validation') is not None:
    raise SystemExit('Fixture policies have legacy/unconfigured values; refuse to migrate them as a side effect of this test')
records=[]
failures=[]
started=int(time.time())-1
deployment=json.loads((m.ROOT/'manifests/deployment.json').read_text())
latest=m.subprocess.check_output(['git','ls-remote','https://github.com/QuantumNous/new-api.git','refs/heads/main'],text=True).split()[0]
if latest!=deployment['main_revision']:raise SystemExit('Official main advanced; refresh before testing')


def user_policy(value):
    payload={key:original_user[key] for key in ['id','username','display_name','email','group'] if key in original_user}
    payload['stream_protocol_validation']=value
    m.api(candidate,'/api/user/',payload,'PUT')


def channel_policy(value,restore=False):
    payload=m.api(candidate,'/api/channel/'+str(channel['id']))
    settings=dict(original_settings)
    # Omission preserves an existing override; explicit null restores inherit.
    settings['stream_protocol_validation']=original_settings.get('stream_protocol_validation') if restore else value
    payload['setting']=json.dumps(settings)
    payload.pop('key',None);payload.pop('status',None)
    m.api(candidate,'/api/channel/',payload,'PUT')


try:
    m.set_case(main,'claude','max_tokens_open_block')
    spec=next(spec for spec in m.SPECS if spec[0]=='claude_sse')
    for global_value,channel_value,user_value in itertools.product([False,True],[None,False,True],[None,False,True]):
        m.strict(candidate,global_value)
        channel_policy(channel_value)
        user_policy(user_value)
        expected=user_value if user_value is not None else channel_value if channel_value is not None else global_value
        source='user' if user_value is not None else 'channel' if channel_value is not None else 'global'
        status,headers,body=m.invoke(candidate,spec)
        request_id=next(value for key,value in headers.items() if key.lower()=='x-oneapi-request-id')
        row={'global':global_value,'channel':channel_value,'user':user_value,'expected':expected,'source':source,
             'request_id':request_id,'http_status':status,'wire_usage':m.wire_usage(body)}
        deadline=time.monotonic()+8
        logs=[]
        while time.monotonic()<deadline:
            logs=m.api(candidate,f'/api/log/?request_id={request_id}&start_timestamp={started}&end_timestamp={int(time.time())+1}&page_size=100')['items']
            logs=[log for log in logs if log['type'] in [2,5]]
            if logs:break
            time.sleep(.2)
        row['logs']=logs
        if not logs:failures.append({**row,'error':'missing request log'})
        else:
            other=json.loads(logs[0].get('other') or '{}')
            policy=other.get('admin_info',{}).get('stream_protocol_validation')
            row['observed_policy']=policy
            if policy!={'enabled':expected,'source':source,'config_origin':'canonical'}:
                failures.append({'request_id':request_id,'error':'wrong effective policy','expected':[expected,source],'actual':policy})
        if m.error_response(body)!=expected:failures.append({'request_id':request_id,'error':'protocol outcome did not follow policy'})
        if not expected and (not logs or logs[0]['completion_tokens']!=32000):
            failures.append({'request_id':request_id,'error':'main final usage not preserved'})
        records.append(row)
finally:
    user_policy(original_user.get('stream_protocol_validation'))
    channel_policy(None,restore=True)
    m.strict(candidate,original_global=='true')
    m.set_case(main,'claude','normal')

restored_user=m.api(candidate,'/api/user/'+str(user['id']))
restored_channel=m.api(candidate,'/api/channel/'+str(channel['id']))
restored_settings=json.loads(restored_channel.get('setting') or '{}')
if restored_user.get('stream_protocol_validation')!=original_user.get('stream_protocol_validation') or restored_settings.get('stream_protocol_validation')!=original_settings.get('stream_protocol_validation'):
    failures.append({'error':'fixture policy restoration failed'})
result={'status':'passed' if not failures else 'failed','cases':len(records),'failures':failures,'records':records,'deployment':deployment}
path=m.ROOT/'reports'/('policy-precedence-'+time.strftime('%Y%m%d-%H%M%S')+'.json')
path.write_text(json.dumps(result,indent=2))
print(json.dumps({'status':result['status'],'cases':len(records),'failures':failures,'report':str(path)}))
raise SystemExit(bool(failures))
