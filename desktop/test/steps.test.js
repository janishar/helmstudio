// The step runner with faked commands: success, a failure then Try again resuming at that step, cancel, an ask;
// plus the admin form's checks
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { StepRunner, exec } = require('../src/steps');
const { validateAdmin } = require('../src/stack');

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
