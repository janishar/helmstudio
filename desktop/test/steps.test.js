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
function imagesRun({ inVM, onHost = () => true, local = false }) {
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
  assert.equal(runner.snapshot().steps[0].detail, '1.0 GB');
  assert.match(fs.readFileSync(runner.logFile, 'utf8'), /Pull complete/);
});

test('images: the released names carry the app version', () => {
  const { version } = require('../package.json');
  assert.equal(cfg.composeEnv.IMAGE_TAG, version);
  const ours = cfg.images.filter((i) => i.startsWith(cfg.composeEnv.IMAGE_PREFIX));
  assert.equal(ours.length, 7);
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
