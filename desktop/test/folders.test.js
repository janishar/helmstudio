// The Mac app's model-folder bridge checks (src/folders.js): only absolute or ~/ paths, never `..`.
const test = require('node:test');
const assert = require('node:assert');
const { localPath, tilde } = require('../src/folders');

test('localPath: ~/ and absolute paths resolve; anything else is refused', () => {
  assert.strictEqual(localPath('~/GenAI/models/X', '/Users/a'), '/Users/a/GenAI/models/X');
  assert.strictEqual(localPath('/Volumes/T9/models', '/Users/a'), '/Volumes/T9/models');
  for (const bad of ['models', '~other/x', '/a/../etc', '~/..', '', 'x'.repeat(401), '/a\nb', 3, null])
    assert.strictEqual(localPath(bad, '/Users/a'), null, String(bad));
});

test('tilde: the home directory written ~', () => {
  assert.strictEqual(tilde('/Users/a/GenAI/models', '/Users/a'), '~/GenAI/models');
  assert.strictEqual(tilde('/Users/ab/x', '/Users/a'), '/Users/ab/x');
  assert.strictEqual(tilde('/Volumes/T9/models', '/Users/a'), '/Volumes/T9/models');
});
