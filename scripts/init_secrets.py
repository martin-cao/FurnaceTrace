"""Create local demo credentials once; do not print their values."""
from pathlib import Path
import secrets
import shutil
root=Path(__file__).resolve().parents[1]
path=root/".env"
legacy=root/".local/secrets.env"
if not path.exists() and legacy.exists():
    shutil.copyfile(legacy,path)
    path.chmod(0o600)
if not path.exists():
    core=secrets.token_hex(20); notify=secrets.token_hex(20)
    values={"PG_PASSWORD":secrets.token_hex(20),"CORE_DB_PASSWORD":core,"NOTIFY_DB_PASSWORD":notify,
        "CORE_DATABASE_URL":f"postgres://iot_core:{core}@postgres:5432/furnace?sslmode=disable",
        "NOTIFY_DATABASE_URL":f"postgres://iot_notify:{notify}@postgres:5432/furnace?sslmode=disable",
        "SERVICE_TOKEN":secrets.token_hex(32),"TELEGRAM_TOKEN":"","TELEGRAM_PROXY":""}
    path.write_text("".join(f"{k}={v}\n" for k,v in values.items()))
    path.chmod(0o600)
    print("Created .env; values are not printed")
else:
    print("Using existing .env")
text=path.read_text()
lines=text.splitlines()
values=dict(line.split('=',1) for line in lines if line and not line.startswith('#') and '=' in line)
required=('PG_PASSWORD','CORE_DB_PASSWORD','NOTIFY_DB_PASSWORD','CORE_DATABASE_URL','NOTIFY_DATABASE_URL','SERVICE_TOKEN')
missing=[key for key in required if not values.get(key)]
if missing:
    raise SystemExit('Existing .env has missing or empty required fields: '+', '.join(missing)
                     +'. For a fresh installation, remove .env and rerun; with existing volumes, restore the original .env.')
for key,default in [('ADMIN_USERNAME','admin'),('ADMIN_PASSWORD',secrets.token_urlsafe(24)),('TELEGRAM_PROXY',''),('CAMERA_ENCRYPTION_KEY',secrets.token_hex(32))]:
    if key not in values or key in ('ADMIN_PASSWORD','CAMERA_ENCRYPTION_KEY') and not values[key]:
        lines=[line for line in lines if not line.startswith(key+'=')]
        lines.append(key+'='+default)
lines=[line for line in lines if not line.startswith('MEDIA_AUTH_USERS=')]
# Chat IDs now come only from private Bot pairing, never from a global environment value.
lines=[line for line in lines if not line.startswith('TELEGRAM_CHAT_ID=')]
updated='\n'.join(lines)+'\n'
if updated!=text:
    path.write_text(updated)
    path.chmod(0o600)
    print('Account bootstrap fields prepared; credential values are not printed')
