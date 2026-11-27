// The concrete steps (docs/plan/31-mac-app.md §35.2): the VM (bundled Colima), the images (pulled into the VM's Docker
// from GitHub's registry and Docker Hub, with the bundled docker CLI), the secrets (a bundled .tools/make-secrets), the admin account, the runners (bundled uv,
// one venv per env) and the stack (compose in the VM). Everything the app writes lives under its home folder
// (~/Library/Application Support/helmstudio); the bundled files (Resources) are only read.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const { exec } = require('./steps');
const rules = require('./rules');

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

// 2e: the lowest free space of these folders' disks, in GB, and that disk's name ("Macintosh HD", "T9")
function freeDisk(dirs) {
  let low = null;
  for (const dir of dirs) {
    let st;
    try {
      st = fs.statfsSync(tilde(dir));
    } catch {
      continue; // not there (an unplugged drive: 2d says it)
    }
    const free = (st.bavail * st.bsize) / 1e9;
    if (!low || free < low.free) low = { free, name: volumeName(tilde(dir)) };
  }
  return low;
}

// the boot volume is the /Volumes entry that points at /
function volumeName(dir) {
  const m = /^\/Volumes\/([^/]+)/.exec(dir);
  if (m) return m[1];
  try {
    return fs.readdirSync('/Volumes').find((v) => fs.realpathSync(path.join('/Volumes', v)) === '/') || 'this Mac';
  } catch {
    return 'this Mac';
  }
}

// 2c: who holds a host port: null when it is free (on 127.0.0.1 and on every address), else the app's name from lsof
// (null when lsof cannot see it: another user's) and whether it is ours (our VM's port forwarder or our runners: the
// command runs from one of `mine`)
const bindable = (host, port) =>
  new Promise((resolve) => {
    const s = net.createServer();
    s.once('error', () => resolve(false));
    s.listen(port, host, () => s.close(() => resolve(true)));
  });
async function portHolder(port, mine) {
  if ((await bindable('127.0.0.1', port)) && (await bindable('0.0.0.0', port))) return null;
  const h = rules.lsofHolder(await exec('/usr/sbin/lsof', ['-nP', `-iTCP:${port}`, '-sTCP:LISTEN', '-Fpc']).catch(() => ''));
  const cmd = h ? await exec('/bin/ps', ['-p', `${h.pid}`, '-o', 'command=']).catch(() => '') : '';
  return { name: h?.name || null, ours: mine.some((m) => cmd.includes(m)) };
}

