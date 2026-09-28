"""Drive independent simulated physical devices. All business control still passes FUXA.

Usage: python3 scripts/demo.py success [ABC0001]
       python3 scripts/demo.py reject
       python3 scripts/demo.py reset
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request
import urllib.error
import http.cookiejar
import uuid

ROOT=Path(__file__).resolve().parents[1]
BASE=os.getenv("FURNACE_TRACE_BASE_URL", "http://localhost:31080").rstrip("/")
CLIENT=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
AUTHENTICATED=False

def login():
    global AUTHENTICATED
    values=dict(line.split('=',1) for line in (ROOT/'.env').read_text().splitlines() if line and not line.startswith('#') and '=' in line)
    body=json.dumps({'username':values['ADMIN_USERNAME'],'password':values['ADMIN_PASSWORD']}).encode()
    with CLIENT.open(urllib.request.Request(BASE+'/api/v1/auth/login',data=body,headers={'Content-Type':'application/json'}),timeout=5) as response:
        json.load(response)
    AUTHENTICATED=True

def public(path, body=None):
    if not AUTHENTICATED:
        login()
    data=None if body is None else json.dumps(body).encode()
    request=urllib.request.Request(BASE+"/api/v1"+path,data=data,headers={"Content-Type":"application/json","Idempotency-Key":str(uuid.uuid4())})
    for attempt in range(3):
        try:
            with CLIENT.open(request,timeout=5) as response:
                return json.load(response)
        except (urllib.error.URLError,TimeoutError):
            if attempt==2:
                raise
            time.sleep(.5)

def internal(service,path,method="GET",body=None):
    # Run from the FUXA Pod so tests do not depend on host ClusterIP routing.
    spec=json.dumps({"url":f"http://{service}:8080"+path,"method":method,"body":body})
    code="""const spec=JSON.parse(process.argv[1]);
