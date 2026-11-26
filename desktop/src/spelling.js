// The spelling of a right-clicked word for the studio page's own menu (alpha.5 item 17): what crosses to the page,
// and the check on a word that comes back.

/** Electron's context-menu params → { word, suggestions } (up to three); no word when it is spelt right. */
const spellingOf = (p) => ({
  word: typeof p.misspelledWord === 'string' ? p.misspelledWord : '',
  suggestions: (Array.isArray(p.dictionarySuggestions) ? p.dictionarySuggestions : []).slice(0, 3),
});

/** A single word, as the page sends it back (a suggestion, or the word to add). */
const isWord = (w) => typeof w === 'string' && w.length > 0 && w.length <= 100 && !/[\n\r\0]/.test(w);

module.exports = { spellingOf, isWord };
