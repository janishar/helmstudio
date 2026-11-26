// The studio page's model folders in the Mac app (plan 40 P5, docs/mock/ModelFolders.dc.html): the check on a path the
// page sends for Show / Open in Finder, and how a folder the dialog picked goes back (~ for the home directory, as the
// runner writes it).
const path = require('node:path');

/** A path the page sent ("~/GenAI/models/X", "/Volumes/ALI T9/models") → absolute, or null: absolute or ~/ only, at most
 * 400 characters (the contract's limit), no `..`, no control characters. */
function localPath(p, home) {
  if (typeof p !== 'string' || !p || p.length > 400 || /[\0-\x1f]/.test(p)) return null;
  if (!p.startsWith('/') && !p.startsWith('~/')) return null;
  if (p.split('/').includes('..')) return null;
  return path.resolve(p.startsWith('~/') ? path.join(home, p.slice(2)) : p);
}

/** A picked folder as the page lists it: under home → "~/…". */
const tilde = (p, home) => (p === home ? '~' : p.startsWith(home + '/') ? '~' + p.slice(home.length) : p);

module.exports = { localPath, tilde };
