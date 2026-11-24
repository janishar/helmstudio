// The concrete steps (docs/plan/31-mac-app.md §35.2, decision 11): the VM (bundled Colima), the images (from this
// Mac's Docker into the VM's), the secrets (a bundled .tools/make-secrets), the admin account, the runners (bundled uv,
// one venv per env) and the stack (compose in the VM). Everything the app writes lives under its home folder
// (~/Library/Application Support/helmstudio); the bundled files (Resources) are only read.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');

function layout(home, res) {
  const vm = path.join(home, 'vm');
  return {
    home,
    res,
    logs: path.join(home, 'logs'),
    state: path.join(home, 'first-open.json'),
    settings: path.join(home, 'settings.json'),
    vm, // COLIMA_HOME (sockets under it: keep the path short)
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

// An app opened from Finder gets a bare PATH: the bundled tools first, then where Docker's CLI usually is
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

// the VM's Docker uses its own config folder; docker compose is found through the user's CLI plugins
function dockerConfig(p) {
  fs.mkdirSync(p.dockerConfig, { recursive: true });
  const plugins = path.join(os.homedir(), '.docker', 'cli-plugins');
  const link = path.join(p.dockerConfig, 'cli-plugins');
  if (fs.existsSync(plugins) && !fs.existsSync(link)) fs.symlinkSync(plugins, link);
}

function validateAdmin(a) {
  if (!/^[^\s@']+@[^\s@']+\.[^\s@']+$/.test(a?.email || '')) return 'Enter an email address.';
  if ((a.password || '').length < 12) return 'The password needs at least 12 characters.';
  if (a.password !== a.repeat) return 'The passwords differ.';
  if (/['\n\r]/.test(a.password)) return 'The password cannot contain a quote or a line break.';
  return null;
}

const gb = (n) => (n / 1e9).toFixed(1);

function stackSteps({ p, cfg, settings, runners }) {
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
    runners.start();
    await runners.waitReady(c.signal);
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
        await c.run('docker', ['--version'], { env: e.host }).catch(() => {
          throw new Error('Docker is not on this Mac: install OrbStack or Docker, then try again.');
        });
        c.detail(`Apple Silicon · macOS ${ver} · ${free.toFixed(0)} GB free`);
      },
    },
    { id: 'vm', title: 'Start the VM', hint: 'The VM did not start.', run: vm },
    {
      id: 'images',
      title: 'Load the images',
      run: async (c) => {
        const inspect = (img, env) =>
          c.run('docker', ['image', 'inspect', '-f', '{{.Id}} {{.Size}}', img], { env, quiet: true }).then((o) => o.trim().split(' '));
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
      },
    },
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
      run: async (c) => {
        if (!settings.modelsDir) {
          const dir = tilde(await c.ask('models', { dir: cfg.defaultModelsDir }));
          fs.mkdirSync(dir, { recursive: true });
          settings.modelsDir = dir;
          settings.save();
        }
        fs.rmSync(p.runner, { recursive: true, force: true });
        fs.cpSync(path.join(p.res, 'runner'), p.runner, { recursive: true });
        const n = cfg.venvs.length;
        for (const [i, v] of cfg.venvs.entries()) {
          c.progress(i / n, `${i + 1} of ${n}`);
          await c.run(
            p.uv,
            ['sync', '--frozen', '--no-dev', '--project', path.join(p.runner, v.project), ...v.extras.flatMap((x) => ['--extra', x])],
            { env: { ...e.base, UV_PROJECT_ENVIRONMENT: path.join(p.venvs, v.name), UV_PYTHON_INSTALL_DIR: p.python } },
          );
        }
        c.detail('');
      },
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

  // Quit entirely: the runners, the stack (data stays) and the VM
  const stopAll = async (run) => {
    await runners.stop();
    await run('docker', compose(['down']), { env: e.compose, cwd: p.stack }).catch(() => {});
    await run(p.colima, ['stop', '--profile', cfg.profile], { env: e.vm }).catch(() => {});
  };

  return { first, every, stopAll, envs: e };
}

// The native runners as `make runners` starts them (root Makefile): one process per env with its env vars, restarted
// after a stall (exit 70) or a SIGKILL (jetsam), stopped with SIGTERM
class Runners {
  constructor({ p, cfg, settings, onChange = () => {} }) {
    Object.assign(this, { p, cfg, settings, onChange, procs: {}, stopping: false });
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

  start() {
    this.stopping = false;
    for (const r of this.cfg.runners) if (!this.procs[r.env]) this.spawn(r);
  }

  spawn(r) {
    fs.mkdirSync(this.p.logs, { recursive: true });
    const out = fs.openSync(path.join(this.p.logs, `runner-${r.env}.log`), 'a');
    const child = spawn(path.join(this.p.venvs, r.venv, 'bin', 'python'), ['main.py'], {
      cwd: path.join(this.p.runner, 'runner_service'),
      env: this.env(r),
      stdio: ['ignore', out, out],
    });
    fs.closeSync(out);
    this.procs[r.env] = child;
    child.on('error', () => {}); // reported by exit
    child.on('exit', (code, sig) => {
      if (this.procs[r.env] !== child) return;
      delete this.procs[r.env];
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
  }

  async restart() {
    await this.stop();
    this.start();
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

module.exports = { layout, loadSettings, stackSteps, Runners, validateAdmin, envs };