// the last n lines of a log file (its last 16 KB)
function tailOf(file, n) {
  try {
    const fd = fs.openSync(file, 'r');
    const size = fs.fstatSync(fd).size;
    const buf = Buffer.alloc(Math.min(size, 16384));
    fs.readSync(fd, buf, 0, buf.length, size - buf.length);
    fs.closeSync(fd);
    return buf.toString().split('\n').filter((l) => l.trim()).slice(-n);
  } catch {
    return [];
  }
}

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
  let e = envs(p, cfg); // made again when a port changes (2c)
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

  // 2c: before the services and the runners start, every host port they use is free or ours; one another app holds
  // asks for another (saved in settings.ports: the stack, the runners and the studio's address follow it)
  const checkPorts = async (c) => {
    const mine = [p.vm, p.venvs, p.res, p.home];
    for (;;) {
      const clash = await rules.portClash(rules.hostPorts(cfg), (n) => portHolder(n, mine));
      if (!clash) return;
      c.log(`port ${clash.port} is in use by ${clash.holder}`);
      const port = Number(await c.ask('port', clash));
      // Try again with the same port checks again; one another of ours uses would clash with it
      if (!rules.validPort(port) || rules.hostPorts(cfg).some((x) => x.port === port)) continue;
      settings.ports = { ...settings.ports, [clash.key]: port };
      settings.save();
      rules.applyPorts(shipped, settings.ports);
      rules.applyPorts(cfg, settings.ports); // a copy in the developer path (imagesCfg)
      e = envs(p, cfg);
      c.log(`${clash.label}: port ${port}`);
      // the Mac runners' NATS URLs carry the port: make-secrets writes them again (their passwords stay)
      const dir = path.join(p.stack, 'deploy', cfg.composeEnv.HELM_SECRETS);
      if (clash.key === 'NATS_HOST_PORT' && fs.existsSync(dir))
        for (const f of fs.readdirSync(dir, { withFileTypes: true }).filter((d) => /^nats_runner_mac_.*_url$/.test(d.name)))
          fs.rmSync(path.join(dir, f.name), { force: true });
    }
  };

  // extra: compose up's flags (a restart recreates the containers)
  const services = async (c, extra = []) => {
    dockerConfig(p);
    await checkPorts(c);
    await secrets(c);
    // API 1.64: ~/.helm/plugins (the user's plugins, mounted read-only into studio) exists before compose mounts it
    const helm = process.env.HELM_HOME || path.join(os.homedir(), '.helm');
    fs.mkdirSync(path.join(helm, 'plugins'), { recursive: true });
    fs.chmodSync(helm, 0o700);
    const t0 = Date.now();
    const tick = setInterval(() => c.detail(`${Math.round((Date.now() - t0) / 1000)} s`), 1000);
    try {
      await c.run('docker', compose(['up', '-d', '--wait', ...extra]), { env: e.compose, cwd: p.stack });
    } finally {
      clearInterval(tick);
    }
  };

  // 2d: the models folder's drive is unplugged: Continue starts with what is there, Choose folder… picks another, and
  // the start goes on by itself when the drive is back
  const checkDrive = async (c) => {
    const gone = () => rules.missingVolume(settings.modelsDir, fs.existsSync);
    for (let volume = gone(); volume; volume = gone()) {
      c.log(`the models folder ${settings.modelsDir} is on ${volume}, not connected`);
      const a = await c.ask('drive', { volume, dir: settings.modelsDir }, () => !gone());
      if (typeof a?.dir !== 'string' || !path.isAbsolute(tilde(a.dir))) return; // Continue, or the drive is back
      settings.modelsDir = tilde(a.dir);
      settings.save();
    }
  };

  // 2e: at every start, the system disk and the models folder's disk
  const checkDisk = async (c) => {
    const d = freeDisk([p.home, settings.modelsDir || cfg.defaultModelsDir]);
    const rule = d ? rules.diskRule(d.free, settings.diskWarnedAt, Date.now()) : 'ok';
    if (rule === 'stop')
      throw Object.assign(new Error(`About ${cfg.minFreeGB} GB of free disk is needed; ${d.free.toFixed(0)} GB is free.`), { title: 'Check this Mac failed' });
    if (rule !== 'warn') return;
    settings.diskWarnedAt = Date.now();
    settings.save();
    await c.ask('disk', { free: Math.floor(d.free), disk: d.name });
  };

  const startRunners = async (c) => {
    await checkDrive(c);
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
  // first open labels the sizes: "0.4 of 0.6 GB download · 2.5 GB on disk" (on disk: stack.json's estimate while
  // pulling, then the pulled images' own sizes); an update shows the download only
  const images = async (c, first) => {
    if (cfg.local) return copyImages(c, (img, env) => inspect(c, img, env));
    const disk = first ? ` · ${cfg.imagesDiskGB} GB on disk` : '';
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
            const b = Math.min(bytes, total);
            return c.progress(b / total, first ? `${gb(b)} of ${gb(total)} GB download${disk}` : sizeOf(b, total));
          }
          seen.set(m[1], done);
          c.progress((i + [...seen.values()].filter(Boolean).length / seen.size) / todo.length);
        },
      });
      if (!total) pulled += Number((await inspect(c, img, e.docker))[1]);
    }
    if (!first) return c.detail(todo.length ? sizeOf(null, total || pulled) : 'up to date');
    let onDisk = 0;
    for (const img of cfg.images) onDisk += Number((await inspect(c, img, e.docker))[1]);
    c.detail(`${total ? `${gb(total)} GB download · ` : ''}${gb(onDisk)} GB on disk`);
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
      await syncVenv(v, c.run);
    }
    c.detail('');
  };
  const syncVenv = (v, run) =>
    run(
      p.uv,
      ['sync', '--frozen', '--no-dev', '--managed-python', '--project', path.join(p.runner, v.project), ...v.extras.flatMap((x) => ['--extra', x])],
      { env: { ...e.base, UV_PROJECT_ENVIRONMENT: path.join(p.venvs, v.name), UV_PYTHON_INSTALL_DIR: p.python } },
    );

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
    { id: 'images', title: 'Download the images', hint: imagesHint, run: (c) => images(c, true) },
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
    { id: 'vm', title: 'VM', hint: 'The VM did not start.', run: async (c) => (await checkDisk(c), await vm(c)) },
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

  // 2a's Restart and the menu's Restart stack: the services recreated (data stays in the volumes), the runners again
  const restart = [
    every[0],
    { ...every[1], run: (c) => services(c, ['--force-recreate']) },
    { ...every[2], run: async (c) => (await runners.stop(), await startRunners(c)) },
  ];

  // 2a's VM row
  const vmUp = () => exec(p.colima, ['status', '--profile', cfg.profile], { env: e.vm }).then(() => true, () => false);

  // Quit: the runners, the stack (data stays) and the VM
  const stopAll = async (run) => {
    await runners.stop();
    await run('docker', compose(['down']), { env: e.compose, cwd: p.stack }).catch(() => {});
    await run(p.colima, ['stop', '--profile', cfg.profile], { env: e.vm }).catch(() => {});
  };

  // 2b's Restart runner for one that didn't start: its venv set up again first
  const setupVenv = (name, run) => syncVenv(cfg.venvs.find((v) => v.name === name), run);

  return { first, every, update, restart, stopAll, vmUp, setupVenv, freeDisk: () => freeDisk([p.home, settings.modelsDir || cfg.defaultModelsDir]) };
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

