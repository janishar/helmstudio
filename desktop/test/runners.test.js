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
  process.env.HELM_HOME = path.join(home, 'helm'); // never the real ~/.helm/run
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
  assert.equal(a.procs.torch.spawnfile, path.join(p.venvs, 'runner', 'bin', 'helmstudio-runner'), 'runs under the helm name');
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(home, 'runners.json'))), { torch: old });

  a.stopping = true; // a is the killed app: it restarts nothing
  const b = new Runners({ p, cfg, settings: {} }); // the app again, a forgotten
  await b.start();
  assert.ok(!alive(old), 'the left-behind runner was stopped');
  assert.notEqual(b.procs.torch.pid, old);
  await b.stop();
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(home, 'runners.json'))), {});
});

test('a stop or start stops the plugin groups a killed native runner left, only in their plugin folders', async () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'helm-plugins-'));
  process.env.HELM_HOME = home;
  const { spawn } = require('node:child_process');
  const idle = (cwd) => spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { cwd, detached: true, stdio: 'ignore' });
  fs.mkdirSync(path.join(home, 'plugins', 'web'), { recursive: true });
  fs.mkdirSync(path.join(home, 'run'));
  const left = idle(path.join(home, 'plugins', 'web'));
  const other = idle(home); // a reused pid: not in its plugin's folder
  fs.writeFileSync(path.join(home, 'run', 'web.pid'), `${left.pid}`);
  fs.writeFileSync(path.join(home, 'run', 'x.pid'), `${other.pid}`);
  const gone = new Promise((r) => left.once('exit', r));
  try {
    await new Runners({ p: { home, venvs: path.join(home, 'venvs') }, cfg: {}, settings: {} }).reapPlugins();
    await gone;
    assert.equal(other.exitCode, null, 'the other process runs on');
    assert.deepEqual(fs.readdirSync(path.join(home, 'run')), []);
  } finally {
    left.kill();
    other.kill();
  }
});

// 2b: a stand-in runner home (node under the venv's python name) whose main.py is `script`
function standIn(script) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'helm-backoff-'));
  process.env.HELM_HOME = path.join(home, 'helm');
  const p = layout(home, home);
  fs.mkdirSync(path.join(p.venvs, 'runner', 'bin'), { recursive: true });
  fs.symlinkSync(process.execPath, path.join(p.venvs, 'runner', 'bin', 'python'));
  fs.mkdirSync(path.join(p.runner, 'runner_service'), { recursive: true });
  fs.writeFileSync(path.join(p.runner, 'runner_service', 'main.py'), script);
  const cfg = { profile: 'x', composeEnv: { HELM_SECRETS: 'secrets' }, runners: [{ env: 'torch', venv: 'runner', healthPort: 1 }], defaultModelsDir: home };
  return { home, p, cfg };
}
const until = async (ok) => {
  for (let i = 0; i < 300 && !ok(); i++) await new Promise((r) => setTimeout(r, 20));
  assert.ok(ok(), 'in time');
};

test('2b: a runner that stops is restarted with back-off, any exit code; after 5 in 10 minutes it is given up', async () => {
  const { p, cfg } = standIn('process.exit(3)');
  const waits = [];
  const a = new Runners({ p, cfg, settings: {}, backoff: (n) => (waits.push(n), 10) });
  await a.start();
  await until(() => a.issues.torch);
  assert.deepEqual(waits, [0, 1, 2, 3], 'four restarts, each one step further along the back-off');
  assert.equal(a.issues.torch.kind, 'nostart', 'every stop came within 5 s of its start');
  assert.equal(a.state().torch.kind, 'nostart');
  assert.match(a.issues.torch.lines.join('\n'), /exit 3[\s\S]*stopped 5 of 5 · not restarted/);
  await assert.rejects(a.waitReady(null, 5000), /The torch runner didn’t start/);
  a.restartOne('torch'); // Restart runner: the count reset, started now
  assert.equal(a.issues.torch, undefined);
  assert.equal(a.state().torch, 'run');
  await until(() => a.issues.torch);
  await a.stop();
});

test('2b: a runner that cannot be spawned is given up at once, with the cause', async () => {
  const { p, cfg } = standIn('');
  fs.rmSync(path.join(p.venvs, 'runner'), { recursive: true });
  const a = new Runners({ p, cfg, settings: {} });
  await a.start();
  assert.deepEqual(a.issues.torch && { kind: a.issues.torch.kind, cause: a.issues.torch.cause }, { kind: 'nostart', cause: 'Its Python environment is missing.' });
  assert.match(a.issues.torch.lines.at(-1), /spawn runner-torch: ENOENT/);
});

test('2b: a stop while a restart waits cancels it', async () => {
  const { p, cfg } = standIn('process.exit(0)');
  const a = new Runners({ p, cfg, settings: {}, backoff: () => 200 });
  await a.start();
  await until(() => a.state().torch.wait != null);
  await a.stop();
  await new Promise((r) => setTimeout(r, 300));
  assert.deepEqual(a.procs, {});
  assert.equal(a.state().torch, 'down');
});
