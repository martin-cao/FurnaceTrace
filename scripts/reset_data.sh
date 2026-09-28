#!/bin/sh
set -eu

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$root"
if [ "$#" -ne 1 ] || [ "$1" != '--delete-demo-history' ]; then
    echo 'This deletes project demo records and basket reservations. Run with --delete-demo-history explicitly.' >&2
    exit 1
fi

active=$(sh scripts/kube.sh exec deployment/postgres -- psql -U postgres -d furnace -tAc "SELECT count(*) FROM core.cycles WHERE status NOT IN ('COMPLETED','ABORTED')")
if [ "$active" != 0 ]; then
    echo 'Resolve or explicitly reset the active cycle before deleting history.' >&2
    exit 1
fi

mkdir -p .local/backups
umask 077
backup=".local/backups/demo-$(date -u '+%Y%m%dT%H%M%SZ').sql"
sh scripts/kube.sh exec deployment/postgres -- pg_dump -U postgres -d furnace --schema=core --schema=notify > "$backup"
sh scripts/kube.sh scale deployment/core deployment/notifier --replicas=0
sh scripts/kube.sh wait --for=delete pod -l app=core --timeout=40s
sh scripts/kube.sh wait --for=delete pod -l app=notifier --timeout=40s
sh scripts/kube.sh exec deployment/postgres -- psql -v ON_ERROR_STOP=1 -U postgres -d furnace -c "TRUNCATE core.events,core.commands,core.occupancy,core.alarms,core.idempotency,core.audit,core.cycles,notify.jobs,notify.events,notify.closed_rule_alarms; DELETE FROM core.outbox WHERE kind IN ('notification','scan-session','cancel-reminders'); UPDATE core.alarm_rule_states SET active_alarm_id='',notified=false,next_reminder_at=NULL; UPDATE core.catalog_revision SET version=version+1,rules_version=rules_version+1 WHERE id=1"
sh scripts/kube.sh scale deployment/core deployment/notifier --replicas=1
echo "Demo history reset; recovery backup: $backup"
