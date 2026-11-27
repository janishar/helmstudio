// The error states' rules (board Desktop.dc.html "Error states"): back-off, the health check, the port check, the
// disk rule, the quit rule
const test = require('node:test');
const assert = require('node:assert');
const r = require('../src/rules');
const cfg = require('../stack.json');

const MIN = 60000;

test('2b back-off: 2, 4, 8, 16, 32 s, then every minute', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 9].map(r.backoff), [2000, 4000, 8000, 16000, 32000, MIN, MIN]);
});

test('2b: 5 stops in 10 minutes gives up; older stops drop out; all quick is "didn\'t start"', () => {
  let stops = [];
  for (let i = 0; i < 4; i++) {
    const s = r.stopped(stops, i * MIN, false);
    assert.equal(s.giveUp, false);
    stops = s.stops;
  }
  const five = r.stopped(stops, 4 * MIN, true);
  assert.equal(five.giveUp, true);
  assert.equal(five.nostart, false, 'some ran longer than 5 s: keeps stopping');
  // the first stop is 10 minutes old by now: 4 in the window, keeps going
  assert.equal(r.stopped(stops, 10 * MIN, false).giveUp, false);
  let quick = [];
  for (let i = 0; i < 5; i++) quick = r.stopped(quick, i * 1000, true).stops;
  assert.equal(r.stopped(quick.slice(0, 4), 4000, true).nostart, true);
});

test('2a: a probe answers under 500; no answer, 502 and 503 are down', () => {
  assert.deepEqual([200, 307, 401, 404, 500, 502, 503, null, undefined].map(r.answers), [true, true, true, true, false, false, false, false, false]);
});

test('2a: the window after 3 misses in a row, 5 s re-checks, one notification per outage, none within 10 min', () => {
  const o = new r.Outage();
  assert.equal(o.every, 20000);
  assert.deepEqual(o.record(false, 0), { down: false, notify: false });
  assert.equal(o.every, 5000, 'checked again every 5 s after the first miss');
  assert.equal(o.since, 0);
  assert.deepEqual(o.record(false, 5000), { down: false, notify: false });
  assert.deepEqual(o.record(false, 10000), { down: true, notify: true });
  assert.deepEqual(o.record(false, 15000), { down: true, notify: false }, 'one per outage');
  o.record(true, 20000);
  assert.equal(o.every, 20000);
  assert.equal(o.since, null);
  for (const t of [3, 4]) o.record(false, t * MIN);
  assert.deepEqual(o.record(false, 5 * MIN), { down: true, notify: false }, 'a second outage within 10 minutes: no notification');
  o.record(true, 6 * MIN);
  for (const t of [20, 21]) o.record(false, t * MIN);
  assert.equal(o.record(false, 22 * MIN).notify, true, '10 minutes after the last one, it notifies again');
  o.record(true, 23 * MIN);
  assert.equal(o.record(false, 24 * MIN).down, false, 'a blip never takes the page away');
});

test('2e disk: under 2 GB stops, under 10 warns once a day, else ok', () => {
  const now = 10 * 24 * 60 * MIN;
  assert.equal(r.diskRule(1.5, 0, now), 'stop');
  assert.equal(r.diskRule(4, undefined, now), 'warn');
  assert.equal(r.diskRule(4, now - 60 * MIN, now), 'low', 'warned an hour ago: the tray only');
  assert.equal(r.diskRule(4, now - 24 * 60 * MIN, now), 'warn');
  assert.equal(r.diskRule(10, 0, now), 'ok');
});

test('2f quit: asks only when the stack will stop and runs are going', () => {
  assert.equal(r.quitAsks({ via: 'menu', keepRunning: true, runs: 2 }), true);
  assert.equal(r.quitAsks({ via: 'menu', keepRunning: true, runs: 0 }), false);
  assert.equal(r.quitAsks({ via: 'cmdq', keepRunning: false, runs: 1 }), true);
  assert.equal(r.quitAsks({ via: 'cmdq', keepRunning: true, runs: 3 }), false, '⌘Q with Keep running only hides');
  assert.equal(r.quitAsks({ via: 'shutdown', keepRunning: false, runs: 3 }), false);
  assert.equal(r.quitAsks({ via: 'update', keepRunning: false, runs: 3 }), false);
});

test('2c ports: every host port of stack.json, the first one another app holds, the next free one', async () => {
  const ports = r.hostPorts(cfg);
  assert.deepEqual(ports.map((p) => p.port), [8310, 5466, 4325, 8436, 8446, 8191, 8192, 8193]);
  assert.equal(ports[0].label, 'the studio');
  const held = { 8310: { name: 'node', ours: false }, 8311: { name: 'x', ours: false }, 5466: { name: 'helmstudio-vm', ours: true } };
  const clash = await r.portClash(ports, async (n) => held[n] || null);
  assert.deepEqual(clash, { key: 'KONG_HTTP_PORT', label: 'the studio', port: 8310, holder: 'node', suggest: 8312 });
  assert.equal(await r.portClash(ports, async (n) => (n === 5466 ? held[n] : null)), null, 'our own VM holding it is fine');
  const other = await r.portClash(ports, async (n) => (n === 8191 ? { name: null, ours: false } : null));
  assert.equal(other.holder, 'another app');
  assert.equal(other.suggest, 8194, 'never one of the other ports (8192, 8193)');
  assert.deepEqual(r.lsofHolder('p4242\ncnode\nf21\n'), { pid: 4242, name: 'node' });
  assert.equal(r.lsofHolder(''), null);
});

test('2c ports: a picked port reaches compose, the studio address, NATS, S3 and the runners', () => {
  const c = r.applyPorts(structuredClone(cfg), { KONG_HTTP_PORT: 8311, NATS_HOST_PORT: 4326, S3_HOST_PORT: 8437, 'runner:mlx': 8199 });
  assert.equal(c.composeEnv.KONG_HTTP_PORT, '8311');
  assert.equal(c.studioUrl, 'http://127.0.0.1:8311');
  assert.equal(c.composeEnv.PUBLIC_BASE_URL, 'http://127.0.0.1:8311');
  assert.equal(c.natsPort, 4326);
  assert.equal(c.composeEnv.NATS_HOST_PORT, '4326');
  assert.equal(c.s3Port, 8437);
  assert.equal(c.runners.find((x) => x.env === 'mlx').healthPort, 8199);
  assert.deepEqual(r.applyPorts(structuredClone(cfg), undefined), cfg);
  assert.deepEqual([1023, 1024, 65535, 65536, 8.5, NaN].map(r.validPort), [false, true, true, false, false, false]);
});

test('2d: a models folder on a volume that is not mounted', () => {
  const mounted = new Set(['/Volumes/T9']);
  const exists = (p) => mounted.has(p);
  assert.equal(r.missingVolume('/Volumes/ALI T9/models', exists), 'ALI T9');
  assert.equal(r.missingVolume('/Volumes/T9/helmstudio-models', exists), null);
  assert.equal(r.missingVolume('/Users/me/helmstudio-models', exists), null);
  assert.equal(r.missingVolume(undefined, exists), null);
});
