"""Render project-owned Kubernetes resources and the importable FUXA project.

Run: uv run --with pyyaml python scripts/render_deploy.py
"""
import json
import os
from urllib.parse import urlparse
from pathlib import Path
import yaml

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "deploy/k8s"
OUT.mkdir(parents=True, exist_ok=True)
furnaces = json.loads((ROOT / "configs/furnaces.json").read_text())
tag_file = ROOT / ".local/image-tags.json"
image_tags = json.loads(tag_file.read_text()) if tag_file.exists() else {}
def image(service):
    reference = image_tags.get(service, "0.1.0")
    # Older local installations recorded only the tag, not the full image name.
    return reference if ":" in reference else f"furnace-trace-{service}:{reference}"
PROJECT_LABEL = {"app.kubernetes.io/name": "furnace-trace"}
resources = [{"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": "furnace-trace", "labels": PROJECT_LABEL}}]

def add(kind, name, spec=None, api="v1", **fields):
    value = {"apiVersion": api, "kind": kind, "metadata": {"name": name, "labels": PROJECT_LABEL | {"app.kubernetes.io/component": name}}}
    if spec is not None:
        value["spec"] = spec
    value.update(fields)
    resources.append(value)

def config(name, data):
    add("ConfigMap", name, data=data)

def deployment(name, image, port=8080, env=None, volumes=None, mounts=None, args=None, probe="/healthz", extra=None):
    container = {"name": name, "image": image, "imagePullPolicy": "IfNotPresent", "ports": [{"containerPort": port}],
        "resources": {"requests": {"cpu": "50m", "memory": "64Mi"}, "limits": {"memory": "512Mi"}}}
    if env:
        container["env"] = [{"name": k, "valueFrom": {"secretKeyRef": {"name": "furnace-trace-secrets", "key": v[1:]}}} if v.startswith("$") else {"name": k, "value": v} for k,v in env.items()]
    if mounts:
        container["volumeMounts"] = mounts
    if args:
        container["args"] = args
    if probe:
        container["readinessProbe"] = {"httpGet": {"path": probe, "port": port}, "periodSeconds": 3, "timeoutSeconds": 2}
        container["livenessProbe"] = {"httpGet": {"path": probe, "port": port}, "periodSeconds": 10, "initialDelaySeconds": 15, "timeoutSeconds": 2}
    if extra:
        container.update(extra)
    pod = {"containers": [container]}
    if volumes:
        pod["volumes"] = volumes
    pod_labels = {"app": name} | PROJECT_LABEL | {"app.kubernetes.io/component": name}
    add("Deployment", name, {"replicas": 1, "strategy": {"type": "Recreate"}, "selector": {"matchLabels": {"app": name}}, "template": {"metadata": {"labels": pod_labels}, "spec": pod}}, "apps/v1")
    service = {"selector": {"app": name}, "ports": [{"name": "http", "port": port, "targetPort": port}]}
    if name in ("core", "qr-display"):
        service["type"] = "NodePort"
        service["ports"][0]["nodePort"] = {"core": 31080, "qr-display": 31082}[name]
    add("Service", name, service)
    if name == "core":
        add("Service", "fuxa-access", {"type":"NodePort","selector":{"app":"core"},"ports":[{"name":"scada","port":1881,"targetPort":8081,"nodePort":31081}]})

def pvc(name, size):
    spec = {"accessModes": ["ReadWriteOnce"], "resources": {"requests": {"storage": size}}}
    storage_class = os.getenv("FURNACE_TRACE_STORAGE_CLASS", "default")
    if storage_class and storage_class != "default":
        spec["storageClassName"] = storage_class
    add("PersistentVolumeClaim", name, spec)

