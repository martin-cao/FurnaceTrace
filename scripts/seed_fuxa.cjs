// Run inside the FUXA Pod, reading the versioned project from stdin.
let input = '';
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', async () => {
  const base = 'http://127.0.0.1:1881';
  const current = await fetch(base + '/api/project').then(r => r.json());
  if (current.devices?.['mqtt-lab'] && process.argv[1] !== '--replace-demo-project') {
    console.log('FUXA project already initialized');
    return;
  }
  const res = await fetch(base + '/api/project', {
    method: 'POST', headers: {'Content-Type': 'application/json'}, body: input
  });
  if (!res.ok) throw new Error('FUXA project import failed: ' + res.status);
  console.log('Imported furnace MQTT project');
});
