# 配置与操作

## 服务

一个 `core` 负责业务状态机与 Vue 静态资源；`scada-adapter` 负责设备点位；每个摄像头有独立 `vision`；`notifier` 和 PostgreSQL 台账服务 `mes` 各自独立。

模拟现场由 camera-sim、到位 sensor、door、lamp、temperature 五类独立实例组成。摄像头发布 H.264 RTSP 视频，识别端以 5 FPS 抽帧，连续两帧同一码才提交。稳定识别即启动 MES 校验；空画面不创建周期，同一码持续停留只触发一次。

FUXA、PostgreSQL、Mosquitto 使用 PVC。其余服务不依赖本地持久文件。模拟设备的内存状态重启后重置，并生成新 bootId；业务会识别设备重启。

## 配置与 API

`configs/furnaces.json` 定义炉子、设备编号、摄像头、vision 服务与预览地址。增加炉子时使用不同 ID，并给 `visionUrl` 使用独立服务名；部署生成器据此创建另一组设备和视频服务。

配置变更后：

```sh
sh scripts/up.sh
python3 scripts/import_fuxa.py --replace-demo-project
sh scripts/kube.sh rollout restart deployment/core deployment/scada-adapter
```

`--replace-demo-project` 只用于更新本项目生成的 FUXA 工程；如果手动编辑过画面，先通过 FUXA 导出保存。

默认料筐编号取二维码末尾 7 位字母数字。core 的 `BASKET_LENGTH` 和 `BASKET_PATTERN` 可以覆盖长度与校验规则。

独立 MES 现从 PostgreSQL 的批次与料筐台账校验入炉资格；不再根据编号前缀放行。料筐、批次、二维码和完整报警规则操作见 [MES 台账与报警规则](catalog-and-rules.md)。

炉温输入仍在 FUXA 现场页面。旧炉温配置和所有固定报警触发入口已迁入统一规则，规则默认值可修改、停用和删除。

## Telegram

首次启动生成根目录 `.env`，权限 0600，Git 忽略。只需配置 `TELEGRAM_TOKEN`；值不加引号，`TELEGRAM_PROXY` 不需要时留空。Chat ID 由每位用户的 Bot 私聊配对取得，不再来自环境变量。

```sh
sh scripts/up.sh
sh scripts/kube.sh rollout restart deployment/notifier
```

用户自主注册登录后，打开“个人通知”，私聊同一个 Bot 发送 `/start`，把返回的 8 位配对码填写到网页。配对码 10 分钟有效，只能绑定一次；重复请求只能返回本人仍存在的同一绑定。一个 Telegram 私聊只能绑定一个网页账户。

每人可以选择炉子、扫码/MES/设备/流程/炉温/自定义规则通知类别，以及恢复通知和总开关。网页解绑或 Bot `/unlink` 会取消尚未发送的通知；已开始发送的消息可能仍会到达。`/status` 查询自己的绑定状态。

旧版没有用户归属的积压任务保留但停止自动发送，不会分配给新用户。新事件按绑定时点与个人偏好创建独立投递，投递前再次检查。用户只能查询自己的 Telegram 记录。

- `pending`：待发送、缺少配置或明确可以重试的错误。
- `sent`：Telegram 返回 `ok=true`。
- `failed`：明确拒绝，需修正配置。
- `unknown`：请求可能已送达但没有可靠结果，或发送期间服务中断。暂停自动重发，避免悄悄重复发送。
- `cancelled`：已解绑、暂停或不再符合个人偏好。

429 使用 Telegram 的 `retry_after`；其他可重试错误退避到最多 256 秒。通知不可达不阻塞入炉。

## 账户

普通用户自行注册，拥有共享生产数据的只读权限及自己的通知设置。管理员由 `.env` 的 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 首次初始化。已有管理员不会在重启时被重新设置密码。

密码使用 bcrypt，Cookie 会话有效 12 小时，HttpOnly、SameSite=Strict。退出会撤销数据库中的会话；SSE 与 FUXA WebSocket 最迟在下一次 5 秒会话检查时停止。当前使用本机 HTTP；以后配置 HTTPS 时可为 core 设置 `COOKIE_SECURE=true`。

