// The Mac app's error-state rules (docs/mock/Desktop.dc.html, "Error states", frames 2a–2h and "choices made"): pure,
// so the tests pin them. The windows, the tray and the steps that use them are in main.js and stack.js.
const MIN = 60000;

// 2b: the wait before a stopped runner's restart n (0-based, n in a row): 2, 4, 8, 16, 32 s, then every minute
const backoff = (n) => (n < 5 ? 2000 * 2 ** n : MIN);

// 2b: a runner stopped (any exit code counts). The stops of the last 10 minutes, this one included; at 5 it is not
// restarted again. "nostart" when every one of them came within 5 s of its start (it never really ran)
function stopped(stops, now, quick) {
  const recent = [...stops, { at: now, quick }].filter((s) => now - s.at < 10 * MIN);
  const giveUp = recent.length >= 5;
  return { stops: recent, giveUp, nostart: giveUp && recent.every((s) => s.quick) };
}

// 2a: a probe's answer counts for any status under 500 (studio's 401 is an answer; Kong's 502 or 503 is not)
const answers = (status) => Number.isInteger(status) && status < 500;

// 2a: the stack check. The window after 3 misses in a row (the web's own banner shows before that); one
// notification per outage, none again within 10 minutes of the last. `every`: 20 s, 5 s after a miss
class Outage {
  constructor() {
    this.misses = 0;
    this.since = null;
    this.notified = -Infinity;
  }

  record(ok, now) {
    if (ok) {
      this.misses = 0;
      this.since = null;
      return { down: false, notify: false };
    }
    if (!this.misses++) this.since = now;
    const notify = this.misses === 3 && now - this.notified >= 10 * MIN;
    if (notify) this.notified = now;
    return { down: this.misses >= 3, notify };
  }

  get every() {
    return this.misses ? 5000 : 20000;
  }
}

// 2e: at every start. Under 2 GB stops (as the first open's check does); under 10 GB warns at most once a day ("low"
// when it warned already today: the tray's sub line still says it)
const diskRule = (freeGB, warnedAt, now) =>
  freeGB < 2 ? 'stop' : freeGB < 10 ? (now - (warnedAt || 0) >= 24 * 60 * MIN ? 'warn' : 'low') : 'ok';

// 2f: Quit asks only when the stack will stop (the menu bar's Quit, or ⌘Q with Keep running off) and runs are going;
// never for a shutdown, a log out or an update's restart
const quitAsks = ({ via, keepRunning, runs }) => runs > 0 && (via === 'menu' || (via === 'cmdq' && !keepRunning));

// 2c: the host ports the app's stack and runners listen on (stack.json), with the words for the port field
function hostPorts(cfg) {
  const stack = [
    ['KONG_HTTP_PORT', 'the studio'],
    ['POSTGRES_HOST_PORT', 'Postgres'],
    ['NATS_HOST_PORT', 'NATS'],
    ['S3_HOST_PORT', 'storage'],
    ['S3_TLS_HOST_PORT', 'storage over TLS'],
  ].map(([key, label]) => ({ key, label, port: Number(cfg.composeEnv[key]) }));
  return [...stack, ...cfg.runners.map((r) => ({ key: `runner:${r.env}`, label: `the ${r.env} runner`, port: r.healthPort }))];
}

// 2c: the ports a user picked (settings.ports, keyed as hostPorts) into the config, in place: compose's variables, the
// studio's address, the runners' NATS, S3 and health ports
function applyPorts(cfg, ports = {}) {
  for (const [key, port] of Object.entries(ports || {})) {
    if (!validPort(port)) continue; // settings.json edited by hand
    if (key.startsWith('runner:')) {
      const r = cfg.runners.find((x) => x.env === key.slice(7));
      if (r) r.healthPort = port;
      continue;
    }
    cfg.composeEnv[key] = `${port}`;
    if (key === 'KONG_HTTP_PORT') cfg.studioUrl = cfg.composeEnv.PUBLIC_BASE_URL = `http://127.0.0.1:${port}`;
    if (key === 'NATS_HOST_PORT') cfg.natsPort = port;
    if (key === 'S3_HOST_PORT') cfg.s3Port = port;
  }
  return cfg;
}

const validPort = (n) => Number.isInteger(n) && n >= 1024 && n <= 65535;

// 2c: the first port another app holds, with its name and the next free port to suggest; null when all are free.
// holder(port) -> null (free) | {name, ours}: one our own VM or runners hold is free for this check
async function portClash(ports, holder) {
  for (const p of ports) {
    const h = await holder(p.port);
    if (!h || h.ours) continue;
    const used = new Set(ports.map((x) => x.port));
    let suggest = null;
    for (let n = p.port + 1; n <= 65535 && !suggest; n++) if (!used.has(n) && !(await holder(n))) suggest = n;
    return { ...p, holder: h.name || 'another app', suggest };
  }
  return null;
}

// `lsof -Fpc` → the first listener's pid and command name
function lsofHolder(out) {
  const pid = /^p(\d+)$/m.exec(out || '')?.[1];
  return pid ? { pid: Number(pid), name: /^c(.+)$/m.exec(out)?.[1] || null } : null;
}

// 2d: the volume a models folder is on, when that volume is not mounted ("/Volumes/T9/…" without /Volumes/T9)
const missingVolume = (dir, exists) => {
  const m = /^\/Volumes\/([^/]+)/.exec(dir || '');
  return m && !exists(`/Volumes/${m[1]}`) ? m[1] : null;
};

module.exports = { backoff, stopped, answers, Outage, diskRule, quitAsks, hostPorts, applyPorts, validPort, portClash, lsofHolder, missingVolume };
