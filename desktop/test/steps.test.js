// The step runner with faked commands: success, a failure then Try again resuming at that step, cancel, an ask;
// plus the images step (pull, or the developer switch's copy) and the admin form's checks
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { StepRunner, exec } = require('../src/steps');
const { validateAdmin, stackSteps, layout, imagesCfg } = require('../src/stack');
const cfg = require('../stack.json');

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), 'helm-steps-'));

function setup({ failOn = { value: null } } = {}) {
  const dir = tmp();
  const calls = [];
  const fake = async (cmd) => {
    calls.push(cmd);
    if (cmd === failOn.value) throw new Error(`${cmd}: no such host`);
    return 'ok';
  };
  const steps = ['a', 'b', 'c'].map((id) => ({ id, title: `Step ${id}`, hint: `hint ${id}`, run: (c) => c.run(id, []) }));
  const runner = new StepRunner({ steps, stateFile: path.join(dir, 'state.json'), logFile: path.join(dir, 'log'), exec: fake });
  return { dir, calls, runner, failOn };
}

test('runs every step in order and records them', async () => {
  const { runner, calls, dir } = setup({ failOn: { value: null } });
  assert.equal(await runner.run(), 'done');
  assert.deepEqual(calls, ['a', 'b', 'c']);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(dir, 'state.json'))).done, ['a', 'b', 'c']);
});

test('a failure stops there; Try again resumes at the failed step', async () => {
  const failOn = { value: 'b' };
  const { runner, calls } = setup({ failOn });
  assert.equal(await runner.run(), 'failed');
  const snap = runner.snapshot();
  assert.deepEqual(snap.steps.map((s) => s.status), ['done', 'fail', 'todo']);
  assert.equal(snap.error.title, 'Step b failed');
  assert.equal(snap.error.message, 'hint b');
  assert.match(snap.tail.at(-1), /no such host/);
  failOn.value = null;
  assert.equal(await runner.run(), 'done');
  assert.deepEqual(calls, ['a', 'b', 'b', 'c']); // a is not run again
});

test('a new runner on the same state file resumes too (app restarted)', async () => {
  const failOn = { value: 'c' };
  const { runner, dir } = setup({ failOn });
  await runner.run();
  const calls = [];
  const steps = ['a', 'b', 'c'].map((id) => ({ id, title: id, run: async () => calls.push(id) }));
  const again = new StepRunner({ steps, stateFile: path.join(dir, 'state.json'), logFile: path.join(dir, 'log') });
  assert.equal(await again.run(), 'done');
  assert.deepEqual(calls, ['c']);
});

test('cancel stops the running command and leaves the step to do', async () => {
  const dir = tmp();
  const steps = [{ id: 'x', title: 'X', run: (c) => c.run('sleep', ['30']) }];
  const runner = new StepRunner({ steps, stateFile: path.join(dir, 's.json'), logFile: path.join(dir, 'log'), exec });
  const done = runner.run();
  setTimeout(() => runner.cancel(), 100);
  const t0 = Date.now();
  assert.equal(await done, 'cancelled');
  assert.ok(Date.now() - t0 < 5000);
  assert.equal(runner.snapshot().steps[0].status, 'todo');
  assert.ok(!fs.existsSync(path.join(dir, 's.json')));
});

test('ask waits for the answer and never logs it', async () => {
  const dir = tmp();
  let got;
  const steps = [{ id: 'q', title: 'Q', run: async (c) => (got = await c.ask('admin')) }];
  const runner = new StepRunner({ steps, logFile: path.join(dir, 'log') });
  runner.on('update', (s) => s.ask && runner.answer('admin', { password: 'secret-value-123' }));
  assert.equal(await runner.run(), 'done');
  assert.equal(got.password, 'secret-value-123');
  assert.doesNotMatch(fs.readFileSync(path.join(dir, 'log'), 'utf8'), /secret-value/);
});

test('real exec: a failing command reports its last line', async () => {
  await assert.rejects(exec('/bin/sh', ['-c', 'echo first; echo "the reason" >&2; exit 3']), /the reason/);
});

test('admin account checks', () => {
  const ok = { email: 'a@b.co', password: 'twelve chars', repeat: 'twelve chars' };
  assert.equal(validateAdmin(ok), null);
  assert.match(validateAdmin({ ...ok, email: 'nope' }), /email/);
  assert.match(validateAdmin({ ...ok, password: 'short', repeat: 'short' }), /12/);
  assert.match(validateAdmin({ ...ok, repeat: 'other' }), /differ/);
  assert.match(validateAdmin({ ...ok, password: "it's twelve+", repeat: "it's twelve+" }), /quote/);
});

