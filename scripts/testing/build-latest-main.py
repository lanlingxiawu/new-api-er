"""Build the current official main; never reuse a historical test baseline.

The isolated checkout may contain generated, ignored build artifacts only.
Credentials are not accepted or stored by this script.
"""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time

ROOT=Path(__file__).resolve().parents[2]
OFFICIAL='https://github.com/QuantumNous/new-api.git'
parser=argparse.ArgumentParser()
parser.add_argument('--checkout',type=Path,default=ROOT.parent/(ROOT.name+'-official-main'))
parser.add_argument('--output',type=Path,default=ROOT/'_local/latest-main-build')
parser.add_argument('--head-check-container', help='Optional local runner for GitHub HEAD checks when host TLS is unavailable')
args=parser.parse_args()
checkout=args.checkout.resolve(); output=args.output.resolve(); output.mkdir(parents=True,exist_ok=True)

def run(command,cwd=ROOT,env=None):
    return subprocess.check_output(command,cwd=cwd,env=env,text=True,encoding='utf-8').strip()
def remote_git(command):
    for attempt in range(5):
        try:
            return run(command)
        except subprocess.CalledProcessError:
            if attempt==4: raise
            time.sleep(2**attempt)

def latest():
    command=['git','-c','http.version=HTTP/1.1','ls-remote',OFFICIAL,'refs/heads/main']
    if args.head_check_container:
        command=['docker','exec',args.head_check_container]+command
    return remote_git(command).split()[0]
while True:
    revision=latest()
    remote_git(['git','fetch',OFFICIAL,'main'])
    if not checkout.exists(): run(['git','worktree','add','--detach',str(checkout),revision])
    if run(['git','status','--porcelain'],checkout): raise SystemExit('Official checkout has source changes; refusing to overwrite it')
    run(['git','checkout','--detach',revision],checkout)
    for name,command in [('install',['bun','install','--frozen-lockfile']),('frontend',['bun','run','build'])]:
        with (output/(name+'.log')).open('w',encoding='utf-8') as log:
            subprocess.run(command,cwd=checkout/'web',stdout=log,stderr=subprocess.STDOUT,check=True)
    compiler=Path(run(['go','env','GOTOOLDIR'])).parents[2]/'bin'/('go.exe' if os.name=='nt' else 'go')
    env=os.environ.copy()
    env.update({'GOTOOLCHAIN':'local','GOWORK':'off','GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0','GOEXPERIMENT':'greenteagc'})
    pgo_modules=re.findall(r'^\s*(github.com/(?:MoeForge|Calcium-Ion)/moejs)\s', (checkout/'go.mod').read_text(encoding='utf-8'), re.MULTILINE)
    if len(pgo_modules)!=1: raise SystemExit('Cannot identify the official moejs PGO module')
    run([str(compiler),'mod','download',pgo_modules[0]],checkout,env)
    pgo=run([str(compiler),'list','-m','-f','{{.Dir}}',pgo_modules[0]],checkout,env)
    with (output/'backend.log').open('w',encoding='utf-8') as log:
        subprocess.run([str(compiler),'build','-pgo='+pgo+'/default.pgo','-ldflags','-s -w -X github.com/QuantumNous/new-api/common.Version=official-main-'+revision[:12],'-o',str(output/'new-api-main'),'.'],cwd=checkout,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
    # A .git worktree file inside another repository can make Go stamp the
    # enclosing fork revision. Keep the default checkout outside that tree and
    # reject contradictory build metadata instead of trusting the version label.
    build_info=run([str(compiler),'version','-m',str(output/'new-api-main')],checkout,env)
    vcs_revision=re.search(r'vcs.revision=(\w+)',build_info)
    if vcs_revision and vcs_revision.group(1)!=revision:
        raise SystemExit('Binary VCS revision differs from official checkout; rebuild outside the fork directory')
    (output/'binary-build-info.txt').write_text(build_info,encoding='utf-8')
    if latest()!=revision:
        print('Official main advanced during build; rebuilding newest commit',flush=True)
        continue
    binary=output/'new-api-main'
    with binary.open('rb') as source,gzip.open(output/'new-api-main.gz','wb',compresslevel=6) as target: shutil.copyfileobj(source,target)
    result={'official_remote':OFFICIAL,'revision':revision,'latest_checked_at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'sha256':hashlib.file_digest(binary.open('rb'),'sha256').hexdigest(),'source_clean':not bool(run(['git','status','--porcelain'],checkout)),'bytes':binary.stat().st_size}
    (output/'build-manifest.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
    formats=(checkout/'relaykit/types/relay_format.go').read_text(encoding='utf-8')
    inventory={'revision':revision,'relay_formats':sorted(re.findall(r'RelayFormat\w+\s+(?:RelayFormat\s+)?=\s*"([^"]+)"',formats)),'source_sha256':hashlib.sha256(formats.encode()).hexdigest()}
    routing=(checkout/'pkg/jsplugin/routing.go').read_text(encoding='utf-8')
    constants=dict(re.findall(r'const\s+(Protocol\w+)\s*=\s*"([^"]+)"',routing))
    host_protocols=[]; host_operations=[]; current=None
    for line in routing.splitlines():
        definition=re.search(r'\{Name: ("[^"]+"|Protocol\w+), Operations:',line)
        if definition:
            token=definition.group(1);current=token.strip('"') if token.startswith('"') else constants[token];host_protocols.append(current)
        operation=re.search(r'\{Name: "([^"]+)", Methods:.*Path: "([^"]+)"',line)
        if operation and current:host_operations.append(current+'.'+operation.group(1))
    if not inventory['relay_formats'] or not host_protocols:raise SystemExit('Cannot discover main protocol registry; update the inventory parser')
    inventory.update(host_protocols=host_protocols,host_operations=host_operations,host_source_sha256=hashlib.sha256(routing.encode()).hexdigest())
    (output/'protocol-inventory.json').write_text(json.dumps(inventory,indent=2),encoding='utf-8')
    print(json.dumps(result),flush=True)
    break
