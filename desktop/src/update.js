// Updates (board docs/mock/Desktop.dc.html frames 8–11; docs/guides/mac-app.md, Updates): the newest GitHub release
// newer than this app that carries a .dmg and SHA256SUMS; the download (only on the user's Install), its SHA256
// check, and the swap: a detached shell script (install-update.sh) that waits for the app to exit, copies the new
// helmstudio.app from the .dmg over this bundle and opens it again.
// HELMSTUDIO_UPDATE_URL (tests only) points the check at another releases list, e.g. a local fake.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { spawn } = require('node:child_process');
const http = require('node:http');
const https = require('node:https');
const { pipeline } = require('node:stream/promises');

const RELEASES = process.env.HELMSTUDIO_UPDATE_URL || 'https://api.github.com/repos/janishar/helmstudio/releases';
const UA = { 'User-Agent': 'helmstudio-app', Accept: 'application/vnd.github+json' };

// semver order with pre-releases: 1.0.0-alpha.1 < 1.0.0-alpha.2 < 1.0.0-beta < 1.0.0; null when not a version
function parse(v) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$/.exec(v || '');
  return m && { nums: m.slice(1, 4).map(Number), pre: m[4] ? m[4].split('.') : [] };
}

function compare(a, b) {
  const x = parse(a);
  const y = parse(b);
  for (let i = 0; i < 3; i++) if (x.nums[i] !== y.nums[i]) return x.nums[i] < y.nums[i] ? -1 : 1;
  if (!x.pre.length || !y.pre.length) return Math.sign(y.pre.length - x.pre.length); // a release is above its pre-releases
  for (let i = 0; i < Math.min(x.pre.length, y.pre.length); i++) {
    const [p, q] = [x.pre[i], y.pre[i]];
    if (p === q) continue;
    const [pn, qn] = [/^\d+$/.test(p), /^\d+$/.test(q)];
    if (pn && qn) return Number(p) < Number(q) ? -1 : 1;
    if (pn !== qn) return pn ? -1 : 1; // numbers sort before words
    return p < q ? -1 : 1;
  }
  return Math.sign(x.pre.length - y.pre.length);
}

// the first 3 bullet lines of the release body, without Markdown
function notes(body = '') {
  return body
    .split('\n')
    .filter((l) => /^\s*[-*] /.test(l))
    .slice(0, 3)
    .map((l) => l.replace(/^\s*[-*] /, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1').replace(/[*`_]/g, '').trim());
}

// the newest usable release above `current` (pre-releases only while this app is one), or null
function pickRelease(releases, current) {
  const pre = parse(current).pre.length > 0;
  let best = null;
  for (const r of Array.isArray(releases) ? releases : []) {
    const version = (r.tag_name || '').replace(/^v/, '');
    if (r.draft || !parse(version) || (r.prerelease && !pre) || compare(version, current) <= 0) continue;
    const dmg = r.assets?.find((a) => a.name.endsWith('.dmg'));
    const sums = r.assets?.find((a) => a.name === 'SHA256SUMS');
    if (!dmg || !sums) continue;
    if (best && compare(version, best.version) <= 0) continue;
    best = {
      version,
      page: r.html_url,
      notes: notes(r.body),
      dmg: { name: path.basename(dmg.name), url: dmg.browser_download_url, size: dmg.size },
      sums: sums.browser_download_url,
    };
  }
  return best;
}

// "<hash>  <file>" lines (shasum -a 256 output; "*" marks binary mode)
function sumFor(text, name) {
  for (const l of text.split('\n')) {
    const m = /^([0-9a-f]{64}) [ *](.+)$/i.exec(l.trim());
    if (m && m[2] === name) return m[1].toLowerCase();
  }
  return null;
}

async function sha256(file) {
  const h = crypto.createHash('sha256');
  await pipeline(fs.createReadStream(file), h);
  return h.digest('hex');
}

async function get(url, signal) {
  const r = await fetch(url, { headers: UA, signal: signal || AbortSignal.timeout(15000) });
  if (!r.ok) throw new Error(`${url}: HTTP ${r.status}`);
  return r;
}

const check = async (current) => pickRelease(await (await get(RELEASES)).json(), current);

// Node's http(s), following redirects (GitHub sends assets to another host), resolves with the response stream.
// Not fetch: undici's body stream, piped to a file with backpressure, crashed the main process when the socket
// ended while paused (assert(!this.paused) in undici's parser, an uncaught exception)
function open(url, signal, hops = 5) {
  return new Promise((resolve, reject) => {
    const lib = url.startsWith('https:') ? https : url.startsWith('http:') ? http : null;
    if (!lib) return reject(new Error(`not a web address: ${url}`));
    const req = lib.get(url, { headers: UA, signal, timeout: 30000 }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location && hops > 0) {
        res.resume();
        return open(new URL(res.headers.location, url).href, signal, hops - 1).then(resolve, reject);
      }
      if (res.statusCode !== 200) return (res.resume(), reject(new Error(`${url}: HTTP ${res.statusCode}`)));
      resolve(res);
    });
    req.on('timeout', () => req.destroy(new Error(`${url}: no answer`)));
    req.on('error', reject);
  });
}

// to <file>.part, renamed when complete; a failure or Cancel (the signal) deletes the part. Every error rejects
async function download(url, file, { signal, onBytes }) {
  const part = `${file}.part`;
  try {
    const res = await open(url, signal);
    res.on('data', (chunk) => onBytes(chunk.length));
    await pipeline(res, fs.createWriteStream(part), { signal });
    fs.renameSync(part, file);
  } catch (e) {
    fs.rmSync(part, { force: true });
    throw e;
  }
}

// null when the bundle can be replaced, else why not (read-only: the .dmg itself, App Translocation)
function installProblem(bundle) {
  if (!bundle) return 'Updates install into the built app only (make app).';
  try {
    fs.accessSync(path.dirname(bundle), fs.constants.W_OK);
    fs.accessSync(bundle, fs.constants.W_OK);
    return null;
  } catch {
    return `helmstudio runs from ${path.dirname(bundle)}, which it cannot change. Drag it to Applications, open it from there, then update.`;
  }
}

// the swap, after the app exits: install-update.sh <pid> <dmg> <bundle> [NAME=VALUE for open --env ...]; the
// HELMSTUDIO_* settings go along, so a scratch run (HELMSTUDIO_HOME) opens again on its own folder
function install({ dmg, bundle, logFile }) {
  const env = Object.entries(process.env).filter(([k]) => k.startsWith('HELMSTUDIO_')).map(([k, v]) => `${k}=${v}`);
  fs.mkdirSync(path.dirname(logFile), { recursive: true });
  const out = fs.openSync(logFile, 'a');
  const child = spawn('/bin/sh', [path.join(__dirname, 'install-update.sh'), `${process.pid}`, dmg, bundle, ...env], {
    detached: true,
    stdio: ['ignore', out, out],
  });
  fs.closeSync(out);
  child.unref();
}

module.exports = { compare, notes, pickRelease, sumFor, sha256, check, download, installProblem, install };