config("furnaces", {"furnaces.json": json.dumps(furnaces, ensure_ascii=False, indent=2)})
config("mosquitto-config", {"mosquitto.conf": "listener 1883\nallow_anonymous true\npersistence true\npersistence_location /mosquitto/data/\nlog_dest stdout\n"})
media_paths={f["id"]:{"source":f"rtsp://127.0.0.1:8554/{f['id']}-sim","rtspTransport":"tcp"} for f in furnaces}
media_paths.update({f["id"]+"-sim":{"source":"publisher"} for f in furnaces})
media_paths.update({f["id"]+"-live":{"source":"publisher"} for f in furnaces})
media_users=[
    {"user":"any","ips":["127.0.0.1","::1"],"permissions":[{"action":"read","path":f["id"]+suffix} for f in furnaces for suffix in ("-sim","-live")]},
    {"user":"media","pass":"","ips":[],"permissions":[{"action":"read","path":f["id"]} for f in furnaces]+[{"action":"publish","path":f["id"]+suffix} for f in furnaces for suffix in ("-sim","-live")]},
    {"user":"controller","pass":"","ips":[],"permissions":[{"action":"api"}]}
]
config("mediamtx-config", {"mediamtx.yml":yaml.safe_dump({"logLevel":"error","api":True,"apiAddress":":9997","authInternalUsers":media_users,"rtspTransports":["tcp"],"hls":True,"hlsAlwaysRemux":True,"hlsAllowOrigins":["*"],"paths":media_paths})})
config("postgres-init", {"10-roles.sh": '''#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=core_pass="$CORE_DB_PASSWORD" --set=notify_pass="$NOTIFY_DB_PASSWORD" <<'SQL'
CREATE ROLE iot_core LOGIN PASSWORD :'core_pass';
CREATE ROLE iot_notify LOGIN PASSWORD :'notify_pass';
CREATE SCHEMA core AUTHORIZATION iot_core;
CREATE SCHEMA notify AUTHORIZATION iot_notify;
SQL
'''})
for name,size in [("postgres-data","2Gi"),("fuxa-data","1Gi"),("mqtt-data","128Mi")]:
    pvc(name,size)
deployment("postgres", "postgres:17-alpine",5432,
    env={"POSTGRES_USER":"postgres","POSTGRES_DB":"furnace","POSTGRES_PASSWORD":"$PG_PASSWORD","CORE_DB_PASSWORD":"$CORE_DB_PASSWORD","NOTIFY_DB_PASSWORD":"$NOTIFY_DB_PASSWORD"},
    volumes=[{"name":"data","persistentVolumeClaim":{"claimName":"postgres-data"}},{"name":"init","configMap":{"name":"postgres-init"}}],
    mounts=[{"name":"data","mountPath":"/var/lib/postgresql/data"},{"name":"init","mountPath":"/docker-entrypoint-initdb.d"}],probe=None,
    extra={"readinessProbe":{"exec":{"command":["pg_isready","-U","postgres","-d","furnace"]},"periodSeconds":3}})
deployment("mosquitto","eclipse-mosquitto:2.0.22",1883,
    volumes=[{"name":"data","persistentVolumeClaim":{"claimName":"mqtt-data"}},{"name":"config","configMap":{"name":"mosquitto-config"}}],
    mounts=[{"name":"data","mountPath":"/mosquitto/data"},{"name":"config","mountPath":"/mosquitto/config"}],probe=None,
    extra={"readinessProbe":{"tcpSocket":{"port":1883},"periodSeconds":3}})
deployment("fuxa",image("fuxa"),1881,
    env={"SERVICE_TOKEN":"$SERVICE_TOKEN"},
    volumes=[{"name":"data","persistentVolumeClaim":{"claimName":"fuxa-data"}}],
    mounts=[{"name":"data","mountPath":"/usr/src/app/FUXA/server/_appdata"}],probe="/api/version",
    extra={"resources":{"requests":{"cpu":"100m","memory":"256Mi"},"limits":{"memory":"1Gi"}}})
deployment("mediamtx","bluenviron/mediamtx:1.15.6",8554,
    env={"MTX_AUTHINTERNALUSERS_1_PASS":"$SERVICE_TOKEN","MTX_AUTHINTERNALUSERS_2_PASS":"$SERVICE_TOKEN"},
    volumes=[{"name":"config","configMap":{"name":"mediamtx-config"}}],mounts=[{"name":"config","mountPath":"/mediamtx.yml","subPath":"mediamtx.yml"}],probe=None,
    extra={"readinessProbe":{"tcpSocket":{"port":8554},"periodSeconds":3}})
resources[-1]["spec"]["ports"].extend([{"name":"hls","port":8888,"targetPort":8888},{"name":"api","port":9997,"targetPort":9997}])

common={"MEDIA_AUTH_ENABLED":"true","SERVICE_TOKEN":"$SERVICE_TOKEN","FURNACES_CONFIG":"/config/furnaces.json"}
cfg_vol=[{"name":"config","configMap":{"name":"furnaces"}}]
cfg_mount=[{"name":"config","mountPath":"/config"}]
apps={
    "core": {"DATABASE_URL":"$CORE_DATABASE_URL","SCADA_URL":"http://scada-adapter:8080","NOTIFIER_URL":"http://notifier:8080","MES_URL":"http://mes:8080","MEDIA_URL":"http://mediamtx:8888","ADMIN_USERNAME":"$ADMIN_USERNAME","ADMIN_PASSWORD":"$ADMIN_PASSWORD","CAMERA_ENCRYPTION_KEY":"$CAMERA_ENCRYPTION_KEY","MEDIA_API_URL":"http://mediamtx:9997"},
    "scada-adapter":{"FUXA_URL":"http://fuxa:1881","CORE_URL":"http://core:8080"},
    "notifier":{"DATABASE_URL":"$NOTIFY_DATABASE_URL","TELEGRAM_TOKEN":"$TELEGRAM_TOKEN","TELEGRAM_PROXY":"$TELEGRAM_PROXY"},
    "mes":{"DATABASE_URL":"$CORE_DATABASE_URL"},
}
for name,env in apps.items():
    if not (ROOT / "cmd" / name / "main.go").exists():
        continue
    deployment(name,image(name),env=common|env,volumes=cfg_vol,mounts=cfg_mount)
