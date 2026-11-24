// Updates: version order, the release picked, the SHA256 check, the steps of the first open after an update and
// the old images it removes
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { compare, pickRelease, sumFor, sha256, notes } = require('../src/update');
const { stackSteps, layout, openMode, oldImages, layers, sizeOf, bytesOf } = require('../src/stack');
const cfg = require('../stack.json');

test('versions: semver order with pre-releases', () => {
  const order = ['0.1.0-alpha.1', '0.1.0-alpha.2', '0.1.0-alpha.10', '0.1.0-beta', '0.1.0-beta.1', '0.1.0', '0.1.1', '0.2.0', '1.0.0'];
  for (let i = 0; i < order.length - 1; i++) {
    assert.equal(compare(order[i], order[i + 1]), -1, `${order[i]} < ${order[i + 1]}`);
    assert.equal(compare(order[i + 1], order[i]), 1);
  }
  assert.equal(compare('v0.1.0-alpha.1', '0.1.0-alpha.1'), 0);
  assert.equal(compare('0.1.0-alpha.2-dev', '0.1.0-alpha.1'), 1); // the real check's scratch build
});

const rel = (tag, { draft = false, prerelease = true, assets = ['helmstudio-x.dmg', 'SHA256SUMS'] } = {}) => ({
  tag_name: `v${tag}`,
  draft,
  prerelease,
  html_url: `https://github.com/janishar/helmstudio/releases/tag/v${tag}`,
  body: '## What\n\n- **Steer** the assistant\n- Stop marks steers read ([more](https://x))\n- Faster `first` open\n- a fourth',
  assets: assets.map((name) => ({ name, size: 290e6, browser_download_url: `https://dl/${tag}/${name}` })),
});

test('release: the newest above this version, with a dmg and SHA256SUMS; never a draft', () => {
  const list = [
    rel('0.1.0-alpha.4', { draft: true }),
    rel('0.1.0-alpha.3', { assets: ['helmstudio-x.dmg'] }), // no SHA256SUMS
    rel('0.1.0-alpha.2'),
    rel('0.1.0-alpha.1'),
  ];
  const r = pickRelease(list, '0.1.0-alpha.1');
  assert.equal(r.version, '0.1.0-alpha.2');
  assert.deepEqual(r.dmg, { name: 'helmstudio-x.dmg', url: 'https://dl/0.1.0-alpha.2/helmstudio-x.dmg', size: 290e6 });
  assert.equal(r.sums, 'https://dl/0.1.0-alpha.2/SHA256SUMS');
  assert.deepEqual(r.notes, ['Steer the assistant', 'Stop marks steers read (more)', 'Faster first open']);
  assert.equal(pickRelease(list, '0.1.0-alpha.2'), null); // up to date
  assert.equal(pickRelease([rel('0.1.0-alpha.2', { assets: ['SHA256SUMS'] })], '0.1.0-alpha.1'), null); // no dmg
  assert.equal(pickRelease({ message: 'rate limited' }, '0.1.0-alpha.1'), null);
});

test('release: pre-releases only while this app is one', () => {
  const list = [rel('0.2.0-alpha.1'), rel('0.1.0', { prerelease: false })];
  assert.equal(pickRelease(list, '0.1.0-alpha.1').version, '0.2.0-alpha.1');
  assert.equal(pickRelease(list, '0.1.0'), null);
  assert.equal(pickRelease([...list, rel('0.1.1', { prerelease: false })], '0.1.0').version, '0.1.1');
});

test('SHA256: the file against its SHA256SUMS line', async () => {
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'helm-upd-')), 'helmstudio-x.dmg');
  fs.writeFileSync(file, 'the dmg bytes');
  const want = crypto.createHash('sha256').update('the dmg bytes').digest('hex');
  const sums = `${'0'.repeat(64)}  other.dmg\n${want}  helmstudio-x.dmg\n`;
  assert.equal(sumFor(sums, 'helmstudio-x.dmg'), want);
  assert.equal(await sha256(file), want);
  assert.equal(sumFor(sums, 'missing.dmg'), null);
  assert.equal(sumFor(`${want.toUpperCase()} *helmstudio-x.dmg`, 'helmstudio-x.dmg'), want);
  assert.deepEqual(notes('no bullets'), []);
});

