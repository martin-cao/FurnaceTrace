# 接口契约

先定义 `openapi.yaml`，再实现 Go 服务与 Vue 前端。已通过 OpenAPI 3.0.3 规范校验。

- 公共接口由 core 提供，除登录/注册外需要 Cookie 会话；内部接口通过 `x-service` 指定提供者。
- 独立二维码展示页（31082）复用既有 `POST /api/v1/auth/login`、`GET /api/v1/me`、`GET /api/v1/baskets` 和 `GET /api/v1/baskets/{basketNo}/qrcode`。由同源代理转发 Cookie 到 core；不新增字段、公开匿名台账接口或数据库连接。QR 内容仍为料筐编号，放行仍由 MES 判断。
- 内部接口使用 Bearer SERVICE_TOKEN；公共操作界面用于本机可信演示。
- 前端执行 `npm run api:generate --prefix web` 生成 `src/api/schema.d.ts`。
- Go wire types 位于 `internal/protocol`，字段 JSON 名称与规范一致。
- MQTT 使用 `DeviceState` 和 `DeviceCommand`；`mqtt.schema.json` 从它们导出，不另维护一套字段。
- 幂等写接口使用路径与 Idempotency-Key 作为作用域，并比较 JSON 内容；同一键不同内容返回冲突。
- core 与 notifier 的幂等记录持久化到 PostgreSQL。设备命令使用 commandId 和 bootId 幂等；设备重启后不接受旧 bootId。
- 扫码会话的去重在 vision 内存中；vision 重启后 core 根据会话超时进入人工补录，不把内存丢失当成完成。
- 用户操作的通用幂等作用域包含用户身份；配对码原子消费且不允许跨用户复用。个人通知只返回当前会话的用户记录。
- 管理员操作的 operator 从登录身份取得，忽略客户端手填值。
- SSE 是最新快照流，不是可重放审计日志。审计通过周期详情读取。

接口变更时，先修改 OpenAPI，再更新实现，并重新生成受影响类型。纯文档调整不需要重新运行整套服务测试。
