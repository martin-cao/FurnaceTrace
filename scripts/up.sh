#!/bin/sh
set -eu

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$root"
python3 scripts/init_secrets.py
uv run --with pyyaml python scripts/render_deploy.py
sh scripts/kube.sh create namespace furnace-trace --dry-run=client -o yaml | sh scripts/kube.sh apply -f -
sh scripts/kube.sh create secret generic furnace-trace-secrets --from-env-file=.env --dry-run=client -o yaml | sh scripts/kube.sh apply -f -
sh scripts/kube.sh apply -k deploy/k8s
python3 scripts/camera_bridge.py start
sh scripts/kube.sh rollout status deployment/fuxa --timeout=180s
python3 scripts/import_fuxa.py

# The old MES stub had no data volume; the PostgreSQL-backed service replaces it.
sh scripts/kube.sh delete deployment/mes-sim service/mes-sim --ignore-not-found

for service in core qr-display; do
    sh scripts/kube.sh rollout status "deployment/$service" --timeout=180s
done
echo 'NodePorts deployed: workbench 31080, FUXA 31081, QR display 31082. Use a reachable cluster node address.'