// The native runners as `make runners` starts them (root Makefile): one process per env with its env vars, stopped
// with SIGTERM. One that stops (any exit code) is restarted with back-off (2b: 2, 4, 8, 16, 32 s, then every minute)
// until 5 stops in 10 minutes; then, or when it cannot be spawned, it waits for Restart runner (`issues`). Their pids
// go to runners.json, so a start after the app was killed or crashed first stops the runners it left behind (else
// they hold the health ports, unstoppable). backoff: the tests' shorter waits
class Runners {
  constructor({ p, cfg, settings, onChange = () => {}, backoff = rules.backoff }) {
    Object.assign(this, { p, cfg, settings, onChange, backoff, procs: {}, stopping: false, pidFile: path.join(p.home, 'runners.json') });
    this.reset();
  }

  // the stop counts, the restarts waiting and the runners given up (a start, Restart runners)
  reset(env) {
    for (const k of ['stops', 'streak', 'pending', 'issues']) {
      if (!this[k]) this[k] = {};
      for (const e of env ? [env] : Object.keys(this[k])) {
        if (k === 'pending') clearTimeout(this.pending[e]?.timer);
        delete this[k][e];
      }
    }
  }

  logOf(env) {
    return path.join(this.p.logs, `runner-${env}.log`);
  }

  // the app's own lines in a runner's log (its exits, a spawn error, a setup), so 2b's last lines say what happened
  note(env, line) {
    try {
      fs.mkdirSync(this.p.logs, { recursive: true });
      fs.appendFileSync(this.logOf(env), `${new Date().toISOString()} ${line}\n`);
    } catch {}
  }

  // 2b: not restarted again until Restart runner; kind keeps | nostart
  giveUp(r, kind, cause) {
    this.issues[r.env] = { kind, cause, lines: tailOf(this.logOf(r.env), 4) };
    this.onChange();
  }

  // per env: 'run', {wait: ms until its restart}, {kind, cause, lines} (given up) or 'down' (stopping, or starting)
  state() {
    const now = Date.now();
    return Object.fromEntries(
      this.cfg.runners.map((r) => [
        r.env,
        this.issues[r.env] || (this.pending[r.env] ? { wait: this.pending[r.env].at - now } : this.procs[r.env] ? 'run' : 'down'),
      ]),
    );
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
    await this.reapPlugins();
  }

  // API 1.67: the plugin groups a killed native runner left ($HELM_HOME/run/<name>.pid, pid = group), only while the
  // native runner isn't ours and alive, and only a process whose cwd is that plugin's folder (a pid can be reused)
  async reapPlugins() {
    if (this.procs.native) return;
    const run = path.join(process.env.HELM_HOME || path.join(os.homedir(), '.helm'), 'run');
    let files = [];
    try {
      files = fs.readdirSync(run).filter((f) => f.endsWith('.pid'));
    } catch {}
    for (const f of files) {
      const pid = parseInt(fs.readFileSync(path.join(run, f), 'utf8'), 10);
      const cwd = pid > 0 ? await exec('/usr/sbin/lsof', ['-a', '-d', 'cwd', '-p', `${pid}`, '-Fn']).catch(() => '') : '';
      const cmd = pid > 0 ? await exec('/bin/ps', ['-p', `${pid}`, '-o', 'command=']).catch(() => '') : '';
      const theirs = cwd.split('\n').some((l) => l.startsWith('n') && l.endsWith(`/plugins/${f.slice(0, -4)}`));
      if (theirs && cmd.trim() && !cmd.includes(this.p.venvs)) {
        try {
          process.kill(-pid, 'SIGTERM');
          for (let i = 0; i < 50; i++) {
            process.kill(-pid, 0);
            await new Promise((r) => setTimeout(r, 100));
          }
          process.kill(-pid, 'SIGKILL');
        } catch {}
      }
      fs.rmSync(path.join(run, f), { force: true });
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
      // API 1.67: the native runner starts the plugins that start themselves (the app's plugins, then ~/.helm/plugins)
      ...(r.env === 'native' && {
        RUNNER_PLUGINS: 'on',
        RUNNER_PLUGINS_DIRS: `${path.join(this.p.stack, 'plugins')}:${path.join(process.env.HELM_HOME || path.join(os.homedir(), '.helm'), 'plugins')}`,
      }),
    };
  }