deployment("qr-display", image("qr-display"), env={"CORE_URL":"http://core:8080","SERVICE_TOKEN":"$SERVICE_TOKEN"})
for f in furnaces:
    deployment(urlparse(f["visionUrl"]).hostname,image("vision"),env=common|{"CAMERA_ID":f["camera"],"FURNACE_ID":f["id"],"RTSP_URL":f"rtsp://mediamtx:8554/{f['id']}","CORE_URL":"http://core:8080"})
    deployment("camera-sim" if f["id"]=="f1" else f["id"]+"-camera-sim",image("camera-sim"),env=common|{"RTSP_URL":f"rtsp://mediamtx:8554/{f['id']}-sim"})
    for kind in ("sensor","door","lamp","temperature"):
        if not f.get(kind):
            continue
        deployment(f[kind],image("device-sim"),env=common|{"FURNACE_ID":f["id"],"DEVICE_ID":f[kind],"DEVICE_KIND":kind,"MQTT_URL":"tcp://mosquitto:1883"})

tags={}
for f in furnaces:
    for kind in ("sensor","door","lamp","temperature"):
        if not f.get(kind):
            continue
        device=f[kind]
        for role in ["state"]+([] if kind == "sensor" else ["command"]):
            tid=f"{device}-{role}";topic=f"iot/v1/devices/{device}/{role}"
            options={"subs":topic} if role=="state" else {"pubs":[],"retain":False}
            tags[tid]={"id":tid,"name":tid,"type":"raw","address":topic,"memaddress":topic,"options":options,"daq":{"enabled":False,"interval":60},"init":""}
project={"version":"1.00","server":{"id":"0","name":"FUXA Server","type":"FuxaServer","property":{}},
    "devices":{"mqtt-lab":{"id":"mqtt-lab","name":"入炉设备 MQTT","type":"MQTTclient","enabled":True,"polling":100,"property":{"address":"mqtt://mosquitto:1883"},"tags":tags}},
    "hmi":{"layout":{"start":"furnace-view","autoresize":True,"showdev":True,"navigation":{"items":[],"mode":"over","type":"text"},"header":{"title":"入炉现场监控","items":[]}},"views":[{"id":"furnace-view","name":"入炉现场","type":"svg","profile":{"width":1280,"height":720,"bkcolor":"#111b27"},"items":{},"variables":{},"svgcontent":"<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1280\" height=\"720\"><rect width=\"1280\" height=\"720\" fill=\"#111b27\"/><text x=\"60\" y=\"100\" font-size=\"36\" fill=\"white\">入炉现场监控</text><text x=\"60\" y=\"160\" font-size=\"22\" fill=\"#9ab2c8\">MQTT → FUXA → 业务服务</text></svg>"}]}}
