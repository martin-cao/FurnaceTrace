"""Initialize only this project's FUXA, preserving an existing project."""
from pathlib import Path
import subprocess
import sys
import datetime
import json
root=Path(__file__).resolve().parents[1]
kube=["sh",str(root/"scripts/kube.sh"),"exec","-i","deployment/fuxa","--","node","-e"]
if "--replace-demo-project" in sys.argv[1:]:
    read="fetch('http://127.0.0.1:1881/api/project').then(async r=>{if(!r.ok)throw Error('project export failed');console.log(JSON.stringify(await r.json()))}).catch(()=>process.exit(1))"
    current=subprocess.run(kube+[read],capture_output=True,check=True).stdout
    project=json.loads(current)
    directory=root/".local/backups"
    directory.mkdir(parents=True,exist_ok=True)
    stamp=datetime.datetime.now().strftime('%Y%m%dT%H%M%S%f')
    backup=directory/f"fuxa-project-{stamp}.json"
    backup.write_text(json.dumps(project,ensure_ascii=False,indent=2)+"\n")
    backup.chmod(0o600)
    print("Saved existing FUXA project:",backup.relative_to(root))
code=(root/"scripts/seed_fuxa.cjs").read_text()
subprocess.run(kube+[code,"--",*sys.argv[1:]],input=(root/"deploy/fuxa/project.json").read_bytes(),check=True)