  async start() {
    this.stopping = false;
    this.reset();
    await this.reap();
    for (const r of this.cfg.runners) if (!this.procs[r.env]) this.spawn(r);
  }

  spawn(r) {
    fs.mkdirSync(this.p.logs, { recursive: true });
    const bin = path.join(this.p.venvs, r.venv, 'bin');
    let exe;
    try {
      exe = runnerExe(bin);
    } catch (e) {
      this.note(r.env, `spawn runner-${r.env}: ${e.message}`);
      return this.giveUp(r, 'nostart', e.code === 'ENOENT' ? 'Its Python environment is missing.' : e.message);
    }
    const out = fs.openSync(this.logOf(r.env), 'a');
    const child = spawn(exe, ['main.py'], { cwd: path.join(this.p.runner, 'runner_service'), env: this.env(r), stdio: ['ignore', out, out] });
    fs.closeSync(out);
    const started = Date.now();
    this.procs[r.env] = child;
    this.savePids();
    const gone = () => {
      if (this.procs[r.env] !== child) return false;
      delete this.procs[r.env];
      this.savePids();
      return true;
    };
    child.on('error', (e) => {
      if (!gone()) return;
      this.note(r.env, `spawn runner-${r.env}: ${e.message}`);
      if (!this.stopping) this.giveUp(r, 'nostart', e.message);
      else this.onChange();
    });
    child.on('exit', (code, sig) => {
      if (!gone()) return;
      if (!this.stopping) this.stopped(r, code ?? sig, Date.now() - started);
      this.onChange();
    });
    this.onChange();
  }

  stopped(r, how, lived) {
    const now = Date.now();
    this.note(r.env, `exit ${how}`);
    const s = rules.stopped(this.stops[r.env] || [], now, lived < 5000);
    this.stops[r.env] = s.stops;
    if (s.giveUp) {
      this.note(r.env, `stopped ${s.stops.length} of 5 · not restarted`);
      return this.giveUp(r, s.nostart ? 'nostart' : 'keeps', s.nostart ? 'It stops within 5 s of every start.' : null);
    }
    if (lived >= 120000) this.streak[r.env] = 0; // it ran 2 minutes: the back-off starts again at 2 s
    const n = this.streak[r.env] || 0;
    this.streak[r.env] = n + 1;
    const ms = this.backoff(n);
    const timer = setTimeout(() => {
      delete this.pending[r.env];
      if (!this.stopping && !this.procs[r.env]) this.spawn(r);
    }, ms);
    this.pending[r.env] = { at: now + ms, timer };
  }

  // 2b's Restart runner: that one's count reset, started now
  restartOne(env) {
    const r = this.cfg.runners.find((x) => x.env === env);
    if (!r) return;
    this.reset(env);
    this.stopping = false;
    if (!this.procs[env]) this.spawn(r);
    this.onChange();
  }

  alive() {
    return this.cfg.runners.every((r) => this.procs[r.env]);
  }

  async stop() {
    this.stopping = true;
    for (const x of Object.values(this.pending)) clearTimeout(x.timer);
    this.pending = {};
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
    await this.reapPlugins(); // a runner that needed SIGKILL left its plugins
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
      const [env, given] = Object.entries(this.issues)[0] || [];
      if (given)
        throw Object.assign(new Error(given.cause || (given.kind === 'keeps' ? 'It stopped 5 times in 10 minutes, so it is not restarted again until you say so.' : `See ${this.logOf(env)}.`)), {
          title: `The ${env} runner ${given.kind === 'keeps' ? 'keeps stopping' : 'didn’t start'}`,
        });
      if (Date.now() > end) throw new Error(`The runners did not answer on their health ports: see ${this.p.logs}/runner-*.log`);
      await new Promise((r) => setTimeout(r, 1000));
    }
  }
}

module.exports = { layout, loadSettings, stackSteps, Runners, validateAdmin, envs, imagesCfg, openMode, oldImages, layers, sizeOf, bytesOf, tailOf, freeDisk };
