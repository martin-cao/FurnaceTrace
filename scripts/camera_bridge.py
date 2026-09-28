"""Local UDP camera bridge for the demo: start / stop / run. No global proxy changes."""
import json
import os
from pathlib import Path
import select
import signal
import socket
import struct
import subprocess
import sys
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / '.local/camera-bridge.json'
LOCAL_CONFIG = ROOT / '.local/camera-bridge-config.json'
CONFIG = LOCAL_CONFIG if LOCAL_CONFIG.exists() else ROOT / 'configs/camera-bridge.json'


def alive(pid):
    try:
        command = subprocess.check_output(['ps', '-p', str(pid), '-o', 'command='], text=True)
        return any(marker in command for marker in ('camera_bridge.py run', 'rtsp://127.0.0.1:18554/', 'port-forward service/mediamtx 18555:8554'))
    except subprocess.CalledProcessError:
        return False


def control(mode):
    state = json.loads(STATE.read_text()) if STATE.exists() else {'pids': []}
    pids = [p for p in state['pids'] if alive(p)]
    if mode == 'stop':
        for pid in pids:
            os.kill(pid, signal.SIGTERM)
        STATE.unlink(missing_ok=True)
        print('Local camera bridge stopped')
    elif pids:
        print('Local camera bridge already running')
    elif CONFIG.exists() and json.loads(CONFIG.read_text()).get('enabled'):
        STATE.parent.mkdir(exist_ok=True)
        with (STATE.parent / 'camera-bridge.log').open('ab') as log:
            child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), 'run'], cwd=ROOT,
                                     stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
        STATE.write_text(json.dumps({'pids': [child.pid]}))
        print('Local UDP camera bridge started')


def forward(client, cfg):
    upstream = None
    try:
        upstream = socket.create_connection((cfg['proxyHost'], cfg['proxyPort']), timeout=5)
        def read_exact(n):
            data = b''
            while len(data) < n:
                part = upstream.recv(n-len(data))
                if not part:
                    raise OSError('proxy closed')
                data += part
            return data
        upstream.sendall(b'\x05\x01\x00')
        if read_exact(2) != b'\x05\x00':
            raise OSError('proxy authentication unsupported')
        host = cfg['cameraHost'].encode()
        upstream.sendall(b'\x05\x01\x00\x03' + bytes([len(host)]) + host + struct.pack('!H', cfg['cameraPort']))
        reply = read_exact(4)
        if reply[1] != 0:
            raise OSError('proxy connection failed')
        length = {1:4, 4:16}.get(reply[3])
        if reply[3] == 3:
            length = read_exact(1)[0]
        if length is None:
            raise OSError('invalid proxy response')
        read_exact(length + 2)
        upstream.settimeout(None)
        while True:
            readable, _, _ = select.select([client, upstream], [], [], 30)
            for source in readable:
                data = source.recv(65536)
                if not data:
                    return
                (upstream if source is client else client).sendall(data)
    except OSError:
        pass
    finally:
        client.close()
        if upstream:
            upstream.close()


def run():
    cfg = json.loads(CONFIG.read_text())
    values = dict(line.split('=', 1) for line in (ROOT / '.env').read_text().splitlines()
                  if line and not line.startswith('#') and '=' in line)
    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind(('127.0.0.1', 18554))
    listener.listen()
    def accept():
        while True:
            client, _ = listener.accept()
            threading.Thread(target=forward, args=(client, cfg), daemon=True).start()
    threading.Thread(target=accept, daemon=True).start()
    children = []
    def stop(*_):
        for child in children:
            if child.poll() is None:
                child.terminate()
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        while True:
            port = subprocess.Popen(['sh', str(ROOT / 'scripts/kube.sh'), 'port-forward', 'service/mediamtx', '18555:8554', '--address', '127.0.0.1'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            children[:] = [port]
            time.sleep(1)
            command = ['ffmpeg', '-nostdin', '-hide_banner', '-loglevel', 'error', '-rtsp_transport', 'udp', '-timeout', '5000000', '-i', 'rtsp://127.0.0.1:18554/', '-an', '-c:v', 'copy', '-rtsp_transport', 'tcp', '-f', 'rtsp', f'rtsp://media:{values["SERVICE_TOKEN"]}@127.0.0.1:18555/f1-live']
            # FFmpeg errors can include the credential-bearing output URL; do not persist them.
            media = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            children.append(media)
            while port.poll() is None and media.poll() is None:
                time.sleep(1)
            for child in children:
                if child.poll() is None:
                    child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
            print('Camera disconnected; reconnecting', flush=True)
            time.sleep(2)
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate()


if __name__ == '__main__':
    mode = sys.argv[1] if len(sys.argv) > 1 else 'start'
    if mode == 'run':
        run()
    elif mode in ('start', 'stop'):
        control(mode)
    else:
        raise SystemExit('Usage: camera_bridge.py start|stop|run')
