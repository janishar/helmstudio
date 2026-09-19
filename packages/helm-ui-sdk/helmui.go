// Package helmui embeds helm-ui-sdk's browser build so the daemon can serve it
// under /sdk/v1/ (docs/design/04-packages.md §8, docs/decisions.md M6 Q14).
// The JavaScript is the package; this file only carries it into the Go binary,
// and npm never publishes it ("files": ["src"]).
package helmui

import "embed"

// Version is helm-ui-sdk's own version, which moves independently of helm-css
// and the runtime SDK (04 §9). It must match src/index.js.
const Version = "1.0.0-rc.3"

// Files holds the modules a page imports.
//
//go:embed src/index.js src/base.js src/terminal.js src/gallery.js src/player.js src/timeline.js src/sequence.js src/preview.js
var Files embed.FS

// Served maps each file to the name it is served under /sdk/v1/ui/.
var Served = map[string]string{
	"index.js":    "src/index.js",
	"base.js":     "src/base.js",
	"terminal.js": "src/terminal.js",
	"gallery.js":  "src/gallery.js",
	"player.js":   "src/player.js",
	"timeline.js": "src/timeline.js",
	"sequence.js": "src/sequence.js",
	"preview.js":  "src/preview.js",
}
