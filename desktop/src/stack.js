// The concrete steps (docs/plan/31-mac-app.md §35.2): the VM (bundled Colima), the images (pulled into the VM's Docker
// from GitHub's registry and Docker Hub, with the bundled docker CLI), the secrets (a bundled .tools/make-secrets), the admin account, the runners (bundled uv,
// one venv per env) and the stack (compose in the VM). Everything the app writes lives under its home folder
// (~/Library/Application Support/helmstudio); the bundled files (Resources) are only read.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { exec } = require('./steps');

function layout(home, res) {
  // Lima's sockets (…/_lima/colima-<profile>/ssh.sock.<16 digits>) must stay under 104 characters: under
  // "Application Support" they are 110 for an 11-letter user name, so the VM lives in a short folder in the home
  // (not ~/.helmstudio: the reference app keeps its data there)
  const vm = process.env.HELMSTUDIO_HOME ? path.join(home, 'vm') : path.join(os.homedir(), '.helmstudio-vm');
  return {
    home,
    res,
    logs: path.join(home, 'logs'),
    state: path.join(home, 'first-open.json'),
    settings: path.join(home, 'settings.json'),
    vm, // COLIMA_HOME: ~/.helmstudio-vm (sockets under it: keep the path short)
    lima: path.join(vm, '_lima'), // LIMA_HOME, where Colima puts it
    dockerConfig: path.join(home, 'docker'), // DOCKER_CONFIG for the VM's Docker: the user's ~/.docker stays as it is
    stack: path.join(home, 'stack'), // .tools/make-secrets, deploy/ (compose files, secrets), auth_service/.env
    runner: path.join(home, 'runner'), // runner_service/ and contracts/, copied from Resources
    venvs: path.join(home, 'venvs'),
    python: path.join(home, 'python'),
    colima: path.join(res, 'vm', 'bin', 'colima'),
    uv: path.join(res, 'bin', 'uv'),
  };
}

const tilde = (p) => (p.startsWith('~/') ? path.join(os.homedir(), p.slice(2)) : p);

function loadSettings(p) {
  let s = {};
  try {
    s = JSON.parse(fs.readFileSync(p.settings, 'utf8'));
  } catch {}
  const settings = { keepRunning: true, ...s };
  Object.defineProperty(settings, 'save', {
    value: () => {
      fs.mkdirSync(p.home, { recursive: true });
      fs.writeFileSync(p.settings, JSON.stringify(settings, null, 2));
    },
  });
  return settings;
}

// An app opened from Finder gets a bare PATH: the bundled tools first (colima, docker, uv, helm), then the usual folders
function envs(p, cfg) {
  const PATH = [
    path.join(p.res, 'vm', 'bin'),
    path.join(p.res, 'bin'),
    '/usr/local/bin',
    '/opt/homebrew/bin',
    path.join(os.homedir(), '.orbstack', 'bin'),
    '/usr/bin',
    '/bin',
    '/usr/sbin',
    '/sbin',
  ].join(':');
  const base = { ...process.env, PATH };
  const vm = { ...base, COLIMA_HOME: p.vm, LIMA_HOME: p.lima, DOCKER_CONFIG: p.dockerConfig };
  const docker = { ...vm, DOCKER_HOST: `unix://${path.join(p.vm, cfg.profile, 'docker.sock')}` };
  return { base, host: base, vm, docker, compose: { ...docker, ...cfg.composeEnv } };
}

// the VM's Docker uses its own config folder; docker compose is the bundled plugin (Resources/docker/cli-plugins)
function dockerConfig(p) {
  fs.mkdirSync(p.dockerConfig, { recursive: true });
  const link = path.join(p.dockerConfig, 'cli-plugins');
  fs.rmSync(link, { force: true }); // an older app linked the user's ~/.docker/cli-plugins
  fs.symlinkSync(path.join(p.res, 'docker', 'cli-plugins'), link);
}

