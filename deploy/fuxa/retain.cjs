const fs = require('node:fs');
const file = '/usr/src/app/FUXA/server/runtime/devices/mqtt/index.js';
const before = 'const topicOptions = { retain: true };';
const after = 'const topicOptions = { retain: tags[key].options?.retain ?? true };';
const source = fs.readFileSync(file, 'utf8');
if (!source.includes(before)) throw new Error('FUXA MQTT source differs from v1.3.4; review the patch');
fs.writeFileSync(file, source.replace(before, after));