view = project["hmi"]["views"][0]
project["scripts"] = []
svg = ['<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="720"><rect width="1280" height="720" fill="#111b27"/><text x="60" y="75" font-family="sans-serif" font-size="32" fill="#f4f8fa">入炉现场监控</text><text x="60" y="115" font-family="sans-serif" font-size="17" fill="#93acbc">设备 → MQTT → FUXA → 入炉业务服务</text>']
for row,f in enumerate(furnaces):
    y=175+row*350
    svg.append(f'<text x="60" y="{y}" fill="#cae0e8" font-size="23">{f["name"]}</text>')
    displays=[
        ("sensor","present","入口到位","有料筐","入口空闲"),
        ("door","doorOpen","炉门状态","已打开","已关闭"),
        ("sensor","inFurnace","入炉到位","已到位","未到位"),
        ("lamp","lampOn","声光报警","报警中","正常")]
    if f.get("temperature"):
        displays.append(("temperature","temperatureC","炉温","",""))
    for col,(kind,field,label,yes,no) in enumerate(displays):
        device=f[kind];tid=f"{device}-view-{field}";scriptid=f"s_{f['id']}_{field}"
        tags[tid]={"id":tid,"name":label,"type":"raw","address":f"iot/v1/devices/{device}/state","memaddress":f"iot/v1/devices/{device}/state","options":{"subs":f"iot/v1/devices/{device}/state"},"scaleReadFunction":scriptid,"daq":{"enabled":False,"interval":60}}
        code=f"const s=JSON.parse(value); return s.values.{field} ? '{yes}' : '{no}';"
        if kind=="temperature":
            code="const s=JSON.parse(value); return Number(s.values.temperatureC).toFixed(1) + ' °C';"
        project["scripts"].append({"id":scriptid,"name":f"read_{f['id']}_{field}","code":code,"sync":True,"mode":"SERVER","parameters":[{"name":"value","type":"value","value":""}],"permission":0})
        gid=f"svg-ext-value-{f['id']}-{field}"
        view["items"][gid]={"id":gid,"name":label,"type":"svg-ext-value","property":{"variableId":tid,"readonly":True,"ranges":[],"actions":[],"events":[]}}
        view["variables"][tid]={"id":tid,"name":label,"device":"mqtt-lab"}
        width=1160//len(displays)
        x=60+col*width
        svg.append(f'<rect x="{x}" y="{y+25}" width="{width-20}" height="132" rx="8" fill="#20313f"/><text x="{x+18}" y="{y+60}" fill="#91aab9" font-size="16">{label}</text><g id="{gid}"><text x="{x+18}" y="{y+115}" fill="#b9e8d7" font-size="26">等待数据</text></g>')
    if f.get("temperature"):
        device=f["temperature"]; tid=f"{device}-command"; scriptid=f"s_{f['id']}_set_temperature"
        tags[tid]["scaleWriteFunction"]=scriptid
        code=("const temperatureC=Number(value); if(String(value).trim()==='' || !Number.isFinite(temperatureC)) throw Error('Invalid temperature'); "
              + f"const state=JSON.parse($getTag('{device}-state')); "
              + "const now=Date.now(); const age=now-Date.parse(state.observedAt); if(!state.bootId || !Number.isFinite(age) || age>3000 || age< -1000) throw Error('Temperature sensor offline'); "
              + "return JSON.stringify({commandId:require('crypto').randomUUID(),cycleId:'',deviceId:state.deviceId,furnaceId:state.furnaceId,targetBootId:state.bootId,action:'SET_TEMPERATURE',temperatureC,issuedAt:new Date(now).toISOString(),expiresAt:new Date(now+10000).toISOString()});")
        project["scripts"].append({"id":scriptid,"name":f"set_{f['id']}_temperature","code":code,"sync":True,"mode":"SERVER","parameters":[{"name":"value","type":"value","value":""}],"permission":0})
        gid=f"svg-ext-html_input-{f['id']}-temperature"
        view["items"][gid]={"id":gid,"name":"模拟炉温输入","type":"svg-ext-html_input","property":{"variableId":tid,"readonly":False,"ranges":[],"actions":[],"events":[],"options":{"type":"number","numeric":True,"updated":False,"readonly":False}}}
        view["variables"][tid]={"id":tid,"name":"模拟炉温输入","device":"mqtt-lab"}
        svg.append(f'<text x="60" y="{y+200}" fill="#cae0e8" font-size="19">模拟炉温设定（°C）</text><g id="{gid}"><rect x="285" y="{y+173}" width="210" height="44" rx="5" fill="#f4f8fa"/><foreignObject x="285" y="{y+173}" width="210" height="44"><div xmlns="http://www.w3.org/1999/xhtml" style="width:100%;height:100%"><input id="I-HXI_{f["id"]}_temperature" type="number" step="0.1" aria-label="模拟炉温" style="box-sizing:border-box;width:100%;height:100%;font-size:22px;padding:6px 12px;color:#18303b;border:0;border-radius:5px"/></div></foreignObject></g><text x="520" y="{y+201}" fill="#93acbc" font-size="17">输入后按 Enter 下发，以右上方回传炉温为准</text>')
svg.append('<text x="60" y="640" fill="#8da5b5" font-size="16">现场设温经 MQTT 下发到独立模拟器；MES 放行、报警阈值和记录查询在业务工作台。</text></svg>')
height=max(720,350*len(furnaces)+330)
view["profile"]["height"]=height
view["svgcontent"]="".join(svg).replace('height="720"',f'height="{height}"').replace('y="640"',f'y="{height-80}"')
(ROOT/"deploy/fuxa/project.json").write_text(json.dumps(project,ensure_ascii=False,indent=2)+"\n")
(OUT/"resources.yaml").write_text(yaml.safe_dump_all(resources,sort_keys=False,allow_unicode=True))
(OUT/"kustomization.yaml").write_text("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: furnace-trace\nresources:\n  - resources.yaml\n")
print(f"Rendered {len(resources)} project resources")
