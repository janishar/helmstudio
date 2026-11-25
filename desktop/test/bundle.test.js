// The plugins the app bundles (make plugin-files, what `make resources` copies): Your Claude subscription is in it,
// and never an .env, a log or a token
const test = require('node:test');
const assert = require('node:assert');
const { execFileSync } = require('node:child_process');
const path = require('node:path');

test('the bundled plugins include Your Claude subscription and no .env, log or token', () => {
  const files = execFileSync('make', ['-s', 'plugin-files'], { cwd: path.join(__dirname, '..'), encoding: 'utf8' })
    .split('\n')
    .filter(Boolean);
  for (const f of ['plugin.json', 'run.sh', 'bridge.py']) assert.ok(files.includes(`plugins/claude-code-bridge/${f}`), f);
  assert.ok(files.includes('plugins/web-search/plugin.json'));
  assert.deepStrictEqual(files.filter((f) => /(^|\/)(\.env|[^/]*\.log$|[^/]*token)/.test(f)), []);
});
