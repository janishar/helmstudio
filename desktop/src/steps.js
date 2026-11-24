// The step runner (docs/plan/31-mac-app.md §35.2): a list of steps run in order, each a few commands with progress.
// Output goes to a log file; the ids of finished steps go to a state file, so a run after a failure (Try again) or a
// restart resumes at the step that failed. Commands run with spawn and an argument list, never a shell string.
const { spawn } = require('node:child_process');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const path = require('node:path');

// exec(cmd, args, {env, cwd, signal, onLine}) -> Promise<stdout>; rejects with the last output line on a non-zero exit
function exec(cmd, args, { env, cwd, signal, onLine = () => {}, stdin } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { env, cwd, signal, stdio: [stdin ? 'pipe' : 'ignore', 'pipe', 'pipe'] });
    let out = '';
    let last = '';
    const lines = (buf, keep) => {
      const s = buf.toString();
      if (keep) out += s;
      for (const l of s.split('\n')) if (l.trim()) onLine((last = l));
    };
    child.stdout.on('data', (b) => lines(b, true));
    child.stderr.on('data', (b) => lines(b, false));
    if (stdin) child.stdin.end(stdin);
    child.on('error', reject); // not found, or aborted (AbortError)
    child.on('close', (code, sig) =>
      code === 0 ? resolve(out) : reject(new Error(last || `${path.basename(cmd)} exited ${code ?? sig}`)),
    );
  });
}

// pipe: a | b, counting the bytes that pass (docker save | docker load with progress)
function pipe(a, b, { signal, onBytes = () => {}, onLine = () => {} } = {}) {
  return new Promise((resolve, reject) => {
    const pa = spawn(a.cmd, a.args, { env: a.env, signal, stdio: ['ignore', 'pipe', 'pipe'] });
    const pb = spawn(b.cmd, b.args, { env: b.env, signal, stdio: ['pipe', 'pipe', 'pipe'] });
    let last = '';
    const log = (buf) => buf.toString().split('\n').filter((l) => l.trim()).forEach((l) => onLine((last = l)));
    pa.stdout.on('data', (d) => onBytes(d.length));
    pa.stdout.pipe(pb.stdin);
    pb.stdin.on('error', () => {}); // b died first: its exit code reports it
    [pa.stderr, pb.stdout, pb.stderr].forEach((s) => s.on('data', log));
    const codes = [];
    const done = (i) => (code) => {
      codes[i] = code;
      if (codes.length === 2 && codes[0] !== undefined) {
        codes.every((c) => c === 0) ? resolve() : reject(new Error(last || 'the copy stopped'));
      }
    };
    pa.on('error', reject);
    pb.on('error', reject);
    pa.on('close', done(0));
    pb.on('close', done(1));
  });
}

class StepRunner extends EventEmitter {
  // steps: [{id, title, hint?, run: async (ctx) => {}}]; stateFile: null for steps that always run (every open)
  constructor({ steps, stateFile = null, logFile, exec: run = exec, pipe: runPipe = pipe }) {
    super();
    this.steps = steps.map((s) => ({ ...s, status: 'todo', detail: '', progress: null }));
    this.stateFile = stateFile;
    this.logFile = logFile;
    this.exec = run;
    this.pipe = runPipe;
    this.tail = [];
    this.error = null;
    this.asking = null;
    this.running = false;
  }

  done() {
    try {
      return new Set(JSON.parse(fs.readFileSync(this.stateFile, 'utf8')).done);
    } catch {
      return new Set();
    }
  }

  log(line) {
    this.tail = [...this.tail, line].slice(-6);
    fs.mkdirSync(path.dirname(this.logFile), { recursive: true });
    fs.appendFileSync(this.logFile, `${new Date().toISOString()} ${line}\n`);
  }

  snapshot() {
    const steps = this.steps.map(({ id, title, status, detail, progress }) => ({ id, title, status, detail, progress }));
    return { steps, error: this.error, ask: this.asking && { kind: this.asking.kind, data: this.asking.data }, tail: this.tail };
  }

  emitUpdate() {
    this.emit('update', this.snapshot());
  }

  // run every step not done yet; resolves 'done', 'failed' or 'cancelled'
  async run() {
    if (this.running) return 'running';
    this.running = true;
    this.abort = new AbortController();
    this.error = null;
    const finished = this.stateFile ? this.done() : new Set();
    for (const s of this.steps) s.status = finished.has(s.id) ? 'done' : 'todo';
    this.emitUpdate();
    try {
      for (const step of this.steps) {
        if (step.status === 'done') continue;
        step.status = 'run';
        step.progress = null;
        this.log(`== ${step.title}`);
        this.emitUpdate();
        try {
          await step.run(this.context(step));
        } catch (e) {
          if (this.abort.signal.aborted) {
            step.status = 'todo';
            this.log(`cancelled: ${step.title}`);
            this.emitUpdate();
            return 'cancelled';
          }
          step.status = 'fail';
          this.log(`failed: ${e.message}`);
          this.error = { step: step.id, title: `${step.title} failed`, message: step.hint || e.message, detail: e.message };
          this.emitUpdate();
          return 'failed';
        }
        step.status = 'done';
        step.progress = null;
        finished.add(step.id);
        if (this.stateFile) fs.writeFileSync(this.stateFile, JSON.stringify({ done: [...finished] }));
        this.emitUpdate();
      }
      return 'done';
    } finally {
      this.running = false;
      this.asking = null;
    }
  }

  cancel() {
    this.abort?.abort();
    if (this.asking) this.asking.reject(new Error('cancelled'));
  }

  // the UI's answer to ctx.ask
  answer(kind, value) {
    if (this.asking?.kind === kind) this.asking.resolve(value);
  }

  context(step) {
    const signal = this.abort.signal;
    const onLine = (l) => this.log(l);
    return {
      signal,
      log: onLine,
      detail: (text) => {
        step.detail = text;
        this.emitUpdate();
      },
      progress: (fraction, detail) => {
        step.progress = fraction;
        if (detail !== undefined) step.detail = detail;
        this.emitUpdate();
      },
      run: (cmd, args, opts = {}) => this.exec(cmd, args, { ...opts, signal, onLine: opts.quiet ? () => {} : onLine }),
      pipe: (a, b, opts = {}) => this.pipe(a, b, { ...opts, signal, onLine }),
      // waits for the UI (an admin account, a folder); data goes to the UI (e.g. an error from the last answer)
      ask: (kind, data = {}) =>
        new Promise((resolve, reject) => {
          if (signal.aborted) return reject(new Error('cancelled'));
          this.asking = { kind, data, resolve, reject };
          step.status = 'ask';
          this.emitUpdate();
        }).finally(() => {
          this.asking = null;
          step.status = 'run';
          this.emitUpdate();
        }),
    };
  }
}

module.exports = { StepRunner, exec, pipe };