// the images step alone, on a StepRunner whose commands are faked: inVM(img) says whether the VM's Docker has it
function imagesRun({ inVM, onHost = () => true, local = false, manifest = false }) {
  const dir = tmp();
  process.env.HELMSTUDIO_HOME = dir;
  if (local) process.env.HELMSTUDIO_LOCAL_IMAGES = '1';
  else delete process.env.HELMSTUDIO_LOCAL_IMAGES;
  const steps = stackSteps({ p: layout(dir, dir), cfg, settings: {}, runners: {} }).first.filter((s) => s.id === 'images');
  delete process.env.HELMSTUDIO_LOCAL_IMAGES;
  const calls = [];
  const fake = async (cmd, args, { env, onLine }) => {
    calls.push({ args, vm: Boolean(env.DOCKER_HOST) });
    const img = args.at(-1);
    if (args[0] === 'image') {
      if (env.DOCKER_HOST ? !inVM.has(img) : !onHost(img)) throw new Error(`No such image: ${img}`);
      return 'sha256:1 500000000\n';
    }
    if (args[0] === 'manifest') {
      if (!manifest) throw new Error('no manifest');
      const layer = (d, size) => ({ digest: `sha256:${d.repeat(64)}`, size });
      return JSON.stringify([{ Descriptor: { platform: { architecture: 'arm64', os: 'linux' } }, OCIManifest: { layers: [layer('a', 400e6), layer('b', 200e6)] } }]);
    }
    if (args[0] === 'pull') {
      ['aaaaaaaaaaaa: Pulling fs layer', 'bbbbbbbbbbbb: Already exists', 'aaaaaaaaaaaa: Pull complete', `Status: Downloaded ${img}`].forEach(onLine);
      inVM.add(img);
    }
    return '';
  };
  const pipes = [];
  const pipe = async (a, b) => pipes.push([a.args, b.args, Boolean(a.env.DOCKER_HOST), Boolean(b.env.DOCKER_HOST)]);
  const runner = new StepRunner({ steps, logFile: path.join(dir, 'log'), exec: fake, pipe });
  return { runner, calls, pipes };
}

test('images: the released ones, pulled into the VM when missing', async () => {
  const [kong, auth] = cfg.images;
  const { runner, calls, pipes } = imagesRun({ inVM: new Set(cfg.images.filter((i) => i !== kong && i !== auth)) });
  const seen = [];
  runner.on('update', (s) => s.steps[0].progress != null && seen.push(s.steps[0].progress));
  assert.equal(await runner.run(), 'done');
  const pulls = calls.filter((c) => c.args[0] === 'pull');
  assert.deepEqual(pulls.map((c) => c.args), [['pull', kong], ['pull', auth]]);
  assert.ok(pulls.every((c) => c.vm)); // the VM's Docker; nothing asks this Mac's
  assert.ok(calls.every((c) => c.vm));
  assert.equal(pipes.length, 0);
  assert.deepEqual(seen.slice(0, 5), [0, 0, 0.25, 0.5, 0.5]); // per image, then per layer done
  assert.equal(runner.snapshot().steps[0].detail, '5.5 GB on disk'); // first open: all eleven images' own sizes
  assert.match(fs.readFileSync(runner.logFile, 'utf8'), /Pull complete/);
});

test('images: the released names carry the app version', () => {
  const { version } = require('../package.json');
  assert.equal(cfg.composeEnv.IMAGE_TAG, version);
  const ours = cfg.images.filter((i) => i.startsWith(cfg.composeEnv.IMAGE_PREFIX));
  assert.equal(ours.length, 8); // media-tools too (owner, 2026-11-25)
  assert.ok(ours.every((i) => i.endsWith(`:${version}`)));
});

test('images: HELMSTUDIO_LOCAL_IMAGES=1 copies make prod\'s images from this Mac instead', async () => {
  const local = imagesCfg(cfg, true);
  assert.equal(local.composeEnv.IMAGE_PREFIX, 'helmstudio/');
  assert.equal(local.composeEnv.IMAGE_TAG, 'prod');
  assert.ok(local.images.includes('helmstudio/kong:prod') && local.images.includes('postgres:18.7-alpine3.24'));
  const { runner, calls, pipes } = imagesRun({ inVM: new Set(local.images.filter((i) => i !== 'helmstudio/web:prod')), local: true });
  assert.equal(await runner.run(), 'done');
  assert.equal(calls.filter((c) => c.args[0] === 'pull').length, 0);
  assert.deepEqual(pipes, [[['save', 'helmstudio/web:prod'], ['load'], false, true]]);
});

test('images: byte progress from the registry\'s layer sizes', async () => {
  const [kong] = cfg.images;
  const { runner } = imagesRun({ inVM: new Set(cfg.images.filter((i) => i !== kong)), manifest: true });
  const seen = [];
  runner.on('update', (s) => s.steps[0].progress != null && seen.push([s.steps[0].progress, s.steps[0].detail]));
  assert.equal(await runner.run(), 'done');
  // bbbb… already there, then aaaa… pulled; first open labels download and on disk (stack.json's estimate, then measured)
  assert.deepEqual(seen.slice(0, 2).map(([p, d]) => [p.toFixed(2), d]), [
    ['0.33', `0.2 of 0.6 GB download · ${cfg.imagesDiskGB} GB on disk`],
    ['1.00', `0.6 of 0.6 GB download · ${cfg.imagesDiskGB} GB on disk`],
  ]);
  assert.equal(runner.snapshot().steps[0].detail, '0.6 GB download · 5.5 GB on disk');
});

