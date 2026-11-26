// The spelling a right-click passes to the studio page (alpha.5 item 17)
const test = require('node:test');
const assert = require('node:assert');
const { spellingOf, isWord } = require('../src/spelling');

test('spelling: the word and up to three suggestions; none when spelt right', () => {
  assert.deepEqual(spellingOf({ misspelledWord: 'snwo', dictionarySuggestions: ['snow', 'sown', 'snows', 'sow'] }), {
    word: 'snwo',
    suggestions: ['snow', 'sown', 'snows'],
  });
  assert.deepEqual(spellingOf({ misspelledWord: '', dictionarySuggestions: [] }), { word: '', suggestions: [] });
  assert.deepEqual(spellingOf({}), { word: '', suggestions: [] });
});

test('spelling: only a word comes back', () => {
  assert.ok(isWord('snow'));
  assert.ok(!isWord(''));
  assert.ok(!isWord(42));
  assert.ok(!isWord('a\nb'));
  assert.ok(!isWord('x'.repeat(101)));
});
