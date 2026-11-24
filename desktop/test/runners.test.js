// Runners: a start after the app was killed stops the runners it left behind (their pids in runners.json)
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { Runners, layout } = require('../src/stack');

test('a new start stops the runners a killed app left behind', async () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'helm-runners-'));
  process.env.HELMSTUDIO_HOME = home;
  const p = layout(home, home);
  // a stand-in runner: node under the venv's python name, running a main.py that idles
  fs.mkdirSync(path.join(p.venvs, 'runner', 'bin'), { recursive: true });
  fs.symlinkSync(process.execPath, path.join(p.venvs, 'runner', 'bin', 'python'));
  fs.mkdirSync(path.join(p.runner, 'runner_service'), { recursive: true });
  fs.writeFileSync(path.join(p.runner, 'runner_service', 'main.py'), 'setInterval(() => {}, 1000);');
  const cfg = { profile: 'x', composeEnv: { HELM_SECRETS: 'secrets' }, runners: [{ env: 'torch', venv: 'runner', healthPort: 1 }], defaultModelsDir: home };
  const alive = (pid) => { try { return process.kill(pid, 0); } catch { return false; } };

  const a = new Runners({ p, cfg, settings: {} });
  await a.start();
  const old = a.procs.torch.pid;
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(home, 'runners.json'))), { torch: old });

  const b = new Runners({ p, cfg, settings: {} }); // the app again, a forgotten
  await b.start();
  assert.ok(!alive(old), 'the left-behind runner was stopped');
  assert.notEqual(b.procs.torch.pid, old);
  await b.stop();
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(home, 'runners.json'))), {});
});
