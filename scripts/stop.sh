#!/bin/sh
set -eu

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$root"
python3 scripts/camera_bridge.py stop
# Keep Deployments, Secrets, and PVCs so up.sh can resume with existing data.
sh scripts/kube.sh scale deployment --all --replicas=0
sh scripts/kube.sh wait --for=delete pod --all --timeout=45s
echo 'furnace-trace stopped; database, catalog, and configuration volumes were retained.'
