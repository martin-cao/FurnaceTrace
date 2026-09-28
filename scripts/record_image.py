"""Remember built image references for the generated deployment."""
import json
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[1]
path=root/".local/image-tags.json"
path.parent.mkdir(exist_ok=True)
tags=json.loads(path.read_text()) if path.exists() else {}
tags[sys.argv[1]]=sys.argv[2]
path.write_text(json.dumps(tags,indent=2)+"\n")