test('an error with its own title shows its words over the step\'s hint (2e: under 2 GB at any start)', async () => {
  const dir = tmp();
  const steps = [{ id: 'vm', title: 'VM', hint: 'The VM did not start.', run: async () => { throw Object.assign(new Error('About 6 GB of free disk is needed; 1 GB is free.'), { title: 'Check this Mac failed' }); } }];
  const runner = new StepRunner({ steps, logFile: path.join(dir, 'log') });
  assert.equal(await runner.run(), 'failed');
  assert.deepEqual([runner.error.title, runner.error.message], ['Check this Mac failed', 'About 6 GB of free disk is needed; 1 GB is free.']);
});

test('ask with until answers itself when it holds (2d: the drive is back)', async () => {
  const dir = tmp();
  let back = false;
  let got;
  const steps = [{ id: 'q', title: 'Q', run: async (c) => (got = await c.ask('drive', {}, () => back)) }];
  const runner = new StepRunner({ steps, logFile: path.join(dir, 'log') });
  setTimeout(() => (back = true), 100);
  assert.equal(await runner.run(), 'done');
  assert.equal(got, 'auto');
});

// the every-open steps on a scratch home, the commands faked; free ports only (never the dev stack's or the app's)
async function everyRun({ settings, runners = { start: async () => {}, waitReady: async () => {} } }) {
  const net = require('node:net');
  const dir = tmp();
  process.env.HELMSTUDIO_HOME = dir;
  process.env.HELM_HOME = path.join(dir, 'helm');
  const free = () => new Promise((r) => { const s = net.createServer().listen(0, '127.0.0.1', () => { const { port } = s.address(); s.close(() => r(port)); }); });
  const c = structuredClone(cfg);
  for (const k of ['KONG_HTTP_PORT', 'POSTGRES_HOST_PORT', 'NATS_HOST_PORT', 'S3_HOST_PORT', 'S3_TLS_HOST_PORT']) c.composeEnv[k] = `${await free()}`;
  for (const r of c.runners) r.healthPort = await free();
  fs.mkdirSync(path.join(dir, 'res', 'stack', '.tools'), { recursive: true });
  const calls = [];
  const fake = async (cmd, args, o) => (calls.push({ cmd, args, env: o.env }), '');
  const s = { save() {}, ...settings };
  const steps = stackSteps({ p: layout(dir, path.join(dir, 'res')), cfg: c, settings: s, runners });
  return { dir, c, s, calls, steps, fake };
}

test('2c: a port another app holds asks for another; the picked one is saved and reaches compose and the studio address', async () => {
  const net = require('node:net');
  const { c, s, calls, steps, fake, dir } = await everyRun({ settings: {} });
  const kong = Number(c.composeEnv.KONG_HTTP_PORT);
  const held = net.createServer().listen(kong, '127.0.0.1');
  await new Promise((r) => held.once('listening', r));
  const runner = new StepRunner({ steps: steps.every.filter((x) => x.id === 'services'), logFile: path.join(dir, 'log'), exec: fake });
  let asked;
  let asks = 0;
  // first another of our own ports (asked again), then the suggested one
  runner.on('update', (snap) => {
    if (snap.ask?.kind !== 'port' || snap.steps[0].status !== 'ask') return;
    asked = snap.ask.data;
    if (++asks === 1) return runner.answer('port', c.composeEnv.POSTGRES_HOST_PORT);
    if (asks === 2) (held.close(), runner.answer('port', `${asked.suggest}`));
  });
  try {
    const out = await runner.run();
    assert.equal(out, 'done', JSON.stringify(runner.error));
  } finally {
    held.close();
  }
  assert.equal(asks, 2, 'a port another of ours uses is asked again');
  assert.deepEqual([asked.port, asked.holder, asked.label, asked.key], [kong, 'node', 'the studio', 'KONG_HTTP_PORT']);
  assert.ok(asked.suggest > kong);
  assert.deepEqual(s.ports, { KONG_HTTP_PORT: asked.suggest });
  assert.equal(c.studioUrl, `http://127.0.0.1:${asked.suggest}`);
  const up = calls.find((x) => x.args.includes('up'));
  assert.equal(up.env.KONG_HTTP_PORT, `${asked.suggest}`);
});

test('2d: a models folder on an unplugged drive asks; Continue starts the runners with what is there', async () => {
  let started = false;
  const { steps, fake, dir } = await everyRun({ settings: { modelsDir: '/Volumes/helm-test-not-there/models' }, runners: { start: async () => (started = true), waitReady: async () => {} } });
  const runner = new StepRunner({ steps: steps.every.filter((x) => x.id === 'runners'), logFile: path.join(dir, 'log'), exec: fake });
  let asked;
  runner.on('update', (snap) => snap.ask?.kind === 'drive' && !asked && ((asked = snap.ask.data), runner.answer('drive', 'continue')));
  assert.equal(await runner.run(), 'done');
  assert.deepEqual(asked, { volume: 'helm-test-not-there', dir: '/Volumes/helm-test-not-there/models' });
  assert.ok(started);
});
