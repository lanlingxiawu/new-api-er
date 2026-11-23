"""Combine immutable protocol reports, replacing rerun samples by exact key."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
from review_evidence import review


def sample_key(row):
    return (row['spec'],row['case'],row.get('strict',True),row['layer'])


if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('reports',nargs='+',type=Path)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--inventory',type=Path)
    args=parser.parse_args()
    samples={}; source_reports=[]; reports=[]; revision=None; fingerprint=None
    for path in args.reports:
        raw=path.read_bytes(); report=json.loads(raw)
        current=report['deployment']['main_revision']
        if revision and current!=revision:raise SystemExit('Cannot aggregate different main revisions')
        builds=(current,report['deployment']['main_sha256'],report['deployment']['candidate_sha256'])
        if fingerprint and builds!=fingerprint:raise SystemExit('Cannot aggregate different gateway builds')
        fingerprint=builds
        revision=current; reports.append((path,report))
        source_reports.append({'path':str(path),'sha256':hashlib.sha256(raw).hexdigest(),'status':report['status'],'requests':report['requests']})
        for original in report['records']:
            row={**original,'source_report':str(path),'body_file':str(path.parent/original['body_file'])}
            samples[sample_key(row)]=row
    failures=[]
    # Evidence review is rerun over the final selected samples. Its original
    # results remain in their immutable source reports.
    review_errors={'wire and ledger cache-read differ','Claude cache-write lifetime accounting differs','native video content download failed','native video HEAD failed'}
    for path,report in reports:
        for failure in report['failures']:
            if failure['error'] in review_errors:continue
            key=sample_key(failure)
            if key in samples and samples[key]['source_report']==str(path):failures.append({**failure,'source_report':str(path)})
    result={'deployment':reports[-1][1]['deployment'],'main_revision':revision,'source_reports':source_reports,'records':list(samples.values()),'failures':failures}
    result['evidence_review']=review(result); failures.extend(result['evidence_review']['failures'])
    result.update(status='failed' if failures else 'passed',formats=sorted({row['format'] for row in samples.values()}),specs=len({row['spec'] for row in samples.values()}),requests=len(samples),normal_requests=sum(row['case']=='normal' for row in samples.values()))
    result['failure_summary']=dict(Counter(failure['error'] for failure in failures))
    result['checks']={'mock_hops':sum(bool(row.get('mock_evidence')) for row in samples.values()),'single_accounting':sum(sum(log['type']==2 for log in row['logs'])<=1 for row in samples.values())}
    parity=[row for row in samples.values() if row['layer']=='candidate' and row['spec'].startswith('claude_') and row['mode']=='sse' and not row['strict']]
    result['checks']['claude_disabled_parity']=sum(row['comparison']['main_status']==row['status'] and row['comparison']['main_wire_usage']==row['wire_usage'] for row in parity)
    result['checks']['claude_disabled_samples']=len(parity)
    if args.inventory:
        inventory=json.loads(args.inventory.read_text())
        if inventory['revision']!=revision:raise SystemExit('Inventory revision differs from report builds')
        result['protocol_inventory']=inventory
        if set(inventory['relay_formats'])!=set(result['formats']):raise SystemExit('Aggregate does not cover every main protocol')
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2))
    print(json.dumps({key:result[key] for key in ['status','main_revision','formats','specs','requests','normal_requests','checks','failure_summary','evidence_review']}|{'failure_count':len(failures),'output':str(args.output)}))
    raise SystemExit(bool(failures))
