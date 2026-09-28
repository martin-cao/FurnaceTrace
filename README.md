# FurnaceTrace

FurnaceTrace is a furnace admission traceability system built as a course project. It tracks a basket from RTSP QR-code detection through MES eligibility checks, FUXA/MQTT control of simulated equipment, execution feedback, and PostgreSQL archiving. A Vue workbench shows live state, history, and alarms. A separate notifier service supports per-user Telegram Bot pairing.

The project covers **furnace admission only**. The camera, sensors, door, and temperature source can be simulated as separate processes. It does not implement a real PLC, furnace exit, or a complete external MES. The simulated demo still exercises the RTSP, FUXA, MQTT, and PostgreSQL paths. Real IP cameras and PLCs have not been validated.

## Prerequisites

- A Linux build/deployment host. The commands below target Linux and standard Kubernetes; no desktop container runtime is required.
- A Kubernetes cluster, `kubectl`, and dynamic volume provisioning through a default or named StorageClass.
- Docker Buildx and a container registry that the cluster can pull from. You need permission to push images to that registry.
- A POSIX shell, Python 3.9+, [uv](https://docs.astral.sh/uv/), and Node.js/npm (Node.js 20.19.x or 22.12+).
- Optional: Go 1.26+ for running Go tests on the host. Docker builds use the Go version in the Dockerfile. A local FFmpeg installation is needed only for the optional UDP camera bridge.

The first build downloads base images, Go/npm dependencies, and a Python package. Deployment uses the fixed `furnace-trace` namespace and three NodePort services. Project scripts require an explicit `FURNACE_TRACE_KUBE_CONTEXT`; they also honor the standard `KUBECONFIG` environment variable. `FURNACE_TRACE_KUBECONFIG` can override it for this project alone.

## First deployment on Linux

Clone the repository on the Linux host and enter the directory containing this README and `scripts/`. Run the following commands in a POSIX-compatible shell such as `sh` or `bash`. Replace the example Kubernetes context and image prefix with your own values. The resulting images must be readable by the cluster; the example uses public GHCR packages.

```sh
kubectl config get-contexts
export FURNACE_TRACE_KUBE_CONTEXT=your-k8s-context
export FURNACE_TRACE_IMAGE_PREFIX=ghcr.io/your-account/furnace-trace-
export FURNACE_TRACE_IMAGE_PLATFORM=linux/amd64
export FURNACE_TRACE_STORAGE_CLASS=default
docker login ghcr.io
sh scripts/build.sh
```

Set `FURNACE_TRACE_IMAGE_PLATFORM` to match your cluster nodes; the example targets `linux/amd64`. `FURNACE_TRACE_STORAGE_CLASS=default` uses the cluster default StorageClass. If the cluster has no default, set it to the name of an available class. `build.sh` builds the frontend and all service images, pushes them under `FURNACE_TRACE_IMAGE_PREFIX`, and records the exact image references in the Git-ignored `.local/image-tags.json`.

Make sure the pushed images are pullable by the cluster. If the GHCR packages are private, make them public before deploying. The manifests do not create image pull credentials. Then run:

```sh
sh scripts/up.sh
```

`up.sh` generates `deploy/k8s/resources.yaml` with your image references and storage settings, creates the namespace and Secret, deploys the services, and initializes FUXA. The generated manifest is Git-ignored; run `build.sh` before `up.sh` on a fresh checkout.

On first use, `up.sh` creates a Git-ignored `.env` with the initial administrator password, database credentials, and service keys. **Do not copy `.env.example` to `.env` first**: it has blank credentials, and setup will stop with a clear error. For a fresh installation, remove that copied file and rerun `up.sh`. Read `ADMIN_USERNAME` and `ADMIN_PASSWORD` from your generated `.env` to sign in. Keep this file with any existing database volumes so the keys remain consistent. Other users can register from the login page.

Open these NodePort URLs from a machine that can reach a cluster node. Replace `<node-address>` with that node’s reachable address:

| Service | URL |
| --- | --- |
| Workbench | `http://<node-address>:31080` |
| FUXA view (administrator login required) | `http://<node-address>:31081` |
| QR display | `http://<node-address>:31082` |

## Run the simulated admission flow

Point the demo script at the workbench address, replacing `NODE_ADDRESS`:

```sh
export FURNACE_TRACE_BASE_URL='http://NODE_ADDRESS:31080'
python3 scripts/demo.py success
```

The script coordinates simulated physical inputs; it does not write a successful business result directly. `python3 scripts/demo.py reject` demonstrates an MES rejection. To inspect the deployment or stop this project:

```sh
sh scripts/kube.sh get pods
sh scripts/kube.sh logs deployment/core --tail=30
sh scripts/stop.sh
```

`stop.sh` retains the PostgreSQL, FUXA, and MQTT volumes. Run `up.sh` again to resume with the same data. After changing a service, `sh scripts/build.sh core` builds and pushes only that image; then run `up.sh`. Keep the environment variables above set in the shell session.

Project-owned images and binaries use `furnace-trace-<role>` names, such as `furnace-trace-core` and `furnace-trace-notifier`. Inside the `furnace-trace` namespace, Services and Deployments use short role names (`core`, `mes`, `notifier`, and so on) for stable internal DNS. Third-party services keep their upstream names.

An older `iot-lab` installation is not migrated automatically. Deploying this version creates separate volumes in `furnace-trace`; the old namespace and its data are left intact.

## Optional camera and Telegram setup

An administrator can select the simulated camera or set an RTSP URL in the workbench. For a camera that only supports UDP RTP and needs a local SOCKS proxy, copy `configs/camera-bridge.json` to the Git-ignored `.local/camera-bridge-config.json`, enter the local camera and proxy addresses, and set `enabled` to `true`. The startup script prefers this local override. The versioned example keeps the bridge disabled. See [operations](docs/operations.md).

Telegram is optional. Set `TELEGRAM_TOKEN` in `.env` and restart the notifier. Each user sends `/start` to that Bot and completes pairing in the workbench. There is no shared Chat ID. Without a Bot token, actual Telegram delivery cannot be tested.

## Repository map

- [OpenAPI contract](api/openapi.yaml): public HTTP endpoints and shared message structures; frontend types are generated from it.
- `internal/core`: admission state machine, MES checks, accounts, alarms, audit records, and archiving.
- `internal/scada`: FUXA integration. `internal/device`, `internal/camera`, and `internal/vision`: simulated equipment and QR scanning.
- `internal/notifier`: Telegram pairing and delivery.
- `web/`: Vue 3 workbench. `deploy/`: container and Kubernetes resources.
- [User guide](docs/user-guide.md), [PDF guide](output/pdf/furnace-user-guide.pdf), [catalog and alarm rules](docs/catalog-and-rules.md), and [accounts and pairing](docs/accounts-and-pairing.md) are historical Chinese-language documentation. Their screenshots reflect specific demo versions.
- [Demo evidence and validation limits](evidence/README.md) distinguish the simulated communication path from unvalidated physical equipment.

## License

Project-owned code and documentation are available under the [MIT License](LICENSE). FUXA, MediaMTX, PostgreSQL, and other third-party components retain their own licenses. The course handouts in `handout/` and local runtime data are not part of the repository.