test('open mode: a new version (or none recorded) runs the update steps once', () => {
  assert.equal(openMode({}, '0.1.0-alpha.2'), 'first');
  assert.equal(openMode({ installed: true }, '0.1.0-alpha.2'), 'update'); // installed by 0.1.0-alpha.1
  assert.equal(openMode({ installed: true, version: '0.1.0-alpha.1' }, '0.1.0-alpha.2'), 'update');
  assert.equal(openMode({ installed: true, version: '0.1.0-alpha.2' }, '0.1.0-alpha.2'), 'start');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'helm-upd-'));
  const s = stackSteps({ p: layout(dir, dir), cfg, settings: {}, runners: {} });
  assert.deepEqual(s.update.map((x) => x.title), ['VM', 'Update the images', 'Services', 'Runners', 'Remove old images']);
  assert.deepEqual(s.every.map((x) => x.title), ['VM', 'Services', 'Runners']);
});

test('old images: ours not in use, never the current tag, never third-party', () => {
  const pre = cfg.composeEnv.IMAGE_PREFIX;
  const refs = [
    ...cfg.images,
    `${pre}studio:0.0.9`,
    `${pre}kong:0.1.0-alpha.0`,
    'helmstudio/web:prod',
    'helmstudio/web:<none>',
    'postgres:17-alpine',
    'nats:2.10',
    'ghcr.io/someone-else/tool:1',
    '',
  ];
  assert.deepEqual(oldImages(refs, cfg.images, pre), [`${pre}studio:0.0.9`, `${pre}kong:0.1.0-alpha.0`, 'helmstudio/web:prod']);
  assert.deepEqual(oldImages(['helmstudio/web:prod', `${pre}web:${cfg.composeEnv.IMAGE_TAG}`], [...cfg.images, 'helmstudio/web:prod'], pre), []);
});

test('sizes: layers per platform, MB / GB, docker size strings', () => {
  const m = (arch, size) => ({ Descriptor: { platform: { architecture: arch, os: 'linux' } }, OCIManifest: { layers: [{ digest: 'sha256:abc', size }] } });
  assert.equal(layers([m('amd64', 1), m('arm64', 2)])[0].size, 2);
  assert.equal(layers(m('unknown', 3))[0].size, 3);
  assert.deepEqual(layers(null), []);
  assert.equal(sizeOf(38e6, 112e6), '38 of 112 MB');
  assert.equal(sizeOf(1.2e9, 2e9), '1.2 of 2.0 GB');
  assert.equal(sizeOf(null, 1.4e9), '1.4 GB');
  assert.equal(bytesOf('2.205GB'), 2.205e9);
  assert.equal(bytesOf('512kB'), 512e3);
  assert.equal(bytesOf('0B'), 0);
});

// a local server: /big is 64 MB in 64 KB chunks with backpressure, /moved redirects to it
async function server() {
  const http = require('node:http');
  const chunk = Buffer.alloc(65536, 7);
  const srv = http.createServer((req, res) => {
    if (req.url === '/moved') return res.writeHead(302, { location: '/big' }).end();
    res.writeHead(200, { 'content-length': 1024 * chunk.length });
    let n = 0;
    const more = () => {
      while (n < 1024) if ((n++, !res.write(chunk))) return res.once('drain', more);
      res.end();
    };
    more();
  });
  await new Promise((r) => srv.listen(0, '127.0.0.1', r));
  return { url: `http://127.0.0.1:${srv.address().port}`, close: () => srv.close() };
}

test('download: a big file through a redirect; Cancel mid-way deletes the part and throws nothing uncaught', async () => {
  const { download } = require('../src/update');
  const s = await server();
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'helm-upd-'));
  try {
    let got = 0;
    await download(`${s.url}/moved`, path.join(dir, 'a.dmg'), { onBytes: (n) => (got += n) });
    assert.equal(fs.statSync(path.join(dir, 'a.dmg')).size, 64 * 1024 * 1024);
    assert.equal(got, 64 * 1024 * 1024);

    const ac = new AbortController();
    let part = 0;
    const p = download(`${s.url}/big`, path.join(dir, 'b.dmg'), {
      signal: ac.signal,
      onBytes: (n) => (part += n) > 8e6 && ac.abort(),
    });
    await assert.rejects(p);
    assert.deepEqual(fs.readdirSync(dir), ['a.dmg']); // no b.dmg, no b.dmg.part
    await assert.rejects(download(`${s.url}/big`.replace('http:', 'ftp:'), path.join(dir, 'c.dmg'), { onBytes() {} }));
    await new Promise((r) => setTimeout(r, 200)); // an uncaught error after the abort would fail the run here
  } finally {
    s.close();
  }
});
