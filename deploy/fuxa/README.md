# FUXA v1.3.4 retain 修正

基于官方 `frangoteam/fuxa:1.3.4` 镜像，只替换 MQTT 驱动中的一行：

```diff
- const topicOptions = { retain: true };
+ const topicOptions = { retain: tags[key].options?.retain ?? true };
```

项目中的 command 点位显式设置 `options.retain=false`。状态点位保持默认行为。
HTTP 写入成功不等于执行完成，最终仍需设备命令编号与执行状态反馈。

FUXA 来源：https://github.com/frangoteam/FUXA/tree/v1.3.4
