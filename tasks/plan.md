# 入炉追溯系统实施计划（历史记录）

已批准范围：Go 静态服务、Vue 静态前端、RTSP 扫码、MQTT 设备模拟、FUXA 必经设备链路、PostgreSQL、独立 Telegram 通知、OrbStack Kubernetes。只做入炉；不做水压、出炉或真实 PLC。真摄像头与真实 Telegram 凭据属于后续外部验收条件。

## 实施顺序

1. **先确定 `api/openapi.yaml`**：公共 API、内部 API、消息结构、状态与错误、幂等规则；校验规范后才开发前后端。
2. 通信骨架：设备模拟器、Mosquitto、FUXA 1.3.4 retain 补丁、SCADA 适配器；验证真实读写及重连。
3. 视频：摄像头模拟器 → MediaMTX → FFmpeg → gozxing；验证二维码来自 RTSP 解码。
4. 入炉：持久状态机、MES 模拟、命令确认、复位、事件与报警。
5. 通知与界面：独立持久通知服务、Vue 工作台、SSE、HLS、人工补录和审计。
6. 必要验证：真实入炉闭环，以及直接涉及实现的重复命令、掉线和未知反馈处理。遵循 AGENTS.md，不默认做连续 20 次或完整故障矩阵。
7. 交付：Kustomize、启动/停止/演示脚本、FUXA 工程、OpenAPI、运行证据与操作文档。

## 不可破坏的约束

- core 不连接 MQTT；所有传感器/执行器数据经 FUXA，RTSP 扫码例外。
- 一台炉子最多一个活动周期；命令以 commandId 去重，绑定 bootId 并设有效期。
- 设备反馈证实完成才能归档成功；未知执行结果进入人工处理。
- 命令不能 retained；设备重启不能执行旧命令。
- 业务事件与通知 outbox 同一事务；通知失败不阻塞流程。
- 显式使用 OrbStack kubeconfig、context=orbstack、namespace=iot-lab；不切换全局 context，也不操作其他集群。
- 停止服务保留 PVC，演示数据重置独立执行。

## 验收边界

实际运行与静态测试分开记录。真实 IP 摄像头待用户后续接入；没有 Token/Chat ID 时 Telegram 只做可重复的模拟 HTTP 服务测试，不宣称真实发送成功。