fetch(spec.url,{method:spec.method,headers:{'Content-Type':'application/json',Authorization:'Bearer '+process.env.SERVICE_TOKEN},body:spec.body===null?undefined:JSON.stringify(spec.body)})
.then(async r=>{const text=await r.text();if(!r.ok)throw Error(r.status+' '+text);console.log(text||'null')}).catch(e=>{console.error(e.message);process.exit(1)});"""
    result=subprocess.run(["sh",str(ROOT/"scripts/kube.sh"),"exec","deployment/fuxa","--","node","-e",code,spec],capture_output=True,text=True,check=True)
    return json.loads(result.stdout)

def wait(predicate, description, timeout=30):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        result=predicate()
        if result:
            return result
        time.sleep(.3)
    raise RuntimeError("Timed out: "+description)

def furnace():
    return public("/furnaces/f1")

def set_sensor(**values):
    return internal("f1-sensor","/internal/v1/sim/state","PATCH",values)

def reset():
    internal("camera-sim","/internal/v1/sim/scene","PUT",{"codes":[]})
    set_sensor(present=False,inFurnace=False)
    internal("f1-door","/internal/v1/sim/state","PATCH",{"jammed":False,"forceClosed":True})
    f=furnace()
    if f["cycle"]:
        wait(lambda: all(d["online"] for d in furnace()["devices"]),"devices online")
        time.sleep(1)
        public("/furnaces/f1/resets",{"operator":"演示脚本","reason":"模拟现场已清空并人工关门"})
        wait(lambda:furnace()["phase"]=="IDLE","manual reset")
    print("Simulation inputs cleared; history and basket reservations retained")

def ensure_catalog(success,basket):
    # Explicit demo setup: real scans never create or approve unknown baskets.
    try:
        existing=public('/baskets/'+basket)
    except urllib.error.HTTPError as error:
        if error.code!=404:
            raise
        existing=None
    if existing:
        allowed=existing['enabled'] and existing['batchEnabled'] and existing['qualityStatus']=='passed' and not existing['occupied']
        if success and not allowed:
            raise RuntimeError('Existing basket is not eligible; use a new basket or correct its catalogue data')
        if not success and allowed:
            raise RuntimeError('Existing basket is eligible; choose a basket whose actual catalogue rejects entry')
        return
    batch_no='DEMO-PASS' if success else 'DEMO-FAIL'
    batches=public('/batches?pageSize=100&q='+batch_no)['items']
    match=next((b for b in batches if b['batchNo']==batch_no),None)
    if match is None:
        created=public('/batches',{'batchNo':batch_no,'materialName':'演示物料','processSpec':'模拟热处理','qualityStatus':'passed' if success else 'failed','enabled':True,'notes':'演示脚本创建的真实台账记录'})
        match=public('/batches/'+created['id'])
    public('/baskets',{'basketNo':basket,'batchId':match['id'],'quantity':1,'enabled':True,'notes':'演示脚本显式登记；扫码服务不会自动登记'})
    print('Registered demo basket in PostgreSQL catalogue:',basket,flush=True)

def run(success,basket):
    if furnace()["cycle"]:
        raise RuntimeError("Existing cycle needs attention; run the explicit reset command first")
    ensure_catalog(success,basket)
    set_sensor(present=False,inFurnace=False)
    internal("camera-sim","/internal/v1/sim/scene","PUT",{"codes":[basket]})
    wait(lambda:furnace()["ready"],"camera and devices online")
    time.sleep(1)
    set_sensor(present=True)
    target="WAITING_ENTRY" if success else "BLOCKED"
    f=wait(lambda:(s if (s:=furnace())["phase"]==target else None),target)
    cycle=f["cycle"]["id"]
    if success:
        set_sensor(present=False,inFurnace=True)
        detail=wait(lambda:(d if (d:=public("/cycles/"+cycle))["cycle"]["status"]=="COMPLETED" else None),"completed entry")
        if len(detail["commands"])!=3 or any(c["status"]!="COMPLETED" for c in detail["commands"]):
            raise RuntimeError("Missing physical command completion")
        print(json.dumps({"result":"PASS","cycleId":cycle,"basketNo":basket,"commands":[c["command"]["action"] for c in detail["commands"]]},ensure_ascii=False))
    else:
        detail=public("/cycles/"+cycle)
        if detail["commands"] or f["cycle"]["reason"]!="MES_REJECTED":
            raise RuntimeError("MES rejection did not prevent door command")
        print(json.dumps({"result":"PASS","scenario":"MES rejected; no door command","cycleId":cycle},ensure_ascii=False))
    (ROOT/"evidence").mkdir(exist_ok=True)
    (ROOT/"evidence"/("entry-success.json" if success else "mes-rejected.json")).write_text(json.dumps(detail,ensure_ascii=False,indent=2)+"\n")

def scada_offline():
    before=furnace()
    kube=["sh",str(ROOT/"scripts/kube.sh")]
    try:
        subprocess.run(kube+["scale","deployment/fuxa","--replicas=0"],check=True,capture_output=True)
        # Scaling starts graceful termination; the existing keep-alive connection may
        # still serve until the Pod exits. Measure loss after the process is gone.
        subprocess.run(kube+["wait","--for=delete","pod","-l","app=fuxa","--timeout=40s"],check=True,capture_output=True)
        after=wait(lambda:(f if not (f:=furnace())["ready"] and all(not d["online"] for d in f["devices"]) else None),"SCADA disconnection")
        result={"result":"PASS","scenario":"FUXA required in device path","beforePhase":before["phase"],"afterPhase":after["phase"],"devices":[{"deviceId":d["deviceId"],"online":d["online"],"reason":d["reason"]} for d in after["devices"]]}
        (ROOT/"evidence/scada-offline.json").write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
        print(json.dumps(result,ensure_ascii=False))
    finally:
        subprocess.run(kube+["scale","deployment/fuxa","--replicas=1"],check=True,capture_output=True)

if __name__=="__main__":
    mode=sys.argv[1] if len(sys.argv)>1 else "success"
    if mode=="reset":
        reset()
    elif mode=="scada-offline":
        scada_offline()
    elif mode in ("success","reject"):
        basket=sys.argv[2] if len(sys.argv)>2 else ("A"+str(int(time.time()))[-6:] if mode=="success" else "BAD0001")
        run(mode=="success",basket)
    else:
        raise SystemExit("Use success, reject, reset, or scada-offline")
