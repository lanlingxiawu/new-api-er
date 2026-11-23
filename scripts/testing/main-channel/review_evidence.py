"""Additional assertions over immutable, captured gateway evidence.

Does not send requests, read credentials, or modify the original report.
"""
import argparse
import hashlib
import json
from pathlib import Path


def review(report):
    failures=[]
    checks={'cache_read':0,'claude_cache_write':0,'native_video_download':0,'native_video_head':0,'cache_split_consistency':0}
    excluded={'moderation','gemini_embedding','gemini_batch_embedding','realtime_ws','audio_transcription','audio_translation'}
    for row in report['records']:
        for charge in row.get('logs', []):
            if charge.get('type') != 2: continue
            details=json.loads(charge.get('other') or '{}')
            if any((details.get(key,0) or 0)<0 for key in ['cache_tokens','cache_creation_tokens','cache_creation_tokens_5m','cache_creation_tokens_1h']):
                failures.append({'layer':row['layer'],'spec':row['spec'],'case':row['case'],'strict':row.get('strict'),'error':'negative cache token accounting','request_id':charge.get('request_id')})
            if details.get('usage_semantic')=='anthropic' or details.get('claude'):
                total=details.get('cache_creation_tokens',0) or 0
                split=(details.get('cache_creation_tokens_5m',0) or 0)+(details.get('cache_creation_tokens_1h',0) or 0)
                if split>total and row['case']!='cache_total_missing':
                    failures.append({'layer':row['layer'],'spec':row['spec'],'case':row['case'],'strict':row.get('strict'),'error':'cache write split exceeds total in settled ledger','total':total,'split':split,'request_id':charge.get('request_id')})
                else: checks['cache_split_consistency']+=1
        if row['case']=='normal' and row['wire_usage'] and row['spec'] not in excluded and not row['mode'].startswith('task'):
            usage=row['wire_usage'][-1]
            charge=next((log for log in row['logs'] if log['type']==2),None)
            if not charge or not isinstance(usage,dict): continue
            details=json.loads(charge.get('other') or '{}')
            cached=usage.get('cache_read_input_tokens',usage.get('cachedContentTokenCount'))
            if cached is None:
                token_details=(usage.get('input_tokens_details') or {}) if row['format'] in ['openai_responses','openai_responses_compaction'] else (usage.get('prompt_tokens_details') or usage.get('input_tokens_details') or {})
                cached=token_details.get('cached_tokens')
            if cached is not None:
                if details.get('cache_tokens')!=cached: failures.append({'layer':row['layer'],'spec':row['spec'],'case':row['case'],'strict':row['strict'],'error':'wire and ledger cache-read differ','wire':cached,'ledger':details.get('cache_tokens')})
                else: checks['cache_read']+=1
            if row['format']=='claude':
                creation=usage.get('cache_creation',{})
                expected={'cache_creation_tokens':usage.get('cache_creation_input_tokens',0),'cache_creation_tokens_5m':creation.get('ephemeral_5m_input_tokens',0),'cache_creation_tokens_1h':creation.get('ephemeral_1h_input_tokens',0)}
                if any(details.get(key)!=value for key,value in expected.items()): failures.append({'layer':row['layer'],'spec':row['spec'],'error':'Claude cache-write lifetime accounting differs','expected':expected})
                else: checks['claude_cache_write']+=1
        lifecycle=row.get('lifecycle',{})
        content=lifecycle.get('native_content')
        if content:
            if content['status']!=200 or content['bytes']<=0: failures.append({'layer':row['layer'],'spec':row['spec'],'case':row['case'],'error':'native video content download failed'})
            else: checks['native_video_download']+=1
        head=lifecycle.get('native_head')
        if head:
            if head['status']!=200 or head['bytes']!=0: failures.append({'layer':row['layer'],'spec':row['spec'],'case':row['case'],'error':'native video HEAD failed'})
            else: checks['native_video_head']+=1
    return {'checks':checks,'failures':failures,'status':'passed' if not failures else 'failed'}


if __name__=='__main__':
    parser=argparse.ArgumentParser(); parser.add_argument('report',type=Path); args=parser.parse_args()
    raw=args.report.read_bytes(); result=review(json.loads(raw))
    result['report_sha256']=hashlib.sha256(raw).hexdigest()
    target=args.report.with_name('evidence-review.json')
    target.write_text(json.dumps(result,indent=2))
    print(json.dumps({**result,'output':str(target)}))
    raise SystemExit(bool(result['failures']))