// HELMSTUDIO_LOCAL_IMAGES=1 (developers, unreleased builds): make prod's images (helmstudio/<svc>:prod) copied from
// this Mac's Docker into the VM, instead of the released ones pulled from GitHub's registry
function imagesCfg(cfg, local = process.env.HELMSTUDIO_LOCAL_IMAGES === '1') {
  if (!local) return cfg;
  const pre = cfg.composeEnv.IMAGE_PREFIX;
  const images = cfg.images.map((i) => (i.startsWith(pre) ? `helmstudio/${i.slice(pre.length).split(':')[0]}:prod` : i));
  return { ...cfg, images, local, composeEnv: { ...cfg.composeEnv, IMAGE_PREFIX: 'helmstudio/', IMAGE_TAG: 'prod' } };
}

// first open, the first open of a new version (none recorded before 0.1.0-alpha.2), or a plain start
const openMode = (settings, version) => (!settings.installed ? 'first' : settings.version !== version ? 'update' : 'start');

function validateAdmin(a) {
  if (!/^[^\s@']+@[^\s@']+\.[^\s@']+$/.test(a?.email || '')) return 'Enter an email address.';
  if ((a.password || '').length < 12) return 'The password needs at least 12 characters.';
  if (a.password !== a.repeat) return 'The passwords differ.';
  if (/['\n\r]/.test(a.password)) return 'The password cannot contain a quote or a line break.';
  return null;
}

const gb = (n) => (n / 1e9).toFixed(1);

// "38 of 112 MB", "1.2 of 2.0 GB"; with no first number only the total ("112 MB")
function sizeOf(n, total) {
  const [d, unit, digits] = total >= 1e9 ? [1e9, 'GB', 1] : [1e6, 'MB', 0];
  const f = (x) => (x / d).toFixed(digits);
  return n == null ? `${f(total)} ${unit}` : `${f(n)} of ${f(total)} ${unit}`;
}

// docker's sizes ("2.205GB", "512.3MB", "0B") to bytes
function bytesOf(s) {
  const m = /^([\d.]+)\s*([kKMGT]?)B$/.exec(s || '');
  return m ? Number(m[1]) * { '': 1, k: 1e3, K: 1e3, M: 1e6, G: 1e9, T: 1e12 }[m[2]] : 0;
}

// the layers for linux/arm64 from `docker manifest inspect -v` (one manifest, or a list per platform)
function layers(m) {
  const list = Array.isArray(m) ? m : m ? [m] : [];
  const one = list.length === 1 ? list[0] : list.find((x) => x.Descriptor?.platform?.architecture === 'arm64' && x.Descriptor?.platform?.os === 'linux');
  return (one?.OCIManifest || one?.SchemaV2Manifest)?.layers || [];
}

// image refs ("repo:tag") to remove after an update: ours by name (the registry prefix, or helmstudio/ from a
// developer build), never one in `keep` (the images this version runs), never a third-party or untagged one
function oldImages(refs, keep, prefix) {
  const k = new Set(keep);
  return refs.map((r) => r.trim()).filter((r) => r && !r.endsWith(':<none>') && (r.startsWith(prefix) || r.startsWith('helmstudio/')) && !k.has(r));
}

function stackSteps({ p, cfg: shipped, settings, runners }) {
  const cfg = imagesCfg(shipped);
  const e = envs(p, cfg);
  const compose = (args) => [
    'compose',
    '-p',
    cfg.project,
    ...cfg.composeFiles.flatMap((f) => ['-f', path.join(p.stack, f)]),
    ...args,
  ];

  const vm = async (c) => {
    dockerConfig(p);
    const up = await c.run(p.colima, ['status', '--profile', cfg.profile], { env: e.vm, quiet: true }).then(
      () => true,
      () => false,
    );
    if (!up) {
      const { cpus, memoryGB, diskGB } = cfg.vm;
      // vz, Docker, ports forwarded on 127.0.0.1 only (Lima's default; no --network-address)
      await c.run(
        p.colima,
        ['start', '--profile', cfg.profile, '--vm-type', 'vz', '--arch', 'aarch64', '--runtime', 'docker',
          '--cpu', `${cpus}`, '--memory', `${memoryGB}`, '--disk', `${diskGB}`, '--mount-type', 'virtiofs'],
        { env: e.vm },
      );
    }
    c.detail(`${cfg.vm.memoryGB} GB memory`);
  };

  // copies the shipped stack files (never the secrets or auth_service/.env) and makes the missing secrets
  const secrets = async (c) => {
    fs.cpSync(path.join(p.res, 'stack'), p.stack, { recursive: true });
    await c.run('/bin/sh', [path.join(p.stack, '.tools', 'make-secrets')], {
      env: { ...e.base, NATS_HOST_PORT: `${cfg.natsPort}`, HELM_SECRETS: cfg.composeEnv.HELM_SECRETS },
    });
  };

  const services = async (c) => {
    dockerConfig(p);
    await secrets(c);
    const t0 = Date.now();
    const tick = setInterval(() => c.detail(`${Math.round((Date.now() - t0) / 1000)} s`), 1000);
    try {
      await c.run('docker', compose(['up', '-d', '--wait']), { env: e.compose, cwd: p.stack });
    } finally {
      clearInterval(tick);
    }
  };

  const startRunners = async (c) => {
    await runners.start();
    await runners.waitReady(c.signal);
  };

  // the developer path (HELMSTUDIO_LOCAL_IMAGES=1): docker save on this Mac | docker load in the VM
  const copyImages = async (c, inspect) => {
    const todo = [];
    for (const img of cfg.images) {
      const [id, size] = await inspect(img, e.host).catch(() => {
        throw new Error(`${img} is not in Docker on this Mac. Build the images first (make prod).`);
      });
      const [vmId] = await inspect(img, e.docker).catch(() => []);
      if (vmId !== id) todo.push({ img, size: Number(size) });
    }
    const total = todo.reduce((n, t) => n + t.size, 0);
    let done = 0;
    for (const t of todo) {
      c.log(`load ${t.img}`);
      await c.pipe(
        { cmd: 'docker', args: ['save', t.img], env: e.host },
        { cmd: 'docker', args: ['load'], env: e.docker },
        { onBytes: (n) => c.progress(Math.min((done += n) / total, 1), `${gb(Math.min(done, total))} of ${gb(total)} GB`) },
      );
    }
    c.detail(total ? `${gb(total)} GB` : 'up to date');
  };

  const inspect = (c, img, env) =>
    c.run('docker', ['image', 'inspect', '-f', '{{.Id}} {{.Size}}', img], { env, quiet: true }).then((o) => o.trim().split(' '));

  // docker pull with byte progress: each layer's size from the registry (docker manifest inspect -v), counted when
  // docker says that layer is done (its 12-letter id is the start of its digest); without sizes, by layer count
  const images = async (c) => {
    if (cfg.local) return copyImages(c, (img, env) => inspect(c, img, env));
    const todo = [];
    for (const img of cfg.images) if (!(await inspect(c, img, e.docker).catch(() => null))) todo.push(img);
    const sizes = new Map();
    for (const img of todo) {
      const m = await c.run('docker', ['manifest', 'inspect', '-v', img], { env: e.docker, quiet: true }).then((o) => JSON.parse(o)).catch(() => null);
      for (const l of layers(m)) sizes.set(l.digest.replace(/^sha256:/, '').slice(0, 12), l.size);
    }
    const total = [...sizes.values()].reduce((a, b) => a + b, 0);
    let bytes = 0;
    let pulled = 0; // without sizes: the pulled images' sizes, for the detail
    const counted = new Set();
    for (const [i, img] of todo.entries()) {
      const seen = new Map(); // layer id -> done, for the count when there are no sizes
      if (!total) c.progress(i / todo.length, `${i + 1} of ${todo.length} images`);
      await c.run('docker', ['pull', img], {
        env: e.docker,
        onLine: (l) => {
          const m = /^(\w{12}): (.*)/.exec(l);
          if (!m) return;
          const done = /Pull complete|Already exists/.test(m[2]);
          if (total) {
            if (!done || counted.has(m[1])) return;
            counted.add(m[1]);
            bytes += sizes.get(m[1]) || 0;
            return c.progress(Math.min(bytes / total, 1), sizeOf(Math.min(bytes, total), total));
          }
          seen.set(m[1], done);
          c.progress((i + [...seen.values()].filter(Boolean).length / seen.size) / todo.length);
        },
      });
      if (!total) pulled += Number((await inspect(c, img, e.docker))[1]);
    }
    c.detail(todo.length ? sizeOf(null, total || pulled) : 'up to date');
  };
  const imagesHint = cfg.local ? undefined : 'The images could not be downloaded. The internet is needed for this step.';

  // the runner code from Resources and one venv per env (uv sync); asks for the models folder the first time
  const setupRunners = async (c) => {
    if (!settings.modelsDir) {
      const dir = tilde(await c.ask('models', { dir: cfg.defaultModelsDir }));
      fs.mkdirSync(dir, { recursive: true });
      settings.modelsDir = dir;
      settings.save();
    }
    fs.rmSync(p.runner, { recursive: true, force: true });
    fs.cpSync(path.join(p.res, 'runner'), p.runner, { recursive: true });
    // --managed-python: uv's own Python in the app's folder, never a Homebrew one an upgrade would break
    const n = cfg.venvs.length;
    for (const [i, v] of cfg.venvs.entries()) {
      c.progress(i / n, `${i + 1} of ${n}`);
      await c.run(
        p.uv,
        ['sync', '--frozen', '--no-dev', '--managed-python', '--project', path.join(p.runner, v.project), ...v.extras.flatMap((x) => ['--extra', x])],
        { env: { ...e.base, UV_PROJECT_ENVIRONMENT: path.join(p.venvs, v.name), UV_PYTHON_INSTALL_DIR: p.python } },
      );
    }
    c.detail('');
  };

  // after an update, once the new version runs: our images not in use any more (an older tag, or the helmstudio/*
  // ones a developer build loaded), then the dangling layers
  const removeOld = async (c) => {
    const df = () =>
      c.run('docker', ['system', 'df', '--format', '{{.Type}} {{.Size}}'], { env: e.docker, quiet: true }).then(
        (o) => bytesOf(/^Images (\S+)/m.exec(o)?.[1]),
        () => 0,
      );
    const before = await df();
    const refs = await c.run('docker', ['image', 'ls', '--format', '{{.Repository}}:{{.Tag}}'], { env: e.docker, quiet: true });
    for (const ref of oldImages(refs.split('\n'), [...shipped.images, ...cfg.images], shipped.composeEnv.IMAGE_PREFIX)) {
      await c.run('docker', ['image', 'rm', ref], { env: e.docker }).catch((err) => c.log(`kept ${ref}: ${err.message}`));
    }
    await c.run('docker', ['image', 'prune', '-f'], { env: e.docker });
    const freed = Math.max(before - (await df()), 0);
    c.detail(freed ? `${sizeOf(null, freed)} freed` : '');
  };

  const first = [
    {
      id: 'check',
      title: 'Check this Mac',
      run: async (c) => {
        if (process.arch !== 'arm64') throw new Error('helmstudio needs a Mac with Apple Silicon.');
        const ver = (await c.run('/usr/bin/sw_vers', ['-productVersion'])).trim();
        if (parseInt(ver, 10) < cfg.minMacOS) throw new Error(`helmstudio needs macOS ${cfg.minMacOS} or later.`);
        fs.mkdirSync(p.home, { recursive: true });
        const st = fs.statfsSync(p.home);
        const free = (st.bavail * st.bsize) / 1e9;
        if (free < cfg.minFreeGB) throw new Error(`About ${cfg.minFreeGB} GB of free disk is needed; ${free.toFixed(0)} GB is free.`);
        c.detail(`Apple Silicon · macOS ${ver} · ${free.toFixed(0)} GB free`);
      },
    },
    { id: 'vm', title: 'Start the VM', hint: 'The VM did not start.', run: vm },
    { id: 'images', title: 'Load the images', hint: imagesHint, run: images },
    {
      id: 'secrets',
      title: 'Make the secrets',
      hint: 'The secrets could not be made. Show log says why.',
      run: async (c) => {
        await secrets(c);
        const envFile = path.join(p.stack, 'auth_service', '.env'); // compose's env_file for auth (../auth_service/.env)
        if (fs.existsSync(envFile) && /^ADMIN_EMAIL=/m.test(fs.readFileSync(envFile, 'utf8'))) return;
        let error = null;
        for (;;) {
          const a = await c.ask('admin', { error });
          error = validateAdmin(a);
          if (!error) {
            fs.mkdirSync(path.dirname(envFile), { recursive: true });
            fs.writeFileSync(envFile, `ADMIN_EMAIL='${a.email}'\nADMIN_PASSWORD='${a.password}'\n`, { mode: 0o600 });
            c.log('admin account saved'); // never the values
            return;
          }
        }
      },
    },
    {
      id: 'runners',
      title: 'Set up the runners',
      hint: 'Setting up the runners failed. The internet is needed for this step (about 2 GB).',
      run: setupRunners,
    },
    {
      id: 'start',
      title: 'Start',
      hint: 'The stack or the runners did not start. Show log says why.',
      run: async (c) => {
        await services(c);
        await startRunners(c);
      },
    },
  ];

  const every = [
    { id: 'vm', title: 'VM', hint: 'The VM did not start.', run: vm },
    { id: 'services', title: 'Services', hint: 'The services did not start.', run: services },
    { id: 'runners', title: 'Runners', hint: 'The runners did not start.', run: startRunners },
  ];

  // the first open of a new version: its images (before the services), its runner code and packages, and once it
  // all runs, the old images; a failure before that keeps them
  const update = [
    every[0],
    { id: 'images', title: 'Update the images', hint: imagesHint, run: images },
    every[1],
    { ...every[2], run: async (c) => (await setupRunners(c), await startRunners(c)) },
    { id: 'clean', title: 'Remove old images', hint: 'The old images could not be removed. Show log says why.', run: removeOld },
  ];

  // Quit: the runners, the stack (data stays) and the VM
  const stopAll = async (run) => {
    await runners.stop();
    await run('docker', compose(['down']), { env: e.compose, cwd: p.stack }).catch(() => {});
    await run(p.colima, ['stop', '--profile', cfg.profile], { env: e.vm }).catch(() => {});
  };

  return { first, every, update, stopAll, envs: e };
}

// Activity Monitor names a process after its binary, and the venv's python is a symlink to uv's python3.14: so the
// runners run a hard link (a copy across volumes) of that binary named helmstudio-runner, refreshed when uv's python
// changes. Python finds the venv by the pyvenv.cfg beside its bin folder, whatever the name
function runnerExe(bin) {
  const real = fs.realpathSync(path.join(bin, 'python'));
  const exe = path.join(bin, 'helmstudio-runner');
  const a = fs.statSync(real);
  const b = fs.statSync(exe, { throwIfNoEntry: false });
  if (b && b.size === a.size && b.mtimeMs === a.mtimeMs) return exe;
  const tmp = `${exe}.${process.pid}`;
  try {
    fs.linkSync(real, tmp);
  } catch {
    fs.copyFileSync(real, tmp);
    fs.utimesSync(tmp, a.atime, a.mtime);
  }
  fs.renameSync(tmp, exe);
  return exe;
}

// The native runners as `make runners` starts them (root Makefile): one process per env with its env vars, restarted
// after a stall (exit 70) or a SIGKILL (jetsam), stopped with SIGTERM. Their pids go to runners.json, so a start after
// the app was killed or crashed first stops the runners it left behind (else they hold the health ports, unstoppable)
class Runners {
  constructor({ p, cfg, settings, onChange = () => {} }) {
    Object.assign(this, { p, cfg, settings, onChange, procs: {}, stopping: false, pidFile: path.join(p.home, 'runners.json') });
  }

  savePids() {
    fs.mkdirSync(this.p.home, { recursive: true });
    fs.writeFileSync(this.pidFile, JSON.stringify(Object.fromEntries(Object.entries(this.procs).map(([k, c]) => [k, c.pid]))));
  }

  // the runners a killed app left: only pids whose command is still one of our venvs' runner (a pid can be reused)
  async reap() {
    let old = {};
    try {
      old = JSON.parse(fs.readFileSync(this.pidFile, 'utf8'));
    } catch {}
    const mine = new Set(Object.values(this.procs).map((c) => c.pid));
    const alive = (pid) => {
      try {
        return process.kill(pid, 0);
      } catch {
        return false;
      }
    };
    for (const pid of Object.values(old)) {
      if (mine.has(pid)) continue;
      const cmd = await exec('/bin/ps', ['-p', `${pid}`, '-o', 'command=']).catch(() => '');
      if (!cmd.includes(this.p.venvs)) continue;
      process.kill(pid, 'SIGTERM');
      for (let i = 0; i < 100 && alive(pid); i++) await new Promise((r) => setTimeout(r, 100));
      if (alive(pid)) process.kill(pid, 'SIGKILL');
    }
  }

  env(r) {
    const s = path.join(this.p.stack, 'deploy', this.cfg.composeEnv.HELM_SECRETS);
    return {
      ...envs(this.p, this.cfg).base,
      RUNNER_HOST: 'mac',
      RUNNER_ENV: r.env,
      HEALTH_PORT: `${r.healthPort}`,
      NATS_URL_FILE: path.join(s, `nats_runner_mac_${r.env}_url`),
      S3_ENDPOINT: `http://127.0.0.1:${this.cfg.s3Port}`,
      S3_BUCKET: this.cfg.s3Bucket,
      S3_ACCESS_KEY_ID_FILE: path.join(s, 's3_access_key_id'),
      S3_SECRET_ACCESS_KEY_FILE: path.join(s, 's3_secret_access_key'),
      HELMSTUDIO_MODELS: this.settings.modelsDir || tilde(this.cfg.defaultModelsDir),
      CONTRACTS_DIR: path.join(this.p.runner, 'contracts'),
    };
  }

  async start() {
    this.stopping = false;
    await this.reap();
    for (const r of this.cfg.runners) if (!this.procs[r.env]) this.spawn(r);
  }

  spawn(r) {
    fs.mkdirSync(this.p.logs, { recursive: true });
    const out = fs.openSync(path.join(this.p.logs, `runner-${r.env}.log`), 'a');
    const child = spawn(runnerExe(path.join(this.p.venvs, r.venv, 'bin')), ['main.py'], {
      cwd: path.join(this.p.runner, 'runner_service'),
      env: this.env(r),
      stdio: ['ignore', out, out],
    });
    fs.closeSync(out);
    this.procs[r.env] = child;
    this.savePids();
    child.on('error', () => {}); // reported by exit
    child.on('exit', (code, sig) => {
      if (this.procs[r.env] !== child) return;
      delete this.procs[r.env];
      this.savePids();
      if (!this.stopping && (code === 70 || code === 137 || sig === 'SIGKILL')) setTimeout(() => !this.stopping && !this.procs[r.env] && this.spawn(r), 2000);
      this.onChange();
    });
    this.onChange();
  }

  alive() {
    return this.cfg.runners.every((r) => this.procs[r.env]);
  }

  async stop() {
    this.stopping = true;
    const live = Object.values(this.procs);
    await Promise.all(
      live.map(
        (ch) =>
          new Promise((resolve) => {
            const t = setTimeout(() => ch.kill('SIGKILL'), 10000);
            ch.once('exit', () => (clearTimeout(t), resolve()));
            ch.kill('SIGTERM');
          }),
      ),
    );
    this.procs = {};
    this.savePids();
  }

  async restart() {
    await this.stop();
    await this.start();
  }

  healthy() {
    return Promise.all(
      this.cfg.runners.map((r) =>
        fetch(`http://127.0.0.1:${r.healthPort}/health`, { signal: AbortSignal.timeout(2000) }).then((x) => x.ok, () => false),
      ),
    ).then((all) => all.every(Boolean));
  }

  async waitReady(signal, ms = 180000) {
    const end = Date.now() + ms;
    while (!(await this.healthy())) {
      if (signal?.aborted) throw new Error('cancelled');
      if (Date.now() > end) throw new Error(`The runners did not answer on their health ports: see ${this.p.logs}/runner-*.log`);
      await new Promise((r) => setTimeout(r, 1000));
    }
  }
}

module.exports = { layout, loadSettings, stackSteps, Runners, validateAdmin, envs, imagesCfg, openMode, oldImages, layers, sizeOf, bytesOf };