FUXA 本身只有 ClusterIP。节点的 `31081` NodePort 由 core 的管理员认证代理提供，使用与工作台相同的 Cookie；必须先在同一节点地址登录管理员。

## 真实 IP 摄像头

对只接受 UDP RTP、且容器无法直连的摄像头，可通过运行部署脚本的机器上的 `scripts/camera_bridge.py` 和 SOCKS 代理建立 RTSP 控制连接，再由本机 FFmpeg 无转码转发到 MediaMTX 的 `f1-live`。识别服务和网页仍从 MediaMTX 读取。默认配置关闭桥接，模拟摄像头无需此步骤。

需要桥接时，先将 `configs/camera-bridge.json` 复制到 `.local/camera-bridge-config.json`，只在本地文件填写摄像头与代理地址，并将 `enabled` 设为 `true`。`up.sh` / `stop.sh` 会相应启动和停止本机桥接进程。然后在工作台保存 **`rtsp://127.0.0.1:8554/f1-live`**（这里的 127.0.0.1 是 MediaMTX 容器）。恢复普通直连摄像头时，停止桥接、关闭本地配置中的 `enabled`，再在工作台保存可从集群访问的真实地址。

管理员在“设置 → 摄像头 / RTSP 视频源”选择 RTSP 并保存完整地址；无需编辑 ConfigMap，也无需停止模拟摄像头。模拟摄像头发布到独立的 `f1-sim` 路径，实际识别与预览读取的 `f1` 由 MediaMTX 代理到选定源。

配置加密保存在 PostgreSQL，管理员可在设置页查看和编辑完整地址，重启后自动应用。必须保留 `.env` 中的 `CAMERA_ENCRYPTION_KEY`。切换前需完成或复位活动周期；配置应用与视频连通在页面分别显示。详细步骤见 [设置说明](settings.md)。

## 故障与恢复

设备每秒发布状态，适配器校验源时间与序号。超过 3 秒没有新状态会离线。core 自身也检测适配器停止推送。

适配器每 500 ms 批量读取点位，约 600 次/5 分钟，给 FUXA 默认 1000 次/5 分钟的 API 限额留出余量。core 启动后的前 10 秒用于初始化连接，不发送连接类报警；运行中的 3 秒数据过期判定不变。

每个动作有命令 ID、bootId 和有效期。HTTP 202 或 MQTT 发出均不能代表动作完成；反馈必须匹配命令编号与实际门状态。

执行期间掉线、反馈超时或进程重启后，无法证明完成的周期进入恢复流程。core 不会盲目重放开门，而是等待新鲜设备状态与旧命令到期，再排队关门、复位；只有匹配反馈到达才归档为 ABORTED。

扫码/MES 拒绝或流程异常后，系统自动排队关门、复位，不再显示人工复位按钮。自动等待炉门新鲜状态与旧命令到期，完成反馈后以 ABORTED 归档并继续扫码；恢复失败时等待后重试，不伪造 COMPLETED。

已有入炉占用不会通过人工复位偷偷释放，因为本期没有出炉流程。演示数据需要彻底重置时，先处理活动周期，再明确运行：

```sh
sh scripts/reset_data.sh --delete-demo-history
```

该脚本先备份 core/notify 数据到 `.local/backups/`，再清空本项目的演示历史与占用；保留用户、会话、配对、个人偏好和炉温报警设置，不删除 PVC。未在本次交付中执行这个删除操作。

## 集群环境边界

部署前设置 `FURNACE_TRACE_KUBE_CONTEXT`，脚本固定在该 context 的 `furnace-trace` 命名空间操作；`KUBECONFIG` 或 `FURNACE_TRACE_KUBECONFIG` 可指定配置文件。镜像需从集群可访问的仓库拉取，PVC 使用 `FURNACE_TRACE_STORAGE_CLASS` 指定的类，设为 `default` 时采用集群默认 StorageClass。

工作台、FUXA 管理员代理与二维码展示页使用 NodePort 31080/31081/31082；节点地址能否从客户端访问取决于集群网络与防火墙。内部服务通过 Kubernetes Service 通信。

FUXA 工程和 MQTT broker 按本机可信演示环境配置；不要直接暴露到公网。各自研内部 HTTP 接口必须带 SERVICE_TOKEN。界面不提供直接绕过 MES 的开门按钮。
