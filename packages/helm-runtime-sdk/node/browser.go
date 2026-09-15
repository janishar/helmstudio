// Package helmruntimenode embeds the Node runtime SDK's browser build so the
// daemon can serve it under /sdk/v1/ (docs/design/04-packages.md §8,
// docs/decisions.md M6 Q14). The JavaScript is the package; this file only
// carries it into the Go binary, and npm never publishes it ("files": ["src"]).
package helmruntimenode

import "embed"

// Browser holds the files a page imports: browser.js and what it imports.
// proxy.js and index.js are for Node and are not served.
//
//go:embed src/browser.js src/generated.js src/transport.js src/theme.js
var Browser embed.FS

// BrowserFiles are Browser's files, by the name they are served under
// /sdk/v1/runtime/.
var BrowserFiles = map[string]string{
	"browser.js":   "src/browser.js",
	"generated.js": "src/generated.js",
	"transport.js": "src/transport.js",
	"theme.js":     "src/theme.js",
}
