"""Build only when frontend inputs changed, then embed the static assets in Go."""
from pathlib import Path
import shutil
import subprocess
root=Path(__file__).resolve().parents[1]
web=root/"web"
index=web/"dist/index.html"
inputs=[root/"api/openapi.yaml",web/"package.json",web/"package-lock.json",web/"vite.config.ts",*web.glob("*.html"),*list((web/"src").rglob("*"))]
if not index.exists() or any(p.is_file() and p.stat().st_mtime>index.stat().st_mtime for p in inputs):
    subprocess.run(["npm","run","build","--prefix",str(web)],check=True)
shutil.copytree(web/"dist",root/"internal/webui/dist",dirs_exist_ok=True)
print("Static frontend embedded")
