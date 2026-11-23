"""Classify captured failures without changing their original pass/fail status.

The primary comparison continues to preserve every event, usage and error. This
secondary report identifies the fork's pre-existing request-ID/error presentation
differences and defects also observed in the unmodified main reference.
"""
import argparse
import ast
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

parser=argparse.ArgumentParser()
parser.add_argument('report',type=Path)
args=parser.parse_args()
raw=args.report.read_bytes()
report=json.loads(raw)
source=Path(__file__).with_name('matrix.py')
tree=ast.parse(source.read_text(encoding='utf-8'))
namespace={'json':json,'re':re}
functions=ast.Module(body=[node for node in tree.body if isinstance(node,ast.FunctionDef)
                          and node.name in ('parsed','comparable_stream')],type_ignores=[])
exec(compile(functions,str(source),'exec'),namespace)
compare=namespace['comparable_stream']


def presentation(value,status,path=()):
    if isinstance(value,dict):return {key:presentation(item,status,path+(key,)) for key,item in value.items()}
    if isinstance(value,list):return [presentation(item,status,path) for item in value]
    if isinstance(value,str):
        if path and path[-1]=='message':
            return re.sub(r'\s*\(request id: <request-id>\)','',value)
        if path[-2:]==('error','type') and value=='<nil>':
            return {400:'invalid_request_error',401:'authentication_error',403:'permission_error',429:'rate_limit_error'}.get(status,'api_error')
    return value


records={(row['layer'],row['spec'],row['case'],row['strict']):row for row in report['records']}
classified=[]
for failure in report['failures']:
    finding=dict(failure)
    category='unresolved'
    if failure.get('layer')=='main':
        category='official_main_failure'
    elif failure['error']=='disabled protocol policy differs from equal-depth main ordered events':
        key=(failure['spec'],failure['case'],failure['strict'])
        candidate=compare((args.report.parent/records[('candidate',*key)]['body_file']).read_bytes())
        main=compare((args.report.parent/records[('main',*key)]['body_file']).read_bytes())
        if presentation(candidate,records[('candidate',*key)]['status'])==presentation(main,records[('main',*key)]['status']):category='existing_error_presentation'
    elif failure.get('layer')=='candidate' and failure['error'] in ['negative billing value','wire and ledger cache-read differ','provider error disappeared from the response']:
        peer=next((item for item in report['failures'] if item.get('layer')=='main'
                   and all(item.get(key)==failure.get(key) for key in ['spec','case','strict','error'])),None)
        if peer is not None:category='also_observed_in_official_main'
    finding['classification']=category
    classified.append(finding)

result={'original_status':report['status'],'report_sha256':hashlib.sha256(raw).hexdigest(),
        'counts':dict(Counter(item['classification'] for item in classified)),
        'findings':classified,
        'note':'Classification does not waive failures or claim byte-for-byte main compatibility.'}
output=args.report.with_name('failure-classification.json')
output.write_text(json.dumps(result,indent=2),encoding='utf-8')
print(json.dumps({key:value for key,value in result.items() if key!='findings'}))
